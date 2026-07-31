import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { expect, test, type Page, type Response } from '@playwright/test';

const realBaseURL = process.env.GALLERY_REAL_BASE_URL;
const rulePackagePath = process.env.GALLERY_REAL_RULE_PACKAGE;
test.skip(!realBaseURL || !rulePackagePath, '仅由隔离真实 E2E 运行器执行');
test.setTimeout(120_000);
test.use({ screenshot: 'off', trace: 'off', video: 'off' });

const genericJSONLimit = 1 << 20;
const rulePackageLimit = 8 << 20;
const packageName = '真实浏览器大正文规则';
const invalidSentinel = 'EV-149-CANONICAL-OVER-LIMIT';
const validSentinel = 'EV-149-CANONICAL-RECOVERED';
const utf8Encoder = new TextEncoder();

function sha256(value: string): string {
  return createHash('sha256').update(value).digest('hex');
}

function byteLength(value: string): number {
  return utf8Encoder.encode(value).byteLength;
}

function rulePackageAtBytes(
  sourcePackage: Record<string, unknown>,
  ruleSetId: string,
  targetBytes: number,
  sentinel: string
): string {
  const build = (padding: string) =>
    JSON.stringify({
      ...sourcePackage,
      rule_set_id: ruleSetId,
      extensions: {
        'gallery.transport-limit': { padding }
      }
    });
  const base = build(sentinel);
  const paddingBytes = targetBytes - byteLength(base);
  expect(paddingBytes).toBeGreaterThan(0);
  const result = build(`${'x'.repeat(paddingBytes)}${sentinel}`);
  expect(byteLength(result)).toBe(targetBytes);
  return result;
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

test('EV-148/149 真实 galleryd 对近 8 MiB 草稿保持 canonical 边界与可恢复性 @real-rule-size', async ({
  page
}) => {
  const sourcePackage = JSON.parse(await readFile(rulePackagePath ?? '', 'utf8')) as Record<string, unknown>;
  await pair(page);
  await page.getByRole('link', { name: '规则', exact: true }).click();
  await expect(page.getByRole('heading', { name: '规则', exact: true })).toBeVisible();
  await page.getByRole('textbox', { name: '名称', exact: true }).fill(packageName);
  await page
    .getByRole('textbox', { name: '说明', exact: true })
    .fill('验证规则专用传输预算与前端 UTF-8 边界');
  const [createResponse] = await Promise.all([
    page.waitForResponse((response) => pathIs(response, '/api/v1/rule-packages', 'POST')),
    page.getByRole('button', { name: '创建规则包' }).click()
  ]);
  expect(createResponse.status()).toBe(201);
  const createdPackage = (await createResponse.json()) as {
    id: string;
    name: string;
    ruleSetId: string;
  };
  expect(createdPackage.name).toBe(packageName);
  expect(createdPackage.ruleSetId).toMatch(/^rset_[a-f0-9-]+$/);

  // 原文只比正式上限少 1 字节，但服务端追加两个 hash 后 canonical 必然越界。
  // 保存仍应按既有“无效草稿可编辑”语义成功，且绝不能持久化超限结果。
  const canonicalOverflowText = rulePackageAtBytes(
    sourcePackage,
    createdPackage.ruleSetId,
    rulePackageLimit - 1,
    invalidSentinel
  );
  const recoveredText = rulePackageAtBytes(
    sourcePackage,
    createdPackage.ruleSetId,
    rulePackageLimit - 1024,
    validSentinel
  );

  const packagePath = `/api/v1/rule-packages/${createdPackage.id}`;
  const packageLink = page.getByRole('link', { name: packageName, exact: true });
  await expect(packageLink).toBeVisible();
  const [missingDraft] = await Promise.all([
    page.waitForResponse((response) => pathIs(response, `${packagePath}/draft`)),
    packageLink.click()
  ]);
  expect(missingDraft.status()).toBe(404);
  await page.getByRole('tab', { name: 'JSON 文本' }).click();

  const editor = page.getByRole('textbox', { name: '草稿内容' });
  await editor.fill(canonicalOverflowText);
  const saveButton = page.getByRole('button', { name: '保存草稿' });
  await expect(saveButton).toBeEnabled({ timeout: 30_000 });
  const [overflowResponse] = await Promise.all([
    page.waitForResponse((response) => pathIs(response, `${packagePath}/draft`, 'PUT')),
    saveButton.click()
  ]);
  expect(overflowResponse.status()).toBe(200);
  expect(overflowResponse.request().headers()['if-match']).toBe('"0"');
  const overflowRequest = overflowResponse.request().postDataJSON() as { content: string; format: string };
  expect(overflowRequest.format).toBe('json');
  expect(overflowRequest.content).toBe(canonicalOverflowText);

  // 8 MiB 级 response body 可能被 Chromium inspector cache 回收；生产应用已经在
  // 页面上下文解析同一响应，因此从可见状态和编辑器精确文本核对，不依赖 CDP body。
  const overflowRevision = 1;
  await expect(page.getByText(`草稿已保存（revision ${overflowRevision}）`)).toBeVisible();
  await expect(page.getByText('校验未通过', { exact: true })).toBeVisible();
  await expect(page.locator('.manage-code').filter({ hasText: '规则包大小超限' })).toBeVisible();
  const retainedOverflowText = await editor.inputValue();
  expect(byteLength(retainedOverflowText)).toBeLessThanOrEqual(rulePackageLimit);
  expect(retainedOverflowText).toContain(invalidSentinel);

  // 缩减 1 KiB 后，连同服务端物化 hash 的 canonical 重新落回边界内；同一页面、
  // 同一草稿必须能够继续保存为 validated，证明前一轮没有制造不可恢复状态。
  await editor.fill(recoveredText);
  await expect(saveButton).toBeEnabled({ timeout: 30_000 });
  const [saveResponse] = await Promise.all([
    page.waitForResponse((response) => pathIs(response, `${packagePath}/draft`, 'PUT')),
    saveButton.click()
  ]);
  expect(saveResponse.status()).toBe(200);
  expect(saveResponse.request().headers()['if-match']).toBe(`"${overflowRevision}"`);
  const requestBody = saveResponse.request().postDataJSON() as { content: string; format: string };
  expect(requestBody.format).toBe('json');
  expect(requestBody.content).toBe(recoveredText);

  const savedRevision = overflowRevision + 1;
  await expect(page.getByText(`草稿已保存（revision ${savedRevision}）`)).toBeVisible();
  await expect(page.getByText('校验通过', { exact: true })).toBeVisible();
  const savedContentText = await editor.inputValue();
  expect(byteLength(savedContentText)).toBeGreaterThan(genericJSONLimit);
  expect(byteLength(savedContentText)).toBeLessThanOrEqual(rulePackageLimit);
  expect(savedContentText).toContain(validSentinel);

  const [reloadResponse] = await Promise.all([
    page.waitForResponse((response) => pathIs(response, `${packagePath}/draft`)),
    page.reload()
  ]);
  expect(reloadResponse.status()).toBe(200);
  await page.getByRole('tab', { name: 'JSON 文本' }).click();
  const reloadedText = await page.getByRole('textbox', { name: '草稿内容' }).inputValue();
  expect(byteLength(reloadedText)).toBeGreaterThan(genericJSONLimit);
  expect(byteLength(reloadedText)).toBeLessThanOrEqual(rulePackageLimit);
  expect(reloadedText).toContain(validSentinel);
  expect(sha256(reloadedText)).toBe(sha256(savedContentText));
});
