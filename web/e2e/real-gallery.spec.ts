import { expect, test } from '@playwright/test';

const realBaseURL = process.env.GALLERY_REAL_BASE_URL;
test.skip(!realBaseURL, '仅在显式隔离 galleryd 地址存在时执行');

test('真实 galleryd 嵌入资产与 Personal 配对闭环 @real', async ({ browser }) => {
  const first = await browser.newContext();
  const firstPage = await first.newPage();
  await firstPage.goto('/');
  await expect(firstPage.getByRole('heading', { name: 'Gallery · 画廊' })).toBeVisible();
  const [attemptResponse, exchangeResponse] = await Promise.all([
    firstPage.waitForResponse((response) => response.url().endsWith('/api/v1/personal/pairing-attempts')),
    firstPage.waitForResponse((response) => response.url().endsWith('/api/v1/personal/pair')),
    firstPage.getByRole('button', { name: '在此浏览器完成一次性配对' }).click()
  ]);
  expect(attemptResponse.status()).toBe(201);
  expect(exchangeResponse.status()).toBe(201);
  await expect(firstPage.getByRole('heading', { name: '浏览作品' })).toBeVisible();
  const firstCookies = await first.cookies();
  const firstSession = firstCookies.find((cookie) => cookie.name.includes('session'));
  expect(firstSession?.httpOnly).toBe(true);
  expect(
    await firstPage.evaluate(() => ({
      local: Object.keys(localStorage),
      session: Object.keys(sessionStorage),
      url: location.href
    }))
  ).toEqual({ local: ['gallery.theme'], session: [], url: `${realBaseURL}/browse` });

  const second = await browser.newContext();
  const secondPage = await second.newPage();
  await secondPage.goto('/');
  const secondExchangePromise = secondPage.waitForResponse((response) =>
    response.url().endsWith('/api/v1/personal/pair')
  );
  await secondPage.getByRole('button', { name: '在此浏览器完成一次性配对' }).click();
  const secondExchange = await secondExchangePromise;
  const secondBody = (await secondExchange.json()) as { session: { id: string } };
  await expect(secondPage.getByRole('heading', { name: '浏览作品' })).toBeVisible();
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
  await expect(secondPage.getByRole('heading', { name: 'Gallery · 画廊' })).toBeVisible({ timeout: 10_000 });
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
