/*
 * 缩略图调度的克制。
 *
 * 每一条断言对应一个"不这么做就会一屏 50 个 Job"的具体契约事实：
 * 只吃 JPEG、只对已确认内容、只在有 media.derive 时、每个媒体只探一次、预算满就不升级。
 */

import { describe, expect, it, vi } from 'vitest';
import { canRequestThumbnail, pollDelayMs, ThumbnailScheduler } from './thumbnails';
import type { Job } from './contracts';

const verifiedJpeg = { mimeType: 'image/jpeg', contentVerificationState: 'content_verified' } as const;

function job(overrides: Partial<Job>): Job {
  return {
    id: 'job_1',
    type: 'derived',
    status: 'queued',
    stage: 'queued',
    progress: { current: 0, total: 1, sequence: 1 },
    attempt: 1,
    createdAt: '2026-07-27T00:00:00Z',
    updatedAt: '2026-07-27T00:00:00Z',
    ...overrides
  };
}

describe('是否值得请求缩略图', () => {
  it('只对已确认内容的 JPEG 且账户有 media.derive 时才请求', () => {
    expect(canRequestThumbnail(verifiedJpeg, true)).toBe(true);
  });

  it('没有 media.derive 就不请求：服务端一定返回 403', () => {
    expect(canRequestThumbnail(verifiedJpeg, false)).toBe(false);
  });

  it('未完成内容确认就不请求：服务端一定返回 409', () => {
    expect(
      canRequestThumbnail({ mimeType: 'image/jpeg', contentVerificationState: 'located_unverified' }, true)
    ).toBe(false);
  });

  it('非 JPEG 不请求：当前 transform 只解码 JPEG', () => {
    expect(
      canRequestThumbnail({ mimeType: 'image/png', contentVerificationState: 'content_verified' }, true)
    ).toBe(false);
    expect(
      canRequestThumbnail({ mimeType: 'video/mp4', contentVerificationState: 'content_verified' }, true)
    ).toBe(false);
  });
});

describe('调度器', () => {
  const options = { csrfToken: 'csrf' };

  it('缓存命中时 Job 立刻是 completed，直接拿到 assetKey', async () => {
    const createJob = vi.fn(() =>
      Promise.resolve(job({ status: 'completed', derivedAssetKey: 'a'.repeat(64) }))
    );
    const scheduler = new ThumbnailScheduler({
      transport: { createJob, readJob: () => Promise.reject(new Error('不应轮询')) }
    });
    await expect(scheduler.request('media_1', options)).resolves.toEqual({
      status: 'ready',
      assetKey: 'a'.repeat(64)
    });
    expect(createJob).toHaveBeenCalledTimes(1);
  });

  it('同一个媒体只探一次：契约没有"是否已生成"查询，重复 POST 就是浪费', async () => {
    const createJob = vi.fn(() =>
      Promise.resolve(job({ status: 'completed', derivedAssetKey: 'b'.repeat(64) }))
    );
    const scheduler = new ThumbnailScheduler({
      transport: { createJob, readJob: () => Promise.reject(new Error('不应轮询')) }
    });
    await scheduler.request('media_1', options);
    await scheduler.request('media_1', options);
    await scheduler.request('media_1', options);
    expect(createJob).toHaveBeenCalledTimes(1);
    expect(scheduler.known('media_1')).toEqual({ status: 'ready', assetKey: 'b'.repeat(64) });
  });

  it('同一个 CanonicalMedia 在不同 publication 独立求值，不能复用旧快照派生资产', async () => {
    const createJob = vi
      .fn()
      .mockResolvedValueOnce(job({ status: 'completed', derivedAssetKey: 'd'.repeat(64) }))
      .mockResolvedValueOnce(job({ status: 'completed', derivedAssetKey: 'e'.repeat(64) }));
    const scheduler = new ThumbnailScheduler({
      transport: { createJob, readJob: () => Promise.reject(new Error('不应轮询')) }
    });

    await scheduler.request('media_1', { ...options, queryPublicationId: 'qpub_1' });
    await scheduler.request('media_1', { ...options, queryPublicationId: 'qpub_2' });

    expect(createJob).toHaveBeenCalledTimes(2);
    expect(scheduler.known('media_1', 'qpub_1')).toEqual({
      status: 'ready',
      assetKey: 'd'.repeat(64)
    });
    expect(scheduler.known('media_1', 'qpub_2')).toEqual({
      status: 'ready',
      assetKey: 'e'.repeat(64)
    });
  });

  it('排队的 Job 会被轮询直到完成', async () => {
    const statuses: Job[] = [
      job({ status: 'running' }),
      job({ status: 'completed', derivedAssetKey: 'c'.repeat(64) })
    ];
    let index = 0;
    const scheduler = new ThumbnailScheduler({
      delay: () => Promise.resolve(),
      transport: {
        createJob: () => Promise.resolve(job({ status: 'queued' })),
        readJob: () => Promise.resolve(statuses[index++] ?? job({ status: 'failed' }))
      }
    });
    await expect(scheduler.request('media_1', options)).resolves.toEqual({
      status: 'ready',
      assetKey: 'c'.repeat(64)
    });
  });

  it('失败的 Job 记为不可用，不再反复重试', async () => {
    const createJob = vi.fn(() =>
      Promise.resolve(job({ status: 'failed', issueCode: 'DERIVED_ASSET_INVALID' }))
    );
    const scheduler = new ThumbnailScheduler({
      transport: { createJob, readJob: () => Promise.reject(new Error('不应轮询')) }
    });
    await expect(scheduler.request('media_1', options)).resolves.toEqual({
      status: 'unavailable',
      code: 'DERIVED_ASSET_INVALID'
    });
    await scheduler.request('media_1', options);
    expect(createJob).toHaveBeenCalledTimes(1);
  });

  it('预算满时直接放弃升级，而不是排一条越滚越长的队', async () => {
    const createJob = vi.fn(
      () =>
        new Promise<Job>(() => {
          /* 永不完成，占住唯一的预算 */
        })
    );
    const scheduler = new ThumbnailScheduler({
      limit: 1,
      transport: { createJob, readJob: () => Promise.reject(new Error('不应轮询')) }
    });
    void scheduler.request('media_1', options);
    await expect(scheduler.request('media_2', options)).resolves.toEqual({ status: 'skipped' });
    // 被跳过的媒体不记忆结论：下一次仍然可以尝试。
    expect(scheduler.known('media_2')).toBeUndefined();
    expect(createJob).toHaveBeenCalledTimes(1);
  });

  it('轮询退避有上限', () => {
    expect(pollDelayMs(1)).toBe(500);
    expect(pollDelayMs(3)).toBe(2000);
    expect(pollDelayMs(9)).toBe(8000);
  });
});
