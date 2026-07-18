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
- 当前仓库已有正式产品代码（`cmd/`、`internal/`、`pkg/`）。阶段 0 契约骨架、Walking Skeleton、Architecture Proof 正确性切片、阶段 1「领域和数据所有权」、阶段 2「规则闭环」与阶段 3「扫描、任务和 Catalog」均已完成代码与合成 Correctness 实现，并完成阶段 3 Correctness 修正。已落地并配套 API 的能力包括：Personal 配对/Session/capability、Library/Source/RuleVersion/SourceRuleBinding、限定同一父 Scan 的持久 Hash Job 与完整 SHA-256 publication、同一逻辑 Job 的多 Attempt/租约回收/退避重试、六类非阻塞有界调度池、Watcher dirty/overflow/动态 Source/失败重启与低频周期收敛、任务临时目录所有权、服务端维护空间估算与 publication 互斥，以及既有双 revision 查询、Overlay、Catalog 重建、八点强杀恢复、Canonical/Binding/规则/备份恢复等 Correctness 能力。尚未完成：真实 HDD、SMB/NAS、网络挂载和正式 Reference/Degradation Performance Gate，以及查询/媒体/安全/Web/平台发行等后续能力。
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
- **SourceRuleBinding 当前正式兼容基线是单生效规则**：按 active、受限条件匹配、priority、binding_id 稳定选择一条；同一 Source 同一 priority 由数据库拒绝，未匹配时返回稳定错误。多规则链、Provider 路由组合和多 Binding 合并执行仍未冻结，不得声称已支持。
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
4. 按领域/规则/扫描/查询与媒体/安全/Web/PWA/平台发行的顺序扩展。**（阶段 1、阶段 2、阶段 3 已完成代码与合成 Correctness；下一步阶段 4 查询和媒体）**

阶段 1 已完成。阶段 1 Schema Freeze Gate 冻结的是**核心领域身份与唯一约束**（不是最终物理数据库唯一约束）：`(source_id, source_key) WHERE status='active'`、`(work_id, ordinal)`、CanonicalWork 持久 ID 身份、Work/Creator/Media Binding 的 active/inactive/manual_unbound/orphan_candidate/orphaned 生命周期、同 Blob 多 occurrence、SourceWork 拆分/合并检测与结构决策 fingerprint 唯一、多 Source 隔离、Binding issue 指纹去重，登记于 control 迁移 `00016_schema_freeze_phase1` 的 `schema_freeze` 表（FROZEN）。SourceWork 决策的撤回仅适用于尚未被扫描消费的 pre-seed Binding；消费后返回结构化 `CONFLICT`，不执行已生效结构变化的完整反向操作。阶段 2 的 RulePackage canonical JSON 所有权、已发布版本不可变、草稿 revision CAS 和 Job 规则执行快照登记于 `00017_rules_lifecycle` 的 `schema_freeze` 表；Rule extension 注册表、单生效 Binding、参数最终命名空间、Impact 调度联动和完整表单 UI 保持 compatibility baseline。阶段 3 已增加并修正持久 Hash Job、同一 Job 多 Attempt、周期租约回收和退避重试、六类独立非阻塞资源池、动态 Watcher 与低频周期收敛、staging/publication、所有权 Temp GC、GC/VACUUM 服务端空间预检和外部执行边界，但真实 HDD、SMB/NAS、网络挂载与正式 Reference/Degradation Performance Gate 仍待下一轮实测。仍保持 pre-freeze/compatibility-baseline/deferred（未完成，但**不因此重开阶段 1 或阶段 2**）：FileLocation 在 SMB/inode/无 FileID 下的最终唯一约束、句柄式文件打开与 TOCTOU 进一步收紧、Blob 哈希算法升级、external ID 冲突最终策略、WorkOrigin 独立模型、完整 REST/过滤/排序/排名/高亮与 cursor 内部格式、显式且可撤销的 CanonicalWork merge/split、`split.bind_existing` 等已延后分支。这些最终物理约束的整体冻结属于「领域 Schema 最终冻结门禁」，见 `Documents/指南/02-测试与发布门禁.md`。阶段 2 已在既有有限原语/CEL/Rule IR 基础上完成规则产品闭环，阶段 3 不得重复实现第二套规则引擎。修改标记 FROZEN 的约束前须新增或修订 ADR。不要据此提前展开前端、LAN 完整账户、桌面壳或发行。

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

- 仓库已有正式构建与检查入口：根级 `Check.ps1`（委托 `scripts/Check.ps1`）执行 `go mod tidy -diff`、OpenAPI 生成一致性（`go generate ./...`）、gofmt、`go vet ./...`、`CGO_ENABLED=0 go test ./...` 与 `go build ./cmd/...`，`Check.ps1 -Race` 追加 `go test -race ./...`；也可直接运行 `go test ./...`、`go vet ./...`、`go build ./cmd/...`（`galleryd`/`galleryctl`）与 `govulncheck ./...`。Windows 本机 race 有 `WaitOnAddress` 限制，race 门禁在 Linux/WSL 执行。`go` 不在 PATH 时用 `GALLERY_GO` 指定工具链。不要另建重复脚本；Web/PWA 与可选壳的独立命令待相应阶段补充。
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

本节是仓库内唯一、强制、自包含的提交信息规范。任何 Agent 无需访问外部文件即可据此正确提交。仓库外的 `Gallery-Commit-Convention.md` 是与本节完全一致的更详细人类参考，只做展开解释、边界案例和正反示例，不得引入与本节冲突的规则；本节与外部文档冲突时以本节为准。发现规则与实际流程不符时优先更新本节，但不得放宽签名、只读 Source、测试或历史重写要求。

### 提交粒度

一个提交应代表一个可以独立解释、审查、验证和撤销的逻辑变动。一个合理提交通常同时包含实现同一能力所必需的生产代码、对应 migration、对应单元与集成测试、OpenAPI 或 Schema、由该契约生成的客户端，以及为保持规范同步所必需的少量文档；这些文件不因类型不同而机械拆开。

以下变动原则上并入其所属的逻辑提交，不单独成提交：

- 紧跟功能提交的 `gofmt`、拼写修复和遗漏 import；
- 只为修复前一提交测试而产生的小提交；
- OpenAPI 与由它生成的客户端；
- migration 与唯一依赖该 migration 的模型、仓储和测试；
- 同一功能的生产代码与直接测试；
- 同一轮统一术语、统一链接或统一阶段状态；
- 同一原因导致的大量文件新增或批量更新；
- 不能独立理解或独立撤销的零碎提交。

以下情况必须拆为不同提交：

- 两个彼此独立的正式能力；
- 功能实现与无关重构；
- 业务修复与大范围纯格式化；
- 数据迁移与无关文档整理；
- 多个可以独立撤销的领域变动；
- 一个提交同时跨越规则、查询、安全等多个无直接依赖的模块；
- 一个巨型提交包含多个可以分别验收的垂直切片。

除非确实能独立解释和撤销，否则不得单独提交：单个 `gofmt`、单个拼写修正、补一个遗漏测试、更新一处阶段状态、生成文件刷新、对上一提交的即时补漏，以及只有一两行且完全依赖前一提交的修改。真正独立的 bug 修复不得为整洁而埋回旧功能提交。

不得用一个提交笼统承载：整个阶段的所有独立子系统、多项互不依赖的 migration、多组不同 API、完全不同领域的重构与功能与文档，或任何无法用一句标题准确概括的变动集合。

提交前必须能明确回答：

1. 这个提交只有一个主要意图吗？
2. 能否用一句准确标题概括？
3. 独立撤销后，仓库是否仍保持逻辑一致？
4. 测试、migration 和生成文件是否与功能属于同一原子变动？
5. 是否存在应当并入本提交的尾随修复？
6. 是否存在可以独立拆出的无关能力？

### 唯一格式

每个正式提交都必须同时包含标题和正文，并严格使用：

```text
<type>(<scope>): <中文标题>

变动内容：
- `<仓库相对文件路径或文件组>`：<中文变动内容汇总>
- `<仓库相对文件路径或文件组>`：<中文变动内容汇总>
```

标题与正文之间必须恰好有一个空行。正文必须以顶格书写的 `变动内容：` 开始，其后至少包含一个列表项；不得使用 `changes:`、YAML 键值结构、二级列表或其他正文段落。列表标记必须是半角短横线和一个半角空格 `- `，不得直接使用 `·` 或 `•`。

### 标题

- 标题必须匹配 `^(feat|fix|refactor|perf|test|docs|build|ci|chore|revert)\([a-z0-9]+(?:-[a-z0-9]+)*\): .+$`，并严格采用 `<type>(<scope>): <中文标题>`。
- `type` 必须是下表允许的小写英文值；`scope` 必须始终填写，只能包含小写英文、数字和用于连接多词的单个半角连字符。
- `)` 后必须紧跟英文半角冒号，冒号后必须恰好一个半角空格；不得使用全角冒号。
- 标题必须使用动宾结构、包含中文并保持单行，准确说明主要实际变动，代码标识和必要技术术语可以保留英文，总长度不得超过 72 个字符。
- 阶段编号统一写作 `阶段 1`、`阶段 2`（数字前后各一个半角空格），不得写 `阶段一`、`阶段1`、`第 1 阶段`、`第一阶段` 或 `Phase 1`。
- 标题末尾不得出现句号、分号、冒号、问号、感叹号或其他结束标点，不得使用 Emoji、全角冒号或 `!`。
- 标题只概括提交的主要实际变动，不得描述测试结果、验收结果、提交数量、性能数字、Git 状态、动机或下一步。

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
- `scope` 必须是稳定领域或模块名，不得为一次性文件或单个函数创造临时 `scope`，也不得与 `type` 无意义重复（禁止 `docs(docs)`、`test(test)`、`ci(ci)`、`build(build)`）。
- 推荐取值：`repo`、`agents`、`core`、`config`、`contract`、`api`、`ws`、`auth`、`session`、`library`、`source`、`rules`、`scan`、`jobs`、`catalog`、`query`、`search`、`media`、`assets`、`storage`、`migration`、`backup`、`recovery`、`overlay`、`creator`、`binding`、`platform`、`security`、`cli`、`web`、`desktop`、`docs`、`guide`、`adr`、`test`、`ci`、`build`。
- 文档提交按实际修改的文档领域选择 `scope`，例如 `docs(agents)`、`docs(guide)`、`docs(adr)`、`docs(repo)`、`docs(api)`；不使用 `docs(docs)`。

### 固定阶段编号与术语

- 阶段编号统一使用 `阶段 0`、`阶段 1`、`阶段 2`、`阶段 3`、`阶段 4`、`阶段 5`、`阶段 6`、`阶段 7`，数字前后各一个半角空格；禁止 `阶段一`、`阶段1`、`第 1 阶段`、`Phase 1`、`第一阶段`。
- 提交消息与本规范统一使用仓库当前正式术语，包括 `Walking Skeleton`、`Architecture Proof`、`Correctness Gate`、`Schema Freeze Gate`、`RulePackage`、`RuleVersion`、`Rule IR`、`SourceRuleBinding`、`SourceWork`、`CanonicalWork`、`Binding issue`、`query publication`、`AppDirs`、`WebSocket`、`OpenAPI`；不得在同一语境随意翻译、缩写或另造近义写法。

### Markdown 正文

正文第一行必须恰好为：

```text
变动内容：
```

`变动内容：` 必须顶格书写，使用这四个中文字符和一个全角冒号，后面立即换行，不得在同一行附加内容，也不得替换为 `changes:`、`修改内容：`、`变更：` 或其他写法。

每个列表项必须独占一个物理行，并严格使用：

```text
- `<仓库相对文件路径或文件组>`：<中文变动内容汇总>
```

- 每行必须顶格，不得有前导空格；必须以半角 `- ` 开始。
- 路径或文件组必须放在一对反引号中，反引号后紧跟中文全角冒号 `：`，冒号后直接填写摘要，不增加额外空格。
- 路径必须是使用 `/` 分隔符的仓库相对路径，禁止绝对路径和用含义模糊的顶层目录概括代替具体文件。
- 列表项按路径（或文件组首路径）字典序排列，行之间不得插入空行。
- 摘要必须包含中文并说明该文件或文件组实际变动了什么，代码标识和必要技术术语可以保留英文；中文并列关系优先使用顿号或中文逗号；摘要末尾不得使用句号、分号、冒号、问号或感叹号。
- 正文不得使用 YAML、代码围栏、编号列表、表格、二级列表或额外段落。
- 正文不得描述修改动机、背景、测试是否通过、验收结果、性能数字、用户影响、Git 状态、提交哈希、下一步、方案理由或变动后的效果，也不得逐字复述 diff 或与标题重复大段说明。

### 文件分组

一个列表项可以是单个文件，也可以是因**同一原因、同一性质、可用同一句摘要准确描述**的一组文件。允许三种写法：

```text
- `internal/rules/import.go`：实现 JSON、YAML 和 TOML 严格导入
- `AGENTS.md`、`README.md`、`Documents/README.md`：统一阶段 2 完成状态与下一阶段入口
- `internal/rules/testdata/examples/*.json`：新增三类通用规则示例
```

- 1～5 个文件优先逐个列出或用中文顿号 `、` 显式并列；同类文件达到 6 个及以上、且完全同因、同质、可用同一句摘要准确概括时，才使用 glob 或 brace 分组，例如 `Documents/规范/{04-扫描-Catalog与任务.md,05-规则系统.md}`；即使达到 6 个，只要其中存在特殊变动，也必须把特殊文件单独列出，且 glob 或 brace 不得包含任何未变化文件。
- 只有同时满足以下条件才允许分组：文件因同一原因变化；使用完全相同的摘要仍然准确；分组不掩盖某个文件的特殊变动；glob 或 brace 表达式唯一、清楚地覆盖实际变动文件且不包含未变化文件。
- 生成源与生成物可以合并为一项，例如 ``- `internal/contract/api/openapi.yaml`、`pkg/galleryapi/openapi.gen.go`：扩展规则生命周期契约并同步生成客户端``。
- 新建大量结构一致的文件可按目录模式分组；存在特殊内容的文件必须单独列出。
- 禁止用 `internal/**`、`Documents/` 或“多个文件”这类模糊概括掩盖实际变动。

### 空格、标点与 Markdown

- 中文普通文字使用中文全角标点；代码、路径、命令、ID、hash、`type`、`scope` 使用 ASCII。
- 中文与英文技术名之间不强制机械插入空格，但同一提交消息必须保持一致。
- `阶段 1` 中数字前后各一个半角空格；Markdown 列表使用 `- `，不使用 `•`、`·` 或全角空格缩进。
- 路径必须置于反引号中并使用 `/` 分隔符；文件组路径之间使用中文顿号 `、`。
- 标题与正文摘要末尾不加句号；同一提交消息不得混用全角和半角冒号；不允许尾随空白，文件末尾保留一个换行。

### 新增、删除、重命名与生成文件

- 新增单个文件使用 ``- `new/path/file.md`：新增文件并记录接口使用说明``；批量新增同类文件按文件分组规则合并，例如 ``- `internal/rules/testdata/examples/*.json`：新增三类通用规则示例``，不逐个重复完全相同的“新增文件”。
- 删除文件使用 ``- `old/path/file.md`：删除废弃文件``。
- 重命名通常只列新路径，例如 ``- `new/path/file.md`：从 `old/path/file.md` 重命名并更新内容``；只有旧路径和新路径在提交后都真实存在时才分别列出。
- 生成文件与生成源可以合并描述，但不得只写“重新生成代码”，也不得用生成目录代替实际进入提交的文件或文件组。

### Merge、Revert 与不兼容变更

- Merge Commit 必须保留 Merge 结构，标题使用 `chore(merge): 合并 <中文分支说明>`，正文列出该 Merge 自身实际带入或解决冲突时修改的文件。
- Merge Commit 没有独立文件树变化时，唯一允许的特殊节点是 ``- `.`：合并提交历史``；`.` 不得用于其他提交。
- Revert Commit 使用 `revert(<scope>): 撤销 <中文原提交摘要>`，正文列出实际恢复的文件或文件组。
- Gallery 尚未发布稳定公共版本，当前禁止 `!` 和 `BREAKING CHANGE:`。未来确需不兼容变更时必须先更新本规范，历史重写不得添加二者。

### 签名、测试与历史重写

- 所有正式提交必须使用 SSH 密钥签名，创建提交时使用 `git commit --gpg-sign=~/.ssh/id_ed25519.pub` 或等效的显式 SSH 签名方式；仓库已配置 `commit.gpgsign=true` 时正常提交即会签名。
- 工作树是否干净分三个时间点判定：开始任务前工作树必须干净；创建某个提交前，暂存区只包含该提交的变动，不得存在与该提交无关的已暂存、未暂存或未跟踪文件；创建提交后若本轮没有计划内的下一个增量，工作树必须干净。
- 门禁（`Check.ps1`、`go test ./...`、`go vet ./...`、`go build ./cmd/...`）针对即将提交的完整文件树执行；不得把无关修改顺带提交，也不得提交无法构建或明显不一致的中间状态。
- 历史重写后的每个提交都必须重新签名，不得伪造旧签名或留下未签名提交；判断是否签名以 commit object 是否含 `gpgsig` 为准，不能仅凭 `%G?` 显示 `N` 就断言未签名。最终检查必须遍历全部历史确认每个提交都带 `gpgsig`。
- 历史重写允许 `reword` 修正标题与正文、`fixup`/`squash` 合并无意义尾随提交、`edit` 拆分巨型提交，以及在依赖关系安全时调整顺序；但不得改变最终文件树、丢失 migration/测试/生成代码/文档、修改历史 migration 内容，或为凑数字而机械合并互不相关的能力。
- 重写必须尽可能保留作者和提交者的姓名、邮箱与日期。执行前必须创建不可误覆盖的本地备份引用与仓库外 bundle，并记录原 HEAD、提交数量、提交图和工作树状态。
- 重写完成后必须证明新旧最终 tree SHA 完全一致再替换引用；如果环境无法为全部提交重新签名，或无法证明文件树逐提交一致，必须恢复原始历史并报告阻塞。历史重写只能在用户明确要求时推送，且只使用 `--force-with-lease`，不得使用裸 `--force`，不得自动创建 PR 或删除备份引用。

### 历史整理自检

历史整理或重写后必须自动复核，不能只看标题与正文格式：

- `docs` 提交只能修改文档或文档型契约；若包含实际测试语义或生产实现，改用与主要性质相符的 `test`、`fix`、`feat` 或其他类型。
- 不得保留纯 `gofmt`、即时补漏或单行状态纠偏的独立提交。
- 标题与正文格式正确不代表提交粒度正确，必须单独核对每个提交的单一意图。
- 大型提交必须按实际依赖评估能否拆成多个可构建、可测试的逻辑切片，并以逐提交构建结果证明。
- 「拆分困难」必须由依赖关系和逐步构建结果证明，不能只凭文件数量断言。

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
- `internal/backup`：control.db 一致性备份、manifest、恢复验证与启动期原子替换；
- `internal/transport/httpapi` 与 `internal/contract/api/openapi.yaml`（生成物在 `pkg/galleryapi`）。

## 当前可开工结论

阶段 0、Walking Skeleton、Architecture Proof 正确性切片、阶段 1「领域和数据所有权」、阶段 2「规则闭环」与阶段 3「扫描、任务和 Catalog」已完成代码与合成 Correctness（含 SourceWork 拆分/合并、阶段 1/2 Schema Freeze Gate、RulePackage 生命周期、参数/Binding 快照、规则调试契约、持久 Job/Hash/Watcher/维护闭环）。真实 HDD/SMB/NAS/网络挂载、正式 Reference/Degradation Performance、搜索排名/高亮/精确总数、游标租约、FileLocation 最终唯一约束、Wails/Tauri、Linux/macOS/Docker 支持等仍是后续冻结或发行门禁。下一条正式垂直切片是阶段 4 查询和媒体；不要提前展开前端、LAN 完整账户、桌面壳或发行。
