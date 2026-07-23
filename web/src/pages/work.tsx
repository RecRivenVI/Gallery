import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Checkbox, Form, Input, Label, TextArea, TextField } from 'react-aria-components';
import { useEffect, useState } from 'react';
import { Link, useParams, useSearchParams } from 'react-router-dom';
import { api, csrfHeaders, errorMessage, expectData } from '../api/client';
import type { components } from '../api/schema.gen';
import {
  DefinitionList,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  StatusBadge
} from '../components/ui';
import { useSession } from '../auth/session';

export function WorkPage() {
  const { workId = '' } = useParams();
  const [search] = useSearchParams();
  const publication = search.get('publication') ?? undefined;
  const client = useQueryClient();
  const { bootstrap, can } = useSession();
  const work = useQuery({
    queryKey: ['work', workId],
    queryFn: async ({ signal }) =>
      expectData(await api.GET('/api/v1/works/{workId}', { signal, params: { path: { workId } } }))
  });
  const overlay = useQuery({
    queryKey: ['overlay', workId],
    queryFn: async ({ signal }) =>
      expectData(await api.GET('/api/v1/works/{workId}/overlay', { signal, params: { path: { workId } } }))
  });
  const media = useQuery({
    queryKey: ['media', workId, publication],
    queryFn: async ({ signal }) =>
      expectData(
        await api.GET('/api/v1/works/{workId}/media', {
          signal,
          params: { path: { workId }, query: { queryPublicationId: publication } }
        })
      )
  });
  if (work.isPending || overlay.isPending || media.isPending) return <LoadingState />;
  if (work.isError) return <ErrorState error={work.error} onRetry={() => void work.refetch()} />;
  if (overlay.isError) return <ErrorState error={overlay.error} onRetry={() => void overlay.refetch()} />;
  if (media.isError) return <ErrorState error={media.error} onRetry={() => void media.refetch()} />;
  return (
    <>
      <PageHeader
        title={overlay.data.titleOverride || work.data.title}
        description={`${work.data.creator || '未知创作者'} · ${work.data.mediaCount} 个媒体`}
        actions={
          <StatusBadge tone={overlay.data.projectionStatus === 'published' ? 'success' : 'warning'}>
            投影：{overlay.data.projectionStatus}
          </StatusBadge>
        }
      />
      <div className="detail-layout">
        <section className="panel">
          <h2>作品事实</h2>
          <DefinitionList
            items={[
              ['稳定 ID', <code>{work.data.id}</code>],
              ['publication', <code>{work.data.queryPublicationId}</code>],
              ['标签', work.data.tags.join('、') || '—'],
              ['收藏', overlay.data.favorite ? '是' : '否'],
              ['进度', `${Math.round(overlay.data.progress * 100)}%`]
            ]}
          />
        </section>
        {can('overlay.write') && (
          <OverlayEditor
            initial={overlay.data}
            csrf={bootstrap.csrfToken}
            onSaved={async () => {
              await client.invalidateQueries({ queryKey: ['overlay', workId] });
            }}
          />
        )}
      </div>
      <section>
        <h2>媒体</h2>
        {media.data.media.length === 0 ? (
          <EmptyState title="没有媒体" detail="当前 publication 不含该作品的媒体。" />
        ) : (
          <div className="media-grid">
            {media.data.media.map((item) => (
              <article className="media-card" key={item.id}>
                {item.available && item.contentVerificationState === 'content_verified' ? (
                  item.mimeType.startsWith('image/') ? (
                    <img
                      loading="lazy"
                      src={`/api/v1/media/${encodeURIComponent(item.id)}/content?queryPublicationId=${encodeURIComponent(media.data.queryPublicationId)}`}
                      alt=""
                    />
                  ) : (
                    <div className="media-placeholder">{item.kind}</div>
                  )
                ) : (
                  <div className="media-placeholder">离线或未确认</div>
                )}
                <h3>
                  #{item.ordinal} · {item.kind}
                </h3>
                <p>
                  {item.mimeType} · {formatBytes(item.sizeBytes)}
                </p>
                <Link
                  to={`/media/${encodeURIComponent(item.id)}?publication=${encodeURIComponent(media.data.queryPublicationId)}`}
                >
                  查看媒体
                </Link>
              </article>
            ))}
          </div>
        )}
      </section>
    </>
  );
}

type Overlay = components['schemas']['WorkOverlayState'];
function OverlayEditor({
  initial,
  csrf,
  onSaved
}: {
  initial: Overlay;
  csrf: string;
  onSaved: () => Promise<void>;
}) {
  const [title, setTitle] = useState(initial.titleOverride);
  const [tags, setTags] = useState(initial.manualTags.join('\n'));
  const [favorite, setFavorite] = useState(initial.favorite);
  const [hidden, setHidden] = useState(initial.hidden);
  const [progress, setProgress] = useState(String(initial.progress));
  useEffect(() => {
    setTitle(initial.titleOverride);
    setTags(initial.manualTags.join('\n'));
    setFavorite(initial.favorite);
    setHidden(initial.hidden);
    setProgress(String(initial.progress));
  }, [initial]);
  const mutation = useMutation({
    mutationFn: async () =>
      expectData(
        await api.PUT('/api/v1/works/{workId}/overlay', {
          params: { path: { workId: initial.workId }, header: csrfHeaders(csrf) },
          body: {
            titleOverride: title,
            manualTags: tags
              .split('\n')
              .map((tag) => tag.trim())
              .filter(Boolean),
            favorite,
            hidden,
            progress: Math.max(0, Math.min(1, Number(progress) || 0)),
            customCoverMediaId: initial.customCoverMediaId
          }
        })
      ),
    onSuccess: onSaved
  });
  return (
    <Form
      className="panel form-stack"
      onSubmit={(event) => {
        event.preventDefault();
        mutation.mutate();
      }}
    >
      <h2>用户 Overlay</h2>
      <TextField value={title} onChange={setTitle}>
        <Label>标题覆盖</Label>
        <Input />
      </TextField>
      <TextField value={tags} onChange={setTags}>
        <Label>手工标签（每行一个）</Label>
        <TextArea />
      </TextField>
      <TextField value={progress} onChange={setProgress}>
        <Label>阅读进度（0–1）</Label>
        <Input type="number" min="0" max="1" step="0.01" />
      </TextField>
      <Checkbox isSelected={favorite} onChange={setFavorite}>
        {' '}
        收藏
      </Checkbox>
      <Checkbox isSelected={hidden} onChange={setHidden}>
        {' '}
        隐藏
      </Checkbox>
      <Button type="submit" className="button primary" isPending={mutation.isPending}>
        保存事实
      </Button>
      {mutation.isError && (
        <p role="alert" className="field-error">
          {errorMessage(mutation.error)}
        </p>
      )}
    </Form>
  );
}

function formatBytes(value: number) {
  return new Intl.NumberFormat('zh-CN', { style: 'unit', unit: 'megabyte', maximumFractionDigits: 1 }).format(
    value / 1_000_000
  );
}
