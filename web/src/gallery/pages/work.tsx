/*
 * 作品详情。
 *
 * 这里同时呈现三类事实，必须让用户分得清：
 *
 * - **规则派生**：标题、创作者、标签、角标、发布时间、描述、来源链接。重扫会重算。
 * - **用户事实**：收藏、进度、标题覆盖、手动标签、隐藏、自定义封面。重扫**不会**覆盖。
 * - **快照身份**：这一页的媒体与正文来自哪个 publication。
 *
 * 媒体列表用 current 模式读取，响应会告诉我们实际使用的快照；随后所有正文读取都绑定它，
 * 保证同一次浏览里列表与字节来自同一代次。
 */

import { useRef } from 'react';
import { Link, useParams } from 'react-router-dom';
import { Badge, Button, EmptyState, ErrorState, Spinner, useToast } from '../../design';
import { describeError, errorCode, errorCorrelationId } from '../../shared/errors';
import { useCapability, useSession } from '../../shared/session';
import {
  formatCreator,
  formatProgress,
  formatPublishedAt,
  isCreatorMissing,
  isImageMedia,
  isVideoMedia,
  mediaContentUrl,
  MISSING_PUBLISHED_AT_TEXT,
  type PublishedMedia
} from '../contracts';
import { useVerificationJob, useWork, useWorkMedia, useWorkOverlay } from '../queries';
import { RuleBadges } from '../components/badges';
import { MediaImage, useInView, useThumbnailSource } from '../components/media';
import { OverlayEditor } from '../components/overlay-editor';
import { FavoriteToggle } from '../components/work';

/* ————————————————————————————— 媒体缩略 ————————————————————————————— */

interface MediaThumbProps {
  workId: string;
  media: PublishedMedia;
  queryPublicationId: string;
}

function MediaThumb({ workId, media, queryPublicationId }: MediaThumbProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const inView = useInView(containerRef, true);
  const { csrfToken } = useSession();
  const canDerive = useCapability('media.derive');
  const canVerify = useCapability('scan.run');
  const verification = useVerificationJob(media.id);
  const toast = useToast();
  // 只有这里才有 mimeType 与内容确认状态，也只有这里才有资格判断"缩略图是否可能存在"。
  const src = useThumbnailSource({
    mediaId: media.id,
    media,
    queryPublicationId,
    csrfToken,
    canDerive,
    inView
  });
  const unverified = media.contentVerificationState !== 'content_verified';

  return (
    <div className="gal-thumb" ref={containerRef}>
      <Link
        className="gal-thumb__link"
        to={`/works/${encodeURIComponent(workId)}/view/${encodeURIComponent(media.id)}`}
      >
        {isImageMedia(media) && !unverified ? (
          <MediaImage src={src} alt={`第 ${media.ordinal + 1} 项媒体`} allowRetry={false} />
        ) : (
          <span className="gal-thumb__kind" aria-hidden="true">
            {isVideoMedia(media) ? '▶' : '⧉'}
          </span>
        )}
        <span className="gal-thumb__label">
          第 {media.ordinal + 1} 项 · {media.mimeType}
        </span>
      </Link>
      {media.available ? null : <Badge tone="warning">位置离线</Badge>}
      {unverified ? (
        <div className="gal-thumb__notice">
          <Badge tone="warning">内容未确认</Badge>
          {canVerify ? (
            <Button
              variant="ghost"
              isPending={verification.isPending}
              onPress={() => {
                verification.mutate(undefined, {
                  onSuccess: () => {
                    toast.show({ title: '已排队内容确认任务', tone: 'success' });
                  },
                  onError: (error: unknown) => {
                    toast.show({
                      title: '无法创建确认任务',
                      description: describeError(error),
                      tone: 'danger'
                    });
                  }
                });
              }}
            >
              确认内容
            </Button>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

/* ————————————————————————————— 详情页 ————————————————————————————— */

export function WorkPage() {
  const params = useParams();
  const workId = params.workId ?? '';
  const work = useWork(workId);
  const media = useWorkMedia(workId);
  const overlay = useWorkOverlay(workId, false);
  const canEditOverlay = useCapability('overlays.write');

  if (work.isPending) {
    return (
      <div className="gal-page">
        <Spinner label="正在加载作品" />
      </div>
    );
  }
  if (work.error !== null) {
    return (
      <div className="gal-page">
        <ErrorState
          title="无法打开这个作品"
          description={describeError(work.error)}
          code={errorCode(work.error)}
          correlationId={errorCorrelationId(work.error)}
          onRetry={() => void work.refetch()}
        />
      </div>
    );
  }

  const item = work.data;
  const published = formatPublishedAt(item.publishedAt);
  const items = media.data?.media ?? [];
  const first = items[0];
  const publicationId = media.data?.queryPublicationId ?? item.queryPublicationId;
  const titleOverride = overlay.data?.titleOverride ?? '';

  return (
    <div className="gal-page gal-work">
      <header className="gal-work__header">
        <h1 className="gal-page__title">{titleOverride === '' ? item.title : titleOverride}</h1>
        {titleOverride === '' ? null : <p className="gal-muted">规则解析的标题：{item.title}</p>}
        <p className={isCreatorMissing(item.creator) ? 'gal-muted' : undefined}>
          {isCreatorMissing(item.creator) ? formatCreator(item.creator) : item.creator}
        </p>
        <p className="gal-work__facts">
          <span>{item.mediaCount} 项媒体</span>
          <span aria-hidden="true">·</span>
          <span className={published === undefined ? 'gal-muted' : undefined}>
            {published ?? MISSING_PUBLISHED_AT_TEXT}
          </span>
          <span aria-hidden="true">·</span>
          <span>进度 {formatProgress(overlay.data?.progress ?? item.progress)}</span>
        </p>
        <div className="gal-work__badges">
          <RuleBadges badges={item.badges} position="cover_top_left" />
          <RuleBadges badges={item.badges} position="cover_top_right" />
          <RuleBadges badges={item.badges} position="tag_leading" />
        </div>
        <div className="gal-work__tags">
          {item.tags.map((tag) => (
            <Link key={tag} className="gal-tag" to={`/browse?tag=${encodeURIComponent(tag)}`}>
              {tag}
            </Link>
          ))}
        </div>
        {canEditOverlay ? (
          <div className="gal-work__actions">
            <FavoriteToggle workId={workId} snapshotFavorite={item.favorite} />
          </div>
        ) : null}
        {item.description === undefined || item.description === '' ? null : (
          <p className="gal-work__description">{item.description}</p>
        )}
        {item.sourceUrl === undefined || item.sourceUrl === '' ? null : (
          <p>
            <a className="gal-link" href={item.sourceUrl} rel="noreferrer noopener" target="_blank">
              查看来源页面
            </a>
          </p>
        )}
      </header>

      <section className="gal-section">
        <h2 className="gal-section__title">媒体</h2>
        {media.isPending ? (
          <Spinner label="正在加载媒体" />
        ) : media.error !== null ? (
          <ErrorState
            description={describeError(media.error)}
            code={errorCode(media.error)}
            correlationId={errorCorrelationId(media.error)}
            onRetry={() => void media.refetch()}
          />
        ) : items.length === 0 ? (
          <EmptyState title="这个作品没有可显示的媒体" description="规则没有为它解析出任何媒体。" />
        ) : (
          <div className="gal-thumbs">
            {items.map((entry) => (
              <MediaThumb key={entry.id} workId={workId} media={entry} queryPublicationId={publicationId} />
            ))}
          </div>
        )}
      </section>

      {canEditOverlay ? <OverlayEditor workId={workId} media={items} /> : null}

      <p className="gal-snapshot">
        本页内容来自快照 <code>{publicationId}</code>
        {first === undefined ? null : (
          <>
            {' · '}
            <a className="gal-link" href={mediaContentUrl(first.id, { download: true })} download>
              下载第一项媒体
            </a>
          </>
        )}
      </p>
    </div>
  );
}
