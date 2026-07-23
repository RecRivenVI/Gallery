import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Form as AriaForm, Input, Label, TextArea, TextField } from 'react-aria-components';
import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import RJSFForm from '@rjsf/core';
import validator from '@rjsf/validator-ajv8';
import type { RJSFSchema } from '@rjsf/utils';
import { api, csrfHeaders, errorMessage, expectData } from '../api/client';
import { useSession } from '../auth/session';
import {
  DefinitionList,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  StatusBadge
} from '../components/ui';

export function RulesPage() {
  const { bootstrap, can } = useSession();
  const client = useQueryClient();
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [parameterName, setParameterName] = useState('');
  const [semanticHash, setSemanticHash] = useState('');
  const [parameterJson, setParameterJson] = useState('{}');
  const [bindingSourceId, setBindingSourceId] = useState('');
  const [bindingPriority, setBindingPriority] = useState('0');
  const [bindingParameters, setBindingParameters] = useState('{}');
  const packages = useQuery({
    queryKey: ['rule-packages'],
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/rule-packages', { signal }))
  });
  const parameters = useQuery({
    queryKey: ['rule-parameters'],
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/rule-parameters', { signal }))
  });
  const bindings = useQuery({
    queryKey: ['rule-bindings'],
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/source-rule-bindings', { signal }))
  });
  const create = useMutation({
    mutationFn: async () =>
      expectData(
        await api.POST('/api/v1/rule-packages', {
          params: { header: csrfHeaders(bootstrap.csrfToken) },
          body: { name, description }
        })
      ),
    onSuccess: async () => {
      setName('');
      setDescription('');
      await client.invalidateQueries({ queryKey: ['rule-packages'] });
    }
  });
  const createParameter = useMutation({
    mutationFn: async () =>
      expectData(
        await api.POST('/api/v1/rule-parameters', {
          params: { header: csrfHeaders(bootstrap.csrfToken) },
          body: {
            name: parameterName,
            semanticHash,
            parameters: JSON.parse(parameterJson) as Record<string, unknown>
          }
        })
      ),
    onSuccess: async () => {
      setParameterName('');
      setParameterJson('{}');
      await client.invalidateQueries({ queryKey: ['rule-parameters'] });
    }
  });
  const createBinding = useMutation({
    mutationFn: async () =>
      expectData(
        await api.POST('/api/v1/source-rule-bindings', {
          params: { header: csrfHeaders(bootstrap.csrfToken) },
          body: {
            sourceId: bindingSourceId,
            semanticHash,
            parameters: JSON.parse(bindingParameters) as Record<string, unknown>,
            priority: Number(bindingPriority)
          }
        })
      ),
    onSuccess: async () => {
      setBindingSourceId('');
      await client.invalidateQueries({ queryKey: ['rule-bindings'] });
    }
  });
  if (packages.isPending || parameters.isPending || bindings.isPending) return <LoadingState />;
  if (packages.isError) return <ErrorState error={packages.error} />;
  if (parameters.isError) return <ErrorState error={parameters.error} />;
  if (bindings.isError) return <ErrorState error={bindings.error} />;
  return (
    <>
      <PageHeader
        title="规则工作台"
        description="规范 JSON 是运行时唯一事实源；Schema 驱动表单和高级 JSON 使用同一服务端校验语义。"
      />
      {can('rules.write') && (
        <AriaForm
          className="panel form-grid"
          onSubmit={(event) => {
            event.preventDefault();
            create.mutate();
          }}
        >
          <TextField isRequired value={name} onChange={setName}>
            <Label>规则包名称</Label>
            <Input />
          </TextField>
          <TextField value={description} onChange={setDescription}>
            <Label>说明</Label>
            <Input />
          </TextField>
          <Button type="submit" className="button primary">
            创建草稿容器
          </Button>
        </AriaForm>
      )}
      {create.isError && (
        <p role="alert" className="field-error">
          {errorMessage(create.error)}
        </p>
      )}
      {can('rules.write') && (
        <div className="detail-layout">
          <AriaForm
            className="panel form-grid"
            onSubmit={(event) => {
              event.preventDefault();
              createParameter.mutate();
            }}
          >
            <h2>创建参数集</h2>
            <TextField isRequired value={parameterName} onChange={setParameterName}>
              <Label>名称</Label>
              <Input />
            </TextField>
            <TextField isRequired value={semanticHash} onChange={setSemanticHash}>
              <Label>已发布 semantic hash</Label>
              <Input spellCheck={false} />
            </TextField>
            <TextField isRequired value={parameterJson} onChange={setParameterJson}>
              <Label>参数 JSON</Label>
              <TextArea className="code-editor compact" spellCheck={false} />
            </TextField>
            <Button type="submit" className="button primary">
              创建参数集
            </Button>
          </AriaForm>
          <AriaForm
            className="panel form-grid"
            onSubmit={(event) => {
              event.preventDefault();
              createBinding.mutate();
            }}
          >
            <h2>绑定 Source</h2>
            <TextField isRequired value={bindingSourceId} onChange={setBindingSourceId}>
              <Label>Source ID</Label>
              <Input spellCheck={false} />
            </TextField>
            <TextField isRequired value={semanticHash} onChange={setSemanticHash}>
              <Label>已发布 semantic hash</Label>
              <Input spellCheck={false} />
            </TextField>
            <TextField isRequired value={bindingPriority} onChange={setBindingPriority}>
              <Label>优先级</Label>
              <Input inputMode="numeric" />
            </TextField>
            <TextField isRequired value={bindingParameters} onChange={setBindingParameters}>
              <Label>最终参数 JSON</Label>
              <TextArea className="code-editor compact" spellCheck={false} />
            </TextField>
            <Button type="submit" className="button primary">
              创建单生效 Binding
            </Button>
          </AriaForm>
        </div>
      )}
      {(createParameter.isError || createBinding.isError) && (
        <p role="alert" className="field-error">
          {errorMessage(createParameter.error ?? createBinding.error)}
        </p>
      )}
      <h2>规则包</h2>
      {packages.data.items.length === 0 ? (
        <EmptyState title="没有规则包" detail="创建后可保存、校验并发布不可变 RuleVersion。" />
      ) : (
        <div className="card-grid">
          {packages.data.items.map((item) => (
            <article className="card" key={item.id}>
              <h3>
                <Link to={`/rules/${encodeURIComponent(item.id)}`}>{item.name}</Link>
              </h3>
              <p>{item.description || '无说明'}</p>
              <StatusBadge tone={item.status === 'active' ? 'success' : 'warning'}>{item.status}</StatusBadge>
              <DefinitionList
                items={[
                  ['revision', item.revision],
                  ['当前版本', item.currentSemanticHash ? <code>{item.currentSemanticHash}</code> : '—']
                ]}
              />
            </article>
          ))}
        </div>
      )}
      <div className="detail-layout">
        <section>
          <h2>参数集</h2>
          <div className="card-grid">
            {parameters.data.parameterSets.map((item) => (
              <article className="card" key={item.id}>
                <strong>{item.name}</strong>
                <p>
                  <code>{item.semanticHash}</code>
                </p>
                <StatusBadge>{item.status}</StatusBadge>
              </article>
            ))}
          </div>
        </section>
        <section>
          <h2>Source Binding</h2>
          <div className="card-grid">
            {bindings.data.bindings.map((item) => (
              <article className="card" key={item.id}>
                <strong>
                  <code>{item.sourceId}</code>
                </strong>
                <p>优先级 {item.priority}</p>
                <StatusBadge>{item.status ?? 'active'}</StatusBadge>
              </article>
            ))}
          </div>
        </section>
      </div>
    </>
  );
}

export function RulePackagePage() {
  const { packageId = '' } = useParams();
  const { bootstrap, can } = useSession();
  const client = useQueryClient();
  const pkg = useQuery({
    queryKey: ['rule-package', packageId],
    queryFn: async ({ signal }) =>
      expectData(
        await api.GET('/api/v1/rule-packages/{packageId}', { signal, params: { path: { packageId } } })
      )
  });
  const draft = useQuery({
    queryKey: ['rule-draft', packageId],
    retry: false,
    queryFn: async ({ signal }) =>
      expectData(
        await api.GET('/api/v1/rule-packages/{packageId}/draft', { signal, params: { path: { packageId } } })
      )
  });
  const schema = useQuery({
    queryKey: ['rule-schema'],
    queryFn: async ({ signal }) =>
      expectData(
        await api.GET('/api/v1/rules/schema', { signal, headers: { Accept: 'application/schema+json' } })
      )
  });
  const versions = useQuery({
    queryKey: ['rule-versions', packageId],
    queryFn: async ({ signal }) =>
      expectData(
        await api.GET('/api/v1/rule-packages/{packageId}/versions', {
          signal,
          params: { path: { packageId }, query: {} }
        })
      )
  });
  const [json, setJson] = useState('{}');
  const [formData, setFormData] = useState<Record<string, unknown>>({});
  const [result, setResult] = useState<unknown>();
  useEffect(() => {
    if (draft.data) {
      const data =
        typeof draft.data.content === 'string'
          ? draft.data.content
          : JSON.stringify(draft.data.content, null, 2);
      setJson(data);
      try {
        setFormData(JSON.parse(data) as Record<string, unknown>);
      } catch {
        /* advanced editor keeps invalid draft text */
      }
    }
  }, [draft.data]);
  const mutationHeader = {
    ...csrfHeaders(bootstrap.csrfToken),
    ...(draft.data ? { 'If-Match': String(draft.data.revision) } : {})
  };
  const save = useMutation({
    mutationFn: async () =>
      expectData(
        await api.PUT('/api/v1/rule-packages/{packageId}/draft', {
          params: { path: { packageId }, header: mutationHeader },
          body: {
            content: json,
            format: 'json',
            expectedRevision: draft.data?.revision,
            baseSemanticHash: pkg.data?.currentSemanticHash
          }
        })
      ),
    onSuccess: () => client.invalidateQueries({ queryKey: ['rule-draft', packageId] })
  });
  const validate = useMutation({
    mutationFn: async () =>
      expectData(
        await api.POST('/api/v1/rule-packages/{packageId}/draft/validate', {
          params: { path: { packageId }, header: mutationHeader }
        })
      ),
    onSuccess: (data) => {
      setResult(data);
      void client.invalidateQueries({ queryKey: ['rule-draft', packageId] });
    }
  });
  const publish = useMutation({
    mutationFn: async () =>
      expectData(
        await api.POST('/api/v1/rule-packages/{packageId}/publish', {
          params: { path: { packageId }, header: mutationHeader },
          body: { expectedRevision: draft.data?.revision, reason: 'Gallery Web 发布' }
        })
      ),
    onSuccess: (data) => {
      setResult(data);
      void client.invalidateQueries({ queryKey: ['rule-package', packageId] });
      void client.invalidateQueries({ queryKey: ['rule-versions', packageId] });
    }
  });
  const dryRun = useMutation({
    mutationFn: async () =>
      expectData(
        await api.POST('/api/v1/rules/dry-run', {
          params: { header: csrfHeaders(bootstrap.csrfToken) },
          body: {
            package: JSON.parse(json) as Record<string, unknown>,
            parameters: {},
            sample: { path: 'synthetic', files: [], metadata: {} }
          }
        })
      ),
    onSuccess: setResult
  });
  if (pkg.isPending || schema.isPending || versions.isPending) return <LoadingState />;
  if (pkg.isError) return <ErrorState error={pkg.error} />;
  if (schema.isError) return <ErrorState error={schema.error} />;
  if (versions.isError) return <ErrorState error={versions.error} />;
  const mutationError = save.error ?? validate.error ?? publish.error ?? dryRun.error;
  return (
    <>
      <PageHeader
        title={pkg.data.name}
        description={`规则包 revision ${pkg.data.revision}`}
        actions={<StatusBadge>{draft.data?.validationStatus ?? '尚无草稿'}</StatusBadge>}
      />
      <div className="rule-layout">
        <section className="panel">
          <h2>Schema 表单</h2>
          <RJSFForm
            schema={schema.data as RJSFSchema}
            formData={formData}
            validator={validator}
            liveValidate={false}
            onChange={(event) => {
              const value = (event.formData ?? {}) as Record<string, unknown>;
              setFormData(value);
              setJson(JSON.stringify(value, null, 2));
            }}
            onSubmit={() => save.mutate()}
            disabled={!can('rules.write')}
          >
            <Button type="submit" className="button primary">
              保存表单草稿
            </Button>
          </RJSFForm>
        </section>
        <section className="panel form-stack">
          <h2>高级规范 JSON</h2>
          <TextField value={json} onChange={setJson}>
            <Label>JSON</Label>
            <TextArea className="code-editor" spellCheck={false} />
          </TextField>
          <div className="button-row">
            {can('rules.write') && (
              <Button className="button primary" onPress={() => save.mutate()}>
                保存
              </Button>
            )}
            <Button className="button secondary" onPress={() => validate.mutate()}>
              校验
            </Button>
            <Button className="button secondary" onPress={() => dryRun.mutate()}>
              合成 Dry Run
            </Button>
            {can('rules.publish') && (
              <Button className="button danger" onPress={() => publish.mutate()}>
                发布不可变版本
              </Button>
            )}
          </div>
          {mutationError && (
            <p role="alert" className="field-error">
              {errorMessage(mutationError)}
            </p>
          )}
        </section>
      </div>
      <h2>服务端结果</h2>
      <pre className="json-output">{result ? JSON.stringify(result, null, 2) : '尚无运行结果'}</pre>
      <h2>版本历史</h2>
      <div className="table-wrap">
        <table className="data-grid">
          <thead>
            <tr>
              <th>版本</th>
              <th>semantic hash</th>
              <th>状态</th>
            </tr>
          </thead>
          <tbody>
            {versions.data.items.map((item) => (
              <tr key={item.semanticHash}>
                <td>{item.version}</td>
                <td>
                  <code>{item.semanticHash}</code>
                </td>
                <td>{item.status}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}
