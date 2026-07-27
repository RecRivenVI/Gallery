import { expect, test, type Page, type Response } from '@playwright/test';

const realBaseURL = process.env.GALLERY_REAL_BASE_URL;
const originalLibraryName = '真实浏览器资料库';
const restoreSentinelLibraryName = 'EV-64 备份后资料库';
test.skip(!realBaseURL, '仅由隔离 Personal galleryd 恢复重启运行器执行');
test.setTimeout(90_000);

function pathIs(response: Response, path: string, method = 'GET'): boolean {
  return response.request().method() === method && new URL(response.url()).pathname === path;
}

async function pair(page: Page): Promise<void> {
  await page.goto('/manage/scans');
  await expect(page.getByRole('heading', { name: '管理需要认证', exact: true })).toBeVisible();
  const exchange = page.waitForResponse((response) => pathIs(response, '/api/v1/personal/pair', 'POST'));
  await page.getByRole('button', { name: '开始配对', exact: true }).click();
  expect((await exchange).status()).toBe(201);
  await expect(page.getByRole('heading', { name: '扫描与任务', exact: true })).toBeVisible();
}

test('同一 AppDirs 重启后应用 control 恢复并留下安全审计 @real-restore-restart', async ({ page }) => {
  await pair(page);
  await expect(page.getByText('实时通道：已连接', { exact: true })).toBeVisible();

  const libraryTable = page.getByRole('table', { name: 'Library', exact: true });
  await expect(libraryTable.getByRole('row').filter({ hasText: originalLibraryName })).toHaveCount(1);
  await expect(libraryTable.getByRole('row').filter({ hasText: restoreSentinelLibraryName })).toHaveCount(0);

  await page.goto('/manage/security');
  await expect(page.getByRole('heading', { name: '连接与安全', exact: true })).toBeVisible();
  await page.getByRole('tab', { name: '安全审计', exact: true }).click();
  const auditTable = page.getByRole('table', { name: '安全审计', exact: true });
  const restoreAudit = auditTable.getByRole('row').filter({ hasText: 'restore.finalize' });
  await expect(restoreAudit).toHaveCount(1);
  await expect(restoreAudit).toContainText('success');
  await expect(restoreAudit).toContainText('control:control.db');
});
