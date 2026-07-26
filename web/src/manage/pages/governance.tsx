/*
 * 治理：绑定问题、结构决策、孤儿候选与人工解绑。
 *
 * 这一页刻意**没有**全选框，也没有「全部忽略」按钮：服务端的每个治理端点都只接受一个对象，
 * 几百条待处理就是几百次独立往返。用一个假的批量按钮掩盖这件事，会让人在真正需要清理时对
 * 耗时与失败面完全没有预期。界面反而把「还需要多少次操作」直接写出来。
 *
 * 另一处必须表达清楚的是并发：每个动作都带 version，服务端用它做 CAS。冲突返回 409，
 * 此时必须重新载入列表，而不是把 version 加一再试。
 */

import { useState } from 'react';
import { Badge, Button, Dialog, Select, Tabs, TextInput, useToast } from '../../design';
import { useCapability } from '../../shared/session';
import {
  useBindingAction,
  useBindingIssues,
  useDecideOrphanCandidate,
  useDismissBindingIssue,
  useOrphanCandidates,
  useReopenBindingIssue,
  useResolveBindingIssue,
  useResolveStructureIssue,
  useSources,
  useStructureDecisions,
  useUndoStructureDecision,
  type BindingActionKind,
  type BindingIssue,
  type OrphanCandidate
} from '../api';
import {
  BINDING_ISSUE_STATUS_LABELS,
  BINDING_ISSUE_STATUS_TONES,
  ENTITY_TYPE_LABELS,
  ORPHAN_DECISION_LABELS,
  STRUCTURE_ACTION_LABELS
} from '../labels';
import {
  Absent,
  AsyncPanel,
  ConfirmAction,
  ContractNoteList,
  DataTable,
  InlineError,
  MonoId,
  PageHeader,
  Section,
  formatDateTime
} from '../ui';

const ENTITY_FILTERS = [
  { id: 'all', label: '全部类型' },
  { id: 'work', label: '作品' },
  { id: 'creator', label: '创作者' },
  { id: 'media', label: '媒体' }
] as const;

const STATUS_FILTERS = [
  { id: 'open', label: '待处理' },
  { id: 'resolved', label: '已修复' },
  { id: 'dismissed', label: '已忽略' },
  { id: 'superseded', label: '已被取代' },
  { id: 'stale', label: '已过期' }
] as const;

const RESOLVE_DECISIONS = [
  { id: 'bind_existing', label: '绑定到已有实体（需要目标 ID）' },
  { id: 'create_new', label: '创建新的 Canonical 实体' },
  { id: 'keep_separate', label: '保持独立，不绑定' }
] as const;

const SPLIT_ACTIONS = ['split_inherit', 'split_keep_same', 'split_create_new'] as const;
const MERGE_ACTIONS = ['merge_bind_existing', 'merge_create_new'] as const;

/* ————————————————————————————— 单条修复 ————————————————————————————— */

function ResolveIssueDialog({ issue }: { issue: BindingIssue }) {
  const { show } = useToast();
  const resolve = useResolveBindingIssue();
  const [decision, setDecision] = useState<'bind_existing' | 'create_new' | 'keep_separate'>('bind_existing');
  const [targetId, setTargetId] = useState('');

  const needsTarget = decision === 'bind_existing';
  const ready = !needsTarget || targetId.trim() !== '';

  return (
    <Dialog
      title="修复绑定问题"
      isDismissable={false}
      trigger={<Button variant="primary">修复</Button>}
      footer={(close) => (
        <>
          <Button variant="ghost" onPress={close}>
            取消
          </Button>
          <Button
            variant="primary"
            isDisabled={!ready}
            onPress={() => {
              resolve.mutate(
                {
                  issueId: issue.id,
                  decision,
                  version: issue.version,
                  ...(needsTarget ? { targetId: targetId.trim() } : {})
                },
                {
                  onSuccess: () => {
                    show({ title: '绑定问题已修复', tone: 'success' });
                  }
                }
              );
              close();
            }}
          >
            提交修复
          </Button>
        </>
      )}
    >
      <p className="manage-section__description">
        来源 {issue.sourceId} 的 {ENTITY_TYPE_LABELS[issue.entityType]} 绑定需要人工判断。稳定 code：
        {issue.code}。本次修复基于 version {issue.version}，若期间被其他会话改动会收到 409，
        届时请重新载入列表。
      </p>
      {issue.candidates.length === 0 ? (
        <p className="manage-section__description">服务端没有给出候选项。</p>
      ) : (
        <ul className="manage-notes">
          {issue.candidates.map((candidate) => (
            <li className="manage-note" key={candidate.candidateId}>
              <p className="manage-note__title">{candidate.label}</p>
              <p className="manage-note__detail">
                {candidate.candidateKind} · {candidate.matchSignal} = {candidate.matchValue} ·{' '}
                {candidate.candidateId}
              </p>
            </li>
          ))}
        </ul>
      )}
      <Select
        label="决定"
        options={RESOLVE_DECISIONS.map((item) => ({ id: item.id, label: item.label }))}
        selectedKey={decision}
        onSelectionChange={(key) => {
          if (key !== null) setDecision(key as 'bind_existing' | 'create_new' | 'keep_separate');
        }}
      />
      {needsTarget ? (
        <TextInput
          label="目标 Canonical ID"
          value={targetId}
          onChange={setTargetId}
          isRequired
          description="从上面的候选项里复制，或填写已知的稳定 ID。"
        />
      ) : null}
    </Dialog>
  );
}

function ResolveStructureDialog({ issue }: { issue: BindingIssue }) {
  const { show } = useToast();
  const resolve = useResolveStructureIssue();
  const kind = issue.structureKind ?? 'split';
  const actions = kind === 'split' ? SPLIT_ACTIONS : MERGE_ACTIONS;
  const [action, setAction] = useState<string>(actions[0]);
  const [targetSourceKey, setTargetSourceKey] = useState('');
  const [targetWorkId, setTargetWorkId] = useState('');

  return (
    <Dialog
      title={kind === 'split' ? '确认作品拆分' : '确认作品合并'}
      isDismissable={false}
      trigger={<Button variant="primary">确认结构</Button>}
      footer={(close) => (
        <>
          <Button variant="ghost" onPress={close}>
            取消
          </Button>
          <Button
            variant="primary"
            onPress={() => {
              resolve.mutate(
                {
                  issueId: issue.id,
                  action: action as (typeof SPLIT_ACTIONS)[number] | (typeof MERGE_ACTIONS)[number],
                  version: issue.version,
                  ...(targetSourceKey.trim() === '' ? {} : { targetSourceKey: targetSourceKey.trim() }),
                  ...(targetWorkId.trim() === '' ? {} : { targetWorkId: targetWorkId.trim() })
                },
                {
                  onSuccess: () => {
                    show({ title: '结构决策已记录', tone: 'success' });
                  }
                }
              );
              close();
            }}
          >
            提交决策
          </Button>
        </>
      )}
    >
      <p className="manage-section__description">
        结构决策会预置 Binding。撤回只对尚未被扫描消费的决策有效；一旦被消费，服务端返回结构化
        CONFLICT，并且不会反向执行已经生效的结构变化。
      </p>
      <Select
        label="决策"
        options={actions.map((value) => ({ id: value, label: STRUCTURE_ACTION_LABELS[value] ?? value }))}
        selectedKey={action}
        onSelectionChange={(key) => {
          if (key !== null) setAction(key);
        }}
      />
      <TextInput label="目标 sourceKey（可选）" value={targetSourceKey} onChange={setTargetSourceKey} />
      <TextInput label="目标 Work ID（可选）" value={targetWorkId} onChange={setTargetWorkId} />
    </Dialog>
  );
}

/* ————————————————————————————— 绑定问题 ————————————————————————————— */

function BindingIssuesPanel() {
  const { show } = useToast();
  const [status, setStatus] = useState<BindingIssue['status']>('open');
  const [entityType, setEntityType] = useState<string>('all');
  const [sourceId, setSourceId] = useState<string | null>(null);
  const sources = useSources();
  const issues = useBindingIssues({
    status,
    ...(entityType === 'all' ? {} : { entityType: entityType as BindingIssue['entityType'] }),
    ...(sourceId === null ? {} : { sourceId })
  });
  const dismiss = useDismissBindingIssue();
  const reopen = useReopenBindingIssue();
  const canWrite = useCapability('bindings.write');

  const rows = issues.data?.pages.flatMap((page) => page.issues) ?? [];

  return (
    <Section
      title="绑定问题"
      description="扫描发现的、无法由规则单独判定的绑定。每条都要单独处理，服务端没有批量端点。"
      actions={
        <>
          <Select
            label="状态"
            options={STATUS_FILTERS.map((item) => ({ id: item.id, label: item.label }))}
            selectedKey={status}
            onSelectionChange={(key) => {
              if (key !== null) setStatus(key as BindingIssue['status']);
            }}
          />
          <Select
            label="实体类型"
            options={ENTITY_FILTERS.map((item) => ({ id: item.id, label: item.label }))}
            selectedKey={entityType}
            onSelectionChange={(key) => {
              if (key !== null) setEntityType(key);
            }}
          />
          <AsyncPanel query={sources}>
            {(data) => (
              <Select
                label="来源"
                placeholder="全部来源"
                options={[
                  { id: '', label: '全部来源' },
                  ...data.sources.map((item) => ({ id: item.id, label: item.displayName }))
                ]}
                selectedKey={sourceId ?? ''}
                onSelectionChange={(key) => setSourceId(key === null || key === '' ? null : key)}
              />
            )}
          </AsyncPanel>
        </>
      }
    >
      <ContractNoteList area="governance" />
      <p className="manage-section__description">
        已载入 {rows.length} 条{issues.hasNextPage ? '（还有更多未载入）' : ''}。逐条处理需要 {rows.length}{' '}
        次独立请求——这是服务端契约决定的，不是界面偷懒。
      </p>
      <InlineError error={dismiss.error} title="忽略未能完成" />
      <InlineError error={reopen.error} title="重新打开未能完成" />
      <AsyncPanel query={issues}>
        {() => (
          <>
            <DataTable
              caption="绑定问题"
              rows={rows}
              rowKey={(row) => row.id}
              emptyTitle="没有符合条件的绑定问题"
              emptyDescription="切换状态筛选可以查看已修复或已忽略的历史记录。"
              columns={[
                {
                  id: 'status',
                  header: '状态',
                  render: (row) => (
                    <Badge tone={BINDING_ISSUE_STATUS_TONES[row.status]}>
                      {BINDING_ISSUE_STATUS_LABELS[row.status]}
                    </Badge>
                  )
                },
                { id: 'entity', header: '实体', render: (row) => ENTITY_TYPE_LABELS[row.entityType] },
                {
                  id: 'structure',
                  header: '结构',
                  render: (row) =>
                    row.structureKind === null || row.structureKind === undefined ? (
                      <Absent />
                    ) : (
                      <Badge tone="warning">{row.structureKind === 'split' ? '拆分' : '合并'}</Badge>
                    )
                },
                {
                  id: 'code',
                  header: '稳定 code',
                  render: (row) => <Badge tone="neutral">{row.code}</Badge>
                },
                { id: 'sourceKey', header: 'sourceKey', render: (row) => row.sourceKey, wrap: true },
                {
                  id: 'source',
                  header: 'Source',
                  render: (row) => <MonoId value={row.sourceId} label="Source ID" />
                },
                { id: 'candidates', header: '候选数', render: (row) => row.candidateCount },
                { id: 'version', header: 'version', render: (row) => row.version },
                { id: 'updated', header: '更新时间', render: (row) => formatDateTime(row.updatedAt) },
                {
                  id: 'actions',
                  header: '操作',
                  render: (row) => (
                    <span className="manage-cell-actions">
                      {canWrite && row.status === 'open' ? (
                        row.structureKind === null || row.structureKind === undefined ? (
                          <ResolveIssueDialog issue={row} />
                        ) : (
                          <ResolveStructureDialog issue={row} />
                        )
                      ) : null}
                      {canWrite && row.status === 'open' ? (
                        <ConfirmAction
                          label="忽略"
                          variant="secondary"
                          dialogTitle="忽略绑定问题"
                          confirmLabel="确认忽略"
                          description={`忽略后该问题不再出现在待处理列表里，但事实仍然保留，可以随时重新打开。version ${row.version}。`}
                          onConfirm={() => {
                            dismiss.mutate(
                              { issueId: row.id, version: row.version },
                              {
                                onSuccess: () => {
                                  show({ title: '已忽略', tone: 'success' });
                                }
                              }
                            );
                          }}
                        />
                      ) : null}
                      {canWrite && (row.status === 'dismissed' || row.status === 'resolved') ? (
                        <Button
                          variant="secondary"
                          onPress={() => {
                            reopen.mutate({ issueId: row.id, version: row.version });
                          }}
                        >
                          重新打开
                        </Button>
                      ) : null}
                      {canWrite ? null : <Absent>无变更入口</Absent>}
                    </span>
                  )
                }
              ]}
            />
            {issues.hasNextPage ? (
              <div className="manage-form__actions">
                <Button
                  variant="secondary"
                  isPending={issues.isFetchingNextPage}
                  onPress={() => void issues.fetchNextPage()}
                >
                  载入下一页
                </Button>
              </div>
            ) : null}
          </>
        )}
      </AsyncPanel>
    </Section>
  );
}

/* ————————————————————————————— 结构决策 ————————————————————————————— */

function StructureDecisionsPanel() {
  const { show } = useToast();
  const decisions = useStructureDecisions(null);
  const undo = useUndoStructureDecision();
  const canWrite = useCapability('bindings.write');

  return (
    <Section
      title="结构决策"
      description="已经记录的拆分与合并决策。撤回只适用于尚未被扫描消费的 pre-seed Binding；消费后服务端返回 CONFLICT，且不会反向执行已生效的结构变化。"
    >
      <InlineError error={undo.error} title="撤回未能完成" />
      <AsyncPanel query={decisions}>
        {(data) => (
          <DataTable
            caption="结构决策"
            rows={data.decisions}
            rowKey={(row) => row.decisionId}
            emptyTitle="还没有结构决策"
            columns={[
              {
                id: 'status',
                header: '状态',
                render: (row) => (
                  <Badge tone={row.status === 'applied' ? 'success' : 'neutral'}>
                    {row.status === 'applied' ? '已生效' : '已撤回'}
                  </Badge>
                )
              },
              { id: 'kind', header: '类型', render: (row) => (row.kind === 'split' ? '拆分' : '合并') },
              {
                id: 'action',
                header: '决策',
                render: (row) => STRUCTURE_ACTION_LABELS[row.action] ?? row.action
              },
              {
                id: 'issue',
                header: '来源问题',
                render: (row) => <MonoId value={row.issueId} label="问题 ID" />
              },
              {
                id: 'source',
                header: 'Source',
                render: (row) => <MonoId value={row.sourceId} label="Source ID" />
              },
              {
                id: 'targetKey',
                header: '目标 sourceKey',
                render: (row) => row.targetSourceKey ?? <Absent />,
                wrap: true
              },
              { id: 'targetWork', header: '目标 Work', render: (row) => row.targetWorkId ?? <Absent /> },
              { id: 'version', header: 'version', render: (row) => row.version },
              { id: 'updated', header: '更新时间', render: (row) => formatDateTime(row.updatedAt) },
              {
                id: 'actions',
                header: '操作',
                render: (row) =>
                  canWrite && row.status === 'applied' ? (
                    <ConfirmAction
                      label="撤回"
                      dialogTitle="撤回结构决策"
                      confirmLabel="确认撤回"
                      description="只有尚未被扫描消费的决策才能撤回。若已被消费，服务端会返回 CONFLICT，并且不会反向执行已经发生的结构变化。"
                      onConfirm={() => {
                        undo.mutate(
                          { decisionId: row.decisionId, version: row.version },
                          {
                            onSuccess: () => {
                              show({ title: '结构决策已撤回', tone: 'success' });
                            }
                          }
                        );
                      }}
                    />
                  ) : (
                    <Absent />
                  )
              }
            ]}
          />
        )}
      </AsyncPanel>
    </Section>
  );
}

/* ————————————————————————————— 孤儿候选 ————————————————————————————— */

function OrphanDecisionDialog({ candidate }: { candidate: OrphanCandidate }) {
  const { show } = useToast();
  const decide = useDecideOrphanCandidate();
  const [decision, setDecision] = useState<'retain' | 'extend' | 'confirm_orphaned' | 'unbind'>('retain');
  const [extendScans, setExtendScans] = useState('3');

  const extendValue = Number.parseInt(extendScans, 10);
  const extendValid = decision !== 'extend' || (Number.isFinite(extendValue) && extendValue > 0);

  return (
    <Dialog
      title="处理孤儿候选"
      isDismissable={false}
      trigger={<Button variant="primary">处理</Button>}
      footer={(close) => (
        <>
          <Button variant="ghost" onPress={close}>
            取消
          </Button>
          <Button
            variant="primary"
            isDisabled={!extendValid}
            onPress={() => {
              decide.mutate(
                {
                  bindingId: candidate.bindingId,
                  decision,
                  ...(decision === 'extend' ? { extendScans: extendValue } : {})
                },
                {
                  onSuccess: (result) => {
                    show({ title: `已处理，新状态 ${result.newStatus}`, tone: 'success' });
                  }
                }
              );
              close();
            }}
          >
            提交决策
          </Button>
        </>
      )}
    >
      <p className="manage-section__description">
        {candidate.canonicalLabel} 已连续 {candidate.missedScans} 次扫描未被发现，阈值为{' '}
        {candidate.retentionThreshold}。确认为孤儿不会删除用户事实，只是标记这条 Binding 不再活跃。
      </p>
      <Select
        label="决定"
        options={(['retain', 'extend', 'confirm_orphaned', 'unbind'] as const).map((value) => ({
          id: value,
          label: ORPHAN_DECISION_LABELS[value] ?? value
        }))}
        selectedKey={decision}
        onSelectionChange={(key) => {
          if (key !== null) setDecision(key as 'retain' | 'extend' | 'confirm_orphaned' | 'unbind');
        }}
      />
      {decision === 'extend' ? (
        <TextInput
          label="延长的扫描次数"
          value={extendScans}
          onChange={setExtendScans}
          errorMessage={extendValid ? undefined : '必须是正整数'}
        />
      ) : null}
    </Dialog>
  );
}

function OrphanCandidatesPanel() {
  const [entityType, setEntityType] = useState<string>('all');
  const candidates = useOrphanCandidates(
    entityType === 'all' ? {} : { entityType: entityType as OrphanCandidate['entityType'] }
  );
  const canWrite = useCapability('bindings.write');
  const rows = candidates.data?.pages.flatMap((page) => page.candidates) ?? [];

  return (
    <Section
      title="孤儿候选"
      description="连续多次扫描未被发现的 Binding。它们不会被自动删除：用户事实不可被重扫覆盖，最终状态需要人工决定。"
      actions={
        <Select
          label="实体类型"
          options={ENTITY_FILTERS.map((item) => ({ id: item.id, label: item.label }))}
          selectedKey={entityType}
          onSelectionChange={(key) => {
            if (key !== null) setEntityType(key);
          }}
        />
      }
    >
      <p className="manage-section__description">
        已载入 {rows.length} 条{candidates.hasNextPage ? '（还有更多未载入）' : ''}。同样没有批量端点。
      </p>
      <AsyncPanel query={candidates}>
        {() => (
          <>
            <DataTable
              caption="孤儿候选"
              rows={rows}
              rowKey={(row) => row.bindingId}
              emptyTitle="没有孤儿候选"
              emptyDescription="所有 Binding 在最近的扫描中都被重新发现。"
              columns={[
                { id: 'entity', header: '实体', render: (row) => ENTITY_TYPE_LABELS[row.entityType] },
                { id: 'label', header: '名称', render: (row) => row.canonicalLabel, wrap: true },
                { id: 'sourceKey', header: 'sourceKey', render: (row) => row.sourceKey, wrap: true },
                {
                  id: 'source',
                  header: 'Source',
                  render: (row) => <MonoId value={row.sourceId} label="Source ID" />
                },
                {
                  id: 'missed',
                  header: '连续缺席',
                  render: (row) => `${row.missedScans} / ${row.retentionThreshold}`
                },
                { id: 'updated', header: '更新时间', render: (row) => formatDateTime(row.updatedAt) },
                {
                  id: 'actions',
                  header: '操作',
                  render: (row) =>
                    canWrite ? <OrphanDecisionDialog candidate={row} /> : <Absent>无变更入口</Absent>
                }
              ]}
            />
            {candidates.hasNextPage ? (
              <div className="manage-form__actions">
                <Button
                  variant="secondary"
                  isPending={candidates.isFetchingNextPage}
                  onPress={() => void candidates.fetchNextPage()}
                >
                  载入下一页
                </Button>
              </div>
            ) : null}
          </>
        )}
      </AsyncPanel>
    </Section>
  );
}

/* ————————————————————————————— 人工解绑 ————————————————————————————— */

const BINDING_ACTIONS: readonly { id: BindingActionKind; label: string; description: string }[] = [
  {
    id: 'unbind-work',
    label: '解绑作品',
    description:
      '把该 sourceKey 对应的作品 Binding 标记为 manual_unbound。Canonical 作品与其上的用户事实都会保留。'
  },
  {
    id: 'unbind-media',
    label: '解绑媒体',
    description: '把该 sourceKey 对应的媒体 Binding 标记为 manual_unbound。'
  },
  {
    id: 'undo-unbind',
    label: '撤销解绑',
    description: '把之前的人工解绑撤回，使 Binding 重新可被扫描激活。'
  }
];

function BindingActionsPanel() {
  const { show } = useToast();
  const sources = useSources();
  const action = useBindingAction();
  const canWrite = useCapability('bindings.write');
  const [kind, setKind] = useState<BindingActionKind>('unbind-work');
  const [sourceId, setSourceId] = useState<string | null>(null);
  const [sourceKey, setSourceKey] = useState('');

  const current = BINDING_ACTIONS.find((item) => item.id === kind);

  return (
    <Section
      title="人工解绑"
      description="按 (Source, sourceKey) 精确定位一条 Binding。解绑不会删除 Canonical 实体，也不会删除任何用户事实。"
    >
      {canWrite ? (
        <div className="manage-form">
          <div className="manage-form__row">
            <Select
              label="动作"
              options={BINDING_ACTIONS.map((item) => ({ id: item.id, label: item.label }))}
              selectedKey={kind}
              onSelectionChange={(key) => {
                if (key !== null) setKind(key as BindingActionKind);
              }}
            />
            <AsyncPanel query={sources}>
              {(data) => (
                <Select
                  label="来源"
                  placeholder="选择来源"
                  options={data.sources.map((item) => ({ id: item.id, label: item.displayName }))}
                  selectedKey={sourceId}
                  onSelectionChange={setSourceId}
                />
              )}
            </AsyncPanel>
            <TextInput label="sourceKey" value={sourceKey} onChange={setSourceKey} isRequired />
          </div>
          <p className="manage-section__description">{current?.description}</p>
          <div className="manage-form__actions">
            <ConfirmAction
              label={current?.label ?? '执行'}
              dialogTitle={current?.label ?? '执行绑定动作'}
              confirmLabel="确认执行"
              isDisabled={sourceId === null || sourceKey.trim() === ''}
              isPending={action.isPending}
              description={current?.description ?? ''}
              onConfirm={() => {
                if (sourceId === null) return;
                action.mutate(
                  { kind, sourceId, sourceKey: sourceKey.trim() },
                  {
                    onSuccess: (result) => {
                      show({
                        title: '绑定动作已完成',
                        description: `${result.entityKind} ${result.canonicalId}`,
                        tone: 'success'
                      });
                    }
                  }
                );
              }}
            />
          </div>
          <InlineError error={action.error} title="绑定动作未能完成" />
        </div>
      ) : (
        <p className="manage-section__description">当前主体没有 bindings.write，人工解绑入口已隐藏。</p>
      )}
    </Section>
  );
}

export function GovernancePage() {
  return (
    <>
      <PageHeader
        title="治理"
        lead="扫描无法自行判定的绑定、拆分与合并、孤儿候选与人工解绑都在这里。所有动作都是单条的：服务端没有批量端点，界面也不会假装有。"
      />
      <Tabs
        label="治理分区"
        defaultSelectedKey="issues"
        items={[
          { id: 'issues', label: '绑定问题', content: <BindingIssuesPanel /> },
          { id: 'structure', label: '结构决策', content: <StructureDecisionsPanel /> },
          { id: 'orphans', label: '孤儿候选', content: <OrphanCandidatesPanel /> },
          { id: 'unbind', label: '人工解绑', content: <BindingActionsPanel /> }
        ]}
      />
    </>
  );
}
