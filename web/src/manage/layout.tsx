/*
 * 管理端外壳：常驻诊断信息、导航、实时通道状态与快照刷新。
 *
 * 管理端的第一原则是「状态先于名称」。因此顶部常驻的不是品牌，而是：当前部署模式、主体、
 * 三个协议版本、实时通道状态，以及一个显式的「重新拉取快照」入口——因为实时事件只是提示，
 * 真正的事实来自 HTTP 快照。
 */

import { useCallback, useEffect } from 'react';
import type { ReactNode } from 'react';
import { NavLink } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { Badge, Button, Dialog, Select } from '../design';
import { SNAPSHOT_QUERY_PREFIXES } from '../shared/query';
import { useRealtime, useRealtimeEvent, type RealtimeStatus } from '../shared/realtime';
import { SignOutButton, useSession } from '../shared/session';
import { DENSITY_LABELS, THEME_LABELS, useTheme, type Density, type ThemePreference } from '../shared/theme';
import './manage.css';

export interface NavItem {
  to: string;
  label: string;
}

/** 导航顺序即产品路径：先看现状，再动手，再验证，再管连接，再改规则，最后收拾治理欠账。 */
export const MANAGE_NAV: readonly NavItem[] = [
  { to: '/', label: '概览' },
  { to: '/scans', label: '扫描与任务' },
  { to: '/diagnostics', label: '验证和诊断' },
  { to: '/security', label: '连接与安全' },
  { to: '/rules', label: '规则' },
  { to: '/governance', label: '治理' }
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

  const refreshSnapshots = useCallback(() => {
    for (const prefix of SNAPSHOT_QUERY_PREFIXES) {
      void queryClient.invalidateQueries({ queryKey: [prefix] });
    }
    void queryClient.invalidateQueries({ queryKey: ['security'] });
  }, [queryClient]);

  return (
    <div className="manage-shell">
      <header className="manage-header">
        <div>
          <h1 className="manage-header__title">Gallery 管理</h1>
          <p className="manage-header__meta">
            <span>部署模式：{mode === 'personal' ? 'Personal（仅本机）' : 'LAN'}</span>
            <span>主体：{bootstrap.principalId ?? '未认证'}</span>
            <span>API {bootstrap.apiVersion}</span>
            <span>WS 协议 v{bootstrap.websocketProtocolVersion}</span>
            <span>排序协议 v{bootstrap.sortProtocolVersion}</span>
            <span>规则 Schema v{bootstrap.ruleSchemaVersion}</span>
          </p>
        </div>
        <div className="manage-header__spacer" />
        <div className="manage-header__actions">
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
          <span className="manage-status-bar">
            <Badge tone={realtimeTone(status)}>实时通道：{REALTIME_LABELS[status]}</Badge>
            <span>序号 {lastSequence}</span>
            {closedReason === undefined ? null : <span>停止原因：{closedReasonLabel(closedReason)}</span>}
          </span>
          <Button variant="secondary" onPress={refreshSnapshots}>
            重新拉取快照
          </Button>
          <Select
            label="主题"
            options={(Object.keys(THEME_LABELS) as ThemePreference[]).map((value) => ({
              id: value,
              label: THEME_LABELS[value]
            }))}
            selectedKey={theme}
            onSelectionChange={(key) => {
              if (key !== null) setTheme(key as ThemePreference);
            }}
          />
          <Select
            label="密度"
            options={(Object.keys(DENSITY_LABELS) as Density[]).map((value) => ({
              id: value,
              label: DENSITY_LABELS[value]
            }))}
            selectedKey={density}
            onSelectionChange={(key) => {
              if (key !== null) setDensity(key as Density);
            }}
          />
          <SignOutButton />
        </div>
      </header>

      <nav className="manage-nav" aria-label="管理功能">
        <ManageNavList />
      </nav>

      <main className="manage-main" id="manage-main">
        {children}
      </main>
    </div>
  );
}

function closedReasonLabel(reason: string): string {
  switch (reason) {
    case 'session-revoked':
      return '会话已被吊销，需要重新认证';
    case 'grant-revoked':
      return '授权已被吊销，需要重新认证';
    case 'retries-exhausted':
      return '重连次数已用尽，请用上方按钮手动拉取快照';
    case 'protocol-mismatch':
      return '协议版本不一致，请刷新页面';
    default:
      return reason;
  }
}
