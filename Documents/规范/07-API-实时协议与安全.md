# API、实时协议与安全

> 类型：规范。公共 API、WebSocket、账户、会话和授权以本文为唯一权威来源。

## API 原则

- `/api/v1` 的 REST/JSON + OpenAPI 是 Web、PWA、CLI、壳和未来客户端的共同契约；
- 资源 DTO 与内部数据库表、Go 结构和绝对路径解耦；
- 所有可变操作先做认证、effective capability、资源范围和并发版本检查；
- 所有长操作返回 `202` 和 job ID，不保持 HTTP 请求直到完成；
- 空结果、离线、不可见、未找到、冲突、校验失败和内部错误使用不同状态与 code；
- 后端只返回结构化 code 和字段，客户端负责本地化文案。

## 资源面

OpenAPI v1 至少覆盖：

| 资源组 | 能力 |
| --- | --- |
| bootstrap/session | 部署模式、当前主体、effective capabilities、CSRF、协议版本 |
| libraries/sources/providers | 查询范围、Source 配置和 Provider 注册表 |
| creators/works/media | CanonicalCreator/CanonicalWork/CanonicalMedia、Origin、有效字段和 publication 元数据 |
| search/query | 过滤、搜索、排序、快照游标 |
| rules | RulePackage/Draft/Version/ParameterSet/Binding 生命周期、JSON/YAML/TOML 导入导出、Schema/UI metadata、validate/compile、Dry Run、Trace、Explain、Impact、diff、rollback、内置示例测试和审计 |
| jobs | 创建、状态、issue、取消、重试和关联 publication |
| overlays | Override、标签操作、收藏、隐藏、进度、备注和封面选择 |
| shares | 最小 scope、过期、吊销和固定/跟随版本语义 |
| administration | 账户、token、session、grants、备份和诊断 |

最终细粒度路由在 API Schema 门禁中冻结，本文不规定具体控制器或表结构。

阶段 2 的规则 API 已形成以下稳定边界：规则编辑器从 `/api/v1/rules/schema` 读取同一 JSON Schema/UI metadata；`RuleDraft` 保存和验证使用 revision 或 `If-Match`，冲突返回 `RULE_DRAFT_CONFLICT`；只有 `rules.publish` 可发布、弃用或回滚，Viewer 不能改变当前 RuleVersion；`RuleVersion` 读取、diff、canonical JSON 导出和审计读取使用 `rules.read`。`/api/v1/rules/examples` 只列出仓库内嵌的三类合成样本，示例测试使用 `rules.debug` 且只执行受限 Dry Run。

规则 API 不接受服务器任意文件路径，不把 YAML/TOML 作为持久执行格式，也不返回数据库表结构。短小的 validate、compile、import、diff、Explain 和受限示例 Dry Run 可同步完成并受大小/成本上限约束；多 Source Impact、规则发布后的重扫、重投影和黄金测试批次必须复用持久 Job/Scheduler，并返回 job ID。

## 查询响应与实时附加状态

列表和搜索响应必须显式返回：

```text
query_publication_id
sort_protocol_version
```

服务端可以同时返回 `catalog_revision` 和 `overlay_projection_revision` 作为诊断元数据，但公共查询参数不得接受它们的任意组合。首次查询使用当前 active publication；后续页只从签名游标取得 `query_publication_id`。

OpenAPI 必须按**本次查询用途**声明一致性，不能给 Overlay 字段永久贴 snapshot/live 标签：

- **snapshot predicates**：本次进入过滤、搜索、排序、可见性或集合判断的 Overlay 值；在游标租约内固定；
- **live presentation**：同一或其他 Overlay 值若只用于当前响应展示，可以从 `control.db` 实时读取；不得由客户端用来重排或改写当前页集合。

Overlay 写响应至少返回 `overlay_fact_version`、`projection_status` 和需要时的 `projection_job_id`。control 事实同步提交后，直接 Overlay/实体读取必须读己之写；列表和搜索默认异步等待投影。客户端若提交 `after_overlay_fact_version` 屏障，服务端只能返回覆盖该水位的 `query_publication_id`，否则返回稳定的 pending/failed code。连续写可以合并，但状态必须区分 saved、pending、failed、superseded、published。

前端可以乐观更新编辑控件和实时展示值，但不能据此本地改变列表成员或排序。它必须保留旧 publication 的列表、展示投影中/失败状态，并在收到新 `query_publication_id` 后从第一页刷新依赖该字段的查询。

## 错误信封

错误响应至少包含稳定 `code`、可选 `field/path`、可重试性和 correlation ID。禁止把数据库错误文本、绝对路径、堆栈、token 或 metadata 私密值直接返回客户端。

```json
{
  "error": {
    "code": "CURSOR_EXPIRED",
    "retryable": true,
    "correlationId": "..."
  }
}
```

OpenAPI 必须枚举关键 code；客户端不得依赖中文 message 解析逻辑。

## WebSocket v1

`/ws/v1` 使用独立版本化事件信封，至少包含 protocol version、event type、sequence、resource scope、payload 和 server time。连接后服务端发送授权后的 snapshot/ready；断线重连后客户端必须以 sequence/`query_publication_id` 请求补齐或回退 HTTP snapshot，不能假设错过的事件会重播。

v1 事件至少覆盖 Job 状态/issue、Catalog publication、Overlay projection publication/失败、Session/Grant 失效和服务生命周期。publication 事件必须携带 `query_publication_id`；两类 revision 只能作为诊断字段。订阅和每个 payload 均按 effective capability 过滤；Session 吊销后服务端主动关闭连接，约定稳定关闭 code。WebSocket 不是事实存储，客户端状态恢复必须能通过 HTTP 完成。

当前防滥用基线为每个 Principal 最多 8 条并发连接、单条客户端入站消息最多 16 KiB、每连接每分钟最多 60 条客户端数据消息；超额握手返回 `RATE_LIMITED`/429，已建立连接的入站速率超限以 1008 关闭，消息大小超限由 WebSocket 1009 关闭。这些数值属于 `PRE_FREEZE`，待真实 LAN 多设备与浏览器标签页验证后冻结；ping/pong 控制帧不计入应用数据消息。

## capability 授权

授权原子是 capability，Owner/Operator/Viewer 只是预设包。示例：

```text
library.read[:libraryId]
media.read
media.download
media.derive
scan.run[:sourceId]
rules.read / rules.write / rules.publish / rules.debug
overlays.write
creators.write
bindings.read / bindings.write
shares.create
clients.manage
users.manage / tokens.manage / audit.read
logs.read
diagnostics.export
service.control
```

`available_capabilities` 是角色预设提供的 capability 上限；`effective_capabilities` 是 available 与部署模式、显式 allow/deny Grant、资源范围、Source 只读性、功能开关和 Session/API Token 状态的交集。显式 deny 优先于 allow；API Token 还要再次与创建时冻结的 capability/scope 取交集，不能扩大 Principal 权限。API 和 UI 只按 effective 判定。

每个公开服务方法必须在进入业务动作前检查 capability 和实际资源 scope，不能依赖路由隐藏或前端按钮。Library grant 可覆盖其 Source，Source grant 只覆盖单一 Source；Provider 名称不是授权边界。对象读取时，无权资源与不存在资源使用相同的 `NOT_FOUND` 外部语义，列表不得返回无权对象。

阶段 5 当前 API 的权威授权矩阵如下；表中“global”表示该对象本身跨 Source 或属于管理员面，资源限定 Grant 不能借此扩大到全局：

| 资源/动作 | capability | scope 与列表规则 |
| --- | --- | --- |
| User/Grant、安全审计、Session 管理 | `users.manage` / `audit.read` / `clients.manage` | global；Session/Token 普通列表只返回当前 Principal 自身对象 |
| Library 创建/读取，Source 创建/读取/状态 | `library.write` / `library.read` | 创建 Library 为 global；创建 Source 按目标 Library；读取按 Library 或 Source，无权对象返回 `NOT_FOUND` |
| RulePackage/Draft/Version/ParameterSet | `rules.read` / `rules.write` / `rules.publish` / `rules.debug` | global，因为规则包可跨 Source 复用且不是 Source 事实 |
| SourceRuleBinding 读取/修改/生效解析 | `rules.read` / `rules.write` | 按 Binding 实际 Source；创建按请求 Source |
| 扫描、按需内容确认 | `scan.run` | 按 Source |
| Job 读取/列表/取消/重试 | Source Job 用 `library.read`/`scan.run`；维护类用对应 `admin.*`；Derived 用 `media.read`/`media.derive` | 列表逐 Job 过滤；控制动作按 Job 类型和实际 Source 判定，`scan.run` 不能控制备份/恢复/维护 Job |
| Creator 读取 | `library.read` | global 主体可见全部；资源限定主体只返回至少有一个授权 Source Binding 的 Creator，详情剔除无权 Binding；合并历史与合并写为 global |
| Binding issue、孤立候选、结构决策、解绑 | `bindings.read` / `bindings.write` | 单对象按实际 Source；列表带 `sourceId` 时按 Source，省略时要求 global |
| Work 查询/详情 | `library.read` | 查询必须显式落在获授权 Library/Source，否则要求 global；详情按 Work 的实际 Source |
| Overlay 读取/写入 | `library.read` / `overlays.write` | 按 Work 的实际 Source |
| Media metadata/正文、DerivedAsset 正文 | `media.read` | 按 Media Source；DerivedAsset 先由稳定输入 Blob 反查当前已发布位置，global 或至少一个获授权 Source 才可建立读取租约 |
| DerivedAsset 创建 | `media.derive` | 按请求 publication 中 Media 的实际 Source |
| Share 创建/列出/吊销 | `shares.create` | 当前为 global 管理能力；创建时另检查目标 Library/Work/Media 的实际读权限，列表/吊销限创建 Principal |
| Backup/Restore/GC/VACUUM/Checkpoint | `admin.backup` / `admin.restore` / `admin.maintenance` | global |
| WebSocket | 订阅 capability + payload 对应 capability | 每个 payload 按 Library/Source 重新授权；无资源 scope 的全局事件只发给 global 主体 |

API Token 在上表判定之外还要同时通过 Token 创建时冻结的 capability 与 Library/Source scope；因此同一主体的 Token 只能缩权，不能扩权。

HEAD/GET、下载 disposition、内容确认和派生生成不得全部隐含落在同一个过宽 capability 上：读取媒体 metadata/正文（含已生成的 DerivedAsset 正文）使用 `media.read`；建立按需内容确认 Job 使用 `scan.run`（它本质是一个受限扫描 Job）；**创建**新的 DerivedAsset 生成工作使用独立的 `media.derive`，与只读的 `media.read` 分离——只读媒体账户可以读取已生成资源，但不能触发新的 CPU/磁盘生成工作。Personal owner 默认拥有三者；LAN Viewer 不含 `media.derive`，Operator/Owner 才含。仅以 `assetKey` 定位的 DerivedAsset 正文端点先从 DerivedAsset 注册事实取得稳定输入 Blob，再反查当前 publication 的 Source occurrence；资源限定主体至少对其中一个 Source 拥有 `media.read` 才能在授权后建立文件读取租约，猜中 `assetKey` 不能绕过授权。若 Blob 已无任何当前 occurrence，则只有 global `media.read` 可读该缓存资产。

## 媒体与 DerivedAsset 的查询快照绑定

媒体读取（列出作品媒体、媒体详情、媒体正文 HEAD/GET、按需内容确认 Job、DerivedAsset 创建）都支持通过可选 `queryPublicationId` 查询参数绑定到具体快照：

- 省略该参数是 **current 模式**：读当前 active publication，响应的 `queryPublicationId` 字段返回实际使用的快照 ID，不得与 snapshot 模式混淆；
- 显式提供是 **snapshot 模式**：只从该 publication 的 `(catalog_revision, overlay_projection_revision)` 组合解析，媒体必须属于该快照中对应的作品；该 publication 不存在或已被 GC 一律返回稳定 `CURSOR_EXPIRED`，不静默回退到 active；
- 服务端为显式快照读取的请求处理期间建立短期 publication 读取租约（复用既有 `query_publication_leases` 表与 GC 保护判据），防止解析、读取正文期间被 GC 回收；current 模式因为 active publication 永不被 GC 而不需要额外租约；
- DerivedAsset 创建的输入 ContentBlob 从请求指定（或省略时当前 active）的快照解析并在创建时冻结，异步生成过程中不得重新从"之后可能变化的 active publication"寻找输入，保证同一 Job 的重试和幂等语义引用的是同一个输入 Blob。

这解决了"客户端通过旧 cursor 拿到的 Work 引用了某个媒体，后台新 publication 发布后该媒体内容或存在性已变化"的快照一致性问题：媒体读取与 `/works` 列表查询遵循同一条"服务端签发/校验 `query_publication_id`，不允许客户端自行选择两类 revision 任意组合"的规则（见「查询响应与实时附加状态」）。

## Personal 模式

Personal 模式采用普通浏览器一次性配对：

```text
galleryd 启动
  ├─ 壳用一次性进程 bootstrap 建立 WebView Session
  └─ galleryctl/壳生成短时一次性配对 attempt
       └─ 浏览器同源 POST 交换 HttpOnly Session
```

- loopback 匿名访问不是管理员；
- 配对码短时、一次使用，只经 POST 交换，不长期出现在 URL、日志或 Referer；
- Host allowlist 只接受明确 loopback 名称/地址和实际端口，拒绝 DNS rebinding；
- Pairing 和 Session mutation 同时检查 Origin、Fetch Metadata 和 CSRF；
- Session、配对 attempt 和 WebSocket 可吊销，多标签页可共享浏览器 Session；
- Personal owner 映射为持久 `personal-owner` Principal 与 Owner 角色；阶段 5 安全迁移主动失效无法安全转换的旧 Session，不改变 Canonical、Binding、Overlay 或其它用户事实；
- descriptor 与恢复材料由当前 OS 用户权限和 CredentialStore 保护；
- 不声称防御同一 OS 用户下的恶意高权限进程。

## LAN 模式

- 绑定非 loopback 前必须完成 Owner 初始化；
- 本地 Owner 初始化由 `security_state` 单行 CAS 与数据库约束保证只能成功一次；初始化必须先以 LAN+loopback 运行，完成后才可重启并绑定私有 LAN 地址；
- 本地密码使用 PHC 格式 Argon2id，参数版本进入 hash 表达；当前 `m=19456 KiB,t=2,p=1` 属于 `PRE_FREEZE`，解析器对内存、迭代、并行度、盐和 key 长度设硬上限并保留登录时重哈希路径；
- 浏览器使用服务端存储的 HttpOnly Session Cookie；CLI/自动化使用可列出、可限定、可吊销的 API Token；
- Session secret、CSRF 验证值、API Token 和分享 credential 在 `control.db` 只保存 SHA-256 摘要；Token/分享 secret 仅在创建响应出现一次，列表只返回安全前缀与状态；
- Session Cookie 使用 `HttpOnly`、`SameSite=Strict`、HTTPS 时 `Secure`、全站 Path，以及绝对/空闲过期；当前绝对 30 天、空闲 24 小时均为 `PRE_FREEZE`；Cookie 状态修改另需严格同源 Origin/Fetch Metadata/CSRF，Bearer Token 不使用 Cookie CSRF；
- 登录失败、配对和敏感管理操作限速并审计；
- 用户禁用/删除、密码修改、Grant 变化会递增 Principal `security_version` 并失效全部 Session/API Token；既有 WebSocket 收到 `session.revoked` 或 `grant.revoked` 后以 4401/4403 关闭；
- CORS 的带凭据面默认严格同源，公开只读面即使开放也必须显式配置。

## Remote 模式

Remote/OIDC 延后。未来启用前必须同时具备 HTTPS、OIDC RP、受信代理 allowlist、严格 Cookie/CSP、外部安全测试和持续安全更新承诺。`X-Forwarded-*` 只在显式受信代理来源读取。反索引头或 loopback 检查不能被描述为公网访问控制。

## 分享

分享链接是最小匿名 capability，绑定 Library/CanonicalWork/CanonicalMedia scope、允许动作、过期和吊销状态。默认跟随逻辑 Library/CanonicalWork/CanonicalMedia 的当前 publication；匿名资源响应只返回公开 scope、权限、到期时间和相应 Library/Work/Media 安全 DTO，不返回创建者、secret 前缀、Source 路径或内部 row ID。Work 分享可列出其媒体，Library/Work/Media 分享只能通过专用公开 GET/HEAD 内容端点读取 scope 内媒体；`view` 允许内联读取，`download` 才允许 `download=true` 的 attachment，Range/ETag/If-Range 与认证媒体端点一致。越界对象、过期/吊销 credential 均隐藏为 `NOT_FOUND`。

“固定版本分享”只允许 Media scope，创建时必须精确匹配当前已确认 ContentBlob，并在 `control.db` 保存算法版本 + 完整 digest 稳定引用，不保存 Catalog 内部 Blob row ID；后续 publication 改变时仍按完整摘要从仍存在的 occurrence 读取。有效固定 Share 的 Blob 进入 Catalog GC 保护集合，过期或吊销后解除保护。公开 URL 中的 credential 必须在请求日志中以路由模板或 `<redacted>` 代替，不能写入日志。分享不能授予目录遍历、Source 配置或管理能力。

## 备份与恢复安全语义

账户、角色、Grant、分享定义与安全审计是 `control.db` 中不可重建事实，随一致性备份保留。备份 manifest v2 显式声明 Session/pairing/API Token/分享只含验证摘要；应用恢复后必须删除 Session/pairing、吊销全部 Token/分享、使非终态 Job 按既有恢复规则收敛，并写入脱敏的 `restore.finalize` 安全审计。启动期恢复标记、目录名和 manifest 内 `backupId` 都是不可信输入，必须先通过带类型 UUIDv7 校验并彼此完全一致，才可参与路径拼接。manifest v1 保持可读，未来版本必须显式拒绝，不能把未知字段静默解释为当前语义。

## 威胁边界

| 威胁 | 必需控制 |
| --- | --- |
| 未认证远程访问 | 默认 loopback；LAN 显式启用并认证；Remote 默认关闭 |
| Viewer 越权 | 应用层 capability + 资源范围；契约测试覆盖每个写操作 |
| DNS rebinding/跨站请求 | Host/Origin/Fetch Metadata/CSRF/Cookie 联合校验 |
| Session/token 泄漏 | 最小 scope、短期/轮换、服务端吊销、日志脱敏 |
| 恶意 metadata | 限大小/深度；规则无任意执行；结构化错误 |
| 恶意媒体 | 主进程不解析不可信容器；受限外部工具；超时和资源上限 |
| 路径穿越/链接逃逸 | 唯一安全解析入口、根内复核、句柄式打开门禁 |
| WS 滥用 | 握手认证、订阅授权、速率/连接数限制、每帧 Session 状态 |
| SSRF | 后端不按任意用户 URL 发请求；未来外呼仅允许版本化 Provider/插件策略 |
| 同 OS 用户高权限进程 | 明确在 Personal 威胁边界外；需要系统级隔离而非 HTTP 伪防护 |

安全门禁见 [测试与发布门禁](../指南/02-测试与发布门禁.md)。对应决策见 [ADR-006](../ADR/ADR-006-API与安全.md)。
