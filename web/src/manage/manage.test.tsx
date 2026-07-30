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
import { MemoryRouter } from 'react-router';
import { beforeEach, describe, expect, it } from 'vitest';
import { CAPABILITIES } from '../auth/capabilities';
import { createQueryClient } from '../shared/query';
import { RealtimeProvider, type RealtimeHandlers, type RealtimeTransport } from '../shared/realtime';
import { SessionProvider } from '../shared/session';
import { ThemeProvider } from '../shared/theme';
import { ToastProvider } from '../design';
import { faultResponse, jsonResponse, setFetchHandler } from '../../tests/http';
import { ManageApp } from './app';
import RULE_SCHEMA from '../../../internal/rules/rule-package.schema.json';
import { DataTable, formatDateTime } from './ui';

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
  route('GET /api/v1/rule-parameters', () => jsonResponse({ parameterSets: [] }));
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

describe('响应式数据表契约', () => {
  it('每个单元格携带窄屏堆叠所需的列标签', () => {
    render(
      <DataTable
        caption="任务表"
        columns={[
          { id: 'status', header: '状态', render: (row: { status: string }) => row.status },
          { id: 'stage', header: '阶段', render: (row: { status: string; stage: string }) => row.stage }
        ]}
        rows={[{ status: '运行中', stage: '哈希' }]}
        rowKey={(row) => row.status}
        emptyTitle="没有任务"
      />
    );

    expect(screen.getByRole('cell', { name: '运行中' })).toHaveAttribute('data-label', '状态');
    expect(screen.getByRole('cell', { name: '哈希' })).toHaveAttribute('data-label', '阶段');
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

function bindingIssue(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: 'bissue_PAGE_1',
    sourceId: 'src_01',
    entityType: 'work',
    structureKind: null,
    sourceKey: 'work/page-1',
    workSourceKey: null,
    providerId: null,
    externalId: null,
    code: 'BINDING_REVIEW_REQUIRED',
    candidateCount: 1,
    status: 'open',
    resolution: null,
    resolvedTargetId: null,
    resolvedBy: null,
    version: 1,
    createdAt: '2026-07-30T01:00:00Z',
    updatedAt: '2026-07-30T01:00:00Z',
    resolvedAt: null,
    candidates: [],
    ...overrides
  };
}

function orphanCandidate(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    bindingId: 'wb_PAGE_1',
    sourceId: 'src_01',
    sourceKey: 'work/orphan-page-1',
    canonicalId: 'work_PAGE_1',
    canonicalLabel: '孤儿第一页',
    entityType: 'work',
    missedScans: 3,
    retentionThreshold: 3,
    lastSeenGeneration: 7,
    createdAt: '2026-07-30T01:00:00Z',
    updatedAt: '2026-07-30T01:00:00Z',
    ...overrides
  };
}

function structureDecision(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    decisionId: 'sdec_PAGE_1',
    issueId: 'bissue_STRUCTURE_PAGE_1',
    sourceId: 'src_01',
    kind: 'split',
    action: 'split_inherit',
    targetSourceKey: 'work/structure-page-1',
    targetWorkId: 'work_STRUCTURE_PAGE_1',
    status: 'applied',
    version: 1,
    createdAt: '2026-07-30T01:00:00Z',
    updatedAt: '2026-07-30T01:00:00Z',
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
const FORM_RULE_SET_ID = 'rset_00000000-0000-7000-8000-000000000001';

const LOSSLESS_RULE_TEXT = `{
  "rule_set_id": "${FORM_RULE_SET_ID}",
  "version": "1.0.0",
  "schema_version": 1,
  "normalization_algorithm_version": "gallery-canonical-json-v1",
  "compiler_requirement": "gallery-rule-compiler-v1",
  "cel_profile_version": "gallery-cel-v1",
  "parameter_schema": {"type": "object", "additionalProperties": false},
  "provider_namespaces": [],
  "primitives": [{"id": "metadata", "kind": "metadata_map", "config": {"integer": 9007199254740993123, "decimal": 1.2300000000000000001}}],
  "cel_expressions": [],
  "tests": [{"input": {"exponent": 1e+40}}],
  "extensions": {"example.lossless": {"value": 9007199254740993123}}
}`;

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
  const content = overrides.content ?? { version: 1 };
  return {
    id: 'draft_01',
    packageId: 'pkg_01',
    content,
    contentText: typeof content === 'string' ? content : JSON.stringify(content, null, 2),
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

function ruleParameterSet(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  const parametersText =
    typeof overrides.parametersText === 'string'
      ? overrides.parametersText
      : '{"minimumSize":9007199254740993123}';
  return {
    id: 'rparam_00000000-0000-7000-8000-000000000001',
    name: '共享参数',
    semanticHash: NEW_RULE_HASH,
    currentRevision: 2,
    currentHash: 'f'.repeat(64),
    status: 'active',
    parameters: { minimumSize: 1 },
    parametersText,
    createdBy: 'principal_manage',
    createdAt: '2026-07-27T05:00:00Z',
    updatedAt: '2026-07-27T05:00:00Z',
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
  it('按新到旧分页任务，续页失败保留当前页且已访问页可前后复用', async () => {
    let olderAttempts = 0;
    const seen: Array<{ status: string | null; cursor: string | null }> = [];
    route('GET /api/v1/sources', () => jsonResponse({ sources: [] }));
    route('GET /api/v1/jobs', (_request, url) => {
      const status = url.searchParams.get('status');
      const cursor = url.searchParams.get('cursor');
      seen.push({ status, cursor });
      if (status === 'failed') {
        return jsonResponse({ jobs: [job({ id: 'job_FAILED', status: 'failed' })] });
      }
      if (cursor === null) {
        return jsonResponse({ jobs: [job({ id: 'job_NEWEST' })], nextCursor: 'cursor-older' });
      }
      olderAttempts += 1;
      if (olderAttempts === 1) return faultResponse('VALIDATION_ERROR', 400, 'corr-older');
      return jsonResponse({ jobs: [job({ id: 'job_OLDER', createdAt: '2026-07-26T01:00:00Z' })] });
    });

    renderManage('/scans');
    await screen.findByRole('link', { name: 'job_NEWEST' });
    expect(screen.getByText('第 1 页 · 本页 1 条 · 每页最多 50 条（还有更早任务）。')).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: '下一页（更早）' }));
    await screen.findByText('更早的任务暂时未能载入');
    expect(screen.getByRole('link', { name: 'job_NEWEST' })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'job_OLDER' })).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: '重试下一页（更早）' }));
    await screen.findByRole('link', { name: 'job_OLDER' });
    expect(screen.queryByRole('link', { name: 'job_NEWEST' })).not.toBeInTheDocument();
    expect(screen.getByText('第 2 页 · 本页 1 条 · 每页最多 50 条（已到末页）。')).toBeInTheDocument();
    expect(seen.filter((entry) => entry.cursor === 'cursor-older')).toHaveLength(2);

    await userEvent.click(screen.getByRole('button', { name: '上一页（较新）' }));
    await screen.findByRole('link', { name: 'job_NEWEST' });
    expect(screen.queryByRole('link', { name: 'job_OLDER' })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '下一页（更早）' }));
    await screen.findByRole('link', { name: 'job_OLDER' });
    expect(seen.filter((entry) => entry.cursor === 'cursor-older')).toHaveLength(2);

    await selectOption(/状态/, '已失败');
    await screen.findByRole('link', { name: 'job_FAILED' });
    expect(screen.getByText('第 1 页 · 本页 1 条 · 每页最多 50 条（已到末页）。')).toBeInTheDocument();
    expect(seen.some((entry) => entry.status === 'failed' && entry.cursor === null)).toBe(true);
    expect(screen.queryByRole('link', { name: 'job_NEWEST' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '上一页（较新）' })).not.toBeInTheDocument();
  });

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

  it('失败后等待自动重试的 Job 可以取消，取消终态不会错误提供重试入口', async () => {
    const nextAttemptAt = '2026-07-27T02:00:00Z';
    let current = job({
      id: 'job_01BACKOFF',
      type: 'catalog_gc',
      sourceId: undefined,
      status: 'failed',
      stage: 'retry_backoff',
      issueCode: 'E2E_TRANSIENT',
      failureRetryable: true,
      nextAttemptAt
    });
    route('GET /api/v1/sources', () => jsonResponse({ sources: [SOURCE] }));
    route('GET /api/v1/jobs', () => jsonResponse({ jobs: [current] }));
    route('POST /api/v1/jobs/job_01BACKOFF/cancel', () => {
      current = {
        ...current,
        status: 'cancelled',
        stage: 'cancelled',
        failureRetryable: false,
        nextAttemptAt: null,
        cancelRequested: true
      };
      return jsonResponse(current, 202);
    });

    renderManage('/scans');
    const link = await screen.findByRole('link', { name: 'job_01BACKOFF' });
    const row = link.closest('tr');
    expect(row).not.toBeNull();
    expect(within(row as HTMLElement).getByText('已失败')).toBeInTheDocument();
    expect(within(row as HTMLElement).getByText(formatDateTime(nextAttemptAt))).toBeInTheDocument();

    await userEvent.click(within(row as HTMLElement).getByRole('button', { name: '取消' }));
    const dialog = await screen.findByRole('dialog', { name: '取消任务' });
    expect(within(dialog).getByText(/取消后不会再次入队，既有失败 Attempt 保持不变/)).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole('button', { name: '确认取消' }));

    await waitFor(() => expect(requestsTo('POST /api/v1/jobs/job_01BACKOFF/cancel')).toHaveLength(1));
    expect(requestsTo('POST /api/v1/jobs/job_01BACKOFF/cancel')[0]?.csrf).toBe(BOOTSTRAP.csrfToken);
    const cancelledRow = screen.getByRole('link', { name: 'job_01BACKOFF' }).closest('tr');
    await waitFor(() => {
      expect(within(cancelledRow as HTMLElement).getByText('已取消')).toBeInTheDocument();
    });
    expect(
      within(cancelledRow as HTMLElement).queryByRole('button', { name: '取消' })
    ).not.toBeInTheDocument();
    expect(
      within(cancelledRow as HTMLElement).queryByRole('button', { name: '重试' })
    ).not.toBeInTheDocument();
  });
});

describe('治理列表当前页窗口', () => {
  it('绑定问题续页失败保留当前页，成功后前后复用且筛选回到第一页', async () => {
    let nextAttempts = 0;
    const seen: Array<{ status: string | null; cursor: string | null }> = [];
    route('GET /api/v1/sources', () => jsonResponse({ sources: [SOURCE] }));
    route('GET /api/v1/binding-issues', (_request, url) => {
      const status = url.searchParams.get('status');
      const cursor = url.searchParams.get('cursor');
      seen.push({ status, cursor });
      if (status === 'resolved') {
        return jsonResponse({
          issues: [bindingIssue({ id: 'bissue_RESOLVED', sourceKey: 'work/resolved', status: 'resolved' })]
        });
      }
      if (cursor === null) {
        return jsonResponse({ issues: [bindingIssue()], nextCursor: 'cursor-issue-page-2' });
      }
      nextAttempts += 1;
      if (nextAttempts === 1) return faultResponse('VALIDATION_ERROR', 400, 'corr-issue-page-2');
      return jsonResponse({
        issues: [
          bindingIssue({
            id: 'bissue_PAGE_2',
            sourceKey: 'work/page-2',
            createdAt: '2026-07-30T02:00:00Z',
            updatedAt: '2026-07-30T02:00:00Z'
          })
        ]
      });
    });

    renderManage('/governance');
    const table = await screen.findByRole('table', { name: '绑定问题' });
    expect(within(table).getByText('work/page-1')).toBeInTheDocument();
    expect(
      screen.getByText(
        '第 1 页 · 本页 1 条 · 每页最多 50 条（还有下一页）。本页逐条处理需要 1 次独立请求——这是服务端契约决定的，不是界面偷懒。'
      )
    ).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: '下一页' }));
    await screen.findByText('下一页绑定问题暂时未能载入');
    expect(within(table).getByText('work/page-1')).toBeInTheDocument();
    expect(within(table).queryByText('work/page-2')).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: '重试下一页' }));
    await within(table).findByText('work/page-2');
    expect(within(table).queryByText('work/page-1')).not.toBeInTheDocument();
    expect(screen.getByText(/第 2 页 · 本页 1 条 · 每页最多 50 条（已到末页）/)).toBeInTheDocument();
    expect(seen.filter((entry) => entry.cursor === 'cursor-issue-page-2')).toHaveLength(2);

    await userEvent.click(screen.getByRole('button', { name: '上一页' }));
    await within(table).findByText('work/page-1');
    await userEvent.click(screen.getByRole('button', { name: '下一页' }));
    await within(table).findByText('work/page-2');
    expect(seen.filter((entry) => entry.cursor === 'cursor-issue-page-2')).toHaveLength(2);

    await selectOption(/状态/, '已修复');
    await screen.findByText('work/resolved');
    expect(screen.getByText(/第 1 页 · 本页 1 条 · 每页最多 50 条（已到末页）/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '上一页' })).not.toBeInTheDocument();
    expect(seen.some((entry) => entry.status === 'resolved' && entry.cursor === null)).toBe(true);
  });

  it('孤儿候选只渲染当前页，并复用已访问页', async () => {
    const seenCursors: Array<string | null> = [];
    route('GET /api/v1/sources', () => jsonResponse({ sources: [] }));
    route('GET /api/v1/binding-issues', () => jsonResponse({ issues: [] }));
    route('GET /api/v1/orphan-candidates', (_request, url) => {
      const cursor = url.searchParams.get('cursor');
      seenCursors.push(cursor);
      if (cursor === null) {
        return jsonResponse({ candidates: [orphanCandidate()], nextCursor: 'cursor-orphan-page-2' });
      }
      return jsonResponse({
        candidates: [
          orphanCandidate({
            bindingId: 'wb_PAGE_2',
            sourceKey: 'work/orphan-page-2',
            canonicalId: 'work_PAGE_2',
            canonicalLabel: '孤儿第二页'
          })
        ]
      });
    });

    renderManage('/governance');
    await userEvent.click(await screen.findByRole('tab', { name: '孤儿候选' }));
    const table = await screen.findByRole('table', { name: '孤儿候选' });
    expect(within(table).getByText('孤儿第一页')).toBeInTheDocument();
    expect(screen.getByText(/第 1 页 · 本页 1 条 · 每页最多 50 条（还有下一页）/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: '下一页' }));
    await within(table).findByText('孤儿第二页');
    expect(within(table).queryByText('孤儿第一页')).not.toBeInTheDocument();
    expect(screen.getByText(/第 2 页 · 本页 1 条 · 每页最多 50 条（已到末页）/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: '上一页' }));
    await within(table).findByText('孤儿第一页');
    await userEvent.click(screen.getByRole('button', { name: '下一页' }));
    await within(table).findByText('孤儿第二页');
    expect(seenCursors.filter((cursor) => cursor === 'cursor-orphan-page-2')).toHaveLength(1);
  });

  it('结构决策续页失败保留当前页，成功后前后复用', async () => {
    let nextAttempts = 0;
    const seenCursors: Array<string | null> = [];
    route('GET /api/v1/sources', () => jsonResponse({ sources: [] }));
    route('GET /api/v1/binding-issues', () => jsonResponse({ issues: [] }));
    route('GET /api/v1/source-structure-decisions', (_request, url) => {
      const cursor = url.searchParams.get('cursor');
      seenCursors.push(cursor);
      if (cursor === null) {
        return jsonResponse({
          decisions: [structureDecision()],
          nextCursor: 'cursor-structure-page-2'
        });
      }
      nextAttempts += 1;
      if (nextAttempts === 1) return faultResponse('VALIDATION_ERROR', 400, 'corr-structure-page-2');
      return jsonResponse({
        decisions: [
          structureDecision({
            decisionId: 'sdec_PAGE_2',
            issueId: 'bissue_STRUCTURE_PAGE_2',
            targetSourceKey: 'work/structure-page-2',
            targetWorkId: 'work_STRUCTURE_PAGE_2',
            createdAt: '2026-07-29T01:00:00Z',
            updatedAt: '2026-07-29T01:00:00Z'
          })
        ]
      });
    });

    renderManage('/governance');
    await userEvent.click(await screen.findByRole('tab', { name: '结构决策' }));
    const table = await screen.findByRole('table', { name: '结构决策' });
    expect(within(table).getByText('work/structure-page-1')).toBeInTheDocument();
    expect(screen.getByText(/第 1 页 · 本页 1 条 · 每页最多 50 条（还有下一页）/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: '下一页' }));
    await screen.findByText('下一页结构决策暂时未能载入');
    expect(within(table).getByText('work/structure-page-1')).toBeInTheDocument();
    expect(within(table).queryByText('work/structure-page-2')).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: '重试下一页' }));
    await within(table).findByText('work/structure-page-2');
    expect(within(table).queryByText('work/structure-page-1')).not.toBeInTheDocument();
    expect(screen.getByText(/第 2 页 · 本页 1 条 · 每页最多 50 条（已到末页）/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: '上一页' }));
    await within(table).findByText('work/structure-page-1');
    await userEvent.click(screen.getByRole('button', { name: '下一页' }));
    await within(table).findByText('work/structure-page-2');
    expect(seenCursors.filter((cursor) => cursor === 'cursor-structure-page-2')).toHaveLength(2);
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
    route('GET /api/v1/rule-packages/pkg_01/versions', () =>
      jsonResponse({
        items: [ruleVersion({ semanticHash: OLD_RULE_HASH, version: '0.9.0' })]
      })
    );
    route('GET /api/v1/rule-packages/pkg_unpublished/versions', () => jsonResponse({ items: [] }));
    route('GET /api/v1/source-rule-bindings', () => jsonResponse({ bindings: [] }));
    route('GET /api/v1/sources/src_b/effective-rule-binding', () =>
      faultResponse('NOT_FOUND', 404, 'corr-effective')
    );
    route('POST /api/v1/source-rule-bindings', (request) => {
      body = request.clone().json();
      return jsonResponse(
        {
          id: 'binding_01',
          sourceId: 'src_b',
          semanticHash: 'a'.repeat(64),
          ruleIrHash: 'b'.repeat(64),
          parameters: '{}',
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
    await userEvent.click(screen.getByRole('option', { name: '合成规则包 · 0.9.0 · aaaaaaaaaaaa…' }));
    await userEvent.click(screen.getByRole('button', { name: '创建绑定' }));

    await waitFor(() => expect(requestsTo('POST /api/v1/source-rule-bindings')).toHaveLength(1));
    expect(await body).toEqual({
      sourceId: 'src_b',
      semanticHash: OLD_RULE_HASH,
      priority: 100,
      parameters: '{}'
    });
  });

  it('已有规则绑定可以暂停、标记无效并恢复，列表与生效绑定都从服务端快照重取', async () => {
    let status: 'active' | 'paused' | 'invalid' = 'active';
    let patchBody: Promise<unknown> | undefined;
    const binding = () => ({
      id: 'srb_01',
      sourceId: 'src_b',
      semanticHash: OLD_RULE_HASH,
      parameters: {},
      parametersText: '{}',
      overrideText: '{}',
      priority: 0,
      ruleIrHash: 'd'.repeat(64),
      status,
      createdAt: '2026-07-27T03:00:00Z',
      updatedAt: '2026-07-27T03:00:00Z'
    });
    route('GET /api/v1/sources', () => jsonResponse({ sources: duplicateSources }));
    route('GET /api/v1/rule-packages', () =>
      jsonResponse({ items: [rulePackage({ currentSemanticHash: OLD_RULE_HASH })] })
    );
    route('GET /api/v1/rule-packages/pkg_01/versions', () =>
      jsonResponse({ items: [ruleVersion({ semanticHash: OLD_RULE_HASH })] })
    );
    route('GET /api/v1/source-rule-bindings', () => jsonResponse({ bindings: [binding()] }));
    route('GET /api/v1/sources/src_b/effective-rule-binding', () =>
      status === 'active' ? jsonResponse(binding()) : faultResponse('NOT_FOUND', 404, 'corr-paused')
    );
    route('PATCH /api/v1/source-rule-bindings/srb_01', (request) => {
      patchBody = request.clone().json();
      const requested = request.clone().json() as Promise<{ status: 'active' | 'paused' | 'invalid' }>;
      return requested.then((body) => {
        status = body.status;
        return jsonResponse(binding());
      });
    });

    renderManage('/rules');
    await screen.findByRole('button', { name: /来源/ });
    await selectOption(/来源/, '同名来源 · src_b');
    const bindingsTable = await screen.findByRole('table', { name: 'Source 规则绑定' });
    const bindingRow = within(bindingsTable).getByText('srb_01').closest('tr');
    expect(bindingRow).not.toBeNull();

    await userEvent.click(within(bindingRow as HTMLElement).getByRole('button', { name: '暂停' }));
    let dialog = await screen.findByRole('dialog', { name: '暂停规则绑定' });
    await userEvent.click(within(dialog).getByRole('button', { name: '确认暂停' }));
    await waitFor(() => expect(requestsTo('PATCH /api/v1/source-rule-bindings/srb_01')).toHaveLength(1));
    expect(await patchBody).toEqual({ status: 'paused' });
    await waitFor(() => expect(within(bindingRow as HTMLElement).getByText('paused')).toBeInTheDocument());
    expect(screen.getByText(/服务端没有返回生效绑定（NOT_FOUND）/)).toBeInTheDocument();

    await userEvent.click(within(bindingRow as HTMLElement).getByRole('button', { name: '恢复' }));
    dialog = await screen.findByRole('dialog', { name: '恢复规则绑定' });
    await userEvent.click(within(dialog).getByRole('button', { name: '确认恢复' }));
    await waitFor(() => expect(requestsTo('PATCH /api/v1/source-rule-bindings/srb_01')).toHaveLength(2));
    expect(await patchBody).toEqual({ status: 'active' });
    await waitFor(() => expect(within(bindingRow as HTMLElement).getByText('active')).toBeInTheDocument());
    expect(screen.queryByText(/服务端没有返回生效绑定/)).not.toBeInTheDocument();

    await userEvent.click(within(bindingRow as HTMLElement).getByRole('button', { name: '标记无效' }));
    dialog = await screen.findByRole('dialog', { name: '标记规则绑定无效' });
    await userEvent.click(within(dialog).getByRole('button', { name: '确认标记无效' }));
    await waitFor(() => expect(requestsTo('PATCH /api/v1/source-rule-bindings/srb_01')).toHaveLength(3));
    expect(await patchBody).toEqual({ status: 'invalid' });
    await waitFor(() => expect(within(bindingRow as HTMLElement).getByText('invalid')).toBeInTheDocument());
    expect(screen.getByText(/服务端没有返回生效绑定（NOT_FOUND）/)).toBeInTheDocument();

    await userEvent.click(within(bindingRow as HTMLElement).getByRole('button', { name: '恢复' }));
    dialog = await screen.findByRole('dialog', { name: '恢复规则绑定' });
    await userEvent.click(within(dialog).getByRole('button', { name: '确认恢复' }));
    await waitFor(() => expect(requestsTo('PATCH /api/v1/source-rule-bindings/srb_01')).toHaveLength(4));
    expect(await patchBody).toEqual({ status: 'active' });
    await waitFor(() => expect(within(bindingRow as HTMLElement).getByText('active')).toBeInTheDocument());
    expect(screen.queryByText(/服务端没有返回生效绑定/)).not.toBeInTheDocument();
  });

  it.each([
    ['open', false],
    ['resolved', false],
    ['dismissed', true],
    ['superseded', true],
    ['stale', true]
  ] as const)('绑定问题状态 %s 的重新打开动作与后端状态机一致', async (status, canReopen) => {
    let reopenBody: Promise<unknown> | undefined;
    const issue = {
      id: 'bissue_01',
      sourceId: 'src_b',
      entityType: 'work' as const,
      sourceKey: 'work/source-key',
      code: 'BINDING_REVIEW_REQUIRED',
      candidateCount: 2,
      status,
      resolution: status === 'dismissed' ? ('dismissed' as const) : null,
      resolvedTargetId: null,
      resolvedBy: status === 'open' ? null : 'principal_manage',
      version: 7,
      createdAt: '2026-07-27T03:00:00Z',
      updatedAt: '2026-07-27T03:00:00Z',
      resolvedAt: status === 'resolved' ? '2026-07-27T03:00:00Z' : null,
      candidates: []
    };
    route('GET /api/v1/sources', () => jsonResponse({ sources: duplicateSources }));
    route('GET /api/v1/binding-issues', () => jsonResponse({ issues: [issue] }));
    route('POST /api/v1/binding-issues/bissue_01/reopen', (request) => {
      reopenBody = request.clone().json();
      return jsonResponse({ ...issue, status: 'open', version: 8 });
    });

    renderManage('/governance');
    const table = await screen.findByRole('table', { name: '绑定问题' });
    const reopenButton = within(table).queryByRole('button', { name: '重新打开' });
    if (!canReopen) {
      expect(reopenButton).not.toBeInTheDocument();
      return;
    }
    expect(reopenButton).toBeInTheDocument();
    if (status === 'dismissed') {
      if (reopenButton === null) throw new Error('允许重新打开的状态缺少操作按钮');
      await userEvent.click(reopenButton);
      await waitFor(() => expect(requestsTo('POST /api/v1/binding-issues/bissue_01/reopen')).toHaveLength(1));
      expect(await reopenBody).toEqual({ version: 7 });
    }
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
  const routeLosslessSchemaDraft = (contentText = LOSSLESS_RULE_TEXT) => {
    route('GET /api/v1/rule-packages/pkg_01', () =>
      jsonResponse(rulePackage({ ruleSetId: FORM_RULE_SET_ID }))
    );
    route('GET /api/v1/rule-packages/pkg_01/draft', () =>
      jsonResponse(
        ruleDraft({
          content: JSON.parse(contentText),
          contentText,
          validationStatus: 'draft'
        })
      )
    );
    route('GET /api/v1/rule-packages/pkg_01/versions', () => jsonResponse({ items: [] }));
    route('GET /api/v1/rule-packages/pkg_01/audits', () => jsonResponse({ items: [] }));
    route('GET /api/v1/rules/schema', () => jsonResponse(RULE_SCHEMA));
    route('GET /api/v1/rules/examples', () => jsonResponse({ items: [] }));
  };

  it('首次草稿可从内置模板起步，并注入当前 ruleSetId、移除派生 hash', async () => {
    const examplePackage: Record<string, unknown> = {
      rule_set_id: 'rset_00000000-0000-7000-8000-000000000099',
      version: '1.0.0',
      schema_version: 1,
      normalization_algorithm_version: 'gallery-canonical-json-v1',
      compiler_requirement: 'gallery-rule-compiler-v1',
      cel_profile_version: 'gallery-cel-v1',
      parameter_schema: { type: 'object', additionalProperties: false },
      provider_namespaces: [],
      primitives: [
        {
          id: 'works',
          kind: 'path_match',
          config: {
            scope: 'work_directory',
            glob: '*',
            title: 'directory_name',
            stable_key: 'relative_path',
            metadata_file: 'metadata.json'
          }
        }
      ],
      cel_expressions: [],
      tests: [{}],
      extensions: {},
      package_hash: 'c'.repeat(64),
      semantic_hash: 'd'.repeat(64)
    };
    route('GET /api/v1/rule-packages/pkg_01', () =>
      jsonResponse(rulePackage({ ruleSetId: FORM_RULE_SET_ID }))
    );
    route('GET /api/v1/rule-packages/pkg_01/draft', () => faultResponse('NOT_FOUND', 404, 'corr-empty'));
    route('GET /api/v1/rule-packages/pkg_01/versions', () => jsonResponse({ items: [] }));
    route('GET /api/v1/rule-packages/pkg_01/audits', () => jsonResponse({ items: [] }));
    route('GET /api/v1/rules/schema', () => jsonResponse(RULE_SCHEMA));
    route('GET /api/v1/rules/examples', () =>
      jsonResponse({
        items: [
          {
            id: 'author-work-media',
            name: '作者—作品—媒体层级',
            category: 'hierarchical',
            packageHash: 'c'.repeat(64),
            semanticHash: 'd'.repeat(64),
            package: examplePackage
          }
        ]
      })
    );

    renderManage('/rules/pkg_01');
    await userEvent.click(await screen.findByRole('tab', { name: 'Schema 表单' }));
    await screen.findByTestId('rule-schema-form', {}, { timeout: 10_000 });
    await selectOption(/起始模板/, '作者—作品—媒体层级');
    await userEvent.click(screen.getByRole('button', { name: '载入起始模板' }));

    expect(screen.getByText('有未保存修改')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '保存草稿' })).not.toBeDisabled();
    const workGlob = await screen.findByRole('textbox', { name: /作品目录 glob/ });
    expect(workGlob).toHaveValue('*');
    expect(screen.queryByRole('textbox', { name: /原语配置/ })).not.toBeInTheDocument();
    fireEvent.change(workGlob, { target: { value: 'works/*' } });
    await userEvent.click(screen.getByRole('tab', { name: 'JSON 文本' }));
    const text = (screen.getByRole('textbox', { name: /草稿内容/ }) as HTMLTextAreaElement).value;
    expect(text).toContain(`"rule_set_id": "${FORM_RULE_SET_ID}"`);
    expect(text).toContain('"glob": "works/*"');
    expect(text).not.toContain('package_hash');
    expect(text).not.toContain('semantic_hash');
    expect(requestsTo('PUT /api/v1/rule-packages/pkg_01/draft')).toHaveLength(0);
  }, 10_000);

  it('从当前草稿执行 Dry Run、Explain 与 Trace，并无损发送合成输入', async () => {
    const debugBodies = new Map<string, string>();
    route('GET /api/v1/rule-packages/pkg_01', () =>
      jsonResponse(rulePackage({ ruleSetId: FORM_RULE_SET_ID }))
    );
    route('GET /api/v1/rule-packages/pkg_01/draft', () =>
      jsonResponse(
        ruleDraft({
          content: JSON.parse(LOSSLESS_RULE_TEXT),
          contentText: LOSSLESS_RULE_TEXT,
          validationStatus: 'draft'
        })
      )
    );
    route('GET /api/v1/rule-packages/pkg_01/versions', () => jsonResponse({ items: [] }));
    route('GET /api/v1/rule-packages/pkg_01/audits', () => jsonResponse({ items: [] }));
    route('POST /api/v1/rules/dry-run', async (request) => {
      debugBodies.set('dry-run', await request.text());
      return new Response(
        `{"ruleVersion":"${'1'.repeat(64)}","ruleIrHash":"${'2'.repeat(64)}","work":{"title":"合成作品","exact":9007199254740993123},"trace":[{"primitiveId":"work","reasonCode":"MATCHED"}],"issues":[]}`,
        { status: 200, headers: { 'Content-Type': 'application/json' } }
      );
    });
    route('POST /api/v1/rules/explain', async (request) => {
      debugBodies.set('explain', await request.text());
      return jsonResponse({
        ruleVersion: '1'.repeat(64),
        ruleIrHash: '2'.repeat(64),
        fields: [{ target: 'title', primitiveId: 'work' }],
        trace: [{ reasonCode: 'MATCHED' }]
      });
    });
    route('POST /api/v1/rules/trace', async (request) => {
      debugBodies.set('trace', await request.text());
      return jsonResponse({
        ruleVersion: '1'.repeat(64),
        ruleIrHash: '2'.repeat(64),
        trace: [{ primitiveId: 'work', inputPointer: '/path' }],
        issues: []
      });
    });

    renderManage('/rules/pkg_01');
    const parameters = await screen.findByRole('textbox', { name: '调试参数 JSON' });
    fireEvent.change(parameters, { target: { value: '{"threshold":9007199254740993123}' } });

    await userEvent.click(screen.getByRole('button', { name: '执行 Dry Run' }));
    const dryRunResult = await screen.findByLabelText('Dry Run 作品结果');
    expect(dryRunResult).toHaveTextContent('合成作品');
    expect(dryRunResult).toHaveTextContent('9007199254740993123');
    await userEvent.click(screen.getByRole('button', { name: '查看 Explain' }));
    expect(await screen.findByLabelText('Explain 字段来源')).toHaveTextContent('primitiveId');
    await userEvent.click(screen.getByRole('button', { name: '查看 Trace' }));
    expect(await screen.findByLabelText('Trace 步骤')).toHaveTextContent('inputPointer');

    for (const endpoint of ['dry-run', 'explain', 'trace']) {
      const body = debugBodies.get(endpoint);
      expect(body).toContain('9007199254740993123');
      expect(body).toContain('1.2300000000000000001');
      expect(requestsTo(`POST /api/v1/rules/${endpoint}`)[0]?.csrf).toBe(BOOTSTRAP.csrfToken);
    }

    fireEvent.change(screen.getByRole('textbox', { name: '合成 Sample JSON' }), {
      target: { value: '{"path":"changed","files":[],"metadata":{}}' }
    });
    expect(
      screen.getByText('规则包、参数或合成 Sample 已变化；旧调试结果已隐藏，请重新执行。')
    ).toBeInTheDocument();
    expect(screen.queryByLabelText('Trace 步骤')).not.toBeInTheDocument();
  });

  it('Schema 表单复用草稿状态机，Schema 错误不阻止保存且未知数字无损', async () => {
    let saveBody: Promise<{ content: string; format: string }> | undefined;
    routeLosslessSchemaDraft();
    route('PUT /api/v1/rule-packages/pkg_01/draft', (request) => {
      saveBody = request.clone().json() as Promise<{ content: string; format: string }>;
      return request
        .clone()
        .json()
        .then((body: { content: string }) =>
          jsonResponse(
            ruleDraft({
              content: JSON.parse(body.content),
              contentText: body.content,
              revision: 4,
              validationStatus: 'invalid',
              diagnostics: [{ path: '/version', message: '版本格式无效' }]
            })
          )
        );
    });

    renderManage('/rules/pkg_01');
    const formTab = await screen.findByRole('tab', { name: 'Schema 表单' });
    expect(screen.getByRole('button', { name: '保存草稿' })).toBeDisabled();
    await userEvent.click(formTab);
    await waitFor(() => expect(formTab).toHaveAttribute('aria-selected', 'true'));
    await waitFor(() => expect(requestsTo('GET /api/v1/rules/schema')).toHaveLength(1));
    const formState = await screen.findByText(/运行时 Schema 与前端预编译版本不一致|Schema 错误是即时预检/);
    expect(formState).toHaveTextContent('Schema 错误是即时预检');
    await screen.findByTestId('rule-schema-form', {}, { timeout: 10_000 });
    const extensions = screen.getByRole('textbox', { name: /extensions/ }) as HTMLTextAreaElement;
    const exactExtensions = extensions.value;
    expect(exactExtensions).toContain('9007199254740993123');
    const version = await screen.findByRole('textbox', { name: /规则版本/ });
    fireEvent.change(version, { target: { value: '1.0.1' } });
    fireEvent.change(extensions, { target: { value: '{' } });
    expect(screen.getByRole('button', { name: '保存草稿' })).toBeDisabled();
    await userEvent.click(screen.getByRole('tab', { name: 'JSON 文本' }));
    expect(screen.getByRole('tab', { name: 'Schema 表单' })).toHaveAttribute('aria-selected', 'true');
    expect(
      screen.getByText('请先修复表单中无损 JSON 字段的语法错误，再切换到文本模式。')
    ).toBeInTheDocument();
    fireEvent.change(extensions, { target: { value: exactExtensions } });
    await waitFor(() => expect(screen.getByRole('button', { name: '保存草稿' })).not.toBeDisabled());

    fireEvent.change(version, { target: { value: 'invalid-version' } });
    fireEvent.blur(version);

    expect((await screen.findAllByText(/must match pattern/i)).length).toBeGreaterThan(0);
    // 本地 AJV 是预检：服务端明确允许保存 invalid 草稿，因此按钮仍可用。
    expect(screen.getByRole('button', { name: '保存草稿' })).not.toBeDisabled();
    await userEvent.click(screen.getByRole('button', { name: '保存草稿' }));
    await waitFor(() => expect(requestsTo('PUT /api/v1/rule-packages/pkg_01/draft')).toHaveLength(1));

    const body = await saveBody;
    expect(body?.format).toBe('json');
    expect(body?.content).toContain('9007199254740993123');
    expect(body?.content).toContain('1.2300000000000000001');
    expect(body?.content).toContain('1e+40');
    expect(body?.content).toContain('"version": "invalid-version"');
    expect(requestsTo('PUT /api/v1/rule-packages/pkg_01/draft')[0]?.ifMatch).toBe('"3"');
  });

  it('参数 Schema 可从结构控件建立字段、类型和 required', async () => {
    routeLosslessSchemaDraft();

    renderManage('/rules/pkg_01');
    await userEvent.click(await screen.findByRole('tab', { name: 'Schema 表单' }));
    await screen.findByTestId('rule-schema-form', {}, { timeout: 10_000 });

    fireEvent.change(screen.getByRole('textbox', { name: '新参数名称' }), {
      target: { value: 'minimumSize' }
    });
    await userEvent.click(screen.getByRole('button', { name: '添加参数' }));
    const parameterTitle = screen.getByRole('textbox', { name: 'minimumSize 标题' });
    const parameterCard = parameterTitle.closest('article');
    if (parameterCard === null) throw new Error('未找到新增参数卡片');
    await userEvent.click(within(parameterCard).getByRole('button', { name: /minimumSize 类型/ }));
    await userEvent.click(await screen.findByRole('option', { name: 'integer' }));
    await userEvent.click(screen.getByRole('checkbox', { name: '必填参数' }));
    fireEvent.change(parameterTitle, {
      target: { value: '最小文件大小' }
    });

    const expandParameter = screen.getAllByRole('button', { name: '展开完整 JSON 结构' }).at(0);
    if (expandParameter === undefined) throw new Error('未找到参数 Schema 完整结构入口');
    await userEvent.click(expandParameter);
    expect(expandParameter).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByRole('button', { name: '对象 parameter_schema 类型' })).toBeVisible();

    await userEvent.click(screen.getByRole('tab', { name: 'JSON 文本' }));
    const text = (screen.getByRole('textbox', { name: '草稿内容' }) as HTMLTextAreaElement).value;
    expect(text).toContain('"minimumSize"');
    expect(text).toContain('"type": "integer"');
    expect(text).toContain('"title": "最小文件大小"');
    expect(text).toContain('"required": [');
    expect(text).toContain('9007199254740993123');
  });

  it('规则测试可从结构控件新增并保留未冻结字段', async () => {
    routeLosslessSchemaDraft();

    renderManage('/rules/pkg_01');
    await userEvent.click(await screen.findByRole('tab', { name: 'Schema 表单' }));
    await screen.findByTestId('rule-schema-form', {}, { timeout: 10_000 });

    fireEvent.change(screen.getByRole('textbox', { name: '新测试 ID' }), {
      target: { value: 'missing-metadata' }
    });
    await userEvent.click(screen.getByRole('button', { name: '添加测试' }));
    fireEvent.change(screen.getByRole('textbox', { name: '测试 2 说明' }), {
      target: { value: 'metadata 缺失时仍保持稳定身份' }
    });

    await userEvent.click(screen.getByRole('tab', { name: 'JSON 文本' }));
    const text = (screen.getByRole('textbox', { name: '草稿内容' }) as HTMLTextAreaElement).value;
    expect(text).toContain('"missing-metadata"');
    expect(text).toContain('"metadata 缺失时仍保持稳定身份"');
    expect(text).toContain('1e+40');
    expect(text).toContain('9007199254740993123');
  });

  it('分类扩展和任意 JSON payload 可视化构建且精确数字无损', async () => {
    routeLosslessSchemaDraft();

    renderManage('/rules/pkg_01');
    await userEvent.click(await screen.findByRole('tab', { name: 'Schema 表单' }));
    await screen.findByTestId('rule-schema-form', {}, { timeout: 10_000 });

    fireEvent.change(screen.getByRole('textbox', { name: '新 Extension namespace' }), {
      target: { value: 'gallery.identity' }
    });
    await userEvent.click(screen.getByRole('button', { name: '添加 extension' }));
    const version = screen.getByRole('textbox', { name: 'gallery.identity version' });
    const extensionCard = version.closest('article');
    if (extensionCard === null) throw new Error('未找到新增 extension 卡片');
    await userEvent.click(within(extensionCard).getByRole('checkbox', { name: 'semantic' }));
    fireEvent.change(version, { target: { value: '1' } });
    await userEvent.click(within(extensionCard).getByRole('button', { name: '添加 JSON 属性' }));
    fireEvent.change(within(extensionCard).getByRole('textbox', { name: '属性 1 名称' }), {
      target: { value: 'stable_key_prefix' }
    });
    fireEvent.change(
      within(extensionCard).getByRole('textbox', {
        name: '/stable_key_prefix 字符串'
      }),
      { target: { value: 'pixiv:' } }
    );

    const legacyExtensionCard = screen
      .getAllByDisplayValue('example.lossless')
      .map((element) => element.closest('article'))
      .find((element): element is HTMLElement => element instanceof HTMLElement);
    if (legacyExtensionCard === undefined) throw new Error('未找到 legacy extension 卡片');
    const exactNumber = within(legacyExtensionCard).getByRole('textbox', {
      name: '/value（精确 JSON 数字）'
    });
    fireEvent.change(exactNumber, { target: { value: '1e' } });
    expect(screen.getByRole('button', { name: '保存草稿' })).toBeDisabled();
    fireEvent.change(exactNumber, { target: { value: '0.12345678901234567890' } });
    await waitFor(() => expect(screen.getByRole('button', { name: '保存草稿' })).not.toBeDisabled());

    const rawExtensions = screen.getByRole('textbox', { name: 'extensions 原始 JSON' });
    const exactExtensions = (rawExtensions as HTMLTextAreaElement).value;
    fireEvent.change(rawExtensions, { target: { value: '1' } });
    expect(screen.getByText('必须是 object 类型的 JSON 值')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '保存草稿' })).toBeDisabled();
    fireEvent.change(rawExtensions, { target: { value: exactExtensions } });
    await waitFor(() => expect(screen.getByRole('button', { name: '保存草稿' })).not.toBeDisabled());

    await userEvent.click(screen.getByRole('tab', { name: 'JSON 文本' }));
    const text = (screen.getByRole('textbox', { name: '草稿内容' }) as HTMLTextAreaElement).value;
    expect(text).toContain('"gallery.identity"');
    expect(text).toContain('"stable_key_prefix": "pixiv:"');
    expect(text).toContain('0.12345678901234567890');
    expect(text).toContain('1e+40');
    expect(text).toContain('1.2300000000000000001');
  });

  const windowedRuleText = (overrides: Record<string, unknown>) =>
    JSON.stringify(
      {
        rule_set_id: FORM_RULE_SET_ID,
        version: '1.0.0',
        schema_version: 1,
        normalization_algorithm_version: 'gallery-canonical-json-v1',
        compiler_requirement: 'gallery-rule-compiler-v1',
        cel_profile_version: 'gallery-cel-v1',
        parameter_schema: { type: 'object', additionalProperties: false },
        provider_namespaces: [],
        primitives: [{ id: 'base', kind: 'metadata_map', config: { fields: {} } }],
        cel_expressions: [],
        tests: [{ id: 'base-test' }],
        extensions: {},
        ...overrides
      },
      null,
      2
    );

  const openWindowedRule = async (contentText: string) => {
    routeLosslessSchemaDraft(contentText);
    renderManage('/rules/pkg_01');
    await userEvent.click(await screen.findByRole('tab', { name: 'Schema 表单' }));
    await screen.findByTestId('rule-schema-form', {}, { timeout: 10_000 });
  };

  it('RJSF 规则数组只挂载当前 20 项并可前后往返', async () => {
    await openWindowedRule(
      windowedRuleText({
        primitives: Array.from({ length: 21 }, (_, index) => ({
          id: `primitive_${String(index).padStart(2, '0')}`,
          kind: 'metadata_map',
          config: { fields: {} }
        }))
      })
    );

    expect(
      screen.getByText('规则原语 · 第 1 / 2 页 · 本页 20 项 · 共 21 项 · 每页最多 20 项。')
    ).toBeInTheDocument();
    expect(screen.getByDisplayValue('primitive_00')).toBeInTheDocument();
    expect(screen.queryByDisplayValue('primitive_20')).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '下一页：规则原语' }));
    expect(await screen.findByDisplayValue('primitive_20')).toBeInTheDocument();
    expect(screen.queryByDisplayValue('primitive_00')).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '上一页：规则原语' }));
    expect(await screen.findByDisplayValue('primitive_00')).toBeInTheDocument();
  }, 20_000);

  it('参数属性与完整对象结构分别只挂载当前 20 项', async () => {
    const properties = Object.fromEntries(
      Array.from({ length: 21 }, (_, index) => [
        `parameter${String(index).padStart(2, '0')}`,
        { type: 'string', title: `参数 ${index}` }
      ])
    );
    await openWindowedRule(
      windowedRuleText({
        parameter_schema: { type: 'object', additionalProperties: false, properties }
      })
    );

    expect(
      screen.getByText('参数 Schema 属性 · 第 1 / 2 页 · 本页 20 项 · 共 21 项 · 每页最多 20 项。')
    ).toBeInTheDocument();
    expect(screen.getByDisplayValue('parameter00')).toBeInTheDocument();
    expect(screen.queryByDisplayValue('parameter20')).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '下一页：参数 Schema 属性' }));
    expect(await screen.findByDisplayValue('parameter20')).toBeInTheDocument();

    const expandParameter = screen.getAllByRole('button', { name: '展开完整 JSON 结构' }).at(0);
    if (expandParameter === undefined) throw new Error('未找到参数 Schema 完整结构入口');
    await userEvent.click(expandParameter);
    expect(
      screen.getByText('/properties 对象属性 · 第 1 / 2 页 · 本页 20 项 · 共 21 项 · 每页最多 20 项。')
    ).toBeInTheDocument();
  }, 20_000);

  it('规则测试只挂载当前 20 项且新增后跳到包含新项的末页', async () => {
    await openWindowedRule(
      windowedRuleText({
        tests: Array.from({ length: 21 }, (_, index) => ({
          id: `test-${String(index).padStart(2, '0')}`,
          description: `测试 ${index}`
        }))
      })
    );

    expect(screen.getByDisplayValue('test-00')).toBeInTheDocument();
    expect(screen.queryByDisplayValue('test-20')).not.toBeInTheDocument();
    fireEvent.change(screen.getByRole('textbox', { name: '新测试 ID' }), {
      target: { value: 'test-new' }
    });
    await userEvent.click(screen.getByRole('button', { name: '添加测试' }));
    expect(await screen.findByDisplayValue('test-new')).toBeInTheDocument();
    expect(screen.getByText(/规则测试 · 第 2 \/ 2 页 · 本页 2 项 · 共 22 项/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole('tab', { name: 'JSON 文本' }));
    const text = (screen.getByRole('textbox', { name: '草稿内容' }) as HTMLTextAreaElement).value;
    expect(text).toContain('"test-00"');
    expect(text).toContain('"test-20"');
    expect(text).toContain('"test-new"');
  }, 20_000);

  it('Extensions 与嵌套 payload 数组分别只挂载当前 20 项', async () => {
    const extensions = Object.fromEntries(
      Array.from({ length: 21 }, (_, index) => [
        `gallery.extension-${String(index).padStart(2, '0')}`,
        {
          required: false,
          semantic: false,
          payload:
            index === 0
              ? Array.from({ length: 21 }, (_, itemIndex) => `payload-${String(itemIndex).padStart(2, '0')}`)
              : {}
        }
      ])
    );
    await openWindowedRule(windowedRuleText({ extensions }));

    expect(
      screen.getByText('Extensions · 第 1 / 2 页 · 本页 20 项 · 共 21 项 · 每页最多 20 项。')
    ).toBeInTheDocument();
    expect(screen.getByDisplayValue('gallery.extension-00')).toBeInTheDocument();
    expect(screen.queryByDisplayValue('gallery.extension-20')).not.toBeInTheDocument();
    expect(screen.getByDisplayValue('payload-00')).toBeInTheDocument();
    expect(screen.queryByDisplayValue('payload-20')).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '下一页：gallery.extension-00 payload 数组' }));
    expect(await screen.findByDisplayValue('payload-20')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '下一页：Extensions' }));
    expect(await screen.findByDisplayValue('gallery.extension-20')).toBeInTheDocument();
  }, 20_000);

  it('普通字段撤销全部修改后恢复精确草稿文本', async () => {
    routeLosslessSchemaDraft();

    renderManage('/rules/pkg_01');
    await userEvent.click(await screen.findByRole('tab', { name: 'Schema 表单' }));
    await screen.findByTestId('rule-schema-form', {}, { timeout: 10_000 });
    expect(screen.getByText('当前没有可撤销的字段修改。')).toBeInTheDocument();

    const version = await screen.findByRole('textbox', { name: /规则版本/ });
    fireEvent.change(version, { target: { value: '1.0.1' } });
    await userEvent.click(screen.getByRole('button', { name: '撤销字段 /version' }));
    expect(version).toHaveValue('1.0.0');
    expect(screen.getByText('当前没有可撤销的字段修改。')).toBeInTheDocument();

    await userEvent.click(screen.getByRole('tab', { name: 'JSON 文本' }));
    expect(screen.getByRole('textbox', { name: '草稿内容' })).toHaveValue(LOSSLESS_RULE_TEXT);
    expect(screen.getByRole('button', { name: '保存草稿' })).toBeDisabled();
  });

  it('非法 opaque JSON 可独立撤销且不覆盖相邻字段修改', async () => {
    routeLosslessSchemaDraft();

    renderManage('/rules/pkg_01');
    await userEvent.click(await screen.findByRole('tab', { name: 'Schema 表单' }));
    await screen.findByTestId('rule-schema-form', {}, { timeout: 10_000 });
    const version = await screen.findByRole('textbox', { name: /规则版本/ });
    const extensions = screen.getByRole('textbox', { name: /extensions/ }) as HTMLTextAreaElement;
    const exactExtensions = extensions.value;

    fireEvent.change(version, { target: { value: '1.0.1' } });
    fireEvent.change(extensions, { target: { value: '{' } });
    expect(screen.getByRole('button', { name: '保存草稿' })).toBeDisabled();
    await userEvent.click(screen.getByRole('button', { name: '撤销字段 /extensions' }));

    expect(extensions).toHaveValue(exactExtensions);
    expect(version).toHaveValue('1.0.1');
    expect(screen.getByRole('button', { name: '保存草稿' })).not.toBeDisabled();
    expect(screen.getByRole('button', { name: '撤销字段 /version' })).toBeVisible();
  });

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

  it('远端 revision 漂移后按字段撤销仍恢复本地 base 快照', async () => {
    let remoteChanged = false;
    const remoteText = LOSSLESS_RULE_TEXT.replace('"version": "1.0.0"', '"version": "2.0.0"');
    route('GET /api/v1/rule-packages/pkg_01', () =>
      jsonResponse(rulePackage({ ruleSetId: FORM_RULE_SET_ID }))
    );
    route('GET /api/v1/rule-packages/pkg_01/draft', () => {
      const contentText = remoteChanged ? remoteText : LOSSLESS_RULE_TEXT;
      return jsonResponse(
        ruleDraft({
          content: JSON.parse(contentText),
          contentText,
          revision: remoteChanged ? 4 : 3
        })
      );
    });
    route('GET /api/v1/rule-packages/pkg_01/versions', () => jsonResponse({ items: [] }));
    route('GET /api/v1/rule-packages/pkg_01/audits', () => jsonResponse({ items: [] }));
    route('GET /api/v1/rules/schema', () => jsonResponse(RULE_SCHEMA));
    route('GET /api/v1/rules/examples', () => jsonResponse({ items: [] }));
    route('PUT /api/v1/rule-packages/pkg_01/draft', () => {
      remoteChanged = true;
      return faultResponse('RULE_DRAFT_CONFLICT', 409, 'corr-field-undo');
    });

    renderManage('/rules/pkg_01');
    await userEvent.click(await screen.findByRole('tab', { name: 'Schema 表单' }));
    const version = await screen.findByRole('textbox', { name: /规则版本/ });
    await userEvent.clear(version);
    await userEvent.type(version, '1.0.1');
    await userEvent.click(screen.getByRole('button', { name: '保存草稿' }));

    await screen.findByText('服务端草稿已经变化，本地编辑仍被保留');
    await waitFor(() =>
      expect(screen.getByText('服务端草稿 revision').nextElementSibling).toHaveTextContent('4')
    );
    expect(version).toHaveValue('1.0.1');
    await userEvent.click(screen.getByRole('button', { name: '撤销字段 /version' }));
    expect(version).toHaveValue('1.0.0');
    expect(version).not.toHaveValue('2.0.0');
    expect(screen.getByText('服务端草稿已经变化，本地编辑仍被保留')).toBeInTheDocument();
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
    expect(await saveBody).toEqual({ content: '{"version":1}', format: 'json' });

    await userEvent.click(await screen.findByRole('button', { name: '校验草稿' }));
    await screen.findByText('校验通过');
    expect(requestsTo('POST /api/v1/rule-packages/pkg_01/draft/validate')[0]?.ifMatch).toBe('"1"');

    await userEvent.click(screen.getByRole('button', { name: '评估本次修改的影响' }));
    await waitFor(() => expect(requestsTo('POST /api/v1/rules/impact')).toHaveLength(1));
    expect(await impactBody).toEqual({ before: null, after: '{\n  "version": 1\n}' });
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
      content: '{"version":2}',
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
    route('POST /api/v1/rule-versions/diff', () =>
      jsonResponse({
        oldSemanticHash: NEW_RULE_HASH,
        newSemanticHash: OLD_RULE_HASH,
        oldPackageHash: 'c'.repeat(64),
        newPackageHash: 'e'.repeat(64),
        category: 'NO_ACTION',
        parameterCompatible: true,
        bindingReview: false,
        entries: []
      })
    );
    route('GET /api/v1/rule-packages/pkg_01/audits', () => jsonResponse({ items: [] }));
    route(`GET /api/v1/rule-versions/${NEW_RULE_HASH}/export`, () => jsonResponse({ version: 1 }));
    route('POST /api/v1/rule-packages/pkg_01/rollback', (request) => {
      rollbackBody = request.clone().json();
      return jsonResponse(ruleVersion({ semanticHash: OLD_RULE_HASH }));
    });

    renderManage('/rules/pkg_01');
    await screen.findByRole('button', { name: /回滚到/ });
    await selectOption(/回滚到/, '0.9.0 · published · aaaaaaaaaaaa…');
    await userEvent.type(screen.getByRole('textbox', { name: '回滚理由' }), '恢复稳定版本');
    await waitFor(() => expect(screen.getByRole('button', { name: '回滚 current 指针' })).toBeEnabled());
    await userEvent.click(screen.getByRole('button', { name: '回滚 current 指针' }));
    const dialog = await screen.findByRole('dialog', { name: '回滚规则包 current 指针' });
    await userEvent.click(within(dialog).getByRole('button', { name: '确认回滚指针' }));

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

/* ————————————————————————————— 6. 规则生命周期与 ParameterSet ————————————————————————————— */

describe('规则生命周期与 ParameterSet', () => {
  it('ParameterSet Binding 只发送 parameterId 与无损 override，不混入 direct 字段', async () => {
    const parameter = ruleParameterSet();
    let body: Promise<unknown> | undefined;
    route('GET /api/v1/sources', () => jsonResponse({ sources: [SOURCE] }));
    route('GET /api/v1/rule-packages', () =>
      jsonResponse({ items: [rulePackage({ currentSemanticHash: NEW_RULE_HASH })] })
    );
    route('GET /api/v1/rule-packages/pkg_01/versions', () => jsonResponse({ items: [ruleVersion()] }));
    route('GET /api/v1/rule-parameters', () => jsonResponse({ parameterSets: [parameter] }));
    route('GET /api/v1/source-rule-bindings', () => jsonResponse({ bindings: [] }));
    route('GET /api/v1/sources/src_01/effective-rule-binding', () =>
      faultResponse('NOT_FOUND', 404, 'corr-no-effective')
    );
    route('POST /api/v1/source-rule-bindings', (request) => {
      body = request.clone().json();
      return jsonResponse(
        {
          id: 'srbind_01',
          sourceId: SOURCE.id,
          semanticHash: NEW_RULE_HASH,
          parameters: {},
          parametersText: parameter.parametersText,
          priority: 100,
          ruleIrHash: 'd'.repeat(64),
          parameterId: parameter.id,
          parameterRevision: parameter.currentRevision,
          parameterHash: parameter.currentHash,
          override: {},
          overrideText: '{"minimumSize":9007199254740993124}',
          status: 'active',
          createdAt: '2026-07-27T05:00:00Z'
        },
        201
      );
    });

    renderManage('/rules');
    await screen.findByRole('button', { name: /来源/ });
    await selectOption(/来源/, '合成来源 · src_01');
    await selectOption(/绑定参数来源/, 'ParameterSet · 共享参数集 + override');
    await selectOption(/active ParameterSet/, `共享参数 · r2 · ${NEW_RULE_HASH.slice(0, 12)}…`);
    const exactOverride = '{"minimumSize":9007199254740993124}';
    fireEvent.change(screen.getByRole('textbox', { name: /override（精确 JSON 对象文本）/ }), {
      target: { value: exactOverride }
    });
    await userEvent.click(screen.getByRole('button', { name: '创建绑定' }));

    await waitFor(() => expect(requestsTo('POST /api/v1/source-rule-bindings')).toHaveLength(1));
    expect(await body).toEqual({
      sourceId: SOURCE.id,
      parameterId: parameter.id,
      priority: 100,
      override: exactOverride
    });
  });

  it('新 Binding 列出 active 规则包全部可采纳 published 版本及其 ParameterSet', async () => {
    const current = ruleParameterSet();
    const historical = ruleParameterSet({
      id: 'rparam_00000000-0000-7000-8000-000000000002',
      name: '历史参数',
      semanticHash: OLD_RULE_HASH
    });
    route('GET /api/v1/sources', () => jsonResponse({ sources: [SOURCE] }));
    route('GET /api/v1/rule-packages', () =>
      jsonResponse({ items: [rulePackage({ currentSemanticHash: NEW_RULE_HASH })] })
    );
    route('GET /api/v1/rule-packages/pkg_01/versions', () =>
      jsonResponse({
        items: [
          ruleVersion(),
          ruleVersion({ id: 'version_02', semanticHash: OLD_RULE_HASH, version: '0.9.0' })
        ]
      })
    );
    route('GET /api/v1/rule-parameters', () => jsonResponse({ parameterSets: [current, historical] }));
    route('GET /api/v1/source-rule-bindings', () => jsonResponse({ bindings: [] }));
    route('GET /api/v1/sources/src_01/effective-rule-binding', () =>
      faultResponse('NOT_FOUND', 404, 'corr-no-effective')
    );

    renderManage('/rules');
    await screen.findByRole('button', { name: /来源/ });
    await selectOption(/来源/, '合成来源 · src_01');
    await userEvent.click(screen.getByRole('button', { name: /已发布版本/ }));
    expect(
      screen.getByRole('option', { name: `合成规则包 · 1.0.0 · ${NEW_RULE_HASH.slice(0, 12)}…` })
    ).toBeVisible();
    expect(
      screen.getByRole('option', { name: `合成规则包 · 0.9.0 · ${OLD_RULE_HASH.slice(0, 12)}…` })
    ).toBeVisible();
    await userEvent.keyboard('{Escape}');
    await selectOption(/绑定参数来源/, 'ParameterSet · 共享参数集 + override');
    await userEvent.click(screen.getByRole('button', { name: /active ParameterSet/ }));

    expect(
      screen.getByRole('option', { name: `共享参数 · r2 · ${NEW_RULE_HASH.slice(0, 12)}…` })
    ).toBeVisible();
    expect(
      screen.getByRole('option', { name: `历史参数 · r2 · ${OLD_RULE_HASH.slice(0, 12)}…` })
    ).toBeVisible();
  });

  it('ParameterSet 大整数经 Impact 后必须确认，并以 If-Match CAS 更新', async () => {
    let stored = ruleParameterSet();
    let impactBody: Promise<unknown> | undefined;
    let updateBody: Promise<unknown> | undefined;
    route('GET /api/v1/rule-packages/pkg_01', () =>
      jsonResponse(rulePackage({ currentSemanticHash: NEW_RULE_HASH }))
    );
    route('GET /api/v1/rule-packages/pkg_01/draft', () =>
      jsonResponse(ruleDraft({ baseSemanticHash: NEW_RULE_HASH }))
    );
    route('GET /api/v1/rule-packages/pkg_01/versions', () => jsonResponse({ items: [ruleVersion()] }));
    route('GET /api/v1/rule-packages/pkg_01/audits', () => jsonResponse({ items: [] }));
    route(`GET /api/v1/rule-versions/${NEW_RULE_HASH}/export`, () => jsonResponse({ version: 1 }));
    route('GET /api/v1/rules/schema', () => jsonResponse(RULE_SCHEMA));
    route('GET /api/v1/rules/examples', () => jsonResponse({ items: [] }));
    route('GET /api/v1/rule-parameters', () => jsonResponse({ parameterSets: [stored] }));
    route(`POST /api/v1/rule-parameters/${String(stored.id)}/impact`, (request) => {
      impactBody = request.clone().json();
      return jsonResponse(
        ruleImpact({ category: 'RESCAN_PARTIAL', affectedSources: [SOURCE.id], partialRescan: true })
      );
    });
    route(`PUT /api/v1/rule-parameters/${String(stored.id)}`, (request) => {
      updateBody = request.clone().json();
      stored = ruleParameterSet({
        ...stored,
        currentRevision: 3,
        currentHash: '9'.repeat(64),
        parametersText: '{"minimumSize":9007199254740993124}'
      });
      return jsonResponse(stored);
    });

    renderManage('/rules/pkg_01');
    const editor = await screen.findByRole('textbox', { name: '参数（精确 JSON 对象文本）' });
    const nextText = '{"minimumSize":9007199254740993124}';
    fireEvent.change(editor, { target: { value: nextText } });
    await userEvent.click(screen.getByRole('button', { name: '评估参数影响' }));
    await waitFor(() =>
      expect(requestsTo(`POST /api/v1/rule-parameters/${String(stored.id)}/impact`)).toHaveLength(1)
    );
    expect(await impactBody).toEqual({ parameters: nextText });

    const updateButton = screen.getByRole('button', { name: 'CAS 更新 ParameterSet' });
    expect(updateButton).toBeDisabled();
    await userEvent.click(
      screen.getByRole('checkbox', { name: /我已审阅本 revision 与当前参数文本的 Impact/ })
    );
    expect(updateButton).toBeEnabled();
    await userEvent.click(updateButton);
    const dialog = await screen.findByRole('dialog', { name: '更新共享 ParameterSet' });
    await userEvent.click(within(dialog).getByRole('button', { name: '确认更新共享参数' }));

    await waitFor(() =>
      expect(requestsTo(`PUT /api/v1/rule-parameters/${String(stored.id)}`)).toHaveLength(1)
    );
    expect(requestsTo(`PUT /api/v1/rule-parameters/${String(stored.id)}`)[0]?.ifMatch).toBe('"2"');
    expect(await updateBody).toEqual({
      parameters: nextText,
      expectedRevision: 2,
      confirmImpact: true
    });
  });

  it('ParameterSet CAS 冲突保留本地精确文本，并要求显式采用服务器 revision', async () => {
    const parameterId = 'rparam_00000000-0000-7000-8000-000000000001';
    let stored = ruleParameterSet({ id: parameterId });
    route('GET /api/v1/rule-packages/pkg_01', () =>
      jsonResponse(rulePackage({ currentSemanticHash: NEW_RULE_HASH }))
    );
    route('GET /api/v1/rule-packages/pkg_01/draft', () =>
      jsonResponse(ruleDraft({ baseSemanticHash: NEW_RULE_HASH }))
    );
    route('GET /api/v1/rule-packages/pkg_01/versions', () => jsonResponse({ items: [ruleVersion()] }));
    route('GET /api/v1/rule-packages/pkg_01/audits', () => jsonResponse({ items: [] }));
    route(`GET /api/v1/rule-versions/${NEW_RULE_HASH}/export`, () => jsonResponse({ version: 1 }));
    route('GET /api/v1/rules/schema', () => jsonResponse(RULE_SCHEMA));
    route('GET /api/v1/rules/examples', () => jsonResponse({ items: [] }));
    route('GET /api/v1/rule-parameters', () => jsonResponse({ parameterSets: [stored] }));
    route(`POST /api/v1/rule-parameters/${parameterId}/impact`, () => jsonResponse(ruleImpact()));
    route(`PUT /api/v1/rule-parameters/${parameterId}`, () => {
      stored = ruleParameterSet({
        id: parameterId,
        currentRevision: 3,
        currentHash: '8'.repeat(64),
        parametersText: '{"minimumSize":7}'
      });
      return faultResponse('RULE_PARAMETER_CONFLICT', 409, 'corr-param-conflict');
    });

    renderManage('/rules/pkg_01');
    const editor = await screen.findByRole('textbox', { name: '参数（精确 JSON 对象文本）' });
    const localText = '{"minimumSize":9007199254740993999}';
    fireEvent.change(editor, { target: { value: localText } });
    await userEvent.click(screen.getByRole('button', { name: '评估参数影响' }));
    await userEvent.click(
      await screen.findByRole('checkbox', { name: /我已审阅本 revision 与当前参数文本的 Impact/ })
    );
    await userEvent.click(screen.getByRole('button', { name: 'CAS 更新 ParameterSet' }));
    const dialog = await screen.findByRole('dialog', { name: '更新共享 ParameterSet' });
    await userEvent.click(within(dialog).getByRole('button', { name: '确认更新共享参数' }));

    await screen.findByText('ParameterSet revision 已变化');
    expect(screen.getByRole('textbox', { name: '参数（精确 JSON 对象文本）' })).toHaveValue(localText);
    expect(
      screen.getAllByRole('alert').some((item) => item.textContent.includes('RULE_PARAMETER_CONFLICT'))
    ).toBe(true);
    expect(screen.getByRole('button', { name: '采用服务器最新参数' })).toBeEnabled();
  });

  it('RuleVersion 在用时弃用错误常驻，并提交非空理由', async () => {
    let body: Promise<unknown> | undefined;
    route('GET /api/v1/rule-packages/pkg_01', () =>
      jsonResponse(rulePackage({ currentSemanticHash: NEW_RULE_HASH }))
    );
    route('GET /api/v1/rule-packages/pkg_01/draft', () => jsonResponse(ruleDraft()));
    route('GET /api/v1/rule-packages/pkg_01/versions', () =>
      jsonResponse({
        items: [ruleVersion(), ruleVersion({ version: '0.9.0', semanticHash: OLD_RULE_HASH })]
      })
    );
    route('GET /api/v1/rule-packages/pkg_01/audits', () => jsonResponse({ items: [] }));
    route(`GET /api/v1/rule-versions/${NEW_RULE_HASH}/export`, () => jsonResponse({ version: 1 }));
    route('GET /api/v1/rules/schema', () => jsonResponse(RULE_SCHEMA));
    route('GET /api/v1/rules/examples', () => jsonResponse({ items: [] }));
    route(`POST /api/v1/rule-versions/${OLD_RULE_HASH}/deprecate`, (request) => {
      body = request.clone().json();
      return faultResponse('RULE_VERSION_IN_USE', 409, 'corr-version-in-use');
    });

    renderManage('/rules/pkg_01');
    await screen.findByRole('button', { name: /要弃用的 RuleVersion/ });
    await selectOption(/要弃用的 RuleVersion/, '0.9.0 · published · aaaaaaaaaaaa…');
    await userEvent.type(screen.getByRole('textbox', { name: '版本弃用理由' }), '停止新采用');
    await userEvent.click(screen.getByRole('button', { name: '弃用所选 RuleVersion' }));
    const dialog = await screen.findByRole('dialog', { name: '弃用不可变 RuleVersion' });
    await userEvent.click(within(dialog).getByRole('button', { name: '确认弃用版本' }));

    expect(await body).toEqual({ reason: '停止新采用' });
    expect(await screen.findByRole('alert')).toHaveTextContent('RULE_VERSION_IN_USE');
    expect(screen.getByText(/RuleVersion 仍被 current 指针或 active Binding 使用/)).toBeVisible();
  });

  it('规则包弃用带 package revision，成功后锁定作者操作但保留生命周期清理', async () => {
    let packageValue = rulePackage({ revision: 7, currentSemanticHash: NEW_RULE_HASH });
    let body: Promise<unknown> | undefined;
    route('GET /api/v1/rule-packages/pkg_01', () => jsonResponse(packageValue));
    route('GET /api/v1/rule-packages/pkg_01/draft', () => jsonResponse(ruleDraft()));
    route('GET /api/v1/rule-packages/pkg_01/versions', () => jsonResponse({ items: [ruleVersion()] }));
    route('GET /api/v1/rule-packages/pkg_01/audits', () => jsonResponse({ items: [] }));
    route(`GET /api/v1/rule-versions/${NEW_RULE_HASH}/export`, () => jsonResponse({ version: 1 }));
    route('GET /api/v1/rules/schema', () => jsonResponse(RULE_SCHEMA));
    route('GET /api/v1/rules/examples', () => jsonResponse({ items: [] }));
    route('POST /api/v1/rule-packages/pkg_01/deprecate', (request) => {
      body = request.clone().json();
      packageValue = rulePackage({
        revision: 8,
        status: 'deprecated',
        currentSemanticHash: NEW_RULE_HASH
      });
      return jsonResponse(packageValue);
    });

    renderManage('/rules/pkg_01');
    await userEvent.type(await screen.findByRole('textbox', { name: '规则包弃用理由' }), '停止维护');
    await userEvent.click(screen.getByRole('button', { name: '永久弃用规则包' }));
    const dialog = await screen.findByRole('dialog', { name: '永久弃用规则包' });
    await userEvent.click(within(dialog).getByRole('button', { name: '确认永久弃用' }));

    await waitFor(() => expect(requestsTo('POST /api/v1/rule-packages/pkg_01/deprecate')).toHaveLength(1));
    expect(requestsTo('POST /api/v1/rule-packages/pkg_01/deprecate')[0]?.ifMatch).toBe('"7"');
    expect(await body).toEqual({ expectedRevision: 7, reason: '停止维护' });
    expect(await screen.findByRole('heading', { name: '规则包作者操作已锁定' })).toBeVisible();
    expect(screen.queryByRole('button', { name: '保存草稿' })).not.toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '弃用非当前版本' })).toBeVisible();
  });
});

/* ————————————————————————————— 7. 密文只显示一次 ————————————————————————————— */

describe('安全资源分页窗口', () => {
  it('五类资源只渲染当前 50 条，续页失败保留当前页且已访问页不重复请求', async () => {
    const seen = new Map<string, Array<string | null>>();
    const pagedRoute = (
      key: string,
      firstItems: Record<string, unknown>[],
      lastItem: Record<string, unknown>,
      options: { failFirstContinuation?: boolean } = {}
    ) => {
      let continuationAttempts = 0;
      return (_request: Request, url: URL) => {
        const cursor = url.searchParams.get('cursor');
        const values = seen.get(key) ?? [];
        values.push(cursor);
        seen.set(key, values);
        expect(url.searchParams.get('limit')).toBe('50');
        if (cursor === null) {
          return jsonResponse({ [key]: firstItems, nextCursor: `cursor-${key}` });
        }
        expect(cursor).toBe(`cursor-${key}`);
        continuationAttempts += 1;
        if (options.failFirstContinuation === true && continuationAttempts === 1) {
          return faultResponse('RATE_LIMITED', 429, `corr-${key}-page`);
        }
        return jsonResponse({ [key]: [lastItem] });
      };
    };

    const sessions = Array.from({ length: 50 }, (_, index) => ({
      id: `ses_page_${index.toString().padStart(2, '0')}`,
      principalId: 'principal_manage',
      authMethod: 'password',
      clientLabel: `session-${index.toString().padStart(2, '0')}`,
      createdAt: '2026-07-31T01:00:00Z',
      lastSeenAt: '2026-07-31T01:01:00Z',
      expiresAt: '2026-08-01T01:00:00Z',
      revoked: false
    }));
    const tokens = Array.from({ length: 50 }, (_, index) => ({
      id: `tok_page_${index.toString().padStart(2, '0')}`,
      principalId: 'principal_manage',
      name: `token-${index.toString().padStart(2, '0')}`,
      secretPrefix: `tp${index.toString().padStart(2, '0')}`,
      capabilities: ['library.read'],
      scopes: [{ kind: 'global' }],
      createdAt: '2026-07-31T01:00:00Z',
      revoked: false
    }));
    const shares = Array.from({ length: 50 }, (_, index) => ({
      id: `shr_page_${index.toString().padStart(2, '0')}`,
      createdBy: 'principal_manage',
      scopeKind: 'work',
      scopeId: `wrk_page_${index.toString().padStart(2, '0')}`,
      permissions: ['view'],
      secretPrefix: `sp${index.toString().padStart(2, '0')}`,
      createdAt: '2026-07-31T01:00:00Z',
      expiresAt: '2026-08-01T01:00:00Z',
      revoked: false
    }));
    const users = Array.from({ length: 50 }, (_, index) => ({
      id: `usr_page_${index.toString().padStart(2, '0')}`,
      username: `user-${index.toString().padStart(2, '0')}`,
      displayName: `User ${index.toString().padStart(2, '0')}`,
      status: 'active',
      roles: ['viewer'],
      securityVersion: 1,
      createdAt: '2026-07-31T01:00:00Z',
      updatedAt: '2026-07-31T01:00:00Z'
    }));
    const grants = Array.from({ length: 50 }, (_, index) => ({
      id: `grnt_page_${index.toString().padStart(2, '0')}`,
      principalId: 'usr_page_50',
      effect: 'allow',
      capability: `fixture.capability.${index.toString().padStart(2, '0')}`,
      scope: { kind: 'global' },
      revoked: false
    }));

    route(
      'GET /api/v1/sessions',
      pagedRoute(
        'sessions',
        sessions,
        { ...sessions[0], id: 'ses_page_50', clientLabel: 'session-50' },
        { failFirstContinuation: true }
      )
    );
    route(
      'GET /api/v1/api-tokens',
      pagedRoute('tokens', tokens, { ...tokens[0], id: 'tok_page_50', name: 'token-50' })
    );
    route(
      'GET /api/v1/shares',
      pagedRoute('shares', shares, { ...shares[0], id: 'shr_page_50', scopeId: 'wrk_page_50' })
    );
    route(
      'GET /api/v1/admin/users',
      pagedRoute('users', users, {
        ...users[0],
        id: 'usr_page_50',
        username: 'user-50',
        displayName: 'User 50'
      })
    );
    route(
      'GET /api/v1/admin/users/usr_page_50/grants',
      pagedRoute('grants', grants, {
        ...grants[0],
        id: 'grnt_page_50',
        capability: 'fixture.capability.50'
      })
    );
    route('GET /api/v1/security-audits', () => jsonResponse({ audits: [] }));

    renderManage('/security');

    expect(await screen.findByText('session-00')).toBeVisible();
    expect(within(screen.getByRole('table', { name: '活动会话' })).getAllByRole('row')).toHaveLength(51);
    await userEvent.click(screen.getByRole('button', { name: '下一页' }));
    expect(await screen.findByText('下一页会话暂时未能载入')).toBeVisible();
    expect(screen.getByText('session-00')).toBeVisible();
    await userEvent.click(screen.getByRole('button', { name: '重试下一页' }));
    expect(await screen.findByText('session-50')).toBeVisible();
    expect(screen.queryByText('session-00')).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '上一页' }));
    expect(await screen.findByText('session-00')).toBeVisible();
    await userEvent.click(screen.getByRole('button', { name: '下一页' }));
    expect(await screen.findByText('session-50')).toBeVisible();
    expect(seen.get('sessions')).toEqual([null, 'cursor-sessions', 'cursor-sessions']);

    await userEvent.click(screen.getByRole('tab', { name: 'API Token' }));
    expect(await screen.findByText('token-00')).toBeVisible();
    await userEvent.click(screen.getByRole('button', { name: '下一页' }));
    expect(await screen.findByText('token-50')).toBeVisible();
    expect(screen.queryByText('token-00')).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole('tab', { name: '分享' }));
    expect(await screen.findByText('work:wrk_page_00')).toBeVisible();
    await userEvent.click(screen.getByRole('button', { name: '下一页' }));
    expect(await screen.findByText('work:wrk_page_50')).toBeVisible();
    expect(screen.queryByText('work:wrk_page_00')).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole('tab', { name: '账户与授权' }));
    expect(await screen.findByText('user-00')).toBeVisible();
    await userEvent.click(screen.getByRole('button', { name: '下一页' }));
    expect(await screen.findByText('user-50')).toBeVisible();
    expect(screen.queryByText('user-00')).not.toBeInTheDocument();
    const userRow = screen.getByText('user-50').closest('tr');
    expect(userRow).not.toBeNull();
    if (userRow === null) throw new Error('未找到第二页账户行');
    await userEvent.click(within(userRow).getByRole('button', { name: '查看授权' }));
    expect(await screen.findByText('fixture.capability.00')).toBeVisible();
    await userEvent.click(screen.getByRole('button', { name: '下一页' }));
    expect(await screen.findByText('fixture.capability.50')).toBeVisible();
    expect(screen.queryByText('fixture.capability.00')).not.toBeInTheDocument();

    expect(seen.get('tokens')).toEqual([null, 'cursor-tokens']);
    expect(seen.get('shares')).toEqual([null, 'cursor-shares']);
    expect(seen.get('users')).toEqual([null, 'cursor-users']);
    expect(seen.get('grants')).toEqual([null, 'cursor-grants']);
  }, 15_000);
});

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
    expect(screen.queryByText('分享已创建')).not.toBeInTheDocument();
    // 也不会为了再显示一次而重新请求服务端。
    expect(requestsTo('POST /api/v1/shares')).toHaveLength(1);
  });
});
