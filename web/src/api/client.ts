import createClient from 'openapi-fetch';
import type { components, paths } from './schema.gen';

export const api = createClient<paths>({ baseUrl: '', credentials: 'same-origin' });

export type Bootstrap = components['schemas']['BootstrapResponse'];
export type ErrorCode = components['schemas']['ErrorCode'];
export type ErrorEnvelope = components['schemas']['ErrorEnvelope'];

export class GalleryError extends Error {
  readonly code: string;
  readonly retryable: boolean;
  readonly correlationId: string;
  readonly field?: string;

  constructor(envelope?: ErrorEnvelope, status?: number) {
    const detail = envelope?.error;
    super(detail?.code ?? `HTTP_${status ?? 0}`);
    this.name = 'GalleryError';
    this.code = detail?.code ?? `HTTP_${status ?? 0}`;
    this.retryable = detail?.retryable ?? (status !== undefined && status >= 500);
    this.correlationId = detail?.correlationId ?? 'client';
    this.field = detail?.field;
  }
}

type Result<TData, TError> = {
  data?: TData;
  error?: TError;
  response: Response;
};

export function expectData<TData>(result: Result<TData, unknown>): TData {
  if (result.data !== undefined) return result.data;
  throw new GalleryError(asEnvelope(result.error), result.response.status);
}

export function expectNoContent(result: Result<unknown, unknown>): void {
  if (result.response.ok) return;
  throw new GalleryError(asEnvelope(result.error), result.response.status);
}

function asEnvelope(value: unknown): ErrorEnvelope | undefined {
  if (typeof value !== 'object' || value === null || !('error' in value)) return undefined;
  return value as ErrorEnvelope;
}

export function csrfHeaders(csrfToken: string): { 'X-Gallery-CSRF': string } {
  return { 'X-Gallery-CSRF': csrfToken };
}

export function errorMessage(error: unknown): string {
  if (error instanceof GalleryError) {
    return errorCopy[error.code] ?? `请求失败（${error.code}，关联 ID：${error.correlationId}）`;
  }
  if (error instanceof TypeError) return '无法连接 Gallery。请检查服务是否运行，或稍后重试。';
  return '发生未预期的客户端错误。';
}

export const errorCopy: Record<string, string> = {
  UNAUTHENTICATED: '会话已过期或被吊销，请重新认证。',
  FORBIDDEN: '当前账户没有执行此操作的权限。',
  NOT_FOUND: '资源不存在，或当前账户没有查看它的权限。',
  VALIDATION_ERROR: '输入不符合服务端契约，请检查标出的字段。',
  CONFLICT: '资源已发生变化，请刷新后重试。',
  QUERY_TOO_SHORT: '搜索词太短，请增加关键词或使用结构化过滤。',
  CURSOR_EXPIRED: '查询快照已过期，请从第一页重新开始。',
  SOURCE_UNAVAILABLE: 'Source 当前离线，Catalog 中的资料仍可浏览。',
  MEDIA_OFFLINE: '媒体位置当前离线，请在 Source 恢复后重试。',
  CONTENT_NOT_VERIFIED: '媒体尚未完成内容确认，可先创建按需确认任务。',
  CONTENT_DISAPPEARED: '媒体在读取前已消失或发生移动。',
  JOB_STATE_CONFLICT: '任务当前状态不允许此操作，请刷新任务快照。',
  RATE_LIMITED: '请求过于频繁，请等待服务端限流窗口结束。',
  INVALID_CREDENTIALS: '用户名或密码不正确。',
  LAN_OWNER_REQUIRED: 'LAN Owner 尚未初始化。',
  LAN_ALREADY_INITIALIZED: 'LAN Owner 已初始化，请直接登录。',
  INTERNAL: 'Gallery 发生内部错误，请使用关联 ID 查看诊断。'
};
