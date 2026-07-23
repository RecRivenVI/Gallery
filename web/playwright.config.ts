import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: true,
  retries: 1,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL:
      process.env.GALLERY_REAL_BASE_URL ?? process.env.GALLERY_REAL_LAN_BASE_URL ?? 'http://127.0.0.1:4173',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure'
  },
  webServer:
    process.env.GALLERY_REAL_BASE_URL || process.env.GALLERY_REAL_LAN_BASE_URL
      ? undefined
      : {
          command: 'node ./node_modules/vite/bin/vite.js preview --host 127.0.0.1 --port 4173 --strictPort',
          url: 'http://127.0.0.1:4173/gallery-web.json',
          reuseExistingServer: false
        },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
    { name: 'chrome', use: { ...devices['Desktop Chrome'], channel: 'chrome' } },
    { name: 'edge', use: { ...devices['Desktop Edge'], channel: 'msedge' } }
  ]
});
