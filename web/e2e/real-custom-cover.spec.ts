import { expect, test, type Locator, type Page, type Response } from '@playwright/test';

const realBaseURL = process.env.GALLERY_REAL_BASE_URL;
const isolatedSourceRoot = process.env.GALLERY_REAL_SOURCE_ROOT;
const isolatedRulePackage = process.env.GALLERY_REAL_RULE_PACKAGE;
test.skip(
  !realBaseURL || !isolatedSourceRoot || !isolatedRulePackage,
  '仅由已完成媒体 P1→P2 的隔离真实 E2E 运行器执行'
);
test.setTimeout(90_000);

interface MediaItem {
  id: string;
  ordinal: number;
  contentVerificationState: string;
}

interface WorkBody {
  id: string;
  coverMediaId: string | null;
  queryPublicationId: string;
}

interface OverlayBody {
  customCoverMediaId?: string;
  projectionStatus: string;
  projectionJobId?: string;
  publishedQueryPublicationId?: string;
}

function pathIs(response: Response, path: string, method = 'GET'): boolean {
  return response.request().method() === method && new URL(response.url()).pathname === path;
}

async function pair(page: Page, destination: string): Promise<void> {
  await page.goto(destination);
  const button = page.getByRole('button', { name: '配对本机浏览器' });
  await expect(button).toBeVisible();
  const exchange = page.waitForResponse((response) => response.url().endsWith('/api/v1/personal/pair'));
  await button.click();
  expect((await exchange).status()).toBe(201);
}

async function waitForJob(
  page: Page,
  jobId: string
): Promise<{ status: string; queryPublicationId?: string }> {
  let job: { status: string; queryPublicationId?: string; issueCode?: string } | undefined;
  await expect
    .poll(
      async () => {
        job = await page.evaluate(async (id) => {
          const response = await fetch(`/api/v1/jobs/${encodeURIComponent(id)}`);
          return (await response.json()) as {
            status: string;
            queryPublicationId?: string;
            issueCode?: string;
          };
        }, jobId);
        return job.status;
      },
      { timeout: 30_000 }
    )
    .toMatch(/^(completed|failed|cancelled|superseded|needs_repair)$/);
  expect(job?.status, JSON.stringify(job)).toBe('completed');
  return job ?? { status: 'missing' };
}

async function fetchWork(page: Page, workId: string, publication: string): Promise<WorkBody> {
  return page.evaluate(
    async ({ id, qpub }) => {
      const response = await fetch(
        `/api/v1/works/${encodeURIComponent(id)}?queryPublicationId=${encodeURIComponent(qpub)}`
      );
      if (!response.ok) throw new Error(`读取作品失败: ${response.status}`);
      return (await response.json()) as WorkBody;
    },
    { id: workId, qpub: publication }
  );
}

async function fetchOverlay(page: Page, workId: string): Promise<OverlayBody> {
  return page.evaluate(async (id) => {
    const response = await fetch(`/api/v1/works/${encodeURIComponent(id)}/overlay`);
    if (!response.ok) throw new Error(`读取 Overlay 失败: ${response.status}`);
    return (await response.json()) as OverlayBody;
  }, workId);
}

async function expectImageDimensions(image: Locator, width: number, height: number): Promise<void> {
  await expect(image).toBeVisible();
  await expect
    .poll(() =>
      image.evaluate((element) => {
        const value = element as HTMLImageElement;
        return value.complete ? [value.naturalWidth, value.naturalHeight] : [0, 0];
      })
    )
    .toEqual([width, height]);
}

async function chooseCover(page: Page, option: string): Promise<void> {
  await page.getByRole('button', { name: /自定义封面/ }).click();
  await page.getByRole('option', { name: option, exact: true }).click();
}

test('CustomCover 设置、历史快照冻结与清除回退 @real-custom-cover', async ({ page }) => {
  await pair(page, '/browse');
  await expect(page.getByRole('heading', { name: '全部作品' })).toBeVisible();
  const workLink = page.getByText('work-one', { exact: true }).locator('xpath=ancestor::a[1]');
  await expect(workLink).toBeVisible();
  const workHref = await workLink.getAttribute('href');
  if (!workHref) throw new Error('当前 publication 的作品链接缺少 href');
  const workURL = new URL(workHref, realBaseURL);
  const mediaPublication = workURL.searchParams.get('queryPublicationId');
  if (!mediaPublication) throw new Error('当前作品链接缺少 publication');
  const workId = decodeURIComponent(workURL.pathname.split('/').at(-1) ?? '');
  if (!workId) throw new Error('当前作品链接缺少 work ID');
  const workPath = `/works/${encodeURIComponent(workId)}`;

  const mediaPromise = page.waitForResponse((response) => pathIs(response, `/api/v1${workPath}/media`));
  await page.goto(`${workPath}?queryPublicationId=${encodeURIComponent(mediaPublication)}`);
  const mediaResponse = await mediaPromise;
  const mediaBody = (await mediaResponse.json()) as { queryPublicationId: string; media: MediaItem[] };
  expect(mediaBody.queryPublicationId).toBe(mediaPublication);
  expect(mediaBody.media).toHaveLength(2);
  const ruleMedia = mediaBody.media.at(0);
  const customMedia = mediaBody.media.at(1);
  if (!ruleMedia || !customMedia) throw new Error('双媒体 fixture 不完整');
  expect(ruleMedia.contentVerificationState).toBe('content_verified');
  expect(customMedia.contentVerificationState).toBe('located_unverified');
  expect((await fetchWork(page, workId, mediaPublication)).coverMediaId).toBe(ruleMedia.id);

  // 先通过真实 UI 确认第二项，确保 CustomCover 的字节在后续 publication 中可读。
  const verificationPromise = page.waitForResponse((response) =>
    pathIs(response, `/api/v1/media/${customMedia.id}/verification-jobs`, 'POST')
  );
  await page.getByRole('button', { name: '确认内容' }).click();
  const verification = await verificationPromise;
  expect(verification.status()).toBe(202);
  const verificationJob = (await verification.json()) as { id: string };
  const verificationResult = await waitForJob(page, verificationJob.id);
  const bothVerifiedPublication = verificationResult.queryPublicationId;
  expect(bothVerifiedPublication).toBeTruthy();
  expect(bothVerifiedPublication).not.toBe(mediaPublication);
  if (!bothVerifiedPublication) throw new Error('第二项确认任务没有返回 publication');

  const bothVerifiedWork = await fetchWork(page, workId, bothVerifiedPublication);
  expect(bothVerifiedWork.coverMediaId).toBe(ruleMedia.id);
  const bothMedia = await page.evaluate(
    async ({ id, qpub }) => {
      const response = await fetch(
        `/api/v1/works/${encodeURIComponent(id)}/media?queryPublicationId=${encodeURIComponent(qpub)}`
      );
      return (await response.json()) as { media: MediaItem[] };
    },
    { id: workId, qpub: bothVerifiedPublication }
  );
  expect(bothMedia.media.map((item) => item.contentVerificationState)).toEqual([
    'content_verified',
    'content_verified'
  ]);

  await page.goto(`${workPath}?queryPublicationId=${encodeURIComponent(bothVerifiedPublication)}`);
  await expect(page.getByRole('heading', { name: 'work-one' })).toBeVisible();
  await chooseCover(page, '第 2 项 · image/png');
  const coverPutPromise = page.waitForResponse((response) =>
    pathIs(response, `/api/v1${workPath}/overlay`, 'PUT')
  );
  await page.getByRole('button', { name: '保存' }).click();
  const coverPut = await coverPutPromise;
  expect(coverPut.status()).toBe(200);
  const coverRequest = coverPut.request().postDataJSON() as Record<string, unknown>;
  expect(coverRequest).toMatchObject({
    titleOverride: '',
    manualTags: [],
    hidden: false,
    favorite: false,
    progress: 0,
    customCoverMediaId: customMedia.id
  });
  const pendingCover = (await coverPut.json()) as OverlayBody;
  expect(pendingCover.projectionStatus).toBe('pending');
  expect(pendingCover.projectionJobId).toBeTruthy();
  if (!pendingCover.projectionJobId) throw new Error('CustomCover 写入没有 projection Job');
  const coverJob = await waitForJob(page, pendingCover.projectionJobId);
  expect(coverJob.queryPublicationId).toBeTruthy();

  // pending-only 轮询必须让界面收敛，并显式给出新快照入口。
  const projectedLink = page.getByRole('link', { name: '打开已投影版本' });
  await expect(projectedLink).toBeVisible({ timeout: 10_000 });
  const customPublication = new URL(
    (await projectedLink.getAttribute('href')) ?? '',
    realBaseURL
  ).searchParams.get('queryPublicationId');
  expect(customPublication).toBeTruthy();
  expect(customPublication).toBe(coverJob.queryPublicationId);
  expect(customPublication).not.toBe(bothVerifiedPublication);
  if (!customPublication) throw new Error('CustomCover 新版本入口缺少 publication');

  // 历史快照必须继续指向规则封面，新快照才使用 CustomCover。
  expect((await fetchWork(page, workId, bothVerifiedPublication)).coverMediaId).toBe(ruleMedia.id);
  expect((await fetchWork(page, workId, customPublication)).coverMediaId).toBe(customMedia.id);
  await projectedLink.click();
  await expect(page).toHaveURL(new RegExp(`queryPublicationId=${customPublication}$`));
  await expect(page.getByText(`本页内容来自快照 ${customPublication}`)).toBeVisible();

  const customListPromise = page.waitForResponse((response) => pathIs(response, '/api/v1/works'));
  const customContentPromise = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return (
      pathIs(response, `/api/v1/media/${customMedia.id}/content`) &&
      url.searchParams.get('queryPublicationId') === customPublication
    );
  });
  await page.goto('/browse');
  const customList = await customListPromise;
  const customListBody = (await customList.json()) as { queryPublicationId: string; works: WorkBody[] };
  expect(customListBody.queryPublicationId).toBe(customPublication);
  expect(customListBody.works.at(0)?.coverMediaId).toBe(customMedia.id);
  expect((await customContentPromise).status()).toBe(200);
  await expectImageDimensions(page.locator('.gal-card__cover img'), 3, 2);

  // 从 P4 详情通过 UI 清除 CustomCover；PUT 省略字段即为清除，而不是发送空字符串。
  await page.goto(`${workPath}?queryPublicationId=${encodeURIComponent(customPublication)}`);
  await chooseCover(page, '不指定（使用规则解析的封面）');
  const clearPutPromise = page.waitForResponse((response) =>
    pathIs(response, `/api/v1${workPath}/overlay`, 'PUT')
  );
  await page.getByRole('button', { name: '保存' }).click();
  const clearPut = await clearPutPromise;
  expect(clearPut.status()).toBe(200);
  const clearRequest = clearPut.request().postDataJSON() as Record<string, unknown>;
  expect(clearRequest).not.toHaveProperty('customCoverMediaId');
  expect(clearRequest).toMatchObject({
    titleOverride: '',
    manualTags: [],
    hidden: false,
    favorite: false,
    progress: 0
  });
  const pendingClear = (await clearPut.json()) as OverlayBody;
  expect(pendingClear.projectionStatus).toBe('pending');
  if (!pendingClear.projectionJobId) throw new Error('清除 CustomCover 没有 projection Job');
  const clearJob = await waitForJob(page, pendingClear.projectionJobId);

  const fallbackLink = page.getByRole('link', { name: '打开已投影版本' });
  await expect(fallbackLink).toBeVisible({ timeout: 10_000 });
  const fallbackPublication = new URL(
    (await fallbackLink.getAttribute('href')) ?? '',
    realBaseURL
  ).searchParams.get('queryPublicationId');
  expect(fallbackPublication).toBeTruthy();
  expect(fallbackPublication).toBe(clearJob.queryPublicationId);
  expect(fallbackPublication).not.toBe(customPublication);
  if (!fallbackPublication) throw new Error('清除后的新版本入口缺少 publication');

  expect((await fetchWork(page, workId, customPublication)).coverMediaId).toBe(customMedia.id);
  expect((await fetchWork(page, workId, fallbackPublication)).coverMediaId).toBe(ruleMedia.id);
  const finalOverlay = await fetchOverlay(page, workId);
  expect(finalOverlay.projectionStatus).toBe('published');
  expect(finalOverlay.publishedQueryPublicationId).toBe(fallbackPublication);
  expect(finalOverlay).not.toHaveProperty('customCoverMediaId');

  const fallbackListPromise = page.waitForResponse((response) => pathIs(response, '/api/v1/works'));
  const fallbackContentPromise = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return (
      pathIs(response, `/api/v1/media/${ruleMedia.id}/content`) &&
      url.searchParams.get('queryPublicationId') === fallbackPublication
    );
  });
  await page.goto('/browse');
  const fallbackList = await fallbackListPromise;
  const fallbackListBody = (await fallbackList.json()) as { queryPublicationId: string; works: WorkBody[] };
  expect(fallbackListBody.queryPublicationId).toBe(fallbackPublication);
  expect(fallbackListBody.works.at(0)?.coverMediaId).toBe(ruleMedia.id);
  expect((await fallbackContentPromise).status()).toBe(200);
  await expectImageDimensions(page.locator('.gal-card__cover img'), 2, 2);
});
