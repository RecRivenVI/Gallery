import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';
import { VitePWA } from 'vite-plugin-pwa';

export default defineConfig({
  plugins: [
    react(),
    VitePWA({
      registerType: 'prompt',
      injectRegister: false,
      includeAssets: ['favicon.svg', 'icons/gallery-192.png', 'icons/gallery-512.png'],
      manifest: {
        id: '/',
        name: 'Gallery · 画廊',
        short_name: 'Gallery',
        description: '本地优先、Source 永久只读的个人媒体目录',
        start_url: '/',
        scope: '/',
        display: 'standalone',
        orientation: 'any',
        background_color: '#101316',
        theme_color: '#101316',
        categories: ['photo', 'entertainment', 'utilities'],
        icons: [
          { src: '/icons/gallery-192.png', sizes: '192x192', type: 'image/png', purpose: 'any maskable' },
          { src: '/icons/gallery-512.png', sizes: '512x512', type: 'image/png', purpose: 'any maskable' }
        ]
      },
      workbox: {
        globPatterns: ['**/*.{js,css,html,svg,png,json,webmanifest}'],
        navigateFallback: '/index.html',
        navigateFallbackDenylist: [/^\/api\//, /^\/ws\//],
        cleanupOutdatedCaches: true,
        clientsClaim: false,
        skipWaiting: false,
        runtimeCaching: []
      }
    })
  ],
  build: {
    outDir: '../internal/webapp/dist',
    emptyOutDir: true,
    sourcemap: false,
    target: ['es2022'],
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes('@rjsf')) return 'rule-forms';
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
