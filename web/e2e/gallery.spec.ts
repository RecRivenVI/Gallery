import AxeBuilder from '@axe-core/playwright';
import { expect, test, type Page, type Route } from '@playwright/test';

test.skip(
  Boolean(process.env.GALLERY_REAL_BASE_URL ?? process.env.GALLERY_REAL_LAN_BASE_URL),
  '此文件使用浏览器内合成 API'
);

const publication = 'qpub_01SYNTHETIC';
const bootstrap = {
  mode: 'personal',
  authenticated: true,
  lanInitialized: false,
  availableCapabilities: [
    'library.read',
    'overlay.write',
    'media.read',
    'jobs.cancel',
    'rules.read',
    'clients.manage',
    'shares.create',
    'users.manage'
  ],
  principalId: 'principal_test',
  effectiveCapabilities: [
    'library.read',
    'overlay.write',
    'media.read',
    'jobs.cancel',
    'rules.read',
    'clients.manage',
    'shares.create',
    'users.manage'
  ],
  csrfToken: 'csrf-browser-only',
  apiVersion: 'v1',
  websocketProtocolVersion: 1,
  sortProtocolVersion: 1,
  ruleSchemaVersion: 1
};

async function mockGallery(page: Page) {
  await page.addInitScript(() => {
    class OfflineSocket extends EventTarget {
      static readonly OPEN = 1;
      readonly readyState = OfflineSocket.OPEN;
      constructor() {
        super();
        queueMicrotask(() => this.dispatchEvent(new Event('open')));
      }
      close() {
        this.dispatchEvent(new CloseEvent('close', { code: 1000 }));
      }
      send() {
        /* no client messages in protocol v1 */
      }
      addEventListener(type: string, listener: EventListenerOrEventListenerObject) {
        super.addEventListener(type, listener);
      }
    }
    Object.defineProperty(window, 'WebSocket', { value: OfflineSocket });
  });
  await page.route('**/api/v1/**', async (route) => respond(route));
}

async function respond(route: Route) {
  const url = new URL(route.request().url());
  const json = (body: unknown, status = 200) =>
    route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
  if (url.pathname === '/api/v1/bootstrap') return json(bootstrap);
  if (url.pathname === '/api/v1/works')
    return json({
      queryPublicationId: publication,
      sortProtocolVersion: 1,
      rankProtocolVersion: 2,
      catalogRevision: 'cat_1',
      overlayProjectionRevision: 'overlay_1',
      total: { mode: 'exact', value: 1, protocolVersion: 1 },
      dependencySet: [],
      liveUserStateFields: ['favorite', 'progress'],
      works: [
        {
          id: 'work_01SYNTHETIC',
          title: '合成作品',
          creator: '测试创作者',
          tags: ['合成', '只读'],
          mediaCount: 1,
          favorite: false,
          progress: 0.25,
          queryPublicationId: publication
        }
      ]
    });
  if (url.pathname === '/api/v1/libraries') return json({ libraries: [] });
  if (url.pathname === '/api/v1/sources') return json({ sources: [] });
  if (url.pathname === '/api/v1/jobs') return json({ jobs: [] });
  if (url.pathname === '/api/v1/creators') return json({ creators: [] });
  if (url.pathname === '/api/v1/rule-packages') return json({ items: [] });
  if (url.pathname === '/api/v1/rule-parameters') return json({ parameterSets: [] });
  if (url.pathname === '/api/v1/source-rule-bindings') return json({ bindings: [] });
  if (url.pathname === '/api/v1/sessions') return json({ sessions: [] });
  if (url.pathname === '/api/v1/api-tokens') return json({ tokens: [] });
  if (url.pathname === '/api/v1/admin/users') {
    if (route.request().method() === 'POST')
      return json(
        {
          id: 'user_new',
          username: 'viewer',
          displayName: '只读访客',
          status: 'active',
          roles: ['viewer'],
          securityVersion: 1,
          createdAt: new Date().toISOString(),
          updatedAt: new Date().toISOString()
        },
        201
      );
    return json({ users: [] });
  }
  if (url.pathname === '/api/v1/shares') {
    if (route.request().method() === 'POST')
      return json(
        {
          id: 'share_new',
          scopeKind: 'work',
          scopeId: 'work_01SYNTHETIC',
          permissions: ['view'],
          createdAt: new Date().toISOString(),
          expiresAt: new Date(Date.now() + 86_400_000).toISOString(),
          revoked: false,
          secret: 'share_abcdefghijklmnopqrstuvwxyz123456'
        },
        201
      );
    return json({ shares: [] });
  }
  return json({ error: { code: 'NOT_FOUND', retryable: false, correlationId: 'corr_e2e' } }, 404);
}

test.beforeEach(async ({ page }) => mockGallery(page));

test('浏览、主题和响应式导航可用 @smoke', async ({ page }) => {
  await page.goto('/browse');
  await expect(page.getByRole('heading', { name: '浏览作品' })).toBeVisible();
  await expect(page.getByRole('link', { name: '合成作品' })).toBeVisible();
  await page.getByRole('button', { name: '切换导航' }).click();
  await expect(page.locator('.app-shell')).toHaveClass(/sidebar-collapsed/);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator('main')).toBeVisible();
});

test('核心页面没有严重可访问性违规 @smoke', async ({ page }) => {
  await page.goto('/browse');
  const results = await new AxeBuilder({ page }).disableRules(['color-contrast']).analyze();
  expect(results.violations.filter((item) => ['critical', 'serious'].includes(item.impact ?? ''))).toEqual(
    []
  );
});

test('服务端错误显示稳定、可恢复的中文状态', async ({ page }) => {
  await page.route('**/api/v1/works*', (route) =>
    route.fulfill({
      status: 409,
      contentType: 'application/json',
      body: JSON.stringify({
        error: { code: 'CURSOR_EXPIRED', retryable: false, correlationId: 'corr_expired' }
      })
    })
  );
  await page.goto('/browse?cursor=expired');
  await expect(page.getByText('查询快照已过期，请从第一页重新开始。')).toBeVisible();
  await expect(page.getByRole('button', { name: '重试' })).toBeVisible();
});

test('安全写路径只在内存显示一次性分享 credential', async ({ page }) => {
  await page.goto('/security');
  await page.getByLabel('范围 ID').fill('work_01SYNTHETIC');
  await page.getByRole('button', { name: '创建 7 天只读分享' }).click();
  await expect(page.getByRole('heading', { name: '请立即保存分享链接' })).toBeVisible();
  const storage = await page.evaluate(() => Object.fromEntries(Object.entries(localStorage)));
  expect(JSON.stringify(storage)).not.toContain('share_abcdefghijklmnopqrstuvwxyz123456');

  await page.getByLabel('新账户用户名').fill('viewer');
  await page.getByLabel('显示名称').fill('只读访客');
  await page.getByLabel('初始密码').fill('synthetic-password');
  await page.getByRole('button', { name: '创建 Viewer 账户' }).click();
});
