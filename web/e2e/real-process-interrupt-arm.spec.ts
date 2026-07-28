import { writeFile } from 'node:fs/promises';
import { expect, test, type Page, type Response } from '@playwright/test';

const realBaseURL = process.env.GALLERY_REAL_BASE_URL;
const sourceRoot = process.env.GALLERY_REAL_PROCESS_INTERRUPT_SOURCE_ROOT;
const statePath = process.env.GALLERY_REAL_PROCESS_INTERRUPT_STATE;
test.skip(!realBaseURL || !sourceRoot || !statePath, '仅由真实进程强杀恢复运行器执行');
test.setTimeout(90_000);
test.use({ screenshot: 'off', video: 'off', trace: 'off' });

const libraryName = '真实浏览器资料库';
const baselineSourceName = '真实浏览器合成来源';
const sourceName = '真实浏览器进程中断来源';

interface LibrarySnapshot {
  id: string;
  name: string;
}

interface SourceSnapshot {
  id: string;
  displayName: string;
}

interface BindingSnapshot {
  id: string;
  sourceId: string;
  semanticHash: string;
  parametersText: string;
  status?: 'active' | 'paused' | 'invalid';
}

interface JobSnapshot {
  id: string;
  type: string;
  sourceId?: string;
  status: string;
  stage: string;
  attempt: number;
  queryPublicationId?: string | null;
}

interface JobAttemptSnapshot {
  attemptId: string;
  attempt: number;
  status: string;
}

interface SourceScanState {
  currentPublicationId?: string | null;
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

test('从可见 UI 建立真实运行中的 Scan/Hash 供进程强杀 @real-process-interrupt-arm', async ({ page }) => {
  await pair(page);
  await expect(page.getByText('实时通道：已连接', { exact: true })).toBeVisible();

  const libraries = await readJSON<{ libraries: LibrarySnapshot[] }>(page, '/api/v1/libraries');
  const library = only(
    libraries.libraries.filter((item) => item.name === libraryName),
    '恢复后保留的 Library'
  );

  await page.goto('/manage/scans');
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
    '恢复后保留的基线 Source'
  );
  const baselineBinding = await readJSON<BindingSnapshot>(
    page,
    `/api/v1/sources/${encodeURIComponent(baselineSource.id)}/effective-rule-binding`
  );

  await page.goto('/manage/rules');
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
  const binding = (await bindingResponse.json()) as BindingSnapshot;
  expect(binding).toMatchObject({ sourceId: source.id, status: 'active' });

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
  const scan = (await scanResponse.json()) as JobSnapshot;

  let hash: JobSnapshot | undefined;
  await expect
    .poll(
      async () => {
        const currentScan = await readJSON<JobSnapshot>(page, `/api/v1/jobs/${encodeURIComponent(scan.id)}`);
        const active = await readJSON<{ jobs: JobSnapshot[] }>(page, '/api/v1/jobs?status=running&limit=200');
        const hashes = active.jobs.filter((item) => item.type === 'hash' && item.sourceId === source.id);
        expect(hashes).toHaveLength(1);
        hash = hashes[0];
        return currentScan.status === 'running' && hash.status === 'running';
      },
      { timeout: 30_000 }
    )
    .toBe(true);
  if (hash === undefined) throw new Error('未找到实际运行中的 Hash Job');

  const scanAttempts = await readJSON<{ attempts: JobAttemptSnapshot[] }>(
    page,
    `/api/v1/jobs/${encodeURIComponent(scan.id)}/attempts`
  );
  const hashAttempts = await readJSON<{ attempts: JobAttemptSnapshot[] }>(
    page,
    `/api/v1/jobs/${encodeURIComponent(hash.id)}/attempts`
  );
  expect(scanAttempts.attempts).toEqual([expect.objectContaining({ attempt: 1, status: 'running' })]);
  expect(hashAttempts.attempts).toEqual([expect.objectContaining({ attempt: 1, status: 'running' })]);
  const scanState = await readJSON<SourceScanState>(
    page,
    `/api/v1/sources/${encodeURIComponent(source.id)}/scan-status`
  );
  expect(scanState.currentPublicationId ?? null).toBeNull();

  const jobsTable = page.getByRole('table', { name: '任务快照', exact: true });
  await expect(
    jobsTable.getByRole('row').filter({ hasText: scan.id }).getByText('执行中', { exact: true })
  ).toBeVisible();
  await writeFile(
    statePath ?? '',
    `${JSON.stringify({
      armedAt: new Date().toISOString(),
      sourceId: source.id,
      bindingId: binding.id,
      scanJobId: scan.id,
      hashJobId: hash.id
    })}\n`,
    { encoding: 'utf8', mode: 0o600 }
  );
});
