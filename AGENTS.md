# AGENTS.md

## 工作语言与基本原则

- 对话、说明、计划、报告和提交信息使用中文；代码标识、协议字段和必要术语保持英文。
- 开始工作前先检查当前目录、Git 状态和实际文件，不根据旧对话、旧报告或文件名猜测现状。
- 默认只做用户请求范围内的最小必要修改。文档任务不得顺手修改实现，调查任务不得擅自落地架构变化。
- 不读取或输出 secret、token、Cookie、私密 metadata、完整媒体路径或真实媒体内容。
- 当前仓库处于正式实现前期。不要把原型、规划或可交叉编译状态描述成已经交付的产品能力。

## 项目身份与当前状态

- 英文公开产品名：**Gallery**。
- 中文公开产品名：**画廊**。
- 代码、仓库、包、命令和服务代号：`gallery`。
- 建议后端命令：`galleryd`；建议 CLI：`galleryctl`。
- Gallery 是独立的净室产品，不以任何旧 Gallery 的数据库、配置、API、目录结构或行为作为兼容、迁移或对拍目标。
- 当前仓库保存已收敛的工程文档和早期 cleanroom 验证台，尚没有正式产品代码。下一步是阶段 0 契约骨架和 Walking Skeleton，不是继续无边界调研。

## 权威资料与阅读顺序

所有当前产品语义从 [Documents/README.md](Documents/README.md) 进入。首次接手时按下列顺序阅读：

1. `Documents/规范/01-产品定义与不变量.md`
2. `Documents/规范/02-系统架构与模块边界.md`
3. `Documents/规范/03-领域模型与数据所有权.md`
4. `Documents/规范/04-扫描-Catalog与任务.md`
5. 当前任务对应的其余规范
6. `Documents/指南/01-v1实施计划.md`
7. `Documents/指南/02-测试与发布门禁.md`
8. `Documents/ADR/README.md` 及相关 ADR
9. `Documents/证据/验证记录.md`

文档职责严格区分：

- **规范**定义当前实现必须遵守的行为，是主题的唯一权威来源。
- **ADR**记录决策、理由和重新审议条件；状态只以 `Documents/ADR/README.md` 为准。
- **实施指南**定义开发顺序、冻结门禁和发布验收。
- **验证记录**只说明证据和局限，不能覆盖规范。
- `Test-Bench/` 中的 README、源码和结果是历史实验材料；若与当前规范冲突，以当前规范为准。

不要为同一主题另写一份“最终方案”。有必要调整语义时，修改唯一权威规范；改变已接受决策时同步 ADR。

## 已接受的技术方向

- Go、API-first 模块化单体、单主进程；基础发行不得依赖 cgo。
- Go 标准 `net/http` 为基础，可使用小型路由或中间件库，不引入重型应用容器。
- SQLite WAL；`control.db` 与 `catalog.db` 分离生命周期，不引入默认远程数据库。
- SQLite FTS5 作为 v1 搜索引擎，与 Catalog 查询投影共同发布。
- REST/JSON + OpenAPI 是 Web、CLI、桌面壳和第三方客户端的共同契约。
- 版本化 WebSocket 提供实时事件，HTTP snapshot 是断线恢复的事实源。
- 规则使用规范 JSON、JSON Schema、有限原语和受限 Gallery CEL Profile。
- 响应式 Web/PWA 是唯一业务 UI 基线。
- 桌面壳是可替换适配器；Wails 仅为当前 Windows 优先候选，仍须与 Tauri 对照。
- 微服务、外部队列、Redis、PostgreSQL、独立搜索服务、任意配置脚本和壳直连数据库均不是 v1 默认方案。

前端框架、CSS 组件库、SQLite 驱动、最终细粒度路由和物理表结构尚未冻结，不要仅凭原型依赖替未来实现做决定。

## 不可违反的产品边界

1. **媒体根永久只读**：数据库、配置、日志、缓存、临时文件、缩略图、转码和更新文件全部位于独立 AppDirs。
2. **用户事实不可被重扫覆盖**：Canonical 实体、Binding、Override、Collection、Favorite、Progress、Note、Share、账户和授权必须可备份恢复。
3. **路径不是身份**：CanonicalMedia、ContentBlob 和 FileLocation 是不同概念；新 Blob 由算法版本和完整 SHA-256 确认。
4. **Catalog 只发布完整快照**：扫描、搜索、排序和 Overlay 查询投影不得混合新旧代次；外部只通过服务端签发的 `query_publication_id` 选择合法快照。
5. **规则是 Source 差异的唯一解释入口**：不得按 Provider 或平台名在业务代码中增加特例分支。
6. **API 拥有协议语义**：排序、过滤、分页、授权和有效字段由后端决定；客户端不得重排服务端列表或直读数据库。
7. **权限按 capability 判定**：角色只是预设包，所有服务方法检查 effective capability 和资源范围。
8. **核心与平台隔离**：文件身份、Watcher、路径、进程、工具、AppDirs 和凭据通过平台适配器；桌面壳不是核心依赖。
9. **失败可解释、可恢复**：离线、空结果、权限不足、校验错误、冲突、游标过期和内部失败使用稳定结构化 code。
10. **不夸大证据**：合成数据不代表真实规模，交叉编译不代表目标平台支持，WSL DrvFS 不代表 Linux ext4。

## 数据和媒体原则

- `control.db` 保存不可重建的 Canonical/User Overlay、Binding、账户、授权和分享；它是最高备份优先级。
- `catalog.db` 保存可重建的 Source-derived 事实、内容/位置记录、查询投影和 FTS；删除后必须能从 Source、规则和 control 稳定引用重建。
- control 中不得永久保存 Catalog revision 内部 row ID。
- 快速指纹、路径、mtime、FileID 或 inode 只能筛选候选，不能代替完整内容哈希。
- 新 ContentBlob 首次完整 SHA-256 是相关 publication 的前置条件；超大文件或网络盘只能延迟发布，不能降低身份强度。
- DerivedAsset 使用完整稳定 key 和受校验 manifest；生成使用临时文件与原子发布，GC 不得删除活跃读取。
- v1 不改名、移动、删除原媒体，也不写回 metadata。

## 规则和配置边界

- 运行时规则唯一事实源是规范 JSON；YAML/TOML 只允许显式导入后转换为规范 JSON，CUE 仅可作开发工具。
- JSON Schema 驱动结构、默认值、约束、表单和编辑器元数据；保存前后使用同一校验语义。
- 字符串规范化必须由 Schema 逐字段声明；regex、glob、路径、JSON Pointer、metadata 键和 external ID 默认逐 code point 保留。
- JSON 数字使用精确十进制规范化，不得让 `float64` 中转影响规则身份。
- `package_hash` 标识完整分发包，`semantic_hash` 是 RuleVersion 运行语义身份，`rule_ir_hash` 标识具体编译执行计划；tests-only 修改不得触发新 RuleVersion。
- CEL 只做受限布尔条件、集合谓词和简单值选择，禁止文件、网络、进程、时钟、随机、反射、递归和任意 host 函数。
- 跨记录去重、文件 I/O、全局聚合、压缩包解析和外部工具属于核心服务或未来插件边界，不属于规则表达式。

## 开发顺序

严格遵循 `Documents/指南/01-v1实施计划.md`：

1. 阶段 0：正式领域 ID、两库迁移/备份骨架、OpenAPI、错误 code、WebSocket 信封、规则 Schema 和 AppDirs 写入守卫。
2. Walking Skeleton：用一个作品和一个媒体的合成只读 Source 打通 Personal 配对、Library/Source、规则绑定、完整哈希、最小 publication、REST、媒体 Range 和 WebSocket Job。
3. Architecture Proof：补齐快照分页、Overlay、FTS、Catalog 重建、强杀恢复和多客户端边界后，再冻结数据库与 API。
4. 按领域/规则/扫描/查询与媒体/安全/Web/PWA/平台发行的顺序扩展。

Walking Skeleton 功能可以少，但基础模型不能是临时替代品：

- 使用正式 `control.db`/`catalog.db` 迁移框架；
- 使用稳定 Canonical/Source ID、SourceRuleBinding、完整 Blob 身份和 `query_publication_id`；
- 使用正式 OpenAPI DTO、错误信封、Session 和 capability；
- 禁止内存替代数据库、路径主键、临时 DTO、匿名管理员或客户端直连数据层。

具体类名、函数名和表结构应留给实现，但不得推翻已选中的身份、所有权、授权和 publication 契约。

## 测试与验证

- 正式实现至少分为单元、数据、契约、集成、UI 和平台测试；发布前另做强杀、磁盘满、GC/VACUUM、真实只读样本和目标平台验证。
- 普通 CI 使用合成 Source 和临时 AppDirs；不得把真实全库扫描、批量缩略图、转码或维护任务作为默认步骤。
- 任何真实媒体验证都必须由用户明确授权，并在执行前后比较只读 guard；输出不得包含绝对路径、metadata 原文、媒体内容或完整 URL。
- 平台支持只按实际运行结果声明。Windows 11 x64 是 v1 正式目标；Linux ext4、macOS、Docker 和 SMB/NAS 仍需各自门禁。
- 性能结论必须记录硬件、OS、存储、依赖版本、样本、缓存状态、并发和分位数；不能复用旧原型数字充当正式 SLA。

### cleanroom 验证台

- `Test-Bench/cleanroom-lab/`：第一轮合成技术对照，包含 Go、ASP.NET、Wails、PWA、搜索、规则、文件身份和安全原型。
- `Test-Bench/cleanroom-lab-real/`：反馈型验证，包含真实只读盘点、百万/千万合成搜索、Catalog publication、平台和安全契约探针。
- 两者是独立 Go module。运行前先读各自 README，在目录内使用对应 `go.mod`；不要在仓库根假设统一 workspace。
- 这些目录包含约 5 GB 的历史结果、数据库和构建产物。未经用户要求，不要删除、重建、批量格式化或提交大型产物；新增结果要记录生成命令、环境和局限。
- 大规模命令必须显式指定验证台内部或系统临时目录作为输出；绝不能把结果写入真实 Source。
- 旧实验 README 中的早期结论可能已被当前 ADR 修订；决策只看 `Documents/`，实验用于追溯证据。

## API、安全和客户端

- 公共 API 统一位于 `/api/v1`；WebSocket 使用 `/ws/v1`。最终细粒度路由以未来 OpenAPI 为准，不要提前在文档中冻结控制器。
- 长任务先创建持久 Job，返回 job ID；不能依赖单个 HTTP 请求或未记录的 goroutine 完成不可恢复工作。
- Personal 默认只监听 loopback，但匿名访问不是管理员；普通浏览器使用一次性配对建立 HttpOnly Session。
- LAN 必须显式启用，先初始化 Owner，再使用本地账户、服务端 Session、CSRF、API Token 和资源范围授权。
- Remote/OIDC 延后且默认不可启用；不得把 LAN 加反向代理描述成安全公网部署。
- Web/PWA 必须在无壳浏览器完成业务闭环。壳只能处理进程、目录选择、托盘、通知、自启、凭据和电源事件。
- 第三方客户端只能使用 OpenAPI、WebSocket 和媒体 HTTP；不得拥有内部包、数据库或排序捷径。

## 构建、依赖和发行

- 正式产品尚无根级构建命令；建立实现时应为 `galleryd`、`galleryctl`、Web/PWA 和可选壳提供明确、可重复的独立命令。
- ffmpeg/ffprobe 等外部工具必须经 ToolDiscovery、版本允许列表、参数数组、超时和资源限制调用，不能拼接 shell 命令。
- 程序资源与用户 AppDirs 分离；覆盖升级不得删除用户数据。数据升级前优先备份 control，Catalog 不兼容时可重建。
- 发行前完成 OpenAPI/WS/规则/数据版本、许可证、SBOM、依赖安全、签名和升级/降级说明。
- Windows、Linux、macOS、Docker 和网络盘能力分别验收，不从 Go 可交叉编译目标自动生成支持矩阵。

## 文档维护

- `Documents/README.md` 是唯一导航入口；不要恢复多轮调研报告或另建历史归档目录。
- 产品定义、领域模型、扫描、规则、查询、API、安全、文件系统和跨平台主题各有唯一权威规范，其他文档只链接，不复制长段结论。
- 证据更新只进入 `Documents/证据/验证记录.md`，并标明数据、环境、结果、局限和需要重测的门禁。
- 实施进度、一次性测试日志和修复汇报不进入权威规范；由代码、测试、Issue 和 Git 历史承担。
- 修改已接受决策时同步 ADR 的问题、决策、理由、替代方案、影响和重新审议条件；状态只在 ADR 索引维护。

## Git 与交付

- 修改前后检查 `git status --short`，保留并绕开用户已有改动。
- 不使用 `git reset --hard`、`git checkout --` 等破坏性回退；只撤销本轮明确产生的内容。
- 提交遵循 Conventional Commits：`type(scope): 中文描述`。
- 提交正文列出变动文件或同类文件组，并说明验证结果。
- 需要提交时使用 SSH 密钥签名：`git commit --gpg-sign=~/.ssh/id_ed25519.pub`。
- 未经用户明确要求，不提交、不推送、不创建 PR，也不删除 cleanroom 大型历史结果。

## 当前可开工结论

当前规范不存在阻塞 Walking Skeleton 开工的架构冲突。搜索排名/高亮/精确总数、游标租约、最终唯一约束、HDD/NAS 性能、Wails/Tauri、Linux/macOS/Docker 支持等仍是后续冻结或发行门禁，但不应继续延迟阶段 0 和第一条正式垂直切片。
