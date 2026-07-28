import { expect, test, type Page, type Route } from '@playwright/test';

const realBaseURL = process.env.GALLERY_REAL_BASE_URL;
test.skip(!realBaseURL, '仅在显式隔离 galleryd 地址存在时执行');
test.setTimeout(60_000);

interface NetworkProbe {
  oldPending: boolean;
  oldSignalAborted: boolean;
  oldReleased: boolean;
  releaseOldError?: () => void;
}

type NetworkProbeWindow = Window & { __galleryNetworkProbe: NetworkProbe };

async function pair(page: Page): Promise<void> {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: '画廊', exact: true })).toBeVisible();
  const button = page.getByRole('button', { name: '配对本机浏览器', exact: true });
  if (await button.isVisible().catch(() => false)) {
    const exchange = page.waitForResponse((response) =>
      new URL(response.url()).pathname.endsWith('/api/v1/personal/pair')
    );
    await button.click();
    expect((await exchange).status()).toBe(201);
  }
}

async function fulfillFromUnfilteredBackend(route: Route): Promise<void> {
  const url = new URL(route.request().url());
  url.searchParams.delete('q');
  const response = await route.fetch({ url: url.toString() });
  await route.fulfill({ response });
}

test('真实后端查询从传输中断恢复且迟到旧错误不覆盖新搜索 @real-network-degradation', async ({ page }) => {
  await page.addInitScript(() => {
    const probe: NetworkProbe = {
      oldPending: false,
      oldSignalAborted: false,
      oldReleased: false
    };
    (window as unknown as NetworkProbeWindow).__galleryNetworkProbe = probe;
    const nativeFetch = window.fetch.bind(window);
    window.fetch = (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
      const request = new Request(input, init);
      const url = new URL(request.url);
      if (url.pathname === '/api/v1/works' && url.searchParams.get('q') === '会失败的旧查询') {
        probe.oldPending = true;
        request.signal.addEventListener(
          'abort',
          () => {
            probe.oldSignalAborted = true;
          },
          { once: true }
        );
        // 故意不把 AbortSignal 交给传输层：即使调用方已经取消，这个旧结构化错误仍会迟到返回。
        return new Promise<Response>((resolve) => {
          probe.releaseOldError = () => {
            probe.oldReleased = true;
            resolve(
              new Response(
                JSON.stringify({
                  error: { code: 'FORBIDDEN', retryable: false, correlationId: 'corr_late_old_error' }
                }),
                { status: 403, headers: { 'Content-Type': 'application/json' } }
              )
            );
          };
        });
      }
      // Request 构造器会接管带正文请求的 body；后续必须发送这个新 Request，不能再发送已经
      // 被接管正文的原 input，否则配对等 POST 会以 TypeError 失败。
      return nativeFetch(request);
    };
  });

  let interruptedAttempts = 0;
  let delayedResponses = 0;
  let latestResponses = 0;
  await page.route('**/api/v1/works?*', async (route) => {
    const query = new URL(route.request().url()).searchParams.get('q') ?? '';
    if (query === '传输中断后恢复') {
      interruptedAttempts += 1;
      if (interruptedAttempts === 1) {
        await route.abort('failed');
        return;
      }
      delayedResponses += 1;
      await new Promise((resolve) => setTimeout(resolve, 300));
      await fulfillFromUnfilteredBackend(route);
      return;
    }
    if (query === '最新真实查询') {
      latestResponses += 1;
      await fulfillFromUnfilteredBackend(route);
      return;
    }
    await route.fallback();
  });

  await pair(page);
  await page.goto('/browse');
  const firstTitle = page.locator('.gal-card__title').first();
  await expect(firstTitle).toBeVisible();
  const expectedTitle = (await firstTitle.textContent())?.trim();
  expect(expectedTitle).toBeTruthy();
  const search = page.getByRole('searchbox', { name: '搜索作品', exact: true });

  await search.fill('传输中断后恢复');
  await page.getByRole('button', { name: '搜索', exact: true }).click();
  await expect.poll(() => interruptedAttempts, { timeout: 10_000 }).toBe(2);
  expect(delayedResponses).toBe(1);
  await expect(page.locator('.gal-card__title').first()).toHaveText(expectedTitle ?? '', {
    timeout: 10_000
  });

  await search.fill('会失败的旧查询');
  await page.getByRole('button', { name: '搜索', exact: true }).click();
  await expect
    .poll(() =>
      page.evaluate(() => (window as unknown as NetworkProbeWindow).__galleryNetworkProbe.oldPending)
    )
    .toBe(true);

  await search.fill('最新真实查询');
  await page.getByRole('button', { name: '搜索', exact: true }).click();
  await expect.poll(() => latestResponses).toBe(1);
  await expect(page.locator('.gal-card__title').first()).toHaveText(expectedTitle ?? '');
  await expect
    .poll(() =>
      page.evaluate(() => (window as unknown as NetworkProbeWindow).__galleryNetworkProbe.oldSignalAborted)
    )
    .toBe(true);
  await page.evaluate(() => {
    (window as unknown as NetworkProbeWindow).__galleryNetworkProbe.releaseOldError?.();
  });
  await expect
    .poll(() =>
      page.evaluate(() => (window as unknown as NetworkProbeWindow).__galleryNetworkProbe.oldReleased)
    )
    .toBe(true);
  await expect(page.getByText('FORBIDDEN', { exact: true })).toHaveCount(0);
  await expect(page.getByText(/当前账户没有执行此操作的权限/)).toHaveCount(0);
  await expect(page.locator('.gal-card__title').first()).toHaveText(expectedTitle ?? '');
});
