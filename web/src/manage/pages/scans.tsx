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
import { Badge, Button, Select, useToast } from '../../design';
import { errorCode } from '../../shared/errors';
import { useAnyCapability, JOB_MUTATION_CAPABILITIES } from '../../shared/session';
import {
  newIdempotencyKey,
  useCancelJob,
  useCreateScanJob,
  useJobs,
  useRetryJob,
  useSourceScanStatus,
  useSources,
  type Job,
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

/** 任务列表可选的取数量。契约只有 limit，没有游标，因此这是唯一的「看更多」手段。 */
const JOB_LIMITS = [25, 50, 100, 200] as const;

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

function canCancel(job: Job): boolean {
  return job.status === 'queued' || job.status === 'running';
}

function canRetry(job: Job): boolean {
  return job.status === 'failed' || job.status === 'needs_repair' || job.status === 'cancelled';
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
                  label: `${item.displayName}${item.available ? '' : '（离线）'}`
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
  const [limit, setLimit] = useState<number>(50);
  const jobs = useJobs(status === 'all' ? null : status, limit);
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
          <Select
            label="取回条数"
            options={JOB_LIMITS.map((value) => ({ id: String(value), label: `最近 ${value} 条` }))}
            selectedKey={String(limit)}
            onSelectionChange={(key) => {
              if (key !== null) setLimit(Number(key));
            }}
          />
        </>
      }
    >
      <ContractNoteList area="jobs" only={['jobs-no-cursor']} />
      <InlineError error={cancel.error} title="取消未能完成" />
      <InlineError error={retry.error} title="重试未能开始" />
      <AsyncPanel query={jobs}>
        {(data) => (
          <DataTable
            caption="任务快照"
            rows={data.jobs}
            rowKey={(row) => row.id}
            emptyTitle="这一段快照里没有任务"
            emptyDescription="调整状态筛选或提高取回条数。契约没有游标，无法翻到更早的历史。"
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
                        description={`任务 ${row.id} 会在下一个安全点停止。已经完成的部分不会回滚。变更所需 capability：${JOB_MUTATION_CAPABILITY[row.type]}（绑定 Source 的任务按 Source 作用域判定）。`}
                        onConfirm={() => {
                          cancel.mutate(row.id, {
                            onSuccess: () => {
                              show({ title: '取消已请求', description: `任务 ${row.id}`, tone: 'success' });
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
        )}
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
        lead="发起扫描、观察任务、取消或重试。任务状态永远以 HTTP 快照为准；实时通道断开只影响你多快看到变化，不影响这里显示的数据是否有效。"
      />
      <ScanLauncher />
      <SourceTable />
      <JobTable />
    </>
  );
}
