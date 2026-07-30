/*
 * 画廊页面级行为。
 *
 * 覆盖的是"契约写着、但很容易在界面上写反"的四件事：
 *
 * 1. `lower_bound` 数量必须显示成下限，不能伪装成精确值；
 * 2. `CURSOR_EXPIRED` 必须丢弃游标从第一页重来，并告诉用户；
 * 3. 收藏切换必须先 GET 再整体 PUT（否则会清掉别的用户事实），
 *    并以服务端返回的 live 值调和，不能显示回列表快照里的旧值；
 * 4. 没有创作者、没有发布时间是真实事实，要有中文空态，不能出现 undefined / Invalid Date。
 */

import { QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Link, MemoryRouter, Route, Routes } from 'react-router';
import { beforeEach, describe, expect, it } from 'vitest';
import { useState, type ReactNode } from 'react';
import { faultResponse, jsonResponse, setFetchHandler } from '../../tests/http';
import { ToastProvider } from '../design';
import { createQueryClient } from '../shared/query';
import { SessionProvider } from '../shared/session';
import { ThemeProvider } from '../shared/theme';
import { WorkBrowser } from './components/browser';
import { TopBar } from './components/chrome';
import { MediaImage, MediaLoaderProvider } from './components/media';
import { MediaLoader } from './media';
import { WorkPage } from './pages/work';
import { CreatorsPage } from './pages/discover';

const PUBLICATION = 'qpub_test';

const BOOTSTRAP = {
  mode: 'personal',
  authenticated: true,
  lanInitialized: false,
  availableCapabilities: ['library.read', 'media.read', 'overlays.write'],
  effectiveCapabilities: ['library.read', 'media.read', 'overlays.write'],
  principalId: 'principal_test',
  csrfToken: 'csrf-token-for-tests',
  apiVersion: 'v1',
  websocketProtocolVersion: 1,
  sortProtocolVersion: 2,
  ruleSchemaVersion: 1
};

interface WorkOverrides {
  id?: string;
  title?: string;
  creator?: string;
  publishedAt?: string | null;
  favorite?: boolean;
  coverMediaId?: string | null;
}

function work(overrides: WorkOverrides = {}) {
  return {
    id: overrides.id ?? 'work_1',
    title: overrides.title ?? '合成作品',
    creator: overrides.creator ?? '画师甲',
    tags: ['合成'],
    mediaCount: 3,
    coverMediaId: overrides.coverMediaId ?? null,
    badges: [],
    favorite: overrides.favorite ?? false,
    progress: 0.25,
    publishedAt: overrides.publishedAt === undefined ? '2026-07-01T00:00:00Z' : overrides.publishedAt,
    queryPublicationId: PUBLICATION
  };
}

interface ListOverrides {
  works?: unknown[];
  total?: unknown;
  nextCursor?: string;
}

function workList(overrides: ListOverrides = {}) {
  return {
    queryPublicationId: PUBLICATION,
    sortProtocolVersion: 2,
    rankProtocolVersion: 2,
    catalogRevision: 'cat_1',
    overlayProjectionRevision: 'overlay_1',
    total: overrides.total ?? { mode: 'exact', value: 1, protocolVersion: 1 },
    dependencySet: [],
    liveUserStateFields: ['favorite', 'progress'],
    works: overrides.works ?? [work()],
    ...(overrides.nextCursor === undefined ? {} : { nextCursor: overrides.nextCursor })
  };
}

function overlayState(overrides: Record<string, unknown> = {}) {
  return {
    workId: 'work_1',
    titleOverride: '我的标题',
    manualTags: ['我的标签'],
    hidden: false,
    favorite: false,
    progress: 0.25,
    factWatermark: 1,
    queryWatermark: 1,
    projectedWatermark: 1,
    projectionStatus: 'published',
    publishedQueryPublicationId: PUBLICATION,
    ...overrides
  };
}

function publishedMedia(id: string, ordinal: number) {
  return {
    id,
    workId: 'work_1',
    kind: 'image',
    mimeType: 'image/png',
    sizeBytes: 80,
    blob: null,
    available: true,
    ordinal,
    queryPublicationId: PUBLICATION,
    contentVerificationState: 'located_unverified'
  };
}

/** 媒体加载器在测试里被完全替换：jsdom 没有 object URL，也不该发真实媒体请求。 */
function testMediaLoader(): MediaLoader {
  return new MediaLoader({
    fetchImpl: () => Promise.resolve(new Response('bytes', { status: 200 })),
    createObjectUrl: () => 'blob:test',
    revokeObjectUrl: () => undefined
  });
}

function renderGallery(ui: ReactNode, initialEntry = '/browse', loader = testMediaLoader()) {
  return render(
    <QueryClientProvider client={createQueryClient()}>
      <ThemeProvider surface="gallery">
        <ToastProvider>
          <MemoryRouter initialEntries={[initialEntry]}>
            <SessionProvider>
              <MediaLoaderProvider loader={loader}>{ui}</MediaLoaderProvider>
            </SessionProvider>
          </MemoryRouter>
        </ToastProvider>
      </ThemeProvider>
    </QueryClientProvider>
  );
}

interface Recorded {
  method: string;
  path: string;
  cursor: string | null;
  body?: unknown;
}

let recorded: Recorded[] = [];

beforeEach(() => {
  recorded = [];
});

describe('双栏导航契约', () => {
  it('收藏查询只标记收藏入口，不同时标记全部作品', async () => {
    setFetchHandler((request) => {
      const url = new URL(request.url);
      if (url.pathname === '/api/v1/bootstrap') {
        return jsonResponse({
          ...BOOTSTRAP,
          availableCapabilities: [...BOOTSTRAP.availableCapabilities, 'files.browse'],
          effectiveCapabilities: [...BOOTSTRAP.effectiveCapabilities, 'files.browse']
        });
      }
      if (url.pathname === '/api/v1/sources') return jsonResponse({ sources: [] });
      if (url.pathname === '/api/v1/file-roots') return jsonResponse({ fileRoots: [] });
      return faultResponse('NOT_FOUND', 404);
    });

    renderGallery(<TopBar />, '/browse?fav=1');

    const navigation = await screen.findByRole('navigation', { name: '画廊导航' });
    expect(within(navigation).getByRole('link', { name: '收藏' })).toHaveAttribute('aria-current', 'page');
    expect(within(navigation).getByRole('link', { name: '全部作品' })).not.toHaveAttribute('aria-current');
  });
});

describe('数量协议的渲染', () => {
  it('lower_bound 显示成 1000+，绝不显示成精确的 1000 件', async () => {
    setFetchHandler((request) => {
      const url = new URL(request.url);
      if (url.pathname === '/api/v1/bootstrap') return jsonResponse(BOOTSTRAP);
      if (url.pathname === '/api/v1/works') {
        return jsonResponse(workList({ total: { mode: 'lower_bound', value: 1000, protocolVersion: 1 } }));
      }
      return faultResponse('NOT_FOUND', 404);
    });

    renderGallery(<WorkBrowser />);

    expect(await screen.findByText(/共 1000\+ 件作品/)).toBeInTheDocument();
    expect(screen.queryByText('共 1000 件作品')).not.toBeInTheDocument();
    expect(screen.getByText(/超出服务端统计预算/)).toBeInTheDocument();
  });
});

describe('服务端排序协议', () => {
  it('按规则下发的作品排序集渲染，并把选择作为 sort 查询参数发送', async () => {
    let requestedSort: string | null = null;
    setFetchHandler((request) => {
      const url = new URL(request.url);
      if (url.pathname === '/api/v1/bootstrap') return jsonResponse(BOOTSTRAP);
      if (url.pathname === '/api/v1/works') {
        requestedSort = url.searchParams.get('sort');
        return jsonResponse(workList());
      }
      return faultResponse('NOT_FOUND', 404);
    });

    renderGallery(
      <WorkBrowser
        presentation={{
          showInSidebar: true,
          showInManager: true,
          sort: { workDefault: 'date_desc', workOptions: ['date_desc', 'title_asc'] }
        }}
      />
    );

    expect(await screen.findByText('合成作品')).toBeInTheDocument();
    expect(requestedSort).toBe('date_desc');
    await userEvent.click(screen.getByLabelText('排序'));
    await userEvent.click(await screen.findByRole('option', { name: '标题升序' }));
    await waitFor(() => expect(requestedSort).toBe('title_asc'));
  });
});

describe('创作者浏览分页', () => {
  it('继承平台作者称谓，并以显式 Source 范围和游标连续加载', async () => {
    const requests: Array<Record<string, string | null>> = [];
    setFetchHandler((request) => {
      const url = new URL(request.url);
      if (url.pathname === '/api/v1/bootstrap') return jsonResponse(BOOTSTRAP);
      if (url.pathname === '/api/v1/sources') {
        return jsonResponse({
          sources: [
            {
              id: 'src_pixiv',
              libraryId: 'lib_main',
              displayName: 'pixiv source',
              presentation: {
                name: 'pixiv',
                authorLabel: '画师',
                showInSidebar: true,
                showInManager: true
              },
              readOnly: true,
              available: true,
              createdAt: '2026-07-01T00:00:00Z'
            }
          ]
        });
      }
      if (url.pathname === '/api/v1/creators') {
        requests.push({
          sourceId: url.searchParams.get('sourceId'),
          includeMerged: url.searchParams.get('includeMerged'),
          sort: url.searchParams.get('sort'),
          limit: url.searchParams.get('limit'),
          cursor: url.searchParams.get('cursor')
        });
        const cursor = url.searchParams.get('cursor');
        return jsonResponse({
          creators: [
            {
              id: cursor === null ? 'creator_a' : 'creator_b',
              name: cursor === null ? '画师甲' : '画师乙',
              effectiveId: cursor === null ? 'creator_a' : 'creator_b',
              sourceCount: 1,
              createdAt: '2026-07-01T00:00:00Z'
            }
          ],
          ...(cursor === null ? { nextCursor: 'next-page' } : {})
        });
      }
      return faultResponse('NOT_FOUND', 404);
    });

    renderGallery(<CreatorsPage />, '/creators?sourceId=src_pixiv');

    expect(await screen.findByRole('heading', { name: 'pixiv · 画师' })).toBeInTheDocument();
    const firstCreator = await screen.findByText('画师甲');
    expect(firstCreator.closest('a')).toHaveAttribute('href', '/creators/creator_a?sourceId=src_pixiv');
    await userEvent.click(screen.getByRole('button', { name: '加载更多画师' }));
    expect(await screen.findByText('画师乙')).toBeInTheDocument();
    expect(requests).toEqual([
      { sourceId: 'src_pixiv', includeMerged: 'false', sort: 'name_asc', limit: '48', cursor: null },
      {
        sourceId: 'src_pixiv',
        includeMerged: 'false',
        sort: 'name_asc',
        limit: '48',
        cursor: 'next-page'
      }
    ]);
  });
});

describe('缺失事实的空态', () => {
  it('没有创作者、没有发布时间时显示中文空态，不出现 undefined 或 Invalid Date', async () => {
    setFetchHandler((request) => {
      const url = new URL(request.url);
      if (url.pathname === '/api/v1/bootstrap') return jsonResponse(BOOTSTRAP);
      if (url.pathname === '/api/v1/works') {
        return jsonResponse(workList({ works: [work({ creator: '', publishedAt: null })] }));
      }
      return faultResponse('NOT_FOUND', 404);
    });

    renderGallery(<WorkBrowser />);

    expect(await screen.findByText('未标注创作者')).toBeInTheDocument();
    expect(screen.getByText(/未记录发布时间/)).toBeInTheDocument();
    expect(document.body.textContent).not.toContain('Invalid Date');
    expect(document.body.textContent).not.toContain('undefined');
  });
});

describe('publication 快照传播', () => {
  it('作品卡片的详情链接和封面正文都绑定列表 publication', async () => {
    let loadedURL = '';
    const loader = new MediaLoader({
      fetchImpl: (input) => {
        loadedURL = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
        return Promise.resolve(new Response('image-bytes', { status: 200 }));
      },
      createObjectUrl: () => 'blob:cover',
      revokeObjectUrl: () => undefined
    });
    setFetchHandler((request) => {
      const url = new URL(request.url);
      if (url.pathname === '/api/v1/bootstrap') return jsonResponse(BOOTSTRAP);
      if (url.pathname === '/api/v1/works') {
        return jsonResponse(workList({ works: [work({ coverMediaId: 'media_cover' })] }));
      }
      return faultResponse('NOT_FOUND', 404);
    });

    renderGallery(<WorkBrowser />, '/browse', loader);
    const title = await screen.findByText('合成作品');
    expect(title.closest('a')).toHaveAttribute('href', `/works/work_1?queryPublicationId=${PUBLICATION}`);
    await waitFor(() =>
      expect(loadedURL).toBe(`/api/v1/media/media_cover/content?queryPublicationId=${PUBLICATION}`)
    );
  });

  it('详情先绑定 Work，再用同一个 publication 读取媒体', async () => {
    const requested: Array<{ path: string; publication: string | null }> = [];
    setFetchHandler((request) => {
      const url = new URL(request.url);
      if (url.pathname === '/api/v1/bootstrap') return jsonResponse(BOOTSTRAP);
      if (url.pathname === '/api/v1/works/work_1') {
        requested.push({ path: url.pathname, publication: url.searchParams.get('queryPublicationId') });
        return jsonResponse(work());
      }
      if (url.pathname === '/api/v1/works/work_1/media') {
        requested.push({ path: url.pathname, publication: url.searchParams.get('queryPublicationId') });
        return jsonResponse({ queryPublicationId: PUBLICATION, media: [] });
      }
      if (url.pathname === '/api/v1/works/work_1/overlay') return jsonResponse(overlayState());
      return faultResponse('NOT_FOUND', 404);
    });

    renderGallery(
      <Routes>
        <Route path="/works/:workId" element={<WorkPage />} />
      </Routes>,
      `/works/work_1?queryPublicationId=${PUBLICATION}`
    );

    expect(await screen.findByText(/本页内容来自快照/)).toHaveTextContent(PUBLICATION);
    expect(screen.getByRole('heading', { name: '合成作品' })).toBeInTheDocument();
    expect(screen.queryByText('我的标题')).not.toBeInTheDocument();
    await waitFor(() => expect(requested).toHaveLength(2));
    expect(requested).toEqual([
      { path: '/api/v1/works/work_1', publication: PUBLICATION },
      { path: '/api/v1/works/work_1/media', publication: PUBLICATION }
    ]);
  });

  it('Work 已绑定但媒体快照失效时提供 current 恢复入口', async () => {
    setFetchHandler((request) => {
      const url = new URL(request.url);
      if (url.pathname === '/api/v1/bootstrap') return jsonResponse(BOOTSTRAP);
      if (url.pathname === '/api/v1/works/work_1') return jsonResponse(work());
      if (url.pathname === '/api/v1/works/work_1/media') return faultResponse('CURSOR_EXPIRED', 409);
      if (url.pathname === '/api/v1/works/work_1/overlay') return jsonResponse(overlayState());
      return faultResponse('NOT_FOUND', 404);
    });

    renderGallery(
      <Routes>
        <Route path="/works/:workId" element={<WorkPage />} />
      </Routes>,
      `/works/work_1?queryPublicationId=${PUBLICATION}`
    );

    expect(await screen.findByRole('link', { name: '打开当前版本' })).toHaveAttribute(
      'href',
      '/works/work_1'
    );
    expect(screen.getByRole('link', { name: '返回全部作品' })).toHaveAttribute('href', '/browse');
  });
});

describe('游标失效', () => {
  it('CURSOR_EXPIRED 丢弃游标从第一页重来，并向用户解释', async () => {
    setFetchHandler((request) => {
      const url = new URL(request.url);
      if (url.pathname === '/api/v1/bootstrap') return jsonResponse(BOOTSTRAP);
      if (url.pathname === '/api/v1/works') {
        const cursor = url.searchParams.get('cursor');
        recorded.push({ method: request.method, path: url.pathname, cursor });
        // 带游标的续页一律判定为快照过期。
        if (cursor !== null) return faultResponse('CURSOR_EXPIRED', 409);
        return jsonResponse(workList({ nextCursor: 'cursor-page-2' }));
      }
      return faultResponse('NOT_FOUND', 404);
    });

    renderGallery(<WorkBrowser />);
    await screen.findByText('合成作品');

    // 续页失败后必须出现两次"无游标"的第一页请求：一次是初始加载，一次是重来。
    await waitFor(() => {
      expect(recorded.filter((entry) => entry.cursor === null)).toHaveLength(2);
    });
    expect(recorded.some((entry) => entry.cursor === 'cursor-page-2')).toBe(true);
    expect(await screen.findByText(/已自动从第一页重新加载/)).toBeInTheDocument();

    // 通知未确认前不再自动续页，避免"续页→再次过期→再重来"的循环。
    const before = recorded.length;
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(recorded.length).toBe(before);
  });

  it('CURSOR_INVALID 不自动重试，由用户按下按钮才从第一页重来', async () => {
    setFetchHandler((request) => {
      const url = new URL(request.url);
      if (url.pathname === '/api/v1/bootstrap') return jsonResponse(BOOTSTRAP);
      if (url.pathname === '/api/v1/works') {
        const cursor = url.searchParams.get('cursor');
        recorded.push({ method: request.method, path: url.pathname, cursor });
        if (cursor !== null) return faultResponse('CURSOR_INVALID', 400);
        return jsonResponse(workList({ nextCursor: 'cursor-page-2' }));
      }
      return faultResponse('NOT_FOUND', 404);
    });

    renderGallery(<WorkBrowser />);
    await screen.findByText('合成作品');
    await screen.findByRole('button', { name: '从第一页重新开始' });

    // 不可重试的失败绝不自动重来：此时仍然只有最初那一次无游标请求。
    expect(recorded.filter((entry) => entry.cursor === null)).toHaveLength(1);

    await userEvent.click(screen.getByRole('button', { name: '从第一页重新开始' }));
    await waitFor(() => {
      expect(recorded.filter((entry) => entry.cursor === null)).toHaveLength(2);
    });
  });

  it('新搜索不继承旧查询的游标通知，并能独立续页', async () => {
    setFetchHandler((request) => {
      const url = new URL(request.url);
      if (url.pathname === '/api/v1/bootstrap') return jsonResponse(BOOTSTRAP);
      if (url.pathname === '/api/v1/works') {
        const query = url.searchParams.get('q') ?? '';
        const cursor = url.searchParams.get('cursor');
        if (query === '新的查询') {
          if (cursor === 'fresh-page-2') {
            return jsonResponse(workList({ works: [work({ id: 'work_fresh_2', title: '新查询第二页' })] }));
          }
          return jsonResponse(
            workList({
              works: [work({ id: 'work_fresh_1', title: '新查询第一页' })],
              nextCursor: 'fresh-page-2'
            })
          );
        }
        if (cursor !== null) return faultResponse('CURSOR_INVALID', 400);
        return jsonResponse(workList({ nextCursor: 'old-page-2' }));
      }
      return faultResponse('NOT_FOUND', 404);
    });

    renderGallery(<WorkBrowser />);
    await screen.findByRole('button', { name: '从第一页重新开始' });

    const search = screen.getByRole('searchbox', { name: '搜索作品' });
    await userEvent.clear(search);
    await userEvent.type(search, '新的查询');
    await userEvent.click(screen.getByRole('button', { name: '搜索' }));

    expect(await screen.findByText('新查询第一页')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '从第一页重新开始' })).not.toBeInTheDocument();
    expect(await screen.findByText('新查询第二页')).toBeInTheDocument();
  });
});

describe('迟到 HTTP 响应', () => {
  it('新筛选快照到达前保留旧结果并明确显示后台获取状态', async () => {
    let releaseNew: ((response: Response) => void) | undefined;
    setFetchHandler((request) => {
      const url = new URL(request.url);
      if (url.pathname === '/api/v1/bootstrap') return jsonResponse(BOOTSTRAP);
      if (url.pathname === '/api/v1/works') {
        if (url.searchParams.get('q') === '新筛选') {
          return new Promise<Response>((resolve) => {
            releaseNew = resolve;
          });
        }
        return jsonResponse(workList({ works: [work({ id: 'work_old', title: '旧快照作品' })] }));
      }
      return faultResponse('NOT_FOUND', 404);
    });

    renderGallery(<WorkBrowser />);
    expect(await screen.findByText('旧快照作品')).toBeInTheDocument();

    const search = screen.getByRole('searchbox', { name: '搜索作品' });
    await userEvent.type(search, '新筛选');
    await userEvent.click(screen.getByRole('button', { name: '搜索' }));
    await waitFor(() => expect(releaseNew).toBeDefined());

    expect(screen.getByText('旧快照作品')).toBeInTheDocument();
    expect(screen.getByText('正在获取结果')).toBeInTheDocument();
    const replacingGrid = screen.getByRole('list', { hidden: true });
    expect(replacingGrid).toHaveAttribute('inert');
    expect(replacingGrid).toHaveAttribute('aria-hidden', 'true');

    await act(async () => {
      releaseNew?.(jsonResponse(workList({ works: [work({ id: 'work_new', title: '新快照作品' })] })));
      await Promise.resolve();
    });
    expect(await screen.findByText('新快照作品')).toBeInTheDocument();
    expect(screen.queryByText('旧快照作品')).not.toBeInTheDocument();
  });

  it('切换 Source 浏览范围时不复用上一范围的旧视觉', async () => {
    let releaseSourceB: ((response: Response) => void) | undefined;
    setFetchHandler((request) => {
      const url = new URL(request.url);
      if (url.pathname === '/api/v1/bootstrap') return jsonResponse(BOOTSTRAP);
      if (url.pathname === '/api/v1/works') {
        if (url.searchParams.get('sourceId') === 'source_b') {
          return new Promise<Response>((resolve) => {
            releaseSourceB = resolve;
          });
        }
        return jsonResponse(workList({ works: [work({ id: 'work_a', title: '来源甲作品' })] }));
      }
      return faultResponse('NOT_FOUND', 404);
    });

    function ScopedBrowser() {
      const [sourceId, setSourceId] = useState('source_a');
      return (
        <>
          <button type="button" onClick={() => setSourceId('source_b')}>
            切换来源
          </button>
          <WorkBrowser scope={{ sourceId }} />
        </>
      );
    }

    renderGallery(<ScopedBrowser />);
    expect(await screen.findByText('来源甲作品')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '切换来源' }));
    await waitFor(() => expect(releaseSourceB).toBeDefined());

    expect(screen.queryByText('来源甲作品')).not.toBeInTheDocument();
    expect(screen.getByText('正在加载作品')).toBeInTheDocument();

    await act(async () => {
      releaseSourceB?.(jsonResponse(workList({ works: [work({ id: 'work_b', title: '来源乙作品' })] })));
      await Promise.resolve();
    });
    expect(await screen.findByText('来源乙作品')).toBeInTheDocument();
  });

  it('旧搜索的迟到分页响应不能追加到较新的搜索结果', async () => {
    let releaseOldPage: ((response: Response) => void) | undefined;
    let oldPageSignal: AbortSignal | undefined;
    setFetchHandler((request) => {
      const url = new URL(request.url);
      if (url.pathname === '/api/v1/bootstrap') return jsonResponse(BOOTSTRAP);
      if (url.pathname === '/api/v1/works') {
        const query = url.searchParams.get('q') ?? '';
        const cursor = url.searchParams.get('cursor');
        if (query === '最新查询') {
          return jsonResponse(workList({ works: [work({ id: 'work_fresh', title: '最新查询结果' })] }));
        }
        if (cursor === 'old-page-2') {
          oldPageSignal = request.signal;
          return new Promise<Response>((resolve) => {
            releaseOldPage = resolve;
          });
        }
        return jsonResponse(
          workList({ works: [work({ id: 'work_old_1', title: '旧查询第一页' })], nextCursor: 'old-page-2' })
        );
      }
      return faultResponse('NOT_FOUND', 404);
    });

    renderGallery(<WorkBrowser />);
    expect(await screen.findByText('旧查询第一页')).toBeInTheDocument();
    await waitFor(() => expect(releaseOldPage).toBeDefined());

    const search = screen.getByRole('searchbox', { name: '搜索作品' });
    await userEvent.clear(search);
    await userEvent.type(search, '最新查询');
    await userEvent.click(screen.getByRole('button', { name: '搜索' }));

    expect(await screen.findByText('最新查询结果')).toBeInTheDocument();
    await waitFor(() => expect(oldPageSignal?.aborted).toBe(true));
    await act(async () => {
      releaseOldPage?.(
        jsonResponse(workList({ works: [work({ id: 'work_old_2', title: '迟到的旧查询第二页' })] }))
      );
      await Promise.resolve();
    });
    expect(screen.queryByText('迟到的旧查询第二页')).not.toBeInTheDocument();
    expect(screen.getByText('最新查询结果')).toBeInTheDocument();
  });

  it('旧路由详情响应即使在取消后返回，也不能覆盖新路由', async () => {
    let releaseOldWork: ((response: Response) => void) | undefined;
    let oldWorkSignal: AbortSignal | undefined;
    setFetchHandler((request) => {
      const url = new URL(request.url);
      if (url.pathname === '/api/v1/bootstrap') return jsonResponse(BOOTSTRAP);
      if (url.pathname === '/api/v1/works/work_1') {
        oldWorkSignal = request.signal;
        return new Promise<Response>((resolve) => {
          releaseOldWork = resolve;
        });
      }
      if (url.pathname === '/api/v1/works/work_2') {
        return jsonResponse(work({ id: 'work_2', title: '较新的路由作品' }));
      }
      if (url.pathname === '/api/v1/works/work_2/media') {
        return jsonResponse({ queryPublicationId: PUBLICATION, media: [] });
      }
      return faultResponse('NOT_FOUND', 404);
    });

    renderGallery(
      <>
        <nav>
          <Link to={`/works/work_2?queryPublicationId=${PUBLICATION}`}>切换到新作品</Link>
        </nav>
        <Routes>
          <Route path="/works/:workId" element={<WorkPage />} />
        </Routes>
      </>,
      `/works/work_1?queryPublicationId=${PUBLICATION}`
    );
    await waitFor(() => expect(releaseOldWork).toBeDefined());

    await userEvent.click(screen.getByRole('link', { name: '切换到新作品' }));
    expect(await screen.findByRole('heading', { name: '较新的路由作品' })).toBeInTheDocument();
    await waitFor(() => expect(oldWorkSignal?.aborted).toBe(true));
    await act(async () => {
      releaseOldWork?.(jsonResponse(work({ id: 'work_1', title: '迟到的旧路由作品' })));
      await Promise.resolve();
    });
    expect(screen.queryByRole('heading', { name: '迟到的旧路由作品' })).not.toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '较新的路由作品' })).toBeInTheDocument();
  });

  it('旧搜索的迟到错误即使无视取消，也不能覆盖较新的搜索结果', async () => {
    let releaseOldError: ((response: Response) => void) | undefined;
    let oldSearchSignal: AbortSignal | undefined;
    setFetchHandler((request) => {
      const url = new URL(request.url);
      if (url.pathname === '/api/v1/bootstrap') return jsonResponse(BOOTSTRAP);
      if (url.pathname === '/api/v1/works') {
        const query = url.searchParams.get('q') ?? '';
        if (query === '会失败的旧查询') {
          oldSearchSignal = request.signal;
          return new Promise<Response>((resolve) => {
            releaseOldError = resolve;
          });
        }
        if (query === '最新查询') {
          return jsonResponse(workList({ works: [work({ id: 'work_fresh', title: '最新查询结果' })] }));
        }
        return jsonResponse(workList({ works: [work({ id: 'work_initial', title: '初始查询结果' })] }));
      }
      return faultResponse('NOT_FOUND', 404);
    });

    renderGallery(<WorkBrowser />);
    expect(await screen.findByText('初始查询结果')).toBeInTheDocument();
    const search = screen.getByRole('searchbox', { name: '搜索作品' });
    await userEvent.clear(search);
    await userEvent.type(search, '会失败的旧查询');
    await userEvent.click(screen.getByRole('button', { name: '搜索' }));
    await waitFor(() => expect(releaseOldError).toBeDefined());

    await userEvent.clear(search);
    await userEvent.type(search, '最新查询');
    await userEvent.click(screen.getByRole('button', { name: '搜索' }));
    expect(await screen.findByText('最新查询结果')).toBeInTheDocument();
    await waitFor(() => expect(oldSearchSignal?.aborted).toBe(true));

    await act(async () => {
      // 测试传输层故意无视 AbortSignal，把旧查询的结构化错误继续交还调用方。
      releaseOldError?.(faultResponse('FORBIDDEN', 403));
      await Promise.resolve();
    });
    expect(screen.queryByText('FORBIDDEN')).not.toBeInTheDocument();
    expect(screen.queryByText(/当前账户没有执行此操作的权限/)).not.toBeInTheDocument();
    expect(screen.getByText('最新查询结果')).toBeInTheDocument();
  });
});

describe('媒体读取的降级表现', () => {
  it('图片解码完成前保留同尺寸占位，完成后只在媒体层内部显现', async () => {
    render(
      <QueryClientProvider client={createQueryClient()}>
        <ThemeProvider surface="gallery">
          <MediaLoaderProvider loader={testMediaLoader()}>
            <MediaImage eager src="/api/v1/media/m1/content" alt="封面" />
          </MediaLoaderProvider>
        </ThemeProvider>
      </QueryClientProvider>
    );

    const image = await screen.findByRole('img', { name: '封面' });
    const container = image.closest('.gal-media');
    expect(container).toHaveAttribute('aria-busy', 'true');
    expect(image).not.toHaveClass('gal-media__img--ready');

    fireEvent.load(image);

    expect(image).toHaveClass('gal-media__img--ready');
    expect(container).not.toHaveAttribute('aria-busy');
    expect(container?.querySelector('.gal-media__placeholder')).toHaveClass('gal-media__placeholder--hidden');
  });

  it('MEDIA_READ_BUSY 用尽重试后显示可重试的占位，而不是碎图', async () => {
    setFetchHandler((request) => {
      const url = new URL(request.url);
      if (url.pathname === '/api/v1/bootstrap') return jsonResponse(BOOTSTRAP);
      return faultResponse('NOT_FOUND', 404);
    });
    const busyLoader = new MediaLoader({
      maxRetries: 1,
      delay: () => Promise.resolve(),
      createObjectUrl: () => 'blob:test',
      revokeObjectUrl: () => undefined,
      fetchImpl: () =>
        Promise.resolve(
          new Response(
            JSON.stringify({ error: { code: 'MEDIA_READ_BUSY', retryable: true, correlationId: 'c' } }),
            { status: 503, headers: { 'Content-Type': 'application/json' } }
          )
        )
    });

    render(
      <QueryClientProvider client={createQueryClient()}>
        <ThemeProvider surface="gallery">
          <MediaLoaderProvider loader={busyLoader}>
            <MediaImage eager src="/api/v1/media/m1/content" alt="封面" />
          </MediaLoaderProvider>
        </ThemeProvider>
      </QueryClientProvider>
    );

    expect(await screen.findByText('媒体读取通道已满')).toBeInTheDocument();
    expect(screen.getByText('MEDIA_READ_BUSY')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument();
    // 关键：不能留下一个加载失败的 <img>，那在浏览器里就是碎图图标。
    expect(screen.queryByRole('img')).not.toBeInTheDocument();
  });
});

describe('收藏与 live overlay 调和', () => {
  it('切换收藏时先读全量再整体 PUT，并以服务端返回的 live 值显示', async () => {
    let stored = overlayState();
    setFetchHandler(async (request) => {
      const url = new URL(request.url);
      if (url.pathname === '/api/v1/bootstrap') return jsonResponse(BOOTSTRAP);
      if (url.pathname === '/api/v1/works') return jsonResponse(workList());
      if (url.pathname === '/api/v1/works/work_1/overlay') {
        if (request.method === 'PUT') {
          const body: unknown = await request.json();
          recorded.push({ method: 'PUT', path: url.pathname, cursor: null, body });
          const patch = body as { favorite: boolean };
          stored = overlayState({ favorite: patch.favorite, projectionStatus: 'pending' });
          return jsonResponse(stored);
        }
        recorded.push({ method: 'GET', path: url.pathname, cursor: null });
        return jsonResponse(stored);
      }
      return faultResponse('NOT_FOUND', 404);
    });

    renderGallery(<WorkBrowser />);
    const card = (await screen.findByText('合成作品')).closest('article');
    if (!card) throw new Error('作品卡片没有渲染出来');
    const toggle = within(card).getByRole('button', { name: '收藏' });

    await userEvent.click(toggle);

    // 列表快照的 favorite 是过时值，写入必须基于一次真实的 GET。
    await waitFor(() => {
      expect(recorded.filter((entry) => entry.method === 'PUT')).toHaveLength(1);
    });
    const put = recorded.find((entry) => entry.method === 'PUT');
    expect(recorded.findIndex((entry) => entry.method === 'GET')).toBeLessThan(
      recorded.findIndex((entry) => entry.method === 'PUT')
    );

    // PUT 是整体替换：缺任何一个必填字段都会清掉对应的用户事实。
    expect(put?.body).toMatchObject({
      titleOverride: '我的标题',
      manualTags: ['我的标签'],
      hidden: false,
      favorite: true,
      progress: 0.25
    });

    // 调和：卡片显示服务端返回的 live 值，而不是列表快照里的 false。
    expect(await within(card).findByRole('button', { name: '取消收藏' })).toBeInTheDocument();
  });
});

describe('CustomCover 写后快照', () => {
  it('路由复用时不把前一作品的未保存草稿串入新作品', async () => {
    setFetchHandler((request) => {
      const url = new URL(request.url);
      if (url.pathname === '/api/v1/bootstrap') return jsonResponse(BOOTSTRAP);
      const workMatch = /^\/api\/v1\/works\/(work_[12])$/.exec(url.pathname);
      if (workMatch) {
        const id = workMatch[1];
        return jsonResponse(work({ id, title: id === 'work_1' ? '作品 A' : '作品 B' }));
      }
      const mediaMatch = /^\/api\/v1\/works\/(work_[12])\/media$/.exec(url.pathname);
      if (mediaMatch) return jsonResponse({ queryPublicationId: PUBLICATION, media: [] });
      const overlayMatch = /^\/api\/v1\/works\/(work_[12])\/overlay$/.exec(url.pathname);
      if (overlayMatch) {
        const id = overlayMatch[1];
        return jsonResponse(
          overlayState({ workId: id, titleOverride: id === 'work_1' ? '覆盖 A' : '覆盖 B' })
        );
      }
      return faultResponse('NOT_FOUND', 404);
    });

    renderGallery(
      <>
        <nav>
          <Link to={`/works/work_1?queryPublicationId=${PUBLICATION}`}>打开作品 A</Link>
          <Link to={`/works/work_2?queryPublicationId=${PUBLICATION}`}>打开作品 B</Link>
        </nav>
        <Routes>
          <Route path="/works/:workId" element={<WorkPage />} />
        </Routes>
      </>,
      `/works/work_2?queryPublicationId=${PUBLICATION}`
    );

    await screen.findByRole('heading', { name: '作品 B' });
    expect(await screen.findByRole('textbox', { name: '标题覆盖' })).toHaveValue('覆盖 B');
    await userEvent.click(screen.getByRole('link', { name: '打开作品 A' }));
    await screen.findByRole('heading', { name: '作品 A' });
    const title = await screen.findByRole('textbox', { name: '标题覆盖' });
    await userEvent.clear(title);
    await userEvent.type(title, '未保存 A');

    await userEvent.click(screen.getByRole('link', { name: '打开作品 B' }));
    await screen.findByRole('heading', { name: '作品 B' });
    expect(await screen.findByRole('textbox', { name: '标题覆盖' })).toHaveValue('覆盖 B');
  });

  it('选择封面时整体 PUT，并保留其余用户事实', async () => {
    let stored = overlayState();
    const media = [publishedMedia('media_1', 0), publishedMedia('media_2', 1)];
    setFetchHandler(async (request) => {
      const url = new URL(request.url);
      if (url.pathname === '/api/v1/bootstrap') return jsonResponse(BOOTSTRAP);
      if (url.pathname === '/api/v1/works/work_1') return jsonResponse(work());
      if (url.pathname === '/api/v1/works/work_1/media') {
        return jsonResponse({ queryPublicationId: PUBLICATION, media });
      }
      if (url.pathname === '/api/v1/works/work_1/overlay') {
        if (request.method === 'PUT') {
          const body: unknown = await request.json();
          recorded.push({ method: 'PUT', path: url.pathname, cursor: null, body });
          stored = overlayState({
            customCoverMediaId: 'media_2',
            projectionStatus: 'pending',
            projectionJobId: 'job_overlay_1'
          });
          return jsonResponse(stored);
        }
        return jsonResponse(stored);
      }
      return faultResponse('NOT_FOUND', 404);
    });

    renderGallery(
      <Routes>
        <Route path="/works/:workId" element={<WorkPage />} />
      </Routes>,
      `/works/work_1?queryPublicationId=${PUBLICATION}`
    );

    await screen.findByRole('heading', { name: '合成作品' });
    await userEvent.click(await screen.findByRole('button', { name: /自定义封面/ }));
    await userEvent.click(await screen.findByRole('option', { name: '第 2 项 · image/png' }));
    await userEvent.click(screen.getByRole('button', { name: '保存' }));

    await waitFor(() => expect(recorded.filter((entry) => entry.method === 'PUT')).toHaveLength(1));
    expect(recorded.find((entry) => entry.method === 'PUT')?.body).toEqual({
      titleOverride: '我的标题',
      manualTags: ['我的标签'],
      hidden: false,
      favorite: false,
      progress: 0.25,
      customCoverMediaId: 'media_2'
    });
    expect(await screen.findByText(/重新投影排队中/)).toBeInTheDocument();
  });

  it('pending 状态自行收敛，并提供显式打开新 publication 的入口', async () => {
    let reads = 0;
    const nextPublication = 'qpub_after_overlay';
    setFetchHandler((request) => {
      const url = new URL(request.url);
      if (url.pathname === '/api/v1/bootstrap') return jsonResponse(BOOTSTRAP);
      if (url.pathname === '/api/v1/works/work_1') return jsonResponse(work());
      if (url.pathname === '/api/v1/works/work_1/media') {
        return jsonResponse({ queryPublicationId: PUBLICATION, media: [publishedMedia('media_1', 0)] });
      }
      if (url.pathname === '/api/v1/works/work_1/overlay') {
        reads += 1;
        return jsonResponse(
          reads === 1
            ? overlayState({ projectionStatus: 'pending', projectionJobId: 'job_overlay_1' })
            : overlayState({ publishedQueryPublicationId: nextPublication })
        );
      }
      return faultResponse('NOT_FOUND', 404);
    });

    renderGallery(
      <Routes>
        <Route path="/works/:workId" element={<WorkPage />} />
      </Routes>,
      `/works/work_1?queryPublicationId=${PUBLICATION}`
    );

    expect(await screen.findByText(/重新投影排队中/)).toBeInTheDocument();
    const title = screen.getByRole('textbox', { name: '标题覆盖' });
    await userEvent.clear(title);
    await userEvent.type(title, '尚未保存的标题');
    const link = await screen.findByRole('link', { name: '打开已投影版本' }, { timeout: 2_500 });
    expect(reads).toBeGreaterThanOrEqual(2);
    expect(title).toHaveValue('尚未保存的标题');
    expect(screen.getByRole('status')).toHaveTextContent('已生成新快照，可通过下方链接打开');
    expect(link).toHaveAttribute('href', `/works/work_1?queryPublicationId=${nextPublication}`);
  });

  it('失效的当前选择有明确提示并仍可清除', async () => {
    setFetchHandler((request) => {
      const url = new URL(request.url);
      if (url.pathname === '/api/v1/bootstrap') return jsonResponse(BOOTSTRAP);
      if (url.pathname === '/api/v1/works/work_1') return jsonResponse(work());
      if (url.pathname === '/api/v1/works/work_1/media') {
        return jsonResponse({ queryPublicationId: PUBLICATION, media: [publishedMedia('media_1', 0)] });
      }
      if (url.pathname === '/api/v1/works/work_1/overlay') {
        return jsonResponse(overlayState({ customCoverMediaId: 'media_missing' }));
      }
      return faultResponse('NOT_FOUND', 404);
    });

    renderGallery(
      <Routes>
        <Route path="/works/:workId" element={<WorkPage />} />
      </Routes>,
      `/works/work_1?queryPublicationId=${PUBLICATION}`
    );

    expect(await screen.findByText(/当前选择已经不在本快照的媒体中/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: /自定义封面/ }));
    expect(
      await screen.findByRole('option', { name: '当前自定义封面已失效（请选择替代项或清除）' })
    ).toHaveAttribute('aria-disabled', 'true');
    expect(screen.getByRole('option', { name: '不指定（使用规则解析的封面）' })).toBeInTheDocument();
  });

  it('历史快照缺少当前有效封面时不误报失效', async () => {
    const nextPublication = 'qpub_after_overlay';
    setFetchHandler((request) => {
      const url = new URL(request.url);
      if (url.pathname === '/api/v1/bootstrap') return jsonResponse(BOOTSTRAP);
      if (url.pathname === '/api/v1/works/work_1') return jsonResponse(work());
      if (url.pathname === '/api/v1/works/work_1/media') {
        return jsonResponse({ queryPublicationId: PUBLICATION, media: [publishedMedia('media_1', 0)] });
      }
      if (url.pathname === '/api/v1/works/work_1/overlay') {
        return jsonResponse(
          overlayState({
            customCoverMediaId: 'media_new_snapshot',
            publishedQueryPublicationId: nextPublication
          })
        );
      }
      return faultResponse('NOT_FOUND', 404);
    });

    renderGallery(
      <Routes>
        <Route path="/works/:workId" element={<WorkPage />} />
      </Routes>,
      `/works/work_1?queryPublicationId=${PUBLICATION}`
    );

    expect(await screen.findByText(/当前选择属于另一个已投影快照/)).toBeInTheDocument();
    expect(screen.queryByText(/展示已回退到规则封面/)).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: /自定义封面/ }));
    expect(
      await screen.findByRole('option', { name: '当前自定义封面属于另一快照（可打开新版本查看）' })
    ).toHaveAttribute('aria-disabled', 'true');
    await userEvent.keyboard('{Escape}');
    expect(screen.getByRole('link', { name: '打开已投影版本' })).toHaveAttribute(
      'href',
      `/works/work_1?queryPublicationId=${nextPublication}`
    );
  });

  it('媒体列表读取失败时不把同快照 CustomCover 误报为失效', async () => {
    setFetchHandler((request) => {
      const url = new URL(request.url);
      if (url.pathname === '/api/v1/bootstrap') return jsonResponse(BOOTSTRAP);
      if (url.pathname === '/api/v1/works/work_1') return jsonResponse(work());
      if (url.pathname === '/api/v1/works/work_1/media') return faultResponse('FORBIDDEN', 403);
      if (url.pathname === '/api/v1/works/work_1/overlay') {
        return jsonResponse(overlayState({ customCoverMediaId: 'media_valid_but_unavailable' }));
      }
      return faultResponse('NOT_FOUND', 404);
    });

    renderGallery(
      <Routes>
        <Route path="/works/:workId" element={<WorkPage />} />
      </Routes>,
      `/works/work_1?queryPublicationId=${PUBLICATION}`
    );

    expect(await screen.findByText(/媒体列表尚未加载/)).toBeInTheDocument();
    expect(screen.queryByText(/展示已回退到规则封面/)).not.toBeInTheDocument();
    expect(screen.queryByText(/当前自定义封面已失效/)).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: /自定义封面/ })).toBeDisabled();
  });
});
