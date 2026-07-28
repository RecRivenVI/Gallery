import { readFile } from 'node:fs/promises';
import { expect, test, type Page, type Response } from '@playwright/test';

const realBaseURL = process.env.GALLERY_REAL_BASE_URL;
const sourceRoot = process.env.GALLERY_REAL_SOURCE_ROOT;
const rulePackagePath = process.env.GALLERY_REAL_RULE_PACKAGE;
test.skip(!realBaseURL || !sourceRoot || !rulePackagePath, '仅由隔离真实 E2E 运行器执行');
test.setTimeout(90_000);

function pathIs(response: Response, path: string, method = 'GET'): boolean {
  return response.request().method() === method && new URL(response.url()).pathname === path;
}

interface JobSnapshot {
  id: string;
  status: string;
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
  return job ?? { id: jobId, status: 'missing' };
}

function fact(page: Page, term: RegExp) {
  return page.locator('dt', { hasText: term }).locator('xpath=following-sibling::dd[1]');
}

test('真实 galleryd 从空实例完成规则 UI 生命周期、绑定与 publication @real-bootstrap', async ({ page }) => {
  const rulePackage = JSON.parse(await readFile(rulePackagePath ?? '', 'utf8')) as Record<string, unknown>;
  const ruleSetId = rulePackage.rule_set_id;
  if (typeof ruleSetId !== 'string' || ruleSetId === '') {
    throw new Error('隔离规则包缺少有效的 rule_set_id');
  }
  const initialRuleVersion = rulePackage.version;
  if (typeof initialRuleVersion !== 'string' || initialRuleVersion === '') {
    throw new Error('隔离规则包缺少有效的 version');
  }
  const firstParameterText = '{"minimumSize":9007199254740993123}';
  const v1RulePackage = {
    ...rulePackage,
    parameter_schema: {
      type: 'object',
      properties: { minimumSize: { type: 'integer', minimum: 0 } },
      required: ['minimumSize'],
      additionalProperties: false
    }
  };

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

  const [schemaResponse, examplesResponse] = await Promise.all([
    page.waitForResponse((response) => pathIs(response, '/api/v1/rules/schema')),
    page.waitForResponse((response) => pathIs(response, '/api/v1/rules/examples')),
    page.getByRole('tab', { name: 'Schema 表单' }).click()
  ]);
  expect(schemaResponse.status()).toBe(200);
  expect(schemaResponse.headers()['content-type']).toContain('application/schema+json');
  expect(examplesResponse.status()).toBe(200);
  await page.getByRole('button', { name: /起始模板/ }).click();
  await page.getByRole('option', { name: '作者—作品—媒体层级' }).click();
  await page.getByRole('button', { name: '载入起始模板' }).click();
  await page.getByRole('textbox', { name: /规则版本/ }).fill('1.0.1');

  await page.getByRole('textbox', { name: '新参数名称' }).fill('minimumSize');
  await page.getByRole('button', { name: '添加参数' }).click();
  await page.getByRole('button', { name: /minimumSize 类型/ }).click();
  await page.getByRole('option', { name: 'integer' }).click();
  await page.getByRole('textbox', { name: 'minimumSize 标题' }).fill('最小文件大小');
  const requiredParameter = page.getByRole('checkbox', { name: '必填参数' });
  await requiredParameter.press('Space');
  await expect(requiredParameter).toBeChecked();

  await page.getByRole('textbox', { name: '新测试 ID' }).fill('missing-metadata');
  await page.getByRole('button', { name: '添加测试' }).click();
  await page.getByRole('textbox', { name: '测试 2 说明' }).fill('metadata 缺失时仍保持稳定身份');

  const newExtension = page.getByRole('textbox', { name: '新 Extension namespace' });
  await newExtension.fill('gallery.identity');
  await page.getByRole('button', { name: '添加 extension' }).click();
  const identityExtension = page
    .getByRole('textbox', { name: 'Extension namespace' })
    .locator('xpath=ancestor::article[1]');
  await expect(identityExtension).toBeVisible();
  await expect(identityExtension.getByRole('textbox', { name: 'Extension namespace' })).toHaveValue(
    'gallery.identity'
  );
  const semanticExtension = identityExtension.getByRole('checkbox', { name: 'semantic' });
  await semanticExtension.press('Space');
  await expect(semanticExtension).toBeChecked();
  await identityExtension.getByRole('textbox', { name: 'gallery.identity version' }).fill('1');
  await identityExtension.getByRole('button', { name: '添加 JSON 属性' }).click();
  await identityExtension.getByRole('textbox', { name: '属性 1 名称' }).fill('stable_key_prefix');
  await identityExtension.getByRole('textbox', { name: '/stable_key_prefix 字符串' }).fill('pixiv:');

  await newExtension.fill('example.lossless');
  await page.getByRole('button', { name: '添加 extension' }).click();
  const losslessExtension = page
    .getByRole('textbox', { name: 'Extension namespace' })
    .nth(1)
    .locator('xpath=ancestor::article[1]');
  await expect(losslessExtension).toBeVisible();
  await expect(losslessExtension.getByRole('textbox', { name: 'Extension namespace' })).toHaveValue(
    'example.lossless'
  );
  await losslessExtension.getByRole('button', { name: '添加 JSON 属性' }).click();
  await losslessExtension.getByRole('textbox', { name: '属性 1 名称' }).fill('value');
  await losslessExtension.getByRole('button', { name: '字符串 /value 类型' }).click();
  await page.getByRole('option', { name: '数字' }).click();
  const exactNumber = losslessExtension.getByRole('textbox', {
    name: '/value（精确 JSON 数字）'
  });
  await exactNumber.fill('1e');
  await expect(page.getByRole('button', { name: '保存草稿' })).toBeDisabled();
  await exactNumber.fill('0.12345678901234567890');
  await expect(page.getByRole('button', { name: '保存草稿' })).toBeEnabled();

  await page.getByRole('textbox', { name: '调试参数 JSON' }).fill('{"minimumSize":0}');
  const [structuredDryRunResponse] = await Promise.all([
    page.waitForResponse((response) => pathIs(response, '/api/v1/rules/dry-run', 'POST')),
    page.getByRole('button', { name: '执行 Dry Run' }).click()
  ]);
  expect(structuredDryRunResponse.status()).toBe(200);
  await expect(page.getByLabel('Dry Run 作品结果')).toBeVisible();
  await page.getByRole('button', { name: '查看 Explain' }).click();
  await expect(page.getByLabel('Explain 字段来源')).toBeVisible();
  await page.getByRole('button', { name: '查看 Trace' }).click();
  await expect(page.getByLabel('Trace 步骤')).toBeVisible();
  const visualWorkGlob = page.getByRole('textbox', { name: /作品目录 glob/ }).first();
  await expect(visualWorkGlob).toBeVisible();
  await visualWorkGlob.fill('visual-proof/*');
  await expect(page.getByText('有未保存修改', { exact: true })).toBeVisible();
  await page.getByRole('tab', { name: 'JSON 文本' }).click();
  const textEditor = page.getByRole('textbox', { name: '草稿内容' });
  const templatedText = await textEditor.inputValue();
  expect(templatedText).toContain(ruleSetId);
  expect(templatedText).toContain('"glob": "visual-proof/*"');
  expect(templatedText).toContain('"minimumSize"');
  expect(templatedText).toContain('"type": "integer"');
  expect(templatedText).toContain('"missing-metadata"');
  expect(templatedText).toContain('"gallery.identity"');
  expect(templatedText).toContain('"stable_key_prefix": "pixiv:"');
  expect(templatedText).toContain('0.12345678901234567890');
  expect(templatedText).not.toContain('package_hash');
  expect(templatedText).not.toContain('semantic_hash');

  const draftText = JSON.stringify(v1RulePackage, null, 2);
  await textEditor.fill(draftText);
  const [saveResponse] = await Promise.all([
    page.waitForResponse((response) =>
      pathIs(response, `/api/v1/rule-packages/${createdPackage.id}/draft`, 'PUT')
    ),
    page.getByRole('button', { name: '保存草稿' }).click()
  ]);
  expect(saveResponse.status()).toBe(200);
  expect(saveResponse.request().headers()['if-match']).toBe('"0"');
  expect(saveResponse.request().postDataJSON()).toEqual({ content: draftText, format: 'json' });
  const savedDraft = (await saveResponse.json()) as {
    revision: number;
    format: string;
    content: unknown;
    contentText: string;
  };
  expect(savedDraft.revision).toBeGreaterThan(0);
  expect(savedDraft.format).toBe('json');
  expect(savedDraft.content).toEqual(expect.objectContaining(v1RulePackage));
  const canonicalRulePackage = savedDraft.content as Record<string, unknown>;
  expect(canonicalRulePackage.package_hash).toMatch(/^[a-f0-9]{64}$/);
  expect(canonicalRulePackage.semantic_hash).toMatch(/^[a-f0-9]{64}$/);
  expect(savedDraft.contentText).toBe(JSON.stringify(canonicalRulePackage));
  const savedRevision = savedDraft.revision;
  const serverDraftRevision = page
    .locator('dt', { hasText: /^服务端草稿 revision$/ })
    .locator('xpath=following-sibling::dd[1]');
  const localDraftRevision = page
    .locator('dt', { hasText: /^本地编辑基于的 revision$/ })
    .locator('xpath=following-sibling::dd[1]');
  await expect(serverDraftRevision).toHaveText(String(savedRevision));
  await expect(localDraftRevision).toHaveText(String(savedRevision));

  await page.getByRole('tab', { name: 'Schema 表单' }).click();
  await expect(page.getByText('当前没有可撤销的字段修改。')).toBeVisible();
  const savedVersion = page.getByRole('textbox', { name: /规则版本/ });
  await savedVersion.fill('1.0.2');
  await expect(page.getByRole('button', { name: '撤销字段 /version' })).toBeVisible();
  await page.getByRole('button', { name: '撤销字段 /version' }).click();
  await expect(savedVersion).toHaveValue(initialRuleVersion);
  await expect(page.getByText('当前没有可撤销的字段修改。')).toBeVisible();
  await expect(page.getByText('已与服务端同步', { exact: true })).toBeVisible();

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
    draft: { revision: number; content: unknown; contentText: string; validationStatus: string };
  };
  expect(validation.valid).toBe(true);
  expect(validation.draft.validationStatus).toBe('validated');
  expect(validation.draft.revision).toBeGreaterThan(savedRevision);
  expect(validation.draft.content).toEqual(canonicalRulePackage);
  expect(validation.draft.contentText).toBe(savedDraft.contentText);
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
    after: savedDraft.contentText
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
  const v1 = (await publishResponse.json()) as { semanticHash: string; status: string };
  expect(v1.semanticHash).toMatch(/^[a-f0-9]{64}$/);
  expect(v1.status).toBe('published');
  await expect(page.getByText(v1.semanticHash, { exact: true }).first()).toBeVisible();

  const parameterName = '真实共享大整数参数';
  const createParameterButton = page.getByRole('button', { name: '创建 ParameterSet' });
  await page.getByRole('textbox', { name: '参数集名称' }).fill(parameterName);
  await page.getByRole('textbox', { name: '初始参数（精确 JSON 对象文本）' }).fill(firstParameterText);
  await expect(createParameterButton).toBeEnabled();
  const [createParameterResponse] = await Promise.all([
    page.waitForResponse((response) => pathIs(response, '/api/v1/rule-parameters', 'POST')),
    createParameterButton.click()
  ]);
  expect(createParameterResponse.status()).toBe(201);
  expect(createParameterResponse.request().postDataJSON()).toEqual({
    name: parameterName,
    semanticHash: v1.semanticHash,
    parameters: firstParameterText
  });
  const parameterV1 = (await createParameterResponse.json()) as ParameterSetSnapshot;
  expect(parameterV1).toEqual(
    expect.objectContaining({
      name: parameterName,
      semanticHash: v1.semanticHash,
      currentRevision: 1,
      status: 'active',
      parametersText: firstParameterText
    })
  );
  expect(parameterV1.currentHash).toMatch(/^[a-f0-9]{64}$/);
  await expect(fact(page, /^服务器 revision$/)).toHaveText('1');
  await expect(fact(page, /^parameterHash$/)).toContainText(parameterV1.currentHash);

  await page.getByRole('link', { name: '← 返回规则', exact: true }).click();
  await expect(page.getByRole('heading', { name: '规则', exact: true })).toBeVisible();
  await page.getByRole('button', { name: /来源/ }).click();
  await page.getByRole('option', { name: `${source.displayName} · ${source.id}` }).click();
  await page.getByRole('button', { name: /绑定参数来源/ }).click();
  await page.getByRole('option', { name: 'ParameterSet · 共享参数集 + override' }).click();
  await page.getByRole('button', { name: /active ParameterSet/ }).click();
  await page
    .getByRole('option')
    .filter({ hasText: `${parameterName} · r1 · ${v1.semanticHash.slice(0, 12)}…` })
    .click();
  await page.getByRole('textbox', { name: 'priority' }).fill('0');
  await page.getByRole('textbox', { name: /override（精确 JSON 对象文本）/ }).fill('{}');
  const [bindingResponse] = await Promise.all([
    page.waitForResponse((response) => pathIs(response, '/api/v1/source-rule-bindings', 'POST')),
    page.getByRole('button', { name: '创建绑定' }).click()
  ]);
  expect(bindingResponse.status()).toBe(201);
  expect(bindingResponse.request().postDataJSON()).toEqual({
    sourceId: source.id,
    parameterId: parameterV1.id,
    override: '{}',
    priority: 0
  });
  const bindingV1 = (await bindingResponse.json()) as BindingSnapshot;
  expect(bindingV1).toEqual(
    expect.objectContaining({
      semanticHash: v1.semanticHash,
      parametersText: firstParameterText,
      priority: 0,
      parameterId: parameterV1.id,
      parameterRevision: 1,
      parameterHash: parameterV1.currentHash,
      overrideText: '{}',
      status: 'active'
    })
  );
  const effectiveBinding = page
    .getByRole('heading', { name: '当前生效的绑定', exact: true })
    .locator('xpath=following-sibling::dl[1]');
  await expect(effectiveBinding).toContainText(bindingV1.id);
  await expect(effectiveBinding).toContainText(parameterV1.id);
  await expect(effectiveBinding).toContainText(parameterV1.currentHash);
  await expect(effectiveBinding).toContainText('active');

  let scanMutationCount = 0;
  page.on('request', (request) => {
    if (
      request.method() === 'POST' &&
      new URL(request.url()).pathname === `/api/v1/sources/${source.id}/scan-jobs`
    ) {
      scanMutationCount += 1;
    }
  });

  await page.getByRole('link', { name: '扫描与任务', exact: true }).click();
  await expect(page.getByRole('heading', { name: '扫描与任务', exact: true })).toBeVisible();
  await page.getByRole('button', { name: /来源/ }).click();
  await page.getByRole('option', { name: `${source.displayName} · ${source.id}` }).click();
  await page.getByRole('button', { name: /扫描档案/ }).click();
  await page.getByRole('option', { name: 'index（仅首次扫描）' }).click();
  const [scan1Response] = await Promise.all([
    page.waitForResponse((response) => pathIs(response, `/api/v1/sources/${source.id}/scan-jobs`, 'POST')),
    page.getByRole('button', { name: '发起扫描' }).click()
  ]);
  expect(scan1Response.status()).toBe(202);
  expect(scan1Response.request().postDataJSON()).toEqual({ scanProfile: 'index' });
  const createdJob1 = (await scan1Response.json()) as { id: string };
  const job1 = await waitForJob(page, createdJob1.id);
  expect(job1.queryPublicationId).toBeTruthy();
  expect(job1.ruleSemanticHash).toBe(v1.semanticHash);
  expect(job1.ruleParametersHash).toBe(parameterV1.currentHash);
  expect(job1.ruleIrHash).toBe(bindingV1.ruleIrHash);
  if (job1.queryPublicationId === undefined) throw new Error('J1 缺少 publication');
  const q1 = job1.queryPublicationId;

  await page.goto(`/manage/scans/${job1.id}`);
  await expect(page.getByRole('heading', { name: '任务详情', exact: true })).toBeVisible();
  await expect(fact(page, /^状态$/)).toHaveText('已完成');
  await expect(fact(page, /^产出快照$/)).toContainText(q1);
  await expect(fact(page, /^规则 semanticHash$/)).toContainText(v1.semanticHash);
  await expect(fact(page, /^参数 hash$/)).toContainText(parameterV1.currentHash);

  expect(scanMutationCount).toBe(1);
  expect((await readJSON<{ id: string }>(page, '/api/v1/query-publications/current')).id).toBe(q1);

  const worksResponsePromise = page.waitForResponse((response) => pathIs(response, '/api/v1/works'));
  await page.goto('/browse');
  const worksResponse = await worksResponsePromise;
  expect(worksResponse.status()).toBe(200);
  const works = (await worksResponse.json()) as { queryPublicationId: string };
  expect(works.queryPublicationId).toBe(q1);
  await expect(page.getByRole('heading', { name: '全部作品', exact: true })).toBeVisible();
  const workLink = page.getByText('work-one', { exact: true }).locator('xpath=ancestor::a[1]');
  await expect(workLink).toBeVisible();
  const workHref = await workLink.getAttribute('href');
  const workId = new URL(workHref ?? '', realBaseURL ?? 'http://127.0.0.1').pathname.split('/').at(-1);
  if (workId === undefined || workId === '') throw new Error('初始 browse 缺少 Work ID');
  const media = await readJSON<{
    queryPublicationId: string;
    media: Array<{ contentVerificationState: string }>;
  }>(page, `/api/v1/works/${encodeURIComponent(workId)}/media?queryPublicationId=${encodeURIComponent(q1)}`);
  expect(media.queryPublicationId).toBe(q1);
  expect(media.media).toHaveLength(2);
  expect(media.media.every((item) => item.contentVerificationState === 'located_unverified')).toBe(true);
});
