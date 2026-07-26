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

import { useCallback, useEffect, useState } from 'react';
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
  type RuleDraft
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

interface DraftEditorProps {
  packageId: string;
  onDraftLoaded: (draft: RuleDraft | null) => void;
  /**
   * 把编辑器当前文本抬给父组件，供影响评估使用。
   *
   * 刻意传**字符串**而不是解析后的对象：解析结果每次渲染都是新对象，放进 effect 依赖或
   * setState 会让「渲染→setState→渲染」无限循环。字符串是幂等的，且只在真正的输入事件里回调。
   */
  onDraftText: (text: string, format: DraftFormat) => void;
}

function DraftEditor({ packageId, onDraftLoaded, onDraftText }: DraftEditorProps) {
  const { show } = useToast();
  const draft = useRuleDraft(packageId);
  const save = useSaveRuleDraft();
  const validate = useValidateRuleDraft();
  const canWrite = useCapability('rules.write');

  const [text, setText] = useState<string | null>(null);
  const [format, setFormat] = useState<DraftFormat>('json');
  /** 本地编辑所基于的草稿 revision。null 表示服务端尚无草稿，首次保存不带 If-Match。 */
  const [baseRevision, setBaseRevision] = useState<number | null>(null);

  const missing = draft.isError && errorCode(draft.error) === 'NOT_FOUND';

  useEffect(() => {
    if (text !== null) return;
    if (draft.data !== undefined) {
      const initial = stringifyDraft(draft.data.content, draft.data.format);
      setText(initial);
      setFormat(draft.data.format);
      setBaseRevision(draft.data.revision);
      onDraftLoaded(draft.data);
      onDraftText(initial, draft.data.format);
      return;
    }
    if (missing) {
      setText('');
      setBaseRevision(null);
      onDraftLoaded(null);
      onDraftText('', 'json');
    }
  }, [draft.data, missing, text, onDraftLoaded, onDraftText]);

  const parsed = parseDraft(text ?? '', format);
  const conflict = errorCode(save.error) === 'RULE_DRAFT_CONFLICT';

  if (draft.isPending && draft.fetchStatus !== 'idle') {
    return <p className="manage-section__description">正在读取草稿…</p>;
  }
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

  return (
    <div className="manage-form">
      <Facts
        items={[
          {
            term: '服务端草稿 revision',
            value: draft.data === undefined ? <Absent>尚无草稿</Absent> : draft.data.revision
          },
          { term: '本地编辑基于的 revision', value: baseRevision ?? <Absent>无（首次保存）</Absent> },
          {
            term: '校验状态',
            value:
              draft.data === undefined ? (
                <Absent />
              ) : (
                <Badge
                  tone={
                    draft.data.validationStatus === 'validated'
                      ? 'success'
                      : draft.data.validationStatus === 'invalid'
                        ? 'danger'
                        : 'neutral'
                  }
                >
                  {draft.data.validationStatus}
                </Badge>
              )
          },
          { term: '最后保存者', value: draft.data?.savedBy ?? <Absent /> },
          { term: '最后保存时间', value: formatDateTime(draft.data?.updatedAt) }
        ]}
      />

      <Select
        label="草稿格式"
        options={DRAFT_FORMATS.map((value) => ({ id: value, label: value.toUpperCase() }))}
        selectedKey={format}
        onSelectionChange={(key) => {
          if (key === null) return;
          const next = key as DraftFormat;
          setFormat(next);
          onDraftText(text ?? '', next);
        }}
        description="YAML/TOML 只是导入形态，服务端会转换为规范 JSON 后再保存。运行时唯一事实源永远是规范 JSON。"
      />

      <TextInput
        label="草稿内容"
        value={text ?? ''}
        onChange={(next) => {
          setText(next);
          onDraftText(next, format);
        }}
        isMultiline
        rows={18}
        isDisabled={!canWrite}
        errorMessage={format === 'json' ? parsed.error : undefined}
      />

      {canWrite ? (
        <div className="manage-form__actions">
          <Button
            variant="primary"
            isPending={save.isPending}
            isDisabled={(text ?? '') === '' || (format === 'json' && parsed.error !== undefined)}
            onPress={() => {
              save.mutate(
                {
                  packageId,
                  content: parsed.value,
                  format,
                  expectedRevision: baseRevision
                },
                {
                  onSuccess: (result) => {
                    const saved = stringifyDraft(result.content, result.format);
                    setBaseRevision(result.revision);
                    setText(saved);
                    onDraftLoaded(result);
                    onDraftText(saved, result.format);
                    show({ title: `草稿已保存（revision ${result.revision}）`, tone: 'success' });
                  }
                }
              );
            }}
          >
            保存草稿
          </Button>
          <Button
            variant="secondary"
            isPending={validate.isPending}
            onPress={() => {
              validate.mutate({ packageId, expectedRevision: baseRevision });
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

      {conflict ? (
        <div className="manage-inline-error" role="alert">
          <p className="manage-inline-error__title">草稿已被其他会话修改</p>
          <p className="manage-inline-error__body">
            你的编辑基于 revision {baseRevision ?? 0}，而服务端已经前进到更新的版本，因此这次保存被 409
            RULE_DRAFT_CONFLICT 拒绝。界面不会覆盖别人的修改：请先复制你的改动，再用下面的按钮
            载入服务端最新草稿，然后重新应用你的改动。
          </p>
          <p className="manage-inline-error__code">
            RULE_DRAFT_CONFLICT
            {errorCorrelationId(save.error) === undefined ? '' : ` · ${errorCorrelationId(save.error)}`}
          </p>
          <div className="manage-form__actions">
            <ConfirmAction
              label="用服务端最新草稿覆盖编辑器"
              dialogTitle="覆盖本地编辑"
              confirmLabel="确认覆盖"
              description="编辑器里未保存的改动会被服务端最新草稿替换。请先自行复制需要保留的内容。"
              onConfirm={() => {
                void (async () => {
                  const result = await draft.refetch();
                  const latest = result.data;
                  if (latest !== undefined) {
                    const reloaded = stringifyDraft(latest.content, latest.format);
                    setText(reloaded);
                    setFormat(latest.format);
                    setBaseRevision(latest.revision);
                    onDraftLoaded(latest);
                    onDraftText(reloaded, latest.format);
                    save.reset();
                  }
                })();
              }}
            />
          </div>
        </div>
      ) : (
        <InlineError error={save.error} title="草稿未能保存" />
      )}

      <InlineError error={validate.error} title="校验未能完成" />
      {validate.data === undefined ? null : (
        <>
          <p className="manage-section__description">
            <Badge tone={validate.data.valid ? 'success' : 'danger'}>
              {validate.data.valid ? '校验通过' : '校验未通过'}
            </Badge>
          </p>
          {validate.data.diagnostics.length === 0 ? null : (
            <pre className="manage-code">{JSON.stringify(validate.data.diagnostics, null, 2)}</pre>
          )}
        </>
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
  draftText,
  draftFormat
}: {
  currentSemanticHash: string | undefined;
  draftText: string;
  draftFormat: DraftFormat;
}) {
  const canWrite = useCapability('rules.write');
  const before = useExportRuleVersion(currentSemanticHash ?? null);
  const impact = useRuleImpact();

  const beforePackage: Record<string, unknown> = before.data ?? {};
  const afterPackage = draftAsObject(draftText, draftFormat);

  return (
    <Section
      title="影响评估"
      description="发布前必须看这里：它决定这次修改会不会触发重扫、重投影、搜索重建或派生资源重建。契约要求 rules.write —— 只读分析却要写权限，是已知的过度限制。"
      actions={
        canWrite ? (
          <Button
            variant="secondary"
            isPending={impact.isPending}
            isDisabled={afterPackage === null}
            onPress={() => {
              if (afterPackage === null) return;
              impact.mutate({ before: beforePackage, after: afterPackage });
            }}
          >
            评估本次修改的影响
          </Button>
        ) : undefined
      }
    >
      {currentSemanticHash === undefined ? (
        <p className="manage-section__description">
          该规则包还没有已发布版本，因此对比基线是空对象。首次发布的影响一定被判定为需要完整重扫。
        </p>
      ) : null}
      {afterPackage === null ? (
        <p className="manage-section__description">
          影响评估只接受规范 JSON 对象作为目标状态。当前草稿要么不是 JSON 格式，要么还不是合法的 JSON
          对象；请先保存为 JSON（服务端会在保存时把 YAML/TOML 转换为规范 JSON）。
        </p>
      ) : null}
      <InlineError error={impact.error} title="影响评估未能完成" />
      {impact.data === undefined ? null : (
        <>
          <Facts
            items={[
              {
                term: '结论',
                value: (
                  <Badge tone={IMPACT_CATEGORY_TONES[impact.data.category]}>
                    {IMPACT_CATEGORY_LABELS[impact.data.category]}
                  </Badge>
                )
              },
              {
                term: '是否阻止发布',
                value: (
                  <BoolBadge
                    value={impact.data.blockPublish}
                    trueLabel="是，服务端会拒绝发布"
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
                    value={impact.data.manualConfirmation}
                    trueLabel="是"
                    falseLabel="否"
                    trueTone="warning"
                    falseTone="success"
                  />
                )
              },
              { term: '完整重扫', value: impact.data.fullRescan ? '需要' : '不需要' },
              { term: '部分重扫', value: impact.data.partialRescan ? '需要' : '不需要' },
              { term: '重投影', value: impact.data.reproject ? '需要' : '不需要' },
              { term: '重建搜索', value: impact.data.rebuildSearch ? '需要' : '不需要' },
              { term: '重建派生资源', value: impact.data.rebuildDerived ? '需要' : '不需要' },
              { term: '需要复核绑定', value: impact.data.bindingReview ? '需要' : '不需要' },
              {
                term: '受影响来源',
                value:
                  impact.data.affectedSources.length === 0 ? (
                    <Absent />
                  ) : (
                    impact.data.affectedSources.join('、')
                  )
              },
              {
                term: '涉及字段',
                value: impact.data.fields.length === 0 ? <Absent /> : impact.data.fields.join('、')
              },
              {
                term: '原因码',
                value: impact.data.reasonCodes.length === 0 ? <Absent /> : impact.data.reasonCodes.join('、')
              },
              { term: '预计任务', value: impact.data.estimatedJob ?? <Absent /> }
            ]}
          />
          {impact.data.traceSummary === undefined || impact.data.traceSummary.length === 0 ? null : (
            <pre className="manage-code">{impact.data.traceSummary.join('\n')}</pre>
          )}
        </>
      )}
    </Section>
  );
}

/* ————————————————————————————— 发布与回滚 ————————————————————————————— */

function PublishPanel({ packageId, draftRevision }: { packageId: string; draftRevision: number | null }) {
  const { show } = useToast();
  const publish = usePublishRuleDraft();
  const canPublish = useCapability('rules.publish');
  const [reason, setReason] = useState('');

  return (
    <Section
      title="发布"
      description="发布把当前草稿冻结成不可变的 RuleVersion，并把规则包的当前版本指针指向它。仍有未通过的校验或未确认的影响时返回 409 RULE_PUBLISH_BLOCKED。"
    >
      {canPublish ? (
        <div className="manage-form">
          <TextInput label="发布理由" value={reason} onChange={setReason} description="会写入规则包审计。" />
          <div className="manage-form__actions">
            <ConfirmAction
              label="发布草稿"
              dialogTitle="发布规则草稿"
              confirmLabel="确认发布"
              variant="primary"
              isPending={publish.isPending}
              description="发布后该版本不可变，并可能触发重扫或重投影任务。请先确认影响评估的结论。"
              onConfirm={() => {
                publish.mutate(
                  { packageId, expectedRevision: draftRevision, reason },
                  {
                    onSuccess: (version) => {
                      show({
                        title: '规则版本已发布',
                        description: `semanticHash ${version.semanticHash}`,
                        tone: 'success'
                      });
                    }
                  }
                );
              }}
            />
          </div>
          <InlineError error={publish.error} title="发布被拒绝" />
          {errorCode(publish.error) === 'RULE_PUBLISH_BLOCKED' ? (
            <p className="manage-section__description">
              服务端阻止了这次发布：草稿仍有未通过的校验，或影响评估要求人工确认。请先完成校验并复核
              影响评估结论，再重新发布。
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

function RollbackPanel({ packageId }: { packageId: string }) {
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
                    isDisabled={target === null || reason.trim() === ''}
                    isPending={rollback.isPending}
                    description="回滚会立即改变该规则包在所有绑定处的运行语义，并可能触发重扫。"
                    onConfirm={() => {
                      if (target === null) return;
                      rollback.mutate(
                        {
                          packageId,
                          targetSemanticHash: target,
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

export function RulePackagePage() {
  const params = useParams<{ packageId: string }>();
  const packageId = params.packageId ?? '';
  const rulePackage = useRulePackage(params.packageId);
  const [draftRevision, setDraftRevision] = useState<number | null>(null);
  const [draftSource, setDraftSource] = useState<{ text: string; format: DraftFormat }>({
    text: '',
    format: 'json'
  });
  const handleDraftLoaded = useCallback((draft: RuleDraft | null) => {
    setDraftRevision(draft === null ? null : draft.revision);
  }, []);
  const handleDraftText = useCallback((text: string, format: DraftFormat) => {
    setDraftSource({ text, format });
  }, []);

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
        <DraftEditor packageId={packageId} onDraftLoaded={handleDraftLoaded} onDraftText={handleDraftText} />
      </Section>

      <AsyncPanel query={rulePackage}>
        {(data) => (
          <ImpactPanel
            currentSemanticHash={data.currentSemanticHash}
            draftText={draftSource.text}
            draftFormat={draftSource.format}
          />
        )}
      </AsyncPanel>

      <PublishPanel packageId={packageId} draftRevision={draftRevision} />
      <RollbackPanel packageId={packageId} />
      <AuditPanel packageId={packageId} />
    </>
  );
}
