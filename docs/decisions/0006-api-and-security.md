# ADR-0006：API 与安全

- 状态：Accepted
- 日期：2026-07-16

## 上下文

Personal 浏览器、LAN 多账户、CLI 和未来客户端必须共享资源与授权语义。隐式本机管理员、仅角色判断或手写 DTO 会导致不同客户端行为漂移。

## 决策

- HTTP 以 OpenAPI 为唯一契约源，公开 Go 与 Web 类型从契约生成。
- 失败使用稳定结构化 code，不向客户端暴露内部 cause 或路径。
- Personal 仅 loopback，通过单次配对建立 HttpOnly Session；匿名不是 Owner。
- LAN 仅私网，先从 loopback 初始化 Owner，再使用本地账户与 Argon2id。
- capability 是授权单位；角色、Session、Token scope 和显式 allow/deny Grant 共同计算 effective capability。
- Session、Token、Share、Grant 与 WebSocket 吊销保持一致。
- WebSocket 只发送版本化变化通知，HTTP snapshot 是恢复事实源。
- Remote/OIDC 与受信代理模型延后。

## 理由

单一契约减少客户端漂移；capability 与资源 scope 可以表达最小权限；显式配对和 Owner 初始化避免“本机访问自动变管理员”；HTTP snapshot 能从丢失或乱序实时事件恢复。

## 影响

- 客户端可以隐藏明显无权入口，但最终授权只能由服务端裁决。
- 写请求必须遵守认证、Origin/Host、CSRF 与凭据生命周期。
- 新 capability 同步后端词表、migration、Web 联合类型、OpenAPI 与测试。
- 不为 Remote 预留绕过当前边界的隐式配置。

## 重新审议

引入公网访问、OIDC 或受信代理前必须新增 ADR，定义 TLS、代理头、账户映射、审计和部署责任。
