/*
 * 管理端根组件与路由。
 *
 * 路由 basename 已经由入口固定为 `/manage`（见 main.tsx），因此这里的路径**不再带**该前缀。
 *
 * 管理端与画廊共享会话语义但入口独立：未认证时它渲染自己的认证界面，而不是把人送去画廊。
 */

import { Route, Routes } from 'react-router-dom';
import { useState } from 'react';
import { Button, EmptyState, Spinner, TextInput } from '../design';
import { AuthGate, useAuthActions, useSession } from '../shared/session';
import { ManageLayout } from './layout';
import { DiagnosticsPage } from './pages/diagnostics';
import { GovernancePage } from './pages/governance';
import { JobDetailPage } from './pages/jobDetail';
import { OverviewPage } from './pages/overview';
import { RulePackagePage } from './pages/rulePackage';
import { RulesPage } from './pages/rules';
import { ScansPage } from './pages/scans';
import { SecurityPage } from './pages/security';
import { InlineError, PageHeader, Section } from './ui';
import './manage.css';

/**
 * 管理端的认证界面。
 *
 * Personal 用一次性配对（仅 loopback 可用）；LAN 先初始化 Owner，之后用本地账户登录。
 * 匿名访问**不是**管理员，这一点在两种模式下都成立。
 */
function ManageSignIn() {
  const { mode, bootstrap } = useSession();
  const { pairPersonal, initializeLanOwner, login, isPending, error } = useAuthActions();
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [displayName, setDisplayName] = useState('');

  return (
    <main className="manage-main" id="manage-main">
      <PageHeader
        title="管理需要认证"
        lead="管理界面不是「登录后的画廊」，它有独立入口，但认证语义完全相同。匿名访问不会获得管理员权限。"
      />
      {mode === 'personal' ? (
        <Section
          title="Personal 一次性配对"
          description="仅在 loopback 上可用。配对分两步：创建一次性凭据，再用它换取 HttpOnly 会话。"
        >
          <div className="manage-form__actions">
            {/* 成功后认证壳会立即卸载。这里用稳定 status 播报，避免 RAC pending live-announcer
                在触发按钮消失后短暂留下失去 aria-labelledby 目标的 role=img。 */}
            <Button variant="primary" isDisabled={isPending} onPress={() => void pairPersonal()}>
              开始配对
            </Button>
          </div>
          {isPending ? <Spinner label="正在配对本机浏览器" /> : null}
          <InlineError error={error} title="配对未能完成" />
        </Section>
      ) : bootstrap.lanInitialized ? (
        <Section title="登录" description="LAN 模式下所有访问都要求本地账户。">
          <div className="manage-form">
            <TextInput label="用户名" value={username} onChange={setUsername} autoComplete="username" />
            <TextInput
              label="密码"
              type="password"
              value={password}
              onChange={setPassword}
              autoComplete="current-password"
            />
            <div className="manage-form__actions">
              <Button
                variant="primary"
                isPending={isPending}
                isDisabled={username.trim() === '' || password === ''}
                onPress={() =>
                  void login({ username: username.trim(), password, clientLabel: 'Gallery 管理端' })
                }
              >
                登录
              </Button>
            </div>
            <InlineError error={error} title="登录未能完成" />
          </div>
        </Section>
      ) : (
        <Section
          title="初始化 LAN Owner"
          description="LAN 首次启用必须先创建 Owner。它返回的是账户而不是会话，创建后仍需登录。"
        >
          <div className="manage-form">
            <TextInput label="用户名" value={username} onChange={setUsername} autoComplete="username" />
            <TextInput label="显示名" value={displayName} onChange={setDisplayName} />
            <TextInput
              label="密码"
              type="password"
              value={password}
              onChange={setPassword}
              autoComplete="new-password"
            />
            <div className="manage-form__actions">
              <Button
                variant="primary"
                isPending={isPending}
                isDisabled={username.trim() === '' || displayName.trim() === '' || password === ''}
                onPress={() =>
                  void initializeLanOwner({
                    username: username.trim(),
                    displayName: displayName.trim(),
                    password
                  })
                }
              >
                创建 Owner
              </Button>
            </div>
            <InlineError error={error} title="Owner 初始化未能完成" />
          </div>
        </Section>
      )}
    </main>
  );
}

function NotFoundPage() {
  return (
    <EmptyState
      title="没有这个管理页面"
      description="请从上方导航选择一个分区。管理端的深链由服务端按 /manage 前缀承接，刷新不会落进画廊。"
    />
  );
}

export function ManageApp() {
  return (
    <AuthGate signIn={<ManageSignIn />}>
      <ManageLayout>
        <Routes>
          <Route path="/" element={<OverviewPage />} />
          <Route path="/scans" element={<ScansPage />} />
          <Route path="/scans/:jobId" element={<JobDetailPage />} />
          <Route path="/diagnostics" element={<DiagnosticsPage />} />
          <Route path="/security" element={<SecurityPage />} />
          <Route path="/rules" element={<RulesPage />} />
          <Route path="/rules/:packageId" element={<RulePackagePage />} />
          <Route path="/governance" element={<GovernancePage />} />
          <Route path="*" element={<NotFoundPage />} />
        </Routes>
      </ManageLayout>
    </AuthGate>
  );
}
