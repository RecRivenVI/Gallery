import { isLosslessNumber, parse as parseLossless, stringify as stringifyLossless } from 'lossless-json';

export interface ParsedRuleText {
  value: Record<string, unknown> | null;
  error?: string;
}

export function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

/** 解析任意 JSON 值，同时拒绝会让编辑结果产生歧义的重复键。 */
export function parseRuleValue(text: string): unknown {
  return parseLossless(text, null, {
    onDuplicateKey: ({ key }) => {
      throw new SyntaxError(`对象包含重复键 ${JSON.stringify(key)}`);
    }
  });
}

/**
 * 规则 JSON 的数字不能经过 JavaScript Number。唯一例外是根级 schema_version：它是
 * Schema 固定的安全整数，RJSF/AJV 需要看到真正的 number 才能匹配 const。
 */
export function parseRuleText(text: string): ParsedRuleText {
  try {
    const value = parseRuleValue(text);
    if (!isRecord(value)) return { value: null, error: '规则包必须是 JSON 对象' };
    const schemaVersion = value.schema_version;
    if (isLosslessNumber(schemaVersion) && schemaVersion.toString() === '1') {
      value.schema_version = 1;
    }
    return { value };
  } catch (error) {
    return {
      value: null,
      error: error instanceof Error ? error.message : '不是合法的 JSON'
    };
  }
}

export function stringifyRuleValue(value: unknown, space = 2): string {
  const encoded = stringifyLossless(value, null, space);
  if (encoded === undefined) throw new TypeError('规则值无法写成 JSON');
  return encoded;
}

export function cloneRuleValue<T>(value: T): T {
  return parseLossless(stringifyRuleValue(value)) as T;
}
