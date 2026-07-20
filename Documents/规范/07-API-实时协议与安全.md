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
logs.read
diagnostics.export
service.control
```

`available_capabilities` 是角色/Grant 能提供的上限；`effective_capabilities` 是 available 与部署模式、资源范围、Source 只读性、功能开关和 Session 状态的交集。API 和 UI 只按 effective 判定。

每个服务方法必须在应用层检查 capability，不能依赖路由隐藏或前端按钮。Library/Source 级 grant 是授权边界，Provider 名称不是授权边界。

HEAD/GET、下载 disposition、内容确认和派生生成不得全部隐含落在同一个过宽 capability 上：读取媒体 metadata/正文（含已生成的 DerivedAsset 正文）使用 `media.read`；建立按需内容确认 Job 使用 `scan.run`（它本质是一个受限扫描 Job）；**创建**新的 DerivedAsset 生成工作使用独立的 `media.derive`，与只读的 `media.read` 分离——只读媒体账户可以读取已生成资源，但不能触发新的 CPU/磁盘生成工作。Personal 模式当前只有单一 owner 角色，默认同时拥有全部以上 capability；阶段 5 引入多账户后才需要真正区分。

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
- descriptor 与恢复材料由当前 OS 用户权限和 CredentialStore 保护；
- 不声称防御同一 OS 用户下的恶意高权限进程。

## LAN 模式

- 绑定非 loopback 前必须完成 Owner 初始化；
- 本地密码使用 Argon2id，参数由目标设备基准确定并版本化；
- 浏览器使用服务端存储的 HttpOnly Session Cookie；CLI/自动化使用可列出、可限定、可吊销的 API Token；
- Session Cookie 使用 SameSite、Secure（适用时）和绝对/空闲过期；状态修改另需 CSRF；
- 登录失败、配对和敏感管理操作限速并审计；
- CORS 的带凭据面默认严格同源，公开只读面即使开放也必须显式配置。

## Remote 模式

Remote/OIDC 延后。未来启用前必须同时具备 HTTPS、OIDC RP、受信代理 allowlist、严格 Cookie/CSP、外部安全测试和持续安全更新承诺。`X-Forwarded-*` 只在显式受信代理来源读取。反索引头或 loopback 检查不能被描述为公网访问控制。

## 分享

分享链接是最小匿名 capability，必须绑定 Library/CanonicalWork/CanonicalMedia scope、允许动作、过期和吊销状态。默认跟随逻辑 CanonicalWork/CanonicalMedia 的当前授权内容；“固定版本分享”才锁定 ContentBlob，并在 `control.db` 保存算法版本 + 完整 digest 等稳定引用，不保存 Catalog 内部 Blob row ID。分享不能授予目录遍历、Source 配置或管理能力。

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
