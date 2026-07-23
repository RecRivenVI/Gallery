import { createServer } from 'node:net';
import { defineConfig, devices } from '@playwright/test';

// mock 套件的静态壳预览端口默认取一个空闲端口，而不是硬编码 4173。
// 固定端口 + --strictPort 会让 smoke 门禁在该端口被无关进程占用的机器上完全无法运行
// （EV-39 的 TEST-1）；需要可预测端口时用 GALLERY_WEB_PREVIEW_PORT 显式指定。
function pickPreviewPort(): number {
  const configured = Number(process.env.GALLERY_WEB_PREVIEW_PORT);
  if (Number.isInteger(configured) && configured > 0) return configured;
  const probe = createServer();
  probe.listen(0, '127.0.0.1');
  const address = probe.address();
  const port = typeof address === 'object' && address ? address.port : 4173;
  probe.close();
  return port;
}

const previewPort = pickPreviewPort();

export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: true,
  retries: 1,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL:
      process.env.GALLERY_REAL_BASE_URL ??
      process.env.GALLERY_REAL_LAN_BASE_URL ??
      `http://127.0.0.1:${previewPort}`,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure'
  },
  webServer:
    process.env.GALLERY_REAL_BASE_URL || process.env.GALLERY_REAL_LAN_BASE_URL
      ? undefined
      : {
          command: `node ./node_modules/vite/bin/vite.js preview --host 127.0.0.1 --port ${previewPort} --strictPort`,
          url: `http://127.0.0.1:${previewPort}/gallery-web.json`,
          reuseExistingServer: false
        },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
    { name: 'chrome', use: { ...devices['Desktop Chrome'], channel: 'chrome' } },
    { name: 'edge', use: { ...devices['Desktop Edge'], channel: 'msedge' } }
  ]
});
