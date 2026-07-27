import { expect, test, type BrowserContext, type Page, type Response } from '@playwright/test';

const lanBaseURL = process.env.GALLERY_REAL_LAN_BASE_URL;
test.skip(!lanBaseURL, '仅由显式隔离 LAN loopback galleryd 运行器执行');
test.setTimeout(90_000);

const ownerUsername = 'stage6-owner';
const ownerPassword = 'stage6-synthetic-owner-password-2026';
const viewerUsername = 'stage6-viewer';
const viewerPassword = 'stage6-synthetic-viewer-password-2026';

function pathIs(response: Response, path: string, method = 'GET'): boolean {
  return response.request().method() === method && new URL(response.url()).pathname === path;
}

async function login(page: Page, username: string, password: string): Promise<string> {
  await page.getByLabel('用户名').fill(username);
  await page.getByLabel('密码').fill(password);
  const responsePromise = page.waitForResponse((response) => pathIs(response, '/api/v1/auth/login', 'POST'));
  await page.getByRole('button', { name: '登录', exact: true }).click();
  const response = await responsePromise;
  expect(response.status()).toBe(201);
  const established = (await response.json()) as { session: { id: string } };
  await expect(page.getByRole('heading', { name: 'Gallery 管理', exact: true })).toBeVisible();
  return established.session.id;
}

async function bootstrap(page: Page): Promise<{ effectiveCapabilities: string[] }> {
  return page.evaluate(async () => {
    const response = await fetch('/api/v1/bootstrap', { credentials: 'same-origin' });
    if (!response.ok) throw new Error(`读取 bootstrap 失败: ${response.status}`);
    return (await response.json()) as { effectiveCapabilities: string[] };
  });
}

async function expectHttpOnlySession(context: BrowserContext): Promise<void> {
  const cookie = (await context.cookies()).find((item) => item.name.includes('session'));
  expect(cookie?.httpOnly).toBe(true);
}

test('LAN Owner、账户、Grant、Session 与停用恢复全部经管理 UI @lan-real', async ({ browser }) => {
  const ownerContext = await browser.newContext();
  const ownerPage = await ownerContext.newPage();
  await ownerPage.goto('/manage');
  await expect(ownerPage.getByRole('heading', { name: '管理需要认证', exact: true })).toBeVisible();

  await ownerPage.getByLabel('用户名').fill(ownerUsername);
  await ownerPage.getByLabel('显示名').fill('阶段 6 合成 Owner');
  await ownerPage.getByLabel('密码').fill(ownerPassword);
  const initializePromise = ownerPage.waitForResponse((response) =>
    pathIs(response, '/api/v1/lan/owner', 'POST')
  );
  await ownerPage.getByRole('button', { name: '创建 Owner', exact: true }).click();
  expect((await initializePromise).status()).toBe(201);
  await expect(ownerPage.getByRole('button', { name: '登录', exact: true })).toBeVisible();
  await login(ownerPage, ownerUsername, ownerPassword);
  await expectHttpOnlySession(ownerContext);

  await ownerPage.goto('/manage/security');
  await expect(ownerPage.getByRole('heading', { name: '连接与安全', exact: true })).toBeVisible();
  await ownerPage.getByRole('tab', { name: '账户与授权', exact: true }).click();
  await ownerPage.getByRole('textbox', { name: '用户名', exact: true }).fill(viewerUsername);
  await ownerPage.getByRole('textbox', { name: '显示名', exact: true }).fill('阶段 6 合成 Viewer');
  await ownerPage.getByLabel('初始密码').fill(viewerPassword);
  const userResponsePromise = ownerPage.waitForResponse((response) =>
    pathIs(response, '/api/v1/admin/users', 'POST')
  );
  await ownerPage.getByRole('button', { name: '创建账户', exact: true }).click();
  const userResponse = await userResponsePromise;
  expect(userResponse.status()).toBe(201);
  const viewer = (await userResponse.json()) as { id: string; username: string };

  const viewerRow = ownerPage.getByRole('row').filter({ hasText: viewerUsername });
  await expect(viewerRow).toHaveCount(1);
  await viewerRow.getByRole('button', { name: '查看授权', exact: true }).click();
  async function addGlobalGrant(effect: 'allow' | 'deny', capability: string): Promise<{ id: string }> {
    await ownerPage.getByRole('button', { name: /效果/ }).click();
    await ownerPage
      .getByRole('dialog', { name: '效果', exact: true })
      .getByRole('option', {
        name: effect === 'allow' ? 'allow（授予）' : 'deny（拒绝，优先级更高）',
        exact: true
      })
      .click();
    await ownerPage.getByRole('button', { name: /capability/ }).click();
    await ownerPage.getByRole('option', { name: capability, exact: true }).click();
    const responsePromise = ownerPage.waitForResponse((response) =>
      pathIs(response, `/api/v1/admin/users/${viewer.id}/grants`, 'POST')
    );
    await ownerPage.getByRole('button', { name: '添加授权', exact: true }).click();
    const response = await responsePromise;
    expect(response.status()).toBe(201);
    return (await response.json()) as { id: string };
  }

  await addGlobalGrant('allow', 'library.read');
  await addGlobalGrant('allow', 'media.read');
  const grant = await addGlobalGrant('deny', 'media.read');

  const viewerContext = await browser.newContext();
  const viewerPage = await viewerContext.newPage();
  await viewerPage.goto('/manage');
  await login(viewerPage, viewerUsername, viewerPassword);
  await expectHttpOnlySession(viewerContext);
  const deniedBootstrap = await bootstrap(viewerPage);
  expect(deniedBootstrap.effectiveCapabilities).toContain('library.read');
  expect(deniedBootstrap.effectiveCapabilities).not.toContain('media.read');

  const grantRow = ownerPage.getByRole('row').filter({ hasText: /deny.*media\.read|media\.read.*deny/ });
  await expect(grantRow).toHaveCount(1);
  const revokeGrantPromise = ownerPage.waitForResponse((response) =>
    pathIs(response, `/api/v1/admin/grants/${grant.id}`, 'DELETE')
  );
  await grantRow.getByRole('button', { name: '撤销', exact: true }).click();
  await ownerPage
    .getByRole('dialog', { name: '撤销授权', exact: true })
    .getByRole('button', { name: '确认撤销', exact: true })
    .click();
  expect((await revokeGrantPromise).status()).toBe(204);
  await expect(viewerPage.getByRole('heading', { name: '管理需要认证', exact: true })).toBeVisible({
    timeout: 10_000
  });

  await login(viewerPage, viewerUsername, viewerPassword);
  expect((await bootstrap(viewerPage)).effectiveCapabilities).toContain('media.read');

  const disablePromise = ownerPage.waitForResponse((response) =>
    pathIs(response, `/api/v1/admin/users/${viewer.id}/status`, 'PATCH')
  );
  await viewerRow.getByRole('button', { name: '停用', exact: true }).click();
  await ownerPage
    .getByRole('dialog', { name: '停用账户', exact: true })
    .getByRole('button', { name: '确认停用', exact: true })
    .click();
  expect((await disablePromise).status()).toBe(204);
  await expect(viewerPage.getByRole('heading', { name: '管理需要认证', exact: true })).toBeVisible({
    timeout: 10_000
  });

  await viewerPage.getByLabel('用户名').fill(viewerUsername);
  await viewerPage.getByLabel('密码').fill(viewerPassword);
  const blockedLoginPromise = viewerPage.waitForResponse((response) =>
    pathIs(response, '/api/v1/auth/login', 'POST')
  );
  await viewerPage.getByRole('button', { name: '登录', exact: true }).click();
  expect((await blockedLoginPromise).status()).toBe(401);
  const loginError = viewerPage.getByRole('alert');
  await expect(loginError).toContainText('用户名或密码不正确。');
  await expect(loginError).toContainText('INVALID_CREDENTIALS');

  const enablePromise = ownerPage.waitForResponse((response) =>
    pathIs(response, `/api/v1/admin/users/${viewer.id}/status`, 'PATCH')
  );
  await viewerRow.getByRole('button', { name: '恢复启用', exact: true }).click();
  expect((await enablePromise).status()).toBe(204);
  const viewerSessionId = await login(viewerPage, viewerUsername, viewerPassword);

  await ownerPage.getByRole('tab', { name: '会话', exact: true }).click();
  const sessionRow = ownerPage.getByRole('row').filter({ hasText: viewerSessionId });
  await expect(sessionRow).toHaveCount(1);
  const revokeSessionPromise = ownerPage.waitForResponse((response) =>
    pathIs(response, `/api/v1/sessions/${viewerSessionId}`, 'DELETE')
  );
  await sessionRow.getByRole('button', { name: '吊销', exact: true }).click();
  await ownerPage
    .getByRole('dialog', { name: '吊销会话', exact: true })
    .getByRole('button', { name: '确认吊销', exact: true })
    .click();
  expect((await revokeSessionPromise).status()).toBe(204);
  await expect(viewerPage.getByRole('heading', { name: '管理需要认证', exact: true })).toBeVisible({
    timeout: 10_000
  });

  await ownerPage.getByRole('tab', { name: '安全审计', exact: true }).click();
  for (const action of [
    'owner.initialize',
    'user.create',
    'grant.create',
    'grant.revoke',
    'user.status',
    'session.revoke'
  ]) {
    await expect(ownerPage.getByText(action, { exact: true }).first()).toBeVisible();
  }

  await viewerContext.close();
  await ownerContext.close();
});
