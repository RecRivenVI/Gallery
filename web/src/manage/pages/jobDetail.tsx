/*
 * 任务详情与 Attempt 历史。
 *
 * 重试不会产生新的任务 ID，而是在同一个 Job 上追加一次 Attempt。因此「这个任务失败过几次、
 * 每次失败的稳定 code 是什么」只能在这里看到——任务列表只显示当前 Attempt。
 */

import { Link, useParams } from 'react-router-dom';
import { Badge } from '../../design';
import { useJob, useJobAttempts } from '../api';
import { JOB_MUTATION_CAPABILITY, JOB_STATUS_LABELS, JOB_STATUS_TONES, JOB_TYPE_LABELS } from '../labels';
import { Absent, AsyncPanel, DataTable, Facts, MonoId, PageHeader, Section, formatDateTime } from '../ui';

const ATTEMPT_STATUS_LABELS: Record<string, string> = {
  queued: '排队中',
  running: '执行中',
  completed: '已完成',
  failed: '已失败',
  cancelled: '已取消',
  recovered: '已回收（租约过期后被恢复）'
};

export function JobDetailPage() {
  const params = useParams<{ jobId: string }>();
  const jobId = params.jobId;
  const job = useJob(jobId);
  const attempts = useJobAttempts(jobId);

  return (
    <>
      <PageHeader
        title="任务详情"
        lead={
          <>
            <Link to="/scans">← 返回扫描与任务</Link>
            {' · '}
            任务 ID 在重试后保持不变，每次重试只是新增一个 Attempt。
          </>
        }
      />

      <Section title="任务快照" description="所有字段都来自 GET /api/v1/jobs/{jobId}，不是事件推断的结果。">
        <AsyncPanel query={job}>
          {(data) => (
            <Facts
              items={[
                {
                  term: '状态',
                  value: <Badge tone={JOB_STATUS_TONES[data.status]}>{JOB_STATUS_LABELS[data.status]}</Badge>
                },
                { term: '类型', value: JOB_TYPE_LABELS[data.type] },
                { term: '任务 ID', value: <MonoId value={data.id} label="任务 ID" /> },
                {
                  term: 'Source',
                  value:
                    data.sourceId === undefined ? (
                      <Absent>不绑定 Source</Absent>
                    ) : (
                      <MonoId value={data.sourceId} label="Source ID" />
                    )
                },
                { term: '当前 Attempt', value: data.attempt },
                { term: '阶段', value: data.stage },
                {
                  term: '进度',
                  value:
                    data.progress.total > 0
                      ? `${data.progress.current} / ${data.progress.total}${data.progress.estimated === true ? '（估算）' : ''}`
                      : String(data.progress.current)
                },
                { term: '进度序号', value: data.progress.sequence },
                { term: '扫描档案', value: data.scanProfile ?? <Absent /> },
                {
                  term: '失败码',
                  value:
                    data.issueCode === null || data.issueCode === undefined ? (
                      <Absent />
                    ) : (
                      <Badge tone="danger">{data.issueCode}</Badge>
                    )
                },
                {
                  term: '失败可重试',
                  value:
                    data.failureRetryable === undefined ? <Absent /> : data.failureRetryable ? '是' : '否'
                },
                {
                  term: '已请求取消',
                  value: data.cancelRequested === undefined ? <Absent /> : data.cancelRequested ? '是' : '否'
                },
                { term: '下次尝试时间', value: formatDateTime(data.nextAttemptAt) },
                { term: '资源类别', value: data.resourceClass ?? <Absent /> },
                { term: '目标资源', value: data.targetResource ?? <Absent /> },
                {
                  term: '产出快照',
                  value:
                    data.queryPublicationId === null || data.queryPublicationId === undefined ? (
                      <Absent />
                    ) : (
                      <MonoId value={data.queryPublicationId} label="快照 ID" />
                    )
                },
                { term: '规则 semanticHash', value: data.ruleSemanticHash ?? <Absent /> },
                { term: '规则 IR hash', value: data.ruleIrHash ?? <Absent /> },
                { term: '参数 hash', value: data.ruleParametersHash ?? <Absent /> },
                { term: '编译器版本', value: data.compilerVersion ?? <Absent /> },
                { term: 'CEL Profile', value: data.celProfileVersion ?? <Absent /> },
                { term: '扩展注册表', value: data.extensionRegistryVersion ?? <Absent /> },
                { term: '创建时间', value: formatDateTime(data.createdAt) },
                { term: '开始时间', value: formatDateTime(data.startedAt) },
                { term: '结束时间', value: formatDateTime(data.finishedAt) },
                { term: '变更所需 capability', value: JOB_MUTATION_CAPABILITY[data.type] }
              ]}
            />
          )}
        </AsyncPanel>
      </Section>

      <Section
        title="Attempt 历史"
        description="租约过期后被回收的 Attempt 状态是 recovered。它不代表任务失败，只代表那一次执行没有正常收尾。"
      >
        <AsyncPanel query={attempts}>
          {(data) => (
            <DataTable
              caption="Attempt 历史"
              rows={data.attempts}
              rowKey={(row) => row.attemptId}
              emptyTitle="还没有任何 Attempt"
              emptyDescription="任务尚未被调度执行。"
              columns={[
                { id: 'attempt', header: '#', render: (row) => row.attempt },
                {
                  id: 'status',
                  header: '状态',
                  render: (row) => <Badge>{ATTEMPT_STATUS_LABELS[row.status] ?? row.status}</Badge>
                },
                { id: 'resource', header: '资源类别', render: (row) => row.resourceClass },
                {
                  id: 'error',
                  header: '失败码',
                  render: (row) =>
                    row.errorCode === null || row.errorCode === undefined ? (
                      <Absent />
                    ) : (
                      <Badge tone="danger">{row.errorCode}</Badge>
                    )
                },
                {
                  id: 'retryable',
                  header: '可重试',
                  render: (row) =>
                    row.errorRetryable === undefined ? <Absent /> : row.errorRetryable ? '是' : '否'
                },
                { id: 'progress', header: '进度序号', render: (row) => row.progressSequence },
                { id: 'started', header: '开始', render: (row) => formatDateTime(row.startedAt) },
                { id: 'heartbeat', header: '心跳', render: (row) => formatDateTime(row.heartbeatAt) },
                { id: 'finished', header: '结束', render: (row) => formatDateTime(row.finishedAt) },
                {
                  id: 'id',
                  header: 'Attempt ID',
                  render: (row) => <MonoId value={row.attemptId} label="Attempt ID" />
                }
              ]}
            />
          )}
        </AsyncPanel>
      </Section>
    </>
  );
}
