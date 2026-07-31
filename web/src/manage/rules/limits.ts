// 必须与 internal/rules.MaxRulePackageBytes 保持一致。前端只用它在本地解析、
// Schema/AJV 和网络提交前提供有界降级；服务端仍是规则内容大小的最终权威。
export const RULE_PACKAGE_MAX_BYTES = 8 * 1024 * 1024;

const utf8Encoder = new TextEncoder();

export function rulePackageByteLength(value: string): number {
  return utf8Encoder.encode(value).byteLength;
}

export function rulePackageSizeError(byteLength: number): string | undefined {
  return byteLength > RULE_PACKAGE_MAX_BYTES
    ? `规则包内容超过 8 MiB 上限（当前 ${byteLength} 字节）。`
    : undefined;
}
