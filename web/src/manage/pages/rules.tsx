/*
 * 规则：规则包清单、Source 规则绑定，以及配置编辑器的入口。
 *
 * 规则是 Source 差异的唯一解释入口，因此这一页的重点不是「填表」，而是让人看清：哪个 Source
 * 正在被哪个 RuleVersion 解释、这个绑定是不是唯一生效的那一条。
 *
 * 配置编辑器（Schema 驱动的表单生成）由另一条工作线实现。这里只核对 `GET /api/v1/rules/schema`
 * 可达并读出它的标题与版本，不生成任何表单。
 */

import { useState } from 'react';
import { Link } from 'react-router-dom';
import { Badge, Button, Select, TextInput, useToast } from '../../design';
import { useCapability } from '../../shared/session';
import { errorCode } from '../../shared/errors';
import {
  useCreateRulePackage,
  useCreateSourceRuleBinding,
  useEffectiveRuleBinding,
  useRuleParameterSets,
  useRuleSchema,
  useRulePackages,
  useRuleVersionsForPackages,
  useSourceRuleBindings,
  useSources,
  useUpdateSourceRuleBinding,
  type SourceRuleBinding
} from '../api';
import { isRecord, parseRuleValue } from '../rules/lossless';
import {
  Absent,
  AsyncPanel,
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

const SEMANTIC_HASH_PATTERN = /^[a-f0-9]{64}$/;

function isSemanticHash(value: string | undefined): value is string {
  return value !== undefined && SEMANTIC_HASH_PATTERN.test(value);
}

function jsonObjectError(text: string): string | undefined {
  try {
    return isRecord(parseRuleValue(text)) ? undefined : '必须是 JSON 对象';
  } catch (error) {
    return error instanceof Error ? error.message : '不是合法的 JSON';
  }
}

function RulePackagesPanel() {
  const { show } = useToast();
  const packages = useRulePackages();
  const create = useCreateRulePackage();
  const canWrite = useCapability('rules.write');
  const [name, setName] = useState('');
  const [ruleSetId, setRuleSetId] = useState('');
  const [description, setDescription] = useState('');

  return (
    <Section
      title="规则包"
      description="规则包承载草稿与已发布版本。已发布的 RuleVersion 不可变；修改语义会产生新的 semanticHash。"
    >
      {canWrite ? (
        <div className="manage-form">
          <div className="manage-form__row">
            <TextInput label="名称" value={name} onChange={setName} isRequired />
            <TextInput
              label="ruleSetId"
              value={ruleSetId}
              onChange={setRuleSetId}
              description="留空由服务端生成。它是规则集的稳定标识。"
            />
          </div>
          <TextInput label="说明" value={description} onChange={setDescription} />
          <div className="manage-form__actions">
            <Button
              variant="primary"
              isPending={create.isPending}
              isDisabled={name.trim() === ''}
              onPress={() => {
                create.mutate(
                  {
                    name: name.trim(),
                    ...(ruleSetId.trim() === '' ? {} : { ruleSetId: ruleSetId.trim() }),
                    ...(description.trim() === '' ? {} : { description: description.trim() })
                  },
                  {
                    onSuccess: (item) => {
                      setName('');
                      setRuleSetId('');
                      setDescription('');
                      show({ title: `规则包 ${item.name} 已创建`, tone: 'success' });
                    }
                  }
                );
              }}
            >
              创建规则包
            </Button>
          </div>
          <InlineError error={create.error} title="规则包未能创建" />
        </div>
      ) : (
        <p className="manage-section__description">
          当前主体在 global scope 没有 rules.write，创建入口已隐藏。
        </p>
      )}

      <AsyncPanel query={packages}>
        {(data) => (
          <DataTable
            caption="规则包"
            rows={data.items}
            rowKey={(row) => row.id}
            emptyTitle="还没有规则包"
            emptyDescription="没有规则包就没有可绑定的 RuleVersion，Source 的扫描会因为找不到生效绑定而失败。"
            columns={[
              {
                id: 'status',
                header: '状态',
                render: (row) => (
                  <Badge tone={row.status === 'active' ? 'success' : 'neutral'}>{row.status}</Badge>
                )
              },
              {
                id: 'name',
                header: '名称',
                render: (row) => <Link to={`/rules/${row.id}`}>{row.name}</Link>
              },
              { id: 'ruleSet', header: 'ruleSetId', render: (row) => row.ruleSetId },
              {
                id: 'current',
                header: '当前版本',
                render: (row) =>
                  isSemanticHash(row.currentSemanticHash) ? (
                    <MonoId value={row.currentSemanticHash} label="semanticHash" />
                  ) : (
                    <Absent>尚未发布</Absent>
                  )
              },
              { id: 'revision', header: 'revision', render: (row) => row.revision },
              { id: 'updated', header: '更新时间', render: (row) => formatDateTime(row.updatedAt) }
            ]}
          />
        )}
      </AsyncPanel>
    </Section>
  );
}

function BindingsPanel() {
  const { show } = useToast();
  const sources = useSources();
  const packages = useRulePackages();
  const activePackages = packages.data?.items.filter((item) => item.status === 'active') ?? [];
  const versions = useRuleVersionsForPackages(activePackages.map((item) => item.id));
  const parameterSets = useRuleParameterSets(undefined, 'active');
  const [sourceId, setSourceId] = useState<string | null>(null);
  const bindings = useSourceRuleBindings(sourceId);
  const effective = useEffectiveRuleBinding(sourceId);
  const create = useCreateSourceRuleBinding();
  const update = useUpdateSourceRuleBinding();
  const [bindingMode, setBindingMode] = useState<'direct' | 'parameter'>('direct');
  const [semanticHash, setSemanticHash] = useState<string | null>(null);
  const [parameterId, setParameterId] = useState<string | null>(null);
  const [priority, setPriority] = useState('100');
  const [parameters, setParameters] = useState('{}');
  const [override, setOverride] = useState('{}');

  const priorityValue = Number(priority);
  const priorityValid = Number.isInteger(priorityValue) && priorityValue >= 0 && priorityValue <= 10000;
  const parameterError = jsonObjectError(parameters);
  const overrideError = jsonObjectError(override);
  const activePackageNames = new Map(activePackages.map((item) => [item.id, item.name]));
  const publishedVersions =
    versions.data?.items.flatMap((item) => {
      if (item.packageId === undefined) return [];
      const packageName = activePackageNames.get(item.packageId);
      return packageName !== undefined && item.status === 'published' && item.executable === true
        ? [
            {
              id: item.semanticHash,
              label: `${packageName} · ${item.version} · ${item.semanticHash.slice(0, 12)}…`
            }
          ]
        : [];
    }) ?? [];
  const adoptableSemanticHashes = new Set(publishedVersions.map((item) => item.id));

  return (
    <Section
      title="Source 规则绑定"
      description="当前兼容基线是单生效规则：按 active、条件匹配、priority、binding_id 稳定选择一条。同一 Source 的同一 priority 由数据库拒绝，未匹配时返回稳定错误。"
    >
      <AsyncPanel query={sources}>
        {(data) => (
          <Select
            label="来源"
            placeholder="选择一个来源"
            options={data.sources.map((item) => ({
              id: item.id,
              label: `${item.displayName} · ${item.id}`
            }))}
            selectedKey={sourceId}
            onSelectionChange={setSourceId}
          />
        )}
      </AsyncPanel>

      {sourceId === null ? (
        <p className="manage-section__description">选择来源后可以查看它的全部绑定与当前生效的那一条。</p>
      ) : (
        <>
          <h3 className="manage-section__title">当前生效的绑定</h3>
          <EffectiveBinding query={effective} />

          <h3 className="manage-section__title">全部绑定</h3>
          <InlineError error={update.error} title="绑定状态未能更新" />
          <AsyncPanel query={bindings}>
            {(data) => (
              <DataTable
                caption="Source 规则绑定"
                rows={data.bindings}
                rowKey={(row) => row.id}
                emptyTitle="该来源还没有任何规则绑定"
                emptyDescription="没有生效绑定时扫描会失败，因为规则是解释 Source 结构的唯一入口。"
                columns={[
                  {
                    id: 'id',
                    header: 'Binding ID',
                    render: (row) => <MonoId value={row.id} label="Binding ID" />
                  },
                  {
                    id: 'status',
                    header: '状态',
                    render: (row) => (
                      <Badge tone={row.status === 'active' ? 'success' : 'warning'}>
                        {row.status ?? 'active'}
                      </Badge>
                    )
                  },
                  { id: 'priority', header: 'priority', render: (row) => row.priority },
                  {
                    id: 'semanticHash',
                    header: 'semanticHash',
                    render: (row) => <MonoId value={row.semanticHash} label="semanticHash" />
                  },
                  {
                    id: 'ir',
                    header: 'ruleIrHash',
                    render: (row) => <MonoId value={row.ruleIrHash} label="ruleIrHash" />
                  },
                  {
                    id: 'parameterSet',
                    header: 'ParameterSet',
                    render: (row) =>
                      row.parameterId === undefined ? (
                        <Absent>direct</Absent>
                      ) : (
                        <>
                          <MonoId value={row.parameterId} label="ParameterSet ID" />
                          <br />
                          revision {row.parameterRevision ?? 0}
                        </>
                      )
                  },
                  {
                    id: 'parameterHash',
                    header: 'parameterHash',
                    render: (row) =>
                      row.parameterHash === undefined || row.parameterHash === '' ? (
                        <Absent />
                      ) : (
                        <MonoId value={row.parameterHash} label="parameterHash" />
                      )
                  },
                  { id: 'created', header: '创建时间', render: (row) => formatDateTime(row.createdAt) },
                  {
                    id: 'actions',
                    header: '操作',
                    render: (row) => (
                      <BindingStatusActions
                        binding={row}
                        isPending={update.isPending}
                        onStatus={(status) => {
                          update.mutate(
                            { bindingId: row.id, status },
                            {
                              onSuccess: () => {
                                show({
                                  title:
                                    status === 'active'
                                      ? '绑定已恢复'
                                      : status === 'paused'
                                        ? '绑定已暂停'
                                        : '绑定已标记无效',
                                  tone: status === 'active' ? 'success' : 'warning'
                                });
                              }
                            }
                          );
                        }}
                      />
                    )
                  }
                ]}
              />
            )}
          </AsyncPanel>

          <div className="manage-form">
            <h3 className="manage-section__title">新增绑定</h3>
            <Select
              label="绑定参数来源"
              options={[
                { id: 'direct', label: 'direct · 直接参数文本' },
                { id: 'parameter', label: 'ParameterSet · 共享参数集 + override' }
              ]}
              selectedKey={bindingMode}
              onSelectionChange={(value) => {
                if (value !== 'direct' && value !== 'parameter') return;
                setBindingMode(value);
                create.reset();
              }}
              description="两种请求体严格互斥：direct 只发送 semanticHash/parameters；ParameterSet 模式只发送 parameterId/override。"
            />
            <div className="manage-form__row">
              {bindingMode === 'direct' ? (
                <AsyncPanel query={versions}>
                  {() => (
                    <Select
                      label="已发布版本"
                      placeholder="选择 active 规则包的已发布版本"
                      options={publishedVersions}
                      selectedKey={semanticHash}
                      onSelectionChange={setSemanticHash}
                      description="列出 active 规则包下全部 published + executable 的 immutable RuleVersion；current 只是作者工作流指针。"
                    />
                  )}
                </AsyncPanel>
              ) : (
                <AsyncPanel query={parameterSets}>
                  {(data) => (
                    <Select
                      label="active ParameterSet"
                      placeholder="选择共享参数集"
                      options={data.parameterSets
                        .filter((item) => adoptableSemanticHashes.has(item.semanticHash))
                        .map((item) => ({
                          id: item.id,
                          label: `${item.name} · r${String(item.currentRevision)} · ${item.semanticHash.slice(0, 12)}…`
                        }))}
                      selectedKey={parameterId}
                      onSelectionChange={setParameterId}
                      description="列出 active 规则包下 published + executable 版本的 active ParameterSet。创建时冻结所选 revision/hash；以后更新会在服务端事务中刷新引用它的 Binding。"
                    />
                  )}
                </AsyncPanel>
              )}
              <TextInput
                label="priority"
                value={priority}
                onChange={setPriority}
                errorMessage={priorityValid ? undefined : '必须是整数'}
              />
            </div>
            {bindingMode === 'direct' ? (
              <TextInput
                label="参数（精确 JSON 对象文本）"
                value={parameters}
                onChange={setParameters}
                isMultiline
                rows={6}
                errorMessage={parameterError}
                description="文本原样发送，大整数和高精度小数不会先经过 JavaScript Number。"
              />
            ) : (
              <TextInput
                label="override（精确 JSON 对象文本）"
                value={override}
                onChange={setOverride}
                isMultiline
                rows={6}
                errorMessage={overrideError}
                description="只允许一层 canonical object override；不传 semanticHash 或 direct parameters。"
              />
            )}
            <div className="manage-form__actions">
              <Button
                variant="primary"
                isPending={create.isPending}
                isDisabled={
                  !priorityValid ||
                  (bindingMode === 'direct'
                    ? semanticHash === null || parameterError !== undefined
                    : parameterId === null || overrideError !== undefined)
                }
                onPress={() => {
                  if (!priorityValid) return;
                  const input =
                    bindingMode === 'direct'
                      ? semanticHash === null || parameterError !== undefined
                        ? null
                        : {
                            sourceId,
                            semanticHash,
                            priority: priorityValue,
                            parameters
                          }
                      : parameterId === null || overrideError !== undefined
                        ? null
                        : {
                            sourceId,
                            parameterId,
                            priority: priorityValue,
                            override
                          };
                  if (input === null) return;
                  create.mutate(input, {
                    onSuccess: () => {
                      setSemanticHash(null);
                      setParameterId(null);
                      setPriority('100');
                      setParameters('{}');
                      setOverride('{}');
                      void bindings.refetch();
                      void effective.refetch();
                      show({ title: '绑定已创建', tone: 'success' });
                    }
                  });
                }}
              >
                创建绑定
              </Button>
            </div>
            <p className="manage-section__description">
              Binding 按所选 Source 做资源作用域授权；global capability 仅用于提示，最终以服务端结果为准。
            </p>
            <InlineError error={create.error} title="绑定未能创建" />
          </div>
        </>
      )}
    </Section>
  );
}

function BindingStatusActions({
  binding,
  isPending,
  onStatus
}: {
  binding: SourceRuleBinding;
  isPending: boolean;
  onStatus: (status: NonNullable<SourceRuleBinding['status']>) => void;
}) {
  const status = binding.status ?? 'active';
  return (
    <span className="manage-cell-actions">
      {status === 'active' ? null : (
        <ConfirmAction
          label="恢复"
          variant="secondary"
          dialogTitle="恢复规则绑定"
          confirmLabel="确认恢复"
          isPending={isPending}
          description={`恢复后该 Binding 会重新参与 Source ${binding.sourceId} 的生效选择；若引用的规则版本或规则包已不可用，服务端会拒绝且保持原状态。`}
          onConfirm={() => onStatus('active')}
        />
      )}
      {status === 'paused' ? null : (
        <ConfirmAction
          label="暂停"
          variant="secondary"
          dialogTitle="暂停规则绑定"
          confirmLabel="确认暂停"
          isPending={isPending}
          description={`暂停后该 Binding 不再参与 Source ${binding.sourceId} 的生效选择；已经入队的 Job 仍使用自己冻结的规则快照。`}
          onConfirm={() => onStatus('paused')}
        />
      )}
      {status === 'invalid' ? null : (
        <ConfirmAction
          label="标记无效"
          variant="danger"
          dialogTitle="标记规则绑定无效"
          confirmLabel="确认标记无效"
          isPending={isPending}
          description={`这会把 Binding ${binding.id} 显式标记为 invalid，使其不再参与生效选择；不会删除规则版本或历史 Job。`}
          onConfirm={() => onStatus('invalid')}
        />
      )}
    </span>
  );
}

function EffectiveBinding({ query }: { query: ReturnType<typeof useEffectiveRuleBinding> }) {
  if (query.isPending && query.fetchStatus === 'idle') return null;
  if (query.isError) {
    const code = errorCode(query.error);
    return (
      <p className="manage-section__description">
        服务端没有返回生效绑定（{code ?? '未知失败'}）。这通常意味着该 Source 没有匹配的 active
        绑定，或多条绑定产生了冲突。扫描在这种状态下会稳定失败，而不是随便挑一条执行。
      </p>
    );
  }
  if (query.data === undefined) return null;
  return (
    <Facts
      items={[
        { term: '绑定 ID', value: <MonoId value={query.data.id} label="绑定 ID" /> },
        { term: 'semanticHash', value: <MonoId value={query.data.semanticHash} label="semanticHash" /> },
        { term: 'ruleIrHash', value: <MonoId value={query.data.ruleIrHash} label="ruleIrHash" /> },
        {
          term: 'ParameterSet',
          value:
            query.data.parameterId === undefined ? (
              <Absent>direct</Absent>
            ) : (
              <MonoId value={query.data.parameterId} label="ParameterSet ID" />
            )
        },
        { term: '参数 revision', value: query.data.parameterRevision ?? <Absent /> },
        {
          term: 'parameterHash',
          value:
            query.data.parameterHash === undefined || query.data.parameterHash === '' ? (
              <Absent />
            ) : (
              <MonoId value={query.data.parameterHash} label="parameterHash" />
            )
        },
        { term: 'priority', value: query.data.priority },
        { term: '状态', value: query.data.status ?? 'active' }
      ]}
    />
  );
}

function SchemaEntryPanel() {
  const canRead = useCapability('rules.read');
  const [probe, setProbe] = useState(false);
  const schema = useRuleSchema(probe);

  return (
    <Section
      title="规则 Schema 契约"
      description="GET /api/v1/rules/schema 返回 application/schema+json；规则包详情使用同一文档生成字段并核对预编译校验器版本。这里可以独立探测服务端契约。"
      actions={
        canRead ? (
          <Button variant="secondary" onPress={() => setProbe(true)}>
            检查 Schema 可用性
          </Button>
        ) : undefined
      }
    >
      <ContractNoteList area="rules" only={['rules-schema-editor-active']} />
      {probe ? (
        <AsyncPanel query={schema}>
          {(data) => {
            const document = data as Record<string, unknown>;
            const title = typeof document.title === 'string' ? document.title : '（Schema 未声明 title）';
            const id = typeof document.$id === 'string' ? document.$id : '（Schema 未声明 $id）';
            const dialect = typeof document.$schema === 'string' ? document.$schema : '（未声明 $schema）';
            return (
              <Facts
                items={[
                  { term: 'Schema 可达', value: <Badge tone="success">是</Badge> },
                  { term: 'title', value: title },
                  { term: '$id', value: id },
                  { term: '$schema', value: dialect }
                ]}
              />
            );
          }}
        </AsyncPanel>
      ) : (
        <p className="manage-section__description">尚未探测。点击右上角按钮向服务端请求一次 Schema。</p>
      )}
    </Section>
  );
}

export function RulesPage() {
  return (
    <>
      <PageHeader
        title="规则"
        lead="规则是 Source 差异的唯一解释入口：业务代码里不会有按平台名的特例分支。这里管理规则包与 Source 绑定；具体的草稿编辑、校验、影响评估与发布在规则包详情里。"
      />
      <RulePackagesPanel />
      <BindingsPanel />
      <SchemaEntryPanel />
      <Section title="规则相关的契约事实" description="以下是服务端当前的真实约束。">
        <ContractNoteList area="rules" only={['rules-analysis-needs-write', 'rules-draft-if-match']} />
      </Section>
    </>
  );
}
