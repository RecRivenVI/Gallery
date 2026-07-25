import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Button,
  Checkbox,
  Form,
  Input,
  Label,
  Radio,
  RadioGroup,
  TextArea,
  TextField
} from 'react-aria-components';
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
import { WorkCover } from '../components/work-cover';

export function WorkPage() {
  const { workId = '' } = useParams();
  const [search] = useSearchParams();
  const publication = search.get('publication') ?? undefined;
  const client = useQueryClient();
  const { bootstrap, can } = useSession();
  const canReadMedia = can('media.read');
  const work = useQuery({
    queryKey: ['work', workId, publication],
    queryFn: async ({ signal }) =>
      expectData(
        await api.GET('/api/v1/works/{workId}', {
          signal,
          params: { path: { workId }, query: { queryPublicationId: publication } }
        })
      )
  });
  const overlay = useQuery({
    queryKey: ['overlay', workId],
    queryFn: async ({ signal }) =>
      expectData(await api.GET('/api/v1/works/{workId}/overlay', { signal, params: { path: { workId } } }))
  });
  const mediaPublication = publication ?? work.data?.queryPublicationId;
  const media = useQuery({
    queryKey: ['media', workId, mediaPublication],
    enabled: canReadMedia && mediaPublication !== undefined,
    queryFn: async ({ signal }) => {
      if (!mediaPublication) throw new Error('media publication is not resolved');
      return expectData(
        await api.GET('/api/v1/works/{workId}/media', {
          signal,
          params: { path: { workId }, query: { queryPublicationId: mediaPublication } }
        })
      );
    }
  });
  if (work.isPending || overlay.isPending || (canReadMedia && media.isPending)) return <LoadingState />;
  if (work.isError) return <ErrorState error={work.error} onRetry={() => void work.refetch()} />;
  if (overlay.isError) return <ErrorState error={overlay.error} onRetry={() => void overlay.refetch()} />;
  if (canReadMedia && media.isError)
    return <ErrorState error={media.error} onRetry={() => void media.refetch()} />;
  const mediaItems = media.data?.media ?? [];
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
          <WorkCover
            title={work.data.title}
            coverMediaId={work.data.coverMediaId}
            queryPublicationId={work.data.queryPublicationId}
            canReadMedia={canReadMedia}
            alt={`《${work.data.title}》的封面`}
            className="work-detail-cover"
          />
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
        {can('overlays.write') && (
          <OverlayEditor
            initial={overlay.data}
            media={mediaItems}
            canReadMedia={canReadMedia}
            publishedCoverMediaId={work.data.coverMediaId}
            csrf={bootstrap.csrfToken}
            onSaved={async () => {
              await client.invalidateQueries({ queryKey: ['overlay', workId] });
            }}
          />
        )}
      </div>
      <section>
        <h2>媒体</h2>
        {!canReadMedia ? (
          <EmptyState title="无法读取媒体" detail="当前账户没有 media.read capability。" />
        ) : mediaItems.length === 0 ? (
          <EmptyState title="没有媒体" detail="当前 publication 不含该作品的媒体。" />
        ) : (
          <div className="media-grid">
            {mediaItems.map((item) => (
              <article className="media-card" key={item.id}>
                {item.available && item.contentVerificationState === 'content_verified' ? (
                  item.mimeType.startsWith('image/') ? (
                    <img
                      loading="lazy"
                      src={`/api/v1/media/${encodeURIComponent(item.id)}/content?queryPublicationId=${encodeURIComponent(item.queryPublicationId)}`}
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
                {item.id === work.data.coverMediaId && <StatusBadge tone="info">有效封面</StatusBadge>}
                <p>
                  {item.mimeType} · {formatBytes(item.sizeBytes)}
                </p>
                <Link
                  to={`/media/${encodeURIComponent(item.id)}?publication=${encodeURIComponent(item.queryPublicationId)}`}
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
type PublishedMedia = components['schemas']['PublishedMedia'];
const RULE_COVER = '__rule_cover__';

export function OverlayEditor({
  initial,
  media,
  canReadMedia,
  publishedCoverMediaId,
  csrf,
  onSaved
}: {
  initial: Overlay;
  media: PublishedMedia[];
  canReadMedia: boolean;
  publishedCoverMediaId: string | null;
  csrf: string;
  onSaved: () => Promise<void>;
}) {
  const [title, setTitle] = useState(initial.titleOverride);
  const [tags, setTags] = useState(initial.manualTags.join('\n'));
  const [favorite, setFavorite] = useState(initial.favorite);
  const [hidden, setHidden] = useState(initial.hidden);
  const [progress, setProgress] = useState(String(initial.progress));
  const [customCoverMediaId, setCustomCoverMediaId] = useState(initial.customCoverMediaId);
  useEffect(() => {
    setTitle(initial.titleOverride);
    setTags(initial.manualTags.join('\n'));
    setFavorite(initial.favorite);
    setHidden(initial.hidden);
    setProgress(String(initial.progress));
    setCustomCoverMediaId(initial.customCoverMediaId);
  }, [initial]);
  const currentMediaIds = new Set(media.map((item) => item.id));
  const customCoverIsMissing =
    canReadMedia && customCoverMediaId !== undefined && !currentMediaIds.has(customCoverMediaId);
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
            customCoverMediaId
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
      <RadioGroup
        className="cover-options"
        value={customCoverMediaId ?? RULE_COVER}
        onChange={(value) => setCustomCoverMediaId(value === RULE_COVER ? undefined : value)}
      >
        <Label>自定义封面</Label>
        <Radio value={RULE_COVER} className="cover-option">
          使用规则封面（清除 CustomCover）
        </Radio>
        {!canReadMedia && customCoverMediaId && (
          <Radio value={customCoverMediaId} className="cover-option">
            保留当前自定义封面（无 media.read，无法检查媒体）
          </Radio>
        )}
        {customCoverIsMissing && customCoverMediaId && (
          <Radio value={customCoverMediaId} className="cover-option invalid-cover-option">
            已失效的自定义封面（不在当前 publication）
          </Radio>
        )}
        {canReadMedia &&
          media.map((item) => (
            <Radio value={item.id} className="cover-option" key={item.id}>
              媒体 #{item.ordinal} · {item.kind} · {item.mimeType}
              {item.id === publishedCoverMediaId ? '（当前 publication 的有效封面）' : ''}
            </Radio>
          ))}
      </RadioGroup>
      {customCoverIsMissing && (
        <div className="callout warning" role="status">
          <p>当前 CustomCover 已不在此 publication 的媒体中；可清除它或改选当前媒体。</p>
          <Button className="button secondary" onPress={() => setCustomCoverMediaId(undefined)}>
            清除失效选择并使用规则封面
          </Button>
        </div>
      )}
      <p className="field-help">
        这里编辑 live Overlay；页面封面与“有效封面”标记继续使用当前 publication 的冻结值，直到新 publication
        发布。
      </p>
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
