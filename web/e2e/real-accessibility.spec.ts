import AxeBuilder from '@axe-core/playwright';
import { expect, test, type Page, type Response } from '@playwright/test';

const realBaseURL = process.env.GALLERY_REAL_BASE_URL;
test.skip(!realBaseURL, '仅由隔离 Personal galleryd E2E 运行器执行');
test.setTimeout(60_000);

// 本用例会短暂显示真实隔离实例签发的一次性 Token。失败诊断只能报告 axe rule ID，
// 不得把 DOM、截图、录像或 trace 中的密文带出临时 AppDirs。
test.use({ screenshot: 'off', video: 'off', trace: 'off' });

const wcagTextSpacingOverride = `
  :where(body, body *) {
    line-height: 1.5 !important;
    letter-spacing: 0.12em !important;
    word-spacing: 0.16em !important;
  }
  p {
    margin-bottom: 2em !important;
  }
`;

function pathIs(response: Response, path: string, method = 'GET'): boolean {
  return response.request().method() === method && new URL(response.url()).pathname === path;
}

async function pairFromVisibleUI(page: Page): Promise<void> {
  await page.goto('/manage/security');
  await expect(page.getByRole('heading', { name: '管理需要认证', exact: true })).toBeVisible();
  const exchange = page.waitForResponse((response) => pathIs(response, '/api/v1/personal/pair', 'POST'));
  await page.getByRole('button', { name: '开始配对', exact: true }).click();
  expect((await exchange).status()).toBe(201);
  await expect(page.getByRole('heading', { name: '连接与安全', exact: true })).toBeVisible();
  // 配对完成会立即换壳。生产按钮不得让 React Aria pending live-announcer 留下引用已卸载按钮的
  // role=img，否则目标交互尚未开始就会出现无替代文本的瞬态节点。
  await expect(page.locator('[data-live-announcer="true"] div[role="img"]')).toHaveCount(0);
}

async function installTextSpacingStylesheet(page: Page): Promise<void> {
  await page.route('**/*.css', async (route) => {
    const response = await route.fetch();
    const body = await response.text();
    await route.fulfill({ response, body: `${body}\n${wcagTextSpacingOverride}` });
  });
}

async function applyAccessibilityEnvironment(page: Page): Promise<void> {
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'high-contrast');
  expect(await page.evaluate(() => window.matchMedia('(forced-colors: active)').matches)).toBe(true);
}

async function expectNoHorizontalOverflow(page: Page, context: string): Promise<void> {
  const overflow = await page.evaluate(() => {
    const clientWidth = document.documentElement.clientWidth;
    const scrollWidth = document.documentElement.scrollWidth;
    const offenders = Array.from(document.querySelectorAll('*'))
      .map((element) => {
        const rect = element.getBoundingClientRect();
        return {
          element: `${element.tagName.toLowerCase()}${element.id ? `#${element.id}` : ''}`,
          left: Math.round(rect.left * 100) / 100,
          right: Math.round(rect.right * 100) / 100,
          width: Math.round(rect.width * 100) / 100
        };
      })
      .filter((item) => item.right > clientWidth + 0.5)
      .sort((left, right) => right.right - left.right)
      .slice(0, 10);
    return { clientWidth, scrollWidth, offenders };
  });
  expect(
    overflow.scrollWidth,
    `${context} 页面横向溢出：clientWidth=${overflow.clientWidth}, scrollWidth=${overflow.scrollWidth}, ` +
      `elements=${JSON.stringify(overflow.offenders)}`
  ).toBeLessThanOrEqual(overflow.clientWidth);
}

async function expectAccessible(page: Page, context: string): Promise<void> {
  await expectNoHorizontalOverflow(page, context);
  const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
  if (results.violations.length !== 0) {
    const summary = results.violations
      .map(
        (item) =>
          `${item.id}(${item.nodes.flatMap((node) => node.target.map((target) => String(target))).join('|')})`
      )
      .join(', ');
    throw new Error(`${context} axe violations: ${summary}`);
  }
}

async function expectDialogFocusContained(page: Page, dialogName: string): Promise<void> {
  const dialog = page.getByRole('dialog', { name: dialogName, exact: true });
  await expect(dialog).toBeVisible();
  await expect.poll(() => dialog.evaluate((element) => element.contains(document.activeElement))).toBe(true);
  for (let index = 0; index < 6; index += 1) {
    await page.keyboard.press(index % 2 === 0 ? 'Tab' : 'Shift+Tab');
    expect(await dialog.evaluate((element) => element.contains(document.activeElement))).toBe(true);
  }
}

test('双入口可见配对及真实 Session 下的关键交互保持可访问 @real-accessibility', async ({ page, browser }) => {
  await page.addInitScript(() => window.localStorage.setItem('gallery.theme', 'high-contrast'));
  await page.emulateMedia({ forcedColors: 'active', contrast: 'more' });
  await page.setViewportSize({ width: 320, height: 800 });
  // 真实 galleryd 的 style-src 不允许测试插入 inline <style>。改写同源 CSS 响应可在不放宽
  // 生产 CSP 的前提下施加 WCAG 文本间距，并让页面继续走真实外部样式加载路径。
  await installTextSpacingStylesheet(page);
  await pairFromVisibleUI(page);
  await applyAccessibilityEnvironment(page);

  await page.getByRole('tab', { name: 'API Token', exact: true }).click();
  const tokenName = 'EV-118 高对比真实 Token';
  await page.getByRole('textbox', { name: '名称', exact: true }).fill(tokenName);
  const capability = page.getByRole('checkbox', { name: 'library.read', exact: true });
  await capability.focus();
  await capability.press('Space');
  await expect(capability).toBeChecked();
  const expiry = page.getByRole('textbox', { name: '有效期（天）', exact: true });
  await expiry.fill('0');
  await expect(page.getByText('必须是正整数', { exact: true })).toBeVisible();
  await expectAccessible(page, '真实 Token 表单校验错误');

  await expiry.fill('1');
  const createResponsePromise = page.waitForResponse((response) =>
    pathIs(response, '/api/v1/api-tokens', 'POST')
  );
  await page.getByRole('button', { name: '创建 Token', exact: true }).click();
  const createResponse = await createResponsePromise;
  expect(createResponse.status()).toBe(201);
  const created = (await createResponse.json()) as { id: string };
  const secretNode = page.getByTestId('one-time-secret');
  await expect(secretNode).toBeVisible();
  // Playwright 即使关闭 screenshot/video/trace，失败时仍可能生成 DOM error-context。
  // 在任何后续可失败断言前原子读取长度并用等长占位符替换，既证明真实密文出现，也避免落盘泄露。
  const secretLength = await secretNode.evaluate((element) => {
    const length = element.textContent.length;
    element.textContent = 'x'.repeat(length);
    return length;
  });
  expect(secretLength).toBeGreaterThanOrEqual(32);
  await expectDialogFocusContained(page, 'API Token 密文');
  await expectAccessible(page, '真实一次性 Token 密文对话框');
  await page.getByRole('button', { name: '我已保存，关闭', exact: true }).click();
  await expect(secretNode).toBeHidden();

  const tokenRow = page.getByRole('row').filter({ hasText: tokenName });
  await expect(tokenRow).toHaveCount(1);
  await tokenRow.getByRole('button', { name: '吊销', exact: true }).click();
  await expectDialogFocusContained(page, '吊销 API Token');
  await expectAccessible(page, '真实 Token 吊销确认对话框');
  const revokeResponsePromise = page.waitForResponse((response) =>
    pathIs(response, `/api/v1/api-tokens/${created.id}`, 'DELETE')
  );
  await page
    .getByRole('dialog', { name: '吊销 API Token', exact: true })
    .getByRole('button', {
      name: '确认吊销',
      exact: true
    })
    .click();
  expect((await revokeResponsePromise).status()).toBe(204);

  await page.goto('/manage/diagnostics');
  await expect(page.getByRole('heading', { name: '验证和诊断', exact: true })).toBeVisible();
  await applyAccessibilityEnvironment(page);
  const retention = page.getByRole('textbox', { name: '保留期（秒）', exact: true });
  await retention.fill('-1');
  await expect(page.getByText('必须是非负整数', { exact: true })).toBeVisible();
  await expectAccessible(page, '真实维护表单校验错误');

  await retention.fill('86400');
  await page.getByRole('button', { name: '创建维护任务', exact: true }).click();
  await expectDialogFocusContained(page, '创建维护任务');
  await expectAccessible(page, '真实维护确认对话框');
  await page
    .getByRole('dialog', { name: '创建维护任务', exact: true })
    .getByRole('button', {
      name: '取消',
      exact: true
    })
    .click();

  const galleryContext = await browser.newContext();
  try {
    await galleryContext.addInitScript(() => window.localStorage.setItem('gallery.theme', 'high-contrast'));
    const galleryPage = await galleryContext.newPage();
    await galleryPage.emulateMedia({ forcedColors: 'active', contrast: 'more' });
    await galleryPage.setViewportSize({ width: 320, height: 800 });
    await installTextSpacingStylesheet(galleryPage);
    await galleryPage.goto('/');
    await expect(galleryPage.getByRole('heading', { name: '画廊', exact: true })).toBeVisible();
    const exchange = galleryPage.waitForResponse((response) =>
      pathIs(response, '/api/v1/personal/pair', 'POST')
    );
    await galleryPage.getByRole('button', { name: '配对本机浏览器', exact: true }).click();
    expect((await exchange).status()).toBe(201);
    await expect(galleryPage.getByRole('button', { name: '配对本机浏览器', exact: true })).toBeHidden();
    await expect(galleryPage.locator('[data-live-announcer="true"] div[role="img"]')).toHaveCount(0);
    await applyAccessibilityEnvironment(galleryPage);
    await expectAccessible(galleryPage, '真实用户端配对后首页');
  } finally {
    await galleryContext.close();
  }
});
