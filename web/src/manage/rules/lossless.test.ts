import { isLosslessNumber } from 'lossless-json';
import { describe, expect, it } from 'vitest';
import { cloneRuleValue, parseRuleText, stringifyRuleValue } from './lossless';

describe('规则 JSON 无损往返', () => {
  it('保留未知字段中的大整数、高精度小数和指数词法', () => {
    const source =
      '{"rule_set_id":"rset_018f47d2-5c16-7a44-a8a0-000000000001","schema_version":1,"version":"0.1.0","extensions":{"example.lossless":{"payload":{"integer":9007199254740993123,"decimal":0.1234567890123456789,"exponent":1e+40}}}}';
    const parsed = parseRuleText(source);
    expect(parsed.error).toBeUndefined();
    expect(parsed.value?.schema_version).toBe(1);
    const extension = parsed.value?.extensions as Record<string, unknown>;
    const payload = (extension['example.lossless'] as Record<string, unknown>).payload as Record<
      string,
      unknown
    >;
    expect(isLosslessNumber(payload.integer)).toBe(true);

    if (parsed.value === null) throw new Error('规则对象未解析');
    parsed.value.version = '0.1.1';
    const written = stringifyRuleValue(parsed.value);
    expect(written).toContain('9007199254740993123');
    expect(written).toContain('0.1234567890123456789');
    expect(written).toContain('1e+40');
  });

  it('拒绝重复键和非对象根值', () => {
    expect(parseRuleText('{"version":"1","version":"2"}').error).toContain('重复键');
    expect(parseRuleText('[1,2,3]').error).toBe('规则包必须是 JSON 对象');
  });

  it('深拷贝时不把 LosslessNumber 转成普通 Number', () => {
    const value = parseRuleText('{"schema_version":1,"tests":[{"exact":9007199254740993}]}').value;
    const clone = cloneRuleValue(value);
    const exact = ((clone?.tests as unknown[])[0] as Record<string, unknown>).exact;
    expect(isLosslessNumber(exact)).toBe(true);
    expect(stringifyRuleValue(clone)).toContain('9007199254740993');
  });
});
