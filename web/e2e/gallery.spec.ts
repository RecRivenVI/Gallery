import AxeBuilder from '@axe-core/playwright';
import { expect, test, type Page, type Route } from '@playwright/test';
// 合成 bootstrap 必须直接引用后端权威词表的前端副本：自行书写 capability 名会让 mock
// 套件自证通过，EV-39 的 CAP-1 正是这样被掩盖的。
import { CAPABILITIES } from '../src/auth/capabilities';

test.skip(
  Boolean(process.env.GALLERY_REAL_BASE_URL ?? process.env.GALLERY_REAL_LAN_BASE_URL),
  '此文件使用浏览器内合成 API'
);

const publication = 'qpub_01SYNTHETIC';
const bootstrap = {
  mode: 'personal',
  authenticated: true,
  lanInitialized: false,
  availableCapabilities: [...CAPABILITIES],
  principalId: 'principal_test',
  effectiveCapabilities: [...CAPABILITIES],
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
          mediaCount: 2,
          coverMediaId: 'media_01FIRST',
          favorite: false,
          progress: 0.25,
          queryPublicationId: publication
        }
      ]
    });
  if (url.pathname === '/api/v1/works/work_01SYNTHETIC')
    return json({
      id: 'work_01SYNTHETIC',
      title: '合成作品',
      creator: '测试创作者',
      tags: ['合成', '只读'],
      mediaCount: 2,
      coverMediaId: 'media_01FIRST',
      favorite: false,
      progress: 0.25,
      queryPublicationId: publication
    });
  if (url.pathname === '/api/v1/works/work_01SYNTHETIC/overlay') {
    if (route.request().method() === 'PUT') {
      const body = route.request().postDataJSON() as { customCoverMediaId?: string };
      return json({
        workId: 'work_01SYNTHETIC',
        titleOverride: '',
        manualTags: [],
        hidden: false,
        favorite: false,
        progress: 0.25,
        factWatermark: 2,
        queryWatermark: 1,
        projectedWatermark: 1,
        projectionStatus: 'pending',
        ...(body.customCoverMediaId ? { customCoverMediaId: body.customCoverMediaId } : {})
      });
    }
    return json({
      workId: 'work_01SYNTHETIC',
      titleOverride: '',
      manualTags: [],
      hidden: false,
      favorite: false,
      progress: 0.25,
      factWatermark: 1,
      queryWatermark: 1,
      projectedWatermark: 1,
      projectionStatus: 'published',
      publishedQueryPublicationId: publication
    });
  }
  if (url.pathname === '/api/v1/works/work_01SYNTHETIC/media')
    return json({
      queryPublicationId: publication,
      media: [
        {
          id: 'media_01FIRST',
          workId: 'work_01SYNTHETIC',
          kind: 'image',
          mimeType: 'image/svg+xml',
          sizeBytes: 128,
          blob: { algorithm: 'sha256-v1', digest: 'a'.repeat(64) },
          available: true,
          ordinal: 1,
          queryPublicationId: publication,
          contentVerificationState: 'content_verified',
          verifiedAt: new Date().toISOString()
        },
        {
          id: 'media_01SECOND',
          workId: 'work_01SYNTHETIC',
          kind: 'image',
          mimeType: 'image/svg+xml',
          sizeBytes: 128,
          blob: { algorithm: 'sha256-v1', digest: 'b'.repeat(64) },
          available: true,
          ordinal: 2,
          queryPublicationId: publication,
          contentVerificationState: 'content_verified',
          verifiedAt: new Date().toISOString()
        }
      ]
    });
  if (url.pathname.startsWith('/api/v1/media/') && url.pathname.endsWith('/content'))
    return route.fulfill({
      status: 200,
      contentType: 'image/svg+xml',
      body: '<svg xmlns="http://www.w3.org/2000/svg" width="8" height="6"><rect width="8" height="6" fill="#087f68"/></svg>'
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
  await expect(page.locator('.work-card img')).toHaveAttribute(
    'src',
    '/api/v1/media/media_01FIRST/content?queryPublicationId=qpub_01SYNTHETIC'
  );
  await page.getByRole('button', { name: '切换导航' }).click();
  await expect(page.locator('.app-shell')).toHaveClass(/sidebar-collapsed/);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator('main')).toBeVisible();
});

test('作品详情把 current 解析为单一快照，并保存或清除 CustomCover @smoke', async ({ page }) => {
  const requests: string[] = [];
  page.on('request', (request) => requests.push(request.url()));

  await page.goto('/works/work_01SYNTHETIC');

  await expect(page.getByRole('heading', { name: '合成作品' })).toBeVisible();
  await expect(page.getByRole('img', { name: '《合成作品》的封面' })).toHaveAttribute(
    'src',
    '/api/v1/media/media_01FIRST/content?queryPublicationId=qpub_01SYNTHETIC'
  );
  await expect(page.getByText('有效封面', { exact: true })).toBeVisible();
  const accessibility = await new AxeBuilder({ page }).disableRules(['color-contrast']).analyze();
  expect(
    accessibility.violations.filter((item) => ['critical', 'serious'].includes(item.impact ?? ''))
  ).toEqual([]);

  const workRequest = requests.find((value) => new URL(value).pathname === '/api/v1/works/work_01SYNTHETIC');
  const mediaRequest = requests.find(
    (value) => new URL(value).pathname === '/api/v1/works/work_01SYNTHETIC/media'
  );
  if (!workRequest || !mediaRequest) throw new Error('作品详情请求未完成');
  expect(new URL(workRequest).searchParams.has('queryPublicationId')).toBe(false);
  expect(new URL(mediaRequest).searchParams.get('queryPublicationId')).toBe(publication);
  expect(
    requests
      .filter((value) => new URL(value).pathname.endsWith('/content'))
      .every((value) => new URL(value).searchParams.get('queryPublicationId') === publication)
  ).toBe(true);

  await page.getByText(/^媒体 #2/).click();
  const selectedRequest = page.waitForRequest(
    (request) => request.method() === 'PUT' && new URL(request.url()).pathname.endsWith('/overlay')
  );
  await page.getByRole('button', { name: '保存事实' }).click();
  expect((await selectedRequest).postDataJSON()).toMatchObject({ customCoverMediaId: 'media_01SECOND' });

  await page.getByText(/^使用规则封面/).click();
  const clearedRequest = page.waitForRequest(
    (request) => request.method() === 'PUT' && new URL(request.url()).pathname.endsWith('/overlay')
  );
  await page.getByRole('button', { name: '保存事实' }).click();
  expect((await clearedRequest).postDataJSON()).not.toHaveProperty('customCoverMediaId');
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
