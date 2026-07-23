import { Button, Select, SelectValue, ListBox, ListBoxItem, Popover } from 'react-aria-components';
import { NavLink, Outlet } from 'react-router-dom';
import { useSession } from '../auth/session';
import type { Capability } from '../auth/capabilities';
import { useRealtime } from '../realtime/realtime';
import { usePreferences, type Theme } from '../state/preferences';
import { StatusBadge } from './ui';

const nav: { to: string; label: string; capability: Capability | Capability[] }[] = [
  { to: '/browse', label: '浏览', capability: 'library.read' },
  { to: '/creators', label: '创作者', capability: 'library.read' },
  { to: '/jobs', label: '任务', capability: 'library.read' },
  { to: '/libraries', label: '资料库与 Source', capability: 'library.read' },
  { to: '/rules', label: '规则', capability: 'rules.read' },
  { to: '/governance', label: '人工治理', capability: 'bindings.read' },
  {
    to: '/security',
    label: '账户与安全',
    capability: ['clients.manage', 'tokens.manage', 'shares.create', 'users.manage']
  },
  { to: '/maintenance', label: '备份与维护', capability: 'admin.backup' }
];

export function AppShell() {
  const { bootstrap, can, logout } = useSession();
  const realtime = useRealtime();
  const preferences = usePreferences();
  return (
    <div className={`app-shell ${preferences.sidebarOpen ? '' : 'sidebar-collapsed'}`}>
      <a className="skip-link" href="#main-content">
        跳到主要内容
      </a>
      <header className="topbar">
        <Button
          className="icon-button"
          aria-label="切换导航"
          onPress={() => preferences.setSidebarOpen(!preferences.sidebarOpen)}
        >
          ☰
        </Button>
        <NavLink to="/browse" className="brand">
          <span className="brand-mark" aria-hidden="true">
            G
          </span>
          <span>
            Gallery <small>画廊</small>
          </span>
        </NavLink>
        <div className="topbar-status">
          <StatusBadge tone={realtime.state === 'ready' ? 'success' : 'warning'}>
            实时：{realtime.state}
          </StatusBadge>
          <StatusBadge tone="info">{bootstrap.mode === 'personal' ? 'Personal' : 'LAN'}</StatusBadge>
          <Select
            selectedKey={preferences.theme}
            onSelectionChange={(key) => preferences.setTheme(key as Theme)}
            aria-label="主题"
          >
            <Button className="select-button">
              <SelectValue />
            </Button>
            <Popover>
              <ListBox>
                {['system', 'light', 'dark'].map((value) => (
                  <ListBoxItem id={value} key={value}>
                    {value === 'system' ? '跟随系统' : value === 'light' ? '浅色' : '深色'}
                  </ListBoxItem>
                ))}
              </ListBox>
            </Popover>
          </Select>
          <Button className="button ghost" onPress={() => void logout()}>
            退出
          </Button>
        </div>
      </header>
      <aside className="sidebar" aria-label="主导航">
        <nav>
          {nav
            .filter((item) =>
              Array.isArray(item.capability)
                ? item.capability.some((capability) => can(capability))
                : can(item.capability)
            )
            .map((item) => (
              <NavLink key={item.to} to={item.to}>
                {item.label}
              </NavLink>
            ))}
        </nav>
        <p className="sidebar-note">
          Source 永久只读
          <br />
          API {bootstrap.apiVersion}
        </p>
      </aside>
      <main id="main-content" className="main-content" tabIndex={-1}>
        <Outlet />
      </main>
      <div className="live-region" aria-live="polite" aria-atomic="true">
        {realtime.state === 'reconnecting' ? '实时连接中断，正在通过 HTTP 恢复状态' : ''}
      </div>
    </div>
  );
}
