/*
 * 全屏查看。
 *
 * 它是一个**路由**而不是模态框：这样返回键就是关闭、地址可以直接分享、焦点管理由页面
 * 切换天然承担，不需要手写焦点收束。Esc 也只是"返回上一页"。
 *
 * 图片走 media.ts 的并发闸（eager 加载，因为它是用户此刻唯一在看的东西）；视频与音频交给
 * 原生元素直连正文地址，由浏览器发 Range 请求边下边播——把视频塞进 blob 加载器等于必须
 * 下完整个文件才能开始播放。
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { Badge, Button, ErrorState, IconButton, Spinner } from '../../design';
import { describeError, errorCode, errorCorrelationId } from '../../shared/errors';
import { useTheme } from '../../shared/theme';
import {
  isAudioMedia,
  isImageMedia,
  isVideoMedia,
  mediaContentUrl,
  publicationHref,
  type PublishedMedia
} from '../contracts';
import { useWorkMedia } from '../queries';
import { MediaImage } from '../components/media';

const MIN_SCALE = 1;
const MAX_SCALE = 8;
const DOUBLE_TAP_SCALE = 2.5;

interface Transform {
  scale: number;
  x: number;
  y: number;
}

const IDENTITY: Transform = { scale: 1, x: 0, y: 0 };

function clampScale(value: number): number {
  return Math.min(MAX_SCALE, Math.max(MIN_SCALE, value));
}

/**
 * 可缩放、可拖动、支持双指捏合的图片舞台。
 *
 * `touch-action: none`（见 app.css）把触摸手势的解释权交给我们自己，否则浏览器的默认
 * 平移/缩放会和这里的变换打架。缩放回到 1 时位移一并归零，避免图片"卡"在视口外。
 */
function ImageStage({ media, publicationId }: { media: PublishedMedia; publicationId: string }) {
  const [transform, setTransform] = useState<Transform>(IDENTITY);
  const [directManipulation, setDirectManipulation] = useState(false);
  const stageRef = useRef<HTMLDivElement>(null);
  const pointers = useRef(new Map<number, { x: number; y: number }>());
  const pinchDistance = useRef<number | null>(null);
  const { reducedMotion } = useTheme();

  useEffect(() => {
    setTransform(IDENTITY);
    setDirectManipulation(false);
  }, [media.id]);

  const zoomBy = useCallback((factor: number) => {
    setTransform((current) => {
      const scale = clampScale(current.scale * factor);
      if (scale === MIN_SCALE) return IDENTITY;
      return { ...current, scale };
    });
  }, []);

  useEffect(() => {
    const element = stageRef.current;
    if (!element) return;
    // React 的 wheel 监听是被动的，preventDefault 不会生效；缩放必须自己挂非被动监听，
    // 否则页面会跟着一起滚。
    const onWheel = (event: WheelEvent) => {
      if (!event.ctrlKey && Math.abs(event.deltaY) < 1) return;
      event.preventDefault();
      zoomBy(event.deltaY < 0 ? 1.15 : 1 / 1.15);
    };
    element.addEventListener('wheel', onWheel, { passive: false });
    return () => {
      element.removeEventListener('wheel', onWheel);
    };
  }, [zoomBy]);

  const distanceBetweenPointers = (): number | null => {
    const values = [...pointers.current.values()];
    const first = values[0];
    const second = values[1];
    if (!first || !second) return null;
    return Math.hypot(first.x - second.x, first.y - second.y);
  };

  return (
    <div
      ref={stageRef}
      className="gal-viewer__stage"
      onPointerDown={(event) => {
        setDirectManipulation(true);
        pointers.current.set(event.pointerId, { x: event.clientX, y: event.clientY });
        event.currentTarget.setPointerCapture(event.pointerId);
        pinchDistance.current = distanceBetweenPointers();
      }}
      onPointerMove={(event) => {
        const previous = pointers.current.get(event.pointerId);
        if (!previous) return;
        pointers.current.set(event.pointerId, { x: event.clientX, y: event.clientY });
        if (pointers.current.size >= 2) {
          const next = distanceBetweenPointers();
          const start = pinchDistance.current;
          if (next !== null && start !== null && start > 0) {
            pinchDistance.current = next;
            zoomBy(next / start);
          }
          return;
        }
        setTransform((current) => {
          if (current.scale <= MIN_SCALE) return current;
          return {
            ...current,
            x: current.x + (event.clientX - previous.x),
            y: current.y + (event.clientY - previous.y)
          };
        });
      }}
      onPointerUp={(event) => {
        pointers.current.delete(event.pointerId);
        pinchDistance.current = null;
        if (pointers.current.size === 0) setDirectManipulation(false);
      }}
      onPointerCancel={(event) => {
        pointers.current.delete(event.pointerId);
        pinchDistance.current = null;
        if (pointers.current.size === 0) setDirectManipulation(false);
      }}
      onDoubleClick={() => {
        setTransform((current) =>
          current.scale > MIN_SCALE ? IDENTITY : { scale: DOUBLE_TAP_SCALE, x: 0, y: 0 }
        );
      }}
    >
      <div
        className="gal-viewer__canvas"
        style={{
          transform: `translate(${transform.x}px, ${transform.y}px) scale(${transform.scale})`,
          transition:
            reducedMotion || directManipulation
              ? 'none'
              : `transform var(--motion-state) var(--ease-structure)`
        }}
      >
        <MediaImage
          eager
          src={mediaContentUrl(media.id, { queryPublicationId: publicationId })}
          alt={`第 ${media.ordinal + 1} 项媒体`}
        />
      </div>
      <div className="gal-viewer__zoom">
        <IconButton label="放大" variant="ghost" onPress={() => zoomBy(1.25)}>
          <span aria-hidden="true">＋</span>
        </IconButton>
        <IconButton label="缩小" variant="ghost" onPress={() => zoomBy(1 / 1.25)}>
          <span aria-hidden="true">－</span>
        </IconButton>
        <IconButton label="重置缩放" variant="ghost" onPress={() => setTransform(IDENTITY)}>
          <span aria-hidden="true">⤢</span>
        </IconButton>
        <span className="gal-viewer__scale" aria-live="polite">
          {Math.round(transform.scale * 100)}%
        </span>
      </div>
    </div>
  );
}

/** 视频与音频：直连正文地址，由浏览器负责 Range 与缓冲。 */
function PlayerStage({ media, publicationId }: { media: PublishedMedia; publicationId: string }) {
  const src = mediaContentUrl(media.id, { queryPublicationId: publicationId });
  if (isVideoMedia(media)) {
    return <video className="gal-viewer__player" src={src} controls preload="metadata" playsInline />;
  }
  return <audio className="gal-viewer__player" src={src} controls preload="metadata" />;
}

export function ViewerPage() {
  const params = useParams();
  const workId = params.workId ?? '';
  const mediaId = params.mediaId ?? '';
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const requestedPublicationId = searchParams.get('queryPublicationId') ?? undefined;
  const media = useWorkMedia(workId, requestedPublicationId);
  const loaded = media.data?.media;
  // useMemo 不是性能优化：go() 依赖 items，每次渲染换身份会让键盘监听不断重挂。
  const items = useMemo(() => loaded ?? [], [loaded]);
  const index = items.findIndex((item) => item.id === mediaId);
  const current = index < 0 ? undefined : items[index];
  const publicationId = media.data?.queryPublicationId ?? '';

  const go = useCallback(
    (offset: number) => {
      const target = items[index + offset];
      if (!target) return;
      // replace：翻阅媒体不应该在历史里堆出几十个条目，返回键要能一步回到作品页。
      void navigate(
        publicationHref(
          `/works/${encodeURIComponent(workId)}/view/${encodeURIComponent(target.id)}`,
          publicationId
        ),
        { replace: true }
      );
    },
    [items, index, navigate, workId, publicationId]
  );

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        void navigate(publicationHref(`/works/${encodeURIComponent(workId)}`, publicationId));
        return;
      }
      if (event.key === 'ArrowLeft') go(-1);
      if (event.key === 'ArrowRight') go(1);
    };
    window.addEventListener('keydown', onKeyDown);
    return () => {
      window.removeEventListener('keydown', onKeyDown);
    };
  }, [go, navigate, workId, publicationId]);

  if (media.isPending) {
    return (
      <div className="gal-viewer">
        <Spinner label="正在加载媒体" />
      </div>
    );
  }
  if (media.error !== null) {
    const expired = errorCode(media.error) === 'CURSOR_EXPIRED';
    return (
      <div className="gal-viewer">
        <ErrorState
          description={describeError(media.error)}
          code={errorCode(media.error)}
          correlationId={errorCorrelationId(media.error)}
          onRetry={() => void media.refetch()}
        />
        {expired ? (
          <p>
            <Link className="gal-link" to={`/works/${encodeURIComponent(workId)}`}>
              打开作品当前版本
            </Link>
            {' · '}
            <Link className="gal-link" to="/browse">
              返回全部作品
            </Link>
          </p>
        ) : null}
      </div>
    );
  }
  if (!current) {
    return (
      <div className="gal-viewer">
        <ErrorState
          title="找不到这项媒体"
          description="它可能已经不在当前快照里，或当前账户没有查看它的权限。"
          code="NOT_FOUND"
        />
        <Link
          className="gal-link"
          to={publicationHref(
            `/works/${encodeURIComponent(workId)}`,
            publicationId || requestedPublicationId
          )}
        >
          返回作品
        </Link>
      </div>
    );
  }

  const unverified = current.contentVerificationState !== 'content_verified';

  return (
    <div className="gal-viewer">
      <header className="gal-viewer__bar">
        <Link
          className="gal-link"
          to={publicationHref(`/works/${encodeURIComponent(workId)}`, publicationId)}
        >
          返回作品
        </Link>
        <span className="gal-viewer__counter" aria-live="polite">
          第 {index + 1} / {items.length} 项 · {current.mimeType}
        </span>
        <span className="gal-viewer__nav">
          <Button variant="ghost" isDisabled={index <= 0} onPress={() => go(-1)}>
            上一项
          </Button>
          <Button variant="ghost" isDisabled={index >= items.length - 1} onPress={() => go(1)}>
            下一项
          </Button>
        </span>
      </header>

      {current.available ? null : (
        <p className="gal-connection gal-connection--warn" role="status">
          这项媒体所在的位置当前离线，正文暂时读不到。
        </p>
      )}

      {unverified ? (
        <p className="gal-connection gal-connection--warn" role="status">
          <Badge tone="warning">内容未确认</Badge> 尚未完成内容确认的媒体不提供正文读取，
          可以在作品页创建按需确认任务。
        </p>
      ) : isImageMedia(current) ? (
        <ImageStage media={current} publicationId={publicationId} />
      ) : isVideoMedia(current) || isAudioMedia(current) ? (
        <PlayerStage media={current} publicationId={publicationId} />
      ) : (
        <div className="gal-viewer__fallback">
          <p>这种类型不能在浏览器里直接呈现。</p>
          <a
            className="gal-link"
            href={mediaContentUrl(current.id, { queryPublicationId: publicationId, download: true })}
            download
          >
            下载原始文件
          </a>
        </div>
      )}

      <p className="gal-viewer__hint gal-muted">
        键盘：← / → 切换媒体，Esc 返回。图片支持滚轮缩放、拖动平移、双指捏合与双击放大。
      </p>
    </div>
  );
}
