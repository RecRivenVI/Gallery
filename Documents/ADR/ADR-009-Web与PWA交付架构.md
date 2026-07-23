# ADR-009 Web 与 PWA 交付架构

- 状态：接受
- 日期：2026-07-23
- 规范：[跨平台与客户端](../规范/09-跨平台与客户端.md)

## 问题

Gallery 需要在普通浏览器完成业务闭环，同时保持 API-first、单主进程、基础发行无 cgo、程序资源与 AppDirs 分离，并确保 Web 静态资源与后端契约不会错配。还需要决定前端状态权威、PWA 缓存和可访问组件边界。

## 决策

正式 Web 使用 React + TypeScript strict + Vite；服务端状态由 TanStack Query 管理，HTTP 客户端从 OpenAPI 生成类型并通过 `openapi-fetch` 调用，路由使用 React Router。可访问交互原语使用 React Aria Components，规则 Schema 表单使用 RJSF + AJV；视觉由仓库自有语义令牌和 CSS 构成，不引入拥有业务语义的重型组件系统。

生产构建产物提交到 `internal/webapp/dist` 并由 `galleryd` 使用 `embed.FS` 同源提供。构建 manifest 同时登记 Web、OpenAPI contract 和 API major 版本，启动时不匹配则返回稳定的 `WEB_VERSION_MISMATCH`，不得运行混合版本。SPA fallback 明确排除 `/api` 与 `/ws`。

PWA 只预缓存版本化静态壳；不缓存 API、WebSocket 或媒体正文，不提供离线业务事实写入队列。Service Worker 更新采用用户可见的 prompt，不自动跳过等待。浏览器本地存储只允许主题、导航等展示偏好；Session、CSRF、Token、Share secret、Overlay 和任务状态不得持久化为客户端事实。

## 理由

- OpenAPI 生成类型让 Web 与 CLI/第三方客户端共享同一公开契约，避免前端自造 DTO；
- 同源嵌入消除独立静态服务器和 CORS 依赖，保留单二进制发行方向；
- HTTP snapshot 作为事实源，WebSocket 只触发失效与重取，可处理 sequence gap、断线和凭据吊销；
- headless 可访问原语与语义令牌允许同时满足键盘、ARIA、暗色、reduced motion 和可替换视觉需求；
- 静态壳缓存不会把私密媒体、授权响应或过期 publication 固化进浏览器缓存。

## 替代方案

- **服务端模板或 HTMX**：依赖较少，但复杂规则表单、实时任务、多视图状态和 PWA 更新的客户端交互成本更高。
- **Next.js/SSR**：当前本地单进程产品没有 SEO 或边缘渲染需求，会引入第二套服务运行时与部署契约。
- **Electron 内置 UI**：把业务 UI 绑定到桌面壳并增加运行时体积，破坏普通浏览器基线。
- **客户端离线数据库/业务写入队列**：形成第二事实源和冲突协议，v1 明确拒绝。
- **运行时从 CDN 获取前端依赖**：破坏可重复、离线本地发行和 CSP 边界，明确拒绝。

## 影响

- Node/npm 是开发与 CI 构建依赖，不是 `galleryd` 运行时依赖；发行物继续是嵌入静态资产的 Go 二进制；
- `Check.ps1` 必须验证 npm lock、类型、lint、格式、单元测试、OpenAPI 前端生成物和生产资产无漂移；
- hashed asset 使用长期 immutable cache，HTML、manifest 和 Service Worker 使用 no-cache；
- 规则表单模块按路由延迟加载，避免进入浏览主页的初始关键路径；
- CSP、frame、referrer、permissions 和 MIME 安全头由同源 Web handler 统一设置；
- 桌面壳未来只能加载这份 Web 产物并消费公开 API，不得复制业务界面。

## 重新审议条件

若正式浏览器/目标设备性能证明当前框架无法满足启动、内存或可访问性门禁，或浏览器平台移除所需能力，可在保持 OpenAPI、同源资产版本、HTTP 事实源和无客户端业务事实边界的前提下替换前端实现。若产品未来要求真正离线写入，必须先新增独立 ADR 定义同步、冲突、安全和数据所有权，不能通过扩大 Service Worker 缓存暗中实现。
