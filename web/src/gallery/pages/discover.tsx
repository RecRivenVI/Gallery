/*
 * 发现路径：首页 → 平台 / 创作者 → 作品列表。
 *
 * **平台的展示形态完全由服务端下发的 `presentation` 决定**：显示名、字形图标、配色、
 * 对"作者"的称谓、时间显示时区。前端不认识任何平台名，也不按平台名分支——规则是 Source
 * 差异的唯一解释入口，在界面里硬编码平台特例会绕开它。
 */

import { Link, useParams, useSearchParams } from 'react-router-dom';
import { Badge, Button, EmptyState, ErrorState, Select, Spinner } from '../../design';
import { describeError, errorCode, errorCorrelationId } from '../../shared/errors';
import { useCapability } from '../../shared/session';
import { mediaContentUrl, type Creator, type Source, type SourcePresentation } from '../contracts';
import { useCreator, useCreators, useFileRoots, useSources, type CreatorSort } from '../queries';
import { WorkBrowser } from '../components/browser';
import { CoverMissing, MediaImage } from '../components/media';

/* ————————————————————————————— 平台卡片 ————————————————————————————— */

function presentationName(source: Source): string {
  const name = source.presentation?.name;
  return name === undefined || name === '' ? source.displayName : name;
}

function PlatformIcon({ presentation }: { presentation: SourcePresentation | null | undefined }) {
  const icon = presentation?.icon;
  if (!icon) return null;
  return (
    <span
      className="gal-glyph"
      aria-hidden="true"
      style={{
        color: icon.color,
        background: icon.background,
        borderColor: icon.border
      }}
    >
      {icon.glyph}
    </span>
  );
}

function SourceCard({ source }: { source: Source }) {
  const description = source.presentation?.description;
  return (
    <article className="gal-tile">
      <Link className="gal-tile__link" to={`/sources/${encodeURIComponent(source.id)}`}>
        <span className="gal-tile__cover">
          {source.coverMediaId === null || source.coverMediaId === undefined ? (
            <CoverMissing label={`${presentationName(source)} 暂无封面`} />
          ) : (
            <MediaImage
              src={mediaContentUrl(source.coverMediaId, {
                queryPublicationId: source.queryPublicationId ?? undefined
              })}
              alt=""
              allowRetry={false}
            />
          )}
        </span>
        <span className="gal-tile__title">
          <PlatformIcon presentation={source.presentation} />
          {presentationName(source)}
        </span>
        {description === undefined ? null : <span className="gal-tile__meta gal-muted">{description}</span>}
      </Link>
      {source.available ? null : (
        <p className="gal-tile__meta">
          <Badge tone="warning">Source 离线</Badge>
          <span className="gal-muted"> 已发布的资料仍可浏览。</span>
        </p>
      )}
    </article>
  );
}

/* ————————————————————————————— 首页 ————————————————————————————— */

export function HomePage() {
  const sources = useSources();
  const canBrowseFiles = useCapability('files.browse');
  const fileRoots = useFileRoots(canBrowseFiles);

  const visible = (sources.data ?? []).filter(
    // presentation 缺席表示尚未绑定规则或绑定冲突；此时按默认呈现显示，而不是把来源藏起来。
    (source) =>
      source.presentation === null || source.presentation === undefined || source.presentation.showInSidebar
  );

  return (
    <div className="gal-page">
      <section className="gal-section">
        <h1 className="gal-page__title">画廊</h1>
        <p className="gal-muted">按平台浏览，或直接进入全部作品与创作者。</p>
        <nav className="gal-quicklinks" aria-label="快速入口">
          <Link className="gal-quicklink" to="/browse">
            全部作品
          </Link>
          <Link className="gal-quicklink" to="/browse?fav=1">
            我的收藏
          </Link>
          <Link className="gal-quicklink" to="/creators">
            创作者
          </Link>
          {canBrowseFiles ? (
            <Link className="gal-quicklink" to="/files">
              文件浏览
            </Link>
          ) : null}
        </nav>
      </section>

      <section className="gal-section">
        <h2 className="gal-section__title">平台</h2>
        {sources.isPending ? (
          <Spinner label="正在加载平台" />
        ) : sources.error !== null ? (
          <ErrorState
            description={describeError(sources.error)}
            code={errorCode(sources.error)}
            correlationId={errorCorrelationId(sources.error)}
            onRetry={() => void sources.refetch()}
          />
        ) : visible.length === 0 ? (
          <EmptyState
            title="还没有可浏览的平台"
            description="登记 Source 并完成一次扫描发布后，平台会出现在这里。"
          />
        ) : (
          <div className="gal-tiles">
            {visible.map((source) => (
              <SourceCard key={source.id} source={source} />
            ))}
          </div>
        )}
      </section>

      {canBrowseFiles && (fileRoots.data ?? []).length > 0 ? (
        <section className="gal-section">
          <h2 className="gal-section__title">文件根</h2>
          <p className="gal-muted">文件根是只读的实时浏览入口：它不产生 Catalog 事实，也没有快照一致性。</p>
          <nav className="gal-quicklinks" aria-label="文件根">
            {(fileRoots.data ?? []).map((root) => (
              <Link key={root.id} className="gal-quicklink" to={`/files/${encodeURIComponent(root.id)}`}>
                {root.name}
              </Link>
            ))}
          </nav>
        </section>
      ) : null}
    </div>
  );
}

/* ————————————————————————————— 全部作品 ————————————————————————————— */

export function BrowsePage() {
  return (
    <div className="gal-page">
      <WorkBrowser heading={<h1 className="gal-page__title">全部作品</h1>} />
    </div>
  );
}

/* ————————————————————————————— 平台页 ————————————————————————————— */

export function SourcePage() {
  const params = useParams();
  const sourceId = params.sourceId ?? '';
  const sources = useSources();
  const source = (sources.data ?? []).find((item) => item.id === sourceId);

  if (sources.isPending) {
    return (
      <div className="gal-page">
        <Spinner label="正在加载平台" />
      </div>
    );
  }
  if (!source) {
    return (
      <div className="gal-page">
        <ErrorState
          title="找不到这个平台"
          description="它可能已被移除，或当前账户没有查看它的权限。"
          code="NOT_FOUND"
        />
      </div>
    );
  }

  return (
    <div className="gal-page">
      <WorkBrowser
        scope={{ sourceId }}
        presentation={source.presentation}
        heading={
          <header className="gal-page__header">
            <h1 className="gal-page__title">
              <PlatformIcon presentation={source.presentation} />
              {presentationName(source)}
            </h1>
            {source.available ? null : <Badge tone="warning">Source 离线</Badge>}
            {source.presentation?.description === undefined ? null : (
              <p className="gal-muted">{source.presentation.description}</p>
            )}
            <nav className="gal-quicklinks" aria-label={`${presentationName(source)}浏览方式`}>
              <Link className="gal-quicklink" to={`/creators?sourceId=${encodeURIComponent(source.id)}`}>
                浏览{source.presentation?.authorLabel ?? '创作者'}
              </Link>
            </nav>
          </header>
        }
      />
    </div>
  );
}

/* ————————————————————————————— 创作者 ————————————————————————————— */

function CreatorTile({ creator, sourceId }: { creator: Creator; sourceId?: string }) {
  const sourceQuery = sourceId === undefined ? '' : `?sourceId=${encodeURIComponent(sourceId)}`;
  return (
    <article className="gal-tile">
      <Link
        className="gal-tile__link"
        to={`/creators/${encodeURIComponent(creator.effectiveId)}${sourceQuery}`}
      >
        <span className="gal-tile__cover">
          {creator.coverMediaId === null || creator.coverMediaId === undefined ? (
            <CoverMissing label={`${creator.name} 暂无封面`} />
          ) : (
            <MediaImage
              src={mediaContentUrl(creator.coverMediaId, {
                queryPublicationId: creator.queryPublicationId ?? undefined
              })}
              alt=""
              allowRetry={false}
            />
          )}
        </span>
        <span className="gal-tile__title">{creator.name}</span>
        <span className="gal-tile__meta gal-muted">来自 {creator.sourceCount} 个来源</span>
      </Link>
      {creator.mergedInto === null || creator.mergedInto === undefined ? null : (
        <p className="gal-tile__meta">
          <Badge tone="accent">已合并</Badge>
        </p>
      )}
    </article>
  );
}

const CREATOR_SORT_LABELS: Record<CreatorSort, string> = {
  name_asc: '名称升序',
  name_desc: '名称降序'
};

function isCreatorSort(value: string | null | undefined): value is CreatorSort {
  return value !== null && value !== undefined && Object.hasOwn(CREATOR_SORT_LABELS, value);
}

export function CreatorsPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const sourceId = searchParams.get('sourceId') ?? undefined;
  const sources = useSources();
  const source = sourceId === undefined ? undefined : sources.data?.find((item) => item.id === sourceId);
  const declaredSorts = source?.presentation?.sort?.authorOptions?.filter(isCreatorSort) ?? [];
  const sortValues: CreatorSort[] =
    declaredSorts.length === 0 ? ['name_asc', 'name_desc'] : [...new Set(declaredSorts)];
  const declaredDefault = source?.presentation?.sort?.authorDefault;
  const fallbackSort: CreatorSort =
    isCreatorSort(declaredDefault) && sortValues.includes(declaredDefault)
      ? declaredDefault
      : (sortValues[0] ?? 'name_asc');
  const requestedSort = searchParams.get('sort');
  const sort: CreatorSort =
    isCreatorSort(requestedSort) && sortValues.includes(requestedSort) ? requestedSort : fallbackSort;
  // Source 下发的 authorDefault 决定首个请求的排序。Source 尚未解析时先暂停 Creator
  // 查询，避免用全局默认发出一条随即废弃的第一页请求。
  const creators = useCreators(sourceId, sort, sourceId === undefined || source !== undefined);
  const items = creators.data?.pages.flatMap((page) => page.creators) ?? [];
  const authorLabel = source?.presentation?.authorLabel ?? '创作者';

  if (sourceId !== undefined && sources.error !== null) {
    return (
      <div className="gal-page">
        <ErrorState
          description={describeError(sources.error)}
          code={errorCode(sources.error)}
          correlationId={errorCorrelationId(sources.error)}
          onRetry={() => void sources.refetch()}
        />
      </div>
    );
  }
  if (sourceId !== undefined && !sources.isPending && source === undefined) {
    return (
      <div className="gal-page">
        <ErrorState
          title="找不到这个平台"
          description="它可能已被移除，或当前账户没有查看它的权限。"
          code="NOT_FOUND"
        />
      </div>
    );
  }

  return (
    <div className="gal-page">
      <header className="gal-page__header">
        <h1 className="gal-page__title">
          {source === undefined ? '创作者' : `${presentationName(source)} · ${authorLabel}`}
        </h1>
        {source === undefined ? null : (
          <p className="gal-muted">
            这里只显示当前平台有 active Binding 的{authorLabel}；代表图也不会借用其它平台的媒体。
          </p>
        )}
      </header>
      <div className="gal-toolbar">
        <Select
          className="gal-toolbar__control"
          label={`${authorLabel}排序`}
          options={sortValues.map((id) => ({ id, label: CREATOR_SORT_LABELS[id] }))}
          selectedKey={sort}
          onSelectionChange={(key) => {
            const next = new URLSearchParams(searchParams);
            if (key === null || key === fallbackSort) next.delete('sort');
            else next.set('sort', key);
            setSearchParams(next);
          }}
        />
        {source === undefined ? null : (
          <Link className="gal-quicklink" to={`/sources/${encodeURIComponent(source.id)}`}>
            返回{presentationName(source)}作品
          </Link>
        )}
      </div>
      {creators.isPending || (sourceId !== undefined && sources.isPending) ? (
        <Spinner label={`正在加载${authorLabel}`} />
      ) : creators.error !== null && items.length === 0 ? (
        <ErrorState
          description={describeError(creators.error)}
          code={errorCode(creators.error)}
          correlationId={errorCorrelationId(creators.error)}
          onRetry={() => void creators.refetch()}
        />
      ) : items.length === 0 ? (
        <EmptyState
          title={`还没有${authorLabel}`}
          description={
            source === undefined
              ? '作品的创作者由规则从来源解析；有些平台的作品本来就没有创作者，这是正常的。'
              : `当前平台还没有可浏览的${authorLabel}，或现有作品没有解析出这项事实。`
          }
        />
      ) : (
        <>
          <div className="gal-tiles">
            {items.map((creator) => (
              <CreatorTile key={creator.id} creator={creator} sourceId={sourceId} />
            ))}
          </div>
          {creators.isFetchNextPageError ? (
            <ErrorState
              title={`更多${authorLabel}加载失败`}
              description={describeError(creators.error)}
              code={errorCode(creators.error)}
              correlationId={errorCorrelationId(creators.error)}
              onRetry={() => void creators.fetchNextPage()}
            />
          ) : creators.hasNextPage ? (
            <div className="gal-browser__more">
              <Button onPress={() => void creators.fetchNextPage()} isDisabled={creators.isFetchingNextPage}>
                {creators.isFetchingNextPage ? `正在加载更多${authorLabel}` : `加载更多${authorLabel}`}
              </Button>
            </div>
          ) : null}
        </>
      )}
    </div>
  );
}

export function CreatorPage() {
  const params = useParams();
  const creatorId = params.creatorId ?? '';
  const [searchParams] = useSearchParams();
  const sourceId = searchParams.get('sourceId') ?? undefined;
  const sources = useSources();
  const source = sourceId === undefined ? undefined : sources.data?.find((item) => item.id === sourceId);
  const creator = useCreator(creatorId);

  if (sourceId !== undefined && sources.error !== null) {
    return (
      <div className="gal-page">
        <ErrorState
          description={describeError(sources.error)}
          code={errorCode(sources.error)}
          correlationId={errorCorrelationId(sources.error)}
          onRetry={() => void sources.refetch()}
        />
      </div>
    );
  }
  if (sourceId !== undefined && sources.isPending) {
    return (
      <div className="gal-page">
        <Spinner label="正在加载平台" />
      </div>
    );
  }
  if (sourceId !== undefined && source === undefined) {
    return (
      <div className="gal-page">
        <ErrorState
          title="找不到这个平台"
          description="它可能已被移除，或当前账户没有查看它的权限。"
          code="NOT_FOUND"
        />
      </div>
    );
  }

  const authorLabel = source?.presentation?.authorLabel ?? '创作者';

  return (
    <div className="gal-page">
      <WorkBrowser
        scope={{ creatorId, ...(sourceId === undefined ? {} : { sourceId }) }}
        presentation={source?.presentation}
        heading={
          <header className="gal-page__header">
            <h1 className="gal-page__title">{creator.data?.name ?? '创作者'}</h1>
            {creator.data === undefined ? null : (
              <p className="gal-muted">
                {source === undefined
                  ? `来自 ${creator.data.sourceCount} 个来源的作品`
                  : `${presentationName(source)}中的${authorLabel}作品`}
              </p>
            )}
            {source === undefined ? null : (
              <Link className="gal-link" to={`/creators?sourceId=${encodeURIComponent(source.id)}`}>
                返回{presentationName(source)}的{authorLabel}清单
              </Link>
            )}
          </header>
        }
        emptyDescription={`这位${authorLabel}在当前范围内没有可见的作品。`}
      />
    </div>
  );
}
