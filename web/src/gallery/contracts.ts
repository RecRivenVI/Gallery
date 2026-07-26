/*
 * 画廊侧的契约投影与纯函数。
 *
 * 这里只放**不依赖 React 也不发起请求**的东西：类型别名、展示格式化、结构化过滤 AST 的
 * 构造、失败分类。页面与 hook 都从这里取，避免同一条契约事实在多个组件里各写一遍。
 *
 * 三条最容易写错、且已在契约里明确的事实：
 *
 * 1. `total` 有三种模式。`lower_bound` 是**下限**，把它当精确数渲染会直接对用户说谎，
 *    因此这里统一渲染成 “1000+” 形态；`omitted` 不显示任何数量。
 * 2. 作品可能没有发布时间，也可能没有创作者。两者都是真实数据事实，不是错误，
 *    绝不能渲染成 `Invalid Date` 或 `undefined`。
 * 3. `badges` 是规则派生的展示事实，随快照下发，客户端**不得**自行推导出现条件或配色。
 */

import type { CSSProperties } from 'react';
import type { components } from '../api/schema.gen';
import { errorCode } from '../shared/errors';
import type { ResolvedTheme } from '../shared/theme';

/* ————————————————————————————— 契约类型别名 ————————————————————————————— */

export type PublishedWork = components['schemas']['PublishedWork'];
export type WorkListResponse = components['schemas']['WorkListResponse'];
export type PublishedMedia = components['schemas']['PublishedMedia'];
export type MediaListResponse = components['schemas']['MediaListResponse'];
export type WorkOverlayState = components['schemas']['WorkOverlayState'];
export type WorkOverlayPutRequest = components['schemas']['WorkOverlayPutRequest'];
export type RuleBadge = components['schemas']['Badge'];
export type Source = components['schemas']['Source'];
export type SourcePresentation = components['schemas']['SourcePresentation'];
export type Library = components['schemas']['Library'];
export type Creator = components['schemas']['Creator'];
export type FileRoot = components['schemas']['FileRoot'];
export type FileRootEntry = components['schemas']['FileRootEntry'];
export type FileRootEntryListResponse = components['schemas']['FileRootEntryListResponse'];
export type Total = components['schemas']['Total'];
export type Job = components['schemas']['Job'];

/* ————————————————————————————— 数量 ————————————————————————————— */

/**
 * 把 `total` 渲染成中文说明。返回 undefined 表示**不显示任何数量**。
 *
 * `lower_bound` 表示命中数超过服务端统计预算，服务端只给出下限值。它必须显示成
 * “1000+”：写成“共 1000 件”是把下限伪造成精确值。
 */
export function formatTotal(total: Total | undefined): string | undefined {
  if (!total) return undefined;
  switch (total.mode) {
    case 'exact':
      return total.value === undefined ? undefined : `共 ${total.value} 件作品`;
    case 'lower_bound':
      return total.value === undefined ? '作品数超出服务端统计预算' : `共 ${total.value}+ 件作品`;
    case 'omitted':
      return undefined;
  }
}

/** 该数量是否只是下限。用于给数量补一句解释，而不是让用户以为它精确。 */
export function isLowerBoundTotal(total: Total | undefined): boolean {
  return total?.mode === 'lower_bound';
}

/* ————————————————————————————— 时间与创作者 ————————————————————————————— */

/** 没有发布时间时的统一说法。它是常态，不是错误。 */
export const MISSING_PUBLISHED_AT_TEXT = '未记录发布时间';
/** 没有创作者时的统一说法。 */
export const MISSING_CREATOR_TEXT = '未标注创作者';

/**
 * 按来源声明的显示时区格式化发布时间。
 *
 * 返回 undefined 表示这个作品**没有**可用发布时间（`null`、空串或不可解析）。调用方据此
 * 渲染 MISSING_PUBLISHED_AT_TEXT，绝不能把 undefined 直接插进模板变成 “undefined”，
 * 也不能把不可解析的值交给 `toLocaleString` 变成 “Invalid Date”。
 */
export function formatPublishedAt(value: string | null | undefined, timeZone?: string): string | undefined {
  if (value === null || value === undefined || value === '') return undefined;
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return undefined;
  const options: Intl.DateTimeFormatOptions = {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit'
  };
  if (timeZone !== undefined && timeZone !== '') {
    try {
      // 规则下发的时区名可能不被当前浏览器识别；识别不了时退回本地时区，
      // 而不是让整棵组件树因为一个 RangeError 崩掉。
      return new Intl.DateTimeFormat('zh-CN', { ...options, timeZone }).format(parsed);
    } catch {
      /* 落到下面的本地时区分支 */
    }
  }
  return new Intl.DateTimeFormat('zh-CN', options).format(parsed);
}

/** 创作者显示名。空串表示该作品没有创作者，这是真实数据事实。 */
export function formatCreator(creator: string | undefined): string {
  return creator === undefined || creator.trim() === '' ? MISSING_CREATOR_TEXT : creator;
}

/** 该作品是否没有创作者。用于把缺失渲染成弱化文本而不是正常署名。 */
export function isCreatorMissing(creator: string | undefined): boolean {
  return creator === undefined || creator.trim() === '';
}

/** 阅读进度百分比文本。 */
export function formatProgress(progress: number): string {
  const clamped = Math.min(1, Math.max(0, progress));
  return `${Math.round(clamped * 100)}%`;
}

/* ————————————————————————————— 角标 ————————————————————————————— */

/**
 * 规则下发的角标配色。
 *
 * 高对比主题下**忽略**规则配色：那套颜色由规则作者按普通浅/深色主题挑选，无法保证
 * 7:1 对比度。此时返回 undefined，调用方回退到设计系统的 Badge（token 配色）。
 */
export function badgeStyle(badge: RuleBadge, theme: ResolvedTheme): CSSProperties | undefined {
  if (theme === 'high-contrast') return undefined;
  const color = theme === 'light' ? badge.colorLight : badge.color;
  const background = theme === 'light' ? badge.backgroundLight : badge.background;
  const border = theme === 'light' ? badge.borderLight : badge.border;
  const style: CSSProperties = {};
  if (color !== undefined && color !== '') style.color = color;
  if (background !== undefined && background !== '') style.background = background;
  if (border !== undefined && border !== '') style.borderColor = border;
  return Object.keys(style).length === 0 ? undefined : style;
}

/** 按位置挑选角标。服务端已按 order、id 排好序，这里只过滤，**不重排**。 */
export function badgesAt(
  badges: readonly RuleBadge[] | undefined,
  position: RuleBadge['position']
): RuleBadge[] {
  return (badges ?? []).filter((badge) => badge.position === position);
}

/* ————————————————————————————— 结构化过滤 ————————————————————————————— */

/** 隐藏作品的可见性选择。服务端默认排除隐藏作品，`exclude` 因此不产生任何过滤节点。 */
export type HiddenVisibility = 'exclude' | 'include' | 'only';

export interface WorkFilterState {
  /** 只看收藏。 */
  favorite?: boolean;
  /** 隐藏作品的可见性。引用 `overlay.hidden` 需要 library.write，调用方先判定 capability。 */
  hidden?: HiddenVisibility;
  /** 限定创作者（等价组由服务端解析，客户端只传 ID）。 */
  creatorId?: string;
  /** 限定媒体类型，例如 image / video。 */
  mediaKind?: string;
}

type FilterNode =
  { all: FilterNode[] } | { any: FilterNode[] } | { field: string; op: string; value: unknown };

/**
 * 构造服务端权威过滤 AST 的 JSON 编码。返回 undefined 表示本次查询不带 `filter`。
 *
 * 字段名必须与服务端注册表一致（`internal/query/filter.go`）：未注册字段一律
 * VALIDATION_ERROR。这里只使用当前确实注册的字段，不发明新字段。
 */
export function buildFilterAst(state: WorkFilterState): string | undefined {
  const nodes: FilterNode[] = [];
  if (state.favorite === true) nodes.push({ field: 'overlay.favorite', op: 'eq', value: true });
  if (state.hidden === 'only') nodes.push({ field: 'overlay.hidden', op: 'eq', value: true });
  if (state.hidden === 'include') {
    // 服务端默认追加“非隐藏”的隐式条件；显式写出两种取值的并集才能真正包含隐藏作品。
    nodes.push({
      any: [
        { field: 'overlay.hidden', op: 'eq', value: true },
        { field: 'overlay.hidden', op: 'eq', value: false }
      ]
    });
  }
  if (state.creatorId !== undefined && state.creatorId !== '') {
    nodes.push({ field: 'creator.id', op: 'eq', value: state.creatorId });
  }
  if (state.mediaKind !== undefined && state.mediaKind !== '') {
    nodes.push({ field: 'media.kind', op: 'eq', value: state.mediaKind });
  }
  if (nodes.length === 0) return undefined;
  if (nodes.length === 1) return JSON.stringify(nodes[0]);
  return JSON.stringify({ all: nodes });
}

/** 该过滤是否引用了 `overlay.hidden`。服务端对它额外要求 library.write。 */
export function filterNeedsHiddenCapability(state: WorkFilterState): boolean {
  return state.hidden === 'include' || state.hidden === 'only';
}

/* ————————————————————————————— 分页失败 ————————————————————————————— */

/**
 * 游标失败后的恢复方式。
 *
 * - `restart-auto`：`CURSOR_EXPIRED`。快照已经换代，游标不可能再用；服务端声明可重试，
 *   因此丢弃游标、自动从第一页重来，并告知用户列表已经刷新。
 * - `restart-manual`：`CURSOR_INVALID`。不可重试，同样只能从第一页重来，但**不自动**发起，
 *   由用户按下按钮，避免对一个确定性失败反复打服务端。
 * - `none`：其它失败按普通错误处理。
 */
export type CursorRecovery = 'restart-auto' | 'restart-manual' | 'none';

export function classifyCursorFailure(error: unknown): CursorRecovery {
  switch (errorCode(error)) {
    case 'CURSOR_EXPIRED':
      return 'restart-auto';
    case 'CURSOR_INVALID':
      return 'restart-manual';
    default:
      return 'none';
  }
}

/* ————————————————————————————— live 字段 ————————————————————————————— */

/**
 * 该字段在列表快照里是否可能已经过时。
 *
 * `liveUserStateFields` 当前是 `["favorite","progress"]`：这两个字段的快照值只解释
 * “本次结果为什么这样过滤/排序”，真值必须从 `GET /works/{id}/overlay` 读。列表里直接
 * 相信快照值，会在用户切换收藏之后把旧值显示回去。
 */
export function isLiveUserStateField(
  response: Pick<WorkListResponse, 'liveUserStateFields'> | undefined,
  field: string
): boolean {
  return (response?.liveUserStateFields ?? []).includes(field);
}

/* ————————————————————————————— 媒体地址 ————————————————————————————— */

export interface MediaContentOptions {
  queryPublicationId?: string;
  /** 强制以附件交付。用于浏览器无法内联渲染的类型。 */
  download?: boolean;
}

/** 媒体正文地址。Cookie 认证，可直接交给 `<video src>` 或本目录的媒体加载器。 */
export function mediaContentUrl(mediaId: string, options: MediaContentOptions = {}): string {
  const params = new URLSearchParams();
  if (options.queryPublicationId !== undefined && options.queryPublicationId !== '') {
    params.set('queryPublicationId', options.queryPublicationId);
  }
  if (options.download === true) params.set('download', 'true');
  const query = params.toString();
  return `/api/v1/media/${encodeURIComponent(mediaId)}/content${query === '' ? '' : `?${query}`}`;
}

/** 派生资源正文地址。assetKey 是内容寻址的完整 SHA-256。 */
export function derivedAssetUrl(assetKey: string): string {
  return `/api/v1/derived-assets/${encodeURIComponent(assetKey)}/content`;
}

/** 该媒体能否在浏览器里当作图片内联渲染。 */
export function isImageMedia(media: Pick<PublishedMedia, 'kind' | 'mimeType'>): boolean {
  return media.kind === 'image' || media.mimeType.startsWith('image/');
}

/** 该媒体能否用原生 `<video>` 播放。视频**不走** blob 加载器，否则会整段下载。 */
export function isVideoMedia(media: Pick<PublishedMedia, 'kind' | 'mimeType'>): boolean {
  return media.kind === 'video' || media.mimeType.startsWith('video/');
}

export function isAudioMedia(media: Pick<PublishedMedia, 'kind' | 'mimeType'>): boolean {
  return media.kind === 'audio' || media.mimeType.startsWith('audio/');
}
