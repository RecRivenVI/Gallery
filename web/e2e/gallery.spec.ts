import AxeBuilder from '@axe-core/playwright';
import { expect, test, type Page, type Route } from '@playwright/test';
import { readFileSync } from 'node:fs';
// 合成 bootstrap 必须直接引用后端权威词表的前端副本：自行书写 capability 名会让 mock
// 套件自证通过，EV-39 的 CAP-1 正是这样被掩盖的。
import { CAPABILITIES } from '../src/auth/capabilities';

test.skip(
  Boolean(process.env.GALLERY_REAL_BASE_URL ?? process.env.GALLERY_REAL_LAN_BASE_URL),
  '此文件使用浏览器内合成 API'
);

const publication = 'qpub_01SYNTHETIC';
const rulePackageSchema = JSON.parse(
  readFileSync(new URL('../../internal/rules/rule-package.schema.json', import.meta.url), 'utf8')
) as unknown;
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
  sortProtocolVersion: 2,
  ruleSchemaVersion: 1
};

async function mockGallery(page: Page) {
  await page.addInitScript(() => {
    let connectionCount = 0;
    class OfflineSocket extends EventTarget {
      static readonly OPEN = 1;
      readonly readyState = OfflineSocket.OPEN;
      constructor() {
        super();
        connectionCount += 1;
        const configuredFailures = Number(new URLSearchParams(location.search).get('e2eWsFailures') ?? '0');
        const shouldFail = Number.isSafeInteger(configuredFailures) && connectionCount <= configuredFailures;
        queueMicrotask(() =>
          this.dispatchEvent(shouldFail ? new CloseEvent('close', { code: 1006 }) : new Event('open'))
        );
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
    Object.defineProperty(window, '__galleryE2EWebSocketConnections', {
      configurable: true,
      get: () => connectionCount
    });
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
      sortProtocolVersion: 2,
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
  if (url.pathname === '/api/v1/api-tokens') {
    if (route.request().method() === 'POST') {
      const body = route.request().postDataJSON() as {
        name: string;
        capabilities: string[];
        scopes: Array<{ kind: string; id?: string }>;
        expiresAt?: string;
      };
      return json(
        {
          id: 'token_01SYNTHETIC',
          principalId: bootstrap.principalId,
          name: body.name,
          secretPrefix: 'gal_syn',
          capabilities: body.capabilities,
          scopes: body.scopes,
          createdAt: new Date().toISOString(),
          expiresAt: body.expiresAt ?? null,
          lastUsedAt: null,
          revoked: false,
          secret: 'gallery_token_synthetic_abcdefghijklmnopqrstuvwxyz'
        },
        201
      );
    }
    return json({ tokens: [] });
  }
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
// 下面这组覆盖的是当前双入口真正交付的东西：双入口各自加载、Service Worker 只归画廊、主题跨入口
// 共享、完整路由表的稳定成功/空/错误状态与可访问性基线。主要写链另由真实 galleryd E2E 覆盖；这里
// 的合成 API 只负责让每条路由进入确定状态，不冒充后端业务证据。
//
// 刻意**不**在这里测 `/manage/jobs` 深链：那条回落由 Go 侧的嵌入处理器按路径前缀决定，
// `vite preview` 用的是它自己的单入口回落，在这里测只会测到预览服务器的行为。该语义已由
// internal/webapp 的 TestManagementDeepLinkServesManagementShell 覆盖，并将在 M6f 对真实
// galleryd 复验。
test('双入口各自加载自己的外壳 @smoke', async ({ page }) => {
  // 断言必须落在"应用真的渲染了"上：#root 存在与否分不出"外壳加载成功"与"路由没匹配、
  // 渲染出空"——管理端曾因 basename 与字面路径 /manage.html 不匹配而恰好是后者，
  // 而 toBeAttached 照样通过。
  await page.goto('/');
  await expect(page).toHaveTitle(/画廊/);
  await expect(page.locator('meta[name="robots"]')).toHaveCount(0);
  await expect(page.locator('#root > *')).not.toHaveCount(0);
  await expect(page.getByRole('navigation', { name: '画廊导航' })).toBeVisible();
  await expect(page.getByRole('button', { name: '导航', exact: true })).toBeHidden();

  await page.goto('/manage');
  await expect(page).toHaveTitle(/管理/);
  await expect(page.locator('#root > *')).not.toHaveCount(0);
  await expect(page.getByRole('navigation', { name: '管理功能' })).toBeVisible();
  await expect(page.getByRole('button', { name: '导航', exact: true })).toBeHidden();
});

test('只有画廊进入 PWA scope @smoke', async ({ page }) => {
  await page.goto('/manage');
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

  await page.goto('/manage');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
});

test('管理端只使用局部状态过渡并尊重减弱动画 @smoke', async ({ page }) => {
  await page.goto('/manage');
  const navigation = page.locator('.manage-nav__link').first();
  await expect(navigation).toBeVisible();
  expect(
    await navigation.evaluate((element) => Number.parseFloat(getComputedStyle(element).transitionDuration))
  ).toBeGreaterThan(0);

  await page.emulateMedia({ reducedMotion: 'reduce' });
  await expect
    .poll(() =>
      navigation.evaluate((element) => Number.parseFloat(getComputedStyle(element).transitionDuration))
    )
    .toBeLessThanOrEqual(0.00001);
  expect(await navigation.evaluate((element) => element.getAnimations().length)).toBe(0);
});

test('平台创作者按有效身份游标分页且窄屏无溢出 @smoke', async ({ page }) => {
  const requested: Array<Record<string, string | null>> = [];
  let creatorWorksRequest: Record<string, string | null> | undefined;
  const json = (route: Route, body: unknown) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) });
  await page.route('**/api/v1/sources', (route) =>
    json(route, {
      sources: [
        {
          id: 'src_pixiv',
          libraryId: 'lib_main',
          displayName: 'pixiv source',
          presentation: {
            name: 'pixiv',
            authorLabel: '画师',
            showInSidebar: true,
            showInManager: true,
            sort: { authorDefault: 'name_desc', authorOptions: ['name_desc', 'name_asc'] }
          },
          readOnly: true,
          available: true,
          createdAt: '2026-07-01T00:00:00Z'
        }
      ]
    })
  );
  await page.route('**/api/v1/creators?*', (route) => {
    const url = new URL(route.request().url());
    requested.push({
      sourceId: url.searchParams.get('sourceId'),
      includeMerged: url.searchParams.get('includeMerged'),
      sort: url.searchParams.get('sort'),
      limit: url.searchParams.get('limit'),
      cursor: url.searchParams.get('cursor')
    });
    const cursor = url.searchParams.get('cursor');
    return json(route, {
      creators:
        cursor === null
          ? Array.from({ length: 48 }, (_, index) => ({
              id: index === 0 ? 'creator_b' : `creator_b_${String(index).padStart(2, '0')}`,
              name: index === 0 ? '画师乙' : `画师乙 ${String(index).padStart(2, '0')}`,
              effectiveId: index === 0 ? 'creator_b' : `creator_b_${String(index).padStart(2, '0')}`,
              sourceCount: 1,
              createdAt: '2026-07-01T00:00:00Z'
            }))
          : [
              {
                id: 'creator_a',
                name: '画师甲',
                effectiveId: 'creator_a',
                sourceCount: 1,
                createdAt: '2026-07-01T00:00:00Z'
              }
            ],
      ...(cursor === null ? { nextCursor: 'page-two' } : {})
    });
  });
  await page.route('**/api/v1/creators/creator_b', (route) =>
    json(route, {
      creator: {
        id: 'creator_b',
        name: '画师乙',
        effectiveId: 'creator_b',
        sourceCount: 1,
        createdAt: '2026-07-01T00:00:00Z'
      },
      sourceBindings: []
    })
  );
  await page.route('**/api/v1/works?*', async (route) => {
    const url = new URL(route.request().url());
    creatorWorksRequest = {
      sourceId: url.searchParams.get('sourceId'),
      filter: url.searchParams.get('filter'),
      sort: url.searchParams.get('sort')
    };
    await route.fallback();
  });

  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/creators?sourceId=src_pixiv');
  await expect(page.getByRole('heading', { name: 'pixiv · 画师' })).toBeVisible();
  await expect(page.getByText('画师乙', { exact: true })).toBeVisible();
  await expect(page.locator('.gal-tile')).toHaveCount(48);
  await page.getByRole('button', { name: '下一页画师', exact: true }).click();
  await expect(page.getByText('画师甲', { exact: true })).toBeVisible();
  await expect(page.getByText('画师乙', { exact: true })).toHaveCount(0);
  await expect(page.locator('.gal-tile')).toHaveCount(1);
  await page.getByRole('button', { name: '上一页画师', exact: true }).click();
  await expect(page.getByText('画师乙', { exact: true })).toBeVisible();
  await expect(page.locator('.gal-tile')).toHaveCount(48);
  await page.getByRole('button', { name: '下一页画师', exact: true }).click();
  await expect(page.getByText('画师甲', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: '上一页画师', exact: true }).click();
  await expect(page.getByText('画师乙', { exact: true })).toBeVisible();
  expect(requested).toEqual([
    { sourceId: 'src_pixiv', includeMerged: 'false', sort: 'name_desc', limit: '48', cursor: null },
    {
      sourceId: 'src_pixiv',
      includeMerged: 'false',
      sort: 'name_desc',
      limit: '48',
      cursor: 'page-two'
    }
  ]);
  const listResults = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
  expect(listResults.violations).toEqual([]);
  await page.getByText('画师乙', { exact: true }).click();
  await expect(page).toHaveURL(/\/creators\/creator_b\?sourceId=src_pixiv$/);
  await expect(page.getByRole('heading', { name: '画师乙' })).toBeVisible();
  await expect
    .poll(() => creatorWorksRequest)
    .toEqual({
      sourceId: 'src_pixiv',
      filter: JSON.stringify({ field: 'creator.id', op: 'eq', value: 'creator_b' }),
      sort: 'title_asc'
    });
  await expectNoHorizontalOverflow(page);
  const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
  expect(results.violations).toEqual([]);
});

test('实时文件目录只挂载当前批并复用已访问批次 @smoke', async ({ page }) => {
  const requestedAfter: Array<string | null> = [];
  await page.route('**/api/v1/file-roots/root_window/entries*', (route) => {
    const url = new URL(route.request().url());
    const after = url.searchParams.get('after');
    requestedAfter.push(after);
    const entries =
      after === null
        ? Array.from({ length: 500 }, (_, index) => {
            const label = String(index + 1).padStart(3, '0');
            return {
              name: `窗口文件 ${label}.jpg`,
              relativePath: `窗口文件 ${label}.jpg`,
              kind: 'file',
              sizeBytes: index + 1
            };
          })
        : [
            {
              name: '窗口文件 501.jpg',
              relativePath: '窗口文件 501.jpg',
              kind: 'file',
              sizeBytes: 501
            }
          ];
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        rootId: 'root_window',
        entries,
        ...(after === null ? { nextAfter: 'page-two' } : {})
      })
    });
  });

  await page.goto('/files/root_window');
  await expect(page.getByText('窗口文件 001.jpg', { exact: true })).toBeVisible();
  await expect(page.locator('.gal-entry')).toHaveCount(500);
  await page.getByRole('button', { name: '下一批', exact: true }).click();
  await expect(page.getByText('窗口文件 501.jpg', { exact: true })).toBeVisible();
  await expect(page.getByText('窗口文件 001.jpg', { exact: true })).toHaveCount(0);
  await expect(page.locator('.gal-entry')).toHaveCount(1);
  await page.getByRole('button', { name: '上一批', exact: true }).click();
  await expect(page.getByText('窗口文件 001.jpg', { exact: true })).toBeVisible();
  await expect(page.locator('.gal-entry')).toHaveCount(500);
  await page.getByRole('button', { name: '下一批', exact: true }).click();
  await expect(page.getByText('窗口文件 501.jpg', { exact: true })).toBeVisible();
  expect(requestedAfter).toEqual([null, 'page-two']);
  await expectNoHorizontalOverflow(page);
  const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
  expect(results.violations).toEqual([]);
});

test('结构决策只挂载当前 keyset 页并复用已访问页 @smoke', async ({ page }) => {
  const requestedCursors: Array<string | null> = [];
  const json = (route: Route, body: unknown) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) });
  await page.route('**/api/v1/source-structure-decisions?*', (route) => {
    const url = new URL(route.request().url());
    const cursor = url.searchParams.get('cursor');
    requestedCursors.push(cursor);
    const decisions =
      cursor === null
        ? Array.from({ length: 50 }, (_, index) => ({
            decisionId: `sdec_PAGE_1_${String(index).padStart(2, '0')}`,
            issueId: `bissue_PAGE_1_${String(index).padStart(2, '0')}`,
            sourceId: 'src_01',
            kind: 'split',
            action: 'split_create_new',
            targetSourceKey: `structure/page-1/${String(index).padStart(2, '0')}`,
            status: 'applied',
            version: 1,
            createdAt: '2026-07-30T01:00:00Z',
            updatedAt: '2026-07-30T01:00:00Z'
          }))
        : [
            {
              decisionId: 'sdec_PAGE_2',
              issueId: 'bissue_PAGE_2',
              sourceId: 'src_01',
              kind: 'merge',
              action: 'merge_create_new',
              targetSourceKey: 'structure/page-2',
              status: 'applied',
              version: 1,
              createdAt: '2026-07-29T01:00:00Z',
              updatedAt: '2026-07-29T01:00:00Z'
            }
          ];
    return json(route, {
      decisions,
      ...(cursor === null ? { nextCursor: 'structure-page-2' } : {})
    });
  });

  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/manage');
  await navigateWithinMountedEntry(page, '/manage/governance');
  await page.getByRole('tab', { name: '结构决策', exact: true }).click();
  const table = page.getByRole('table', { name: '结构决策', exact: true });
  await expect(table.locator('tbody tr')).toHaveCount(50);
  await expect(table.getByText('structure/page-1/00', { exact: true })).toBeVisible();
  await expect(page.getByText(/第 1 页 · 本页 50 条 · 每页最多 50 条（还有下一页）/)).toBeVisible();

  await page.getByRole('button', { name: '下一页', exact: true }).click();
  await expect(table.locator('tbody tr')).toHaveCount(1);
  await expect(table.getByText('structure/page-2', { exact: true })).toBeVisible();
  await expect(table.getByText('structure/page-1/00', { exact: true })).toHaveCount(0);
  await expect(page.getByText(/第 2 页 · 本页 1 条 · 每页最多 50 条（已到末页）/)).toBeVisible();

  await page.getByRole('button', { name: '上一页', exact: true }).click();
  await expect(table.locator('tbody tr')).toHaveCount(50);
  await page.getByRole('button', { name: '下一页', exact: true }).click();
  await expect(table.getByText('structure/page-2', { exact: true })).toBeVisible();
  expect(requestedCursors).toEqual([null, 'structure-page-2']);
  await expectNoHorizontalOverflow(page, '结构决策窄屏分页');
});

test('五类安全资源只挂载当前 keyset 页并复用已访问页 @smoke', async ({ page }) => {
  const requested = new Map<string, Array<string | null>>();
  const register = (
    pattern: string,
    key: string,
    firstItems: Record<string, unknown>[],
    secondItem: Record<string, unknown>
  ) =>
    page.route(pattern, (route) => {
      const url = new URL(route.request().url());
      const cursor = url.searchParams.get('cursor');
      expect(url.searchParams.get('limit')).toBe('50');
      requested.set(key, [...(requested.get(key) ?? []), cursor]);
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          [key]: cursor === null ? firstItems : [secondItem],
          ...(cursor === null ? { nextCursor: `cursor-${key}` } : {})
        })
      });
    });

  const sessions = Array.from({ length: 50 }, (_, index) => ({
    id: `ses_SECURITY_${index}`,
    principalId: 'principal_test',
    authMethod: 'password',
    clientLabel: `安全会话 ${String(index).padStart(2, '0')}`,
    createdAt: '2026-07-31T01:00:00Z',
    lastSeenAt: '2026-07-31T01:01:00Z',
    expiresAt: '2026-08-01T01:00:00Z',
    revoked: false
  }));
  const tokens = Array.from({ length: 50 }, (_, index) => ({
    id: `tok_SECURITY_${index}`,
    principalId: 'principal_test',
    name: `安全 Token ${String(index).padStart(2, '0')}`,
    secretPrefix: `tp${String(index).padStart(2, '0')}`,
    capabilities: ['library.read'],
    scopes: [{ kind: 'global' }],
    createdAt: '2026-07-31T01:00:00Z',
    revoked: false
  }));
  const shares = Array.from({ length: 50 }, (_, index) => ({
    id: `shr_SECURITY_${index}`,
    createdBy: 'principal_test',
    scopeKind: 'work',
    scopeId: `安全分享目标 ${String(index).padStart(2, '0')}`,
    permissions: ['view'],
    secretPrefix: `sp${String(index).padStart(2, '0')}`,
    createdAt: '2026-07-31T01:00:00Z',
    expiresAt: '2026-08-01T01:00:00Z',
    revoked: false
  }));
  const users = Array.from({ length: 50 }, (_, index) => ({
    id: `usr_SECURITY_${index}`,
    username: `security-user-${String(index).padStart(2, '0')}`,
    displayName: `安全账户 ${String(index).padStart(2, '0')}`,
    status: 'active',
    roles: ['viewer'],
    securityVersion: 1,
    createdAt: '2026-07-31T01:00:00Z',
    updatedAt: '2026-07-31T01:00:00Z'
  }));
  const grants = Array.from({ length: 50 }, (_, index) => ({
    id: `grnt_SECURITY_${index}`,
    principalId: 'usr_SECURITY_50',
    effect: 'allow',
    capability: `security.fixture.${String(index).padStart(2, '0')}`,
    scope: { kind: 'global' },
    revoked: false
  }));

  await register('**/api/v1/sessions?*', 'sessions', sessions, {
    ...sessions[0],
    id: 'ses_SECURITY_50',
    clientLabel: '安全会话 50'
  });
  await register('**/api/v1/api-tokens?*', 'tokens', tokens, {
    ...tokens[0],
    id: 'tok_SECURITY_50',
    name: '安全 Token 50'
  });
  await register('**/api/v1/shares?*', 'shares', shares, {
    ...shares[0],
    id: 'shr_SECURITY_50',
    scopeId: '安全分享目标 50'
  });
  await register('**/api/v1/admin/users?*', 'users', users, {
    ...users[0],
    id: 'usr_SECURITY_50',
    username: 'security-user-50',
    displayName: '安全账户 50'
  });
  await register('**/api/v1/admin/users/usr_SECURITY_50/grants?*', 'grants', grants, {
    ...grants[0],
    id: 'grnt_SECURITY_50',
    capability: 'security.fixture.50'
  });

  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/manage');
  // 先让连接初始快照屏障完成，再挂载安全页；否则 onOpen 与首个 HTTP 查询的先后取决于
  // 浏览器调度，语义正确的快照重置会被误计成分页缓存的重复请求。
  // 窄屏下桌面侧栏本来就不在可见树中；DOM 文案切到“已连接”已经足以证明屏障完成。
  await expect(page.getByText('实时通道：已连接', { exact: true })).toBeAttached();
  await navigateWithinMountedEntry(page, '/manage/security');

  const assertWindow = async (tableName: string, firstText: string, secondText: string) => {
    const table = page.getByRole('table', { name: tableName, exact: true });
    await expect(table.locator('tbody tr')).toHaveCount(50);
    await expect(table.getByText(firstText, { exact: true })).toBeVisible();
    await page.getByRole('button', { name: '下一页', exact: true }).click();
    await expect(table.locator('tbody tr')).toHaveCount(1);
    await expect(table.getByText(secondText, { exact: true })).toBeVisible();
    await expect(table.getByText(firstText, { exact: true })).toHaveCount(0);
  };

  await assertWindow('活动会话', '安全会话 00', '安全会话 50');
  await page.getByRole('button', { name: '上一页', exact: true }).click();
  await page.getByRole('button', { name: '下一页', exact: true }).click();
  await expect(page.getByText('安全会话 50', { exact: true })).toBeVisible();

  await page.getByRole('tab', { name: 'API Token', exact: true }).click();
  await assertWindow('API Token', '安全 Token 00', '安全 Token 50');
  await page.getByRole('tab', { name: '分享', exact: true }).click();
  await assertWindow('分享', 'work:安全分享目标 00', 'work:安全分享目标 50');
  await page.getByRole('tab', { name: '账户与授权', exact: true }).click();
  await assertWindow('本地账户', 'security-user-00', 'security-user-50');
  const selectedRow = page.getByText('security-user-50', { exact: true }).locator('..').locator('..');
  await selectedRow.getByRole('button', { name: '查看授权', exact: true }).click();
  await assertWindow('账户授权', 'security.fixture.00', 'security.fixture.50');

  expect(requested.get('sessions')).toEqual([null, 'cursor-sessions']);
  expect(requested.get('tokens')).toEqual([null, 'cursor-tokens']);
  expect(requested.get('shares')).toEqual([null, 'cursor-shares']);
  expect(requested.get('users')).toEqual([null, 'cursor-users']);
  expect(requested.get('grants')).toEqual([null, 'cursor-grants']);
  await expectNoHorizontalOverflow(page, '安全资源窄屏分页');
  const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
  expect(results.violations).toEqual([]);
});

test('规则配置大数组窗口与超深 JSON 守卫在窄屏稳定 @smoke', async ({ page }) => {
  const ruleSetId = 'rset_00000000-0000-7000-8000-000000000001';
  let deepPayload: unknown = 'deep-leaf';
  for (let depth = 0; depth < 254; depth += 1) deepPayload = { child: deepPayload };
  const content = {
    rule_set_id: ruleSetId,
    version: '1.0.0',
    schema_version: 1,
    normalization_algorithm_version: 'gallery-canonical-json-v1',
    compiler_requirement: 'gallery-rule-compiler-v1',
    cel_profile_version: 'gallery-cel-v1',
    parameter_schema: { type: 'object', additionalProperties: false },
    provider_namespaces: [],
    primitives: Array.from({ length: 4096 }, (_, index) => ({
      id: `primitive_limit_${String(index).padStart(4, '0')}`,
      kind: 'metadata_map',
      config: { fields: {} }
    })),
    cel_expressions: [],
    tests: Array.from({ length: 10_000 }, (_, index) => ({
      id: `test-limit-${String(index).padStart(5, '0')}`
    })),
    extensions: {
      'gallery.window': {
        required: false,
        semantic: false,
        payload: Array.from({ length: 21 }, (_, index) => `payload-${String(index).padStart(2, '0')}`)
      },
      'gallery.deep': {
        required: false,
        semantic: false,
        payload: deepPayload
      }
    }
  };
  const json = (route: Route, body: unknown, contentType = 'application/json') =>
    route.fulfill({ status: 200, contentType, body: JSON.stringify(body) });

  await page.route('**/api/v1/rule-packages/pkg_window', (route) =>
    json(route, {
      id: 'pkg_window',
      ruleSetId,
      name: '规则窗口合成夹具',
      description: '',
      status: 'active',
      createdBy: 'principal_test',
      revision: 5,
      createdAt: '2026-07-31T00:00:00Z',
      updatedAt: '2026-07-31T00:00:00Z'
    })
  );
  await page.route('**/api/v1/rule-packages/pkg_window/draft', (route) =>
    json(route, {
      id: 'draft_window',
      packageId: 'pkg_window',
      content,
      contentText: JSON.stringify(content, null, 2),
      format: 'json',
      validationStatus: 'draft',
      diagnostics: [],
      revision: 3,
      savedBy: 'principal_test',
      createdAt: '2026-07-31T00:00:00Z',
      updatedAt: '2026-07-31T00:00:00Z'
    })
  );
  await page.route('**/api/v1/rule-packages/pkg_window/versions', (route) => json(route, { items: [] }));
  await page.route('**/api/v1/rule-packages/pkg_window/audits', (route) => json(route, { items: [] }));
  await page.route('**/api/v1/rules/schema', (route) =>
    json(route, rulePackageSchema, 'application/schema+json')
  );
  await page.route('**/api/v1/rules/examples', (route) => json(route, { items: [] }));

  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/manage');
  await navigateWithinMountedEntry(page, '/manage/rules/pkg_window');
  await page.getByRole('tab', { name: 'Schema 表单', exact: true }).click();
  await expect(page.getByTestId('rule-schema-form')).toBeVisible();

  await expect(
    page.getByText('规则原语 · 第 1 / 205 页 · 本页 20 项 · 共 4096 项 · 每页最多 20 项。')
  ).toBeVisible();
  await expect(
    page.getByText('规则测试 · 第 1 / 500 页 · 本页 20 项 · 共 10000 项 · 每页最多 20 项。')
  ).toBeVisible();
  await expect(page.locator('input[value^="primitive_limit_"]')).toHaveCount(20);
  await expect(page.locator('input[value^="test-limit-"]')).toHaveCount(20);
  await expect(page.locator('input[value="primitive_limit_0000"]')).toBeVisible();
  await expect(page.locator('input[value="primitive_limit_0020"]')).toHaveCount(0);
  await page.getByRole('button', { name: '下一页：规则原语', exact: true }).click();
  await expect(page.locator('input[value="primitive_limit_0020"]')).toBeVisible();
  await expect(page.locator('input[value="primitive_limit_0000"]')).toHaveCount(0);
  await page.getByRole('button', { name: '上一页：规则原语', exact: true }).click();
  await expect(page.locator('input[value="primitive_limit_0000"]')).toBeVisible();

  await expect(page.locator('input[value="payload-00"]')).toBeVisible();
  await expect(page.locator('input[value="payload-20"]')).toHaveCount(0);
  await page.getByRole('button', { name: '下一页：gallery.window payload 数组', exact: true }).click();
  await expect(page.locator('input[value="payload-20"]')).toBeVisible();
  await expect(page.locator('input[value="payload-00"]')).toHaveCount(0);
  await expect(page.getByRole('alert')).toContainText(
    '结构化编辑已暂停：gallery.deep payload 会使规则容器嵌套超过 256 层'
  );
  await expect(page.getByRole('textbox', { name: 'extensions 原始 JSON' })).toHaveValue(/deep-leaf/);

  await expectNoHorizontalOverflow(page, '规则配置大数组与超深 JSON 窄屏边界');
  const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
  expect(results.violations).toEqual([]);
  await expect(page.getByRole('textbox', { name: 'tests 原始 JSON' })).toHaveValue(/test-limit-09999/);
  await page.getByRole('tab', { name: 'JSON 文本', exact: true }).click();
  const completeDraft = page.getByRole('textbox', { name: '草稿内容', exact: true });
  await expect(completeDraft).toHaveValue(/primitive_limit_4095/);
  await expect(completeDraft).toHaveValue(/test-limit-09999/);
});

test('迟到的旧分页响应不会覆盖较新的搜索结果 @smoke', async ({ page }) => {
  let releaseOldPage: (() => void) | undefined;
  let oldPageSettled = false;
  const list = (id: string, title: string, nextCursor?: string) => ({
    queryPublicationId: publication,
    sortProtocolVersion: 2,
    rankProtocolVersion: 2,
    catalogRevision: 'cat_weak_network',
    overlayProjectionRevision: 'overlay_weak_network',
    total: { mode: 'exact', value: 1, protocolVersion: 1 },
    dependencySet: [],
    liveUserStateFields: ['favorite', 'progress'],
    works: [
      {
        id,
        title,
        creator: '弱网测试创作者',
        tags: ['合成'],
        mediaCount: 0,
        coverMediaId: null,
        favorite: false,
        progress: 0,
        queryPublicationId: publication
      }
    ],
    ...(nextCursor === undefined ? {} : { nextCursor })
  });

  // 该路由在 beforeEach 的通用 API 桩之后注册，因此只接管本用例的作品查询；其余请求
  // 继续回落到同一合成 bootstrap。第二页故意无视客户端取消并迟到返回，锁定 UI 代次。
  await page.route('**/api/v1/works?*', async (route) => {
    const url = new URL(route.request().url());
    const query = url.searchParams.get('q') ?? '';
    const cursor = url.searchParams.get('cursor');
    const fulfill = (body: unknown) =>
      route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) });

    if (query === '旧分页查询' && cursor === null) {
      await fulfill(list('work_old_first', '旧分页第一页', 'old-late-page'));
      return;
    }
    if (query === '旧分页查询' && cursor === 'old-late-page') {
      try {
        await new Promise<void>((resolve) => {
          releaseOldPage = resolve;
        });
        await fulfill(list('work_old_late', '迟到的旧分页第二页'));
      } catch {
        // Chromium/Firefox 都允许已取消的 fetch 不再接收 fulfill；无论传输层是否保留迟到
        // 字节，页面都必须保持较新的查询。settled 标记让断言等到该分支确实收尾。
      } finally {
        oldPageSettled = true;
      }
      return;
    }
    if (query === '最新查询') {
      await fulfill(list('work_latest', '最新查询结果'));
      return;
    }
    await route.fallback();
  });

  await page.goto('/browse');
  await expect(page.getByText('合成作品', { exact: true })).toBeVisible();
  const search = page.getByRole('searchbox', { name: '搜索作品' });
  await search.fill('旧分页查询');
  await page.getByRole('button', { name: '搜索', exact: true }).click();
  await expect(page.getByText('旧分页第一页', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: '加载更多', exact: true }).click();
  await expect.poll(() => releaseOldPage !== undefined).toBe(true);

  await search.fill('最新查询');
  await page.getByRole('button', { name: '搜索', exact: true }).click();
  await expect(page.getByText('最新查询结果', { exact: true })).toBeVisible();
  releaseOldPage?.();
  await expect.poll(() => oldPageSettled).toBe(true);
  await expect(page.getByText('迟到的旧分页第二页', { exact: true })).toHaveCount(0);
  await expect(page.getByText('最新查询结果', { exact: true })).toBeVisible();
});

test('大量作品连续加载保持有界 DOM 并在返回时恢复位置 @smoke @work-window', async ({ page }) => {
  const pageSize = 48;
  const pageCount = 12;
  const totalWorks = pageSize * pageCount;
  const listCursors: Array<string | null> = [];
  const work = (index: number) => {
    const ordinal = String(index + 1).padStart(3, '0');
    return {
      id: `work_window_${ordinal}`,
      title: `窗口作品 ${ordinal}`,
      creator: '窗口测试创作者',
      tags: ['合成'],
      mediaCount: 0,
      coverMediaId: null,
      favorite: false,
      progress: 0,
      queryPublicationId: publication
    };
  };
  const json = (route: Route, body: unknown) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) });

  await page.route('**/api/v1/works**', async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname === '/api/v1/works') {
      const cursor = url.searchParams.get('cursor');
      listCursors.push(cursor);
      const pageIndex = cursor === null ? 0 : Number(cursor.slice('window-page-'.length));
      const start = pageIndex * pageSize;
      await json(route, {
        queryPublicationId: publication,
        sortProtocolVersion: 2,
        rankProtocolVersion: 2,
        catalogRevision: 'cat_window',
        overlayProjectionRevision: 'overlay_window',
        total: { mode: 'exact', value: totalWorks, protocolVersion: 1 },
        dependencySet: [],
        liveUserStateFields: ['favorite', 'progress'],
        works: Array.from({ length: pageSize }, (_, offset) => work(start + offset)),
        ...(pageIndex + 1 < pageCount ? { nextCursor: `window-page-${pageIndex + 1}` } : {})
      });
      return;
    }

    const mediaMatch = /^\/api\/v1\/works\/(work_window_(\d+))\/media$/.exec(url.pathname);
    if (mediaMatch !== null) {
      await json(route, { queryPublicationId: publication, media: [] });
      return;
    }
    const overlayMatch = /^\/api\/v1\/works\/(work_window_(\d+))\/overlay$/.exec(url.pathname);
    if (overlayMatch !== null) {
      await json(route, {
        workId: overlayMatch[1],
        titleOverride: '',
        manualTags: [],
        hidden: false,
        favorite: false,
        progress: 0,
        factWatermark: 1,
        queryWatermark: 1,
        projectedWatermark: 1,
        projectionStatus: 'published',
        publishedQueryPublicationId: publication
      });
      return;
    }
    const detailMatch = /^\/api\/v1\/works\/work_window_(\d+)$/.exec(url.pathname);
    if (detailMatch !== null) {
      await json(route, work(Number(detailMatch[1]) - 1));
      return;
    }
    await route.fallback();
  });

  await page.goto('/browse');
  await expect(page.getByText('窗口作品 048', { exact: true })).toBeAttached();
  for (let pageIndex = 1; pageIndex < pageCount; pageIndex += 1) {
    await page.getByRole('button', { name: '加载更多', exact: true }).click();
    await expect(
      page.getByText(`窗口作品 ${String((pageIndex + 1) * pageSize).padStart(3, '0')}`, { exact: true })
    ).toBeAttached();
  }

  expect(listCursors).toEqual([
    null,
    ...Array.from({ length: pageCount - 1 }, (_, index) => `window-page-${index + 1}`)
  ]);
  await expect(page.getByText('窗口作品 576', { exact: true })).toBeVisible();
  await expect(page.getByText('窗口作品 001', { exact: true })).toHaveCount(0);
  await expect.poll(() => page.locator('.gal-grid__cell').count()).toBeLessThanOrEqual(pageSize * 3);

  await page.evaluate(() => window.scrollTo(0, 0));
  await expect(page.getByText('窗口作品 001', { exact: true })).toBeVisible();
  await expect(page.getByText('窗口作品 576', { exact: true })).toHaveCount(0);

  await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
  await expect(page.getByText('窗口作品 576', { exact: true })).toBeVisible();
  await page.getByText('窗口作品 576', { exact: true }).click();
  await expect(page.getByRole('heading', { name: '窗口作品 576' })).toBeVisible();
  await page.goBack();
  await expect(page).toHaveURL(/\/browse$/);
  await expect(page.getByText('窗口作品 576', { exact: true })).toBeVisible();
  await expect.poll(() => page.locator('.gal-grid__cell').count()).toBeLessThanOrEqual(pageSize * 3);

  const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
  expect(results.violations).toEqual([]);
});

test('作品快照替换保留旧内容并按业务身份连续交接 @smoke', async ({ page }) => {
  let releaseReordered: (() => void) | undefined;
  const work = (id: string, title: string) => ({
    id,
    title,
    creator: '动效测试创作者',
    tags: ['合成'],
    mediaCount: 0,
    coverMediaId: null,
    favorite: false,
    progress: 0,
    queryPublicationId: publication
  });
  const list = (works: unknown[]) => ({
    queryPublicationId: publication,
    sortProtocolVersion: 2,
    rankProtocolVersion: 2,
    catalogRevision: 'cat_motion',
    overlayProjectionRevision: 'overlay_motion',
    total: { mode: 'exact', value: works.length, protocolVersion: 1 },
    dependencySet: [],
    liveUserStateFields: ['favorite', 'progress'],
    works
  });

  await page.route('**/api/v1/works?*', async (route) => {
    const query = new URL(route.request().url()).searchParams.get('q') ?? '';
    if (query === '重排') {
      await new Promise<void>((resolve) => {
        releaseReordered = resolve;
      });
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(
          list([work('work_c', '作品丙'), work('work_a', '作品甲'), work('work_d', '作品丁')])
        )
      });
    }
    if (query === '减弱') {
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(
          list([work('work_a', '作品甲'), work('work_c', '作品丙'), work('work_e', '作品戊')])
        )
      });
    }
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(
        list([work('work_a', '作品甲'), work('work_b', '作品乙'), work('work_c', '作品丙')])
      )
    });
  });

  await page.goto('/browse');
  await expect(page.getByText('作品乙', { exact: true })).toBeVisible();
  // 延长本用例的观察窗口只为稳定读取浏览器动画状态，不改变生产 token。
  await page.evaluate(() => {
    document.documentElement.style.setProperty('--motion-state', '1600ms');
    document.documentElement.style.setProperty('--motion-structure', '1600ms');
  });

  const search = page.getByRole('searchbox', { name: '搜索作品' });
  await search.fill('重排');
  await page.getByRole('button', { name: '搜索', exact: true }).click();
  await expect.poll(() => releaseReordered !== undefined).toBe(true);
  await expect(page.getByText('作品乙', { exact: true })).toBeVisible();
  await expect(page.getByText('正在获取结果', { exact: true })).toBeAttached();
  await expect(page.locator('.gal-grid')).toHaveAttribute('inert', '');
  await expect(page.locator('.gal-grid')).toHaveAttribute('aria-hidden', 'true');

  releaseReordered?.();
  await expect(page.getByText('作品丁', { exact: true })).toBeVisible();
  await expect
    .poll(() =>
      page
        .locator('.gal-grid__cell')
        .filter({ hasText: '作品丙' })
        .evaluate((element) => element.getAnimations().length)
    )
    .toBeGreaterThan(0);
  const ghost = page.locator('.ui-motion-ghost');
  await expect(ghost).toHaveCount(1);
  await expect(ghost).toHaveAttribute('aria-hidden', 'true');
  await expect(ghost).toHaveAttribute('inert', '');
  await page.evaluate(() => document.getAnimations().forEach((animation) => animation.finish()));
  await expect(ghost).toHaveCount(0);

  await page.evaluate(() => {
    document.documentElement.style.removeProperty('--motion-state');
    document.documentElement.style.removeProperty('--motion-structure');
  });
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await expect
    .poll(() =>
      page.evaluate(() =>
        Number.parseFloat(
          getComputedStyle(document.documentElement).getPropertyValue('--motion-structure').trim()
        )
      )
    )
    .toBe(0);
  await search.fill('减弱');
  await page.getByRole('button', { name: '搜索', exact: true }).click();
  await expect(page.getByText('作品戊', { exact: true })).toBeVisible();
  expect(
    await page
      .locator('.gal-grid__cell')
      .evaluateAll((elements) =>
        elements.reduce((count, element) => count + element.getAnimations().length, 0)
      )
  ).toBe(0);
  await expect(page.locator('.ui-motion-ghost')).toHaveCount(0);
});

test('服务停机超过旧重连预算后仍会自动恢复快照通道 @smoke', async ({ page }) => {
  await page.clock.install();
  await page.goto('/manage?e2eWsFailures=9');
  await expect(page.getByText('实时通道：重连中', { exact: true })).toBeVisible();

  // 第九次失败发生在旧实现约 75 秒的永久停止点；再经过一次 15 秒封顶退避后，
  // 第十条连接恢复。Clock 只压缩测试墙钟，不改变生产资产实际执行的 timer/状态机。
  await page.clock.runFor(90_000);
  await expect(page.getByText('实时通道：已连接', { exact: true })).toBeVisible();
  await expect
    .poll(() =>
      page.evaluate(() => {
        const value: unknown = Reflect.get(window, '__galleryE2EWebSocketConnections');
        return typeof value === 'number' ? value : -1;
      })
    )
    .toBe(10);
  await expect(page.getByText(/停止原因：/)).toHaveCount(0);
});

test('两个入口都没有严重可访问性违规 @smoke', async ({ page }) => {
  for (const path of ['/', '/manage']) {
    await page.goto(path);
    // color-contrast 必须启用：旧基线把它 disableRules 掉了，等于放弃了对比度这一项。
    const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
    expect(results.violations, `${path} 存在可访问性违规`).toEqual([]);
  }
});

type AccessibilityRoute = {
  path: string;
  ready: (page: Page) => ReturnType<Page['locator']>;
};

const galleryAccessibilityRoutes: readonly AccessibilityRoute[] = [
  { path: '/', ready: (current) => current.getByRole('heading', { name: '画廊', exact: true }) },
  { path: '/browse', ready: (current) => current.getByRole('heading', { name: '全部作品' }) },
  { path: '/sources/missing', ready: (current) => current.getByText('找不到这个平台', { exact: true }) },
  { path: '/creators', ready: (current) => current.getByRole('heading', { name: '创作者', exact: true }) },
  {
    path: '/creators/missing',
    ready: (current) => current.getByRole('heading', { name: '创作者', exact: true })
  },
  {
    path: '/works/work_01SYNTHETIC',
    ready: (current) => current.getByRole('heading', { name: '合成作品', exact: true })
  },
  {
    path: `/works/work_01SYNTHETIC/view/media_01FIRST?queryPublicationId=${publication}`,
    ready: (current) => current.getByRole('link', { name: '返回作品', exact: true })
  },
  { path: '/files', ready: (current) => current.getByRole('heading', { name: '文件', exact: true }) },
  {
    path: '/files/missing',
    ready: (current) => current.getByRole('navigation', { name: '路径', exact: true })
  },
  { path: '/route-not-found', ready: (current) => current.getByText('这个页面不存在', { exact: true }) }
];

const manageAccessibilityRoutes: readonly AccessibilityRoute[] = [
  { path: '/manage', ready: (current) => current.getByRole('heading', { name: '概览', exact: true }) },
  {
    path: '/manage/scans',
    ready: (current) => current.getByRole('heading', { name: '扫描与任务', exact: true })
  },
  {
    path: '/manage/scans/missing',
    ready: (current) => current.getByRole('heading', { name: '任务详情', exact: true })
  },
  {
    path: '/manage/diagnostics',
    ready: (current) => current.getByRole('heading', { name: '验证和诊断', exact: true })
  },
  {
    path: '/manage/security',
    ready: (current) => current.getByRole('heading', { name: '连接与安全', exact: true })
  },
  {
    path: '/manage/rules',
    ready: (current) => current.getByRole('heading', { name: '规则', exact: true })
  },
  {
    path: '/manage/rules/missing',
    ready: (current) => current.getByRole('heading', { name: '规则包', exact: true })
  },
  {
    path: '/manage/governance',
    ready: (current) => current.getByRole('heading', { name: '治理', exact: true })
  },
  {
    path: '/manage/route-not-found',
    ready: (current) => current.getByText('没有这个管理页面', { exact: true })
  }
];

async function navigateWithinMountedEntry(page: Page, path: string): Promise<void> {
  await page.evaluate((nextPath) => {
    window.history.pushState({}, '', nextPath);
    window.dispatchEvent(new PopStateEvent('popstate'));
  }, path);
  await expect(page).toHaveURL(new RegExp(`${path.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}$`));
}

async function expectRoutesAccessible(page: Page, routes: readonly AccessibilityRoute[]): Promise<void> {
  for (const route of routes) {
    await navigateWithinMountedEntry(page, route.path);
    await expect(route.ready(page), `${route.path} 没有进入预期稳定状态`).toBeVisible();
    // 页面标题往往先于并行查询出现；等局部加载指示全部收敛后再检查最终 DOM，避免只测到骨架。
    await expect(page.locator('.ui-spinner')).toHaveCount(0);
    const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
    expect(results.violations, `${route.path} 存在可访问性违规`).toEqual([]);
  }
}

test('当前用户端与管理端完整路由表在桌面与窄屏通过自动可访问性基线 @smoke', async ({ page }) => {
  test.setTimeout(90_000);
  for (const viewport of [
    { width: 1280, height: 800 },
    { width: 390, height: 844 }
  ]) {
    await page.setViewportSize(viewport);
    await page.goto('/');
    await expectRoutesAccessible(page, galleryAccessibilityRoutes);
    await page.goto('/manage');
    await expectRoutesAccessible(page, manageAccessibilityRoutes);
  }
});

async function expectDialogFocusBoundary(page: Page, dialogName: string, triggerName: string) {
  const trigger = page.getByRole('button', { name: triggerName, exact: true });
  await trigger.focus();
  await page.keyboard.press('Enter');

  const dialog = page.getByRole('dialog', { name: dialogName, exact: true });
  await expect(dialog).toBeVisible();
  await expect.poll(() => dialog.evaluate((element) => element.contains(document.activeElement))).toBe(true);

  for (let index = 0; index < 10; index += 1) {
    await page.keyboard.press(index % 2 === 0 ? 'Tab' : 'Shift+Tab');
    expect(await dialog.evaluate((element) => element.contains(document.activeElement))).toBe(true);
  }

  const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
  expect(results.violations, `${dialogName} 存在可访问性违规`).toEqual([]);

  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
  await expect(trigger).toBeFocused();
}

async function expectNoHorizontalOverflow(page: Page, context = '') {
  const overflow = await page.evaluate(() => {
    const clientWidth = document.documentElement.clientWidth;
    const scrollWidth = document.documentElement.scrollWidth;
    const elements: Element[] = Array.from(document.querySelectorAll('*'));
    const offenders = elements
      .map((element: Element) => {
        const rect = element.getBoundingClientRect();
        const style = getComputedStyle(element);
        const parentRect = element.parentElement?.getBoundingClientRect();
        return {
          element: `${element.tagName.toLowerCase()}${element.id ? `#${element.id}` : ''}${
            typeof element.className === 'string' && element.className
              ? `.${element.className.trim().replace(/\s+/g, '.')}`
              : ''
          }`,
          left: Math.round(rect.left * 100) / 100,
          right: Math.round(rect.right * 100) / 100,
          width: Math.round(rect.width * 100) / 100,
          cssWidth: style.width,
          minWidth: style.minWidth,
          boxSizing: style.boxSizing,
          parentWidth: parentRect ? Math.round(parentRect.width * 100) / 100 : null,
          parentRight: parentRect ? Math.round(parentRect.right * 100) / 100 : null
        };
      })
      .filter((item) => item.right > clientWidth + 0.5)
      .sort((left, right) => right.right - left.right)
      .slice(0, 10);
    return { clientWidth, scrollWidth, offenders };
  });

  expect(
    overflow.scrollWidth,
    `${context ? `${context} ` : ''}页面发生横向溢出：clientWidth=${overflow.clientWidth}, scrollWidth=${overflow.scrollWidth}, ` +
      `elements=${JSON.stringify(overflow.offenders)}`
  ).toBeLessThanOrEqual(overflow.clientWidth);
}

const wcagTextSpacingOverride = `
  :where(body, body *) {
    line-height: 1.5 !important;
    letter-spacing: 0.12em !important;
    word-spacing: 0.16em !important;
  }
  p {
    margin-bottom: 2em !important;
  }
`;

async function expectRoutesAccessibleWithReflow(
  page: Page,
  routes: readonly AccessibilityRoute[]
): Promise<void> {
  for (const route of routes) {
    await navigateWithinMountedEntry(page, route.path);
    await expect(route.ready(page), `${route.path} 没有进入预期稳定状态`).toBeVisible();
    await expect(page.locator('.ui-spinner')).toHaveCount(0);
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'high-contrast');
    await expectNoHorizontalOverflow(page, route.path);
    const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
    expect(results.violations, `${route.path} 在高对比/文本间距/320px 下存在可访问性违规`).toEqual([]);
  }
}

async function expectCurrentInteractiveStateAccessible(page: Page, context: string): Promise<void> {
  await expectNoHorizontalOverflow(page, context);
  const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
  expect(results.violations, `${context} 存在可访问性违规`).toEqual([]);
}

test('高对比、文本间距与 400% 等效宽度下完整路由保持可访问重排 @smoke', async ({ page }) => {
  test.setTimeout(90_000);
  await page.addInitScript(() => window.localStorage.setItem('gallery.theme', 'high-contrast'));
  await page.emulateMedia({ forcedColors: 'active', contrast: 'more' });
  await page.setViewportSize({ width: 320, height: 800 });

  await page.goto('/');
  expect(await page.evaluate(() => window.matchMedia('(forced-colors: active)').matches)).toBe(true);
  await page.addStyleTag({ content: wcagTextSpacingOverride });
  await expectRoutesAccessibleWithReflow(page, galleryAccessibilityRoutes);

  await page.goto('/manage');
  expect(await page.evaluate(() => window.matchMedia('(forced-colors: active)').matches)).toBe(true);
  await page.addStyleTag({ content: wcagTextSpacingOverride });
  await expectRoutesAccessibleWithReflow(page, manageAccessibilityRoutes);
});

test('高对比组合下关键表单、校验、弹出层与一次性密文保持可访问 @smoke', async ({ page }) => {
  test.setTimeout(60_000);
  await page.addInitScript(() => window.localStorage.setItem('gallery.theme', 'high-contrast'));
  await page.emulateMedia({ forcedColors: 'active', contrast: 'more' });
  await page.setViewportSize({ width: 320, height: 800 });

  await page.goto(`/works/work_01SYNTHETIC?queryPublicationId=${publication}`);
  await page.addStyleTag({ content: wcagTextSpacingOverride });
  await expect(page.getByRole('heading', { name: '我的编辑', exact: true })).toBeVisible();
  await page.getByRole('button', { name: /自定义封面$/ }).click();
  await expect(page.getByRole('listbox')).toBeVisible();
  await expectCurrentInteractiveStateAccessible(page, '作品自定义封面选单');
  await page.keyboard.press('Escape');

  await page.goto('/manage');
  await page.addStyleTag({ content: wcagTextSpacingOverride });
  await navigateWithinMountedEntry(page, '/manage/diagnostics');
  const retention = page.getByRole('textbox', { name: '保留期（秒）', exact: true });
  await retention.fill('-1');
  await expect(page.getByText('必须是非负整数', { exact: true })).toBeVisible();
  await expectCurrentInteractiveStateAccessible(page, '维护表单校验错误');
  await retention.fill('86400');
  await page.getByRole('button', { name: '创建维护任务', exact: true }).click();
  const maintenanceDialog = page.getByRole('dialog', { name: '创建维护任务', exact: true });
  await expect(maintenanceDialog).toBeVisible();
  await expectCurrentInteractiveStateAccessible(page, '维护确认对话框');
  await maintenanceDialog.getByRole('button', { name: '取消', exact: true }).click();

  await navigateWithinMountedEntry(page, '/manage/security');
  await page.getByRole('tab', { name: 'API Token', exact: true }).click();
  await page.getByRole('textbox', { name: '名称', exact: true }).fill('可访问性门禁');
  const capability = page.getByRole('checkbox', { name: 'library.read', exact: true });
  await capability.focus();
  await capability.press('Space');
  await expect(capability).toBeChecked();
  const expiry = page.getByRole('textbox', { name: '有效期（天）', exact: true });
  await expiry.fill('0');
  await expect(page.getByText('必须是正整数', { exact: true })).toBeVisible();
  await expectCurrentInteractiveStateAccessible(page, 'Token 表单校验错误');
  await expiry.fill('1');
  await page.getByRole('button', { name: '创建 Token', exact: true }).click();
  const secretDialog = page.getByRole('dialog', { name: 'API Token 密文', exact: true });
  await expect(secretDialog).toBeVisible();
  await expectCurrentInteractiveStateAccessible(page, '一次性密文对话框');
});

test('窄屏导航限制焦点并由 Escape 关闭后返还触发点 @smoke', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });

  await page.goto('/');
  await expect(page.getByRole('navigation', { name: '画廊导航' })).toBeHidden();
  await expectDialogFocusBoundary(page, '画廊导航', '导航');
  await page.getByRole('button', { name: '导航', exact: true }).click();
  await page
    .getByRole('dialog', { name: '画廊导航', exact: true })
    .getByRole('link', { name: '全部作品' })
    .click();
  await expect(page).toHaveURL(/\/browse$/);
  await expect(page.getByRole('dialog', { name: '画廊导航', exact: true })).toBeHidden();
  await expect(page.getByRole('button', { name: '导航', exact: true })).toBeFocused();
  await expectNoHorizontalOverflow(page);

  await page.goto('/manage');
  await expect(page.getByRole('navigation', { name: '管理功能' })).toBeHidden();
  await expectDialogFocusBoundary(page, '管理导航', '导航');
  await page.getByRole('button', { name: '导航', exact: true }).click();
  await page
    .getByRole('dialog', { name: '管理导航', exact: true })
    .getByRole('link', { name: '扫描与任务' })
    .click();
  await expect(page).toHaveURL(/\/manage\/scans$/);
  await expect(page.getByRole('dialog', { name: '管理导航', exact: true })).toBeHidden();
  await expect(page.getByRole('button', { name: '导航', exact: true })).toBeFocused();
  await expectNoHorizontalOverflow(page);

  await page.setViewportSize({ width: 320, height: 844 });
  await expect(page.getByRole('navigation', { name: '管理功能' })).toBeHidden();
  await expectNoHorizontalOverflow(page);
});
