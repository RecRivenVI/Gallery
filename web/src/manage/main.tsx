/*
 * 管理入口（web/manage.html → 本文件）。
 *
 * 管理端与画廊同源、共享设计系统与会话，但是**两个独立的 SPA**：
 *   - 不注册 Service Worker，也不进 PWA scope（离线壳会掩盖诊断页面的真实状态）；
 *   - 路由 basename 固定为 `/manage`，服务端按路径前缀用 manage.html 承接深链
 *     （见 internal/webapp/handler.go 的 appShells）；
 *   - 默认密度是 compact：管理端以表格、状态与诊断为中心，信息密度优先。
 *
 * 本文件只负责装配外壳；页面在 `web/src/manage/app.tsx` 及 `web/src/manage/pages/**`。
 */

import { QueryClientProvider } from '@tanstack/react-query';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router';
import { SkipLink, ToastProvider } from '../design';
import { createQueryClient } from '../shared/query';
import { RealtimeProvider } from '../shared/realtime';
import { SessionProvider } from '../shared/session';
import { ThemeProvider } from '../shared/theme';
import { ManageApp } from './app';

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
                <ManageApp />
              </RealtimeProvider>
            </SessionProvider>
          </BrowserRouter>
        </ToastProvider>
      </ThemeProvider>
    </QueryClientProvider>
  </StrictMode>
);
