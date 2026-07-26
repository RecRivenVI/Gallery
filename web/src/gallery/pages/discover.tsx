/*
 * 发现路径：首页 → 平台 / 创作者 → 作品列表。
 *
 * **平台的展示形态完全由服务端下发的 `presentation` 决定**：显示名、字形图标、配色、
 * 对"作者"的称谓、时间显示时区。前端不认识任何平台名，也不按平台名分支——规则是 Source
 * 差异的唯一解释入口，在界面里硬编码平台特例会绕开它。
 */

import { Link, useParams } from 'react-router-dom';
import { Badge, EmptyState, ErrorState, Spinner } from '../../design';
import { describeError, errorCode, errorCorrelationId } from '../../shared/errors';
import { useCapability } from '../../shared/session';
import { mediaContentUrl, type Creator, type Source, type SourcePresentation } from '../contracts';
import { useCreator, useCreators, useFileRoots, useSources } from '../queries';
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
            <MediaImage src={mediaContentUrl(source.coverMediaId)} alt="" allowRetry={false} />
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
          </header>
        }
      />
    </div>
  );
}

/* ————————————————————————————— 创作者 ————————————————————————————— */

function CreatorTile({ creator }: { creator: Creator }) {
  return (
    <article className="gal-tile">
      <Link className="gal-tile__link" to={`/creators/${encodeURIComponent(creator.effectiveId)}`}>
        <span className="gal-tile__cover">
          {creator.coverMediaId === null || creator.coverMediaId === undefined ? (
            <CoverMissing label={`${creator.name} 暂无封面`} />
          ) : (
            <MediaImage src={mediaContentUrl(creator.coverMediaId)} alt="" allowRetry={false} />
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

export function CreatorsPage() {
  const creators = useCreators();
  // 服务端已排好序，这里只做展示过滤：被合并掉的身份不单独出现，避免同一个人出现两次。
  const items = (creators.data ?? []).filter(
    (creator) => creator.mergedInto === null || creator.mergedInto === undefined
  );

  return (
    <div className="gal-page">
      <h1 className="gal-page__title">创作者</h1>
      {creators.isPending ? (
        <Spinner label="正在加载创作者" />
      ) : creators.error !== null ? (
        <ErrorState
          description={describeError(creators.error)}
          code={errorCode(creators.error)}
          correlationId={errorCorrelationId(creators.error)}
          onRetry={() => void creators.refetch()}
        />
      ) : items.length === 0 ? (
        <EmptyState
          title="还没有创作者"
          description="作品的创作者由规则从来源解析；有些平台的作品本来就没有创作者，这是正常的。"
        />
      ) : (
        <div className="gal-tiles">
          {items.map((creator) => (
            <CreatorTile key={creator.id} creator={creator} />
          ))}
        </div>
      )}
    </div>
  );
}

export function CreatorPage() {
  const params = useParams();
  const creatorId = params.creatorId ?? '';
  const creator = useCreator(creatorId);

  return (
    <div className="gal-page">
      <WorkBrowser
        scope={{ creatorId }}
        heading={
          <header className="gal-page__header">
            <h1 className="gal-page__title">{creator.data?.name ?? '创作者'}</h1>
            {creator.data === undefined ? null : (
              <p className="gal-muted">来自 {creator.data.sourceCount} 个来源的作品</p>
            )}
          </header>
        }
        emptyDescription="这位创作者名下没有可见的作品。"
      />
    </div>
  );
}
