import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Select, SelectValue, ListBox, ListBoxItem, Popover } from 'react-aria-components';
import { Link, useParams, useSearchParams } from 'react-router-dom';
import { api, csrfHeaders, errorMessage, expectData } from '../api/client';
import { useSession } from '../auth/session';
import { JOB_MUTATION_CAPABILITIES } from '../auth/capabilities';
import {
  ConfirmAction,
  DefinitionList,
  EmptyState,
  ErrorState,
  formatDate,
  LoadingState,
  PageHeader,
  ProgressBar,
  StatusBadge
} from '../components/ui';

export function JobsPage() {
  const [params, setParams] = useSearchParams();
  const status = params.get('status') ?? '';
  const jobs = useQuery({
    queryKey: ['jobs', status],
    queryFn: async ({ signal }) =>
      expectData(
        await api.GET('/api/v1/jobs', {
          signal,
          params: { query: { status: status || undefined, limit: 100 } }
        })
      )
  });
  if (jobs.isPending) return <LoadingState />;
  if (jobs.isError) return <ErrorState error={jobs.error} onRetry={() => void jobs.refetch()} />;
  return (
    <>
      <PageHeader
        title="持久任务"
        description="WebSocket 只做提示；断线后始终用 HTTP snapshot 恢复。"
        actions={
          <Select
            selectedKey={status || 'all'}
            onSelectionChange={(key) => setParams(key === 'all' ? {} : { status: String(key) })}
          >
            <Button className="select-button">
              <SelectValue />
            </Button>
            <Popover>
              <ListBox>
                {['all', 'queued', 'running', 'completed', 'failed', 'cancelled'].map((value) => (
                  <ListBoxItem id={value} key={value}>
                    {value}
                  </ListBoxItem>
                ))}
              </ListBox>
            </Popover>
          </Select>
        }
      />
      {jobs.data.jobs.length === 0 ? (
        <EmptyState title="没有任务" detail="当前过滤条件下没有持久 Job。" />
      ) : (
        <div className="table-wrap">
          <table className="data-grid">
            <thead>
              <tr>
                <th>任务</th>
                <th>类型</th>
                <th>状态</th>
                <th>进度</th>
                <th>更新时间</th>
              </tr>
            </thead>
            <tbody>
              {jobs.data.jobs.map((job) => (
                <tr key={job.id}>
                  <td>
                    <Link to={`/jobs/${encodeURIComponent(job.id)}`}>{job.id}</Link>
                  </td>
                  <td>{job.type}</td>
                  <td>
                    <StatusBadge
                      tone={
                        job.status === 'completed' ? 'success' : job.status === 'failed' ? 'danger' : 'info'
                      }
                    >
                      {job.status}
                    </StatusBadge>
                  </td>
                  <td>
                    <ProgressBar
                      value={progressRatio(job.progress)}
                      label={`${Math.round(progressRatio(job.progress) * 100)}%`}
                    />
                  </td>
                  <td>{formatDate(job.updatedAt)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

export function JobPage() {
  const { jobId = '' } = useParams();
  const { bootstrap, canAny } = useSession();
  // 服务端按 Job 类别派生取消/重试所需的 capability，没有单一的 jobs.cancel/jobs.retry；
  // 前端只判断"是否可能有权变更某类 Job"，最终裁决与结构化错误仍由服务端给出。
  const canMutateJob = canAny(JOB_MUTATION_CAPABILITIES);
  const client = useQueryClient();
  const job = useQuery({
    queryKey: ['jobs', jobId],
    queryFn: async ({ signal }) =>
      expectData(await api.GET('/api/v1/jobs/{jobId}', { signal, params: { path: { jobId } } })),
    refetchInterval: (query) =>
      ['queued', 'running'].includes(query.state.data?.status ?? '') ? 2000 : false
  });
  const attempts = useQuery({
    queryKey: ['job-attempts', jobId],
    queryFn: async ({ signal }) =>
      expectData(await api.GET('/api/v1/jobs/{jobId}/attempts', { signal, params: { path: { jobId } } }))
  });
  const cancel = useMutation({
    mutationFn: async () =>
      expectData(
        await api.POST('/api/v1/jobs/{jobId}/cancel', {
          params: { path: { jobId }, header: csrfHeaders(bootstrap.csrfToken) }
        })
      ),
    onSuccess: () => client.invalidateQueries({ queryKey: ['jobs'] })
  });
  const retry = useMutation({
    mutationFn: async () =>
      expectData(
        await api.POST('/api/v1/jobs/{jobId}/retry', {
          params: { path: { jobId }, header: csrfHeaders(bootstrap.csrfToken) }
        })
      ),
    onSuccess: () => client.invalidateQueries({ queryKey: ['jobs'] })
  });
  if (job.isPending || attempts.isPending) return <LoadingState />;
  if (job.isError) return <ErrorState error={job.error} onRetry={() => void job.refetch()} />;
  if (attempts.isError) return <ErrorState error={attempts.error} />;
  return (
    <>
      <PageHeader
        title={job.data.type}
        description={job.data.id}
        actions={
          <StatusBadge
            tone={
              job.data.status === 'completed' ? 'success' : job.data.status === 'failed' ? 'danger' : 'info'
            }
          >
            {job.data.status}
          </StatusBadge>
        }
      />
      <section className="panel">
        <ProgressBar
          value={progressRatio(job.data.progress)}
          label={`任务进度 ${Math.round(progressRatio(job.data.progress) * 100)}%`}
        />
        <DefinitionList
          items={[
            ['状态', job.data.status],
            ['阶段', job.data.stage],
            ['当前 Attempt', job.data.attempt],
            ['创建', formatDate(job.data.createdAt)],
            ['更新', formatDate(job.data.updatedAt)],
            ['错误 code', job.data.issueCode ?? '—']
          ]}
        />
        <div className="button-row">
          {canMutateJob && ['queued', 'running'].includes(job.data.status) && (
            <ConfirmAction
              label="取消任务"
              title="取消持久任务？"
              detail="服务端会持久化取消请求，并由 worker 在安全点收敛。"
              danger
              onConfirm={async () => {
                await cancel.mutateAsync();
              }}
            />
          )}
          {canMutateJob && ['failed', 'cancelled'].includes(job.data.status) && (
            <Button className="button primary" onPress={() => retry.mutate()}>
              创建新 Attempt
            </Button>
          )}
        </div>
        {(cancel.isError || retry.isError) && (
          <p className="field-error" role="alert">
            {errorMessage(cancel.error ?? retry.error)}
          </p>
        )}
      </section>
      <h2>尝试记录</h2>
      <div className="table-wrap">
        <table className="data-grid">
          <thead>
            <tr>
              <th>#</th>
              <th>状态</th>
              <th>开始</th>
              <th>结束</th>
            </tr>
          </thead>
          <tbody>
            {attempts.data.attempts.map((attempt) => (
              <tr key={attempt.attempt}>
                <td>{attempt.attempt}</td>
                <td>{attempt.status}</td>
                <td>{formatDate(attempt.startedAt)}</td>
                <td>{formatDate(attempt.finishedAt)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}

function progressRatio(progress: { current: number; total: number }): number {
  return progress.total > 0 ? progress.current / progress.total : 0;
}
