/*
 * 扫描与任务。
 *
 * 两条不可让步的表达：
 *
 * 1. **任务状态以 HTTP 快照为准。** 列表来自 `GET /api/v1/jobs`，WebSocket 事件只负责让这个
 *    查询失效重取。界面不维护任何由事件累积出来的本地任务状态。
 * 2. **index 档案被拒绝时如实报告。** Source 已发布或已有持久历史后显式请求 index 会被稳定
 *    拒绝为 409。这里把原因讲清楚，并**不会**自动改成 incremental 重发。
 */

import { useState } from 'react';
import { Link } from 'react-router-dom';
import { Badge, Button, Select, TextInput, useToast } from '../../design';
import { errorCode } from '../../shared/errors';
import { useAnyCapability, useCapability, JOB_MUTATION_CAPABILITIES } from '../../shared/session';
import {
  newIdempotencyKey,
  useCancelJob,
  useCreateLibrary,
  useCreateScanJob,
  useCreateSource,
  useJobs,
  useLibraries,
  useRetryJob,
  useSourceScanStatus,
  useSources,
  type Job,
  type JobStatus,
  type ScanProfile
} from '../api';
import {
  JOB_MUTATION_CAPABILITY,
  JOB_STATUS_LABELS,
  JOB_STATUS_TONES,
  JOB_TYPE_LABELS,
  SCAN_PROFILE_DESCRIPTIONS,
  SCAN_PROFILE_LABELS,
  SOURCE_SCAN_STATUS_LABELS,
  SOURCE_SCAN_STATUS_TONES
} from '../labels';
import {
  Absent,
  AsyncPanel,
  BoolBadge,
  ConfirmAction,
  ContractNoteList,
  DataTable,
  Facts,
  InlineError,
  MonoId,
  PageHeader,
  Section,
  formatDateTime
} from '../ui';

const SCAN_PROFILES: readonly ScanProfile[] = ['incremental', 'index', 'verify'];

const JOB_PAGE_SIZE = 50;

const JOB_STATUS_FILTERS = [
  { id: 'all', label: '全部状态' },
  { id: 'queued', label: '排队中' },
  { id: 'running', label: '执行中' },
  { id: 'publishing', label: '发布中' },
  { id: 'cancelling', label: '取消中' },
  { id: 'completed', label: '已完成' },
  { id: 'failed', label: '已失败' },
  { id: 'cancelled', label: '已取消' },
  { id: 'superseded', label: '已被取代' },
  { id: 'needs_repair', label: '需要修复' }
] as const;

/** JSON Schema 的 min/maxLength 按 Unicode code point，不按 UTF-16 code unit。 */
function codePointLength(value: string): number {
  return Array.from(value).length;
}

function SourceCreationExplanation({ error }: { error: unknown }) {
  const code = errorCode(error);
  if (code === 'SOURCE_PATH_INVALID') {
    return (
      <p className="manage-section__description">
        根路径必须是 galleryd 所在机器上已经存在、可解析的绝对目录。浏览器所在机器与服务端不是同一台时，
        请填写服务端看到的路径；本界面不会把它转换成浏览器本机路径。
      </p>
    );
  }
  if (code === 'APPDIRS_SOURCE_OVERLAP' || code === 'SOURCE_ROOTS_OVERLAP') {
    return (
      <p className="manage-section__description">
        Source 不能与 Gallery 的 AppDirs 或其他 Source 互相包含。该隔离同时保护媒体根只读与 Catalog
        可重建边界，不能通过界面放宽。
      </p>
    );
  }
  return null;
}

/**
 * 新实例的资源自举入口。
 *
 * 创建 Library 需要 global library.write，因此可以用 bootstrap capability 隐藏必然失败的入口；
 * 创建 Source 则按所选 Library 授权，bootstrap 不含 scoped grant，必须把最终裁决交给服务端。
 */
function ResourceBootstrap() {
  const { show } = useToast();
  const libraries = useLibraries();
  const createLibrary = useCreateLibrary();
  const createSource = useCreateSource();
  const canCreateLibrary = useCapability('library.write');
  const [libraryName, setLibraryName] = useState('');
  const [libraryId, setLibraryId] = useState<string | null>(null);
  const [sourceName, setSourceName] = useState('');
  const [rootPath, setRootPath] = useState('');

  const normalizedLibraryName = libraryName.trim();
  const normalizedSourceName = sourceName.trim();
  const libraryNameError =
    codePointLength(normalizedLibraryName) > 256 ? '名称不能超过 256 个字符' : undefined;
  const sourceNameError =
    codePointLength(normalizedSourceName) > 256 ? '显示名不能超过 256 个字符' : undefined;
  const rootPathError = codePointLength(rootPath) > 32768 ? '路径不能超过 32768 个字符' : undefined;

  return (
    <Section
      title="Library 与 Source"
      description="先创建资料库，再登记只读媒体根。当前契约只支持创建；提交前请确认名称与服务端绝对路径。"
    >
      <ContractNoteList area="resources" only={['resources-create-only']} />

      {canCreateLibrary ? (
        <div className="manage-form">
          <div className="manage-form__row">
            <TextInput
              label="Library 名称"
              value={libraryName}
              onChange={setLibraryName}
              errorMessage={libraryNameError}
              isDisabled={createLibrary.isPending}
              isRequired
            />
            <div className="manage-form__actions">
              <Button
                variant="primary"
                isPending={createLibrary.isPending}
                isDisabled={normalizedLibraryName === '' || libraryNameError !== undefined}
                onPress={() => {
                  createLibrary.mutate(
                    { name: normalizedLibraryName },
                    {
                      onSuccess: (library) => {
                        setLibraryName('');
                        setLibraryId(library.id);
                        show({ title: `Library ${library.name} 已创建`, tone: 'success' });
                      }
                    }
                  );
                }}
              >
                创建 Library
              </Button>
            </div>
          </div>
          <InlineError error={createLibrary.error} title="Library 未能创建" />
        </div>
      ) : (
        <p className="manage-section__description">
          当前主体在 global scope 没有 library.write，无法创建新的 Library；已有 Library 下的 Source 仍可能由
          scoped grant 授权，最终以服务端响应为准。
        </p>
      )}

      <AsyncPanel query={libraries}>
        {(data) => (
          <>
            <DataTable
              caption="Library"
              rows={data.libraries}
              rowKey={(row) => row.id}
              emptyTitle="还没有创建任何 Library"
              emptyDescription="新实例必须先创建 Library，随后才能登记 Source。"
              columns={[
                { id: 'name', header: '名称', render: (row) => row.name },
                {
                  id: 'id',
                  header: 'Library ID',
                  render: (row) => <MonoId value={row.id} label="Library ID" />
                },
                { id: 'createdAt', header: '创建时间', render: (row) => formatDateTime(row.createdAt) }
              ]}
            />

            {data.libraries.length === 0 ? null : (
              <div className="manage-form">
                <div className="manage-form__row">
                  <Select
                    label="所属 Library"
                    placeholder="选择资料库"
                    options={data.libraries.map((item) => ({
                      id: item.id,
                      label: `${item.name} · ${item.id}`
                    }))}
                    selectedKey={libraryId}
                    onSelectionChange={setLibraryId}
                    isDisabled={createSource.isPending}
                    isRequired
                  />
                  <TextInput
                    label="Source 显示名"
                    value={sourceName}
                    onChange={setSourceName}
                    errorMessage={sourceNameError}
                    isDisabled={createSource.isPending}
                    isRequired
                  />
                </div>
                <TextInput
                  label="Source 根路径"
                  value={rootPath}
                  onChange={setRootPath}
                  errorMessage={rootPathError}
                  isDisabled={createSource.isPending}
                  description="galleryd 所在机器上已存在的绝对目录。路径只在创建请求中发送，Source 列表不会回显。"
                  isMultiline
                  rows={2}
                  isRequired
                />
                <div className="manage-form__actions">
                  <Button
                    variant="primary"
                    isPending={createSource.isPending}
                    isDisabled={
                      libraryId === null ||
                      normalizedSourceName === '' ||
                      rootPath === '' ||
                      sourceNameError !== undefined ||
                      rootPathError !== undefined
                    }
                    onPress={() => {
                      if (libraryId === null) return;
                      createSource.mutate(
                        { libraryId, displayName: normalizedSourceName, rootPath },
                        {
                          onSuccess: (source) => {
                            setSourceName('');
                            setRootPath('');
                            createSource.reset();
                            show({ title: `Source ${source.displayName} 已登记`, tone: 'success' });
                          }
                        }
                      );
                    }}
                  >
                    登记 Source
                  </Button>
                </div>
                <InlineError error={createSource.error} title="Source 未能登记" />
                <SourceCreationExplanation error={createSource.error} />
              </div>
            )}
          </>
        )}
      </AsyncPanel>
    </Section>
  );
}

function canCancel(job: Job): boolean {
  return (
    job.status === 'queued' ||
    job.status === 'running' ||
    ((job.status === 'failed' || job.status === 'needs_repair') &&
      job.failureRetryable === true &&
      job.nextAttemptAt !== null &&
      job.nextAttemptAt !== undefined)
  );
}

function canRetry(job: Job): boolean {
  return (job.status === 'failed' || job.status === 'needs_repair') && job.failureRetryable === true;
}

/**
 * 扫描创建失败的解释。
 *
 * 409 在这里有两种完全不同的含义，混成一句「冲突」会让用户无从下手：
 *   - SCAN_ALREADY_RUNNING：该 Source 已有扫描在跑，等它结束即可；
 *   - CONFLICT + 请求了 index：Source 已发布或已有持久历史，index 档案对它永久不再可用。
 */
function ScanFailureExplanation({ error, profile }: { error: unknown; profile: ScanProfile | undefined }) {
  const code = errorCode(error);
  if (code === 'SCAN_ALREADY_RUNNING') {
    return (
      <p className="manage-section__description">
        该 Source 已有扫描在进行。服务端一次只允许一个扫描 Job，请等待当前扫描结束后再发起；
        重复点击不会排队，只会得到同一个冲突。
      </p>
    );
  }
  if (code === 'CONFLICT' && profile === 'index') {
    return (
      <p className="manage-section__description">
        index 档案只对首次扫描有效。该 Source 已经发布过快照或已有持久历史，服务端会稳定拒绝 显式的 index
        请求。本界面不会替你改成 incremental 再提交——那会让你以为执行的是快速索引，
        实际却做了另一件事。需要继续扫描请显式改选 incremental 或 verify。
      </p>
    );
  }
  return null;
}

function ScanLauncher() {
  const { show } = useToast();
  const sources = useSources();
  const createScan = useCreateScanJob();
  const [sourceId, setSourceId] = useState<string | null>(null);
  const [profile, setProfile] = useState<ScanProfile>('incremental');
  const scanStatus = useSourceScanStatus(sourceId);

  return (
    <Section
      title="发起扫描"
      description="扫描创建携带 Idempotency-Key，网络重发不会产生第二个扫描 Job。扫描永远不写入 Source。"
    >
      <AsyncPanel query={sources}>
        {(data) => (
          <div className="manage-form">
            <div className="manage-form__row">
              <Select
                label="来源"
                placeholder="选择要扫描的来源"
                options={data.sources.map((item) => ({
                  id: item.id,
                  label: `${item.displayName} · ${item.id}${item.available ? '' : '（离线）'}`
                }))}
                selectedKey={sourceId}
                onSelectionChange={setSourceId}
              />
              <Select
                label="扫描档案"
                options={SCAN_PROFILES.map((value) => ({ id: value, label: SCAN_PROFILE_LABELS[value] }))}
                selectedKey={profile}
                onSelectionChange={(key) => {
                  if (key !== null) setProfile(key as ScanProfile);
                }}
              />
              <Button
                variant="primary"
                isDisabled={sourceId === null}
                isPending={createScan.isPending}
                onPress={() => {
                  if (sourceId === null) return;
                  createScan.mutate(
                    { sourceId, scanProfile: profile, idempotencyKey: newIdempotencyKey() },
                    {
                      onSuccess: (job) => {
                        show({
                          title: '扫描任务已排队',
                          description: `任务 ${job.id}，档案 ${job.scanProfile ?? profile}`,
                          tone: 'success'
                        });
                      }
                    }
                  );
                }}
              >
                发起扫描
              </Button>
            </div>
            <p className="manage-section__description">{SCAN_PROFILE_DESCRIPTIONS[profile]}</p>
            {createScan.error === null ? null : (
              <>
                <InlineError error={createScan.error} title="扫描未能创建" />
                <ScanFailureExplanation error={createScan.error} profile={createScan.variables.scanProfile} />
              </>
            )}
          </div>
        )}
      </AsyncPanel>

      {sourceId === null ? null : (
        <AsyncPanel query={scanStatus} idle={null}>
          {(state) => (
            <Facts
              items={[
                {
                  term: '来源状态',
                  value: (
                    <Badge tone={SOURCE_SCAN_STATUS_TONES[state.status] ?? 'neutral'}>
                      {SOURCE_SCAN_STATUS_LABELS[state.status] ?? state.status}
                    </Badge>
                  )
                },
                {
                  term: '有未收敛变更',
                  value: (
                    <BoolBadge
                      value={state.dirty}
                      trueLabel="是"
                      falseLabel="否"
                      trueTone="warning"
                      falseTone="success"
                    />
                  )
                },
                {
                  term: 'Watcher',
                  value: (
                    <BoolBadge
                      value={state.watcherAvailable}
                      trueLabel={state.watcherOverflow ? '可用（已溢出，改用周期收敛）' : '可用'}
                      falseLabel="不可用（周期收敛）"
                    />
                  )
                },
                { term: '待哈希媒体', value: state.pendingHashCount },
                {
                  term: '当前扫描任务',
                  value:
                    state.currentJobId === null || state.currentJobId === undefined ? (
                      <Absent />
                    ) : (
                      <Link to={`/scans/${state.currentJobId}`}>{state.currentJobId}</Link>
                    )
                },
                {
                  term: '当前快照',
                  value:
                    state.currentPublicationId === null || state.currentPublicationId === undefined ? (
                      <Absent />
                    ) : (
                      <MonoId value={state.currentPublicationId} label="快照 ID" />
                    )
                },
                {
                  term: '阻塞原因',
                  value:
                    state.blockingIssueCode === null || state.blockingIssueCode === undefined ? (
                      <Absent />
                    ) : (
                      <Badge tone="danger">{state.blockingIssueCode}</Badge>
                    )
                },
                { term: '最后检查', value: formatDateTime(state.lastCheckedAt) }
              ]}
            />
          )}
        </AsyncPanel>
      )}

      <ContractNoteList area="scan" />
    </Section>
  );
}

function SourceTable() {
  const sources = useSources();
  return (
    <Section
      title="已登记的来源"
      description="Source 只能创建，不能改名、删除或停用；媒体根永久只读，Gallery 不会写入其中任何字节。"
    >
      <AsyncPanel query={sources}>
        {(data) => (
          <DataTable
            caption="已登记的来源"
            rows={data.sources}
            rowKey={(row) => row.id}
            emptyTitle="还没有登记任何来源"
            emptyDescription="在「连接与安全」之外，Source 的登记入口属于资源管理；当前契约只提供创建。"
            columns={[
              {
                id: 'available',
                header: '状态',
                render: (row) => <BoolBadge value={row.available} trueLabel="可用" falseLabel="离线" />
              },
              { id: 'name', header: '显示名', render: (row) => row.displayName },
              { id: 'id', header: 'Source ID', render: (row) => <MonoId value={row.id} label="Source ID" /> },
              {
                id: 'library',
                header: 'Library ID',
                render: (row) => <MonoId value={row.libraryId} label="Library ID" />
              },
              {
                id: 'readonly',
                header: '只读',
                render: (row) => (
                  <BoolBadge value={row.readOnly} trueLabel="只读" falseLabel="可写" falseTone="danger" />
                )
              },
              {
                id: 'presentation',
                header: '规则呈现',
                render: (row) =>
                  row.presentation === null || row.presentation === undefined ? (
                    <Absent>未绑定规则或绑定冲突</Absent>
                  ) : (
                    (row.presentation.name ?? row.displayName)
                  )
              },
              { id: 'createdAt', header: '登记时间', render: (row) => formatDateTime(row.createdAt) }
            ]}
          />
        )}
      </AsyncPanel>
    </Section>
  );
}

function JobTable() {
  const { show } = useToast();
  const [status, setStatus] = useState<string>('all');
  const jobs = useJobs(status === 'all' ? null : (status as JobStatus), JOB_PAGE_SIZE);
  // 追加页失败时 TanStack 会同时把整个 InfiniteQuery 标为 isError；已有第一页仍是
  // 可用的 HTTP snapshot，不能因此被通用 AsyncPanel 替换成整块错误页。
  const jobSnapshotQuery = { ...jobs, isError: jobs.isError && !jobs.isFetchNextPageError };
  const cancel = useCancelJob();
  const retry = useRetryJob();
  // capability 只用于隐藏明显不可用的入口：任务的变更权限是按 Job 类别与 Source 派生的，
  // 这里只能判断「是否可能有权变更某类任务」，最终仍由服务端裁决。
  const mayMutate = useAnyCapability(JOB_MUTATION_CAPABILITIES);

  return (
    <Section
      title="任务"
      description="这是任务状态的权威来源。实时事件只负责让它失效重取，界面不用事件累积本地状态。"
      actions={
        <>
          <Select
            label="状态"
            options={JOB_STATUS_FILTERS.map((item) => ({ id: item.id, label: item.label }))}
            selectedKey={status}
            onSelectionChange={(key) => {
              if (key !== null) setStatus(key);
            }}
          />
        </>
      }
    >
      <InlineError error={cancel.error} title="取消未能完成" />
      <InlineError error={retry.error} title="重试未能开始" />
      <InlineError error={jobs.isFetchNextPageError ? jobs.error : null} title="更早的任务暂时未能载入" />
      <AsyncPanel query={jobSnapshotQuery}>
        {(data) => {
          const rows = data.pages.flatMap((page) => page.jobs);
          return (
            <>
              <p className="manage-section__description">
                已按新到旧载入 {rows.length} 条{jobs.hasNextPage ? '（还有更早任务）' : '（已到末页）'}。
              </p>
              <DataTable
                caption="任务快照"
                rows={rows}
                rowKey={(row) => row.id}
                emptyTitle="当前筛选下没有任务"
                emptyDescription="调整状态筛选后再试。"
                columns={[
                  {
                    id: 'status',
                    header: '状态',
                    render: (row) => (
                      <Badge tone={JOB_STATUS_TONES[row.status]}>{JOB_STATUS_LABELS[row.status]}</Badge>
                    )
                  },
                  { id: 'type', header: '类型', render: (row) => JOB_TYPE_LABELS[row.type] },
                  {
                    id: 'id',
                    header: '任务 ID',
                    render: (row) => <Link to={`/scans/${row.id}`}>{row.id}</Link>
                  },
                  {
                    id: 'source',
                    header: 'Source',
                    render: (row) =>
                      row.sourceId === undefined ? (
                        <Absent>不绑定 Source</Absent>
                      ) : (
                        <MonoId value={row.sourceId} label="Source ID" />
                      )
                  },
                  { id: 'attempt', header: 'Attempt', render: (row) => row.attempt },
                  {
                    id: 'progress',
                    header: '进度',
                    render: (row) =>
                      row.progress.total > 0
                        ? `${row.progress.current} / ${row.progress.total}${row.progress.estimated === true ? '（估算）' : ''}`
                        : String(row.progress.current)
                  },
                  { id: 'stage', header: '阶段', render: (row) => row.stage, wrap: true },
                  {
                    id: 'nextAttemptAt',
                    header: '下次重试',
                    render: (row) =>
                      row.nextAttemptAt === null || row.nextAttemptAt === undefined ? (
                        <Absent />
                      ) : (
                        formatDateTime(row.nextAttemptAt)
                      )
                  },
                  {
                    id: 'profile',
                    header: '扫描档案',
                    render: (row) => row.scanProfile ?? <Absent />
                  },
                  {
                    id: 'issue',
                    header: '失败码',
                    render: (row) =>
                      row.issueCode === null || row.issueCode === undefined ? (
                        <Absent />
                      ) : (
                        <Badge tone="danger">{row.issueCode}</Badge>
                      )
                  },
                  { id: 'updatedAt', header: '更新时间', render: (row) => formatDateTime(row.updatedAt) },
                  {
                    id: 'actions',
                    header: '操作',
                    render: (row) => (
                      <span className="manage-cell-actions">
                        {mayMutate && canCancel(row) ? (
                          <ConfirmAction
                            label="取消"
                            dialogTitle="取消任务"
                            confirmLabel="确认取消"
                            description={
                              (row.status === 'failed' || row.status === 'needs_repair') &&
                              row.failureRetryable === true &&
                              row.nextAttemptAt !== null &&
                              row.nextAttemptAt !== undefined
                                ? `任务 ${row.id} 正在等待 ${formatDateTime(row.nextAttemptAt)} 自动重试。取消后不会再次入队，既有失败 Attempt 保持不变。变更所需 capability：${JOB_MUTATION_CAPABILITY[row.type]}。`
                                : `任务 ${row.id} 会在下一个安全点停止。已经完成的部分不会回滚。变更所需 capability：${JOB_MUTATION_CAPABILITY[row.type]}（绑定 Source 的任务按 Source 作用域判定）。`
                            }
                            onConfirm={() => {
                              cancel.mutate(row.id, {
                                onSuccess: () => {
                                  show({
                                    title: '取消已请求',
                                    description: `任务 ${row.id}`,
                                    tone: 'success'
                                  });
                                }
                              });
                            }}
                          />
                        ) : null}
                        {mayMutate && canRetry(row) ? (
                          <Button
                            variant="secondary"
                            onPress={() => {
                              retry.mutate(row.id, {
                                onSuccess: (job) => {
                                  show({
                                    title: '已开始新的 Attempt',
                                    description: `任务 ${job.id} 仍是同一个 ID，当前 Attempt ${job.attempt}`,
                                    tone: 'success'
                                  });
                                }
                              });
                            }}
                          >
                            重试
                          </Button>
                        ) : null}
                        {mayMutate ? null : <Absent>无变更入口</Absent>}
                      </span>
                    )
                  }
                ]}
              />
              {jobs.hasNextPage ? (
                <div className="manage-form__actions">
                  <Button
                    variant="secondary"
                    isPending={jobs.isFetchingNextPage}
                    onPress={() => void jobs.fetchNextPage()}
                  >
                    {jobs.isFetchNextPageError ? '重试加载更早任务' : '加载更早任务'}
                  </Button>
                </div>
              ) : null}
            </>
          );
        }}
      </AsyncPanel>
      <ContractNoteList area="jobs" only={['jobs-derived-authorization', 'jobs-retry-same-id']} />
    </Section>
  );
}

export function ScansPage() {
  return (
    <>
      <PageHeader
        title="扫描与任务"
        lead="创建 Library、登记只读 Source、发起扫描并观察任务。任务状态永远以 HTTP 快照为准；实时通道断开只影响你多快看到变化，不影响这里显示的数据是否有效。"
      />
      <ResourceBootstrap />
      <ScanLauncher />
      <SourceTable />
      <JobTable />
    </>
  );
}
