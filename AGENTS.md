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
- 当前仓库已有正式产品代码（`cmd/`、`internal/`、`pkg/`）。阶段 0 契约骨架、Walking Skeleton、Architecture Proof 正确性切片、阶段 1「领域和数据所有权」、阶段 2「规则闭环」、阶段 3「扫描、任务和 Catalog」与阶段 4「查询和媒体」均已完成代码与合成 Correctness 实现，并完成阶段 3 Correctness 修正与真实 SSD/HDD 大数据集结构抽样、只读清单和有界样本验收引发的扫描档案重构（全量扫描未完成，正式全量性能 Gate 仍未通过；见 [验证记录 EV-25](Documents/证据/验证记录.md)）。已落地并配套 API 的能力包括：Personal 配对/Session/capability、Library/Source/RuleVersion/SourceRuleBinding、按 `scanProfile`（`index`/`incremental`/`verify`，未显式指定时按 Source 是否已有 publication、以及 control.db 是否保留该 Source 的持久领域历史（Binding/Binding issue/SourceWork 结构决策）自动选择 `index` 或 `incremental`，避免 Catalog 删除重建后被误判为全新 Source）区分的持久 Hash Job 与完整 SHA-256 publication（`incremental` 按 Source/相对路径/大小/mtime 组合证据跨扫描复用既往已确认摘要且只在真正完成完整哈希时推进确认时间，`index` 发布 `located_unverified` 媒体但不建立 ContentBlob，已有 publication 或 Catalog 已丢失但仍有持久领域历史的 Source 显式请求 `index` 会被拒绝）、同一逻辑 Job 的多 Attempt/租约回收/退避重试、六类非阻塞有界调度池、Watcher dirty/overflow/动态 Source/失败重启与低频周期收敛、任务临时目录所有权、服务端维护空间估算与 publication 互斥、既有双 revision 查询、Overlay、Catalog 重建、八点强杀恢复、Canonical/Binding/规则/备份恢复等 Correctness 能力，以及服务端权威结构化查询字段注册表（AND/OR/NOT，含 `overlay.favorite`/`overlay.hidden`/`overlay.progress`）、Creator 合并查询等价组解析、字段级 Ranking v2（标题/Creator/Tag/文件名 `match_class × field_priority` 组合元组）、通用版本化命中表达 `matches`、Total 协议、签名 keyset 游标 rank 扩展、Overlay 字段能力注册表与按查询实际生成的动态 dependency set planner、目标化单媒体按需内容确认（不再触发整个 Source 的 `verify`，执行阶段真正校验冻结的 MediaID/ObservationFingerprint，且 EV-34 修正为 VerificationTarget 显式冻结实际使用的 `queryPublicationId`、媒体身份与 observation 均从同一个已确认为 active 的 publication 解析、执行阶段读取该冻结 publication 而非任意执行时刻的 active publication、幂等键随 `queryPublicationId` 变化、前置身份不匹配统一为不可重试的 `VERIFICATION_TARGET_MISMATCH`）、媒体与 DerivedAsset 的 `queryPublicationId` 快照绑定读取（含专用 publication 读取租约）、独立 `media.derive` capability 与媒体 If-Range、DerivedAsset 异步输入跨 revision 内容寻址解析并受非终态 Job 状态与 `media.BlobReadLease` 双重保护（不依赖固定租约 TTL 覆盖排队/退避/生成耗时）、catalog v9→v10 查询快照列启动期回填只在驱动既有或新建 Overlay 投影 Job 真正 `completed` 后才标记完成、通用命中表达 `matches` 截断边界安全性、多值查询投影分隔符碰撞的权威边界拒绝，均已完成阶段 4 压力测试前代码 Correctness 收尾（见 [验证记录 EV-31](Documents/证据/验证记录.md)、[EV-32](Documents/证据/验证记录.md)、[EV-33](Documents/证据/验证记录.md)、[EV-34](Documents/证据/验证记录.md)），随后于 2026-07-21～23 执行阶段 4 首轮正式压力测试：1M/10M 实测（见 [验证记录 EV-35](Documents/证据/验证记录.md)）确立 500,000 WorkProjection 为推荐正式验证规模、`≥1,000,000` 降级为非推荐诊断场景；重构后的可复用测试框架 `tools/testlab` 完成 500,000 规模验证与全部 10 个目标来源的规则包及有界真实 Source 验证（见 [验证记录 EV-36](Documents/证据/验证记录.md)），其中 500,000 规模 Correctness/Cursor 全部通过、Perf 矩阵在预算内完成但 `wide-cjk`/结构化过滤等类别仍有已知未修复的架构性延迟（Reference Performance Gate 仍未通过），10 个目标来源中 8 个完成有界真实 Source 验证、Gank 与 Pawchive 因有界子目录抽样算法未命中候选而未完成验证。尚未完成：真实全量规模 HDD 性能特征、SMB/NAS、网络挂载、真实平台文件身份（FileID/`dev+inode`）接入、正式 Reference/Degradation Performance Gate、ranking 权重/total 预算/cursor 及 publication 读取租约时长等 PRE_FREEZE 数值冻结、AND/OR 子项 canonical 化、Progress 排序，以及安全/Web/平台发行等后续能力。
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
- 根目录 `README.md` 和 `PROJECT_STATUS.md` 是面向普通用户和潜在贡献者的项目介绍与阶段摘要，用于快速了解现状，不是规范、ADR、实施计划或验证记录的替代品，不承载它们的权威语义；两者的定位、触发同步的情形和更新方式见「文档维护」一节。

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
4. 按领域/规则/扫描/查询与媒体/安全/Web/PWA/平台发行的顺序扩展。**（阶段 1、阶段 2、阶段 3、阶段 4 已完成代码与合成 Correctness；下一步阶段 5 账户、安全和多客户端，以及阶段 4 正式压力测试/API Freeze 审计）**

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

### 第三方材料与依赖安全

- 仓库中直接复制、改编或内嵌的第三方源码、字体、图片等资产必须保留原始版权与许可证声明，并登记在 `THIRD_PARTY_NOTICES.md`；不得把第三方文件重新声明为项目原创的 AGPL 材料。
- `go.mod`/`package.json` 等 manifest 中声明的依赖关系是外部依赖，不等同于仓库复制了其源码；两者的合规处理方式不同，不得混淆。
- 新增依赖前检查其许可证与仓库当前许可证的兼容性，标记未知许可证、强 copyleft 或条款不兼容的候选，不得默认接受。
- Dependabot 等安全告警必须按 `manifest_path` 与该依赖是否真正被构建、测试、发行或运行使用来分类，不得仅凭目录名（例如 `Test-Bench`）判断告警可以忽略。
- 不得批量、无具体理由地 dismiss 告警；确认某依赖不进入正式构建、测试、发行或运行后，dismiss 理由必须写明对应 manifest 路径和依据。历史实验依赖若仍被 CI 或人工流程实际使用，必须升级而不是 dismiss。
- 不得为制造“无告警”的表面状态而关闭 Dependabot alerts、secret scanning、push protection 或 private vulnerability reporting 等已启用的安全功能。

## 固定工具链与多环境调用

本节是后续 Agent 在 Windows 原生、Git Bash/MSYS、WSL2 Debian 和 GitHub Actions 四种环境下解析和调用 Go 工具链的唯一权威规则，优先于任何“PATH 中找不到 `go` 就判定工具链缺失并自动安装”的默认行为。

### 版本基线

当前项目固定使用 `Go 1.26.5`：

- 本地环境统一设置 `GOTOOLCHAIN=local`，不允许 Go 自动下载其他 toolchain，也不得静默改用系统中其他版本；
- 执行测试前必须打印并记录实际 `go version`；
- 若实际版本不是 Go 1.26.5，应停止对应门禁并报告，不能继续生成混合版本结果。

### Windows 原生环境

仓库路径为 `D:\GitHubRecRivenVI\Gallery`。Windows 正式 Go 可执行文件固定为：

```text
C:\Users\RavenYin\AppData\Local\CodexToolchains\go1.26.5\go\bin\go.exe
```

PowerShell 中必须显式设置：

```powershell
$env:GALLERY_GO = "C:\Users\RavenYin\AppData\Local\CodexToolchains\go1.26.5\go\bin\go.exe"
$env:GOTOOLCHAIN = "local"
```

验证方式：

```powershell
if (-not (Test-Path -LiteralPath $env:GALLERY_GO -PathType Leaf)) {
    throw "固定 Windows Go 工具链不存在：$env:GALLERY_GO"
}
& $env:GALLERY_GO version
```

Windows 下运行仓库门禁时优先使用 `.\Check.ps1`；直接调用 Go 时必须使用 `& $env:GALLERY_GO test ./...`、`& $env:GALLERY_GO vet ./...`、`& $env:GALLERY_GO build ./cmd/...`、`& $env:GALLERY_GO run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...`，不得仅执行裸 `go test ./...` 后因为 `go` 不在 PATH 就判断工具链缺失。

### Windows Git Bash / MSYS 环境

Git Bash 中 Windows Go 的固定可执行文件为：

```text
/c/Users/RavenYin/AppData/Local/CodexToolchains/go1.26.5/go/bin/go.exe
```

需要在 Bash 中直接调用 Windows Go 时：

```bash
export GOTOOLCHAIN=local
GALLERY_GO="/c/Users/RavenYin/AppData/Local/CodexToolchains/go1.26.5/go/bin/go.exe"
test -x "$GALLERY_GO" || { echo "固定 Windows Go 工具链不存在：$GALLERY_GO" >&2; exit 1; }
"$GALLERY_GO" version
```

PowerShell 脚本门禁仍应通过 `pwsh` 调用，并向其传递 Windows 格式的 `GALLERY_GO`：

```bash
export GALLERY_GO='C:\Users\RavenYin\AppData\Local\CodexToolchains\go1.26.5\go\bin\go.exe'
export GOTOOLCHAIN=local
pwsh -NoProfile -File ./Check.ps1
```

不得把 Windows Go 路径误认为 WSL Linux Go。

### WSL2 Debian 环境

WSL 发行版固定为 `Debian`，仓库路径为 `/mnt/d/GitHubRecRivenVI/Gallery`。WSL 用户级 Go 工具链固定为 `$HOME/go-sdk/bin/go`（工具链根目录 `$HOME/go-sdk`）。每次运行 WSL Go 命令前必须显式执行：

```bash
export PATH="$HOME/go-sdk/bin:$PATH"
export GOTOOLCHAIN=local
```

验证方式：

```bash
test -x "$HOME/go-sdk/bin/go" || { echo "固定 WSL Go 工具链不存在：$HOME/go-sdk/bin/go" >&2; exit 1; }
"$HOME/go-sdk/bin/go" version
```

WSL race 所需 GCC 固定为 `/usr/bin/gcc`，验证方式：

```bash
test -x /usr/bin/gcc || { echo "WSL GCC 不存在：/usr/bin/gcc" >&2; exit 1; }
/usr/bin/gcc --version | head -1
```

正式 WSL race 调用模板：

```powershell
wsl.exe -d Debian -- bash -lc '
  set -euo pipefail
  export PATH="$HOME/go-sdk/bin:$PATH"
  export GOTOOLCHAIN=local
  test -x "$HOME/go-sdk/bin/go"
  test -x /usr/bin/gcc
  go version
  gcc --version | head -1
  cd /mnt/d/GitHubRecRivenVI/Gallery
  CGO_ENABLED=1 go test -race ./...
'
```

定向 race 只需替换最后一行的包路径，例如 `CGO_ENABLED=1 go test -race ./internal/rules/... ./internal/scanner/...`。不得仅运行 `which go` 或 `command -v go` 并因普通 PATH 中找不到 Go 就判定工具链不存在；必须先检查 `test -x "$HOME/go-sdk/bin/go"`。

### Windows race 限制

原生 Windows Go race runtime 在当前环境中存在已记录的运行时兼容问题（`0xc0000139`、`WaitOnAddress` 导出缺失）。因此：

- Windows 原生只负责普通 test、vet、build 和 `CGO_ENABLED=0` 门禁；
- 正式 `go test -race` 必须在 WSL2 Debian 中执行；
- 不得在 Windows race 失败后尝试重新安装 Go；
- 不得把 Windows race 工具链问题误判为项目代码竞态；
- WSL race 结果不等于 Linux 原生 ext4 平台支持，因为仓库实际位于 `/mnt/d` 的 DrvFS/v9fs。

### GitHub Actions 环境

GitHub Actions 不使用本地硬编码用户路径，使用 `actions/setup-go` 安装 Go 1.26.5 并设置 `GOTOOLCHAIN=local`；Ubuntu runner 使用 runner 已有的 GCC 执行 race。本地固定路径规则不得写进 CI workflow；CI 不得依赖 `C:\Users\RavenYin` 或 `$HOME/go-sdk`；本地和 CI 必须输出实际 Go 版本；workflow 中的版本必须与 `go.mod` 和本节保持一致。

### 工具链解析顺序

**Windows**：检查固定路径 `C:\Users\RavenYin\AppData\Local\CodexToolchains\go1.26.5\go\bin\go.exe` → 设置 `GALLERY_GO` → 执行该可执行文件的 `version` → 使用 `Check.ps1` 或 `& $env:GALLERY_GO ...` → 固定路径不存在时停止并报告。不得先依赖 PATH 中的 `go`。

**WSL**：检查 `$HOME/go-sdk/bin/go` → 将 `$HOME/go-sdk/bin` 放到 PATH 最前 → 检查 `/usr/bin/gcc` → 打印版本 → 进入 `/mnt/d/GitHubRecRivenVI/Gallery` → 执行 race → 固定路径不存在时停止并报告。不得扫描整个根文件系统寻找 Go，不得自动安装系统级 Go，不得因 `command -v go` 在设置 PATH 前失败就安装 Go，不得将 Windows `.exe` 用作 WSL race 工具链，不得自动从网络下载新版本。

### 缺失工具链时的处理

若任一固定路径确实不存在：保存准确命令和错误输出；列出预期路径的父目录（Windows 示例 `Get-Item -LiteralPath "C:\Users\RavenYin\AppData\Local\CodexToolchains\go1.26.5\go\bin" -ErrorAction SilentlyContinue`，WSL 示例 `ls -la "$HOME/go-sdk/bin" 2>/dev/null || true`）；检查是否只是环境变量或 PATH 未设置；不修改系统环境；不执行安装；继续完成所有不依赖该工具链的安全工作；最终将其报告为环境阻塞。禁止使用无界 `find /` 或递归扫描整个磁盘寻找工具链，禁止未经用户明确授权执行 `sudo apt install`、`sudo rm -rf /usr/local/go`、`curl ... | sudo tar ...`、`winget install`、`choco install` 等系统级工具链安装或升级操作。

## 文档维护

### 文档职责与权威顺序

- `Documents/README.md` 是工程文档的唯一导航入口；不要恢复多轮调研报告或另建历史归档目录。
- 产品定义、领域模型、扫描、规则、查询、API、安全、文件系统和跨平台等主题各有唯一权威规范（`Documents/规范/*.md`），其他文档只链接，不复制长段结论；有必要调整语义时只修改该唯一权威规范，不得为同一主题另写"最终方案"。
- ADR 记录决策、理由、替代方案、影响和重新审议条件；状态只以 `Documents/ADR/README.md` 索引为准；改变已接受决策时同步该 ADR 与索引。
- `Documents/指南/01-v1实施计划.md` 定义开发顺序、阶段、冻结点和下一步；阶段推进、范围或冻结状态变化时更新。
- `Documents/指南/02-测试与发布门禁.md` 定义验收要求、门禁状态和支持声明条件；门禁定义、状态或平台矩阵变化时更新。
- `Documents/证据/验证记录.md` 只保存实际证据：数据、环境、结果、局限和需要重测的门禁；取得新验证结果时新增条目，新证据推翻旧结论时必须同时修正所有仍引用旧结论的其他文档摘要和状态，不得只追加新证据而不改正文。
- 根目录 `README.md` 与 `PROJECT_STATUS.md` 是用户向摘要，不得覆盖或重新定义规范、ADR、实施计划或验证记录；两者的定位、触发情形和更新方式见下文「`README.md` 与 `PROJECT_STATUS.md` 的定位与同步」。
- `AGENTS.md` 本身同时承载 Agent 行为规则、当前开发状态、开发顺序和当前可开工结论，必须随事实进度更新，但不得放宽安全、只读 Source、Git、签名或测试要求，也不得把临时实装写成已冻结决策。
- `Test-Bench/` 下的 README、源码和结果是历史实验材料，记录的是实验当时的真实结论；与当前规范冲突时以当前规范为准，但不得因此把历史 README 改写成当前规范，也不得抹去实验当时的真实结果。
- 功能实现结论以事实代码为准，测试或门禁完成结论以 `Documents/证据/验证记录.md` 的正式验证记录为准；当前阶段、下一步和未完成项不得只靠旧报告或模型记忆判断，须对照实际代码、Git 历史和验证记录确认。
- 实施进度、一次性测试日志和修复汇报不进入以上权威文档；由代码、测试、Issue 和 Git 历史承担。
- 同一主题只能有一个权威来源，不得另建重复"最终方案"。

### 仓库级文档影响检查

完成一个编号阶段、命名切片、跨模块正式能力、门禁变化或发行状态变化后（触发条件见下节「触发检查的开发变化」），Agent 必须建立一份"文档与仓库元数据影响清单"，逐项检查以下全部对象，不得只检查 `README.md` 与 `PROJECT_STATUS.md`。

#### 当前状态与用户文档

```text
AGENTS.md
README.md
PROJECT_STATUS.md
```

检查内容至少包括：当前阶段、已完成能力、测试与门禁状态、当前可用性、主要缺口、下一条正式切片、技术栈、用户可感知特色、UI/安装包/发行状态、PRE_FREEZE 与延期/支持矩阵。三者之间不得出现互相矛盾的阶段状态或完成度表述；具体更新规则见下文「`README.md` 与 `PROJECT_STATUS.md` 的定位与同步」。

#### 权威工程文档

```text
Documents/README.md
相关 Documents/规范/*.md
Documents/ADR/README.md
相关 Documents/ADR/*.md
Documents/指南/01-v1实施计划.md
Documents/指南/02-测试与发布门禁.md
Documents/证据/验证记录.md
```

按变化类型对应处理：行为语义变化改唯一权威规范；已接受决策变化新增或修订 ADR 并同步索引；阶段、范围、顺序、冻结状态或下一步变化改实施计划；门禁定义、状态、支持条件或平台矩阵变化改测试与发布门禁；取得实际验证结果新增验证记录条目；新证据推翻旧结论时不仅追加新证据，还必须修正所有仍引用旧结论的摘要和状态。

#### 贡献、支持与社区文档

```text
CONTRIBUTING.md
SECURITY.md
THIRD_PARTY_NOTICES.md
LICENSE
存在时的 CODE_OF_CONDUCT.md
存在时的 SUPPORT.md
存在时的 GOVERNANCE.md
```

- `CONTRIBUTING.md`：当前开发目标、工具链、构建测试入口、贡献流程、架构约束或 PR 要求变化时检查；
- `SECURITY.md`：支持版本、威胁面、报告方式、部署形态、账户模型、安全能力或响应流程变化时检查；
- `THIRD_PARTY_NOTICES.md`：第三方直接材料新增、删除、替换、升级、移动或许可证变化时检查，不得只追加而保留已失效的条目；
- `LICENSE`：只有许可证、版权主体、授权范围或重新许可发生变化时修改；
- 行为准则、支持政策和治理文档存在时，社区结构、维护者、支持范围或治理流程变化后检查；当前仓库均不存在这三份文档，不得假设其已存在，也不得替它们编造内容。

#### GitHub 社区模板

```text
.github/ISSUE_TEMPLATE/config.yml
.github/ISSUE_TEMPLATE/*.yml
.github/PULL_REQUEST_TEMPLATE.md
```

- 产品出现 UI、安装包、正式版本或新客户端后，检查 Bug Form 中的环境、版本、复现和模块字段；
- API、扫描模式、模块名称或用户群变化后，检查表单字段是否仍适用；
- 安全报告渠道变化后，检查 Issue Form 和 `config.yml`；
- 产品边界变化后，检查 Feature Form 的约束；
- 文档职责变化后，检查 Documentation Form；
- 开发流程、门禁或文档影响要求变化后，检查 PR 模板；PR 模板不得只要求勾选"已检查 README 与 PROJECT_STATUS"，本节要求的仓库级影响清单发生变化时应据此评估是否需要修订模板本身的检查项。

本节只在 `AGENTS.md` 中建立以上检查要求，不代表模板本身已按此改写；模板文件只在被本节触发条件命中且确有需要时单独修改。

#### 其它工程和历史说明

```text
tests/README.md
Test-Bench/**/README*.md
OpenAPI 或其它公开契约说明
生成流程说明
构建与 CI 说明
```

仅在实际测试入口、实验用途、契约、生成方式或环境要求变化时检查和更新；不得把历史实验 README 改写成当前规范，也不得因为当前实现变化而抹去实验当时的真实结果。

#### 仓库非文件元数据

```text
Repository Description
Repository Topics
Private vulnerability reporting
Dependabot alerts
Secret scanning
Push protection
```

- 产品定位、主要能力、成熟度或核心技术栈变化时检查 Description 和 Topics；不再是 pre-alpha、获得 UI、安装包、正式发行或跨平台支持时必须检查 Description；Topics 由 GitHub 决定显示顺序，不得把顺序当作稳定语义；
- 安全能力启停变化时同步检查 `SECURITY.md`；不得为制造"无告警"的表面绿色状态而关闭 Dependabot alerts、secret scanning、push protection 或 private vulnerability reporting 等已启用的安全功能；
- 这些元数据不产生 Git diff，但必须在最终交付报告中说明是否检查、是否更新以及实际结果，不得因为没有 diff 就跳过。

### 触发检查的开发变化

以下变化后必须执行上述仓库级文档影响检查：

- 编号阶段完成或进入新阶段；
- Walking Skeleton、Architecture Proof 或其它命名切片完成；
- Correctness 收口；
- Freeze Gate、Reference Performance Gate、Degradation Gate 或平台门禁状态变化；
- 新增或完成跨模块的正式能力；
- 公开 API、数据模型、migration、查询、媒体、扫描、规则、任务、恢复或授权语义显著变化；
- 新增用户可感知能力；
- UI、Web/PWA、桌面客户端、安装包或正式发行状态变化；
- 账户、安全、LAN、多客户端或远程访问模型变化；
- 支持版本、平台矩阵或兼容性承诺变化；
- 工具链、构建、测试、CI、代码生成或贡献流程变化；
- PRE_FREEZE、延期、半成品或未完成事项状态变化；
- 新验证证据改变完成度结论，或旧验证结论被推翻；
- 新增、删除或替换第三方直接材料，或依赖许可证、安全告警、许可证策略变化；
- 文件重命名、术语变化或文档职责调整；
- 仓库定位、Description 或 Topics 所表达的事实发生变化。

局部内部重构、拼写修正或不影响任何现有描述的 bug 修复不要求机械修改所有文档，但仍必须执行影响检查并说明结论，不得默默跳过判断。

### 文档与仓库元数据影响矩阵

| 变化类型 | 必查对象 |
| --- | --- |
| 阶段或命名切片完成 | `AGENTS.md`、`README.md`、`PROJECT_STATUS.md`、实施计划、测试门禁、验证记录、`CONTRIBUTING.md` |
| 新验证证据或旧结论被推翻 | 验证记录、`PROJECT_STATUS.md`，必要时 `README.md`、`AGENTS.md`、实施计划和测试门禁 |
| 公开行为或架构语义变化 | 相关规范、ADR、OpenAPI/契约说明、`AGENTS.md`、用户文档、`CONTRIBUTING.md` |
| 安全、账户或部署模型变化 | 安全相关规范、ADR、`SECURITY.md`、`README.md`、`PROJECT_STATUS.md`、Issue Forms |
| UI、安装包或正式发行变化 | `README.md`、`PROJECT_STATUS.md`、`AGENTS.md`、`CONTRIBUTING.md`、`SECURITY.md`、Issue Forms、Description、Topics |
| 工具链、测试或贡献流程变化 | `AGENTS.md`、`CONTRIBUTING.md`、PR 模板、相关测试/构建说明和 CI |
| 第三方直接材料变化 | `THIRD_PARTY_NOTICES.md`、`CONTRIBUTING.md`，必要时 `README.md` 和 `LICENSE` |
| 产品定位或主要技术栈变化 | `README.md`、`PROJECT_STATUS.md`、`AGENTS.md`、Description、Topics |
| 文档路径或职责变化 | 所有引用方、Issue Forms、PR 模板、`Documents/README.md` 导航 |

"必查"不等于"必须修改"：逐项检查是否实际受影响，只有内容确实变化才修改；未受影响的项也必须在交付报告中标记"已检查，无需更新"，不得因为不修改就省略检查本身。

### 更新方式：重写与有序追加

#### 必须重写受影响区域

以下文档表达的是"当前真相"，内容变化时必须重新编写受影响区域，不得打补丁式追加：

```text
AGENTS.md
README.md
PROJECT_STATUS.md
CONTRIBUTING.md
SECURITY.md
Documents/指南/01-v1实施计划.md
Documents/指南/02-测试与发布门禁.md
Documents/规范/*.md
Issue Forms
Pull Request 模板
Repository Description
Repository Topics
```

不得：

- 在文末追加"现在已经完成"、新证据链接或日期说明来掩盖正文仍然过时的问题；
- 保留旧状态表，再在下方补一句更正段落；
- 只添加新证据链接而不修正被该证据推翻的旧结论；
- 让新旧阶段摘要同时存在，或让 README 总体表和 `PROJECT_STATUS.md` 总体表出现不同状态；
- 用注释解释正文已经过时，而不修正正文本身；
- 只改一张总体表而让其它章节保留旧状态；
- 在 `AGENTS.md` 中不断追加新规则而不整合已有的重复区域，让"文档维护"内部出现多套互相重叠的要求。

正确做法：找到所有受变化影响的标题、摘要、表格行、状态、限制、下一步、链接、示例、表单字段和检查项；重新编写这些区域；删除或改写已经过时的表述；合并重复信息；同步更新有意重复的总体进度表（例如 README 与 `PROJECT_STATUS.md` 的总体进度表）；保留历史演进时明确写出旧结论被何时、因何证据修正；使文档在不阅读末尾追加说明的情况下也能直接得到当前正确结论。

#### 允许有序追加的情况

只有文档结构本身属于累计记录时，才允许有序增加新条目：

- `Documents/证据/验证记录.md` 中新增带编号、环境、结果和局限的新验证条目；
- ADR 中新增一项独立决策，并同步 ADR 索引；
- `THIRD_PARTY_NOTICES.md` 中新增一项真实第三方材料记录；
- 存在时的 changelog 或 release notes 中新增对应版本记录。

即使允许追加，也必须同时检查并重写：被新证据推翻的旧摘要；已失效的状态表；已删除或替换的第三方材料条目；ADR 索引和当前状态；其它引用该结论的文档。不得把"有序追加"解释为只增不删或只在文末打补丁。

#### `README.md` 与 `PROJECT_STATUS.md` 的定位与同步

- 根目录 `README.md` 是面向普通用户和潜在贡献者的项目首页：产品定位、特色功能、技术栈、当前可用性提示和总体进度摘要。
- 根目录 `PROJECT_STATUS.md` 是面向一般读者的完整项目状态、测试门禁、限制和后续路线汇总，包含逐阶段的细粒度功能状态与测试状态。
- 两者都是**用户向摘要**，不是规范、ADR、实施计划或验证记录的替代品：功能实现结论以事实代码为准，测试或门禁结论以 `Documents/证据/验证记录.md` 为准；若两者与权威规范、实施计划、ADR 或验证记录冲突，应修正这两份用户向文档，不得让它们覆盖或重新定义权威来源。
- README 中的总体进度表是 `PROJECT_STATUS.md` 总体进度表的简明副本：阶段状态、Emoji、最大缺口和下一步必须与 `PROJECT_STATUS.md` 保持一致；README 可以更简洁，但不得对完成度作更乐观的表述。`PROJECT_STATUS.md` 必须保留更细的逐阶段功能与测试双状态表格。README 中列出的特色功能必须能在事实代码和 `PROJECT_STATUS.md` 中找到依据；在尚无 UI、安装包或正式发行版本之前，不得写出"可安装""可日常使用"等使用就绪的措辞。
- 大型开发完成后不得打补丁式追加，须按上文「必须重写受影响区域」整体重写受影响章节；小型改动仍须显式判断是否需要同步，即使最终判断无需改动，也应在任务过程中明确说明已经检查过这两份文档，不得默默跳过判断。

### 交付前文档检查结果

大型开发任务完成时，最终报告必须提供逐项文档影响清单，每项只能标记为「已更新」「已检查，无需更新」「不适用」「无法确认」之一：

```text
AGENTS.md
README.md
PROJECT_STATUS.md
Documents/README.md
相关规范
相关 ADR 与 ADR 索引
实施计划
测试与发布门禁
验证记录
CONTRIBUTING.md
SECURITY.md
THIRD_PARTY_NOTICES.md
LICENSE
Issue Forms
PR 模板
tests/Test-Bench 相关 README
Repository Description
Repository Topics
仓库安全功能
```

要求：「无法确认」必须说明缺少的事实；不得默默跳过某项；不得用"已检查相关文档"一句话代替逐项结果；仅在小型且明显不涉及多数文档的任务中可以按类别合并，但必须明确列出实际检查范围；即使全部判断无需修改，也必须在报告中说明。

## Git 与交付

- 修改前后检查 `git status --short`，保留并绕开用户已有改动；工作树干净的三个判定时间点见“签名、测试与历史重写”一节。
- 不使用 `git reset --hard`、`git checkout --` 等破坏性回退；只撤销本轮明确产生的内容。
- 优先级：系统与安全约束优先于本节任何规则；用户本轮给出的明确边界（例如“不要提交”“不要推送”“只改工作树”“执行历史重写”）优先于下述默认交付流程；用户未明确指定时，才适用本节默认行为。
- 所有提交信息必须遵循“Git Commit Message 规范”一节；该节是仓库内唯一、强制的提交信息格式。
- 未经用户明确要求，不创建 PR，也不删除 cleanroom 大型历史结果；用户明确要求创建 PR 时，在推送完成后按其要求创建。历史重写只在用户明确要求历史重写任务时启用，遵循“签名、测试与历史重写”一节的专门流程，不适用下述普通任务的推送规则。

### 默认交付流程

用户未明确禁止提交或推送的普通开发或文档任务，按以下阶段推进，前一阶段完成才进入下一阶段：

1. 完成本轮请求范围内的代码或文档修改（本地工作完成）；
2. 执行适用门禁：涉及生产代码、测试、migration、OpenAPI、生成物或 Go 依赖的任务执行完整 `Check.ps1`；仅修改纯文档且不改变代码、契约、生成物或构建配置时，执行轻量门禁——`git diff --check`、Markdown 结构与引用检查、提交消息格式审查，以及必要时核对文档引用的路径或命令确实存在；
3. 创建提交；
4. 使用普通 fast-forward push 推送本轮产生的提交；不得推送与本轮无关的提交或其他分支；
5. 跟踪该次推送后 HEAD 对应的 GitHub Actions 至完成状态（见下方“GitHub Actions 跟踪”）；
6. 汇报并停止。

第 1～2 阶段是“本地工作完成”；只有完成全部 6 个阶段才是“完整交付完成”。用户明确要求“只改工作树”时止步于第 1 阶段；明确要求“不提交”时止步于第 1～2 阶段；明确要求“不推送”时可以提交但止步于第 3 阶段，不得推送。

### GitHub Actions 跟踪

- 跟踪目标是本轮实际推送的本地 HEAD；推送后必须确认 `git rev-parse HEAD` 与 `git rev-parse origin/<当前分支>` 一致，再据此 SHA 查找对应 workflow run，不得读取与该 SHA 无关的最新 run。
- Workflow run 可能尚未出现，应在合理时间内轮询等待；若同一 SHA 触发多个 workflow，须等待全部相关 run 结束，任一 run 为 `failure`、`cancelled`、`timed_out` 或 `action_required` 均判定整体失败；全部 run 为 `success`（`neutral`/`skipped` 不计入失败）才判定整体成功。
- 若 GitHub API、CLI 或连接器无法取得状态，应如实记录“无法取得状态”，不得伪造成功或假设已通过。
- 只允许普通 fast-forward push；禁止裸 `--force`；本类普通任务也不得使用 `--force-with-lease`（历史重写场景的受限 `--force-with-lease` 用法见“签名、测试与历史重写”一节，二者不冲突、不得混用）。“主动推送”只指推送本轮任务自身提交的普通 fast-forward push，不得被解释为允许强制推送、创建 PR 或推送无关提交。
- 完成 Actions 检查后，无论成功、失败还是无法取得状态，都不得因此继续修改代码、测试、文档、提交或历史，也不得自行追加修复、重跑 workflow、推送新的 SHA 或重写历史，除非用户另行明确下令。
- 最终报告中的 Actions 部分三选一记录：成功；失败并附失败步骤的最小相关日志片段；或“无法取得状态”。不得在该部分继续提出或执行修复方案、给出建议或描述后续计划。

## Git Commit Message 规范

本节是仓库内唯一、强制、自包含的提交信息规范，无需访问仓库外文件即可执行。系统与安全约束、用户本轮明确边界、`Git 与交付` 的授权和推送限制优先于本节；在不冲突时，本节对提交格式、粒度、签名、验证和历史重写具有唯一解释权。任何例外都必须先修改本节，不得从旧提交、外部参考或错误实践反推规则。

### 提交粒度

一个提交代表一个能够独立解释、审查、验证和撤销的逻辑切片。提交边界由语义和依赖决定，不由文件数量或文件类型决定。

同一逻辑切片通常同时包含：

- 生产代码；
- 直接测试；
- 必要 migration；
- OpenAPI 或 Schema；
- 对应生成物；
- 必要且直接相关的文档。

#### 必须合并

以下内容通常必须并入其所属提交：

- 功能与其直接测试；
- 错误修复与复现该错误的回归测试；
- migration 与唯一依赖该 migration 的模型和测试；
- OpenAPI 与由它生成的客户端；
- 同一协议变动的 Schema、错误码和 handler；
- 同一功能产生的必要规范同步；
- 即时 `gofmt`、import、拼写和尾随补漏；
- 不能独立解释或撤销的零碎变动。

#### 必须拆分

以下内容必须独立提交：

- 两个互不依赖的正式能力；
- 功能与无关重构；
- 业务修复与大范围格式化；
- 不同领域且可独立验收的变动；
- 多个互不依赖的 migration；
- 可独立撤销的 API 与领域能力；
- 巨型提交中多个可以逐步构建和测试的垂直切片；
- Agent 规则修订与生产代码修改；
- Git 历史规则修订与实际历史重写。

#### 粒度判定

提交前必须逐项回答：

1. 是否只有一个主要逻辑结果？
2. 标题能否准确概括整个提交？
3. 正文 1～8 项能否完整描述变动？
4. 独立撤销后仓库是否仍逻辑一致？
5. 是否存在必须随它提交的测试、migration、契约或生成物？
6. 是否混入可独立撤销的其它能力？
7. 是否因为文件数量多而错误认定不可拆分？
8. 是否存在只能通过依赖和逐提交构建证明的拆分边界？

标题无法概括整个提交或正文超过 8 个逻辑项时，必须优先拆分。真正独立的错误修复不得为了整洁而埋回旧功能提交；不能独立解释的即时补漏也不得单独存在。

### 唯一格式

除 Merge 和 Revert 的专用格式外，每个正式提交必须同时包含标题和正文，并且只能使用以下两种结构。

非阶段提交：

```text
<type>(<scope>): <中文标题>

变动内容：
- <详细逻辑变动一>
- <详细逻辑变动二>
```

阶段任务提交：

```text
<type>(<scope>): <中文标题>

开发阶段：阶段 N
变动内容：
- <详细逻辑变动一>
- <详细逻辑变动二>
```

格式约束：

- 标题和正文之间恰好一个空行；
- `开发阶段：阶段 N` 与 `变动内容：` 之间不得插入空行；
- `变动内容：` 必须顶格书写并使用中文全角冒号；
- `变动内容：` 后至少一个列表项；
- 列表使用半角 `- `，每项顶格且独占一个物理行；
- 各列表项之间不得插入空行；
- 不允许正文之外的额外说明、表格、代码围栏、编号列表、二级列表、自由段落或 Git trailer；
- 提交消息开头不得有空行，不得包含尾随空白或全角空格，末尾保留且只保留一个换行；
- Merge 和 Revert 必须严格使用下文规定的完整专用格式，Revert 额外包含 `撤销对象：` 字段；Revert 是否同时包含 `开发阶段：阶段 N` 取决于被撤销对象是否属于明确编号开发阶段，规则见"开发阶段"与"Merge、Revert 与不兼容变更"两节。

### 标题职责

标题只负责：**用一句话概括该提交完成的核心逻辑结果。**

标题必须：

- 匹配 `<type>(<中文或批准的固定 scope>): <中文标题>`；
- 使用动宾结构并包含中文，代码标识和必要术语可以保留英文；
- 保持单行，整个标题行不超过 72 个 Unicode code point，不按 UTF-8 字节数计算；
- 在 `)` 后使用英文半角冒号，冒号后恰好一个半角空格；
- 只概括一个主要逻辑结果，不机械复述正文。

标题不得：

- 写测试结果、Actions 状态、性能数字、提交数量、Git 状态或下一步；
- 罗列多个并列子项；
- 使用“以及”“并且”“同时”等词连接多个独立能力；
- 以文件名或文件数量概括提交；
- 使用结束标点、Emoji、全角冒号或 `!`。

阶段任务的阶段编号原则上只写在正文的 `开发阶段：阶段 N`。只有提交本身专门调整阶段划分、阶段状态或阶段边界时，标题才允许出现阶段编号。

### 正文职责

正文负责：**将标题概括的核心结果拆成更细、可以逐项核对的逻辑变动。**

正文列表必须按逻辑变动组织，不得按文件组织。每个列表项必须：

- 描述一个逻辑子变动；
- 使用动宾结构并包含中文；
- 比标题更具体，说明实际改变了什么语义；
- 不以文件路径开头；
- 不逐文件复述 diff；
- 不把多个彼此独立的动作塞进同一行；
- 不使用“修改若干文件”“更新相关代码”等模糊表述；
- 不写测试是否通过、Actions 状态、提交 SHA、工作树状态、开发耗时、token 消耗或未来计划；
- 不使用结束标点。

列表项可以按需要提及类型名、API 路径、migration 编号、错误码、协议字段以及极少量关键文件名或文件路径，但这些内容只能服务于逻辑说明，文件路径不得成为正文的组织键。每个提交建议包含 1～8 个列表项；超过 8 项通常说明提交过粗、标题无法覆盖全部变动或存在可独立拆出的逻辑切片，必须重新评估提交粒度。

正文必须处于“逐文件复述”和“过度概括”之间。以下两类写法均错误：

```text
变动内容：
- 更新扫描代码
- 修改测试
- 同步文档
```

```text
变动内容：
- 修改 scanner 服务文件
- 修改 catalog 存储文件
- 修改 scanner 测试文件
```

正确的详细程度是：

```text
变动内容：
- 将媒体确认目标绑定至同一个 query publication
- 将冻结 observation 失效收敛为不可重试错误
- 隔离不同 publication 下媒体确认请求的幂等身份
- 补充快照切换、目标变化和重启恢复测试
```

### 开发阶段

凡提交明确属于 Gallery 正式开发阶段，正文必须包含：

```text
开发阶段：阶段 N
```

当前合法值只有：

- `阶段 0`
- `阶段 1`
- `阶段 2`
- `阶段 3`
- `阶段 4`
- `阶段 5`
- `阶段 6`
- `阶段 7`

以下提交必须写开发阶段：

- 阶段计划中的功能切片；
- 阶段 Correctness 修复；
- 阶段 Freeze Gate；
- 阶段收尾；
- 阶段专项测试；
- 阶段契约或 Schema 演进；
- 为完成某阶段而直接产生的文档提交。

以下提交通常不写开发阶段：

- Agent 规则；
- 仓库维护；
- 通用 CI；
- 工具链修正；
- 与任何开发阶段无关的历史整理规则；
- 独立安全修复，且事实代码不能归入某一阶段；
- Merge Commit；
- Git 历史规范化本身。

不得根据仓库整体所处阶段给所有提交机械添加同一个阶段。开发阶段必须依据该提交实际所属的实施任务判断。`Walking Skeleton` 和 `Architecture Proof` 是非编号命名切片，不得擅自映射为 `阶段 0`～`阶段 7`；只有实施计划明确把某项任务归入编号阶段时才填写阶段字段。

Merge Commit 描述的是分支历史合并行为，而不是某个开发阶段内的实现切片：其主要语义由合并主题和引入的逻辑变动表达，不是该提交发生在哪个开发阶段。因此 Merge 通常省略开发阶段字段，除非未来规范明确增加特殊 Merge 阶段管理需求。

Revert Commit 是否包含开发阶段字段，取决于被撤销的原提交，而不是仓库当前整体所处阶段：若被撤销对象属于明确编号开发阶段（阶段 0～阶段 7），Revert 正文必须在 `撤销对象：` 之后、`变动内容：` 之前加入对应的 `开发阶段：阶段 N`；若被撤销对象不属于任何编号开发阶段，Revert 不得包含开发阶段字段。普通阶段任务提交必须包含阶段字段，Merge 不属于普通阶段任务提交因此通常省略，Revert 单独按被撤销对象判断，三者不得混为一谈。Merge 与 Revert 的完整专用格式、条件判定和示例见"Merge、Revert 与不兼容变更"一节。

### `type`

`type` 继续使用英文小写 Conventional Commit 值，唯一允许：

| type | 用途 |
| --- | --- |
| `feat` | 新增正式能力 |
| `fix` | 修复错误行为 |
| `refactor` | 不改变外部行为的结构调整 |
| `perf` | 性能优化 |
| `test` | 独立测试能力或长期门禁 |
| `docs` | 只修改文档 |
| `build` | 构建、依赖或打包 |
| `ci` | 持续集成和自动化 |
| `chore` | 其它仓库维护 |
| `revert` | 撤销已有提交 |

不得新增其它 `type`。包含多种性质时，选择能够描述主要逻辑结果的 `type`。功能或修复与其直接测试、migration、契约和必要文档处于同一逻辑切片时，仍使用 `feat` 或 `fix`，不得因为包含测试和文档而改用 `test` 或 `docs`。`docs` 提交只能修改文档；若文档型契约变动会改变协议或实现语义，或提交包含实际测试语义或生产实现，必须使用与主要结果相符的其它类型。

### 中文优先的 `scope`

`scope` 必须始终填写，并遵守：

- 中文优先，使用稳定的领域名或模块名；
- 不以单个文件、函数、Issue 号或一次性任务命名；
- 不使用空格、斜杠、下划线或标点；
- 一般使用 1～8 个中文字符；
- 仅在中文表达明显不自然时允许使用已批准的固定英文专名或缩写；
- 同一领域不得同时出现多套近义 `scope`；
- 新增 `scope` 前必须确认下表没有等价项，并先更新本词表。

Gallery 的正式 `scope` 词表为：

| scope | 适用范围 |
| --- | --- |
| `仓库` | 根级仓库结构、基础说明和通用维护 |
| `代理规则` | `AGENTS.md` 和 Agent 工作规则 |
| `核心` | 基础领域、公共内核和进程基础 |
| `配置` | 配置解析和配置模型 |
| `契约` | 通用协议、Schema 和错误契约 |
| `接口` | REST API 与 OpenAPI |
| `实时协议` | WebSocket 和实时事件 |
| `认证` | 配对、Session、授权和 capability |
| `资料库` | Library 领域 |
| `来源` | Source 领域 |
| `规则` | RulePackage、RuleVersion、Rule IR 和 CEL |
| `扫描` | Scanner、发现、哈希编排和扫描档案 |
| `任务` | Job、Attempt、Scheduler 和任务恢复 |
| `目录` | Catalog、publication 和查询快照存储 |
| `查询` | 结构化过滤、排序、游标和 Total |
| `搜索` | FTS、Ranking 和高亮 |
| `媒体` | CanonicalMedia、内容读取和 Range |
| `派生资源` | 缩略图、DerivedAsset 和工具执行 |
| `存储` | SQLite、数据库基础和通用仓储 |
| `迁移` | 数据库 migration |
| `备份` | control.db 备份与恢复 |
| `恢复` | 强杀恢复、跨库 Saga 和 reconciliation |
| `叠加层` | Overlay 和用户事实投影 |
| `创作者` | CanonicalCreator |
| `绑定` | Binding、Binding issue 和人工修复 |
| `平台` | AppDirs、文件系统、锁、Watcher 和 OS adapter |
| `安全` | 安全策略和安全边界 |
| `命令行` | `galleryctl` |
| `网页` | Web/PWA |
| `桌面端` | 桌面壳 |
| `指南` | 实施计划、测试门禁和验证记录 |
| `决策` | ADR |
| `工具链` | Go、构建工具和本地环境 |
| `工作流` | GitHub Actions 和 CI workflow |
| `发布` | 打包、版本和发行流程 |
| `测试基建` | `tools/testlab/**` 等阶段无关、跨阶段共用的测试框架、Source guard、语料生成和规则验收夹具 |

本词表只定义提交 `scope` 标签，不改写产品和文档的正式术语或分类。`目录` 用作 Catalog 领域的 `scope` 时，正文仍使用正式术语 Catalog；`指南` 汇总实施计划、测试门禁和验证记录的提交作用域，不改变验证记录在文档导航中的“证据”分类；`测试基建` 只指代跨阶段测试工具本身，不代表任何生产阶段的功能范围。

文档提交按文档主题选择 `scope`，例如：

```text
docs(代理规则)
docs(指南)
docs(决策)
docs(接口)
docs(仓库)
```

不得使用：

```text
docs(文档)
test(测试)
ci(持续集成)
build(构建)
```

`type` 已经表达提交性质，`scope` 必须表达作用领域，不得重复 `type`。

### Merge、Revert 与不兼容变更

#### Merge Commit

Merge 描述的是分支历史合并行为，而不是某个开发阶段内的实现切片：

- 合并的主要语义由合并主题和引入的逻辑变动表达，不是某个开发阶段的实现进度；
- 因此 Merge Commit 必须保留 Merge 结构，通常省略开发阶段字段，除非未来规范明确增加特殊 Merge 阶段管理需求。

Merge Commit 使用：

```text
chore(仓库): 合并 <中文分支或主题说明>

变动内容：
- <该 Merge 实际引入的逻辑变动>
- <解决冲突时产生的逻辑变动>
```

没有独立文件树变动时允许：

```text
chore(仓库): 合并 <中文分支或主题说明>

变动内容：
- 合并指定分支的提交历史
```

Merge 正文仍按逻辑变动组织，不得列出冲突文件清单，也不得机械添加 `开发阶段：阶段 N`。

#### Revert Commit

Revert 是否包含开发阶段字段，取决于被撤销的原提交，而不是仓库当前整体所处阶段：

- 如果被撤销对象属于明确编号开发阶段（阶段 0～阶段 7），必须在 `撤销对象：` 之后、`变动内容：` 之前加入对应的 `开发阶段：阶段 N`：

```text
revert(<scope>): 撤销 <原提交中文摘要>

撤销对象：<完整或不少于 12 位的 commit SHA>
开发阶段：阶段 N
变动内容：
- <恢复的逻辑行为>
- <撤销后重新生效的契约或边界>
```

  例如：

```text
revert(扫描): 撤销媒体确认目标冻结改动

撤销对象：1234567890abcdef1234567890abcdef12345678
开发阶段：阶段 4
变动内容：
- 恢复按当前活动快照创建媒体确认任务的旧行为
- 移除冻结 publication 与 observation 的执行期校验
```

- 如果被撤销对象不属于任何编号开发阶段，则省略开发阶段字段：

```text
revert(<scope>): 撤销 <原提交中文摘要>

撤销对象：<完整或不少于 12 位的 commit SHA>
变动内容：
- <恢复的逻辑行为>
- <撤销后重新生效的契约或边界>
```

  例如：

```text
revert(代理规则): 撤销提交规范调整

撤销对象：1234567890abcdef1234567890abcdef12345678
变动内容：
- 恢复旧提交说明约束
```

只有一个 Revert Commit 确实撤销连续范围时，才允许改用：

```text
revert(<scope>): 撤销 <原提交范围中文摘要>

撤销对象：<最早 commit SHA>^..<最新 commit SHA>
变动内容：
- <该范围恢复的逻辑行为一>
- <该范围恢复的逻辑行为二>
```

范围 Revert 的开发阶段字段判定与单一对象一致：仅当范围内全部原提交都属于同一个编号开发阶段时，才在 `撤销对象：` 之后补充该 `开发阶段：阶段 N`；只要范围内存在不属于编号阶段的提交，或范围跨越多个不同编号阶段，整个范围 Revert 都不得包含开发阶段字段。

`撤销对象：` 只允许 Revert 使用。单个对象优先使用完整 SHA，最少保留 12 位。多个提交不得用一个 Revert 模糊撤销；只有 Git 实际生成一个连续且明确的提交范围、正文逐项说明时才允许范围格式。`<最早 SHA>^..<最新 SHA>` 使用 Git revision selector 表示包含最早与最新提交的连续范围，两个 SHA 都必须完整或不少于 12 位；若最早对象是没有父提交的根提交，则不得使用范围格式。非连续提交集合不得合并为一个 Revert。Revert 不得只写“恢复文件”。

#### 三类提交与阶段字段的关系

- 普通阶段任务提交：必须包含阶段字段。
- Merge：通常省略阶段字段，因为 Merge 不属于普通阶段任务提交，其语义由合并主题和引入的逻辑变动表达。
- Revert：根据被撤销对象是否属于明确编号阶段决定，不由 Revert 本身的提交类型或仓库当前阶段决定。

不得笼统表述为“Merge 和 Revert 都不附加阶段字段”，因为 Revert 在被撤销对象属于编号阶段时必须附加；也不得表述为“所有阶段任务提交必须包含阶段字段”并据此要求 Merge 附加阶段字段，因为 Merge 不是普通阶段任务提交。

#### 不兼容变更

Gallery 尚未发布稳定公共版本，继续禁止：

```text
!
BREAKING CHANGE:
```

未来若启用，必须先修改本规范，任何历史重写也不得擅自添加。

### 签名、测试与历史重写

#### 签名与工作树

- 所有正式提交必须使用 SSH 签名；可以使用仓库已配置的 `commit.gpgsign=true` 或等效的显式 SSH 签名方式。
- 签名判断以 commit object 是否包含 `gpgsig` 为准，不得仅凭 `%G?` 显示 `N` 就认定未签名。
- 工作树干净状态分三个时间点判定：任务开始前必须干净；创建提交前暂存区只能包含该逻辑切片，且不得夹带无关的已暂存、未暂存或未跟踪文件；若本轮没有计划内的下一增量，提交后必须干净。
- 每个提交都不得处于无法构建、无法执行适用测试或明显不一致的中间状态。

#### 提交前验证

- 门禁针对即将提交的完整文件树执行，并按 `默认交付流程` 选择完整门禁或纯文档轻量门禁。
- 功能、修复、migration、契约和生成物必须执行与风险相称的直接验证。
- 提交前必须核对暂存 diff、提交标题、`type`、`scope`、开发阶段归属及适用时强制的阶段字段、逻辑正文和提交粒度。
- 验收结果记录在任务报告或 CI 中，不写入提交正文。

#### 历史重写授权与隔离

- 只有用户明确下令才允许历史重写；提交规范修订与实际历史重写必须属于不同提交和不同任务轮次。
- 历史重写不得与普通开发、生产修复或文档更新混在同一任务中。
- 历史正文必须依据每个原提交当时的真实 diff、依赖和时代语义编写，不得按当前最终文件树为早期提交编造当时尚不存在的能力。

#### 历史重写前保护

开始重写前必须：

1. 确认工作树、暂存区和未跟踪文件均为空；
2. 创建不可误覆盖的本地备份引用；
3. 创建不可误覆盖的远程备份引用；
4. 在仓库外创建完整 bundle；
5. 记录原 HEAD、原 tree SHA、提交数量、提交图和工作树状态；
6. 验证备份引用和 bundle 可以解析到原 HEAD；
7. 确认 SSH 签名环境能够为全部重写提交签名。

不得删除任何备份引用。无法完成上述保护时必须停止，不得开始重写。

#### 历史重写执行

- 可以使用 `reword` 修正标题与正文，使用 `fixup` 或 `squash` 合并无意义尾随提交，使用 `edit` 拆分巨型提交，并在依赖安全时调整顺序。
- 每个重写后的提交都必须重新 SSH 签名；不得伪造旧签名或留下未签名提交。
- 必须逐提交审查标题、`type`、`scope`、正文、阶段字段、粒度和依赖，不得只做格式替换。
- 改变文件归属或提交顺序时，必须逐提交验证中间树可以构建并执行适用测试。
- 不得丢失 migration、测试、生成代码或文档，不得修改历史 migration 内容，不得为凑提交数量机械合并互不相关的能力。
- 尽可能保留作者和提交者的姓名、邮箱与日期。

#### 历史重写后验证与发布

重写完成后必须：

1. 遍历全部目标历史，确认每个提交都包含 `gpgsig`；
2. 逐提交复核单一逻辑结果、格式、粒度和依赖；
3. 证明新旧最终 tree SHA 完全一致；
4. 确认 `git diff` 为空且工作树干净；
5. 确认没有修改历史 migration；
6. 保存重写后的提交数量和提交图；
7. 在替换远程历史前再次确认本地、远程和 bundle 备份仍可用。

历史重写只能在用户明确授权推送时发布，并且只允许带明确 lease 的 `--force-with-lease`；禁止裸 `--force`。不得自动创建 PR、删除备份引用或把普通推送授权解释为历史重写授权。若无法为全部提交重新签名、无法证明最终 tree 一致或逐提交验证失败，必须恢复原始历史并报告阻塞。

#### 历史整理自检

- 不得保留纯 `gofmt`、即时补漏或单行状态纠偏等不能独立解释的提交。
- 标题与正文格式正确不代表粒度正确，必须单独核对每个提交的单一意图。
- 大型提交必须按实际依赖评估能否拆成多个可构建、可测试的逻辑切片，并以逐提交构建结果证明。
- “拆分困难”必须由依赖关系和逐提交构建结果证明，不能只凭文件数量断言。

### 正例

#### 阶段功能

```text
feat(查询): 建立结构化过滤与快照分页主线

开发阶段：阶段 4
变动内容：
- 建立服务端权威过滤字段注册表并实现 AND、OR、NOT 组合
- 将排序协议、查询指纹和授权范围绑定至签名游标
- 增加 Total 关系表达和稳定分页边界
- 补充过滤、游标篡改与快照租约测试
```

#### 阶段修复

```text
fix(扫描): 修正媒体确认混用不同查询快照

开发阶段：阶段 4
变动内容：
- 将媒体身份与 observation 绑定至同一个 query publication
- 将幂等身份扩展为包含 publication 与冻结 observation
- 将目标失效统一收敛为不可重试错误
- 补充历史快照、当前快照和执行前文件变化测试
```

#### 非阶段 Agent 规则

```text
docs(代理规则): 重写提交信息与历史整理规范

变动内容：
- 重新划分标题、中文 scope 与正文的职责
- 取消逐文件正文并改用细粒度逻辑变动列表
- 增加阶段字段、粒度判定和历史重写规则
- 补充功能、修复、文档、Merge 与 Revert 案例
```

#### CI

```text
ci(工作流): 固化双平台测试和竞态检查

变动内容：
- 在 Windows runner 执行普通测试、静态检查和构建
- 在 Ubuntu runner 执行竞态检测与漏洞检查
- 将 Go 版本统一绑定至仓库工具链声明
```

#### Merge

```text
chore(仓库): 合并查询快照修复分支

变动内容：
- 合并查询快照修复分支的提交历史
- 保留主线新增的游标签名字段并解决契约冲突
```

#### Revert（撤销对象属于编号阶段）

```text
revert(扫描): 撤销媒体确认目标冻结改动

撤销对象：1234567890abcdef1234567890abcdef12345678
开发阶段：阶段 4
变动内容：
- 恢复按当前活动快照创建媒体确认任务的旧行为
- 移除冻结 publication 与 observation 的执行期校验
```

#### Revert（撤销对象不属于任何编号阶段）

```text
revert(代理规则): 撤销提交规范调整

撤销对象：1234567890abcdef1234567890abcdef12345678
变动内容：
- 恢复旧提交说明约束
```

### 反例

#### 逐文件正文

```text
fix(扫描): 修正媒体确认

变动内容：
- `internal/scanner/service.go`：修改扫描逻辑
- `internal/catalog/store.go`：修改查询逻辑
```

错误原因：正文仍按文件组织，未描述真实语义。

#### 英文 scope

```text
fix(scan): 修正媒体确认
```

错误原因：Gallery 的 `scope` 中文优先且已有固定词表，应使用 `fix(扫描)`。

#### 阶段任务缺少阶段字段

```text
feat(查询): 完成结构化过滤

变动内容：
- 建立过滤字段注册表
```

错误原因：该能力明确属于阶段 4，必须写 `开发阶段：阶段 4`。

#### Revert 遗漏应有的阶段字段

```text
revert(扫描): 撤销媒体确认目标冻结改动

撤销对象：1234567890abcdef1234567890abcdef12345678
变动内容：
- 恢复按当前活动快照创建媒体确认任务的旧行为
- 移除冻结 publication 与 observation 的执行期校验
```

错误原因：被撤销对象属于阶段 4 的 Correctness 修复，Revert 必须在 `撤销对象：` 之后补上 `开发阶段：阶段 4`，不能因为“Merge 和 Revert 都不写阶段字段”而省略。

#### Revert 误加不应有的阶段字段

```text
revert(代理规则): 撤销提交规范调整

撤销对象：1234567890abcdef1234567890abcdef12345678
开发阶段：阶段 4
变动内容：
- 恢复旧提交说明约束
```

错误原因：被撤销对象是 Agent 规则文档调整，不属于任何编号开发阶段，不能因为“所有阶段任务提交必须包含阶段字段”而给 Revert 机械附加阶段字段。

#### 标题承载多个独立能力

```text
feat(查询): 完成过滤、搜索、媒体读取、派生资源和安全授权
```

错误原因：多个独立垂直切片无法用单一提交合理撤销。

#### 正文过度概括

```text
变动内容：
- 更新代码
- 增加测试
- 同步文档
```

错误原因：无法判断具体语义变化。

#### 正文包含验收结果

```text
变动内容：
- 修复扫描错误并通过 100 次测试
```

错误原因：正文只描述逻辑变动，不记录验收结果。

### 提交前检查表

创建提交前逐项确认：

1. 提交只有一个主要逻辑结果，且已包含所有直接依赖的测试、migration、契约、生成物和必要文档。
2. 标题使用允许的 `type`、正式 `scope` 和中文动宾结构，整个标题行不超过 72 个 Unicode code point。
3. 阶段任务包含唯一合法的 `开发阶段：阶段 N`，非阶段任务没有机械添加阶段；Merge 通常省略阶段字段，Revert 已按被撤销对象是否属于编号阶段正确决定阶段字段的有无。
4. 正文至少包含一个且通常不超过 8 个细粒度逻辑变动列表项；超过 8 项时已重新评估提交粒度，且没有逐文件组织、验收结果、状态或未来计划。
5. Merge 或 Revert 严格使用专用格式，Revert 包含可追踪 SHA，且阶段字段的有无与被撤销对象是否属于编号阶段一致。
6. 标题与正文之间恰好一个空行，其余字段和列表之间没有多余空行。
7. 暂存区只包含当前逻辑切片，工作树没有无关改动。
8. 已执行适用门禁，提交树可以构建、测试且逻辑一致。
9. 提交将使用 SSH 签名，并会从 commit object 检查 `gpgsig`。
10. 没有使用 `!`、`BREAKING CHANGE:`、尾随空白、全角空格或正文外额外段落，提交消息末尾只保留一个换行。

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

阶段 0、Walking Skeleton、Architecture Proof 正确性切片、阶段 1「领域和数据所有权」、阶段 2「规则闭环」、阶段 3「扫描、任务和 Catalog」与阶段 4「查询和媒体」已完成代码与合成 Correctness（含 SourceWork 拆分/合并、阶段 1/2 Schema Freeze Gate、RulePackage 生命周期、参数/Binding 快照、规则调试契约、持久 Job/Hash/Watcher/维护闭环、结构化查询字段注册表（含 Favorite/Hidden/Progress）、字段级 Ranking v2/通用命中表达/Total 协议、Overlay 字段能力注册表与动态 dependency set planner、执行阶段真正校验冻结身份且绑定同一 publication 的目标化单媒体按需内容确认、跨 revision 内容寻址且受非终态 Job 状态与 `media.BlobReadLease` 双重保护的媒体/DerivedAsset 查询快照绑定读取、独立 `media.derive` capability、catalog v9→v10 查询快照列启动期回填只在驱动投影 Job 真正 `completed` 后才标记完成）。阶段 4 正式压力测试首轮已执行：1M/10M 实测（EV-35）确立 500,000 WorkProjection 为推荐正式验证规模；重构后的 `tools/testlab` 框架完成 500,000 规模验证与全部 10 个目标来源验证（EV-36），Correctness/Cursor 通过但 Reference Performance Gate 仍未通过（`wide-cjk`/结构化过滤等类别有已知未修复的架构性延迟），10 个来源中 Gank、Pawchive 因抽样工具局限未完成真实验证。真实 HDD/SMB/NAS/网络挂载、ranking 权重/total 预算/各类租约时长等 PRE_FREEZE 数值冻结、AND/OR 子项 canonical 化、Progress 排序、FileLocation 最终唯一约束、Wails/Tauri、Linux/macOS/Docker 支持等仍是后续冻结或发行门禁。下一条正式垂直切片是阶段 5 账户、安全和多客户端，以及阶段 4 剩余的性能优化与 API Freeze 审计；不要提前展开前端、LAN 完整账户、桌面壳或发行。
