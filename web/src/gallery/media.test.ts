/*
 * 媒体加载器：并发自律、背压重试、引用计数缓存。
 *
 * 这些是画廊唯一能保护服务端 16 名额并发闸的地方，因此每一条都必须有断言，不能靠"应该
 * 没问题"。
 */

import { describe, expect, it, vi } from 'vitest';
import { busyBackoffMs, isAbortError, MediaLoader } from './media';
import { GalleryError } from '../api/client';

function busyResponse(): Response {
  return new Response(
    JSON.stringify({ error: { code: 'MEDIA_READ_BUSY', retryable: true, correlationId: 'corr' } }),
    { status: 503, headers: { 'Content-Type': 'application/json' } }
  );
}

function okResponse(): Response {
  return new Response('bytes', { status: 200 });
}

function stubObjectUrls() {
  let counter = 0;
  const revoked: string[] = [];
  return {
    revoked,
    createObjectUrl: () => `blob:test-${(counter += 1)}`,
    revokeObjectUrl: (value: string) => revoked.push(value)
  };
}

describe('并发自律', () => {
  it('在飞请求数永远不超过上限，其余请求排队', async () => {
    const gates: (() => void)[] = [];
    const urls = Array.from({ length: 12 }, (_, index) => `/api/v1/media/m${index}/content`);
    const stub = stubObjectUrls();
    const loader = new MediaLoader({
      limit: 3,
      ...stub,
      fetchImpl: async () => {
        await new Promise<void>((resolve) => gates.push(resolve));
        return okResponse();
      }
    });

    const pending = urls.map((url) => loader.acquire(url));
    // 12 个请求同时发起，但同时进入 fetch 的只能有 3 个。
    await vi.waitFor(() => {
      expect(gates.length).toBe(3);
    });
    expect(loader.stats().inFlight).toBe(3);

    // 放行一个名额，队列里补上一个，仍然不超过上限。
    gates[0]?.();
    await vi.waitFor(() => {
      expect(gates.length).toBe(4);
    });
    expect(loader.stats().inFlight).toBe(3);

    // 逐个放行：每放行一个，队列才补上一个，全程不会同时超过 3 个。
    for (let index = 1; index < urls.length; index += 1) {
      await vi.waitFor(() => {
        expect(gates.length).toBeGreaterThan(index);
      });
      gates[index]?.();
    }
    await Promise.all(pending);

    expect(loader.stats().peakInFlight).toBeLessThanOrEqual(3);
    expect(loader.stats().inFlight).toBe(0);
  });

  it('同一地址的并发请求只发一次网络请求', async () => {
    const stub = stubObjectUrls();
    const fetchImpl = vi.fn(() => Promise.resolve(okResponse()));
    const loader = new MediaLoader({ ...stub, fetchImpl });
    const url = '/api/v1/media/m1/content';
    const [first, second] = await Promise.all([loader.acquire(url), loader.acquire(url)]);
    expect(first).toBe(second);
    expect(fetchImpl).toHaveBeenCalledTimes(1);
  });

  it('排队期间被取消的请求不会占用名额', async () => {
    const gates: (() => void)[] = [];
    const stub = stubObjectUrls();
    const loader = new MediaLoader({
      limit: 1,
      ...stub,
      fetchImpl: async () => {
        await new Promise<void>((resolve) => gates.push(resolve));
        return okResponse();
      }
    });
    const controller = new AbortController();
    const first = loader.acquire('/api/v1/media/m1/content');
    const queued = loader.acquire('/api/v1/media/m2/content', controller.signal);
    await vi.waitFor(() => {
      expect(gates.length).toBe(1);
    });
    controller.abort();
    await expect(queued).rejects.toSatisfy(isAbortError);
    gates[0]?.();
    await first;
    // 被取消的那个从未进入 fetch。
    expect(gates.length).toBe(1);
  });
});

describe('MEDIA_READ_BUSY 背压', () => {
  it('退避重试后成功，不把背压当成故障', async () => {
    const stub = stubObjectUrls();
    let call = 0;
    const loader = new MediaLoader({
      ...stub,
      delay: () => Promise.resolve(),
      fetchImpl: () => {
        call += 1;
        return Promise.resolve(call <= 2 ? busyResponse() : okResponse());
      }
    });
    await expect(loader.acquire('/api/v1/media/m1/content')).resolves.toMatch(/^blob:/);
    expect(call).toBe(3);
  });

  it('重试用尽后抛出可识别的 MEDIA_READ_BUSY，供界面降级呈现', async () => {
    const stub = stubObjectUrls();
    const fetchImpl = vi.fn(() => Promise.resolve(busyResponse()));
    const loader = new MediaLoader({ ...stub, maxRetries: 2, delay: () => Promise.resolve(), fetchImpl });
    await expect(loader.acquire('/api/v1/media/m1/content')).rejects.toMatchObject({
      code: 'MEDIA_READ_BUSY'
    });
    expect(fetchImpl).toHaveBeenCalledTimes(3);
  });

  it('确定性失败不重试', async () => {
    const stub = stubObjectUrls();
    const fetchImpl = vi.fn(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({ error: { code: 'MEDIA_OFFLINE', retryable: false, correlationId: 'c' } }),
          { status: 503, headers: { 'Content-Type': 'application/json' } }
        )
      )
    );
    const loader = new MediaLoader({ ...stub, delay: () => Promise.resolve(), fetchImpl });
    await expect(loader.acquire('/api/v1/media/m1/content')).rejects.toBeInstanceOf(GalleryError);
    expect(fetchImpl).toHaveBeenCalledTimes(1);
  });

  it('退避是指数增长的', () => {
    expect(busyBackoffMs(1)).toBe(300);
    expect(busyBackoffMs(2)).toBe(900);
    expect(busyBackoffMs(3)).toBe(2700);
  });
});

describe('引用计数缓存', () => {
  it('仍被引用的条目不会被回收', async () => {
    const stub = stubObjectUrls();
    const loader = new MediaLoader({
      ...stub,
      cacheCapacity: 1,
      fetchImpl: () => Promise.resolve(okResponse())
    });
    await loader.acquire('/a');
    await loader.acquire('/b');
    expect(stub.revoked).toEqual([]);
  });

  it('引用归零且超出容量的最旧条目才被回收', async () => {
    const stub = stubObjectUrls();
    const loader = new MediaLoader({
      ...stub,
      cacheCapacity: 1,
      fetchImpl: () => Promise.resolve(okResponse())
    });
    const first = await loader.acquire('/a');
    loader.release('/a');
    await loader.acquire('/b');
    expect(stub.revoked).toEqual([first]);
  });

  it('失败不进缓存，下一次进入视口是真正的重试', async () => {
    const stub = stubObjectUrls();
    let call = 0;
    const loader = new MediaLoader({
      ...stub,
      delay: () => Promise.resolve(),
      fetchImpl: () => {
        call += 1;
        return Promise.resolve(
          call === 1
            ? new Response(
                JSON.stringify({ error: { code: 'MEDIA_OFFLINE', retryable: false, correlationId: 'c' } }),
                { status: 503, headers: { 'Content-Type': 'application/json' } }
              )
            : okResponse()
        );
      }
    });
    await expect(loader.acquire('/a')).rejects.toBeInstanceOf(GalleryError);
    await expect(loader.acquire('/a')).resolves.toMatch(/^blob:/);
    expect(call).toBe(2);
  });
});
