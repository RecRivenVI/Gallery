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

// 本轮前端从零重写：旧的页面级用例（浏览/作品详情/CustomCover/错误态/分享 credential）针对的
// 页面已随旧单入口一并删除，因此不能原样保留——让它们"通过"只会让门禁自证。
//
// 下面这组覆盖的是本切片**真正交付**的东西：双入口各自加载、Service Worker 只归画廊、主题跨入口
// 共享、两个入口的可访问性基线。页面级端到端在画廊端与管理端实现后由 M6f 重建，届时连同真实后端
// 一起进 CI，而不是继续用浏览器内合成 API。
//
// 刻意**不**在这里测 `/manage/jobs` 深链：那条回落由 Go 侧的嵌入处理器按路径前缀决定，
// `vite preview` 用的是它自己的单入口回落，在这里测只会测到预览服务器的行为。该语义已由
// internal/webapp 的 TestManagementDeepLinkServesManagementShell 覆盖，并将在 M6f 对真实
// galleryd 复验。
test('双入口各自加载自己的外壳 @smoke', async ({ page }) => {
  await page.goto('/');
  await expect(page).toHaveTitle(/画廊/);
  await expect(page.locator('meta[name="robots"]')).toHaveCount(0);
  await expect(page.locator('#root')).toBeAttached();

  await page.goto('/manage.html');
  await expect(page).toHaveTitle(/管理/);
  await expect(page.locator('#root')).toBeAttached();
});

test('只有画廊进入 PWA scope @smoke', async ({ page }) => {
  await page.goto('/manage.html');
  // 管理端不是可分享内容，也不应出现"安装 Gallery"入口。
  await expect(page.locator('link[rel="manifest"]')).toHaveCount(0);
  await expect(page.locator('meta[name="robots"]')).toHaveAttribute('content', /noindex/);

  await page.goto('/');
  await expect(page.locator('link[rel="manifest"]')).toHaveCount(1);
});

test('主题选择跨两个入口共享 @smoke', async ({ page }) => {
  await page.goto('/');
  await page.evaluate(() => window.localStorage.setItem('gallery.theme', 'dark'));
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');

  await page.goto('/manage.html');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
});

test('两个入口都没有严重可访问性违规 @smoke', async ({ page }) => {
  for (const path of ['/', '/manage.html']) {
    await page.goto(path);
    // color-contrast 必须启用：旧基线把它 disableRules 掉了，等于放弃了对比度这一项。
    const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
    expect(results.violations, `${path} 存在可访问性违规`).toEqual([]);
  }
});
