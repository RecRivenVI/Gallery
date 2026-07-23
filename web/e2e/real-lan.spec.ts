import { expect, test, type BrowserContext, type Page } from '@playwright/test';

const lanBaseURL = process.env.GALLERY_REAL_LAN_BASE_URL;
test.skip(!lanBaseURL, '仅在显式隔离 LAN galleryd 地址存在时执行');

const username = 'stage6-owner';
const password = 'stage6-synthetic-password-2026';

async function login(page: Page) {
  await page.getByLabel('用户名').fill(username);
  await page.getByLabel('密码').fill(password);
  const responsePromise = page.waitForResponse((response) => response.url().endsWith('/api/v1/auth/login'));
  await page.getByRole('button', { name: '登录' }).click();
  const response = await responsePromise;
  await expect(page.getByRole('heading', { name: '浏览作品' })).toBeVisible();
  return (await response.json()) as { session: { id: string } };
}

async function currentSessionId(context: BrowserContext, page: Page) {
  const cookie = (await context.cookies()).find((item) => item.name.includes('session'));
  expect(cookie?.httpOnly).toBe(true);
  const response = await page.evaluate(async () => {
    const result = await fetch('/api/v1/sessions', { credentials: 'same-origin' });
    return (await result.json()) as { sessions: { id: string; revoked: boolean }[] };
  });
  return response.sessions.find((session) => !session.revoked)?.id;
}

test('LAN Owner 初始化、双浏览器登录和 Session 吊销 @lan-real', async ({ browser }) => {
  const ownerContext = await browser.newContext();
  const ownerPage = await ownerContext.newPage();
  await ownerPage.goto('/');
  const initialize = ownerPage.getByRole('button', { name: '初始化 LAN Owner' });
  if (await initialize.isVisible()) {
    await ownerPage.getByLabel('用户名').fill(username);
    await ownerPage.getByLabel('显示名称').fill('阶段 6 合成 Owner');
    await ownerPage.getByLabel('Owner 密码（至少 10 字符）').fill(password);
    await initialize.click();
    await expect(ownerPage.getByRole('button', { name: '登录' })).toBeVisible();
  }
  await login(ownerPage);
  const viewerContext = await browser.newContext();
  const viewerPage = await viewerContext.newPage();
  await viewerPage.goto('/');
  const viewerLogin = await login(viewerPage);
  await currentSessionId(viewerContext, viewerPage);

  const after = await ownerPage.evaluate(async () => {
    const response = await fetch('/api/v1/sessions', { credentials: 'same-origin' });
    return (await response.json()) as { sessions: { id: string }[] };
  });
  expect(after.sessions.some((session) => session.id === viewerLogin.session.id)).toBe(true);
  const status = await ownerPage.evaluate(async (sessionId) => {
    const bootstrap = await fetch('/api/v1/bootstrap', { credentials: 'same-origin' });
    const state = (await bootstrap.json()) as { csrfToken: string };
    return (
      await fetch(`/api/v1/sessions/${encodeURIComponent(sessionId)}`, {
        method: 'DELETE',
        credentials: 'same-origin',
        headers: { 'X-Gallery-CSRF': state.csrfToken }
      })
    ).status;
  }, viewerLogin.session.id);
  expect(status).toBe(204);
  await expect(viewerPage.getByRole('button', { name: '登录' })).toBeVisible({ timeout: 10_000 });
  await viewerContext.close();
  await ownerContext.close();
});
