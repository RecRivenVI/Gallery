import { readFile } from 'node:fs/promises';
import { expect, test, type Page, type Response } from '@playwright/test';

const realBaseURL = process.env.GALLERY_REAL_BASE_URL;
const rulePackagePath = process.env.GALLERY_REAL_RULE_PACKAGE;
test.skip(!realBaseURL || !rulePackagePath, '仅由已完成 publication 测试的隔离真实 E2E 运行器执行');
test.setTimeout(90_000);

const packageName = '真实浏览器合成规则';
const sourceName = '真实浏览器合成来源';
const parameterName = '真实共享大整数参数';
const firstParameterText = '{"minimumSize":9007199254740993123}';
const secondParameterText = '{"minimumSize":9007199254740993124}';

interface RulePackageSnapshot {
  id: string;
  name: string;
  status: string;
  revision: number;
  currentSemanticHash?: string;
}

interface SourceSnapshot {
  id: string;
  displayName: string;
}

interface JobSnapshot {
  id: string;
  type: string;
  sourceId?: string;
  status: string;
  createdAt: string;
  queryPublicationId?: string;
  ruleSemanticHash?: string;
  ruleParametersHash?: string;
  ruleIrHash?: string;
  issueCode?: string;
}

interface BindingSnapshot {
  id: string;
  semanticHash: string;
  parametersText: string;
  priority: number;
  ruleIrHash: string;
  parameterId?: string;
  parameterRevision?: number;
  parameterHash?: string;
  overrideText?: string;
  status?: string;
}

interface ParameterSetSnapshot {
  id: string;
  name: string;
  semanticHash: string;
  currentRevision: number;
  currentHash: string;
  status: string;
  parametersText: string;
}

function pathIs(response: Response, path: string, method = 'GET'): boolean {
  return response.request().method() === method && new URL(response.url()).pathname === path;
}

async function pair(page: Page): Promise<void> {
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
}

async function readJSON<T>(page: Page, path: string): Promise<T> {
  return page.evaluate(async (target) => {
    const response = await fetch(target);
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
  return job ?? { id: jobId, type: 'scan', status: 'missing', createdAt: '' };
}

function fact(page: Page, term: RegExp) {
  return page.locator('dt', { hasText: term }).locator('xpath=following-sibling::dd[1]');
}

function only<T>(items: T[], description: string): T {
  expect(items, description).toHaveLength(1);
  const item = items.at(0);
  if (item === undefined) throw new Error(`${description}: 未找到唯一项`);
  return item;
}

test('EV-59 真实规则参数、版本和回滚状态链 @real-rule-lifecycle', async ({ page }) => {
  const sourceRulePackage = JSON.parse(await readFile(rulePackagePath ?? '', 'utf8')) as Record<
    string,
    unknown
  >;
  const v1RulePackage = {
    ...sourceRulePackage,
    parameter_schema: {
      type: 'object',
      properties: { minimumSize: { type: 'integer', minimum: 0 } },
      required: ['minimumSize'],
      additionalProperties: false
    }
  };
  await pair(page);

  const packages = await readJSON<{ items: RulePackageSnapshot[] }>(page, '/api/v1/rule-packages');
  const createdPackage = only(
    packages.items.filter((item) => item.name === packageName),
    '固定名称 RulePackage'
  );
  expect(createdPackage.status).toBe('active');
  const v1SemanticHash = createdPackage.currentSemanticHash;
  if (v1SemanticHash === undefined) throw new Error('基线 RulePackage 没有 currentSemanticHash');
  const v1 = { semanticHash: v1SemanticHash };

  const sources = await readJSON<{ sources: SourceSnapshot[] }>(page, '/api/v1/sources');
  const source = only(
    sources.sources.filter((item) => item.displayName === sourceName),
    '固定名称 Source'
  );
  const parameters = await readJSON<{ parameterSets: ParameterSetSnapshot[] }>(
    page,
    `/api/v1/rule-parameters?semanticHash=${encodeURIComponent(v1.semanticHash)}`
  );
  const parameterV1 = only(
    parameters.parameterSets.filter((item) => item.name === parameterName),
    '固定名称 ParameterSet'
  );
  expect(parameterV1).toEqual(
    expect.objectContaining({
      semanticHash: v1.semanticHash,
      currentRevision: 1,
      status: 'active',
      parametersText: firstParameterText
    })
  );
  const bindingV1 = await readJSON<BindingSnapshot>(
    page,
    `/api/v1/sources/${source.id}/effective-rule-binding`
  );
  expect(bindingV1).toEqual(
    expect.objectContaining({
      semanticHash: v1.semanticHash,
      parametersText: firstParameterText,
      parameterId: parameterV1.id,
      parameterRevision: 1,
      parameterHash: parameterV1.currentHash,
      overrideText: '{}',
      status: 'active'
    })
  );
  const initialJobs = await readJSON<{ jobs: JobSnapshot[] }>(page, '/api/v1/jobs?limit=200');
  const job1 = only(
    initialJobs.jobs
      .filter(
        (job) =>
          job.type === 'scan' &&
          job.sourceId === source.id &&
          job.status === 'completed' &&
          job.ruleSemanticHash === v1.semanticHash &&
          job.ruleParametersHash === parameterV1.currentHash
      )
      .sort((left, right) => left.createdAt.localeCompare(right.createdAt))
      .slice(0, 1),
    '首次 index J1'
  );
  expect(job1.queryPublicationId).toBeTruthy();
  expect(job1.ruleIrHash).toBe(bindingV1.ruleIrHash);
  if (job1.queryPublicationId === undefined) throw new Error('J1 缺少 publication');
  const q1 = job1.queryPublicationId;
  const draftV1 = await readJSON<{ revision: number }>(
    page,
    `/api/v1/rule-packages/${createdPackage.id}/draft`
  );
  const validatedRevision = draftV1.revision;
  const publicationBeforeParameterUpdate = (
    await readJSON<{ id: string }>(page, '/api/v1/query-publications/current')
  ).id;

  let scanMutationCount = 0;
  page.on('request', (request) => {
    if (
      request.method() === 'POST' &&
      new URL(request.url()).pathname === `/api/v1/sources/${source.id}/scan-jobs`
    ) {
      scanMutationCount += 1;
    }
  });

  await page.goto(`/manage/rules/${createdPackage.id}`);
  await expect(page.getByRole('heading', { name: '规则包', exact: true })).toBeVisible();
  const parameterEditor = page.getByRole('textbox', {
    name: '参数（精确 JSON 对象文本）',
    exact: true
  });
  await expect(parameterEditor).toHaveValue(firstParameterText);
  await parameterEditor.fill(secondParameterText);
  const [parameterImpactResponse] = await Promise.all([
    page.waitForResponse((response) =>
      pathIs(response, `/api/v1/rule-parameters/${parameterV1.id}/impact`, 'POST')
    ),
    page.getByRole('button', { name: '评估参数影响' }).click()
  ]);
  expect(parameterImpactResponse.status()).toBe(200);
  expect(parameterImpactResponse.request().postDataJSON()).toEqual({ parameters: secondParameterText });
  const parameterImpact = (await parameterImpactResponse.json()) as {
    category: string;
    affectedSources: string[];
    bindingReview: boolean;
    fullRescan: boolean;
    manualConfirmation: boolean;
  };
  expect(parameterImpact).toEqual(
    expect.objectContaining({
      category: 'BINDING_REVIEW',
      affectedSources: [source.id],
      bindingReview: true,
      fullRescan: true,
      manualConfirmation: true
    })
  );
  await expect(fact(page, /^受影响 Source$/)).toHaveText('1');
  const confirmParameterImpact = page.getByRole('checkbox', {
    name: '我已审阅本 revision 与当前参数文本的 Impact，并确认更新共享参数集'
  });
  await confirmParameterImpact.press('Space');
  await expect(confirmParameterImpact).toBeChecked();
  await page.getByRole('button', { name: 'CAS 更新 ParameterSet' }).click();
  const parameterUpdateDialog = page.getByRole('dialog', { name: '更新共享 ParameterSet' });
  const [parameterUpdateResponse] = await Promise.all([
    page.waitForResponse((response) => pathIs(response, `/api/v1/rule-parameters/${parameterV1.id}`, 'PUT')),
    parameterUpdateDialog.getByRole('button', { name: '确认更新共享参数' }).click()
  ]);
  expect(parameterUpdateResponse.status()).toBe(200);
  expect(parameterUpdateResponse.request().headers()['if-match']).toBe('"1"');
  expect(parameterUpdateResponse.request().postDataJSON()).toEqual({
    parameters: secondParameterText,
    expectedRevision: 1,
    confirmImpact: true
  });
  const parameterV2 = (await parameterUpdateResponse.json()) as ParameterSetSnapshot;
  expect(parameterV2).toEqual(
    expect.objectContaining({
      id: parameterV1.id,
      semanticHash: v1.semanticHash,
      currentRevision: 2,
      status: 'active',
      parametersText: secondParameterText
    })
  );
  expect(parameterV2.currentHash).toMatch(/^[a-f0-9]{64}$/);
  expect(parameterV2.currentHash).not.toBe(parameterV1.currentHash);
  expect(scanMutationCount).toBe(0);
  expect((await readJSON<{ id: string }>(page, '/api/v1/query-publications/current')).id).toBe(
    publicationBeforeParameterUpdate
  );
  expect(await readJSON<JobSnapshot>(page, `/api/v1/jobs/${encodeURIComponent(job1.id)}`)).toEqual(job1);
  const jobsAfterParameterUpdate = await readJSON<{ jobs: JobSnapshot[] }>(page, '/api/v1/jobs?limit=200');
  expect(jobsAfterParameterUpdate.jobs.map((job) => job.id).sort()).toEqual(
    initialJobs.jobs.map((job) => job.id).sort()
  );

  await page.getByRole('link', { name: '← 返回规则', exact: true }).click();
  const effectiveRefresh = page.waitForResponse((response) =>
    pathIs(response, `/api/v1/sources/${source.id}/effective-rule-binding`)
  );
  await page.getByRole('button', { name: /来源/ }).click();
  await page.getByRole('option', { name: `${source.displayName} · ${source.id}` }).click();
  const effectiveResponse = await effectiveRefresh;
  expect(effectiveResponse.status()).toBe(200);
  const bindingV2 = (await effectiveResponse.json()) as BindingSnapshot;
  expect(bindingV2).toEqual(
    expect.objectContaining({
      id: bindingV1.id,
      semanticHash: v1.semanticHash,
      parametersText: secondParameterText,
      parameterId: parameterV1.id,
      parameterRevision: 2,
      parameterHash: parameterV2.currentHash,
      overrideText: '{}',
      status: 'active'
    })
  );
  expect(bindingV2.ruleIrHash).not.toBe(bindingV1.ruleIrHash);
  const effectiveBinding = page
    .getByRole('heading', { name: '当前生效的绑定', exact: true })
    .locator('xpath=following-sibling::dl[1]');
  await expect(effectiveBinding).toContainText(parameterV2.currentHash);
  await expect(effectiveBinding).toContainText('2');

  await page.getByRole('link', { name: '扫描与任务', exact: true }).click();
  await expect(page.getByRole('heading', { name: '扫描与任务', exact: true })).toBeVisible();
  await page.getByRole('button', { name: /来源/ }).click();
  await page.getByRole('option', { name: `${source.displayName} · ${source.id}` }).click();
  await page.getByRole('button', { name: /扫描档案/ }).click();
  await page.getByRole('option', { name: 'incremental（默认）' }).click();
  const [scan2Response] = await Promise.all([
    page.waitForResponse((response) => pathIs(response, `/api/v1/sources/${source.id}/scan-jobs`, 'POST')),
    page.getByRole('button', { name: '发起扫描' }).click()
  ]);
  expect(scan2Response.status()).toBe(202);
  expect(scan2Response.request().postDataJSON()).toEqual({ scanProfile: 'incremental' });
  const createdJob2 = (await scan2Response.json()) as { id: string };
  const job2 = await waitForJob(page, createdJob2.id);
  expect(job2.queryPublicationId).toBeTruthy();
  expect(job2.queryPublicationId).not.toBe(publicationBeforeParameterUpdate);
  expect(job2.ruleSemanticHash).toBe(v1.semanticHash);
  expect(job2.ruleParametersHash).toBe(parameterV2.currentHash);
  expect(job2.ruleIrHash).toBe(bindingV2.ruleIrHash);
  if (job2.queryPublicationId === undefined) throw new Error('J2 缺少 publication');
  const q2 = job2.queryPublicationId;
  expect(scanMutationCount).toBe(1);
  expect((await readJSON<{ id: string }>(page, '/api/v1/query-publications/current')).id).toBe(q2);

  await page.goto(`/manage/rules/${createdPackage.id}`);
  await page.getByRole('tab', { name: 'JSON 文本' }).click();
  const v2DraftText = JSON.stringify({ ...v1RulePackage, version: '0.2.0' }, null, 2);
  await page.getByRole('textbox', { name: '草稿内容' }).fill(v2DraftText);
  const [v2SaveResponse] = await Promise.all([
    page.waitForResponse((response) =>
      pathIs(response, `/api/v1/rule-packages/${createdPackage.id}/draft`, 'PUT')
    ),
    page.getByRole('button', { name: '保存草稿' }).click()
  ]);
  expect(v2SaveResponse.status()).toBe(200);
  expect(v2SaveResponse.request().headers()['if-match']).toBe(`"${validatedRevision}"`);
  expect(v2SaveResponse.request().postDataJSON()).toEqual({
    content: v2DraftText,
    format: 'json',
    baseSemanticHash: v1.semanticHash
  });
  const v2SavedDraft = (await v2SaveResponse.json()) as { revision: number };
  const [v2ValidationResponse] = await Promise.all([
    page.waitForResponse((response) =>
      pathIs(response, `/api/v1/rule-packages/${createdPackage.id}/draft/validate`, 'POST')
    ),
    page.getByRole('button', { name: '校验草稿' }).click()
  ]);
  expect(v2ValidationResponse.status()).toBe(200);
  expect(v2ValidationResponse.request().headers()['if-match']).toBe(`"${v2SavedDraft.revision}"`);
  const v2Validation = (await v2ValidationResponse.json()) as {
    valid: boolean;
    draft: { revision: number; contentText: string };
  };
  expect(v2Validation.valid).toBe(true);
  const [v2ImpactResponse] = await Promise.all([
    page.waitForResponse((response) => pathIs(response, '/api/v1/rules/impact', 'POST')),
    page.getByRole('button', { name: '评估本次修改的影响' }).click()
  ]);
  expect(v2ImpactResponse.status()).toBe(200);
  const v2Impact = (await v2ImpactResponse.json()) as { category: string; bindingReview: boolean };
  expect(v2Impact).toEqual(expect.objectContaining({ category: 'RESCAN_FULL', bindingReview: false }));
  const v2PublishReason = '建立兼容第二版本供回滚验证';
  await page.getByRole('textbox', { name: '发布理由' }).fill(v2PublishReason);
  await page.getByRole('button', { name: '发布草稿' }).click();
  const v2PublishDialog = page.getByRole('dialog', { name: '发布规则草稿' });
  const [v2PublishResponse] = await Promise.all([
    page.waitForResponse((response) =>
      pathIs(response, `/api/v1/rule-packages/${createdPackage.id}/publish`, 'POST')
    ),
    v2PublishDialog.getByRole('button', { name: '确认发布' }).click()
  ]);
  expect(v2PublishResponse.status()).toBe(201);
  expect(v2PublishResponse.request().headers()['if-match']).toBe(`"${v2Validation.draft.revision}"`);
  const v2 = (await v2PublishResponse.json()) as { semanticHash: string; status: string };
  expect(v2.semanticHash).toMatch(/^[a-f0-9]{64}$/);
  expect(v2.semanticHash).not.toBe(v1.semanticHash);
  expect(v2.status).toBe('published');

  // publish 响应只证明写入已经提交；RulePackage current 与 RuleVersion 列表是两个独立
  // HTTP snapshot，失效后会并行重取。必须先从可见表确认二者已经收敛到 v2，再打开
  // React Aria Select；否则 Firefox 可能在旧 current 下冻结一次瞬态 option collection。
  const versionTableAfterV2 = page.getByRole('table', { name: 'RuleVersion 列表', exact: true });
  const v2RowAfterPublish = versionTableAfterV2.getByRole('row').filter({ hasText: v2.semanticHash });
  await expect(v2RowAfterPublish.getByText('current', { exact: true })).toBeVisible();

  const versionInUseReason = '确认 active Binding 阻止弃用';
  await page.getByRole('button', { name: /要弃用的 RuleVersion/ }).click();
  await page
    .getByRole('option')
    .filter({ hasText: v1.semanticHash.slice(0, 12) })
    .click();
  await page.getByRole('textbox', { name: '版本弃用理由' }).fill(versionInUseReason);
  await page.getByRole('button', { name: '弃用所选 RuleVersion' }).click();
  const deprecateV1Dialog = page.getByRole('dialog', { name: '弃用不可变 RuleVersion' });
  const [deprecateV1Response] = await Promise.all([
    page.waitForResponse((response) =>
      pathIs(response, `/api/v1/rule-versions/${v1.semanticHash}/deprecate`, 'POST')
    ),
    deprecateV1Dialog.getByRole('button', { name: '确认弃用版本' }).click()
  ]);
  expect(deprecateV1Response.status()).toBe(409);
  expect(((await deprecateV1Response.json()) as { error: { code: string } }).error.code).toBe(
    'RULE_VERSION_IN_USE'
  );

  const packageBeforeRollback = await readJSON<RulePackageSnapshot>(
    page,
    `/api/v1/rule-packages/${createdPackage.id}`
  );
  expect(packageBeforeRollback.currentSemanticHash).toBe(v2.semanticHash);
  const jobsBeforeRollback = await readJSON<{ jobs: JobSnapshot[] }>(page, '/api/v1/jobs?limit=200');
  const bindingBeforeRollback = await readJSON<BindingSnapshot>(
    page,
    `/api/v1/sources/${source.id}/effective-rule-binding`
  );
  const parameterBeforeRollback = await readJSON<ParameterSetSnapshot>(
    page,
    `/api/v1/rule-parameters/${parameterV1.id}`
  );
  const job1BeforeRollback = await readJSON<JobSnapshot>(page, `/api/v1/jobs/${encodeURIComponent(job1.id)}`);
  const job2BeforeRollback = await readJSON<JobSnapshot>(page, `/api/v1/jobs/${encodeURIComponent(job2.id)}`);
  await page.getByRole('button', { name: /回滚到/ }).click();
  const diffPromise = page.waitForResponse((response) =>
    pathIs(response, '/api/v1/rule-versions/diff', 'POST')
  );
  await page
    .getByRole('option')
    .filter({ hasText: v1.semanticHash.slice(0, 12) })
    .click();
  const diffResponse = await diffPromise;
  expect(diffResponse.status()).toBe(200);
  expect(diffResponse.request().postDataJSON()).toEqual({
    oldSemanticHash: v2.semanticHash,
    newSemanticHash: v1.semanticHash
  });
  const rollbackReason = '恢复 V1 current 指针但保留执行快照';
  await page.getByRole('textbox', { name: '回滚理由' }).fill(rollbackReason);
  await page.getByRole('button', { name: '回滚 current 指针' }).click();
  const rollbackDialog = page.getByRole('dialog', { name: '回滚规则包 current 指针' });
  const [rollbackResponse] = await Promise.all([
    page.waitForResponse((response) =>
      pathIs(response, `/api/v1/rule-packages/${createdPackage.id}/rollback`, 'POST')
    ),
    rollbackDialog.getByRole('button', { name: '确认回滚指针' }).click()
  ]);
  expect(rollbackResponse.status()).toBe(200);
  expect(rollbackResponse.request().headers()['if-match']).toBe(`"${packageBeforeRollback.revision}"`);
  expect(rollbackResponse.request().postDataJSON()).toEqual({
    targetSemanticHash: v1.semanticHash,
    expectedRevision: packageBeforeRollback.revision,
    reason: rollbackReason,
    confirmImpact: false
  });
  expect(scanMutationCount).toBe(1);

  const jobsAfterRollback = await readJSON<{ jobs: JobSnapshot[] }>(page, '/api/v1/jobs?limit=200');
  expect(jobsAfterRollback.jobs.map((job) => job.id).sort()).toEqual(
    jobsBeforeRollback.jobs.map((job) => job.id).sort()
  );
  expect(
    await readJSON<BindingSnapshot>(page, `/api/v1/sources/${source.id}/effective-rule-binding`)
  ).toEqual(bindingBeforeRollback);
  expect(await readJSON<ParameterSetSnapshot>(page, `/api/v1/rule-parameters/${parameterV1.id}`)).toEqual(
    parameterBeforeRollback
  );
  expect(await readJSON<JobSnapshot>(page, `/api/v1/jobs/${encodeURIComponent(job1.id)}`)).toEqual(
    job1BeforeRollback
  );
  expect(await readJSON<JobSnapshot>(page, `/api/v1/jobs/${encodeURIComponent(job2.id)}`)).toEqual(
    job2BeforeRollback
  );
  expect((await readJSON<{ id: string }>(page, '/api/v1/query-publications/current')).id).toBe(q2);

  const deprecateV2Reason = '回滚后弃用未绑定 V2';
  await page.getByRole('button', { name: /要弃用的 RuleVersion/ }).click();
  await page
    .getByRole('option')
    .filter({ hasText: v2.semanticHash.slice(0, 12) })
    .click();
  await page.getByRole('textbox', { name: '版本弃用理由' }).fill(deprecateV2Reason);
  await page.getByRole('button', { name: '弃用所选 RuleVersion' }).click();
  const deprecateV2Dialog = page.getByRole('dialog', { name: '弃用不可变 RuleVersion' });
  const [deprecateV2Response] = await Promise.all([
    page.waitForResponse((response) =>
      pathIs(response, `/api/v1/rule-versions/${v2.semanticHash}/deprecate`, 'POST')
    ),
    deprecateV2Dialog.getByRole('button', { name: '确认弃用版本' }).click()
  ]);
  expect(deprecateV2Response.status()).toBe(200);

  const parameterSetSection = page
    .getByRole('heading', { name: '共享 ParameterSet', exact: true })
    .locator('xpath=ancestor::section[1]');
  const parameterVersionSelect = parameterSetSection.locator('button[aria-haspopup="listbox"]').first();
  await parameterVersionSelect.click();
  await page
    .getByRole('option')
    .filter({ hasText: v2.semanticHash.slice(0, 12) })
    .click();
  await expect(
    page.getByText('所选 RuleVersion 已弃用：既有 ParameterSet 仍可查看或弃用，但不能创建、更新或复制。', {
      exact: true
    })
  ).toBeVisible();
  await page.getByRole('textbox', { name: '参数集名称' }).fill('已弃用版本锁定验证');
  await page.getByRole('textbox', { name: '初始参数（精确 JSON 对象文本）' }).fill(secondParameterText);
  await expect(page.getByRole('button', { name: '创建 ParameterSet' })).toBeDisabled();
  await parameterVersionSelect.click();
  await page
    .getByRole('option')
    .filter({ hasText: v1.semanticHash.slice(0, 12) })
    .click();

  const copyName = '真实共享参数副本';
  await page.getByRole('textbox', { name: '副本名称' }).fill(copyName);
  const [copyResponse] = await Promise.all([
    page.waitForResponse((response) =>
      pathIs(response, `/api/v1/rule-parameters/${parameterV1.id}/copy`, 'POST')
    ),
    page.getByRole('button', { name: '复制当前 ParameterSet' }).click()
  ]);
  expect(copyResponse.status()).toBe(201);
  const parameterCopy = (await copyResponse.json()) as ParameterSetSnapshot;
  expect(parameterCopy).toEqual(
    expect.objectContaining({
      name: copyName,
      semanticHash: v1.semanticHash,
      currentRevision: 1,
      currentHash: parameterV2.currentHash,
      status: 'active',
      parametersText: secondParameterText
    })
  );
  await page.getByRole('button', { name: /正在查看的 ParameterSet/ }).click();
  await page
    .getByRole('option')
    .filter({ hasText: `${copyName} · r1 · active` })
    .click();
  const deprecateCopyReason = '清理未绑定参数副本';
  await page.getByRole('textbox', { name: '参数集弃用理由' }).fill(deprecateCopyReason);
  await page.getByRole('button', { name: '弃用当前 ParameterSet' }).click();
  const deprecateCopyDialog = page.getByRole('dialog', { name: '弃用共享 ParameterSet' });
  const [deprecateCopyResponse] = await Promise.all([
    page.waitForResponse((response) =>
      pathIs(response, `/api/v1/rule-parameters/${parameterCopy.id}/deprecate`, 'POST')
    ),
    deprecateCopyDialog.getByRole('button', { name: '确认弃用参数集' }).click()
  ]);
  expect(deprecateCopyResponse.status()).toBe(200);

  const packageBeforeDeprecate = await readJSON<RulePackageSnapshot>(
    page,
    `/api/v1/rule-packages/${createdPackage.id}`
  );
  const deprecatePackageReason = '停止新采用并保留既有执行事实';
  await page.getByRole('textbox', { name: '规则包弃用理由' }).fill(deprecatePackageReason);
  await page.getByRole('button', { name: '永久弃用规则包' }).click();
  const deprecatePackageDialog = page.getByRole('dialog', { name: '永久弃用规则包' });
  const [deprecatePackageResponse] = await Promise.all([
    page.waitForResponse((response) =>
      pathIs(response, `/api/v1/rule-packages/${createdPackage.id}/deprecate`, 'POST')
    ),
    deprecatePackageDialog.getByRole('button', { name: '确认永久弃用' }).click()
  ]);
  expect(deprecatePackageResponse.status()).toBe(200);
  expect(deprecatePackageResponse.request().headers()['if-match']).toBe(
    `"${packageBeforeDeprecate.revision}"`
  );
  expect(deprecatePackageResponse.request().postDataJSON()).toEqual({
    expectedRevision: packageBeforeDeprecate.revision,
    reason: deprecatePackageReason
  });
  expect((await deprecatePackageResponse.json()) as RulePackageSnapshot).toEqual(
    expect.objectContaining({
      id: createdPackage.id,
      status: 'deprecated',
      revision: packageBeforeDeprecate.revision + 1,
      currentSemanticHash: v1.semanticHash
    })
  );
  await expect(page.getByRole('heading', { name: '规则包作者操作已锁定' })).toBeVisible();
  await expect(page.getByText(/该规则包已弃用。草稿、Impact、发布、回滚/)).toBeVisible();
  await expect(page.getByRole('heading', { name: '弃用非当前版本' })).toBeVisible();
  await expect(page.getByRole('heading', { name: '共享 ParameterSet' })).toBeVisible();
  await expect(page.getByRole('button', { name: '保存草稿' })).toHaveCount(0);
  expect(scanMutationCount).toBe(1);
  expect(
    await readJSON<BindingSnapshot>(page, `/api/v1/sources/${source.id}/effective-rule-binding`)
  ).toEqual(bindingBeforeRollback);
  expect(await readJSON<ParameterSetSnapshot>(page, `/api/v1/rule-parameters/${parameterV1.id}`)).toEqual(
    parameterBeforeRollback
  );
  expect((await readJSON<{ id: string }>(page, '/api/v1/query-publications/current')).id).toBe(q2);

  const audits = await readJSON<{
    items: Array<{ subjectType: string; subjectId: string; action: string; reason: string }>;
  }>(page, `/api/v1/rule-packages/${createdPackage.id}/audits`);
  expect(audits.items).toEqual(
    expect.arrayContaining([
      expect.objectContaining({
        subjectType: 'package',
        subjectId: createdPackage.id,
        action: 'rollback',
        reason: rollbackReason
      }),
      expect.objectContaining({
        subjectType: 'package',
        subjectId: createdPackage.id,
        action: 'status_deprecated',
        reason: deprecatePackageReason
      }),
      expect.objectContaining({
        subjectType: 'version',
        subjectId: v2.semanticHash,
        action: 'deprecate',
        reason: deprecateV2Reason
      }),
      expect.objectContaining({
        subjectType: 'parameter_set',
        subjectId: parameterCopy.id,
        action: 'deprecate_parameter',
        reason: deprecateCopyReason
      })
    ])
  );
  const auditUI = page.locator('pre.manage-code');
  await expect(auditUI).toContainText('"action": "rollback"');
  await expect(auditUI).toContainText('"action": "deprecate"');
  await expect(auditUI).toContainText('"action": "deprecate_parameter"');
  await expect(auditUI).toContainText('"action": "status_deprecated"');

  expect(scanMutationCount).toBe(1);
  expect((await readJSON<{ id: string }>(page, '/api/v1/query-publications/current')).id).toBe(q2);
  const worksResponsePromise = page.waitForResponse((response) => pathIs(response, '/api/v1/works'));
  await page.goto('/browse');
  const worksResponse = await worksResponsePromise;
  expect(worksResponse.status()).toBe(200);
  expect(((await worksResponse.json()) as { queryPublicationId: string }).queryPublicationId).toBe(q2);
  await expect(page.getByText('work-one', { exact: true })).toBeVisible();
  expect(q1).not.toBe(q2);
});
