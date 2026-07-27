import { readFile } from 'node:fs/promises';
import { expect, test, type Response } from '@playwright/test';

const realBaseURL = process.env.GALLERY_REAL_BASE_URL;
const sourceRoot = process.env.GALLERY_REAL_SOURCE_ROOT;
const rulePackagePath = process.env.GALLERY_REAL_RULE_PACKAGE;
test.skip(!realBaseURL || !sourceRoot || !rulePackagePath, '仅由隔离真实 E2E 运行器执行');
test.setTimeout(90_000);

function pathIs(response: Response, path: string, method = 'GET'): boolean {
  return response.request().method() === method && new URL(response.url()).pathname === path;
}

test('真实 galleryd 从空实例完成规则 UI 生命周期、绑定与 publication @real-bootstrap', async ({ page }) => {
  const rulePackage = JSON.parse(await readFile(rulePackagePath ?? '', 'utf8')) as Record<string, unknown>;
  const ruleSetId = rulePackage.rule_set_id;
  if (typeof ruleSetId !== 'string' || ruleSetId === '') {
    throw new Error('隔离规则包缺少有效的 rule_set_id');
  }

  await page.goto('/manage');
  await expect(page.getByRole('heading', { name: '管理需要认证', exact: true })).toBeVisible();
  const [attempt, exchange] = await Promise.all([
    page.waitForResponse((response) => response.url().endsWith('/api/v1/personal/pairing-attempts')),
    page.waitForResponse((response) => response.url().endsWith('/api/v1/personal/pair')),
    page.getByRole('button', { name: '开始配对' }).click()
  ]);
  expect(attempt.status()).toBe(201);
  expect(exchange.status()).toBe(201);
  await expect(page.getByRole('heading', { name: 'Gallery 管理', exact: true })).toBeVisible();

  await page.getByRole('link', { name: '扫描与任务', exact: true }).click();
  await expect(page.getByRole('heading', { name: '扫描与任务', exact: true })).toBeVisible();

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

  await page.getByRole('link', { name: '规则', exact: true }).click();
  await expect(page.getByRole('heading', { name: '规则', exact: true })).toBeVisible();
  const packageName = '真实浏览器合成规则';
  const packageDescription = '由隔离真实浏览器 E2E 创建';
  await page.getByRole('textbox', { name: '名称', exact: true }).fill(packageName);
  await page.getByRole('textbox', { name: 'ruleSetId', exact: true }).fill(ruleSetId);
  await page.getByRole('textbox', { name: '说明', exact: true }).fill(packageDescription);
  const [packageResponse] = await Promise.all([
    page.waitForResponse((response) => pathIs(response, '/api/v1/rule-packages', 'POST')),
    page.getByRole('button', { name: '创建规则包' }).click()
  ]);
  expect(packageResponse.status()).toBe(201);
  expect(packageResponse.request().postDataJSON()).toEqual({
    name: packageName,
    ruleSetId,
    description: packageDescription
  });
  const createdPackage = (await packageResponse.json()) as {
    id: string;
    name: string;
    ruleSetId: string;
  };
  expect(createdPackage.name).toBe(packageName);
  expect(createdPackage.ruleSetId).toBe(ruleSetId);

  const packageLink = page.getByRole('link', { name: packageName, exact: true });
  await expect(packageLink).toBeVisible();
  const [missingDraft] = await Promise.all([
    page.waitForResponse((response) => pathIs(response, `/api/v1/rule-packages/${createdPackage.id}/draft`)),
    packageLink.click()
  ]);
  expect(missingDraft.status()).toBe(404);
  await expect(page.getByRole('heading', { name: '规则包', exact: true })).toBeVisible();
  await expect(page.getByText('尚无草稿', { exact: true }).first()).toBeVisible();

  const draftText = JSON.stringify(rulePackage, null, 2);
  await page.getByRole('textbox', { name: '草稿内容' }).fill(draftText);
  const [saveResponse] = await Promise.all([
    page.waitForResponse((response) =>
      pathIs(response, `/api/v1/rule-packages/${createdPackage.id}/draft`, 'PUT')
    ),
    page.getByRole('button', { name: '保存草稿' }).click()
  ]);
  expect(saveResponse.status()).toBe(200);
  expect(saveResponse.request().headers()['if-match']).toBe('"0"');
  expect(saveResponse.request().postDataJSON()).toEqual({ content: rulePackage, format: 'json' });
  const savedDraft = (await saveResponse.json()) as {
    revision: number;
    format: string;
    content: unknown;
  };
  expect(savedDraft.revision).toBeGreaterThan(0);
  expect(savedDraft.format).toBe('json');
  expect(savedDraft.content).toEqual(expect.objectContaining(rulePackage));
  const canonicalRulePackage = savedDraft.content as Record<string, unknown>;
  expect(canonicalRulePackage.package_hash).toMatch(/^[a-f0-9]{64}$/);
  expect(canonicalRulePackage.semantic_hash).toMatch(/^[a-f0-9]{64}$/);
  const savedRevision = savedDraft.revision;
  const serverDraftRevision = page
    .locator('dt', { hasText: /^服务端草稿 revision$/ })
    .locator('xpath=following-sibling::dd[1]');
  const localDraftRevision = page
    .locator('dt', { hasText: /^本地编辑基于的 revision$/ })
    .locator('xpath=following-sibling::dd[1]');
  await expect(serverDraftRevision).toHaveText(String(savedRevision));
  await expect(localDraftRevision).toHaveText(String(savedRevision));

  const [validationResponse] = await Promise.all([
    page.waitForResponse((response) =>
      pathIs(response, `/api/v1/rule-packages/${createdPackage.id}/draft/validate`, 'POST')
    ),
    page.getByRole('button', { name: '校验草稿' }).click()
  ]);
  expect(validationResponse.status()).toBe(200);
  expect(validationResponse.request().headers()['if-match']).toBe(`"${savedRevision}"`);
  const validation = (await validationResponse.json()) as {
    valid: boolean;
    draft: { revision: number; content: unknown; validationStatus: string };
  };
  expect(validation.valid).toBe(true);
  expect(validation.draft.validationStatus).toBe('validated');
  expect(validation.draft.revision).toBeGreaterThan(savedRevision);
  expect(validation.draft.content).toEqual(canonicalRulePackage);
  const validatedRevision = validation.draft.revision;
  await expect(serverDraftRevision).toHaveText(String(validatedRevision));
  await expect(localDraftRevision).toHaveText(String(validatedRevision));
  await expect(
    page.locator('dt', { hasText: /^校验状态$/ }).locator('xpath=following-sibling::dd[1]')
  ).toHaveText('校验通过');

  const [impactResponse] = await Promise.all([
    page.waitForResponse((response) => pathIs(response, '/api/v1/rules/impact', 'POST')),
    page.getByRole('button', { name: '评估本次修改的影响' }).click()
  ]);
  expect(impactResponse.status()).toBe(200);
  expect(impactResponse.request().postDataJSON()).toEqual({
    before: null,
    after: canonicalRulePackage
  });
  const impact = (await impactResponse.json()) as { category: string; fullRescan: boolean };
  expect(impact.category).toBe('RESCAN_FULL');
  expect(impact.fullRescan).toBe(true);
  await expect(
    page.locator('dt', { hasText: /^结论$/ }).locator('xpath=following-sibling::dd[1]')
  ).toHaveText('需要完整重扫');

  const publishReason = '建立真实浏览器扫描基线';
  await page.getByRole('textbox', { name: '发布理由' }).fill(publishReason);
  await page.getByRole('button', { name: '发布草稿' }).click();
  await expect(page.getByRole('dialog', { name: '发布规则草稿' })).toBeVisible();
  const [publishResponse] = await Promise.all([
    page.waitForResponse((response) =>
      pathIs(response, `/api/v1/rule-packages/${createdPackage.id}/publish`, 'POST')
    ),
    page.getByRole('button', { name: '确认发布' }).click()
  ]);
  expect(publishResponse.status()).toBe(201);
  expect(publishResponse.request().headers()['if-match']).toBe(`"${validatedRevision}"`);
  expect(publishResponse.request().postDataJSON()).toEqual({
    expectedRevision: validatedRevision,
    reason: publishReason,
    confirmImpact: false
  });
  const publishedVersion = (await publishResponse.json()) as { semanticHash: string };
  expect(publishedVersion.semanticHash).toMatch(/^[a-f0-9]{64}$/);
  await expect(page.getByText(publishedVersion.semanticHash, { exact: true }).first()).toBeVisible();

  await page.getByRole('link', { name: '← 返回规则', exact: true }).click();
  await expect(page.getByRole('heading', { name: '规则', exact: true })).toBeVisible();
  await page.getByRole('button', { name: /来源/ }).click();
  await page.getByRole('option', { name: `${source.displayName} · ${source.id}` }).click();
  await page.getByRole('button', { name: /已发布版本/ }).click();
  await page
    .getByRole('option')
    .filter({ hasText: publishedVersion.semanticHash.slice(0, 12) })
    .click();
  await page.getByRole('textbox', { name: 'priority' }).fill('0');
  await page.getByRole('textbox', { name: /参数（规范 JSON 对象）/ }).fill('{}');
  const [bindingResponse] = await Promise.all([
    page.waitForResponse((response) => pathIs(response, '/api/v1/source-rule-bindings', 'POST')),
    page.getByRole('button', { name: '创建绑定' }).click()
  ]);
  expect(bindingResponse.status()).toBe(201);
  expect(bindingResponse.request().postDataJSON()).toEqual({
    sourceId: source.id,
    semanticHash: publishedVersion.semanticHash,
    parameters: {},
    priority: 0
  });
  const binding = (await bindingResponse.json()) as { id: string; semanticHash: string; priority: number };
  expect(binding.semanticHash).toBe(publishedVersion.semanticHash);
  expect(binding.priority).toBe(0);
  const effectiveBinding = page
    .getByRole('heading', { name: '当前生效的绑定', exact: true })
    .locator('xpath=following-sibling::dl[1]');
  await expect(effectiveBinding).toContainText(binding.id);
  await expect(effectiveBinding).toContainText(publishedVersion.semanticHash);
  await expect(effectiveBinding).toContainText('active');

  await page.getByRole('link', { name: '扫描与任务', exact: true }).click();
  await expect(page.getByRole('heading', { name: '扫描与任务', exact: true })).toBeVisible();
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

  let completed:
    | { status: string; queryPublicationId?: string; ruleSemanticHash?: string; issueCode?: string }
    | undefined;
  await expect
    .poll(
      async () => {
        completed = await page.evaluate(async (jobId) => {
          const response = await fetch(`/api/v1/jobs/${encodeURIComponent(jobId)}`);
          return (await response.json()) as {
            status: string;
            queryPublicationId?: string;
            ruleSemanticHash?: string;
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
  expect(completed?.ruleSemanticHash).toBe(publishedVersion.semanticHash);

  await page.goto(`/manage/scans/${createdJob.id}`);
  await expect(page.getByRole('heading', { name: '任务详情', exact: true })).toBeVisible();
  await expect(
    page.locator('dt', { hasText: /^状态$/ }).locator('xpath=following-sibling::dd[1]')
  ).toHaveText('已完成');
  await expect(
    page.locator('dt', { hasText: /^产出快照$/ }).locator('xpath=following-sibling::dd[1]')
  ).toContainText(completed?.queryPublicationId ?? '');
  await expect(
    page.locator('dt', { hasText: /^规则 semanticHash$/ }).locator('xpath=following-sibling::dd[1]')
  ).toHaveText(publishedVersion.semanticHash);

  const currentPublication = await page.evaluate(async () => {
    const response = await fetch('/api/v1/query-publications/current');
    return (await response.json()) as { id: string };
  });
  expect(currentPublication.id).toBe(completed?.queryPublicationId);

  const worksResponsePromise = page.waitForResponse((response) => pathIs(response, '/api/v1/works'));
  await page.goto('/browse');
  const worksResponse = await worksResponsePromise;
  expect(worksResponse.status()).toBe(200);
  const works = (await worksResponse.json()) as { queryPublicationId: string };
  expect(works.queryPublicationId).toBe(completed?.queryPublicationId);
  await expect(page.getByRole('heading', { name: '全部作品', exact: true })).toBeVisible();
  await expect(page.getByText('work-one', { exact: true })).toBeVisible();
});
