/*
 * 会话：bootstrap 快照、认证动作、capability 与 CSRF。
 *
 * 三条必须遵守的契约事实：
 *
 * 1. `GET /api/v1/bootstrap` 是**唯一**状态来源。会话 cookie `gallery_session` 是
 *    HttpOnly + SameSite=Strict，JavaScript 读不到，因此「是否已登录」只能问服务端。
 *
 * 2. 所有变更请求都要带 `X-Gallery-CSRF`，取值来自 bootstrap 的 `csrfToken`。
 *    **认证前后是两个不同的 token**：配对、登录、Owner 初始化、登出之后都必须重新拉
 *    bootstrap，否则下一次变更会收到 `CSRF_INVALID`。本文件的认证动作全部在成功后
 *    清空查询缓存并重取 bootstrap，调用方不需要（也不应该）自己处理这件事。
 *
 * 3. `effectiveCapabilities` 只是 **global scope**，不反映按 Source/Library 的授权；服务端
 *    还会把部分 `FORBIDDEN` 伪装成 `404` 以免泄露资源存在性。因此 capability 只能用来隐藏
 *    明显不可用的入口，**不能**当作真实授权判断——UI 必须能优雅呈现服务端返回的错误。
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { api, csrfHeaders, expectData, expectNoContent, type Bootstrap } from '../api/client';
// capability 词表事实源在 `internal/auth`，前端副本由 Go 的
// TestWebCapabilityVocabularyMatchesBackend 逐项比对。这里只是转发，不要在别处另抄一份。
import { CAPABILITIES, JOB_MUTATION_CAPABILITIES, type Capability } from '../auth/capabilities';
import { describeError, errorCode, errorCorrelationId } from './errors';
import { isSessionInvalidated } from './query';
import { Button, ErrorState, Spinner } from '../design';

export { CAPABILITIES, JOB_MUTATION_CAPABILITIES };
export type { Capability };

export type SessionMode = 'personal' | 'lan';

export interface SessionValue {
  /** 服务端 bootstrap 快照原文。协议版本号等字段直接读它。 */
  bootstrap: Bootstrap;
  authenticated: boolean;
  /** global scope 的有效 capability 集合。只用于隐藏入口，不是授权判断。 */
  capabilities: ReadonlySet<Capability>;
  csrfToken: string;
  mode: SessionMode;
  /** 重新拉取 bootstrap。认证状态可能变化的任何时刻都应调用。 */
  refresh: () => Promise<void>;
}

const SessionContext = createContext<SessionValue | null>(null);

const BOOTSTRAP_QUERY_KEY = ['bootstrap'] as const;

function isBootstrapQueryKey(value: unknown): boolean {
  if (!Array.isArray(value)) return false;
  const queryKey: unknown[] = value;
  return queryKey[0] === 'bootstrap';
}

export interface SessionProviderProps {
  children: ReactNode;
  /** 首次拉取 bootstrap 期间的占位内容。默认是一个居中的加载指示。 */
  fallback?: ReactNode;
}

export function SessionProvider({ children, fallback }: SessionProviderProps) {
  const queryClient = useQueryClient();
  const [reconciliation, setReconciliation] = useState<
    { status: 'idle' | 'pending'; error?: never } | { status: 'error'; error: unknown }
  >({ status: 'idle' });
  const reconciliationRef = useRef<Promise<void> | null>(null);
  const bootstrapQuery = useQuery({
    queryKey: BOOTSTRAP_QUERY_KEY,
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/bootstrap', { signal })),
    // bootstrap 决定认证态与 CSRF，宁可多拉一次也不要用过期的 token 发变更。
    staleTime: 15_000,
    refetchOnWindowFocus: true
  });

  const refresh = useCallback(async () => {
    await queryClient.refetchQueries({ queryKey: BOOTSTRAP_QUERY_KEY });
  }, [queryClient]);

  const reconcileInvalidatedSession = useCallback(() => {
    if (reconciliationRef.current !== null) return;

    setReconciliation({ status: 'pending' });
    const task = (async () => {
      // 一旦服务端判定当前主体无效，旧主体的响应和缓存都不能继续留在界面上。
      // bootstrap 本身保留为活动查询，用它取得新的匿名/认证状态和 CSRF token。
      await queryClient.cancelQueries({ predicate: (query) => query.queryKey[0] !== 'bootstrap' });
      queryClient.removeQueries({ predicate: (query) => query.queryKey[0] !== 'bootstrap' });
      await queryClient.refetchQueries(
        { queryKey: BOOTSTRAP_QUERY_KEY, type: 'active' },
        { throwOnError: true }
      );
    })();
    reconciliationRef.current = task;
    void task
      .then(
        () => setReconciliation({ status: 'idle' }),
        (error: unknown) => setReconciliation({ status: 'error', error })
      )
      .finally(() => {
        if (reconciliationRef.current === task) reconciliationRef.current = null;
      });
  }, [queryClient]);

  useEffect(() => {
    const unsubscribeQueries = queryClient.getQueryCache().subscribe((event) => {
      if (
        event.type === 'updated' &&
        event.action.type === 'error' &&
        !isBootstrapQueryKey(event.query.queryKey) &&
        isSessionInvalidated(event.query.state.error)
      ) {
        reconcileInvalidatedSession();
      }
    });
    const unsubscribeMutations = queryClient.getMutationCache().subscribe((event) => {
      if (
        event.type === 'updated' &&
        event.action.type === 'error' &&
        isSessionInvalidated(event.mutation.state.error)
      ) {
        reconcileInvalidatedSession();
      }
    });
    return () => {
      unsubscribeQueries();
      unsubscribeMutations();
    };
  }, [queryClient, reconcileInvalidatedSession]);

  const bootstrap = bootstrapQuery.data;
  const value = useMemo<SessionValue | null>(() => {
    if (!bootstrap) return null;
    // 服务端可能返回前端词表之外的名字（后端更新在前）。过滤掉未知名字，
    // 而不是把它们当成已知 capability——`can()` 的返回值必须只表达前端理解的语义。
    const known = new Set<string>(CAPABILITIES);
    const capabilities = new Set<Capability>(
      bootstrap.effectiveCapabilities.filter((name): name is Capability => known.has(name))
    );
    return {
      bootstrap,
      authenticated: bootstrap.authenticated,
      capabilities,
      csrfToken: bootstrap.csrfToken,
      mode: bootstrap.mode,
      refresh
    };
  }, [bootstrap, refresh]);

  if (reconciliation.status === 'pending') {
    return (
      <div className="ui-state">
        <Spinner label="正在重新确认会话…" />
      </div>
    );
  }

  if (reconciliation.status === 'error') {
    return (
      <ErrorState
        title="无法重新确认会话"
        description={describeError(reconciliation.error)}
        code={errorCode(reconciliation.error)}
        correlationId={errorCorrelationId(reconciliation.error)}
        onRetry={reconcileInvalidatedSession}
      />
    );
  }

  if (bootstrapQuery.isPending) {
    return (
      fallback ?? (
        <div className="ui-state">
          <Spinner label="正在连接 Gallery…" />
        </div>
      )
    );
  }

  if (!value) {
    const error: unknown = bootstrapQuery.error;
    return (
      <ErrorState
        title="无法连接 Gallery"
        description={describeError(error)}
        code={errorCode(error)}
        correlationId={errorCorrelationId(error)}
        onRetry={() => void refresh()}
      />
    );
  }

  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession(): SessionValue {
  const value = useContext(SessionContext);
  if (!value) throw new Error('SessionProvider 缺失');
  return value;
}

/**
 * 判断 global scope 是否具备某个 capability。
 *
 * 参数是联合类型而不是 string：EV-39 记录过前端发明了 6 个后端不存在的 capability 名，
 * 结果对任何主体都判定为「无权」，整块管理功能静默消失。发明名字必须是编译错误。
 */
export function useCapability(name: Capability): boolean {
  return useSession().capabilities.has(name);
}

/** 任一满足即可。用于服务端按资源类别派生所需 capability 的场景（例如任务取消/重试）。 */
export function useAnyCapability(names: readonly Capability[]): boolean {
  const { capabilities } = useSession();
  return names.some((name) => capabilities.has(name));
}

/** 当前 CSRF header。所有变更请求都必须带上它。 */
export function useCsrfHeaders(): { 'X-Gallery-CSRF': string } {
  return csrfHeaders(useSession().csrfToken);
}

export interface LoginInput {
  username: string;
  password: string;
  clientLabel?: string;
}

export interface LanOwnerInput {
  username: string;
  displayName: string;
  password: string;
}

export interface AuthActions {
  /** Personal 一次性配对：创建 attempt → 交换 credential。两步都要 CSRF，且仅 loopback 可用。 */
  pairPersonal: () => Promise<void>;
  /** LAN 首次初始化 Owner。它返回的是**用户**而不是会话，必须再调用 login。 */
  initializeLanOwner: (input: LanOwnerInput) => Promise<void>;
  login: (input: LoginInput) => Promise<void>;
  logout: () => Promise<void>;
  isPending: boolean;
  error: unknown;
  reset: () => void;
}

/**
 * 认证动作。
 *
 * 每个动作成功后都会：清空非 bootstrap 的查询缓存（旧数据属于上一个主体），然后重取
 * bootstrap（拿到新的 CSRF 与新的 effectiveCapabilities）。这是契约要求，不是优化。
 */
export function useAuthActions(): AuthActions {
  const { csrfToken, refresh } = useSession();
  const queryClient = useQueryClient();

  const afterAuthChange = useCallback(async () => {
    queryClient.removeQueries({ predicate: (query) => query.queryKey[0] !== 'bootstrap' });
    await refresh();
  }, [queryClient, refresh]);

  const mutation = useMutation({
    mutationFn: async (action: () => Promise<void>) => action(),
    onSettled: afterAuthChange
  });

  const run = mutation.mutateAsync;
  // 按 token 值记忆：csrfHeaders 每次都返回新对象，不记忆会让下面四个回调每次渲染都变身份，
  // 调用方一旦把它们放进 useEffect 依赖就会无限触发。
  const header = useMemo(() => csrfHeaders(csrfToken), [csrfToken]);

  const pairPersonal = useCallback(
    async () =>
      run(async () => {
        const attempt = expectData(
          await api.POST('/api/v1/personal/pairing-attempts', { params: { header } })
        );
        expectData(
          await api.POST('/api/v1/personal/pair', {
            params: { header },
            body: { credential: attempt.credential }
          })
        );
      }),
    [run, header]
  );

  const initializeLanOwner = useCallback(
    async (input: LanOwnerInput) =>
      run(async () => {
        expectData(await api.POST('/api/v1/lan/owner', { params: { header }, body: input }));
      }),
    [run, header]
  );

  const login = useCallback(
    async (input: LoginInput) =>
      run(async () => {
        expectData(
          await api.POST('/api/v1/auth/login', {
            params: { header },
            body: { clientLabel: 'Gallery Web', ...input }
          })
        );
      }),
    [run, header]
  );

  const logout = useCallback(
    async () =>
      run(async () => {
        expectNoContent(await api.POST('/api/v1/auth/logout', { params: { header } }));
      }),
    [run, header]
  );

  return {
    pairPersonal,
    initializeLanOwner,
    login,
    logout,
    isPending: mutation.isPending,
    error: mutation.error,
    reset: mutation.reset
  };
}

/**
 * 未认证时的门禁。已认证渲染 children，否则渲染 signIn。
 *
 * 两端都要用它：管理端不是「登录后的画廊」，它有独立入口，但认证语义完全相同。
 */
export function AuthGate({ children, signIn }: { children: ReactNode; signIn: ReactNode }) {
  const { authenticated } = useSession();
  return <>{authenticated ? children : signIn}</>;
}

/** 登出按钮。两端共用，避免各写一套忘记刷新 CSRF 的版本。 */
export function SignOutButton({ label = '退出登录' }: { label?: string }) {
  const { logout, isPending } = useAuthActions();
  return (
    <Button variant="ghost" isPending={isPending} onPress={() => void logout()}>
      {label}
    </Button>
  );
}
