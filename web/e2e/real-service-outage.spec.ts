import { access, writeFile } from 'node:fs/promises';
import { expect, test, type Page, type Response } from '@playwright/test';

const realBaseURL = process.env.GALLERY_REAL_BASE_URL;
const readyPath = process.env.GALLERY_REAL_SERVICE_OUTAGE_READY;
const budgetPath = process.env.GALLERY_REAL_SERVICE_OUTAGE_BUDGET;
const restartedPath = process.env.GALLERY_REAL_SERVICE_OUTAGE_RESTARTED;
test.skip(!realBaseURL || !readyPath || !budgetPath || !restartedPath, '仅由真实服务长停机恢复运行器执行');
test.setTimeout(180_000);
test.use({ screenshot: 'off', video: 'off', trace: 'off' });

function pathIs(response: Response, path: string, method = 'GET'): boolean {
  return response.request().method() === method && new URL(response.url()).pathname === path;
}

async function pair(page: Page): Promise<void> {
  await page.goto('/manage');
  await expect(page.getByRole('heading', { name: '管理需要认证', exact: true })).toBeVisible();
  const exchange = page.waitForResponse((response) => pathIs(response, '/api/v1/personal/pair', 'POST'));
  await page.getByRole('button', { name: '开始配对', exact: true }).click();
  expect((await exchange).status()).toBe(201);
  await expect(page.getByRole('heading', { name: 'Gallery 管理', exact: true })).toBeVisible();
}

async function fileExists(path: string): Promise<boolean> {
  try {
    await access(path);
    return true;
  } catch {
    return false;
  }
}

test('同一页面在服务长停机超过旧预算后自动恢复实时连接与 HTTP 快照 @real-service-outage', async ({
  page
}) => {
  let websocketCount = 0;
  let websocketClosed = 0;
  let jobsResponses = 0;
  page.on('websocket', (socket) => {
    websocketCount += 1;
    socket.on('close', () => {
      websocketClosed += 1;
    });
  });
  page.on('response', (response) => {
    if (pathIs(response, '/api/v1/jobs')) jobsResponses += 1;
  });

  await pair(page);
  await page.goto('/manage/scans');
  await expect(page.getByText('实时通道：已连接', { exact: true })).toBeVisible();
  await expect(page.getByRole('table', { name: '任务快照', exact: true })).toBeVisible();
  const baselineSockets = websocketCount;
  const baselineClosed = websocketClosed;
  const baselineJobsResponses = jobsResponses;
  await writeFile(readyPath ?? '', 'ready\n', { encoding: 'utf8', mode: 0o600 });

  await expect(page.getByText('实时通道：重连中', { exact: true })).toBeVisible({ timeout: 30_000 });
  await expect.poll(() => websocketClosed).toBeGreaterThan(baselineClosed);

  // 使用真实墙钟走过 1/2/4/8 秒及五次 15 秒封顶退避。旧实现会在第九次失败时
  // 永久进入 retries-exhausted，只能看到基线后的八条连接；新实现必须在约 90 秒建立
  // 第十条连接并继续等待服务恢复。这里不再用虚拟时钟驱动原生 WebSocket。
  await expect.poll(() => websocketCount, { timeout: 120_000 }).toBeGreaterThanOrEqual(baselineSockets + 9);
  await expect.poll(() => websocketClosed, { timeout: 15_000 }).toBeGreaterThanOrEqual(baselineClosed + 10);
  await expect(page.getByText('实时通道：重连中', { exact: true })).toBeVisible();
  await expect(page.getByText(/停止原因：/)).toHaveCount(0);
  await writeFile(budgetPath ?? '', 'budget-exceeded\n', { encoding: 'utf8', mode: 0o600 });

  await expect.poll(() => fileExists(restartedPath ?? ''), { timeout: 30_000 }).toBe(true);
  await expect(page.getByText('实时通道：已连接', { exact: true })).toBeVisible({ timeout: 30_000 });
  await expect.poll(() => jobsResponses).toBeGreaterThan(baselineJobsResponses);
});
