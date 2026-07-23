import { useQuery } from '@tanstack/react-query';
import { useParams } from 'react-router-dom';
import type { components } from '../api/schema.gen';
import { EmptyState, ErrorState, LoadingState, PageHeader, StatusBadge } from '../components/ui';

export function PublicSharePage() {
  const { credential = '' } = useParams();
  const share = useQuery({
    queryKey: ['public-share', credential],
    queryFn: async ({ signal }) => {
      const response = await fetch(`/api/v1/public/shares/${encodeURIComponent(credential)}`, {
        credentials: 'omit',
        signal,
        headers: { Accept: 'application/json' }
      });
      if (!response.ok) throw new Error(`SHARE_${response.status}`);
      return (await response.json()) as components['schemas']['PublicShareResource'];
    }
  });
  if (share.isPending) return <LoadingState label="正在验证分享…" />;
  if (share.isError) return <ErrorState error={share.error} />;
  const data = share.data;
  const items = data.mediaItems ?? (data.media ? [data.media] : []);
  return (
    <main className="public-share">
      <PageHeader
        title={data.work?.title ?? data.library?.name ?? 'Gallery 分享'}
        description={`有效期至 ${new Date(data.expiresAt).toLocaleString('zh-CN')}`}
        actions={<StatusBadge tone="success">{data.fixed ? '固定内容' : '跟随当前 publication'}</StatusBadge>}
      />
      {data.work && (
        <section className="panel">
          <h2>{data.work.title}</h2>
          <p>{data.work.creator}</p>
        </section>
      )}
      {items.length === 0 ? (
        <EmptyState title="没有可显示的媒体" detail="分享有效，但当前范围中没有媒体正文。" />
      ) : (
        <div className="media-grid">
          {items.map((media) => {
            const src = `/api/v1/public/shares/${encodeURIComponent(credential)}/media/${encodeURIComponent(media.id)}/content`;
            return (
              <article className="media-card" key={media.id}>
                {media.mimeType.startsWith('image/') ? (
                  <img src={src} alt="分享媒体" referrerPolicy="no-referrer" />
                ) : media.mimeType.startsWith('video/') ? (
                  <video controls preload="metadata" src={src} />
                ) : (
                  <a className="button secondary" href={src}>
                    打开媒体
                  </a>
                )}
                <p>{media.mimeType}</p>
                {data.permissions.includes('download') && <a href={`${src}?download=true`}>下载</a>}
              </article>
            );
          })}
        </div>
      )}
      <footer>
        <p>由 Gallery · 画廊安全分享。此页面不会使用登录 Cookie。</p>
      </footer>
    </main>
  );
}
