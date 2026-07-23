import { useQueryClient } from '@tanstack/react-query';
import { createContext, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { useSession } from '../auth/session';
import { acceptSequence } from '../state/generation';

type ConnectionState = 'idle' | 'connecting' | 'ready' | 'reconnecting' | 'unavailable';
type RealtimeValue = { state: ConnectionState; lastSequence: number };
const RealtimeContext = createContext<RealtimeValue>({ state: 'idle', lastSequence: 0 });

type Envelope = { protocolVersion: number; type: string; sequence: number; payload?: unknown };

export function RealtimeProvider({ children }: { children: ReactNode }) {
  const { bootstrap, refresh } = useSession();
  const queryClient = useQueryClient();
  const [state, setState] = useState<ConnectionState>('idle');
  const [lastSequence, setLastSequence] = useState(0);
  const sequenceRef = useRef(0);

  useEffect(() => {
    if (!bootstrap.authenticated) return;
    let disposed = false;
    let socket: WebSocket | undefined;
    let reconnect: number | undefined;
    let attempts = 0;

    const recoverSnapshot = async () => {
      await Promise.all([
        refresh(),
        queryClient.invalidateQueries({ queryKey: ['jobs'] }),
        queryClient.invalidateQueries({ queryKey: ['publication'] }),
        queryClient.invalidateQueries({ queryKey: ['works'] }),
        queryClient.invalidateQueries({ queryKey: ['overlay'] })
      ]);
    };

    const connect = () => {
      if (disposed) return;
      setState(attempts === 0 ? 'connecting' : 'reconnecting');
      const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
      socket = new WebSocket(`${protocol}//${location.host}/ws/v1`);
      socket.addEventListener('open', () => {
        attempts = 0;
        setState('ready');
        void recoverSnapshot();
      });
      socket.addEventListener('message', (event) => {
        if (typeof event.data !== 'string') return;
        let envelope: Envelope;
        try {
          envelope = JSON.parse(event.data) as Envelope;
        } catch {
          void recoverSnapshot();
          return;
        }
        if (envelope.protocolVersion !== bootstrap.websocketProtocolVersion) {
          socket?.close(1002, 'protocol mismatch');
          return;
        }
        const decision = acceptSequence(sequenceRef.current, envelope.sequence);
        if (decision === 'duplicate') return;
        if (decision === 'gap') void recoverSnapshot();
        sequenceRef.current = envelope.sequence;
        setLastSequence(envelope.sequence);
        if (envelope.type.includes('revoked')) {
          void refresh();
          return;
        }
        if (envelope.type.startsWith('job.')) void queryClient.invalidateQueries({ queryKey: ['jobs'] });
        if (envelope.type.includes('publication')) {
          void queryClient.invalidateQueries({ queryKey: ['publication'] });
          void queryClient.invalidateQueries({ queryKey: ['works'] });
        }
      });
      socket.addEventListener('close', (event) => {
        if (disposed) return;
        // 关闭码可能被代理或浏览器归一化为 1006；任何断线都先以
        // bootstrap 事实源重新验证凭据，再决定是否重连。
        void recoverSnapshot();
        if (event.code === 4401 || event.code === 4403) {
          setState('unavailable');
          return;
        }
        attempts += 1;
        if (attempts > 8) {
          setState('unavailable');
          return;
        }
        reconnect = window.setTimeout(connect, Math.min(1000 * 2 ** (attempts - 1), 15_000));
      });
      socket.addEventListener('error', () => socket?.close());
    };

    connect();
    return () => {
      disposed = true;
      if (reconnect !== undefined) window.clearTimeout(reconnect);
      socket?.close(1000, 'route disposed');
    };
  }, [bootstrap.authenticated, bootstrap.websocketProtocolVersion, queryClient, refresh]);

  const value = useMemo(() => ({ state, lastSequence }), [state, lastSequence]);
  return <RealtimeContext.Provider value={value}>{children}</RealtimeContext.Provider>;
}

export function useRealtime(): RealtimeValue {
  return useContext(RealtimeContext);
}
