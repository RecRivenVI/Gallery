/*
 * 管理端的契约回归。
 *
 * 这些用例断言的都是「界面有没有说实话」，不是像素：
 *
 * 1. capability 通过不等于有权：403 与 404 都必须得体呈现，且不得伪装成空列表；
 * 2. 任务状态以 HTTP 快照为准：WebSocket 事件只触发重取，绝不直接改本地状态；
 * 3. index 档案被 409 拒绝时如实报告，且**不会**偷偷改成 incremental 重发；
 * 4. 草稿保存带 If-Match，冲突时不覆盖别人的编辑；
 * 5. 分享密文只显示一次，关闭后界面里没有任何再次查看的入口。
 */

import { QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it } from 'vitest';
import { CAPABILITIES } from '../auth/capabilities';
import { createQueryClient } from '../shared/query';
import { RealtimeProvider, type RealtimeHandlers, type RealtimeTransport } from '../shared/realtime';
import { SessionProvider } from '../shared/session';
import { ThemeProvider } from '../shared/theme';
import { ToastProvider } from '../design';
import { faultResponse, jsonResponse, setFetchHandler } from '../../tests/http';
import { ManageApp } from './app';

/* ————————————————————————————— HTTP 桩 ————————————————————————————— */

type RouteHandler = (request: Request, url: URL) => Response;

interface RecordedRequest {
  method: string;
  path: string;
  ifMatch: string | null;
  idempotencyKey: string | null;
}

let routes: Map<string, RouteHandler>;
let recorded: RecordedRequest[];

const BOOTSTRAP = {
  mode: 'personal',
  authenticated: true,
  lanInitialized: false,
  availableCapabilities: [...CAPABILITIES],
  principalId: 'principal_manage',
  effectiveCapabilities: [...CAPABILITIES],
  csrfToken: 'csrf-manage-000000000000000000000000',
  apiVersion: 'v1',
  websocketProtocolVersion: 1,
  sortProtocolVersion: 1,
  ruleSchemaVersion: 1
};

function route(key: string, handler: RouteHandler): void {
  routes.set(key, handler);
}

function requestsTo(key: string): RecordedRequest[] {
  return recorded.filter((entry) => `${entry.method} ${entry.path}` === key);
}

beforeEach(() => {
  routes = new Map();
  recorded = [];
  route('GET /api/v1/bootstrap', () => jsonResponse(BOOTSTRAP));
  setFetchHandler((request) => {
    const url = new URL(request.url);
    recorded.push({
      method: request.method,
      path: url.pathname,
      ifMatch: request.headers.get('If-Match'),
      idempotencyKey: request.headers.get('Idempotency-Key')
    });
    const handler = routes.get(`${request.method} ${url.pathname}`);
    if (handler === undefined) return faultResponse('NOT_FOUND', 404, 'corr-unrouted');
    return handler(request, url);
  });
});

/* ————————————————————————————— 渲染 ————————————————————————————— */

let sockets: RealtimeHandlers[] = [];

const fakeTransport: RealtimeTransport = (_url, handlers) => {
  sockets.push(handlers);
  queueMicrotask(() => handlers.onOpen());
  return { close: () => undefined };
};

function renderManage(path: string, options: { realtime?: boolean } = {}) {
  sockets = [];
  const tree = (
    <QueryClientProvider client={createQueryClient()}>
      <ThemeProvider surface="manage">
        <ToastProvider>
          <MemoryRouter initialEntries={[path]}>
            <SessionProvider>
              {options.realtime === true ? (
                <RealtimeProvider transport={fakeTransport} url="ws://manage.test/ws/v1">
                  <ManageApp />
                </RealtimeProvider>
              ) : (
                <ManageApp />
              )}
            </SessionProvider>
          </MemoryRouter>
        </ToastProvider>
      </ThemeProvider>
    </QueryClientProvider>
  );
  return render(tree);
}

/** 打开一个 react-aria Select 并选中某个选项。 */
async function selectOption(selectLabel: RegExp, optionName: string): Promise<void> {
  const trigger = screen.getByRole('button', { name: selectLabel });
  await act(async () => {
    await userEvent.click(trigger);
  });
  const option = await screen.findByRole('option', { name: optionName });
  await act(async () => {
    await userEvent.click(option);
  });
}

/* ————————————————————————————— 夹具 ————————————————————————————— */

function job(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: 'job_01SCAN',
    type: 'scan',
    sourceId: 'src_01',
    status: 'running',
    stage: 'discover',
    progress: { current: 3, total: 10, sequence: 7 },
    attempt: 1,
    createdAt: '2026-07-27T01:00:00Z',
    updatedAt: '2026-07-27T01:02:00Z',
    scanProfile: 'incremental',
    ...overrides
  };
}

const SOURCE = {
  id: 'src_01',
  libraryId: 'lib_01',
  displayName: '合成来源',
  readOnly: true,
  available: true,
  createdAt: '2026-07-20T00:00:00Z'
};

/* ————————————————————————————— 1. capability 不是授权判断 ————————————————————————————— */

describe('capability 不能当作授权判断', () => {
  it('capability 齐全但服务端返回 403 / 404 时，页面给出无权与不存在的得体呈现', async () => {
    // bootstrap 声明主体拥有全部 capability——这正是最危险的情况：界面若把 capability
    // 当成授权，就会认为「不可能失败」，于是把 403 渲染成空白或空列表。
    route('GET /api/v1/jobs', () => faultResponse('FORBIDDEN', 403, 'corr-jobs'));
    route('GET /api/v1/sources', () => faultResponse('NOT_FOUND', 404, 'corr-sources'));

    renderManage('/scans');

    const forbidden = await screen.findByText('没有执行此操作的权限');
    expect(forbidden).toBeInTheDocument();
    expect(screen.getAllByText(/FORBIDDEN/).length).toBeGreaterThan(0);
    expect(screen.getAllByText(/corr-jobs/).length).toBeGreaterThan(0);

    // 404 必须表达成「不存在或无权查看」，因为服务端会把部分 FORBIDDEN 伪装成 404。
    expect(screen.getAllByText('不存在，或当前账户无权查看').length).toBeGreaterThan(0);
    expect(screen.getAllByText(/corr-sources/).length).toBeGreaterThan(0);

    // 不得退化成「空」：空状态文案不能出现。
    expect(screen.queryByText('这一段快照里没有任务')).not.toBeInTheDocument();
    expect(screen.queryByText('还没有登记任何来源')).not.toBeInTheDocument();
  });

  it('缺少任务变更 capability 时隐藏取消与重试入口，而不是渲染一个必然失败的按钮', async () => {
    route('GET /api/v1/bootstrap', () =>
      // 只保留读权限：scan.run / media.derive / overlays.write 全部缺席。
      jsonResponse({ ...BOOTSTRAP, effectiveCapabilities: ['library.read', 'media.read'] })
    );
    route('GET /api/v1/sources', () => jsonResponse({ sources: [SOURCE] }));
    route('GET /api/v1/jobs', () => jsonResponse({ jobs: [job()] }));

    renderManage('/scans');

    // 等的是真正的任务行，而不是筛选下拉里同名的选项文本。
    await screen.findByRole('link', { name: 'job_01SCAN' });
    expect(screen.queryByRole('button', { name: '取消' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '重试' })).not.toBeInTheDocument();
    expect(screen.getAllByText('无变更入口').length).toBeGreaterThan(0);
  });
});

/* ————————————————————————————— 2. 任务状态以 HTTP 快照为准 ————————————————————————————— */

describe('任务状态的事实源', () => {
  it('WebSocket 的 job.completed 只触发重新拉取快照，不直接改变本地状态', async () => {
    let jobsFetches = 0;
    route('GET /api/v1/sources', () => jsonResponse({ sources: [SOURCE] }));
    route('GET /api/v1/jobs', () => {
      jobsFetches += 1;
      // 快照坚持说这个任务还在执行中。事件声称它已完成——以快照为准。
      return jsonResponse({ jobs: [job({ status: 'running' })] });
    });

    renderManage('/scans', { realtime: true });
    const link = await screen.findByRole('link', { name: 'job_01SCAN' });
    const row = link.closest('tr');
    expect(row).not.toBeNull();
    expect(within(row as HTMLElement).getByText('执行中')).toBeInTheDocument();
    await waitFor(() => {
      expect(sockets.length).toBeGreaterThan(0);
    });
    const before = jobsFetches;

    const socket = sockets[0];
    expect(socket).toBeDefined();
    act(() => {
      socket?.onMessage(
        JSON.stringify({
          protocolVersion: 1,
          eventType: 'job.completed',
          sequence: 1,
          scope: { jobId: 'job_01SCAN' },
          payload: { jobId: 'job_01SCAN', status: 'completed' },
          serverTime: '2026-07-27T01:03:00Z'
        })
      );
    });

    // 事件必须导致一次新的 HTTP 快照请求……
    await waitFor(() => {
      expect(jobsFetches).toBeGreaterThan(before);
    });
    // ……而界面显示的仍然是快照说的「执行中」，不是事件 payload 里的 completed。
    const currentRow = screen.getByRole('link', { name: 'job_01SCAN' }).closest('tr');
    expect(within(currentRow as HTMLElement).getByText('执行中')).toBeInTheDocument();
    expect(within(currentRow as HTMLElement).queryByText('已完成')).not.toBeInTheDocument();
  });
});

/* ————————————————————————————— 3. index 档案被 409 拒绝 ————————————————————————————— */

describe('扫描档案', () => {
  it('index 被 409 拒绝时如实解释，并且不会改成 incremental 重发', async () => {
    route('GET /api/v1/sources', () => jsonResponse({ sources: [SOURCE] }));
    route('GET /api/v1/jobs', () => jsonResponse({ jobs: [] }));
    route('GET /api/v1/sources/src_01/scan-status', () =>
      jsonResponse({
        sourceId: 'src_01',
        status: 'online',
        dirty: false,
        watcherAvailable: true,
        watcherOverflow: false,
        pendingHashCount: 0,
        updatedAt: '2026-07-27T01:00:00Z'
      })
    );
    let postBodies: string[] = [];
    route('POST /api/v1/sources/src_01/scan-jobs', () => {
      postBodies.push('called');
      return faultResponse('CONFLICT', 409, 'corr-index');
    });
    postBodies = [];

    renderManage('/scans');
    await screen.findByRole('button', { name: /发起扫描/ });

    await selectOption(/来源/, '合成来源');
    await selectOption(/扫描档案/, 'index（仅首次扫描）');

    await act(async () => {
      await userEvent.click(screen.getByRole('button', { name: /发起扫描/ }));
    });

    // 「本界面不会替你改成 incremental」这句只出现在失败解释里，契约说明用的是另一套措辞，
    // 因此它能唯一标识这次 409 被如实解释过。
    await waitFor(() => {
      expect(screen.getAllByText(/本界面不会替你改成 incremental/).length).toBe(1);
    });

    // 只发出一次请求：没有任何「换个档案再试一次」的隐式行为。
    const posts = requestsTo('POST /api/v1/sources/src_01/scan-jobs');
    expect(posts).toHaveLength(1);
    // 幂等键必须存在，否则网络重发会产生第二个扫描 Job。
    expect(posts[0]?.idempotencyKey).toBeTruthy();
  });
});

/* ————————————————————————————— 4. 草稿 If-Match 冲突 ————————————————————————————— */

describe('规则草稿', () => {
  it('保存带 If-Match 修订号，冲突时报告 RULE_DRAFT_CONFLICT 且不覆盖服务端内容', async () => {
    route('GET /api/v1/rule-packages/pkg_01', () =>
      jsonResponse({
        id: 'pkg_01',
        ruleSetId: 'ruleset.synthetic',
        name: '合成规则包',
        description: '',
        status: 'active',
        createdBy: 'principal_manage',
        revision: 5,
        createdAt: '2026-07-20T00:00:00Z',
        updatedAt: '2026-07-26T00:00:00Z'
      })
    );
    route('GET /api/v1/rule-packages/pkg_01/draft', () =>
      jsonResponse({
        id: 'draft_01',
        packageId: 'pkg_01',
        content: { version: 1 },
        format: 'json',
        validationStatus: 'draft',
        diagnostics: [],
        revision: 3,
        savedBy: 'principal_manage',
        createdAt: '2026-07-26T00:00:00Z',
        updatedAt: '2026-07-26T01:00:00Z'
      })
    );
    route('GET /api/v1/rule-packages/pkg_01/versions', () => jsonResponse({ items: [] }));
    route('GET /api/v1/rule-packages/pkg_01/audits', () => jsonResponse({ items: [] }));
    route('PUT /api/v1/rule-packages/pkg_01/draft', () =>
      faultResponse('RULE_DRAFT_CONFLICT', 409, 'corr-draft')
    );

    renderManage('/rules/pkg_01');

    const editor = await screen.findByRole('textbox', { name: /草稿内容/ });
    act(() => {
      fireEvent.change(editor, { target: { value: '{"version":2}' } });
    });
    await act(async () => {
      await userEvent.click(screen.getByRole('button', { name: /保存草稿/ }));
    });

    await screen.findByText('草稿已被其他会话修改');
    expect(screen.getAllByText(/RULE_DRAFT_CONFLICT/).length).toBeGreaterThan(0);

    const puts = requestsTo('PUT /api/v1/rule-packages/pkg_01/draft');
    expect(puts).toHaveLength(1);
    // If-Match 必须是 GET 草稿返回的 revision（3），而不是规则包的 revision（5）。
    expect(puts[0]?.ifMatch).toBe('"3"');

    // 编辑器仍保留用户的内容：界面绝不静默丢弃它，也不自动覆盖服务端。
    expect(screen.getByRole('textbox', { name: /草稿内容/ })).toHaveValue('{"version":2}');
  });
});

/* ————————————————————————————— 5. 密文只显示一次 ————————————————————————————— */

describe('分享密文', () => {
  it('创建后只显示一次，关闭对话框即从界面消失且没有再次查看的入口', async () => {
    const secret = 'share_abcdefghijklmnopqrstuvwxyz012345';
    route('GET /api/v1/sessions', () => jsonResponse({ sessions: [] }));
    route('GET /api/v1/shares', () => jsonResponse({ shares: [] }));
    route('POST /api/v1/shares', () =>
      jsonResponse(
        {
          id: 'share_01',
          createdBy: 'principal_manage',
          scopeKind: 'work',
          scopeId: 'work_01',
          permissions: ['view'],
          secretPrefix: 'share_abcd',
          createdAt: '2026-07-27T01:00:00Z',
          expiresAt: '2026-07-28T01:00:00Z',
          revoked: false,
          secret
        },
        201
      )
    );

    renderManage('/security');

    await act(async () => {
      await userEvent.click(await screen.findByRole('tab', { name: '分享' }));
    });

    const target = await screen.findByRole('textbox', { name: /目标 ID/ });
    act(() => {
      fireEvent.change(target, { target: { value: 'work_01' } });
    });
    await act(async () => {
      await userEvent.click(screen.getByRole('button', { name: /创建分享/ }));
    });

    const dialog = await screen.findByRole('dialog', { name: '分享 credential' });
    expect(within(dialog).getByTestId('one-time-secret')).toHaveTextContent(secret);

    await act(async () => {
      await userEvent.click(within(dialog).getByRole('button', { name: '我已保存，关闭' }));
    });

    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
    // 密文彻底离开 DOM，并且界面上没有任何「再看一次」的入口。
    expect(screen.queryByText(secret)).not.toBeInTheDocument();
    expect(screen.queryByTestId('one-time-secret')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /查看密文/ })).not.toBeInTheDocument();
    // 也不会为了再显示一次而重新请求服务端。
    expect(requestsTo('POST /api/v1/shares')).toHaveLength(1);
  });
});
