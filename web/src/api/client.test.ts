import { describe, expect, it } from 'vitest';
import { errorMessage, GalleryError } from './client';

describe('GalleryError', () => {
  it('只暴露稳定 code、字段和 correlation id', () => {
    const error = new GalleryError(
      { error: { code: 'CURSOR_EXPIRED', retryable: false, correlationId: 'corr_test', field: 'cursor' } },
      409
    );
    expect(error.code).toBe('CURSOR_EXPIRED');
    expect(error.field).toBe('cursor');
    expect(error.correlationId).toBe('corr_test');
    expect(errorMessage(error)).toContain('快照已过期');
  });

  it('网络错误给出可恢复提示', () => {
    expect(errorMessage(new TypeError('fetch failed'))).toContain('无法连接');
  });
});
