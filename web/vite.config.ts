import { fileURLToPath } from 'node:url';
import react from '@vitejs/plugin-react';
import { defineConfig, type Plugin } from 'vitest/config';
import { VitePWA } from 'vite-plugin-pwa';

/**
 * 把 VitePWA 注入的 `<link rel="manifest">` 从管理端外壳里去掉。
 *
 * VitePWA 会给**每一个** HTML 入口注入 manifest 链接，没有按入口关闭的选项。管理端不是可
 * 安装应用：留着它，浏览器会在管理端页面提示"安装 Gallery"，而安装后打开的却是画廊
 * （manifest 的 start_url 是 `/`）。enforce: 'post' 保证本插件在 VitePWA 注入之后运行。
 */
function stripManifestLinkFromManageShell(): Plugin {
  return {
    name: 'gallery-strip-manage-manifest-link',
    enforce: 'post',
    transformIndexHtml: {
      order: 'post',
      handler(html, context) {
        if (!context.path.endsWith('manage.html')) return html;
        return html.replace(/\s*<link rel="manifest"[^>]*>/g, '');
      }
    }
  };
}

/*
 * 双入口构建。
 *
 * index.html  → src/gallery/main.tsx   画廊，注册 Service Worker，可安装 PWA
 * manage.html → src/manage/main.tsx    管理，同源但不进 PWA scope
 *
 * 服务端（internal/webapp）按路径前缀选外壳：`/manage/**` 由 manage.html 承接。这里只需
 * 保证产物中确实存在 manage.html。
 */
export default defineConfig({
  plugins: [
    react(),
    VitePWA({
      registerType: 'prompt',
      injectRegister: false,
      includeManifestIcons: false,
      manifest: {
        id: '/',
        name: 'Gallery · 画廊',
        short_name: 'Gallery',
        description: '本地优先、Source 永久只读的个人媒体目录',
        start_url: '/',
        scope: '/',
        display: 'standalone',
        orientation: 'any',
        background_color: '#0f1416',
        theme_color: '#0f1416',
        categories: ['photo', 'entertainment', 'utilities'],
        icons: [
          { src: '/icons/gallery-192.png', sizes: '192x192', type: 'image/png', purpose: 'any maskable' },
          { src: '/icons/gallery-512.png', sizes: '512x512', type: 'image/png', purpose: 'any maskable' }
        ]
      },
      workbox: {
        globPatterns: ['**/*.{js,css,html,svg,png,json}'],
        manifestTransforms: [
          // precache 清单必须按 URL 稳定排序：internal/webapp 的 TestPrecacheManifestIsCanonical
          // 依赖这个顺序判断产物是否规范。
          (entries) =>
            Promise.resolve({
              manifest: [...entries].sort((left, right) =>
                left.url < right.url ? -1 : left.url > right.url ? 1 : 0
              ),
              warnings: []
            })
        ],
        navigateFallback: '/index.html',
        // 管理端必须排除在导航回落之外：否则 SW 会用**画廊**外壳应答 `/manage/**` 的导航，
        // 刷新或书签打开管理端深链会落进画廊。/api 与 /ws 同理不能被静态外壳应答。
        navigateFallbackDenylist: [/^\/manage/, /^\/api\//, /^\/ws\//],
        cleanupOutdatedCaches: true,
        clientsClaim: false,
        skipWaiting: false,
        // 绝不缓存 API 响应或媒体字节：媒体是 Source 的只读投影，缓存会让离线时看到
        // 已经不成立的内容；API 响应带 publication 快照语义，跨代次拼接是明确禁止的。
        runtimeCaching: []
      }
    }),
    stripManifestLinkFromManageShell()
  ],
  build: {
    outDir: '../internal/webapp/dist',
    emptyOutDir: true,
    sourcemap: false,
    target: ['es2022'],
    rollupOptions: {
      input: {
        index: fileURLToPath(new URL('index.html', import.meta.url)),
        manage: fileURLToPath(new URL('manage.html', import.meta.url))
      },
      output: {
        manualChunks(id) {
          if (id.includes('react-aria-components')) return 'aria';
          if (id.includes('@tanstack') || id.includes('openapi-fetch')) return 'query';
          if (id.includes('node_modules/react')) return 'react';
          return undefined;
        }
      }
    }
  },
  server: {
    host: '127.0.0.1',
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': { target: 'http://127.0.0.1:8081', changeOrigin: false },
      '/ws': { target: 'ws://127.0.0.1:8081', ws: true, changeOrigin: false }
    }
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./tests/setup.ts'],
    exclude: ['e2e/**', 'node_modules/**'],
    css: true,
    coverage: { reporter: ['text', 'html'] }
  }
});
