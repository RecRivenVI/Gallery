import { describe, expect, it } from 'vitest';
import contractSchema from '../../../internal/contract/realtime/envelope.schema.json';
import { classifyEnvelope, ENVELOPE_EVENT_TYPE_FIELD, type EnvelopeAction } from './envelope';

const schema = contractSchema as {
  required: string[];
  properties: { eventType: { enum: string[] }; protocolVersion: { const: number } };
};

describe('WebSocket 信封契约', () => {
  it('客户端读取的事件字段名必须出现在契约 required 中', () => {
    // 这条断言是 WS-2 的回归防线：前端一旦改回 `type` 或契约改名，此处立刻失败。
    expect(schema.required).toContain(ENVELOPE_EVENT_TYPE_FIELD);
    expect(schema.required).toContain('protocolVersion');
    expect(schema.required).toContain('sequence');
  });

  it('契约声明的每个事件都有确定的本地恢复动作', () => {
    const expected: Record<string, EnvelopeAction> = {
      'connection.ready': 'none',
      'job.status': 'invalidate-jobs',
      'job.issue': 'invalidate-jobs',
      'catalog.publication': 'invalidate-publication',
      'overlay.publication': 'invalidate-publication',
      'overlay.publication_failed': 'invalidate-publication',
      'session.revoked': 'refresh-session',
      'grant.revoked': 'refresh-session',
      'service.lifecycle': 'none',
      'job.queued': 'invalidate-jobs',
      'job.progress': 'invalidate-jobs',
      'job.completed': 'invalidate-jobs',
      'job.failed': 'invalidate-jobs',
      'query.publication.published': 'invalidate-publication'
    };
    // 契约新增事件而前端未登记时失败，避免静默落入 'none'。
    expect(Object.keys(expected).sort()).toEqual([...schema.properties.eventType.enum].sort());
    for (const [eventType, action] of Object.entries(expected)) {
      expect(classifyEnvelope(eventType), eventType).toBe(action);
    }
  });

  it('protocolVersion 常量与契约一致', () => {
    expect(schema.properties.protocolVersion.const).toBe(1);
  });
});
