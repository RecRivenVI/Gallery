import { expect, test, type Page, type Response } from '@playwright/test';

const realBaseURL = process.env.GALLERY_REAL_BASE_URL;
const cancelJobId = process.env.GALLERY_REAL_CANCEL_JOB_ID;
const retryJobId = process.env.GALLERY_REAL_RETRY_JOB_ID;
test.skip(
  !realBaseURL || !cancelJobId || !retryJobId,
  '仅由带正式 retry-backoff Job 夹具的隔离 Personal galleryd E2E 运行器执行'
);
test.setTimeout(90_000);
test.use({ screenshot: 'off', video: 'off', trace: 'off' });

const sourceName = '真实浏览器合成来源';
const sourceKey = 'work-one';

interface SourceSnapshot {
  id: string;
  displayName: string;
}

interface BindingSnapshot {
  id: string;
  sourceId: string;
  status?: 'active' | 'paused' | 'invalid';
}

interface JobSnapshot {
  id: string;
  status: string;
  stage: string;
  attempt: number;
  issueCode?: string | null;
  failureRetryable?: boolean;
  cancelRequested?: boolean;
  nextAttemptAt?: string | null;
}

interface JobAttemptSnapshot {
  attemptId: string;
  attempt: number;
  status: string;
  errorCode?: string | null;
  errorRetryable?: boolean;
}

interface BindingActionResult {
  canonicalId: string;
  entityKind: 'work' | 'media';
}

function pathIs(response: Response, path: string, method = 'GET'): boolean {
  return response.request().method() === method && new URL(response.url()).pathname === path;
}

async function pair(page: Page): Promise<void> {
  await page.goto('/manage');
  await expect(page.getByRole('heading', { name: '管理需要认证', exact: true })).toBeVisible();
  const exchange = page.waitForResponse((response) => pathIs(response, '/api/v1/personal/pair', 'POST'));
  await page.getByRole('button', { name: '开始配对', exact: true }).click();
  expect((await exchange).status()).toBe(201);
  await expect(page.getByRole('heading', { name: 'Gallery 管理', exact: true })).toBeVisible();
}

async function readJSON<T>(page: Page, path: string): Promise<T> {
  return page.evaluate(async (target) => {
    const response = await fetch(target, { credentials: 'same-origin' });
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
  return job ?? { id: jobId, status: 'missing', stage: 'missing', attempt: 0 };
}

function only<T>(items: T[], description: string): T {
  expect(items, description).toHaveLength(1);
  const item = items.at(0);
  if (item === undefined) throw new Error(`${description}: 未找到唯一项`);
  return item;
}

function fact(page: Page, term: string) {
  return page.locator('dt', { hasText: term }).locator('xpath=following-sibling::dd[1]').first();
}

test('规则绑定、人工解绑与 retry-backoff Job 真实管理链 @real-jobs-governance', async ({ page }) => {
  await pair(page);
  await expect(page.getByText('实时通道：已连接', { exact: true })).toBeVisible();

  const sources = await readJSON<{ sources: SourceSnapshot[] }>(page, '/api/v1/sources');
  const source = only(
    sources.sources.filter((item) => item.displayName === sourceName),
    '固定名称 Source'
  );
  const bindings = await readJSON<{ bindings: BindingSnapshot[] }>(
    page,
    `/api/v1/source-rule-bindings?sourceId=${encodeURIComponent(source.id)}`
  );
  const binding = only(
    bindings.bindings.filter((item) => (item.status ?? 'active') === 'active'),
    'Source 的 active Binding'
  );

  // 绑定状态通过可见按钮修改；暂停后 effective snapshot 必须失败，恢复后必须回到同一 Binding。
  await page.goto('/manage/rules');
  await expect(page.getByRole('heading', { name: '规则', exact: true })).toBeVisible();
  await page.getByRole('button', { name: /来源/ }).click();
  await page.getByRole('option', { name: `${source.displayName} · ${source.id}`, exact: true }).click();
  const bindingTable = page.getByRole('table', { name: 'Source 规则绑定', exact: true });
  const bindingRow = bindingTable.getByRole('row').filter({ hasText: binding.id });
  await expect(bindingRow).toHaveCount(1);

  const pausePromise = page.waitForResponse((response) =>
    pathIs(response, `/api/v1/source-rule-bindings/${binding.id}`, 'PATCH')
  );
  await bindingRow.getByRole('button', { name: '暂停', exact: true }).click();
  const pauseDialog = page.getByRole('dialog', { name: '暂停规则绑定', exact: true });
  await pauseDialog.getByRole('button', { name: '确认暂停', exact: true }).click();
  const pauseResponse = await pausePromise;
  expect(pauseResponse.status()).toBe(200);
  expect(pauseResponse.request().postDataJSON()).toEqual({ status: 'paused' });
  expect((await pauseResponse.json()) as BindingSnapshot).toMatchObject({ id: binding.id, status: 'paused' });
  await expect(bindingRow.getByText('paused', { exact: true })).toBeVisible();
  await expect(page.getByText(/服务端没有返回生效绑定（NOT_FOUND）/)).toBeVisible();

  const restorePromise = page.waitForResponse((response) =>
    pathIs(response, `/api/v1/source-rule-bindings/${binding.id}`, 'PATCH')
  );
  await bindingRow.getByRole('button', { name: '恢复', exact: true }).click();
  const restoreDialog = page.getByRole('dialog', { name: '恢复规则绑定', exact: true });
  await restoreDialog.getByRole('button', { name: '确认恢复', exact: true }).click();
  const restoreResponse = await restorePromise;
  expect(restoreResponse.status()).toBe(200);
  expect(restoreResponse.request().postDataJSON()).toEqual({ status: 'active' });
  expect((await restoreResponse.json()) as BindingSnapshot).toMatchObject({
    id: binding.id,
    status: 'active'
  });
  await expect(bindingRow.getByText('active', { exact: true })).toBeVisible();
  await expect(page.getByText(/服务端没有返回生效绑定/)).toHaveCount(0);
  await expect(fact(page, '绑定 ID')).toContainText(binding.id);

  // 人工解绑与撤销均从同一可见表单提交，并核对精确 (Source, sourceKey) 请求；撤销恢复后续用例前置。
  await page.goto('/manage/governance');
  await expect(page.getByRole('heading', { name: '治理', exact: true })).toBeVisible();
  await page.getByRole('tab', { name: '人工解绑', exact: true }).click();
  await page.getByRole('button', { name: /来源/ }).click();
  await page.getByRole('option', { name: `${source.displayName} · ${source.id}`, exact: true }).click();
  await page.getByRole('textbox', { name: 'sourceKey', exact: true }).fill(sourceKey);

  const unbindPromise = page.waitForResponse((response) =>
    pathIs(response, '/api/v1/binding-actions/unbind-work', 'POST')
  );
  await page.getByRole('button', { name: '解绑作品', exact: true }).click();
  const unbindDialog = page.getByRole('dialog', { name: '解绑作品', exact: true });
  await unbindDialog.getByRole('button', { name: '确认执行', exact: true }).click();
  const unbindResponse = await unbindPromise;
  expect(unbindResponse.status()).toBe(200);
  expect(unbindResponse.request().postDataJSON()).toEqual({ sourceId: source.id, sourceKey });
  const unbound = (await unbindResponse.json()) as BindingActionResult;
  expect(unbound.entityKind).toBe('work');
  expect(unbound.canonicalId).not.toBe('');
  await expect(page.getByText('绑定动作已完成', { exact: true })).toBeVisible();

  await page.getByRole('button', { name: /动作/ }).click();
  await page.getByRole('option', { name: '撤销解绑', exact: true }).click();
  const undoPromise = page.waitForResponse((response) =>
    pathIs(response, '/api/v1/binding-actions/undo-unbind', 'POST')
  );
  await page.getByRole('button', { name: '撤销解绑', exact: true }).click();
  const undoDialog = page.getByRole('dialog', { name: '撤销解绑', exact: true });
  await undoDialog.getByRole('button', { name: '确认执行', exact: true }).click();
  const undoResponse = await undoPromise;
  expect(undoResponse.status()).toBe(200);
  expect(undoResponse.request().postDataJSON()).toEqual({ sourceId: source.id, sourceKey });
  expect((await undoResponse.json()) as BindingActionResult).toEqual(unbound);

  // 两个 Job 都是运行器通过正式状态机形成的失败 Attempt；不是浏览器伪造出的 DTO。
  await page.goto('/manage/scans');
  await expect(page.getByRole('heading', { name: '扫描与任务', exact: true })).toBeVisible();
  const jobsTable = page.getByRole('table', { name: '任务快照', exact: true });
  const cancelRow = jobsTable.getByRole('row').filter({ hasText: cancelJobId ?? '' });
  await expect(cancelRow).toHaveCount(1);
  await expect(cancelRow.getByText('已失败', { exact: true })).toBeVisible();
  await expect(cancelRow.getByText('E2E_TRANSIENT', { exact: true })).toBeVisible();
  await expect(cancelRow.getByRole('button', { name: '取消', exact: true })).toBeVisible();

  const cancelPromise = page.waitForResponse((response) =>
    pathIs(response, `/api/v1/jobs/${cancelJobId}/cancel`, 'POST')
  );
  await cancelRow.getByRole('button', { name: '取消', exact: true }).click();
  const cancelDialog = page.getByRole('dialog', { name: '取消任务', exact: true });
  await expect(cancelDialog.getByText(/取消后不会再次入队，既有失败 Attempt 保持不变/)).toBeVisible();
  await cancelDialog.getByRole('button', { name: '确认取消', exact: true }).click();
  const cancelResponse = await cancelPromise;
  expect(cancelResponse.status()).toBe(202);
  const cancelled = (await cancelResponse.json()) as JobSnapshot;
  expect(cancelled).toMatchObject({
    id: cancelJobId,
    status: 'cancelled',
    stage: 'cancelled',
    attempt: 1,
    cancelRequested: true,
    failureRetryable: false
  });
  expect(cancelled.nextAttemptAt ?? null).toBeNull();
  await expect(cancelRow.getByText('已取消', { exact: true })).toBeVisible();
  await expect(cancelRow.getByRole('button', { name: '取消', exact: true })).toHaveCount(0);
  await expect(cancelRow.getByRole('button', { name: '重试', exact: true })).toHaveCount(0);

  const cancelAttempts = await readJSON<{ attempts: JobAttemptSnapshot[] }>(
    page,
    `/api/v1/jobs/${cancelJobId}/attempts`
  );
  expect(cancelAttempts.attempts).toEqual([
    expect.objectContaining({
      attempt: 1,
      status: 'failed',
      errorCode: 'E2E_TRANSIENT',
      errorRetryable: true
    })
  ]);

  const retryRow = jobsTable.getByRole('row').filter({ hasText: retryJobId ?? '' });
  await expect(retryRow).toHaveCount(1);
  await expect(retryRow.getByText('已失败', { exact: true })).toBeVisible();
  const retryPromise = page.waitForResponse((response) =>
    pathIs(response, `/api/v1/jobs/${retryJobId}/retry`, 'POST')
  );
  await retryRow.getByRole('button', { name: '重试', exact: true }).click();
  const retryResponse = await retryPromise;
  expect(retryResponse.status()).toBe(202);
  expect((await retryResponse.json()) as JobSnapshot).toMatchObject({
    id: retryJobId,
    status: 'queued',
    attempt: 2
  });
  await waitForJob(page, retryJobId ?? '');
  await expect(retryRow.getByText('已完成', { exact: true })).toBeVisible({ timeout: 15_000 });

  const attempts = await readJSON<{ attempts: JobAttemptSnapshot[] }>(
    page,
    `/api/v1/jobs/${retryJobId}/attempts`
  );
  expect(attempts.attempts).toEqual([
    expect.objectContaining({ attempt: 1, status: 'failed', errorCode: 'E2E_TRANSIENT' }),
    expect.objectContaining({ attempt: 2, status: 'completed' })
  ]);
  await page.goto(`/manage/scans/${retryJobId}`);
  await expect(page.getByRole('heading', { name: '任务详情', exact: true })).toBeVisible();
  await expect(fact(page, '当前 Attempt')).toHaveText('2');
  const attemptsTable = page.getByRole('table', { name: 'Attempt 历史', exact: true });
  for (const attempt of attempts.attempts) {
    const row = attemptsTable.getByRole('row').filter({ hasText: attempt.attemptId });
    await expect(row).toHaveCount(1);
    await expect(
      row.getByText(attempt.status === 'failed' ? '已失败' : '已完成', { exact: true })
    ).toBeVisible();
  }
});
