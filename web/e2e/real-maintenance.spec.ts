import { expect, test, type Page, type Response } from '@playwright/test';

const realBaseURL = process.env.GALLERY_REAL_BASE_URL;
const restoreSentinelLibraryName = 'EV-64 备份后资料库';
test.skip(!realBaseURL, '仅由隔离 Personal galleryd E2E 运行器执行');
test.setTimeout(90_000);

interface JobSnapshot {
  id: string;
  status: string;
  stage?: string;
  issueCode?: string;
  progress?: {
    current: number;
    total: number;
    sequence: number;
    phase?: string;
    unit?: string;
    estimated?: boolean;
  };
}

interface BackupManifest {
  backupId: string;
}

function pathIs(response: Response, path: string, method = 'GET'): boolean {
  return response.request().method() === method && new URL(response.url()).pathname === path;
}

async function pair(page: Page): Promise<void> {
  await page.goto('/manage/diagnostics');
  await expect(page.getByRole('heading', { name: '管理需要认证', exact: true })).toBeVisible();
  const exchange = page.waitForResponse((response) => pathIs(response, '/api/v1/personal/pair', 'POST'));
  await page.getByRole('button', { name: '开始配对', exact: true }).click();
  expect((await exchange).status()).toBe(201);
  await expect(page.getByRole('heading', { name: '验证和诊断', exact: true })).toBeVisible();
}

async function readJSON<T>(page: Page, path: string): Promise<T> {
  return page.evaluate(async (target) => {
    const response = await fetch(target, { credentials: 'same-origin' });
    if (!response.ok) throw new Error(`只读请求失败: ${response.status}`);
    return (await response.json()) as T;
  }, path);
}

async function waitForJob(page: Page, jobId: string): Promise<JobSnapshot> {
  let job: JobSnapshot | undefined;
  await expect
    .poll(
      async () => {
        job = await readJSON<JobSnapshot>(page, `/api/v1/jobs/${encodeURIComponent(jobId)}`);
        return job.status;
      },
      { timeout: 30_000 }
    )
    .toMatch(/^(completed|failed|cancelled|superseded|needs_repair)$/);
  expect(job?.status, JSON.stringify(job)).toBe('completed');
  return job ?? { id: jobId, status: 'missing' };
}

function fact(page: Page, term: string) {
  return page.locator('dt', { hasText: term }).locator('xpath=following-sibling::dd[1]').first();
}

test('Personal 维护、备份、验证与待重启恢复真实链 @real-maintenance', async ({ page }) => {
  await pair(page);
  await expect(page.getByText('实时通道：已连接', { exact: true })).toBeVisible();
  const backupsBefore = await readJSON<{ backups: BackupManifest[] }>(page, '/api/v1/admin/control-backups');
  const existingBackupIds = new Set(backupsBefore.backups.map((item) => item.backupId));

  // 创建请求返回时 manifest 尚未发布。页面必须依赖真实 job.completed 重新读取维护快照，
  // 不能靠手动刷新、重连或直接把事件 payload 填进列表。
  const backupResponsePromise = page.waitForResponse((response) =>
    pathIs(response, '/api/v1/admin/control-backups', 'POST')
  );
  await page.getByRole('button', { name: '创建备份', exact: true }).click();
  const backupResponse = await backupResponsePromise;
  expect(backupResponse.status()).toBe(202);
  const backupJob = (await backupResponse.json()) as JobSnapshot;
  await waitForJob(page, backupJob.id);

  const backupList = await readJSON<{ backups: BackupManifest[] }>(page, '/api/v1/admin/control-backups');
  expect(backupList.backups).toHaveLength(backupsBefore.backups.length + 1);
  const manifest = backupList.backups.find((item) => !existingBackupIds.has(item.backupId));
  if (manifest === undefined) throw new Error('已完成备份缺少 manifest');
  const backupId = manifest.backupId;
  const backupRow = page.getByRole('row').filter({ hasText: backupId });
  await expect(backupRow).toHaveCount(1);

  // 备份发布后再经可见 UI 写入一个不会接触 Source 的 Library 事实。运行器稍后重启同一
  // AppDirs，下一段浏览器用例以该事实消失、原有 Library 保留来证明恢复确实已经应用。
  await page.goto('/manage/scans');
  await expect(page.getByRole('heading', { name: '扫描与任务', exact: true })).toBeVisible();
  await page.getByRole('textbox', { name: /Library 名称/ }).fill(restoreSentinelLibraryName);
  const sentinelResponsePromise = page.waitForResponse((response) =>
    pathIs(response, '/api/v1/libraries', 'POST')
  );
  await page.getByRole('button', { name: '创建 Library', exact: true }).click();
  const sentinelResponse = await sentinelResponsePromise;
  expect(sentinelResponse.status()).toBe(201);
  expect((await sentinelResponse.json()) as { name: string }).toMatchObject({
    name: restoreSentinelLibraryName
  });
  const libraryTable = page.getByRole('table', { name: 'Library', exact: true });
  await expect(libraryTable.getByRole('row').filter({ hasText: restoreSentinelLibraryName })).toHaveCount(1);

  await page.goto('/manage/diagnostics');
  await expect(page.getByRole('heading', { name: '验证和诊断', exact: true })).toBeVisible();

  // Select 的选项来自同一份 HTTP manifest 快照；验证是只读 dry-run。
  await page.getByRole('button', { name: /要恢复的备份/ }).click();
  await page.getByRole('option', { name: new RegExp(backupId) }).click();
  const verifyResponsePromise = page.waitForResponse((response) =>
    pathIs(response, '/api/v1/admin/control-restores/verify', 'POST')
  );
  await page.getByRole('button', { name: '验证（干跑，不改变任何东西）', exact: true }).click();
  const verifyResponse = await verifyResponsePromise;
  expect(verifyResponse.status()).toBe(200);
  expect(verifyResponse.request().postDataJSON()).toEqual({ backupId });
  await expect(page.getByRole('heading', { name: '干跑验证结论', exact: true })).toBeVisible();
  await expect(fact(page, '兼容性')).toHaveText('兼容');
  await expect(fact(page, '校验和')).toHaveText('已核对');
  await expect(fact(page, '完整性')).toHaveText('通过');
  await expect(fact(page, '不变量')).toHaveText('通过');

  // 登记恢复只写入隔离 AppDirs 的 pending 请求；本段不会把登记误当成已执行。运行器会在
  // 本用例结束后优雅停止进程，并以同一 AppDirs 重启，再由独立浏览器用例验证恢复结果。
  const restoreResponsePromise = page.waitForResponse((response) =>
    pathIs(response, '/api/v1/admin/control-restores', 'POST')
  );
  await page.getByRole('button', { name: '登记恢复', exact: true }).click();
  const restoreDialog = page.getByRole('dialog', { name: '登记 control 恢复', exact: true });
  await restoreDialog.getByRole('button', { name: '确认登记', exact: true }).click();
  const restoreResponse = await restoreResponsePromise;
  expect(restoreResponse.status()).toBe(202);
  expect(restoreResponse.request().postDataJSON()).toEqual({ backupId });
  await expect(page.getByText('已登记，需要重启 galleryd 才会生效', { exact: true })).toBeVisible();

  // 最后经可见 UI 创建安全的 Catalog GC dry-run，并确认持久 Job 正常完成。
  const gcResponsePromise = page.waitForResponse((response) =>
    pathIs(response, '/api/v1/admin/maintenance/gc', 'POST')
  );
  await page.getByRole('button', { name: '创建维护任务', exact: true }).click();
  const maintenanceDialog = page.getByRole('dialog', { name: '创建维护任务', exact: true });
  await maintenanceDialog.getByRole('button', { name: '确认创建', exact: true }).click();
  const gcResponse = await gcResponsePromise;
  expect(gcResponse.status()).toBe(202);
  expect(gcResponse.request().postDataJSON()).toEqual({ retentionSeconds: 86_400, dryRun: true });
  const maintenance = (await gcResponse.json()) as { job: JobSnapshot };
  const completedMaintenance = await waitForJob(page, maintenance.job.id);
  expect(completedMaintenance.stage).toBe('completed');
  expect(completedMaintenance.progress).toMatchObject({
    current: 2,
    total: 2,
    unit: 'phases',
    estimated: true
  });
  expect(completedMaintenance.progress?.sequence).toBeGreaterThanOrEqual(6);
  await expect(fact(page, '任务 ID')).toContainText(maintenance.job.id);
  await expect(fact(page, '空间是否充足')).toHaveText('充足');

  // 管理任务表必须消费同一 HTTP 快照中的估算阶段进度；后端字段存在但前端仍显示 0
  // 不能算作“维护窗口对用户可见”。
  await page.goto('/manage/scans');
  await expect(page.getByRole('heading', { name: '扫描与任务', exact: true })).toBeVisible();
  const maintenanceRow = page.getByRole('row').filter({ hasText: maintenance.job.id });
  await expect(maintenanceRow).toHaveCount(1);
  await expect(maintenanceRow).toContainText('2 / 2（估算）');
  await expect(maintenanceRow).toContainText('completed');
});
