import { access, writeFile } from 'node:fs/promises';
import { expect, test, type Page, type Response } from '@playwright/test';

const realBaseURL = process.env.GALLERY_REAL_BASE_URL;
const readyFile = process.env.GALLERY_REAL_MEDIA_READ_READY;
const releaseFile = process.env.GALLERY_REAL_MEDIA_READ_RELEASE;
test.skip(!realBaseURL || !readyFile || !releaseFile, '仅由隔离真实 E2E 运行器执行');
test.setTimeout(90_000);

interface WorkList {
  queryPublicationId: string;
  works: Array<{ id: string; title: string }>;
}

interface MediaList {
  queryPublicationId: string;
  media: Array<{ id: string; contentVerificationState: string }>;
}

interface JobSnapshot {
  status: string;
  queryPublicationId?: string;
}

type MediaBusyWindow = Window & {
  __galleryE2EMediaOccupant?: Promise<{ status: number; length: number }>;
};

function pathIs(response: Response, path: string, method = 'GET'): boolean {
  return response.request().method() === method && new URL(response.url()).pathname === path;
}

async function pair(page: Page): Promise<void> {
  await page.goto('/files');
  const button = page.getByRole('button', { name: '配对本机浏览器' });
  await expect(button).toBeVisible();
  const exchange = page.waitForResponse((response) => pathIs(response, '/api/v1/personal/pair', 'POST'));
  await button.click();
  expect((await exchange).status()).toBe(201);
  await expect(page.getByRole('heading', { name: '文件', exact: true })).toBeVisible();
}

async function readJSON<T>(page: Page, path: string): Promise<T> {
  return page.evaluate(async (target) => {
    const response = await fetch(target, { credentials: 'same-origin' });
    if (!response.ok) throw new Error(`只读请求失败: ${response.status}`);
    return (await response.json()) as T;
  }, path);
}

async function exists(path: string): Promise<boolean> {
  try {
    await access(path);
    return true;
  } catch {
    return false;
  }
}

test('真实媒体闸门返回 MEDIA_READ_BUSY 后用户端自动退避恢复 @real-media-busy', async ({ page }) => {
  await pair(page);

  const initial = await readJSON<WorkList>(page, '/api/v1/works?sort=title_asc&limit=100');
  const work = initial.works.find((item) => item.title === 'work-busy');
  if (work === undefined) throw new Error('当前 publication 缺少媒体背压合成作品');

  await page.goto(
    `/works/${encodeURIComponent(work.id)}?queryPublicationId=${encodeURIComponent(initial.queryPublicationId)}`
  );
  await expect(page.getByRole('heading', { name: 'work-busy', exact: true })).toBeVisible();
  await expect(page.getByText('内容未确认', { exact: true })).toBeVisible();
  const verificationResponse = page.waitForResponse((response) =>
    /\/api\/v1\/media\/[^/]+\/verification-jobs$/.test(new URL(response.url()).pathname)
  );
  await page.getByRole('button', { name: '确认内容' }).click();
  const verification = await verificationResponse;
  expect(verification.status()).toBe(202);
  const verificationJob = (await verification.json()) as { id: string };

  let job: JobSnapshot | undefined;
  await expect
    .poll(
      async () => {
        job = await readJSON<JobSnapshot>(page, `/api/v1/jobs/${encodeURIComponent(verificationJob.id)}`);
        return job.status;
      },
      { timeout: 30_000 }
    )
    .toMatch(/^(completed|failed|cancelled|superseded|needs_repair)$/);
  expect(job?.status, JSON.stringify(job)).toBe('completed');
  const publication = job?.queryPublicationId;
  if (publication === undefined || publication === '') throw new Error('媒体确认任务缺少 publication');

  // 回到不渲染媒体的页面，再用原始 fetch 占住唯一真实读取名额。download=true 只用于区分
  // 占位请求与随后由生产 MediaLoader 发出的普通正文请求，不绕过任何服务端逻辑。
  await page.goto('/files');
  const media = await readJSON<MediaList>(
    page,
    `/api/v1/works/${encodeURIComponent(work.id)}/media?queryPublicationId=${encodeURIComponent(publication)}`
  );
  const item = media.media.at(0);
  expect(item?.contentVerificationState).toBe('content_verified');
  if (item === undefined) throw new Error('媒体背压作品缺少已确认媒体');
  const contentPath = `/api/v1/media/${encodeURIComponent(item.id)}/content`;
  // 占位读取必须留在独立标签页；若由即将导航的主页面发出，page.goto 会取消 fetch 并
  // 提前释放服务端闸门，测试就无法区分真正的背压恢复与导航取消。
  const occupantPage = await page.context().newPage();
  await occupantPage.goto('/files');
  await occupantPage.evaluate(
    ({ path, queryPublicationId }) => {
      const target =
        `${path}?queryPublicationId=${encodeURIComponent(queryPublicationId)}` + '&download=true';
      (window as MediaBusyWindow).__galleryE2EMediaOccupant = fetch(target, {
        credentials: 'same-origin'
      }).then(async (response) => ({
        status: response.status,
        length: (await response.arrayBuffer()).byteLength
      }));
    },
    { path: contentPath, queryPublicationId: publication }
  );
  await expect.poll(() => exists(readyFile ?? ''), { timeout: 10_000 }).toBe(true);

  const busyResponse = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return pathIs(response, contentPath) && !url.searchParams.has('download') && response.status() === 503;
  });
  const recoveredResponse = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return pathIs(response, contentPath) && !url.searchParams.has('download') && response.status() === 200;
  });
  await page.goto(
    `/works/${encodeURIComponent(work.id)}?queryPublicationId=${encodeURIComponent(publication)}`
  );
  const busy = await busyResponse;
  expect(await busy.json()).toMatchObject({
    error: { code: 'MEDIA_READ_BUSY', retryable: true }
  });

  // 真实 503 已到达生产加载器后才释放首个请求。加载器会按既定 300 ms 退避自动重试；
  // 不点击人工重试，也不刷新页面。
  await writeFile(releaseFile ?? '', 'release\n', { encoding: 'utf8', flag: 'wx' });
  expect((await recoveredResponse).status()).toBe(200);
  const image = page.getByRole('img', { name: '第 1 项媒体' });
  await expect(image).toBeVisible();
  await expect
    .poll(() =>
      image.evaluate((element) => {
        const value = element as HTMLImageElement;
        return value.complete && value.naturalWidth === 4 && value.naturalHeight === 3;
      })
    )
    .toBe(true);
  await expect(page.getByText('媒体读取通道已满', { exact: true })).toHaveCount(0);

  const occupant = await occupantPage.evaluate(async () => {
    const pending = (window as MediaBusyWindow).__galleryE2EMediaOccupant;
    if (pending === undefined) throw new Error('占位媒体请求不存在');
    return pending;
  });
  expect(occupant.status).toBe(200);
  expect(occupant.length).toBeGreaterThan(8);
  await occupantPage.close();
});
