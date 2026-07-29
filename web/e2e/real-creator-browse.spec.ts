import { expect, test } from '@playwright/test';

const realBaseURL = process.env.GALLERY_REAL_BASE_URL;
test.skip(!realBaseURL, '仅由隔离真实 E2E 运行器执行');
test.setTimeout(45_000);

test('真实扫描作者按 Source 分页并把范围继承到作品查询 @real-creator-browse', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: '画廊', exact: true })).toBeVisible();
  const pair = page.getByRole('button', { name: '配对本机浏览器' });
  if (await pair.isVisible().catch(() => false)) {
    const [attempt, exchange] = await Promise.all([
      page.waitForResponse((response) => response.url().endsWith('/api/v1/personal/pairing-attempts')),
      page.waitForResponse((response) => response.url().endsWith('/api/v1/personal/pair')),
      pair.click()
    ]);
    expect(attempt.status()).toBe(201);
    expect(exchange.status()).toBe(201);
  }

  const sources = await page.evaluate(async () => {
    const response = await fetch('/api/v1/sources', { credentials: 'same-origin' });
    if (!response.ok) throw new Error(`读取 Source 失败: ${response.status}`);
    return (await response.json()) as {
      sources: Array<{ id: string; displayName: string }>;
    };
  });
  const source = sources.sources.find((item) => item.displayName === '真实浏览器合成来源');
  if (source === undefined) throw new Error('找不到 bootstrap 创建的合成 Source');

  const creatorListResponsePromise = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return (
      response.request().method() === 'GET' &&
      url.pathname === '/api/v1/creators' &&
      url.searchParams.get('sourceId') === source.id
    );
  });
  await page.goto(`/creators?sourceId=${encodeURIComponent(source.id)}`);
  const creatorListResponse = await creatorListResponsePromise;
  expect(creatorListResponse.status()).toBe(200);
  const creatorListURL = new URL(creatorListResponse.url());
  expect(creatorListURL.searchParams.get('includeMerged')).toBe('false');
  expect(creatorListURL.searchParams.get('sort')).toBe('name_asc');
  expect(creatorListURL.searchParams.get('limit')).toBe('48');
  const creatorList = (await creatorListResponse.json()) as {
    creators: Array<{ effectiveId: string; name: string }>;
    nextCursor?: string;
  };
  expect(creatorList.creators.length).toBeGreaterThan(0);
  expect(creatorList.creators.every((creator) => creator.name === 'Synthetic Creator')).toBe(true);
  const creatorItem = creatorList.creators.at(0);
  if (creatorItem === undefined || creatorItem.effectiveId === '') {
    throw new Error('作者列表缺少有效 Creator');
  }
  expect(creatorList.nextCursor).toBeUndefined();
  await expect(page.getByRole('heading', { name: '真实浏览器合成来源 · 创作者', exact: true })).toBeVisible();

  const creatorLink = page
    .getByText('Synthetic Creator', { exact: true })
    .first()
    .locator('xpath=ancestor::a[1]');
  await expect(creatorLink).toBeVisible();
  const creatorHref = await creatorLink.getAttribute('href');
  const creatorURL = new URL(creatorHref ?? '', realBaseURL ?? 'http://127.0.0.1');
  expect(creatorURL.searchParams.get('sourceId')).toBe(source.id);

  const creatorWorksResponsePromise = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return (
      response.request().method() === 'GET' &&
      url.pathname === '/api/v1/works' &&
      url.searchParams.get('sourceId') === source.id &&
      url.searchParams.get('filter') ===
        JSON.stringify({ field: 'creator.id', op: 'eq', value: creatorItem.effectiveId })
    );
  });
  await creatorLink.click();
  const creatorWorksResponse = await creatorWorksResponsePromise;
  expect(creatorWorksResponse.status()).toBe(200);
  const creatorWorks = (await creatorWorksResponse.json()) as { works: unknown[] };
  expect(creatorWorks.works.length).toBeGreaterThan(0);
  await expect(page.getByRole('heading', { name: 'Synthetic Creator', exact: true })).toBeVisible();
});
