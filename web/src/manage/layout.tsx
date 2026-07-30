/*
 * 管理端外壳：常驻诊断信息、导航、实时通道状态与快照刷新。
 *
 * 管理端的第一原则是「状态先于名称」。因此顶部常驻的不是品牌，而是：当前部署模式、主体、
 * 三个协议版本、实时通道状态，以及一个显式的「重新拉取快照」入口——因为实时事件只是提示，
 * 真正的事实来自 HTTP 快照。
 */

import { useCallback, useEffect } from 'react';
import type { ReactNode } from 'react';
import { NavLink } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import { Badge, Button, Dialog, Icon, Menu, type IconName } from '../design';
import { SNAPSHOT_QUERY_PREFIXES } from '../shared/query';
import { useRealtime, useRealtimeEvent, type RealtimeStatus } from '../shared/realtime';
import { SignOutButton, useSession } from '../shared/session';
import { DENSITY_LABELS, THEME_LABELS, useTheme, type ThemePreference } from '../shared/theme';
import './manage.css';

export interface NavItem {
  to: string;
  label: string;
  icon: IconName;
}

/** 导航顺序即产品路径：先看现状，再动手，再验证，再管连接，再改规则，最后收拾治理欠账。 */
export const MANAGE_NAV: readonly NavItem[] = [
  { to: '/', label: '概览', icon: 'home' },
  { to: '/scans', label: '扫描与任务', icon: 'scan' },
  { to: '/diagnostics', label: '验证和诊断', icon: 'diagnostics' },
  { to: '/security', label: '连接与安全', icon: 'security' },
  { to: '/rules', label: '规则', icon: 'rules' },
  { to: '/governance', label: '治理', icon: 'governance' }
];

function ManageNavList({ dialog, onNavigate }: { dialog?: boolean; onNavigate?: () => void }) {
  return (
    <ul className={dialog ? 'manage-nav-dialog__list' : 'manage-nav__list'}>
      {MANAGE_NAV.map((item) => (
        <li key={item.to}>
          <NavLink
            className={dialog ? 'manage-nav-dialog__link' : 'manage-nav__link'}
            to={item.to}
            end={item.to === '/'}
            onClick={onNavigate}
          >
            <Icon className="manage-nav__icon" name={item.icon} />
            {item.label}
          </NavLink>
        </li>
      ))}
    </ul>
  );
}

const REALTIME_LABELS: Record<RealtimeStatus, string> = {
  idle: '未连接',
  connecting: '连接中',
  open: '已连接',
  reconnecting: '重连中',
  closed: '已断开'
};

function realtimeTone(status: RealtimeStatus): 'success' | 'warning' | 'danger' | 'neutral' {
  if (status === 'open') return 'success';
  if (status === 'closed') return 'danger';
  if (status === 'reconnecting' || status === 'connecting') return 'warning';
  return 'neutral';
}

/**
 * 安全资源的快照失效。
 *
 * `shared/query.ts` 的 SNAPSHOT_QUERY_PREFIXES 里没有 `security`（会话、Token、分享、账户、
 * 授权、审计），因此实时层的自动失效覆盖不到它们。这里显式补上：会话或授权被吊销时，
 * 以及任何一次「必须重新拉快照」时，都失效 `['security']`。
 */
function useSecuritySnapshotSync(): void {
  const queryClient = useQueryClient();
  const { snapshotEpoch } = useRealtime();
  const invalidate = useCallback(() => {
    void queryClient.invalidateQueries({ queryKey: ['security'] });
  }, [queryClient]);

  useRealtimeEvent('session.revoked', invalidate);
  useRealtimeEvent('grant.revoked', invalidate);
  useEffect(invalidate, [snapshotEpoch, invalidate]);
}

export function ManageLayout({ children }: { children: ReactNode }) {
  const { bootstrap, mode } = useSession();
  const { status, closedReason, lastSequence } = useRealtime();
  const { theme, setTheme, density, setDensity } = useTheme();
  const queryClient = useQueryClient();
  useSecuritySnapshotSync();

  const themeItems = (Object.keys(THEME_LABELS) as ThemePreference[]).map((value) => ({
    id: `theme:${value}`,
    label: `${THEME_LABELS[value]}${theme === value ? ' ✓' : ''}`
  }));

  const refreshSnapshots = useCallback(() => {
    for (const prefix of SNAPSHOT_QUERY_PREFIXES) {
      void queryClient.invalidateQueries({ queryKey: [prefix] });
    }
    void queryClient.invalidateQueries({ queryKey: ['security'] });
  }, [queryClient]);

  return (
    <div className="manage-shell">
      <aside className="manage-sidebar">
        <div className="manage-sidebar__brand">
          <h1 className="manage-header__title">Gallery 管理</h1>
          <p>控制与治理工作区</p>
        </div>
        <nav className="manage-nav" aria-label="管理功能">
          <ManageNavList />
        </nav>
        <div className="manage-sidebar__footer">
          <span className="manage-status-bar">
            <Badge tone={realtimeTone(status)}>实时通道：{REALTIME_LABELS[status]}</Badge>
            <span>序号 {lastSequence}</span>
            {closedReason === undefined ? null : <span>停止原因：{closedReasonLabel(closedReason)}</span>}
          </span>
          <a className="manage-sidebar__gallery-link" href="/">
            打开用户前端
            <Icon name="external" />
          </a>
        </div>
      </aside>

      <div className="manage-workspace">
        <header className="manage-header">
          <div className="manage-header__mobile-brand">Gallery 管理</div>
          <Dialog
            title="管理导航"
            size="sm"
            trigger={
              <Button className="manage-nav__trigger" variant="secondary">
                导航
              </Button>
            }
          >
            {(close) => (
              <nav aria-label="管理页面">
                <ManageNavList dialog onNavigate={close} />
              </nav>
            )}
          </Dialog>
          <div className="manage-header__context">
            <strong>{mode === 'personal' ? 'Personal · 本机' : 'LAN'}</strong>
            <span>{bootstrap.principalId ?? '未认证'}</span>
          </div>
          <details className="manage-header__protocols">
            <summary>接口协议</summary>
            <p className="manage-header__meta">
              <span>API {bootstrap.apiVersion}</span>
              <span>WS v{bootstrap.websocketProtocolVersion}</span>
              <span>排序 v{bootstrap.sortProtocolVersion}</span>
              <span>规则 Schema v{bootstrap.ruleSchemaVersion}</span>
            </p>
          </details>
          <div className="manage-header__spacer" />
          <div className="manage-header__actions">
            <Button variant="secondary" onPress={refreshSnapshots}>
              重新拉取快照
            </Button>
            <Menu
              label="外观"
              items={[...themeItems, { id: 'density', label: `密度：${DENSITY_LABELS[density]}` }]}
              onAction={(id) => {
                if (id === 'density') {
                  setDensity(density === 'comfortable' ? 'compact' : 'comfortable');
                  return;
                }
                const value = id.slice('theme:'.length);
                if (value in THEME_LABELS) setTheme(value as ThemePreference);
              }}
            />
            <SignOutButton />
          </div>
        </header>

        <main className="manage-main" id="manage-main">
          {children}
        </main>
      </div>
    </div>
  );
}

function closedReasonLabel(reason: string): string {
  switch (reason) {
    case 'session-revoked':
      return '会话已被吊销，需要重新认证';
    case 'grant-revoked':
      return '授权已被吊销，需要重新认证';
    case 'protocol-mismatch':
      return '协议版本不一致，请刷新页面';
    default:
      return reason;
  }
}
