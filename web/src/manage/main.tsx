/*
 * 管理入口（web/manage.html → 本文件）。
 *
 * 管理端与画廊同源、共享设计系统与会话，但是**两个独立的 SPA**：
 *   - 不注册 Service Worker，也不进 PWA scope（离线壳会掩盖诊断页面的真实状态）；
 *   - 路由 basename 固定为 `/manage`，服务端按路径前缀用 manage.html 承接深链
 *     （见 internal/webapp/handler.go 的 appShells）；
 *   - 默认密度是 compact：管理端以表格、状态与诊断为中心，信息密度优先。
 *
 * 本文件只负责装配外壳；页面属于 web/src/manage/** 的其余文件，由管理工作线实现。
 */

import { QueryClientProvider } from '@tanstack/react-query';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { EmptyState, SkipLink, ToastProvider } from '../design';
import { createQueryClient } from '../shared/query';
import { RealtimeProvider } from '../shared/realtime';
import { SessionProvider } from '../shared/session';
import { ThemeProvider } from '../shared/theme';

/**
 * 管理页面接入点。
 *
 * 管理工作线请在 `web/src/manage/app.tsx` 导出根组件，然后把这里替换成
 * `import { ManageApp } from './app';` 并在下面渲染 `<ManageApp />`。
 */
function ManagePlaceholder() {
  return (
    <main id="manage-main" style={{ padding: 'var(--space-5)' }}>
      <EmptyState
        title="管理界面尚未接入"
        description="共享设计系统与双入口骨架已就绪，Source、规则、任务与安全页面由管理工作线实现。"
      />
    </main>
  );
}

const root = document.getElementById('root');
if (!root) throw new Error('管理入口缺少 #root 挂载点');

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={createQueryClient()}>
      <ThemeProvider surface="manage">
        <ToastProvider>
          <BrowserRouter basename="/manage">
            <SessionProvider>
              <RealtimeProvider>
                <SkipLink targetId="manage-main" />
                <ManagePlaceholder />
              </RealtimeProvider>
            </SessionProvider>
          </BrowserRouter>
        </ToastProvider>
      </ThemeProvider>
    </QueryClientProvider>
  </StrictMode>
);
