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

test('Personal 安全资源、断线快照恢复与一次性密文真实链 @real-security', async ({ browser, request }) => {
  const ownerContext = await browser.newContext();
  const ownerPage = await ownerContext.newPage();
  await ownerPage.addInitScript(() => {
    const NativeWebSocket = window.WebSocket;
    const sockets: WebSocket[] = [];
    const ObservedWebSocket = new Proxy(NativeWebSocket, {
      construct(Target, argumentsList) {
        const socket = Reflect.construct(Target, argumentsList) as WebSocket;
        sockets.push(socket);
        return socket;
      }
    });
    Object.defineProperty(window, 'WebSocket', { value: ObservedWebSocket });
    (window as typeof window & { __galleryE2ECloseSockets?: () => void }).__galleryE2ECloseSockets = () => {
      for (const socket of sockets) {
        if (socket.readyState === WebSocket.OPEN) socket.close(4000, 'EV-60 reconnect');
      }
    };
  });
  await pair(ownerPage);

  const peerContext = await browser.newContext();
  const peerPage = await peerContext.newPage();
  const peerSessionId = await pair(peerPage);

  await openSecurity(ownerPage, 'API Token');
  await expect(ownerPage.getByText('实时通道：已连接', { exact: true })).toBeVisible();

  // 让 Owner A 真正离线，Owner B 在断线窗口经 UI 创建 Token。A 恢复网络后不能依赖事件重播，
  // 必须由新连接的 HTTP snapshot 自动看到这项变化。
  await ownerContext.setOffline(true);
  await ownerPage.evaluate(() => {
    (window as typeof window & { __galleryE2ECloseSockets?: () => void }).__galleryE2ECloseSockets?.();
  });
  await expect(ownerPage.getByText('实时通道：重连中', { exact: true })).toBeVisible({ timeout: 10_000 });
  const tokenName = 'EV-60 断线窗口只读 Token';
  const token = await createToken(peerPage, tokenName);
  await ownerContext.setOffline(false);
  await expect(ownerPage.getByText('实时通道：已连接', { exact: true })).toBeVisible({ timeout: 15_000 });
  const tokenRow = ownerPage.getByRole('row').filter({ hasText: tokenName });
  await expect(tokenRow).toHaveCount(1);
  await expectTokenStatus(request, token.secret, 200);

  // Session ID 必须在 UI 中可辨认，否则同一浏览器标签的多条 Session 无法安全选择。
  await ownerPage.getByRole('tab', { name: '会话', exact: true }).click();
  await revokeFromRow(ownerPage, peerSessionId, '吊销会话', `/api/v1/sessions/${peerSessionId}`);
  await expect(peerPage.getByRole('heading', { name: '管理需要认证', exact: true })).toBeVisible({
    timeout: 10_000
  });

  await ownerPage.getByRole('tab', { name: 'API Token', exact: true }).click();
  await revokeFromRow(ownerPage, tokenName, '吊销 API Token', `/api/v1/api-tokens/${token.id}`);
  await expectTokenStatus(request, token.secret, 401);

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
