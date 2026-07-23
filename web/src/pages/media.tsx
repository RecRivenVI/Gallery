import { useMutation, useQuery } from '@tanstack/react-query';
import { Button } from 'react-aria-components';
import { Link, useParams, useSearchParams } from 'react-router-dom';
import { api, csrfHeaders, errorMessage, expectData } from '../api/client';
import { useSession } from '../auth/session';
import { DefinitionList, ErrorState, LoadingState, PageHeader, StatusBadge } from '../components/ui';

export function MediaPage() {
  const { mediaId = '' } = useParams();
  const [search] = useSearchParams();
  const publication = search.get('publication') ?? undefined;
  const { bootstrap, can } = useSession();
  const media = useQuery({
    queryKey: ['media-detail', mediaId, publication],
    queryFn: async ({ signal }) =>
      expectData(
        await api.GET('/api/v1/media/{mediaId}', {
          signal,
          params: { path: { mediaId }, query: { queryPublicationId: publication } }
        })
      )
  });
  const verify = useMutation({
    mutationFn: async () =>
      expectData(
        await api.POST('/api/v1/media/{mediaId}/verification-jobs', {
          params: {
            path: { mediaId },
            header: csrfHeaders(bootstrap.csrfToken),
            query: { queryPublicationId: publication }
          }
        })
      )
  });
  const derive = useMutation({
    mutationFn: async () =>
      expectData(
        await api.POST('/api/v1/media/{mediaId}/derived-assets', {
          params: {
            path: { mediaId },
            header: csrfHeaders(bootstrap.csrfToken),
            query: { queryPublicationId: publication }
          },
          body: {
            transformId: 'thumbnail-jpeg',
            transformVersion: 'v1',
            parameters: { maxWidth: 1600, maxHeight: 1600, quality: 85 }
          }
        })
      )
  });
  if (media.isPending) return <LoadingState />;
  if (media.isError) return <ErrorState error={media.error} onRetry={() => void media.refetch()} />;
  const item = media.data;
  const src = `/api/v1/media/${encodeURIComponent(item.id)}/content${publication ? `?queryPublicationId=${encodeURIComponent(publication)}` : ''}`;
  return (
    <>
      <PageHeader
        title={`媒体 #${item.ordinal}`}
        description={item.mimeType}
        actions={
          <StatusBadge tone={item.available ? 'success' : 'warning'}>
            {item.available ? '在线' : '离线'}
          </StatusBadge>
        }
      />
      <div className="detail-layout">
        <section className="viewer panel">
          {item.available && item.contentVerificationState === 'content_verified' ? (
            item.mimeType.startsWith('image/') ? (
              <img src={src} alt="媒体预览" />
            ) : item.mimeType.startsWith('video/') ? (
              <video controls preload="metadata" src={src} />
            ) : item.mimeType.startsWith('audio/') ? (
              <audio controls preload="metadata" src={src} />
            ) : (
              <a className="button secondary" href={src}>
                打开正文
              </a>
            )
          ) : (
            <p>媒体尚未确认或位置离线。</p>
          )}
        </section>
        <aside className="panel">
          <DefinitionList
            items={[
              ['稳定 ID', <code>{item.id}</code>],
              ['作品', <Link to={`/works/${encodeURIComponent(item.workId)}`}>{item.workId}</Link>],
              ['确认状态', item.contentVerificationState],
              ['Blob', item.blob ? `${item.blob.algorithm}:${item.blob.digest}` : '—'],
              ['字节', item.sizeBytes]
            ]}
          />
          <div className="button-row">
            {can('scan.run') && item.contentVerificationState === 'located_unverified' && (
              <Button className="button primary" onPress={() => verify.mutate()}>
                创建确认任务
              </Button>
            )}
            {can('media.derive') && item.contentVerificationState === 'content_verified' && (
              <Button className="button secondary" onPress={() => derive.mutate()}>
                生成缩略图
              </Button>
            )}
          </div>
          {(verify.isError || derive.isError) && (
            <p role="alert" className="field-error">
              {errorMessage(verify.error ?? derive.error)}
            </p>
          )}
        </aside>
      </div>
    </>
  );
}
