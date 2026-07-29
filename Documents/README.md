# Gallery 工程文档

本目录是 Gallery（中文名“画廊”、代码代号 `gallery`）未来正式实现的唯一工程文档入口。这里保存当前规范、实施指南、验证证据、架构决策，以及集中待评审的前端设计材料，不保存分散的调研过程、阶段汇报或已完成任务清单。

2026-07-28 的 [EV-65](证据/验证记录.md) 已把当时的 Personal 11/11、LAN 1/1、同 AppDirs 恢复重启和只读 Source guard 的隔离真实后端链从 Chromium 扩展到桌面 Firefox，并让 Chromium/Firefox smoke 与完整隔离链进入 CI；[EV-66](证据/验证记录.md) 又关闭显式扫描/Watcher 状态脱节、扫描期间事件丢失与维护 Job 终态不刷新的竞态；[EV-67](证据/验证记录.md) 再把全部 15 类现有 primitive config 接入权威 Schema 可视化字段；[EV-68](证据/验证记录.md) 让当前规范 JSON 草稿以无损参数和显式合成 Sample 执行 Dry Run、Explain、Trace；[EV-69](证据/验证记录.md) 补齐基于本地精确基线的按字段撤销；[EV-70](证据/验证记录.md) 继续为 `parameter_schema`、tests、extensions 建立无损结构化编辑和任意 JSON 树；[EV-71](证据/验证记录.md) 再用真实单帧丢失证明 sequence gap 会重取 jobs、libraries 与 sources HTTP snapshot；[EV-72](证据/验证记录.md) 又从双浏览器 UI 证明运行中 Scan、活动 Hash、Attempt 与 publication 的取消收敛；[EV-73](证据/验证记录.md) 继续以真实 `galleryd` 强杀、同 AppDirs 立即重启和未来租约不改表，证明启动期单写者能立即接管遗留 Attempt，并让 UI 解释和治理 `PROCESS_INTERRUPTED`；[EV-74](证据/验证记录.md) 建立首批治理链，[EV-75](证据/验证记录.md) 再覆盖 SourceWork merge、全部 orphan decision/实体类型与已消费决策冲突，[EV-76](证据/验证记录.md) 又补齐普通 Binding issue 三决定、真实生命周期、双标签页冲突与 51 条 keyset 分页并修复活动唯一性，[EV-77](证据/验证记录.md) 再实际消费三种剩余结构 action，并经同 AppDirs 重启验证 Work/Creator/Media 孤儿重现身份语义。阶段 6 Web Gate 仍因真实移动设备/屏幕阅读器、完整弱网矩阵与正式可用性门禁缺口而未通过。

[EV-78](证据/验证记录.md) 又基于当前从零重写后的正式双入口 UI 建立 42rem 窄屏模态导航：宽屏常驻导航与窄屏触发器互斥进入可见/焦点树，React Aria Dialog 提供焦点陷阱、Escape 关闭与焦点返还，当前页经 `aria-current` 暴露。Chromium/Firefox 390×844 smoke 合计 10/10，并在两个打开的导航模态上执行未禁用 `color-contrast` 的 WCAG A/AA axe；这关闭当前代码的窄屏导航焦点缺口，但不代表真实移动设备、触控或人工屏幕阅读器门禁通过。

[EV-79](证据/验证记录.md) 随后纠正上述“无横向溢出”的跨平台证据：精确 `ee69eee` 的 Linux Chromium 在 `/manage/scans` 390×844 复现 3px 页面级溢出，根因是单列 Grid 的默认 intrinsic minimum 被协议标识撑宽；当前代码以可收缩轨道和显式长标识换行修复，并把 320px 最低宽度加入回归。Windows Chromium/Firefox 10/10 与 WSL Linux Chromium 320/360/390/412px 产物探针通过，最终文档 HEAD `dd0343e9f4740d2fcd5f3b7fd9f004c1218cd743` 的 Actions run `30378490971` 全部成功；Web Gate 仍未通过。

[EV-80](证据/验证记录.md) 继续关闭两个弱网恢复缺陷：离线超过约 75 秒不再耗尽 8 次重连预算，网络恢复会立即刷新 bootstrap、重建 socket 与 HTTP snapshot；在线异常关闭也会在第一次退避前刷新 bootstrap，使 Firefox 丢失 4401、只报告 1006 时仍能由 HTTP 事实源收敛到未认证。Chromium/Firefox 完整真实后端链各 17 项通过，其中同一 Owner 连续三轮 offline/online 并逐轮核对新 socket、bootstrap/安全快照及断线期间另一 Session 创建的事实。该证据仍不包含带宽限制、随机延迟/丢包、服务长停机、移动网络切换或真实设备，完整弱网与 Web Gate 继续未通过。

[EV-81](证据/验证记录.md) 又把游标错误通知绑定到产生它的查询条件，修复旧 `CURSOR_INVALID` 跨搜索残留并阻止新查询续页；组件级 QueryClient/Router 回归证明旧分页和旧详情即使在取消后仍迟到返回，也不能覆盖新搜索或新路由，Chromium/Firefox 生产资产 smoke 另以可控延迟锁定旧分页不能进入新搜索结果。该证据不包含错误响应乱序、带宽限制、随机延迟/丢包、服务长停机、代理或移动网络，完整弱网与 Web Gate 仍未通过。

[EV-82](证据/验证记录.md) 再修复浏览器保持 online、但 `galleryd` 长时间不可用时 8 次普通重连耗尽后永久停止的问题；普通异常关闭改为 15 秒封顶持续退避，安全与协议终态不变。Chromium/Firefox 各 18 项真实后端链在同一页面、Session、临时 AppDirs 与 origin 下停止服务，跨过旧预算后按原端口重启，并验证 WebSocket 与 `/api/v1/jobs` snapshot 无刷新恢复。该证据关闭同 origin 服务长停机切片，不包含随机延迟/丢包、带宽、代理、移动网络、反复崩溃或真实设备，完整弱网与 Web Gate 仍未通过。

[EV-83](证据/验证记录.md) 又在真实 `galleryd` 的作品查询边界注入一次 GET 传输中断和第二次请求的 300 ms 受控延迟，并让已取消旧查询的结构化 `FORBIDDEN` 无视 `AbortSignal` 后迟到交付。Chromium/Firefox 各 19 项完整链均证明网络失败自动重试后显示真实后端数据，旧错误不覆盖新搜索；现有生产实现无需修改。该证据仍不包含随机延迟/丢包分布、带宽、代理、移动网络或真实设备，完整弱网与 Web Gate 仍未通过。

当前实现状态：阶段 0、Walking Skeleton、Architecture Proof 正确性切片、阶段 1「领域和数据所有权」、阶段 2「规则闭环」、阶段 3「扫描、任务和 Catalog」与阶段 4「查询和媒体」已完成代码与合成 Correctness 实现；阶段 5「账户、安全和多客户端」已完成代码与合成安全收尾但 Security Gate 未通过（[EV-37](证据/验证记录.md)、[EV-38](证据/验证记录.md)）；阶段 6「Web/PWA」已实现页面代码基线但 Web Gate 未通过。[EV-39](证据/验证记录.md) 发现的 WebSocket 与 capability 阻断已由 [EV-40](证据/验证记录.md) 修复；[EV-54～EV-77](证据/验证记录.md) 已把管理自举、publication-bound 画廊/媒体、CustomCover、规则生命周期/ParameterSet/Schema 表单、当前草稿 Dry Run/Explain/Trace、按字段撤销及三类任意 JSON 子树结构化编辑、安全、断线与单帧 sequence gap 恢复、维护、规则绑定状态、作品/媒体人工解绑与撤销、普通 Binding issue 三决定/生命周期/分页、全部五种 SourceWork 结构 action、全部 orphan decision/实体类型及重现身份语义、已消费决策冲突、retry-backoff Job 取消/同 ID 重试、运行中 Scan/Hash 级联取消、进程强杀后的启动接管和 control 恢复实际重启接入隔离 Chromium/Firefox 真实 `galleryd` 持续门禁，并关闭扫描/Watcher 与维护终态同步竞态。证据仍为合成 Source/同机 loopback，不含完整业务闭环、真实设备或正式发行支持。以下段落记录阶段 3～4 的实现细节，并完成阶段 3 Correctness 修正。阶段 3 覆盖同一 Job 多 Attempt、周期租约恢复与退避、非阻塞独立资源池、限定同一父 Scan 复用的完整 SHA-256 Hash Job、动态 Watcher 与低频周期收敛、所有权 Temp GC、服务端维护空间估算、staging/publication 互斥、DerivedAsset/外部工具不可用边界及对应 REST/OpenAPI 契约。阶段 4 覆盖结构化查询字段注册表（AND/OR/NOT，含 Favorite/Hidden/Progress）、Creator 合并查询等价组解析、字段级 Ranking v2、通用版本化命中表达、Total 协议、签名 keyset 游标 rank 扩展、Overlay 字段能力注册表与按查询实际生成的动态 dependency set planner、媒体 If-Range、未确认媒体按需内容确认闭环（已收敛为只强制目标媒体，不再触发整个 Source 的 `verify`，执行阶段真正校验冻结的 MediaID/ObservationFingerprint，且 EV-34 修正为显式冻结实际使用的 `queryPublicationId`、媒体身份与 observation 均从同一个已确认为 active 的 publication 解析、执行阶段读取该冻结 publication、幂等键随 `queryPublicationId` 变化、前置身份不匹配统一为不可重试的 `VERIFICATION_TARGET_MISMATCH`）、媒体与 DerivedAsset 的查询快照绑定读取、独立 `media.derive` capability 与 DerivedAsset 受限 JPEG 缩略图端到端公共契约（异步输入已改为跨 revision 内容寻址，并同时受非终态/退避等待中 Job 状态与 `media.BlobReadLease` 保护，不依赖固定租约 TTL 覆盖排队/退避/生成耗时的假设）、catalog v9→v10 查询快照列启动期回填只在驱动既有或新建 Overlay 投影 Job 真正 `completed` 后才标记完成（见 [验证记录 EV-31](证据/验证记录.md)、[EV-32](证据/验证记录.md)、[EV-33](证据/验证记录.md)、[EV-34](证据/验证记录.md)）。阶段 4 正式压力测试首轮已执行：1M/10M 实测（见 [验证记录 EV-35](证据/验证记录.md)）发现两档规模均不适合作为标准发布 Gate，据此把推荐正式验证规模调整为 500,000 WorkProjection、`≥1,000,000` 降级为非推荐诊断场景；重构后的可复用测试框架 `tools/testlab` 随后完成 500,000 规模验证与全部 10 个目标来源的规则包及有界真实 Source 验证（见 [验证记录 EV-36](证据/验证记录.md)）：500,000 规模 Correctness/Cursor 全部通过，Perf 矩阵在预算内完成，但 `wide-cjk`/结构化过滤等类别仍有已知未修复的架构性延迟，Reference Performance Gate 仍未通过；当时 10 个目标来源中只有 8 个完成有界真实 Source 验证，Gank、Pawchive 因旧抽样算法未命中候选目录而停止。[EV-108](证据/验证记录.md) 已直接登记两个真实根并关闭这项“完全未验证”缺口：Gank 12/12、Pawchive 2/2 按需确认成功且全树零写入；Pawchive 12 目标运行仍暴露取消后 30 秒未收敛与每目标重复全 Source 处理缺口。真实 HDD、SMB/NAS、网络挂载、完整规则语义、全量扫描、ranking/total/cursor 及 publication 读取租约等 PRE_FREEZE 数值冻结留待下一轮实测，不代表完整产品、平台或发行门禁已经完成。

EV-67 延续上述真实后端持续门禁：当前 primitive config 的 15 个 kind 已由同一 `/api/v1/rules/schema` 文档生成字段，Chromium/Firefox 均证明可视化修改进入精确草稿文本；EV-68 又以当前未保存规范 JSON 草稿、无损参数与显式合成 Sample 打通 Dry Run、Explain、Trace，输入变化时旧结果失效隐藏；EV-69 再以本地 `baseRevision` 精确快照建立普通字段、数组和 opaque JSON 的按字段撤销；EV-70 继续用快捷区、递归 JSON 树和原始文本三层编辑 `parameter_schema`、tests、extensions，并保持未知内容与精确数字无损；EV-71 以同一真实连接的可控单帧丢失证明后续 sequence gap 会重取无关的 Library/Source HTTP snapshot；EV-72 再以独立单媒体合成 Source 和仅限 E2E 构建的首字节后阻塞点，锁定同一活动 Hash Job 后从 UI 取消 Scan，证明父子任务与 Attempt 收敛且不发布半成品；EV-73 沿用该确定性运行窗口强杀真实进程，立即重启同一 AppDirs 后同时核对 recovered Attempt、稳定错误、无半成品 publication 与 UI 治理；EV-74 建立首批治理 fixture，EV-75 再以同一可见 UI 覆盖 SourceWork merge、全部 orphan decision/实体类型和已消费决策冲突；EV-76 又补齐普通 Binding issue 三决定、真实 `stale`/`superseded` 生命周期、同身份 sibling 409 与 51 条 keyset 分页，并修复历史 issue 重开可破坏活动唯一性的缺陷；EV-77 最后补齐三种剩余结构 action 的应用层消费和三类孤儿重现身份语义。测试断言协议仍未冻结，Schema 表单只作辅助，服务端保存、校验和编译继续拥有最终权威。

[EV-109](证据/验证记录.md) 关闭 control 恢复的代码级 fail-open：候选落位后旧库无法回滚时不再继续 bootstrap 创建空库，而是保留 pending/轮换副本并返回 `RESTORE_FAILED`。真实 Windows 落位 sharing violation、磁盘满、ACL 与断电/中断仍是阶段 7 平台门禁，不得把确定性 seam 测试描述为 RC 证据。

2026-07-26 的 [EV-42](证据/验证记录.md#ev-42规则封面customcover-与-work-快照封面闭环e1) 又补齐规则 `CoverPath` → SourceMedia/CanonicalMedia → publication 有效封面的显式链路：同 Work 有效 CustomCover 优先，失效事实保留并回退规则封面，媒体顺序不再借用 `ordinal=-1`；`PublishedWork.coverMediaId` 为 required nullable，作品详情支持 `queryPublicationId`，Web 浏览、详情、封面与媒体沿用同一快照并提供 CustomCover 编辑。该轮通过根级 `Check.ps1`、WSL2 Debian 定向 race、合成 migration 与 Web Vitest/Chromium mock，未使用真实 Source/媒体，也没有真实后端浏览器证据；阶段 4 Reference Performance/API Freeze、阶段 5 Security Gate 与阶段 6 Web Gate 状态不变。

## 如何使用

没有参与前期设计的开发者应按以下顺序阅读：

1. [产品定义与不变量](规范/01-产品定义与不变量.md)：先明确 Gallery 是什么、什么不可违反。
2. [系统架构与模块边界](规范/02-系统架构与模块边界.md)：了解进程、模块、数据库和依赖方向。
3. [领域模型与数据所有权](规范/03-领域模型与数据所有权.md)：确定实体身份、事实来源和生命周期。
4. [扫描、Catalog 与任务](规范/04-扫描-Catalog与任务.md)：理解扫描发布、崩溃恢复和后台任务。
5. 按正在实现的主题阅读其余规范，再从 [v1 实施计划](指南/01-v1实施计划.md) 选择垂直切片。
6. 修改已冻结决策前，先查阅 [ADR 索引](ADR/README.md) 的重新审议条件。

## 文档地图

| 类型 | 文档 | 解决的问题 |
| --- | --- | --- |
| 规范 | [01 产品定义与不变量](规范/01-产品定义与不变量.md) | 产品边界、命名、v1 范围与系统不变量 |
| 规范 | [02 系统架构与模块边界](规范/02-系统架构与模块边界.md) | 技术基础、模块职责、依赖和部署形态 |
| 规范 | [03 领域模型与数据所有权](规范/03-领域模型与数据所有权.md) | 实体所有权、稳定引用、Binding、Overlay 和媒体生命周期 |
| 规范 | [04 扫描、Catalog 与任务](规范/04-扫描-Catalog与任务.md) | `query_publication_id`、Catalog/Overlay 快照、Saga、GC 与恢复 |
| 规范 | [05 规则系统](规范/05-规则系统.md) | Schema 感知规范化、RulePackage 生命周期、package/semantic/IR hash、编译、解释和测试 |
| 规范 | [06 查询、搜索与排序](规范/06-查询-搜索与排序.md) | 搜索、排序、`query_publication_id` 快照和实时附加状态 |
| 规范 | [07 API、实时协议与安全](规范/07-API-实时协议与安全.md) | REST/OpenAPI、WebSocket、认证、授权和部署模式 |
| 规范 | [08 文件系统与媒体处理](规范/08-文件系统与媒体处理.md) | 只读根、路径安全、媒体读取、派生资源和离线语义 |
| 规范 | [09 跨平台与客户端](规范/09-跨平台与客户端.md) | 平台适配层、最低支持矩阵、Web UI 与桌面壳边界 |
| 指南 | [01 v1 实施计划](指南/01-v1实施计划.md) | Walking Skeleton、Architecture Proof、阶段顺序与交付范围 |
| 指南 | [02 测试与发布门禁](指南/02-测试与发布门禁.md) | Correctness、Reference Performance、Degradation 与发布验收 |
| 设计材料 | [前端设计材料索引](前端设计/README.md) | 待评审的视觉、信息架构、交互、响应式与可访问性改版输入；不直接覆盖规范或 ADR |
| 证据 | [验证记录](证据/验证记录.md) | 仍影响当前决策的原型、样本、结果和局限 |
| 证据 | [历史重写记录](证据/历史重写记录.md) | 已发生的 Git 历史重写事实、备份位置与恢复方式 |
| 决策 | [ADR 索引](ADR/README.md) | 所有当前 ADR 的唯一状态入口 |

## 单一权威来源

| 主题 | 唯一权威文档 |
| --- | --- |
| 产品名称、边界、v1 范围 | `规范/01-产品定义与不变量.md` |
| 模块和技术边界 | `规范/02-系统架构与模块边界.md` |
| 实体、身份、数据所有权 | `规范/03-领域模型与数据所有权.md` |
| 扫描、Catalog 发布、任务恢复 | `规范/04-扫描-Catalog与任务.md` |
| 规则语义和 CEL Profile | `规范/05-规则系统.md` |
| 搜索、排序、过滤、分页 | `规范/06-查询-搜索与排序.md` |
| API、WebSocket、账户与权限 | `规范/07-API-实时协议与安全.md` |
| 文件系统、CanonicalMedia/ContentBlob 读取 | `规范/08-文件系统与媒体处理.md` |
| 跨平台和客户端形态 | `规范/09-跨平台与客户端.md` |
| 开发顺序与交付范围 | `指南/01-v1实施计划.md` |
| 验收阈值与发布门禁 | `指南/02-测试与发布门禁.md` |
| 原型数字和验证局限 | `证据/验证记录.md` |
| 决策状态与重新审议条件 | `ADR/README.md` 及其链接的当前 ADR |

其他文档只能链接到上述权威来源，不得另行定义同一概念。

## 状态词

- **规范**：当前实现必须遵守；修改通常需要同步 ADR、契约测试或门禁。
- **接受**：已有足够证据，可按决策实施。
- **有条件接受**：方向可用于原型或受限交付，但列出的门禁完成前不得冻结为不可替换实现。
- **延后**：不进入 v1；只有触发条件出现后才重新决策。
- **拒绝**：已比较且不采用；除非重新审议条件成立，不再重复论证。
- **开放问题**：证据不足，当前规范明确留白，不得由实现细节静默替代产品决策。

## 维护规则

1. 产品语义先改对应规范；若改变已接受决策，再新增或替换 ADR。
2. ADR 只记录当前决策和必要演进，不复制规范正文；状态只在 [ADR 索引](ADR/README.md) 维护一份。
3. 原型数据只进入 [验证记录](证据/验证记录.md)，并标明样本、环境、局限和是否需要重测。
4. 实施进度、一次性测试日志和修复汇报不进入本目录；代码、测试和 Git 历史承担追溯。
5. 任何新主题必须先判断能否并入现有权威文档，避免再次形成多份“最终结论”。
6. 文档不得以任何现有应用的数据库、API、配置或行为作为 Gallery 的兼容目标。
7. `前端设计/` 中的新材料默认是待评审候选；采纳后必须把产品语义同步到对应规范，涉及已接受决策时同步 ADR，不以设计稿本身替代权威文档。
