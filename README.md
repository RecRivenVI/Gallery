# Gallery

Gallery（画廊）是一个本地优先、只读来源、规则驱动的个人媒体目录系统。

媒体文件始终留在用户自己的目录里：Gallery 不改名、不移动、不删除原文件，只读取内容并建立一个可以随时删除重建的目录数据库（Catalog）。收藏、阅读进度和人工整理结果单独保存，不会因为重新扫描而丢失。内嵌 Web/PWA、CLI、未来桌面壳和第三方客户端均基于同一套 API 契约。

> [!IMPORTANT]
> Gallery 当前仍处于 pre-alpha 开发阶段。阶段 0～4 后端主线、阶段 5 安全代码基线及阶段 6 Web/PWA 页面代码基线均已实现；Chrome/Edge 已完成真实认证主路径验证，Chromium/Firefox 已完成合成 smoke 与隔离真实后端持续链，但阶段 5 Security Gate 和阶段 6 Web Gate 均未通过。2026-07-23 的独立审计（[验证记录 EV-39](Documents/证据/验证记录.md)）发现实时 WebSocket 与权限名阻断；这些缺陷已由 [EV-40](Documents/证据/验证记录.md) 修复。[EV-54～EV-59](Documents/证据/验证记录.md) 随后以隔离 Chromium/真实 `galleryd` 打通管理自举、publication-bound 画廊/媒体、CustomCover、规则草稿→发布→Binding→扫描、无损规则文本、模板驱动 Schema 表单及规则回滚/弃用/ParameterSet；[EV-60](Documents/证据/验证记录.md) 又补齐 Personal Session/API Token/Share、安全管理写链和真实 WebSocket 断线后的 snapshot 恢复，并覆盖独立 loopback LAN 账户/Grant/Session 管理；[EV-61](Documents/证据/验证记录.md) 覆盖 control 备份、恢复干跑/待重启登记和 Catalog GC dry-run；[EV-62](Documents/证据/验证记录.md) 再覆盖规则绑定暂停/恢复、作品人工解绑/撤销，以及 retry-backoff Job 取消、同 ID 重试和 Attempt 历史；[EV-64](Documents/证据/验证记录.md) 进一步以同一隔离 AppDirs 的实际重启证明 control 恢复生效；[EV-65](Documents/证据/验证记录.md) 把以上 Personal/LAN 完整链扩展到桌面 Firefox 并纳入 CI；[EV-66](Documents/证据/验证记录.md) 再关闭显式扫描与 Watcher 状态脱节、扫描期间事件可能丢失和维护 Job 终态不刷新任务表的竞态；[EV-67～EV-71](Documents/证据/验证记录.md) 又补齐全部现有 primitive config 的 Schema 字段、当前草稿 Dry Run/Explain/Trace、按字段撤销、参数 Schema/tests/extensions 的无损结构化编辑，以及单帧 sequence gap 后的 HTTP 全快照恢复；[EV-72](Documents/证据/验证记录.md) 再从双浏览器 UI 验证运行中 incremental Scan、活动 Hash 子任务、Attempt 与 publication 的取消收敛；[EV-73](Documents/证据/验证记录.md) 继续以真实进程强杀和同 AppDirs 立即重启证明启动期能接管未来租约下的遗留 Attempt，并从 UI 解释和治理 `PROCESS_INTERRUPTED`；[EV-74](Documents/证据/验证记录.md) 建立首批治理链，[EV-75](Documents/证据/验证记录.md) 再覆盖 SourceWork merge、全部 orphan decision/实体类型与已消费决策冲突，[EV-76](Documents/证据/验证记录.md) 又补齐普通 Binding issue 三决定、真实生命周期、双标签页冲突和 51 条分页并修复活动唯一性，[EV-77](Documents/证据/验证记录.md) 最后补齐全部五种 SourceWork 结构 action 的浏览器写路径、三种剩余 action 的实际消费和孤儿重现身份语义。这些仍不等于真实媒体或完整业务闭环。EV-103～EV-107 已建立未签名 Windows x64 便携测试制品/SBOM/smoke、恢复/回滚、真实 schema 23→24/反向拒绝及首次轮换失败基线，但没有正式安装发行版本或完整使用教程；EV-108 已让 Gank/Pawchive 各完成一条真实规则、真实 Source、全树零写入的有界成功链，同时保留 Pawchive 12 目标确认在取消后未及时收敛的失败。真实全量性能、SMB/NAS、真实 LAN 多设备、目标低端设备、真实移动设备、签名和跨平台发行门禁仍未完成。
>
> [EV-109](Documents/证据/验证记录.md) 已阻止恢复候选落位与旧库回滚双失败后继续创建空 control 库；[EV-110](Documents/证据/验证记录.md) 又以真实 Windows 便携 `galleryd` 证明候选落位 sharing violation 后可安全回滚并保留备份后事实；[EV-119](Documents/证据/验证记录.md) 再用真实 Win32 handle 同时阻断候选与轮换副本 Rename，证明双失败会保留 pending/双副本并以 `RESTORE_FAILED` 停止。后者仍是包内链，不替代便携进程级双失败、磁盘满/中断、正式签名或 RC Gate。
>
> [EV-111](Documents/证据/验证记录.md) 已把同一 Source 的多个按需确认目标合并为一个兼容批量 Job，保留单媒体入口与每目标完整哈希。真实 Pawchive 12 目标最终以一个 Job 在 74.003 秒确认 12/12，全树 11,595 文件/2,353 目录前后零变化；这关闭逐目标重复完整 Source 处理，不代表活动 Hash 取消、全量扫描、HDD/SMB/NAS、正式性能或 RC Gate 已通过。

> [EV-112](Documents/证据/验证记录.md) 已在全新 Pawchive 隔离运行中先从公共 Job API 观察到真实 `hash/running`，再取消父 Scan；父子任务在 4 ms 内被观察为 `cancelled`，完整 124,660,469,885-byte Source 前后增删改为 0。该结果关闭 Windows 本地 SSD/Pawchive 这一条活动 Hash 取消切片；HDD/SMB/NAS、publishing 临界点、崩溃恢复、全量与 RC Gate 仍未通过。
>
> [EV-67](Documents/证据/验证记录.md) 已把 15 类现有 primitive config 从整块 JSON 文本提升为同一权威 Schema 驱动的可视化字段；[EV-68](Documents/证据/验证记录.md) 又把当前草稿的 Dry Run、Explain 与 Trace 接入同一 Chromium/Firefox 真实后端链，并保持请求/响应精确数字无损；[EV-69](Documents/证据/验证记录.md) 再补齐以本地精确基线为准的按字段撤销；[EV-70](Documents/证据/验证记录.md) 继续以快捷区、递归 JSON 树和原始文本编辑参数 Schema、tests 与 extensions；[EV-71](Documents/证据/验证记录.md) 再证明真实单帧丢失形成 sequence gap 时会重取无关的 Library/Source HTTP snapshot；[EV-72](Documents/证据/验证记录.md) 证明从 UI 取消运行中的 Scan 会级联活动 Hash 且不发布半成品；[EV-73](Documents/证据/验证记录.md) 又证明真实强杀后的同 AppDirs 立即重启会保留 recovered Attempt、稳定错误和可见治理入口；[EV-74](Documents/证据/验证记录.md) 建立首批治理链，[EV-75](Documents/证据/验证记录.md) 再补齐 merge、完整 orphan decision/实体类型和已消费冲突，[EV-76](Documents/证据/验证记录.md) 则补齐普通 issue 三决定、真实生命周期、双标签页 sibling 冲突和 51 条游标分页，[EV-77](Documents/证据/验证记录.md) 再补齐三种剩余结构 action 的消费和孤儿重现。未知结构、legacy extension 和精确数字仍可往返，服务端校验/编译保持权威；Web Gate 与 pre-alpha 状态不变。
>
> [EV-78](Documents/证据/验证记录.md) 又为当前正式双入口 UI 增加 42rem 窄屏模态导航，并用 Chromium/Firefox 390×844 smoke 验证桌面/窄屏导航互斥、焦点陷阱、Escape、焦点返还、当前页语义、axe 与无横向溢出。它关闭的是桌面浏览器 viewport 下的窄屏导航焦点缺口，不代表真实移动设备、触控或人工屏幕阅读器 Gate 通过。

> [EV-79](Documents/证据/验证记录.md) 纠正 EV-78 的跨平台溢出证据：精确 `ee69eee` 的 Linux Chromium 在管理端 390×844 暴露 3px 横向溢出，现已通过可收缩 Grid 轨道与长协议标识换行修复，并把 320px 最低宽度加入回归。Windows 双浏览器与 WSL Linux Chromium 多宽度产物探针通过，最终文档 HEAD `dd0343e9f4740d2fcd5f3b7fd9f004c1218cd743` 的 Actions run `30378490971` 全部成功；真实移动/触控与人工屏幕阅读器 Gate 仍未完成。

> [EV-80](Documents/证据/验证记录.md) 修复长时间离线会在约 75 秒后耗尽重连预算，以及 Firefox 丢失 Session 吊销 4401 时认证外壳无法收敛的问题；离线期间暂停退避，恢复网络或在线异常关闭时刷新 bootstrap 与 HTTP snapshot，并用连接代次拒绝迟到旧回调。Chromium/Firefox 各 17 个真实后端测试连续三轮切换 offline/online 并逐轮验证事实收敛；带宽、延迟、丢包、服务长停机与真实移动网络仍属完整弱网门禁缺口。

> [EV-81](Documents/证据/验证记录.md) 修复旧查询的游标错误通知跨搜索残留并阻止新结果续页的问题；查询继续向 OpenAPI 客户端传递取消信号，组件回归锁定旧分页与旧详情迟到后不能覆盖新搜索/路由，Chromium/Firefox 生产资产 smoke 另锁定旧分页迟到返回不进入新结果。该证据使用可控合成延迟，不代表随机延迟/丢包、带宽、代理、服务长停机或移动网络门禁。

> [EV-82](Documents/证据/验证记录.md) 修复浏览器仍在线但 `galleryd` 长时间不可用时 8 次普通重连耗尽后永久停止的问题；普通异常关闭现在以 15 秒封顶持续退避，安全与协议终态不变。Chromium/Firefox 各 18 项真实后端链在同一页面、Session、临时 AppDirs 与 origin 下停止服务，跨过旧预算后按原端口重启，并验证 WebSocket 与 `/api/v1/jobs` snapshot 无刷新恢复。随机延迟/丢包、带宽、代理、移动网络、反复崩溃与真实设备仍未覆盖。

> [EV-83](Documents/证据/验证记录.md) 在真实 `galleryd` 作品查询中加入一次 GET 传输中断、第二次请求 300 ms 受控延迟及旧结构化错误无视取消后的迟到交付。Chromium/Firefox 各 19 项完整链均证明网络失败自动重试后显示真实后端数据，旧 `FORBIDDEN` 不覆盖新搜索；现有生产实现无需修改。随机延迟/丢包分布、带宽、代理、移动网络和真实设备仍未覆盖。

> [EV-84](Documents/证据/验证记录.md) 依据三份 Legacy 前端设计材料净室重做共享设计语言、用户端媒体优先侧栏/抽屉与管理端紧凑控制台/响应式表格；认证、Capability、publication、Overlay、规则、安全、维护和治理契约保持不变。Chromium/Firefox mock smoke 14/14、各 19 项隔离真实 `galleryd` 完整链和根级 202 项 Vitest 均通过；真实移动/触控设备、人工屏幕阅读器和正式 Web Gate 仍未完成。

> [EV-85](Documents/证据/验证记录.md) 让 E2E 专用真实 `galleryd` 在已打开合成 Source 句柄后耗尽媒体读取名额；生产用户端收到真实 503 `MEDIA_READ_BUSY/retryable=true` 后，无刷新自动退避并恢复 200 与图片解码。Chromium/Firefox 完整链各增至 20 项；默认 16 名额/5 秒仍未冻结，真实存储和多客户端争用门禁仍待完成。

## 特色功能

| 特色 | 说明 | 当前状态 |
| --- | --- | --- |
| 媒体来源永久只读 | 不修改、移动或删除原始媒体文件 | 已实现 |
| 本地优先 | 数据库、缓存和用户信息保存在本机 AppDirs（程序自身数据目录） | 已实现 |
| 规则驱动识别 | 用受限规则描述不同目录和 metadata 结构，无需为每种来源写死逻辑 | 已实现 |
| 可重建 Catalog | 扫描出的目录数据库可以随时删除后重建 | 已实现 |
| 用户数据独立保存 | 收藏、进度、人工绑定和覆盖信息不会被重新扫描覆盖 | 已实现 |
| 原子快照发布 | 用户不会看到扫描到一半的中间数据 | 已实现 |
| 全文搜索和结构化查询 | 支持搜索、过滤、排序、分页、高亮和 publication 同快照有效封面 | 主线已实现，部分数值待冻结 |
| 后台任务与崩溃恢复 | 支持取消、重试、租约和强制终止后自动恢复 | 已实现 |
| 媒体读取与缩略图 | 支持按字节范围下载（Range）、ETag、按需内容校验和 JPEG 缩略图 | 已实现 |
| API-first | Web、CLI、桌面壳和第三方客户端共用同一套正式 API | 已实现后端契约 |
| 本地账户、资源授权与分享 | Personal 配对、LAN 本地账户、Session、API Token、Library/Source Grant、即时吊销，以及匿名 Work/Media/媒体正文 Share | 后端代码与合成安全门禁已实现；Personal 与同机 loopback LAN 安全管理已有真实浏览器链，物理 LAN/目标设备 Gate 未通过 |
| 图形界面 | 响应式 Web/PWA 覆盖浏览、作品/媒体、实际封面与 CustomCover 编辑、Overlay、任务、规则、安全和维护页面 | 页面代码基线、Library/Source/扫描、publication-bound 画廊/媒体、CustomCover、规则生命周期、Schema 表单、安全、维护、规则绑定状态、作品/媒体人工解绑与撤销、普通 Binding issue 三决定/生命周期/分页、全部五种 SourceWork 结构 action、全部 orphan decision/实体类型及重现身份语义、已消费决策冲突、retry-backoff、运行中 Scan/Hash 级联取消、进程强杀启动接管与 control 恢复实际重启 E2E 已实现；完整设备门禁仍待覆盖 |
| 安装与发行 | 安装包、签名、升级、跨平台发行 | 已有未签名 Windows x64 便携测试包/SBOM/smoke 基线；正式发行未开始 |

## 设计特点

- **数据分层**：从来源扫描出的事实（Source-derived）、系统认定的权威数据（Canonical）、事实与权威数据的绑定关系（Binding）、用户自己整理的信息（Overlay）彼此分离，各自有清晰的生命周期。
- **两个数据库**：不可凭空重建的用户数据存放在 `control.db`；可以随时删除重建的扫描结果存放在 `catalog.db`。
- **路径不是身份**：文件的身份由内容本身（完整哈希）决定，不是文件名或路径，改名、移动都不影响识别。
- **一致性来源明确**：REST 接口返回的快照是事实依据，WebSocket 只作为实时提示，断线重连后以快照为准。
- **发布保证一致性**：目录库只发布完整的快照，不会让用户看到扫描到一半、新旧数据混杂的中间状态。
- **规则不能执行任意代码**：识别文件夹结构的规则只能使用有限的、可分析的表达方式，不能运行任意脚本。
- **核心与平台隔离**：文件身份识别、监听、路径处理等操作系统相关的差异被隔离在独立的适配层之外，不影响核心逻辑。

## 技术栈

| 层面 | 技术 |
| --- | --- |
| 后端语言 | Go 1.26 系列 |
| 服务架构 | API-first 模块化单体，单主进程 |
| HTTP | Go `net/http` |
| 数据库 | SQLite WAL，`control.db` 与 `catalog.db` 分离 |
| 搜索 | SQLite FTS5 + CJK bigram（中文双字分词）+ 拉丁文与文件名 trigram |
| API | REST/JSON + OpenAPI |
| 实时通信 | 版本化 WebSocket（浏览器端握手已修复并有真实浏览器门禁） |
| 规则系统 | 规范 JSON、JSON Schema、有限原语、受限 CEL 表达式 |
| SQLite 驱动 | `modernc.org/sqlite`，基础发行不依赖 cgo |
| 自动化 | PowerShell、GitHub Actions |
| Web/PWA | React 19、TypeScript 5.9 strict、Vite 8、TanStack Query、React Aria、RJSF/AJV |
| 当前客户端状态 | `galleryd` 同源内嵌响应式 Web/PWA；可构建未签名 Windows 便携测试包，暂无桌面壳或正式安装包 |
| 未来客户端方向 | 先关闭无壳 Web 业务与可访问性 Gate，再评估可选薄桌面壳 |

## 当前进度

| 阶段 | 功能总体状态 | 测试/门禁总体状态 | 最重要的已完成能力 | 最大缺口 | 下一步 |
|---|---|---|---|---|---|
| 阶段 0：契约骨架 | ✅ | ✅（限定范围内） | 建立两个数据库、错误码、接口协议等基础设施 | 具体数据库表结构当时未定死（计划内安排） | 已完成 |
| Walking Skeleton | ✅ | ✅（限定范围内） | 用最简单的例子验证整条链路能跑通 | 只验证了单文件的最简单场景 | 已完成 |
| Architecture Proof | ✅ | ✅（限定范围内） | 验证了强制中断后系统能自行恢复 | 数据库最终表结构仍未冻结（计划内安排） | 已完成 |
| 阶段 1：领域和数据所有权 | ✅ | ✅（限定范围内） | 备份/恢复、目录库整体重建、作者合并等已通过验证 | 网络共享盘、底层文件身份识别留待以后阶段 | 已完成 |
| 阶段 2：规则系统 | ✅（正确性层面） | ✅（限定范围内） | 规则生命周期、编译执行、参数/绑定和影响调度已形成闭环 | 正式性能/平台门禁留待后续 | 已完成 |
| 阶段 3：扫描、任务与目录库 | ✅（代码与模拟数据层面） | 🟡（真实大盘抽样与三平台有界链通过，全量未完成） | 真实 Pixiv 370,712 文件完成 discovery 取消/恢复与零写入；Gank/Pawchive 确认成功；Pawchive 又完成公共 Job API 观察下的活动 Hash 取消 | Pixiv/真实盘全量扫描与哈希、HDD/SMB/NAS 取消、publishing/崩溃恢复、正式性能门禁和网络共享盘尚未完成 | 继续其它存储与异常切片，同时优先收口阶段 4 正式压力门禁 |
| 阶段 4：查询与媒体 | 🟡（主线完成，部分参数未冻结） | 🟡（正确性收口完成，500,000 规模正式压力测试已执行） | 搜索、排序、分页、显式规则/有效封面、媒体读取、缩略图及用户/治理 Creator keyset 均有代码闭环；500,000 规模 Correctness/Cursor 通过，可断点续跑的精确全局变化 publication 执行器已具备，并以 100k 实测每 Work 两条 Creator 关系 3/3 | 正式 500,000 十来源 publication 多样本、并发/冷缓存与 Degradation 尚未执行；排序权重、Total、租约和兼容版本策略仍待 API Freeze | 分窗执行正式 500k 性能矩阵并冻结接口 |
| 阶段 5：账户、安全与多客户端 | 🟠（代码与合成安全收尾已实现） | 🟡（Personal 与同机 LAN 安全管理补证，正式 Gate 未通过） | LAN 本地账户、Argon2id、Session、API Token、资源 Grant、匿名 Share 与 WS 防滥用已形成代码闭环并有真实浏览器管理链 | 真实 LAN 多设备、目标设备 Argon2id 与真实恶意输入资源门禁未完成 | 完成外部设备安全门禁 |
| 阶段 6：Web/PWA 界面 | 🟠（页面代码基线与双入口设计重构已实现） | 🟡（隔离 Chromium/Firefox 真实后端 E2E、当前全路由 axe 与模拟强制颜色/400% 等效重排已建立；正式 Gate 未通过） | 同源 Web/PWA 覆盖浏览与管理页面；主要业务/治理链已有双浏览器真实后端持续 E2E；当前 19 条路由及 5 个关键交互状态在桌面、窄屏或 320px 高对比/文本间距组合通过 axe | 浏览器业务链仍使用合成 Source；真实存储浏览器链、其余弱网矩阵、真实移动/触控、人工屏幕阅读器、真实浏览器缩放、物理操作系统高对比及其余交互状态组合未完成 | 扩大真实业务与可访问性门禁，不进入桌面壳 |
| 阶段 7：平台适配与正式发行 | 🟠（Windows 便携、恢复/回滚及首条真实升级边界） | 🟡（本地制品、schema 23→24/反向拒绝、轮换/落位失败 smoke 与包内真实 Win32 双失败门禁通过，正式 Gate 未通过） | 精确干净提交可生成同源双前端 ZIP、三份 SBOM、清单、摘要与签名状态门禁；正常/损坏备份、真实 schema 23→24/反向拒绝、Windows 首次轮换失败、真实候选落位失败后安全回滚，以及真实 Win32 双 sharing-violation 下 fail-closed 均已通过 | 正式签名、安装/更新、更多支持版本跨度、磁盘满/权限/便携进程级双失败与中断、平台矩阵及桌面壳未完成 | 完成 Windows RC 门禁 |

状态图例、每个阶段的详细功能清单、测试与门禁证据，见完整项目状态文档：

- [查看完整项目状态、测试门禁与未完成事项](./PROJECT_STATUS.md)
- [查看工程规范、实施计划、ADR 与验证记录](./Documents/README.md)

## 项目当前处于什么位置

阶段 0～4 的后端主线已经完成代码实现与合成正确性验证（Correctness，即在模拟/构造数据下验证逻辑是否正确，不代表真实规模下的性能表现）。阶段 4 的正式性能与 API Freeze 尚未完成。阶段 5 已增加 Chrome/Edge 同机双上下文、Session 吊销、LAN 模式登录和当前工作站 Argon2id 证据，EV-60 又以隔离 Chromium 覆盖 Personal Token/Share/Session 与独立 loopback LAN 账户/Grant/Session 管理；真实跨设备与目标设备门禁仍缺，完整 Security Gate 未通过。阶段 6 已形成可由 `galleryd` 直接提供的 Web/PWA 页面代码基线；EV-39 发现、EV-40 修复的实时通道与权限阻断不再回归完成度表述，EV-54～EV-59 建立管理自举、publication-bound 画廊/媒体、CustomCover、规则生命周期、无损文本与模板驱动 Schema 表单，EV-60 补齐安全管理链和真实 WebSocket 断线后的 HTTP snapshot 恢复，EV-61 覆盖备份、恢复验证/登记与安全的 GC dry-run，EV-62 再覆盖规则绑定状态、作品人工解绑/撤销与 retry-backoff Job 取消/重试，EV-64 已用同一隔离 AppDirs 实际重启证明 control 恢复生效，EV-65 已让同一完整链在桌面 Firefox 等价通过并进入 CI，EV-66 已关闭显式扫描/Watcher 与维护 Job 终态同步竞态，EV-67～EV-70 又收口全部现有 primitive config 的 Schema 字段、当前草稿调试、按字段撤销与三个任意 JSON 根字段的结构化编辑，EV-71 再关闭真实单帧 sequence gap 的全 snapshot 恢复门禁，EV-72 再覆盖从 UI 取消运行中的 Scan、级联活动 Hash 和禁止半成品 publication，EV-73 又覆盖真实强杀后的启动期立即接管、recovered Attempt 和 UI 治理，EV-74 建立首批治理链，EV-75 再覆盖 SourceWork merge、全部 orphan decision/实体类型与已消费决策冲突，EV-76 又补齐普通 Binding issue 三决定、真实生命周期、双标签页冲突和 51 条分页并修复活动唯一性，EV-77 最后补齐全部五种 SourceWork 结构 action、三种剩余 action 的实际消费与孤儿重现身份语义。这些链路仍使用合成 Source；完整弱网矩阵、真实移动设备和可访问性 Gate 均未完成。EV-103 已生成通过独立 smoke 的未签名 Windows x64 便携测试包，但当前仍没有面向普通用户的正式安装包；真实机械硬盘全量扫描、SMB/NAS、原生平台文件身份、签名和正式发行门禁均尚未完成。

EV-78 已在当前 UI 上关闭窄屏导航焦点陷阱、Escape、焦点返还与响应式显隐缺口；EV-79 又修复 Linux Chromium 暴露的管理端 intrinsic Grid 横向溢出并把 320px 最低宽度纳入回归；EV-80 再修复长离线耗尽预算和 Firefox 丢失 4401 后认证态不收敛，并将连续三轮 offline/online 加入双浏览器真实后端链；EV-81 隔离旧游标通知和取消后迟到的旧分页/详情响应；EV-82 又让同一页面在同 origin 服务长停机并按原端口恢复后持续自愈；EV-83 再锁定一次查询传输中断后的自动重试和迟到旧错误隔离；EV-84 按评审后的共享设计语言重做用户/管理双入口；EV-85 再让真实媒体闸门的 503 背压由生产用户端自动退避恢复；EV-89 打通 Source 作者分页与作品范围继承，EV-91 又把管理端 Job 历史改为授权后的新到旧 keyset 连续加载；EV-95 再把保留 merged/Binding 证据的 Creator 治理读取改成授权后的有界 keyset 页，移除最后一条 Creator 全量响应主链。EV-91 的新增真实后端用例只做了 Chromium/Firefox 定向验证，未重跑完整链。真实手机/平板、触控、人工屏幕阅读器及随机延迟/丢包、带宽、代理等其余弱网矩阵继续列为未完成门禁。

EV-92 首次将正式转换后的 36 primitive Pixiv 规则接入真实 Source：完整只读 guard 记录 370,712 文件、105,202 目录与 562,792,663,280 bytes，45 秒有界 `index` 最终 cancelled，前后 added/removed/modified 均为 0；同时修复长 guard 期间 Watcher 抢跑的 testlab 编排缺陷。该结果只证明真实只读预检和有界取消，不代表全量扫描/哈希/发布或 RC；取消到终态仍有约 71.6 秒延迟，正式全量运行准备和高优先级发布门禁继续推进。

EV-93 已定位并修复上述取消延迟的确定性根因：Scheduler 虽已取消 Scan context，discovery 的 `filepath.WalkDir` 却不感知 context；现在每个回调会在后续 Source 读取前检查取消。Windows scanner、WSL2 race 和根级检查通过，但尚未重跑真实 Pixiv，因此 116,584 ms 仍只是修复前基线，真实取消响应待下一次有界复测。

EV-94 已完成真实补证：首轮 Scan/Attempt 在 45 秒边界同秒 cancelled、无 publication，取消 POST 后 201 ms 即观察终态；随后同 AppDirs 恢复重跑 531.695 秒完整通过，7 findings/0 failures，`bounded-index-scan=45,397 ms`，最终全树 guard 零变化。该结论仅覆盖 Windows 本地 SSD/Pixiv/index discovery，不代表活动 Hash、HDD/SMB/NAS、全量扫描/哈希/发布或 RC Gate。

EV-96 已建立 publication 性能执行器：十 Source 加权语料可精确测量全局 1%/10%/50% 变化，覆盖生产 Store 的完整候选、验证、发布、GC/Checkpoint、历史快照与空间报告。但初版虽声明每 Work 两条 Creator 关系，实际只写入一条；旧 1,000/100,000 Work 数字因此只保留为容量与工具证据。EV-97 已真正实装并核对每 Work 两条关系，增加 fail-closed 原子断点续跑；EV-98 随后以 2 核亲和性完成纠正后 100,000 Work/10 Source 预检，全局三档 3/3、0 failure，每轮实测 200,000 条 WorkCreator 关系，同环境完成报告 `-resume` 退出码 0；EV-99 又将十个权威目标来源代号和实测 `goMaxProcs` 纳入报告/续跑指纹；EV-100 再让通用 Query Reference seed/probe 强制同一 500k/十目标来源/双关系形状，并在计时前验证实际 active publication 与 manifest 一致；EV-101 又把 63 组合 Query 矩阵改为只复用完整成功前缀的原子分窗续跑，并把矩阵、缓存、publication 与实测环境共同封入报告身份。正式 500,000 多样本、并发/冷缓存与 Degradation 仍未执行，因此 Reference Performance/API Freeze Gate 继续保持未通过。

EV-102 部分采纳新增的共享动效语言基座：双入口共用重新选定的四档时序和曲线，用户端同范围查询在新快照到达前保留不可交互旧视觉，并以稳定作品身份完成有预算、可中断且无残留的网格交接；媒体在实际解码后于固定槽位显现，灯箱拖动/捏合保持跟手，管理端只增加局部状态与浮层反馈。当前作品 API 是 keyset cursor，数量还可能只是下限，因此没有伪造精确页码滑轨。Chromium/Firefox mock smoke 20/20 与两浏览器各 21 项隔离真实 `galleryd` 完整链通过，但物理移动设备、人工屏幕阅读器和正式 Web Gate 仍未完成。

EV-113 把当前用户端 10 条、管理端 9 条路由的稳定成功/空/错误状态统一纳入 WCAG 2 A/AA axe；1280×800 与 390×844 两档 viewport 使每个浏览器检查 38 个最终 DOM 状态，Chromium/Firefox 完整 mock smoke 22/22、根级检查 609.3 秒通过。该切片没有修改生产前端，也不替代真实移动/触控、缩放/高对比、交互状态组合或人工屏幕阅读器验证，正式 Web Gate 仍未通过。

EV-116 将这 19 条路由进一步放入应用高对比主题、模拟 `forced-colors`/`prefers-contrast`、WCAG 文本间距和 320×800 viewport 的组合门禁，修复强制颜色主题优先级、Firefox 按钮/链接系统色语义及管理概览 Grid 溢出。双浏览器定向 2/2、完整 mock smoke 24/24、15 文件 212 项 Vitest 和 595.7 秒根级检查通过。320 CSS px 只代表 1280px 在 400% 下的等效重排宽度，不是实际浏览器缩放；forced-colors 也不是物理 Windows High Contrast 验收，Web Gate 仍未通过。

EV-117 在同一组合下补入五个关键交互状态：作品自定义封面选单、维护表单错误、维护确认、Token 表单错误及一次性密文对话框；双浏览器完整 mock smoke 26/26、根级检查 491.3 秒通过。该切片只增强合成前端门禁，不替代真实后端安全写链，也没有穷举全部对话框、权限、加载、分页和网络退化状态，Web Gate 仍未通过。

EV-118 将其中的 Token 校验、真实创建/一次性密文/吊销，以及维护校验/确认接入隔离 Personal `galleryd`，并从用户端、管理端可见按钮各完成一次真实配对。真实操作发现并修复认证壳切换后 React Aria pending live-announcer 短暂失去标签目标的问题；最终 Chromium/Firefox 定向各 1/1、完整 mock smoke 26/26、根级检查 541.3 秒通过。它仍是同机 loopback 与桌面浏览器模拟，不替代物理高对比、真实缩放/触控、人工屏幕阅读器或完整 Web Gate。

EV-114 纠正外部工具测试状态的文档漂移：提交 `2af146a` 已用真实测试进程树证明 1,024-byte 输出溢出与 3 秒超时都会终止父子孙进程，Windows 重复和 WSL2 race 复验通过。生产 Resolver、真实 ffmpeg/恶意媒体容器及 CPU/内存硬限额仍未验证，正式 Security Gate 继续保持未通过。

EV-115 又增加默认关闭、仅接受显式绝对路径的真实 `ffprobe` 门禁：先证明版本输出确实超过 128 bytes，再覆盖 128-byte 溢出、3 秒 loopback 输入超时与纯合成截断 MP4。Windows 5 轮与 WSL2 默认跳过路径 race 通过；生产 ToolDiscovery/版本允许列表没有启用，恶意语料库和 CPU/内存硬限额仍未完成，Security Gate 状态不变。

EV-103 开始阶段 7 的窄发行基线：精确干净提交 `ac92f57` 可构建同源内嵌完整当前用户端/管理端的 Windows x64 便携 ZIP，并生成三个 CycloneDX SBOM、发行清单、包内/外 SHA-256 与实际 Authenticode 状态。12,454,092-byte 本地包通过版本、摘要、SBOM、内嵌 Web 和同 AppDirs 强杀重启 smoke，清单为 `dirty=false`、`unsigned`。它没有安装器、自动更新、CredentialStore、正式签名或真实升级/回滚，不能称为 RC。

EV-104 在上述基线上增加同源双版本标签切换与 control 恢复门禁：两个独立 ZIP 先各自通过完整制品 smoke，旧标签在临时 AppDirs 建立用户事实与备份，新标签承接全部事实、dry-run 校验备份、登记恢复并在同 AppDirs 重启后证明备份后哨兵消失；两个解压程序树运行前后按目录/长度/SHA-256 封印一致，三个服务均优雅停止。精确干净提交 `3ef9acf` 的 `0.1.9-ev104` 与 `0.2.0-ev104` 本地包均为 `dirty=false`、`unsigned` 并通过。两份二进制来自同一源码，这只证明制品编排、程序/数据分离与恢复主路径，不是历史 Schema 升级、降级或失败回滚证据，仍不能称为 RC。

EV-105 将损坏备份失败回滚加入同一真实制品链：正常恢复后再次建立备份与备份后用户事实，登记恢复、优雅停机，再只破坏临时 AppDirs 中的专用备份；下一次 `galleryd` 启动保留当前库和备份后事实，消费 pending 标记并记录失败，程序目录仍零变化。精确干净提交 `61211f2` 的 `0.1.9-ev105` 与 `0.2.0-ev105` 本地包均为 `dirty=false`、`unsigned` 并通过。该切片本身不覆盖历史 Schema、磁盘满、权限失败、原子落位中断、正式签名或 RC。

EV-106 首次使用真实历史提交而非同源标签：从祖先 `60dbdd9` 构建最后一个 schema 23 程序，从当前干净提交 `a063583` 构建 schema 24 程序；旧版本用户事实与备份经当前程序启动迁移后保留，恢复验证明确要求 `WillMigrate=true`。旧程序随后以未知 migration 24 拒绝新库，control 文件字节封印不变，当前程序仍可复启读回迁移前后事实。该结果只覆盖 23→24 与反向 fail-closed，不代表任意历史跨度、磁盘满/权限/中断、正式签名或 RC。

EV-107 在真实便携包中增加 Windows 首次轮换失败门禁：探针仅在临时 AppDirs 以允许读写但拒绝删除/重命名的文件 handle 持有当前 `control.db`；恢复的首次 Rename 必须失败，服务仍以当前库启动，备份后事实保留，pending 被消费并记录准确失败阶段。精确干净提交 `e0dbf61` 的 `0.1.9-ev107`/`0.2.0-ev107` 两包均为 `dirty=false`、`unsigned` 并通过。该结果不覆盖当前库已轮换后的候选落位失败、磁盘满、ACL/断电、正式签名或 RC。

EV-108 修复独立 Windows 规则转换器依赖外部 Go SDK 时区数据的问题，并把真实 Source 按需确认收紧为共享墙钟与取消后 30 秒终态要求。真实 schema v3 的十个平台配置成功转换；Gank 的 3 分钟有界 index 和 12/12 确认、Pawchive 的 2/2 确认均通过，全树 guard 前后零变化。Pawchive 扩到 12 个目标时，取消后父 Scan 仍未在 30 秒内进入终态，报告按失败退出；这暴露了每目标重复全 Source 处理和真实取消响应缺口，不构成完整规则语义、全量扫描、性能 Gate 或 RC。

EV-111 保留上述历史失败并修复其重复执行根因：新增同源批量确认契约，把 1～200 个同 current publication 的唯一未确认媒体原子合并到一个目标化 Scan Job；跨 Source、重复、已确认或历史快照整批拒绝，单媒体 API/用户交互不变。真实 Pawchive 12 目标连续两轮 11 findings/0 failures，最终轮 index 91.083 秒、一个 Job 确认 12/12 用时 74.003 秒，全树 guard 增删改均 0。成功链未触发取消，因此真实活动 Hash 取消与其它存储门禁仍未关闭。

EV-112 随后收口该单本地 SSD 取消切片：新模式默认关闭，且必须先从公共 Job API 观察到同 Source、本轮 `hash/running`，才能取消父 Scan；未观察到 Hash 则清理后失败。全新 Pawchive 隔离运行 13 findings/0 failures，index 91.798 秒，确认阶段 66.889 秒观察活动 Hash，取消后 4 ms 观察父子都为 cancelled，全树 124,660,469,885 bytes 零变化。HDD/SMB/NAS、publishing 临界点、真实存储崩溃恢复与全量 Gate 仍未完成。

EV-109 修复恢复替换中的 fail-open：候选落位失败后若旧库也无法回滚，旧实现仍继续 bootstrap，可能在缺失路径创建空 `control.db`。现在 WAL/SHM 清理、候选落位与回滚错误都进入同一判定；旧库已安全回到原路径时记录失败、消费 pending 并继续使用旧库，连续性未知时则保留轮换副本和 pending、记录完整根因并返回 `RESTORE_FAILED` 阻止启动。确定性 Windows 单元测试、WSL2 race 与根级检查通过；真实 Windows 落位 sharing violation、磁盘满、ACL/断电、正式签名及 RC Gate 仍待验证。

EV-110 将其中“候选落位失败、旧库安全回滚”提升为真实 Windows 便携制品门禁：探针只在系统临时 AppDirs 监视并持有精确 `control.db.incoming`，允许 SQLite 读写但拒绝 Rename。真实 `galleryd` 已轮换当前库后，候选落位收到 sharing violation，旧库回到原路径；服务继续启动，备份后新增 Library 保留，pending 消费、失败阶段精确记录，程序树封印和全部优雅停止通过。纠正探针候选名后的完整链 4/4；落位与回滚双失败、磁盘满、ACL/断电、正式签名及 RC Gate 仍未完成。

EV-119 将“双失败”从确定性 seam 推进到真实 Windows 文件系统：测试同时持有 `control.db.incoming` 与已轮换旧库且均不共享删除，两次生产 `os.Rename` 分别收到 `ERROR_SHARING_VIOLATION`；落位逻辑返回连续性错误，失败处理保留 pending 和两份数据库、记录两个阶段并映射为 `RESTORE_FAILED`。解除 handle 后旧库仍可恢复到原路径。20 次定向、备份包 5 轮和根级完整检查通过；该证据未启动便携 `galleryd`，不替代进程级双失败、磁盘满、ACL/断电、正式签名或 RC。

详细依据见 [PROJECT_STATUS.md](./PROJECT_STATUS.md)。

## 仓库导航

- [Agent 与工程规则](AGENTS.md)
- [完整项目状态](./PROJECT_STATUS.md)
- [工程文档唯一入口](Documents/README.md)
- [v1 实施计划](Documents/指南/01-v1实施计划.md)
- [测试与发布门禁](Documents/指南/02-测试与发布门禁.md)
- [正式测试约定](tests/README.md)
- [贡献指南](CONTRIBUTING.md)
- [安全政策](SECURITY.md)

## 许可证

Gallery 采用 [GNU Affero General Public License v3.0 only](LICENSE) 发布。

SPDX-License-Identifier: AGPL-3.0-only

仓库内直接包含的第三方源码或资产来源见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
