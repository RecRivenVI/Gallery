// 后端权威 capability 词表的前端副本。
//
// 词表事实源是 `internal/auth.PersonalOwnerCapabilities` 与 control 迁移
// `00020_phase5_security.sql` 的 `security_role_capabilities`；`internal/auth` 的
// `TestWebCapabilityVocabularyMatchesBackend` 会逐项比对本文件，任何一侧新增或改名
// 都会让 Go 门禁失败。
//
// 之所以需要它：`can()` 此前接受任意字符串，前端因此使用了 6 个后端并不存在的名字
// （`overlay.write`/`media.verify`/`library.manage`/`bindings.resolve`/`jobs.cancel`/
// `jobs.retry`），使 Overlay 编辑、任务取消与重试、Library 创建、Source 登记、按需内容
// 确认和全部治理动作对任何主体都不渲染；而 mock 浏览器套件把同样的错误名字硬编码进
// 合成 bootstrap，因此测试自证通过。改为联合类型后，发明名字会直接是 TypeScript 错误。

export const CAPABILITIES = [
  'admin.backup',
  'admin.maintenance',
  'admin.restore',
  'audit.read',
  'bindings.read',
  'bindings.write',
  'clients.manage',
  'creators.write',
  'library.read',
  'library.write',
  'media.derive',
  'media.read',
  'overlays.write',
  'rules.debug',
  'rules.publish',
  'rules.read',
  'rules.write',
  'scan.run',
  'shares.create',
  'tokens.manage',
  'users.manage'
] as const;

export type Capability = (typeof CAPABILITIES)[number];

/**
 * 任务的取消与重试在服务端按 Job 类别派生所需 capability（Source 扫描类 → `scan.run`，
 * 派生资源 → `media.derive`，Overlay 重投影 → `overlays.write`），没有单一的
 * `jobs.cancel`/`jobs.retry`。前端只能据此判断"是否可能有权变更某类 Job"，最终仍由
 * 服务端裁决并返回结构化错误。
 */
export const JOB_MUTATION_CAPABILITIES: readonly Capability[] = [
  'scan.run',
  'media.derive',
  'overlays.write'
];
