/*
 * 管理端的契约事实与已知缺口登记表。
 *
 * 为什么单独一份文件：管理端最容易犯的错误是「界面假装服务端有某种能力」——假装能翻页、
 * 假装能批量处理、假装能改配置、假装能下载备份。这些缺口是**产品事实**，必须出现在界面上，
 * 而不是散落在各页面的注释里。集中登记后，页面只负责按 area 渲染，测试也能直接断言某条
 * 事实确实呈现给了用户。
 *
 * 每条记录都对应一个已核实的服务端行为或缺失，不是猜测。新增条目前先确认 OpenAPI 与
 * `internal/transport/httpapi` 的实际实现。
 */

export type ContractArea =
  | 'authorization'
  | 'realtime'
  | 'jobs'
  | 'scan'
  | 'resources'
  | 'rules'
  | 'governance'
  | 'maintenance'
  | 'security'
  | 'diagnostics';

export interface ContractNote {
  id: string;
  area: ContractArea;
  /** 一句话结论，直接可读。 */
  title: string;
  /** 说明为什么会这样，以及用户应当如何理解当前界面。 */
  detail: string;
  /** true 表示这是「服务端没有此能力」而不是「此处暂未实现」。 */
  gap: boolean;
}

export const CONTRACT_NOTES: readonly ContractNote[] = [
  {
    id: 'authorization-global-only',
    area: 'authorization',
    title: 'capability 只反映 global scope，不能当作真实授权判断',
    detail:
      'bootstrap 的 effectiveCapabilities 只有 global scope，不包含按 Library/Source 的授权；服务端还会把部分 FORBIDDEN 伪装成 404 以免泄露资源存在性。因此本界面只用 capability 隐藏明显不可用的入口，按钮可见不代表一定有权执行，最终结论以每次操作返回的结构化错误为准。',
    gap: false
  },
  {
    id: 'realtime-hint-only',
    area: 'realtime',
    title: '实时事件只是提示，HTTP 快照才是权威',
    detail:
      '14 种事件里当前只有 8 种真有发送方；WebSocket 断开只影响「多快知道变化」，不影响已加载数据的有效性。每次连接、重连或序号缺口都会重新拉取 HTTP 快照。每个主体最多 8 个并发连接。',
    gap: false
  },
  {
    id: 'jobs-no-cursor',
    area: 'jobs',
    title: '任务列表只有 limit，没有游标',
    detail:
      'GET /api/v1/jobs 只接受 status 与 limit，契约里没有 cursor，因此更早的任务历史无法在此翻页。这是契约限制，界面不会伪造分页控件。需要更长历史时只能提高 limit。',
    gap: true
  },
  {
    id: 'jobs-derived-authorization',
    area: 'jobs',
    title: '任务的授权是派生的，没有 jobs.cancel / jobs.retry',
    detail:
      '绑定 Source 的任务读用 library.read、写用 scan.run；control_backup 用 admin.backup、control_restore 用 admin.restore；catalog_gc/checkpoint/vacuum/derived_gc 用 admin.maintenance；derived 读用 media.read、写用 media.derive；overlay_projection 读用 library.read、写用 overlays.write。词表里不存在任务专用的取消或重试 capability。',
    gap: false
  },
  {
    id: 'jobs-retry-same-id',
    area: 'jobs',
    title: '重试在同一个任务 ID 上开新 Attempt',
    detail:
      '重试不会产生新的任务 ID，而是在原任务上追加一次 Attempt。任务当前状态不允许取消或重试时返回 409 JOB_STATE_CONFLICT，此时应重新拉取任务快照而不是反复点击。',
    gap: false
  },
  {
    id: 'scan-index-first-only',
    area: 'scan',
    title: 'index 档案只对首次扫描有效',
    detail:
      'Source 已发布或已有持久历史后，显式请求 index 会被服务端稳定拒绝为 409 CONFLICT。界面会如实报告这次拒绝，不会偷偷改成 incremental 再提交——那会让你以为快速索引成功了，而实际上执行的是另一种档案。',
    gap: false
  },
  {
    id: 'resources-create-only',
    area: 'resources',
    title: 'Library 与 Source 只能创建，不能改名、删除或停用',
    detail:
      '契约只有 GET/POST /api/v1/libraries 与 /api/v1/sources，没有 PATCH、DELETE 或停用端点。登记前请确认名称与根路径，本界面无法在事后修正它们。',
    gap: true
  },
  {
    id: 'resources-no-settings',
    area: 'resources',
    title: '没有任何设置或配置读写接口',
    detail:
      '服务端当前不提供配置读取或写入端点，因此管理端没有「设置」页面。监听地址、限流、保留期等运行参数只能在服务端配置文件中修改并重启，界面不会为它们造一个假的表单。',
    gap: true
  },
  {
    id: 'rules-analysis-needs-write',
    area: 'rules',
    title: '只读分析也要求写权限',
    detail:
      'validate、compile、impact 三个只读分析端点要求 rules.write；dry-run、explain、trace 要求 rules.debug。这是已知的过度限制，界面如实呈现，不做本地放宽。',
    gap: false
  },
  {
    id: 'rules-draft-if-match',
    area: 'rules',
    title: '草稿保存必须带 If-Match 修订号',
    detail:
      '保存草稿时携带 GET 草稿返回的 revision 作为 If-Match；其他会话已经改过草稿时返回 409 RULE_DRAFT_CONFLICT。此时必须重新载入草稿再基于最新内容编辑，界面不会覆盖别人的修改。',
    gap: false
  },
  {
    id: 'rules-schema-editor-elsewhere',
    area: 'rules',
    title: '配置编辑器由另一条工作线实现',
    detail:
      'GET /api/v1/rules/schema 返回 application/schema+json，是 Schema 驱动配置编辑器的输入。本界面只保留入口并核对该端点可达，不在这里生成表单。',
    gap: false
  },
  {
    id: 'governance-no-bulk',
    area: 'governance',
    title: '治理动作全部是单条操作，没有批量接口',
    detail:
      'resolve、dismiss、reopen、resolve-structure、undo、unbind、orphan decide 都只接受一个对象。几百条待处理就是几百次独立往返，界面不会用一个「全选」按钮掩盖这件事。',
    gap: true
  },
  {
    id: 'governance-keyset-cursor',
    area: 'governance',
    title: '治理列表用 keyset 游标，与作品查询的签名游标不是一回事',
    detail:
      'binding-issues 与 orphan-candidates 使用简单 keyset 游标：没有快照租约，也没有 CURSOR_EXPIRED 契约。逐页向后追加即可，但不要把它当作可以随意跳页的偏移量。',
    gap: false
  },
  {
    id: 'maintenance-backup-manifest-only',
    area: 'maintenance',
    title: '备份只返回清单，既不能下载也不能上传',
    detail:
      'GET /api/v1/admin/control-backups 返回 manifest（大小、校验和、Schema 版本），不返回字节。契约里没有下载端点，也没有上传端点：备份文件只存在于服务端的应用数据目录中。',
    gap: true
  },
  {
    id: 'maintenance-restore-next-start',
    area: 'maintenance',
    title: '恢复只是登记，下次启动才生效，且没有任何事件通知重启',
    detail:
      'POST /api/v1/admin/control-restores 只登记一次待恢复请求，真正的恢复在 galleryd 下次启动时执行。service.lifecycle 事件当前没有发送方，界面无法告诉你服务是否已经重启，请自行重启并复核。',
    gap: true
  },
  {
    id: 'security-secret-once',
    area: 'security',
    title: 'API Token 与分享的密文只返回一次',
    detail:
      '创建响应里的 secret 只出现这一次，服务端只保留前缀。关闭对话框后本界面会立即丢弃它，没有任何再次查看的入口——没有记录下来就只能吊销后重建。',
    gap: false
  },
  {
    id: 'security-no-role-management',
    area: 'security',
    title: '没有角色管理接口，也没有管理员重置密码',
    detail:
      '角色只能在创建用户时指定，之后只能通过 allow/deny 授权（grant）调整实际权限。契约里没有修改角色的端点，也没有管理员替他人重置密码的端点；用户只能自己改密码。',
    gap: true
  },
  {
    id: 'security-audit-no-filter',
    area: 'security',
    title: '安全审计没有任何查询参数',
    detail:
      'GET /api/v1/admin/security-audits 不接受时间、主体、动作或分页参数，返回的就是服务端决定的那一段。界面不提供本地过滤框，以免让人以为筛掉的部分不存在。',
    gap: true
  },
  {
    id: 'diagnostics-no-logs',
    area: 'diagnostics',
    title: '没有日志、指标或诊断接口',
    detail:
      '服务端不提供日志读取、指标导出或诊断查询端点。每个响应的 correlationId 是随机生成的，只能用于人工报告，无法在此按它检索任何东西。',
    gap: true
  },
  {
    id: 'diagnostics-health-minimal',
    area: 'diagnostics',
    title: '健康检查只返回两个数据库的 ok / 非 ok',
    detail:
      'GET /api/v1/health 只声明 control.db 与 catalog.db 是否可用，不包含磁盘、队列、Watcher 或外部工具的状态。它返回 ok 不代表扫描、派生或维护一定可用。',
    gap: true
  }
];

export function contractNotes(area: ContractArea): readonly ContractNote[] {
  return CONTRACT_NOTES.filter((note) => note.area === area);
}
