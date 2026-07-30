/*
 * 作品浏览器：工具条 + 网格 + 快照分页。
 *
 * 平台页、作者页与全部作品共用它，差别只有一个固定的查询范围。这样搜索、排序、筛选、
 * 分页、游标失效与空/错误态的行为在整个画廊里只有一份实现。
 *
 * 排序由服务端执行并进入签名游标。Source 页面按规则下发的 workOptions/default 渲染，
 * 全局页面使用完整公共词表；客户端不对分页结果做本地重排。
 */

import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { useSearchParams } from 'react-router';
import { Button, EmptyState, ErrorState, Select, Spinner, Switch, TextInput } from '../../design';
import { describeError, errorCode, errorCopy, errorCorrelationId } from '../../shared/errors';
import { useCapability } from '../../shared/session';
import {
  buildFilterAst,
  formatTotal,
  isLowerBoundTotal,
  type HiddenVisibility,
  type SourcePresentation
} from '../contracts';
import { useWorkList, type WorkQueryParams } from '../queries';
import { useInView } from './media';
import { WorkGrid } from './work';

const HIDDEN_OPTIONS = [
  { id: 'exclude', label: '不显示隐藏作品' },
  { id: 'include', label: '包含隐藏作品' },
  { id: 'only', label: '只看隐藏作品' }
] as const;

const KIND_OPTIONS = [
  { id: 'any', label: '全部媒体类型' },
  { id: 'image', label: '含图片' },
  { id: 'video', label: '含视频' },
  { id: 'audio', label: '含音频' }
] as const;

const SORT_LABELS: Record<WorkQueryParams['sort'], string> = {
  title_asc: '标题升序',
  title_desc: '标题降序',
  name_asc: '名称升序',
  name_desc: '名称降序',
  date_asc: '发布时间升序',
  date_desc: '发布时间降序',
  progress_asc: '阅读进度升序',
  progress_desc: '阅读进度降序'
};

const DEFAULT_SORTS: WorkQueryParams['sort'][] = [
  'title_asc',
  'title_desc',
  'date_desc',
  'date_asc',
  'progress_desc',
  'progress_asc'
];

function isWorkSort(value: string | undefined | null): value is WorkQueryParams['sort'] {
  return value !== undefined && value !== null && Object.hasOwn(SORT_LABELS, value);
}

function readHidden(value: string | null): HiddenVisibility {
  return value === 'include' || value === 'only' ? value : 'exclude';
}

export interface WorkBrowserScope {
  sourceId?: string;
  libraryId?: string;
  creatorId?: string;
}

export interface WorkBrowserProps {
  /** 页面固定的查询范围，不由 URL 参数控制。 */
  scope?: WorkBrowserScope;
  /** 来源的呈现配置。决定作者称谓与时间显示时区，**不得**在前端硬编码平台差异。 */
  presentation?: SourcePresentation | null;
  /** 标题区。放在工具条之上。 */
  heading?: ReactNode;
  /** 结果为空时的说明。 */
  emptyDescription?: ReactNode;
}

export function WorkBrowser({ scope, presentation, heading, emptyDescription }: WorkBrowserProps) {
  const [searchParams, setSearchParams] = useSearchParams();
  const canSeeHidden = useCapability('library.write');

  const q = searchParams.get('q') ?? '';
  const tag = searchParams.get('tag') ?? '';
  const sortOptions = useMemo(() => {
    const declared = presentation?.sort?.workOptions?.filter(isWorkSort) ?? [];
    const values = declared.length === 0 ? DEFAULT_SORTS : [...new Set(declared)];
    return values.map((id) => ({ id, label: SORT_LABELS[id] }));
  }, [presentation?.sort?.workOptions]);
  const declaredDefault = presentation?.sort?.workDefault;
  const fallbackSort =
    isWorkSort(declaredDefault) && sortOptions.some((option) => option.id === declaredDefault)
      ? declaredDefault
      : (sortOptions[0]?.id ?? 'title_asc');
  const requestedSort = searchParams.get('sort');
  const sort =
    isWorkSort(requestedSort) && sortOptions.some((option) => option.id === requestedSort)
      ? requestedSort
      : fallbackSort;
  const favorite = searchParams.get('fav') === '1';
  const hidden = canSeeHidden ? readHidden(searchParams.get('hidden')) : 'exclude';
  const kind = searchParams.get('kind') ?? 'any';

  const [draft, setDraft] = useState(q);
  useEffect(() => {
    setDraft(q);
  }, [q]);

  const update = useCallback(
    (changes: Record<string, string | undefined>) => {
      const next = new URLSearchParams(searchParams);
      for (const [key, value] of Object.entries(changes)) {
        if (value === undefined || value === '') next.delete(key);
        else next.set(key, value);
      }
      // 换筛选条件等于换一次查询，历史里应当留下记录：用户按返回时回到上一组条件。
      setSearchParams(next);
    },
    [searchParams, setSearchParams]
  );

  const filter = useMemo(
    () =>
      buildFilterAst({
        favorite,
        hidden,
        creatorId: scope?.creatorId,
        mediaKind: kind === 'any' ? undefined : kind
      }),
    [favorite, hidden, scope?.creatorId, kind]
  );

  const params: WorkQueryParams = {
    scopeKey: JSON.stringify([scope?.sourceId ?? null, scope?.libraryId ?? null, scope?.creatorId ?? null]),
    q,
    tag,
    sourceId: scope?.sourceId,
    libraryId: scope?.libraryId,
    filter,
    sort
  };
  const list = useWorkList(params);

  const sentinelRef = useRef<HTMLDivElement>(null);
  const sentinelVisible = useInView(sentinelRef, true);
  const { hasNextPage, isFetching, fetchNextPage, cursorNotice } = list;
  useEffect(() => {
    // 有游标通知时不自动续页：刚刚就是因为续页失败才重来的，立刻再续一次只会原地打转。
    if (cursorNotice !== undefined) return;
    if (sentinelVisible && hasNextPage && !isFetching) fetchNextPage();
  }, [sentinelVisible, hasNextPage, isFetching, fetchNextPage, cursorNotice]);

  const totalText = formatTotal(list.total);
  const authorLabel = presentation?.authorLabel;
  const timeZone = presentation?.time?.displayTimezone;
  const hasFilters = q !== '' || tag !== '' || favorite || hidden !== 'exclude' || kind !== 'any';

  return (
    <div className="gal-browser">
      {heading}
      <form
        className="gal-toolbar"
        role="search"
        onSubmit={(event) => {
          event.preventDefault();
          update({ q: draft });
        }}
      >
        <TextInput
          className="gal-toolbar__search"
          label="搜索作品"
          type="search"
          value={draft}
          onChange={setDraft}
          placeholder="标题、创作者、标签或文件名"
        />
        <Button type="submit" variant="primary">
          搜索
        </Button>
        <Select
          className="gal-toolbar__control"
          label="排序"
          options={sortOptions}
          selectedKey={sort}
          onSelectionChange={(key) => update({ sort: key === fallbackSort ? undefined : (key ?? undefined) })}
          description="服务端排序；缺失日期始终排在末尾"
        />
        <Select
          className="gal-toolbar__control"
          label="媒体类型"
          options={KIND_OPTIONS}
          selectedKey={kind}
          onSelectionChange={(key) => update({ kind: key === 'any' ? undefined : (key ?? undefined) })}
        />
        {canSeeHidden ? (
          <Select
            className="gal-toolbar__control"
            label="隐藏作品"
            options={HIDDEN_OPTIONS}
            selectedKey={hidden}
            onSelectionChange={(key) =>
              update({ hidden: key === 'exclude' ? undefined : (key ?? undefined) })
            }
          />
        ) : null}
        <Switch isSelected={favorite} onChange={(next) => update({ fav: next ? '1' : undefined })}>
          只看收藏
        </Switch>
        {hasFilters ? (
          <Button
            variant="ghost"
            onPress={() => {
              setDraft('');
              update({ q: undefined, tag: undefined, fav: undefined, hidden: undefined, kind: undefined });
            }}
          >
            清除筛选
          </Button>
        ) : null}
      </form>

      {tag === '' ? null : (
        <p className="gal-browser__chips">
          <span>标签筛选：{tag}</span>
          <Button variant="ghost" onPress={() => update({ tag: undefined })}>
            移除
          </Button>
        </p>
      )}

      <p className="gal-browser__summary" role="status">
        {totalText ?? '未统计数量'}
        {isLowerBoundTotal(list.total) ? '（超出服务端统计预算，仅给出下限）' : ''}
        {list.isFetching ? <Spinner label="正在获取结果" /> : null}
      </p>

      {list.cursorNotice ? (
        <div className="gal-notice" role="status">
          <span>
            {list.cursorNotice.kind === 'restart-auto'
              ? `${errorCopy('CURSOR_EXPIRED')}（已自动从第一页重新加载）`
              : errorCopy('CURSOR_INVALID')}
          </span>
          {list.cursorNotice.kind === 'restart-manual' ? (
            <Button variant="secondary" onPress={list.restart}>
              从第一页重新开始
            </Button>
          ) : (
            <Button variant="secondary" onPress={list.dismissNotice}>
              知道了
            </Button>
          )}
        </div>
      ) : null}

      {list.isPending ? (
        <div className="gal-browser__state">
          <Spinner label="正在加载作品" />
        </div>
      ) : list.error !== undefined ? (
        <ErrorState
          description={describeError(list.error)}
          code={errorCode(list.error)}
          correlationId={errorCorrelationId(list.error)}
          onRetry={list.refetch}
        />
      ) : list.works.length === 0 ? (
        <EmptyState
          title="没有匹配的作品"
          description={
            emptyDescription ??
            (hasFilters
              ? '当前筛选条件下没有作品。可以移除部分条件，或换一个关键词。'
              : '这个范围内还没有已发布的作品。完成一次扫描与发布后即可浏览。')
          }
          action={
            hasFilters ? (
              <Button
                variant="secondary"
                onPress={() => {
                  setDraft('');
                  update({
                    q: undefined,
                    tag: undefined,
                    fav: undefined,
                    hidden: undefined,
                    kind: undefined
                  });
                }}
              >
                清除筛选
              </Button>
            ) : undefined
          }
        />
      ) : (
        <>
          <WorkGrid
            works={list.works}
            timeZone={timeZone}
            authorLabel={authorLabel}
            isVisualPlaceholder={list.isPlaceholderData}
          />
          <div className="gal-browser__more" ref={sentinelRef}>
            {list.hasNextPage ? (
              // 最后一页会卸载这个按钮；使用 isPending 会让 RAC live-announcer 短暂保留
              // 指向已消失按钮的 role=img。稳定的可见文案 + disabled 已完整表达当前状态。
              <Button variant="secondary" isDisabled={list.isFetchingNextPage} onPress={list.fetchNextPage}>
                {list.isFetchingNextPage ? '正在加载更多' : '加载更多'}
              </Button>
            ) : (
              <span className="gal-muted">已到末页</span>
            )}
          </div>
        </>
      )}

      {list.queryPublicationId === undefined ? null : (
        <p className="gal-snapshot">
          结果来自快照 <code>{list.queryPublicationId}</code>
        </p>
      )}
    </div>
  );
}
