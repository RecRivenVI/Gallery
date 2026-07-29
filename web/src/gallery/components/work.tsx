/*
 * 作品卡片与作品网格。
 *
 * 两条与契约直接相关的显示规则：
 *
 * 1. **收藏是 live 字段**。列表快照里的 `favorite` 只解释“本次结果为什么这样过滤”，
 *    真值在 `GET /works/{id}/overlay`。所以卡片优先显示已缓存的 live 值；用户在卡片上
 *    切换收藏后，写入结果直接进 overlay 缓存，卡片立刻反映服务端返回的值，
 *    不会因为列表快照没变而“弹回”旧状态。
 * 2. **没有创作者、没有发布时间都是真实事实**，必须有得体的空态，
 *    不能出现 `undefined` 或 `Invalid Date`。
 */

import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { Badge, IconButton, useKeyedLayoutMotion, useToast } from '../../design';
import { describeError } from '../../shared/errors';
import { useCapability } from '../../shared/session';
import { useTheme } from '../../shared/theme';
import {
  formatCreator,
  formatPublishedAt,
  isCreatorMissing,
  mediaContentUrl,
  MISSING_PUBLISHED_AT_TEXT,
  publicationHref,
  type PublishedWork
} from '../contracts';
import { useOverlayMutation, useWorkOverlay } from '../queries';
import { CoverMissing, MediaImage } from './media';
import { RuleBadges } from './badges';

/* ————————————————————————————— 搜索命中 ————————————————————————————— */

export interface MatchedTextProps {
  value: string;
  /** 命中区间，**code point 偏移**，左闭右开。不是 UTF-16 code unit，也不是字节。 */
  spans?: readonly { start: number; end: number }[];
}

/**
 * 按服务端给出的命中区间高亮。
 *
 * 偏移是 code point 级的，因此必须用 `Array.from` 把字符串拆成 code point 再切；
 * 直接 `slice` 会在 emoji 或增补平面汉字上切错位置。
 */
export function MatchedText({ value, spans }: MatchedTextProps) {
  if (!spans || spans.length === 0) return <>{value}</>;
  const points = Array.from(value);
  const ordered = [...spans].sort((left, right) => left.start - right.start);
  const nodes: ReactNode[] = [];
  let cursor = 0;
  ordered.forEach((span, index) => {
    const start = Math.max(cursor, Math.min(span.start, points.length));
    const end = Math.max(start, Math.min(span.end, points.length));
    if (start > cursor) nodes.push(points.slice(cursor, start).join(''));
    if (end > start) {
      nodes.push(
        <mark key={`${span.start}-${span.end}-${index}`} className="gal-mark">
          {points.slice(start, end).join('')}
        </mark>
      );
    }
    cursor = end;
  });
  if (cursor < points.length) nodes.push(points.slice(cursor).join(''));
  return <>{nodes}</>;
}

/* ————————————————————————————— 收藏 ————————————————————————————— */

export interface FavoriteToggleProps {
  workId: string;
  /** 列表快照里的值。仅在还没有 live 值时使用。 */
  snapshotFavorite: boolean;
  compact?: boolean;
}

/**
 * 收藏开关。
 *
 * 写入走整体 PUT（先 GET 再合并），成功后以服务端响应为准更新 overlay 缓存——契约没有
 * If-Match，本地值不能被当成最终结果。
 */
export function FavoriteToggle({ workId, snapshotFavorite, compact }: FavoriteToggleProps) {
  // enabled=false：不为每张卡片各发一次 overlay 请求，但仍订阅缓存，
  // 于是“刚刚切换过收藏”的作品会立即显示 live 值。
  const overlay = useWorkOverlay(workId, false);
  const mutation = useOverlayMutation(workId);
  const toast = useToast();
  const favorite = overlay.data?.favorite ?? snapshotFavorite;

  return (
    <IconButton
      label={favorite ? '取消收藏' : '收藏'}
      variant={compact === true ? 'ghost' : 'secondary'}
      isPending={mutation.isPending}
      onPress={() => {
        mutation.mutate(
          { favorite: !favorite },
          {
            onError: (error: unknown) => {
              toast.show({ title: '收藏未能保存', description: describeError(error), tone: 'danger' });
            }
          }
        );
      }}
    >
      <span aria-hidden="true">{favorite ? '★' : '☆'}</span>
    </IconButton>
  );
}

/* ————————————————————————————— 卡片 ————————————————————————————— */

export interface WorkCardProps {
  work: PublishedWork;
  /** 来源声明的显示时区。跨来源浏览时没有唯一答案，此时留空用本地时区。 */
  timeZone?: string;
  /** 来源声明的“作者”称谓，例如画师、用户。缺省是“创作者”。 */
  authorLabel?: string;
}

export function WorkCard({ work, timeZone, authorLabel = '创作者' }: WorkCardProps) {
  const canEditOverlay = useCapability('overlays.write');
  const published = formatPublishedAt(work.publishedAt, timeZone);
  const titleMatch = work.matches?.find((match) => match.field === 'title');

  return (
    <article className="gal-card">
      <Link
        className="gal-card__link"
        to={publicationHref(`/works/${encodeURIComponent(work.id)}`, work.queryPublicationId)}
      >
        <span className="gal-card__cover">
          {work.coverMediaId === null ? (
            <CoverMissing />
          ) : (
            <MediaImage
              src={mediaContentUrl(work.coverMediaId, { queryPublicationId: work.queryPublicationId })}
              alt=""
              allowRetry={false}
            />
          )}
          <RuleBadges badges={work.badges} position="cover_top_left" className="gal-badges--tl" />
          <RuleBadges badges={work.badges} position="cover_top_right" className="gal-badges--tr" />
        </span>
        <span className="gal-card__title">
          <MatchedText value={work.title} spans={titleMatch?.spans} />
        </span>
        <span className={isCreatorMissing(work.creator) ? 'gal-card__meta gal-muted' : 'gal-card__meta'}>
          {isCreatorMissing(work.creator) ? formatCreator(work.creator) : `${authorLabel}：${work.creator}`}
        </span>
        <span className="gal-card__meta gal-muted">
          {work.mediaCount} 项媒体 · {published ?? MISSING_PUBLISHED_AT_TEXT}
        </span>
      </Link>
      <div className="gal-card__foot">
        <RuleBadges badges={work.badges} position="tag_leading" />
        {work.tags.slice(0, 3).map((tag) => (
          <Badge key={tag} tone="neutral">
            {tag}
          </Badge>
        ))}
        {canEditOverlay ? (
          <span className="gal-card__actions">
            <FavoriteToggle workId={work.id} snapshotFavorite={work.favorite} compact />
          </span>
        ) : null}
      </div>
    </article>
  );
}

/* ————————————————————————————— 网格 ————————————————————————————— */

export interface WorkGridProps {
  works: readonly PublishedWork[];
  timeZone?: string;
  authorLabel?: string;
  /** 旧快照只用于视觉交接，必须退出点击、焦点和无障碍树。 */
  isVisualPlaceholder?: boolean;
}

export function WorkGrid({ works, timeZone, authorLabel, isVisualPlaceholder = false }: WorkGridProps) {
  const { reducedMotion } = useTheme();
  const motion = useKeyedLayoutMotion(
    works.map((work) => work.id),
    { reducedMotion }
  );

  return (
    <div
      className="gal-grid"
      role="list"
      inert={isVisualPlaceholder}
      aria-hidden={isVisualPlaceholder ? true : undefined}
      data-replacing={isVisualPlaceholder ? true : undefined}
    >
      {works.map((work) => (
        <div key={work.id} ref={motion.itemRef(work.id)} role="listitem" className="gal-grid__cell">
          <WorkCard work={work} timeZone={timeZone} authorLabel={authorLabel} />
        </div>
      ))}
    </div>
  );
}
