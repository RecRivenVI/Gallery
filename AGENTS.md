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
- 当前仓库已有正式产品代码（`cmd/`、`internal/`、`pkg/`、`web/`）。阶段 0 契约骨架、Walking Skeleton、Architecture Proof、阶段 1「领域和数据所有权」、阶段 2「规则闭环」、阶段 3「扫描、任务和 Catalog」与阶段 4「查询和媒体」均已完成代码与合成 Correctness；阶段 5 已完成账户/安全/多客户端代码与合成收尾，并取得同机 Personal/LAN 与当前工作站 Argon2id 补证，但正式 Security Gate 未通过；阶段 6 已实现 React/TypeScript Web/PWA 双入口及认证、浏览/媒体、Overlay、任务、规则、安全和维护页面，EV-54～EV-91 已将主要业务、治理、恢复、弱网切片、响应式、Source 作者与授权 Job 历史接入 Chromium/Firefox 真实 `galleryd` 门禁，EV-102 又部分采纳共享动效基座，补齐同范围旧快照保护、作品身份承接、媒体显现、直接操纵、浮层与管理端克制反馈，但正式 Web Gate 未通过。EV-92 首次把 legacy schema v3 的真实 Pixiv 规则经正式转换接入独立 AppDirs，在 370,712 文件/105,202 目录上完成 45 秒有界 `index` 与全树零写入 guard；EV-93 修复 discovery 不响应 Scan context，EV-94 再以真实 Pixiv 将扫描总耗时从修复前 116,584 ms 收敛到 45,397 ms，并在取消 POST 后 201 ms 观察到终态、同 AppDirs 恢复重跑 7/7 通过。该结论只覆盖 Windows 本地 SSD/Pixiv/index discovery，不是全量扫描/哈希、完整规则语义或性能 Gate。阶段 4 的 EV-35/36/51/52/87～89 已建立 500,000 合成 Correctness 与多项候选/聚合性能基线，EV-95 又关闭 Creator 治理入口的无界响应；EV-96 建立十来源、全局 1%/10%/50% 变化的生产 Store publication 执行器，EV-97 真正实装每 Work 两条 Creator 关系并加入 fail-closed 断点续跑，EV-98 再以纠正形状完成 100,000 Work 低资源预检 3/3；EV-99 又把十个权威目标来源代号与 `goMaxProcs` 纳入报告/续跑指纹；EV-100 再让通用 Query Reference seed/probe 强制相同 500k/十目标来源/双关系形状并在计时前绑定实际 active publication；EV-101 又让 63 组合 Query 矩阵按完整成功前缀原子分窗续跑，并把矩阵、缓存、publication 与环境封入报告身份。阶段 7 的 EV-103～EV-107 已建立未签名 Windows 便携制品、同源切换/恢复、损坏备份失败回滚、真实 schema 23→24/反向拒绝与首次轮换拒绝；EV-109/110/119 又覆盖落位后连续性 fail-closed、真实安全回滚和包内双 sharing-violation，EV-121/122 再关闭当前库缺失时的 pre-placement 空库风险及落位后安全收尾中断窗口；EV-123 进一步以真实便携进程证明 `restore-last` 原子替换失败和 pending 删除 sharing violation 均在 descriptor 前 fail-closed；EV-124 又在同一个真实便携进程中同时阻断候选落位与旧库回滚，证明双 `ERROR_SHARING_VIOLATION` 会保留 pending、候选和字节精确的旧库轮换副本，并能在解除阻断后完成同一恢复请求；EV-125 再于 `placed_pending_finalize` 已持久化、descriptor 尚未发布的窗口真实强杀该进程，证明同一 pending 可在重启后续接且旧 Session、用户事实与轮换副本按预期收敛；EV-133 又把 Windows v1 已验证升级范围连续扩展为 control schema 20/21/22/23→24，并把支持范围写入便携包清单。正式签名、安装/更新、schema 20 以前的开发快照、磁盘满/低完整性/多账户/继承 ACL、其它恢复窗口强杀与真实断电仍未完成。EV-108 已用真实 schema v3 转换结果关闭 Gank/Pawchive“完全未进入真实 Source”的旧缺口：Gank 12/12、Pawchive 2/2 按需确认成功且全树零写入；但 Pawchive 12 目标运行在共享墙钟到界后 30 秒仍未收敛，暴露每目标重复全 Source 处理与取消响应缺口。正式 500,000 多样本、并发、冷缓存、Degradation、API Freeze、完整真实规则语义与全量扫描仍未完成。
- **EV-111 更新 EV-108 的当前结论**：Pawchive 12 目标不再逐个重复完整 Source 处理；同一 current publication、同一 Source 的 1～200 个唯一未确认媒体可经兼容批量 API 原子建立一个目标化 Scan Job，单媒体入口与用户端交互保持不变。真实 Pawchive 12 目标连续两轮通过，最终轮 `jobs=1`、12/12、确认 74,003 ms，全树 11,595 文件/2,353 目录增删改 0。EV-108 的旧取消失败仍是历史事实，但本成功链没有触发取消，因此真实活动 Hash/HDD/SMB/NAS 取消、完整规则语义、全量扫描、正式性能/API Freeze 与 RC 仍未完成。
- **[EV-39](Documents/证据/验证记录.md)（2026-07-23）的真实浏览器复核下调了阶段 6 的完成度陈述**：`/ws/v1` 在 Chrome/Edge 中 100% 握手失败（服务端对 WebSocket 强制要求浏览器不会发送的 `Sec-Fetch-Site` 头），前端信封字段名与 `internal/contract/realtime` 不符，且 6 个前端 capability 名不在后端权威词表中，导致 Overlay 编辑、任务取消/重试、Library 创建、Source 登记、按需内容确认与全部治理动作对任何主体都不渲染。[EV-40](Documents/证据/验证记录.md) 已修复这些阻断项并经真实 Chrome/Edge 复验；EV-54～EV-77 已建立管理自举、publication-bound 画廊/媒体、CustomCover、规则发布/回滚/弃用、ParameterSet、无损文本、Schema 表单、当前草稿 Dry Run/Explain/Trace、按字段撤销、参数 Schema/tests/extensions 无损结构化编辑、安全资源管理、断线与单帧 sequence gap 的恢复、维护、规则绑定状态、作品/媒体人工解绑与撤销、普通 Binding issue 三决定/生命周期/分页、全部五种 SourceWork 结构 action、全部 orphan decision/实体类型及重现身份语义、已消费决策冲突、retry-backoff Job 取消/重试、运行中 Scan/Hash 级联取消、进程强杀后的启动接管及 control 恢复实际重启的 Chromium/Firefox 真实后端 E2E，并关闭显式扫描与 Watcher 状态脱节、维护 Job 终态不刷新的持续门禁竞态；EV-78/EV-79 又建立当前双入口的窄屏焦点与最低宽度 Grid 溢出回归，EV-80 再修复长离线耗尽重连预算及 Firefox 丢失 4401 时认证态不能收敛，并把连续三轮 offline/online 恢复接入双浏览器真实后端门禁，但**仍不得把阶段 6 描述为「已完成业务闭环」**——真实设备、完整弱网与可访问性门禁仍未完成。[EV-44](Documents/证据/验证记录.md) 又关闭 `AUTHZ-1` 与 `QRY-1`，[EV-45](Documents/证据/验证记录.md) 关闭 `TEST-2`，[EV-46](Documents/证据/验证记录.md) 关闭 `MED-1` 与 `SEC-3`。**EV-39 登记的缺陷至此全部关闭**；EV-46 另行发现并修复了三项新缺陷（`LINK-1` Windows 目录联接被识别为普通文件、`TX-1` WAL 下读后写事务的过期读快照失败、迁移预算门禁用固定墙钟导致不可复现），详见 EV-39、EV-40、EV-44、EV-45 与 EV-46。
- [EV-42](Documents/证据/验证记录.md)（2026-07-26）补齐规则 `CoverPath` 到 SourceMedia/CanonicalMedia、显式规则/有效封面投影、CustomCover 优先与失效回退、`PublishedWork.coverMediaId`、Work 详情快照绑定和 Web 同快照封面/编辑；当前证据为根级 `Check.ps1`、WSL2 Debian 定向 race、合成 migration 与 Web Vitest/Chromium mock，不含真实 Source、真实媒体或真实后端浏览器，阶段 4/5/6 Gate 均未因此通过。
- [EV-67](Documents/证据/验证记录.md)（2026-07-28）把当前 `gallery-primitives-v7` 的 15 类 primitive 配置字段纳入后端权威 RulePackage Schema `$defs`，Web 依据 sibling `kind` 动态生成嵌套控件，并以 Chromium/Firefox 真实后端链验证表单修改可无损进入草稿文本。该切片关闭“primitive config 全部只能手写 JSON”的缺口，但 `parameter_schema`、tests、extensions 的任意 JSON 可视化构建仍未完成，**不得据此声称任意规则已可从空白完全可视化构建或 Web Gate 已通过**。
- [EV-68](Documents/证据/验证记录.md)（2026-07-28）把既有 `rules.debug` 后端契约接入规则草稿页：当前未保存规范 JSON、无损参数与显式合成 Sample 可分别执行 Dry Run、Explain、Trace，精确数字在请求和响应中不经 JavaScript `Number`，输入变化后旧结果失效隐藏；Chromium/Firefox 隔离真实后端链已覆盖该闭环。该切片不读取真实 Source/metadata，也不完成完整治理或 Web Gate；当时遗留的三个任意 JSON 根字段已由 EV-70 补齐。
- [EV-69](Documents/证据/验证记录.md)（2026-07-28）为 Schema 表单建立基于本地 `baseRevision` 精确快照的按字段撤销：普通字段按 JSON Pointer 列出差异，数组长度变化与 opaque 根字段作为原子字段，非法无损 JSON 也可独立恢复；远端 revision 漂移时不会把后来内容冒充撤销基线，全部字段恢复后精确回到原草稿字节。组件及 Chromium/Firefox 隔离真实后端链已覆盖该闭环；它不提供远端合并，三个根字段继续保持原子撤销，结构化构建随后由 EV-70 补齐。
- [EV-70](Documents/证据/验证记录.md)（2026-07-28）为 `parameter_schema`、tests、extensions 建立常用结构快捷区、递归任意 JSON 树和无损原始文本；分类/legacy extension、未知字段与精确数字均保持，非法数字阻止保存。Chromium/Firefox 隔离真实后端链证明结构进入精确草稿且 required 参数 Schema 参与真实 Dry Run；服务端保存、Schema 校验、extension 注册表与 CompilePackage 仍为最终权威，测试断言 DTO 没有因此冻结。
- [EV-71](Documents/证据/验证记录.md)（2026-07-28）在 Chromium/Firefox 与真实 `galleryd` 的同一 WebSocket 连接中可控丢弃一条合法非 ready 信封，下一条真实信封形成严格 sequence gap 后，生产状态机必须重取已挂载的 jobs、libraries 与 sources HTTP snapshot；Job 可见状态、socket/CSP、同 AppDirs 恢复重启、LAN 链和 Source guard 等价通过。该证据关闭显式单帧丢失 gap 的真实门禁，不代表反复 offline/online、长延迟、重连风暴或退避耗尽等完整弱网矩阵。
- [EV-72](Documents/证据/验证记录.md)（2026-07-28）使用独立单媒体合成 Source 与仅存在于 E2E build tag 的首字节后阻塞点，从 Chromium/Firefox 可见 UI 发起真实 incremental Scan，锁定同一个已实际读取数据的活动 Hash Job 后取消，并要求父 Scan、两级 Attempt 与该 Hash 子任务持久收敛为 cancelled、不得产生 publication、`pendingHashCount=0` 且 Source guard 不变；测试末尾通过 UI 暂停专用 Binding，避免 Watcher 污染后续 publication。该证据关闭合成 loopback 的运行中取消浏览器门禁，不代表真实 HDD/SMB/NAS 取消响应、publishing 临界点或进程强杀恢复。
- [EV-73](Documents/证据/验证记录.md)（2026-07-28）强杀持有未来租约的真实 `galleryd` 后立即重启同一 AppDirs：新单写者在 Scheduler 接收提交前先完成 publication-first 对账，再把遗留 Attempt 收敛为 recovered/`PROCESS_INTERRUPTED`；运行期间仍尊重租约。Chromium/Firefox 14/14 均从 UI 核对 recovered 历史、无半成品 publication、取消后续重试和暂停专用 Binding；Windows descriptor 以当前 PID 与非空启动 nonce 拒绝旧端口。该证据只使用合成 Source/loopback，不代表真实存储崩溃响应或完整 Web Gate。
- [EV-74](Documents/证据/验证记录.md)（2026-07-28）修正 Binding issue 可重开状态与后端权威状态机不一致，并把人工解绑撤销收紧为显式 `entityKind: work|media`；Chromium/Firefox 各 16 个真实后端测试从可见 UI 覆盖 issue 忽略/重开/解决、SourceWork split 决策/撤回、Work orphan 延长及 Media 解绑/撤销。fixture 只在服务停止后通过应用层建立，Source guard 覆盖四个治理子根；剩余治理分支、真实 Source/设备与 Web Gate 仍未完成。
- [EV-75](Documents/证据/验证记录.md)（2026-07-28）在同一双浏览器真实后端链补齐 SourceWork merge 决策/撤回、已消费 split 决策撤回的 409 `CONFLICT`、Work orphan unbind/显式恢复、Creator `confirm_orphaned` 与 Media `retain`；四种 orphan decision 和三类实体至此至少各有一条浏览器写路径。证据仍为应用层合成 fixture/loopback，不等于治理组合穷举或 Web Gate 完成。
- [EV-76](Documents/证据/验证记录.md)（2026-07-28）修复历史 Binding issue 重开可绕过同身份 open/dismissed 活动唯一性的缺陷，并在 Chromium/Firefox 真实后端链补齐 `bind_existing`、`keep_separate`、真实 `stale`/`superseded` 生命周期、双标签页 sibling 409 与 51 条 keyset 分页；治理 Source guard 扩展到 8 个合成子根。该证据仍不包含其它结构 action、孤儿重现、真实 Source/设备或完整 Web Gate。
- [EV-77](Documents/证据/验证记录.md)（2026-07-28）在同一双浏览器真实后端链补齐 `split_keep_same`、`split_create_new`、`merge_create_new` 的可见写入和应用层实际消费，并以同一 AppDirs 再次重启验证 Work/Creator/Media 孤儿重现复用、Work `manual_unbound` 身份拆分及已消费撤回冲突；两浏览器完整链各为 17 个实际测试，Source guard 扩展到 11 个合成子根。该证据仍只使用合成 Source/同机 loopback，不代表完整 Web Gate。
- [EV-78](Documents/证据/验证记录.md)（2026-07-28）基于当前从零重写后的双入口 UI 建立 42rem 窄屏模态导航：常驻导航与触发按钮互斥进入可见/焦点树，React Aria Dialog 负责焦点陷阱、Escape 关闭和焦点返还，`NavLink` 暴露当前页；Chromium/Firefox 390×844 smoke 合计 10/10 覆盖双入口 axe、正反向 Tab、路由关闭与无横向溢出。该证据是桌面浏览器 viewport 模拟，不代表真实移动设备、触控或人工屏幕阅读器门禁。
- [EV-79](Documents/证据/验证记录.md)（2026-07-29）纠正 EV-78 的跨平台溢出证据：精确 HEAD `ee69eee` 的 Actions run `30374209499` 中 Windows/Ubuntu Go Job 通过，但 Linux Chromium 在 `/manage/scans` 390×844 以 `scrollWidth=393 > clientWidth=390` 失败，Firefox 通过。当前实现把管理主区、Section 与契约说明卡的单列 Grid 改为可收缩轨道，并允许长协议标识换行；Windows 双浏览器 10/10 与 WSL Linux Chromium 320/360/390/412px 产物探针通过，最终文档 HEAD `dd0343e9f4740d2fcd5f3b7fd9f004c1218cd743` 的 Actions run `30378490971` 全部成功（含 Ubuntu race/漏洞扫描与双浏览器真实后端 E2E）。该修复仍不代表真实移动/触控或人工屏幕阅读器门禁。
- [EV-80](Documents/证据/验证记录.md)（2026-07-29）修复设备离线仍消耗 8 次退避并在约 75 秒后永久停止重连，以及 Firefox 将 Session 吊销 4401 暴露成 1006 后旧认证态无法收敛的问题。离线期间现在暂停 timer/预算，`online` 后立即刷新 bootstrap、重建 socket 与 HTTP snapshot；连接 generation 拒绝迟到旧回调，在线异常关闭在第一次退避前刷新 bootstrap。Chromium/Firefox 各 17 个真实后端测试连续三轮切换 offline/online，并逐轮验证新 socket、bootstrap/安全快照和断线期间另一 Session 创建的事实；这仍不是带宽、延迟、丢包、服务长停机和移动网络切换的完整弱网矩阵。
- [EV-81](Documents/证据/验证记录.md)（2026-07-29）把游标错误通知绑定到产生它的搜索、排序、过滤和资源范围，避免旧 `CURSOR_INVALID` 跨查询残留并阻止新搜索续页；所有画廊查询继续消费 TanStack Query `AbortSignal`。组件回归锁定旧分页与旧 Work 详情在取消后仍迟到也不能覆盖新搜索/路由，Chromium/Firefox 生产资产 smoke 进一步锁定旧分页迟到返回不进入新结果。证据是可控合成延迟，不代表随机延迟/丢包、带宽限制、代理、服务长停机或移动网络门禁。
- [EV-82](Documents/证据/验证记录.md)（2026-07-29）修复浏览器仍在线但 `galleryd` 长时间不可用时，8 次普通重连耗尽后页面永久停止自愈的问题。普通异常关闭现在以 15 秒封顶持续退避，4401/4403/协议不兼容继续保持终态；Chromium/Firefox 各 18 个真实后端测试在同一页面、Session、临时 AppDirs 与 origin 中优雅停止服务，跨过旧预算后按原端口重启，并验证 WebSocket 与 `/api/v1/jobs` HTTP snapshot 无刷新恢复。该证据不代表随机延迟/丢包、带宽、代理、移动网络或反复崩溃循环门禁。
- [EV-83](Documents/证据/验证记录.md)（2026-07-29）把一次作品 GET 传输中断、第二次请求 300 ms 受控延迟，以及旧结构化错误无视 `AbortSignal` 后迟到交付接入真实 `galleryd` 的 Chromium/Firefox 完整链。两浏览器各 19 个实际测试均要求网络层 `TypeError` 自动重试后显示真实后端数据，且旧 `FORBIDDEN` 不得覆盖新搜索；现有生产实现无需修改。该证据不代表随机延迟/丢包分布、带宽、代理、移动网络或真实设备门禁。
- [EV-84](Documents/证据/验证记录.md)（2026-07-29）按三份 Legacy 前端设计材料净室重做共享视觉基座、用户端媒体优先侧栏/抽屉与管理端紧凑控制台/响应式表格，同时保持现行认证、Capability、publication、Overlay、规则、安全、维护和治理契约。Chromium/Firefox mock smoke 14/14、各 19 项隔离真实 `galleryd` 完整链及根级 202 项 Vitest 均通过；证据仍是桌面浏览器 viewport 和合成 Source，不代表真实移动/触控、人工屏幕阅读器、全页面可访问性或正式 Web Gate。
- [EV-85](Documents/证据/验证记录.md)（2026-07-29）在仅限 E2E build tag 的真实媒体读取阻塞点下，把服务端闸门收窄为 1：独立标签页占住已打开的合成 Source 句柄，用户端必须收到真实 503 `MEDIA_READ_BUSY/retryable=true`，不刷新页面地自动退避并在释放后恢复 200 与图片解码。Chromium/Firefox 各 20 项完整链和 Source guard 通过；默认 16 名额/5 秒仍为 PRE_FREEZE，真实 HDD/SMB/NAS、大文件/视频 Range、多客户端争用和正式 Web Gate 未因此通过。
- [EV-86](Documents/证据/验证记录.md)（2026-07-29）关闭 Creator/Library 聚合封面的逐主体资源授权缺口：身份可见性继续只要求 `library.read`，非空封面候选则在 publication 冻结 Source 成员上批量求 `library.read` 与 `media.read` 的交集，并应用 deny 与 Token scope；全成员主体复用已物化全局结果，受限主体从同快照回退到下一条获授权候选。LAN HTTP 回归覆盖列表/详情、缺少 `media.read`、两种 Source deny、Source Token scope 与回退；该证据是合成同机授权正确性，不代表正式 500,000 受限重选性能、物理 LAN 或 Security Gate。
- [EV-87](Documents/证据/验证记录.md)（2026-07-29）增加 catalog v20 `creator_source_cover_projections`，把每个 publication 内每个 Creator/Source 的最佳封面候选持久化，并记录全局聚合胜出项的 `source_id`；受限 Creator 查询按 allowed/deny 规模选择 Source 索引窗口或仅对被拒绝全局胜出项沿 rank 索引回退，不再请求期连接 WorkProjection/Creator 关系。当前工作站 500,000 Work、5,000 Creator、10 Source、暖缓存、单并发 31 次下三路径 P95 为 11.7/9.6/130.1 ms；50,000 Creator 高基数下为 276.1/285.0 ms/1.32 s，明确保留为 Degradation 与响应分页化缺口。该夹具不是正式十来源完整关系、变化 publication、并发或冷缓存矩阵，Reference Performance Gate 状态不变。
- [EV-88](Documents/证据/验证记录.md)（2026-07-29）让 Creator/Library 详情只向 Catalog 请求单一 scope ID，身份被授权裁剪的列表只请求最终可见 ID；全部身份可见的列表继续走原全量快路径，不改变公开 API、DTO 或授权语义。定向 Creator 查询在 500,000 Work、5,000 Creator 下 P95 1.00 ms，在 50,000 Creator 高基数下 P95 0.55 ms；同轮全量高基数 deny-winner P95 波动到 9.31 s，证明未分页 Creator 列表仍是 Degradation 缺口。该证据不代表正式并发/冷缓存/真实存储矩阵或 Reference Performance Gate 通过。
- [EV-89](Documents/证据/验证记录.md)（2026-07-29）为 `GET /api/v1/creators` 增加用户分页浏览模式：浏览参数在授权 active Source 与有效合并根上按 NaturalSortKey v2/keyset 分页，cursor 绑定授权与查询指纹；Source 作者页、等价组封面和后续作品过滤保持同一平台范围。control v23 增加/原子回填 Creator 排序键；100,000 Creator 无合并 P95 为 0.621 ms，一个合并图 P90 43.6 ms。Chromium/Firefox mock 与各 21 项真实后端完整链均覆盖该入口；当时保留的无参数全量响应已由 EV-95 收口，并发/冷缓存/超大合并图、API Freeze 与正式 Web/Performance Gate 仍未完成。
- [EV-90](Documents/证据/验证记录.md)（2026-07-29）把 `creator.id` 结构化过滤从阶段 4 testlab 的显式 limitation 改为持续 finding：真实扫描建立 12 个同名轮换但身份独立的 CanonicalCreator 后，逐 ID 叠加 Source 范围查询，要求 exact total、展示身份、Work 唯一归属与全量覆盖；阶段 4 smoke 现为 40 项 query + 6 项 Cursor + 20 项 media/derived。该证据仍为 1k Catalog 加 12 Work 合成 Source，不替代 500,000 十来源、真实平台、Reference/Degradation 或 API Freeze。
- [EV-91](Documents/证据/验证记录.md)（2026-07-29）把 Job 历史从最旧优先的全量读取/N+1 改为 control v24 索引支持的新到旧 keyset 分页；严格 cursor 绑定状态、limit 与授权指纹，但只作为边界，所有候选仍在响应 limit 前逐 Job 重新授权。EV-91 当时的管理端仍会把已加载页连续累积到同一张表；该 UI 行为已由 EV-136 取代。该证据不代表超大历史性能、API Freeze、Web Gate 或 RC 已完成。
- [EV-92](Documents/证据/验证记录.md)（2026-07-29）首次把用户提供的 legacy schema v3 规则经正式 `rulesimport` 接入真实 Pixiv Source：转换产物含 36 个 primitive，完整只读 guard 为 370,712 文件、105,202 目录、562,792,663,280 bytes；45 秒有界 `index` 最终 cancelled，前后 added/removed/modified 均为 0。`sourcelab` 现让专用 Binding 只在显式 Job 创建窗口 active，冻结快照后立即 paused，并在续跑 semantic hash 不同时 fail-closed，关闭长 guard 期间 Watcher 抢跑导致 409 的编排缺陷。取消终态总耗时 116,584 ms；这不是全量扫描/哈希/发布、完整规则语义、正式性能或 RC Gate。
- [EV-93](Documents/证据/验证记录.md)（2026-07-29）修复 EV-92 暴露的 discovery 取消缺口：HTTP/Scheduler 已取消运行中 Scan context，但 `filepath.WalkDir` 不感知 context，导致真实大 Source 仍继续枚举。现在每个回调在后续 Source 读取前检查 `ctx.Err()`，由既有持久取消状态机收敛 Job/Attempt；Windows 定向、低资源 WSL2 race 和根级检查通过。尚未重新运行真实 Pixiv，116,584 ms 只作为修复前基线，真实取消响应 Gate 仍未通过。
- [EV-94](Documents/证据/验证记录.md)（2026-07-29）在真实 Pixiv/Windows 本地 SSD 上复测 EV-93：首轮 Scan 与 Attempt 均在 45 秒边界同秒 cancelled、无 publication，取消 POST 后 201 ms 已观察到终态；13 分钟外层看门狗只因 6 次全树 guard 尚未全部结束而触发。随后同 AppDirs 恢复重跑 531.695 秒完整通过，7 findings/0 failures，`bounded-index-scan=45,397 ms`，final guard 在 370,712 文件/105,202 目录上 added/removed/modified 均为 0。该证据关闭当前 Pixiv/index discovery 取消补证，不代表活动 Hash、HDD/SMB/NAS、全量扫描/哈希/发布或 RC Gate。
- [EV-95](Documents/证据/验证记录.md)（2026-07-29）把 `GET /api/v1/creators` 的无参数治理读取从全量响应改为默认 50、最大 200 的授权 keyset 页；只带 limit/cursor 的请求保持治理模式，继续暴露已合并 base 身份与任意状态 Binding 证据，`effectiveId` 只递归本页身份，global/受限授权与封面 scope 均在 LIMIT/响应物化前收窄。51 项 HTTP/生成客户端回归以 20/20/11 无重无漏通过，Windows 根级检查与 WSL2 定向 race 通过。该切片关闭一条明确宽查询，但不代表整体 API Freeze、Reference/Degradation、Web/Security Gate 或 RC 完成。
- [EV-96](Documents/证据/验证记录.md)（2026-07-29）增加独立 `publication-perf` 执行器：十 Source 以主 Source 占全局 50% 的加权语料，精确执行全局 1%/10%/50% 变化并沿生产 Store 完成 Stage/Overlay/Validate/Publish/GC/Checkpoint；`reference` 模式强制 500,000 Work 且每档至少 20 样本，降级形态不能冒充正式结果。其初版虽在报告中声明每 Work 两条 Creator 关系，实际只写入一条；因此旧 1,000/100,000 Work 数字只保留为容量与工具证据，不是正式双关系形状证据。
- [EV-97](Documents/证据/验证记录.md)（2026-07-29）为生产 Catalog Stage 增加多 Creator 关系事实，保持旧主关系字段兼容，并在候选写入前拒绝空字段、负 ordinal 与重复 `(role, ordinal)`。`publication-perf` 现真实写入和核对每 Work 两条关系，且可通过原子报告 fail-closed 续跑：复核参数、宿主/存储环境、active publication 与完整计数，收敛遗留 staging candidate，且不重复已记录样本。纠正后 1,000 Work 低资源预检 6/6 通过；正式 500,000 多样本、并发、冷缓存和 Degradation 仍未执行，Reference/API Freeze Gate 不变。
- [EV-98](Documents/证据/验证记录.md)（2026-07-29）以 ProcessorAffinity 0～1、`GOMAXPROCS=2`、包并发 1 和 `BelowNormal` 完成纠正后 100,000 Work/10 Source publication 容量预检：全局 1%/10%/50% 单样本 3/3、0 failure，每轮均核对 200,000 条 WorkCreator 关系、100,000 媒体/FTS 投影和 66,666 Blob/Location，三档完整候选总耗时 310.799/123.139/120.941 秒。相同亲和性的完成报告 `-resume` 退出码 0，不同亲和性则明确以 `cpuLogicalCores` 漂移 fail-closed。该结果仍不是 500,000 正式多样本、冷缓存、并发或 Degradation，Reference/API Freeze Gate 不变。
- [EV-99](Documents/证据/验证记录.md)（2026-07-29）在正式 500k 长跑前加固报告身份：`CorpusFacts` 现按顺序记录 Pixiv、Pixiv FANBOX、Gank、Fantia、Patreon、Pawchive、X、微博、微博 Legacy 和 Venera 的非敏感代号，环境事实新增实测 `goMaxProcs`，两者均进入续跑指纹。1,000 Work 报告 3/3、0 failure、退出码 0，同指纹续跑退出码 0；只把 `GOMAXPROCS` 从 2 改为 1 时稳定以 `goMaxProcs` 漂移退出码 1。该切片不是 500k 结果，Reference/API Freeze Gate 不变。
- [EV-100](Documents/证据/验证记录.md)（2026-07-29）关闭通用 Query Reference 入口仍可被单关系、匿名 Source 或错配 AppRoot 冒充的缺口：共享 corpus 现统一 500k/10 Source/每 Work 两关系及十目标来源顺序；`testlabseed -tier reference` 在构建前拒绝降级参数，manifest/report 持久记录关系数与来源代号；`testlabprobe` 在计时前验证完整 reference manifest，并从真实 HTTP 核对当前 publication/Catalog revision 与 manifest 一致。预检不进入分位数且使 warm 首组合不再误标冷，cold-process 仍逐组合重启。该切片只加固入口，尚未执行新的 500k Query 冷/热矩阵，Gate 不变。
- [EV-101](Documents/证据/验证记录.md)（2026-07-29）为 Query 性能矩阵增加 fail-closed 原子分窗续跑：报告指纹覆盖组合顺序/次数、单项超时、缓存/warmup、query publication/Catalog revision 与实测环境；只允许受控 warm/cold-process，且完整成功前缀才可复用，失败/超时/未派发组合不能被续跑洗白。隔离 1,000 Work/真实 `galleryd` 从 0/63 断点恢复到 63/63、0 failure，完成报告再次续跑为 no-op；该切片仍未执行正式 500k Query 数值，Reference/API Freeze Gate 不变。
- [EV-102](Documents/证据/验证记录.md)（2026-07-29）部分采纳新增共享动效基座并按当前 React/契约净室实现：共享四档时序与曲线；作品查询只在相同 Source/Library/Creator 范围保留旧快照视觉，旧网格 `inert`/`aria-hidden`，新数据以稳定 `work.id` 做有预算、可中断且彻底清理的位移/进出交接；媒体解码后在固定槽位显现，灯箱直接操纵不加缓动，管理端仅继承局部状态/浮层反馈。211 项 Vitest、Chromium/Firefox mock smoke 20/20 及两浏览器各 21 项隔离真实 `galleryd` 完整链通过；当前 cursor/下限 total 不支持精确页码滑轨，物理移动设备、人工屏幕阅读器和正式 Web Gate 仍未完成。
- [EV-113](Documents/证据/验证记录.md)（2026-07-30）把当前用户端 10 条、管理端 9 条路由的稳定成功/空/错误状态统一纳入 WCAG 2 A/AA axe，并在 1280×800 与 390×844 两档 viewport 对 Chromium/Firefox 各检查 38 个最终 DOM 状态；完整 mock smoke 两浏览器 22/22、根级检查 609.3 秒通过。首轮 Firefox 因整条矩阵沿用 30 秒普通预算，在完成用户端后被测试自身超时；改为该重型矩阵专用 90 秒后 40.2 秒通过。**该证据只关闭当前路由表的桌面浏览器自动 axe 切片，不代表交互状态组合穷举、真实移动/触控、缩放/高对比、人工屏幕阅读器或正式 Web Gate**。
- [EV-116](Documents/证据/验证记录.md)（2026-07-30）把同一 19 条路由扩展到 320×800、应用高对比主题、Playwright `forced-colors`/`prefers-contrast` 模拟和 WCAG 文本间距覆盖；修复显式主题优先级导致系统色失效、Firefox 系统调色板中按钮/链接语义混用，以及管理概览固定 20rem Grid 轨道溢出。Chromium/Firefox 定向 2/2、完整 mock smoke 24/24，15 文件 212 项 Vitest 与 595.7 秒根级检查通过；实现提交 `4374906` 含原始 `gpgsig`。**320 CSS px 只是 1280px 在 400% 下的等效重排宽度，forced-colors 也是桌面浏览器模拟；不代表真实浏览器缩放、物理 Windows High Contrast、真实移动/触控或人工屏幕阅读器 Gate**。
- [EV-117](Documents/证据/验证记录.md)（2026-07-30）在 EV-116 的相同组合下增加作品自定义封面选单、维护表单错误、维护确认对话框、Token 表单错误和一次性密文对话框五个关键交互状态；每个状态都要求无页面级横向溢出且 WCAG 2 A/AA axe 零 violation。Chromium/Firefox 定向各 1/1、完整 mock smoke 26/26，491.3 秒根级检查通过；测试提交 `727ce2d` 含原始 `gpgsig`。**该切片使用合成 API，不证明真实后端安全写链，也不等于交互状态穷举、真实设备或人工辅助技术 Gate**。
- [EV-118](Documents/证据/验证记录.md)（2026-07-30）把 EV-117 的 Token 校验/创建/一次性密文/吊销和维护校验/确认状态接入隔离 Personal `galleryd`，并从用户端与管理端可见按钮完成真实配对；检查发现 React Aria `isPending` 的 live-announcer 会在认证壳立即卸载后短暂留下失去标签目标的 `role=img`，现改为禁用按钮配合稳定显式 `Spinner`。最终 Chromium/Firefox 定向各 1/1、完整 mock smoke 26/26，541.3 秒根级检查通过；测试提交 `9c39479` 与修复提交 `d8b6d2b` 均含原始 `gpgsig`。**该切片仍是 320px 桌面浏览器模拟与同机 loopback，不代表物理高对比、真实缩放/触控、人工屏幕阅读器、交互穷举或 Web Gate 完成**。
- [EV-114](Documents/证据/验证记录.md)（2026-07-30）纠正外部工具门禁的文档漂移：既有提交 `2af146a` 已用真实测试二进制构造父子孙 OS 进程树，让 1,024-byte 输出上限和 3 秒执行超时实际触发，并要求孙进程心跳停止；Windows 两包各 `-count=5` 与 WSL2 Debian 定向 `-race` 复验通过。该证据关闭“测试从未真正触及上限”的错误陈述，但生产 Resolver 仍为空，也没有真实 ffmpeg/ffprobe、恶意媒体容器或 OS 级 CPU/内存硬限额证据，**不得据此声称 Security Gate 已通过**。
- [EV-115](Documents/证据/验证记录.md)（2026-07-30）增加默认关闭、只接受显式绝对路径的真实 `ffprobe` 门禁：同一工具先在 64 KiB 上限下成功并证明版本输出超过 128 bytes，再在 128-byte 上限下失败；无数据 loopback 输入在 3 秒执行上限后收敛，纯合成截断 MP4 被拒绝。Windows 最终 `-count=5` 与 WSL2 默认跳过路径全包 `-race` 通过。该测试没有把本机路径写入仓库，也没有启用生产 Resolver/版本允许列表；单个截断容器不是恶意语料库，CPU/内存硬限额仍缺，**Security Gate 状态不变**。
- [EV-103](Documents/证据/验证记录.md)（2026-07-30）开始阶段 7 的窄 Windows x64 便携测试制品基线：从精确干净提交注入 SemVer，构建 `CGO_ENABLED=0` 的 `galleryd.exe`/`galleryctl.exe` 并同源携带完整当前双前端；三个 CycloneDX SBOM、发行清单、包内/外 SHA-256、实际 Authenticode 状态、ZIP 越界检查、动态 loopback 启动和同 AppDirs 强杀重启进入独立 smoke 与 Windows CI。精确提交 `ac92f57` 的 12,454,092-byte 本地包通过，清单为 `dirty=false`/`unsigned`；它没有安装器、自动更新、CredentialStore、正式签名/时间戳、真实升级/回滚或桌面壳，**不是 RC，阶段 7 Gate 仍未通过**。
- [EV-104](Documents/证据/验证记录.md)（2026-07-30）把 Windows CI 扩展为两个独立便携 ZIP 的同源双版本标签切换门禁：每个包先独立验证摘要/SBOM/版本/签名与嵌入 Web，再从旧标签在临时 AppDirs 建立用户事实和 control 备份，以新标签承接事实、dry-run 校验备份、登记恢复并在同 AppDirs 重启后核对备份后哨兵消失；两个解压程序树运行前后按目录、长度和 SHA-256 封印一致，三个 `galleryd` 均优雅停止。精确提交 `3ef9acf` 的 `0.1.9-ev104`/`0.2.0-ev104` 本地包均为 `dirty=false`/`unsigned` 并通过；两者来自同一源码，因此只证明制品编排、程序/数据分离和恢复主路径，**不代表真实历史 Schema 升级、降级、损坏备份/磁盘满失败回滚、正式签名或 RC Gate 已完成**。
- [EV-105](Documents/证据/验证记录.md)（2026-07-30）把 control 损坏备份失败回滚加入同一真实便携制品链：正常恢复完成后再建立备份与备份后用户事实，登记恢复、优雅停机并只在临时 AppDirs 内破坏该备份；下一次真实 `galleryd` 启动必须保留当前库与备份后事实、消费 pending 标记、记录 `applied=false`，程序树封印与全部优雅停止继续成立。精确提交 `61211f2` 的 `0.1.9-ev105`/`0.2.0-ev105` 干净未签名本地包通过；这关闭损坏备份主路径，**不代表真实历史 Schema 升级/降级、磁盘满、权限失败、原子落位中断、正式签名或 RC Gate 已完成**。
- [EV-106](Documents/证据/验证记录.md)（2026-07-30）建立首条真实历史提交/Schema 门禁：从当前 HEAD 的真实祖先 `60dbdd9` detached clone 构建最后一个 control schema 23 `galleryd`，从当前干净提交 `a063583` 构建 schema 24 程序；旧程序创建的用户事实与备份经当前程序启动迁移后保留，备份 dry-run 明确 `WillMigrate=true`，旧程序随后以未知 migration 24 fail-closed，前后 control 文件封印一致，当前程序可再次启动并读回迁移前后事实。固定工具链根级检查与干净低资源链通过；这只覆盖 23→24 及反向拒绝，**不代表任意旧版本、磁盘满/权限/中断、正式签名或 RC Gate 已完成**。
- [EV-107](Documents/证据/验证记录.md)（2026-07-30）把真实 Windows `FILE_SHARE_DELETE` 拒绝接入便携恢复链：探针以允许其它进程读写、但禁止删除/重命名的 handle 持有临时 AppDirs 当前 `control.db`，真实 `galleryd` 的首次轮换必须收到 sharing violation；服务仍以原库启动，备份后 Library 保留，pending 被消费且失败详情精确落在“轮换当前 control.db”，程序树封印与全部正常优雅停止继续成立。精确提交 `e0dbf61` 的两份干净未签名包通过；这只覆盖轮换前失败，**不代表候选落位后回滚、磁盘满、ACL/断电、正式签名或 RC Gate 已完成**。
- [EV-108](Documents/证据/验证记录.md)（2026-07-30）为规则执行路径嵌入 Go IANA 时区数据，使 `-trimpath` 独立 Windows 规则转换器在无 SDK zoneinfo 时仍可加载 `Asia/Shanghai`；同轮把真实 Source 按需确认改为单次运行共享墙钟，到界后经公共 API 取消并要求 30 秒内收敛。真实 legacy schema v3 的 10 个平台均可转换；Gank 在 3 分钟扫描边界内完成 index 并确认 12/12，Pawchive 缩小为 2 个目标后确认 2/2，两者完整 Source guard 均为零变化。Pawchive 12 目标运行则在取消后 30 秒仍停留 `running/cancelling`，因此该失败如实保留，**不得声称完整取消 Gate、全量扫描、完整规则语义、Reference Performance 或 RC 已通过**。
- [EV-109](Documents/证据/验证记录.md)（2026-07-30）修复 control 恢复替换的 fail-open 边界：旧实现忽略候选落位失败后的回滚错误，启动流程仍可能在缺失的 `control.db` 路径创建空库；现在 sidecar 清理、候选落位与旧库回滚均保留根因，只有确认旧库回到原路径时才消费 pending 并继续启动，连续性未知则保留轮换副本和 pending、记录失败并返回 `RESTORE_FAILED` 阻止 bootstrap。Windows 定向、WSL2 race 与根级检查通过；证据使用确定性文件操作 seam，**不代表真实 Windows 落位 sharing violation、磁盘满、ACL/断电、正式签名或 RC Gate 已完成**。
- [EV-110](Documents/证据/验证记录.md)（2026-07-30）把 EV-109 的安全回滚分支接入真实 Windows 便携制品：探针预先监视精确临时 AppDirs 的 `control.db.incoming`，以允许 SQLite 读写但拒绝删除/重命名的真实 handle 阻止候选落位；当前库已成功轮换，候选 Rename 收到 sharing violation，旧库随后回到原路径，服务继续启动并保留备份后 Library，pending 被消费且失败详情精确落在“落位恢复候选”。纠正候选名后的完整链 4/4、Windows watcher 20 次与 WSL2 race 通过；**不代表落位与旧库回滚双失败、磁盘满、ACL/断电、正式签名或 RC Gate 已完成**。
- [EV-119](Documents/证据/验证记录.md)（2026-07-30）在真实 Windows 文件系统上同时以不共享删除的 handle 持有恢复候选与已轮换旧库，使两次实际 `os.Rename` 分别收到 `ERROR_SHARING_VIOLATION`；生产落位逻辑进入连续性 fail-closed，启动失败处理返回 `RESTORE_FAILED`、保留 pending，并在 `restore-last` 中记录落位与回滚两个阶段。20 次定向、备份包 5 轮和 507.6 秒根级检查通过，提交 `b298d3a` 含原始 `gpgsig`。**该证据是包内真实 Win32 故障链，不是便携 `galleryd` 进程重启；磁盘满、ACL/低权限、中断、人工恢复、正式签名及 RC Gate 仍未完成**。
- [EV-121](Documents/证据/验证记录.md)（2026-07-30）扩展便携恢复链时发现新的 pre-placement fail-open：若当前 `control.db` 已不可用而恢复在候选生成/落位前失败，旧处理会消费 pending 并继续创建空库。现在任何普通恢复失败都必须先确认当前库仍是普通文件；否则保留 pending、完整记录原错误与当前库缺失原因，并以 `RESTORE_FAILED` 在 descriptor 前退出。精确提交 `3071558` 的两份干净未签名包用真实 Windows handle 持有 stale incoming 后验证 fail-closed，解除阻断并恢复已保全当前库后同一 pending 自动成功；最终链全部字段通过，探针提交 `2d67be5`。**这不等于便携进程内落位与回滚双 Rename 同时失败，也不覆盖磁盘满、ACL/断电、正式签名或 RC Gate**。
- [EV-122](Documents/证据/验证记录.md)（2026-07-30）关闭 control 恢复落位后到安全收尾之间的启动中断窗口：pending 现在先原子持久化为 `placed_pending_finalize`，重启只续接幂等 `FinalizeRestore`，不重复应用备份；只有安全收尾、原子 `restore-last` 成功记录和 pending 消费都完成后才报告成功。结果记录或标记消费失败会保留 pending 并返回 `RESTORE_FAILED`，当前库缺失且 marker 损坏也不再继续创建空库。精确实现提交 `5e55103` 的两份干净未签名包由探针提交 `38f5eb2` 构造持久中断阶段，真实 `galleryd` 保留备份后事实、吊销旧 Session 并完成收尾；**这是确定性重启续接证据，不是恰好在该指令窗口强杀/断电、磁盘满/ACL、便携双 Rename、正式签名或 RC Gate**。
- [EV-123](Documents/证据/验证记录.md)（2026-07-30）把 EV-122 的两个状态文件失败分支推进到真实便携进程：系统临时 AppDirs 中以非空目录占用 `restore-last.json`，以及用不共享删除的真实 Win32 handle 持有 `restore-pending.json`。两次未打测试 tag 的 `galleryd` 都必须在 descriptor 前返回 `RESTORE_FAILED`、保留 pending 与当前事实；解除阻断后同一请求成功收敛。pending 场景额外要求本地删除实际返回 `ERROR_SHARING_VIOLATION`。结果路径场景只是文件类型冲突，**不代表 ACL/磁盘满；两包仍为 EV-122 的同源未签名制品，也不替代实际强杀/断电、便携双 Rename、安装更新、正式签名或 RC Gate**。
- [EV-124](Documents/证据/验证记录.md)（2026-07-30）关闭“便携同进程双 Rename”证据缺口：探针先以共享删除的句柄允许真实 `control.db` 首次轮换，原路径消失后用 Win32 `ReOpenFile` 对同一已轮换文件建立不共享删除的句柄；另一真实句柄同时阻断 incoming。未打测试 tag 的 `galleryd` 因候选落位与旧库回滚两个实际 `ERROR_SHARING_VIOLATION` 在 descriptor 前 fail-closed，pending、候选及与失败前当前库 SHA-256 相同的轮换副本都保留；解除句柄后同一请求成功，轮换副本继续字节不变。**该证据不是磁盘满、ACL/低权限、强杀/断电、正式签名、安装更新或 RC Gate。**
- [EV-125](Documents/证据/验证记录.md)（2026-07-30）关闭恢复已落位、`placed_pending_finalize` 已原子持久化而 descriptor 尚未发布这一窗口的真实进程强杀切片：Windows 原生目录通知只观察 `restore-pending.json` 的 Rename，不打开目标文件；观察后取消启动 context，运行器只有在对仍存活的未打测试 tag `galleryd` 成功调用 OS Kill 时才登记 `ForcedKill`。强杀后 marker 精确保留、descriptor 不存在、轮换副本与强杀前当前库 SHA-256 相同；同 AppDirs 重启后只续接安全收尾，旧 Session 失效、备份后哨兵消失、成功结果与 pending 消费收敛。**该证据不等于电源中断、其它指令窗口、磁盘满、ACL/低权限、正式签名、安装更新或 RC Gate。**
- [EV-126](Documents/证据/验证记录.md)（2026-07-30）把默认关闭的生产 ToolDiscovery 接入 `galleryd`：只有同时显式声明绝对路径、精确 `-version` token 与可执行文件 SHA-256 的 `ffprobe`/`ffmpeg` 才可用，不搜索 PATH；启动期以受控参数数组、5 秒/64 KiB 探针核对版本和摘要，能力日志不含路径，每次 Resolve 前再核对摘要，未配置工具在 Job 创建前返回 `EXTERNAL_TOOL_UNAVAILABLE`。本机真实 `ffprobe` 通过隔离生产启动探针，Windows 全量 Go、Linux amd64 测试包交叉编译及 580.9 秒根级门禁通过。**该切片不新增 ffmpeg 转码/API，不覆盖恶意媒体语料库或 OS 级 CPU/内存硬限额，Security Gate 仍未通过。**
- [EV-127](Documents/证据/验证记录.md)（2026-07-30）为外部工具 Job 冻结整棵进程树的提交内存与累计用户态 CPU 时间预算；Windows ProcessController 在子进程仍挂起时配置 `JOB_OBJECT_LIMIT_JOB_MEMORY`、`JOB_OBJECT_LIMIT_JOB_TIME` 与 `KILL_ON_JOB_CLOSE` 后再恢复执行。默认 512 MiB、CPU 等于墙钟超时，最大 2 GiB/3,600 秒均为 PRE_FREEZE；Resolver 不能覆盖，旧 Job 执行时补安全默认值。真实父子进程树 CPU/聚合内存门禁和真实 `ffprobe` 复验通过；非 Windows 目前在 Job 创建前返回 `EXTERNAL_TOOL_UNAVAILABLE`，只完成 Linux amd64 交叉编译。**恶意媒体语料库、非 Windows 等价硬限制、物理 LAN/低端设备与整体 Security Gate 仍未完成。**
- [EV-128](Documents/证据/验证记录.md)（2026-07-30）建立默认关闭的 Windows 恶意媒体真实工具门禁：13 个纯合成、合计 332,653 bytes 的样本覆盖有效 PNG 对照、PNG 解压/尺寸炸弹、JPEG/GIF 极端尺寸、MP4 截断/深层 box、Matroska/RIFF/Ogg/FLAC/ID3 异常长度、ZIP 高压缩比附件及 HLS 外部引用；显式 pin 的 `ffprobe`/`ffmpeg` 经生产 ToolDiscovery、协议/格式白名单与 256 MiB/2 秒 CPU/5 秒墙钟/64 KiB 每流预算串行执行，最终 25 findings/0 failures、网络零连接且语料零变化。Windows 根级检查与 WSL2 定向 race 通过。**该切片不是 coverage-guided fuzz、第三方 CVE 全集、真实媒体或非 Windows 门禁，不新增外部转换业务，整体 Security Gate 仍未通过。**
- [EV-129](Documents/证据/验证记录.md)（2026-07-30）把双入口从存在生产审计例外的 `react-router-dom@7.18.1` 迁移到官方 v8 单包 `react-router@8.3.0`，全部 20 处 Declarative SPA 导入改走 `react-router`，Node 最低基线同步为 22.22.0；审计策略删除 RSC 公告的 production 例外并继续禁止 `react-router-dom`、RSC/SSR 依赖与服务端入口。当前 production-only `npm audit` 为 0，full 报告只剩 OpenAPI 生成器链的 4 个 high/1 条 dev-only 限时例外。精确提交 `8cea6bd` 的根级检查、Chromium/Firefox mock smoke 26/26，以及真实 `galleryd` 完整链各 23/23 均通过，Source guard 零变化且正式 8081 未触碰。**该切片关闭生产路由依赖审计例外和最终生产资产未全量复跑缺口，不代表 dev-only 例外、真实设备或完整 Web/Security Gate 已完成。**
- [EV-130](Documents/证据/验证记录.md)（2026-07-30）从精确干净提交 `ffdf75d` 构建当前双前端 Windows x64 便携测试包 `0.3.0-ev130`：12,586,256-byte ZIP 的清单为 `dirty=false`、`unsigned`，包外 SHA-256 为 `0FE458F57D7DAE143206C2DD977181ADA348E5E402E3D7A5587DA7DEBF54C227`；官方 `Test-WindowsPortable.ps1` 已验证版本/提交、包内外摘要、三份 SBOM、同源内嵌 Web 及同 AppDirs 强杀重启。**该制品只用于当前功能实测，尚无 Authenticode/时间戳、安装更新、真实用户数据或完整 Windows 发行门禁，不得称为 RC。**
- [EV-131](Documents/证据/验证记录.md)（2026-07-30）把真实当前用户 NTFS ACL 拒绝接入 Windows 双便携恢复门禁：同时对 `control.db` 拒绝 `DELETE`、对父目录拒绝 `FILE_DELETE_CHILD`，仍允许数据库读写；真实 `galleryd` 在轮换当前库时收到 OS `ERROR_ACCESS_DENIED`，保留当前事实、记录失败并消费 pending，恢复原 DACL 后后续恢复链继续通过。精确干净提交 `457bef6` 的 `0.3.1-ev131` ZIP 为 12,586,266 bytes、`dirty=false`、`unsigned`，SHA-256 为 `814E16C250F3508F3405D39C820906CA9826B2BDF13751AAB63D14015BF5C94B`；独立制品 smoke、EV-130→EV-131 全恢复链、Windows 单元测试、Linux amd64 交叉编译和根级检查均通过。**该切片只证明当前工作站、本地 NTFS、当前用户显式拒绝，不代表低完整性令牌、其它账户/服务、企业继承 ACL、ReFS/SMB、磁盘满、签名/安装更新或 RC Gate。**
- [EV-132](Documents/证据/验证记录.md)（2026-07-30）清除最后一条 OpenAPI 生成器 dev-only 审计例外：仓库内私有 build-only `minimatch` 兼容层保持 Redocly 1.x 需要的可调用 CommonJS/v5 表面，实际委托精确锁定的 `minimatch@10.2.5` 与已修复 `brace-expansion@5.0.8`；依赖门禁同时锁定 package/lock 解析、Redocly 实际解析路径、8 个行为用例及 894 个内部 OpenAPI 引用。精确实现提交 `a3420aa` 上 `npm ci`、full/production `npm audit` 0 漏洞/0 例外、12 项定向测试、15 文件 212 项 Web 测试、字节一致 API 生成、生产资产同哈希构建和 684.6 秒根级检查通过。**该兼容层只存在于构建工具链，应在上游提供安全兼容版本时复审移除；运行时前端未变化，真实设备、真实存储及完整 Web/Security/RC Gate 状态不变。**
- [EV-133](Documents/证据/验证记录.md)（2026-07-30）把 Windows 历史升级门禁从单一 schema 23→24 扩展为清单驱动的连续 schema 20/21/22/23→24：四个真实祖先提交分别创建 Library、control 备份及只显示一次密文的 API Token；当前真实 `galleryd` 迁移后必须保留事实、Token 元数据与实际 Bearer 鉴权，旧程序对 schema 24 fail-closed 且数据库字节封印不变，当前程序随后可复启。精确干净提交 `b1b5ea8` 的 `0.3.2-ev133` Windows x64 ZIP 为 12,586,590 bytes、`dirty=false`、`unsigned`，SHA-256 为 `48315E220B8C3A47826BD359B04C05ABAA3FBBB5360FDB09E7BADADD4A534DBC`；manifest v2 声明 current=24、minimum=20、verified=20～23，独立 smoke、EV-131→EV-133 完整恢复链及最终四基线矩阵均通过。**这是 Windows v1 当前已验证的升级下限，不证明 schema 20 以前开发快照、正式 Authenticode/时间戳、安装更新、磁盘满、其它身份/文件系统或完整 RC Gate。**
- [EV-134](Documents/证据/验证记录.md)（2026-07-30）把既有 `FileIdentityProvider` 接入生产 Scanner、持久 Hash Job、SourceMedia observation 与目标化确认：Windows 从调用方已经打开的同一只读句柄读取卷序列号 + 128-bit FileID，受支持 Unix 用 `fstat` 取得 `dev+inode`，均写成不含路径的 versioned opaque 值；双方都有完整身份时不相等即文件级重新完整哈希，不可用时显式回退，FileID 永不代替 ContentBlob digest。同路径、同大小、同 mtime 的真实 NTFS 替代文件、真实 `galleryd` 停启持久化、WSL2 DrvFS 三包 race 与 844.5 秒根级检查通过。**该切片不证明 Linux 原生、SMB/NAS/UNC、重挂载/跨卷稳定性，不冻结 FileLocation 最终唯一约束或平台 Gate。**
- [EV-135](Documents/证据/验证记录.md)（2026-07-30）为 `catalog_gc`、`catalog_checkpoint`、`catalog_vacuum` 与 `derived_gc` 持久化 `preflight` 0/2、`executing` 1/2、`finalizing` 2/2 的估算阶段进度，每次推进都失效 HTTP Job snapshot，并在执行时重新做服务端空间预检；取消与 shutdown 继续收敛原有终态。Windows 单元/受影响包、WSL2 两包 race、Chromium/Firefox 各 23 个真实后端测试及 1004.1 秒根级检查通过，管理端实际 Job 行显示 completed 与 `2 / 2（估算）`。**该切片不证明真实慢盘上的中间阶段可观察性、实际字节/页计数、VACUUM 内部取消响应、磁盘满或完整 Degradation Gate。**
- [EV-136](Documents/证据/验证记录.md)（2026-07-30）把管理端任务历史从“每次续页都永久追加到同一 DOM 表”收敛为每页最多 50 条的当前页窗口，提供较新/更早前后导航、续页原地重试和已访问页缓存复用；状态筛选重置到第一页，授权后 keyset、HTTP snapshot 事实源与 WebSocket 失效语义不变。44 项管理端组件测试、Chromium/Firefox 定向真实后端分页、双浏览器 26/26 mock smoke 及 1234.5 秒根级检查通过。**该切片只限定 Job 表的单页渲染规模，不代表其余管理列表、配置数组、100k UI 性能、真实设备或完整 Web Gate。**
- [EV-111](Documents/证据/验证记录.md)（2026-07-30）在 Scanner 既有多 `verificationTargets` 语义上增加兼容批量 API：1～200 个同 current publication、同 Source 的唯一未确认媒体在创建前整批校验并规范化幂等，只建立一个 Scan Job；跨 Source、重复、已确认和历史快照整批拒绝，原单媒体入口与 Web 交互不变。真实 Pawchive 最终轮 index 91,083 ms、一个 Job 确认 12/12 用时 74,003 ms，完整 Source guard 零变化；Windows 定向/5 次重复、WSL2 race 和 605.9 秒根级检查通过，提交 `3b3357f2666e823ac4b7f6d248aee0d7b286143d` 含 `gpgsig`。**不代表活动 Hash 取消、HDD/SMB/NAS、全量扫描、完整规则语义、正式性能/API Freeze 或 RC Gate 已完成**。
- [EV-112](Documents/证据/验证记录.md)（2026-07-30）为 `sourcelab` 增加默认关闭的真实活动 Hash 取消门禁：只在公共 Job API 观察到同 Source、本轮 `hash/running` 后取消父 Scan，并要求父子 30 秒内都为 `cancelled`；只在 discovery 超时会安全清理后失败，不得冒充证据。全新 Pawchive 隔离运行 13 findings/0 failures，index 91,798 ms，确认阶段 66,889 ms 观察活动 Hash，取消后 4 ms 观察父子收敛，全树 11,595 文件/2,353 目录/124,660,469,885 bytes 增删改为 0；Windows 5 次定向、WSL2 race 与 478.3 秒根级检查通过，提交 `53ecbcf` 含 `gpgsig`。**只关闭 Windows 本地 SSD/Pawchive 这一条活动 Hash 取消切片，不代表 HDD/SMB/NAS、publishing 临界点、真实存储崩溃恢复、全量或 RC Gate**。
- [EV-50](Documents/证据/验证记录.md)（2026-07-27）修复同一 Creator 横跨多个 Source 时全局 Creator 封面被错误扩散到各 Source 的资源边界缺陷，并让 Creator/Source 复用一次物化候选连接；Source 现在严格只引用自身 Work/Media。该轮只有合成行为与查询计划证据，没有 500,000 正式性能结果；当时遗留的 Creator/Library 逐主体授权裁剪已由 EV-86 关闭正确性缺口，受限重选的代表性与高基数测量随后由 EV-87 补齐，但完整 Reference/Degradation 矩阵仍未完成。
- [EV-51](Documents/证据/验证记录.md)（2026-07-27）增加 catalog v18 `work_search_candidates`：FTS5 以显式同 rowid 命中窄候选，分页前完成授权、过滤、Ranking、排序、keyset 与 Total，只有 `limit+1` 行回读宽 WorkProjection。Stage/clone/Overlay/Creator merge/自然排序回填/GC 和发布事务均已纳入三方一致性与 fail-closed 门禁；500,000 WorkProjection 当前工作站候选基准通过执行，但不等于正式 Reference Performance/API Freeze，Ranking/Total/租约数值仍为 PRE_FREEZE。
- [EV-52](Documents/证据/验证记录.md)（2026-07-27）增加 catalog v19 `candidate_validation_seals` 与 Overlay candidate 持久创建基线：完整 projection/FTS/成员/封面验证与聚合封面计算在候选验证 IMMEDIATE 事务中完成并写入封印；封印是候选完成标记，所有 Store 内容写入在同一事务核对 staging 状态、候选身份与“未封印”，验证后和发布后的旧 Candidate 一律拒绝原地改写；Overlay Apply/Validate 核对持久 base 身份而不把并发 active 漂移误判为损坏，Catalog/Overlay Publish 才确认封印、候选状态和 active CAS 后切换指针。当前工作站最终版 500,000 窄候选 fixture 的指针切换为 0.539 ms，100,000/10 Source 生产候选预检十次 Publish 合计 7 ms，但后者完整构建约 8 分 59 秒且两项测量都不是正式 500,000 变化 publication 多样本矩阵，Gate 状态不变。
- [EV-53](Documents/证据/验证记录.md)（2026-07-27）补齐 Job 取消线性化与 Scan→Hash 级联：取消/失败/维护完成/普通完成/publication 恢复的 Job 与 active Attempt 同事务收敛，retry-backoff 可取消且不重写失败 Attempt；取消先于 `BeginPublishing` 时禁止发布，publishing 后迟到取消冲突，已提交 Catalog publication 始终恢复 completed；Scan 显式取消传播至活动/退避 Hash，Hash 在读取前、长读取期间和摘要提交前复核父状态，纯 galleryd shutdown 保存 retryable `PROCESS_INTERRUPTED`。当前证据只含合成 Source、Windows 高重复与 WSL2 DrvFS race，不代表真实 HDD/SMB/NAS 取消响应 Gate。
- [EV-54](Documents/证据/验证记录.md)（2026-07-27）建立首条真实后端管理自举门禁：管理端可创建首个 Library、登记服务端绝对路径的只读 Source 并发起首次 `index` 扫描；隔离运行器以系统临时 AppDirs、动态 loopback 端口和复制出的合成 Source 驱动真实 Chromium/`galleryd`，RuleVersion/Binding 仍经 API 建立，最终核对 Job completed/current publication、`eventType` 解码后的 sequence 推进与任务快照重取，以及 Source 文件/目录零写入。GitHub Actions workflow 已纳入该门禁，失败诊断只保存不含 Session 网络 trace 的明确白名单；这不是完整规则 UI、真实媒体或完整 Web Gate。
- [EV-55](Documents/证据/验证记录.md)（2026-07-27）把画廊列表、作品详情、封面、媒体列表、按需确认、有效图片正文、Viewer、Range/ETag/If-Range 和失效恢复绑定到同一 `queryPublicationId`，修正 Work 标题误读 live Overlay、缩略图跨 publication 缓存复用与可选 publication 参数 fail-open/错误状态等缺陷；隔离 Chromium/真实 `galleryd` 以临时生成 2×2 PNG 证明 P1 未确认→确认生成 P2→旧 P1 保持不可读→P2 可解码/Range/恢复。该证据仍是合成 Source/Personal/Chromium，不是完整 Web Gate。
- [EV-56](Documents/证据/验证记录.md)（2026-07-27）把 2×2 规则封面与 3×2 自定义封面组成同一隔离双 PNG Source，真实 Chromium 依次确认第二项、从 UI 设置 CustomCover、证明旧快照冻结/新快照 3×2 解码，再从 UI 清除并证明回退 2×2 规则封面。同轮补齐 Overlay 投影轮询/实时失效、草稿保护与跨 Work 隔离，并关闭 publication-first 恢复及并发 watermark 丢失窗口。证据仍仅为合成 Source/Personal/Chromium，Web Gate 状态不变。
- [EV-57](Documents/证据/验证记录.md)（2026-07-27）以同一隔离真实后端门禁关闭规则生命周期的首条浏览器业务链：从 UI 创建 RulePackage、以 `If-Match: "0"` 保存首个草稿、吸收校验 revision、用 `before=null` 评估首次影响、发布 immutable RuleVersion、选择该版本建立 Source Binding，再发起扫描并核对 Job 冻结的 `ruleSemanticHash`、publication 与 `/browse`。同轮收紧 required If-Match、草稿/发布事务、弃用版本复活、Binding oneOf 与扫描前版本防御，并保护请求期间的前端脏编辑；证据仍仅为 Personal/Chromium/合成 Source。
- [EV-58](Documents/证据/验证记录.md)（2026-07-27）建立规则内容的精确文本契约与首个模板驱动 Schema 表单：Save/Import/Impact 可用文本绕开 JavaScript number 精度边界，JSON/YAML/TOML 成功导入收敛而失败保留原文，Publish 前预编译 `parameter_schema`；Web 使用构建期预编译 Ajv2020 validator，不放宽 CSP，RJSF 与文本模式共享同一草稿/CAS/dirty 状态并保护原子 JSON 的精确数字。真实 Chromium 从空草稿选择模板、表单编辑、切换 JSON，再沿 EV-57 完成保存、校验、Impact、发布、Binding 和扫描；证据仍仅为 Personal/Chromium/合成 Source，不代表任意规则的完整可视化构建。
- [EV-59](Documents/证据/验证记录.md)（2026-07-28）收口规则 rollback、Package/Version/ParameterSet deprecate、共享参数精确文本/Impact/CAS、Binding 原子刷新与 Job 快照不变量；control v22 为 RuleAudit 增加准确主体。隔离 Chromium 以独立最后阶段证明参数更新不自动创建 Job/publication、显式 J2 冻结新 hash/IR、rollback 只改 current、弃用保留既有执行事实；证据仍仅为 Personal/Chromium/合成 Source。
- [EV-60](Documents/证据/验证记录.md)（2026-07-28）把 Personal Session/API Token/Share、一次性密文收口、真实 WebSocket 断开后的 HTTP snapshot 恢复，以及独立 LAN loopback Owner/Viewer/Grant/停用恢复/Session 吊销接入隔离 Chromium/真实 `galleryd`；同轮修复 bootstrap/login 把角色 capability 上限误报为 global effective capability 的授权缺陷。该 LAN 证据仍是同机动态 loopback，不是物理第二设备或真实网络门禁；全程仅用临时 AppDirs 与合成 Source。
- [EV-61](Documents/证据/验证记录.md)（2026-07-28）把 control 备份、manifest 写后刷新、恢复 dry-run、待重启恢复登记和 Catalog GC dry-run 接入隔离 Chromium/真实 `galleryd`；同轮修复 `job.completed`/`job.failed` 只失效任务列表、导致异步发布的备份 manifest 在已打开诊断页长期不可见的问题。恢复请求只登记到临时 AppDirs，运行器不会再用该 AppDirs 启动服务，因此本证据不包含真正的重启恢复或失败回滚演练。
- [EV-62](Documents/证据/验证记录.md)（2026-07-28）为 SourceRuleBinding 增加 active/paused/invalid 可见状态操作，为任务列表增加 retry-backoff 取消与 `nextAttemptAt`，并去掉 cancelled Job 的错误重试入口；隔离 Chromium/真实 `galleryd` 从 UI 完成 Binding 暂停/恢复、作品人工解绑/撤销、退避 Job 持久取消及同 Job 第 2 个 Attempt 重试完成。Job 夹具在服务启动取得单写锁前经正式 Storage/Job Store 状态机建立，不直接写表，也不接触 Source。
- [EV-64](Documents/证据/验证记录.md)（2026-07-28）把 EV-61 的待重启 control 恢复登记扩展为同一隔离 AppDirs 的实际进程重启：备份后经可见 UI 创建 Library 标记，登记恢复并优雅停止首个 Personal `galleryd`，第二个进程启动期应用恢复；新 Session 从 UI 证明原 Library 保留、备份后标记消失且安全审计出现 `restore.finalize/success/control:control.db`。该证据不包含旧浏览器 Session 失效、损坏备份/磁盘满回滚或长任务进程中断恢复。
- [EV-65](Documents/证据/验证记录.md)（2026-07-28）把 mock smoke 与完整隔离真实后端链扩展到 Playwright Desktop Firefox：Personal 11/11、LAN 1/1、同 AppDirs 恢复重启和 Source guard 与 Chromium 等价通过；Firefox 的合法 `If-None-Match` 重验证会让 Viewer reload 收到 304，媒体 E2E 现显式验证强 ETag、304 回显与实际图片解码。CI 顺序执行 Chromium/Firefox，浏览器项目只允许白名单值；该轮证据使用当时的 14 线程/50% 上限，EV-72 后本机重型门禁改用本文件「测试与验证」的约 25% 默认预算。
- [EV-66](Documents/证据/验证记录.md)（2026-07-28）修复显式/同 ID Retry/按需确认 Scan Job 未登记到 `source_scan_states.current_job_id` 导致 Watcher 首次 30 秒收敛重复扫描、消费 `manual_unbound` 或切换 current publication 的竞态；扫描开始后到达的 Watcher 事件不再被完成态无条件清除，恢复中的未登记活动 Scan 会被接管。维护服务同时为 queued/running/completed/failed/cancelled 持久状态发出 `JobChanged`，使任务表按 HTTP 快照重取终态。Chromium/Firefox 完整隔离链、根级检查和 WSL2 DrvFS race 在 14 线程/50% 资源预算内通过。
- 尚未完成：阶段 5 真实物理 LAN 多设备、目标低端设备 Argon2id 及非 Windows 外部工具进程树资源门禁；阶段 6 真实移动设备/触控、人工屏幕阅读器、真实浏览器 200%/400% 缩放、物理操作系统高对比与交互状态组合的完整可访问性、任意规则的完整可视化构建、真实存储上的取消与崩溃恢复响应及完整弱网抖动矩阵；以及 Pixiv/真实 HDD 全量扫描与哈希、SMB/NAS/UNC、Linux 原生与重挂载/跨卷文件身份稳定性、真实慢盘维护中间进度/VACUUM 取消/磁盘满、正式 500,000 publication 多样本与 Reference/Degradation Performance Gate、ranking/total/租约及外部工具资源预算等 PRE_FREEZE 数值、AND/OR canonical 化，以及正式 Authenticode、安装更新、更多升级跨度、恢复磁盘满、低完整性/多账户/继承 ACL、其它指令窗口强杀与真实断电及平台发行。
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

阶段 6 当前 Web 技术组合已按 ADR-009 接受为 React 19、TypeScript strict、Vite、OpenAPI 生成类型、TanStack Query、React Aria 与 RJSF/AJV；视觉组件样式、最终细粒度路由、SQLite 驱动和物理表结构仍未冻结。改变已接受的 Web 交付架构须同步 ADR，不要仅凭原型依赖替正式实现做决定。

### 平台适配边界（开发约束）

- 新增任何平台相关能力必须通过 `internal/platform/*` 与 `internal/ports`，不得把 OS 专有实现写进领域或应用层。AppDirs 进程独占锁已按此实现（`internal/platform/lock`，Windows LockFileEx / Unix flock 分文件构建）。
- FileID、句柄式打开与 ToolDiscovery 已按此边界接入；后续原生 Watcher、CredentialStore 及其它平台能力同样不得直接进入领域层，必须经平台 adapter。
- `internal/scanner`、`internal/media`、`internal/derived` 中现存对 `os`/`filepath` 的直接依赖是技术债，只在本轮实际触及的代码中做局部迁移，不为形式统一制造大量空接口或一次性大重构。

### 当前限定的暂定行为

- **Overlay 查询影响由字段能力与本次查询动态决定，不是字段永久分类**：TitleOverride/ManualTag/Hidden/CustomCover/Favorite/ReadingProgress 的查询能力都经 publication 写后屏障；Favorite/ReadingProgress 额外提供 live 展示。`PublishedWork.coverMediaId` 是 publication 冻结的有效封面，因此普通作品查询也固定把 CustomCover 以 `{overlay.customCoverMediaId, resource}` 放入 dependency set；同 Work 有效 CustomCover 优先，失效事实保留并回退规则封面。
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
- 建立新 ContentBlob 必须以首次完整 SHA-256 为前置条件，不能降低身份强度；但自阶段 3 起，媒体「已定位」与「内容已确认」解耦为 `located_unverified`/`content_verified` 两态，未确认媒体可以进入 publication 与列表（`blob=null`），正文读取返回专用 `CONTENT_NOT_VERIFIED`。因此超大文件或网络盘不再阻塞整个 Source 的发布，而是保持未确认状态直到 `incremental`/`verify` 档案或按需确认完成哈希。
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
4. 按领域/规则/扫描/查询与媒体/安全/Web/PWA/平台发行的顺序扩展。**（阶段 1～4 已完成代码与合成 Correctness；阶段 5 代码基线完成但 Security Gate 未通过；阶段 6 Web/PWA 正式代码基线已实现但 Web Gate 未通过；阶段 7 已建立未签名 Windows 便携测试包、同源切换/恢复、损坏备份、真实 schema 20/21/22/23→24 连续升级/凭据承接/反向拒绝、首次轮换失败保留当前库、候选落位后安全回滚、包内及便携真实进程的 Win32 落位/回滚双失败、当前库缺失时的 fail-closed/解除阻断重试、落位后安全收尾的可恢复阶段续接、结果记录/标记删除失败，以及 finalize 持久阶段真实强杀后的同 AppDirs 续接门禁。下一步并行完成阶段 4 性能/API Freeze、阶段 5 外部设备安全门禁、阶段 6 完整浏览器/可访问性门禁，以及阶段 7 正式签名、schema 20 以前策略、磁盘满/权限、其它强杀窗口与真实断电，不进入桌面壳或把便携测试包冒充 RC）**

阶段 1 已完成。阶段 1 Schema Freeze Gate 冻结的是**核心领域身份与唯一约束**（不是最终物理数据库唯一约束）：`(source_id, source_key) WHERE status='active'`、`(work_id, ordinal)`、CanonicalWork 持久 ID 身份、Work/Creator/Media Binding 的 active/inactive/manual_unbound/orphan_candidate/orphaned 生命周期、同 Blob 多 occurrence、SourceWork 拆分/合并检测与结构决策 fingerprint 唯一、多 Source 隔离、Binding issue 指纹去重，登记于 control 迁移 `00016_schema_freeze_phase1` 的 `schema_freeze` 表（FROZEN）。SourceWork 决策的撤回仅适用于尚未被扫描消费的 pre-seed Binding；消费后返回结构化 `CONFLICT`，不执行已生效结构变化的完整反向操作。阶段 2 的 RulePackage canonical JSON 所有权、已发布版本不可变、草稿 revision CAS 和 Job 规则执行快照登记于 `00017_rules_lifecycle` 的 `schema_freeze` 表；Rule extension 注册表、单生效 Binding、参数最终命名空间、Impact 调度联动和完整表单 UI 保持 compatibility baseline。阶段 3 已增加并修正持久 Hash Job、同一 Job 多 Attempt、周期租约回收和退避重试、六类独立非阻塞资源池、动态 Watcher 与低频周期收敛、staging/publication、所有权 Temp GC、GC/VACUUM 服务端空间预检和外部执行边界，但真实 HDD、SMB/NAS、网络挂载与正式 Reference/Degradation Performance Gate 仍待下一轮实测。阶段 5 安全结构方向登记于 control v20 的 `schema_freeze` 表；Argon2id、Session 与限流数值仍 PRE_FREEZE。阶段 6 的 Web 交付架构由 ADR-009 接受，但浏览器/可访问性 Gate 未冻结为发布支持。仍保持 pre-freeze/compatibility-baseline/deferred 的其它旧项不因此重开已完成阶段。修改标记 FROZEN 的约束前须新增或修订 ADR；不得因阶段 6 代码基线存在而跳过阶段 5 未完成门禁或提前进入桌面壳/发行。

EV-42 在上述 compatibility baseline 内增加 catalog v11 显式封面列：规则 `CoverPath` 映射同一 SourceWork 的稳定 SourceMedia，经 Binding 解析 CanonicalMedia；媒体 `ordinal` 仅保留非负内容顺序。`PublishedWork.coverMediaId` 为 required nullable，`GET /api/v1/works/{workId}` 可用 `queryPublicationId` 绑定 publication 并建立显式快照读取租约；这些是当前实现，不代表 API Freeze。

EV-44 在此后增加 catalog v12 revision Source/Library 成员表及 Source/Library browse 索引，用于 Work 聚合查询逐成员授权与发布完整性复核；该迁移仍属兼容演进实现，不代表物理 Schema、API 或性能数值已经 Freeze。

EV-46 继续在同一 compatibility baseline 内演进到 catalog v14：v13 把发布时刻的 mtime 固化进 `media_projections.mtime_ns`，使已确认媒体的正文可以在发送任何字节之前判定身份是否仍然成立（ADR-010）；v14 用**独立列**承载规则派生的展示事实——`media_projections.rule_hidden` 与 `work_projections.badges_json`。规则隐藏与用户 Overlay 的 `hidden` 必须保持两列，合并会让重扫抹掉不可重建的用户事实。规则原语注册表同时递增为 `gallery-primitives-v2`（新增 `media_hidden`、`cover_disable_marker`、`badge`，`cover_candidate` 支持 `priority`/`media_type`）。这些同样是当前实现，不代表 Schema 或 API Freeze。

EV-51/EV-52/EV-87 在 compatibility baseline 内演进到 catalog v20：v15 增加 Work 标量展示事实，v16 增加 Creator/Source/Library 聚合封面，v17 登记自然排序编码 v2，v18 增加与 FTS5 docid 一一对应的 `work_search_candidates` 窄候选投影，v19 增加候选验证封印与 Overlay candidate 持久创建基线，v20 增加 publication 绑定的 Creator/Source 聚合封面窄候选与全局胜出 Source。任何写入/克隆/重投影/Creator merge/自然排序回填和 GC 路径都必须同时维护 WorkProjection、搜索候选、FTS 与聚合候选；完整 Validate 须复核数量、rowid、业务键、候选事实、FTS 文本、成员和封面并在同一 IMMEDIATE 事务内重建聚合候选/全局聚合后写入封印。封印是候选完成标记：所有 Store 内容写入必须在同一事务确认匹配 Job/Source/watermark（Overlay 另以 revision 行中的 `base_overlay_revision_id` 核对创建基线）、目标仍为 staging 且没有封印；Apply/Validate 不得把后续 active publication 漂移误判为候选损坏，同一 Store 的候选生命周期由互斥边界禁止 Stage/Validate/Publish 交错，验证后与发布后的旧 Candidate 一律拒绝原地改写。GC 必须保护仍被 staging Overlay candidate 持久引用的 base publication/revision，直到候选进入 terminal 状态才可回收。Publish 只允许确认匹配封印、候选状态与 active CAS 后切换 publication；调用方替换 persisted base 必须 fail-closed，合法旧基线候选则在 Publish 得到可重试 `CONFLICT`。绕过 Store/AppDirs 进程锁直接写 catalog.db 不属于支持的产品写入边界；显式重验会先核对候选身份再撤销旧封印，失败后不得恢复。该迁移与当前性能结果不代表物理 Schema、API 或数值 Freeze。

数据库事务基线：`control.db` 与 `catalog.db` 的 DSN 使用 `_txlock=immediate`。默认 DEFERRED 事务在 WAL 下的读后写形态会遇到 `SQLITE_BUSY_SNAPSHOT`，而 `busy_timeout` 对该情况不生效；新增读后写路径不得改回延迟事务，详见 EV-46 的 `TX-1`。

Walking Skeleton 功能可以少，但基础模型不能是临时替代品：

- 使用正式 `control.db`/`catalog.db` 迁移框架；
- 使用稳定 Canonical/Source ID、SourceRuleBinding、完整 Blob 身份和 `query_publication_id`；
- 使用正式 OpenAPI DTO、错误信封、Session 和 capability；
- 禁止内存替代数据库、路径主键、临时 DTO、匿名管理员或客户端直连数据层。

具体类名、函数名和表结构应留给实现，但不得推翻已选中的身份、所有权、授权和 publication 契约。

## 测试与验证

- 正式实现至少分为单元、数据、契约、集成、UI 和平台测试；发布前另做强杀、磁盘满、GC/VACUUM、真实只读样本和目标平台验证。
- 本机交互式验证默认把重型门禁进一步控制在整机约 25% 以内：重型门禁串行执行，当前 28 逻辑处理器工作站的 Windows Go 使用 `GOMAXPROCS=4`、`GOFLAGS=-p=2` 与 BelowNormal 优先级；WSL race 只允许 4 个 CPU、`-p=1`，并使用低 CPU/IO 优先级；Playwright 保持单 worker。不得同时运行构建、race、全量扫描等高内存或高 I/O 任务；只有用户明确授权时才提高预算。Vitest 现有根级脚本仍为 `maxWorkers=50%`，本地执行时必须通过串行调度和降低宿主进程优先级控制峰值，后续应单独把脚本并发参数化。
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

- 仓库已有正式构建与检查入口：根级 `Check.ps1`（委托 `scripts/Check.ps1`）先断言 tracked 文件的 index 与工作树换行均为 LF（规则见「仓库文本换行与跨平台格式一致性」），再执行 `go mod tidy -diff`、OpenAPI Go 生成一致性（`go generate ./...`）、Web 的 `npm ci`、OpenAPI TypeScript/图标生成一致性、TypeScript、ESLint、Prettier、Vitest 和生产构建，再执行 gofmt、`go vet ./...`、`CGO_ENABLED=0 go test ./...` 与 `go build ./cmd/...`；`Check.ps1 -Race` 追加 `go test -race ./...`。Web 独立浏览器 smoke 使用 `cd web && npm run build && npm run test:smoke`，真实后端 E2E 只允许临时 AppDirs/隔离端口。也可直接运行 Go 门禁与 `govulncheck ./...`。Windows 本机 race 有 `WaitOnAddress` 限制，race 门禁在 Linux/WSL 执行。`go` 不在 PATH 时用 `GALLERY_GO` 指定固定工具链。不要另建重复脚本。
- ffmpeg/ffprobe 等外部工具必须经 ToolDiscovery、版本允许列表、固定参数数组、协议/格式允许列表、探测边界、超时和资源限制调用，不能拼接 shell 命令，也不能让不可信容器打开网络或未允许的外部引用。
- 程序资源与用户 AppDirs 分离；覆盖升级不得删除用户数据。数据升级前优先备份 control，Catalog 不兼容时可重建。
- 发行前完成 OpenAPI/WS/规则/数据版本、许可证、SBOM、依赖安全、签名和升级/降级说明。
- Windows、Linux、macOS、Docker 和网络盘能力分别验收，不从 Go 可交叉编译目标自动生成支持矩阵。

### 仓库文本换行与跨平台格式一致性

- `.gitattributes` 是 tracked files 换行的唯一权威事实源，当前规则是 `* text=auto eol=lf` 加少量二进制 `binary` 声明。任何平台、任何 `core.autocrlf`/`core.eol` 下的检出结果都必须一致。
- 提交信息（commit message）的行分隔符要求见「排版：行分隔符」，与本节是两套彼此独立的规则：前者约束 commit object 的原始字节，后者约束 tracked 文件的 blob 与工作树内容。二者不得互相引用为保障，也不得合并表述。
- 不得依赖用户或 runner 的全局 `core.autocrlf`/`core.eol` 来达成换行一致，不得为解决换行问题建议或修改开发者的全局 Git 配置，也不得只修正当前 runner 而不落到仓库事实源。
- 引入新语言、新前端资产或新文件类型时不需要逐个补充扩展名；确需例外（例如必须保留 CRLF 的脚本或新的二进制类型）时，只为仓库中实际存在的类型添加规则，不得套用覆盖大量无关扩展名的通用模板。
- 修改 `.gitattributes` 后必须执行 `git add --renormalize .` 并逐项审查 `git status --short`、`git diff --cached --numstat` 与 `git diff --check`：确认只有预期文本变化、二进制文件未被改动、生成产物未发生无关变化。若产生无法解释的大范围变化，撤回暂存结果并缩小规则，不得直接提交。索引本来就正确时不得为形式制造全仓库 renormalize 提交。
- Windows 与 Ubuntu 必须运行同一条格式检查命令并得到同一结论。跨平台格式失败只能通过修正仓库事实源解决，禁止把 Prettier `endOfLine` 改为 `auto`、删除或跳过某个平台的 Job、对失败命令加 `continue-on-error`、忽略退出码、在 CI 中先 `prettier --write` 再检查，或把普通源码批量写入 `.prettierignore`。

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
- 上一条只约束本轮任务的边界，不是让已知失败长期存在的许可。若某个失败签名在 `main` 上已经不是第一次出现，Agent 必须在本轮结束时把它记入 `PROJECT_STATUS.md` 的已知缺口或对应门禁文档，作为下一轮的正式修复任务；不得只在报告里描述为“既有问题”“与本轮无关”而不留下任何可追踪的修复入口。判断是否重复出现要读实际 run 历史，不得依据模型记忆。
- 修复重复出现的 CI 失败时，必须先用干净检出复现并确认根因，再落到仓库事实源；只让 CI 表面变绿的处理（跳过步骤、忽略退出码、排除失败文件、放宽检查口径）一律不接受。

## Git Commit Message 规范

本节是仓库内唯一、强制、自包含的提交信息规范，无需访问仓库外文件即可执行。系统与安全约束、用户本轮明确边界、`Git 与交付` 的授权和推送限制优先于本节；在不冲突时，本节对提交粒度、消息结构、文字排版、签名、验证和历史重写具有唯一解释权。

本节的全部要求同时作用于新提交和任何历史重写结果。任何例外都必须先修改本节，不得从旧提交、外部参考或错误实践反推规则。发现本节内部出现重复、冲突或绕行表述时，必须整体重写受影响区域，不得追加补充段落。

本节约束的对象是提交信息文本本身。仓库内文档（含本节的反例代码块）为了说明规则会故意写出被禁止的形式，这些内容不受本节排版约束，机器检查也必须只读取提交信息，不得扫描仓库文件内容。

### 提交粒度

一个提交代表一个能够独立解释、审查、验证和撤销的逻辑切片。提交边界由语义和依赖决定，不由文件数量或文件类型决定。

提交粒度必须同时满足下列目标，任何一条不成立都要重新评估边界：

- 语义原子：只表达一个主要逻辑结果；
- 可独立审查：审查者不必理解多个互不依赖的子系统；
- 可合理回退：单独撤销后仓库仍然逻辑一致；
- 直接测试与实现同行；
- 契约源与生成物同行；
- 不制造明显不可构建的中间状态；
- 不因文件类型机械拆分；
- 不因处于同一阶段机械合并。

同一逻辑切片通常同时包含生产代码、直接测试、必要 migration、OpenAPI 或 Schema、对应生成物，以及必要且直接相关的文档。

#### 必须合并

以下内容通常必须并入其所属提交：

- 功能与其直接测试；
- 错误修复与复现该错误、且会在旧实现下失败的回归测试；
- migration 与唯一依赖该 migration 的模型和测试；
- OpenAPI 源、由它生成的 Go 客户端与 TypeScript 类型，以及对应契约测试；
- 同一协议变动的 Schema、错误码和 handler；
- 协议 Schema 与直接使用该 Schema 的代码；
- 构建脚本变化与证明该变化必要的测试；
- 同一功能产生的必要规范同步；
- 即时 `gofmt`、import、拼写和尾随补漏；
- 不能独立解释或撤销的零碎变动。

因此禁止出现下列提交：

- 单独补交生成物；
- 单独补交本应与实现同行的直接测试；
- 一个提交引入接口，后续提交才让仓库恢复编译；
- 一个提交只为修复前一个提交遗漏的格式或 import。

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

#### 巨大提交拆分标准

出现下列任一情况都必须进行拆分评估，不得因为属于同一个阶段就保留为单个提交：

- 同时修改多个互不依赖的领域；
- 同时引入 migration、认证、授权、分享、前端和文档；
- 包含多个可以独立回退的用户能力；
- 包含多个风险级别不同的安全边界；
- 修改文件很多，但无法用一句话准确描述其单一目的；
- 审查者必须理解数个互不依赖的子系统才能完成审查；
- 测试失败时无法快速定位责任范围。

行数和文件数只是发现候选的信号，不是判定标准；最终边界由依赖关系决定，并必须以逐提交构建结果证明。拆分后相邻切片若存在真实依赖，必须合并到同一提交，使每个提交都不引用尚未存在的 Schema、符号或路由，都有对应测试，且仍然足够小、足够可理解。

拆分困难必须由依赖关系和逐提交构建结果证明，不能只凭文件数量或同处一个文件断言。构建失败不是放弃拆分的结论，而是重新选择依赖边界的依据。

#### 微提交与相邻提交合并标准

以下情况必须积极评估合并，不得为了让每个细小目的独立成提交而长期保留没有回退价值的中间状态：

- 连续多个提交修改同一个文件；
- 同一次开发或同一次审计中，分别修改实施计划、验证记录、`README.md`、`PROJECT_STATUS.md` 和 `AGENTS.md`；
- 文档提交的具体目的略有不同，但共同描述同一个事实状态或同一次验证；
- 后一提交只是修正前一提交的措辞、计数、链接、格式或遗漏；
- 多个相邻提交分别增加测试配置、端口修正和 CI 调整，但共同形成一个可运行门禁；
- 相邻 fixup 只修补尚未离开开发窗口的原提交；
- 单独提交没有合理回退价值，其中间状态不应被长期保留。

合并文档提交时允许上升到更高层次的 `scope`，前提是合并后的标题和正文准确描述完整目的，而不是把多个无关目的并列。

#### 不应合并的情况

以下情况必须保持独立提交：

- 互不相关的产品领域；
- migration 与无关 UI；
- 安全修复与普通格式调整；
- 可独立回退的协议变更；
- 具有独立风险和独立测试证据的缺陷修复；
- 后续提交明确依赖某个中间状态；
- 合并会掩盖真实的架构决策；
- 合并会让提交重新变成不可审查的巨大提交。

后期独立审计发现的缺陷修复通常保留独立提交，因为它们有独立复现、独立验证记录和独立安全或正确性价值；不得为了让历史看起来从未犯错而把后期修复折叠回原始阶段提交。

#### 文档提交的合并规则

同一次事实更新产生的下列文档变化可以合并进同一个提交，不再因为文件身份而强制独立：

```text
AGENTS.md
README.md
PROJECT_STATUS.md
Documents/指南/01-v1实施计划.md
Documents/指南/02-测试与发布门禁.md
Documents/证据/验证记录.md
CONTRIBUTING.md
tests/README.md
Issue Form 与 Pull Request 模板
```

`AGENTS.md` 只有在修改独立、面向未来的 Agent 行为规范时才应单独提交；当它只是随同一次事实更新同步当前状态、阶段结论或优先级时，应并入该次事实更新提交。该放宽只作用于文档之间的合并，不改变 Agent 规则修订与生产代码修改必须拆分这一条：`AGENTS.md` 仍不得与生产代码放进同一个提交。

规范与 ADR 的处理：

- 改变产品语义时，规范或 ADR 应在实现之前提交，或与实现同行；
- 仅记录已经发生的实现演进时，可与对应阶段文档合并；
- 不得把规范或 ADR 的语义变化埋进纯状态同步提交。

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
9. 是否存在相邻提交与它描述同一事实、且单独没有回退价值？

标题无法概括整个提交或正文超过 8 个逻辑项时，必须优先拆分。真正独立的错误修复不得为了整洁而埋回旧功能提交；不能独立解释的即时补漏也不得单独存在。

### 消息结构

除 Merge 和 Revert 的专用格式外，每个正式提交必须同时包含标题和正文，并且只能使用以下两种结构。

非阶段提交：

```text
<type>(<scope>): <中文标题>

变动内容：
- <逻辑变动一>
- <逻辑变动二>
```

阶段任务提交：

```text
<type>(<scope>): <中文标题>

阶段 N 变动内容：
- <逻辑变动一>
- <逻辑变动二>
```

结构约束：

- 消息第一行就是标题，开头不得有空行；
- 标题与正文标题之间恰好一个空行；
- 正文标题只有 `变动内容：` 和 `阶段 N 变动内容：` 两种，各占一行，一个提交只能有一行正文标题；
- 正文标题后直接接第一个列表项，两者之间不得有空行；
- 正文标题后至少一个列表项；
- 列表项之间不得插入空行；
- 除 Revert 的 `撤销对象：` 行和本节允许的 Footer 之外，不得出现正文之外的额外说明、表格、代码围栏、编号列表、嵌套列表、自由段落或其它 Git trailer；
- 消息末尾保留且只保留一个换行。

已废止的 `开发阶段：阶段 N` 独立字段行不再允许出现，也不得与 `变动内容：` 并列书写。

### 标题

标题只负责用一句话概括该提交完成的核心逻辑结果。

标题必须：

- 匹配 `<type>(<scope>): <中文标题>`；
- 使用动宾结构并包含中文，代码标识和必要术语可以保留英文；
- 保持单行，整个标题行不超过 72 个 Unicode code point，按 code point 计数而不按 UTF-8 字节数计数；
- 只概括一个主要逻辑结果，不机械复述正文。

标题不得：

- 写测试结果、Actions 状态、性能数字、提交数量、Git 状态或下一步；
- 罗列多个并列子项；
- 使用“以及”“并且”“同时”等词连接多个独立能力；
- 以文件名或文件数量概括提交；
- 使用结束标点、Emoji 或 `!`。

`type`、`scope` 与冒号构成的前缀使用半角符号，这是 Conventional Commit 的机器格式，不适用中文标点规则：

- `type` 与 `(` 之间没有空格；
- `scope` 两侧使用半角圆括号 `(` 和 `)`；
- `)` 后紧跟半角冒号 `:`；
- 冒号后恰好一个半角空格；
- 冒号不得写成全角 `：`。

全角字符在终端中占两个显示列，因此即使标题未超过 72 个 code point，也应保持在一眼可读的长度内。

### 开发阶段归属

凡提交明确属于 Gallery 正式开发阶段，正文标题必须写成 `阶段 N 变动内容：`；不属于任何编号开发阶段的提交，正文标题只能写成 `变动内容：`。两者互斥。

当前合法编号只有 `阶段 0`、`阶段 1`、`阶段 2`、`阶段 3`、`阶段 4`、`阶段 5`、`阶段 6`、`阶段 7`。

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

不得根据仓库整体所处阶段给所有提交机械添加同一个阶段。开发阶段必须依据该提交实际所属的实施任务判断。`Walking Skeleton`、`Architecture Proof`、通用审计、CI、仓库治理和历史整理都是非编号工作，一律按非阶段提交处理并使用 `变动内容：`，不得擅自映射为 `阶段 0`～`阶段 7`；只有实施计划明确把某项任务归入编号阶段时才使用阶段正文标题。

一个提交若同时跨越多个编号阶段，必须优先按阶段拆分，不得在同一个正文中并列多个阶段编号。

Merge 描述的是分支历史合并行为，不是某个开发阶段内的实现切片，因此通常使用非阶段正文标题。Revert 使用哪一种正文标题取决于被撤销对象是否属于编号阶段，而不取决于 Revert 自身的类型或仓库当前阶段。三者的完整规则见 `Merge Commit` 与 `Revert Commit` 两节。

不得笼统表述为“Merge 和 Revert 都不写阶段编号”，因为 Revert 在被撤销对象属于编号阶段时必须写；也不得表述为“所有阶段任务提交都写阶段编号”并据此要求 Merge 写阶段编号，因为 Merge 不是普通阶段任务提交。

### `type`

`type` 使用英文小写 Conventional Commit 值，唯一允许：

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

### `scope`

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
| `审计` | 跨文档的一次性审计事实校正与结论归档 |
| `决策` | ADR |
| `工具链` | Go、构建工具和本地环境 |
| `工作流` | GitHub Actions 和 CI workflow |
| `发布` | 打包、版本和发行流程 |
| `测试基建` | `tools/testlab/**` 等阶段无关、跨阶段共用的测试框架、Source guard、语料生成和规则验收夹具 |

本词表只定义提交 `scope` 标签，不改写产品和文档的正式术语或分类。`目录` 用作 Catalog 领域的 `scope` 时，正文仍使用正式术语 Catalog；`指南` 汇总实施计划、测试门禁和验证记录的提交作用域，不改变验证记录在文档导航中的证据分类；`测试基建` 只指代跨阶段测试工具本身，不代表任何生产阶段的功能范围。

文档提交按文档主题选择 `scope`，例如 `docs(代理规则)`、`docs(指南)`、`docs(决策)`、`docs(接口)`、`docs(仓库)`。

`type` 已经表达提交性质，`scope` 必须表达作用领域，不得重复 `type`；因此不得出现 `docs(文档)`、`test(测试)`、`ci(持续集成)`、`build(构建)` 这类写法。

### 正文

正文负责把标题概括的核心结果拆成更细、可以逐项核对的逻辑变动。

正文列表必须按逻辑变动组织，不得按文件组织。每个列表项必须：

- 描述一个逻辑子变动；
- 使用动宾结构并包含中文；
- 比标题更具体，说明实际改变了什么语义；
- 不以文件路径开头；
- 不逐文件复述 diff；
- 不把多个彼此独立的动作塞进同一行；
- 不写测试是否通过、Actions 状态、提交 SHA、工作树状态、开发耗时、token 消耗或未来计划；
- 不使用结束标点。

列表项可以按需要提及类型名、API 路径、migration 编号、错误码、协议字段以及极少量关键文件名或文件路径，但这些内容只能服务于逻辑说明，文件路径不得成为正文的组织键。每个提交建议包含 1～8 个列表项；超过 8 项通常说明提交过粗、标题无法覆盖全部变动或存在可独立拆出的逻辑切片，必须重新评估提交粒度。

正文必须处于逐文件复述和过度概括之间。正确的详细程度是让读者不看 diff 也能判断语义变化，例如：

```text
变动内容：
- 定义合法状态集合与允许的转换条件
- 拒绝不满足前置条件的状态变更并返回稳定错误
- 将并发变更收敛为一次成功与一次冲突
- 补充正常路径与拒绝路径的回归测试
```

#### 措辞

正文必须使用具体、可验证的结果。禁止使用没有明确对象和结果的表述，例如：

```text
- 优化代码
- 更新文档
- 调整逻辑
- 完善测试
- 修复问题
- 同步相关内容
- 处理若干情况
- 做一些清理
```

不得用“等等”“若干”“相关”“部分”“一些”代替能够明确列出的逻辑范围。这些词允许出现在确实有明确限定对象的句子中，但不得用作模糊提交范围的替代品。

### 语义真实性

- 提交信息只能描述该提交实际引入的结果；
- 不得使用后续提交或当前最终文件树的信息改写早期提交，为当时尚不存在的能力编造描述；
- 不得把测试存在写成正式门禁通过；
- 不得把代码存在写成阶段完成；
- 不得把构建成功写成平台支持；
- 不得把合成数据结果写成真实规模结论；
- 不得在正文中记录本地测试日志、通过率或验收结论，这些内容属于任务报告和 CI。

### 排版：字符与编码

- 提交信息使用 UTF-8，不带 BOM；
- 使用 Unicode NFC 规范化形式；
- 禁止 Tab；
- 所有普通空格必须是半角 ASCII 空格 `U+0020`；
- 禁止全角数字和全角拉丁字母；
- 禁止下列不可见字符与特殊空白：`U+00A0`、`U+2000`～`U+200B`、`U+202F`、`U+205F`、`U+3000`、`U+FEFF`；
- 禁止行尾空白，包括标题行、正文标题行和每个列表项；
- 行分隔符只能是 LF，完整字节级规则见 `排版：行分隔符`。

### 排版：行分隔符

提交信息在 commit object 中的原始字节里，唯一合法的行分隔符是：

```text
LF（U+000A，字节 0A）
```

提交信息不得包含：

```text
CRLF（U+000D U+000A，字节 0D 0A）
孤立 CR（U+000D，字节 0D）
```

该规则作用于提交信息的全部结构，包括：

- 标题；
- 标题与正文之间的空行；
- 正文标题；
- 每个列表项；
- Revert 的 `撤销对象：` 行；
- Footer；
- 提交末尾的换行。

每个提交信息在原始字节层面必须满足：

- 开头不得有空行；
- 各逻辑行之间只使用单个 LF 分隔；
- 末尾恰好保留一个 LF；
- 末尾不得存在额外空行；
- 不得包含任何 `0D` 字节。

行分隔符一致性只能按 commit object 原始字节判定，不得依赖会归一化换行的显示接口，判定方法见 `人工审查与机器检查`。`core.autocrlf` 只影响工作树文件的 blob 内容，不得被当作提交信息换行的保障。

### 排版：阶段编号

标题、正文标题、列表项、`撤销对象：` 行和任何自然语言中，只要引用编号开发阶段，都必须写作 `阶段 N`：

- `阶段` 与编号之间恰好一个半角空格；
- `N` 使用半角阿拉伯数字；
- 合法编号为 `0`～`7`；
- 不得使用中文数字，不得使用全角数字。

多个阶段分别完整书写，并保留各自的空格：

```text
阶段 5 与阶段 6
```

连续范围使用全角波浪号，两端都写完整形式：

```text
阶段 0～阶段 7
```

正文标题写作：

```text
阶段 5 变动内容：
```

禁止下列全部形式：

```text
阶段五
阶段5
阶段  5
阶段　5
阶段 ５
第五阶段
阶段 5与阶段 6
阶段 5 与阶段6
阶段 0～7
阶段 0-阶段 7
阶段 0 ~ 阶段 7
```

`阶段` 之后紧跟中文数字时，机器检查一律判定为违规。若某个词组确实需要让 `阶段` 与中文数字相邻（例如把“执行阶段”与“一致性”连写），必须改写句子避免相邻，不得为它保留例外。

### 排版：汉字、拉丁字母与数字

中文自然语言与拉丁字母或技术缩写相邻时，使用一个半角空格：

```text
使用 Git 管理历史
生成 OpenAPI 客户端
建立 WebSocket 连接
```

不得写成 `使用Git管理历史`，也不得写成 `使用  Git 管理历史`。

汉字与阿拉伯数字相邻时，同样使用一个半角空格，包括数字与中文量词之间：

```text
包含 3 个步骤
等待 5 分钟
阶段 4
版本 2
10 个
5 次
```

下列内部结构不插入空格，它们是单个技术标识而不是中文与外文的相邻：

```text
OpenAPI
WebSocket
TypeScript
Argon2id
SHA-256
UTF-8
HTTP/2
REST/OpenAPI
x86_64
v2
0/0
```

数字与百分号之间不插入空格，写作 `100%`。数值与拉丁单位之间使用一个半角空格，写作 `16 KiB`、`5 ms`、`2 GiB`。

### 排版：行内代码、路径与技术标识

精确的命令、路径、字段名、函数名、类型名、配置键、错误码和文件名使用反引号包裹：

```text
运行 `go test ./...`
更新 `queryPublicationId`
读取 `AGENTS.md`
返回 `INVALID_ARGUMENT`
```

- 反引号内部保持原始字符，不应用任何中文排版规则，机器检查必须先剔除行内代码再判定空格与标点；
- 行内代码与相邻中文之间使用一个半角空格；
- URL、完整 SHA、文件路径和命令内部不插入中文排版空格；
- 已经被广泛当作普通名词使用的技术术语（例如 Catalog、Session、Job）可以不加反引号，但一旦写成精确标识符形式就必须加。

### 排版：标点与符号

中文自然语言使用全角中文标点：`，`、`。`、`；`、`：`、`！`、`？`、`、`、`（）`、`【】`、`“”`、`《》`。

- 中文标点前后不添加空格；
- 不得在中文语句中混用半角逗号、句号和冒号；
- 正文标题使用全角冒号，写作 `变动内容：`、`阶段 5 变动内容：`、`撤销对象：`；
- 中文解释性插入语使用全角圆括号，例如 `状态已更新（不改变外部行为）`；
- 函数调用、正则、命令、机器语法和 Conventional Commit 前缀使用半角括号；
- 中文引用使用 `“”`，文档或作品名称使用 `《》`；
- 范围使用全角波浪号 `～`；
- 负数、命令选项、标识符和版本内部的连字符使用半角 `-`；
- 中文省略号使用 `……`，不得用三个半角点代替；
- 中文破折号使用 `——`。

标题和列表项都不使用结束标点，因此中文句号不应出现在列表项末尾。

### 排版：空格、换行与缩进

- 提交信息的所有行均顶格书写，标题、正文标题和列表项都不得缩进；
- 列表项使用半角连字符加一个半角空格 `- ` 作为标记，每项独占一个物理行，物理行只由 LF 分隔；
- 正文使用平铺列表，不使用嵌套列表；确有必要的嵌套只允许两个半角空格缩进，禁止四个空格和 Tab；
- 词与词之间只允许一个半角空格，不得出现连续空格；
- 空行只出现在标题与正文标题之间，以及正文与 Footer 之间，且空行本身也只由单个 LF 构成；
- 消息开头不得有空行，末尾恰好保留一个 LF，末尾不得存在额外空行；
- 全部换行的字节级要求见 `排版：行分隔符`。

### Merge Commit

Merge 描述的是分支历史合并行为，其主要语义由合并主题和引入的逻辑变动表达，不是某个开发阶段的实现进度。Merge Commit 必须保留 Merge 结构，并使用非阶段正文标题：

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

Merge 正文仍按逻辑变动组织，不得列出冲突文件清单，也不得机械改用 `阶段 N 变动内容：`。

### Revert Commit

Revert 使用哪一种正文标题，取决于被撤销的原提交，而不是仓库当前整体所处阶段。

被撤销对象属于编号开发阶段时，在 `撤销对象：` 之后使用 `阶段 N 变动内容：`：

```text
revert(<scope>): 撤销 <原提交中文摘要>

撤销对象：<完整或不少于 12 位的 commit SHA>
阶段 N 变动内容：
- <恢复的逻辑行为>
- <撤销后重新生效的契约或边界>
```

被撤销对象不属于任何编号开发阶段时，使用 `变动内容：`：

```text
revert(<scope>): 撤销 <原提交中文摘要>

撤销对象：<完整或不少于 12 位的 commit SHA>
变动内容：
- <恢复的逻辑行为>
- <撤销后重新生效的契约或边界>
```

只有一个 Revert Commit 确实撤销连续范围时，才允许把 `撤销对象：` 写成 `<最早 commit SHA>^..<最新 commit SHA>`。范围 Revert 的正文标题判定与单一对象一致：仅当范围内全部原提交都属于同一个编号开发阶段时才使用该阶段的正文标题；只要范围内存在不属于编号阶段的提交，或范围跨越多个不同编号阶段，整个范围 Revert 都必须使用 `变动内容：`。

`撤销对象：` 只允许 Revert 使用，且必须紧跟在标题后的空行之后、正文标题之前。单个对象优先使用完整 SHA，最少保留 12 位；范围格式的两个 SHA 都必须完整或不少于 12 位。若最早对象是没有父提交的根提交，则不得使用范围格式。非连续提交集合不得合并为一个 Revert。Revert 不得只写“恢复文件”。

### 不兼容变更

Gallery 尚未发布稳定公共版本，继续禁止在标题中使用 `!`，也禁止在正文中使用 `BREAKING CHANGE:`。未来若启用，必须先修改本节，任何历史重写也不得擅自添加。

### Footer

Footer 是可选的，只在确实需要建立外部关联时使用。

- Footer 与最后一个列表项之间恰好一个空行；
- 每条 Footer 独占一行且顶格书写；
- 唯一允许的形式是 `Closes #<编号>`、`Refs #<编号>` 和 `Co-authored-by: <姓名> <邮箱>`；
- 不得编造 Issue 编号、共同作者或兼容性声明；
- 不得使用 Footer 记录验收结果、测试状态或下一步计划；
- 除上述三种之外，不得引入其它 Git trailer。

`Co-authored-by:` 只在提交确实包含多个真实作者的贡献时使用，历史重写中必须原样保留已有的真实署名。

### 签名、测试与历史重写

#### 签名、工作树与提交前验证

- 所有正式提交必须使用 SSH 签名，可以使用仓库已配置的 `commit.gpgsign=true` 或等效的显式 SSH 签名方式；
- 签名判断以 commit object 是否包含 `gpgsig` 为准，不得仅凭 `%G?` 显示 `N` 就认定未签名；
- 工作树干净状态分三个时间点判定：任务开始前必须干净；创建提交前暂存区只能包含该逻辑切片，且不得夹带无关的已暂存、未暂存或未跟踪文件；若本轮没有计划内的下一增量，提交后必须干净；
- 每个提交都不得处于无法构建、无法执行适用测试或明显不一致的中间状态；
- 门禁针对即将提交的完整文件树执行，并按 `默认交付流程` 选择完整门禁或纯文档轻量门禁；
- 功能、修复、migration、契约和生成物必须执行与风险相称的直接验证；
- 提交前必须核对暂存 diff、提交标题、`type`、`scope`、开发阶段归属及对应的正文标题形态、逻辑正文、文字排版和提交粒度；
- 验收结果记录在任务报告或 CI 中，不写入提交正文。

#### 历史重写授权与隔离

- 只有用户明确下令才允许历史重写；提交规范修订与实际历史重写必须属于不同提交和不同任务轮次；
- 历史重写不得与普通开发、生产修复或文档更新混在同一任务中；
- 历史正文必须依据每个原提交当时的真实 diff、依赖和时代语义编写。

#### 历史重写前保护

开始重写前必须：

1. 确认工作树、暂存区和未跟踪文件均为空；
2. 创建不可误覆盖的本地备份引用；
3. 创建不可误覆盖的远程备份引用；
4. 在仓库外创建完整 bundle 与镜像克隆；
5. 记录原 HEAD、原 tree SHA、提交数量、提交图和工作树状态；
6. 实际验证备份引用、bundle 与镜像可以解析到原 HEAD；
7. 确认 SSH 签名环境能够为全部重写提交签名。

不得删除任何历史备份。无法完成上述保护时必须停止，不得开始重写。

#### 历史重写执行

- 可以使用 `reword` 修正标题与正文，使用 `fixup` 或 `squash` 合并无意义尾随提交，使用 `edit` 拆分巨型提交，并在依赖安全时调整顺序；
- 每个重写后的提交都必须重新 SSH 签名，不得伪造旧签名或留下未签名提交；
- 必须逐提交审查标题、`type`、`scope`、正文、正文标题形态、文字排版、粒度和依赖，不得只做格式替换；
- 改变文件归属或提交顺序时，必须逐提交验证中间树可以构建并执行适用测试；
- 不得丢失 migration、测试、生成代码或文档，不得修改历史 migration 内容，不得为凑提交数量机械合并互不相关的能力；
- 尽可能保留作者和提交者的姓名、邮箱与日期；拆分沿用原提交作者，合并使用主要实现提交的作者，涉及多个真实作者时保留 `Co-authored-by:`，不得伪造他人作者身份；
- 每个旧提交都必须被明确归类为 `KEEP_AND_REWORD`、`SPLIT`、`MERGE`、`FOLD_FIXUP` 或 `DROP_REDUNDANT` 之一，并保存完整的 old→new 映射；
- `DROP_REDUNDANT` 只允许用于空提交、被后续相邻提交完全覆盖且没有独立证据价值的中间状态，以及已被合并结果自然包含的纯格式修正；不得因此丢失任何最终代码、测试、规范或验证事实；
- 内容守恒必须逐组证明：合并后的 diff 等于全部被合并提交的净效果，拆分后各提交 diff 之和等于原提交 diff，fold 后等于原提交与其 fixup 的净效果。

#### 历史重写后验证与发布

重写完成后必须：

1. 遍历全部目标历史，确认每个提交都包含 `gpgsig`；
2. 逐提交复核单一逻辑结果、格式、排版、粒度和依赖；
3. 按 commit object 原始字节遍历全部目标历史，确认每个提交信息只使用 LF，没有 `0D` 字节、没有多余末尾空行，方法见 `人工审查与机器检查`；
4. 证明新旧最终 tree SHA 完全一致，或准确说明允许的差异范围；
5. 确认 `git diff` 为空且工作树干净；
6. 确认没有修改历史 migration；
7. 保存重写后的提交数量和提交图；
8. 在替换远程历史前再次确认本地、远程和 bundle 备份仍可用。

历史重写只能在用户明确授权推送时发布，并且只允许带明确旧 SHA 的 `--force-with-lease=refs/heads/<分支>:<旧 SHA>`；禁止裸 `--force`，也禁止不带显式旧 SHA 的 `--force-with-lease`。推送前必须重新 `git fetch` 并确认远端仍停在该旧 SHA；一旦远端发生移动，立即停止强推、审查新增远端提交并重新生成重写计划。远端备份分支和备份 Tag 必须先于 `main` 推送成功，并保留到用户另行决定删除为止。不得自动创建 PR、删除备份引用或把普通推送授权解释为历史重写授权。若无法为全部提交重新签名、无法证明最终文件树等价或逐提交验证失败，必须恢复原始历史并报告阻塞。

历史重写完成后必须在仓库内保留一份重写记录，至少写明重写日期、原 HEAD、新 HEAD、备份分支、备份 Tag、bundle 名称与校验值、新旧提交数量、拆分与合并摘要、树等价结论和恢复方法；数百行的完整 old→new 映射保存在仓库外的备份目录，不写进仓库。

新一轮重写更新旧记录中的 SHA 时，必须保持该记录当时的事实，注明旧 SHA 属于上一代历史，不得把旧记录改写成它当时就使用新 SHA。

#### 历史整理自检

- 不得保留纯 `gofmt`、即时补漏或单行状态纠偏等不能独立解释的提交；
- 标题与正文格式正确不代表粒度正确，必须单独核对每个提交的单一意图；
- 大型提交必须按实际依赖评估能否拆成多个可构建、可测试的逻辑切片，并以逐提交构建结果证明。

### 人工审查与机器检查

人工审查和机器检查都是强制项，两个维度必须分别通过：

- 机器检查负责结构与排版：行分隔符、格式、`type`、`scope`、正文标题形态、列表形式、阶段编号写法、空格、标点、字符宽度和不可见字符；
- 人工审查负责语义与粒度：单一逻辑结果、描述是否与该提交真实 diff 一致、措辞是否具体、是否存在应拆或应并的边界；
- 机器检查通过不代表语义正确，语义正确也不代表排版合格；
- 不得用自动修正结果直接替换提交信息，自动修正只能产生候选文案，最终必须经人工复核。

机器检查必须读取 commit object 的原始字节，不得只依赖会归一化换行的展示接口。禁止把下列任一来源当作行分隔符判定的唯一依据：

- GitHub 网页；
- GitHub API 已解析的 message 字段；
- `git log` 的终端显示；
- PowerShell、Python 或 Node.js 的文本模式管道；
- 任何会执行 universal newline 转换的文件读取方式。

检查器必须按顺序执行：

1. 以二进制模式读取 commit object；
2. 在原始字节中定位 header 与 message 之间的首个 `0A 0A`，据此分离 header 与 message，不得把多行 `gpgsig` 签名头误判为 message；
3. 对 message 字节直接搜索 `0D`，出现任何 `0D` 即判定违规；
4. 确认 message 以且仅以一个 `0A` 结尾，且开头没有空行；
5. 再对解码后的 UTF-8 文本执行 BOM、NFC、排版和语义 lint。

只在文本层读取提交信息、不按原始字节拒绝 `0D` 的检查，不满足行分隔符门禁。

### 正例

下列案例保持抽象与中性，不绑定任何具体功能、接口或本轮历史问题；其中每个真实换行都是 LF。

#### 阶段功能

```text
feat(核心): 建立状态转换的服务端约束

阶段 2 变动内容：
- 定义合法状态集合与每次转换的前置条件
- 将不满足前置条件的转换收敛为稳定拒绝错误
- 为并发转换保留一次成功与一次冲突的判定
- 补充正常路径与拒绝路径的回归测试
```

#### 阶段修复

```text
fix(接口): 修正条件判定在边界输入下的取值

阶段 3 变动内容：
- 将空集合与缺省值区分为两种独立输入
- 修正上界取值把最后一个元素排除在结果之外的偏移
- 补充在旧实现下会失败的边界回归测试
```

#### 合并后的文档

```text
docs(指南): 统一实施说明的状态表述与引用格式

变动内容：
- 将同一事实在多份说明中的表述收敛为一处权威结论
- 校正指向已失效章节的内部引用
- 补充被新结论推翻的旧表述的修正依据
```

#### 非阶段 Agent 规则

```text
docs(代理规则): 重写提交信息与历史整理规范

变动内容：
- 重新划分标题、`scope` 与正文的职责
- 建立字符、空格、标点与阶段编号的统一排版规则
- 增加粒度判定、历史重写与机器检查要求
- 用中性案例替换与具体实现绑定的正反例
```

#### 测试基建

```text
test(测试基建): 建立隔离运行的测试夹具

变动内容：
- 为每次运行分配独立的临时目录与端口
- 在夹具退出时确认没有残留进程与数据
- 补充夹具自身的隔离性验证
```

#### CI

```text
ci(工作流): 固化双平台门禁的执行范围

变动内容：
- 在两类 runner 上分别执行常规检查与竞态检查
- 将工具链版本绑定至仓库声明的唯一来源
- 用显式包集合取代通配范围
```

#### Merge

```text
chore(仓库): 合并功能分支

变动内容：
- 合并指定分支的提交历史
- 保留主线新增的字段并解决契约冲突
```

#### Revert（撤销对象属于编号阶段）

```text
revert(核心): 撤销状态转换约束改动

撤销对象：1234567890abcdef1234567890abcdef12345678
阶段 2 变动内容：
- 恢复不校验前置条件的旧转换行为
- 移除并发转换的冲突判定
```

#### Revert（撤销对象不属于任何编号阶段）

```text
revert(代理规则): 撤销提交规范调整

撤销对象：1234567890abcdef1234567890abcdef12345678
变动内容：
- 恢复上一版提交说明约束
```

### 反例

每个反例只展示一个错误。

#### 逐文件正文

```text
变动内容：
- `internal/example/service.go`：修改服务逻辑
- `internal/example/store.go`：修改存储逻辑
```

错误原因：正文按文件组织，没有描述语义变化。

#### 正文过度概括

```text
变动内容：
- 更新代码
- 增加测试
- 同步文档
```

错误原因：无法判断具体语义变化。

#### 正文使用模糊范围词

```text
变动内容：
- 修正边界判定等若干问题
```

错误原因：用“若干”代替了能够明确列出的逻辑范围。

#### 正文包含验收结果

```text
变动内容：
- 修正边界判定并通过全部测试
```

错误原因：正文只描述逻辑变动，不记录验收结果。

#### 英文 scope

```text
fix(boundary): 修正条件判定的边界取值
```

错误原因：`scope` 中文优先且有固定词表，应使用词表中的中文作用域。

#### 使用已废止的开发阶段字段

```text
feat(核心): 建立状态转换的服务端约束

开发阶段：阶段 2
变动内容：
- 定义合法状态集合
```

错误原因：`开发阶段：阶段 N` 独立字段行已废止，阶段提交只能使用单行正文标题。

#### 阶段任务缺少阶段正文标题

```text
feat(核心): 建立状态转换的服务端约束

变动内容：
- 定义合法状态集合
```

错误原因：该能力属于编号开发阶段，正文标题必须写成 `阶段 N 变动内容：`。

#### Revert 遗漏应有的阶段正文标题

```text
revert(核心): 撤销状态转换约束改动

撤销对象：1234567890abcdef1234567890abcdef12345678
变动内容：
- 恢复不校验前置条件的旧转换行为
```

错误原因：被撤销对象属于编号开发阶段，必须使用该阶段的正文标题。

#### Revert 误加不应有的阶段正文标题

```text
revert(代理规则): 撤销提交规范调整

撤销对象：1234567890abcdef1234567890abcdef12345678
阶段 2 变动内容：
- 恢复上一版提交说明约束
```

错误原因：被撤销对象不属于任何编号开发阶段，不得机械附加阶段正文标题。

#### 标题承载多个独立能力

```text
feat(核心): 完成状态约束、查询边界、内容读取与授权判定
```

错误原因：多个独立垂直切片无法用单一提交合理撤销。

#### 标题使用结束标点

```text
feat(核心): 建立状态转换的服务端约束。
```

错误原因：标题不使用结束标点。

#### 标题使用全角冒号

```text
feat(核心)：建立状态转换的服务端约束
```

错误原因：Conventional Commit 前缀是机器格式，必须使用半角冒号加一个半角空格。

#### 阶段编号使用中文数字

```text
阶段二 变动内容：
```

错误原因：阶段编号必须使用半角阿拉伯数字。

#### 阶段与编号之间缺少空格

```text
阶段2 变动内容：
```

错误原因：`阶段` 与编号之间必须恰好一个半角空格。

#### 阶段范围写法不完整

```text
- 复核阶段 0～7 的遗留项
```

错误原因：连续范围两端都必须写成完整的 `阶段 N` 形式。

#### 汉字与拉丁字母之间缺少空格

```text
- 建立WebSocket 连接的授权判定
```

错误原因：中文与拉丁字母相邻处必须有一个半角空格。

#### 汉字与拉丁字母之间空格过多

```text
- 建立  WebSocket 连接的授权判定
```

错误原因：只允许一个半角空格。

#### 中文标点前后出现空格

```text
- 收敛状态判定 ，并补充回归测试
```

错误原因：中文标点前后不添加空格。

#### 列表项之间插入空行

```text
变动内容：
- 定义合法状态集合

- 拒绝不满足前置条件的变更
```

错误原因：列表项之间不得插入空行。

#### 列表项使用缩进

```text
变动内容：
  - 定义合法状态集合
```

错误原因：正文所有行顶格书写，列表项不得缩进。

由于 Markdown 无法直观显示原始字节，下面四个换行反例用转义序列表达实际字节，每个只展示一个错误。

#### 行分隔符使用 CRLF

```text
feat(核心): 建立状态转换的服务端约束\r\n\r\n阶段 2 变动内容：\r\n- 定义合法状态集合\n
```

错误原因：出现 `0D 0A`，行分隔符必须只使用单字节 `0A`。

#### 出现孤立 CR

```text
feat(核心): 建立状态转换的服务端约束\n\n变动内容：\r- 定义合法状态集合\n
```

错误原因：出现不带 `0A` 的孤立 `0D`，提交信息不得包含任何 `0D` 字节。

#### 末尾存在多个空行

```text
feat(核心): 建立状态转换的服务端约束\n\n变动内容：\n- 定义合法状态集合\n\n\n
```

错误原因：消息末尾必须恰好保留一个 `0A`，不得存在额外空行。

#### 缺少末尾 LF

```text
feat(核心): 建立状态转换的服务端约束\n\n变动内容：\n- 定义合法状态集合
```

错误原因：消息必须以且仅以一个 `0A` 结尾。

### 提交前检查表

创建提交前逐项确认：

1. 提交只有一个主要逻辑结果，且已包含所有直接依赖的测试、migration、契约、生成物和必要文档。
2. 标题使用允许的 `type`、词表中的 `scope` 和中文动宾结构，半角括号与冒号正确，整行不超过 72 个 Unicode code point。
3. 阶段任务使用唯一合法的 `阶段 N 变动内容：`，非阶段任务使用 `变动内容：` 且没有机械添加阶段编号；Merge 使用非阶段正文标题，Revert 已按被撤销对象是否属于编号阶段正确选择正文标题。
4. 正文至少包含一个且通常不超过 8 个细粒度逻辑变动列表项，没有逐文件组织、模糊范围词、验收结果、状态或未来计划。
5. 正文描述与该提交的真实 diff 一致，没有借用后续提交或最终文件树的信息。
6. 全部阶段引用写作 `阶段 N`，范围写作 `阶段 M～阶段 N`。
7. 汉字与拉丁字母、汉字与数字之间恰好一个半角空格；固定技术标识、行内代码、路径、URL 和 SHA 内部未被插入空格。
8. 中文标点为全角且前后无空格，Conventional Commit 前缀为半角，标题与列表项没有结束标点。
9. 没有 Tab、全角数字、全角拉丁字母、全角空格、不间断空格、零宽字符、连续空格或行尾空白；文本为 NFC 且不含 BOM。
10. 标题与正文标题之间恰好一个空行，列表项之间没有空行，正文所有行顶格，消息末尾只保留一个换行；按 commit object 原始字节确认全部行分隔符为 LF，没有 `0D` 字节，末尾没有多余空行。
11. Merge 或 Revert 严格使用专用格式，Revert 包含可追踪 SHA。
12. 没有使用 `!` 或 `BREAKING CHANGE:`；若存在 Footer，只使用允许的三种形式且与正文之间恰好一个空行。
13. 已执行适用门禁，提交树可以构建、测试且逻辑一致。
14. 提交将使用 SSH 签名，并会从 commit object 检查 `gpgsig`。

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

阶段 0～4 已完成代码与合成 Correctness；EV-42 又补齐显式规则/有效封面、Work 快照契约和 Web 同快照封面/编辑的合成 Correctness，EV-44 关闭 Work 聚合查询逐成员授权缺口并增加 catalog v12 revision 成员事实，EV-45 再把 1,000 Work 的查询/Cursor/媒体/DerivedAsset Correctness 接入普通 `go test` 持续门禁，EV-51 又用 catalog v18 窄候选修复 FTS 命中后的宽投影查找并取得 500,000 单并发候选基准，EV-87 再用 catalog v20 窄候选补齐 500,000 受限聚合代表分布与高 Creator 基数诊断，但均不改变 API Freeze/Reference Performance 整体结论。阶段 5 安全代码基线及 EV-37 合成证据完整，EV-38 又补充 Chrome/Edge 同机 Personal/LAN 与当前工作站 Argon2id 证据，EV-44 补齐 Work 查询的 deny/Token scope/hidden 写权限合成授权证据，EV-60 再补齐 Personal 与独立 loopback LAN 安全资源管理浏览器链，EV-86 继续关闭 Creator/Library 聚合封面的逐主体授权缺口，EV-126～128 再关闭生产 ToolDiscovery、Windows 进程树 CPU/内存和 13 样本真实双工具有界恶意媒体切片，但正式 Security Gate 未通过。阶段 6 React/TypeScript Web/PWA、同源嵌入资产、静态壳 PWA 和主要页面骨架已实现，EV-54～EV-77 已把管理自举、publication-bound 画廊/媒体、CustomCover、规则生命周期/ParameterSet/Schema 表单、Dry Run/Explain/Trace、按字段撤销与参数 Schema/tests/extensions 无损结构化编辑、安全管理、真实 WS 断线与单帧 sequence gap 的 snapshot 恢复、维护、规则绑定状态、作品/媒体人工解绑与撤销、普通 Binding issue 三决定/生命周期/分页、全部五种 SourceWork 结构 action、全部 orphan decision/实体类型及重现身份语义、已消费决策冲突、retry-backoff Job、运行中 Scan/Hash 级联取消、进程强杀后的启动接管与 control 恢复实际重启接入隔离 Chromium/Firefox 真实后端持续门禁，并关闭扫描/Watcher 与维护终态同步竞态；EV-78/EV-79 再建立双入口窄屏焦点和 320px Grid 溢出回归，但正式 Web Gate 未通过，且 EV-39 证明其「业务闭环」此前被高估；真实设备与完整可访问性仍缺。阶段 4 的 EV-35/EV-36 Correctness/Cursor 结论保持；EV-108 已让 Gank/Pawchive 真实规则与 Source 至少各完成一条有界成功链，但 Pawchive 12 目标确认仍暴露取消响应和重复全 Source 处理缺口。Reference Performance、完整真实语义、全量扫描与 API Freeze 仍未完成。

EV-111 已关闭上一段 EV-108 所述“Pawchive 多目标逐个重复完整 Source 处理”缺口；EV-112 又以公共 Job API 观察到真实 `hash/running` 后取消，关闭 Windows 本地 SSD/Pawchive 的活动 Hash 父子收敛切片。当前可开工重点转为 HDD/SMB/NAS 取消、publishing 临界点与真实存储崩溃恢复，以及正式 500,000 publication/Query 长跑、签名/RC 和真实设备门禁；不得把单个本地 SSD 切片外推为全量扫描或完整取消 Gate。

EV-39 登记的阻断性缺陷已由 [EV-40](Documents/证据/验证记录.md)、[EV-44](Documents/证据/验证记录.md) 与 [EV-45](Documents/证据/验证记录.md) 分轮关闭：EV-40 关闭 `WS-1`、`WS-2`、`CAP-1`、`API-1`、`SEC-1`、`SEC-2`、`SEC-4`、`TEST-1`、`BLD-1` 与 `A11Y-1` 的键盘部分；EV-44 关闭 `AUTHZ-1` 与 `QRY-1`，使 Work 聚合查询在 total/分页前按 publication 成员应用 effective capability、deny 与 Token scope，并使 `overlay.hidden` 同时要求逐成员 `library.write`；EV-45 关闭 `TEST-2`，使 39 项查询、6 项 Cursor 与 20 项媒体/DerivedAsset finding 通过生产 bootstrap、真实 loopback HTTP 和临时 AppDirs 进入普通 `go test`；[EV-46](Documents/证据/验证记录.md) 关闭 `MED-1` 与 `SEC-3`。**EV-39 登记的缺陷至此全部关闭。**

`MED-1` 的裁决见 [ADR-010](Documents/ADR/ADR-010-已确认媒体的正文读取语义.md)：正文改为流式区间读取，完整性由 publication 冻结的 size/mtime 证据（catalog v13）+ 整文件读取顺带复算 digest 分层保证。「快速指纹只能筛选候选、不能代替完整内容哈希」这条边界**未被弱化**——身份证据只用于判定既有 ContentBlob 是否仍然成立，建立新 Blob 仍以首次完整 SHA-256 为前置条件。`SEC-3` 的呈现策略已写入 `规范/08`「呈现策略与内联白名单」。

EV-46 另行发现并修复三项本轮新缺陷，后续开发必须继续遵守其结论：`LINK-1` Windows 目录联接报告为 `fs.ModeIrregular` 而非 `fs.ModeSymlink`，链接判定一律走 `internal/platform/filesystem.IsLink`；`TX-1` WAL 下 DEFERRED 事务的读后写会遇到 `busy_timeout` 无法吸收的 `SQLITE_BUSY_SNAPSHOT`，DSN 固定 `_txlock=immediate`，新增读后写路径不得改回延迟事务；迁移耗时预算不得以固定墙钟写进可移植单元测试。

[EV-48](Documents/证据/验证记录.md) 关闭审计遗留的恶意输入缺口：全部规则递归路径共享 `MaxRuleNestingDepth=256`（PRE_FREEZE），不得恢复为空格式隐式 JSON 或依赖解析器/goroutine 栈的不同上限；Source 相对路径不得接受段内冒号、NTFS 备用数据流与 `CONIN$`/`CONOUT$`；Range 位置只接受 ASCII 数字；签名 Cursor 只接受签发器产生的规范 base64url 原文。

EV-54～EV-77 已让管理自举、publication-bound 画廊/媒体、CustomCover 真实写后 publication、规则生命周期/ParameterSet、模板驱动 Schema 表单/无损文本、当前草稿 Dry Run/Explain/Trace、按字段撤销与参数 Schema/tests/extensions 无损结构化编辑、安全资源管理、真实 WS 断线和单帧 sequence gap 的 HTTP snapshot 恢复、维护、规则绑定状态、作品/媒体人工解绑与撤销、普通 Binding issue 三决定/生命周期/分页、全部五种 SourceWork 结构 action、全部 orphan decision/实体类型及重现身份语义、已消费决策冲突、retry-backoff Job、运行中 Scan/Hash 级联取消、进程强杀后的启动接管与 control 恢复实际重启 E2E 以 Chromium/Firefox 进入 CI，并关闭显式扫描/Watcher 状态脱节、扫描期间事件丢失与维护终态不刷新的竞态；EV-78～EV-85 又补齐窄屏焦点/溢出、长离线/吊销收敛、画廊查询迟到成功/错误响应隔离、一次传输中断恢复、同 origin 服务长停机后的持续自愈、双入口共享设计语言重构及真实媒体读取背压恢复，当前最大的 Web 门禁缺口转为其余完整弱网抖动矩阵、真实设备、真实存储响应与完整可访问性。`gallery-rules.json` 所需的三级聚合封面、独立只读文件根、平台呈现/排序集下发均已实现；EV-50 已锁定 Source-local 聚合资源边界并消除 Creator→Source 的二次候选扇出，EV-86 又关闭 Creator/Library 聚合封面的逐主体授权正确性缺口，EV-87 再以 catalog v20 窄候选补齐代表分布与高 Creator 基数测量；EV-51 已用窄候选消除 FTS 后逐命中宽投影查找，排序协议 v2 已贯通 title/name、publishedAt、Progress、签名 keyset 与 Web 规则菜单，但完整 Reference Performance/API Freeze 证据仍未取得。

其后并行关闭阶段 4 性能/API Freeze、阶段 5 真实物理 LAN/目标低端设备/恶意资源门禁、阶段 6 真实移动设备/屏幕阅读器/完整业务 E2E，以及阶段 7 便携基线之后的 Authenticode、升级/回滚与 Windows 发行门禁；在这些完成前不要进入桌面壳或把测试制品称为 RC。真实 HDD/SMB/NAS、Linux 原生与重挂载文件身份稳定性、ranking/total/租约等 PRE_FREEZE 数值、AND/OR canonical 化、Wails/Tauri 与跨平台发行仍属后续门禁。
