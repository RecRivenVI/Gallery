import { useEffect, useRef, useState } from 'react';
import { Badge, Button, Checkbox, Select, TextInput, useToast } from '../../design';
import { useCapability } from '../../shared/session';
import {
  useCopyRuleParameterSet,
  useCreateRuleParameterSet,
  useDeprecateRuleParameterSet,
  useImpactRuleParameterSet,
  useRuleParameterSets,
  useRuleVersions,
  useUpdateRuleParameterSet,
  type RuleImpactResult,
  type RulePackage,
  type RuleParameterSet
} from '../api';
import { IMPACT_CATEGORY_LABELS, IMPACT_CATEGORY_TONES } from '../labels';
import { isRecord, parseRuleValue } from './lossless';
import {
  AsyncPanel,
  ConfirmAction,
  DataTable,
  Facts,
  InlineError,
  MonoId,
  Section,
  formatDateTime
} from '../ui';

interface ParameterWorkspace {
  server: RuleParameterSet;
  text: string;
  baseRevision: number;
  dirty: boolean;
}

interface ParameterImpactEvidence {
  parameterId: string;
  revision: number;
  text: string;
  result: RuleImpactResult;
}

export interface RuleParameterSetsPanelProps {
  packageId: string;
  packageStatus: RulePackage['status'] | undefined;
  currentSemanticHash: string | undefined;
}

function validateObjectText(text: string): string | undefined {
  try {
    if (!isRecord(parseRuleValue(text))) return '参数必须是 JSON 对象';
    return undefined;
  } catch (error) {
    return error instanceof Error ? error.message : '不是合法的 JSON';
  }
}

function freshWorkspace(server: RuleParameterSet): ParameterWorkspace {
  return {
    server,
    text: server.parametersText,
    baseRevision: server.currentRevision,
    dirty: false
  };
}

/**
 * ParameterSet 是带 revision 的共享对象。Update 会在同一服务端事务刷新引用它的 Binding，
 * 但不会改写已入队 Job，也不会自动创建扫描或重投影任务。
 */
export function RuleParameterSetsPanel({
  packageId,
  packageStatus,
  currentSemanticHash
}: RuleParameterSetsPanelProps) {
  const { show } = useToast();
  const canWrite = useCapability('rules.write');
  const versions = useRuleVersions(packageId);
  const [semanticHash, setSemanticHash] = useState<string | null>(currentSemanticHash ?? null);
  const parameterSets = useRuleParameterSets(semanticHash);
  const create = useCreateRuleParameterSet();
  const impact = useImpactRuleParameterSet();
  const update = useUpdateRuleParameterSet();
  const copy = useCopyRuleParameterSet();
  const deprecate = useDeprecateRuleParameterSet();
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [workspace, setWorkspace] = useState<ParameterWorkspace | null>(null);
  const workspaceRef = useRef<ParameterWorkspace | null>(null);
  const [impactEvidence, setImpactEvidence] = useState<ParameterImpactEvidence | null>(null);
  const [confirmImpact, setConfirmImpact] = useState(false);
  const [createName, setCreateName] = useState('');
  const [createText, setCreateText] = useState('{}');
  const [copyName, setCopyName] = useState('');
  const [deprecateReason, setDeprecateReason] = useState('');

  useEffect(() => {
    workspaceRef.current = workspace;
  }, [workspace]);

  useEffect(() => {
    if (versions.data === undefined) return;
    const available = versions.data.items.filter((item) => item.executable === true);
    if (semanticHash !== null && available.some((item) => item.semanticHash === semanticHash)) return;
    const current = available.find((item) => item.semanticHash === currentSemanticHash);
    setSemanticHash(current?.semanticHash ?? available[0]?.semanticHash ?? null);
  }, [currentSemanticHash, semanticHash, versions.data]);

  useEffect(() => {
    const items = parameterSets.data?.parameterSets;
    if (items === undefined) return;
    if (selectedId !== null && items.some((item) => item.id === selectedId)) return;
    setSelectedId(items.find((item) => item.status === 'active')?.id ?? items[0]?.id ?? null);
  }, [parameterSets.data, selectedId]);

  useEffect(() => {
    const selected = parameterSets.data?.parameterSets.find((item) => item.id === selectedId);
    if (selected === undefined) {
      setWorkspace(null);
      setImpactEvidence(null);
      setConfirmImpact(false);
      return;
    }
    setWorkspace((previous) => {
      if (previous?.server.id !== selected.id) return freshWorkspace(selected);
      if (previous.dirty) return { ...previous, server: selected };
      // Mutations update the local workspace before the invalidated list query has refetched.
      // Do not let the stale cached row roll a successful CAS response back on screen.
      if (selected.currentRevision < previous.server.currentRevision) return previous;
      if (previous.server.status === 'deprecated' && selected.status === 'active') return previous;
      return freshWorkspace(selected);
    });
  }, [parameterSets.data, selectedId]);

  const parameterError = workspace === null ? undefined : validateObjectText(workspace.text);
  const createError = validateObjectText(createText);
  const remoteChanged =
    workspace !== null && workspace.dirty && workspace.server.currentRevision !== workspace.baseRevision;
  const currentEvidence =
    workspace !== null &&
    impactEvidence !== null &&
    impactEvidence.parameterId === workspace.server.id &&
    impactEvidence.revision === workspace.baseRevision &&
    impactEvidence.text === workspace.text
      ? impactEvidence
      : null;
  const selectedVersion = versions.data?.items.find((item) => item.semanticHash === semanticHash);
  const updateReady =
    packageStatus === 'active' &&
    selectedVersion?.status === 'published' &&
    workspace !== null &&
    workspace.server.status === 'active' &&
    workspace.dirty &&
    !remoteChanged &&
    parameterError === undefined &&
    currentEvidence !== null &&
    confirmImpact;

  useEffect(() => {
    setConfirmImpact(false);
  }, [currentEvidence]);

  return (
    <Section
      title="共享 ParameterSet"
      description="参数集按 revision/hash 保存并可被多个 SourceRuleBinding 复用。更新只会在服务端事务内刷新引用它的 Binding；已入队 Job 保持原快照，扫描或重投影仍需用户显式创建任务。"
    >
      <AsyncPanel query={versions}>
        {(versionData) => (
          <Select
            label="RuleVersion"
            placeholder="选择参数 Schema 所属版本"
            options={versionData.items
              .filter((item) => item.executable === true)
              .map((item) => ({
                id: item.semanticHash,
                label: `${item.version} · ${item.status} · ${item.semanticHash.slice(0, 12)}…`
              }))}
            selectedKey={semanticHash}
            onSelectionChange={(value) => {
              setSemanticHash(value);
              setSelectedId(null);
              setWorkspace(null);
              setImpactEvidence(null);
            }}
            description="ParameterSet 永久绑定到一个 immutable RuleVersion；规则包 current 改变不会自动迁移它。"
          />
        )}
      </AsyncPanel>

      {selectedVersion?.status === 'deprecated' ? (
        <p className="manage-section__description">
          所选 RuleVersion 已弃用：既有 ParameterSet 仍可查看或弃用，但不能创建、更新或复制。
        </p>
      ) : null}

      {semanticHash === null ? (
        <p className="manage-section__description">该规则包还没有可执行 RuleVersion。</p>
      ) : (
        <AsyncPanel query={parameterSets}>
          {(data) => (
            <>
              <DataTable
                caption="ParameterSet 列表"
                rows={data.parameterSets}
                rowKey={(row) => row.id}
                emptyTitle="该 RuleVersion 还没有 ParameterSet"
                columns={[
                  { id: 'name', header: '名称', render: (row) => row.name },
                  {
                    id: 'status',
                    header: '状态',
                    render: (row) => (
                      <Badge tone={row.status === 'active' ? 'success' : 'neutral'}>{row.status}</Badge>
                    )
                  },
                  { id: 'revision', header: 'revision', render: (row) => row.currentRevision },
                  {
                    id: 'hash',
                    header: 'parameterHash',
                    render: (row) => <MonoId value={row.currentHash} label="parameterHash" />
                  },
                  { id: 'updated', header: '更新时间', render: (row) => formatDateTime(row.updatedAt) }
                ]}
              />

              <Select
                label="正在查看的 ParameterSet"
                placeholder="选择一个参数集"
                options={data.parameterSets.map((item) => ({
                  id: item.id,
                  label: `${item.name} · r${String(item.currentRevision)} · ${item.status}`
                }))}
                selectedKey={selectedId}
                onSelectionChange={setSelectedId}
              />

              {workspace === null ? null : (
                <div className="manage-form">
                  <Facts
                    items={[
                      {
                        term: 'ParameterSet ID',
                        value: <MonoId value={workspace.server.id} label="ParameterSet ID" />
                      },
                      { term: '名称', value: workspace.server.name },
                      { term: '状态', value: workspace.server.status },
                      { term: '服务器 revision', value: workspace.server.currentRevision },
                      { term: '编辑基线 revision', value: workspace.baseRevision },
                      {
                        term: 'parameterHash',
                        value: <MonoId value={workspace.server.currentHash} label="parameterHash" />
                      }
                    ]}
                  />
                  <TextInput
                    label="参数（精确 JSON 对象文本）"
                    value={workspace.text}
                    onChange={(text) => {
                      setWorkspace((previous) =>
                        previous === null
                          ? null
                          : {
                              ...previous,
                              text,
                              dirty: text !== previous.server.parametersText
                            }
                      );
                      setImpactEvidence(null);
                      setConfirmImpact(false);
                    }}
                    isMultiline
                    rows={10}
                    isReadOnly={
                      !canWrite ||
                      packageStatus !== 'active' ||
                      selectedVersion?.status !== 'published' ||
                      workspace.server.status !== 'active'
                    }
                    errorMessage={parameterError}
                    description="文本按原字面量发送；大整数和高精度小数不会先经过 JavaScript Number。"
                  />

                  {remoteChanged ? (
                    <div className="manage-inline-error" role="alert">
                      <p className="manage-inline-error__title">ParameterSet revision 已变化</p>
                      <p className="manage-inline-error__body">
                        本地文本仍被保留，不能用旧 revision 覆盖服务器。请审阅后显式采用服务器最新版本。
                      </p>
                      <Button
                        variant="secondary"
                        onPress={() => {
                          setWorkspace(freshWorkspace(workspace.server));
                          setImpactEvidence(null);
                          setConfirmImpact(false);
                          update.reset();
                        }}
                      >
                        采用服务器最新参数
                      </Button>
                    </div>
                  ) : null}

                  {currentEvidence === null ? null : (
                    <>
                      <Facts
                        items={[
                          {
                            term: '参数影响',
                            value: (
                              <Badge tone={IMPACT_CATEGORY_TONES[currentEvidence.result.category]}>
                                {IMPACT_CATEGORY_LABELS[currentEvidence.result.category]}
                              </Badge>
                            )
                          },
                          {
                            term: '受影响 Source',
                            value: currentEvidence.result.affectedSources.length
                          },
                          {
                            term: 'Binding 复核',
                            value: currentEvidence.result.bindingReview ? '需要' : '不需要'
                          },
                          {
                            term: '完整重扫建议',
                            value: currentEvidence.result.fullRescan ? '是（不会自动执行）' : '否'
                          }
                        ]}
                      />
                      <Checkbox isSelected={confirmImpact} onChange={setConfirmImpact}>
                        我已审阅本 revision 与当前参数文本的 Impact，并确认更新共享参数集
                      </Checkbox>
                    </>
                  )}

                  {canWrite &&
                  packageStatus === 'active' &&
                  selectedVersion?.status === 'published' &&
                  workspace.server.status === 'active' ? (
                    <div className="manage-form__actions">
                      <Button
                        variant="secondary"
                        isPending={impact.isPending}
                        isDisabled={!workspace.dirty || parameterError !== undefined || remoteChanged}
                        onPress={() => {
                          const candidate = workspaceRef.current;
                          if (
                            candidate === null ||
                            !candidate.dirty ||
                            validateObjectText(candidate.text) !== undefined
                          ) {
                            return;
                          }
                          const parameterId = candidate.server.id;
                          const revision = candidate.baseRevision;
                          const text = candidate.text;
                          impact.mutate(
                            { parameterId, parameters: text },
                            {
                              onSuccess: (result) => {
                                const latest = workspaceRef.current;
                                if (
                                  latest?.server.id === parameterId &&
                                  latest.baseRevision === revision &&
                                  latest.text === text
                                ) {
                                  setImpactEvidence({ parameterId, revision, text, result });
                                }
                              }
                            }
                          );
                        }}
                      >
                        评估参数影响
                      </Button>
                      <ConfirmAction
                        label="CAS 更新 ParameterSet"
                        dialogTitle="更新共享 ParameterSet"
                        confirmLabel="确认更新共享参数"
                        variant="primary"
                        description="服务端会在同一事务刷新引用它的 Binding；不会改变已入队 Job，也不会自动创建重扫或重投影任务。"
                        isDisabled={!updateReady}
                        isPending={update.isPending}
                        onConfirm={() => {
                          const candidate = workspaceRef.current;
                          if (candidate === null || !updateReady) return;
                          update.mutate(
                            {
                              parameterId: candidate.server.id,
                              parameters: candidate.text,
                              expectedRevision: candidate.baseRevision,
                              confirmImpact: true
                            },
                            {
                              onSuccess: (saved) => {
                                setWorkspace(freshWorkspace(saved));
                                setImpactEvidence(null);
                                setConfirmImpact(false);
                                show({ title: 'ParameterSet 已更新', tone: 'success' });
                              }
                            }
                          );
                        }}
                      />
                    </div>
                  ) : null}
                  <InlineError error={impact.error} title="参数影响评估失败" />
                  <InlineError error={update.error} title="ParameterSet 更新失败" />

                  {canWrite && packageStatus === 'active' && selectedVersion?.status === 'published' ? (
                    <div className="manage-form">
                      <h3 className="manage-section__title">复制 ParameterSet</h3>
                      <TextInput label="副本名称" value={copyName} onChange={setCopyName} isRequired />
                      <div className="manage-form__actions">
                        <Button
                          variant="secondary"
                          isPending={copy.isPending}
                          isDisabled={copyName.trim() === ''}
                          onPress={() => {
                            if (copyName.trim() === '') return;
                            copy.mutate(
                              { parameterId: workspace.server.id, name: copyName.trim() },
                              {
                                onSuccess: () => {
                                  setCopyName('');
                                  show({ title: 'ParameterSet 副本已创建', tone: 'success' });
                                }
                              }
                            );
                          }}
                        >
                          复制当前 ParameterSet
                        </Button>
                      </div>
                      <InlineError error={copy.error} title="ParameterSet 未能复制" />
                    </div>
                  ) : null}

                  {canWrite && workspace.server.status === 'active' ? (
                    <div className="manage-form">
                      <h3 className="manage-section__title">弃用 ParameterSet</h3>
                      <TextInput
                        label="参数集弃用理由"
                        value={deprecateReason}
                        onChange={setDeprecateReason}
                        isRequired
                      />
                      <div className="manage-form__actions">
                        <ConfirmAction
                          label="弃用当前 ParameterSet"
                          dialogTitle="弃用共享 ParameterSet"
                          confirmLabel="确认弃用参数集"
                          description="弃用后不能更新或用于新 Binding；既有 Binding 和已入队 Job 保持冻结事实。"
                          isDisabled={deprecateReason.trim() === ''}
                          isPending={deprecate.isPending}
                          onConfirm={() => {
                            if (deprecateReason.trim() === '') return;
                            deprecate.mutate(
                              {
                                parameterId: workspace.server.id,
                                expectedRevision: workspace.server.currentRevision,
                                reason: deprecateReason.trim()
                              },
                              {
                                onSuccess: (saved) => {
                                  setWorkspace(freshWorkspace(saved));
                                  setDeprecateReason('');
                                  setImpactEvidence(null);
                                  setConfirmImpact(false);
                                  show({ title: 'ParameterSet 已弃用', tone: 'success' });
                                }
                              }
                            );
                          }}
                        />
                      </div>
                      <InlineError error={deprecate.error} title="ParameterSet 未能弃用" />
                    </div>
                  ) : null}
                </div>
              )}
            </>
          )}
        </AsyncPanel>
      )}

      {canWrite && packageStatus === 'active' ? (
        <div className="manage-form">
          <h3 className="manage-section__title">创建 ParameterSet</h3>
          <TextInput label="参数集名称" value={createName} onChange={setCreateName} isRequired />
          <TextInput
            label="初始参数（精确 JSON 对象文本）"
            value={createText}
            onChange={setCreateText}
            isMultiline
            rows={8}
            errorMessage={createError}
            description="仅对所选 immutable RuleVersion 的 parameter_schema 做服务端校验。"
          />
          <div className="manage-form__actions">
            <Button
              variant="primary"
              isPending={create.isPending}
              isDisabled={
                semanticHash === null ||
                selectedVersion?.status !== 'published' ||
                createName.trim() === '' ||
                createError !== undefined
              }
              onPress={() => {
                if (
                  semanticHash === null ||
                  selectedVersion?.status !== 'published' ||
                  createName.trim() === '' ||
                  createError !== undefined
                ) {
                  return;
                }
                create.mutate(
                  { name: createName.trim(), semanticHash, parameters: createText },
                  {
                    onSuccess: () => {
                      setCreateName('');
                      setCreateText('{}');
                      show({ title: 'ParameterSet 已创建', tone: 'success' });
                    }
                  }
                );
              }}
            >
              创建 ParameterSet
            </Button>
          </div>
          <InlineError error={create.error} title="ParameterSet 未能创建" />
        </div>
      ) : (
        <p className="manage-section__description">
          {packageStatus === 'deprecated'
            ? '规则包已弃用，只能查看或弃用既有 ParameterSet；创建、更新和复制入口已锁定。'
            : '当前主体在 global scope 没有 rules.write，ParameterSet 写入口已隐藏。'}
        </p>
      )}
    </Section>
  );
}
