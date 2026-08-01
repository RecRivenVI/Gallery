# 当前验证记录

## 2026-08-01：文档重写前源码静态核验

### 目的

为全仓库文档重写建立不依赖旧文档和旧对话的事实基线，核对当前源码、配置、测试入口、构建方式、API、客户端与仓库结构。该核验不评价产品质量，也不替代测试与发布门禁。

### 基线与方法

- 仓库：`D:\GitHubRecRivenVI\Gallery`
- 分支：`main`
- 核验开始时 HEAD：`7a9aa46bcc293058966621ef01f2b2cfd6969ad7`
- 工作树：包含任务一和任务二留下的未提交结构调整；本记录核对实际工作树，不把 HEAD 当作唯一文件来源。
- 方法：以 Python 脚本只读枚举和解析源码、配置、工作流、测试、生成入口与 Markdown；对关键行为继续读取对应 Go/TypeScript 实现。

本轮按用户要求暂停新功能开发和测试，因此没有运行 Go、Web、浏览器、打包、性能、真实 Source 或跨设备测试。

### 已核对事实

| 范围 | 静态核验结果 |
| --- | --- |
| 模块与命令 | Go 模块为 `github.com/RecRivenVI/gallery`；正式命令为 `galleryd` 和 `galleryctl` |
| 工具链 | `go.mod` 固定 Go 1.26.5；Web 要求 Node 22.22.0 以上和 npm 10.9.8，CI 使用 Node 22.23.1 |
| 平台 | 当前 CI、Web 和便携包工作流只覆盖 Windows；便携构建目标为 `windows/amd64`、`CGO_ENABLED=0` |
| API | OpenAPI 源包含 121 个 operation；服务端注册相同的 121 个 API method/path，另有 `/ws/v1` 和静态入口 `/` |
| 存储 | `control.db` 有 26 个嵌入式迁移，`catalog.db` 有 21 个嵌入式迁移；启动启用 WAL、外键、busy timeout 和 `BEGIN IMMEDIATE` 写事务 |
| 配置 | 当前只发现命令行配置；运行模式为 `personal` 或 `lan`，未发现配置文件加载器 |
| 扫描 | `index`、`incremental`、`verify` 三种 profile 在源码中具有不同哈希与复用语义 |
| 查询 | 过滤器有深度和节点上限；游标为 HMAC 签名 keyset；`TotalBudget=5000` 在源码中仍标记 `PRE_FREEZE` |
| 规则 | 规则包有大小与嵌套限制，使用受限 CEL 和版本化 primitive registry；JSON、YAML、TOML 导入统一为规范 JSON |
| 媒体 | 原始媒体读取要求已确认事实；完整 GET 复制时复算 SHA-256；Range 只支持单区间；当前派生入口限 JPEG 缩略图 |
| Web | React/TypeScript 提供用户端与 `/manage/` 双入口；只有用户端生成 PWA，API、WebSocket 和媒体不进入运行时缓存 |
| 生成 | OpenAPI Go/TypeScript 客户端、primitive registry、migration checksum 和 Web 生产资源均有可定位生成源 |
| 测试与构建 | 根级 `Check.ps1` 汇总 LF、生成一致性、Go、Web、race 可选项；Windows 便携脚本生成二进制、manifest、SBOM 和校验和 |

### 发现的文档问题

- 根目录入口文件混入阶段进度、证据编号和未来任务，职责与长期维护不相容。
- 设计材料主要描述 Legacy 候选方案，却被后续过程记录叠加成当前设计说明。
- 验证记录包含大量按日累积的结论，部分日期晚于本次核验日期，不能作为当前事实使用。
- 旧大规模测试所述原始报告、数据库和当时的 `tools/stage4` 工具不在当前工作树中，无法独立重放。
- 当前仓库保留 testlab 工具和测试源码，但这些文件的存在只证明入口可审计，不证明本次工作树已通过测试。

### 结论与限制

本记录只证明文档重写所使用的静态事实来源已经定位并交叉核对。它不证明当前工作树可编译、测试通过、性能达标、安全门禁通过、真实 Source 零写入或 RC 就绪。完成文档改写后只执行文档结构、链接、编码、换行和差异检查，不补跑产品测试。
