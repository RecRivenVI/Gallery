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
  useRuleSchema,
  useRulePackages,
  useSourceRuleBindings,
  useSources
} from '../api';
import {
  Absent,
  AsyncPanel,
  ContractNoteList,
  DataTable,
  Facts,
  InlineError,
  MonoId,
  PageHeader,
  Section,
  formatDateTime
} from '../ui';

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
                  row.currentSemanticHash === undefined ? (
                    <Absent>尚未发布</Absent>
                  ) : (
                    <MonoId value={row.currentSemanticHash} label="semanticHash" />
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
  const [sourceId, setSourceId] = useState<string | null>(null);
  const bindings = useSourceRuleBindings(sourceId);
  const effective = useEffectiveRuleBinding(sourceId);
  const create = useCreateSourceRuleBinding();
  const canWrite = useCapability('rules.write');
  const [semanticHash, setSemanticHash] = useState('');
  const [priority, setPriority] = useState('100');
  const [parameters, setParameters] = useState('{}');

  const priorityValue = Number.parseInt(priority, 10);
  const priorityValid = Number.isFinite(priorityValue);
  let parsedParameters: Record<string, unknown> | null = null;
  let parameterError: string | undefined;
  try {
    const parsed: unknown = JSON.parse(parameters);
    if (typeof parsed === 'object' && parsed !== null && !Array.isArray(parsed)) {
      parsedParameters = parsed as Record<string, unknown>;
    } else {
      parameterError = '参数必须是 JSON 对象';
    }
  } catch {
    parameterError = '不是合法的 JSON';
  }

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
            options={data.sources.map((item) => ({ id: item.id, label: item.displayName }))}
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
                  { id: 'created', header: '创建时间', render: (row) => formatDateTime(row.createdAt) }
                ]}
              />
            )}
          </AsyncPanel>

          {canWrite ? (
            <div className="manage-form">
              <h3 className="manage-section__title">新增绑定</h3>
              <div className="manage-form__row">
                <TextInput label="semanticHash" value={semanticHash} onChange={setSemanticHash} isRequired />
                <TextInput
                  label="priority"
                  value={priority}
                  onChange={setPriority}
                  errorMessage={priorityValid ? undefined : '必须是整数'}
                />
              </div>
              <TextInput
                label="参数（规范 JSON 对象）"
                value={parameters}
                onChange={setParameters}
                isMultiline
                rows={6}
                errorMessage={parameterError}
              />
              <div className="manage-form__actions">
                <Button
                  variant="primary"
                  isPending={create.isPending}
                  isDisabled={semanticHash.trim() === '' || !priorityValid || parsedParameters === null}
                  onPress={() => {
                    if (parsedParameters === null || !priorityValid) return;
                    create.mutate(
                      {
                        sourceId,
                        semanticHash: semanticHash.trim(),
                        priority: priorityValue,
                        parameters: parsedParameters
                      },
                      {
                        onSuccess: () => {
                          show({ title: '绑定已创建', tone: 'success' });
                        }
                      }
                    );
                  }}
                >
                  创建绑定
                </Button>
              </div>
              <InlineError error={create.error} title="绑定未能创建" />
            </div>
          ) : null}
        </>
      )}
    </Section>
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
      title="配置编辑器入口"
      description="GET /api/v1/rules/schema 返回 application/schema+json，是 Schema 驱动配置编辑器的输入。表单生成属于另一条工作线，这里只核对端点可达。"
      actions={
        canRead ? (
          <Button variant="secondary" onPress={() => setProbe(true)}>
            检查 Schema 可用性
          </Button>
        ) : undefined
      }
    >
      <ContractNoteList area="rules" only={['rules-schema-editor-elsewhere']} />
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
