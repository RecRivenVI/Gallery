import { expect, test, type Locator, type Page, type Response } from '@playwright/test';

const realBaseURL = process.env.GALLERY_REAL_BASE_URL;
const isolatedSourceRoot = process.env.GALLERY_REAL_SOURCE_ROOT;
const isolatedRulePackage = process.env.GALLERY_REAL_RULE_PACKAGE;
test.skip(
  !realBaseURL || !isolatedSourceRoot || !isolatedRulePackage,
  '仅由已完成空实例 bootstrap 的隔离真实 E2E 运行器执行'
);
test.setTimeout(90_000);

function pathIs(response: Response, path: string, method = 'GET'): boolean {
  return response.request().method() === method && new URL(response.url()).pathname === path;
}

function publicationFromHref(href: string | null): string {
  if (href === null) throw new Error('作品链接缺少 href');
  const publication = new URL(href, realBaseURL).searchParams.get('queryPublicationId');
  if (publication === null || publication === '') throw new Error('作品链接没有携带 queryPublicationId');
  return publication;
}

async function pair(page: Page, destination: string): Promise<void> {
  await page.goto(destination);
  const button = page.getByRole('button', { name: '配对本机浏览器' });
  // 本 spec 由独立 Playwright 进程执行，必定是全新浏览器上下文；先等待 SessionProvider
  // 完成 bootstrap 渲染，不能用非等待式 isVisible() 把“尚未出现”误判成已认证。
  await expect(button).toBeVisible();
  const exchange = page.waitForResponse((response) => response.url().endsWith('/api/v1/personal/pair'));
  await button.click();
  expect((await exchange).status()).toBe(201);
}

async function expectSyntheticImage(image: Locator, width = 2, height = 2): Promise<void> {
  await expect(image).toBeVisible();
  await expect
    .poll(() =>
      image.evaluate(
        (element, dimensions) => {
          const value = element as HTMLImageElement;
          return (
            value.complete &&
            value.naturalWidth === dimensions.width &&
            value.naturalHeight === dimensions.height
          );
        },
        { width, height }
      )
    )
    .toBe(true);
}

test('真实 publication 贯穿浏览、详情、确认、查看与 Range @real-media', async ({ page }) => {
  const mediaRequests: string[] = [];
  page.on('request', (request) => {
    if (/\/api\/v1\/media\/[^/]+\/content$/.test(new URL(request.url()).pathname)) {
      mediaRequests.push(request.url());
    }
  });

  await pair(page, '/browse');
  await expect(page.getByRole('heading', { name: '全部作品' })).toBeVisible();
  const workLink = page.getByText('work-one', { exact: true }).locator('xpath=ancestor::a[1]');
  await expect(workLink).toBeVisible();
  const initialHref = await workLink.getAttribute('href');
  const initialPublication = publicationFromHref(initialHref);
  const initialWorkPath = new URL(initialHref ?? '', realBaseURL).pathname;
  const initialWorkAPIPath = `/api/v1${initialWorkPath}`;
  await expect
    .poll(() =>
      mediaRequests.some(
        (value) => new URL(value).searchParams.get('queryPublicationId') === initialPublication
      )
    )
    .toBe(true);

  const initialDetailPromise = page.waitForResponse((response) => pathIs(response, initialWorkAPIPath));
  const initialMediaPromise = page.waitForResponse((response) =>
    /\/api\/v1\/works\/[^/]+\/media$/.test(new URL(response.url()).pathname)
  );
  await workLink.click();
  const initialDetail = await initialDetailPromise;
  const initialMedia = await initialMediaPromise;
  expect(initialDetail.status()).toBe(200);
  expect(new URL(initialDetail.url()).searchParams.get('queryPublicationId')).toBe(initialPublication);
  expect(initialMedia.status()).toBe(200);
  expect(new URL(initialMedia.url()).searchParams.get('queryPublicationId')).toBe(initialPublication);
  const initialMediaBody = (await initialMedia.json()) as {
    queryPublicationId: string;
    media: Array<{ id: string; contentVerificationState: string }>;
  };
  expect(initialMediaBody.queryPublicationId).toBe(initialPublication);
  expect(initialMediaBody.media).toHaveLength(2);
  const initialMediaItem = initialMediaBody.media.at(0);
  expect(initialMediaItem?.contentVerificationState).toBe('located_unverified');
  expect(initialMediaBody.media.at(1)?.contentVerificationState).toBe('located_unverified');
  await expect(page.getByText('内容未确认', { exact: true }).first()).toBeVisible();

  const mediaId = initialMediaItem?.id;
  if (mediaId === undefined) throw new Error('媒体列表缺少第一项 ID');
  const verificationPromise = page.waitForResponse((response) =>
    pathIs(response, `/api/v1/media/${mediaId}/verification-jobs`, 'POST')
  );
  await page.getByRole('button', { name: '确认内容' }).first().click();
  const verification = await verificationPromise;
  expect(verification.status()).toBe(202);
  expect(new URL(verification.url()).searchParams.get('queryPublicationId')).toBe(initialPublication);
  const verificationJob = (await verification.json()) as { id: string };

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
        }, verificationJob.id);
        return completed.status;
      },
      { timeout: 30_000 }
    )
    .toMatch(/^(completed|failed|cancelled|superseded|needs_repair)$/);
  expect(completed?.status, JSON.stringify(completed)).toBe('completed');
  const verifiedPublication = completed?.queryPublicationId;
  expect(verifiedPublication).toBeTruthy();
  expect(verifiedPublication).not.toBe(initialPublication);
  if (verifiedPublication === undefined) throw new Error('确认任务没有返回新 publication ID');

  // 新 publication 发布后，当前历史页仍必须绑定 P1，不能静默漂移到 P2。
  await expect(page.getByText('内容未确认', { exact: true }).first()).toBeVisible();
  const historicalContent = await page.evaluate(
    async ({ targetMediaId, publication }) => {
      const response = await fetch(
        `/api/v1/media/${encodeURIComponent(targetMediaId)}/content?queryPublicationId=${encodeURIComponent(publication)}`
      );
      return {
        status: response.status,
        code: ((await response.json()) as { error?: { code?: string } }).error?.code
      };
    },
    { targetMediaId: mediaId, publication: initialPublication }
  );
  expect(historicalContent).toEqual({ status: 409, code: 'CONTENT_NOT_VERIFIED' });

  const refreshedListPromise = page.waitForResponse((response) => pathIs(response, '/api/v1/works'));
  await page.goto('/browse');
  const refreshedList = await refreshedListPromise;
  expect(refreshedList.status()).toBe(200);
  const refreshedBody = (await refreshedList.json()) as { queryPublicationId: string };
  expect(refreshedBody.queryPublicationId).toBe(verifiedPublication);
  const refreshedLink = page.getByText('work-one', { exact: true }).locator('xpath=ancestor::a[1]');
  await expect(refreshedLink).toBeVisible();
  const refreshedHref = await refreshedLink.getAttribute('href');
  expect(publicationFromHref(refreshedHref)).toBe(verifiedPublication);
  const workPath = new URL(refreshedHref ?? '', realBaseURL).pathname;
  const workAPIPath = `/api/v1${workPath}`;

  const detailPromise = page.waitForResponse((response) =>
    /\/api\/v1\/works\/[^/]+$/.test(new URL(response.url()).pathname)
  );
  const mediaPromise = page.waitForResponse((response) =>
    /\/api\/v1\/works\/[^/]+\/media$/.test(new URL(response.url()).pathname)
  );
  await refreshedLink.click();
  const detail = await detailPromise;
  const media = await mediaPromise;
  expect(new URL(detail.url()).searchParams.get('queryPublicationId')).toBe(verifiedPublication);
  expect(new URL(media.url()).searchParams.get('queryPublicationId')).toBe(verifiedPublication);
  const mediaBody = (await media.json()) as {
    queryPublicationId: string;
    media: Array<{ id: string; sizeBytes: number; contentVerificationState: string }>;
  };
  expect(mediaBody.queryPublicationId).toBe(verifiedPublication);
  const verifiedMediaItem = mediaBody.media.at(0);
  expect(verifiedMediaItem?.id).toBe(mediaId);
  expect(verifiedMediaItem?.contentVerificationState).toBe('content_verified');
  expect(mediaBody.media.at(1)?.contentVerificationState).toBe('located_unverified');
  const mediaSize = verifiedMediaItem?.sizeBytes;
  if (mediaSize === undefined) throw new Error('已确认媒体缺少 sizeBytes');
  await expectSyntheticImage(page.getByRole('img', { name: '第 1 项媒体' }));

  const contentContract = await page.evaluate(
    async ({ targetMediaId, publication }) => {
      const url =
        `/api/v1/media/${encodeURIComponent(targetMediaId)}/content` +
        `?queryPublicationId=${encodeURIComponent(publication)}`;
      const head = await fetch(url, { method: 'HEAD' });
      const etag = head.headers.get('ETag');
      const full = await fetch(url);
      const fullBytes = new Uint8Array(await full.arrayBuffer());
      const range = await fetch(url, {
        headers: { Range: 'bytes=0-7', ...(etag === null ? {} : { 'If-Range': etag }) }
      });
      const rangeBytes = new Uint8Array(await range.arrayBuffer());
      return {
        headStatus: head.status,
        headType: head.headers.get('Content-Type'),
        headLength: Number(head.headers.get('Content-Length')),
        etagPresent: etag !== null && etag !== '',
        getStatus: full.status,
        getLength: fullBytes.byteLength,
        rangeStatus: range.status,
        acceptRanges: range.headers.get('Accept-Ranges'),
        contentRange: range.headers.get('Content-Range'),
        rangeLength: rangeBytes.byteLength,
        rangeEtagMatches: etag !== null && range.headers.get('ETag') === etag,
        prefixMatches: rangeBytes.every((value, index) => value === fullBytes[index])
      };
    },
    { targetMediaId: mediaId, publication: verifiedPublication }
  );
  expect(contentContract).toEqual({
    headStatus: 200,
    headType: 'image/png',
    headLength: mediaSize,
    etagPresent: true,
    getStatus: 200,
    getLength: mediaSize,
    rangeStatus: 206,
    acceptRanges: 'bytes',
    contentRange: `bytes 0-7/${mediaSize}`,
    rangeLength: 8,
    rangeEtagMatches: true,
    prefixMatches: true
  });
  expect(mediaSize).toBeGreaterThan(8);

  const download = page.getByRole('link', { name: '下载第一项媒体' });
  await expect(download).toHaveAttribute(
    'href',
    `/api/v1/media/${mediaId}/content?queryPublicationId=${verifiedPublication}&download=true`
  );
  const viewerLink = page.locator('a.gal-thumb__link').first();
  await expect(viewerLink).toHaveAttribute('href', new RegExp(`queryPublicationId=${verifiedPublication}$`));
  const viewerHref = await viewerLink.getAttribute('href');
  if (viewerHref === null) throw new Error('Viewer 链接缺少 href');
  const viewerPath = new URL(viewerHref, realBaseURL).pathname;
  await viewerLink.click();
  await expect(page).toHaveURL(new RegExp(`queryPublicationId=${verifiedPublication}$`));
  await expectSyntheticImage(page.getByRole('img', { name: '第 1 项媒体' }));
  const viewerMediaPromise = page.waitForResponse((response) =>
    /\/api\/v1\/works\/[^/]+\/media$/.test(new URL(response.url()).pathname)
  );
  const viewerContentPromise = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return (
      response.request().method() === 'GET' &&
      url.pathname === `/api/v1/media/${mediaId}/content` &&
      url.searchParams.get('queryPublicationId') === verifiedPublication
    );
  });
  await page.reload();
  const viewerMedia = await viewerMediaPromise;
  const viewerContent = await viewerContentPromise;
  expect(viewerMedia.status()).toBe(200);
  expect(new URL(viewerMedia.url()).searchParams.get('queryPublicationId')).toBe(verifiedPublication);
  expect(((await viewerMedia.json()) as { queryPublicationId: string }).queryPublicationId).toBe(
    verifiedPublication
  );
  expect(viewerContent.status()).toBe(200);
  await expectSyntheticImage(page.getByRole('img', { name: '第 1 项媒体' }));
  await expect(page.getByRole('link', { name: '返回作品' })).toHaveAttribute(
    'href',
    new RegExp(`queryPublicationId=${verifiedPublication}$`)
  );

  // 过期/不存在的历史快照必须显式失败，并给用户一个明确的 current 恢复动作。
  const finalNibble = verifiedPublication.at(-1);
  if (finalNibble === undefined) throw new Error('publication ID 为空');
  const bogusPublication = `${verifiedPublication.slice(0, -1)}${finalNibble === '0' ? '1' : '0'}`;
  const expiredPromise = page.waitForResponse(
    (response) =>
      pathIs(response, workAPIPath) &&
      new URL(response.url()).searchParams.get('queryPublicationId') === bogusPublication
  );
  await page.goto(`${workPath}?queryPublicationId=${bogusPublication}`);
  expect((await expiredPromise).status()).toBe(409);
  await expect(page.getByRole('alert').getByText(/^CURSOR_EXPIRED/)).toBeVisible();
  const currentVersion = page.getByRole('link', { name: '打开当前版本' });
  await expect(currentVersion).toHaveAttribute('href', workPath);
  const recoveredPromise = page.waitForResponse(
    (response) =>
      pathIs(response, workAPIPath) && !new URL(response.url()).searchParams.has('queryPublicationId')
  );
  await currentVersion.click();
  const recovered = await recoveredPromise;
  expect(recovered.status()).toBe(200);
  expect(((await recovered.json()) as { queryPublicationId: string }).queryPublicationId).toBe(
    verifiedPublication
  );

  const emptyPublicationPromise = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return pathIs(response, workAPIPath) && url.searchParams.has('queryPublicationId');
  });
  await page.goto(`${workPath}?queryPublicationId=`);
  const emptyPublication = await emptyPublicationPromise;
  expect(emptyPublication.status()).toBe(400);
  expect(((await emptyPublication.json()) as { error: { code: string } }).error.code).toBe(
    'VALIDATION_ERROR'
  );
  await expect(page.getByRole('alert').getByText(/^VALIDATION_ERROR/)).toBeVisible();

  const expiredViewerPromise = page.waitForResponse(
    (response) =>
      pathIs(response, `${workAPIPath}/media`) &&
      new URL(response.url()).searchParams.get('queryPublicationId') === bogusPublication
  );
  await page.goto(`${viewerPath}?queryPublicationId=${bogusPublication}`);
  expect((await expiredViewerPromise).status()).toBe(409);
  await expect(page.getByRole('alert').getByText(/^CURSOR_EXPIRED/)).toBeVisible();
  const currentWorkFromViewer = page.getByRole('link', { name: '打开作品当前版本' });
  await expect(currentWorkFromViewer).toHaveAttribute('href', workPath);
  const recoveredFromViewerPromise = page.waitForResponse(
    (response) =>
      pathIs(response, workAPIPath) && !new URL(response.url()).searchParams.has('queryPublicationId')
  );
  await currentWorkFromViewer.click();
  expect((await recoveredFromViewerPromise).status()).toBe(200);
});
