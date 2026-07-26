/*
 * 缩略图（DerivedAsset）的克制调度。
 *
 * 契约现实，逐条决定了这里为什么这么写：
 *
 * - **没有批量接口**。`POST /api/v1/media/{mediaId}/derived-assets` 一次只处理一个媒体，
 *   一屏 50 个作品最坏要 50 次 POST。
 * - **没有“是否已存在”查询**。想知道缓存里有没有，唯一办法就是 POST 一次（命中缓存时
 *   Job 直接以 completed 返回并带 `derivedAssetKey`）。因此每个媒体只允许探一次，
 *   结果必须记住。
 * - **生成是异步的**：未命中缓存时返回排队中的 Job，要等它完成才能拿到 assetKey。
 * - **当前唯一实现的 transform 只吃 JPEG**（`internal/derived/thumbnail`），
 *   对 PNG/GIF/视频发起请求注定失败，白白消耗 Job 槽位与服务端资源。
 * - 生成本身要读源文件，同样吃服务端的媒体读取名额。
 *
 * 所以策略是：**缩略图是可选升级，不是显示前提**。网格默认直接显示原图（走 media.ts 的
 * 并发闸），只对“停留在视口里、确实是 JPEG、已完成内容确认、且账户有 media.derive”的媒体
 * 尝试升级；同时在飞的生成请求有硬上限，超出就干脆不升级，而不是排一条越滚越长的队。
 */

import { api, expectData } from '../api/client';
import type { Job } from './contracts';

/** 服务端当前唯一接受的 transform（`internal/derived/thumbnail`）。 */
export const THUMBNAIL_TRANSFORM_ID = 'thumbnail';
export const THUMBNAIL_TRANSFORM_VERSION = 'v1';

/** 同时进行的缩略图生成请求上限。 */
export const THUMBNAIL_CONCURRENCY_LIMIT = 2;
/** 单个 Job 的最大轮询次数。超出后放弃等待，继续用原图显示。 */
export const THUMBNAIL_MAX_POLLS = 6;

export type ThumbnailOutcome =
  /** 可用：按 assetKey 读取派生正文。 */
  | { status: 'ready'; assetKey: string }
  /** 明确不可用（不支持的类型、未确认内容、无权限、生成失败）。不再重试。 */
  | { status: 'unavailable'; code?: string }
  /** 本次预算已满或调用方主动取消。**不记忆**，下次仍可尝试。 */
  | { status: 'skipped' };

export interface ThumbnailCandidate {
  mimeType: string;
  contentVerificationState: 'located_unverified' | 'content_verified';
}

/**
 * 是否值得为该媒体请求缩略图。
 *
 * 三个条件都来自服务端的确定性拒绝：非 JPEG 会被 Resolver 拒绝，未确认内容返回 409，
 * 没有 `media.derive` 返回 403。提前判定可以避免用注定失败的请求打满 Job 与媒体名额。
 */
export function canRequestThumbnail(media: ThumbnailCandidate, canDerive: boolean): boolean {
  if (!canDerive) return false;
  if (media.contentVerificationState !== 'content_verified') return false;
  return media.mimeType === 'image/jpeg';
}

export interface ThumbnailRequestOptions {
  csrfToken: string;
  queryPublicationId?: string;
  signal?: AbortSignal;
}

export interface ThumbnailTransport {
  createJob(mediaId: string, options: ThumbnailRequestOptions): Promise<Job>;
  readJob(jobId: string, signal?: AbortSignal): Promise<Job>;
}

const defaultTransport: ThumbnailTransport = {
  createJob: async (mediaId, options) =>
    expectData(
      await api.POST('/api/v1/media/{mediaId}/derived-assets', {
        params: {
          path: { mediaId },
          header: { 'X-Gallery-CSRF': options.csrfToken },
          query:
            options.queryPublicationId === undefined ? {} : { queryPublicationId: options.queryPublicationId }
        },
        body: { transformId: THUMBNAIL_TRANSFORM_ID, transformVersion: THUMBNAIL_TRANSFORM_VERSION },
        signal: options.signal
      })
    ),
  readJob: async (jobId, signal) =>
    expectData(await api.GET('/api/v1/jobs/{jobId}', { params: { path: { jobId } }, signal }))
};

export interface ThumbnailSchedulerOptions {
  transport?: ThumbnailTransport;
  limit?: number;
  maxPolls?: number;
  delay?: (ms: number) => Promise<void>;
}

/** 第 n 次轮询前的等待：500ms、1s、2s、4s、8s，之后固定 8s。 */
export function pollDelayMs(attempt: number): number {
  return Math.min(500 * 2 ** (attempt - 1), 8_000);
}

export class ThumbnailScheduler {
  private readonly transport: ThumbnailTransport;
  private readonly limit: number;
  private readonly maxPolls: number;
  private readonly delay: (ms: number) => Promise<void>;
  private readonly settled = new Map<string, ThumbnailOutcome>();
  private readonly running = new Map<string, Promise<ThumbnailOutcome>>();

  constructor(options: ThumbnailSchedulerOptions = {}) {
    this.transport = options.transport ?? defaultTransport;
    this.limit = options.limit ?? THUMBNAIL_CONCURRENCY_LIMIT;
    this.maxPolls = options.maxPolls ?? THUMBNAIL_MAX_POLLS;
    this.delay =
      options.delay ??
      ((ms) =>
        new Promise<void>((resolve) => {
          setTimeout(resolve, ms);
        }));
  }

  /** 已知结论。用于渲染前同步判断，避免每次挂载都重新发请求。 */
  known(mediaId: string): ThumbnailOutcome | undefined {
    return this.settled.get(mediaId);
  }

  /**
   * 请求一次缩略图。
   *
   * 同一个 mediaId 的结论只求一次：成功记住 assetKey，明确失败记住“不可用”。
   * 预算满时返回 `skipped` 且**不**记忆——它不是关于这个媒体的结论。
   */
  async request(mediaId: string, options: ThumbnailRequestOptions): Promise<ThumbnailOutcome> {
    const known = this.settled.get(mediaId);
    if (known) return known;
    const running = this.running.get(mediaId);
    if (running) return await running;
    if (this.running.size >= this.limit) return { status: 'skipped' };

    const task = this.run(mediaId, options).then(
      (outcome) => {
        this.running.delete(mediaId);
        if (outcome.status !== 'skipped') this.settled.set(mediaId, outcome);
        return outcome;
      },
      () => {
        this.running.delete(mediaId);
        // 网络层失败不是关于这个媒体的结论，不记忆；但也不在本轮重试。
        const outcome: ThumbnailOutcome = { status: 'skipped' };
        return outcome;
      }
    );
    this.running.set(mediaId, task);
    return await task;
  }

  private async run(mediaId: string, options: ThumbnailRequestOptions): Promise<ThumbnailOutcome> {
    let job: Job;
    try {
      job = await this.transport.createJob(mediaId, options);
    } catch (error) {
      return { status: 'unavailable', code: codeOf(error) };
    }
    for (let poll = 0; ; poll += 1) {
      const outcome = classifyJob(job);
      if (outcome) return outcome;
      if (poll >= this.maxPolls) return { status: 'skipped' };
      await this.delay(pollDelayMs(poll + 1));
      if (options.signal?.aborted === true) return { status: 'skipped' };
      try {
        job = await this.transport.readJob(job.id, options.signal);
      } catch (error) {
        return { status: 'unavailable', code: codeOf(error) };
      }
    }
  }
}

function classifyJob(job: Job): ThumbnailOutcome | undefined {
  if (job.status === 'completed') {
    return job.derivedAssetKey === undefined
      ? { status: 'unavailable', code: 'DERIVED_ASSET_UNAVAILABLE' }
      : { status: 'ready', assetKey: job.derivedAssetKey };
  }
  if (job.status === 'failed' || job.status === 'cancelled' || job.status === 'needs_repair') {
    return { status: 'unavailable', code: job.issueCode ?? 'DERIVED_ASSET_FAILED' };
  }
  return undefined;
}

function codeOf(error: unknown): string | undefined {
  if (typeof error === 'object' && error !== null && 'code' in error) {
    const code: unknown = (error as { code: unknown }).code;
    return typeof code === 'string' ? code : undefined;
  }
  return undefined;
}

/** 全客户端共用的调度器：预算是整个客户端的，不是每个网格各有一份。 */
export const thumbnailScheduler = new ThumbnailScheduler();
