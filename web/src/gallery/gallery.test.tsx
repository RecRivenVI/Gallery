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
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it } from 'vitest';
import type { ReactNode } from 'react';
import { faultResponse, jsonResponse, setFetchHandler } from '../../tests/http';
import { ToastProvider } from '../design';
import { createQueryClient } from '../shared/query';
import { SessionProvider } from '../shared/session';
import { ThemeProvider } from '../shared/theme';
import { WorkBrowser } from './components/browser';
import { MediaImage, MediaLoaderProvider } from './components/media';
import { MediaLoader } from './media';
import { WorkPage } from './pages/work';

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
});

describe('媒体读取的降级表现', () => {
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
