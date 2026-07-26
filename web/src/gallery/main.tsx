/*
 * 画廊入口（web/index.html → 本文件）。
 *
 * 画廊是**唯一**注册 Service Worker 的入口，也是唯一可安装的 PWA。管理端同源但不进 PWA
 * scope：它的深链由服务端用 manage.html 承接，SW 的 navigateFallbackDenylist 里排除了
 * `/manage`（见 vite.config.ts）。
 *
 * 本文件只负责装配外壳：主题 → 通知 → 会话 → 实时事件。**页面属于 web/src/gallery/** 的其余
 * 文件**，由画廊工作线实现；接入方式见下方 GalleryPlaceholder 的说明。
 */

import { QueryClientProvider } from '@tanstack/react-query';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { useRegisterSW } from 'virtual:pwa-register/react';
import { Button, EmptyState, SkipLink, ToastProvider, Toast } from '../design';
import { createQueryClient } from '../shared/query';
import { RealtimeProvider } from '../shared/realtime';
import { SessionProvider } from '../shared/session';
import { ThemeProvider } from '../shared/theme';

/**
 * Service Worker 更新提示。
 *
 * registerType 是 'prompt' 且 skipWaiting=false：新版本不会在用户正在浏览时自我替换，
 * 必须由用户确认。缓存只包含静态壳，API 响应与媒体字节从不进缓存。
 */
function ServiceWorkerUpdatePrompt() {
  const {
    needRefresh: [needRefresh, setNeedRefresh],
    updateServiceWorker
  } = useRegisterSW({ immediate: true });
  if (!needRefresh) return null;
  return (
    <div className="ui-toast-region" role="region" aria-label="应用更新">
      <Toast
        tone="info"
        title="画廊已有新版本"
        description="更新只替换静态界面，不会缓存 API 响应或媒体正文。"
        onDismiss={() => setNeedRefresh(false)}
      />
      <div className="ui-toast">
        <Button variant="primary" onPress={() => void updateServiceWorker(true)}>
          立即更新
        </Button>
      </div>
    </div>
  );
}

/**
 * 画廊页面接入点。
 *
 * 画廊工作线请在 `web/src/gallery/app.tsx` 导出根组件，然后把这里替换成
 * `import { GalleryApp } from './app';` 并在下面渲染 `<GalleryApp />`。
 * 在此之前保留这个占位，使入口骨架自身可构建、可运行、可验证。
 */
function GalleryPlaceholder() {
  return (
    <main id="gallery-main" style={{ padding: 'var(--space-6)' }}>
      <EmptyState
        title="画廊界面尚未接入"
        description="共享设计系统与双入口骨架已就绪，浏览、作品与媒体页面由画廊工作线实现。"
      />
    </main>
  );
}

const root = document.getElementById('root');
if (!root) throw new Error('画廊入口缺少 #root 挂载点');

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={createQueryClient()}>
      <ThemeProvider surface="gallery">
        <ToastProvider>
          <ServiceWorkerUpdatePrompt />
          <BrowserRouter>
            <SessionProvider>
              <RealtimeProvider>
                <SkipLink targetId="gallery-main" />
                <GalleryPlaceholder />
              </RealtimeProvider>
            </SessionProvider>
          </BrowserRouter>
        </ToastProvider>
      </ThemeProvider>
    </QueryClientProvider>
  </StrictMode>
);
