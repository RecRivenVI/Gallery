import { expect, test, type APIRequestContext, type Page, type Response } from '@playwright/test';

const realBaseURL = process.env.GALLERY_REAL_BASE_URL;
test.skip(!realBaseURL, '仅由隔离 Personal galleryd E2E 运行器执行');
test.setTimeout(90_000);
// 一次性 Token/Share 密文只在创建对话框短暂出现。真实失败诊断不得把它们写入截图、视频或 trace。
test.use({ screenshot: 'off', video: 'off', trace: 'off' });

interface SessionEstablished {
  session: { id: string };
}

interface WorkList {
  works: { id: string }[];
}

interface RealtimeProbe {
  socketsCreated: number;
  socketsOpened: number;
  closeOpenSockets: () => void;
}

type RealtimeProbeWindow = Window & { __galleryE2ERealtime: RealtimeProbe };

function pathIs(response: Response, path: string, method = 'GET'): boolean {
  return response.request().method() === method && new URL(response.url()).pathname === path;
}

async function pair(page: Page): Promise<string> {
  await page.goto('/manage');
  await expect(page.getByRole('heading', { name: '管理需要认证', exact: true })).toBeVisible();
  const [exchange] = await Promise.all([
    page.waitForResponse((response) => pathIs(response, '/api/v1/personal/pair', 'POST')),
    page.getByRole('button', { name: '开始配对' }).click()
  ]);
  expect(exchange.status()).toBe(201);
  const established = (await exchange.json()) as SessionEstablished;
  await expect(page.getByRole('heading', { name: 'Gallery 管理', exact: true })).toBeVisible();
  return established.session.id;
}

async function openSecurity(page: Page, tab: string): Promise<void> {
  await page.goto('/manage/security');
  await expect(page.getByRole('heading', { name: '连接与安全', exact: true })).toBeVisible();
  await page.getByRole('tab', { name: tab, exact: true }).click();
}

async function createToken(page: Page, name: string): Promise<{ id: string; secret: string }> {
  await openSecurity(page, 'API Token');
  await page.getByRole('textbox', { name: '名称', exact: true }).fill(name);
  const capability = page.getByRole('checkbox', { name: 'library.read', exact: true });
  await capability.focus();
  await capability.press('Space');
  await expect(capability).toBeChecked();
  const responsePromise = page.waitForResponse((response) => pathIs(response, '/api/v1/api-tokens', 'POST'));
  await page.getByRole('button', { name: '创建 Token', exact: true }).click();
  const response = await responsePromise;
  expect(response.status()).toBe(201);
  const created = (await response.json()) as { id: string };
  const secretNode = page.getByTestId('one-time-secret');
  await expect(secretNode).toBeVisible();
  const secret = (await secretNode.textContent()) ?? '';
  if (secret.length < 32) throw new Error('Token 一次性密文缺失或过短');
  await page.getByRole('button', { name: '我已保存，关闭', exact: true }).click();
  await expect(secretNode).not.toBeVisible();
  return { id: created.id, secret };
}

async function expectTokenStatus(request: APIRequestContext, secret: string, status: number): Promise<void> {
  const response = await request.get('/api/v1/libraries', {
    headers: { Authorization: `Bearer ${secret}` }
  });
  expect(response.status()).toBe(status);
  if (status === 200) {
    const body = (await response.json()) as { libraries: unknown[] };
    expect(body.libraries.length).toBeGreaterThan(0);
  }
}

async function revokeFromRow(page: Page, rowText: string, dialogTitle: string, path: string): Promise<void> {
  const row = page.getByRole('row').filter({ hasText: rowText });
  await expect(row).toHaveCount(1);
  const responsePromise = page.waitForResponse((response) => pathIs(response, path, 'DELETE'));
  await row.getByRole('button', { name: '吊销', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: dialogTitle, exact: true });
  await dialog.getByRole('button', { name: '确认吊销', exact: true }).click();
  expect((await responsePromise).status()).toBe(204);
}

test('Personal 安全资源、连续断线快照恢复与一次性密文真实链 @real-security', async ({ browser, request }) => {
  const ownerContext = await browser.newContext();
  const ownerPage = await ownerContext.newPage();
  await ownerPage.addInitScript(() => {
    const NativeWebSocket = window.WebSocket;
    const sockets: WebSocket[] = [];
    const probe: RealtimeProbe = {
      socketsCreated: 0,
      socketsOpened: 0,
      closeOpenSockets: () => {
        for (const socket of sockets) {
          if (socket.readyState === WebSocket.OPEN) socket.close(4000, 'EV-80 reconnect');
        }
      }
    };
    (window as unknown as RealtimeProbeWindow).__galleryE2ERealtime = probe;
    const ObservedWebSocket = new Proxy(NativeWebSocket, {
      construct(Target, argumentsList) {
        const socket = Reflect.construct(Target, argumentsList) as WebSocket;
        sockets.push(socket);
        probe.socketsCreated += 1;
        socket.addEventListener('open', () => {
          probe.socketsOpened += 1;
        });
        return socket;
      }
    });
    Object.defineProperty(window, 'WebSocket', { value: ObservedWebSocket });
  });
  let bootstrapSnapshots = 0;
  let tokenSnapshots = 0;
  ownerPage.on('response', (response) => {
    if (pathIs(response, '/api/v1/bootstrap')) bootstrapSnapshots += 1;
    if (pathIs(response, '/api/v1/api-tokens')) tokenSnapshots += 1;
  });
  await pair(ownerPage);

  const peerContext = await browser.newContext();
  const peerPage = await peerContext.newPage();
  const peerSessionId = await pair(peerPage);

  await openSecurity(ownerPage, 'API Token');
  await expect(ownerPage.getByText('实时通道：已连接', { exact: true })).toBeVisible();

  // 连续三次切断 Owner A 的真实浏览器网络；每个断线窗口都由 Owner B 创建一项新事实。
  // A 恢复后必须建立新 socket、刷新 bootstrap 与安全 HTTP snapshot，并看到断线期间的变化，
  // 不能依赖 WebSocket 事件重播，也不能让短时抖动累积为永久“重试耗尽”。
  const tokens: { id: string; name: string; secret: string }[] = [];
  for (let cycle = 1; cycle <= 3; cycle += 1) {
    const socketsBefore = await ownerPage.evaluate(
      () => (window as unknown as RealtimeProbeWindow).__galleryE2ERealtime.socketsCreated
    );
    const openedBefore = await ownerPage.evaluate(
      () => (window as unknown as RealtimeProbeWindow).__galleryE2ERealtime.socketsOpened
    );
    const bootstrapBefore = bootstrapSnapshots;
    const tokenSnapshotsBefore = tokenSnapshots;

    await ownerContext.setOffline(true);
    await ownerPage.evaluate(() => {
      (window as unknown as RealtimeProbeWindow).__galleryE2ERealtime.closeOpenSockets();
    });
    await expect(ownerPage.getByText('实时通道：重连中', { exact: true })).toBeVisible({ timeout: 10_000 });

    const name = `EV-80 抖动窗口只读 Token ${cycle}`;
    const token = await createToken(peerPage, name);
    tokens.push({ ...token, name });

    await ownerContext.setOffline(false);
    await expect(ownerPage.getByText('实时通道：已连接', { exact: true })).toBeVisible({ timeout: 15_000 });
    await expect
      .poll(
        () =>
          ownerPage.evaluate(
            () => (window as unknown as RealtimeProbeWindow).__galleryE2ERealtime.socketsCreated
          ),
        { timeout: 15_000 }
      )
      .toBeGreaterThan(socketsBefore);
    await expect
      .poll(
        () =>
          ownerPage.evaluate(
            () => (window as unknown as RealtimeProbeWindow).__galleryE2ERealtime.socketsOpened
          ),
        { timeout: 15_000 }
      )
      .toBeGreaterThan(openedBefore);
    await expect.poll(() => bootstrapSnapshots, { timeout: 15_000 }).toBeGreaterThan(bootstrapBefore);
    await expect.poll(() => tokenSnapshots, { timeout: 15_000 }).toBeGreaterThan(tokenSnapshotsBefore);
    await expect(ownerPage.getByRole('row').filter({ hasText: name })).toHaveCount(1);
    await expectTokenStatus(request, token.secret, 200);
  }

  // Session ID 必须在 UI 中可辨认，否则同一浏览器标签的多条 Session 无法安全选择。
  await ownerPage.getByRole('tab', { name: '会话', exact: true }).click();
  await revokeFromRow(ownerPage, peerSessionId, '吊销会话', `/api/v1/sessions/${peerSessionId}`);
  await expect(peerPage.getByRole('heading', { name: '管理需要认证', exact: true })).toBeVisible({
    timeout: 10_000
  });

  await ownerPage.getByRole('tab', { name: 'API Token', exact: true }).click();
  for (const token of tokens) {
    await revokeFromRow(ownerPage, token.name, '吊销 API Token', `/api/v1/api-tokens/${token.id}`);
    await expectTokenStatus(request, token.secret, 401);
  }

  const works = await ownerPage.evaluate(async () => {
    const response = await fetch('/api/v1/works?sort=title_asc&limit=1', { credentials: 'same-origin' });
    if (!response.ok) throw new Error(`读取作品快照失败: ${response.status}`);
    return (await response.json()) as WorkList;
  });
  const workId = works.works.at(0)?.id;
  if (workId === undefined) throw new Error('已发布合成快照缺少作品');

  await ownerPage.getByRole('tab', { name: '分享', exact: true }).click();
  await ownerPage.getByRole('textbox', { name: '目标 ID', exact: true }).fill(workId);
  const shareResponsePromise = ownerPage.waitForResponse((response) =>
    pathIs(response, '/api/v1/shares', 'POST')
  );
  await ownerPage.getByRole('button', { name: '创建分享', exact: true }).click();
  const shareResponse = await shareResponsePromise;
  expect(shareResponse.status()).toBe(201);
  const share = (await shareResponse.json()) as { id: string };
  const shareSecretNode = ownerPage.getByTestId('one-time-secret');
  await expect(shareSecretNode).toBeVisible();
  const shareSecret = (await shareSecretNode.textContent()) ?? '';
  if (shareSecret.length < 32) throw new Error('分享一次性 credential 缺失或过短');
  await ownerPage.getByRole('button', { name: '我已保存，关闭', exact: true }).click();
  await expect(shareSecretNode).not.toBeVisible();

  const publicShare = await request.get(`/api/v1/public/shares/${encodeURIComponent(shareSecret)}`);
  expect(publicShare.status()).toBe(200);
  const publicBody = (await publicShare.json()) as { scopeKind: string; scopeId: string };
  expect(publicBody).toMatchObject({ scopeKind: 'work', scopeId: workId });

  await revokeFromRow(ownerPage, workId, '吊销分享', `/api/v1/shares/${share.id}`);
  expect((await request.get(`/api/v1/public/shares/${encodeURIComponent(shareSecret)}`)).status()).toBe(404);

  await ownerPage.getByRole('tab', { name: '安全审计', exact: true }).click();
  for (const action of ['token.create', 'session.revoke', 'token.revoke', 'share.create', 'share.revoke']) {
    await expect(ownerPage.getByText(action, { exact: true }).first()).toBeVisible();
  }

  await peerContext.close();
  await ownerContext.close();
});
