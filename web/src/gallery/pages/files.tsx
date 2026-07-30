/*
 * 文件根浏览。
 *
 * 它与 Catalog 浏览是**两套语义**，界面必须说清楚：
 *
 * - 文件系统是实时的，没有 publication 快照。续页只保证"从锚点之后继续"，
 *   **不保证可重复读**：两次请求之间目录发生变化时可能漏项或重复。
 * - 因此这里不显示任何总数，也不把多页结果当成一致快照。
 * - `link` 是独立于 file/directory 的第三态：可见但不可进入，大小无意义。
 *   把它并入 file 会显示成一个 0 字节的普通文件，那是错误信息。
 */

import { Link, useParams, useSearchParams } from 'react-router';
import { Badge, Button, EmptyState, ErrorState, Spinner } from '../../design';
import { describeError, errorCode, errorCorrelationId } from '../../shared/errors';
import { useCapability } from '../../shared/session';
import type { FileRootEntry } from '../contracts';
import { useFileEntries, useFileRoots } from '../queries';

function formatBytes(size: number | null | undefined): string {
  if (size === null || size === undefined) return '—';
  if (size < 1024) return `${size} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let value = size / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value.toFixed(1)} ${units[unit] ?? 'TB'}`;
}

function kindBadge(kind: FileRootEntry['kind']) {
  switch (kind) {
    case 'directory':
      return <Badge tone="accent">目录</Badge>;
    case 'link':
      return <Badge tone="warning">链接</Badge>;
    case 'file':
      return <Badge tone="neutral">文件</Badge>;
  }
}

export function FileRootsPage() {
  const canBrowse = useCapability('files.browse');
  const roots = useFileRoots(canBrowse);

  if (!canBrowse) {
    return (
      <div className="gal-page">
        <EmptyState title="没有文件浏览权限" description="当前账户不能浏览文件根。" />
      </div>
    );
  }

  return (
    <div className="gal-page">
      <h1 className="gal-page__title">文件</h1>
      <p className="gal-muted">
        文件根是只读的实时浏览入口：不产生 Catalog 事实、不绑定规则、不被扫描， 也没有快照一致性保证。
      </p>
      {roots.isPending ? (
        <Spinner label="正在加载文件根" />
      ) : roots.error !== null ? (
        <ErrorState
          description={describeError(roots.error)}
          code={errorCode(roots.error)}
          correlationId={errorCorrelationId(roots.error)}
          onRetry={() => void roots.refetch()}
        />
      ) : roots.data.length === 0 ? (
        <EmptyState title="没有配置文件根" description="文件根由服务端配置提供。" />
      ) : (
        <ul className="gal-list">
          {roots.data.map((root) => (
            <li key={root.id}>
              <Link className="gal-link" to={`/files/${encodeURIComponent(root.id)}`}>
                {root.name}
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** 由相对路径生成面包屑。路径一律用斜杠分隔，且不含任何绝对路径信息。 */
function breadcrumbs(path: string): { label: string; path: string }[] {
  if (path === '') return [];
  const parts = path.split('/').filter((part) => part !== '');
  const trail: { label: string; path: string }[] = [];
  let current = '';
  for (const part of parts) {
    current = current === '' ? part : `${current}/${part}`;
    trail.push({ label: part, path: current });
  }
  return trail;
}

export function FileBrowserPage() {
  const params = useParams();
  const rootId = params.rootId ?? '';
  const [searchParams] = useSearchParams();
  const path = searchParams.get('path') ?? '';
  const entries = useFileEntries(rootId, path);
  const pages = entries.data?.pages ?? [];
  const items = pages.flatMap((page) => page.entries);

  const linkTo = (target: string) =>
    target === ''
      ? `/files/${encodeURIComponent(rootId)}`
      : `/files/${encodeURIComponent(rootId)}?path=${encodeURIComponent(target)}`;

  return (
    <div className="gal-page">
      <nav className="gal-breadcrumbs" aria-label="路径">
        <Link className="gal-link" to="/files">
          文件根
        </Link>
        <Link className="gal-link" to={linkTo('')}>
          {rootId}
        </Link>
        {breadcrumbs(path).map((crumb) => (
          <Link key={crumb.path} className="gal-link" to={linkTo(crumb.path)}>
            {crumb.label}
          </Link>
        ))}
      </nav>

      <p className="gal-muted">
        实时目录内容：分页只保证从锚点之后继续，不保证可重复读，因此这里不显示条目总数。
      </p>

      {entries.isPending ? (
        <Spinner label="正在读取目录" />
      ) : entries.error !== null ? (
        <ErrorState
          description={describeError(entries.error)}
          code={errorCode(entries.error)}
          correlationId={errorCorrelationId(entries.error)}
          onRetry={() => void entries.refetch()}
        />
      ) : items.length === 0 ? (
        <EmptyState title="这个目录是空的" description="它当前没有可见的条目。" />
      ) : (
        <ul className="gal-list gal-list--entries">
          {items.map((entry) => (
            <li key={entry.relativePath} className="gal-entry">
              {kindBadge(entry.kind)}
              {entry.kind === 'directory' ? (
                <Link className="gal-link" to={linkTo(entry.relativePath)}>
                  {entry.name}
                </Link>
              ) : (
                <span>{entry.name}</span>
              )}
              <span className="gal-muted">{formatBytes(entry.sizeBytes)}</span>
              {entry.kind === 'link' ? <span className="gal-muted">链接不可进入</span> : null}
            </li>
          ))}
        </ul>
      )}

      {entries.hasNextPage ? (
        <Button
          variant="secondary"
          isPending={entries.isFetchingNextPage}
          onPress={() => void entries.fetchNextPage()}
        >
          继续加载
        </Button>
      ) : null}
    </div>
  );
}
