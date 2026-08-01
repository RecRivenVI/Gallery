# 产品定义与不变量

## 定位

Gallery（画廊）是本地优先的个人媒体目录基座。Go 服务 `galleryd` 从用户登记的只读 `Source` 读取目录、metadata 和媒体事实，建立可重建 `Catalog`，并通过统一 API 向 CLI、可选 Web/PWA 和未来客户端提供能力。

## 产品边界

Gallery 是：

- 只读媒体目录、索引、查询和受控媒体读取服务；
- 将物理来源映射为稳定 Canonical 实体的本地系统；
- 保存账户、授权、Binding、Overlay、分享与备份等不可重建事实的长期产品；
- API-first 的模块化单体，前端不是后端运行的架构前提。

Gallery 不是：

- 文件管理器、下载器、抓取器或云同步系统；
- 在通用代码中内置各 Provider 行为的适配集合；
- 默认公开到互联网的多租户服务；
- 其它 Gallery 产品的兼容层、迁移器或替代前端。

## 当前部署模式

| 模式 | 当前实现 | 边界 |
| --- | --- | --- |
| Personal | 已有实现 | 仅 loopback；一次性配对换取 HttpOnly Session |
| LAN | 已有实现 | loopback 或私有地址；先在 loopback 初始化 Owner，再使用本地账户 |
| Remote | 未实现 | HTTPS、OIDC、受信代理和公网威胁模型均不在当前范围 |

“已有实现”不表示 Security Gate 或发行门禁已通过。

## 系统不变量

1. Source 永久只读，任何写入根与 Source 都必须互不重叠。
2. 用户事实不因重扫、Catalog GC 或 Catalog 重建而丢失。
3. 路径只表示位置，不直接充当 Work、Media 或 Blob 的永久身份。
4. 查询只能读取完整 publication，不观察 staging 中间态。
5. 来源差异由版本化规则解释，业务代码不按 Provider 名称分支。
6. 服务器拥有排序、过滤、分页、授权和错误语义。
7. capability 是授权单位，角色只是 capability 的预设集合。
8. 平台能力通过端口与适配器隔离，桌面壳和 Web 都可替换。
9. 错误必须结构化；空、离线、无权限、冲突和内部失败不能混为一种状态。
10. 验证结论必须注明环境和范围，交叉编译、合成测试或历史记录不能外推为正式支持。

## 当前产品面

当前源码包含 Library/Source、规则生命周期、扫描与任务、Catalog publication、作品与创作者查询、媒体读取、Overlay、Binding 治理、账户与授权、分享、备份恢复、维护、Web/PWA 和 Windows 便携构建路径。各资源的精确定义见[架构文档](../architecture/README.md)和 OpenAPI。

以下仍是计划或发布条件，而非当前承诺：稳定 v1 API、正式性能预算、签名发行、安装与自动更新、真实设备完整门禁、非 Windows 支持、Remote/OIDC、Source 写入、插件运行时和原生移动客户端。

## 完成标准

候选 RC 至少需要证明：Source 零写入；`control.db` 可备份恢复；Catalog 可安全重建；扫描、取消和崩溃恢复有可解释终态；查询与媒体保持 publication 一致性；授权在 HTTP、WebSocket 和客户端之间一致；Windows x64 的功能、安全、性能、可访问性、升级和制品门禁全部通过。

具体顺序和证据要求见[开发路线](../development/roadmap.md)与[测试和发布门禁](../development/testing-and-release-gates.md)。
