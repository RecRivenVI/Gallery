/*
 * TanStack QueryClient 工厂。
 *
 * 两个入口各自建一个实例（它们是两个独立的 SPA），但重试与失效策略必须一致，否则同一个
 * 服务端行为在两端表现不同。
 */

import { QueryClient } from '@tanstack/react-query';
import { GalleryError } from '../api/client';
import { isRetryable } from './errors';

/**
 * 允许自动重试的最大次数。
 *
 * 只对服务端显式声明 retryable 的失败重试；`FORBIDDEN`、`VALIDATION_ERROR`、`CONFLICT`
 * 这类确定性失败重试多少次都是同一个结果，只会放大限流压力。
 */
const MAX_QUERY_RETRIES = 2;

/**
 * 快照类查询的 key 前缀。
 *
 * realtime 收到「必须重新拉取 HTTP 快照」的信号时会失效这些前缀。两条工作线新增列表查询时
 * 应复用这里的前缀作为 queryKey 的第一段，否则重连后不会自动重取。
 */
export const SNAPSHOT_QUERY_PREFIXES = [
  'works',
  'media',
  'creators',
  'libraries',
  'sources',
  'jobs',
  'rules',
  'bindings',
  'publication',
  'overlay',
  'governance',
  'maintenance'
] as const;

export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // 服务端拥有排序、过滤与分页语义；客户端只缓存结果，不做本地重排。
        staleTime: 5_000,
        refetchOnWindowFocus: true,
        retry: (failureCount, error) => failureCount < MAX_QUERY_RETRIES && isRetryable(error),
        retryDelay: (attempt) => Math.min(1_000 * 2 ** attempt, 8_000)
      },
      mutations: {
        // 变更绝不自动重试：重复提交可能产生第二个 Job 或第二条用户事实。
        retry: false
      }
    }
  });
}

/** 判断一个失败是否代表「当前会话已经不成立」，需要重新拉 bootstrap。 */
export function isSessionInvalidated(error: unknown): boolean {
  if (!(error instanceof GalleryError)) return false;
  return error.code === 'UNAUTHENTICATED' || error.code === 'CSRF_INVALID';
}
