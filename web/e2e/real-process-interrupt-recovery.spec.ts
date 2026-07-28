import { readFile } from 'node:fs/promises';
import { expect, test, type Page, type Response } from '@playwright/test';

const realBaseURL = process.env.GALLERY_REAL_BASE_URL;
const statePath = process.env.GALLERY_REAL_PROCESS_INTERRUPT_STATE;
test.skip(!realBaseURL || !statePath, '仅由真实进程强杀后的同 AppDirs 重启运行器执行');
test.setTimeout(120_000);
test.use({ screenshot: 'off', video: 'off', trace: 'off' });

interface InterruptState {
  armedAt: string;
  sourceId: string;
  bindingId: string;
  scanJobId: string;
  hashJobId: string;
}

interface JobSnapshot {
  id: string;
  status: string;
  attempt: number;
  issueCode?: string | null;
  failureRetryable?: boolean;
  nextAttemptAt?: string | null;
}

interface JobAttemptSnapshot {
  attemptId: string;
  attempt: number;
  status: string;
  errorCode?: string | null;
  errorRetryable?: boolean;
}

interface SourceSnapshot {
  id: string;
  displayName: string;
}

interface SourceScanState {
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

function attemptOne(attempts: JobAttemptSnapshot[]): JobAttemptSnapshot | undefined {
  return attempts.find((item) => item.attempt === 1);
}

async function cancelFromVisibleTable(page: Page, jobId: string): Promise<void> {
  let current = await readJSON<JobSnapshot>(page, `/api/v1/jobs/${encodeURIComponent(jobId)}`);
  if (current.status === 'cancelled') return;
  expect(current.status, JSON.stringify(current)).toMatch(/^(queued|running|failed|needs_repair)$/);
  await page.goto('/manage/scans');
  const row = page
    .getByRole('table', { name: '任务快照', exact: true })
    .getByRole('row')
    .filter({ hasText: jobId });
  await expect(row).toHaveCount(1);
  const cancel = row.getByRole('button', { name: '取消', exact: true });
  await expect(cancel).toBeVisible();
  const response = page.waitForResponse((item) => pathIs(item, `/api/v1/jobs/${jobId}/cancel`, 'POST'));
  await cancel.click();
  const dialog = page.getByRole('dialog', { name: '取消任务', exact: true });
  await dialog.getByRole('button', { name: '确认取消', exact: true }).click();
  expect((await response).status()).toBe(202);
  await expect
    .poll(async () => {
      current = await readJSON<JobSnapshot>(page, `/api/v1/jobs/${encodeURIComponent(jobId)}`);
      return current.status;
    })
    .toBe('cancelled');
}

test('同一 AppDirs 立即重启后从可见 UI 解释并治理强杀 Attempt @real-process-interrupt-recovery', async ({
  page
}) => {
  const state = JSON.parse(await readFile(statePath ?? '', 'utf8')) as InterruptState;
  expect(Date.now() - Date.parse(state.armedAt)).toBeLessThan(120_000);
  await pair(page);
  await expect(page.getByText('实时通道：已连接', { exact: true })).toBeVisible();

  let scanAttempts: JobAttemptSnapshot[] = [];
  let hashAttempts: JobAttemptSnapshot[] = [];
  await expect
    .poll(
      async () => {
        scanAttempts = (
          await readJSON<{ attempts: JobAttemptSnapshot[] }>(
            page,
            `/api/v1/jobs/${encodeURIComponent(state.scanJobId)}/attempts`
          )
        ).attempts;
        hashAttempts = (
          await readJSON<{ attempts: JobAttemptSnapshot[] }>(
            page,
            `/api/v1/jobs/${encodeURIComponent(state.hashJobId)}/attempts`
          )
        ).attempts;
        return [attemptOne(scanAttempts)?.status, attemptOne(hashAttempts)?.status];
      },
      { timeout: 30_000 }
    )
    .toEqual(['recovered', 'recovered']);

  for (const [kind, attempts] of [
    ['Scan', scanAttempts],
    ['Hash', hashAttempts]
  ] as const) {
    expect(attemptOne(attempts), `${kind} Attempt 1`).toMatchObject({
      status: 'recovered',
      errorCode: 'PROCESS_INTERRUPTED',
      errorRetryable: true
    });
  }

  for (const [jobId, attempts] of [
    [state.scanJobId, scanAttempts],
    [state.hashJobId, hashAttempts]
  ] as const) {
    await page.goto(`/manage/scans/${jobId}`);
    await expect(page.getByRole('heading', { name: '任务详情', exact: true })).toBeVisible();
    await expect(page.getByText(/进程中断、租约过期或启动期 publication 对账后/)).toBeVisible();
    const first = attemptOne(attempts);
    if (first === undefined) throw new Error(`Job ${jobId} 缺少 Attempt 1`);
    const row = page.getByRole('table', { name: 'Attempt 历史', exact: true }).getByRole('row').filter({
      hasText: first.attemptId
    });
    await expect(row).toHaveCount(1);
    await expect(row.getByText('已回收（执行未正常收尾）', { exact: true })).toBeVisible();
    await expect(row.getByText('PROCESS_INTERRUPTED', { exact: true })).toBeVisible();
  }

  const sources = await readJSON<{ sources: SourceSnapshot[] }>(page, '/api/v1/sources');
  expect(sources.sources.filter((item) => item.id === state.sourceId)).toEqual([
    expect.objectContaining({ displayName: '真实浏览器进程中断来源' })
  ]);
  const beforeCancel = await readJSON<SourceScanState>(
    page,
    `/api/v1/sources/${encodeURIComponent(state.sourceId)}/scan-status`
  );
  expect(beforeCancel.currentPublicationId ?? null).toBeNull();

  await cancelFromVisibleTable(page, state.scanJobId);
  await cancelFromVisibleTable(page, state.hashJobId);

  await page.goto('/manage/rules');
  await page.getByRole('button', { name: /来源/ }).click();
  await page.getByRole('option').filter({ hasText: state.sourceId }).click();
  const bindingTable = page.getByRole('table', { name: 'Source 规则绑定', exact: true });
  const bindingRow = bindingTable.getByRole('row').filter({ hasText: state.bindingId });
  await expect(bindingRow).toHaveCount(1);
  const pause = page.waitForResponse((response) =>
    pathIs(response, `/api/v1/source-rule-bindings/${state.bindingId}`, 'PATCH')
  );
  await bindingRow.getByRole('button', { name: '暂停', exact: true }).click();
  await page
    .getByRole('dialog', { name: '暂停规则绑定', exact: true })
    .getByRole('button', {
      name: '确认暂停',
      exact: true
    })
    .click();
  expect((await pause).status()).toBe(200);
  await expect(bindingRow.getByText('paused', { exact: true })).toBeVisible();

  const finalScanAttempts = await readJSON<{ attempts: JobAttemptSnapshot[] }>(
    page,
    `/api/v1/jobs/${encodeURIComponent(state.scanJobId)}/attempts`
  );
  const finalHashAttempts = await readJSON<{ attempts: JobAttemptSnapshot[] }>(
    page,
    `/api/v1/jobs/${encodeURIComponent(state.hashJobId)}/attempts`
  );
  expect(attemptOne(finalScanAttempts.attempts)).toMatchObject({
    status: 'recovered',
    errorCode: 'PROCESS_INTERRUPTED'
  });
  expect(attemptOne(finalHashAttempts.attempts)).toMatchObject({
    status: 'recovered',
    errorCode: 'PROCESS_INTERRUPTED'
  });
  expect(finalScanAttempts.attempts.some((item) => item.status === 'running')).toBe(false);
  expect(finalHashAttempts.attempts.some((item) => item.status === 'running')).toBe(false);
  const finalScanState = await readJSON<SourceScanState>(
    page,
    `/api/v1/sources/${encodeURIComponent(state.sourceId)}/scan-status`
  );
  expect(finalScanState.currentPublicationId ?? null).toBeNull();
  expect(finalScanState.pendingHashCount).toBe(0);
});
