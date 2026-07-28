import Form from '@rjsf/core';
import type {
  Field as RjsfField,
  FieldProps as RjsfFieldProps,
  FormContextType,
  RJSFSchema,
  UiSchema
} from '@rjsf/utils';
import { createPrecompiledValidator, type ValidatorFunctions } from '@rjsf/validator-ajv8';
import { useCallback, useEffect, useMemo, useState } from 'react';
import buildSchema from '../../../../internal/rules/rule-package.schema.json';
import { Button, ErrorState, Field, Select } from '../../design';
import { describeError, errorCode, errorCorrelationId } from '../../shared/errors';
import { useRuleExamples, useRuleSchema } from '../api';
import { ConfirmAction } from '../ui';
import validatorFunctions from '../ruleSchemaValidator.gen.cjs';
import { cloneRuleValue, isRecord, parseRuleValue, stringifyRuleValue } from './lossless';

interface RuleFormContext extends FormContextType {
  currentValue: Record<string, unknown>;
  setOpaqueInvalid: (path: string, invalid: boolean) => void;
  changeOpaque: (path: readonly (string | number)[], value: unknown) => void;
}

interface RuleSchemaFormProps {
  value: Record<string, unknown>;
  ruleSetId: string;
  isDisabled: boolean;
  isDirty: boolean;
  onChange: (text: string) => void;
  onOpaqueValidityChange: (invalid: boolean) => void;
}

function stableJson(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(stableJson).join(',')}]`;
  if (isRecord(value)) {
    return `{${Object.keys(value)
      .sort()
      .map((key) => `${JSON.stringify(key)}:${stableJson(value[key])}`)
      .join(',')}}`;
  }
  return JSON.stringify(value);
}

function schemaTypeMatches(value: unknown, schema: RJSFSchema): boolean {
  const expected = schema.type;
  if (expected === undefined) return true;
  const types = Array.isArray(expected) ? expected : [expected];
  return types.some((type) => {
    if (type === 'object') return isRecord(value);
    if (type === 'array') return Array.isArray(value);
    if (type === 'null') return value === null;
    return typeof value === type;
  });
}

function OpaqueJsonField({
  fieldPathId,
  formData,
  schema,
  name,
  required,
  disabled,
  readonly,
  rawErrors,
  onBlur,
  onFocus,
  registry
}: RjsfFieldProps<unknown, RJSFSchema, RuleFormContext>) {
  const exactValue = valueAtPath(registry.formContext.currentValue, fieldPathId.path) ?? formData;
  const externalText = useMemo(
    () => (exactValue === undefined ? '' : stringifyRuleValue(exactValue, 2)),
    [exactValue]
  );
  const [text, setText] = useState(externalText);
  const [syntaxError, setSyntaxError] = useState<string>();
  const path = fieldPathId.path.join('/');

  useEffect(() => {
    if (syntaxError === undefined) setText(externalText);
  }, [externalText, syntaxError]);

  const setOpaqueInvalid = registry.formContext.setOpaqueInvalid;
  useEffect(() => () => setOpaqueInvalid(path, false), [path, setOpaqueInvalid]);

  const setInvalid = (message: string | undefined) => {
    setSyntaxError(message);
    registry.formContext.setOpaqueInvalid(path, message !== undefined);
  };
  const schemaErrors = rawErrors?.filter(Boolean).join('；');
  const errorMessage = syntaxError ?? schemaErrors;

  return (
    <Field
      controlId={fieldPathId.$id}
      label={schema.title ?? name}
      description={schema.description ?? '此字段保留为无损 JSON；表单不会展开或删除未知内容。'}
      errorMessage={errorMessage}
      isRequired={required}
    >
      {({ controlId, describedBy, isInvalid }) => (
        <textarea
          id={controlId}
          className="ui-textarea manage-code"
          value={text}
          rows={8}
          disabled={disabled}
          readOnly={readonly}
          aria-describedby={describedBy}
          aria-invalid={isInvalid || undefined}
          onFocus={() => onFocus(fieldPathId.$id, exactValue)}
          onBlur={() => onBlur(fieldPathId.$id, exactValue)}
          onChange={(event) => {
            const nextText = event.currentTarget.value;
            setText(nextText);
            try {
              if (nextText.trim() === '' && !required) {
                setInvalid(undefined);
                registry.formContext.changeOpaque(fieldPathId.path, undefined);
                return;
              }
              const next = parseRuleValue(nextText);
              if (!schemaTypeMatches(next, schema)) {
                throw new TypeError(`必须是 ${String(schema.type)} 类型的 JSON 值`);
              }
              setInvalid(undefined);
              registry.formContext.changeOpaque(fieldPathId.path, next);
            } catch (error) {
              setInvalid(error instanceof Error ? error.message : '不是合法的 JSON');
            }
          }}
        />
      )}
    </Field>
  );
}

const schema = buildSchema as RJSFSchema;

function primitiveConfigUiSchema(kind: string): UiSchema<unknown, RJSFSchema, RuleFormContext> {
  if (kind === 'selector' || kind === 'fallback') {
    return { default: { 'ui:field': 'OpaqueJsonField' } };
  }
  if (kind === 'badge') {
    return { when: { metadata_values: { 'ui:field': 'OpaqueJsonField' } } };
  }
  return {};
}

/**
 * 根据同一 primitive 的 kind 选择后端权威 Schema 中的配置定义。根 Schema 刻意只把
 * `config` 约束为对象：完整语义仍由 CompilePackage 校验，以保持既有 RuleVersion 的错误契约；
 * `$defs.primitive_config_*` 则是服务端和 Web 共用的可视化字段词表，不在前端复制平台规则。
 */
function PrimitiveConfigField(props: RjsfFieldProps<unknown, RJSFSchema, RuleFormContext>) {
  const primitive = valueAtPath(props.registry.formContext.currentValue, props.fieldPathId.path.slice(0, -1));
  const kind = isRecord(primitive) && typeof primitive.kind === 'string' ? primitive.kind : '';
  const definitions = schema.$defs;
  const selected = isRecord(definitions) ? definitions[`primitive_config_${kind}`] : undefined;
  if (!isRecord(selected)) return <OpaqueJsonField {...props} />;

  const ObjectField = props.registry.fields.ObjectField;
  if (ObjectField === undefined) return <OpaqueJsonField {...props} />;
  // RJSF 会在处理 additional properties/default state 时给传入的局部 Schema 加内部标记；
  // 不能把根 Schema 的 `$defs` 节点原样交给它，否则会改写预编译校验器用来比对的根对象。
  const fieldSchema = structuredClone(selected) as RJSFSchema;
  return <ObjectField {...props} schema={fieldSchema} uiSchema={primitiveConfigUiSchema(kind)} />;
}

const validator = createPrecompiledValidator<Record<string, unknown>, RJSFSchema, RuleFormContext>(
  validatorFunctions as ValidatorFunctions,
  schema
);
const fields = {
  OpaqueJsonField: OpaqueJsonField as unknown as RjsfField<
    Record<string, unknown>,
    RJSFSchema,
    RuleFormContext
  >,
  PrimitiveConfigField: PrimitiveConfigField as unknown as RjsfField<
    Record<string, unknown>,
    RJSFSchema,
    RuleFormContext
  >
};

const uiSchema: UiSchema<Record<string, unknown>, RJSFSchema, RuleFormContext> = {
  'ui:title': '规则包字段',
  rule_set_id: { 'ui:title': 'ruleSetId', 'ui:readonly': true },
  version: { 'ui:title': '规则版本' },
  schema_version: { 'ui:title': 'Schema 版本', 'ui:readonly': true },
  normalization_algorithm_version: { 'ui:title': '规范化算法', 'ui:readonly': true },
  compiler_requirement: { 'ui:title': '编译器要求', 'ui:readonly': true },
  cel_profile_version: { 'ui:title': 'CEL Profile', 'ui:readonly': true },
  parameter_schema: { 'ui:title': '参数 Schema', 'ui:field': 'OpaqueJsonField' },
  provider_namespaces: { 'ui:title': 'Provider namespaces' },
  primitives: {
    'ui:title': '规则原语',
    items: { config: { 'ui:title': '原语配置', 'ui:field': 'PrimitiveConfigField' } }
  },
  cel_expressions: { 'ui:title': 'CEL 表达式' },
  tests: { 'ui:title': '规则测试', 'ui:field': 'OpaqueJsonField' },
  extensions: { 'ui:title': '扩展', 'ui:field': 'OpaqueJsonField' },
  ui_metadata: { 'ui:title': '表单元数据', 'ui:field': 'OpaqueJsonField' },
  package_hash: { 'ui:widget': 'hidden' },
  semantic_hash: { 'ui:widget': 'hidden' }
};

const hasOwn = (value: Record<string, unknown>, key: string): boolean =>
  Object.prototype.hasOwnProperty.call(value, key);

function patchRuleValue(
  source: Record<string, unknown>,
  path: readonly (string | number)[],
  value: unknown
): Record<string, unknown> {
  const next = cloneRuleValue(source);
  let parent: unknown = next;
  for (const segment of path.slice(0, -1)) {
    if (typeof segment === 'number' && Array.isArray(parent)) parent = parent[segment];
    else if (typeof segment === 'string' && isRecord(parent)) parent = parent[segment];
    else throw new TypeError('无法定位不透明规则字段');
  }
  const leaf = path.at(-1);
  if (typeof leaf === 'number' && Array.isArray(parent)) {
    if (value === undefined) parent.splice(leaf, 1);
    else parent[leaf] = value;
  } else if (typeof leaf === 'string' && isRecord(parent)) {
    if (value === undefined) Reflect.deleteProperty(parent, leaf);
    else parent[leaf] = value;
  } else {
    throw new TypeError('无法更新不透明规则字段');
  }
  return next;
}

function valueAtPath(source: Record<string, unknown>, path: readonly (string | number)[]): unknown {
  let current: unknown = source;
  for (const segment of path) {
    if (typeof segment === 'number' && Array.isArray(current)) current = current[segment];
    else if (typeof segment === 'string' && isRecord(current)) current = current[segment];
    else return undefined;
  }
  return current;
}

function restoreOpaqueValues(
  changed: Record<string, unknown>,
  source: Record<string, unknown>
): Record<string, unknown> {
  const next = { ...changed };
  for (const key of ['parameter_schema', 'tests', 'extensions', 'ui_metadata'] as const) {
    if (hasOwn(source, key)) next[key] = source[key];
    else Reflect.deleteProperty(next, key);
  }

  return next;
}

export default function RuleSchemaForm({
  value,
  ruleSetId,
  isDisabled,
  isDirty,
  onChange,
  onOpaqueValidityChange
}: RuleSchemaFormProps) {
  const remoteSchema = useRuleSchema(true);
  const examples = useRuleExamples();
  const [templateId, setTemplateId] = useState<string | null>(null);
  const [invalidPaths, setInvalidPaths] = useState<ReadonlySet<string>>(new Set());
  const schemaMatches =
    remoteSchema.data !== undefined && stableJson(remoteSchema.data) === stableJson(schema);

  useEffect(() => {
    onOpaqueValidityChange(invalidPaths.size > 0);
  }, [invalidPaths, onOpaqueValidityChange]);

  const setOpaqueInvalid = useCallback((path: string, invalid: boolean) => {
    setInvalidPaths((current) => {
      const next = new Set(current);
      if (invalid) next.add(path);
      else next.delete(path);
      return next;
    });
  }, []);
  const changeOpaque = useCallback(
    (path: readonly (string | number)[], nextValue: unknown) => {
      onChange(stringifyRuleValue(patchRuleValue(value, path, nextValue), 2));
    },
    [onChange, value]
  );
  const formContext = useMemo<RuleFormContext>(
    () => ({ currentValue: value, setOpaqueInvalid, changeOpaque }),
    [changeOpaque, setOpaqueInvalid, value]
  );

  const applyTemplate = () => {
    const selected = examples.data?.items.find((item) => item.id === templateId);
    if (selected === undefined) return;
    const next = cloneRuleValue(selected.package);
    next.rule_set_id = ruleSetId;
    delete next.package_hash;
    delete next.semantic_hash;
    onChange(stringifyRuleValue(next, 2));
  };

  if (remoteSchema.isPending) return <p className="manage-section__description">正在载入规则 Schema…</p>;
  if (remoteSchema.isError) {
    return (
      <ErrorState
        title="无法载入规则 Schema"
        description={describeError(remoteSchema.error)}
        code={errorCode(remoteSchema.error)}
        correlationId={errorCorrelationId(remoteSchema.error)}
        onRetry={() => void remoteSchema.refetch()}
      />
    );
  }
  if (!schemaMatches) {
    return (
      <div className="manage-inline-error" role="alert">
        <p className="manage-inline-error__title">运行时 Schema 与前端预编译版本不一致</p>
        <p className="manage-inline-error__body">
          为避免用旧校验器改写规则，表单模式已停用。请重新构建并部署同一版本的 Web 资产。
        </p>
      </div>
    );
  }

  return (
    <div className="manage-form" data-testid="rule-schema-form">
      <div className="manage-form">
        {examples.isError ? (
          <ErrorState
            title="无法载入起始模板"
            description={describeError(examples.error)}
            code={errorCode(examples.error)}
            correlationId={errorCorrelationId(examples.error)}
            onRetry={() => void examples.refetch()}
          />
        ) : (
          <>
            <Select
              label="起始模板"
              description="内置模板只填入当前编辑器，不会自动保存；也可以不载入模板，直接从空白表单建立规则包。"
              options={(examples.data?.items ?? []).map((item) => ({ id: item.id, label: item.name }))}
              selectedKey={templateId}
              isDisabled={isDisabled || examples.isPending}
              onSelectionChange={(key) => setTemplateId(key)}
            />
            {templateId === null ? null : isDirty ? (
              <ConfirmAction
                label="载入起始模板"
                dialogTitle="覆盖当前未保存草稿"
                confirmLabel="确认载入"
                description="当前未保存修改会被所选内置模板替换。模板只进入本地编辑器，不会自动保存。"
                onConfirm={applyTemplate}
              />
            ) : (
              <Button variant="secondary" onPress={applyTemplate} isDisabled={isDisabled}>
                载入起始模板
              </Button>
            )}
          </>
        )}
      </div>

      <p className="manage-section__description">
        Schema 错误是即时预检，不会阻止保存无效草稿；服务端保存与校验结果才是规范化和诊断的权威事实。
      </p>
      <Form<Record<string, unknown>, RJSFSchema, RuleFormContext>
        schema={schema}
        validator={validator}
        uiSchema={uiSchema}
        fields={fields}
        formData={value}
        formContext={formContext}
        disabled={isDisabled}
        readonly={isDisabled}
        liveValidate="onBlur"
        showErrorList="top"
        tagName="div"
        className="manage-rule-schema-form"
        experimental_defaultFormStateBehavior={{
          arrayMinItems: { populate: 'never' },
          emptyObjectFields: 'skipDefaults',
          allOf: 'skipDefaults',
          constAsDefaults: 'never'
        }}
        onChange={(event) => {
          if (event.formData === undefined) return;
          const restored = restoreOpaqueValues(event.formData, value);
          if (stringifyRuleValue(restored, 0) === stringifyRuleValue(value, 0)) return;
          onChange(stringifyRuleValue(restored, 2));
        }}
      >
        <></>
      </Form>
    </div>
  );
}
