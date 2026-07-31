import { describe, expect, it } from 'vitest';
import { RULE_PACKAGE_MAX_BYTES, rulePackageByteLength, rulePackageSizeError } from './limits';

describe('规则包字节上限', () => {
  it('按 UTF-8 字节而不是 JavaScript 字符数核对 8 MiB 权威边界', () => {
    expect(rulePackageByteLength('画')).toBe(3);
    expect(rulePackageSizeError(RULE_PACKAGE_MAX_BYTES)).toBeUndefined();
    expect(rulePackageSizeError(RULE_PACKAGE_MAX_BYTES + 1)).toBe(
      `规则包内容超过 8 MiB 上限（当前 ${RULE_PACKAGE_MAX_BYTES + 1} 字节）。`
    );
  });
});
