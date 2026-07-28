import { expect, test } from '@playwright/test';

const realBaseURL = process.env.GALLERY_REAL_BASE_URL;
test.skip(!realBaseURL, '仅在显式隔离 galleryd 地址存在时执行');

interface RealtimeGapProbe {
  armed: boolean;
  droppedSequence: number | null;
  deliveredAfterGap: number | null;
}

type RealtimeGapWindow = Window & { __galleryRealtimeGap: RealtimeGapProbe };

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
  // 失败输出只能包含布尔值，不能让断言框架打印 HttpOnly Session Cookie。
  expect(
    firstSession !== undefined && secondSession !== undefined && secondSession.value !== firstSession.value
  ).toBe(true);

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

    const gap: RealtimeGapProbe = {
      armed: false,
      droppedSequence: null,
      deliveredAfterGap: null
    };
    (window as unknown as RealtimeGapWindow).__galleryRealtimeGap = gap;

    const patchedAddEventListener = function (
      this: WebSocket,
      type: string,
      listener: EventListenerOrEventListenerObject,
      options?: boolean | AddEventListenerOptions
    ): void {
      if (type !== 'message') {
        EventTarget.prototype.addEventListener.call(this, type, listener, options);
        return;
      }

      const wrapped: EventListener = (event) => {
        if (event instanceof MessageEvent && typeof event.data === 'string') {
          try {
            const envelope = JSON.parse(event.data) as {
              eventType?: unknown;
              sequence?: unknown;
            };
            if (
              gap.armed &&
              gap.droppedSequence === null &&
              envelope.eventType !== 'connection.ready' &&
              typeof envelope.sequence === 'number'
            ) {
              gap.armed = false;
              gap.droppedSequence = envelope.sequence;
              return;
            }
            if (
              gap.droppedSequence !== null &&
              gap.deliveredAfterGap === null &&
              typeof envelope.sequence === 'number' &&
              envelope.sequence > gap.droppedSequence
            ) {
              gap.deliveredAfterGap = envelope.sequence;
            }
          } catch {
            // 非 JSON 帧交给生产解析器处理；探针只丢弃一条合法实时信封。
          }
        }

        if (typeof listener === 'function') listener.call(this, event);
        else listener.handleEvent(event);
      };
      EventTarget.prototype.addEventListener.call(this, type, wrapped, options);
    };
    Object.defineProperty(WebSocket.prototype, 'addEventListener', {
      configurable: true,
      writable: true,
      value: patchedAddEventListener
    });
  });
  const socketErrors: string[] = [];
  let framesReceived = 0;
  let jobSnapshotRequests = 0;
  let librarySnapshotRequests = 0;
  let sourceSnapshotRequests = 0;
  page.on('websocket', (socket) => {
    socket.on('socketerror', (error) => socketErrors.push(error));
    socket.on('framereceived', () => {
      framesReceived += 1;
    });
  });
  page.on('response', (response) => {
    if (response.request().method() !== 'GET') return;
    const path = new URL(response.url()).pathname;
    if (path === '/api/v1/jobs') jobSnapshotRequests += 1;
    if (path === '/api/v1/libraries') librarySnapshotRequests += 1;
    if (path === '/api/v1/sources') sourceSnapshotRequests += 1;
  });

  await page.goto('/manage');
  // 等 bootstrap 判定完成再检查配对态；locator.isVisible() 本身不会等待 React 异步渲染，
  // 过早读取会把尚未出现的配对按钮误判为“已经认证”。
  await expect(page.getByRole('heading', { name: /^(管理需要认证|Gallery 管理)$/ })).toBeVisible();
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

  // 先显式刷新一次并等三个当前活跃快照都返回，使后续 libraries/sources 请求不能由连接初始
  // connection.ready 或页面挂载解释。job.* 事件只会失效 jobs；只有 sequence gap 才会让
  // libraries/sources 这两个无关快照一起重取。
  const initialJobRequests = jobSnapshotRequests;
  const initialLibraryRequests = librarySnapshotRequests;
  const initialSourceRequests = sourceSnapshotRequests;
  await page.getByRole('button', { name: '重新拉取快照' }).click();
  await expect.poll(() => jobSnapshotRequests).toBeGreaterThan(initialJobRequests);
  await expect.poll(() => librarySnapshotRequests).toBeGreaterThan(initialLibraryRequests);
  await expect.poll(() => sourceSnapshotRequests).toBeGreaterThan(initialSourceRequests);

  const requestsBeforeEvent = jobSnapshotRequests;
  const libraryRequestsBeforeGap = librarySnapshotRequests;
  const sourceRequestsBeforeGap = sourceSnapshotRequests;
  await page.evaluate(() => {
    (window as unknown as RealtimeGapWindow).__galleryRealtimeGap.armed = true;
  });
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
  // 必须同时严格增加，才能证明服务端 `eventType: job.*` 被前端按权威字段正确解码。本用例还在浏览器
  // 分发层只丢弃一条合法 job.* 信封：下一条真实服务端信封必须形成连续序号缺口，并让与 Job 无关的
  // libraries/sources HTTP 快照一起重取，不能只靠后续 job.* 自身失效 jobs 制造假阳性。
  await expect
    .poll(
      () =>
        page.evaluate(
          () => (window as unknown as RealtimeGapWindow).__galleryRealtimeGap.droppedSequence ?? -1
        ),
      { timeout: 15_000 }
    )
    .toBeGreaterThan(0);
  await expect
    .poll(
      () =>
        page.evaluate(
          () => (window as unknown as RealtimeGapWindow).__galleryRealtimeGap.deliveredAfterGap ?? -1
        ),
      { timeout: 15_000 }
    )
    .toBeGreaterThan(0);
  const gap = await page.evaluate(() => (window as unknown as RealtimeGapWindow).__galleryRealtimeGap);
  expect(gap.deliveredAfterGap).toBe((gap.droppedSequence ?? 0) + 1);
  await expect.poll(readSequence, { timeout: 15_000 }).toBeGreaterThan(sequenceBeforeEvent);
  await expect.poll(() => jobSnapshotRequests, { timeout: 15_000 }).toBeGreaterThan(requestsBeforeEvent);
  await expect
    .poll(() => librarySnapshotRequests, { timeout: 15_000 })
    .toBeGreaterThan(libraryRequestsBeforeGap);
  await expect
    .poll(() => sourceSnapshotRequests, { timeout: 15_000 })
    .toBeGreaterThan(sourceRequestsBeforeGap);
  await expect(page.getByRole('link', { name: createdJobId, exact: true })).toBeVisible();
  expect(socketErrors).toEqual([]);

  violations.push(...(await page.evaluate(() => (window as unknown as { __csp: string[] }).__csp)));
  expect(violations).toEqual([]);
});
