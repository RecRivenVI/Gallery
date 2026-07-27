import { useEffect, useState } from 'react';
import { Badge, Checkbox, Select, TextInput, useToast } from '../../design';
import { errorCode } from '../../shared/errors';
import { useCapability } from '../../shared/session';
import {
  useDeprecateRulePackage,
  useDeprecateRuleVersion,
  useRollbackRulePackage,
  useRuleVersionDiff,
  useRuleVersions,
  type RulePackage,
  type RuleVersion
} from '../api';
import { IMPACT_CATEGORY_LABELS, IMPACT_CATEGORY_TONES } from '../labels';
import {
  Absent,
  AsyncPanel,
  BoolBadge,
  ConfirmAction,
  DataTable,
  Facts,
  InlineError,
  MonoId,
  Section,
  formatDateTime
} from '../ui';

export interface RuleVersionLifecyclePanelProps {
  packageId: string;
  packageStatus: RulePackage['status'] | undefined;
  packageRevision: number | null;
  currentSemanticHash: string | undefined;
}

function versionLabel(version: RuleVersion, currentSemanticHash: string | undefined): string {
  const state = version.semanticHash === currentSemanticHash ? '当前' : version.status;
  return `${version.version} · ${state} · ${version.semanticHash.slice(0, 12)}…`;
}

/**
 * RulePackage current、不可变 RuleVersion 与 SourceRuleBinding 是三个不同状态。
 * 这里故意不把 rollback 写成“部署”：回滚不改 Binding，也不创建扫描任务。
 */
export function RuleVersionLifecyclePanel({
  packageId,
  packageStatus,
  packageRevision,
  currentSemanticHash
}: RuleVersionLifecyclePanelProps) {
  const { show } = useToast();
  const canPublish = useCapability('rules.publish');
  const versions = useRuleVersions(packageId);
  const rollback = useRollbackRulePackage();
  const deprecateVersion = useDeprecateRuleVersion();
  const deprecatePackage = useDeprecateRulePackage();
  const [rollbackTarget, setRollbackTarget] = useState<string | null>(null);
  const [rollbackReason, setRollbackReason] = useState('');
  const [confirmImpact, setConfirmImpact] = useState(false);
  const [deprecateTarget, setDeprecateTarget] = useState<string | null>(null);
  const [versionReason, setVersionReason] = useState('');
  const [packageReason, setPackageReason] = useState('');
  const diff = useRuleVersionDiff(currentSemanticHash ?? null, rollbackTarget);

  useEffect(() => {
    setConfirmImpact(false);
  }, [currentSemanticHash, rollbackTarget, diff.data]);

  const rollbackNeedsConfirmation = diff.data?.bindingReview === true;
  const rollbackReady =
    packageStatus === 'active' &&
    packageRevision !== null &&
    currentSemanticHash !== undefined &&
    rollbackTarget !== null &&
    rollbackReason.trim() !== '' &&
    diff.data !== undefined &&
    diff.data.parameterCompatible &&
    (!rollbackNeedsConfirmation || confirmImpact);

  return (
    <>
      <Section
        title="版本与回滚"
        description="回滚只切换规则包的 current 指针，并可重新激活已弃用目标；既有 Binding、ParameterSet 和已入队 Job 均不改变，也不会自动创建扫描或重投影任务。"
      >
        <AsyncPanel query={versions}>
          {(data) => {
            const rollbackOptions = data.items
              .filter((item) => item.semanticHash !== currentSemanticHash && item.executable === true)
              .map((item) => ({
                id: item.semanticHash,
                label: versionLabel(item, currentSemanticHash)
              }));
            const deprecateOptions = data.items
              .filter((item) => item.semanticHash !== currentSemanticHash && item.status === 'published')
              .map((item) => ({
                id: item.semanticHash,
                label: versionLabel(item, currentSemanticHash)
              }));

            return (
              <>
                {canPublish && packageStatus === 'active' ? (
                  <div className="manage-form">
                    <Select
                      label="回滚到"
                      placeholder="选择另一个可执行版本"
                      options={rollbackOptions}
                      selectedKey={rollbackTarget}
                      onSelectionChange={setRollbackTarget}
                      description="已弃用版本可以作为显式恢复目标，并会在成功后重新激活；当前版本和不可执行版本不会出现在这里。"
                    />
                    {rollbackTarget === null ? null : diff.isPending ? (
                      <p className="manage-section__description" role="status">
                        正在读取当前版本与目标版本的持久差异…
                      </p>
                    ) : diff.isError ? (
                      <InlineError error={diff.error} title="无法读取回滚差异" />
                    ) : (
                      <>
                        <Facts
                          items={[
                            {
                              term: '影响分类',
                              value: (
                                <Badge tone={IMPACT_CATEGORY_TONES[diff.data.category]}>
                                  {IMPACT_CATEGORY_LABELS[diff.data.category]}
                                </Badge>
                              )
                            },
                            {
                              term: '参数兼容',
                              value: (
                                <BoolBadge
                                  value={diff.data.parameterCompatible}
                                  trueLabel="兼容"
                                  falseLabel="不兼容，禁止回滚"
                                  falseTone="danger"
                                />
                              )
                            },
                            {
                              term: '需要复核 Binding',
                              value: diff.data.bindingReview ? '需要' : '不需要'
                            }
                          ]}
                        />
                        <DataTable
                          caption="回滚版本差异"
                          rows={diff.data.entries}
                          rowKey={(row) => `${row.path}:${row.change}`}
                          emptyTitle="两个版本没有运行语义差异"
                          columns={[
                            { id: 'path', header: '字段', render: (row) => row.path },
                            { id: 'change', header: '变化', render: (row) => row.change },
                            {
                              id: 'impact',
                              header: '影响',
                              render: (row) => IMPACT_CATEGORY_LABELS[row.impactCategory]
                            },
                            {
                              id: 'follow-up',
                              header: '后续建议',
                              render: (row) =>
                                [
                                  row.requiresRescan ? '显式重扫' : '',
                                  row.requiresReprojection ? '显式重投影' : ''
                                ]
                                  .filter(Boolean)
                                  .join('、') || '无'
                            }
                          ]}
                        />
                        <p className="manage-section__description">
                          上述重扫或重投影只是建议；回滚成功后仍需先显式调整
                          SourceRuleBinding，再由用户创建对应任务。
                        </p>
                      </>
                    )}
                    <TextInput
                      label="回滚理由"
                      value={rollbackReason}
                      onChange={setRollbackReason}
                      description="会与前后 semantic hash 一起写入规则审计。"
                      isRequired
                    />
                    {rollbackNeedsConfirmation ? (
                      <Checkbox isSelected={confirmImpact} onChange={setConfirmImpact}>
                        我已审阅上面的版本差异，并确认需要人工复核既有 Binding
                      </Checkbox>
                    ) : null}
                    <div className="manage-form__actions">
                      <ConfirmAction
                        label="回滚 current 指针"
                        dialogTitle="回滚规则包 current 指针"
                        confirmLabel="确认回滚指针"
                        isDisabled={!rollbackReady}
                        isPending={rollback.isPending}
                        description="只修改规则包 current 指针；不会修改任何既有 Binding、ParameterSet 或 Job，也不会自动创建扫描任务。"
                        onConfirm={() => {
                          if (rollbackTarget === null || packageRevision === null || !rollbackReady) {
                            return;
                          }
                          rollback.mutate(
                            {
                              packageId,
                              targetSemanticHash: rollbackTarget,
                              expectedRevision: packageRevision,
                              reason: rollbackReason.trim(),
                              confirmImpact
                            },
                            {
                              onSuccess: () => {
                                setRollbackTarget(null);
                                setRollbackReason('');
                                setConfirmImpact(false);
                                show({ title: '规则包 current 指针已回滚', tone: 'success' });
                              }
                            }
                          );
                        }}
                      />
                    </div>
                    <InlineError error={rollback.error} title="回滚被拒绝" />
                  </div>
                ) : (
                  <p className="manage-section__description">
                    {packageStatus === 'deprecated'
                      ? '规则包已弃用，current 指针不可再修改。'
                      : '当前主体在 global scope 没有 rules.publish，回滚入口已隐藏。'}
                  </p>
                )}

                {canPublish ? (
                  <div className="manage-form">
                    <h3 className="manage-section__title">弃用非当前版本</h3>
                    <Select
                      label="要弃用的 RuleVersion"
                      placeholder="选择非 current 的 published 版本"
                      options={deprecateOptions}
                      selectedKey={deprecateTarget}
                      onSelectionChange={setDeprecateTarget}
                      description="current 版本和已弃用版本不会出现在这里；被 active Binding 使用的版本会由服务端以 RULE_VERSION_IN_USE 拒绝。"
                    />
                    <TextInput
                      label="版本弃用理由"
                      value={versionReason}
                      onChange={setVersionReason}
                      isRequired
                    />
                    <div className="manage-form__actions">
                      <ConfirmAction
                        label="弃用所选 RuleVersion"
                        dialogTitle="弃用不可变 RuleVersion"
                        confirmLabel="确认弃用版本"
                        description="弃用不会删除历史 canonical JSON 或已入队 Job；若仍被 active Binding 使用，服务端会拒绝。"
                        isDisabled={deprecateTarget === null || versionReason.trim() === ''}
                        isPending={deprecateVersion.isPending}
                        onConfirm={() => {
                          if (deprecateTarget === null || versionReason.trim() === '') return;
                          deprecateVersion.mutate(
                            { semanticHash: deprecateTarget, reason: versionReason.trim() },
                            {
                              onSuccess: () => {
                                setDeprecateTarget(null);
                                setVersionReason('');
                                show({ title: 'RuleVersion 已弃用', tone: 'success' });
                              }
                            }
                          );
                        }}
                      />
                    </div>
                    <InlineError
                      error={deprecateVersion.error}
                      title={
                        errorCode(deprecateVersion.error) === 'RULE_VERSION_IN_USE'
                          ? 'RuleVersion 仍被 current 指针或 active Binding 使用'
                          : 'RuleVersion 未能弃用'
                      }
                    />
                  </div>
                ) : null}

                <DataTable
                  caption="RuleVersion 列表"
                  rows={data.items}
                  rowKey={(row) => row.semanticHash}
                  emptyTitle="该规则包还没有已发布版本"
                  columns={[
                    {
                      id: 'version',
                      header: '版本',
                      render: (row) => (
                        <>
                          {row.version}{' '}
                          {row.semanticHash === currentSemanticHash ? (
                            <Badge tone="accent">current</Badge>
                          ) : null}
                        </>
                      )
                    },
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
                    {
                      id: 'published',
                      header: '发布时间',
                      render: (row) => formatDateTime(row.publishedAt)
                    }
                  ]}
                />
              </>
            );
          }}
        </AsyncPanel>
      </Section>

      <Section
        title="规则包弃用"
        description="这是不可恢复的单向状态变化。既有 RuleVersion、ParameterSet、Binding 和已入队 Job 保持原事实；版本与参数集仍可继续弃用。"
      >
        {packageStatus === 'deprecated' ? (
          <p className="manage-section__description">
            该规则包已弃用。草稿、Impact、发布、回滚、参数集创建/更新/复制和新 Binding 入口均已锁定。
          </p>
        ) : canPublish ? (
          <div className="manage-form">
            <TextInput label="规则包弃用理由" value={packageReason} onChange={setPackageReason} isRequired />
            <div className="manage-form__actions">
              <ConfirmAction
                label="永久弃用规则包"
                dialogTitle="永久弃用规则包"
                confirmLabel="确认永久弃用"
                description="此操作不可恢复。它不会改写既有 Binding、ParameterSet 或 Job，但会锁定该规则包的全部作者操作和新采用入口。"
                isDisabled={packageRevision === null || packageReason.trim() === ''}
                isPending={deprecatePackage.isPending}
                onConfirm={() => {
                  if (packageRevision === null || packageReason.trim() === '') return;
                  deprecatePackage.mutate(
                    { packageId, expectedRevision: packageRevision, reason: packageReason.trim() },
                    {
                      onSuccess: () => {
                        setPackageReason('');
                        show({ title: '规则包已永久弃用', tone: 'success' });
                      }
                    }
                  );
                }}
              />
            </div>
            <InlineError error={deprecatePackage.error} title="规则包未能弃用" />
          </div>
        ) : (
          <p className="manage-section__description">
            当前主体在 global scope 没有 rules.publish，规则包弃用入口已隐藏。
          </p>
        )}
      </Section>
    </>
  );
}
