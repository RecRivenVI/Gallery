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

type RouteHandler = (request: Request, url: URL) => Response | Promise<Response>;

interface RecordedRequest {
  method: string;
  path: string;
  ifMatch: string | null;
  idempotencyKey: string | null;
  csrf: string | null;
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
  sortProtocolVersion: 2,
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
  route('GET /api/v1/libraries', () => jsonResponse({ libraries: [] }));
  setFetchHandler((request) => {
    const url = new URL(request.url);
    recorded.push({
      method: request.method,
      path: url.pathname,
      ifMatch: request.headers.get('If-Match'),
      idempotencyKey: request.headers.get('Idempotency-Key'),
      csrf: request.headers.get('X-Gallery-CSRF')
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

const OLD_RULE_HASH = 'a'.repeat(64);
const NEW_RULE_HASH = 'b'.repeat(64);

function rulePackage(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: 'pkg_01',
    ruleSetId: 'ruleset.synthetic',
    name: '合成规则包',
    description: '',
    status: 'active',
    createdBy: 'principal_manage',
    revision: 5,
    createdAt: '2026-07-20T00:00:00Z',
    updatedAt: '2026-07-26T00:00:00Z',
    ...overrides
  };
}

function ruleDraft(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: 'draft_01',
    packageId: 'pkg_01',
    content: { version: 1 },
    format: 'json',
    validationStatus: 'draft',
    diagnostics: [],
    revision: 3,
    savedBy: 'principal_manage',
    createdAt: '2026-07-26T00:00:00Z',
    updatedAt: '2026-07-26T01:00:00Z',
    ...overrides
  };
}

function ruleVersion(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: 'version_01',
    packageId: 'pkg_01',
    ruleSetId: 'ruleset.synthetic',
    version: '1.0.0',
    packageHash: 'c'.repeat(64),
    semanticHash: NEW_RULE_HASH,
    ruleIrHash: 'd'.repeat(64),
    status: 'published',
    createdBy: 'principal_manage',
    publishedAt: '2026-07-27T05:00:00Z',
    createdAt: '2026-07-27T05:00:00Z',
    executable: true,
    ...overrides
  };
}

function ruleImpact(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    category: 'NO_ACTION',
    reasonCodes: [],
    fields: [],
    actions: [],
    affectedSources: [],
    entityTypes: [],
    blockPublish: false,
    manualConfirmation: false,
    fullRescan: false,
    partialRescan: false,
    reproject: false,
    rebuildSearch: false,
    rebuildDerived: false,
    bindingReview: false,
    ...overrides
  };
}

/* ————————————————————————————— 0. 新实例资源自举 ————————————————————————————— */

describe('Library 与 Source 自举', () => {
  it('名称长度按 Unicode code point 计算，并在创建未完成时锁定输入', async () => {
    let complete: ((response: Response) => void) | undefined;
    route(
      'POST /api/v1/libraries',
      () =>
        new Promise<Response>((resolve) => {
          complete = resolve;
        })
    );
    route('GET /api/v1/sources', () => jsonResponse({ sources: [] }));
    route('GET /api/v1/jobs', () => jsonResponse({ jobs: [] }));

    renderManage('/scans');
    const input = await screen.findByRole('textbox', { name: /Library 名称/ });
    const submit = screen.getByRole('button', { name: '创建 Library' });

    fireEvent.change(input, { target: { value: '😀'.repeat(256) } });
    expect(submit).not.toBeDisabled();
    fireEvent.change(input, { target: { value: '😀'.repeat(257) } });
    expect(screen.getByText('名称不能超过 256 个字符')).toBeInTheDocument();
    expect(submit).toBeDisabled();

    fireEvent.change(input, { target: { value: '待完成创建' } });
    await userEvent.click(submit);
    await waitFor(() => {
      expect(input).toBeDisabled();
    });
    act(() => {
      complete?.(
        jsonResponse({ id: 'lib_pending', name: '待完成创建', createdAt: '2026-07-27T02:00:00Z' }, 201)
      );
    });
    await waitFor(() => {
      expect(input).toHaveValue('');
      expect(input).not.toBeDisabled();
    });
    expect(requestsTo('POST /api/v1/libraries')).toHaveLength(1);
  });

  it('Source 创建未完成时锁定所属 Library、显示名和根路径', async () => {
    let complete: ((response: Response) => void) | undefined;
    route('GET /api/v1/libraries', () =>
      jsonResponse({ libraries: [{ id: 'lib_01', name: '资料库', createdAt: '2026-07-27T02:00:00Z' }] })
    );
    route('GET /api/v1/sources', () => jsonResponse({ sources: [] }));
    route('GET /api/v1/jobs', () => jsonResponse({ jobs: [] }));
    route(
      'POST /api/v1/sources',
      () =>
        new Promise<Response>((resolve) => {
          complete = resolve;
        })
    );

    renderManage('/scans');
    await screen.findByRole('textbox', { name: /Source 显示名/ });
    await selectOption(/所属 Library/, '资料库 · lib_01');
    const library = screen.getByRole('button', { name: /所属 Library/ });
    const name = screen.getByRole('textbox', { name: /Source 显示名/ });
    const root = screen.getByRole('textbox', { name: /Source 根路径/ });
    await userEvent.type(name, '待完成来源');
    await userEvent.type(root, 'D:\\Pending');
    await userEvent.click(screen.getByRole('button', { name: '登记 Source' }));

    await waitFor(() => {
      expect(library).toBeDisabled();
      expect(name).toBeDisabled();
      expect(root).toBeDisabled();
    });
    act(() => {
      complete?.(
        jsonResponse(
          {
            ...SOURCE,
            displayName: '待完成来源'
          },
          201
        )
      );
    });
    await waitFor(() => {
      expect(name).toHaveValue('');
      expect(root).toHaveValue('');
      expect(name).not.toBeDisabled();
      expect(root).not.toBeDisabled();
    });
    expect(requestsTo('POST /api/v1/sources')).toHaveLength(1);
  });

  it('可以从空实例创建首个 Library、登记 Source，并且不把根路径回显到列表', async () => {
    const library = {
      id: 'lib_01',
      name: 'Pixiv 归档',
      createdAt: '2026-07-27T02:00:00Z'
    };
    let libraryItems: (typeof library)[] = [];
    let sourceItems: (typeof SOURCE)[] = [];
    let libraryBody: Promise<unknown> | undefined;
    let sourceBody: Promise<unknown> | undefined;

    route('GET /api/v1/libraries', () => jsonResponse({ libraries: libraryItems }));
    route('GET /api/v1/sources', () => jsonResponse({ sources: sourceItems }));
    route('GET /api/v1/jobs', () => jsonResponse({ jobs: [] }));
    route('POST /api/v1/libraries', (request) => {
      libraryBody = request.clone().json();
      libraryItems = [library];
      return jsonResponse(library, 201);
    });
    route('POST /api/v1/sources', (request) => {
      sourceBody = request.clone().json();
      sourceItems = [{ ...SOURCE, displayName: 'Pixiv' }];
      return jsonResponse(sourceItems[0], 201);
    });

    renderManage('/scans');
    await screen.findByText('还没有创建任何 Library');

    await userEvent.type(screen.getByRole('textbox', { name: /Library 名称/ }), '  Pixiv 归档  ');
    await userEvent.click(screen.getByRole('button', { name: '创建 Library' }));

    await waitFor(() => {
      expect(screen.getAllByText('Pixiv 归档').length).toBeGreaterThan(0);
    });
    expect(await libraryBody).toEqual({ name: 'Pixiv 归档' });
    expect(requestsTo('POST /api/v1/libraries')).toHaveLength(1);
    expect(requestsTo('POST /api/v1/libraries')[0]?.csrf).toBe(BOOTSTRAP.csrfToken);

    await selectOption(/所属 Library/, 'Pixiv 归档 · lib_01');
    await userEvent.type(screen.getByRole('textbox', { name: /Source 显示名/ }), 'Pixiv');
    const root = screen.getByRole('textbox', { name: /Source 根路径/ });
    await userEvent.type(root, 'D:\\ReadOnly\\Pixiv');
    await userEvent.click(screen.getByRole('button', { name: '登记 Source' }));

    await waitFor(() => {
      expect(screen.getAllByText('Pixiv').length).toBeGreaterThan(0);
    });
    expect(await sourceBody).toEqual({
      libraryId: 'lib_01',
      displayName: 'Pixiv',
      rootPath: 'D:\\ReadOnly\\Pixiv'
    });
    expect(requestsTo('POST /api/v1/sources')).toHaveLength(1);
    expect(requestsTo('POST /api/v1/sources')[0]?.csrf).toBe(BOOTSTRAP.csrfToken);
    expect(screen.queryByText('D:\\ReadOnly\\Pixiv')).not.toBeInTheDocument();
    expect(root).toHaveValue('');
  });

  it('Source 根重叠时保留输入、解释安全边界且不自动重试', async () => {
    route('GET /api/v1/libraries', () =>
      jsonResponse({ libraries: [{ id: 'lib_01', name: '资料库', createdAt: '2026-07-27T02:00:00Z' }] })
    );
    route('GET /api/v1/sources', () => jsonResponse({ sources: [] }));
    route('GET /api/v1/jobs', () => jsonResponse({ jobs: [] }));
    route('POST /api/v1/sources', () => faultResponse('SOURCE_ROOTS_OVERLAP', 409, 'corr-overlap'));

    renderManage('/scans');
    await screen.findByRole('textbox', { name: /Source 显示名/ });
    await selectOption(/所属 Library/, '资料库 · lib_01');
    await userEvent.type(screen.getByRole('textbox', { name: /Source 显示名/ }), '重叠来源');
    const root = screen.getByRole('textbox', { name: /Source 根路径/ });
    await userEvent.type(root, 'D:\\Media');
    await userEvent.click(screen.getByRole('button', { name: '登记 Source' }));

    await screen.findByText(/Source 不能与 Gallery 的 AppDirs 或其他 Source 互相包含/);
    expect(screen.getAllByText(/SOURCE_ROOTS_OVERLAP/).length).toBeGreaterThan(0);
    expect(screen.getAllByText(/corr-overlap/).length).toBeGreaterThan(0);
    expect(screen.getByRole('textbox', { name: /Source 显示名/ })).toHaveValue('重叠来源');
    expect(root).toHaveValue('D:\\Media');
    expect(requestsTo('POST /api/v1/sources')).toHaveLength(1);
  });

  it('没有 global library.write 时隐藏 Library 创建，但仍允许 scoped Source 请求由服务端裁决', async () => {
    route('GET /api/v1/bootstrap', () =>
      jsonResponse({ ...BOOTSTRAP, effectiveCapabilities: ['library.read'] })
    );
    route('GET /api/v1/libraries', () =>
      jsonResponse({
        libraries: [{ id: 'lib_01', name: 'Scoped 资料库', createdAt: '2026-07-27T02:00:00Z' }]
      })
    );
    route('GET /api/v1/sources', () => jsonResponse({ sources: [] }));
    route('GET /api/v1/jobs', () => jsonResponse({ jobs: [] }));
    route('POST /api/v1/sources', () => faultResponse('NOT_FOUND', 404, 'corr-scoped'));

    renderManage('/scans');
    await screen.findByRole('textbox', { name: /Source 显示名/ });
    expect(screen.queryByRole('button', { name: '创建 Library' })).not.toBeInTheDocument();

    await selectOption(/所属 Library/, 'Scoped 资料库 · lib_01');
    const name = screen.getByRole('textbox', { name: /Source 显示名/ });
    const root = screen.getByRole('textbox', { name: /Source 根路径/ });
    await userEvent.type(name, 'Scoped 来源');
    await userEvent.type(root, 'D:\\Scoped');
    await userEvent.click(screen.getByRole('button', { name: '登记 Source' }));

    await screen.findByText('不存在，或当前账户无权查看');
    expect(screen.getAllByText(/corr-scoped/).length).toBeGreaterThan(0);
    expect(name).toHaveValue('Scoped 来源');
    expect(root).toHaveValue('D:\\Scoped');
    expect(requestsTo('POST /api/v1/sources')).toHaveLength(1);
  });
});

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

    await selectOption(/来源/, '合成来源 · src_01');
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

/* ————————————————————————————— 4. 同名 Source 写入身份 ————————————————————————————— */

describe('同名 Source 写入身份', () => {
  const duplicateSources = [
    { ...SOURCE, id: 'src_a', displayName: '同名来源' },
    { ...SOURCE, id: 'src_b', displayName: '同名来源' }
  ];

  it('规则绑定在无 global rules.write 时仍按 Source scope 提交稳定 ID 和已发布版本', async () => {
    let body: Promise<unknown> | undefined;
    route('GET /api/v1/bootstrap', () =>
      jsonResponse({
        ...BOOTSTRAP,
        effectiveCapabilities: BOOTSTRAP.effectiveCapabilities.filter(
          (capability) => capability !== 'rules.write'
        )
      })
    );
    route('GET /api/v1/sources', () => jsonResponse({ sources: duplicateSources }));
    route('GET /api/v1/rule-packages', () =>
      jsonResponse({
        items: [
          rulePackage({ currentSemanticHash: OLD_RULE_HASH }),
          rulePackage({ id: 'pkg_unpublished', name: '未发布规则包', currentSemanticHash: '' })
        ]
      })
    );
    route('GET /api/v1/source-rule-bindings', () => jsonResponse({ bindings: [] }));
    route('GET /api/v1/sources/src_b/effective-rule-binding', () =>
      faultResponse('RULE_BINDING_NOT_MATCHED', 409, 'corr-effective')
    );
    route('POST /api/v1/source-rule-bindings', (request) => {
      body = request.clone().json();
      return jsonResponse(
        {
          id: 'binding_01',
          sourceId: 'src_b',
          semanticHash: 'a'.repeat(64),
          ruleIrHash: 'b'.repeat(64),
          parameters: {},
          priority: 100,
          status: 'active',
          createdAt: '2026-07-27T03:00:00Z'
        },
        201
      );
    });

    renderManage('/rules');
    await screen.findByRole('button', { name: /来源/ });
    await selectOption(/来源/, '同名来源 · src_b');
    await userEvent.click(screen.getByRole('button', { name: /已发布版本/ }));
    expect(screen.queryByRole('option', { name: /未发布规则包/ })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('option', { name: '合成规则包 · aaaaaaaaaaaa…' }));
    await userEvent.click(screen.getByRole('button', { name: '创建绑定' }));

    await waitFor(() => expect(requestsTo('POST /api/v1/source-rule-bindings')).toHaveLength(1));
    expect(await body).toEqual({
      sourceId: 'src_b',
      semanticHash: OLD_RULE_HASH,
      priority: 100,
      parameters: {}
    });
  });

  it('人工解绑确认框回显 Source ID，并把同一 ID 放入请求', async () => {
    let body: Promise<unknown> | undefined;
    route('GET /api/v1/sources', () => jsonResponse({ sources: duplicateSources }));
    route('GET /api/v1/binding-issues', () => jsonResponse({ issues: [] }));
    route('POST /api/v1/binding-actions/unbind-work', (request) => {
      body = request.clone().json();
      return jsonResponse({ entityKind: 'work', canonicalId: 'work_01' });
    });

    renderManage('/governance');
    await userEvent.click(await screen.findByRole('tab', { name: '人工解绑' }));
    await selectOption(/来源/, '同名来源 · src_b');
    await userEvent.type(screen.getByRole('textbox', { name: 'sourceKey' }), 'work/source-key');
    await userEvent.click(screen.getByRole('button', { name: '解绑作品' }));

    const dialog = await screen.findByRole('dialog', { name: '解绑作品' });
    expect(within(dialog).getByText(/目标 Source：同名来源（src_b）/)).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole('button', { name: '确认执行' }));

    await waitFor(() => expect(requestsTo('POST /api/v1/binding-actions/unbind-work')).toHaveLength(1));
    expect(await body).toEqual({ sourceId: 'src_b', sourceKey: 'work/source-key' });
  });
});

/* ————————————————————————————— 5. 草稿 If-Match 冲突 ————————————————————————————— */

describe('规则草稿', () => {
  it('保存带 If-Match 修订号，冲突时报告 RULE_DRAFT_CONFLICT 且不覆盖服务端内容', async () => {
    route('GET /api/v1/rule-packages/pkg_01', () => jsonResponse(rulePackage()));
    route('GET /api/v1/rule-packages/pkg_01/draft', () => jsonResponse(ruleDraft()));
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

    await screen.findByText('服务端草稿已经变化，本地编辑仍被保留');
    expect(screen.getAllByText(/RULE_DRAFT_CONFLICT/).length).toBeGreaterThan(0);

    const puts = requestsTo('PUT /api/v1/rule-packages/pkg_01/draft');
    expect(puts).toHaveLength(1);
    // If-Match 必须是 GET 草稿返回的 revision（3），而不是规则包的 revision（5）。
    expect(puts[0]?.ifMatch).toBe('"3"');

    // 编辑器仍保留用户的内容：界面绝不静默丢弃它，也不自动覆盖服务端。
    expect(screen.getByRole('textbox', { name: /草稿内容/ })).toHaveValue('{"version":2}');
  });

  it('首次草稿从 revision 0 保存、吸收校验 revision，并以 before=null 评估', async () => {
    let storedDraft: Record<string, unknown> | null = null;
    let saveBody: Promise<unknown> | undefined;
    let impactBody: Promise<unknown> | undefined;
    // 防御旧服务端把 optional digest 错误编码为空字符串；前端不得把它回送为 baseSemanticHash。
    route('GET /api/v1/rule-packages/pkg_01', () =>
      jsonResponse(rulePackage({ currentSemanticHash: '', latestValidSemanticHash: '' }))
    );
    route('GET /api/v1/rule-packages/pkg_01/draft', () =>
      storedDraft === null ? faultResponse('NOT_FOUND', 404, 'corr-no-draft') : jsonResponse(storedDraft)
    );
    route('GET /api/v1/rule-packages/pkg_01/versions', () => jsonResponse({ items: [] }));
    route('GET /api/v1/rule-packages/pkg_01/audits', () => jsonResponse({ items: [] }));
    route('PUT /api/v1/rule-packages/pkg_01/draft', (request) => {
      saveBody = request.clone().json();
      storedDraft = ruleDraft({ revision: 1, content: { version: 1 } });
      return jsonResponse(storedDraft);
    });
    route('POST /api/v1/rule-packages/pkg_01/draft/validate', () => {
      storedDraft = ruleDraft({ revision: 2, content: { version: 1 }, validationStatus: 'validated' });
      return jsonResponse({ draft: storedDraft, valid: true, diagnostics: [], validation: {} });
    });
    route('POST /api/v1/rules/impact', (request) => {
      impactBody = request.clone().json();
      return jsonResponse(ruleImpact());
    });

    renderManage('/rules/pkg_01');
    const editor = await screen.findByRole('textbox', { name: /草稿内容/ });
    fireEvent.change(editor, { target: { value: '{"version":1}' } });
    await userEvent.click(screen.getByRole('button', { name: '保存草稿' }));

    await waitFor(() => expect(requestsTo('PUT /api/v1/rule-packages/pkg_01/draft')).toHaveLength(1));
    expect(requestsTo('PUT /api/v1/rule-packages/pkg_01/draft')[0]?.ifMatch).toBe('"0"');
    expect(await saveBody).toEqual({ content: { version: 1 }, format: 'json' });

    await userEvent.click(await screen.findByRole('button', { name: '校验草稿' }));
    await screen.findByText('校验通过');
    expect(requestsTo('POST /api/v1/rule-packages/pkg_01/draft/validate')[0]?.ifMatch).toBe('"1"');

    await userEvent.click(screen.getByRole('button', { name: '评估本次修改的影响' }));
    await waitFor(() => expect(requestsTo('POST /api/v1/rules/impact')).toHaveLength(1));
    expect(await impactBody).toEqual({ before: null, after: { version: 1 } });
  });

  it('保存请求期间的新编辑不会被成功响应覆盖，并推进到服务端新 revision', async () => {
    let storedDraft = ruleDraft();
    let resolveSave: ((response: Response) => void) | undefined;
    route('GET /api/v1/rule-packages/pkg_01', () => jsonResponse(rulePackage()));
    route('GET /api/v1/rule-packages/pkg_01/draft', () => jsonResponse(storedDraft));
    route('GET /api/v1/rule-packages/pkg_01/versions', () => jsonResponse({ items: [] }));
    route('GET /api/v1/rule-packages/pkg_01/audits', () => jsonResponse({ items: [] }));
    route(
      'PUT /api/v1/rule-packages/pkg_01/draft',
      () =>
        new Promise<Response>((resolve) => {
          resolveSave = resolve;
        })
    );

    renderManage('/rules/pkg_01');
    const editor = await screen.findByRole('textbox', { name: /草稿内容/ });
    fireEvent.change(editor, { target: { value: '{"version":2}' } });
    await userEvent.click(screen.getByRole('button', { name: '保存草稿' }));
    await waitFor(() => expect(requestsTo('PUT /api/v1/rule-packages/pkg_01/draft')).toHaveLength(1));

    fireEvent.change(editor, { target: { value: '{"version":3}' } });
    storedDraft = ruleDraft({ revision: 4, content: { version: 2 } });
    await act(async () => {
      resolveSave?.(jsonResponse(storedDraft));
      await Promise.resolve();
    });

    await waitFor(() =>
      expect(screen.getByText('服务端草稿 revision').nextElementSibling).toHaveTextContent('4')
    );
    expect(editor).toHaveValue('{"version":3}');
    expect(screen.getByText('有未保存修改')).toBeInTheDocument();
    expect(screen.getByText('本地编辑基于的 revision').nextElementSibling).toHaveTextContent('4');
  });

  it('校验请求期间的新编辑不会被规范化响应覆盖', async () => {
    let storedDraft = ruleDraft();
    let resolveValidation: ((response: Response) => void) | undefined;
    route('GET /api/v1/rule-packages/pkg_01', () => jsonResponse(rulePackage()));
    route('GET /api/v1/rule-packages/pkg_01/draft', () => jsonResponse(storedDraft));
    route('GET /api/v1/rule-packages/pkg_01/versions', () => jsonResponse({ items: [] }));
    route('GET /api/v1/rule-packages/pkg_01/audits', () => jsonResponse({ items: [] }));
    route(
      'POST /api/v1/rule-packages/pkg_01/draft/validate',
      () =>
        new Promise<Response>((resolve) => {
          resolveValidation = resolve;
        })
    );

    renderManage('/rules/pkg_01');
    const editor = await screen.findByRole('textbox', { name: /草稿内容/ });
    await userEvent.click(screen.getByRole('button', { name: '校验草稿' }));
    await waitFor(() =>
      expect(requestsTo('POST /api/v1/rule-packages/pkg_01/draft/validate')).toHaveLength(1)
    );

    fireEvent.change(editor, { target: { value: '{"version":2}' } });
    storedDraft = ruleDraft({ revision: 4, validationStatus: 'validated' });
    await act(async () => {
      resolveValidation?.(jsonResponse({ draft: storedDraft, valid: true, diagnostics: [], validation: {} }));
      await Promise.resolve();
    });

    await waitFor(() => expect(editor).toHaveValue('{"version":2}'));
    expect(screen.getByText('有未保存修改')).toBeInTheDocument();
  });

  it('校验失败时吸收服务端 invalid 草稿并显示诊断', async () => {
    route('GET /api/v1/rule-packages/pkg_01', () => jsonResponse(rulePackage()));
    route('GET /api/v1/rule-packages/pkg_01/draft', () => jsonResponse(ruleDraft()));
    route('GET /api/v1/rule-packages/pkg_01/versions', () => jsonResponse({ items: [] }));
    route('GET /api/v1/rule-packages/pkg_01/audits', () => jsonResponse({ items: [] }));
    route('POST /api/v1/rule-packages/pkg_01/draft/validate', () => {
      const invalid = ruleDraft({
        revision: 4,
        validationStatus: 'invalid',
        diagnostics: [{ path: '/rules/0', message: 'invalid-value' }]
      });
      return jsonResponse({ draft: invalid, valid: false, diagnostics: invalid.diagnostics });
    });

    renderManage('/rules/pkg_01');
    await userEvent.click(await screen.findByRole('button', { name: '校验草稿' }));
    await screen.findByText('校验未通过');
    expect(screen.getByText(/invalid-value/)).toBeInTheDocument();
    expect(requestsTo('POST /api/v1/rule-packages/pkg_01/draft/validate')[0]?.ifMatch).toBe('"3"');
  });

  it('未保存编辑使旧影响失效，刷新保留本地内容且只能显式覆盖', async () => {
    let remoteDraft = ruleDraft({ validationStatus: 'validated' });
    route('GET /api/v1/rule-packages/pkg_01', () =>
      jsonResponse(rulePackage({ currentSemanticHash: OLD_RULE_HASH }))
    );
    route('GET /api/v1/rule-packages/pkg_01/draft', () => jsonResponse(remoteDraft));
    route('GET /api/v1/rule-packages/pkg_01/versions', () => jsonResponse({ items: [] }));
    route('GET /api/v1/rule-packages/pkg_01/audits', () => jsonResponse({ items: [] }));
    route(`GET /api/v1/rule-versions/${OLD_RULE_HASH}/export`, () => jsonResponse({ version: 0 }));
    route('POST /api/v1/rules/impact', () => jsonResponse(ruleImpact()));

    renderManage('/rules/pkg_01');
    await userEvent.click(await screen.findByRole('button', { name: '评估本次修改的影响' }));
    await screen.findByText('结论');

    const editor = screen.getByRole('textbox', { name: /草稿内容/ });
    fireEvent.change(editor, { target: { value: '{"version":2}' } });
    expect(screen.getByRole('button', { name: '校验草稿' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '评估本次修改的影响' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '发布草稿' })).toBeDisabled();
    expect(screen.queryByText('结论')).not.toBeInTheDocument();

    remoteDraft = ruleDraft({ revision: 4, content: { version: 4 }, validationStatus: 'validated' });
    await userEvent.click(screen.getByRole('button', { name: '重新拉取快照' }));
    await screen.findByText('服务端草稿已经变化，本地编辑仍被保留');
    expect(editor).toHaveValue('{"version":2}');

    fireEvent.change(editor, { target: { value: '{\n  "version": 4\n}' } });
    expect(screen.getByText('服务端草稿已经变化，本地编辑仍被保留')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '校验草稿' })).toBeDisabled();

    await userEvent.click(screen.getByRole('button', { name: '用服务端最新草稿覆盖编辑器' }));
    const dialog = await screen.findByRole('dialog', { name: '覆盖本地编辑' });
    await userEvent.click(within(dialog).getByRole('button', { name: '确认覆盖' }));
    await waitFor(() => expect(editor).toHaveValue('{\n  "version": 4\n}'));
  });

  it('人工影响确认解锁发布，刷新版本列表/审计并让下一次保存继承新 semanticHash', async () => {
    let packageValue = rulePackage({ currentSemanticHash: OLD_RULE_HASH });
    let draftValue = ruleDraft({ baseSemanticHash: OLD_RULE_HASH, validationStatus: 'validated' });
    let versionsReads = 0;
    let auditReads = 0;
    let publishBody: Promise<unknown> | undefined;
    let resolvePublish: ((response: Response) => void) | undefined;
    let nextSaveBody: Promise<unknown> | undefined;
    route('GET /api/v1/rule-packages/pkg_01', () => jsonResponse(packageValue));
    route('GET /api/v1/rule-packages/pkg_01/draft', () => jsonResponse(draftValue));
    route('GET /api/v1/rule-packages/pkg_01/versions', () => {
      versionsReads += 1;
      return jsonResponse({ items: [] });
    });
    route('GET /api/v1/rule-packages/pkg_01/audits', () => {
      auditReads += 1;
      return jsonResponse({ items: [] });
    });
    route(`GET /api/v1/rule-versions/${OLD_RULE_HASH}/export`, () => jsonResponse({ version: 0 }));
    route(`GET /api/v1/rule-versions/${NEW_RULE_HASH}/export`, () => jsonResponse({ version: 1 }));
    route('POST /api/v1/rules/impact', () =>
      jsonResponse(
        ruleImpact({
          category: 'BINDING_REVIEW',
          blockPublish: true,
          manualConfirmation: false,
          bindingReview: true
        })
      )
    );
    route(
      'POST /api/v1/rule-packages/pkg_01/publish',
      (request) =>
        new Promise<Response>((resolve) => {
          publishBody = request.clone().json();
          packageValue = rulePackage({ revision: 6, currentSemanticHash: NEW_RULE_HASH });
          resolvePublish = resolve;
        })
    );
    route('PUT /api/v1/rule-packages/pkg_01/draft', (request) => {
      nextSaveBody = request.clone().json();
      draftValue = ruleDraft({
        revision: 4,
        content: { version: 2 },
        validationStatus: 'draft',
        baseSemanticHash: NEW_RULE_HASH
      });
      return jsonResponse(draftValue);
    });

    renderManage('/rules/pkg_01');
    await userEvent.click(await screen.findByRole('button', { name: '评估本次修改的影响' }));
    const publishTrigger = await screen.findByRole('button', { name: '发布草稿' });
    expect(publishTrigger).toBeDisabled();
    await userEvent.click(screen.getByRole('checkbox', { name: '我已阅读并确认本次影响评估要求的人工复核' }));
    expect(publishTrigger).not.toBeDisabled();
    await userEvent.click(publishTrigger);
    const publishDialog = await screen.findByRole('dialog', { name: '发布规则草稿' });
    await userEvent.click(within(publishDialog).getByRole('button', { name: '确认发布' }));

    await waitFor(() => expect(requestsTo('POST /api/v1/rule-packages/pkg_01/publish')).toHaveLength(1));
    const editor = screen.getByRole('textbox', { name: /草稿内容/ });
    expect(editor).toBeDisabled();
    await act(async () => {
      resolvePublish?.(jsonResponse(ruleVersion({ parentSemanticHash: OLD_RULE_HASH }), 201));
      await Promise.resolve();
    });
    expect(requestsTo('POST /api/v1/rule-packages/pkg_01/publish')[0]?.ifMatch).toBe('"3"');
    expect(await publishBody).toEqual({ expectedRevision: 3, reason: '', confirmImpact: true });
    await waitFor(() => {
      expect(versionsReads).toBeGreaterThan(1);
      expect(auditReads).toBeGreaterThan(1);
    });

    await waitFor(() => expect(editor).not.toBeDisabled());
    fireEvent.change(editor, { target: { value: '{"version":2}' } });
    await userEvent.click(screen.getByRole('button', { name: '保存草稿' }));
    await waitFor(() => expect(requestsTo('PUT /api/v1/rule-packages/pkg_01/draft')).toHaveLength(1));
    expect(requestsTo('PUT /api/v1/rule-packages/pkg_01/draft')[0]?.ifMatch).toBe('"3"');
    expect(await nextSaveBody).toEqual({
      content: { version: 2 },
      format: 'json',
      baseSemanticHash: NEW_RULE_HASH
    });
  });

  it('回滚同时发送规则包 revision 的 If-Match 和 expectedRevision', async () => {
    let rollbackBody: Promise<unknown> | undefined;
    route('GET /api/v1/rule-packages/pkg_01', () =>
      jsonResponse(rulePackage({ revision: 7, currentSemanticHash: NEW_RULE_HASH }))
    );
    route('GET /api/v1/rule-packages/pkg_01/draft', () =>
      jsonResponse(ruleDraft({ baseSemanticHash: NEW_RULE_HASH, validationStatus: 'validated' }))
    );
    route('GET /api/v1/rule-packages/pkg_01/versions', () =>
      jsonResponse({ items: [ruleVersion({ version: '0.9.0', semanticHash: OLD_RULE_HASH })] })
    );
    route('GET /api/v1/rule-packages/pkg_01/audits', () => jsonResponse({ items: [] }));
    route(`GET /api/v1/rule-versions/${NEW_RULE_HASH}/export`, () => jsonResponse({ version: 1 }));
    route('POST /api/v1/rule-packages/pkg_01/rollback', (request) => {
      rollbackBody = request.clone().json();
      return jsonResponse(ruleVersion({ semanticHash: OLD_RULE_HASH }));
    });

    renderManage('/rules/pkg_01');
    await screen.findByRole('button', { name: /回滚到/ });
    await selectOption(/回滚到/, '0.9.0（aaaaaaaaaaaa…）');
    await userEvent.type(screen.getByRole('textbox', { name: '回滚理由' }), '恢复稳定版本');
    await userEvent.click(screen.getByRole('button', { name: '回滚当前版本' }));
    const dialog = await screen.findByRole('dialog', { name: '回滚规则包' });
    await userEvent.click(within(dialog).getByRole('button', { name: '确认回滚' }));

    await waitFor(() => expect(requestsTo('POST /api/v1/rule-packages/pkg_01/rollback')).toHaveLength(1));
    expect(requestsTo('POST /api/v1/rule-packages/pkg_01/rollback')[0]?.ifMatch).toBe('"7"');
    expect(await rollbackBody).toEqual({
      targetSemanticHash: OLD_RULE_HASH,
      expectedRevision: 7,
      reason: '恢复稳定版本',
      confirmImpact: false
    });
  });
});

/* ————————————————————————————— 6. 密文只显示一次 ————————————————————————————— */

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
