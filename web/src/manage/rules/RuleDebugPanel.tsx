import { useMemo, useState } from 'react';
import { Button, TextInput } from '../../design';
import { useCapability } from '../../shared/session';
import {
  useRuleDryRun,
  useRuleExplain,
  useRuleTrace,
  type RuleDebugInput,
  type RuleDebugRequest,
  type RuleDryRunResult,
  type RuleExplainResult,
  type RuleTraceResult
} from '../api';
import { Facts, InlineError, MonoId, Section } from '../ui';
import { isRecord, parseRuleText, parseRuleValue, stringifyRuleValue } from './lossless';

type DebugMode = 'dry-run' | 'explain' | 'trace';

interface RuleDebugPanelProps {
  packageText: string;
  format: 'json' | 'yaml' | 'toml';
  isLocked: boolean;
}

interface PreparedDebugInput {
  input?: RuleDebugInput;
  packageError?: string;
  parametersError?: string;
  sampleError?: string;
}

const DEFAULT_PARAMETERS = '{}';
const DEFAULT_SAMPLE = `{
  "path": "author/work",
  "files": [
    {"path": "image.jpg", "size": 1024}
  ],
  "metadata": {
    "author": {"name": "示例作者"}
  }
}`;

function parseObject(text: string, label: string): Record<string, unknown> {
  const value = parseRuleValue(text);
  if (!isRecord(value)) throw new TypeError(`${label}必须是 JSON 对象`);
  return value;
}

function prepareDebugInput(
  packageText: string,
  format: RuleDebugPanelProps['format'],
  parametersText: string,
  sampleText: string
): PreparedDebugInput {
  let packageValue: Record<string, unknown> | undefined;
  let parametersValue: Record<string, unknown> | undefined;
  let sampleValue: Record<string, unknown> | undefined;
  let packageError: string | undefined;
  let parametersError: string | undefined;
  let sampleError: string | undefined;

  if (format !== 'json') {
    packageError = 'Dry Run、Explain 与 Trace 只执行规范 JSON；请先保存并转换 YAML/TOML 草稿';
  } else {
    const parsed = parseRuleText(packageText);
    packageValue = parsed.value ?? undefined;
    packageError = parsed.error;
  }

  try {
    parametersValue = parseObject(parametersText, '参数');
  } catch (error) {
    parametersError = error instanceof Error ? error.message : '参数不是合法的 JSON 对象';
  }

  try {
    sampleValue = parseObject(sampleText, '合成样本');
    if (typeof sampleValue.path !== 'string' || sampleValue.path.length === 0) {
      sampleError = '合成样本必须包含非空 path';
    } else if (!Array.isArray(sampleValue.files)) {
      sampleError = '合成样本的 files 必须是数组';
    } else if (!Object.prototype.hasOwnProperty.call(sampleValue, 'metadata')) {
      sampleError = '合成样本必须显式包含 metadata';
    }
  } catch (error) {
    sampleError = error instanceof Error ? error.message : '合成样本不是合法的 JSON 对象';
  }

  if (
    packageValue === undefined ||
    parametersValue === undefined ||
    sampleValue === undefined ||
    packageError !== undefined ||
    parametersError !== undefined ||
    sampleError !== undefined
  ) {
    return { packageError, parametersError, sampleError };
  }

  const request: RuleDebugRequest = {
    package: packageValue,
    parameters: parametersValue,
    sample: sampleValue as RuleDebugRequest['sample']
  };
  return {
    input: {
      request,
      exactBody: `{"package":${packageText},"parameters":${parametersText},"sample":${sampleText}}`
    }
  };
}

function JSONBlock({ title, value }: { title: string; value: unknown }) {
  return (
    <div>
      <h3>{title}</h3>
      <pre className="manage-code" aria-label={title}>
        {stringifyRuleValue(value, 2)}
      </pre>
    </div>
  );
}

function HashFacts({ ruleVersion, ruleIrHash }: { ruleVersion: string; ruleIrHash: string }) {
  return (
    <Facts
      items={[
        { term: '规则版本', value: <MonoId value={ruleVersion} label="规则版本 hash" /> },
        { term: 'Rule IR', value: <MonoId value={ruleIrHash} label="Rule IR hash" /> }
      ]}
    />
  );
}

function DryRunResult({ result }: { result: RuleDryRunResult }) {
  return (
    <div className="manage-form">
      <HashFacts ruleVersion={result.ruleVersion} ruleIrHash={result.ruleIrHash} />
      <JSONBlock title="Dry Run 作品结果" value={result.work} />
      <JSONBlock title="Dry Run Trace" value={result.trace} />
      <JSONBlock title="Dry Run issues" value={result.issues} />
    </div>
  );
}

function ExplainResult({ result }: { result: RuleExplainResult }) {
  return (
    <div className="manage-form">
      <HashFacts ruleVersion={result.ruleVersion} ruleIrHash={result.ruleIrHash} />
      <JSONBlock title="Explain 字段来源" value={result.fields} />
      <JSONBlock title="Explain Trace" value={result.trace} />
    </div>
  );
}

function TraceResult({ result }: { result: RuleTraceResult }) {
  const ruleVersion = typeof result.ruleVersion === 'string' ? result.ruleVersion : undefined;
  const ruleIrHash = typeof result.ruleIrHash === 'string' ? result.ruleIrHash : undefined;
  return (
    <div className="manage-form">
      {ruleVersion === undefined || ruleIrHash === undefined ? null : (
        <HashFacts ruleVersion={ruleVersion} ruleIrHash={ruleIrHash} />
      )}
      <JSONBlock title="Trace 步骤" value={result.trace ?? []} />
      <JSONBlock title="Trace issues" value={result.issues ?? []} />
    </div>
  );
}

export function RuleDebugPanel({ packageText, format, isLocked }: RuleDebugPanelProps) {
  const canDebug = useCapability('rules.debug');
  const dryRun = useRuleDryRun();
  const explain = useRuleExplain();
  const trace = useRuleTrace();
  const [parametersText, setParametersText] = useState(DEFAULT_PARAMETERS);
  const [sampleText, setSampleText] = useState(DEFAULT_SAMPLE);
  const [active, setActive] = useState<{ mode: DebugMode; fingerprint: string }>();
  const prepared = useMemo(
    () => prepareDebugInput(packageText, format, parametersText, sampleText),
    [format, packageText, parametersText, sampleText]
  );
  const fingerprint = `${format}\u0000${packageText}\u0000${parametersText}\u0000${sampleText}`;
  const pending = dryRun.isPending || explain.isPending || trace.isPending;
  const stale = active !== undefined && active.fingerprint !== fingerprint;

  const run = (mode: DebugMode) => {
    if (prepared.input === undefined || pending || isLocked) return;
    setActive({ mode, fingerprint });
    if (mode === 'dry-run') dryRun.mutate(prepared.input);
    else if (mode === 'explain') explain.mutate(prepared.input);
    else trace.mutate(prepared.input);
  };

  const activeError =
    active?.mode === 'dry-run' ? dryRun.error : active?.mode === 'explain' ? explain.error : trace.error;

  return (
    <Section
      title="Dry Run、Explain 与 Trace"
      description="只对当前编辑器中的规范 JSON 与这里显式填写的合成 Sample 求值；不会读取 Source、服务器路径或真实 metadata。结果受 rules.debug capability 和服务端资源上限约束。"
    >
      {canDebug ? (
        <div className="manage-form">
          <TextInput
            label="调试参数 JSON"
            value={parametersText}
            onChange={setParametersText}
            isMultiline
            rows={5}
            isDisabled={pending || isLocked}
            errorMessage={prepared.parametersError}
            description="必须是 JSON 对象；请求按原始文本发送，精确数字不会经过 JavaScript Number。"
          />
          <TextInput
            label="合成 Sample JSON"
            value={sampleText}
            onChange={setSampleText}
            isMultiline
            rows={12}
            isDisabled={pending || isLocked}
            errorMessage={prepared.sampleError}
            description="path 必须是相对合成路径；files 与 metadata 只来自请求体，服务端拒绝绝对路径、遍历和超限输入。"
          />
          {prepared.packageError === undefined ? null : (
            <p className="ui-field__error" role="alert">
              {prepared.packageError}
            </p>
          )}
          <div className="manage-form__actions">
            <Button
              variant="primary"
              isPending={active?.mode === 'dry-run' && dryRun.isPending}
              isDisabled={prepared.input === undefined || pending || isLocked}
              onPress={() => run('dry-run')}
            >
              执行 Dry Run
            </Button>
            <Button
              variant="secondary"
              isPending={active?.mode === 'explain' && explain.isPending}
              isDisabled={prepared.input === undefined || pending || isLocked}
              onPress={() => run('explain')}
            >
              查看 Explain
            </Button>
            <Button
              variant="secondary"
              isPending={active?.mode === 'trace' && trace.isPending}
              isDisabled={prepared.input === undefined || pending || isLocked}
              onPress={() => run('trace')}
            >
              查看 Trace
            </Button>
          </div>
          {stale ? (
            <p className="manage-section__description">
              规则包、参数或合成 Sample 已变化；旧调试结果已隐藏，请重新执行。
            </p>
          ) : null}
          {stale ? null : <InlineError error={activeError} title="规则调试未能完成" />}
          {!stale && active !== undefined && pending ? (
            <p className="manage-section__description">正在执行受限规则调试…</p>
          ) : stale || active === undefined ? null : active.mode === 'dry-run' &&
            dryRun.data !== undefined ? (
            <DryRunResult result={dryRun.data} />
          ) : active.mode === 'explain' && explain.data !== undefined ? (
            <ExplainResult result={explain.data} />
          ) : active.mode === 'trace' && trace.data !== undefined ? (
            <TraceResult result={trace.data} />
          ) : null}
        </div>
      ) : (
        <p className="manage-section__description">
          当前主体在 global scope 没有 rules.debug，Dry Run、Explain 与 Trace 入口已隐藏。
        </p>
      )}
    </Section>
  );
}
