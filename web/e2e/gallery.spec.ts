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
      creators: [
        {
          id: cursor === null ? 'creator_b' : 'creator_a',
          name: cursor === null ? '画师乙' : '画师甲',
          effectiveId: cursor === null ? 'creator_b' : 'creator_a',
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
  await page.getByRole('button', { name: '加载更多画师', exact: true }).click();
  await expect(page.getByText('画师甲', { exact: true })).toBeVisible();
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

async function expectNoHorizontalOverflow(page: Page) {
  const overflow = await page.evaluate(() => {
    const clientWidth = document.documentElement.clientWidth;
    const scrollWidth = document.documentElement.scrollWidth;
    const elements: Element[] = Array.from(document.querySelectorAll('*'));
    const offenders = elements
      .map((element: Element) => {
        const rect = element.getBoundingClientRect();
        return {
          element: `${element.tagName.toLowerCase()}${element.id ? `#${element.id}` : ''}${
            typeof element.className === 'string' && element.className
              ? `.${element.className.trim().replace(/\s+/g, '.')}`
              : ''
          }`,
          right: Math.round(rect.right * 100) / 100,
          width: Math.round(rect.width * 100) / 100
        };
      })
      .filter((item) => item.right > clientWidth + 0.5)
      .sort((left, right) => right.right - left.right)
      .slice(0, 10);
    return { clientWidth, scrollWidth, offenders };
  });

  expect(
    overflow.scrollWidth,
    `页面发生横向溢出：clientWidth=${overflow.clientWidth}, scrollWidth=${overflow.scrollWidth}, ` +
      `elements=${JSON.stringify(overflow.offenders)}`
  ).toBeLessThanOrEqual(overflow.clientWidth);
}

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
