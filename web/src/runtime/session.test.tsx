/*
 * 会话契约。
 *
 * 最关键的一条：**认证状态变化后 CSRF 必须刷新**。认证前后是两个不同的 token，继续用旧
 * token 发变更会收到 CSRF_INVALID；因此每个认证动作成功后都必须重新拉 bootstrap。
 */

import { QueryClientProvider, useMutation, useQuery } from '@tanstack/react-query';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it } from 'vitest';
import type { ReactNode } from 'react';
import { faultResponse, jsonResponse, setFetchHandler } from '../../tests/http';
import { api, csrfHeaders, expectData, expectNoContent } from '../api/client';
import { createQueryClient } from './query';
import {
  SessionProvider,
  useAnyCapability,
  useAuthActions,
  useCapability,
  useCsrfHeaders,
  useSession
} from './session';

const ANONYMOUS = {
  mode: 'personal',
  authenticated: false,
  lanInitialized: false,
  availableCapabilities: ['library.read', 'media.read'],
  effectiveCapabilities: [],
  csrfToken: 'csrf-anonymous-000000000000000000000000',
  apiVersion: 'v1',
  websocketProtocolVersion: 1,
  sortProtocolVersion: 2,
  ruleSchemaVersion: 1
};

const AUTHENTICATED = {
  ...ANONYMOUS,
  authenticated: true,
  principalId: 'principal_1',
  // 故意混入一个后端可能新增、前端词表尚未收录的名字，验证它被过滤而不是被当作已知能力。
  effectiveCapabilities: ['library.read', 'media.read', 'capability.from.the.future'],
  csrfToken: 'csrf-authenticated-11111111111111111111'
};

interface Recorded {
  path: string;
  csrf: string | null;
}

let bootstrapBody: unknown = ANONYMOUS;
let recorded: Recorded[] = [];
let bootstrapStatus = 200;

beforeEach(() => {
  bootstrapBody = ANONYMOUS;
  bootstrapStatus = 200;
  recorded = [];
  setFetchHandler((request) => {
    const path = new URL(request.url).pathname;
    const csrf = request.headers.get('X-Gallery-CSRF');
    recorded.push({ path, csrf });
    switch (path) {
      case '/api/v1/bootstrap':
        return bootstrapStatus === 200
          ? jsonResponse(bootstrapBody)
          : faultResponse('HOST_REJECTED', bootstrapStatus, 'corr-boot');
      case '/api/v1/auth/login':
        bootstrapBody = AUTHENTICATED;
        return jsonResponse({ principalId: 'principal_1' }, 201);
      case '/api/v1/auth/logout':
        bootstrapBody = ANONYMOUS;
        return new Response(null, { status: 204 });
      case '/api/v1/personal/pairing-attempts':
        return jsonResponse({ credential: 'pairing-credential' }, 201);
      case '/api/v1/personal/pair':
        bootstrapBody = AUTHENTICATED;
        return jsonResponse({ principalId: 'principal_1' }, 201);
      case '/api/v1/lan/owner':
        return jsonResponse({ id: 'usr_1' }, 201);
      default:
        return faultResponse('NOT_FOUND', 404);
    }
  });
});

function Wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={createQueryClient()}>{children}</QueryClientProvider>;
}

function Probe() {
  const { csrfToken, authenticated, mode, capabilities } = useSession();
  const canRead = useCapability('library.read');
  const canWriteOrScan = useAnyCapability(['rules.write', 'scan.run']);
  const headers = useCsrfHeaders();
  const { login, logout, pairPersonal, isPending } = useAuthActions();
  return (
    <div>
      <span data-testid="csrf">{csrfToken}</span>
      <span data-testid="header-csrf">{headers['X-Gallery-CSRF']}</span>
      <span data-testid="authenticated">{String(authenticated)}</span>
      <span data-testid="mode">{mode}</span>
      <span data-testid="capability-count">{capabilities.size}</span>
      <span data-testid="can-read">{String(canRead)}</span>
      <span data-testid="can-write-or-scan">{String(canWriteOrScan)}</span>
      <span data-testid="pending">{String(isPending)}</span>
      <button onClick={() => void login({ username: 'owner', password: 'secret' })}>登录</button>
      <button onClick={() => void logout()}>登出</button>
      <button onClick={() => void pairPersonal()}>配对</button>
    </div>
  );
}

function renderSession() {
  return render(
    <Wrapper>
      <SessionProvider>
        <Probe />
      </SessionProvider>
    </Wrapper>
  );
}

function InvalidatedQueryProbe() {
  const { authenticated } = useSession();
  const query = useQuery({
    queryKey: ['protected-probe'],
    queryFn: async () => expectData(await api.GET('/api/v1/libraries')),
    enabled: authenticated
  });
  return (
    <div>
      <span data-testid="invalidated-authenticated">{String(authenticated)}</span>
      <span data-testid="invalidated-query-state">{query.status}</span>
    </div>
  );
}

function InvalidatedMutationProbe() {
  const { authenticated, csrfToken } = useSession();
  const mutation = useMutation({
    mutationFn: async () =>
      expectNoContent(await api.POST('/api/v1/auth/logout', { params: { header: csrfHeaders(csrfToken) } }))
  });
  return (
    <div>
      <span data-testid="mutation-authenticated">{String(authenticated)}</span>
      <button onClick={() => mutation.mutate()}>触发失效变更</button>
    </div>
  );
}

describe('SessionProvider', () => {
  it('bootstrap 是唯一状态来源', async () => {
    renderSession();
    await screen.findByTestId('csrf');
    expect(screen.getByTestId('authenticated')).toHaveTextContent('false');
    expect(screen.getByTestId('mode')).toHaveTextContent('personal');
    expect(screen.getByTestId('csrf')).toHaveTextContent(ANONYMOUS.csrfToken);
  });

  it('bootstrap 失败时给出可重试的错误态而不是白屏', async () => {
    // 用 403 HOST_REJECTED：它是确定性失败，服务端声明不可重试，因此查询层不会退避重试，
    // 错误态会立刻呈现。这正是「Host 白名单拒绝」在真实部署里的表现。
    bootstrapStatus = 403;
    renderSession();
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Host 不在允许列表内');
    expect(alert).toHaveTextContent('HOST_REJECTED');
    expect(alert).toHaveTextContent('corr-boot');
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument();
  });

  it('登录后重新拉取 bootstrap 并换用新的 CSRF token', async () => {
    renderSession();
    await screen.findByTestId('csrf');
    expect(screen.getByTestId('csrf')).toHaveTextContent(ANONYMOUS.csrfToken);

    await act(async () => {
      await userEvent.click(screen.getByRole('button', { name: '登录' }));
    });

    await waitFor(() => {
      expect(screen.getByTestId('authenticated')).toHaveTextContent('true');
    });
    // 认证前后是两个不同的 token：会话必须换到新的那个，否则下一次变更会 CSRF_INVALID。
    expect(screen.getByTestId('csrf')).toHaveTextContent(AUTHENTICATED.csrfToken);
    expect(screen.getByTestId('header-csrf')).toHaveTextContent(AUTHENTICATED.csrfToken);

    const login = recorded.find((entry) => entry.path === '/api/v1/auth/login');
    // 登录请求本身用的是认证**前**的 token，这也是契约的一部分。
    expect(login?.csrf).toBe(ANONYMOUS.csrfToken);
    // 登录之后必须至少再拉一次 bootstrap。
    const bootstrapCalls = recorded.filter((entry) => entry.path === '/api/v1/bootstrap');
    expect(bootstrapCalls.length).toBeGreaterThanOrEqual(2);
  });

  it('登出后 CSRF 回到未认证 token', async () => {
    renderSession();
    await screen.findByTestId('csrf');
    await act(async () => {
      await userEvent.click(screen.getByRole('button', { name: '登录' }));
    });
    await waitFor(() => {
      expect(screen.getByTestId('authenticated')).toHaveTextContent('true');
    });

    await act(async () => {
      await userEvent.click(screen.getByRole('button', { name: '登出' }));
    });
    await waitFor(() => {
      expect(screen.getByTestId('authenticated')).toHaveTextContent('false');
    });
    expect(screen.getByTestId('csrf')).toHaveTextContent(ANONYMOUS.csrfToken);

    const logout = recorded.find((entry) => entry.path === '/api/v1/auth/logout');
    expect(logout?.csrf).toBe(AUTHENTICATED.csrfToken);
  });

  it('Personal 配对两步都带 CSRF，并在成功后刷新 bootstrap', async () => {
    renderSession();
    await screen.findByTestId('csrf');
    await act(async () => {
      await userEvent.click(screen.getByRole('button', { name: '配对' }));
    });
    await waitFor(() => {
      expect(screen.getByTestId('authenticated')).toHaveTextContent('true');
    });
    expect(recorded.find((entry) => entry.path === '/api/v1/personal/pairing-attempts')?.csrf).toBe(
      ANONYMOUS.csrfToken
    );
    expect(recorded.find((entry) => entry.path === '/api/v1/personal/pair')?.csrf).toBe(ANONYMOUS.csrfToken);
    expect(screen.getByTestId('csrf')).toHaveTextContent(AUTHENTICATED.csrfToken);
  });

  it('任一查询返回 UNAUTHENTICATED 时撤下旧主体缓存并重新拉取 bootstrap', async () => {
    bootstrapBody = AUTHENTICATED;
    let releaseProtected: (() => void) | undefined;
    setFetchHandler((request) => {
      const path = new URL(request.url).pathname;
      recorded.push({ path, csrf: request.headers.get('X-Gallery-CSRF') });
      if (path === '/api/v1/bootstrap') return jsonResponse(bootstrapBody);
      if (path === '/api/v1/libraries') {
        return new Promise<Response>((resolve) => {
          releaseProtected = () => {
            bootstrapBody = ANONYMOUS;
            resolve(faultResponse('UNAUTHENTICATED', 401));
          };
        });
      }
      return faultResponse('NOT_FOUND', 404);
    });
    const queryClient = createQueryClient();
    queryClient.setQueryData(['sensitive-cache'], { owner: 'principal_1' });
    render(
      <QueryClientProvider client={queryClient}>
        <SessionProvider>
          <InvalidatedQueryProbe />
        </SessionProvider>
      </QueryClientProvider>
    );

    expect(await screen.findByTestId('invalidated-authenticated')).toHaveTextContent('true');
    expect(releaseProtected).toBeTypeOf('function');
    act(() => releaseProtected?.());

    await waitFor(() => {
      expect(screen.getByTestId('invalidated-authenticated')).toHaveTextContent('false');
    });
    expect(queryClient.getQueryData(['sensitive-cache'])).toBeUndefined();
    expect(recorded.filter((entry) => entry.path === '/api/v1/bootstrap')).toHaveLength(2);
  });

  it('任一变更返回 CSRF_INVALID 时也重新确认会话', async () => {
    bootstrapBody = AUTHENTICATED;
    setFetchHandler((request) => {
      const path = new URL(request.url).pathname;
      recorded.push({ path, csrf: request.headers.get('X-Gallery-CSRF') });
      if (path === '/api/v1/bootstrap') return jsonResponse(bootstrapBody);
      if (path === '/api/v1/auth/logout') {
        bootstrapBody = ANONYMOUS;
        return faultResponse('CSRF_INVALID', 403);
      }
      return faultResponse('NOT_FOUND', 404);
    });
    render(
      <Wrapper>
        <SessionProvider>
          <InvalidatedMutationProbe />
        </SessionProvider>
      </Wrapper>
    );

    expect(await screen.findByTestId('mutation-authenticated')).toHaveTextContent('true');
    await userEvent.click(screen.getByRole('button', { name: '触发失效变更' }));
    await waitFor(() => {
      expect(screen.getByTestId('mutation-authenticated')).toHaveTextContent('false');
    });
    expect(recorded.filter((entry) => entry.path === '/api/v1/bootstrap')).toHaveLength(2);
  });
});

describe('capability', () => {
  it('只接受后端权威词表中的名字，未知名字被丢弃', async () => {
    renderSession();
    await screen.findByTestId('csrf');
    await act(async () => {
      await userEvent.click(screen.getByRole('button', { name: '登录' }));
    });
    await waitFor(() => {
      expect(screen.getByTestId('authenticated')).toHaveTextContent('true');
    });
    // 服务端返回 3 个名字，其中 capability.from.the.future 不在前端词表内。
    expect(screen.getByTestId('capability-count')).toHaveTextContent('2');
    expect(screen.getByTestId('can-read')).toHaveTextContent('true');
    expect(screen.getByTestId('can-write-or-scan')).toHaveTextContent('false');
  });

  it('未认证时没有任何有效 capability', async () => {
    renderSession();
    await screen.findByTestId('csrf');
    expect(screen.getByTestId('capability-count')).toHaveTextContent('0');
    expect(screen.getByTestId('can-read')).toHaveTextContent('false');
  });
});
