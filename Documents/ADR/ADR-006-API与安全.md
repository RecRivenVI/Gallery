# ADR-006 API 与安全

- 状态：接受
- 日期：2026-07-16
- 规范：[API、实时协议与安全](../规范/07-API-实时协议与安全.md)

## 问题

Gallery 需要让 Web、CLI、桌面壳和未来客户端共享同一能力，同时覆盖 Personal 默认使用、LAN 账户、长任务、实时状态和即时吊销。依赖 loopback、角色名或壳内权限会让普通浏览器和多客户端出现不同安全边界。

## 决策

公共 v1 契约使用 REST/JSON + OpenAPI；长任务返回持久 job ID；双向实时状态使用独立版本化 WebSocket 信封，HTTP snapshot 是恢复事实源。

授权以 capability 为原子，角色只是预设包；最终判定使用 `effective = available ∩ deployment ∩ resource scope ∩ session state`。

Personal 模式中，普通浏览器通过短时一次性配对建立 HttpOnly 管理 Session，loopback 匿名不是管理员；壳使用一次性进程 bootstrap。LAN 模式使用本地账户、Argon2id、服务端 Session、CSRF、API Token 和 Library grant。Remote/OIDC 延后且默认不可启用。

阶段 4 Correctness 收尾（2026-07-20）新增独立 `media.derive` capability，把"创建 DerivedAsset 生成 Job"从只读的 `media.read` 中拆分出来：读取媒体/已生成 DerivedAsset 正文仍用 `media.read`，只有触发新的 CPU/磁盘生成工作才需要 `media.derive`。Personal 模式当前只有单一 owner 角色，默认同时拥有两者；这条边界是为阶段 5 多账户预留的授权演进点，本轮不引入账户系统本身。

阶段 5 首轮实现（2026-07-23）把上述方向落实为以下兼容演进基线：Personal owner 与 LAN local user 都映射到持久 Principal；Owner/Operator/Viewer 只定义 capability 上限，显式 allow/deny Grant 决定 global/Library/Source 资源范围且 deny 优先；Session 与 API Token 每次认证都核对 Principal 状态和 `security_version`，Token 再与创建时 capability/scope 取交集。Session、Token、CSRF 与分享 credential 只保存摘要，Token/分享 secret 只显示一次。用户状态、密码或 Grant 变化使既有凭据与 WS 立即失效。恢复保留账户/角色/Grant/审计，但删除 Session/pairing 并吊销 Token/分享。

这些结构方向登记为 `COMPATIBILITY_BASELINE`；Argon2id 成本（当前 `m=19456,t=2,p=1`）、Session 绝对/空闲时长（当前 30 天/24 小时）、登录限流阈值，以及 WS 每 Principal 8 连接/单消息 16 KiB/每分钟 60 条入站数据消息仍为 `PRE_FREEZE`，待目标设备与真实 LAN 多客户端证据后裁决。没有将实现参数误写为永久冻结。2026-07-23 的阶段 5 第二轮收尾已补齐匿名 Share 的 Work/Media/正文消费、固定 Blob GC 保护、全 API 资源授权矩阵与路径/metadata/媒体正文/恢复输入合成攻击门禁；但真实 LAN 多设备/浏览器和目标设备 Argon2id 延迟/并发证据仍未完成，因此阶段 5 Security Gate 仍未通过，本次演进不把阶段 5 标记为正式完成。

## 理由

- REST/OpenAPI 对资源、媒体、长任务和多语言客户端契约清晰；
- WebSocket 可处理任务订阅和 Session 吊销，HTTP 可在断线后恢复；
- capability 避免把 UI 角色名变成后端安全逻辑，也支持 Library/Source 级范围；
- 一次性配对让无壳普通浏览器具备完整 Personal 管理能力，同时避免 localhost 匿名提权；
- Session 服务端存储可即时吊销，比不可撤销 JWT 更适合单进程；
- 配对、CSRF、capability 和 WS 吊销原型已通过，见 [EV-08](../证据/验证记录.md#ev-08-跨库游标和-personal-契约e1)。

## 替代方案

- **loopback 匿名管理员**：任何本机网页或进程可借浏览器访问，且无法形成明确 Session。
- **只允许桌面壳管理**：破坏普通浏览器作为完整客户端的目标。
- **JWT Session**：即时吊销和多设备管理复杂，没有无状态扩展收益。
- **在数据库保存可回显 Token/CSRF/分享 secret**：扩大备份、日志和离线泄漏影响面；当前只保存不可逆验证摘要。
- **角色直接等于授权**：无法表达 Library/Source 隔离和显式 deny；角色只保留为 capability 预设上限。
- **角色名硬编码授权**：难以表达资源范围和功能开关，客户端容易漂移。
- **GraphQL 作为唯一 API**：资源/媒体/任务语义没有足够收益，缓存和多语言工具更复杂。
- **只用 SSE/轮询**：Session 主动吊销和双向订阅控制不足；可作为恢复/降级而非唯一实时协议。

## 影响

- 每个应用服务必须显式声明 capability 和资源范围；
- UI 只消费 effective capabilities 和结构化 code；
- Session、Token、Pairing、WS 和分享都需要可列出、过期、审计和吊销；
- localhost Web 面必须同时防 Host、Origin、Fetch Metadata 和 CSRF；
- 壳、CLI 和浏览器不能拥有不同的业务授权捷径；
- Remote 发布必须新建 ADR，不能用反向代理说明替代安全设计。

## 重新审议条件

当产品进入多租户、联邦身份或多节点部署时，可扩展 Principal/Grant 和引入 OIDC，但 capability 仍保留为授权原子。若所有双向实时用例消失且吊销可完全由其他机制满足，可评估 SSE；否则保留 WebSocket。
