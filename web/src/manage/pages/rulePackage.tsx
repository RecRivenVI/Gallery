/*
 * 规则包详情：草稿 → 校验 → 影响评估 → 发布 → 回滚。
 *
 * 这一页承载规则闭环里最容易出错的两处并发语义：
 *
 * 1. **草稿的 If-Match。** 保存时带上 GET 草稿返回的 revision；其他会话改过草稿时服务端返回
 *    409 RULE_DRAFT_CONFLICT。界面**不会**自动重试或强制覆盖，而是明确告诉你发生了什么，
 *    并提供一个显式的「用服务端最新草稿覆盖编辑器」动作。
 * 2. **发布前必须看影响。** POST /rules/impact 给出重扫、重投影、搜索与派生的影响；
 *    blockPublish 为真时服务端会直接拒绝发布（409 RULE_PUBLISH_BLOCKED）。
 */

import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { Badge, Button, Checkbox, ErrorState, Select, TextInput, useToast } from '../../design';
import { describeError, errorCode, errorCorrelationId } from '../../shared/errors';
import { useCapability } from '../../shared/session';
import {
  useExportRuleVersion,
  usePublishRuleDraft,
  useRuleAudits,
  useRuleDraft,
  useRuleImpact,
  useRulePackage,
  useRuleVersions,
  useRollbackRulePackage,
  useSaveRuleDraft,
  useValidateRuleDraft,
  type RuleDraft,
  type RuleImpactResult,
  type RuleVersion
} from '../api';
import { IMPACT_CATEGORY_LABELS, IMPACT_CATEGORY_TONES } from '../labels';
import {
  Absent,
  AsyncPanel,
  BoolBadge,
  ConfirmAction,
  ContractNoteList,
  DataTable,
  Facts,
  InlineError,
  MonoId,
  PageHeader,
  Section,
  formatDateTime
} from '../ui';

type DraftFormat = RuleDraft['format'];

const SHA256_DIGEST = /^[a-f0-9]{64}$/;

function semanticHashOrUndefined(value: string | undefined): string | undefined {
  return value !== undefined && SHA256_DIGEST.test(value) ? value : undefined;
}

const DRAFT_FORMATS: readonly DraftFormat[] = ['json', 'yaml', 'toml'];

function stringifyDraft(content: unknown, format: DraftFormat): string {
  if (typeof content === 'string') return content;
  if (format === 'json') return JSON.stringify(content, null, 2);
  return JSON.stringify(content, null, 2);
}

function parseDraft(text: string, format: DraftFormat): { value: unknown; error?: string } {
  if (format !== 'json') {
    // YAML/TOML 由服务端转换为规范 JSON；这里原样作为字符串提交。
    return { value: text };
  }
  try {
    return { value: JSON.parse(text) };
  } catch (error) {
    return { value: null, error: error instanceof Error ? error.message : '不是合法的 JSON' };
  }
}

/* ————————————————————————————— 草稿 ————————————————————————————— */

interface DraftWorkspace {
  /** 最近一次读取或 mutation 返回的服务端草稿；null 表示服务端尚无草稿。 */
  serverDraft: RuleDraft | null;
  text: string;
  format: DraftFormat;
  /** 本地文本或格式是否偏离 serverDraft。 */
  dirty: boolean;
  /** 本地编辑基于的服务端 revision；0 是“草稿尚不存在”的 CAS 基线。 */
  baseRevision: number;
  /** 下一次发布形成的 RuleVersion 的父版本。 */
  baseSemanticHash?: string;
}

interface ImpactEvidence {
  revision: number;
  currentSemanticHash: string | null;
  result: RuleImpactResult;
}

interface SubmittedDraftSnapshot {
  revision: number;
  text: string;
  format: DraftFormat;
}

function workspaceFromDraft(
  draft: RuleDraft | null,
  currentSemanticHash: string | undefined,
  baseOverride?: string
): DraftWorkspace {
  const normalizedCurrent = semanticHashOrUndefined(currentSemanticHash);
  const normalizedOverride = semanticHashOrUndefined(baseOverride);
  if (draft === null) {
    return {
      serverDraft: null,
      text: '',
      format: 'json',
      dirty: false,
      baseRevision: 0,
      ...((normalizedOverride ?? normalizedCurrent) === undefined
        ? {}
        : { baseSemanticHash: normalizedOverride ?? normalizedCurrent })
    };
  }
  const text = stringifyDraft(draft.content, draft.format);
  const baseSemanticHash =
    normalizedOverride ?? semanticHashOrUndefined(draft.baseSemanticHash) ?? normalizedCurrent;
  return {
    serverDraft: draft,
    text,
    format: draft.format,
    dirty: false,
    baseRevision: draft.revision,
    ...(baseSemanticHash === undefined ? {} : { baseSemanticHash })
  };
}

function absorbDraftMutation(
  current: DraftWorkspace | null,
  result: RuleDraft,
  currentSemanticHash: string | undefined,
  baseOverride: string | undefined,
  submitted: SubmittedDraftSnapshot
): DraftWorkspace {
  const server = workspaceFromDraft(result, currentSemanticHash, baseOverride);
  if (current === null) return server;
  const currentServerRevision = current.serverDraft?.revision ?? 0;
  if (currentServerRevision > result.revision) return current;
  if (
    current.baseRevision === submitted.revision &&
    current.text === submitted.text &&
    current.format === submitted.format
  ) {
    return server;
  }
  return {
    ...server,
    text: current.text,
    format: current.format,
    dirty: current.text !== server.text || current.format !== server.format
  };
}

interface DraftEditorProps {
  workspace: DraftWorkspace | null;
  draft: ReturnType<typeof useRuleDraft>;
  save: ReturnType<typeof useSaveRuleDraft>;
  validate: ReturnType<typeof useValidateRuleDraft>;
  isLocked: boolean;
  remoteChanged: boolean;
  onEdit: (text: string, format: DraftFormat) => void;
  onSave: (content: unknown, format: DraftFormat) => void;
  onValidate: () => void;
  onAdoptLatest: () => void;
}

function DraftEditor({
  workspace,
  draft,
  save,
  validate,
  isLocked,
  remoteChanged,
  onEdit,
  onSave,
  onValidate,
  onAdoptLatest
}: DraftEditorProps) {
  const canWrite = useCapability('rules.write');
  const missing = draft.isError && errorCode(draft.error) === 'NOT_FOUND';
  const parsed = parseDraft(workspace?.text ?? '', workspace?.format ?? 'json');
  const conflict =
    errorCode(save.error) === 'RULE_DRAFT_CONFLICT' || errorCode(validate.error) === 'RULE_DRAFT_CONFLICT';

  if (draft.isError && !missing) {
    return (
      <ErrorState
        title="无法读取草稿"
        description={describeError(draft.error)}
        code={errorCode(draft.error)}
        correlationId={errorCorrelationId(draft.error)}
        onRetry={() => void draft.refetch()}
      />
    );
  }
  if ((draft.isPending && draft.fetchStatus !== 'idle') || workspace === null) {
    return <p className="manage-section__description">正在读取草稿…</p>;
  }

  return (
    <div className="manage-form">
      <Facts
        items={[
          {
            term: '服务端草稿 revision',
            value: workspace.serverDraft === null ? <Absent>尚无草稿</Absent> : workspace.serverDraft.revision
          },
          { term: '本地编辑基于的 revision', value: workspace.baseRevision },
          {
            term: '基于版本',
            value:
              workspace.baseSemanticHash === undefined ? (
                <Absent>首次版本</Absent>
              ) : (
                <MonoId value={workspace.baseSemanticHash} label="父版本 semanticHash" />
              )
          },
          {
            term: '本地状态',
            value: (
              <Badge tone={workspace.dirty ? 'warning' : 'success'}>
                {workspace.dirty ? '有未保存修改' : '已与服务端同步'}
              </Badge>
            )
          },
          {
            term: '校验状态',
            value:
              workspace.serverDraft === null ? (
                <Absent />
              ) : (
                <Badge
                  tone={
                    workspace.serverDraft.validationStatus === 'validated'
                      ? 'success'
                      : workspace.serverDraft.validationStatus === 'invalid'
                        ? 'danger'
                        : 'neutral'
                  }
                >
                  {workspace.serverDraft.validationStatus === 'validated'
                    ? '校验通过'
                    : workspace.serverDraft.validationStatus === 'invalid'
                      ? '校验未通过'
                      : '尚未校验'}
                </Badge>
              )
          },
          { term: '最后保存者', value: workspace.serverDraft?.savedBy ?? <Absent /> },
          { term: '最后保存时间', value: formatDateTime(workspace.serverDraft?.updatedAt) }
        ]}
      />

      <Select
        label="草稿格式"
        options={DRAFT_FORMATS.map((value) => ({ id: value, label: value.toUpperCase() }))}
        selectedKey={workspace.format}
        isDisabled={!canWrite || isLocked}
        onSelectionChange={(key) => {
          if (key === null) return;
          const next = key as DraftFormat;
          onEdit(workspace.text, next);
        }}
        description="YAML/TOML 只是导入形态，服务端会转换为规范 JSON 后再保存。运行时唯一事实源永远是规范 JSON。"
      />

      <TextInput
        label="草稿内容"
        value={workspace.text}
        onChange={(next) => {
          onEdit(next, workspace.format);
        }}
        isMultiline
        rows={18}
        isDisabled={!canWrite || isLocked}
        errorMessage={workspace.format === 'json' ? parsed.error : undefined}
      />

      {canWrite ? (
        <div className="manage-form__actions">
          <Button
            variant="primary"
            isPending={save.isPending}
            isDisabled={
              !workspace.dirty ||
              isLocked ||
              workspace.text === '' ||
              (workspace.format === 'json' && parsed.error !== undefined)
            }
            onPress={() => {
              onSave(parsed.value, workspace.format);
            }}
          >
            保存草稿
          </Button>
          <Button
            variant="secondary"
            isPending={validate.isPending}
            isDisabled={isLocked || workspace.dirty || workspace.serverDraft === null}
            onPress={() => {
              onValidate();
            }}
          >
            校验草稿
          </Button>
        </div>
      ) : (
        <p className="manage-section__description">
          当前主体在 global scope 没有 rules.write，草稿编辑与校验入口已禁用。
        </p>
      )}

      {remoteChanged || conflict ? (
        <div className="manage-inline-error" role="alert">
          <p className="manage-inline-error__title">服务端草稿已经变化，本地编辑仍被保留</p>
          <p className="manage-inline-error__body">
            你的编辑基于 revision {workspace.baseRevision}，服务端当前是 revision{' '}
            {workspace.serverDraft?.revision ?? 0}。界面不会覆盖本地修改；请先复制需要保留的内容，
            再显式载入服务端最新草稿并重新应用改动。
          </p>
          {conflict ? (
            <p className="manage-inline-error__code">
              RULE_DRAFT_CONFLICT
              {errorCorrelationId(save.error ?? validate.error) === undefined
                ? ''
                : ` · ${errorCorrelationId(save.error ?? validate.error)}`}
            </p>
          ) : null}
          <div className="manage-form__actions">
            <ConfirmAction
              label="用服务端最新草稿覆盖编辑器"
              dialogTitle="覆盖本地编辑"
              confirmLabel="确认覆盖"
              description="编辑器里未保存的改动会被服务端最新草稿替换。请先自行复制需要保留的内容。"
              onConfirm={onAdoptLatest}
            />
          </div>
        </div>
      ) : (
        <InlineError error={save.error} title="草稿未能保存" />
      )}

      <InlineError error={validate.error} title="校验未能完成" />
      {workspace.serverDraft === null || workspace.serverDraft.diagnostics.length === 0 ? null : (
        <pre className="manage-code">{JSON.stringify(workspace.serverDraft.diagnostics, null, 2)}</pre>
      )}
    </div>
  );
}

/* ————————————————————————————— 影响评估 ————————————————————————————— */

/** 把编辑器文本解析成影响评估可用的目标状态。只有规范 JSON 对象才是合法输入。 */
function draftAsObject(text: string, format: DraftFormat): Record<string, unknown> | null {
  if (format !== 'json') return null;
  try {
    const parsed: unknown = JSON.parse(text);
    if (typeof parsed === 'object' && parsed !== null && !Array.isArray(parsed)) {
      return parsed as Record<string, unknown>;
    }
    return null;
  } catch {
    return null;
  }
}

function ImpactPanel({
  currentSemanticHash,
  workspace,
  impact,
  evidence,
  onEvidence
}: {
  currentSemanticHash: string | undefined;
  workspace: DraftWorkspace | null;
  impact: ReturnType<typeof useRuleImpact>;
  evidence: ImpactEvidence | null;
  onEvidence: (evidence: ImpactEvidence | null) => void;
}) {
  const canWrite = useCapability('rules.write');
  const before = useExportRuleVersion(currentSemanticHash ?? null);
  const afterPackage = draftAsObject(workspace?.text ?? '', workspace?.format ?? 'json');
  const beforeReady = currentSemanticHash === undefined || before.data !== undefined;
  const currentEvidence =
    workspace !== null &&
    !workspace.dirty &&
    evidence !== null &&
    evidence.revision === workspace.baseRevision &&
    evidence.currentSemanticHash === (currentSemanticHash ?? null)
      ? evidence
      : null;

  return (
    <Section
      title="影响评估"
      description="发布前必须看这里：它决定这次修改会不会触发重扫、重投影、搜索重建或派生资源重建。契约要求 rules.write —— 只读分析却要写权限，是已知的过度限制。"
      actions={
        canWrite ? (
          <Button
            variant="secondary"
            isPending={impact.isPending}
            isDisabled={
              workspace?.serverDraft == null || workspace.dirty || afterPackage === null || !beforeReady
            }
            onPress={() => {
              if (workspace?.serverDraft == null || workspace.dirty) return;
              if (afterPackage === null || !beforeReady) return;
              const revision = workspace.baseRevision;
              const baseline = currentSemanticHash ?? null;
              onEvidence(null);
              impact.mutate(
                {
                  before: currentSemanticHash === undefined ? null : (before.data ?? null),
                  after: afterPackage
                },
                {
                  onSuccess: (result) => {
                    onEvidence({ revision, currentSemanticHash: baseline, result });
                  }
                }
              );
            }}
          >
            评估本次修改的影响
          </Button>
        ) : undefined
      }
    >
      {currentSemanticHash === undefined ? (
        <p className="manage-section__description">
          该规则包还没有已发布版本，因此影响请求以 <code>before=null</code> 明确表示首次发布。
        </p>
      ) : null}
      {workspace?.dirty === true ? (
        <p className="manage-section__description" role="status">
          当前有未保存修改。请先保存并校验；旧的影响结论已经失效。
        </p>
      ) : null}
      {afterPackage === null ? (
        <p className="manage-section__description">
          影响评估只接受规范 JSON 对象作为目标状态。当前草稿要么不是 JSON 格式，要么还不是合法的 JSON
          对象；请先保存为 JSON（服务端会在保存时把 YAML/TOML 转换为规范 JSON）。
        </p>
      ) : null}
      <InlineError error={before.error} title="无法读取影响评估基线" />
      <InlineError error={impact.error} title="影响评估未能完成" />
      {currentEvidence === null ? null : (
        <>
          <Facts
            items={[
              {
                term: '结论',
                value: (
                  <Badge tone={IMPACT_CATEGORY_TONES[currentEvidence.result.category]}>
                    {IMPACT_CATEGORY_LABELS[currentEvidence.result.category]}
                  </Badge>
                )
              },
              {
                term: '是否阻止发布',
                value: (
                  <BoolBadge
                    value={currentEvidence.result.blockPublish}
                    trueLabel={
                      currentEvidence.result.manualConfirmation
                        ? '是，需要显式确认影响'
                        : '是，发布前需要显式确认影响'
                    }
                    falseLabel="否"
                    trueTone="danger"
                    falseTone="success"
                  />
                )
              },
              {
                term: '需要人工确认',
                value: (
                  <BoolBadge
                    value={currentEvidence.result.manualConfirmation}
                    trueLabel="是"
                    falseLabel="否"
                    trueTone="warning"
                    falseTone="success"
                  />
                )
              },
              { term: '完整重扫', value: currentEvidence.result.fullRescan ? '需要' : '不需要' },
              { term: '部分重扫', value: currentEvidence.result.partialRescan ? '需要' : '不需要' },
              { term: '重投影', value: currentEvidence.result.reproject ? '需要' : '不需要' },
              { term: '重建搜索', value: currentEvidence.result.rebuildSearch ? '需要' : '不需要' },
              { term: '重建派生资源', value: currentEvidence.result.rebuildDerived ? '需要' : '不需要' },
              { term: '需要复核绑定', value: currentEvidence.result.bindingReview ? '需要' : '不需要' },
              {
                term: '受影响来源',
                value:
                  currentEvidence.result.affectedSources.length === 0 ? (
                    <Absent />
                  ) : (
                    currentEvidence.result.affectedSources.join('、')
                  )
              },
              {
                term: '涉及字段',
                value:
                  currentEvidence.result.fields.length === 0 ? (
                    <Absent />
                  ) : (
                    currentEvidence.result.fields.join('、')
                  )
              },
              {
                term: '原因码',
                value:
                  currentEvidence.result.reasonCodes.length === 0 ? (
                    <Absent />
                  ) : (
                    currentEvidence.result.reasonCodes.join('、')
                  )
              },
              { term: '预计任务', value: currentEvidence.result.estimatedJob ?? <Absent /> }
            ]}
          />
          {currentEvidence.result.traceSummary === undefined ||
          currentEvidence.result.traceSummary.length === 0 ? null : (
            <pre className="manage-code">{currentEvidence.result.traceSummary.join('\n')}</pre>
          )}
        </>
      )}
    </Section>
  );
}

/* ————————————————————————————— 发布与回滚 ————————————————————————————— */

function PublishPanel({
  packageId,
  currentSemanticHash,
  workspace,
  evidence,
  publish,
  draftMutationPending,
  onPublished,
  onConflict
}: {
  packageId: string;
  currentSemanticHash: string | undefined;
  workspace: DraftWorkspace | null;
  evidence: ImpactEvidence | null;
  publish: ReturnType<typeof usePublishRuleDraft>;
  draftMutationPending: boolean;
  onPublished: (version: RuleVersion) => Promise<void>;
  onConflict: () => void;
}) {
  const { show } = useToast();
  const canPublish = useCapability('rules.publish');
  const [reason, setReason] = useState('');
  const [confirmImpact, setConfirmImpact] = useState(false);
  const currentEvidence =
    workspace !== null &&
    !workspace.dirty &&
    evidence !== null &&
    evidence.revision === workspace.baseRevision &&
    evidence.currentSemanticHash === (currentSemanticHash ?? null)
      ? evidence
      : null;
  const impactResult = currentEvidence?.result;
  const needsConfirmation =
    impactResult?.manualConfirmation === true ||
    impactResult?.bindingReview === true ||
    impactResult?.blockPublish === true;
  const ready =
    workspace?.serverDraft != null &&
    !workspace.dirty &&
    workspace.serverDraft.validationStatus === 'validated' &&
    impactResult !== undefined &&
    !draftMutationPending &&
    (!needsConfirmation || confirmImpact);

  useEffect(() => {
    setConfirmImpact(false);
  }, [currentEvidence?.revision, currentEvidence?.currentSemanticHash, impactResult]);

  return (
    <Section
      title="发布"
      description="发布把当前草稿冻结成不可变的 RuleVersion，并把规则包的当前版本指针指向它。仍有未通过的校验或未确认的影响时返回 409 RULE_PUBLISH_BLOCKED。"
    >
      {canPublish ? (
        <div className="manage-form">
          <TextInput label="发布理由" value={reason} onChange={setReason} description="会写入规则包审计。" />
          {needsConfirmation ? (
            <Checkbox isSelected={confirmImpact} onChange={setConfirmImpact}>
              我已阅读并确认本次影响评估要求的人工复核
            </Checkbox>
          ) : null}
          {workspace?.dirty === true ? (
            <p className="manage-section__description">当前有未保存修改，不能校验影响或发布。</p>
          ) : null}
          {workspace !== null &&
          workspace.serverDraft !== null &&
          workspace.serverDraft.validationStatus !== 'validated' ? (
            <p className="manage-section__description">当前 revision 尚未通过校验，不能发布。</p>
          ) : null}
          {currentEvidence === null ? (
            <p className="manage-section__description">请先对当前已保存 revision 完成影响评估。</p>
          ) : null}
          <div className="manage-form__actions">
            <ConfirmAction
              label="发布草稿"
              dialogTitle="发布规则草稿"
              confirmLabel="确认发布"
              variant="primary"
              isPending={publish.isPending}
              isDisabled={!ready}
              description="发布后该版本不可变，并可能触发重扫或重投影任务。请先确认影响评估的结论。"
              onConfirm={() => {
                if (workspace === null || !ready) return;
                publish.mutate(
                  {
                    packageId,
                    expectedRevision: workspace.baseRevision,
                    reason,
                    confirmImpact
                  },
                  {
                    onSuccess: (version) => {
                      setReason('');
                      setConfirmImpact(false);
                      show({
                        title: '规则版本已发布',
                        description: `semanticHash ${version.semanticHash}`,
                        tone: 'success'
                      });
                      void onPublished(version);
                    },
                    onError: (error) => {
                      if (errorCode(error) === 'RULE_DRAFT_CONFLICT') onConflict();
                    }
                  }
                );
              }}
            />
          </div>
          <InlineError error={publish.error} title="发布被拒绝" />
          {errorCode(publish.error) === 'RULE_PUBLISH_BLOCKED' ? (
            <p className="manage-section__description">
              服务端阻止了这次发布：当前 revision、影响结论或人工确认已经失效。请刷新后重新校验和评估。
            </p>
          ) : null}
        </div>
      ) : (
        <p className="manage-section__description">
          当前主体在 global scope 没有 rules.publish，发布入口已隐藏。
        </p>
      )}
    </Section>
  );
}

function RollbackPanel({
  packageId,
  packageRevision
}: {
  packageId: string;
  packageRevision: number | null;
}) {
  const { show } = useToast();
  const versions = useRuleVersions(packageId);
  const rollback = useRollbackRulePackage();
  const canPublish = useCapability('rules.publish');
  const [target, setTarget] = useState<string | null>(null);
  const [reason, setReason] = useState('');
  const [confirmImpact, setConfirmImpact] = useState(false);

  return (
    <Section
      title="回滚"
      description="回滚只改变当前版本指针，不删除任何已发布版本。目标版本仍被使用或不满足前置条件时返回 409 RULE_ROLLBACK_BLOCKED。"
    >
      <AsyncPanel query={versions}>
        {(data) => (
          <>
            {canPublish ? (
              <div className="manage-form">
                <Select
                  label="回滚到"
                  placeholder="选择一个已发布版本"
                  options={data.items.map((item) => ({
                    id: item.semanticHash,
                    label: `${item.version}（${item.semanticHash.slice(0, 12)}…）`
                  }))}
                  selectedKey={target}
                  onSelectionChange={setTarget}
                />
                <TextInput label="回滚理由" value={reason} onChange={setReason} isRequired />
                <Checkbox isSelected={confirmImpact} onChange={setConfirmImpact}>
                  我已确认回滚的影响（可能触发重扫或重投影）
                </Checkbox>
                <div className="manage-form__actions">
                  <ConfirmAction
                    label="回滚当前版本"
                    dialogTitle="回滚规则包"
                    confirmLabel="确认回滚"
                    isDisabled={target === null || reason.trim() === '' || packageRevision === null}
                    isPending={rollback.isPending}
                    description="回滚会立即改变该规则包在所有绑定处的运行语义，并可能触发重扫。"
                    onConfirm={() => {
                      if (target === null || packageRevision === null) return;
                      rollback.mutate(
                        {
                          packageId,
                          targetSemanticHash: target,
                          expectedRevision: packageRevision,
                          reason: reason.trim(),
                          confirmImpact
                        },
                        {
                          onSuccess: () => {
                            show({ title: '当前版本指针已回滚', tone: 'success' });
                          }
                        }
                      );
                    }}
                  />
                </div>
                <InlineError error={rollback.error} title="回滚被拒绝" />
              </div>
            ) : null}

            <DataTable
              caption="已发布版本"
              rows={data.items}
              rowKey={(row) => row.semanticHash}
              emptyTitle="该规则包还没有已发布版本"
              columns={[
                { id: 'version', header: '版本', render: (row) => row.version },
                {
                  id: 'status',
                  header: '状态',
                  render: (row) => (
                    <Badge tone={row.status === 'published' ? 'success' : 'neutral'}>
                      {row.status ?? 'published'}
                    </Badge>
                  )
                },
                {
                  id: 'semantic',
                  header: 'semanticHash',
                  render: (row) => <MonoId value={row.semanticHash} label="semanticHash" />
                },
                {
                  id: 'package',
                  header: 'packageHash',
                  render: (row) => <MonoId value={row.packageHash} label="packageHash" />
                },
                {
                  id: 'ir',
                  header: 'ruleIrHash',
                  render: (row) => <MonoId value={row.ruleIrHash} label="ruleIrHash" />
                },
                {
                  id: 'executable',
                  header: '可执行',
                  render: (row) =>
                    row.executable === undefined ? (
                      <Absent />
                    ) : (
                      <BoolBadge
                        value={row.executable}
                        trueLabel="是"
                        falseLabel="编译失败"
                        falseTone="danger"
                      />
                    )
                },
                { id: 'published', header: '发布时间', render: (row) => formatDateTime(row.publishedAt) }
              ]}
            />
          </>
        )}
      </AsyncPanel>
    </Section>
  );
}

function AuditPanel({ packageId }: { packageId: string }) {
  const audits = useRuleAudits(packageId);
  return (
    <Section title="规则包审计" description="保存、校验、发布、回滚与弃用都会留下记录。">
      <AsyncPanel query={audits}>
        {(data) =>
          data.items.length === 0 ? (
            <p className="manage-section__description">还没有审计记录。</p>
          ) : (
            <pre className="manage-code">{JSON.stringify(data.items, null, 2)}</pre>
          )
        }
      </AsyncPanel>
    </Section>
  );
}

/* ————————————————————————————— 页面 ————————————————————————————— */

function RulePackageContent({ packageId }: { packageId: string }) {
  const { show } = useToast();
  const rulePackage = useRulePackage(packageId);
  const draft = useRuleDraft(packageId);
  const save = useSaveRuleDraft();
  const validate = useValidateRuleDraft();
  const publish = usePublishRuleDraft();
  const impact = useRuleImpact();
  const [workspace, setWorkspace] = useState<DraftWorkspace | null>(null);
  const [impactEvidence, setImpactEvidence] = useState<ImpactEvidence | null>(null);
  const [publishRefreshPending, setPublishRefreshPending] = useState(false);
  const currentSemanticHash = semanticHashOrUndefined(rulePackage.data?.currentSemanticHash);
  const draftMissing = draft.isError && errorCode(draft.error) === 'NOT_FOUND';

  useEffect(() => {
    const incoming = draft.data ?? (draftMissing ? null : undefined);
    if (incoming === undefined) return;
    setWorkspace((current) => {
      if (current === null) return workspaceFromDraft(incoming, currentSemanticHash);
      const incomingRevision = incoming?.revision ?? 0;
      const currentServerRevision = current.serverDraft?.revision ?? 0;
      if (current.dirty) {
        return incomingRevision === currentServerRevision ? current : { ...current, serverDraft: incoming };
      }
      if (incomingRevision !== currentServerRevision) {
        return workspaceFromDraft(incoming, currentSemanticHash);
      }
      if (current.baseSemanticHash === undefined && currentSemanticHash !== undefined) {
        return {
          ...current,
          baseSemanticHash: semanticHashOrUndefined(incoming?.baseSemanticHash) ?? currentSemanticHash
        };
      }
      return current;
    });
  }, [currentSemanticHash, draft.data, draftMissing]);

  const resetAnalysis = () => {
    impact.reset();
    setImpactEvidence(null);
  };

  const editWorkspace = (text: string, format: DraftFormat) => {
    if (publish.isPending || publishRefreshPending) return;
    setWorkspace((current) => {
      if (current === null) return current;
      const server = workspaceFromDraft(current.serverDraft, currentSemanticHash, current.baseSemanticHash);
      return {
        ...current,
        text,
        format,
        dirty:
          (current.serverDraft?.revision ?? 0) !== current.baseRevision ||
          text !== server.text ||
          format !== server.format
      };
    });
    // pending mutation 的 per-call 回调负责吸收精确服务端 revision；reset 会移除该回调并
    // 让后续编辑停留在旧 CAS 基线。请求完成前保留 observer，完成后再由回调收敛状态。
    if (!save.isPending) save.reset();
    if (!validate.isPending) validate.reset();
    resetAnalysis();
  };

  const saveWorkspace = (content: unknown, format: DraftFormat) => {
    if (!workspace?.dirty || publish.isPending || publishRefreshPending) return;
    const submittedBase = workspace.baseSemanticHash;
    const submitted: SubmittedDraftSnapshot = {
      revision: workspace.baseRevision,
      text: workspace.text,
      format: workspace.format
    };
    save.mutate(
      {
        packageId,
        content,
        format,
        expectedRevision: workspace.baseRevision,
        ...(submittedBase === undefined ? {} : { baseSemanticHash: submittedBase })
      },
      {
        onSuccess: (result) => {
          setWorkspace((current) =>
            absorbDraftMutation(current, result, currentSemanticHash, submittedBase, submitted)
          );
          validate.reset();
          resetAnalysis();
          show({ title: `草稿已保存（revision ${result.revision}）`, tone: 'success' });
        },
        onError: (error) => {
          if (errorCode(error) === 'RULE_DRAFT_CONFLICT') void draft.refetch();
        }
      }
    );
  };

  const validateWorkspace = () => {
    if (
      workspace === null ||
      workspace.dirty ||
      workspace.serverDraft === null ||
      publish.isPending ||
      publishRefreshPending
    )
      return;
    const submittedBase = workspace.baseSemanticHash;
    const submitted: SubmittedDraftSnapshot = {
      revision: workspace.baseRevision,
      text: workspace.text,
      format: workspace.format
    };
    validate.mutate(
      { packageId, expectedRevision: workspace.baseRevision },
      {
        onSuccess: (result) => {
          setWorkspace((current) =>
            absorbDraftMutation(current, result.draft, currentSemanticHash, submittedBase, submitted)
          );
          save.reset();
          resetAnalysis();
          show({
            title: result.valid ? '草稿校验通过' : '草稿校验未通过',
            tone: result.valid ? 'success' : 'danger'
          });
        },
        onError: (error) => {
          if (errorCode(error) === 'RULE_DRAFT_CONFLICT') void draft.refetch();
        }
      }
    );
  };

  const adoptLatestDraft = () => {
    void (async () => {
      const result = await draft.refetch();
      if (result.data !== undefined) {
        setWorkspace(workspaceFromDraft(result.data, currentSemanticHash));
      } else if (result.isError && errorCode(result.error) === 'NOT_FOUND') {
        setWorkspace(workspaceFromDraft(null, currentSemanticHash));
      }
      save.reset();
      validate.reset();
      resetAnalysis();
    })();
  };

  const refreshAfterPublish = async (version: RuleVersion) => {
    setPublishRefreshPending(true);
    setImpactEvidence(null);
    impact.reset();
    save.reset();
    validate.reset();
    try {
      const [latestDraft] = await Promise.all([draft.refetch(), rulePackage.refetch()]);
      if (latestDraft.data !== undefined) {
        // 即使服务端响应来自旧版本，也把下一轮父版本明确推进到刚发布的 immutable 版本。
        setWorkspace((current) => {
          const server = workspaceFromDraft(latestDraft.data, version.semanticHash, version.semanticHash);
          if (current?.dirty !== true) return server;
          return {
            ...server,
            text: current.text,
            format: current.format,
            dirty: current.text !== server.text || current.format !== server.format
          };
        });
      } else {
        // 发布已经成功；刷新失败时宁可锁住编辑器，也不能继续携带旧 revision 写入。
        setWorkspace(null);
      }
    } finally {
      setPublishRefreshPending(false);
    }
  };

  const remoteChanged =
    workspace?.dirty === true && (workspace.serverDraft?.revision ?? 0) !== workspace.baseRevision;

  return (
    <>
      <PageHeader
        title="规则包"
        lead={
          <>
            <Link to="/rules">← 返回规则</Link>
            {' · '}
            草稿保存使用 If-Match 修订号；并发冲突会被如实报告，界面不会覆盖别人的编辑。
          </>
        }
      />

      <Section title="规则包信息">
        <AsyncPanel query={rulePackage}>
          {(data) => (
            <Facts
              items={[
                { term: '名称', value: data.name },
                { term: '说明', value: data.description === '' ? <Absent /> : data.description },
                {
                  term: '状态',
                  value: <Badge tone={data.status === 'active' ? 'success' : 'neutral'}>{data.status}</Badge>
                },
                { term: '规则包 ID', value: <MonoId value={data.id} label="规则包 ID" /> },
                { term: 'ruleSetId', value: data.ruleSetId },
                {
                  term: '当前版本',
                  value:
                    data.currentSemanticHash === undefined ? (
                      <Absent>尚未发布</Absent>
                    ) : (
                      <MonoId value={data.currentSemanticHash} label="semanticHash" />
                    )
                },
                {
                  term: '最近有效版本',
                  value:
                    data.latestValidSemanticHash === undefined ? (
                      <Absent />
                    ) : (
                      <MonoId value={data.latestValidSemanticHash} label="semanticHash" />
                    )
                },
                { term: '规则包 revision', value: data.revision },
                { term: '创建者', value: data.createdBy },
                { term: '更新时间', value: formatDateTime(data.updatedAt) }
              ]}
            />
          )}
        </AsyncPanel>
      </Section>

      <Section
        title="草稿"
        description="草稿是并发编辑对象。保存必须带 If-Match（草稿 revision），冲突时服务端返回 409 RULE_DRAFT_CONFLICT。"
      >
        <ContractNoteList area="rules" only={['rules-draft-if-match']} />
        <DraftEditor
          workspace={workspace}
          draft={draft}
          save={save}
          validate={validate}
          isLocked={publish.isPending || publishRefreshPending}
          remoteChanged={remoteChanged}
          onEdit={editWorkspace}
          onSave={saveWorkspace}
          onValidate={validateWorkspace}
          onAdoptLatest={adoptLatestDraft}
        />
      </Section>

      <AsyncPanel query={rulePackage}>
        {(data) => (
          <ImpactPanel
            currentSemanticHash={semanticHashOrUndefined(data.currentSemanticHash)}
            workspace={workspace}
            impact={impact}
            evidence={impactEvidence}
            onEvidence={setImpactEvidence}
          />
        )}
      </AsyncPanel>

      <PublishPanel
        packageId={packageId}
        currentSemanticHash={currentSemanticHash}
        workspace={workspace}
        evidence={impactEvidence}
        publish={publish}
        draftMutationPending={save.isPending || validate.isPending}
        onPublished={refreshAfterPublish}
        onConflict={() => void draft.refetch()}
      />
      <RollbackPanel packageId={packageId} packageRevision={rulePackage.data?.revision ?? null} />
      <AuditPanel packageId={packageId} />
    </>
  );
}

export function RulePackagePage() {
  const params = useParams<{ packageId: string }>();
  const packageId = params.packageId ?? '';
  return <RulePackageContent key={packageId} packageId={packageId} />;
}
