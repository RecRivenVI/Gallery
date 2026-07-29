/*
 * 画廊的数据获取层。
 *
 * 所有列表都来自服务端快照：客户端不本地重排、不跨快照拼接、不把实时事件当成数据源。
 * queryKey 的第一段一律取自 `shared/query.ts` 的 `SNAPSHOT_QUERY_PREFIXES`，否则实时
 * 重连时不会自动重取。
 *
 * 两条最容易出错的契约，在这里集中处理，页面不需要各自记住：
 *
 * 1. **游标失效**。`CURSOR_EXPIRED` 可重试但游标本身已经不可能再用，必须丢弃游标从第一页
 *    重来；`CURSOR_INVALID` 不可重试，同样只能从头来，但不得自动重试。两者都要告诉用户，
 *    因为“列表突然回到顶部”必须有解释。
 * 2. **Overlay 是整体替换**。`PUT /works/{id}/overlay` 要求带齐 titleOverride、manualTags、
 *    hidden、favorite、progress；只想改收藏也必须先 GET 再整体 PUT，否则会把别的用户事实
 *    清空。契约**没有** If-Match/ETag，所以保存后一律以服务端响应为准并重新读取，
 *    不假装本地值就是最终值。
 */

import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult
} from '@tanstack/react-query';
import { useCallback, useEffect, useState } from 'react';
import { api, expectData } from '../api/client';
import { isRetryable } from '../shared/errors';
import { useCsrfHeaders } from '../shared/session';
import {
  classifyCursorFailure,
  type Creator,
  type CreatorListResponse,
  type CursorRecovery,
  type FileRoot,
  type FileRootEntryListResponse,
  type Library,
  type MediaListResponse,
  type PublishedWork,
  type Source,
  type Total,
  type WorkListResponse,
  type WorkOverlayPutRequest,
  type WorkOverlayState
} from './contracts';

/* ————————————————————————————— 目录导航 ————————————————————————————— */

export function useSources() {
  return useQuery({
    queryKey: ['sources'],
    queryFn: async ({ signal }): Promise<Source[]> =>
      expectData(await api.GET('/api/v1/sources', { signal })).sources
  });
}

export function useLibraries() {
  return useQuery({
    queryKey: ['libraries'],
    queryFn: async ({ signal }): Promise<Library[]> =>
      expectData(await api.GET('/api/v1/libraries', { signal })).libraries
  });
}

export type CreatorSort = 'name_asc' | 'name_desc';

export function useCreators(sourceId: string | undefined, sort: CreatorSort, enabled = true) {
  return useInfiniteQuery({
    enabled,
    queryKey: ['creators', 'browse', sourceId ?? '', sort],
    initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam, signal }): Promise<CreatorListResponse> =>
      expectData(
        await api.GET('/api/v1/creators', {
          params: {
            query: {
              includeMerged: false,
              sort,
              limit: 48,
              ...(sourceId === undefined || sourceId === '' ? {} : { sourceId }),
              ...(pageParam === undefined ? {} : { cursor: pageParam })
            }
          },
          signal
        })
      ),
    getNextPageParam: (lastPage) => lastPage.nextCursor
  });
}

export function useCreator(creatorId: string) {
  return useQuery({
    queryKey: ['creators', 'detail', creatorId],
    queryFn: async ({ signal }): Promise<Creator> =>
      expectData(await api.GET('/api/v1/creators/{creatorId}', { params: { path: { creatorId } }, signal }))
        .creator
  });
}

export function useFileRoots(enabled: boolean) {
  return useQuery({
    enabled,
    // 文件根不是 Catalog 快照，实时事件也不描述文件系统变化，因此刻意不使用快照前缀。
    queryKey: ['files', 'roots'],
    queryFn: async ({ signal }): Promise<FileRoot[]> =>
      expectData(await api.GET('/api/v1/file-roots', { signal })).fileRoots
  });
}

/**
 * 目录分页。
 *
 * **不保证可重复读**：文件系统是实时的，`nextAfter` 只是“从这个锚点之后继续”，
 * 不是签名游标。因此这里不提供总数，也不把多页结果当成一致快照。
 */
export function useFileEntries(rootId: string, path: string) {
  return useInfiniteQuery({
    queryKey: ['files', 'entries', rootId, path],
    initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam, signal }): Promise<FileRootEntryListResponse> =>
      expectData(
        await api.GET('/api/v1/file-roots/{rootId}/entries', {
          params: {
            path: { rootId },
            query: {
              ...(path === '' ? {} : { path }),
              ...(pageParam === undefined ? {} : { after: pageParam })
            }
          },
          signal
        })
      ),
    getNextPageParam: (last) => last.nextAfter ?? undefined
  });
}

/* ————————————————————————————— 作品列表 ————————————————————————————— */

export interface WorkQueryParams {
  q?: string;
  tag?: string;
  sourceId?: string;
  libraryId?: string;
  /** 结构化过滤 AST 的 JSON 编码，由 contracts.ts 的 buildFilterAst 构造。 */
  filter?: string;
  /** 服务端权威排序名；客户端只选择，不得重排已经分页的结果。 */
  sort:
    | 'title_asc'
    | 'title_desc'
    | 'name_asc'
    | 'name_desc'
    | 'date_asc'
    | 'date_desc'
    | 'progress_asc'
    | 'progress_desc';
  limit?: number;
}

export interface CursorNotice {
  kind: Exclude<CursorRecovery, 'none'>;
}

interface StoredCursorNotice extends CursorNotice {
  /**
   * 通知只属于产生它的那组查询条件。用户已经换了搜索、排序或范围时，旧游标错误即使
   * 稍后才进入 React effect，也不能继续阻止新查询自动续页。
   */
  querySignature: string;
}

export interface WorkListView {
  works: PublishedWork[];
  pages: WorkListResponse[];
  /** 首页返回的数量协议。`lower_bound` 必须按下限渲染。 */
  total: Total | undefined;
  queryPublicationId: string | undefined;
  isPending: boolean;
  isFetching: boolean;
  isFetchingNextPage: boolean;
  hasNextPage: boolean;
  error: unknown;
  /** 游标失效通知。`restart-manual` 时必须由用户点“从第一页重新开始”。 */
  cursorNotice: CursorNotice | undefined;
  fetchNextPage: () => void;
  restart: () => void;
  /** 确认已读通知。在此之前不自动续页，避免"续页→再次过期→再重来"的循环。 */
  dismissNotice: () => void;
  refetch: () => void;
}

function workQuery(params: WorkQueryParams, cursor: string | undefined) {
  return {
    ...(params.q === undefined || params.q === '' ? {} : { q: params.q }),
    ...(params.tag === undefined || params.tag === '' ? {} : { tag: params.tag }),
    ...(params.sourceId === undefined || params.sourceId === '' ? {} : { sourceId: params.sourceId }),
    ...(params.libraryId === undefined || params.libraryId === '' ? {} : { libraryId: params.libraryId }),
    ...(params.filter === undefined || params.filter === '' ? {} : { filter: params.filter }),
    ...(params.limit === undefined ? {} : { limit: params.limit }),
    ...(cursor === undefined ? {} : { cursor }),
    sort: params.sort
  };
}

export function useWorkList(params: WorkQueryParams): WorkListView {
  // generation 参与 queryKey：游标失效后 +1，等于丢弃整个已加载序列从第一页重来。
  const [generation, setGeneration] = useState(0);
  const [storedNotice, setStoredNotice] = useState<StoredCursorNotice | undefined>(undefined);
  const queryScope = workQuery(params, undefined);
  // workQuery 按固定字段顺序构造纯 JSON；签名只用于把局部 UI 通知绑定到同一组查询条件，
  // 服务端事实身份仍由 query_publication_id 与签名游标决定。
  const querySignature = JSON.stringify(queryScope);
  const notice = storedNotice?.querySignature === querySignature ? storedNotice : undefined;

  const query = useInfiniteQuery({
    queryKey: ['works', 'list', queryScope, generation],
    initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam, signal }): Promise<WorkListResponse> =>
      expectData(await api.GET('/api/v1/works', { params: { query: workQuery(params, pageParam) }, signal })),
    getNextPageParam: (last) => last.nextCursor ?? undefined,
    // 游标类失败不能靠重试解决：同一个游标再发一次仍然是同一个结果，只会放大限流压力。
    retry: (failureCount, error) =>
      classifyCursorFailure(error) === 'none' && isRetryable(error) && failureCount < 2
  });

  const { fetchNextPage, refetch } = query;
  const nextPage = useCallback(() => {
    void fetchNextPage();
  }, [fetchNextPage]);
  const reload = useCallback(() => {
    void refetch();
  }, [refetch]);

  const restart = useCallback(() => {
    setStoredNotice(undefined);
    setGeneration((value) => value + 1);
  }, []);

  const dismissNotice = useCallback(() => {
    setStoredNotice(undefined);
  }, []);

  const failure = query.error;
  useEffect(() => {
    const recovery = classifyCursorFailure(failure);
    if (recovery === 'none') return;
    setStoredNotice({ kind: recovery, querySignature });
    // CURSOR_EXPIRED 服务端声明可重试，且“重来”本身是有意义的动作：自动从第一页重新加载。
    // CURSOR_INVALID 不可重试，只登记通知，等用户按下按钮。
    if (recovery === 'restart-auto') setGeneration((value) => value + 1);
  }, [failure, querySignature]);

  const pages = query.data?.pages ?? [];
  const first = pages[0];
  return {
    works: pages.flatMap((page) => page.works),
    pages,
    total: first?.total,
    queryPublicationId: first?.queryPublicationId,
    isPending: query.isPending,
    isFetching: query.isFetching,
    isFetchingNextPage: query.isFetchingNextPage,
    hasNextPage: query.hasNextPage,
    // 游标失败由 cursorNotice 表达，不再重复渲染成页面级错误；没有失败时统一成 undefined
    // （TanStack 用 null 表示"没有错误"，直接透出去会让 `!== undefined` 的判断永远成立）。
    error: query.error === null || classifyCursorFailure(query.error) !== 'none' ? undefined : query.error,
    cursorNotice: notice,
    fetchNextPage: nextPage,
    restart,
    dismissNotice,
    refetch: reload
  };
}

/* ————————————————————————————— 作品详情与媒体 ————————————————————————————— */

export function useWork(workId: string, queryPublicationId?: string) {
  return useQuery({
    queryKey: ['works', 'detail', workId, queryPublicationId],
    queryFn: async ({ signal }) =>
      expectData(
        await api.GET('/api/v1/works/{workId}', {
          params: {
            path: { workId },
            query: queryPublicationId === undefined ? {} : { queryPublicationId }
          },
          signal
        })
      ),
    // 显式历史 publication 已失效时，重复同一个 ID 不会恢复；立即交给页面提供 current 导航。
    retry: (failureCount, error) =>
      classifyCursorFailure(error) === 'none' && isRetryable(error) && failureCount < 2
  });
}

/**
 * 作品的媒体列表。
 *
 * queryPublicationId 来自同一次 Work 读取；媒体、正文与后续操作必须继续绑定这个快照。
 */
export function useWorkMedia(workId: string, queryPublicationId?: string, enabled = true) {
  return useQuery({
    enabled,
    queryKey: ['media', 'work', workId, queryPublicationId],
    queryFn: async ({ signal }): Promise<MediaListResponse> =>
      expectData(
        await api.GET('/api/v1/works/{workId}/media', {
          params: {
            path: { workId },
            query: queryPublicationId === undefined ? {} : { queryPublicationId }
          },
          signal
        })
      ),
    retry: (failureCount, error) =>
      classifyCursorFailure(error) === 'none' && isRetryable(error) && failureCount < 2
  });
}

/* ————————————————————————————— Overlay ————————————————————————————— */

export function overlayQueryKey(workId: string): readonly unknown[] {
  return ['overlay', workId];
}

async function fetchOverlay(workId: string, signal?: AbortSignal): Promise<WorkOverlayState> {
  return expectData(
    await api.GET('/api/v1/works/{workId}/overlay', { params: { path: { workId } }, signal })
  );
}

/**
 * 读取 live 用户事实。
 *
 * `enabled=false` 时**不发请求**，但仍订阅缓存：列表里的卡片用它拿“刚刚切换过收藏”的
 * 真值，而不会为一屏 50 个作品各发一次 overlay 请求。
 */
export function useWorkOverlay(workId: string, enabled: boolean) {
  return useQuery({
    enabled,
    queryKey: overlayQueryKey(workId),
    queryFn: async ({ signal }) => fetchOverlay(workId, signal),
    // publication 事件只是提示，可能丢失；而且写入返回时投影本来就尚未完成。
    // 仅在 pending 期间轮询，直到服务端明确收敛为 published/failed 后立即停止。
    refetchInterval: (query) => (query.state.data?.projectionStatus === 'pending' ? 1_000 : false)
  });
}

/** null 表示清除自定义封面（PUT 中省略该字段即为清除）。 */
export type OverlayPatch = Partial<Omit<WorkOverlayPutRequest, 'customCoverMediaId'>> & {
  customCoverMediaId?: string | null;
};

export type OverlayMutation = UseMutationResult<WorkOverlayState, unknown, OverlayPatch>;

/**
 * 写入用户事实。
 *
 * 流程固定为 **GET → 合并 → 整体 PUT → 以响应为准**：
 * - 先 GET 是因为 PUT 是整体替换，少一个字段就等于把它清空；
 * - 用服务端响应覆盖本地缓存，是因为契约没有 If-Match，两个页签同时改不同字段会互相覆盖，
 *   界面必须如实反映服务端最终结果，而不是继续显示自己刚才提交的值。
 */
export function useOverlayMutation(workId: string): OverlayMutation {
  const queryClient = useQueryClient();
  const csrf = useCsrfHeaders();
  return useMutation<WorkOverlayState, unknown, OverlayPatch>({
    mutationFn: async (patch) => {
      const current = await queryClient.fetchQuery({
        queryKey: overlayQueryKey(workId),
        queryFn: async ({ signal }) => fetchOverlay(workId, signal),
        // 基于尽可能新的服务端状态合并；契约没有并发控制，这是能做到的最好保证。
        staleTime: 0
      });
      const cover = Object.hasOwn(patch, 'customCoverMediaId')
        ? patch.customCoverMediaId
        : current.customCoverMediaId;
      const body: WorkOverlayPutRequest = {
        titleOverride: patch.titleOverride ?? current.titleOverride,
        manualTags: patch.manualTags ?? current.manualTags,
        hidden: patch.hidden ?? current.hidden,
        favorite: patch.favorite ?? current.favorite,
        progress: patch.progress ?? current.progress,
        ...(cover === undefined || cover === null ? {} : { customCoverMediaId: cover })
      };
      return expectData(
        await api.PUT('/api/v1/works/{workId}/overlay', {
          params: { path: { workId }, header: csrf },
          body
        })
      );
    },
    onSuccess: (state) => {
      queryClient.setQueryData(overlayQueryKey(workId), state);
      // 保存后重取：服务端结果才是事实，本地合并只是发起请求时的输入。
      void queryClient.invalidateQueries({ queryKey: overlayQueryKey(workId) });
    }
  });
}

/* ————————————————————————————— 按需内容确认 ————————————————————————————— */

/** 为 located_unverified 媒体创建内容确认 Job。需要 scan.run。 */
export function useVerificationJob(mediaId: string, queryPublicationId?: string) {
  const csrf = useCsrfHeaders();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async () =>
      expectData(
        await api.POST('/api/v1/media/{mediaId}/verification-jobs', {
          params: {
            path: { mediaId },
            query: queryPublicationId === undefined ? {} : { queryPublicationId },
            header: csrf
          }
        })
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['media'] });
    }
  });
}
