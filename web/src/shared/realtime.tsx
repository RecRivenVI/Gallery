/*
 * WebSocket 实时事件客户端。
 *
 * 端点 `/ws/v1`，**不在 OpenAPI 里**，信封契约是
 * `internal/contract/realtime/envelope.schema.json`：
 *   { protocolVersion, eventType, sequence, scope, payload, serverTime }
 * 字段名是协议的一部分，不是前端自由命名（EV-39 记录过前端读 `type` 而契约是 `eventType`，
 * 结果每条消息都在属性访问处抛异常，HTTP 快照重取从未触发）。
 *
 * 四条不可协商的行为：
 *
 * 1. **HTTP 快照才是事实源。** `connection.ready` 带 `snapshotRequired: true`，因此每次连接
 *    与重连都必须重新拉取快照；事件本身只是「去重新问一次服务端」的提示。
 * 2. **sequence 每连接单调递增。** 出现缺口说明有事件没收到，同样必须重新拉取快照，而不是
 *    假装无事发生。
 * 3. **协议没有客户端 → 服务端消息。** 不发送任何东西，包括心跳。
 * 4. **4401（会话被吊销）与 4403（授权被吊销）是终态。** 这两种情况下重连只会不断被拒，
 *    正确做法是停止并让用户重新认证。
 */

import { useQueryClient } from '@tanstack/react-query';
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { SNAPSHOT_QUERY_PREFIXES } from './query';
import { useSession } from './session';

/* ————————————————————————————— 协议 ————————————————————————————— */

/** 契约声明的全部事件类型（14 个）。顺序与 envelope.schema.json 保持一致。 */
export const REALTIME_EVENT_TYPES = [
  'connection.ready',
  'job.status',
  'job.issue',
  'catalog.publication',
  'overlay.publication',
  'overlay.publication_failed',
  'session.revoked',
  'grant.revoked',
  'service.lifecycle',
  'job.queued',
  'job.progress',
  'job.completed',
  'job.failed',
  'query.publication.published'
] as const;

export type RealtimeEventType = (typeof REALTIME_EVENT_TYPES)[number];

/**
 * 当前**确实有发送方**的 8 个事件类型。
 *
 * 其余 6 个（job.status / job.issue / catalog.publication / overlay.publication /
 * overlay.publication_failed / service.lifecycle）已在契约里声明但没有实现发送方。
 * 分发逻辑覆盖它们是为了将来接上时不需要改客户端；**但任何功能都不得依赖它们到达**。
 */
export const ACTIVE_REALTIME_EVENT_TYPES: readonly RealtimeEventType[] = [
  'connection.ready',
  'job.queued',
  'job.progress',
  'job.completed',
  'job.failed',
  'query.publication.published',
  'session.revoked',
  'grant.revoked'
];

export interface RealtimeScope {
  libraryId?: string;
  sourceId?: string;
  jobId?: string;
}

export interface RealtimeEnvelope {
  protocolVersion: number;
  eventType: RealtimeEventType;
  sequence: number;
  scope: RealtimeScope;
  payload: unknown;
  serverTime: string;
}

/** 一条事件应当触发的本地动作。 */
export type RealtimeEffect =
  | 'snapshot' // 重新拉取全部快照
  | 'jobs' // 只失效任务列表
  | 'publication' // 失效发布相关的浏览/查询结果
  | 'session' // 重新拉取 bootstrap（认证或授权发生变化）
  | 'none';

/**
 * 事件 → 本地动作。switch 覆盖全部 14 个类型，缺一个就是编译错误
 * （末尾的 never 断言 + tsconfig 的 noFallthroughCasesInSwitch）。
 */
export function realtimeEffect(eventType: RealtimeEventType): RealtimeEffect {
  switch (eventType) {
    case 'connection.ready':
      return 'snapshot';
    case 'job.queued':
    case 'job.progress':
    case 'job.completed':
    case 'job.failed':
    case 'job.status':
    case 'job.issue':
      return 'jobs';
    case 'query.publication.published':
    case 'catalog.publication':
    case 'overlay.publication':
    case 'overlay.publication_failed':
      return 'publication';
    case 'session.revoked':
    case 'grant.revoked':
      return 'session';
    case 'service.lifecycle':
      return 'none';
    default: {
      const unexpected: never = eventType;
      // 服务端可能先于前端引入新事件类型。协议要求未知事件不得导致连接中断，
      // 因此按「不做本地动作」处理，而不是抛异常。
      void unexpected;
      return 'none';
    }
  }
}

/** 解析并校验一条信封。任何不符合契约的内容返回 undefined，由调用方按「需要重取快照」处理。 */
export function parseEnvelope(raw: string): RealtimeEnvelope | undefined {
  let value: unknown;
  try {
    value = JSON.parse(raw);
  } catch {
    return undefined;
  }
  if (typeof value !== 'object' || value === null) return undefined;
  const candidate = value as Partial<RealtimeEnvelope>;
  if (typeof candidate.protocolVersion !== 'number') return undefined;
  if (typeof candidate.sequence !== 'number') return undefined;
  if (typeof candidate.eventType !== 'string') return undefined;
  const known: readonly string[] = REALTIME_EVENT_TYPES;
  if (!known.includes(candidate.eventType)) return undefined;
  return {
    protocolVersion: candidate.protocolVersion,
    eventType: candidate.eventType,
    sequence: candidate.sequence,
    scope: candidate.scope ?? {},
    payload: candidate.payload,
    serverTime: typeof candidate.serverTime === 'string' ? candidate.serverTime : ''
  };
}

/** 判断新的 sequence 相对已接收的最大值意味着什么。 */
export function classifySequence(lastSequence: number, incoming: number): 'accept' | 'duplicate' | 'gap' {
  if (incoming <= lastSequence) return 'duplicate';
  // lastSequence 为 0 表示本连接还没收到任何事件，此时任何起始值都是合法的。
  if (lastSequence !== 0 && incoming !== lastSequence + 1) return 'gap';
  return 'accept';
}

/* ————————————————————————————— 传输 ————————————————————————————— */

export interface RealtimeHandlers {
  onOpen: () => void;
  onMessage: (data: string) => void;
  onClose: (code: number) => void;
}

export interface RealtimeConnection {
  close: (code?: number, reason?: string) => void;
}

/**
 * 传输抽象。默认实现包装浏览器 WebSocket；测试注入假实现。
 *
 * 抽象成「传入回调、返回句柄」而不是直接暴露 WebSocket，是为了让假实现不必伪造
 * EventTarget 的全部类型表面。
 */
export type RealtimeTransport = (url: string, handlers: RealtimeHandlers) => RealtimeConnection;

const browserTransport: RealtimeTransport = (url, handlers) => {
  const socket = new WebSocket(url);
  socket.addEventListener('open', () => handlers.onOpen());
  socket.addEventListener('message', (event: MessageEvent<unknown>) => {
    // 协议只有文本信封；二进制帧不属于 v1，直接忽略而不是当成损坏消息触发重取。
    if (typeof event.data === 'string') handlers.onMessage(event.data);
  });
  socket.addEventListener('close', (event: CloseEvent) => handlers.onClose(event.code));
  socket.addEventListener('error', () => socket.close());
  return { close: (code, reason) => socket.close(code, reason) };
};

/** 会话被吊销。终态，不重连。 */
const CLOSE_SESSION_REVOKED = 4401;
/** 授权被吊销。终态，不重连。 */
const CLOSE_GRANT_REVOKED = 4403;
const MAX_RECONNECT_ATTEMPTS = 8;
const MAX_BACKOFF_MS = 15_000;

/** 第 n 次重连（n 从 1 开始）之前的等待时长：1s、2s、4s、8s，之后固定 15s。 */
export function backoffDelayMs(attempt: number): number {
  return Math.min(1_000 * 2 ** (attempt - 1), MAX_BACKOFF_MS);
}

/* ————————————————————————————— Provider ————————————————————————————— */

export type RealtimeStatus = 'idle' | 'connecting' | 'open' | 'reconnecting' | 'closed';

export type RealtimeClosedReason =
  'session-revoked' | 'grant-revoked' | 'retries-exhausted' | 'protocol-mismatch';

export interface RealtimeValue {
  status: RealtimeStatus;
  closedReason?: RealtimeClosedReason;
  /** 本连接已接收的最大 sequence。断线重连后从 0 重新开始。 */
  lastSequence: number;
  /**
   * 快照代次。每次「必须重新拉取 HTTP 快照」时 +1。
   *
   * 页面可以把它放进 useEffect 依赖里做自定义重取；标准的 TanStack 查询已经由
   * Provider 自动失效，通常不需要用到它。
   */
  snapshotEpoch: number;
}

const RealtimeContext = createContext<RealtimeValue>({
  status: 'idle',
  lastSequence: 0,
  snapshotEpoch: 0
});

type EventHandler = (envelope: RealtimeEnvelope) => void;
const RealtimeSubscribeContext = createContext<
  ((eventType: RealtimeEventType, handler: EventHandler) => () => void) | null
>(null);

export interface RealtimeProviderProps {
  children: ReactNode;
  /** 测试注入的传输实现。生产不要传。 */
  transport?: RealtimeTransport;
  /** 连接地址。默认按当前页面推导 `/ws/v1`。 */
  url?: string;
}

export function RealtimeProvider({ children, transport = browserTransport, url }: RealtimeProviderProps) {
  const { bootstrap, authenticated, refresh } = useSession();
  const queryClient = useQueryClient();
  const [status, setStatus] = useState<RealtimeStatus>('idle');
  const [closedReason, setClosedReason] = useState<RealtimeClosedReason | undefined>(undefined);
  const [lastSequence, setLastSequence] = useState(0);
  const [snapshotEpoch, setSnapshotEpoch] = useState(0);
  const subscribers = useRef(new Map<RealtimeEventType, Set<EventHandler>>());

  const protocolVersion = bootstrap.websocketProtocolVersion;

  const subscribe = useCallback((eventType: RealtimeEventType, handler: EventHandler) => {
    const existing = subscribers.current.get(eventType) ?? new Set<EventHandler>();
    existing.add(handler);
    subscribers.current.set(eventType, existing);
    return () => {
      existing.delete(handler);
    };
  }, []);

  const invalidateSnapshots = useCallback(() => {
    setSnapshotEpoch((epoch) => epoch + 1);
    for (const prefix of SNAPSHOT_QUERY_PREFIXES) {
      void queryClient.invalidateQueries({ queryKey: [prefix] });
    }
  }, [queryClient]);

  useEffect(() => {
    if (!authenticated) {
      setStatus('idle');
      return;
    }

    let disposed = false;
    let attempts = 0;
    let connection: RealtimeConnection | undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let sequence = 0;

    const target = url ?? `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}/ws/v1`;

    const finish = (reason: RealtimeClosedReason) => {
      setStatus('closed');
      setClosedReason(reason);
    };

    const connect = () => {
      if (disposed) return;
      setStatus(attempts === 0 ? 'connecting' : 'reconnecting');
      sequence = 0;
      setLastSequence(0);

      connection = transport(target, {
        onOpen: () => {
          if (disposed) return;
          attempts = 0;
          setStatus('open');
          setClosedReason(undefined);
          // 每次连接与重连都必须重新拉取 HTTP 快照：连接期间发生的变化不会补发。
          // 服务端随后还会发一条带 snapshotRequired 的 connection.ready，两次失效会被
          // TanStack 合并成同一次重取，因此这里的兜底不产生额外往返，却能在
          // connection.ready 缺失时仍然保证不看陈旧数据。
          invalidateSnapshots();
        },
        onMessage: (data) => {
          if (disposed) return;
          const envelope = parseEnvelope(data);
          if (!envelope) {
            // 无法解析的帧意味着我们可能漏掉了状态变化，按缺口处理。
            invalidateSnapshots();
            return;
          }
          if (envelope.protocolVersion !== protocolVersion) {
            // 版本不一致时继续消费会得到语义不明的 payload。停止并要求用户刷新前端。
            disposed = true;
            connection?.close(1002, 'protocol mismatch');
            finish('protocol-mismatch');
            return;
          }
          const decision = classifySequence(sequence, envelope.sequence);
          if (decision === 'duplicate') return;
          if (decision === 'gap') invalidateSnapshots();
          sequence = envelope.sequence;
          setLastSequence(envelope.sequence);

          switch (realtimeEffect(envelope.eventType)) {
            case 'snapshot':
              invalidateSnapshots();
              break;
            case 'jobs':
              void queryClient.invalidateQueries({ queryKey: ['jobs'] });
              break;
            case 'publication':
              void queryClient.invalidateQueries({ queryKey: ['publication'] });
              void queryClient.invalidateQueries({ queryKey: ['works'] });
              void queryClient.invalidateQueries({ queryKey: ['media'] });
              break;
            case 'session':
              // 认证或授权发生变化：bootstrap 是唯一事实源，必须重新拉。
              void refresh();
              invalidateSnapshots();
              break;
            case 'none':
              break;
          }

          for (const handler of subscribers.current.get(envelope.eventType) ?? []) {
            handler(envelope);
          }
        },
        onClose: (code) => {
          if (disposed) return;
          if (code === CLOSE_SESSION_REVOKED) {
            void refresh();
            finish('session-revoked');
            return;
          }
          if (code === CLOSE_GRANT_REVOKED) {
            void refresh();
            finish('grant-revoked');
            return;
          }
          attempts += 1;
          if (attempts > MAX_RECONNECT_ATTEMPTS) {
            finish('retries-exhausted');
            return;
          }
          setStatus('reconnecting');
          timer = setTimeout(connect, backoffDelayMs(attempts));
        }
      });
    };

    connect();
    return () => {
      disposed = true;
      if (timer !== undefined) clearTimeout(timer);
      connection?.close(1000, 'provider disposed');
    };
  }, [authenticated, protocolVersion, transport, url, queryClient, refresh, invalidateSnapshots]);

  const value = useMemo<RealtimeValue>(
    () => ({ status, closedReason, lastSequence, snapshotEpoch }),
    [status, closedReason, lastSequence, snapshotEpoch]
  );

  return (
    <RealtimeContext.Provider value={value}>
      <RealtimeSubscribeContext.Provider value={subscribe}>{children}</RealtimeSubscribeContext.Provider>
    </RealtimeContext.Provider>
  );
}

export function useRealtime(): RealtimeValue {
  return useContext(RealtimeContext);
}

/**
 * 订阅单个事件类型。
 *
 * 只用于「除了失效查询之外还要做点别的」的场景（例如把任务失败弹成 toast）。
 * **不要**用它维护本地列表状态：事件可能丢失，列表必须来自 HTTP 快照。
 */
export function useRealtimeEvent(eventType: RealtimeEventType, handler: EventHandler): void {
  const subscribe = useContext(RealtimeSubscribeContext);
  const handlerRef = useRef(handler);
  // 在 effect 中同步最新 handler：调用方通常传内联函数，直接把它放进订阅依赖会让每次
  // 渲染都退订重订；而在渲染期写 ref 又违反 React 的并发约束。
  useEffect(() => {
    handlerRef.current = handler;
  }, [handler]);
  useEffect(() => {
    if (!subscribe) return;
    return subscribe(eventType, (envelope) => handlerRef.current(envelope));
  }, [subscribe, eventType]);
}
