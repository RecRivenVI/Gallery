/*
 * 画廊外壳：顶栏、连接状态条、滚动位置恢复、未认证时的入口。
 *
 * 设计语言要求画廊"低 chrome"：导航与状态条合计不超过视口高度的 15%，滚动时非必要 chrome
 * 收起。因此顶栏在向下滚动时隐藏、向上滚动或有焦点进入时立刻回来——键盘用户永远不会被
 * 一个"藏起来的导航"困住。
 *
 * 连接状态条只表达"多快知道变化"，**不**把整页切成错误态：WebSocket 断开不会让已经加载
 * 的快照失效，把它渲染成页面级错误是在说谎。
 */

import { useCallback, useEffect, useRef, useState } from 'react';
import { Link, NavLink, NavigationType, useLocation, useNavigate, useNavigationType } from 'react-router-dom';
import { Button, Dialog, Menu, Spinner, TextInput } from '../../design';
import { describeError } from '../../shared/errors';
import { useRealtime } from '../../shared/realtime';
import { useAuthActions, useSession } from '../../shared/session';
import { DENSITY_LABELS, THEME_LABELS, useTheme, type ThemePreference } from '../../shared/theme';

/* ————————————————————————————— 顶栏 ————————————————————————————— */

const HIDE_AFTER_PX = 240;

const GALLERY_NAV_ITEMS = [
  { to: '/browse', label: '全部作品' },
  { to: '/creators', label: '创作者' },
  { to: '/files', label: '文件' }
] as const;

export function TopBar() {
  const [hidden, setHidden] = useState(false);
  const lastY = useRef(0);
  const { theme, setTheme, density, setDensity } = useTheme();

  useEffect(() => {
    const onScroll = () => {
      const y = window.scrollY;
      setHidden(y > HIDE_AFTER_PX && y > lastY.current);
      lastY.current = y;
    };
    window.addEventListener('scroll', onScroll, { passive: true });
    return () => {
      window.removeEventListener('scroll', onScroll);
    };
  }, []);

  const themeItems = (Object.keys(THEME_LABELS) as ThemePreference[]).map((key) => ({
    id: `theme:${key}`,
    label: `${THEME_LABELS[key]}${theme === key ? ' ✓' : ''}`
  }));

  return (
    // focus-within 让顶栏在键盘进入时立刻回到可见位置，收起只是视觉上的让位。
    <header className={hidden ? 'gal-topbar gal-topbar--hidden' : 'gal-topbar'}>
      <Link className="gal-topbar__brand" to="/">
        画廊
      </Link>
      <nav className="gal-topbar__nav" aria-label="画廊导航">
        {GALLERY_NAV_ITEMS.map((item) => (
          <NavLink className="gal-topbar__link" to={item.to} key={item.to}>
            {item.label}
          </NavLink>
        ))}
      </nav>
      <div className="gal-topbar__actions">
        <Dialog
          title="画廊导航"
          size="sm"
          trigger={
            <Button className="gal-topbar__nav-trigger" variant="ghost">
              导航
            </Button>
          }
        >
          {(close) => (
            <nav className="gal-nav-dialog" aria-label="画廊页面">
              <NavLink className="gal-nav-dialog__link" to="/" end onClick={close}>
                首页
              </NavLink>
              {GALLERY_NAV_ITEMS.map((item) => (
                <NavLink className="gal-nav-dialog__link" to={item.to} onClick={close} key={item.to}>
                  {item.label}
                </NavLink>
              ))}
            </nav>
          )}
        </Dialog>
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
      </div>
    </header>
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

  useEffect(() => {
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
    // 等一帧：列表要先从缓存渲染出来，页面才有足够高度可以滚回去。
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
          <Button variant="primary" isPending={actions.isPending} onPress={() => void actions.pairPersonal()}>
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
