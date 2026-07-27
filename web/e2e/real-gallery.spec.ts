import { expect, test } from '@playwright/test';

const realBaseURL = process.env.GALLERY_REAL_BASE_URL;
test.skip(!realBaseURL, '仅在显式隔离 galleryd 地址存在时执行');

test('真实 galleryd 嵌入资产与 Personal 配对闭环 @real', async ({ browser }) => {
  const first = await browser.newContext();
  const firstPage = await first.newPage();
  await firstPage.goto('/');
  await expect(firstPage.getByRole('heading', { name: '画廊' })).toBeVisible();
  const [attemptResponse, exchangeResponse] = await Promise.all([
    firstPage.waitForResponse((response) => response.url().endsWith('/api/v1/personal/pairing-attempts')),
    firstPage.waitForResponse((response) => response.url().endsWith('/api/v1/personal/pair')),
    firstPage.getByRole('button', { name: '配对本机浏览器' }).click()
  ]);
  expect(attemptResponse.status()).toBe(201);
  expect(exchangeResponse.status()).toBe(201);
  await expect(firstPage.getByRole('heading', { name: '画廊' })).toBeVisible();
  const firstCookies = await first.cookies();
  const firstSession = firstCookies.find((cookie) => cookie.name.includes('session'));
  expect(firstSession?.httpOnly).toBe(true);
  expect(
    await firstPage.evaluate(() => ({
      local: Object.keys(localStorage),
      session: Object.keys(sessionStorage),
      url: location.href
    }))
  ).toEqual({ local: [], session: [], url: `${realBaseURL}/` });

  const second = await browser.newContext();
  const secondPage = await second.newPage();
  await secondPage.goto('/');
  const secondExchangePromise = secondPage.waitForResponse((response) =>
    response.url().endsWith('/api/v1/personal/pair')
  );
  await secondPage.getByRole('button', { name: '配对本机浏览器' }).click();
  const secondExchange = await secondExchangePromise;
  const secondBody = (await secondExchange.json()) as { session: { id: string } };
  await expect(secondPage.getByRole('heading', { name: '画廊' })).toBeVisible();
  const secondSession = (await second.cookies()).find((cookie) => cookie.name.includes('session'));
  expect(secondSession?.value).not.toBe(firstSession?.value);

  const sessions = await firstPage.evaluate(async () => {
    const response = await fetch('/api/v1/sessions', { credentials: 'same-origin' });
    return { status: response.status, body: (await response.json()) as { sessions: unknown[] } };
  });
  expect(sessions.status).toBe(200);
  expect(sessions.body.sessions.length).toBeGreaterThanOrEqual(2);

  expect(
    (sessions.body.sessions as { id: string }[]).some((session) => session.id === secondBody.session.id)
  ).toBe(true);
  const revoke = await firstPage.evaluate(async (sessionId) => {
    const bootstrapResponse = await fetch('/api/v1/bootstrap', { credentials: 'same-origin' });
    const state = (await bootstrapResponse.json()) as { csrfToken: string };
    const response = await fetch(`/api/v1/sessions/${encodeURIComponent(sessionId)}`, {
      method: 'DELETE',
      credentials: 'same-origin',
      headers: { 'X-Gallery-CSRF': state.csrfToken }
    });
    return response.status;
  }, secondBody.session.id);
  expect(revoke).toBe(204);
  await expect(secondPage.getByRole('button', { name: '配对本机浏览器' })).toBeVisible({ timeout: 10_000 });
  await second.close();
  await first.close();
});

test('真实后端拒绝恶意 Origin 的写请求 @real', async ({ request }) => {
  const bootstrap = await request.get('/api/v1/bootstrap');
  const state = (await bootstrap.json()) as { csrfToken: string };
  const response = await request.post('/api/v1/personal/pairing-attempts', {
    headers: { Origin: 'https://attacker.invalid', 'X-Gallery-CSRF': state.csrfToken }
  });
  expect(response.status()).toBe(403);
  expect(((await response.json()) as { error: { code: string } }).error.code).toBe('ORIGIN_REJECTED');
});

test('真实后端正确解码 Job WebSocket 事件并且没有 CSP 违规 @real', async ({ page }) => {
  // 这条用例锁定 EV-39 的 WS-1、WS-2 与 CSP 三项修复：
  // 服务端曾要求浏览器在 WebSocket 握手中不会发送的 Sec-Fetch-Site 头，使 /ws/v1 对
  // Chrome/Edge 恒定 403；前端曾读取 `type` 而契约字段是 `eventType`；React Aria 注入的
  // 内联样式曾被 style-src 拦截。三者都只有真实浏览器 + 真实后端才能发现。
  const violations: string[] = [];
  await page.addInitScript(() => {
    (window as unknown as { __csp: string[] }).__csp = [];
    document.addEventListener('securitypolicyviolation', (event) => {
      (window as unknown as { __csp: string[] }).__csp.push(
        `${event.effectiveDirective} ${event.blockedURI}`
      );
    });
  });
  const socketErrors: string[] = [];
  let framesReceived = 0;
  let jobSnapshotRequests = 0;
  page.on('websocket', (socket) => {
    socket.on('socketerror', (error) => socketErrors.push(error));
    socket.on('framereceived', () => {
      framesReceived += 1;
    });
  });
  page.on('response', (response) => {
    if (response.request().method() === 'GET' && new URL(response.url()).pathname === '/api/v1/jobs') {
      jobSnapshotRequests += 1;
    }
  });

  await page.goto('/manage');
  const pair = page.getByRole('button', { name: '开始配对' });
  if (await pair.isVisible().catch(() => false)) {
    const exchange = page.waitForResponse((response) => response.url().endsWith('/api/v1/personal/pair'));
    await pair.click();
    expect((await exchange).status()).toBe(201);
    await expect(pair).not.toBeVisible();
  }
  await page.getByRole('link', { name: '扫描与任务', exact: true }).click();
  await expect(page.getByRole('heading', { name: '扫描与任务' })).toBeVisible();
  await expect.poll(() => jobSnapshotRequests).toBeGreaterThan(0);
  await expect.poll(() => framesReceived, { timeout: 15_000 }).toBeGreaterThan(0);
  const sequenceLabel = page.getByText(/^序号 \d+$/);
  await expect(sequenceLabel).toHaveText(/^序号 [1-9]\d*$/);
  const readSequence = async () => Number((await sequenceLabel.textContent())?.replace('序号 ', '') ?? 'NaN');
  const sequenceBeforeEvent = await readSequence();

  const requestsBeforeEvent = jobSnapshotRequests;
  const created = await page.evaluate(async () => {
    const bootstrapResponse = await fetch('/api/v1/bootstrap', { credentials: 'same-origin' });
    const state = (await bootstrapResponse.json()) as { csrfToken: string };
    const response = await fetch('/api/v1/admin/control-backups', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'X-Gallery-CSRF': state.csrfToken }
    });
    return {
      status: response.status,
      body: (await response.json()) as { id?: string; error?: { code: string } }
    };
  });
  expect(created.status, JSON.stringify(created.body)).toBe(202);
  expect(created.body.id).toBeTruthy();
  const createdJobId = created.body.id;
  if (createdJobId === undefined) throw new Error('备份响应缺少 Job ID');

  // 备份是通过原始 fetch 创建的，没有经过 useCreateControlBackup 的 onSuccess 失效逻辑。无法解析的帧也会
  // 触发全量快照失效，因此仅断言列表重取仍会产生 WS-2 假阳性；lastSequence 只在信封解析成功后推进，
  // 必须同时严格增加，才能证明服务端 `eventType: job.*` 被前端按权威字段正确解码。
  await expect.poll(readSequence, { timeout: 15_000 }).toBeGreaterThan(sequenceBeforeEvent);
  await expect.poll(() => jobSnapshotRequests, { timeout: 15_000 }).toBeGreaterThan(requestsBeforeEvent);
  await expect(page.getByRole('link', { name: createdJobId, exact: true })).toBeVisible();
  expect(socketErrors).toEqual([]);

  violations.push(...(await page.evaluate(() => (window as unknown as { __csp: string[] }).__csp)));
  expect(violations).toEqual([]);
});
