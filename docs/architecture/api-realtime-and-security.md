# API、实时协议与安全

## HTTP 契约

唯一 OpenAPI 源是 `internal/contract/api/openapi.yaml`，当前标识为 `0.6.0-pre-alpha`。静态调查确认它定义 121 个 HTTP 操作，服务端注册同一组 method/path；另外注册 `/ws/v1` 和内嵌 Web 根处理器。

OpenAPI 生成公开 Go package `api` 和 Web TypeScript schema。资源面包括健康与 bootstrap、认证、账户与授权、Library/Source、规则、扫描与 Job、维护与备份、作品/媒体/Creator、Binding 治理和分享。字段、状态码和 DTO 只在契约源中维护。

## 请求与错误

API 使用 `/api/v1`。受保护写请求需要认证、Origin/Host 检查和 CSRF；API Token 使用 Authorization 头并受 token capability/scope 上限约束。

错误响应包含稳定 `code`、`retryable`、可选 `field` 与 correlation ID。内部 cause、绝对路径和 secret 不进入响应。`NOT_FOUND` 可能同时表达不存在或无权查看，避免身份探测。

API 响应默认 `Cache-Control: no-store` 并按 Cookie/Authorization 变化；媒体内容使用自己的条件缓存语义。请求日志记录匹配 route 模式，公开分享 credential 会被路径脱敏。

## Personal

Personal 只允许 loopback。匿名 bootstrap 返回部署与 CSRF 信息，但不自动获得 Owner 权限。配对流程是：创建 5 分钟单次凭据 → 原子消费 → 建立 HttpOnly Session。Session 有绝对和 idle 生命周期，当前数值属于 PRE_FREEZE 安全参数。

## LAN

LAN 只允许 loopback 或私有地址。第一次初始化 Owner 必须从本机完成；未初始化时服务不会绑定非 loopback 地址。随后使用本地账户与 Argon2id 口令登录，可创建用户、角色预设、显式 allow/deny Grant、Token 和分享。

Remote/OIDC、受信反向代理和公网部署未实现。

## Capability

后端 capability 词表由 `internal/auth.PersonalOwnerCapabilities` 和 control migration 共同维护，Web 有编译期副本并由 Go 测试逐项比对。当前词表覆盖备份、维护、恢复、审计、Binding、客户端、Creator、文件浏览、Library、媒体读取/派生、Overlay、规则、扫描、分享、Token 和用户管理。

角色只给出 capability 上限；Session、Token scope 和资源 Grant 共同计算 effective capability，deny 优先。授权 scope 可以是 global、Library 或 Source。列表、聚合封面、Job 和 WebSocket 广播都在响应前重新裁剪。

## Session、Token 与 Share

- Session secret 只存验证摘要，Cookie 为 HttpOnly；吊销会关闭对应 WebSocket。
- API Token 的 secret 只在创建响应出现一次，保存摘要、capability 与 scope；账户安全版本变化会失效旧凭据。
- Share credential 只在创建时出现一次，可限定 Work、Media 或 Library、过期时间与权限；解析和媒体读取使用公开但有界的匿名路由。
- 恢复 control 后，Session、配对、Token 和 Share 都失效，账户、Grant 与审计保留。

## WebSocket v1

`/ws/v1` 在 HTTP 认证成功后升级。每条 envelope 包含：

- `protocolVersion=1`；
- `eventType`；
- 连接内单调 `sequence`；
- Library/Source/Job scope；
- JSON payload；
- `serverTime`。

事件覆盖 ready、Job 状态、Catalog/Overlay publication、Session/Grant 吊销和服务生命周期。每 principal 连接数、输入帧大小和频率有上限。

WebSocket 不是状态数据库：`connection.ready` 要求客户端读取 HTTP snapshot；sequence gap、断线或重连也必须重新读取。事件只提示变化，不能按事件增量拼出权威列表。

## Web 安全

内嵌 Web 只提供 GET/HEAD 静态资源，`/api` 与 `/ws` 不进入 SPA fallback。CSP 禁止外部脚本、对象和 frame，只放行同源连接与一个绑定 react-aria-components 版本的样式 hash。Service Worker 不运行时缓存 API、WebSocket 或媒体响应。

## 主要实现位置

- `internal/contract/api/openapi.yaml`
- `internal/contract/fault/`
- `internal/contract/realtime/`
- `internal/auth/`
- `internal/transport/httpapi/server.go`
- `internal/webapp/handler.go`
