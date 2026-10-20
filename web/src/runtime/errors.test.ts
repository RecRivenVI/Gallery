/*
 * 错误文案覆盖度。
 *
 * 这里逐项对照 Go 端的 fault code 常量：`internal/contract/fault/fault.go` 是事实源，
 * OpenAPI 的 ErrorCode enum 少于它，因此**不能**用生成类型充当覆盖度基准。
 * 名单在这里硬编码，任何一侧新增 code 都会让本测试失败并要求补中文文案。
 */

import { describe, expect, it } from 'vitest';
import { GalleryError } from '../api/client';
import { describeError, errorCode, errorCopy, errorCorrelationId, hasErrorCopy, isRetryable } from './errors';

/** internal/contract/fault/fault.go 声明的全部 Code 常量。 */
const GO_FAULT_CODES = [
  'INTERNAL_ERROR',
  'VALIDATION_ERROR',
  'CONFIG_INVALID',
  'UNAUTHENTICATED',
  'FORBIDDEN',
  'NOT_FOUND',
  'CONFLICT',
  'APPDIRS_SOURCE_OVERLAP',
  'SOURCE_ROOTS_OVERLAP',
  'DATABASE_OPEN_FAILED',
  'MIGRATION_FAILED',
  'BACKUP_FAILED',
  'CURSOR_INVALID',
  'CURSOR_EXPIRED',
  'QUERY_TOO_SHORT',
  'OVERLAY_FACT_INVALID',
  'OVERLAY_PROJECTION_FAILED',
  'BINDING_REVIEW_REQUIRED',
  'DERIVED_ASSET_INVALID',
  'DERIVED_ASSET_UNAVAILABLE',
  'DERIVED_ASSET_FAILED',
  'EXTERNAL_TOOL_UNAVAILABLE',
  'EXTERNAL_TOOL_FAILED',
  'RULE_SCHEMA_INVALID',
  'RULE_COMPILE_ERROR',
  'RULE_CEL_LIMIT',
  'RULE_DRY_RUN_FAILED',
  'RULE_IMPACT_FAILED',
  'RULE_EVAL_ERROR',
  'CATALOG_PUBLICATION_MISSING',
  'CONTENT_CHANGED_DURING_HASH',
  'CONTENT_CHANGED',
  'MEDIA_OFFLINE',
  'MEDIA_READ_BUSY',
  'CONTENT_NOT_VERIFIED',
  'HOST_REJECTED',
  'ORIGIN_REJECTED',
  'CSRF_INVALID',
  'PAIRING_INVALID',
  'PAIRING_EXPIRED',
  'INVALID_CREDENTIALS',
  'RATE_LIMITED',
  'LAN_OWNER_REQUIRED',
  'LAN_ALREADY_INITIALIZED',
  'USER_DISABLED',
  'TOKEN_INVALID',
  'TOKEN_EXPIRED',
  'SOURCE_PATH_INVALID',
  'RULE_PARAMETER_INVALID',
  'RULE_DRAFT_CONFLICT',
  'RULE_PACKAGE_CONFLICT',
  'RULE_PARAMETER_CONFLICT',
  'RULE_PUBLISH_BLOCKED',
  'RULE_ROLLBACK_BLOCKED',
  'RULE_VERSION_IN_USE',
  'RULE_IMPORT_INVALID',
  'RULE_BINDING_CONFLICT',
  'JOB_STATE_CONFLICT',
  'JOB_PROGRESS_REGRESSION',
  'JOB_RETRY_EXHAUSTED',
  'JOB_CANCELLATION_REQUESTED',
  'SCAN_ALREADY_RUNNING',
  'CONTENT_HASH_PENDING',
  'DISK_SPACE_INSUFFICIENT',
  'MAINTENANCE_BLOCKED',
  'WATCHER_OVERFLOW',
  'SOURCE_IDENTITY_CHANGED',
  'SOURCE_PERMISSION_DENIED',
  'SOURCE_UNAVAILABLE',
  'SOURCE_READ_FAILED',
  'CONTENT_DISAPPEARED',
  'VERIFICATION_TARGET_MISMATCH',
  'PATH_ESCAPE',
  'CATALOG_CANDIDATE_INVALID',
  'CATALOG_CANDIDATE_ALREADY_PUBLISHED',
  'PROCESS_INTERRUPTED',
  'RANGE_INVALID',
  'RESTORE_FAILED',
  'BACKUP_NOT_FOUND',
  'BACKUP_CORRUPT',
  'BACKUP_INCOMPATIBLE',
  'INSTANCE_ALREADY_RUNNING',
  'LOCK_UNAVAILABLE'
];

/** OpenAPI 的 ErrorCode enum 里有、但不在 fault.go 常量表中的 SourceWork 结构决策 code。 */
const REVIEW_CODES = ['SOURCE_WORK_SPLIT_REVIEW_REQUIRED', 'SOURCE_WORK_MERGE_REVIEW_REQUIRED'];

/** internal/webapp/handler.go 在静态资产层返回，完全不在 OpenAPI 里。 */
const WEB_ASSET_CODES = ['WEB_ASSETS_UNAVAILABLE', 'WEB_ASSETS_INVALID', 'WEB_VERSION_MISMATCH'];

function envelopeError(code: string, options?: { retryable?: boolean; correlationId?: string }) {
  return new GalleryError(
    {
      error: {
        code: code as never,
        retryable: options?.retryable ?? false,
        correlationId: options?.correlationId ?? 'corr-test'
      }
    },
    400
  );
}

describe('errorCopy', () => {
  it('覆盖 fault.go 的全部 code', () => {
    const missing = GO_FAULT_CODES.filter((code) => !hasErrorCopy(code));
    expect(missing, `缺少中文文案的 code：${missing.join(', ')}`).toEqual([]);
  });

  it('覆盖 SourceWork 结构决策与 Web 资产层的 code', () => {
    for (const code of [...REVIEW_CODES, ...WEB_ASSET_CODES]) {
      expect(hasErrorCopy(code), `缺少 ${code} 的文案`).toBe(true);
    }
  });

  it('任务书点名的高频 code 都有文案', () => {
    const highlighted = [
      'MEDIA_READ_BUSY',
      'CONTENT_CHANGED',
      'CURSOR_EXPIRED',
      'CURSOR_INVALID',
      'RATE_LIMITED',
      'CSRF_INVALID',
      'HOST_REJECTED',
      'ORIGIN_REJECTED',
      'FORBIDDEN',
      'NOT_FOUND',
      'SOURCE_UNAVAILABLE',
      'DERIVED_ASSET_UNAVAILABLE',
      'WEB_VERSION_MISMATCH'
    ];
    for (const code of highlighted) {
      expect(hasErrorCopy(code), `缺少 ${code} 的文案`).toBe(true);
    }
  });

  it('全部文案都是中文且不为空', () => {
    for (const code of [...GO_FAULT_CODES, ...REVIEW_CODES, ...WEB_ASSET_CODES]) {
      const copy = errorCopy(code);
      expect(copy.length, `${code} 文案过短`).toBeGreaterThan(4);
      expect(copy, `${code} 文案缺少中文`).toMatch(/[一-龥]/);
    }
  });

  it('NOT_FOUND 同时覆盖「不存在」与「无权查看」', () => {
    // 服务端会把部分 FORBIDDEN 伪装成 404 以免泄露资源存在性，文案不能只说「不存在」。
    expect(errorCopy('NOT_FOUND')).toContain('权限');
  });

  it('未知 code 走兜底并保留原始 code', () => {
    // 后端可能先于前端引入新 code。兜底必须把它原样带出，否则用户和日志失去唯一线索。
    const copy = errorCopy('SOME_FUTURE_CODE');
    expect(copy).toContain('SOME_FUTURE_CODE');
    expect(hasErrorCopy('SOME_FUTURE_CODE')).toBe(false);
  });
});

describe('describeError', () => {
  it('GalleryError 走 code 文案', () => {
    expect(describeError(envelopeError('SOURCE_UNAVAILABLE'))).toBe(errorCopy('SOURCE_UNAVAILABLE'));
  });

  it('未知 code 的 GalleryError 仍然可展示', () => {
    expect(describeError(envelopeError('BRAND_NEW_CODE'))).toContain('BRAND_NEW_CODE');
  });

  it('fetch 层失败给出连接性文案而不是空白', () => {
    expect(describeError(new TypeError('Failed to fetch'))).toContain('无法连接');
  });

  it('普通异常也有兜底', () => {
    expect(describeError(new Error('boom'))).toContain('boom');
    expect(describeError('不是 Error')).toContain('客户端错误');
  });
});

describe('错误元数据', () => {
  it('取出 code 与关联 ID', () => {
    const error = envelopeError('RATE_LIMITED', { correlationId: 'corr-9' });
    expect(errorCode(error)).toBe('RATE_LIMITED');
    expect(errorCorrelationId(error)).toBe('corr-9');
    expect(errorCode(new Error('x'))).toBeUndefined();
    expect(errorCorrelationId(new Error('x'))).toBeUndefined();
  });

  it('只有服务端声明可重试或网络失败才判定可重试', () => {
    expect(isRetryable(envelopeError('MEDIA_READ_BUSY', { retryable: true }))).toBe(true);
    expect(isRetryable(envelopeError('FORBIDDEN'))).toBe(false);
    expect(isRetryable(new TypeError('Failed to fetch'))).toBe(true);
    expect(isRetryable(new Error('boom'))).toBe(false);
  });
});
