import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Button } from 'react-aria-components';
import { api, csrfHeaders, errorMessage, expectData } from '../api/client';
import { useSession } from '../auth/session';
import {
  DefinitionList,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  StatusBadge
} from '../components/ui';

export function GovernancePage() {
  const { bootstrap, can } = useSession();
  const client = useQueryClient();
  const issues = useQuery({
    queryKey: ['binding-issues'],
    queryFn: async ({ signal }) =>
      expectData(
        await api.GET('/api/v1/binding-issues', { signal, params: { query: { status: 'open', limit: 100 } } })
      )
  });
  const orphans = useQuery({
    queryKey: ['orphan-candidates'],
    queryFn: async ({ signal }) =>
      expectData(await api.GET('/api/v1/orphan-candidates', { signal, params: { query: { limit: 100 } } }))
  });
  const merges = useQuery({
    queryKey: ['creator-merges'],
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/creators/merges', { signal }))
  });
  const dismiss = useMutation({
    mutationFn: async ({ id, version }: { id: string; version: number }) =>
      expectData(
        await api.POST('/api/v1/binding-issues/{issueId}/dismiss', {
          params: { path: { issueId: id }, header: csrfHeaders(bootstrap.csrfToken) },
          body: { version }
        })
      ),
    onSuccess: () => client.invalidateQueries({ queryKey: ['binding-issues'] })
  });
  const orphan = useMutation({
    mutationFn: async ({ id, decision }: { id: string; decision: 'retain' | 'confirm_orphaned' }) =>
      expectData(
        await api.POST('/api/v1/orphan-candidates/{bindingId}/decide', {
          params: { path: { bindingId: id }, header: csrfHeaders(bootstrap.csrfToken) },
          body: { decision }
        })
      ),
    onSuccess: () => client.invalidateQueries({ queryKey: ['orphan-candidates'] })
  });
  const undoMerge = useMutation({
    mutationFn: async (id: string) =>
      expectData(
        await api.DELETE('/api/v1/creators/merges/{mergeId}', {
          params: { path: { mergeId: id }, header: csrfHeaders(bootstrap.csrfToken) }
        })
      ),
    onSuccess: () => client.invalidateQueries({ queryKey: ['creator-merges'] })
  });
  if (issues.isPending || orphans.isPending || merges.isPending) return <LoadingState />;
  if (issues.isError) return <ErrorState error={issues.error} />;
  if (orphans.isError) return <ErrorState error={orphans.error} />;
  if (merges.isError) return <ErrorState error={merges.error} />;
  const error = dismiss.error ?? orphan.error ?? undoMerge.error;
  return (
    <>
      <PageHeader
        title="人工治理"
        description="冲突、结构变化、孤儿候选和创作者合并都保留版本与可解释证据。"
      />
      {error && (
        <p className="field-error" role="alert">
          {errorMessage(error)}
        </p>
      )}
      <h2>开放 Binding issue</h2>
      {issues.data.issues.length === 0 ? (
        <EmptyState title="没有开放 issue" detail="当前无需人工消歧。" />
      ) : (
        <div className="card-grid">
          {issues.data.issues.map((issue) => (
            <article className="card" key={issue.id}>
              <h3>
                {issue.entityType} · {issue.code}
              </h3>
              <StatusBadge tone="warning">{issue.status}</StatusBadge>
              <DefinitionList
                items={[
                  ['Source', <code>{issue.sourceId}</code>],
                  ['候选', issue.candidateCount],
                  ['版本', issue.version],
                  ['结构', issue.structureKind ?? '—']
                ]}
              />
              {issue.candidates.map((candidate) => (
                <p key={candidate.candidateId}>
                  {candidate.label} · {candidate.matchSignal}
                </p>
              ))}
              {can('bindings.resolve') && (
                <Button
                  className="button secondary"
                  onPress={() => dismiss.mutate({ id: issue.id, version: issue.version })}
                >
                  忽略此版本
                </Button>
              )}
            </article>
          ))}
        </div>
      )}
      <h2>孤儿候选</h2>
      {orphans.data.candidates.length === 0 ? (
        <EmptyState title="没有孤儿候选" detail="Source 缺失事实尚未达到人工确认阈值。" />
      ) : (
        <div className="card-grid">
          {orphans.data.candidates.map((item) => (
            <article className="card" key={item.bindingId}>
              <h3>{item.canonicalLabel}</h3>
              <DefinitionList
                items={[
                  ['类型', item.entityType],
                  ['遗漏扫描', `${item.missedScans}/${item.retentionThreshold}`],
                  ['Canonical', <code>{item.canonicalId}</code>]
                ]}
              />
              {can('bindings.resolve') && (
                <div className="button-row">
                  <Button
                    className="button secondary"
                    onPress={() => orphan.mutate({ id: item.bindingId, decision: 'retain' })}
                  >
                    保留
                  </Button>
                  <Button
                    className="button danger"
                    onPress={() => orphan.mutate({ id: item.bindingId, decision: 'confirm_orphaned' })}
                  >
                    确认 orphaned
                  </Button>
                </div>
              )}
            </article>
          ))}
        </div>
      )}
      <h2>创作者合并记录</h2>
      <div className="card-grid">
        {merges.data.merges.map((merge) => (
          <article className="card" key={merge.id}>
            <StatusBadge>{merge.status}</StatusBadge>
            <DefinitionList
              items={[
                ['目标', <code>{merge.targetCreatorId}</code>],
                ['吸收数', merge.absorbedCreatorIds.length],
                ['记录', <code>{merge.id}</code>]
              ]}
            />
            {can('bindings.resolve') && merge.status === 'applied' && (
              <Button className="button danger" onPress={() => undoMerge.mutate(merge.id)}>
                撤销合并
              </Button>
            )}
          </article>
        ))}
      </div>
    </>
  );
}
