import { readFile } from 'node:fs/promises';
import { expect, test, type Locator, type Page, type Response } from '@playwright/test';

const realBaseURL = process.env.GALLERY_REAL_BASE_URL;
const statePath = process.env.GALLERY_REAL_GOVERNANCE_STATE;
test.skip(!realBaseURL || !statePath, '仅由带正式应用层治理夹具的隔离 Personal galleryd E2E 运行器执行');
test.setTimeout(120_000);
test.use({ screenshot: 'off', video: 'off', trace: 'off' });

interface GovernanceFixtureState {
  issueSourceId: string;
  issueSourceName: string;
  issueId: string;
  issueSourceKey: string;
  structureSourceId: string;
  structureSourceName: string;
  structureIssueId: string;
  structureTargetSourceKey: string;
  mergeSourceId: string;
  mergeSourceName: string;
  mergeIssueId: string;
  mergeTargetWorkId: string;
  consumedDecisionId: string;
  consumedDecisionIssueId: string;
  consumedDecisionVersion: number;
  orphanSourceId: string;
  orphanSourceName: string;
  orphanBindingId: string;
  orphanSourceKey: string;
  orphanUnbindBindingId: string;
  orphanUnbindSourceKey: string;
  orphanCreatorBindingId: string;
  orphanCreatorSourceKey: string;
  orphanMediaBindingId: string;
  orphanMediaSourceKey: string;
  mediaSourceId: string;
  mediaSourceName: string;
  mediaSourceKey: string;
}

interface BindingIssueSnapshot {
  id: string;
  sourceId: string;
  sourceKey: string;
  status: 'open' | 'resolved' | 'dismissed' | 'superseded' | 'stale';
  version: number;
}

interface StructureDecisionSnapshot {
  decisionId: string;
  issueId: string;
  status: 'applied' | 'undone';
  version: number;
}

interface OrphanDecisionResult {
  bindingId: string;
  entityType: string;
  decision: string;
  newStatus: string;
  canonicalId: string;
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

async function selectOption(page: Page, scope: Page | Locator, label: RegExp, option: string): Promise<void> {
  await scope.getByRole('button', { name: label }).click();
  await page.getByRole('option', { name: option, exact: true }).click();
}

test('绑定问题、结构决策、孤儿与媒体解绑真实治理链 @real-governance', async ({ page }) => {
  const state = JSON.parse(await readFile(statePath ?? '', 'utf8')) as GovernanceFixtureState;
  await pair(page);
  await expect(page.getByText('实时通道：已连接', { exact: true })).toBeVisible();

  const initialIssue = await readJSON<BindingIssueSnapshot>(page, `/api/v1/binding-issues/${state.issueId}`);
  expect(initialIssue).toMatchObject({
    id: state.issueId,
    sourceId: state.issueSourceId,
    sourceKey: state.issueSourceKey,
    status: 'open'
  });

  await page.goto('/manage/governance');
  await expect(page.getByRole('heading', { name: '治理', exact: true })).toBeVisible();
  await selectOption(page, page, /来源/, `${state.issueSourceName} · ${state.issueSourceId}`);
  const issuesTable = page.getByRole('table', { name: '绑定问题', exact: true });
  let issueRow = issuesTable.getByRole('row').filter({ hasText: state.issueSourceKey });
  await expect(issueRow).toHaveCount(1);

  const dismissPromise = page.waitForResponse((response) =>
    pathIs(response, `/api/v1/binding-issues/${state.issueId}/dismiss`, 'POST')
  );
  await issueRow.getByRole('button', { name: '忽略', exact: true }).click();
  await page
    .getByRole('dialog', { name: '忽略绑定问题', exact: true })
    .getByRole('button', { name: '确认忽略', exact: true })
    .click();
  const dismissResponse = await dismissPromise;
  expect(dismissResponse.status()).toBe(200);
  expect(dismissResponse.request().postDataJSON()).toEqual({ version: initialIssue.version });
  const dismissed = (await dismissResponse.json()) as BindingIssueSnapshot;
  expect(dismissed).toMatchObject({
    id: state.issueId,
    status: 'dismissed',
    version: initialIssue.version + 1
  });

  await selectOption(page, page, /状态/, '已忽略');
  issueRow = issuesTable.getByRole('row').filter({ hasText: state.issueSourceKey });
  await expect(issueRow).toHaveCount(1);
  const reopenPromise = page.waitForResponse((response) =>
    pathIs(response, `/api/v1/binding-issues/${state.issueId}/reopen`, 'POST')
  );
  await issueRow.getByRole('button', { name: '重新打开', exact: true }).click();
  const reopenResponse = await reopenPromise;
  expect(reopenResponse.status()).toBe(200);
  expect(reopenResponse.request().postDataJSON()).toEqual({ version: dismissed.version });
  const reopened = (await reopenResponse.json()) as BindingIssueSnapshot;
  expect(reopened).toMatchObject({ id: state.issueId, status: 'open', version: dismissed.version + 1 });

  await selectOption(page, page, /状态/, '待处理');
  issueRow = issuesTable.getByRole('row').filter({ hasText: state.issueSourceKey });
  await expect(issueRow).toHaveCount(1);
  await issueRow.getByRole('button', { name: '修复', exact: true }).click();
  const resolveDialog = page.getByRole('dialog', { name: '修复绑定问题', exact: true });
  await selectOption(page, resolveDialog, /决定/, '创建新的 Canonical 实体');
  const resolvePromise = page.waitForResponse((response) =>
    pathIs(response, `/api/v1/binding-issues/${state.issueId}/resolve`, 'POST')
  );
  await resolveDialog.getByRole('button', { name: '提交修复', exact: true }).click();
  const resolveResponse = await resolvePromise;
  expect(resolveResponse.status()).toBe(200);
  expect(resolveResponse.request().postDataJSON()).toEqual({
    decision: 'create_new',
    version: reopened.version
  });
  const resolved = (await resolveResponse.json()) as BindingIssueSnapshot;
  expect(resolved).toMatchObject({ id: state.issueId, status: 'resolved', version: reopened.version + 1 });

  await selectOption(page, page, /状态/, '已修复');
  issueRow = issuesTable.getByRole('row').filter({ hasText: state.issueSourceKey });
  await expect(issueRow).toHaveCount(1);
  await expect(issueRow.getByRole('button', { name: '重新打开', exact: true })).toHaveCount(0);

  await selectOption(page, page, /来源/, `${state.structureSourceName} · ${state.structureSourceId}`);
  await selectOption(page, page, /状态/, '待处理');
  const structureIssueRow = issuesTable.getByRole('row').filter({ hasText: 'wkA' });
  await expect(structureIssueRow).toHaveCount(1);
  await structureIssueRow.getByRole('button', { name: '确认结构', exact: true }).click();
  const structureDialog = page.getByRole('dialog', { name: '确认作品拆分', exact: true });
  await structureDialog
    .getByRole('textbox', { name: '目标 sourceKey（可选）', exact: true })
    .fill(state.structureTargetSourceKey);
  const structurePromise = page.waitForResponse((response) =>
    pathIs(response, `/api/v1/binding-issues/${state.structureIssueId}/resolve-structure`, 'POST')
  );
  await structureDialog.getByRole('button', { name: '提交决策', exact: true }).click();
  const structureResponse = await structurePromise;
  expect(structureResponse.status()).toBe(200);
  expect(structureResponse.request().postDataJSON()).toEqual({
    action: 'split_inherit',
    version: 1,
    targetSourceKey: state.structureTargetSourceKey
  });
  const structureDecision = (await structureResponse.json()) as StructureDecisionSnapshot;
  expect(structureDecision).toMatchObject({ issueId: state.structureIssueId, status: 'applied', version: 1 });

  await page.getByRole('tab', { name: '结构决策', exact: true }).click();
  const decisionsTable = page.getByRole('table', { name: '结构决策', exact: true });
  const decisionRow = decisionsTable.getByRole('row').filter({ hasText: state.structureIssueId });
  await expect(decisionRow).toHaveCount(1);
  const undoStructurePromise = page.waitForResponse((response) =>
    pathIs(response, `/api/v1/source-structure-decisions/${structureDecision.decisionId}/undo`, 'POST')
  );
  await decisionRow.getByRole('button', { name: '撤回', exact: true }).click();
  await page
    .getByRole('dialog', { name: '撤回结构决策', exact: true })
    .getByRole('button', { name: '确认撤回', exact: true })
    .click();
  const undoStructureResponse = await undoStructurePromise;
  expect(undoStructureResponse.status()).toBe(200);
  expect(undoStructureResponse.request().postDataJSON()).toEqual({ version: structureDecision.version });
  expect((await undoStructureResponse.json()) as StructureDecisionSnapshot).toMatchObject({
    decisionId: structureDecision.decisionId,
    status: 'undone',
    version: structureDecision.version + 1
  });
  expect(
    await readJSON<BindingIssueSnapshot>(page, `/api/v1/binding-issues/${state.structureIssueId}`)
  ).toMatchObject({
    id: state.structureIssueId,
    status: 'open'
  });

  await page.getByRole('tab', { name: '绑定问题', exact: true }).click();
  await selectOption(page, page, /来源/, `${state.mergeSourceName} · ${state.mergeSourceId}`);
  await selectOption(page, page, /状态/, '待处理');
  const mergeIssueRow = issuesTable.getByRole('row').filter({ hasText: 'wkM' });
  await expect(mergeIssueRow).toHaveCount(1);
  await mergeIssueRow.getByRole('button', { name: '确认结构', exact: true }).click();
  const mergeDialog = page.getByRole('dialog', { name: '确认作品合并', exact: true });
  await mergeDialog
    .getByRole('textbox', { name: '目标 Work ID（可选）', exact: true })
    .fill(state.mergeTargetWorkId);
  const mergePromise = page.waitForResponse((response) =>
    pathIs(response, `/api/v1/binding-issues/${state.mergeIssueId}/resolve-structure`, 'POST')
  );
  await mergeDialog.getByRole('button', { name: '提交决策', exact: true }).click();
  const mergeResponse = await mergePromise;
  expect(mergeResponse.status()).toBe(200);
  expect(mergeResponse.request().postDataJSON()).toEqual({
    action: 'merge_bind_existing',
    version: 1,
    targetWorkId: state.mergeTargetWorkId
  });
  const mergeDecision = (await mergeResponse.json()) as StructureDecisionSnapshot;
  expect(mergeDecision).toMatchObject({ issueId: state.mergeIssueId, status: 'applied', version: 1 });

  await page.getByRole('tab', { name: '结构决策', exact: true }).click();
  const mergeDecisionRow = decisionsTable.getByRole('row').filter({ hasText: state.mergeIssueId });
  await expect(mergeDecisionRow).toHaveCount(1);
  const undoMergePromise = page.waitForResponse((response) =>
    pathIs(response, `/api/v1/source-structure-decisions/${mergeDecision.decisionId}/undo`, 'POST')
  );
  await mergeDecisionRow.getByRole('button', { name: '撤回', exact: true }).click();
  await page
    .getByRole('dialog', { name: '撤回结构决策', exact: true })
    .getByRole('button', { name: '确认撤回', exact: true })
    .click();
  const undoMergeResponse = await undoMergePromise;
  expect(undoMergeResponse.status()).toBe(200);
  expect(undoMergeResponse.request().postDataJSON()).toEqual({ version: mergeDecision.version });
  expect((await undoMergeResponse.json()) as StructureDecisionSnapshot).toMatchObject({
    decisionId: mergeDecision.decisionId,
    status: 'undone',
    version: mergeDecision.version + 1
  });

  const consumedDecisionRow = decisionsTable
    .getByRole('row')
    .filter({ hasText: state.consumedDecisionIssueId });
  await expect(consumedDecisionRow).toHaveCount(1);
  const undoConsumedPromise = page.waitForResponse((response) =>
    pathIs(response, `/api/v1/source-structure-decisions/${state.consumedDecisionId}/undo`, 'POST')
  );
  await consumedDecisionRow.getByRole('button', { name: '撤回', exact: true }).click();
  await page
    .getByRole('dialog', { name: '撤回结构决策', exact: true })
    .getByRole('button', { name: '确认撤回', exact: true })
    .click();
  const undoConsumedResponse = await undoConsumedPromise;
  expect(undoConsumedResponse.status()).toBe(409);
  expect(undoConsumedResponse.request().postDataJSON()).toEqual({ version: state.consumedDecisionVersion });
  await expect(page.getByText('撤回未能完成', { exact: true })).toBeVisible();
  await expect(
    page.getByText('资源已被其他操作改变，请刷新后基于最新状态重试。', { exact: true })
  ).toBeVisible();

  await page.getByRole('tab', { name: '孤儿候选', exact: true }).click();
  await selectOption(page, page, /实体类型/, '作品');
  const orphansTable = page.getByRole('table', { name: '孤儿候选', exact: true });
  const orphanRow = orphansTable.getByRole('row').filter({ hasText: state.orphanSourceKey });
  await expect(orphanRow).toHaveCount(1);
  await orphanRow.getByRole('button', { name: '处理', exact: true }).click();
  const orphanDialog = page.getByRole('dialog', { name: '处理孤儿候选', exact: true });
  await selectOption(page, orphanDialog, /决定/, '延长观察窗口');
  await orphanDialog.getByRole('textbox', { name: '延长的扫描次数', exact: true }).fill('2');
  const orphanPromise = page.waitForResponse((response) =>
    pathIs(response, `/api/v1/orphan-candidates/${state.orphanBindingId}/decide`, 'POST')
  );
  await orphanDialog.getByRole('button', { name: '提交决策', exact: true }).click();
  const orphanResponse = await orphanPromise;
  expect(orphanResponse.status()).toBe(200);
  expect(orphanResponse.request().postDataJSON()).toEqual({ decision: 'extend', extendScans: 2 });
  expect((await orphanResponse.json()) as OrphanDecisionResult).toMatchObject({
    bindingId: state.orphanBindingId,
    entityType: 'work',
    decision: 'extend',
    newStatus: 'inactive'
  });
  await expect(orphanRow).toHaveCount(0);

  const orphanUnbindRow = orphansTable.getByRole('row').filter({ hasText: state.orphanUnbindSourceKey });
  await expect(orphanUnbindRow).toHaveCount(1);
  await orphanUnbindRow.getByRole('button', { name: '处理', exact: true }).click();
  const orphanUnbindDialog = page.getByRole('dialog', { name: '处理孤儿候选', exact: true });
  await selectOption(page, orphanUnbindDialog, /决定/, '人工解绑');
  const orphanUnbindPromise = page.waitForResponse((response) =>
    pathIs(response, `/api/v1/orphan-candidates/${state.orphanUnbindBindingId}/decide`, 'POST')
  );
  await orphanUnbindDialog.getByRole('button', { name: '提交决策', exact: true }).click();
  const orphanUnbindResponse = await orphanUnbindPromise;
  expect(orphanUnbindResponse.status()).toBe(200);
  expect(orphanUnbindResponse.request().postDataJSON()).toEqual({ decision: 'unbind' });
  const orphanUnbound = (await orphanUnbindResponse.json()) as OrphanDecisionResult;
  expect(orphanUnbound).toMatchObject({
    bindingId: state.orphanUnbindBindingId,
    entityType: 'work',
    decision: 'unbind',
    newStatus: 'manual_unbound'
  });

  await selectOption(page, page, /实体类型/, '创作者');
  const orphanCreatorRow = orphansTable.getByRole('row').filter({ hasText: state.orphanCreatorSourceKey });
  await expect(orphanCreatorRow).toHaveCount(1);
  await orphanCreatorRow.getByRole('button', { name: '处理', exact: true }).click();
  const orphanCreatorDialog = page.getByRole('dialog', { name: '处理孤儿候选', exact: true });
  await selectOption(page, orphanCreatorDialog, /决定/, '确认为孤儿');
  const orphanCreatorPromise = page.waitForResponse((response) =>
    pathIs(response, `/api/v1/orphan-candidates/${state.orphanCreatorBindingId}/decide`, 'POST')
  );
  await orphanCreatorDialog.getByRole('button', { name: '提交决策', exact: true }).click();
  const orphanCreatorResponse = await orphanCreatorPromise;
  expect(orphanCreatorResponse.status()).toBe(200);
  expect(orphanCreatorResponse.request().postDataJSON()).toEqual({ decision: 'confirm_orphaned' });
  expect((await orphanCreatorResponse.json()) as OrphanDecisionResult).toMatchObject({
    bindingId: state.orphanCreatorBindingId,
    entityType: 'creator',
    decision: 'confirm_orphaned',
    newStatus: 'orphaned'
  });

  await selectOption(page, page, /实体类型/, '媒体');
  const orphanMediaRow = orphansTable.getByRole('row').filter({ hasText: state.orphanMediaSourceKey });
  await expect(orphanMediaRow).toHaveCount(1);
  await orphanMediaRow.getByRole('button', { name: '处理', exact: true }).click();
  const orphanMediaDialog = page.getByRole('dialog', { name: '处理孤儿候选', exact: true });
  await selectOption(page, orphanMediaDialog, /决定/, '保留（重置缺席计数）');
  const orphanMediaPromise = page.waitForResponse((response) =>
    pathIs(response, `/api/v1/orphan-candidates/${state.orphanMediaBindingId}/decide`, 'POST')
  );
  await orphanMediaDialog.getByRole('button', { name: '提交决策', exact: true }).click();
  const orphanMediaResponse = await orphanMediaPromise;
  expect(orphanMediaResponse.status()).toBe(200);
  expect(orphanMediaResponse.request().postDataJSON()).toEqual({ decision: 'retain' });
  expect((await orphanMediaResponse.json()) as OrphanDecisionResult).toMatchObject({
    bindingId: state.orphanMediaBindingId,
    entityType: 'media',
    decision: 'retain',
    newStatus: 'inactive'
  });

  await page.getByRole('tab', { name: '人工解绑', exact: true }).click();
  await selectOption(page, page, /动作/, '撤销作品解绑');
  await selectOption(page, page, /来源/, `${state.orphanSourceName} · ${state.orphanSourceId}`);
  await page.getByRole('textbox', { name: 'sourceKey', exact: true }).fill(state.orphanUnbindSourceKey);
  const undoOrphanWorkPromise = page.waitForResponse((response) =>
    pathIs(response, '/api/v1/binding-actions/undo-unbind', 'POST')
  );
  await page.getByRole('button', { name: '撤销作品解绑', exact: true }).click();
  await page
    .getByRole('dialog', { name: '撤销作品解绑', exact: true })
    .getByRole('button', { name: '确认执行', exact: true })
    .click();
  const undoOrphanWorkResponse = await undoOrphanWorkPromise;
  expect(undoOrphanWorkResponse.status()).toBe(200);
  expect(undoOrphanWorkResponse.request().postDataJSON()).toEqual({
    sourceId: state.orphanSourceId,
    sourceKey: state.orphanUnbindSourceKey,
    entityKind: 'work'
  });
  expect((await undoOrphanWorkResponse.json()) as BindingActionResult).toEqual({
    canonicalId: orphanUnbound.canonicalId,
    entityKind: 'work'
  });

  await selectOption(page, page, /动作/, '解绑媒体');
  await selectOption(page, page, /来源/, `${state.mediaSourceName} · ${state.mediaSourceId}`);
  await page.getByRole('textbox', { name: 'sourceKey', exact: true }).fill(state.mediaSourceKey);
  const unbindMediaPromise = page.waitForResponse((response) =>
    pathIs(response, '/api/v1/binding-actions/unbind-media', 'POST')
  );
  await page.getByRole('button', { name: '解绑媒体', exact: true }).click();
  await page
    .getByRole('dialog', { name: '解绑媒体', exact: true })
    .getByRole('button', { name: '确认执行', exact: true })
    .click();
  const unbindMediaResponse = await unbindMediaPromise;
  expect(unbindMediaResponse.status()).toBe(200);
  expect(unbindMediaResponse.request().postDataJSON()).toEqual({
    sourceId: state.mediaSourceId,
    sourceKey: state.mediaSourceKey
  });
  const unboundMedia = (await unbindMediaResponse.json()) as BindingActionResult;
  expect(unboundMedia).toMatchObject({ entityKind: 'media' });
  expect(unboundMedia.canonicalId).not.toBe('');

  await selectOption(page, page, /动作/, '撤销媒体解绑');
  const undoMediaPromise = page.waitForResponse((response) =>
    pathIs(response, '/api/v1/binding-actions/undo-unbind', 'POST')
  );
  await page.getByRole('button', { name: '撤销媒体解绑', exact: true }).click();
  await page
    .getByRole('dialog', { name: '撤销媒体解绑', exact: true })
    .getByRole('button', { name: '确认执行', exact: true })
    .click();
  const undoMediaResponse = await undoMediaPromise;
  expect(undoMediaResponse.status()).toBe(200);
  expect(undoMediaResponse.request().postDataJSON()).toEqual({
    sourceId: state.mediaSourceId,
    sourceKey: state.mediaSourceKey,
    entityKind: 'media'
  });
  expect((await undoMediaResponse.json()) as BindingActionResult).toEqual(unboundMedia);
});
