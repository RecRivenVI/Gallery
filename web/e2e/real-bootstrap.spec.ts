import { readFile } from 'node:fs/promises';
import { expect, test } from '@playwright/test';

const realBaseURL = process.env.GALLERY_REAL_BASE_URL;
const sourceRoot = process.env.GALLERY_REAL_SOURCE_ROOT;
const rulePackagePath = process.env.GALLERY_REAL_RULE_PACKAGE;
test.skip(!realBaseURL || !sourceRoot || !rulePackagePath, '仅由隔离真实 E2E 运行器执行');

test('真实 galleryd 从空实例完成 Library → Source → Rule Binding → Scan @real-bootstrap', async ({
  page
}) => {
  const rulePackage = JSON.parse(await readFile(rulePackagePath ?? '', 'utf8')) as Record<string, unknown>;

  await page.goto('/manage');
  await expect(page.getByRole('heading', { name: '管理需要认证' })).toBeVisible();
  const [attempt, exchange] = await Promise.all([
    page.waitForResponse((response) => response.url().endsWith('/api/v1/personal/pairing-attempts')),
    page.waitForResponse((response) => response.url().endsWith('/api/v1/personal/pair')),
    page.getByRole('button', { name: '开始配对' }).click()
  ]);
  expect(attempt.status()).toBe(201);
  expect(exchange.status()).toBe(201);
  await expect(page.getByRole('heading', { name: 'Gallery 管理' })).toBeVisible();

  await page.getByRole('link', { name: '扫描与任务', exact: true }).click();
  await expect(page.getByRole('heading', { name: '扫描与任务' })).toBeVisible();

  await page.getByRole('textbox', { name: /Library 名称/ }).fill('真实浏览器资料库');
  const [libraryResponse] = await Promise.all([
    page.waitForResponse(
      (response) => response.request().method() === 'POST' && response.url().endsWith('/api/v1/libraries')
    ),
    page.getByRole('button', { name: '创建 Library' }).click()
  ]);
  expect(libraryResponse.status()).toBe(201);
  const library = (await libraryResponse.json()) as { id: string; name: string };

  await page.getByRole('button', { name: /所属 Library/ }).click();
  await page.getByRole('option', { name: `${library.name} · ${library.id}` }).click();
  await page.getByRole('textbox', { name: /Source 显示名/ }).fill('真实浏览器合成来源');
  await page.getByRole('textbox', { name: /Source 根路径/ }).fill(sourceRoot ?? '');
  const [sourceResponse] = await Promise.all([
    page.waitForResponse(
      (response) => response.request().method() === 'POST' && response.url().endsWith('/api/v1/sources')
    ),
    page.getByRole('button', { name: '登记 Source' }).click()
  ]);
  expect(sourceResponse.status()).toBe(201);
  const source = (await sourceResponse.json()) as { id: string; displayName: string; readOnly: boolean };
  expect(source.readOnly).toBe(true);

  const ruleSetup = await page.evaluate(
    async ({ sourceId, packageValue }) => {
      const bootstrap = (await (await fetch('/api/v1/bootstrap')).json()) as { csrfToken: string };
      const headers = { 'Content-Type': 'application/json', 'X-Gallery-CSRF': bootstrap.csrfToken };
      const versionResponse = await fetch('/api/v1/rule-versions', {
        method: 'POST',
        credentials: 'same-origin',
        headers,
        body: JSON.stringify({ package: packageValue })
      });
      const version = (await versionResponse.json()) as { semanticHash?: string; error?: { code: string } };
      if (versionResponse.status !== 201 || version.semanticHash === undefined) {
        return { versionStatus: versionResponse.status, version, bindingStatus: 0, binding: null };
      }
      const bindingResponse = await fetch('/api/v1/source-rule-bindings', {
        method: 'POST',
        credentials: 'same-origin',
        headers,
        body: JSON.stringify({ sourceId, semanticHash: version.semanticHash, parameters: {}, priority: 0 })
      });
      return {
        versionStatus: versionResponse.status,
        version,
        bindingStatus: bindingResponse.status,
        binding: (await bindingResponse.json()) as unknown
      };
    },
    { sourceId: source.id, packageValue: rulePackage }
  );
  expect(ruleSetup.versionStatus, JSON.stringify(ruleSetup.version)).toBe(201);
  expect(ruleSetup.bindingStatus, JSON.stringify(ruleSetup.binding)).toBe(201);

  await page.getByRole('button', { name: /来源/ }).click();
  await page.getByRole('option', { name: `${source.displayName} · ${source.id}` }).click();
  await page.getByRole('button', { name: /扫描档案/ }).click();
  await page.getByRole('option', { name: 'index（仅首次扫描）' }).click();
  const [scanResponse] = await Promise.all([
    page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        response.url().endsWith(`/api/v1/sources/${source.id}/scan-jobs`)
    ),
    page.getByRole('button', { name: '发起扫描' }).click()
  ]);
  expect(scanResponse.status()).toBe(202);
  expect(scanResponse.request().postDataJSON()).toEqual({ scanProfile: 'index' });
  const createdJob = (await scanResponse.json()) as { id: string };

  let completed: { status: string; queryPublicationId?: string; issueCode?: string } | undefined;
  await expect
    .poll(
      async () => {
        completed = await page.evaluate(async (jobId) => {
          const response = await fetch(`/api/v1/jobs/${encodeURIComponent(jobId)}`);
          return (await response.json()) as {
            status: string;
            queryPublicationId?: string;
            issueCode?: string;
          };
        }, createdJob.id);
        return completed.status;
      },
      { timeout: 30_000 }
    )
    .toMatch(/^(completed|failed|cancelled|superseded|needs_repair)$/);
  expect(completed?.status, JSON.stringify(completed)).toBe('completed');
  expect(completed?.queryPublicationId).toBeTruthy();

  await page.goto(`/manage/scans/${createdJob.id}`);
  await expect(page.getByRole('heading', { name: '任务详情' })).toBeVisible();
  await expect(
    page.locator('dt', { hasText: /^状态$/ }).locator('xpath=following-sibling::dd[1]')
  ).toHaveText('已完成');
  await expect(
    page.locator('dt', { hasText: /^产出快照$/ }).locator('xpath=following-sibling::dd[1]')
  ).toContainText(completed?.queryPublicationId ?? '');

  const currentPublication = await page.evaluate(async () => {
    const response = await fetch('/api/v1/query-publications/current');
    return (await response.json()) as { id: string };
  });
  expect(currentPublication.id).toBe(completed?.queryPublicationId);
});
