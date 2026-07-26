/*
 * 媒体字节加载器：并发自律、有界重试、引用计数缓存。
 *
 * 为什么不能直接写 `<img src="/api/v1/media/…/content">`：
 *
 * 1. **服务端有 16 名额的并发闸**（`internal/transport/httpapi` 的 `acquireMediaRead`）。
 *    名额满时请求先有界等待，再返回可重试的 503 `MEDIA_READ_BUSY`。一屏几十张封面同时
 *    发起，浏览器会一次性打满这个闸，用户看到的是一片碎图。`<img>` 的请求由浏览器排队，
 *    JavaScript 完全插不上手，因此必须由我们自己发请求、自己限并发。
 * 2. **这些响应带 `Cache-Control: no-store` 与 `Vary: Cookie`**，浏览器不会缓存，
 *    每次重新挂载都会重新拉一遍整张图。缓存只能由我们自己持有（这里是 object URL 的
 *    引用计数 LRU）。
 * 3. `MEDIA_READ_BUSY` 不是错误而是背压信号，必须退避重试，重试用尽后给出**明确的降级
 *    表现**（可重试的占位块），而不是浏览器默认的碎图图标。
 *
 * 并发上限刻意远小于服务端的 16：这 16 个名额是**整台服务端**共享的，还要留给全屏查看、
 * 视频 Range、缩略图生成读取源文件以及其它客户端。单个网格把它吃满，等于用自己的滚动
 * 把别的功能饿死。
 */

import { GalleryError, type ErrorEnvelope } from '../api/client';

/** 单客户端同时在飞的媒体字节请求上限。 */
export const MEDIA_CONCURRENCY_LIMIT = 4;
/** `MEDIA_READ_BUSY` 的最大重试次数。超出后按可重试失败呈现，由用户或再次进入视口触发。 */
export const MEDIA_BUSY_MAX_RETRIES = 3;
/** 同时保留的 object URL 数量上限。超出后回收引用计数为 0 的最旧条目。 */
export const MEDIA_CACHE_CAPACITY = 96;

/** 第 n 次重试前的等待时长（n 从 1 开始）：300ms、900ms、2700ms。 */
export function busyBackoffMs(attempt: number): number {
  return 300 * 3 ** (attempt - 1);
}

export interface MediaLoaderOptions {
  limit?: number;
  maxRetries?: number;
  cacheCapacity?: number;
  fetchImpl?: typeof fetch;
  /** 退避等待。测试注入以避免真实计时。 */
  delay?: (ms: number) => Promise<void>;
  createObjectUrl?: (blob: Blob) => string;
  revokeObjectUrl?: (objectUrl: string) => void;
}

export interface MediaLoaderStats {
  /** 当前在飞请求数。 */
  inFlight: number;
  /** 曾经达到的最大在飞请求数。并发自律的断言依据。 */
  peakInFlight: number;
  /** 已发起的 HTTP 请求次数（含重试）。 */
  requests: number;
  /** 当前缓存条目数。 */
  cached: number;
}

interface CacheEntry {
  refs: number;
  /** 结果槽位。用一层间接是为了让 promise 在 entry 构造完成前就能开始跑。 */
  slot: { objectUrl?: string };
  promise: Promise<string>;
}

interface Waiter {
  resolve: () => void;
  reject: (reason: unknown) => void;
  signal?: AbortSignal;
  onAbort?: () => void;
}

function abortError(): Error {
  const error = new Error('媒体请求已取消');
  error.name = 'AbortError';
  return error;
}

/** 该异常是否只是“调用方主动取消”。取消不是失败，不应渲染成错误态。 */
export function isAbortError(error: unknown): boolean {
  return error instanceof Error && error.name === 'AbortError';
}

function defaultCreateObjectUrl(blob: Blob): string {
  if (typeof URL.createObjectURL !== 'function') {
    throw new Error('当前环境不支持 URL.createObjectURL');
  }
  return URL.createObjectURL(blob);
}

function defaultRevokeObjectUrl(objectUrl: string): void {
  if (typeof URL.revokeObjectURL === 'function') URL.revokeObjectURL(objectUrl);
}

async function readEnvelope(response: Response): Promise<ErrorEnvelope | undefined> {
  try {
    const value: unknown = await response.json();
    if (typeof value !== 'object' || value === null || !('error' in value)) return undefined;
    return value as ErrorEnvelope;
  } catch {
    return undefined;
  }
}

/**
 * 媒体字节加载器。
 *
 * 生命周期与页面一致：默认导出一个单例，全部网格、详情与全屏查看共用同一个信号量与缓存，
 * 这样“并发自律”才是**整个客户端**的自律，而不是每个组件各限各的。
 */
export class MediaLoader {
  private readonly limit: number;
  private readonly maxRetries: number;
  private readonly cacheCapacity: number;
  private readonly fetchImpl: typeof fetch;
  private readonly delay: (ms: number) => Promise<void>;
  private readonly createObjectUrl: (blob: Blob) => string;
  private readonly revokeObjectUrl: (objectUrl: string) => void;

  private readonly cache = new Map<string, CacheEntry>();
  private readonly waiters: Waiter[] = [];
  private active = 0;
  private peak = 0;
  private requests = 0;

  constructor(options: MediaLoaderOptions = {}) {
    this.limit = options.limit ?? MEDIA_CONCURRENCY_LIMIT;
    this.maxRetries = options.maxRetries ?? MEDIA_BUSY_MAX_RETRIES;
    this.cacheCapacity = options.cacheCapacity ?? MEDIA_CACHE_CAPACITY;
    this.fetchImpl = options.fetchImpl ?? ((input, init) => fetch(input, init));
    this.delay =
      options.delay ??
      ((ms) =>
        new Promise<void>((resolve) => {
          setTimeout(resolve, ms);
        }));
    this.createObjectUrl = options.createObjectUrl ?? defaultCreateObjectUrl;
    this.revokeObjectUrl = options.revokeObjectUrl ?? defaultRevokeObjectUrl;
  }

  stats(): MediaLoaderStats {
    return {
      inFlight: this.active,
      peakInFlight: this.peak,
      requests: this.requests,
      cached: this.cache.size
    };
  }

  /**
   * 取得一个可直接放进 `<img src>` 的 object URL，并把该地址的引用计数加一。
   *
   * 同一地址的并发请求共享同一次网络往返。调用方**必须**在不再显示时调用 `release`，
   * 否则缓存永远无法回收（object URL 持有整份字节）。
   */
  async acquire(url: string, signal?: AbortSignal): Promise<string> {
    const existing = this.cache.get(url);
    if (existing) {
      existing.refs += 1;
      // 重新插入以维持 LRU 顺序：Map 保持插入顺序，删除再写入即“最近使用”。
      this.cache.delete(url);
      this.cache.set(url, existing);
      try {
        return await existing.promise;
      } catch (error) {
        existing.refs -= 1;
        throw error;
      }
    }
    const slot: { objectUrl?: string } = {};
    const entry: CacheEntry = {
      refs: 1,
      slot,
      promise: (async () => {
        const blob = await this.fetchBlob(url, signal);
        const objectUrl = this.createObjectUrl(blob);
        slot.objectUrl = objectUrl;
        this.evict();
        return objectUrl;
      })()
    };
    this.cache.set(url, entry);
    try {
      return await entry.promise;
    } catch (error) {
      // 失败不进缓存：下一次进入视口应当真正重试，而不是复用一个失败的 promise。
      if (this.cache.get(url) === entry) this.cache.delete(url);
      throw error;
    }
  }

  /** 释放一次引用。计数归零后条目仍可复用，只有超出容量时才真正回收。 */
  release(url: string): void {
    const entry = this.cache.get(url);
    if (!entry) return;
    entry.refs = Math.max(0, entry.refs - 1);
    this.evict();
  }

  /** 立即丢弃全部缓存。只在测试或会话切换时使用。 */
  clear(): void {
    for (const [url, entry] of this.cache) {
      if (entry.slot.objectUrl !== undefined) this.revokeObjectUrl(entry.slot.objectUrl);
      this.cache.delete(url);
    }
  }

  private evict(): void {
    if (this.cache.size <= this.cacheCapacity) return;
    for (const [url, entry] of this.cache) {
      if (this.cache.size <= this.cacheCapacity) return;
      if (entry.refs > 0) continue;
      if (entry.slot.objectUrl !== undefined) this.revokeObjectUrl(entry.slot.objectUrl);
      this.cache.delete(url);
    }
  }

  private async fetchBlob(url: string, signal?: AbortSignal): Promise<Blob> {
    for (let attempt = 0; ; attempt += 1) {
      // 名额必须覆盖**整段字节读取**：服务端在流式写出正文期间一直占着读取租约，
      // 收到响应头就释放名额等于没有限住真正的并发。
      const outcome = await this.withSlot(async (): Promise<Blob | GalleryError> => {
        this.requests += 1;
        const response = await this.fetchImpl(url, { credentials: 'same-origin', signal });
        if (response.ok) return await response.blob();
        return new GalleryError(await readEnvelope(response), response.status);
      }, signal);
      if (!(outcome instanceof GalleryError)) return outcome;
      // MEDIA_READ_BUSY 是背压而不是故障：退避后重试，重试用尽才交给 UI 降级呈现。
      if (outcome.code !== 'MEDIA_READ_BUSY' || attempt >= this.maxRetries) throw outcome;
      await this.delay(busyBackoffMs(attempt + 1));
      if (signal?.aborted === true) throw abortError();
    }
  }

  private async withSlot<T>(run: () => Promise<T>, signal?: AbortSignal): Promise<T> {
    await this.take(signal);
    try {
      return await run();
    } finally {
      this.give();
    }
  }

  private take(signal?: AbortSignal): Promise<void> {
    if (signal?.aborted === true) return Promise.reject(abortError());
    if (this.active < this.limit) {
      this.active += 1;
      this.peak = Math.max(this.peak, this.active);
      return Promise.resolve();
    }
    return new Promise<void>((resolve, reject) => {
      const waiter: Waiter = { resolve, reject, signal };
      if (signal) {
        // 排队期间被取消的请求直接出队：它们还没占用名额，没有理由继续等下去。
        waiter.onAbort = () => {
          const index = this.waiters.indexOf(waiter);
          if (index >= 0) this.waiters.splice(index, 1);
          reject(abortError());
        };
        signal.addEventListener('abort', waiter.onAbort, { once: true });
      }
      this.waiters.push(waiter);
    });
  }

  private give(): void {
    this.active -= 1;
    while (this.waiters.length > 0) {
      const next = this.waiters.shift();
      if (!next) return;
      if (next.onAbort && next.signal) next.signal.removeEventListener('abort', next.onAbort);
      if (next.signal?.aborted === true) {
        next.reject(abortError());
        continue;
      }
      this.active += 1;
      this.peak = Math.max(this.peak, this.active);
      next.resolve();
      return;
    }
  }
}

/** 全客户端共用的加载器实例。 */
export const mediaLoader = new MediaLoader();
