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
const sentinel = 'EV-148-LARGE-DRAFT-END';

function sha256(value: string): string {
  return createHash('sha256').update(value).digest('hex');
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

test('EV-148 真实 galleryd 完整保存并重载大于 1 MiB 的规则草稿 @real-rule-size', async ({ page }) => {
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

  const largeRulePackage = {
    ...sourcePackage,
    rule_set_id: createdPackage.ruleSetId,
    extensions: {
      'gallery.transport-limit': {
        padding: `${'x'.repeat(genericJSONLimit + 4096)}${sentinel}`
      }
    }
  };
  const largeDraftText = JSON.stringify(largeRulePackage);
  const largeDraftBytes = new TextEncoder().encode(largeDraftText).byteLength;
  expect(largeDraftBytes).toBeGreaterThan(genericJSONLimit);
  expect(largeDraftBytes).toBeLessThan(rulePackageLimit);

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
  await editor.fill(largeDraftText);
  const saveButton = page.getByRole('button', { name: '保存草稿' });
  await expect(saveButton).toBeEnabled({ timeout: 30_000 });
  const [saveResponse] = await Promise.all([
    page.waitForResponse((response) => pathIs(response, `${packagePath}/draft`, 'PUT')),
    saveButton.click()
  ]);
  expect(saveResponse.status()).toBe(200);
  expect(saveResponse.request().headers()['if-match']).toBe('"0"');
  const requestBody = saveResponse.request().postDataJSON() as { content: string; format: string };
  expect(requestBody.format).toBe('json');
  expect(requestBody.content).toBe(largeDraftText);

  const savedDraft = (await saveResponse.json()) as {
    revision: number;
    contentText: string;
  };
  expect(savedDraft.revision).toBeGreaterThan(0);
  expect(new TextEncoder().encode(savedDraft.contentText).byteLength).toBeGreaterThan(genericJSONLimit);
  expect(savedDraft.contentText).toContain(sentinel);
  await expect(page.getByText(`草稿已保存（revision ${savedDraft.revision}）`)).toBeVisible();

  const [reloadResponse] = await Promise.all([
    page.waitForResponse((response) => pathIs(response, `${packagePath}/draft`)),
    page.reload()
  ]);
  expect(reloadResponse.status()).toBe(200);
  await page.getByRole('tab', { name: 'JSON 文本' }).click();
  const reloadedText = await page.getByRole('textbox', { name: '草稿内容' }).inputValue();
  expect(new TextEncoder().encode(reloadedText).byteLength).toBeGreaterThan(genericJSONLimit);
  expect(reloadedText).toContain(sentinel);
  expect(sha256(reloadedText)).toBe(sha256(savedDraft.contentText));
});
