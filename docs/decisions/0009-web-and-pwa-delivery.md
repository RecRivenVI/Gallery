# ADR-0009：Web 与 PWA 交付

- 状态：Accepted
- 日期：2026-07-23

## 上下文

用户浏览与管理诊断需要不同信息密度，但必须共享后端、认证、错误和可访问性基础。PWA 不能缓存带授权或 publication 语义的动态数据。

## 决策

- Web 使用 React/TypeScript，并由 `galleryd` 同源提供生产资产。
- 用户端 `/` 与管理端 `/manage` 使用独立 HTML 外壳和路由表。
- 两端共享 API、Session、实时、错误、主题和设计 primitive。
- 只有用户端注册 PWA manifest/Service Worker；管理端不进入安装入口。
- Service Worker 只 precache 静态资产，不运行时缓存 API、WebSocket 或媒体。
- `/api`、`/ws` 和 `/manage` 从用户端导航 fallback 排除。
- Web 构建产物必须携带 contract/API 版本，服务端在提供前验证。

## 理由

同源减少 CORS 与凭据复杂度；双外壳避免管理深链落入用户端；共享基础防止两端授权和无障碍语义漂移；不缓存动态业务数据保留 publication 与吊销事实。

## 影响

- Node/npm 是 Web 构建依赖，不是 `galleryd` 运行依赖。
- Web 修改需要更新生成类型并验证两个入口。
- 管理端不能依赖用户端 Service Worker 提供离线能力。
- 内嵌资产版本不匹配时服务端返回明确不可用，而不是提供错误外壳。

## 重新审议

若未来拆分独立 Web 部署，必须重新定义 origin、凭据、CSP、缓存、版本协商和升级原子性。
