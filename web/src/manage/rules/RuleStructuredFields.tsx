import type { FieldProps as RjsfFieldProps, FormContextType, RJSFSchema } from '@rjsf/utils';
import { isLosslessNumber } from 'lossless-json';
import { useEffect, useId, useMemo, useState } from 'react';
import { Button, Checkbox, Field, Select, TextInput } from '../../design';
import { LocalCollectionPager, useLocalCollectionWindow } from './LocalCollectionWindow';
import { cloneRuleValue, isRecord, parseRuleValue, stringifyRuleValue } from './lossless';

export interface RuleFormContext extends FormContextType {
  currentValue: Record<string, unknown>;
  baselineValue: Record<string, unknown>;
  setOpaqueInvalid: (path: string, invalid: boolean) => void;
  changeOpaque: (path: readonly (string | number)[], value: unknown) => void;
  restoreField: (path: readonly (string | number)[]) => void;
}

type RuleFieldProps = RjsfFieldProps<unknown, RJSFSchema, RuleFormContext>;
type JsonKind = 'object' | 'array' | 'string' | 'number' | 'boolean' | 'null';

// 必须与 internal/rules.MaxRuleNestingDepth 保持一致。这个值是规则 JSON、Schema
// 与导入格式共享的容器深度契约，不是前端自行设置的展示阈值。
export const RULE_CONTAINER_DEPTH_LIMIT = 256;

const JSON_KIND_OPTIONS = [
  { id: 'object', label: '对象' },
  { id: 'array', label: '数组' },
  { id: 'string', label: '字符串' },
  { id: 'number', label: '数字' },
  { id: 'boolean', label: '布尔值' },
  { id: 'null', label: 'null' }
] as const;

const PARAMETER_TYPE_OPTIONS = [
  { id: '__unset', label: '未指定' },
  { id: 'string', label: 'string' },
  { id: 'integer', label: 'integer' },
  { id: 'number', label: 'number' },
  { id: 'boolean', label: 'boolean' },
  { id: 'array', label: 'array' },
  { id: 'object', label: 'object' },
  { id: 'null', label: 'null' }
] as const;

const EXTENSION_NAMESPACE_PATTERN = /^[a-z0-9][a-z0-9-]{0,62}(?:\.[a-z0-9][a-z0-9-]{0,62})+$/;

function isUnknownArray(value: unknown): value is unknown[] {
  return Array.isArray(value);
}

function isJsonObject(value: unknown): value is Record<string, unknown> {
  return !isLosslessNumber(value) && isRecord(value);
}

// 使用显式栈检查容器深度，避免为了判断恶意超深草稿本身再次递归。initialDepth
// 是该子树在完整 RulePackage 中的 JSON Pointer 深度；标量位于边界处仍合法，
// 但 object/array 容器不得出现在 depth >= RULE_CONTAINER_DEPTH_LIMIT。
export function ruleValueFitsContainerDepth(value: unknown, initialDepth = 0): boolean {
  const pending: Array<{ value: unknown; depth: number }> = [{ value, depth: initialDepth }];
  while (pending.length > 0) {
    const current = pending.pop();
    if (current === undefined) break;
    if (isUnknownArray(current.value)) {
      if (current.depth >= RULE_CONTAINER_DEPTH_LIMIT) return false;
      for (const child of current.value) {
        pending.push({ value: child, depth: current.depth + 1 });
      }
      continue;
    }
    if (isJsonObject(current.value)) {
      if (current.depth >= RULE_CONTAINER_DEPTH_LIMIT) return false;
      for (const child of Object.values(current.value)) {
        pending.push({ value: child, depth: current.depth + 1 });
      }
    }
  }
  return true;
}

export function ruleFieldPointer(path: readonly (string | number)[]): string {
  return `/${path.map((segment) => String(segment).replaceAll('~', '~0').replaceAll('/', '~1')).join('/')}`;
}

export function valueExistsAtPath(
  source: Record<string, unknown>,
  path: readonly (string | number)[]
): boolean {
  let current: unknown = source;
  for (const segment of path) {
    if (typeof segment === 'number' && isUnknownArray(current)) {
      if (segment < 0 || segment >= current.length) return false;
      current = current[segment];
    } else if (typeof segment === 'string' && isJsonObject(current)) {
      if (!Object.prototype.hasOwnProperty.call(current, segment)) return false;
      current = current[segment];
    } else {
      return false;
    }
  }
  return true;
}

export function valueAtPath(source: Record<string, unknown>, path: readonly (string | number)[]): unknown {
  let current: unknown = source;
  for (const segment of path) {
    if (typeof segment === 'number' && isUnknownArray(current)) current = current[segment];
    else if (typeof segment === 'string' && isJsonObject(current)) current = current[segment];
    else return undefined;
  }
  return current;
}

export function patchRuleValue(
  source: Record<string, unknown>,
  path: readonly (string | number)[],
  value: unknown
): Record<string, unknown> {
  const next = cloneRuleValue(source);
  let parent: unknown = next;
  for (const segment of path.slice(0, -1)) {
    if (typeof segment === 'number' && isUnknownArray(parent)) parent = parent[segment];
    else if (typeof segment === 'string' && isJsonObject(parent)) parent = parent[segment];
    else throw new TypeError('无法定位不透明规则字段');
  }
  const leaf = path.at(-1);
  if (typeof leaf === 'number' && isUnknownArray(parent)) {
    if (value === undefined) parent.splice(leaf, 1);
    else parent[leaf] = value;
  } else if (typeof leaf === 'string' && isJsonObject(parent)) {
    if (value === undefined) Reflect.deleteProperty(parent, leaf);
    else parent[leaf] = value;
  } else {
    throw new TypeError('无法更新不透明规则字段');
  }
  return next;
}

function schemaTypeMatches(value: unknown, schema: RJSFSchema): boolean {
  const expected = schema.type;
  if (expected === undefined) return true;
  const types = Array.isArray(expected) ? expected : [expected];
  return types.some((type) => {
    if (type === 'object') return isJsonObject(value);
    if (type === 'array') return isUnknownArray(value);
    if (type === 'null') return value === null;
    if (type === 'number') return isLosslessNumber(value) || typeof value === 'number';
    return typeof value === type;
  });
}

export function OpaqueJsonField({
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
}: RuleFieldProps) {
  const exactValue = valueAtPath(registry.formContext.currentValue, fieldPathId.path) ?? formData;
  const externalText = useMemo(
    () => (exactValue === undefined ? '' : stringifyRuleValue(exactValue, 2)),
    [exactValue]
  );
  const baselineExists = valueExistsAtPath(registry.formContext.baselineValue, fieldPathId.path);
  const baselineValue = baselineExists
    ? valueAtPath(registry.formContext.baselineValue, fieldPathId.path)
    : undefined;
  const baselineText = baselineExists ? stringifyRuleValue(baselineValue, 2) : '';
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
  const canRestore = syntaxError !== undefined || text !== baselineText;
  const pointer = ruleFieldPointer(fieldPathId.path);

  return (
    <div className="manage-opaque-field">
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
      {canRestore ? (
        <div className="manage-form__actions">
          <Button
            variant="secondary"
            isDisabled={disabled === true || readonly === true}
            aria-label={`撤销字段 ${pointer}`}
            onPress={() => {
              setText(baselineText);
              setInvalid(undefined);
              registry.formContext.restoreField(fieldPathId.path);
            }}
          >
            撤销此字段
          </Button>
        </div>
      ) : null}
    </div>
  );
}

function jsonKind(value: unknown): JsonKind {
  if (value === null) return 'null';
  if (isLosslessNumber(value) || typeof value === 'number') return 'number';
  if (isUnknownArray(value)) return 'array';
  if (isJsonObject(value)) return 'object';
  if (typeof value === 'boolean') return 'boolean';
  return 'string';
}

function defaultJsonValue(kind: JsonKind): unknown {
  switch (kind) {
    case 'object':
      return {};
    case 'array':
      return [];
    case 'number':
      return parseRuleValue('0');
    case 'boolean':
      return false;
    case 'null':
      return null;
    default:
      return '';
  }
}

function nextObjectKey(value: Record<string, unknown>): string {
  let suffix = 1;
  while (Object.prototype.hasOwnProperty.call(value, `field${suffix}`)) suffix += 1;
  return `field${suffix}`;
}

function JsonNumberInput({
  value,
  label,
  path,
  isDisabled,
  formContext,
  onChange
}: {
  value: unknown;
  label: string;
  path: readonly (string | number)[];
  isDisabled: boolean;
  formContext: RuleFormContext;
  onChange: (value: unknown) => void;
}) {
  const externalText = String(value);
  const [text, setText] = useState(externalText);
  const [error, setError] = useState<string>();
  const invalidPath = `${ruleFieldPointer(path)}:visual-number`;
  const setOpaqueInvalid = formContext.setOpaqueInvalid;

  useEffect(() => {
    if (error === undefined) setText(externalText);
  }, [error, externalText]);
  useEffect(() => () => setOpaqueInvalid(invalidPath, false), [invalidPath, setOpaqueInvalid]);

  return (
    <TextInput
      label={`${label}（精确 JSON 数字）`}
      value={text}
      isDisabled={isDisabled}
      errorMessage={error}
      description="按原始十进制文本保存，不经过 JavaScript Number。"
      onChange={(nextText) => {
        setText(nextText);
        try {
          const parsed = parseRuleValue(nextText);
          if (!isLosslessNumber(parsed) && typeof parsed !== 'number') {
            throw new TypeError('必须是单个 JSON 数字');
          }
          setError(undefined);
          setOpaqueInvalid(invalidPath, false);
          onChange(parsed);
        } catch (cause) {
          const message = cause instanceof Error ? cause.message : '不是合法的 JSON 数字';
          setError(message);
          setOpaqueInvalid(invalidPath, true);
        }
      }}
    />
  );
}

function RenameableKey({
  label,
  value,
  siblings,
  pattern,
  patternMessage,
  isDisabled,
  onRename
}: {
  label: string;
  value: string;
  siblings: readonly string[];
  pattern?: RegExp;
  patternMessage?: string;
  isDisabled: boolean;
  onRename: (name: string) => void;
}) {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  const duplicate = draft !== value && siblings.includes(draft);
  const patternInvalid = pattern !== undefined && !pattern.test(draft);
  const error = duplicate ? '名称已经存在' : patternInvalid ? patternMessage : undefined;

  return (
    <TextInput
      label={label}
      value={draft}
      isDisabled={isDisabled}
      errorMessage={error}
      onChange={(next) => {
        setDraft(next);
        if (next !== value && !siblings.includes(next) && (pattern === undefined || pattern.test(next))) {
          onRename(next);
        }
      }}
    />
  );
}

interface JsonTreeEditorProps {
  value: unknown;
  label: string;
  path: readonly (string | number)[];
  isDisabled: boolean;
  formContext: RuleFormContext;
  onChange: (value: unknown) => void;
}

function JsonTreeEditor(props: JsonTreeEditorProps) {
  if (!ruleValueFitsContainerDepth(props.value, props.path.length)) {
    return (
      <p className="manage-inline-warning" role="alert">
        结构化编辑已暂停：{props.label} 会使规则容器嵌套超过 {RULE_CONTAINER_DEPTH_LIMIT}{' '}
        层，服务端同样会拒绝；请使用无损原始 JSON 减少嵌套。
      </p>
    );
  }
  return <JsonTreeEditorNode {...props} />;
}

function JsonTreeEditorNode({ value, label, path, isDisabled, formContext, onChange }: JsonTreeEditorProps) {
  const kind = jsonKind(value);
  const objectEntries = isJsonObject(value) ? Object.entries(value) : [];
  const objectWindow = useLocalCollectionWindow(objectEntries.length);
  const arrayWindow = useLocalCollectionWindow(isUnknownArray(value) ? value.length : 0);

  return (
    <div className="manage-json-node">
      <div className="manage-json-node__header">
        <strong>{label}</strong>
        <Select
          label={`${label} 类型`}
          options={JSON_KIND_OPTIONS}
          selectedKey={kind}
          isDisabled={isDisabled}
          onSelectionChange={(key) => {
            if (key !== null && key !== kind) onChange(defaultJsonValue(key as JsonKind));
          }}
        />
      </div>

      {kind === 'object' && isJsonObject(value) ? (
        <div className="manage-json-node__children">
          <LocalCollectionPager label={`${label} 对象属性`} window={objectWindow} />
          {objectEntries.slice(objectWindow.start, objectWindow.end).map(([key, child], offset) => {
            const index = objectWindow.start + offset;
            return (
              <div className="manage-json-entry" key={key}>
                <RenameableKey
                  label={`属性 ${index + 1} 名称`}
                  value={key}
                  siblings={Object.keys(value)}
                  isDisabled={isDisabled}
                  onRename={(nextKey) => {
                    const next = Object.fromEntries(
                      Object.entries(value).map(([entryKey, entryValue]) => [
                        entryKey === key ? nextKey : entryKey,
                        entryValue
                      ])
                    );
                    onChange(next);
                  }}
                />
                <JsonTreeEditorNode
                  value={child}
                  label={`/${key}`}
                  path={[...path, key]}
                  isDisabled={isDisabled}
                  formContext={formContext}
                  onChange={(nextChild) => onChange({ ...value, [key]: nextChild })}
                />
                <Button
                  variant="danger"
                  isDisabled={isDisabled}
                  aria-label={`删除 JSON 属性 ${key}`}
                  onPress={() => {
                    const next = { ...value };
                    Reflect.deleteProperty(next, key);
                    onChange(next);
                  }}
                >
                  删除属性
                </Button>
              </div>
            );
          })}
          <Button
            variant="secondary"
            isDisabled={isDisabled}
            onPress={() => {
              objectWindow.showIndex(objectEntries.length);
              onChange({ ...value, [nextObjectKey(value)]: '' });
            }}
          >
            添加 JSON 属性
          </Button>
        </div>
      ) : null}

      {kind === 'array' && isUnknownArray(value) ? (
        <div className="manage-json-node__children">
          <LocalCollectionPager label={`${label} 数组`} window={arrayWindow} />
          {value.slice(arrayWindow.start, arrayWindow.end).map((child, offset) => {
            const index = arrayWindow.start + offset;
            return (
              <div className="manage-json-entry" key={index}>
                <JsonTreeEditorNode
                  value={child}
                  label={`项目 ${index + 1}`}
                  path={[...path, index]}
                  isDisabled={isDisabled}
                  formContext={formContext}
                  onChange={(nextChild) => {
                    const next = [...value];
                    next[index] = nextChild;
                    onChange(next);
                  }}
                />
                <div className="manage-form__actions">
                  <Button
                    variant="secondary"
                    isDisabled={isDisabled || index === 0}
                    aria-label={`上移 JSON 项目 ${index + 1}`}
                    onPress={() => {
                      const next = [...value];
                      [next[index - 1], next[index]] = [next[index], next[index - 1]];
                      onChange(next);
                    }}
                  >
                    上移
                  </Button>
                  <Button
                    variant="secondary"
                    isDisabled={isDisabled || index === value.length - 1}
                    aria-label={`下移 JSON 项目 ${index + 1}`}
                    onPress={() => {
                      const next = [...value];
                      [next[index], next[index + 1]] = [next[index + 1], next[index]];
                      onChange(next);
                    }}
                  >
                    下移
                  </Button>
                  <Button
                    variant="danger"
                    isDisabled={isDisabled}
                    aria-label={`删除 JSON 项目 ${index + 1}`}
                    onPress={() => onChange(value.filter((_, itemIndex) => itemIndex !== index))}
                  >
                    删除项目
                  </Button>
                </div>
              </div>
            );
          })}
          <Button
            variant="secondary"
            isDisabled={isDisabled}
            onPress={() => {
              arrayWindow.showIndex(value.length);
              onChange([...value, null]);
            }}
          >
            添加 JSON 项目
          </Button>
        </div>
      ) : null}

      {kind === 'string' ? (
        <TextInput
          label={`${label} 字符串`}
          value={typeof value === 'string' ? value : ''}
          isDisabled={isDisabled}
          onChange={onChange}
        />
      ) : null}
      {kind === 'number' ? (
        <JsonNumberInput
          value={value}
          label={label}
          path={path}
          isDisabled={isDisabled}
          formContext={formContext}
          onChange={onChange}
        />
      ) : null}
      {kind === 'boolean' ? (
        <Checkbox
          isSelected={value === true}
          isDisabled={isDisabled}
          onChange={(selected) => onChange(selected)}
        >
          {label}
        </Checkbox>
      ) : null}
      {kind === 'null' ? <p className="manage-section__description">{label} 当前为 null。</p> : null}
    </div>
  );
}

function FullJsonStructureEditor({
  value,
  label,
  path,
  isDisabled,
  formContext,
  onChange
}: {
  value: unknown;
  label: string;
  path: readonly (string | number)[];
  isDisabled: boolean;
  formContext: RuleFormContext;
  onChange: (value: unknown) => void;
}) {
  const [isOpen, setIsOpen] = useState(false);
  const panelId = useId();

  return (
    <div className="manage-structured-field__tree">
      <h4>完整 JSON 结构</h4>
      <p className="manage-section__description">
        需要编辑未知关键字、任意嵌套对象或数组时展开；所有数字继续按精确十进制文本处理。
      </p>
      <Button
        variant="secondary"
        aria-expanded={isOpen}
        aria-controls={panelId}
        onPress={() => setIsOpen((current) => !current)}
      >
        {isOpen ? '收起完整 JSON 结构' : '展开完整 JSON 结构'}
      </Button>
      {isOpen ? (
        <div id={panelId}>
          <JsonTreeEditor
            value={value}
            label={label}
            path={path}
            isDisabled={isDisabled}
            formContext={formContext}
            onChange={onChange}
          />
        </div>
      ) : null}
    </div>
  );
}

function rawFieldProps(props: RuleFieldProps, title: string, description: string): RuleFieldProps {
  return {
    ...props,
    schema: { ...props.schema, title, description }
  };
}

function renameObjectKey(
  value: Record<string, unknown>,
  oldKey: string,
  nextKey: string
): Record<string, unknown> {
  return Object.fromEntries(
    Object.entries(value).map(([key, child]) => [key === oldKey ? nextKey : key, child])
  );
}

function setOptionalString(
  value: Record<string, unknown>,
  key: string,
  nextValue: string
): Record<string, unknown> {
  const next = { ...value };
  if (nextValue === '') Reflect.deleteProperty(next, key);
  else next[key] = nextValue;
  return next;
}

function ParameterPropertyCard({
  name,
  definition,
  schemaValue,
  isDisabled,
  onChange
}: {
  name: string;
  definition: unknown;
  schemaValue: Record<string, unknown>;
  isDisabled: boolean;
  onChange: (value: Record<string, unknown>) => void;
}) {
  const properties = isJsonObject(schemaValue.properties) ? schemaValue.properties : {};
  const propertyNames = Object.keys(properties);
  const requiredValues: unknown[] = isUnknownArray(schemaValue.required) ? schemaValue.required : [];
  const isRequired = requiredValues.some((value) => value === name);
  const definitionObject = isJsonObject(definition) ? definition : null;

  const updateDefinition = (nextDefinition: unknown) => {
    onChange({ ...schemaValue, properties: { ...properties, [name]: nextDefinition } });
  };

  return (
    <article className="manage-structured-card">
      <RenameableKey
        label="参数名称"
        value={name}
        siblings={propertyNames}
        isDisabled={isDisabled}
        onRename={(nextName) => {
          const nextProperties = renameObjectKey(properties, name, nextName);
          const next: Record<string, unknown> = { ...schemaValue, properties: nextProperties };
          if (isUnknownArray(schemaValue.required)) {
            next.required = requiredValues.map((value) => (value === name ? nextName : value));
          }
          onChange(next);
        }}
      />
      {definitionObject === null ? (
        <p className="manage-inline-warning">
          此参数定义不是对象；可在下方完整结构或原始 JSON 中编辑，现有值不会被删除。
        </p>
      ) : (
        <>
          <Select
            label={`${name} 类型`}
            options={PARAMETER_TYPE_OPTIONS}
            selectedKey={typeof definitionObject.type === 'string' ? definitionObject.type : '__unset'}
            isDisabled={isDisabled}
            description={
              isUnknownArray(definitionObject.type)
                ? '当前为联合类型；选择单一类型会替换联合声明。'
                : undefined
            }
            onSelectionChange={(key) => {
              if (key === null) return;
              const next = { ...definitionObject };
              if (key === '__unset') Reflect.deleteProperty(next, 'type');
              else next.type = key;
              updateDefinition(next);
            }}
          />
          <TextInput
            label={`${name} 标题`}
            value={typeof definitionObject.title === 'string' ? definitionObject.title : ''}
            isDisabled={isDisabled}
            onChange={(value) => updateDefinition(setOptionalString(definitionObject, 'title', value))}
          />
          <TextInput
            label={`${name} 说明`}
            value={typeof definitionObject.description === 'string' ? definitionObject.description : ''}
            isDisabled={isDisabled}
            isMultiline
            rows={2}
            onChange={(value) => updateDefinition(setOptionalString(definitionObject, 'description', value))}
          />
          <Checkbox
            isSelected={isRequired}
            isDisabled={
              isDisabled || (!isUnknownArray(schemaValue.required) && schemaValue.required !== undefined)
            }
            onChange={(selected) => {
              const nextRequired = selected
                ? [...requiredValues.filter((value) => value !== name), name]
                : requiredValues.filter((value) => value !== name);
              const next = { ...schemaValue };
              if (nextRequired.length === 0) Reflect.deleteProperty(next, 'required');
              else next.required = nextRequired;
              onChange(next);
            }}
          >
            必填参数
          </Checkbox>
        </>
      )}
      <Button
        variant="danger"
        isDisabled={isDisabled}
        aria-label={`删除参数 ${name}`}
        onPress={() => {
          const nextProperties = { ...properties };
          Reflect.deleteProperty(nextProperties, name);
          const next: Record<string, unknown> = { ...schemaValue, properties: nextProperties };
          if (isUnknownArray(schemaValue.required)) {
            const nextRequired = requiredValues.filter((value) => value !== name);
            if (nextRequired.length === 0) Reflect.deleteProperty(next, 'required');
            else next.required = nextRequired;
          }
          onChange(next);
        }}
      >
        删除参数
      </Button>
    </article>
  );
}

export function ParameterSchemaField(props: RuleFieldProps) {
  const exactValue =
    valueAtPath(props.registry.formContext.currentValue, props.fieldPathId.path) ??
    (props.formData as unknown);
  const schemaValue = isJsonObject(exactValue) ? exactValue : null;
  const properties =
    schemaValue !== null && isJsonObject(schemaValue.properties) ? schemaValue.properties : null;
  const propertyEntries = Object.entries(properties ?? {});
  const propertyWindow = useLocalCollectionWindow(propertyEntries.length);
  const [newName, setNewName] = useState('');
  const duplicate = properties !== null && Object.prototype.hasOwnProperty.call(properties, newName);
  const isDisabled = props.disabled === true || props.readonly === true;
  const update = (next: unknown) => props.registry.formContext.changeOpaque(props.fieldPathId.path, next);

  return (
    <section className="manage-structured-field" aria-labelledby={`${props.fieldPathId.$id}-title`}>
      <div className="manage-structured-field__header">
        <h3 id={`${props.fieldPathId.$id}-title`}>参数 Schema</h3>
        <p className="manage-section__description">
          快捷区维护常用对象参数；完整结构区可构建任意自包含 JSON Schema，所有未知关键字和精确数字都会保留。
        </p>
      </div>
      {schemaValue === null ? (
        <p className="manage-inline-warning">当前参数 Schema 不是对象；请使用完整结构或原始 JSON 修复。</p>
      ) : (
        <>
          <div className="manage-form__row">
            <Checkbox
              isSelected={schemaValue.additionalProperties === true}
              isDisabled={
                isDisabled ||
                (schemaValue.additionalProperties !== undefined &&
                  typeof schemaValue.additionalProperties !== 'boolean')
              }
              onChange={(selected) => update({ ...schemaValue, additionalProperties: selected })}
            >
              允许未声明参数
            </Checkbox>
          </div>
          {properties === null && schemaValue.properties !== undefined ? (
            <p className="manage-inline-warning">
              当前 properties 不是对象；快捷添加已停用，现有值不会被覆盖。
            </p>
          ) : (
            <>
              <div className="manage-structured-list">
                <LocalCollectionPager label="参数 Schema 属性" window={propertyWindow} />
                {propertyEntries.slice(propertyWindow.start, propertyWindow.end).map(([name, definition]) => (
                  <ParameterPropertyCard
                    key={name}
                    name={name}
                    definition={definition}
                    schemaValue={schemaValue}
                    isDisabled={isDisabled}
                    onChange={update}
                  />
                ))}
              </div>
              <div className="manage-form__row">
                <TextInput
                  label="新参数名称"
                  value={newName}
                  isDisabled={isDisabled}
                  errorMessage={duplicate ? '参数名称已经存在' : undefined}
                  onChange={setNewName}
                />
                <Button
                  variant="secondary"
                  isDisabled={isDisabled || newName === '' || duplicate}
                  onPress={() => {
                    propertyWindow.showIndex(propertyEntries.length);
                    update({
                      ...schemaValue,
                      properties: { ...(properties ?? {}), [newName]: { type: 'string' } }
                    });
                    setNewName('');
                  }}
                >
                  添加参数
                </Button>
              </div>
            </>
          )}
        </>
      )}
      <FullJsonStructureEditor
        value={exactValue}
        label="parameter_schema"
        path={props.fieldPathId.path}
        isDisabled={isDisabled}
        formContext={props.registry.formContext}
        onChange={update}
      />
      <div className="manage-structured-field__raw">
        <h4>无损原始 JSON</h4>
        <OpaqueJsonField
          {...rawFieldProps(
            props,
            'parameter_schema 原始 JSON',
            '高级逃生路径：直接编辑完整 JSON Schema；未知关键字和数字原文不会经过普通表单控件。'
          )}
        />
      </div>
    </section>
  );
}

export function RuleTestsField(props: RuleFieldProps) {
  const exactValue =
    valueAtPath(props.registry.formContext.currentValue, props.fieldPathId.path) ??
    (props.formData as unknown);
  const tests: unknown[] | null = isUnknownArray(exactValue) ? exactValue : null;
  const [newTestId, setNewTestId] = useState('');
  const existingIDs = (tests ?? [])
    .map((value) => (isJsonObject(value) && typeof value.id === 'string' ? value.id : null))
    .filter((value): value is string => value !== null);
  const duplicate = existingIDs.includes(newTestId);
  const testWindow = useLocalCollectionWindow(tests?.length ?? 0);
  const isDisabled = props.disabled === true || props.readonly === true;
  const update = (next: unknown) => props.registry.formContext.changeOpaque(props.fieldPathId.path, next);

  return (
    <section className="manage-structured-field" aria-labelledby={`${props.fieldPathId.$id}-title`}>
      <div className="manage-structured-field__header">
        <h3 id={`${props.fieldPathId.$id}-title`}>规则测试</h3>
        <p className="manage-section__description">
          快捷区维护当前稳定的测试标识和说明；输入、断言及未来字段通过完整结构编辑，不预设尚未冻结的测试协议。
        </p>
      </div>
      {tests === null ? (
        <p className="manage-inline-warning">当前 tests 不是数组；请使用完整结构或原始 JSON 修复。</p>
      ) : (
        <>
          <div className="manage-structured-list">
            <LocalCollectionPager label="规则测试" window={testWindow} />
            {tests.slice(testWindow.start, testWindow.end).map((test, offset) => {
              const index = testWindow.start + offset;
              return (
                <article className="manage-structured-card" key={index}>
                  <h4>测试 {index + 1}</h4>
                  {isJsonObject(test) ? (
                    <>
                      <TextInput
                        label={`测试 ${index + 1} ID`}
                        value={typeof test.id === 'string' ? test.id : ''}
                        isDisabled={isDisabled}
                        onChange={(value) => {
                          const next = [...tests];
                          next[index] = setOptionalString(test, 'id', value);
                          update(next);
                        }}
                      />
                      <TextInput
                        label={`测试 ${index + 1} 说明`}
                        value={typeof test.description === 'string' ? test.description : ''}
                        isDisabled={isDisabled}
                        isMultiline
                        rows={2}
                        onChange={(value) => {
                          const next = [...tests];
                          next[index] = setOptionalString(test, 'description', value);
                          update(next);
                        }}
                      />
                    </>
                  ) : (
                    <p className="manage-inline-warning">
                      此测试不是对象；现有值保持不变，可在完整结构或原始 JSON 中编辑。
                    </p>
                  )}
                  <div className="manage-form__actions">
                    <Button
                      variant="secondary"
                      isDisabled={isDisabled || index === 0}
                      aria-label={`上移测试 ${index + 1}`}
                      onPress={() => {
                        const next = [...tests];
                        [next[index - 1], next[index]] = [next[index], next[index - 1]];
                        update(next);
                      }}
                    >
                      上移
                    </Button>
                    <Button
                      variant="secondary"
                      isDisabled={isDisabled || index === tests.length - 1}
                      aria-label={`下移测试 ${index + 1}`}
                      onPress={() => {
                        const next = [...tests];
                        [next[index], next[index + 1]] = [next[index + 1], next[index]];
                        update(next);
                      }}
                    >
                      下移
                    </Button>
                    <Button
                      variant="danger"
                      isDisabled={isDisabled || tests.length <= 1}
                      aria-label={`删除测试 ${index + 1}`}
                      onPress={() => update(tests.filter((_, testIndex) => testIndex !== index))}
                    >
                      删除测试
                    </Button>
                  </div>
                </article>
              );
            })}
          </div>
          <div className="manage-form__row">
            <TextInput
              label="新测试 ID"
              value={newTestId}
              isDisabled={isDisabled}
              errorMessage={duplicate ? '测试 ID 已经存在' : undefined}
              onChange={setNewTestId}
            />
            <Button
              variant="secondary"
              isDisabled={isDisabled || newTestId === '' || duplicate}
              onPress={() => {
                testWindow.showIndex(tests.length);
                update([...tests, { id: newTestId }]);
                setNewTestId('');
              }}
            >
              添加测试
            </Button>
          </div>
        </>
      )}
      <FullJsonStructureEditor
        value={exactValue}
        label="tests"
        path={props.fieldPathId.path}
        isDisabled={isDisabled}
        formContext={props.registry.formContext}
        onChange={update}
      />
      <div className="manage-structured-field__raw">
        <h4>无损原始 JSON</h4>
        <OpaqueJsonField
          {...rawFieldProps(
            props,
            'tests 原始 JSON',
            '高级逃生路径：直接编辑测试数组；任意输入、断言、未知字段和精确数字都会保留。'
          )}
        />
      </div>
    </section>
  );
}

function ExtensionCard({
  namespace,
  extension,
  extensions,
  rootPath,
  isDisabled,
  formContext,
  onChange
}: {
  namespace: string;
  extension: unknown;
  extensions: Record<string, unknown>;
  rootPath: readonly (string | number)[];
  isDisabled: boolean;
  formContext: RuleFormContext;
  onChange: (value: Record<string, unknown>) => void;
}) {
  const classified = isJsonObject(extension) && typeof extension.semantic === 'boolean';

  return (
    <article className="manage-structured-card">
      <RenameableKey
        label="Extension namespace"
        value={namespace}
        siblings={Object.keys(extensions)}
        pattern={EXTENSION_NAMESPACE_PATTERN}
        patternMessage="必须是至少两段的小写反向域名 namespace"
        isDisabled={isDisabled}
        onRename={(nextNamespace) => onChange(renameObjectKey(extensions, namespace, nextNamespace))}
      />
      {classified && isJsonObject(extension) ? (
        <>
          <div className="manage-form__row">
            <Checkbox
              isSelected={extension.required === true}
              isDisabled={isDisabled}
              onChange={(selected) =>
                onChange({ ...extensions, [namespace]: { ...extension, required: selected } })
              }
            >
              required
            </Checkbox>
            <Checkbox
              isSelected={extension.semantic === true}
              isDisabled={isDisabled}
              onChange={(selected) =>
                onChange({ ...extensions, [namespace]: { ...extension, semantic: selected } })
              }
            >
              semantic
            </Checkbox>
          </div>
          <TextInput
            label={`${namespace} version`}
            value={typeof extension.version === 'string' ? extension.version : ''}
            isDisabled={isDisabled}
            description="required 或 semantic 扩展必须填写后端注册表支持的版本。"
            onChange={(value) =>
              onChange({ ...extensions, [namespace]: setOptionalString(extension, 'version', value) })
            }
          />
          {Object.prototype.hasOwnProperty.call(extension, 'payload') ? (
            <JsonTreeEditor
              value={extension.payload}
              label={`${namespace} payload`}
              path={[...rootPath, namespace, 'payload']}
              isDisabled={isDisabled}
              formContext={formContext}
              onChange={(payload) => onChange({ ...extensions, [namespace]: { ...extension, payload } })}
            />
          ) : (
            <Button
              variant="secondary"
              isDisabled={isDisabled}
              onPress={() => onChange({ ...extensions, [namespace]: { ...extension, payload: {} } })}
            >
              添加 payload
            </Button>
          )}
        </>
      ) : (
        <>
          <p className="manage-section__description">
            legacy/未分类扩展：缺少 boolean semantic，按 optional + nonsemantic 无损保留。
          </p>
          <JsonTreeEditor
            value={extension}
            label={`${namespace} 值`}
            path={[...rootPath, namespace]}
            isDisabled={isDisabled}
            formContext={formContext}
            onChange={(value) => onChange({ ...extensions, [namespace]: value })}
          />
        </>
      )}
      <Button
        variant="danger"
        isDisabled={isDisabled}
        aria-label={`删除 extension ${namespace}`}
        onPress={() => {
          const next = { ...extensions };
          Reflect.deleteProperty(next, namespace);
          onChange(next);
        }}
      >
        删除 extension
      </Button>
    </article>
  );
}

export function ExtensionsField(props: RuleFieldProps) {
  const exactValue =
    valueAtPath(props.registry.formContext.currentValue, props.fieldPathId.path) ??
    (props.formData as unknown);
  const extensions = isJsonObject(exactValue) ? exactValue : null;
  const [newNamespace, setNewNamespace] = useState('');
  const duplicate = extensions !== null && Object.prototype.hasOwnProperty.call(extensions, newNamespace);
  const namespaceInvalid = newNamespace !== '' && !EXTENSION_NAMESPACE_PATTERN.test(newNamespace);
  const extensionEntries = Object.entries(extensions ?? {});
  const extensionWindow = useLocalCollectionWindow(extensionEntries.length);
  const isDisabled = props.disabled === true || props.readonly === true;
  const update = (next: unknown) => props.registry.formContext.changeOpaque(props.fieldPathId.path, next);

  return (
    <section className="manage-structured-field" aria-labelledby={`${props.fieldPathId.$id}-title`}>
      <div className="manage-structured-field__header">
        <h3 id={`${props.fieldPathId.$id}-title`}>扩展</h3>
        <p className="manage-section__description">
          分类扩展可直接维护 required、semantic、version 与任意 payload；未知 legacy namespace
          保持原结构和数字文本。
        </p>
      </div>
      {extensions === null ? (
        <p className="manage-inline-warning">当前 extensions 不是对象；请使用完整结构或原始 JSON 修复。</p>
      ) : (
        <>
          <div className="manage-structured-list">
            <LocalCollectionPager label="Extensions" window={extensionWindow} />
            {extensionEntries
              .slice(extensionWindow.start, extensionWindow.end)
              .map(([namespace, extension]) => (
                <ExtensionCard
                  key={namespace}
                  namespace={namespace}
                  extension={extension}
                  extensions={extensions}
                  rootPath={props.fieldPathId.path}
                  isDisabled={isDisabled}
                  formContext={props.registry.formContext}
                  onChange={update}
                />
              ))}
          </div>
          <div className="manage-form__row">
            <TextInput
              label="新 Extension namespace"
              value={newNamespace}
              isDisabled={isDisabled}
              errorMessage={
                duplicate
                  ? 'namespace 已经存在'
                  : namespaceInvalid
                    ? '必须是至少两段的小写反向域名 namespace'
                    : undefined
              }
              onChange={setNewNamespace}
            />
            <Button
              variant="secondary"
              isDisabled={isDisabled || newNamespace === '' || duplicate || namespaceInvalid}
              onPress={() => {
                extensionWindow.showIndex(extensionEntries.length);
                update({
                  ...extensions,
                  [newNamespace]: { required: false, semantic: false, payload: {} }
                });
                setNewNamespace('');
              }}
            >
              添加 extension
            </Button>
          </div>
        </>
      )}
      <FullJsonStructureEditor
        value={exactValue}
        label="extensions"
        path={props.fieldPathId.path}
        isDisabled={isDisabled}
        formContext={props.registry.formContext}
        onChange={update}
      />
      <div className="manage-structured-field__raw">
        <h4>无损原始 JSON</h4>
        <OpaqueJsonField
          {...rawFieldProps(
            props,
            'extensions 原始 JSON',
            '高级逃生路径：直接编辑整个 namespace 对象；未知扩展、字段和值保持无损。'
          )}
        />
      </div>
    </section>
  );
}
