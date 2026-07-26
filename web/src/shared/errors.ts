/*
 * 服务端稳定 fault code → 中文文案。
 *
 * 事实源是 `internal/contract/fault/fault.go`（84 个 code）、SourceWork 结构决策的两个
 * review code，以及 `internal/webapp/handler.go` 在 Web 资产层单独返回的三个 WEB_* code。
 *
 * **code 一律按 string 处理，不使用 OpenAPI 生成的联合类型。** OpenAPI 的 ErrorCode enum
 * 少于 Go 端实际可达的集合（例如 WEB_* 三个 code 根本不在 OpenAPI 里，因为它们由静态资产
 * 处理器而不是 API 层返回），用联合类型会让运行时真实出现的 code 变成「类型上不可能」，
 * 于是兜底分支被当成死代码删掉，用户看到的就是空白。
 */

import { GalleryError } from '../api/client';

/**
 * 全部已知 code 的中文文案。
 *
 * 写法约定：说清「发生了什么」和「用户能做什么」，不暴露内部原因、路径或 metadata 原文。
 */
const COPY: Record<string, string> = {
  /* —— 通用 —— */
  INTERNAL_ERROR: 'Gallery 发生内部错误。请记录关联 ID 后重试，或查看服务端诊断日志。',
  VALIDATION_ERROR: '输入不符合服务端契约，请检查标出的字段后重新提交。',
  CONFIG_INVALID: '配置不合法，Gallery 已拒绝加载它以免产生不可预期的行为。',
  UNAUTHENTICATED: '会话已过期或被吊销，请重新认证。',
  FORBIDDEN: '当前账户没有执行此操作的权限。',
  NOT_FOUND: '资源不存在，或当前账户没有查看它的权限。',
  CONFLICT: '资源已被其他操作改变，请刷新后基于最新状态重试。',

  /* —— 目录与存储 —— */
  APPDIRS_SOURCE_OVERLAP: '应用数据目录与 Source 根目录重叠。Source 必须永久只读，请改用独立目录。',
  SOURCE_ROOTS_OVERLAP: '多个 Source 根目录互相包含，同一份媒体会被重复登记，请调整为互不重叠的目录。',
  DATABASE_OPEN_FAILED: '无法打开本地数据库。请确认应用数据目录可写、未被其他进程独占。',
  MIGRATION_FAILED: '数据库迁移失败，Gallery 已停止以免留下半迁移状态。请从备份恢复后重试。',
  BACKUP_FAILED: '备份未完成，原有数据未被修改。请检查磁盘空间与目标目录权限。',
  RESTORE_FAILED: '恢复未完成。请确认备份文件完整，且没有其他进程正在使用数据库。',
  BACKUP_NOT_FOUND: '指定的备份不存在。',
  BACKUP_CORRUPT: '备份文件校验失败，无法用于恢复。',
  BACKUP_INCOMPATIBLE: '备份来自不兼容的数据版本，当前 Gallery 无法恢复它。',
  DISK_SPACE_INSUFFICIENT: '可用磁盘空间不足，操作已在写入前中止。',
  LOCK_UNAVAILABLE: '无法取得应用数据目录的独占锁。',
  INSTANCE_ALREADY_RUNNING: '已有另一个 Gallery 实例在使用同一份应用数据目录。',
  MAINTENANCE_BLOCKED: '维护任务当前不能执行：仍有正在进行的读取或任务占用目标资源。',
  PROCESS_INTERRUPTED: '操作被进程退出打断，已按可恢复状态保存进度。',

  /* —— 查询与游标 —— */
  CURSOR_INVALID: '分页游标无法解析，请从第一页重新开始。',
  CURSOR_EXPIRED: '查询快照已过期，请从第一页重新开始。列表不会跨快照拼接。',
  QUERY_TOO_SHORT: '搜索词太短，请增加关键词或改用结构化过滤。',
  CATALOG_PUBLICATION_MISSING: 'Catalog 尚无可用快照。请先完成一次扫描与发布。',
  CATALOG_CANDIDATE_INVALID: '候选快照不完整，已拒绝发布。',
  CATALOG_CANDIDATE_ALREADY_PUBLISHED: '该候选快照已经发布过，不能重复发布。',

  /* —— Overlay 与绑定 —— */
  OVERLAY_FACT_INVALID: '用户事实不合法，已拒绝写入以免污染不可重建的数据。',
  OVERLAY_PROJECTION_FAILED: 'Overlay 投影失败，页面显示的仍是上一次成功的快照。',
  BINDING_REVIEW_REQUIRED: '该绑定需要人工确认后才能生效。',
  SOURCE_WORK_SPLIT_REVIEW_REQUIRED: '检测到作品拆分，需要先确认拆分结果才能继续。',
  SOURCE_WORK_MERGE_REVIEW_REQUIRED: '检测到作品合并，需要先确认合并结果才能继续。',

  /* —— 派生资源与外部工具 —— */
  DERIVED_ASSET_INVALID: '派生资源校验失败，已按缺失处理。',
  DERIVED_ASSET_UNAVAILABLE: '缩略图或预览尚未生成。可以先查看原始媒体，或创建派生任务。',
  DERIVED_ASSET_FAILED: '派生资源生成失败。原始媒体未被修改。',
  EXTERNAL_TOOL_UNAVAILABLE: '未找到所需的外部工具，或其版本不在允许列表内。',
  EXTERNAL_TOOL_FAILED: '外部工具执行失败，已按超时或资源上限中止。',

  /* —— 规则 —— */
  RULE_SCHEMA_INVALID: '规则包不符合 Schema，请修正后重新校验。',
  RULE_COMPILE_ERROR: '规则编译失败，请检查表达式与原语参数。',
  RULE_CEL_LIMIT: '表达式超出 Gallery CEL Profile 的限制，请简化条件。',
  RULE_DRY_RUN_FAILED: '试运行失败，规则未被发布。',
  RULE_IMPACT_FAILED: '影响评估失败，无法判断本次修改会波及哪些作品。',
  RULE_EVAL_ERROR: '规则求值失败，本次扫描的对应部分未产生结果。',
  RULE_PARAMETER_INVALID: '规则参数不合法。',
  RULE_PARAMETER_CONFLICT: '规则参数与已有绑定冲突。',
  RULE_DRAFT_CONFLICT: '草稿已被其他会话修改，请刷新后基于最新 revision 重试。',
  RULE_PACKAGE_CONFLICT: '规则包标识冲突：同一标识已存在不同内容的版本。',
  RULE_PUBLISH_BLOCKED: '发布被阻止：仍有未通过的校验或未确认的影响。',
  RULE_ROLLBACK_BLOCKED: '回滚被阻止：目标版本仍被使用或不满足回滚前置条件。',
  RULE_VERSION_IN_USE: '该规则版本仍被 Source 绑定使用，不能删除。',
  RULE_IMPORT_INVALID: '导入内容无法转换为规范 JSON 规则包。',
  RULE_BINDING_CONFLICT: '同一 Source 的同一优先级已存在生效绑定，请调整 priority。',

  /* —— 扫描与任务 —— */
  SCAN_ALREADY_RUNNING: '该 Source 已有扫描在进行，请等待它结束。',
  JOB_STATE_CONFLICT: '任务当前状态不允许此操作，请刷新任务快照。',
  JOB_PROGRESS_REGRESSION: '任务进度出现回退，已按异常处理。',
  JOB_RETRY_EXHAUSTED: '任务重试次数已用尽。请先排除根因再手动重建任务。',
  JOB_CANCELLATION_REQUESTED: '取消已请求，任务会在下一个安全点停止。',
  WATCHER_OVERFLOW: '文件变更事件过多，已改用周期性收敛。结果仍然完整，只是不再实时。',

  /* —— 媒体与内容身份 —— */
  CONTENT_HASH_PENDING: '内容哈希尚未完成，请等待确认任务结束。',
  CONTENT_NOT_VERIFIED: '媒体尚未完成内容确认，可以先创建按需确认任务。',
  CONTENT_CHANGED: '媒体在读取过程中发生变化，本次读取已中止以免返回拼接内容。',
  CONTENT_CHANGED_DURING_HASH: '哈希过程中文件发生变化，本次确认作废，需要重新计算。',
  CONTENT_DISAPPEARED: '媒体在读取前已消失或被移动。',
  MEDIA_OFFLINE: '媒体所在位置当前离线，请在 Source 恢复后重试。',
  MEDIA_READ_BUSY: '媒体读取通道已满，请稍后重试。这是保护 Source 的并发上限，不是错误。',
  RANGE_INVALID: '请求的字节范围不合法。',
  VERIFICATION_TARGET_MISMATCH: '确认目标与当前记录不一致，可能已被重新扫描替换。',
  PATH_ESCAPE: '路径逃逸出允许的根目录，请求已被拒绝。',

  /* —— Source —— */
  SOURCE_PATH_INVALID: 'Source 路径不合法或不可访问。',
  SOURCE_UNAVAILABLE: 'Source 当前离线。Catalog 中已发布的资料仍可浏览。',
  SOURCE_PERMISSION_DENIED: '没有读取该 Source 的系统权限。Gallery 永远不会尝试写入 Source。',
  SOURCE_READ_FAILED: '读取 Source 失败。',
  SOURCE_IDENTITY_CHANGED: 'Source 身份发生变化（可能被替换或重新挂载），需要确认后才能继续。',

  /* —— 认证、CSRF 与限流 —— */
  HOST_REJECTED: '请求的 Host 不在允许列表内，已拒绝。',
  ORIGIN_REJECTED: '请求来源不被允许，已拒绝。请从 Gallery 自身页面发起操作。',
  CSRF_INVALID: 'CSRF 令牌无效或已随认证状态变化失效，请刷新页面后重试。',
  PAIRING_INVALID: '配对凭据无效。',
  PAIRING_EXPIRED: '配对凭据已过期，请重新发起配对。',
  INVALID_CREDENTIALS: '用户名或密码不正确。',
  RATE_LIMITED: '请求过于频繁，请等待服务端限流窗口结束后重试。',
  LAN_OWNER_REQUIRED: 'LAN Owner 尚未初始化，请先完成 Owner 初始化。',
  LAN_ALREADY_INITIALIZED: 'LAN Owner 已初始化，请直接登录。',
  USER_DISABLED: '该账户已被停用。',
  TOKEN_INVALID: 'API Token 无效。',
  TOKEN_EXPIRED: 'API Token 已过期。',

  /* —— Web 静态资产（由 internal/webapp 返回，不在 OpenAPI 的 ErrorCode 里） —— */
  WEB_ASSETS_UNAVAILABLE: 'Web 前端资源缺失，服务端没有可用的界面产物。',
  WEB_ASSETS_INVALID: 'Web 前端资源清单无法解析。',
  WEB_VERSION_MISMATCH: 'Web 前端与服务端契约版本不一致，请重新构建或升级后再访问。'
};

/** 网络层失败（fetch 抛 TypeError）的文案。它没有服务端 code。 */
const OFFLINE_COPY = '无法连接 Gallery。请确认服务仍在运行，然后重试。';

/**
 * 把稳定 code 翻成中文。
 *
 * 未知 code 一律走兜底，并把原始 code 原样带出——服务端可能比当前前端更新，
 * 隐藏 code 会让用户和日志失去唯一能对上的线索。
 */
export function errorCopy(code: string): string {
  return COPY[code] ?? `请求失败（${code}）。当前前端未收录该失败码，请记录它并升级前端或查看服务端诊断。`;
}

/** 该 code 是否已有专门文案。测试与诊断用，不用于业务分支。 */
export function hasErrorCopy(code: string): boolean {
  return Object.hasOwn(COPY, code);
}

/** 从任意异常取出服务端稳定 code。非 GalleryError 返回 undefined。 */
export function errorCode(error: unknown): string | undefined {
  return error instanceof GalleryError ? error.code : undefined;
}

/** 从任意异常取出关联 ID，供用户报告问题时引用。 */
export function errorCorrelationId(error: unknown): string | undefined {
  return error instanceof GalleryError ? error.correlationId : undefined;
}

/** 服务端是否声明该失败可重试。未知情况按不可重试处理，避免无意义的重试风暴。 */
export function isRetryable(error: unknown): boolean {
  if (error instanceof GalleryError) return error.retryable;
  // fetch 层失败（断网、服务未启动）值得重试。
  return error instanceof TypeError;
}

/** 把任意异常翻成可直接展示的中文说明。UI 只应该调用这个函数。 */
export function describeError(error: unknown): string {
  if (error instanceof GalleryError) return errorCopy(error.code);
  if (error instanceof TypeError) return OFFLINE_COPY;
  if (error instanceof Error && error.message.length > 0) return `发生未预期的客户端错误：${error.message}`;
  return '发生未预期的客户端错误。';
}
