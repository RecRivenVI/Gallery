/*
 * 画廊外壳：顶栏、连接状态条、滚动位置恢复、未认证时的入口。
 *
 * 桌面端使用常驻侧栏承载全局搜索、平台与文件根；窄屏收敛为模态抽屉。两种形态共享同一份
 * 动态导航数据与可访问名称，不允许出现桌面能进入、移动端却缺失的页面。
 *
 * 连接状态条只表达"多快知道变化"，**不**把整页切成错误态：WebSocket 断开不会让已经加载
 * 的快照失效，把它渲染成页面级错误是在说谎。
 */

import { useCallback, useEffect, useLayoutEffect, useState, type FormEvent, type ReactNode } from 'react';
import { Link, NavLink, NavigationType, useLocation, useNavigate, useNavigationType } from 'react-router';
import { Button, Dialog, Icon, Menu, Spinner, TextInput, type IconName } from '../../design';
import { describeError } from '../../shared/errors';
import { useRealtime } from '../../shared/realtime';
import { SignOutButton, useAuthActions, useCapability, useSession } from '../../shared/session';
import { DENSITY_LABELS, THEME_LABELS, useTheme, type ThemePreference } from '../../shared/theme';
import type { Source } from '../contracts';
import { useFileRoots, useSources } from '../queries';

/* ————————————————————————————— 应用导航 ————————————————————————————— */

const GALLERY_NAV_ITEMS = [
  { to: '/', label: '首页', icon: 'home', end: true },
  { to: '/browse', label: '全部作品', icon: 'works' },
  { to: '/browse?fav=1', label: '收藏', icon: 'favorite' },
  { to: '/creators', label: '创作者', icon: 'creators' },
  { to: '/files', label: '文件', icon: 'files' }
] as const;

function sourceName(source: Source): string {
  const name = source.presentation?.name;
  return name === undefined || name === '' ? source.displayName : name;
}

function NavIcon({ name }: { name: IconName }) {
  return <Icon className="gal-nav-icon" name={name} />;
}

function GallerySearch({ compact, onNavigate }: { compact?: boolean; onNavigate?: () => void }) {
  const location = useLocation();
  const navigate = useNavigate();
  const current =
    location.pathname === '/browse' ? (new URLSearchParams(location.search).get('q') ?? '') : '';
  const [query, setQuery] = useState(current);

  useEffect(() => {
    setQuery(current);
  }, [current]);

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const value = query.trim();
    void navigate(value === '' ? '/browse' : `/browse?q=${encodeURIComponent(value)}`);
    onNavigate?.();
  };

  return (
    <form
      className={compact ? 'gal-shell-search gal-shell-search--compact' : 'gal-shell-search'}
      role="search"
      onSubmit={submit}
    >
      <label className="ui-visually-hidden" htmlFor={compact ? 'mobile-gallery-search' : 'gallery-search'}>
        全局作品搜索
      </label>
      <Icon className="gal-shell-search__icon" name="search" />
      <input
        className="gal-shell-search__input"
        id={compact ? 'mobile-gallery-search' : 'gallery-search'}
        type="search"
        value={query}
        onChange={(event) => setQuery(event.target.value)}
        placeholder="搜索作品"
      />
    </form>
  );
}

function ShellNavLink({
  to,
  label,
  icon,
  end,
  onNavigate
}: {
  to: string;
  label: string;
  icon?: IconName;
  end?: boolean;
  onNavigate?: () => void;
}) {
  const location = useLocation();
  const [path, search = ''] = to.split('?');
  const currentSearch = new URLSearchParams(location.search);
  const requestedSearch = new URLSearchParams(search);
  const favoriteLink = requestedSearch.get('fav') === '1';
  const active =
    path === '/'
      ? location.pathname === '/'
      : path === '/browse'
        ? location.pathname === '/browse' &&
          (favoriteLink ? currentSearch.get('fav') === '1' : currentSearch.get('fav') !== '1')
        : location.pathname === path || (!end && location.pathname.startsWith(`${path}/`));
  return (
    <Link
      className={active ? 'gal-sidebar__link is-current' : 'gal-sidebar__link'}
      to={to}
      aria-current={active ? 'page' : undefined}
      onClick={onNavigate}
    >
      {icon === undefined ? null : <NavIcon name={icon} />}
      <span>{label}</span>
    </Link>
  );
}

function DynamicNavigation({ onNavigate }: { onNavigate?: () => void }) {
  const sources = useSources();
  const canBrowseFiles = useCapability('files.browse');
  const fileRoots = useFileRoots(canBrowseFiles);
  const visibleSources = (sources.data ?? []).filter(
    (source) =>
      source.presentation === null || source.presentation === undefined || source.presentation.showInSidebar
  );

  return (
    <>
      <div className="gal-sidebar__group">
        <p className="gal-sidebar__label">浏览</p>
        {GALLERY_NAV_ITEMS.map((item) => (
          <ShellNavLink {...item} onNavigate={onNavigate} key={item.to} />
        ))}
      </div>
      {visibleSources.length === 0 ? null : (
        <div className="gal-sidebar__group">
          <p className="gal-sidebar__label">平台与来源</p>
          {visibleSources.map((source) => (
            <NavLink
              className="gal-sidebar__link"
              to={`/sources/${encodeURIComponent(source.id)}`}
              onClick={onNavigate}
              key={source.id}
            >
              {source.presentation?.icon === undefined ? (
                <span className="gal-sidebar__source-dot" aria-hidden="true" />
              ) : (
                <span
                  className="gal-sidebar__source-glyph"
                  style={{
                    color: source.presentation.icon.color,
                    background: source.presentation.icon.background,
                    borderColor: source.presentation.icon.border
                  }}
                  aria-hidden="true"
                >
                  {source.presentation.icon.glyph}
                </span>
              )}
              <span>{sourceName(source)}</span>
              {source.available ? null : <span className="gal-sidebar__state" aria-label="离线" />}
            </NavLink>
          ))}
        </div>
      )}
      {!canBrowseFiles || (fileRoots.data ?? []).length === 0 ? null : (
        <div className="gal-sidebar__group">
          <p className="gal-sidebar__label">文件根</p>
          {(fileRoots.data ?? []).map((root) => (
            <ShellNavLink
              to={`/files/${encodeURIComponent(root.id)}`}
              label={root.name}
              icon="files"
              onNavigate={onNavigate}
              key={root.id}
            />
          ))}
        </div>
      )}
    </>
  );
}

function AppearanceMenu() {
  const { theme, setTheme, density, setDensity } = useTheme();
  const themeItems = (Object.keys(THEME_LABELS) as ThemePreference[]).map((key) => ({
    id: `theme:${key}`,
    label: `${THEME_LABELS[key]}${theme === key ? ' ✓' : ''}`
  }));

  return (
    <Menu
      label="外观"
      buttonVariant="ghost"
      items={[
        ...themeItems,
        {
          id: 'density',
          label: `密度：${DENSITY_LABELS[density]}`
        }
      ]}
      onAction={(id) => {
        if (id === 'density') {
          setDensity(density === 'comfortable' ? 'compact' : 'comfortable');
          return;
        }
        const value = id.slice('theme:'.length);
        if (value in THEME_LABELS) setTheme(value as ThemePreference);
      }}
    />
  );
}

function MobileNavigation({ trigger }: { trigger: ReactNode }) {
  return (
    <Dialog title="画廊导航" size="sm" trigger={trigger}>
      {(close) => (
        <div className="gal-nav-dialog">
          <GallerySearch compact onNavigate={close} />
          <nav className="gal-nav-dialog__nav" aria-label="画廊页面">
            <DynamicNavigation onNavigate={close} />
          </nav>
        </div>
      )}
    </Dialog>
  );
}

export function TopBar() {
  return (
    <>
      <aside className="gal-sidebar">
        <div className="gal-sidebar__header">
          <Link className="gal-sidebar__brand" to="/">
            <span className="gal-sidebar__brand-mark" aria-hidden="true">
              G
            </span>
            <span>
              <strong>画廊</strong>
              <small>Gallery</small>
            </span>
          </Link>
          <GallerySearch />
        </div>
        <nav className="gal-sidebar__nav" aria-label="画廊导航">
          <DynamicNavigation />
        </nav>
        <div className="gal-sidebar__footer">
          <AppearanceMenu />
          <SignOutButton />
          <a className="gal-sidebar__manage" href="/manage">
            管理端
            <Icon name="external" />
          </a>
        </div>
      </aside>
      <header className="gal-mobile-header">
        <Link className="gal-mobile-header__brand" to="/">
          画廊
        </Link>
        <div className="gal-mobile-header__actions">
          <AppearanceMenu />
          <MobileNavigation
            trigger={
              <Button className="gal-topbar__nav-trigger" variant="secondary">
                <Icon name="menu" />
                导航
              </Button>
            }
          />
        </div>
      </header>
    </>
  );
}

/* ————————————————————————————— 连接状态 ————————————————————————————— */

export function ConnectionStatus() {
  const realtime = useRealtime();
  const [online, setOnline] = useState(() => (typeof navigator === 'undefined' ? true : navigator.onLine));

  useEffect(() => {
    const update = () => {
      setOnline(navigator.onLine);
    };
    window.addEventListener('online', update);
    window.addEventListener('offline', update);
    return () => {
      window.removeEventListener('online', update);
      window.removeEventListener('offline', update);
    };
  }, []);

  if (!online) {
    return (
      <p className="gal-connection gal-connection--warn" role="status">
        设备当前离线。已经加载的内容仍可浏览，新的请求会失败。
      </p>
    );
  }
  if (realtime.status === 'reconnecting') {
    return (
      <p className="gal-connection" role="status">
        实时通道断开，正在重连；页面已加载的内容仍然有效，只是变化会晚一点知道。
      </p>
    );
  }
  if (realtime.status === 'closed') {
    return (
      <p className="gal-connection gal-connection--warn" role="status">
        {closedText(realtime.closedReason)}
      </p>
    );
  }
  return null;
}

function closedText(reason: string | undefined): string {
  switch (reason) {
    case 'session-revoked':
      return '会话已被吊销，请重新认证。';
    case 'grant-revoked':
      return '授权已被吊销，可见范围可能已经变化，请刷新页面。';
    case 'protocol-mismatch':
      return '实时协议版本与服务端不一致，请刷新页面加载新版本前端。';
    default:
      return '实时通道已停止重连。刷新页面可以重新建立连接；已加载的内容仍然有效。';
  }
}

/* ————————————————————————————— 滚动位置恢复 ————————————————————————————— */

const SCROLL_KEY_PREFIX = 'gallery.scroll.';

/**
 * 记住并恢复每个历史条目的滚动位置。
 *
 * 只在 POP（浏览器前进/后退）时恢复：PUSH 是一次新的导航，用户期待从顶部开始。
 * 列表内容本身由 TanStack 缓存保留，因此返回时"页数 + 位置"一起回来，
 * 不会出现"回到第一页然后停在第三屏"的错位。
 */
export function ScrollRestoration() {
  const location = useLocation();
  const navigationType = useNavigationType();
  const key = location.key;

  useEffect(() => {
    if (!('scrollRestoration' in window.history)) return;
    const previous = window.history.scrollRestoration;
    window.history.scrollRestoration = 'manual';
    return () => {
      window.history.scrollRestoration = previous;
    };
  }, []);

  useLayoutEffect(() => {
    const save = () => {
      try {
        sessionStorage.setItem(`${SCROLL_KEY_PREFIX}${key}`, String(window.scrollY));
      } catch {
        // 隐私模式下写不进去只影响体验，不影响正确性。
      }
    };
    window.addEventListener('scroll', save, { passive: true });
    return () => {
      save();
      window.removeEventListener('scroll', save);
    };
  }, [key]);

  useLayoutEffect(() => {
    if (navigationType !== NavigationType.Pop) {
      window.scrollTo(0, 0);
      return;
    }
    let raw: string | null;
    try {
      raw = sessionStorage.getItem(`${SCROLL_KEY_PREFIX}${key}`);
    } catch {
      raw = null;
    }
    if (raw === null) return;
    const y = Number(raw);
    if (Number.isNaN(y)) return;
    // 布局清理会在路由 DOM 收缩前保存旧位置；新路由提交后则先用缓存高度同步恢复，
    // 再等一帧复核一次，让窗口块的 IntersectionObserver 有机会物化目标区域。
    window.scrollTo(0, y);
    const frame = requestAnimationFrame(() => {
      window.scrollTo(0, y);
    });
    return () => {
      cancelAnimationFrame(frame);
    };
  }, [key, navigationType]);

  return null;
}

/* ————————————————————————————— 未认证入口 ————————————————————————————— */

/**
 * 未认证时的登录面板。
 *
 * Personal 模式是一次性配对（仅 loopback 可用），LAN 模式是本地账户登录。两者的会话都由
 * 服务端下发 HttpOnly Cookie，前端读不到，因此认证成功后一律重新拉 bootstrap——
 * 这件事由 shared/session 的动作负责，这里不重复处理。
 */
export function SignInPanel() {
  const { mode, bootstrap } = useSession();
  const actions = useAuthActions();
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');

  return (
    <section className="gal-signin">
      <h1 className="gal-signin__title">画廊</h1>
      {mode === 'personal' ? (
        <>
          <p>本机个人模式：完成一次性配对后即可浏览。</p>
          {/* 配对成功会立即换壳；显式 status 在下方播报，不让按钮使用会引用即将卸载节点的
              RAC pending live-announcer。 */}
          <Button
            variant="primary"
            isDisabled={actions.isPending}
            onPress={() => void actions.pairPersonal()}
          >
            配对本机浏览器
          </Button>
        </>
      ) : (
        <form
          className="gal-signin__form"
          onSubmit={(event) => {
            event.preventDefault();
            void actions.login({ username, password });
          }}
        >
          {bootstrap.lanInitialized ? null : <p>此 Gallery 尚未初始化 Owner，请先在管理端完成初始化。</p>}
          <TextInput label="用户名" value={username} onChange={setUsername} autoComplete="username" />
          <TextInput
            label="密码"
            type="password"
            value={password}
            onChange={setPassword}
            autoComplete="current-password"
          />
          <Button type="submit" variant="primary" isPending={actions.isPending}>
            登录
          </Button>
        </form>
      )}
      {actions.isPending ? <Spinner label="正在认证" /> : null}
      {actions.error === null || actions.error === undefined ? null : (
        <p className="gal-signin__error" role="alert">
          {describeError(actions.error)}
        </p>
      )}
    </section>
  );
}

/** 返回上一页。全屏查看用它把 Esc 变成"关闭"，而不是另建一套模态历史。 */
export function useGoBack(): () => void {
  const navigate = useNavigate();
  return useCallback(() => {
    void navigate(-1);
  }, [navigate]);
}
