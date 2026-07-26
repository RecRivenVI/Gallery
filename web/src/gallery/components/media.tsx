/*
 * 媒体呈现组件。
 *
 * 这里承担画廊最难的一件事：**在服务端 16 名额并发闸下把一屏大图显示出来，而且失败时
 * 不是碎图**。做法分三层：
 *
 * 1. 只有进入视口（含 600px 预取边距）的图片才开始加载，滚出视口即释放引用；
 * 2. 真正的网络请求全部经过 `media.ts` 的信号量，整个客户端同时最多 4 个在飞；
 * 3. `MEDIA_READ_BUSY` 在加载器内部退避重试，用尽后渲染成一块**可重试的占位**，
 *    并写明这是服务端的保护上限而不是故障。
 *
 * 视频与音频**不**走这里：它们必须交给原生元素，由浏览器发 Range 请求边下边播；
 * 塞进 blob 加载器等于把整个文件下完才开始播放。
 */

import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react';
import type { ReactNode, RefObject } from 'react';
import { Button, Spinner, VisuallyHidden } from '../../design';
import { describeError, errorCode, isRetryable } from '../../shared/errors';
import { isAbortError, mediaLoader, type MediaLoader } from '../media';
import { thumbnailScheduler, canRequestThumbnail, type ThumbnailCandidate } from '../thumbnails';
import { derivedAssetUrl, mediaContentUrl } from '../contracts';

/* ————————————————————————————— 加载器注入 ————————————————————————————— */

const MediaLoaderContext = createContext<MediaLoader>(mediaLoader);

/** 注入自定义加载器。生产不需要；测试用它断言并发自律与降级表现。 */
export function MediaLoaderProvider({ loader, children }: { loader: MediaLoader; children: ReactNode }) {
  return <MediaLoaderContext.Provider value={loader}>{children}</MediaLoaderContext.Provider>;
}

export function useMediaLoader(): MediaLoader {
  return useContext(MediaLoaderContext);
}

/* ————————————————————————————— 视口探测 ————————————————————————————— */

/** 预取边距：滚动方向上提前一屏左右开始加载，避免用户看到大片占位。 */
const VIEWPORT_MARGIN = '600px';

/**
 * 元素是否在视口内。
 *
 * 环境不支持 IntersectionObserver（例如 jsdom）时一律返回 true：此时并发自律完全由
 * 加载器的信号量承担，功能降级但不出错。
 */
export function useInView(ref: RefObject<Element | null>, enabled: boolean): boolean {
  const observerCtor: unknown = (globalThis as { IntersectionObserver?: unknown }).IntersectionObserver;
  const supported = typeof observerCtor === 'function';
  const [inView, setInView] = useState(!enabled || !supported);

  useEffect(() => {
    if (!enabled || !supported) return;
    const element = ref.current;
    if (!element) return;
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) setInView(entry.isIntersecting);
      },
      { rootMargin: VIEWPORT_MARGIN }
    );
    observer.observe(element);
    return () => {
      observer.disconnect();
    };
  }, [ref, enabled, supported]);

  return inView;
}

/* ————————————————————————————— 图片 ————————————————————————————— */

type ImageState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'ready'; objectUrl: string }
  | { kind: 'error'; error: unknown };

export interface MediaImageProps {
  /** 完整的媒体或派生资源正文地址。 */
  src: string;
  /** 替代文本。空串表示纯装饰（相邻已有可读标题时用它，避免屏幕阅读器重复朗读）。 */
  alt: string;
  /** 关闭视口懒加载。全屏查看这类“必须立刻出现”的场景用它。 */
  eager?: boolean;
  /**
   * 失败时是否渲染“重试”按钮。
   *
   * 卡片封面处于 `<a>` 内部，按钮嵌在链接里既不是合法 HTML，也会被辅助技术判成
   * 嵌套可交互元素。那些位置传 false：仍然显示明确的降级文字（不是碎图），
   * 重试交给重新进入视口或打开详情页。
   */
  allowRetry?: boolean;
  className?: string;
}

/**
 * 一张受并发管束的图片。
 *
 * 刻意不用 `<img src>` 直连：那样浏览器会一次性发起一屏的请求，直接把服务端的媒体读取
 * 名额打满，且 `Cache-Control: no-store` 让每次重新挂载都重下一遍。
 */
export function MediaImage({ src, alt, eager, allowRetry = true, className }: MediaImageProps) {
  const loader = useMediaLoader();
  // 根元素是 span 而不是 div：封面经常嵌在 <a> 里的 <span> 中，div 不是 phrasing content，
  // 那样的嵌套是无效 HTML。
  const containerRef = useRef<HTMLSpanElement>(null);
  const inView = useInView(containerRef, eager !== true);
  const [attempt, setAttempt] = useState(0);
  const [state, setState] = useState<ImageState>({ kind: 'idle' });

  useEffect(() => {
    if (!inView) {
      setState({ kind: 'idle' });
      return;
    }
    const controller = new AbortController();
    let cancelled = false;
    let acquired = false;
    setState({ kind: 'loading' });
    loader.acquire(src, controller.signal).then(
      (objectUrl) => {
        acquired = true;
        // 已经卸载或滚出视口：立即归还引用，不要让 object URL 悬着占内存。
        if (cancelled) {
          loader.release(src);
          return;
        }
        setState({ kind: 'ready', objectUrl });
      },
      (error: unknown) => {
        if (cancelled || isAbortError(error)) return;
        setState({ kind: 'error', error });
      }
    );
    return () => {
      cancelled = true;
      controller.abort();
      if (acquired) loader.release(src);
    };
  }, [loader, src, inView, attempt]);

  const retry = useCallback(() => {
    setAttempt((value) => value + 1);
  }, []);

  return (
    <span ref={containerRef} className={className === undefined ? 'gal-media' : `gal-media ${className}`}>
      {state.kind === 'ready' ? (
        <img className="gal-media__img" src={state.objectUrl} alt={alt} draggable={false} />
      ) : state.kind === 'error' ? (
        <MediaFailure error={state.error} onRetry={allowRetry ? retry : undefined} />
      ) : (
        <span className="gal-media__placeholder" aria-hidden={alt === '' ? true : undefined}>
          {state.kind === 'loading' ? <Spinner label={`正在加载${alt === '' ? '图片' : alt}`} /> : null}
        </span>
      )}
    </span>
  );
}

/**
 * 图片加载失败的降级块。
 *
 * `MEDIA_READ_BUSY` 单独说明：它不是错误，而是服务端为保护 Source 设的并发上限，
 * 稍后重试就会成功。把它显示成通用“加载失败”会让用户以为文件坏了。
 */
function MediaFailure({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const code = errorCode(error);
  const busy = code === 'MEDIA_READ_BUSY';
  const retry = onRetry;
  return (
    <span className={busy ? 'gal-media__failure gal-media__failure--busy' : 'gal-media__failure'}>
      <span className="gal-media__failure-text">{busy ? '媒体读取通道已满' : describeError(error)}</span>
      {code === undefined ? null : <span className="gal-media__failure-code">{code}</span>}
      {retry !== undefined && (busy || isRetryable(error)) ? (
        <Button variant="ghost" onPress={retry}>
          重试
        </Button>
      ) : null}
    </span>
  );
}

/* ————————————————————————————— 缩略图升级 ————————————————————————————— */

/** 停留多久才值得为它建一个缩略图任务。快速滚过的媒体不应该产生任何 Job。 */
const THUMBNAIL_DWELL_MS = 500;

export interface ThumbnailSourceOptions {
  mediaId: string;
  media: ThumbnailCandidate;
  queryPublicationId?: string;
  csrfToken: string;
  canDerive: boolean;
  /** 元素是否已在视口内。只有在视口内停留够久才发起生成请求。 */
  inView: boolean;
}

/**
 * 为一个媒体挑选实际使用的图片地址。
 *
 * 契约现实决定了这里必须克制：没有批量接口，也没有“是否已生成”查询，唯一的探测方式就是
 * `POST /derived-assets` 本身。因此：
 *
 * - **列表页的封面根本不走这条路**：`PublishedWork` 只给出 coverMediaId，不含 mimeType 与
 *   内容确认状态，无法判断缩略图是否可能存在；为此逐个 POST 探测，等于一屏 50 个 Job。
 *   列表封面直接读原图，由并发闸兜住压力。
 * - **作品详情的媒体网格**才使用它：那里一次 `GET /works/{id}/media` 就拿到了全部
 *   mimeType 与确认状态，可以精确挑出“当前 transform 真的能处理”的 JPEG。
 * - 首次访问仍显示原图，缩略图在后台生成；下次进入同一作品时直接命中已记住的 assetKey。
 *   这是刻意的取舍：为了让缩略图更早出现而先卡住首屏，反而更糟。
 */
export function useThumbnailSource(options: ThumbnailSourceOptions): string {
  const { mediaId, media, queryPublicationId, csrfToken, canDerive, inView } = options;
  const fullSize = mediaContentUrl(mediaId, { queryPublicationId });
  const known = thumbnailScheduler.known(mediaId);
  const [assetKey, setAssetKey] = useState<string | undefined>(
    known?.status === 'ready' ? known.assetKey : undefined
  );

  useEffect(() => {
    if (assetKey !== undefined) return;
    if (!inView) return;
    if (!canRequestThumbnail(media, canDerive)) return;
    const controller = new AbortController();
    const timer = setTimeout(() => {
      void thumbnailScheduler
        .request(mediaId, { csrfToken, queryPublicationId, signal: controller.signal })
        .then((outcome) => {
          if (outcome.status === 'ready' && !controller.signal.aborted) setAssetKey(outcome.assetKey);
        });
    }, THUMBNAIL_DWELL_MS);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [assetKey, inView, mediaId, media, canDerive, csrfToken, queryPublicationId]);

  return assetKey === undefined ? fullSize : derivedAssetUrl(assetKey);
}

/* ————————————————————————————— 无封面 ————————————————————————————— */

/** 没有封面时的占位。它是真实事实（规则没有解析出封面），不是加载失败。 */
export function CoverMissing({ label = '暂无封面' }: { label?: string }) {
  return (
    <span className="gal-media gal-media--empty">
      <span className="gal-media__placeholder" aria-hidden="true">
        ▢
      </span>
      <VisuallyHidden>{label}</VisuallyHidden>
    </span>
  );
}
