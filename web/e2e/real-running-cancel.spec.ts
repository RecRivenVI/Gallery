import { expect, test, type Page, type Response } from '@playwright/test';

const realBaseURL = process.env.GALLERY_REAL_BASE_URL;
const sourceRoot = process.env.GALLERY_REAL_RUNNING_CANCEL_SOURCE_ROOT;
test.skip(!realBaseURL || !sourceRoot, '仅由带独立长任务合成 Source 的隔离 Personal E2E 运行器执行');
test.setTimeout(120_000);
test.use({ screenshot: 'off', video: 'off', trace: 'off' });

const libraryName = '真实浏览器资料库';
const baselineSourceName = '真实浏览器合成来源';
const sourceName = '真实浏览器运行中取消来源';

interface LibrarySnapshot {
  id: string;
  name: string;
}

interface SourceSnapshot {
  id: string;
  displayName: string;
}

interface BindingSnapshot {
  semanticHash: string;
  parametersText: string;
}

interface JobSnapshot {
  id: string;
  type: string;
  sourceId?: string;
  status: string;
  stage: string;
  attempt: number;
  progress: {
    current: number;
    total: number;
  };
  cancelRequested?: boolean;
  queryPublicationId?: string | null;
}

interface JobAttemptSnapshot {
  attempt: number;
  status: string;
  errorCode?: string | null;
  errorRetryable?: boolean;
}

interface SourceScanState {
  sourceId: string;
  currentJobId?: string | null;
  currentPublicationId?: string | null;
  pendingHashCount: number;
}

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

async function readJSON<T>(page: Page, path: string): Promise<T> {
  return page.evaluate(async (target) => {
    const response = await fetch(target, { credentials: 'same-origin' });
    if (!response.ok) throw new Error(`只读请求失败: ${response.status}`);
    return (await response.json()) as T;
  }, path);
}

function only<T>(items: T[], description: string): T {
  expect(items, description).toHaveLength(1);
  const item = items.at(0);
  if (item === undefined) throw new Error(`${description}: 未找到唯一项`);
  return item;
}

test('真实 incremental 扫描从 UI 运行中取消并收敛 Attempt @real-running-cancel', async ({ page }) => {
  await pair(page);
  await expect(page.getByText('实时通道：已连接', { exact: true })).toBeVisible();

  const libraries = await readJSON<{ libraries: LibrarySnapshot[] }>(page, '/api/v1/libraries');
  const library = only(
    libraries.libraries.filter((item) => item.name === libraryName),
    'bootstrap 创建的 Library'
  );

  await page.goto('/manage/scans');
  await expect(page.getByRole('heading', { name: '扫描与任务', exact: true })).toBeVisible();
  await page.getByRole('button', { name: /所属 Library/ }).click();
  await page.getByRole('option', { name: `${library.name} · ${library.id}`, exact: true }).click();
  await page.getByRole('textbox', { name: /Source 显示名/ }).fill(sourceName);
  await page.getByRole('textbox', { name: /Source 根路径/ }).fill(sourceRoot ?? '');
  const createSource = page.waitForResponse((response) => pathIs(response, '/api/v1/sources', 'POST'));
  await page.getByRole('button', { name: '登记 Source', exact: true }).click();
  const sourceResponse = await createSource;
  expect(sourceResponse.status()).toBe(201);
  const source = (await sourceResponse.json()) as { id: string; displayName: string; readOnly: boolean };
  expect(source).toMatchObject({ displayName: sourceName, readOnly: true });

  const sources = await readJSON<{ sources: SourceSnapshot[] }>(page, '/api/v1/sources');
  const baselineSource = only(
    sources.sources.filter((item) => item.displayName === baselineSourceName),
    'bootstrap 创建的 Source'
  );
  const baselineBinding = await readJSON<BindingSnapshot>(
    page,
    `/api/v1/sources/${encodeURIComponent(baselineSource.id)}/effective-rule-binding`
  );

  // 复用 bootstrap 已发布的 RuleVersion，但使用 direct 参数保持本用例与后续共享 ParameterSet
  // 生命周期测试隔离；取消用例从可见扫描表单发起，不直接构造 Job 或 Attempt。
  await page.goto('/manage/rules');
  await expect(page.getByRole('heading', { name: '规则', exact: true })).toBeVisible();
  await page.getByRole('button', { name: /来源/ }).click();
  await page.getByRole('option', { name: `${source.displayName} · ${source.id}`, exact: true }).click();
  await page.getByRole('button', { name: /绑定参数来源/ }).click();
  await page.getByRole('option', { name: 'direct · 直接参数文本', exact: true }).click();
  await page.getByRole('button', { name: /已发布版本/ }).click();
  await page
    .getByRole('option')
    .filter({ hasText: baselineBinding.semanticHash.slice(0, 12) })
    .click();
  await page.getByRole('textbox', { name: 'priority', exact: true }).fill('0');
  await page
    .getByRole('textbox', { name: '参数（精确 JSON 对象文本）', exact: true })
    .fill(baselineBinding.parametersText);
  const createBinding = page.waitForResponse((response) =>
    pathIs(response, '/api/v1/source-rule-bindings', 'POST')
  );
  await page.getByRole('button', { name: '创建绑定', exact: true }).click();
  const bindingResponse = await createBinding;
  expect(bindingResponse.status()).toBe(201);
  expect(bindingResponse.request().postDataJSON()).toEqual({
    sourceId: source.id,
    semanticHash: baselineBinding.semanticHash,
    parameters: baselineBinding.parametersText,
    priority: 0
  });

  await page.goto('/manage/scans');
  await page.getByRole('button', { name: /来源/ }).click();
  await page.getByRole('option', { name: `${source.displayName} · ${source.id}`, exact: true }).click();
  await page.getByRole('button', { name: /扫描档案/ }).click();
  await page.getByRole('option', { name: 'incremental（默认）', exact: true }).click();
  const createScan = page.waitForResponse((response) =>
    pathIs(response, `/api/v1/sources/${source.id}/scan-jobs`, 'POST')
  );
  await page.getByRole('button', { name: '发起扫描', exact: true }).click();
  const scanResponse = await createScan;
  expect(scanResponse.status()).toBe(202);
  expect(scanResponse.request().postDataJSON()).toEqual({ scanProfile: 'incremental' });
  const created = (await scanResponse.json()) as JobSnapshot;

  let running: JobSnapshot | undefined;
  await expect
    .poll(
      async () => {
        running = await readJSON<JobSnapshot>(page, `/api/v1/jobs/${encodeURIComponent(created.id)}`);
        return running.status === 'running' && running.progress.current > 0;
      },
      { timeout: 30_000 }
    )
    .toBe(true);
  expect(running?.progress.total).toBeGreaterThan(1_000);

  const jobsTable = page.getByRole('table', { name: '任务快照', exact: true });
  const jobRow = jobsTable.getByRole('row').filter({ hasText: created.id });
  await expect(jobRow).toHaveCount(1);
  await expect(jobRow.getByText('执行中', { exact: true })).toBeVisible();
  const cancelResponsePromise = page.waitForResponse((response) =>
    pathIs(response, `/api/v1/jobs/${created.id}/cancel`, 'POST')
  );
  await jobRow.getByRole('button', { name: '取消', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: '取消任务', exact: true });
  await expect(dialog.getByText(/会在下一个安全点停止/)).toBeVisible();
  await dialog.getByRole('button', { name: '确认取消', exact: true }).click();
  const cancelResponse = await cancelResponsePromise;
  expect(cancelResponse.status()).toBe(202);
  expect((await cancelResponse.json()) as JobSnapshot).toMatchObject({
    id: created.id,
    status: 'cancelling',
    stage: 'cancelling',
    attempt: 1,
    cancelRequested: true
  });

  let cancelled: JobSnapshot | undefined;
  await expect
    .poll(
      async () => {
        cancelled = await readJSON<JobSnapshot>(page, `/api/v1/jobs/${encodeURIComponent(created.id)}`);
        return cancelled.status;
      },
      { timeout: 30_000 }
    )
    .toBe('cancelled');
  expect(cancelled).toMatchObject({
    stage: 'cancelled',
    attempt: 1,
    cancelRequested: true
  });
  expect(cancelled?.queryPublicationId ?? null).toBeNull();
  await expect(jobRow.getByText('已取消', { exact: true })).toBeVisible();
  await expect(jobRow.getByRole('button', { name: '取消', exact: true })).toHaveCount(0);
  await expect(jobRow.getByRole('button', { name: '重试', exact: true })).toHaveCount(0);

  const attempts = await readJSON<{ attempts: JobAttemptSnapshot[] }>(
    page,
    `/api/v1/jobs/${encodeURIComponent(created.id)}/attempts`
  );
  expect(attempts.attempts).toHaveLength(1);
  expect(attempts.attempts[0]).toMatchObject({
    attempt: 1,
    status: 'cancelled',
    errorRetryable: false
  });
  expect(attempts.attempts[0]?.errorCode ?? null).toBeNull();

  // Scan 正在等待第二条 Hash 时取消；服务端必须把活动子 Job 一并持久收敛，不能只让
  // 父 Job 的 UI 看起来已取消而把实际文件读取留在后台继续执行。
  const cancelledJobs = await readJSON<{ jobs: JobSnapshot[] }>(
    page,
    '/api/v1/jobs?status=cancelled&limit=200'
  );
  const cancelledHashes = cancelledJobs.jobs.filter(
    (job) => job.type === 'hash' && job.sourceId === source.id
  );
  expect(cancelledHashes.length).toBeGreaterThan(0);
  expect(cancelledHashes).toEqual(
    expect.arrayContaining([expect.objectContaining({ status: 'cancelled', cancelRequested: true })])
  );

  const scanState = await readJSON<SourceScanState>(
    page,
    `/api/v1/sources/${encodeURIComponent(source.id)}/scan-status`
  );
  expect(scanState).toMatchObject({ sourceId: source.id, pendingHashCount: 0 });
  expect(scanState.currentPublicationId ?? null).toBeNull();
});
