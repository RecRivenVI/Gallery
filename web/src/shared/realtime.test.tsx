/*
 * 实时事件契约。
 *
 * 断言的重点全部围绕同一条原则：**HTTP 快照才是事实源**。
 *   - 每次连接与重连都必须重取快照；
 *   - sequence 出现缺口必须重取快照；
 *   - 4401 / 4403 是终态，不再重连；
 *   - 客户端从不向服务端发送任何消息。
 */

import { QueryClientProvider } from '@tanstack/react-query';
import { act, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ReactNode } from 'react';
import { jsonResponse, setFetchHandler } from '../../tests/http';
import { createQueryClient } from './query';
import {
  ACTIVE_REALTIME_EVENT_TYPES,
  REALTIME_EVENT_TYPES,
  RealtimeProvider,
  backoffDelayMs,
  classifySequence,
  parseEnvelope,
  realtimeEffect,
  useRealtime,
  type RealtimeEventType,
  type RealtimeHandlers
} from './realtime';
import { SessionProvider } from './session';

const AUTHENTICATED_BOOTSTRAP = {
  mode: 'personal',
  authenticated: true,
  lanInitialized: false,
  availableCapabilities: ['library.read'],
  effectiveCapabilities: ['library.read'],
  principalId: 'principal_1',
  csrfToken: 'csrf-authenticated-11111111111111111111',
  apiVersion: 'v1',
  websocketProtocolVersion: 1,
  sortProtocolVersion: 1,
  ruleSchemaVersion: 1
};

/** 记录每次连接的假传输。它同时充当「客户端有没有发过消息」的证据——它根本没有 send。 */
class FakeTransport {
  readonly connections: { handlers: RealtimeHandlers; closed: { code?: number }[] }[] = [];

  readonly connect = (_url: string, handlers: RealtimeHandlers) => {
    const record = { handlers, closed: [] as { code?: number }[] };
    this.connections.push(record);
    return {
      close: (code?: number) => {
        record.closed.push({ code });
      }
    };
  };

  get latest() {
    const last = this.connections.at(-1);
    if (!last) throw new Error('尚未建立任何连接');
    return last.handlers;
  }

  open() {
    act(() => this.latest.onOpen());
  }

  send(envelope: Record<string, unknown>) {
    act(() => this.latest.onMessage(JSON.stringify(envelope)));
  }

  sendRaw(data: string) {
    act(() => this.latest.onMessage(data));
  }

  close(code: number) {
    act(() => this.latest.onClose(code));
  }
}

function envelope(eventType: RealtimeEventType, sequence: number, overrides: Record<string, unknown> = {}) {
  return {
    protocolVersion: 1,
    eventType,
    sequence,
    scope: {},
    payload: {},
    serverTime: '2026-07-27T00:00:00Z',
    ...overrides
  };
}

function Probe() {
  const { status, closedReason, lastSequence, snapshotEpoch } = useRealtime();
  return (
    <div>
      <span data-testid="status">{status}</span>
      <span data-testid="closed-reason">{closedReason ?? ''}</span>
      <span data-testid="last-sequence">{lastSequence}</span>
      <span data-testid="snapshot-epoch">{snapshotEpoch}</span>
    </div>
  );
}

function Harness({ transport, children }: { transport: FakeTransport; children?: ReactNode }) {
  return (
    <QueryClientProvider client={createQueryClient()}>
      <SessionProvider>
        <RealtimeProvider transport={transport.connect} url="ws://test.invalid/ws/v1">
          <Probe />
          {children}
        </RealtimeProvider>
      </SessionProvider>
    </QueryClientProvider>
  );
}

function epoch(): number {
  return Number(screen.getByTestId('snapshot-epoch').textContent);
}

beforeEach(() => {
  setFetchHandler(() => jsonResponse(AUTHENTICATED_BOOTSTRAP));
});

afterEach(() => {
  vi.useRealTimers();
});

describe('协议常量', () => {
  it('覆盖契约声明的 14 个事件类型', () => {
    expect(REALTIME_EVENT_TYPES).toHaveLength(14);
    expect(new Set(REALTIME_EVENT_TYPES).size).toBe(14);
  });

  it('明确区分当前真有发送方的 8 个事件', () => {
    expect(ACTIVE_REALTIME_EVENT_TYPES).toHaveLength(8);
    for (const type of ACTIVE_REALTIME_EVENT_TYPES) {
      expect(REALTIME_EVENT_TYPES).toContain(type);
    }
    // 其余 6 个已声明但当前无发送方，分发要覆盖但功能不得依赖它们到达。
    const inactive = REALTIME_EVENT_TYPES.filter((type) => !ACTIVE_REALTIME_EVENT_TYPES.includes(type));
    expect(inactive).toEqual([
      'job.status',
      'job.issue',
      'catalog.publication',
      'overlay.publication',
      'overlay.publication_failed',
      'service.lifecycle'
    ]);
  });

  it('每个事件类型都有明确的本地动作', () => {
    for (const type of REALTIME_EVENT_TYPES) {
      expect(['snapshot', 'jobs', 'publication', 'session', 'none']).toContain(realtimeEffect(type));
    }
    expect(realtimeEffect('connection.ready')).toBe('snapshot');
    expect(realtimeEffect('session.revoked')).toBe('session');
    expect(realtimeEffect('grant.revoked')).toBe('session');
    expect(realtimeEffect('service.lifecycle')).toBe('none');
  });
});

describe('parseEnvelope', () => {
  it('按契约字段名解析', () => {
    const parsed = parseEnvelope(JSON.stringify(envelope('job.progress', 3)));
    // 契约字段是 eventType 而不是 type；读错字段会让每条消息都在属性访问处失败。
    expect(parsed?.eventType).toBe('job.progress');
    expect(parsed?.sequence).toBe(3);
    expect(parsed?.protocolVersion).toBe(1);
  });

  it('拒绝非法内容而不是抛异常', () => {
    expect(parseEnvelope('not json')).toBeUndefined();
    expect(parseEnvelope('null')).toBeUndefined();
    expect(parseEnvelope(JSON.stringify({ eventType: 'job.progress' }))).toBeUndefined();
    expect(parseEnvelope(JSON.stringify(envelope('job.progress', 1, { eventType: 'made.up' })))).toBe(
      undefined
    );
  });
});

describe('classifySequence 与退避', () => {
  it('识别重复、连续与缺口', () => {
    expect(classifySequence(0, 5)).toBe('accept');
    expect(classifySequence(5, 6)).toBe('accept');
    expect(classifySequence(5, 5)).toBe('duplicate');
    expect(classifySequence(5, 4)).toBe('duplicate');
    expect(classifySequence(5, 8)).toBe('gap');
  });

  it('退避指数增长并封顶在 15 秒', () => {
    expect(backoffDelayMs(1)).toBe(1_000);
    expect(backoffDelayMs(2)).toBe(2_000);
    expect(backoffDelayMs(3)).toBe(4_000);
    expect(backoffDelayMs(4)).toBe(8_000);
    expect(backoffDelayMs(5)).toBe(15_000);
    expect(backoffDelayMs(8)).toBe(15_000);
  });
});

describe('RealtimeProvider', () => {
  it('连接建立即重取快照，connection.ready 再确认一次', async () => {
    const transport = new FakeTransport();
    render(<Harness transport={transport} />);
    await screen.findByTestId('status');

    const before = epoch();
    transport.open();
    // 连接期间发生的变化不会补发，因此每次连接都必须重新拉快照。
    expect(epoch()).toBeGreaterThan(before);

    const afterOpen = epoch();
    transport.send(envelope('connection.ready', 1, { payload: { snapshotRequired: true } }));
    expect(epoch()).toBeGreaterThan(afterOpen);
    expect(screen.getByTestId('status')).toHaveTextContent('open');
  });

  it('sequence 缺口触发重新拉取快照', async () => {
    const transport = new FakeTransport();
    render(<Harness transport={transport} />);
    await screen.findByTestId('status');
    transport.open();
    transport.send(envelope('connection.ready', 1));

    const before = epoch();
    transport.send(envelope('job.progress', 2));
    // 连续 sequence 不触发全量重取，只失效任务列表。
    expect(epoch()).toBe(before);
    expect(screen.getByTestId('last-sequence')).toHaveTextContent('2');

    transport.send(envelope('job.progress', 5));
    // 缺口意味着有事件没收到，必须回到 HTTP 快照重新对齐。
    expect(epoch()).toBeGreaterThan(before);
    expect(screen.getByTestId('last-sequence')).toHaveTextContent('5');
  });

  it('重复 sequence 被忽略且不推进游标', async () => {
    const transport = new FakeTransport();
    render(<Harness transport={transport} />);
    await screen.findByTestId('status');
    transport.open();
    transport.send(envelope('job.progress', 4));
    const before = epoch();
    transport.send(envelope('job.progress', 4));
    transport.send(envelope('job.progress', 2));
    expect(screen.getByTestId('last-sequence')).toHaveTextContent('4');
    expect(epoch()).toBe(before);
  });

  it('无法解析的帧按缺口处理', async () => {
    const transport = new FakeTransport();
    render(<Harness transport={transport} />);
    await screen.findByTestId('status');
    transport.open();
    const before = epoch();
    transport.sendRaw('{ 这不是合法信封');
    expect(epoch()).toBeGreaterThan(before);
  });

  it('协议版本不一致时停止消费', async () => {
    const transport = new FakeTransport();
    render(<Harness transport={transport} />);
    await screen.findByTestId('status');
    transport.open();
    transport.send(envelope('job.progress', 1, { protocolVersion: 99 }));
    expect(screen.getByTestId('status')).toHaveTextContent('closed');
    expect(screen.getByTestId('closed-reason')).toHaveTextContent('protocol-mismatch');
  });

  it('4401 与 4403 是终态，不再重连', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    for (const [code, reason] of [
      [4401, 'session-revoked'],
      [4403, 'grant-revoked']
    ] as const) {
      const transport = new FakeTransport();
      const view = render(<Harness transport={transport} />);
      await screen.findByTestId('status');
      transport.open();
      transport.close(code);

      expect(screen.getByTestId('status')).toHaveTextContent('closed');
      expect(screen.getByTestId('closed-reason')).toHaveTextContent(reason);
      act(() => {
        vi.advanceTimersByTime(60_000);
      });
      // 会话或授权已被吊销，重连只会不断被拒绝。
      expect(transport.connections).toHaveLength(1);
      view.unmount();
    }
  });

  it('连续失败按指数退避重连，8 次后停止', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const transport = new FakeTransport();
    render(<Harness transport={transport} />);
    await screen.findByTestId('status');

    // 一次都没连上：每次失败都推进退避，且不早于对应时长重连。
    for (let attempt = 1; attempt <= 8; attempt += 1) {
      transport.close(1006);
      expect(screen.getByTestId('status')).toHaveTextContent('reconnecting');
      act(() => {
        vi.advanceTimersByTime(backoffDelayMs(attempt) - 1);
      });
      expect(transport.connections).toHaveLength(attempt);
      act(() => {
        vi.advanceTimersByTime(1);
      });
      expect(transport.connections).toHaveLength(attempt + 1);
    }

    transport.close(1006);
    act(() => {
      vi.advanceTimersByTime(60_000);
    });
    expect(screen.getByTestId('status')).toHaveTextContent('closed');
    expect(screen.getByTestId('closed-reason')).toHaveTextContent('retries-exhausted');
    expect(transport.connections).toHaveLength(9);
  });

  it('成功连上之后退避计数归零', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const transport = new FakeTransport();
    render(<Harness transport={transport} />);
    await screen.findByTestId('status');

    for (let attempt = 1; attempt <= 3; attempt += 1) {
      transport.close(1006);
      act(() => {
        vi.advanceTimersByTime(backoffDelayMs(attempt));
      });
    }
    expect(transport.connections).toHaveLength(4);

    // 连上一次就说明服务端可用，下一次断线应当从 1 秒重新开始，而不是继续 8 秒起步。
    transport.open();
    transport.close(1006);
    act(() => {
      vi.advanceTimersByTime(backoffDelayMs(1));
    });
    expect(transport.connections).toHaveLength(5);
  });

  it('重连后 sequence 从零重新开始', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const transport = new FakeTransport();
    render(<Harness transport={transport} />);
    await screen.findByTestId('status');
    transport.open();
    transport.send(envelope('job.progress', 7));
    expect(screen.getByTestId('last-sequence')).toHaveTextContent('7');

    transport.close(1006);
    act(() => {
      vi.advanceTimersByTime(backoffDelayMs(1));
    });
    expect(screen.getByTestId('last-sequence')).toHaveTextContent('0');

    transport.open();
    // 新连接的 sequence 重新计数，起始值不是缺口。
    const before = epoch();
    transport.send(envelope('job.progress', 1));
    expect(epoch()).toBe(before);
    expect(screen.getByTestId('last-sequence')).toHaveTextContent('1');
  });

  it('传输层没有任何向服务端发送消息的入口', () => {
    // 协议 v1 没有客户端 → 服务端消息，包括心跳。假传输只暴露 close，
    // 如果实现试图发送任何东西就会在类型层失败。
    const transport = new FakeTransport();
    const connection = transport.connect('ws://test.invalid/ws/v1', {
      onOpen: () => undefined,
      onMessage: () => undefined,
      onClose: () => undefined
    });
    expect(Object.keys(connection)).toEqual(['close']);
  });
});
