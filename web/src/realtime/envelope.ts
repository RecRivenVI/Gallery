// WebSocket 信封的字段名和事件名是服务端契约
// （`internal/contract/realtime/envelope.schema.json`）的一部分，不是前端自由命名。
// 此前 realtime.tsx 手写的类型读取 `type` 字段而契约字段是 `eventType`，导致每条消息
// 都在属性访问处抛异常、HTTP snapshot 重取从未触发。分类逻辑拆到这里是为了让
// envelope.test.ts 能直接对照契约 Schema 断言字段名与事件名，使同类漂移在测试阶段失败。

/** 客户端读取的信封字段名；必须与契约 Schema 的 required 列表一致。 */
export const ENVELOPE_EVENT_TYPE_FIELD = 'eventType';

export type Envelope = {
  protocolVersion: number;
  eventType: string;
  sequence: number;
  payload?: unknown;
};

/** 一条信封应当触发的本地恢复动作。信封只是提示，事实源始终是 HTTP snapshot。 */
export type EnvelopeAction = 'refresh-session' | 'invalidate-jobs' | 'invalidate-publication' | 'none';

export function classifyEnvelope(eventType: string): EnvelopeAction {
  if (eventType.includes('revoked')) return 'refresh-session';
  if (eventType.startsWith('job.')) return 'invalidate-jobs';
  if (eventType.includes('publication')) return 'invalidate-publication';
  return 'none';
}
