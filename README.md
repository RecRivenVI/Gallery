# Gallery

Gallery（画廊）是一个本地优先、只读来源、规则驱动的个人媒体目录系统。

媒体文件始终留在用户自己的目录里：Gallery 不改名、不移动、不删除原文件，只读取内容并建立一个可以随时删除重建的目录数据库（Catalog）。收藏、阅读进度和人工整理结果单独保存，不会因为重新扫描而丢失。内嵌 Web/PWA、CLI、未来桌面壳和第三方客户端均基于同一套 API 契约。

> [!IMPORTANT]
> Gallery 当前仍处于 pre-alpha 开发阶段。阶段 0～4 后端主线、阶段 5 安全代码基线及阶段 6 Web/PWA 页面代码基线均已实现；Chrome/Edge 已完成真实认证主路径验证，Chromium/Firefox 已完成合成 smoke 与隔离真实后端持续链，但阶段 5 Security Gate 和阶段 6 Web Gate 均未通过。2026-07-23 的独立审计（[验证记录 EV-39](Documents/证据/验证记录.md)）发现实时 WebSocket 与权限名阻断；这些缺陷已由 [EV-40](Documents/证据/验证记录.md) 修复。[EV-54～EV-59](Documents/证据/验证记录.md) 随后以隔离 Chromium/真实 `galleryd` 打通管理自举、publication-bound 画廊/媒体、CustomCover、规则草稿→发布→Binding→扫描、无损规则文本、模板驱动 Schema 表单及规则回滚/弃用/ParameterSet；[EV-60](Documents/证据/验证记录.md) 又补齐 Personal Session/API Token/Share、安全管理写链和真实 WebSocket 断线后的 snapshot 恢复，并覆盖独立 loopback LAN 账户/Grant/Session 管理；[EV-61](Documents/证据/验证记录.md) 覆盖 control 备份、恢复干跑/待重启登记和 Catalog GC dry-run；[EV-62](Documents/证据/验证记录.md) 再覆盖规则绑定暂停/恢复、作品人工解绑/撤销，以及 retry-backoff Job 取消、同 ID 重试和 Attempt 历史；[EV-64](Documents/证据/验证记录.md) 进一步以同一隔离 AppDirs 的实际重启证明 control 恢复生效；[EV-65](Documents/证据/验证记录.md) 把以上 Personal/LAN 完整链扩展到桌面 Firefox 并纳入 CI；[EV-66](Documents/证据/验证记录.md) 再关闭显式扫描与 Watcher 状态脱节、扫描期间事件可能丢失和维护 Job 终态不刷新任务表的竞态；[EV-67～EV-71](Documents/证据/验证记录.md) 又补齐全部现有 primitive config 的 Schema 字段、当前草稿 Dry Run/Explain/Trace、按字段撤销、参数 Schema/tests/extensions 的无损结构化编辑，以及单帧 sequence gap 后的 HTTP 全快照恢复；[EV-72](Documents/证据/验证记录.md) 再从双浏览器 UI 验证运行中 incremental Scan、活动 Hash 子任务、Attempt 与 publication 的取消收敛；[EV-73](Documents/证据/验证记录.md) 继续以真实进程强杀和同 AppDirs 立即重启证明启动期能接管未来租约下的遗留 Attempt，并从 UI 解释和治理 `PROCESS_INTERRUPTED`；[EV-74](Documents/证据/验证记录.md) 建立首批治理链，[EV-75](Documents/证据/验证记录.md) 再覆盖 SourceWork merge、全部 orphan decision/实体类型与已消费决策冲突，[EV-76](Documents/证据/验证记录.md) 又补齐普通 Binding issue 三决定、真实生命周期、双标签页冲突和 51 条分页并修复活动唯一性，[EV-77](Documents/证据/验证记录.md) 最后补齐全部五种 SourceWork 结构 action 的浏览器写路径、三种剩余 action 的实际消费和孤儿重现身份语义。这些仍不等于真实媒体或完整业务闭环。当前没有安装发行版本或完整使用教程，也尚未完成真实全量性能、SMB/NAS、真实 LAN 多设备、目标低端设备、真实移动设备和跨平台发行门禁。
>
> [EV-67](Documents/证据/验证记录.md) 已把 15 类现有 primitive config 从整块 JSON 文本提升为同一权威 Schema 驱动的可视化字段；[EV-68](Documents/证据/验证记录.md) 又把当前草稿的 Dry Run、Explain 与 Trace 接入同一 Chromium/Firefox 真实后端链，并保持请求/响应精确数字无损；[EV-69](Documents/证据/验证记录.md) 再补齐以本地精确基线为准的按字段撤销；[EV-70](Documents/证据/验证记录.md) 继续以快捷区、递归 JSON 树和原始文本编辑参数 Schema、tests 与 extensions；[EV-71](Documents/证据/验证记录.md) 再证明真实单帧丢失形成 sequence gap 时会重取无关的 Library/Source HTTP snapshot；[EV-72](Documents/证据/验证记录.md) 证明从 UI 取消运行中的 Scan 会级联活动 Hash 且不发布半成品；[EV-73](Documents/证据/验证记录.md) 又证明真实强杀后的同 AppDirs 立即重启会保留 recovered Attempt、稳定错误和可见治理入口；[EV-74](Documents/证据/验证记录.md) 建立首批治理链，[EV-75](Documents/证据/验证记录.md) 再补齐 merge、完整 orphan decision/实体类型和已消费冲突，[EV-76](Documents/证据/验证记录.md) 则补齐普通 issue 三决定、真实生命周期、双标签页 sibling 冲突和 51 条游标分页，[EV-77](Documents/证据/验证记录.md) 再补齐三种剩余结构 action 的消费和孤儿重现。未知结构、legacy extension 和精确数字仍可往返，服务端校验/编译保持权威；Web Gate 与 pre-alpha 状态不变。
>
> [EV-78](Documents/证据/验证记录.md) 又为当前正式双入口 UI 增加 42rem 窄屏模态导航，并用 Chromium/Firefox 390×844 smoke 验证桌面/窄屏导航互斥、焦点陷阱、Escape、焦点返还、当前页语义、axe 与无横向溢出。它关闭的是桌面浏览器 viewport 下的窄屏导航焦点缺口，不代表真实移动设备、触控或人工屏幕阅读器 Gate 通过。

> [EV-79](Documents/证据/验证记录.md) 纠正 EV-78 的跨平台溢出证据：精确 `ee69eee` 的 Linux Chromium 在管理端 390×844 暴露 3px 横向溢出，现已通过可收缩 Grid 轨道与长协议标识换行修复，并把 320px 最低宽度加入回归。Windows 双浏览器与 WSL Linux Chromium 多宽度产物探针通过，最终文档 HEAD `dd0343e9f4740d2fcd5f3b7fd9f004c1218cd743` 的 Actions run `30378490971` 全部成功；真实移动/触控与人工屏幕阅读器 Gate 仍未完成。

> [EV-80](Documents/证据/验证记录.md) 修复长时间离线会在约 75 秒后耗尽重连预算，以及 Firefox 丢失 Session 吊销 4401 时认证外壳无法收敛的问题；离线期间暂停退避，恢复网络或在线异常关闭时刷新 bootstrap 与 HTTP snapshot，并用连接代次拒绝迟到旧回调。Chromium/Firefox 各 17 个真实后端测试连续三轮切换 offline/online 并逐轮验证事实收敛；带宽、延迟、丢包、服务长停机与真实移动网络仍属完整弱网门禁缺口。

> [EV-81](Documents/证据/验证记录.md) 修复旧查询的游标错误通知跨搜索残留并阻止新结果续页的问题；查询继续向 OpenAPI 客户端传递取消信号，组件回归锁定旧分页与旧详情迟到后不能覆盖新搜索/路由，Chromium/Firefox 生产资产 smoke 另锁定旧分页迟到返回不进入新结果。该证据使用可控合成延迟，不代表随机延迟/丢包、带宽、代理、服务长停机或移动网络门禁。

> [EV-82](Documents/证据/验证记录.md) 修复浏览器仍在线但 `galleryd` 长时间不可用时 8 次普通重连耗尽后永久停止的问题；普通异常关闭现在以 15 秒封顶持续退避，安全与协议终态不变。Chromium/Firefox 各 18 项真实后端链在同一页面、Session、临时 AppDirs 与 origin 下停止服务，跨过旧预算后按原端口重启，并验证 WebSocket 与 `/api/v1/jobs` snapshot 无刷新恢复。随机延迟/丢包、带宽、代理、移动网络、反复崩溃与真实设备仍未覆盖。

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
| 安装与发行 | 安装包、签名、升级、跨平台发行 | 尚未开始 |

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
| 当前客户端状态 | `galleryd` 同源内嵌响应式 Web/PWA；暂无桌面壳或安装包 |
| 未来客户端方向 | 先关闭无壳 Web 业务与可访问性 Gate，再评估可选薄桌面壳 |

## 当前进度

| 阶段 | 功能总体状态 | 测试/门禁总体状态 | 最重要的已完成能力 | 最大缺口 | 下一步 |
|---|---|---|---|---|---|
| 阶段 0：契约骨架 | ✅ | ✅（限定范围内） | 建立两个数据库、错误码、接口协议等基础设施 | 具体数据库表结构当时未定死（计划内安排） | 已完成 |
| Walking Skeleton | ✅ | ✅（限定范围内） | 用最简单的例子验证整条链路能跑通 | 只验证了单文件的最简单场景 | 已完成 |
| Architecture Proof | ✅ | ✅（限定范围内） | 验证了强制中断后系统能自行恢复 | 数据库最终表结构仍未冻结（计划内安排） | 已完成 |
| 阶段 1：领域和数据所有权 | ✅ | ✅（限定范围内） | 备份/恢复、目录库整体重建、作者合并等已通过验证 | 网络共享盘、底层文件身份识别留待以后阶段 | 已完成 |
| 阶段 2：规则系统 | ✅（正确性层面） | ✅（限定范围内） | 规则生命周期、编译执行、参数/绑定和影响调度已形成闭环 | 正式性能/平台门禁留待后续 | 已完成 |
| 阶段 3：扫描、任务与目录库 | ✅（代码与模拟数据层面） | 🟡（真实大盘抽样通过，全量未完成） | 真实 SSD/HDD 各完成几十万文件规模抽样验收 | 真实全量扫描性能门禁尚未跑完；网络共享盘尚未验证 | 阶段 4 正式压力测试 |
| 阶段 4：查询与媒体 | 🟡（主线完成，部分参数未冻结） | 🟡（正确性收口完成，500,000 规模正式压力测试已执行） | 搜索、排序、分页、显式规则/有效封面、媒体读取、缩略图生成均有代码闭环；500,000 规模 Correctness/Cursor 通过 | 排序权重、结果总数、租约时长等仍是暂定值；500,000 规模下部分查询类别仍有已知未修复的架构性延迟 | 性能优化候选评估与接口冻结 |
| 阶段 5：账户、安全与多客户端 | 🟠（代码与合成安全收尾已实现） | 🟡（Personal 与同机 LAN 安全管理补证，正式 Gate 未通过） | LAN 本地账户、Argon2id、Session、API Token、资源 Grant、匿名 Share 与 WS 防滥用已形成代码闭环并有真实浏览器管理链 | 真实 LAN 多设备、目标设备 Argon2id 与真实恶意输入资源门禁未完成 | 完成外部设备安全门禁 |
| 阶段 6：Web/PWA 界面 | 🟠（页面代码基线与首批真实业务链路已实现） | 🟡（隔离 Chromium/Firefox 真实后端 E2E 已建立；正式 Gate 未通过） | 同源 Web/PWA 覆盖浏览与管理页面；主要业务/治理链已有双浏览器真实后端持续 E2E；EV-78～EV-82 增加窄屏焦点/320px 溢出、长离线/吊销收敛、迟到 HTTP 查询隔离及同 origin 服务长停机恢复门禁 | 真实存储取消与崩溃恢复响应、其余完整弱网矩阵、真实移动/触控设备、人工屏幕阅读器与全页面可访问性未完成 | 扩大真实业务与可访问性门禁，不进入桌面壳 |
| 阶段 7：平台适配与正式发行 | ⏳ | ⛔ | 无 | 安装包、签名、跨平台支持均未开始 | 最后阶段 |

状态图例、每个阶段的详细功能清单、测试与门禁证据，见完整项目状态文档：

- [查看完整项目状态、测试门禁与未完成事项](./PROJECT_STATUS.md)
- [查看工程规范、实施计划、ADR 与验证记录](./Documents/README.md)

## 项目当前处于什么位置

阶段 0～4 的后端主线已经完成代码实现与合成正确性验证（Correctness，即在模拟/构造数据下验证逻辑是否正确，不代表真实规模下的性能表现）。阶段 4 的正式性能与 API Freeze 尚未完成。阶段 5 已增加 Chrome/Edge 同机双上下文、Session 吊销、LAN 模式登录和当前工作站 Argon2id 证据，EV-60 又以隔离 Chromium 覆盖 Personal Token/Share/Session 与独立 loopback LAN 账户/Grant/Session 管理；真实跨设备与目标设备门禁仍缺，完整 Security Gate 未通过。阶段 6 已形成可由 `galleryd` 直接提供的 Web/PWA 页面代码基线；EV-39 发现、EV-40 修复的实时通道与权限阻断不再回归完成度表述，EV-54～EV-59 建立管理自举、publication-bound 画廊/媒体、CustomCover、规则生命周期、无损文本与模板驱动 Schema 表单，EV-60 补齐安全管理链和真实 WebSocket 断线后的 HTTP snapshot 恢复，EV-61 覆盖备份、恢复验证/登记与安全的 GC dry-run，EV-62 再覆盖规则绑定状态、作品人工解绑/撤销与 retry-backoff Job 取消/重试，EV-64 已用同一隔离 AppDirs 实际重启证明 control 恢复生效，EV-65 已让同一完整链在桌面 Firefox 等价通过并进入 CI，EV-66 已关闭显式扫描/Watcher 与维护 Job 终态同步竞态，EV-67～EV-70 又收口全部现有 primitive config 的 Schema 字段、当前草稿调试、按字段撤销与三个任意 JSON 根字段的结构化编辑，EV-71 再关闭真实单帧 sequence gap 的全 snapshot 恢复门禁，EV-72 再覆盖从 UI 取消运行中的 Scan、级联活动 Hash 和禁止半成品 publication，EV-73 又覆盖真实强杀后的启动期立即接管、recovered Attempt 和 UI 治理，EV-74 建立首批治理链，EV-75 再覆盖 SourceWork merge、全部 orphan decision/实体类型与已消费决策冲突，EV-76 又补齐普通 Binding issue 三决定、真实生命周期、双标签页冲突和 51 条分页并修复活动唯一性，EV-77 最后补齐全部五种 SourceWork 结构 action、三种剩余 action 的实际消费与孤儿重现身份语义。这些链路仍使用合成 Source；完整弱网矩阵、真实移动设备和可访问性 Gate 均未完成。当前仍没有面向普通用户的安装包；真实机械硬盘全量扫描、SMB/NAS、原生平台文件身份和正式发行门禁均尚未完成。

EV-78 已在当前 UI 上关闭窄屏导航焦点陷阱、Escape、焦点返还与响应式显隐缺口；EV-79 又修复 Linux Chromium 暴露的管理端 intrinsic Grid 横向溢出并把 320px 最低宽度纳入回归；EV-80 再修复长离线耗尽预算和 Firefox 丢失 4401 后认证态不收敛，并将连续三轮 offline/online 加入双浏览器真实后端链；EV-81 隔离旧游标通知和取消后迟到的旧分页/详情响应；EV-82 又让同一页面在同 origin 服务长停机并按原端口恢复后持续自愈。真实手机/平板、触控、人工屏幕阅读器及随机延迟/丢包、带宽、代理等其余弱网矩阵继续列为未完成门禁。

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
