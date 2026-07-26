/*
 * 契约投影的纯函数。
 *
 * 这里守住的都是"写错了不会崩、只会对用户说谎"的地方：把下限数量说成精确数、把缺失的
 * 发布时间显示成 Invalid Date、把没有创作者显示成 undefined、把未注册字段发给过滤器。
 */

import { describe, expect, it } from 'vitest';
import {
  badgeStyle,
  badgesAt,
  buildFilterAst,
  classifyCursorFailure,
  filterNeedsHiddenCapability,
  formatCreator,
  formatProgress,
  formatPublishedAt,
  formatTotal,
  isCreatorMissing,
  isLiveUserStateField,
  isLowerBoundTotal,
  mediaContentUrl,
  MISSING_CREATOR_TEXT,
  type RuleBadge
} from './contracts';
import { GalleryError } from '../api/client';

describe('数量协议', () => {
  it('exact 显示精确数量', () => {
    expect(formatTotal({ mode: 'exact', value: 42, protocolVersion: 1 })).toBe('共 42 件作品');
  });

  it('lower_bound 必须渲染成下限形态，不能冒充精确值', () => {
    const text = formatTotal({ mode: 'lower_bound', value: 1000, protocolVersion: 1 });
    expect(text).toBe('共 1000+ 件作品');
    // 关键断言：它不能等于同一个数字的精确形态。
    expect(text).not.toBe(formatTotal({ mode: 'exact', value: 1000, protocolVersion: 1 }));
    expect(isLowerBoundTotal({ mode: 'lower_bound', value: 1000, protocolVersion: 1 })).toBe(true);
  });

  it('lower_bound 没有 value 时也不编造数字', () => {
    const text = formatTotal({ mode: 'lower_bound', protocolVersion: 1 });
    expect(text).toBe('作品数超出服务端统计预算');
    expect(text).not.toMatch(/\d/);
  });

  it('omitted 不显示任何数量', () => {
    expect(formatTotal({ mode: 'omitted', protocolVersion: 1 })).toBeUndefined();
    expect(formatTotal(undefined)).toBeUndefined();
  });
});

describe('缺失事实的空态', () => {
  it('没有发布时间返回 undefined，而不是 Invalid Date', () => {
    expect(formatPublishedAt(null)).toBeUndefined();
    expect(formatPublishedAt(undefined)).toBeUndefined();
    expect(formatPublishedAt('')).toBeUndefined();
    expect(formatPublishedAt('不是时间')).toBeUndefined();
  });

  it('能按来源声明的时区格式化，时区无法识别时退回本地时区而不是抛异常', () => {
    expect(formatPublishedAt('2026-07-27T00:00:00Z', 'Asia/Shanghai')).toBe('2026/07/27');
    expect(formatPublishedAt('2026-07-27T00:00:00Z', 'Mars/Olympus')).toMatch(/2026/);
  });

  it('没有创作者时给出中文空态，不出现 undefined', () => {
    expect(formatCreator('')).toBe(MISSING_CREATOR_TEXT);
    expect(formatCreator('   ')).toBe(MISSING_CREATOR_TEXT);
    expect(formatCreator(undefined)).toBe(MISSING_CREATOR_TEXT);
    expect(formatCreator('画师甲')).toBe('画师甲');
    expect(isCreatorMissing('')).toBe(true);
    expect(isCreatorMissing('画师甲')).toBe(false);
  });

  it('进度被夹在 0 到 1 之间', () => {
    expect(formatProgress(0)).toBe('0%');
    expect(formatProgress(0.256)).toBe('26%');
    expect(formatProgress(5)).toBe('100%');
  });
});

describe('角标', () => {
  const badge: RuleBadge = {
    id: 'r18',
    order: 1,
    position: 'cover_top_left',
    label: 'R18',
    color: '#fff',
    background: '#800',
    colorLight: '#400',
    backgroundLight: '#fcc'
  };

  it('按主题挑选规则下发的两套配色', () => {
    expect(badgeStyle(badge, 'dark')).toEqual({ color: '#fff', background: '#800' });
    expect(badgeStyle(badge, 'light')).toEqual({ color: '#400', background: '#fcc' });
  });

  it('高对比主题忽略规则配色，交给设计系统兜底', () => {
    expect(badgeStyle(badge, 'high-contrast')).toBeUndefined();
  });

  it('只按位置过滤，不重排服务端给的顺序', () => {
    const second: RuleBadge = { ...badge, id: 'new', order: 0 };
    const list = [badge, second];
    expect(badgesAt(list, 'cover_top_left').map((item) => item.id)).toEqual(['r18', 'new']);
    expect(badgesAt(list, 'tag_leading')).toEqual([]);
    expect(badgesAt(undefined, 'tag_leading')).toEqual([]);
  });
});

describe('结构化过滤', () => {
  it('没有条件时不发送 filter', () => {
    expect(buildFilterAst({})).toBeUndefined();
    expect(buildFilterAst({ hidden: 'exclude' })).toBeUndefined();
  });

  it('单条件不套 all', () => {
    expect(buildFilterAst({ favorite: true })).toBe(
      JSON.stringify({ field: 'overlay.favorite', op: 'eq', value: true })
    );
  });

  it('包含隐藏作品要显式写出两种取值的并集', () => {
    const parsed: unknown = JSON.parse(buildFilterAst({ hidden: 'include' }) ?? '{}');
    expect(parsed).toEqual({
      any: [
        { field: 'overlay.hidden', op: 'eq', value: true },
        { field: 'overlay.hidden', op: 'eq', value: false }
      ]
    });
  });

  it('多条件用 all 组合，字段名与服务端注册表一致', () => {
    const parsed: unknown = JSON.parse(
      buildFilterAst({ favorite: true, creatorId: 'creator_1', mediaKind: 'image' }) ?? '{}'
    );
    expect(parsed).toEqual({
      all: [
        { field: 'overlay.favorite', op: 'eq', value: true },
        { field: 'creator.id', op: 'eq', value: 'creator_1' },
        { field: 'media.kind', op: 'eq', value: 'image' }
      ]
    });
  });

  it('引用 overlay.hidden 时要求额外 capability', () => {
    expect(filterNeedsHiddenCapability({ hidden: 'only' })).toBe(true);
    expect(filterNeedsHiddenCapability({ hidden: 'include' })).toBe(true);
    expect(filterNeedsHiddenCapability({ favorite: true })).toBe(false);
  });
});

describe('游标失败分类', () => {
  const fault = (code: string, retryable: boolean) =>
    new GalleryError({ error: { code, retryable, correlationId: 'corr' } as never }, 409);

  it('CURSOR_EXPIRED 自动从第一页重来', () => {
    expect(classifyCursorFailure(fault('CURSOR_EXPIRED', true))).toBe('restart-auto');
  });

  it('CURSOR_INVALID 同样从头来，但不自动重试', () => {
    expect(classifyCursorFailure(fault('CURSOR_INVALID', false))).toBe('restart-manual');
  });

  it('其它失败不走游标恢复路径', () => {
    expect(classifyCursorFailure(fault('FORBIDDEN', false))).toBe('none');
    expect(classifyCursorFailure(new Error('network'))).toBe('none');
  });
});

describe('live 字段与媒体地址', () => {
  it('liveUserStateFields 决定哪些快照值可能过时', () => {
    const response = { liveUserStateFields: ['favorite', 'progress'] };
    expect(isLiveUserStateField(response, 'favorite')).toBe(true);
    expect(isLiveUserStateField(response, 'hidden')).toBe(false);
    expect(isLiveUserStateField(undefined, 'favorite')).toBe(false);
  });

  it('媒体地址按需带上快照绑定与下载标记', () => {
    expect(mediaContentUrl('media_1')).toBe('/api/v1/media/media_1/content');
    expect(mediaContentUrl('media_1', { queryPublicationId: 'qpub_1', download: true })).toBe(
      '/api/v1/media/media_1/content?queryPublicationId=qpub_1&download=true'
    );
  });
});
