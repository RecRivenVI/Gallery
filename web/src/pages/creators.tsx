import { useQuery } from '@tanstack/react-query';
import { Link, useParams } from 'react-router-dom';
import { api, expectData } from '../api/client';
import {
  DefinitionList,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  StatusBadge
} from '../components/ui';

export function CreatorsPage() {
  const creators = useQuery({
    queryKey: ['creators'],
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/creators', { signal }))
  });
  if (creators.isPending) return <LoadingState />;
  if (creators.isError) return <ErrorState error={creators.error} onRetry={() => void creators.refetch()} />;
  return (
    <>
      <PageHeader title="创作者" description="CanonicalCreator 合并状态和来源证据由服务端解释。" />
      {creators.data.creators.length === 0 ? (
        <EmptyState title="没有创作者" detail="完成一次有效扫描后会在这里出现。" />
      ) : (
        <div className="card-grid">
          {creators.data.creators.map((creator) => (
            <article className="card" key={creator.id}>
              <h2>
                <Link to={`/creators/${encodeURIComponent(creator.id)}`}>{creator.name}</Link>
              </h2>
              <p>{creator.sourceCount} 个来源</p>
              {creator.mergedInto ? (
                <StatusBadge tone="warning">已合并</StatusBadge>
              ) : (
                <StatusBadge tone="success">有效</StatusBadge>
              )}
            </article>
          ))}
        </div>
      )}
    </>
  );
}

export function CreatorPage() {
  const { creatorId = '' } = useParams();
  const detail = useQuery({
    queryKey: ['creator', creatorId],
    queryFn: async ({ signal }) =>
      expectData(await api.GET('/api/v1/creators/{creatorId}', { signal, params: { path: { creatorId } } }))
  });
  if (detail.isPending) return <LoadingState />;
  if (detail.isError) return <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />;
  return (
    <>
      <PageHeader title={detail.data.creator.name} description="创作者详情与 Source Binding 证据" />
      <section className="panel">
        <DefinitionList
          items={[
            ['Canonical ID', <code>{detail.data.creator.id}</code>],
            ['有效 ID', <code>{detail.data.creator.effectiveId}</code>],
            ['来源数', detail.data.creator.sourceCount]
          ]}
        />
      </section>
      <h2>来源 Binding</h2>
      {detail.data.sourceBindings.length === 0 ? (
        <EmptyState title="没有来源证据" detail="此创作者尚无活跃来源 Binding。" />
      ) : (
        <div className="card-grid">
          {detail.data.sourceBindings.map((binding) => (
            <article className="card" key={binding.bindingId}>
              <StatusBadge>{binding.status}</StatusBadge>
              <DefinitionList
                items={[
                  ['Provider', binding.providerId],
                  ['外部 ID', binding.externalId],
                  ['Source', <code>{binding.sourceId}</code>]
                ]}
              />
            </article>
          ))}
        </div>
      )}
      <p>
        <Link
          to={`/browse?filter=${encodeURIComponent(JSON.stringify({ field: 'creatorId', op: 'eq', value: detail.data.creator.effectiveId }))}`}
        >
          查看相关作品
        </Link>
      </p>
    </>
  );
}
