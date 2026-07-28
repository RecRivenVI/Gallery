import { readFile } from 'node:fs/promises';
import { expect, test, type Page, type Response } from '@playwright/test';

const realBaseURL = process.env.GALLERY_REAL_BASE_URL;
const statePath = process.env.GALLERY_REAL_GOVERNANCE_STATE;
test.skip(!realBaseURL || !statePath, '仅由治理消费与孤儿重现后的隔离 Personal galleryd E2E 运行器执行');
test.setTimeout(90_000);
test.use({ screenshot: 'off', video: 'off', trace: 'off' });

interface GovernanceContinuationState {
  keepSameIssueId: string;
  keepSameDecisionId: string;
  keepSameDecisionVersion: number;
  createNewIssueId: string;
  createNewDecisionId: string;
  createNewDecisionVersion: number;
  mergeNewIssueId: string;
  mergeNewDecisionId: string;
  mergeNewDecisionVersion: number;
  orphanOriginalWorkId: string;
  orphanOriginalCreatorId: string;
  orphanOriginalMediaId: string;
  orphanReappearedWorkId: string;
  orphanReappearedCreatorId: string;
  orphanReappearedMediaId: string;
  orphanReappearUnbindKey: string;
  orphanUnboundOldWorkId: string;
  orphanUnboundOldMediaId: string;
  orphanUnboundOldMediaSourceKey: string;
  orphanUnboundNewWorkId: string;
  orphanUnboundNewMediaId: string;
  orphanSourceKey: string;
  orphanCreatorSourceKey: string;
  orphanMediaSourceKey: string;
}

interface StructureDecisionSnapshot {
  decisionId: string;
  issueId: string;
  action: 'split_keep_same' | 'split_create_new' | 'merge_create_new';
  status: 'applied' | 'undone';
  version: number;
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

async function selectOption(page: Page, label: RegExp, option: string): Promise<void> {
  await page.getByRole('button', { name: label }).click();
  await page.getByRole('option', { name: option, exact: true }).click();
}

test('结构决策消费与孤儿重现保持持久身份语义 @real-governance-reappearance', async ({ page }) => {
  const state = JSON.parse(await readFile(statePath ?? '', 'utf8')) as GovernanceContinuationState;
  expect(state.orphanReappearedWorkId).toBe(state.orphanOriginalWorkId);
  expect(state.orphanReappearedCreatorId).toBe(state.orphanOriginalCreatorId);
  expect(state.orphanReappearedMediaId).toBe(state.orphanOriginalMediaId);
  expect(state.orphanUnboundNewWorkId).not.toBe(state.orphanUnboundOldWorkId);
  expect(state.orphanUnboundNewMediaId).not.toBe(state.orphanUnboundOldMediaId);

  await pair(page);
  await expect(page.getByText('实时通道：已连接', { exact: true })).toBeVisible();

  const expected = [
    {
      decisionId: state.keepSameDecisionId,
      issueId: state.keepSameIssueId,
      version: state.keepSameDecisionVersion,
      action: 'split_keep_same'
    },
    {
      decisionId: state.createNewDecisionId,
      issueId: state.createNewIssueId,
      version: state.createNewDecisionVersion,
      action: 'split_create_new'
    },
    {
      decisionId: state.mergeNewDecisionId,
      issueId: state.mergeNewIssueId,
      version: state.mergeNewDecisionVersion,
      action: 'merge_create_new'
    }
  ] as const;
  for (const item of expected) {
    expect(item.decisionId).not.toBe('');
    expect(
      await readJSON<StructureDecisionSnapshot>(page, `/api/v1/source-structure-decisions/${item.decisionId}`)
    ).toMatchObject({
      decisionId: item.decisionId,
      issueId: item.issueId,
      action: item.action,
      status: 'applied',
      version: item.version
    });
  }

  await page.goto('/manage/governance');
  await page.getByRole('tab', { name: '结构决策', exact: true }).click();
  const decisions = page.getByRole('table', { name: '结构决策', exact: true });
  const labels = ['拆分：保持同一作品', '拆分：创建新作品', '合并：创建新作品'];
  for (const [index, item] of expected.entries()) {
    const row = decisions.getByRole('row').filter({ hasText: item.issueId });
    await expect(row).toHaveCount(1);
    await expect(row).toContainText(labels[index] ?? '');
    await expect(row).toContainText('已生效');
  }

  const keepSameRow = decisions.getByRole('row').filter({ hasText: state.keepSameIssueId });
  const undoPromise = page.waitForResponse((response) =>
    pathIs(response, `/api/v1/source-structure-decisions/${state.keepSameDecisionId}/undo`, 'POST')
  );
  await keepSameRow.getByRole('button', { name: '撤回', exact: true }).click();
  await page
    .getByRole('dialog', { name: '撤回结构决策', exact: true })
    .getByRole('button', { name: '确认撤回', exact: true })
    .click();
  const undoResponse = await undoPromise;
  expect(undoResponse.status()).toBe(409);
  expect(undoResponse.request().postDataJSON()).toEqual({ version: state.keepSameDecisionVersion });
  await expect(page.getByText('撤回未能完成', { exact: true })).toBeVisible();

  await page.getByRole('tab', { name: '孤儿候选', exact: true }).click();
  const orphans = page.getByRole('table', { name: '孤儿候选', exact: true });

  await selectOption(page, /实体类型/, '作品');
  for (const sourceKey of [state.orphanSourceKey, state.orphanReappearUnbindKey]) {
    await expect(orphans.getByRole('row').filter({ hasText: sourceKey })).toHaveCount(0);
  }

  await selectOption(page, /实体类型/, '创作者');
  await expect(orphans.getByRole('row').filter({ hasText: state.orphanCreatorSourceKey })).toHaveCount(0);

  await selectOption(page, /实体类型/, '媒体');
  await expect(orphans.getByRole('row').filter({ hasText: state.orphanMediaSourceKey })).toHaveCount(0);
  const oldMediaCandidate = orphans
    .getByRole('row')
    .filter({ hasText: state.orphanUnboundOldMediaSourceKey });
  await expect(oldMediaCandidate).toHaveCount(1);
  await expect(oldMediaCandidate).toContainText('媒体');
});
