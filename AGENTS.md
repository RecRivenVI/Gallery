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
- 当前仓库已有正式产品代码（`cmd/`、`internal/`、`pkg/`）。阶段 0 契约骨架、Walking Skeleton 与 Architecture Proof 正确性切片均已完成；当前处于**阶段 1「领域和数据所有权」**。已落地并配套 API 的能力包括：Personal 配对/Session/capability、Library/Source/RuleVersion/SourceRuleBinding、持久 Scan Job 与完整 SHA-256 publication、双 revision `query_publication_id` 查询/FTS5/自然排序/签名游标、Overlay 同步写与异步重投影、Catalog 删除重建与稳定重绑、八点强杀恢复、CanonicalCreator 合并/撤销、Work/Media/Creator Binding issue 人工修复、Source-derived active/inactive/orphan_candidate/orphaned 保留窗口与人工审查、AppDirs 进程独占锁、有界 Job 调度器、规则 extension 身份分类。尚未完成：SourceWork 拆分/合并、control 产品级备份恢复与 Catalog 全量重建后的人工决策恢复、阶段 1 Schema Freeze Gate 的最终唯一约束、以及阶段 2+ 的规则闭环/查询/媒体/安全/Web/平台发行。
- 本文件是需要随真实开发状态持续维护的 Agent 规则；发现与代码、有效 ADR 或规范不一致时应更新本文件，但不得放宽安全、只读 Source、Git、签名或测试要求，也不得把临时实装写成已冻结决策。

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

### 平台适配边界（开发约束）

- 新增任何平台相关能力必须通过 `internal/platform/*` 与 `internal/ports`，不得把 OS 专有实现写进领域或应用层。AppDirs 进程独占锁已按此实现（`internal/platform/lock`，Windows LockFileEx / Unix flock 分文件构建）。
- 后续 FileID、Watcher、句柄式打开、ToolDiscovery 与 CredentialStore 同样不得直接进入领域层，必须经平台 adapter。
- `internal/scanner`、`internal/media`、`internal/derived` 中现存对 `os`/`filepath` 的直接依赖是技术债，只在本轮实际触及的代码中做局部迁移，不为形式统一制造大量空接口或一次性大重构。

### 当前限定的暂定行为

- **Overlay 查询影响是当前实现，不是字段永久分类**：TitleOverride/ManualTag/Hidden/CustomCover 目前属于 query-affecting snapshot，Favorite/ReadingProgress 属于实时附加。某字段未来一旦参与过滤、排序、搜索或集合成员判断，必须进入当前查询的 dependency set 与 revision。
- **SourceRuleBinding 目前只执行优先级最高的一条有效 Binding**：尚未支持多规则链、Provider 路由组合或多 Binding 合并执行，这些属于阶段 2 待冻结语义，不得声称已支持。
- 已实装但未冻结的常量与选择集中登记在 `Documents/指南/01-v1实施计划.md` 的「暂定实装决策」表，修改前先查该表的冻结阶段与重新审议门禁。

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

1. 阶段 0：正式领域 ID、两库迁移/备份骨架、OpenAPI、错误 code、WebSocket 信封、规则 Schema 和 AppDirs 写入守卫。**（已完成）**
2. Walking Skeleton：用一个作品和一个媒体的合成只读 Source 打通 Personal 配对、Library/Source、规则绑定、完整哈希、最小 publication、REST、媒体 Range 和 WebSocket Job。**（已完成）**
3. Architecture Proof：补齐快照分页、Overlay、FTS、Catalog 重建、强杀恢复和多客户端边界后，再冻结数据库与 API。**（正确性切片已完成；物理 Schema 与完整 API 仍未冻结）**
4. 按领域/规则/扫描/查询与媒体/安全/Web/PWA/平台发行的顺序扩展。**（进行中：阶段 1 领域和数据所有权）**

当前处于阶段 1。下一优先级为 control 备份/恢复 → SourceWork 拆分/合并 → Catalog 全量重建决策恢复 → 阶段 1 Schema Freeze Gate，之后才进入完整规则闭环、查询/媒体、安全、Web/PWA 与平台发行。不要据此提前展开前端、LAN 完整账户、桌面壳或发行。

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
- 所有提交信息必须遵循下节“Git Commit Message 规范”；该节是仓库内唯一、强制的提交信息格式。
- 未经用户明确要求，不提交、不推送、不创建 PR，也不删除 cleanroom 大型历史结果。

## Git Commit Message 规范

### 唯一格式

每个正式提交都必须同时包含标题和正文，并严格使用：

```text
<type>(<scope>): <中文标题>

变动内容：
- `<仓库相对文件路径>`：<中文变动内容汇总>
- `<仓库相对文件路径>`：<中文变动内容汇总>
```

标题与正文之间必须恰好有一个空行。正文必须以顶格书写的 `变动内容：` 开始，其后至少包含一个文件列表项；不得使用 `changes:`、YAML 键值结构、二级列表或其他正文段落。列表标记必须是半角短横线和一个半角空格 `- `，不得直接使用 `·` 或 `•`。

### 标题

- 标题必须匹配 `^(feat|fix|refactor|perf|test|docs|build|ci|chore|revert)\([a-z0-9]+(?:-[a-z0-9]+)*\): .+$`，并严格采用 `<type>(<scope>): <中文标题>`。
- `type` 必须是下表允许的小写英文值；`scope` 必须始终填写，只能包含小写英文、数字和用于连接多词的单个半角连字符。
- `)` 后必须紧跟英文半角冒号，冒号后必须恰好一个半角空格；不得使用全角冒号。
- 标题必须包含中文并保持单行，代码标识和必要技术术语可以保留英文，总长度不得超过 72 个字符。
- 标题末尾不得出现句号、分号、冒号、问号、感叹号或其他结束标点，不得使用 Emoji，不得添加 `!`。
- 标题只概括提交的主要实际变动，不得描述测试结果、验收结果、性能数字、动机或下一步。

允许的 `type` 只有：

| type | 语义 |
| --- | --- |
| `feat` | 新增用户能力或正式业务能力 |
| `fix` | 修复错误行为 |
| `refactor` | 不改变外部行为的结构调整 |
| `perf` | 性能优化 |
| `test` | 新增或调整测试 |
| `docs` | 只修改文档 |
| `build` | 构建系统、依赖或打包 |
| `ci` | 持续集成与自动化检查 |
| `chore` | 不属于其他类型的仓库维护 |
| `revert` | 撤销历史提交 |

一个提交包含多种性质时，使用最主要、最能描述实际内容的 `type`。

### `scope`

- `scope` 必须使用小写英文，可以包含数字；多词使用单个半角连字符连接。
- 不允许下划线、空格、中文、路径分隔符或省略 `scope`。
- 优先使用稳定领域或模块名，例如 `repo`、`agents`、`core`、`config`、`contract`、`api`、`ws`、`auth`、`session`、`library`、`source`、`rules`、`scan`、`jobs`、`catalog`、`query`、`search`、`media`、`assets`、`storage`、`migration`、`security`、`cli`、`web`、`desktop`、`docs`、`guide`、`adr`、`test`、`ci`、`build`。
- 不得为单次提交创造过细或一次性的 `scope`。

### Markdown 正文

正文第一行必须恰好为：

```text
变动内容：
```

`变动内容：` 必须顶格书写，使用这四个中文字符和一个全角冒号，后面立即换行，不得在同一行附加内容，也不得替换为 `changes:`、`修改内容：`、`变更：` 或其他写法。

每个文件列表项必须独占一个物理行，并严格使用：

```text
- `<仓库相对文件路径>`：<中文变动内容汇总>
```

- 每行必须顶格，不得有前导空格；必须以半角 `- ` 开始。
- 文件路径必须放在一对反引号中，反引号后紧跟中文全角冒号 `：`，冒号后直接填写摘要，不增加额外空格。
- 路径必须是使用 `/` 分隔符的仓库相对路径，禁止绝对路径和用目录替代具体文件。
- 每个文件只能出现一次，文件列表必须按路径字典序排列，文件行之间不得插入空行。
- 同一文件的全部变动应尽可能压缩成一句话；可以使用中文逗号、顿号或分号连接相关变动，但不得换行续写或使用二级列表。
- 摘要必须包含中文，代码标识和必要技术术语可以保留英文；摘要末尾不得使用句号、分号、冒号、问号或感叹号。
- 正文不得使用 YAML、代码围栏、编号列表、表格或额外段落。
- 正文只能描述哪个文件发生变化以及该文件实际变动了什么，不得描述修改动机、背景、测试是否通过、验收结果、性能数字、用户影响、Git 状态、提交哈希、下一步、方案理由或变动后的效果。

正确示例：

```text
feat(query): 实现快照查询能力

变动内容：
- `internal/query/cursor.go`：实现签名游标、租约校验和篡改拒绝
- `internal/query/search.go`：增加 CJK bigram 检索、原文复核和权限范围过滤
- `internal/query/service_test.go`：覆盖跨页查询、条件变化和旧游标过期场景
```

以下形式均不允许：以 `changes:` 开始的 YAML 正文、同一文件重复多行、缩进的二级列表、字面量 `·` 或 `•`、文件行之间的空行，以及正文后的其他段落。

### 新增、删除、重命名与生成文件

- 新增文件使用 ``- `new/path/file.md`：新增文件并记录接口使用说明`` 这类单行摘要。
- 删除文件使用 ``- `old/path/file.md`：删除废弃文件`` 这类单行摘要。
- 重命名通常只列新路径，例如 ``- `new/path/file.md`：从 `old/path/file.md` 重命名并更新内容``；只有旧路径和新路径在提交后都真实存在时才分别列出。
- 生成文件仍须逐个列出实际进入提交的文件，不得用生成目录代替具体文件，也不得只写“重新生成代码”。

### Merge、Revert 与不兼容变更

- Merge Commit 必须保留 Merge 结构，标题使用 `chore(merge): 合并 <中文分支说明>`，正文列出该 Merge 自身实际带入或解决冲突时修改的文件。
- Merge Commit 没有独立文件树变化时，唯一允许的特殊节点是 ``- `.`：合并提交历史``；`.` 不得用于其他提交。
- Revert Commit 使用 `revert(<scope>): 撤销 <中文原提交摘要>`，正文按一个文件一行列出实际恢复的内容。
- Gallery 尚未发布稳定公共版本，当前禁止 `!` 和 `BREAKING CHANGE:`。未来确需不兼容变更时必须先更新本规范，历史重写不得添加二者。

### 签名与历史重写

- 所有正式提交必须使用 SSH 密钥签名，创建提交时使用 `git commit --gpg-sign=~/.ssh/id_ed25519.pub` 或等效的显式 SSH 签名方式。
- 历史重写后的每个提交都必须重新签名，不得伪造旧签名或留下未签名提交；最终检查必须遍历全部历史确认签名有效。
- 历史提交信息重写只能改变 Commit Message、重新生成的签名和必然变化的 Commit Hash；不得改变提交数量、顺序、父子或 Merge 结构、文件树或实际变动范围。
- 重写必须尽可能保留作者和提交者的姓名、邮箱与日期。执行前必须创建不可误覆盖的本地备份引用，并记录原 HEAD、提交数量、提交图和工作树状态。
- 如果环境无法为全部提交重新签名，或无法证明提交结构与文件树逐提交一致，不得替换历史引用；必须恢复原始历史并报告阻塞。历史重写不得自动推送、强制推送、创建 PR 或修改远程引用。

### 完整示例

```text
docs(agents): 采用 Markdown 提交正文格式

变动内容：
- `AGENTS.md`：将 YAML 正文规范替换为一个文件一行的 Markdown 变动列表，并保留 Conventional Commits、签名和历史重写约束
```

## 代码入口

开始改动前，除权威文档外按需阅读相关实现与其测试：

- `cmd/galleryd`、`cmd/galleryctl`：进程入口与公开客户端；
- `internal/bootstrap`：启动顺序（AppDirs 锁 → 迁移 → 服务 → 监听 → descriptor）与关闭；
- `internal/platform/{appdirs,descriptor,lock,clock,identity,filesystem}`：平台适配；
- `internal/storage` 与 `internal/storage/migrations/{control,catalog}`：两库迁移；
- `internal/application`：资源、Binding、Binding issue、orphan candidate；
- `internal/rules`：规则包编译、三类 hash、extension 分类、CEL Profile；
- `internal/jobs`：Job Store 与有界 scheduler；
- `internal/scanner`、`internal/overlay`、`internal/creators`、`internal/catalog`、`internal/query`、`internal/media`、`internal/derived`、`internal/recovery`；
- `internal/transport/httpapi` 与 `internal/contract/api/openapi.yaml`（生成物在 `pkg/galleryapi`）。

## 当前可开工结论

阶段 0、Walking Skeleton 与 Architecture Proof 正确性切片已完成，当前在阶段 1。搜索排名/高亮/精确总数、游标租约、最终唯一约束、HDD/NAS 性能、Wails/Tauri、Linux/macOS/Docker 支持等仍是后续冻结或发行门禁。下一条正式垂直切片优先级为 control 备份/恢复与 SourceWork 拆分/合并，之后是阶段 1 Schema Freeze Gate；不要提前展开前端、LAN 完整账户、桌面壳或发行。
