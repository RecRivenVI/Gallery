import { expect, test, type Page, type Response } from '@playwright/test';

const realBaseURL = process.env.GALLERY_REAL_BASE_URL;
const oldestJobID = process.env.GALLERY_REAL_OLDEST_JOB_ID;
const historyCount = Number(process.env.GALLERY_REAL_JOB_HISTORY_COUNT ?? '0');
test.skip(!realBaseURL || !oldestJobID || historyCount < 51, '仅由隔离真实 E2E 运行器执行');
test.setTimeout(45_000);

function pathIs(response: Response, path: string, method = 'GET'): boolean {
  return response.request().method() === method && new URL(response.url()).pathname === path;
}

async function pair(page: Page): Promise<void> {
  await page.goto('/manage/scans');
  await expect(page.getByRole('heading', { name: /管理需要认证|扫描与任务/ })).toBeVisible();
  const button = page.getByRole('button', { name: '开始配对' });
  if (!(await button.isVisible().catch(() => false))) return;
  const exchange = page.waitForResponse((response) => pathIs(response, '/api/v1/personal/pair', 'POST'));
  await button.click();
  expect((await exchange).status()).toBe(201);
}

test('真实 galleryd 的长期任务历史可从管理端连续载入 @real-job-history', async ({ page }) => {
  await pair(page);
  await expect(page.getByRole('heading', { name: '扫描与任务', exact: true })).toBeVisible();
  await expect(page.getByText('已按新到旧载入 50 条（还有更早任务）。', { exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: oldestJobID ?? '', exact: true })).toHaveCount(0);

  const nextResponse = page.waitForResponse((response) => {
    if (!pathIs(response, '/api/v1/jobs')) return false;
    const url = new URL(response.url());
    return url.searchParams.get('limit') === '50' && url.searchParams.has('cursor');
  });
  await page.getByRole('button', { name: '加载更早任务', exact: true }).click();
  const response = await nextResponse;
  expect(response.status()).toBe(200);
  const body = (await response.json()) as { jobs: Array<{ id: string }>; nextCursor?: string };
  expect(body.jobs.length).toBeGreaterThan(0);

  await expect(page.getByRole('link', { name: oldestJobID ?? '', exact: true })).toBeVisible();
  await expect(page.getByText(/已按新到旧载入 \d+ 条（已到末页）。/)).toBeVisible();
  await expect(page.getByRole('button', { name: '加载更早任务', exact: true })).toHaveCount(0);
});
