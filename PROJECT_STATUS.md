# Gallery 项目状态

> 面向一般读者、潜在用户和贡献者的 Gallery 长期项目状态说明。专业名词第一次出现时会用括号简单解释。关键结论都放进表格里，方便按行核对。文中"阶段"（Phase）指工程文档里划分的开发里程碑。

## 项目现状

Gallery 是一个"本地优先"（数据主要放在自己电脑上，不依赖云端）的个人媒体（图片/图集等）管理软件：只读取用户指定的媒体文件夹（叫作 **Source**，只读来源），不改动、不移动、不删除原文件，把从中提取出的信息整理成一个可以随时删掉重建的**目录数据库（Catalog）**，再配合一个独立保存的"用户自己整理的信息"（收藏、备注、封面等，叫 **Overlay**，覆盖层）对外提供浏览、搜索、看图的服务。

本文档说明当前完成了什么、还缺什么。它遵循两条独立的判断标准：**功能状态只依据事实代码判断**，**测试或门禁状态只依据正式的《验证记录》文档判断**——两者不互相替代。

本文档是面向读者的状态汇总，不是规范、架构决策或验证记录本身。如果本文档与 [Documents/README.md](Documents/README.md) 下的权威规范、实施计划、ADR 或验证记录出现冲突，以后者为准，本文档应随之修正。

---

## 状态图例

### 功能实现状态（只看代码事实）

| 图标 | 含义 | 说明 |
|---|---|---|
| ✅ | 正式代码闭环已完成 | 从对外入口（网页请求/命令行）一路能走到底，数据真的存下来了，出错也有规范的报错方式 |
| 🟡 | 主线已实现，但仍有部分数值/边界未最终敲定 | 功能能跑，但里面某些参数（比如排序权重、锁的有效期）文档中标注为暂定值，还未最终定案 |
| 🟠 | 半成品/仅内部可用/缺少对外入口 | 代码写了，但客户端目前无法使用，或者只完成了一半流程 |
| 🧪 | 仅测试/实验/原型存在 | 只在早期实验代码（`Test-Bench`）或测试文件里出现过，正式产品代码里没有 |
| 📄 | 仅计划或规范存在 | 只在文档里写了要做，代码尚未编写 |
| ⏳ | 未动工/未发现实现 | 目前没有证据表明已经开始做 |
| ⛔ | 明确延期或不进入当前版本 | 文档明确写了"这个先不做，以后再说" |
| ❓ | 证据冲突或无法可靠确认 | 文档之间说法不一致，或缺乏足够证据 |

### 测试 / 门禁状态（只看正式验证文档）

| 图标 | 含义 | 说明 |
|---|---|---|
| ✅ | 正式验证记录明确写了"通过"，而且环境、范围、结果都交代清楚 | 真的做过，做的过程和结论都留了案 |
| 🟡 | 只做了有限样本/部分平台/部分正确性验证，不能当成完整门禁 | 做了一部分，文档中也明确说明这不算全量通过 |
| 🧪 | 有测试代码或测试计划，但正式文档没有给出完成结论 | 代码里能看到测试，但没有正式记录"跑过并且通过了" |
| ⏳ | 正式门禁写明"尚未执行" | 文档中明确说明还没做 |
| ⛔ | 延期或不适用 | 属于以后阶段的事情，现在不评估 |
| ❓ | 文档证据缺失、矛盾或无法定位 | 无法确认 |

**几条读者需要记住的规则：**
1. 代码里有测试文件（`_test.go`）只能证明"有测试能力"，不能证明"测试已经做完并通过"。
2. 只有《验证记录》文档里明确写出的结论才能作为"测试完成"的依据。
3. 用模拟数据做的"正确性验证"（Correctness）≠ 真实大规模数据下的性能表现 ≠ 平台真的支持 ≠ 可以对外发布。
4. 在 WSL（Windows 里跑的 Linux 兼容层）里测试的结果 ≠ 在真正的 Linux 系统上测试的结果。
5. 能"交叉编译"出某个平台的程序 ≠ 那个平台已经被正式支持。
6. 有一次真实大规模全量扫描测试被主动叫停，正式记录写的是"取得了部分方向性证据，正式的全量性能门禁没有通过、也没有走完"，不能算失败，也不能算成功。

---

## 总体进度

| 阶段 | 功能总体状态 | 测试/门禁总体状态 | 最重要的已完成能力 | 最大缺口 | 下一步 |
|---|---|---|---|---|---|
| 阶段 0：契约骨架 | ✅ | ✅（限定范围内） | 建立了两个数据库、错误码、接口协议等基础设施 | 具体的数据库表结构当时未定死（属计划内安排，不算缺口） | 已完成，无需再动 |
| Walking Skeleton（最小可跑通链路） | ✅ | ✅（限定范围内） | 用一个最简单的例子（一个作品、一个文件）证明整条链路从头到尾能跑通 | 只验证了单文件的最简单场景 | 已完成 |
| Architecture Proof（架构验证切片） | ✅ | ✅（限定范围内，含 8 个强制中断模拟测试） | 证明了断电、进程被强制终止后系统能自行恢复，不会数据错乱 | 数据库最终表结构、完整接口范围仍未冻结（计划内安排，不算缺口） | 已完成 |
| 阶段 1：领域和数据所有权 | ✅ | ✅（限定范围内） | 备份/恢复、目录库整体重建、作者合并/撤销合并、文件"孤儿"处理等全部完成并通过验证 | 真实网络共享盘（SMB/NAS）、Windows/Linux 底层文件身份识别等留待以后阶段 | 已完成 |
| 阶段 2：规则系统 | ✅（正确性层面） | ✅（限定范围内） | 规则生命周期、编译执行、参数/绑定和影响调度已形成闭环 | 正式性能/平台测试尚未完成 | 已完成 |
| 阶段 3：扫描、任务与目录库 | ✅（代码与模拟数据层面） | 🟡（真实大盘抽样与三平台有界链通过，全量未完成） | SSD/HDD 各完成几十万文件规模抽样；EV-92～EV-94 在真实 Pixiv 370,712 文件/105,202 目录上完成规则接入、45 秒有界 `index`、201 ms 内观察取消终态、同 AppDirs 恢复重跑与全树零写入；EV-108 让 Gank 12/12、Pawchive 2/2 成功，EV-111 再让 Pawchive 12/12 以一个批量 Job 完成且 guard 零变化 | Pixiv/真实盘全量扫描与哈希、活动 Hash/HDD/SMB/NAS 取消、正式性能门禁和网络共享盘尚未完成 | 补齐真实活动 Hash/其它存储取消，同时收口阶段 4 正式压力门禁 |
| 阶段 4：查询与媒体 | 🟡（主线代码完成，部分参数未冻结） | 🟡（正确性已收口；500,000 窄候选已执行，正式矩阵未完成） | 搜索、排序、分页、显式规则/有效封面、媒体读取/下载、缩略图生成全部有代码闭环；EV-51/52 降低搜索与 publication 开销，EV-87/88 用 catalog v20 窄候选收窄聚合封面，EV-89/95 让用户与治理 Creator 入口均严格分页；EV-96～101 建立真实双关系、十目标来源、可续跑 publication、fail-closed Query Reference 入口及 Query 原子分窗续跑，并完成纠正后 100k 容量预检 | 正式 500,000 十来源 publication 多样本及 Query 并发/冷缓存、Degradation 仍未执行；超大合并图、排序权重、Total、租约、兼容版本策略和正式 API Freeze 尚未冻结 | 分窗执行正式 500k 性能矩阵并冻结接口 |
| 阶段 5：账户、安全与多客户端 | 🟠（代码与合成安全收尾已实现；恶意输入缺陷已收口） | 🟡（Personal 与同机 LAN 安全管理补证，正式 Gate 未通过） | EV-37/EV-38/EV-44/EV-48 的安全收口之外，EV-60 已把 Session、API Token、Share、allow/deny Grant、账户停用恢复和精确 Session 吊销接入隔离真实浏览器；EV-86 又让 Creator/Library 聚合封面遵守 deny、Token scope 与独立 `media.read` | 真实 LAN 多设备、目标低端设备 Argon2id、真实恶意资源和外部安全测试门禁仍未完成；同机 loopback 不能替代正式 Security Gate | 完成外部设备与恶意资源门禁 |
| 阶段 6：Web/PWA 界面 | 🟠（前端双入口、设计重构与首批真实业务链路已实现） | 🟡（隔离 Chromium/Firefox 真实 `galleryd` E2E 已建立，正式 Gate 未通过） | 共享设计系统、媒体优先画廊端与紧凑管理端；EV-54～EV-77 覆盖主要业务/治理/恢复，EV-78～EV-85 建立窄屏、弱网、重构及媒体背压，EV-89 补齐 Source 作者分页浏览和范围继承，EV-91 补齐授权 Job 历史分页与连续加载 | 浏览器业务门禁仍使用合成 Source；EV-92 只补到独立 testlab 的真实 Pixiv 有界预检。真实存储浏览器链、其余弱网矩阵、触摸设备与屏幕阅读器未验证；画廊端无 DOM 虚拟化 | 扩大真实后端业务与可访问性 E2E，不进入桌面壳 |
| 阶段 7：平台适配与正式发行 | 🟠（Windows 便携、恢复/回滚及首条真实历史升级边界已建立） | 🟡（本地制品、schema 23→24/反向拒绝、轮换/落位失败 smoke 与连续性 fail-closed 代码门禁通过，正式 Gate 未通过） | EV-103～105 完成便携、同源切换、正常/损坏备份；EV-106 证明真实 schema 23→24、旧程序拒绝新 migration 与当前程序复启；EV-107 证明 Windows 首次轮换被拒绝时保留当前库；EV-109 阻止连续性未知时创建空库；EV-110 再证明真实候选落位失败后安全回滚 | Authenticode、安装/更新、CredentialStore、更多支持版本跨度、磁盘满/真实 OS 落位与回滚双失败/中断、平台矩阵及桌面壳均未完成 | 先完成签名、其余失败回滚和 Windows RC 门禁 |

**概览**：Gallery 已有正式后端和同源内嵌 Web/PWA 代码基线，Chrome/Edge 已验证主要认证与浏览器恢复路径，Chromium/Firefox 已通过隔离真实后端持续链；但真实大规模、真实多平台、真实网络硬盘、完整 Security/Web Gate 和正式发行仍未完成。2026-07-23 的独立审计（[验证记录 EV-39](Documents/证据/验证记录.md)）用真实浏览器探针发现阶段 6 的「业务闭环」此前被高估：实时 WebSocket 通道在真实浏览器中 100% 握手失败，网页端多数写入口因 capability 名不符而不渲染。这些阻断性缺陷已在同日的 [EV-40](Documents/证据/验证记录.md) 修复并经真实 Chrome/Edge 复验；[EV-54～EV-59](Documents/证据/验证记录.md) 随后把管理自举、publication-bound 画廊/媒体、CustomCover、规则生命周期/ParameterSet、无损文本与 Schema 表单接入隔离真实后端持续门禁，[EV-60](Documents/证据/验证记录.md) 再加入安全资源管理、独立 loopback LAN 账户/Grant/Session 和真实 WebSocket 断线 snapshot 恢复，[EV-61](Documents/证据/验证记录.md) 覆盖 control 备份、恢复验证/登记与 Catalog GC dry-run，[EV-62](Documents/证据/验证记录.md) 再覆盖规则绑定状态、作品人工解绑/撤销与 retry-backoff Job 取消/同 ID 重试，[EV-64](Documents/证据/验证记录.md) 又以同一隔离 AppDirs 的实际重启证明 control 恢复生效，[EV-65](Documents/证据/验证记录.md) 再把相同 Personal/LAN 链扩展到桌面 Firefox，[EV-66](Documents/证据/验证记录.md) 随后关闭显式扫描与 Watcher 状态脱节、扫描期间事件可能丢失和维护 Job 终态不触发任务快照刷新的缺口，[EV-67](Documents/证据/验证记录.md) 把 15 类现有 primitive config 接入权威 Schema 可视化字段，[EV-68](Documents/证据/验证记录.md) 打通当前草稿 Dry Run/Explain/Trace，[EV-69](Documents/证据/验证记录.md) 建立以本地精确基线为准的按字段撤销，[EV-70](Documents/证据/验证记录.md) 再补齐参数 Schema、tests、extensions 的无损结构化编辑，[EV-71](Documents/证据/验证记录.md) 覆盖真实单帧 sequence gap，[EV-72](Documents/证据/验证记录.md) 覆盖运行中 Scan/Hash 级联取消，[EV-73](Documents/证据/验证记录.md) 覆盖进程强杀后的启动接管、recovered Attempt 与 UI 治理，[EV-74](Documents/证据/验证记录.md) 建立首批治理链，[EV-75](Documents/证据/验证记录.md) 再覆盖 SourceWork merge、全部 orphan decision/实体类型与已消费决策冲突，[EV-76](Documents/证据/验证记录.md) 又补齐普通 Binding issue 三决定、真实生命周期、双标签页冲突与 51 条分页并修复活动唯一性，[EV-77](Documents/证据/验证记录.md) 最后补齐三种剩余结构 action 的消费、同 AppDirs 重启持久化和 Work/Creator/Media 孤儿重现身份语义。这些链路仍使用合成 Source；真实设备/可访问性仍未完成，阶段 6 Web Gate 依旧未通过。

EV-78/EV-79 随后建立窄屏焦点与跨平台 320px Grid 溢出回归；EV-80 又修复长离线耗尽 8 次重连预算和 Firefox 丢失 4401 后认证态无法收敛，并把连续三轮 offline/online、新 socket、bootstrap/安全快照及断线窗口事实收敛加入 Chromium/Firefox 各 17 项真实后端链。带宽、随机延迟/丢包、服务长停机、真实移动网络和设备仍未覆盖，因此完整弱网与 Web Gate 结论不变。

EV-81 继续修复旧 `CURSOR_INVALID` 通知跨查询残留并阻止新搜索续页的问题；组件级 QueryClient/Router 回归证明取消后迟到的旧分页和旧详情不能进入新搜索/路由，Chromium/Firefox 生产资产 smoke 另以可控延迟锁定旧分页不覆盖新结果。证据不含错误响应乱序、带宽、随机延迟/丢包、代理、服务长停机或移动网络，因此完整弱网与 Web Gate 结论仍不变。

EV-82 又修复浏览器保持 online、但 `galleryd` 长时间不可用时 8 次普通重连耗尽后永久停止自愈的问题。Chromium/Firefox 各 18 项真实后端链在同一页面、Session、临时 AppDirs 与 origin 中停止服务，跨过旧预算后按原端口重启，并验证 WebSocket 与 `/api/v1/jobs` snapshot 无刷新恢复。同 origin 服务长停机切片已关闭；随机延迟/丢包、带宽、代理、移动网络、反复崩溃和真实设备仍未覆盖，Web Gate 结论不变。

EV-83 再把一次作品 GET 传输中断、第二次请求 300 ms 受控延迟及已取消旧搜索的结构化错误迟到交付接入 Chromium/Firefox 真实后端链。两浏览器各 19 项测试均证明网络失败自动重试后显示真实 `galleryd` 数据，旧 `FORBIDDEN` 不覆盖新搜索；现有生产实现无需修改。随机延迟/丢包分布、带宽、代理、移动网络和真实设备仍未覆盖，Web Gate 结论不变。

EV-84 完整评审三份 Legacy 前端设计材料，并在现行 React/OpenAPI/Capability/publication 契约上净室重做共享视觉基座、用户端媒体优先侧栏/抽屉和管理端紧凑控制台/响应式表格；未移植旧代码、旧参数或桌面 bridge，也没有新增无服务端依据的业务。Chromium/Firefox mock smoke 14/14、各 19 项隔离真实后端完整链及根级 202 项 Vitest 均通过。当前证据仍是桌面浏览器 viewport 与合成 Source，真实移动/触控、人工屏幕阅读器和正式 Web Gate 结论不变。

EV-85 在 E2E 专用真实 `galleryd` 中把媒体读取闸门收窄为 1，并在首个请求已经打开合成 Source 句柄后确定性占满名额；生产用户端收到真实 503 `MEDIA_READ_BUSY/retryable=true`，不刷新页面地自动退避并恢复 200 与 4×3 图片解码。Chromium/Firefox 完整链各增加到 20 项且 Source guard 通过。默认 16 名额/5 秒仍为 PRE_FREEZE，真实 HDD/SMB/NAS、大文件/视频 Range 和多客户端争用尚未验证，Web Gate 结论不变。

EV-86 关闭 EV-50 遗留的 Creator/Library 聚合封面逐主体授权缺口：身份仍只需 `library.read`，非空封面候选则在 publication 冻结 Source 成员上批量求 `library.read` 与 `media.read`，并应用 deny 与 Token scope；全局胜出项不可见时从同快照回退到下一条获授权候选，全成员主体继续读取已物化结果。合成 LAN HTTP 回归覆盖列表/详情、媒体缺权、两种 Source deny 与 Source Token scope。正式 500,000 受限重选性能、物理 LAN 和 Security Gate 结论不变。

EV-87 随后增加 catalog v20 Creator/Source 封面窄候选和全局胜出 Source：请求期不再重连 WorkProjection/Creator 关系，小 allowed 走 Source covering index，大 allowed/小 deny 复用仍获授权的全局胜出项并仅对受影响 Creator 沿 rank index 回退。500,000 Work、5,000 Creator、10 Source 的三路径 P95 为 11.7/9.6/130.1 ms；50,000 Creator 高基数诊断为 276.1/285.0 ms/1.32 s。该结果补齐代表性单机测量，同时把高基数响应明确登记为 Degradation/分页化任务；正式十来源完整关系、变化 publication、并发、冷缓存与真实存储矩阵仍缺，Reference Performance Gate 不变。

EV-88 再让 Creator/Library 详情只查单一 scope ID，身份被裁剪的列表只查最终可见 ID，全部身份可见的列表则保留原全量快路径。500,000 Work、5,000 Creator 下单 Creator deny-winner P95 为 1.00 ms，50,000 Creator 高基数下为 0.55 ms；但同轮全量高基数复测 P95 最高 9.31 s，未分页 Creator 列表仍是明确 Degradation。公开 API/DTO 未改变，Reference Performance Gate 结论不变。

EV-89 把用户前端从上述无分页兼容入口切换到授权 keyset 浏览：Source 作者页消费平台 `authorLabel` 和允许的作者排序，按 active Binding/effective merge root 在 `LIMIT` 前授权裁剪，封面不跨平台借用，进入作者后作品查询继续携带同一 Source。control v23 持久化并原子回填 NaturalSortKey v2；100,000 Creator 的 48 行无合并浏览 P95 为 0.621 ms，一个合并图下 P90 43.6 ms、最大 54.0 ms。Chromium/Firefox mock smoke 与各 21 项真实后端完整链均覆盖该入口。当时保留的兼容全量入口已由 EV-95 收口；正式并发/冷缓存/超大合并图与 API Freeze 仍未完成。

EV-95 把无参数 Creator 治理读取改为默认 50、最大 200 的严格 keyset 页，并保持已合并身份、任意状态 Binding 与 `effectiveId` 证据；只带 `limit`/`cursor` 的续页不会误入用户浏览。global/受限授权在 `LIMIT` 前完成，cursor 绑定授权指纹，聚合封面只查询当前页 scope。51 项真实 HTTP/生成客户端回归按 20/20/11 无重无漏收敛，Windows 根级检查与 WSL2 定向 race 通过。该切片关闭一条明确宽查询，不等于整体 API Freeze 或 Reference/Degradation Gate 通过。

EV-96 建立独立 `publication-perf` 执行器：十 Source 加权语料让一次主 Source publication 精确改变全局 1%/10%/50%，每轮沿生产 Store 覆盖 Stage、Overlay、Validate、Publish、GC 与 Checkpoint。但初版虽声明每 Work 两条 Creator 关系，实际只写入一条；因此旧 1,000/100,000 Work 数字只作容量与工具证据，不作正式双关系形状证据。

EV-97 为生产 Catalog Stage 实装多 Creator 关系事实与输入唯一性防御，并让 `publication-perf` 真正写入、校验每 Work 两条关系。执行器现支持基于原子报告的 fail-closed 断点续跑：核对参数、宿主/存储环境、active publication 与全部形状计数，清理遗留 staging candidate，且不重复已记录样本。纠正后 1,000 Work、每档 2 样本预检 6/6 通过；`reference` 仍强制 500,000 Work 且每档至少 20 样本，正式运行尚未执行，Reference/API Freeze 结论不变。

EV-98 以 2/28 核亲和性、`GOMAXPROCS=2`、包并发 1 和 `BelowNormal` 完成纠正后 100,000 Work/10 Source 容量预检：全局 1%/10%/50% 单样本 3/3、0 failure，每轮均核对 200,000 条 WorkCreator 关系、100,000 媒体/FTS 投影和 66,666 Blob/Location。baseline 591.090 秒，三档完整候选 310.799/123.139/120.941 秒，Publish 0.581/0.544/0.000 ms。相同亲和性的完成报告 `-resume` 退出码 0；不同亲和性明确报 `cpuLogicalCores` 漂移。该结果仍是 100k/暖缓存/单样本预检，Reference/API Freeze 结论不变。

EV-99 在正式 500k 长跑前把十个槽位按规范顺序绑定为 Pixiv、Pixiv FANBOX、Gank、Fantia、Patreon、Pawchive、X、微博、微博 Legacy 和 Venera 的非敏感代号，并让环境事实实测 `goMaxProcs`；两者均进入原子报告与续跑指纹。1,000 Work 报告 3/3、0 failure、退出码 0，同指纹续跑退出码 0；只把 `GOMAXPROCS` 从 2 改为 1 时以 `goMaxProcs` 漂移退出码 1。该切片不是 500k 性能结果，Reference/API Freeze 结论不变。

EV-100 把相同正式形状扩展到通用 Query Reference 入口：共享 corpus 固定 500k/10 Source/每 Work 两关系和十目标来源顺序，reference seed 在构建前拒绝降级参数，manifest/report 明示关系与来源身份；probe 在计时前验证完整 manifest，并经真实 HTTP 绑定当前 active publication/Catalog revision，错配 AppRoot 不启动矩阵。预检不进入分位数且纠正 warm 首组合缓存标注，cold-process 仍逐组合重启。该入口已通过 Windows、WSL race 与根级检查，但尚未执行新形状的正式 Query 冷/热矩阵，Reference/API Freeze 结论不变。

EV-101 为 Query 63 组合矩阵增加只复用完整成功前缀的原子分窗续跑：有序组合/次数、单项超时、缓存/warmup、query publication/Catalog revision 与实测环境共同进入报告身份，失败或不完整组合、损坏终态和任一漂移均 fail-closed；整体 Scenario timeout 只定义窗口。隔离 1,000 Work/真实 `galleryd` 从 0/63 恢复到 63/63、0 failure，完成报告再次续跑为 no-op；正式 500k Query 数值仍未执行，Reference/API Freeze 结论不变。

EV-102 完成新增共享动效材料的首轮评审与部分采纳。共享设计系统现按直接操纵、即时反馈、普通状态和结构变化四档职责统一时序/曲线；用户端作品列表只在同一 Source/Library/Creator 范围保留旧快照视觉，等待期退出点击、焦点和无障碍树，新快照按稳定 `work.id` 做有预算的位移、新项显现与不可交互离场副本，所有 WAAPI 在中断或完成后彻底清理；媒体解码交接和灯箱跟手语义同步收口。管理端只继承局部控件、导航、表格状态和浮层节奏，不让高频数据反复入场。当前 cursor/下限 total 无法支持精确页码滑轨，因此明确未采纳；Chromium/Firefox mock smoke 各 10 项及隔离真实 `galleryd` 各 21 项通过，但真实移动/触控、人工屏幕阅读器和正式 Web Gate 仍未完成。

EV-103 开始阶段 7 的窄 Windows x64 便携测试制品基线：SemVer 经 linker 注入两个 `CGO_ENABLED=0` 二进制，完整当前用户端/管理端随 `galleryd` 同源嵌入，两个 Go 与一个 Web CycloneDX SBOM、发行清单、包内/外 SHA-256、实际 Authenticode 状态及 GitHub 来源证明 workflow 已建立。精确干净提交 `ac92f57` 的 12,454,092-byte 本地 ZIP 通过独立版本/摘要/SBOM/内嵌 Web/同 AppDirs 强杀重启 smoke，清单为 `dirty=false`、`unsigned`。该包没有安装器、自动更新、CredentialStore、正式签名或真实升级/回滚，不能称为 RC；远端 workflow 也尚未由当前提交触发。

EV-104 把同一 Windows CI 扩展为两个独立便携 ZIP 的版本切换门禁：每个包先完成 EV-103 的独立制品校验，旧标签建立两条 Library 用户事实并生成 control 备份，新标签在同一 AppDirs 承接事实、完成 compatible/checksum/integrity/invariants dry-run、登记恢复，并在再次重启后证明备份前事实保留、备份后哨兵移除；两个解压程序树运行前后目录/长度/SHA-256 封印一致，三个服务均优雅停止。精确干净提交 `3ef9acf` 的 `0.1.9-ev104` 与 `0.2.0-ev104` 本地 ZIP 均为 `dirty=false`、`unsigned` 并通过。两者来自同一源码，不代表历史 Schema 迁移、降级或损坏备份/磁盘满失败回滚，正式 RC Gate 不变。

EV-105 再把损坏备份失败回滚接入相同的真实便携制品探针：正常恢复后创建第二份备份与备份后 Library，登记恢复、优雅停止并只在临时 AppDirs 内损坏备份；新进程必须继续读到当前库中的备份后事实、消费 pending 标记并记录 `applied=false`。精确干净提交 `61211f2` 的 `0.1.9-ev105`/`0.2.0-ev105` 两包均为 `dirty=false`、`unsigned`，程序树封印和全部优雅停止继续通过。该切片本身不覆盖历史 Schema、磁盘满、权限失败和原子落位中断，正式 RC Gate 不变。

EV-106 建立首条真实历史提交/Schema 运行门禁：脚本从当前 HEAD 的真实祖先 `60dbdd9` 做 detached 本地 clone，以固定 Go 工具链构建最后一个 control schema 23 `galleryd`，并从精确干净实现提交 `a063583` 构建 schema 24 程序。旧程序建立的 Library 与 schema 23 备份被当前程序迁移并保留，恢复 dry-run 精确返回 `WillMigrate=true`；旧程序再打开升级后的库时必须因未知 migration 24 在 descriptor 前退出，control 数据库文件封印前后一致，当前程序随后可复启并读回迁移前后的事实。该切片只覆盖 23→24 与反向 fail-closed，不等于任意历史跨度或完整 Windows RC Gate。

EV-107 将 Windows 文件共享拒绝加入同一真实便携恢复链：探针以 `FILE_SHARE_READ|FILE_SHARE_WRITE`、不含 `FILE_SHARE_DELETE` 的 handle 持有临时 AppDirs 当前库；其它进程仍能读写，但首次 `control.db` Rename 必须收到 sharing violation。`galleryd` 消费 pending、记录 `applied=false` 与精确轮换阶段，继续打开原库并保留备份后 Library；外层程序树封印和全部正常优雅停机继续通过。精确干净提交 `e0dbf61` 的两份未签名包通过；当前库尚未被轮换，因此不构成候选落位失败后的回滚证据。

EV-109 关闭上述恢复替换的代码级 fail-open：旧实现忽略候选落位失败后的旧库回滚错误，bootstrap 仍可能在缺失路径创建空 `control.db`。现在 sidecar 清理、候选落位与回滚失败均保留阶段和根因；确认旧库已回到原路径时记录失败、消费 pending 并继续，连续性未知时则保留 pending 与轮换副本、写入失败事实并返回 `RESTORE_FAILED` 阻止启动。Windows 定向测试、WSL2 race 和根级检查均通过；真实 Windows 落位 sharing violation、磁盘满、ACL/断电仍未取证，因此阶段 7 Gate 不变。

EV-110 将“候选落位失败、旧库安全回滚”接入真实 Windows 便携制品。测试探针在临时 AppDirs 精确等待 `control.db.incoming`，用允许读写但不共享删除/重命名的 handle 持有候选；当前库已轮换后，候选 Rename 收到 sharing violation，旧库回到原路径。真实服务随后正常启动，备份后新增 Library 保留，pending 消费、失败阶段准确记录，程序树封印与优雅停止通过。纠正探针候选名后的完整链 4/4；开发包是同源、`dirty=true`、`unsigned`，不代表真实历史跨度、落位与回滚双失败、磁盘满/断电或 RC。

EV-90 把 `creator.id` 从阶段 4 testlab 的 limitation 改为持续 finding：12 个同名轮换但身份独立的 Creator 必须逐 ID 返回唯一 Work 并完整覆盖，query finding 现为 40 项，既有 6 项 Cursor 与 20 项 media/derived 保持不变。该切片不扩大为真实平台或 500,000 Reference 结论。

EV-91 把 Job 历史从最旧优先的全量读取/N+1 改为 control v24 索引支持的新到旧授权 keyset 分页；严格 cursor 绑定状态、limit 与授权指纹，但不作为权限凭据，每页仍在响应 limit 前逐 Job 重新授权。管理端以 50 项连续加载，续页失败保留已有页面，状态切换丢弃旧 cursor。Chromium/Firefox 新增真实后端定向链各 1/1 通过并进入完整运行器；本轮没有重跑既有 21 项完整链，因此不登记为 22/22 完整链结论。

EV-92 首次把用户提供的 legacy schema v3 规则通过正式转换器接入真实 Pixiv Source：Pixiv 规则含 36 个 primitive，完整只读 guard 记录 370,712 文件、105,202 目录与 562,792,663,280 bytes；45 秒有界 `index` 最终 cancelled，Source 前后零变化。该轮修复长 guard 期间 Watcher 抢先创建系统扫描的 testlab 编排缺陷，并让续跑规则版本不同时 fail-closed。扫描从墙钟触发取消到终态仍约需 71.6 秒；全量扫描/哈希/发布、规则语义和正式性能均未由此完成。

EV-93 随后定位并修复这 71.6 秒延迟的确定性代码根因：Scheduler 已取消 Scan context，但 discovery 的 `filepath.WalkDir` 不感知 context。现在每个遍历回调会在后续 Source 读取前检查 `ctx.Err()`，由既有持久状态机收敛 Job/Attempt；Windows scanner、WSL2 race 和根级检查均通过。尚未重跑真实 Pixiv，因此修复前的 116,584 ms 不能改写成新延迟，真实取消响应 Gate 仍待下一次有界复测。

EV-94 已完成该真实复测：首轮 Scan/Attempt 在 45 秒边界同秒 cancelled、无 publication，取消 POST 后 201 ms 已观察终态；外层 13 分钟超时来自低资源下 6 次全树 guard 尚未全部完成，不是 Scan 卡死。随后同 AppDirs 恢复重跑 531.695 秒完整通过，7 findings/0 failures，`bounded-index-scan=45,397 ms`，final guard 在 370,712 文件/105,202 目录上零变化。该结论关闭当前 Windows/本地 SSD/Pixiv/index discovery 取消补证，不扩大到活动 Hash、HDD/SMB/NAS 或全量 Gate。

EV-108 关闭 EV-36 遗留的 Gank/Pawchive“完全未进入真实 Source”缺口。独立 Windows 规则转换器现在内嵌 IANA 时区数据，真实 schema v3 十平台配置在无 SDK zoneinfo 的便携环境下成功转换。Gank 在 3 分钟扫描边界内完成 index 并确认 12/12；Pawchive 缩小到 2 个目标后确认 2/2；两个完整根的 Source guard 前后均零变化。Pawchive 12 目标运行则在共享墙钟触发取消后 30 秒仍停留 `running/cancelling`，报告按失败退出，说明每目标重复全 Source discovery/规则处理和真实取消响应仍需修复。该结果不是全量扫描、完整规则语义、真实存储取消 Gate 或性能 Gate。

EV-111 保留 EV-108 的失败事实并关闭其重复执行根因：公共 API 现在可把同一 current publication、同一 Source 的 1～200 个唯一未确认媒体原子合并为一个目标化 Scan Job，目标顺序不改变幂等身份，跨 Source/重复/已确认/历史快照整批拒绝；单媒体入口和用户端交互保持不变。真实 Pawchive 12 目标连续两轮通过，最终轮一个 Job 在 74.003 秒确认 12/12，index 91.083 秒，全树 11,595 文件/2,353 目录增删改均 0。该成功链没有触发取消，因此活动 Hash/HDD/SMB/NAS 取消、完整规则语义、全量与性能 Gate 仍未完成。

**2026-07-27 首次真实来源有界验证与安全审计的结论（[EV-47](Documents/证据/验证记录.md)）**：本轮再次证实
「代码闭环」与「真实可用」之间的距离比此前记录的更大——发现的缺陷全部属于「代码存在、测试通过、
但在真实数据或真实攻击面上完全不工作」这一类，且没有一个是本轮新引入的：

- **零写入守护在目录联接根上只遍历一条**，两个数据量最大的平台此前被一份空清单「验证」；空清单与
  空清单自比必然相等，因此校验一直打印通过。
- **十九个缺元数据的目录让一万一千六百六十七个正常作品一个都索引不出来**——单个作品的数据缺陷中断
  整次扫描，且失败信息没有任何线索指向问题类别或目录。
- **规则转换有四项缺陷各自足以让整个来源扫描失败**，其中作者标识被映射成作品外部身份一项影响十个
  平台中的九个。
- **规则系统完全没有按路径段取值的能力**，导致某平台全部作品既没有创作者、标题也只剩章节号。
- 安全侧新发现并修复：登录路径未认证可达的内存放大、规则参数模式可加载外部资源导致任意本地文件
  读取、日志层对普通业务键触发的进程级崩溃；其后 [EV-48](Documents/证据/验证记录.md) 已关闭规则递归
  深度导致的可达进程崩溃，并一并收紧空格式、Windows 路径、Range 与 Cursor 的非规范输入。

有界验证已覆盖十个平台且守护证明零写入（单平台最大覆盖三十七万文件、五百二十一吉字节）；**全量
扫描、续跑证明与正式性能门禁仍未执行**。本轮全部结论均为有限证据，不构成任何 Gate 通过。

---

## 计划演进

> 本节的比较基准是 `origin/main` 唯一可达根提交 `c07cc0be1967e9fdfd4309c4532d284cd946cd54`（2026-07-16「chore(仓库): 建立独立项目基线」）。该提交只有 26 份文本文档与两个净室实验模块，**没有任何正式产品代码**。逐项核对方法与本轮实测证据见 [验证记录 EV-39](Documents/证据/验证记录.md)。
>
> 核对结论：`Documents/规范/01-产品定义与不变量.md` 自根提交起**逐字未变**；10 条不可违反的产品边界、两库数据所有权、Source 永久只读、Canonical/Binding/Overlay 模型、`query_publication_id` 完整快照、capability 授权、阶段 0～7 顺序与 v1 必含/延期表全部保持不变。**没有任何初始目标被静默放弃。** 实质性演进只有下表标注为「替换」「新增」「延期」的条目。

| 初始主题 | 最初计划 | 演进类型 | 当前状态如何变化 | 变化原因/证据 | 当前结论 |
|---|---|---|---|---|---|
| 产品定位与 10 条不变量 | 独立产品，不兼容任何既有 Gallery 实现 | 保持不变 | `规范/01` 自根提交起逐字未变 | — | 保持一致 |
| 首次完整哈希与发布的关系 | 「首次哈希成功前 SourceMedia 保持 staging `hash_pending`，默认阻塞受影响 Source publication」 | **替换** | 改为 `index`/`incremental`/`verify` 三档案与 `located_unverified`/`content_verified` 两态；未确认媒体可进入 publication 与列表，正文读取返回专用 `CONTENT_NOT_VERIFIED`；`hash_pending` 状态在代码中不存在 | 真实 SSD（约 36.6 万文件）/HDD（约 63.2 万文件）抽样实测，全量哈希在真实 HDD 约需 22 小时（EV-25） | 已落地并有抽样证据；`AGENTS.md` 中「超大文件或网络盘只能延迟发布」的旧表述已随之修正 |
| 正式验证规模 | 「100 万和 1000 万样本」 | **延期/降级** | 推荐正式规模下调为 500,000 WorkProjection，`≥1,000,000` 降级为非推荐诊断场景 | 1M/10M 首轮实测：10M 构建可能超 120 分钟、峰值约 91.47 GiB，均不适合作为可重复门禁（EV-35） | 500,000 规模 Correctness/Cursor 已通过；Reference Performance Gate 仍未通过（EV-36） |
| 前端框架与 CSS 组件库 | 明确「尚未冻结，不要仅凭原型替未来实现做决定」 | **新增决策** | ADR-009 接受 React 19 + TypeScript strict + Vite + TanStack Query + React Aria + RJSF/AJV；`规范/02` 新增「阶段 0 工程实现选型」表 | 阶段 6 正式实现需要确定组合 | 已接受，视觉组件样式与细粒度路由仍未冻结 |
| 阶段 2 内置示例来源 | 「将**真实样本中验证过的**三类目录/metadata 形态转为内置示例」 | 细化（净室边界收紧） | 改为「将三类**通用**目录/metadata 形态转为**仓库内嵌合成示例**和黄金测试」 | 净室与只读 Source 边界要求，避免真实样本内容进入仓库 | 已落地，示例位于 `internal/rules/testdata/examples/` |
| SourceWork 拆分/合并 | 初始阶段 1 只写「SourceWork inactive/orphaned 和人工 Binding review」 | **新增** | 增加基于 ContentBlob digest 集合交集的拆分/合并检测、人工结构决策与仅限未消费决策的撤回 | 阶段 1 领域闭环需要（EV-20） | 已落地；`split.bind_existing` 与显式可撤销 CanonicalWork merge 明确延后 |
| Overlay 一致性分类 | 「Overlay 不按字段永久划分一致性类别，planner 按当前查询生成 `overlay_dependency_set`」 | 细化 | 增加静态字段能力注册表 + 按查询和响应资源投影动态生成的 dependency set planner；`PublishedWork.coverMediaId` 使普通作品查询固定包含 CustomCover resource | 阶段 4 Correctness 收口（EV-31、EV-42） | 与初始意图一致，实现补齐 |
| 读己之写屏障 | 规范 04 描述客户端携带 `after_overlay_fact_version` 屏障，服务端只返回覆盖该水位的快照 | **未实现（文档超前）** | 全仓库（Go/OpenAPI/Web）无任何该字段；读己之写目前只能靠客户端等待 WebSocket publication 事件 | EV-39 全文 grep 核对 | 规范该段落描述的是目标态而非当前实现 |
| 搜索技术选型 | ADR-005：v1 用 SQLite FTS5 + 自定义中文分词；Bleve 原型在特定配置下搜不到文件名中缀 | 保持不变 | 结论未变，用真实百万级/千万级数据重新做了性能对比 | 实验数据持续积累，支持原结论 | 结论稳定，但这些测试仍只在早期实验代码（Test-Bench）里执行过 |
| 目录库发布模型 | ADR-003：整体重新生成快照发布 | 保持不变 | 已在正式代码落地并通过强制中断模拟测试 | 增量发布在 50% 变化时需 20.774 秒，整体快照稳定在 3-8 毫秒 | 已固化，阶段 1-3 验证通过 |
| 规则系统技术选型 | ADR-004：有限原语 + 受限 CEL，不允许任意脚本 | 保持不变 | 阶段 2 完成创建、编译、试运行、影响分析、回滚全闭环 | 安全性与可分析性需求未变 | 阶段 2 正确性层面已完成 |
| 桌面壳选型 | ADR-008：Wails 有条件接受，仍可能改为 Tauri | 保持不变 | Web/PWA 已进入正式代码，壳仍是「有条件接受」 | EV-103 的阶段 7 便携测试包仍无壳，不改变选型 | 维持壳可替换、后端独立 |
| 排序与排名细节 | 初始只提出「需要搜索排序」，未给权重 | 新增细化 | 阶段 4 落地字段级 Ranking v2（标题 3 / 作者 2 / 标签 1 / 文件名 0），结构冻结、数值 PRE_FREEZE | 阶段 4 Correctness 收口逐步细化 | 结构已实现，数值待压力测试后冻结 |
| 按需校验单个文件（VerificationTarget） | 初始未提及 | 新增 | 经 EV-30→EV-34 五轮修正，最终以 EV-34 的 publication 冻结结论为准 | 见验证记录 EV-30~EV-34 | 已收口；说明「验证记录标记通过」也需看是否为最新一轮结论 |
| Progress 排序 | 初始计划未单列 | **已实现并统一语义** | 排序协议 v2 增加服务端 `progress_asc`/`progress_desc`、签名 keyset、动态 dependency set 与 Web 控件；排序判据固定使用 publication snapshot，live Overlay 只展示 | EV-49 合成回归 | EV-39 发现的能力表/实现矛盾已关闭；数值性能仍待 Reference Gate |
| 阶段 6 业务闭环 | 「普通浏览器完成所有业务闭环后，才开始壳集成」 | **完成度被高估后已下调，现恢复首批持续切片** | EV-39 实测发现 WebSocket 与 capability 阻断；EV-40 修复，EV-54～EV-77 又以隔离真实后端覆盖管理自举、扫描 publication、WS→HTTP snapshot、publication-bound 画廊/媒体、CustomCover、规则生命周期/ParameterSet/Schema 表单、当前草稿 Dry Run/Explain/Trace、按字段撤销与三个任意 JSON 根字段结构化编辑、安全、维护、规则绑定状态、作品/媒体人工解绑与撤销、普通 Binding issue 三决定/生命周期/分页、全部五种 SourceWork 结构 action、全部 orphan decision/实体类型及重现身份语义、已消费决策冲突、retry-backoff Job、运行中 Scan/Hash 级联取消、进程强杀启动接管与 control 恢复实际重启，并修复显式扫描/Watcher 与维护终态实时失效竞态 | EV-39/40 真实 Chrome/Edge 探针；EV-54～EV-77 Chromium/Firefox 真实 `galleryd` 合成 Source 持续 E2E | 已覆盖规则、安全、维护、扩展治理/任务状态链、运行中取消、强杀启动接管与 control 恢复重启，但完整业务闭环条件仍未满足，不得进入壳集成 |

---

## 架构与模块现状

| 模块 | 说明 | 现状 |
|---|---|---|
| `galleryd`（后端主程序） | 唯一真正运行业务逻辑的独立进程，负责数据处理和对外接口 | ✅ 完整实现，能启动、能自愈（重启后自动恢复未完成的任务） |
| `galleryctl`（命令行工具） | 通过命令行操作 Gallery 的工具 | 🟠 当前只提供 `version`（查看版本）和 `health`（查看健康状态）两个命令，尚未覆盖后端已有的约 70 个接口对应的管理能力 |
| `control.db`（控制数据库） | 存放不能凭空重建的数据：用户的收藏、备注、账号、规则配置等 | ✅ 有完整的备份/恢复机制，验证过删库重建不会丢失用户数据 |
| `catalog.db`（目录数据库） | 存放扫描出来、可以随时重新生成的数据：作品列表、搜索索引、规则/有效封面投影等 | ✅ 支持整体删除后重新扫描重建；v11～v14 增加显式封面、revision 成员、mtime、规则隐藏/角标，v15～v17 增加 Work 标量、聚合封面与排序协议 v2，v18 增加 FTS 同 rowid 搜索窄候选，v19 增加候选验证封印与 Overlay candidate 创建基线，v20 增加 Creator/Source 封面窄候选与胜出 Source |
| 规则系统 | 让用户自定义"什么样的文件夹结构算一个作品"的规则引擎 | ✅ 规则的编写、检查、试运行、影响分析、上线、回滚全部完成 |
| 任务系统（Job/Attempt） | 后台长时间任务（比如扫描、计算哈希）的排队和进度追踪系统 | ✅ 支持取消、重试、断点恢复，有 6 个独立的任务池（一种任务卡住不会影响其他任务） |
| 扫描（Scanner） | 读取用户文件夹、识别文件的模块 | ✅ 支持"索引/增量/校验"三种模式，真实几十万文件规模的抽样测试通过 |
| Catalog publication（目录库发布） | 把扫描结果"生效"的机制，保证不会出现扫描到一半的数据被用户看到 | ✅ 采用整体快照发布，验证过在 8 个不同的强制中断时刻都能正确恢复 |
| Query / 搜索排序 / Overlay（用户覆盖信息） | 提供浏览、搜索、排序、用户自定义信息（收藏/标签/自定义封面）的模块 | 🟡 已支持 publication 冻结的有效封面与 CustomCover 回退，但排序权重、结果总数计算、锁的持续时间等参数仍是暂定值 |
| 媒体与派生资源（DerivedAsset，比如缩略图） | 提供原始文件的分段下载（Range 请求）、缩略图生成 | ✅ 支持标准的按字节范围下载协议，缩略图生成走完整的任务队列流程 |
| 恢复/备份 | 程序崩溃或被强制终止后的自愈机制 | ✅ 已用真实进程强制终止模拟测试过多个关键时间点 |
| API/WebSocket（接口与实时推送） | 对外的网络接口，以及"任务完成了"这类实时通知 | ✅ 100 条 OpenAPI 路径 / 120 条服务端路由，契约完整且有路由集合契约测试；`/ws/v1` 的浏览器握手缺陷已在 EV-40 修复并经真实 Chrome/Edge 验证 |
| 平台适配层 | 让核心逻辑不用关心操作系统差异的隔离层 | 🟡 接口设计已完成，但真实 Windows 文件身份识别（FileID）、Linux 原生文件系统等具体对接尚未完成 |
| Web/PWA 网页界面 | 用户实际会看到、操作的网页界面 | 🟠 已有隔离真实后端下的同快照画廊/媒体、CustomCover、规则、安全、维护和首条治理/任务管理链；完整 Web Gate 未通过 |
| 桌面壳 / 多账户 / 局域网多用户 | 桌面客户端外壳，以及局域网内多人共用时的账号体系 | 🟠 多账户后端与合成安全收尾已实现；桌面壳、真实 LAN 多设备和完整安全门禁未完成 |
| 正式发行（安装包、签名、跨平台） | 面向普通用户的安装、更新、签名流程 | 🟠 已有未签名 Windows x64 便携测试包/SBOM/smoke 基线；正式发行未开始 |

---

## 阶段 0：契约骨架

| 功能项 | 初始计划 | 当前计划 | 事实代码证据 | 功能状态 | 测试/门禁项目 | 文档证据 | 测试状态 | 局限或缺口 |
|---|---|---|---|---|---|---|---|---|
| 领域基本身份/值对象（Library/Source/CanonicalWork/Creator/Media/ContentBlob/FileLocation 等基础概念） | 需要 | 未变 | `internal/domain` | ✅ | 单元/契约测试 | EV-12 | ✅（限定范围） | 只是最小值对象，具体表结构当时未冻结（计划内安排） |
| 两个数据库（`control.db`/`catalog.db`）的迁移框架 | 需要 | 未变 | `internal/storage`、`internal/storage/migrations/{control,catalog}` | ✅ | 迁移版本/校验和测试 | EV-12 | ✅ | 无 |
| 接口协议骨架（OpenAPI、错误码、WebSocket 信封、排序协议、分页游标） | 需要 | 已大幅扩充（阶段 4 加入排序/排名协议 v2） | `internal/contract/{api,fault,query,realtime}` | ✅ | JSON Schema 一致性测试 | EV-12 | ✅ | 无 |
| 规则包 JSON Schema + 编译器版本 + 受限表达式（CEL）规范 | 需要 | 未变 | `internal/rules/rule-package.schema.json` | ✅ | 契约测试 | EV-12 | ✅ | 无 |
| AppDirs（程序自身数据目录）+ 媒体根目录写保护 | 需要 | 未变 | `internal/platform/appdirs` | ✅ | 单实例锁测试 | EV-12 | ✅ | 无 |
| 可注入的时钟/ID/文件系统/进程端口（用于让测试可以模拟各种情况） | 需要 | 未变 | `internal/ports`、`internal/platform/{clock,identity,filesystem,process}` | ✅ | 单元测试 | EV-12 | ✅ | 无 |

---

## Walking Skeleton

| 功能项 | 初始计划 | 当前计划 | 事实代码证据 | 功能状态 | 测试/门禁项目 | 文档证据 | 测试状态 | 局限或缺口 |
|---|---|---|---|---|---|---|---|---|
| 启动 `galleryd`，Personal 模式配对建立会话 | 需要 | 未变 | `internal/bootstrap/run.go`、`internal/auth` | ✅ | 真实启动测试 | EV-13 | ✅（限定范围） | 只验证了最简单场景 |
| 创建 1 个 Library + 1 个 Source，绑定 1 条规则 | 需要 | 未变 | `internal/application`、`internal/rules` | ✅ | 端到端测试 | EV-13 | ✅ | 同上 |
| 扫描 1 个作品/1 个媒体文件，完成首次完整哈希 | 需要 | 未变 | `internal/scanner`、`internal/hashjob` | ✅ | 端到端测试 | EV-13 | ✅ | 只验证单文件，不代表大规模场景 |
| 发布最小目录库快照 + 匹配的用户覆盖信息投影 | 需要 | 未变 | `internal/catalog`、`internal/overlay` | ✅ | 快照一致性测试 | EV-13 | ✅ | 同上 |
| 通过生成的客户端取得作品数据 | 需要 | 未变 | `pkg/galleryapi` | ✅ | 契约测试 | EV-13 | ✅ | 同上 |
| 媒体文件的 HEAD + 分段下载请求 | 需要 | 未变 | `internal/media` | ✅ | 端到端测试 | EV-13 | ✅ | 同上 |
| 通过 WebSocket 收到任务完成通知，并能用 HTTP 快照核对 | 需要 | 未变 | `internal/contract/realtime` | ✅ | 端到端测试 | EV-13 | ✅ | 同上 |
| 客户端不直接访问数据库或后端内部代码 | 需要 | 未变 | `pkg/galleryapi` 与 `internal/*` 的边界 | ✅ | 边界检查测试（`cmd/galleryctl/boundary_test.go`） | EV-13 | ✅ | 无 |

---

## Architecture Proof

| 功能项 | 初始计划 | 当前计划 | 事实代码证据 | 功能状态 | 测试/门禁项目 | 文档证据 | 测试状态 | 局限或缺口 |
|---|---|---|---|---|---|---|---|---|
| 规则试运行/编译产出 Trace（追踪）与 Impact（影响分析） | 需要 | 未变 | `internal/rules` | ✅ | 端到端测试 | EV-14 | ✅（限定范围） | 无 |
| 分批构建目录库暂存版本，短事务发布 | 需要 | 未变 | `internal/catalog`、`internal/scanner/service.go` | ✅ | 端到端测试 | EV-14 | ✅ | 无 |
| 浏览/过滤/搜索/排序，快照分页 | 需要 | 阶段 4 大幅扩充 | `internal/query` | ✅（阶段 4 之前的最小版本） | 端到端测试 | EV-14 | ✅ | 详见阶段 4 |
| 设置标题覆盖+收藏，等待覆盖层发布；删除重建 `catalog.db`；验证用户数据不丢失 | 需要 | 未变 | `internal/overlay`、`internal/backup` | ✅ | 端到端测试 | EV-14 | ✅ | 无 |
| 8 个关键时间点强制终止进程测试（例如正在写一半时被终止、刚发布完但尚未通知完成时被终止等） | 需要 | 未变 | `internal/recovery/killpoints_test.go` | ✅ | 真实子进程强制终止测试 | EV-14 | ✅ | 只覆盖这 8 个场景，不代表全部可能情况 |
| 最终物理数据库表结构 | 计划中本就不在这一阶段冻结 | 未变 | — | 📄（计划内暂缓） | — | 计划文档 | ⛔（阶段计划本就不要求） | 不算缺口 |

---

## 阶段 1：领域和数据所有权

| 功能项 | 初始计划 | 当前计划 | 事实代码证据 | 功能状态 | 测试/门禁项目 | 文档证据 | 测试状态 | 局限或缺口 |
|---|---|---|---|---|---|---|---|---|
| 作者（CanonicalCreator）合并与撤销合并 | 需要 | 未变 | `internal/creators` | ✅ | 单元+端到端测试 | EV-15 | ✅ | 无 |
| Binding（作品与文件的绑定关系）问题发现、人工修复 | 需要 | 未变 | `internal/application/bindings.go` | ✅ | 单元测试 | EV-16 | ✅ | 无 |
| "孤儿"文件（找不到对应规则/来源）保留窗口 + 人工复核 | 需要 | 未变 | `internal/application/orphans.go` | ✅ | 单元测试 | EV-17 | ✅ | 保留窗口的具体次数（3 次）是可调常量，未最终冻结 |
| `control.db` 备份/恢复 + 目录库整体重建恢复 | 需要 | 未变 | `internal/backup` | ✅ | 端到端恢复测试 | EV-19 | ✅ | 无 |
| 作品拆分/合并检测 + 人工决定 + 有限撤销 | 需要 | 未变 | `internal/application/structure.go` | ✅ | 单元测试 | EV-20 | ✅ | "绑定已有对象"这个分支明确延后 |
| 领域 Schema 冻结门禁（Schema Freeze Gate） | 需要在阶段 1 完成 | 未变 | `internal/storage/migrations/control/00016_schema_freeze_phase1.sql` | ✅ | 唯一性约束等固化测试 | EV-20 | ✅ | 文件唯一标识在网络共享盘环境下的最终约束仍未冻结（不阻塞阶段 1 收尾） |
| 真实 Windows 文件 ID / Linux `dev+inode` 文件身份识别 | 需要，计划标注为后续阶段 | 未变，延后到阶段 7 | 平台端口已预留接口，未接入真实实现 | 🟠 | — | 验证记录 EV-29 | ⛔ 延后 | 明确延期到阶段 7 |

---

## 阶段 2：规则系统

| 功能项 | 初始计划 | 当前计划 | 事实代码证据 | 功能状态 | 测试/门禁项目 | 文档证据 | 测试状态 | 局限或缺口 |
|---|---|---|---|---|---|---|---|---|
| 规则包（RulePackage）草稿/发布/版本/参数/回滚全生命周期 | 需要 | 未变 | `internal/application/rules_lifecycle.go` | ✅ | 单元+端到端测试 | EV-22 | ✅（正确性层面） | 无 |
| 规则解释（Explain）、追踪（Trace）、差异对比（Diff）、影响分析（Impact） | 需要 | 未变 | `internal/rules` | ✅ | 单元测试 | EV-22 | ✅ | 无 |
| 3 类内置示例规则（对应 3 种真实文件夹结构） | 需要 | 未变 | `internal/rules/testdata/examples/*.json`、`internal/rules/examples.go` | ✅ | 黄金样例测试 | EV-02、EV-22 | ✅ | 无 |
| 同一 Source 只能有一条生效规则绑定（SourceRuleBinding） | 需要 | 未变，多规则链明确延后 | `internal/application/bindings.go` | ✅ | 单元测试 | EV-22 | 🟡 | 属于"兼容演进基线"，非最终冻结；多规则链/按来源路由被明确延后 |
| 规则改动后自动触发重新扫描/重新投影的调度联动 | 计划中提及 | 尚未实现 | 未找到对应生产代码 | ⏳ | — | 验证记录：明确列为延后 | ⛔ | 属实的功能缺口 |
| 真实规模下的规则性能/多语言/平台测试 | 计划中在后续阶段 | 未变，延后 | — | ⛔ | — | — | ⛔ | 明确延后 |

---

## 阶段 3：扫描、任务和 Catalog

| 功能项 | 初始计划 | 当前计划 | 事实代码证据 | 功能状态 | 测试/门禁项目 | 文档证据 | 测试状态 | 局限或缺口 |
|---|---|---|---|---|---|---|---|---|
| 持久化任务（Job）+ 尝试（Attempt）+ 心跳租约 + 取消/重试 | 需要 | 未变 | `internal/jobs`，迁移 `00018/00019` | ✅ | 单元/状态机高重复/race | EV-23/24/53 | ✅（合成数据层面） | 真实 HDD/SMB/NAS 取消响应仍待 Degradation Gate |
| 6 个独立有界任务池（扫描/哈希/覆盖投影/派生资源/外部工具/维护） | 需要 | 未变 | `internal/bootstrap/run.go` 调度器注册 | ✅ | 单元测试 | EV-23 | ✅ | 无 |
| Watcher（文件变化监听）只作提示，真正以周期性重新核对为准 | 需要 | 未变 | `internal/watcher` | ✅ | 单元测试 | EV-24/27 | ✅ | 无 |
| 目录库维护：GC（垃圾回收）、检查点、VACUUM（整理数据库文件）、磁盘空间预检查 | 需要 | 未变 | `internal/maintenance` | ✅ | 单元测试 | EV-23 | ✅ | 无 |
| 真实 SSD/HDD 大规模抽样验收 | 需要真实规模性能门禁 | 拆分为"抽样验收"+"正式性能门禁"两步，仅完成第一步 | 通过真实 `galleryd` 对约 36.6 万文件（SSD）/63.2 万文件（HDD）做有界抽样 | ✅（抽样范围内） | 真实环境实测 | EV-25，明确写"全量扫描未完成，正式全量性能 Gate 仍未通过" | 🟡 | 不能把抽样结果当作真实全量场景的性能保证 |
| Pixiv 真实 Source 只读有界预检 | 全量扫描前置 | 新增独立低优先级预检；只做规则转换、注册、45 秒 `index` 取消、恢复重跑与全树 guard | `tools/testlab/cmd/{rulesimport,probe}`、`tools/testlab/stages/sourcelab`、`internal/scanner` | ✅（有界预检范围） | 真实 SSD Source、独立 AppDirs、动态 loopback、低资源运行 | EV-92～EV-94 | ✅（本地 SSD/index discovery 有界范围） | 370,712 文件/105,202 目录前后零变化；取消 POST 后 201 ms 观察终态；未完成全量扫描/哈希/发布及其它存储类型 |
| Gank/Pawchive 真实规则与 Source 有界验证 | EV-36 遗留两个未命中槽位 | 直接登记完整只读根；census、扫描与确认分别有界；同 Source 多目标以一个批量 Job 共享一次 discovery，确认墙钟到界后仍须公共 API 取消并等待 30 秒终态 | `internal/rules`、`internal/transport/httpapi`、`tools/testlab/cmd/rulesimport`、`tools/testlab/stages/sourcelab` | 🟡（三条目标规模成功链，完整 Gate 未过） | 真实 legacy schema v3、Windows 本地 SSD、独立 AppDirs、动态 loopback、全树 guard | EV-108、EV-111 | Gank 12/12、Pawchive 2/2 与批量 12/12 均通过 | 关闭“完全未验证”和逐目标重复处理；成功链未触发活动 Hash 取消，未覆盖完整语义、全量或其它存储 |
| 扫描档案（`index`/`incremental`/`verify`） | 计划外新增（真实测试中发现需要） | 已实现并设为默认使用 `incremental` | `internal/scanner` | ✅ | 单元+真实抽样测试 | EV-25/26 | ✅（抽样范围内） | 默认选择逻辑本身仍标注"有条件接受" |
| 目录库发布与恢复模型收尾（含 6 项条件全部满足） | 需要 | 未变 | `internal/catalog` | ✅ | 单元+强制中断模拟测试 | EV-28："阶段 3 至此在...六项条件全部满足后正式收口" | ✅ | 无 |
| 真实网络共享盘（SMB/NAS/UNC）扫描行为 | 计划中列为需要验证 | 未变 | 未发现相关正式代码或测试 | ⏳ | — | 明确列为"仍需重测的关键门禁" | ⏳ | 尚未验证 |

---

## 阶段 4：查询和媒体

| 功能项 | 初始计划 | 当前计划 | 事实代码证据 | 功能状态 | 测试/门禁项目 | 文档证据 | 测试状态 | 局限或缺口 |
|---|---|---|---|---|---|---|---|---|
| 结构化过滤（11 个字段）+ 逻辑组合（与/或/非） | 需要 | 未变 | `internal/query` | ✅ | 单元测试 | EV-30 | ✅（正确性层面） | AND/OR 的"规范化排序"仍标注 PRE_FREEZE（暂定） |
| 全文搜索（中文双字分词 + 拉丁文三字分词 + 原文复核） | 需要 | 未变 | `internal/query`、`internal/querytext` | ✅ | 黄金样例测试 | ADR-005、EV-05、EV-36 | 🟡 | 500,000 规模（推荐正式验证规模）已在正式测试框架 `tools/testlab` 中执行且 Correctness 通过（EV-36）；≥1,000,000 规模仍只在早期实验代码（Test-Bench）与已归档的一次性首轮实测（EV-35）中执行过，未进入标准自动化测试 |
| 排序与排名（Ranking v2：标题/作者/标签/文件名按优先级加权） | 需要 | 结构已细化 | `internal/query` | ✅（结构） | 单元测试 | EV-30/31 | 🟡 | 具体权重数字标注为暂定值（PRE_FREEZE），需正式压力测试后最终确定 |
| 结果总数三态语义（精确/下限/不计算） | 计划外细化 | 新增 | `internal/query` | ✅（结构） | 单元测试 | EV-30 | 🟡 | 具体"预算"数值同样是暂定值 |
| 签名分页游标（防篡改、绑定发布版本） | 需要 | 已扩充（新增排名协议版本字段） | `internal/contract/query/cursor.schema.json` | ✅ | 契约测试（含篡改/过期测试） | EV-30/31 | ✅ | 游标的有效期（5 分钟）是暂定值 |
| 覆盖层（Overlay）字段能力注册表 + 按查询动态计算依赖 | 需要 | 从"全局静态划分"改为"按查询动态计算"（真实的设计修正） | `internal/overlay` | ✅ | 单元测试 | EV-31 | ✅ | 无 |
| 规则封面、CustomCover 与 Work 快照封面 | 需要 | `CoverPath` 映射稳定 SourceMedia/CanonicalMedia；显式规则/有效封面列；CustomCover 优先、失效保留并回退；`PublishedWork.coverMediaId` required nullable；Work 详情接受 `queryPublicationId` | `internal/rules`、`internal/scanner`、`internal/catalog`、`internal/query`、`internal/transport/httpapi` | ✅ | 8 包定向 Go + 合成 v10→v11 migration | EV-42 | ✅（合成范围） | 未使用真实 Source/媒体，未做真实规模或 API Freeze 验证 |
| Creator/Source/Library 三级聚合封面 | 需要 | Creator 全局选择；Source 严格 Source-local；Library 复用 Source；每个 Creator/Source 持久一条 publication 窄候选；详情/裁剪列表限定最终 scope | `internal/catalog/aggregate_cover.go`、`internal/transport/httpapi/server.go`、catalog `00020` | ✅（合成 Correctness） | 跨 Source/授权/定向 scope 行为、生产 SQL 查询计划、迁移、opt-in 500k 参考性能 | EV-50、EV-86～EV-88 | 🟡 | 500,000 Work 单 Creator 回退 P95 0.55～1.00 ms；50,000 Creator 全量列表复测最坏 P95 9.31 s，完整十来源变化 publication/并发/Degradation 矩阵仍未完成 |
| Creator 用户/治理分页、授权与平台范围 | 需要 | 用户模式按 active Source/effective root、NaturalSortKey v2 分页；治理模式保留 base/merged/任意状态 Binding 证据但同样严格分页；同名身份不按名称去重 | `internal/creators/list.go`、`internal/transport/httpapi/server.go`、`web/src/gallery/pages/discover.tsx`、control `00023` | ✅（代码闭环） | keyset/cursor/auth/merge/查询计划、100k opt-in、51 项治理 HTTP 连续加载、双浏览器用户浏览链 | EV-89、EV-95 | 🟡 | 两种入口均已拒绝无界响应；正式并发/冷缓存/超大合并图、兼容版本策略与 API Freeze 未完成 |
| Work 聚合查询逐 Source 授权与 cursor 绑定 | EV-39 缺陷收口 | 保留 global/显式范围入口授权；按 publication 成员求 `library.read`，hidden 再求 `library.write`；授权 SQL 先于 total/ranking/keyset/limit | `internal/auth`、`internal/query`、`internal/transport/httpapi`、catalog `00012` | ✅（合成 Correctness） | 标量/批量差分、HTTP deny/Token scope、cursor、查询计划、migration/发布完整性 | EV-44 | ✅（合成范围） | 不代表真实 LAN、其他列表端点或正式性能/API Freeze |
| 媒体文件按需校验单个文件（VerificationTarget） | 计划外新增 | 新增，经历 5 轮修正（EV-30→34） | `internal/transport/httpapi/server.go` | ✅（以最新一轮结论为准） | 单元+回归测试 | EV-34 明确说明这是最新、最终生效的结论，此前 EV-33 的判断被更正 | ✅（限定为压力测试前的正确性） | 只证明压力测试前的正确性已收口，不代表压力测试已完成或参数已冻结 |
| 派生资源（缩略图）公开接口 + 真实 JPEG 缩略图生成闭环 | 需要 | 未变 | `internal/derived`、`internal/derived/thumbnail`、`internal/derivedjob` | ✅ | 端到端测试 | EV-30/32/33 | ✅ | 曾发现重试次数上限设为 0 导致重试不生效的问题，已修复 |
| 正式 API 冻结（API Freeze Gate） | 需要在阶段 4/5 完成 | 阶段 5 新资源已兼容扩展；EV-95 已移除 Creator 无界响应，整体仍未冻结 | `internal/contract/api/openapi.yaml` 当前为 `0.6.0-pre-alpha` | 🟡 | OpenAPI 生成一致性、Creator 51 项治理分页通过 | EV-37、EV-95；仍列为关键门禁 | ⏳ | 阶段 4 PRE_FREEZE 数值、性能、兼容版本策略与阶段 5 真实 LAN 门禁未完成，不能冻结 |
| 正式规模搜索排序压力测试（推荐规模 500,000，见测试与发布门禁「正式验证规模分级」） | 需要 | 推荐规模从"百万/千万级"调整为 500,000（EV-35），≥1,000,000 降级为非推荐诊断场景 | `tools/testlab` | ✅（500,000 规模） | 单元+端到端（`tools/testlab/stages/stage4`） | EV-35、EV-36 | 🟡 | 500,000 规模 Correctness/Cursor 通过，Perf 矩阵在预算内完成但 `wide-cjk`/`structured-and`/`structured-or` 类别仍显示已知架构性延迟（秒级，未修复，见 EV-36）；≥1,000,000 规模的历史结果已归档（EV-35），不代表标准 Gate 已通过 |

---

## 阶段 5：账户、安全和多客户端

| 功能项 | 初始计划 | 当前计划 | 事实代码证据 | 功能状态 | 测试/门禁项目 | 文档证据 | 测试状态 | 局限或缺口 |
|---|---|---|---|---|---|---|---|---|
| Personal 模式安全细节（Host/Origin/Fetch Metadata/CSRF、Cookie、Session/WS 吊销） | 需要 | 已扩展为统一 Principal/Session 模型 | `internal/auth`、`internal/transport/httpapi`、`internal/contract/realtime` | ✅（代码主线） | 单元+HTTP/WS 集成+重复/race；隔离 Chromium 双 Session、Token/Share/Session 与断线恢复 | EV-37、EV-60 | 🟡 | EV-60 已有真实 Personal 浏览器链；真实多设备、跨浏览器与恶意资源门禁未验证 |
| 局域网（LAN）模式：本地账号、Argon2id、API Token、资源授权 | 需要 | Role 只作 capability 上限，allow/deny Grant 与 Token scope 服务端求交；Job/Creator/Binding/RuleBinding/Query/Media/Derived/管理面逐资源判定；Work 聚合查询在 total/分页前逐成员授权 | `00020_phase5_security.sql`、`internal/auth/*`、`internal/transport/httpapi/*`、OpenAPI 0.6、catalog `00012` | ✅（代码与合成矩阵） | migration、并发初始化、跨 Library/Source 隔离、列表过滤、Token 生命周期、批量/标量授权差分；独立 loopback LAN Chromium 管理链 | EV-37、EV-44、EV-60 | 🟡 | EV-60 仍是同机动态 loopback，不是物理 LAN 多设备；Argon2id/过期/限流数值 PRE_FREEZE |
| 分享（Share）范围/过期/撤销 | 需要 | credential 只显示一次、摘要存储；Work/Media 安全 DTO 与媒体 GET/HEAD；Media 可选固定 Blob，生命周期内保护 GC | `internal/auth/shares.go`、`internal/transport/httpapi/server.go`、OpenAPI `/api/v1/shares*` | ✅（代码与合成闭环） | 生命周期/过期/恢复吊销、Range/下载权限/越界隐藏、固定 Blob GC | EV-37、EV-60 | 🟡 | EV-60 已有 Personal/Chromium 创建、匿名读取与吊销链；LAN 跨设备消费和正式发布门禁仍未完成 |
| 威胁模型测试（路径攻击、恶意元数据、恶意媒体文件、限流） | 需要 | 认证爆破、Host/Origin/CSRF/Content-Type、WS 防滥用，以及路径/metadata/媒体正文/恢复输入均进入正式合成测试 | `internal/auth/security_test.go`、`internal/transport/httpapi/security_api_test.go`、`internal/contract/realtime/envelope_test.go`、`tools/testlab/stages/stage5/security` | ✅（合成攻击矩阵） | 合成安全回归 + 高重复/race 门禁 | EV-09、EV-37 | 🟡 | 合成输入不代表真实恶意容器/外部工具沙箱或真实 LAN；整体 Security Gate 未通过 |

---

## 阶段 6：Web/PWA

| 功能项 | 初始计划 | 当前计划 | 事实代码证据 | 功能状态 | 测试/门禁项目 | 文档证据 | 测试状态 | 局限或缺口 |
|---|---|---|---|---|---|---|---|---|
| 浏览/搜索/作品/作者/媒体、Overlay、任务、规则、安全与维护界面 | 需要 | 以 OpenAPI/HTTP snapshot 为事实源，同源嵌入 `galleryd`；列表/详情/封面/媒体沿用同一 publication；Source 作者页保持平台文案、排序与作品范围；Job 历史按授权 keyset 连续加载 | `web/src/gallery/pages/*`、`web/src/gallery/components/*`、`web/src/manage/pages/*`、`web/src/manage/rules/*`、`web/src/api/*`、`internal/webapp` | 🟠（EV-54～EV-77 的真实后端业务/治理链及 EV-78～EV-91 的窄屏/弱网/设计/媒体背压/作者与 Job 分页切片已建立） | Vitest、Playwright mock、真实后端 Chrome/Edge Personal/LAN 与 Chromium/Firefox 完整链 | EV-38～EV-40、EV-42、EV-54～EV-91、ADR-009 | 🟡 | 主要合成业务闭环已进入 Chromium/Firefox 真实后端 E2E；EV-91 新 Job 用例只做双浏览器定向验证，尚未重跑完整链；真实移动设备、人工屏幕阅读器、跨设备 LAN 与其余弱网矩阵仍未覆盖 |
| 实时通道与 HTTP 查询恢复 | 需要 | WS 只作提示，断线/gap 以 HTTP snapshot 恢复；旧 HTTP 查询不得覆盖新路由/搜索/分页 | `web/src/shared/realtime.tsx`、`web/src/gallery/queries.ts`、`internal/contract/realtime` | ✅（EV-40 修复 `WS-1`/`WS-2`，EV-71 补齐单帧 gap，EV-80 修复长离线/close code 丢失，EV-81/EV-83 隔离迟到成功/错误查询，EV-82 保持长停机自愈） | 真实 Chrome/Edge 连接/事件/CSP；隔离 Chromium/Firefox 真实断线/gap/offline/服务长停机；组件、生产资产和真实后端受控传输退化 | EV-39、EV-40、EV-60、EV-71、EV-80～EV-83 | 🟡 | 已覆盖一次断线、单帧 gap、长离线预算暂停、连续网络切换、Firefox 1006 吊销、成功/错误响应迟到、一次 GET 中断恢复及同 origin 原端口服务长停机；带宽、随机延迟/丢包分布、代理、移动网络和反复崩溃仍未覆盖 |
| 无障碍访问（键盘、屏幕阅读器等） | 需要 | React Aria + 语义 token、reduced motion、焦点与触控样式 | `web/src/design/*`、`web/src/gallery/components/chrome.tsx`、`web/src/manage/layout.tsx`、`web/e2e/gallery.spec.ts` | 🟠（EV-78 已关闭当前 UI 的窄屏导航焦点边界；EV-79 修复 Linux Chromium intrinsic Grid 溢出） | Chromium/Firefox Playwright + axe：桌面双入口、390×844 双模态导航及 320×844 最低宽度，`color-contrast` 保持启用 | EV-38～EV-40、EV-78、EV-79 | 🟡 | 已覆盖常驻/模态导航互斥、正反向 Tab、Escape、焦点返还、路由关闭、`aria-current` 与诊断式横向溢出回归；仍缺全页面审计、人工屏幕阅读器、真实触控/虚拟键盘/安全区和缩放门禁 |

---

## 阶段 7：平台与发行

| 功能项 | 初始计划 | 当前计划 | 事实代码证据 | 功能状态 | 测试/门禁项目 | 文档证据 | 测试状态 | 局限或缺口 |
|---|---|---|---|---|---|---|---|---|
| Windows 正式支持（真实运行、升级、崩溃恢复、签名） | 需要 | 增加无壳便携测试制品、恢复/回滚及首条真实历史升级边界作为 RC 前基线 | `pkg/galleryversion`、Windows 发行脚本、`tools/testlab/cmd/{portable,historical}-upgrade`、Windows CI | 🟠 | 精确干净提交构建、版本/摘要/SBOM/签名事实、同 AppDirs 强杀重启、正常/损坏备份、真实 schema 23→24/反向拒绝、Windows 首次轮换失败保留当前库、真实候选落位失败后安全回滚、落位/回滚连续性 fail-closed | EV-103～EV-107、EV-109、EV-110 | 🟡（本地制品、恢复、单条历史升级、轮换/落位失败 smoke 及代码级连续性门禁通过） | 仍未完成正式签名、安装/更新、更多支持版本跨度、磁盘满/真实 OS 落位与回滚双失败/中断、低权限/多用户与完整 Windows 平台门禁 |
| Linux 原生（ext4 文件系统）核心支持 | 需要 | 未变 | 目前只在 WSL（Windows 内的 Linux 兼容层）和 GitHub Actions 的 `ubuntu-latest` 里测试 | 🟠 | CI 每次提交自动执行 | 文档明确写"WSL DrvFS ≠ 原生 Linux ext4" | 🟡（不等同于原生支持） | 尚未在原生 Linux 系统上验证过文件身份等底层行为 |
| macOS、Docker 支持 | 需要 | 未变 | 未发现任何代码或测试 | ⏳ | — | — | ⛔ | 尚未开始 |
| 桌面壳（Wails/Tauri）最终选型 | 需要 | 仍是"有条件接受" Wails，早期做过一个极简壳子原型 | `Test-Bench/cleanroom-lab/deploy/wails-shell`（原型） | 🧪（仅原型） | — | EV-10/EV-11 | 🟡（仅原型范围） | 不是正式产品的一部分 |
| 安装包、签名、SBOM（软件物料清单）、依赖安全检查 | 需要 | 先建立 Windows x64 便携 ZIP、恢复/回滚与真实历史迁移门禁，再完成安装与签名 | EV-103 已生成三个 CycloneDX SBOM、清单、包内/外 SHA-256，构建器支持 Authenticode fail-closed；EV-104～107 覆盖同源切换、正常/损坏备份、真实 schema 23→24/反向拒绝和首次轮换失败；EV-109 增加落位/回滚连续性 fail-closed；EV-110 增加真实候选落位拒绝；CI 继续执行依赖审计/漏洞扫描 | 🟠（部分） | 本地正向/负向制品 smoke、恢复/回滚、单条真实历史升级边界、确定性连续性故障与真实 Windows 落位失败安全回滚；Windows CI 与手工来源证明 workflow | EV-103～EV-107、EV-109、EV-110、`ci.yml`、`windows-portable.yml` | 🟡 | 当前精确包仍为 `unsigned`；无安装器、证书/时间戳、自动更新、完整支持版本跨度或签名后平台证据，不能作为 RC |

---

## 测试与门禁矩阵

| 测试项目 | 所属阶段/门禁 | 测试代码位置 | 正式记录 | 状态 | 环境与样本 | 不能扩大解释的局限 |
|---|---|---|---|---|---|---|
| 全部 Go 测试（720 个顶层 `Test*`/`Benchmark*`/`Example*` 函数，覆盖 62 个目录/包） | 贯穿各阶段 | `cmd`/`internal`/`pkg`/`tools` 共 170 个 `*_test.go` 文件 | `scripts/Check.ps1` 每次运行 | ✅（在 CI 上持续运行并要求全部通过） | Windows + Ubuntu（GitHub Actions） | 全部是模拟/合成数据，不是真实媒体库；Argon2id benchmark 需手动 `-bench`，CI 从不执行 |
| 数据库迁移（control 24 个迁移文件，catalog 20 个迁移文件） | 阶段 0-6 | `internal/storage/migrations/{control,catalog}` | EV-12 及各阶段收尾记录、EV-37、EV-42、EV-44、EV-46、EV-51、EV-52、EV-59、EV-87、EV-89、EV-91 | ✅ | 空库/旧库升级单元测试；control v23 增加 Creator NaturalSortKey，v24 增加 Job 历史 keyset 索引；catalog v20 增加 Creator/Source 封面窄候选 | 最终物理 Schema 仍未冻结；旧规则封面只能近似回填，需重扫精确恢复；规则隐藏/角标需重扫；正式变化 publication 仍待完整 Reference Gate |
| 契约/OpenAPI/WebSocket/游标/错误码 Schema 一致性 | 阶段 0、4、5 | `internal/contract/{api,fault,query,realtime}/*_test.go` | EV-12、EV-30、EV-37 | ✅（生成一致性） | 单元测试 | 当前 `0.6.0-pre-alpha`，尚未正式冻结 |
| 集成/端到端（使用固定的小型合成文件夹样例） | Walking Skeleton、Architecture Proof | `internal/bootstrap/run_test.go`、`internal/scanner/{service_test.go,discovery_test.go}`，样例文件在 `tests/fixtures/` | EV-13、EV-14 | ✅（限定范围内） | 单个/几个文件规模，非大规模 | 不能代表大规模真实场景 |
| 强制终止/恢复（8 个关键时间点） | Architecture Proof | `internal/recovery/killpoints_test.go`（真实拉起子进程并终止） | EV-14 | ✅ | Windows + WSL | 只覆盖这 8 个预设场景 |
| 应用级单实例锁的强制终止恢复 | 阶段 1 | `internal/platform/lock/lock_test.go` | EV-18 | ✅ | Windows + WSL | 无 |
| 只读 Source（媒体来源文件夹零写入保证） | 全局不变量 | 原型 `Test-Bench/cleanroom-lab-real`；正式 `tools/testlab/internal/sourceguard` 与 `stages/sourcelab` | EV-01、EV-92；EV-92 在真实 Pixiv 370,712 文件/105,202 目录上前后 added/removed/modified=0 | 🟡（Windows 真实本地盘已复验，网络盘未完成） | 独立 AppDirs、动态 loopback、全树前后 guard | 当前 guard 是人工真实门禁且成本约 90～110 秒/次；未验证 SMB/NAS，也不能替代完整扫描结果 |
| 查询/搜索/游标/Overlay/封面/逐成员授权正确性 | 阶段 4 | `internal/query/*_test.go`、`internal/catalog/*_test.go`、`internal/querytext/*_test.go` | EV-30～34、EV-42、EV-44 | ✅（合成正确性收口） | 单元+黄金样例+合成 migration；授权在 total/keyset/limit 前生效 | 不代表真实 Source、正式规模性能或 API Freeze |
| 媒体 Range 请求/DerivedAsset（缩略图）正确性 | 阶段 4 | `internal/media/*_test.go`、`internal/derived*/**_test.go` | EV-30/32/33 | ✅ | 单元+端到端 | 无 |
| 性能测试（Reference Performance） | 阶段 3、4 | `internal/query/reference_performance_test.go` 微基准、`tools/testlab` 生产 Store/HTTP 矩阵及 `publication-perf`（均需显式运行） | EV-23/35/36/51/52/96～101 均明确区分方向性测量、执行器预检与正式 Gate | 🟡 | 当前补证含 500k/10 Source 窄候选、纠正后 100k/10 Source 真实双关系全局 1%/10%/50% 容量预检、十目标来源/`goMaxProcs` 报告指纹、publication/Query 原子断点续跑，以及 fail-closed Query Reference seed/probe | 执行器与报告入口已加固，但正式 500k publication 每档至少 20 样本、Query 并发/冷缓存、维护、哈希和 Degradation 结果均未完成，不能当作正式门禁结论 |
| SSD/HDD/SMB/NAS 真实场景 | 阶段 3、7 | `tools/testlab/stages/sourcelab` 已用于本地盘有界预检；网络盘仍无正式结果 | EV-25、EV-92～EV-94、EV-108、EV-111 均明确写"全量扫描未完成，正式全量性能 Gate 仍未通过" | 🟡（本地盘抽样/有界） / ⏳（全量与网络盘） | 真实 SSD/HDD 既有抽样；真实 Pixiv 370,712 文件 Source 的 45 秒有界 `index` 与同 AppDirs 恢复重跑；Gank 12/12、Pawchive 单目标 2/2 及批量 12/12 确认成功 | 有界结果不能当作全量吞吐保证；活动 Hash 取消成功链未触发，HDD/SMB/NAS 尚未验证 |
| Windows/Linux/macOS/Docker 平台支持 | 阶段 7 | Windows 增加本地便携测试包、恢复/回滚、真实 schema 23→24/反向拒绝、首次轮换/候选落位失败及落位/回滚连续性代码门禁；CI 仍只有 Windows + Ubuntu | EV-103～EV-107、EV-109、EV-110、ADR-007 四级支持成熟度表 | 🟡（Windows 增加制品/恢复/升级/轮换/落位失败 smoke 与代码级连续性门禁，Linux 仍停在 CI 层级）/ ⏳（macOS/Docker） | 当前工作站 Windows x64 + GitHub Actions 配置 | 未达到"发行候选"或"正式支持"级别；本地无签名包、未覆盖双失败与 CI Linux 都不能外推为正式平台支持 |
| 安全（认证、授权、Web 边界、路径穿越、恶意元数据/媒体、限流） | 阶段 5 | 正式生产包覆盖账户/Token/Grant/Session/WS/Web 与合成攻击；Work 查询逐成员授权；真实 Chrome/Edge 已覆盖 Personal/LAN 主路径和吊销 | EV-09、EV-37、EV-38、EV-44、EV-60 | 🟡 | Windows 合成与浏览器；WSL race；隔离 Personal/LAN Chromium 安全管理链 | 真实物理 LAN 多设备、目标低端设备与真实恶意资源门禁未完成，整体 Gate 未通过 |
| Web/PWA 界面测试 | 阶段 6 | `web/src/**/*.test.ts(x)`、`web/scripts/check-audit.test.mjs`、`web/e2e`、`internal/webapp/*_test.go` 与 `tools/testlab/cmd/web-e2e` | EV-38～EV-40、EV-42、EV-44、EV-54～EV-91、EV-102 | 🟡 | Vitest 15 个文件 211 项；Chromium/Firefox mock smoke 20/20；隔离 Chromium 与 Firefox/真实 `galleryd` 完整运行器各 21 项，同 AppDirs 多阶段重启与 11 个治理子根 Source guard 保持；EV-79 另有 WSL Linux Chromium 320/360/390/412px 产物探针 | EV-102 新增作品身份连续交接、旧快照交互隔离、减弱动画与管理端局部反馈，并已复跑完整真实后端链。其余覆盖 Source 作者分页、管理自举、画廊/媒体、规则、安全、维护、治理、取消、重启、网络退化和媒体背压；真实移动设备/屏幕阅读器和其余弱网矩阵仍未覆盖 |
| 阶段 4 查询/媒体 Correctness（testlab） | 阶段 4 | `tools/testlab/stages/stage4/smoke_test.go` 与既有 query/media orchestrator | EV-36、EV-45、EV-55、EV-90 | ✅（1k 合成持续 smoke）/ 🧪（500k 人工正式矩阵） | 普通 `go test` 经生产 bootstrap + 真实 loopback HTTP 执行 40 查询 + 6 Cursor + 20 media/derived finding；`creator.id` 由真实扫描建立双库身份后逐 ID 验证 | 持续入口已关闭 `TEST-2` 与 `creator.id` limitation；不运行 perf，原裸伪造 publication 错误码差异已关闭；不代表 500k Reference、HDD/SMB/NAS 或 API Freeze |
| 发布/签名/SBOM | 阶段 7 | Windows 便携包、SBOM、清单、摘要、可选 Authenticode、恢复/回滚及真实历史升级边界已进入脚本与 CI；EV-109/110 补齐连续性 fail-closed 代码与真实候选落位安全回滚门禁 | EV-103～EV-107、EV-109、EV-110、Windows 发行脚本与 workflow | 🟡（未签名测试基线） | 本地精确干净 ZIP 正向/负向、正常/损坏备份、schema 23→24/反向拒绝、首次轮换/候选落位失败 smoke 与确定性落位/回滚双失败；远端 workflow 待当前提交触发 | 正式证书/时间戳、安装器、更新、更多支持版本跨度、磁盘满/真实 OS 落位与回滚双失败/中断与 RC 仍不存在 |
| Fuzz（随机变异输入）测试、Benchmark（性能基准）测试 | — | `internal/auth/password_benchmark_test.go` 已有 Argon2id benchmark，尚无正式 Fuzz | EV-38 | 🟡 | 当前 Windows 高性能工作站 | 不代表目标低端设备参数门禁 |

---

## API 与可运行能力

| 分类 | 具体内容 | 客户端现在能做什么 |
|---|---|---|
| 公开 HTTP 接口 | OpenAPI `0.6.0-pre-alpha`，100 条路径 / 服务端 120 条路由，覆盖 Library/Source、规则、任务、查询/媒体、Overlay、账户/授权/分享与维护 | Web/PWA 接入了其中 56 条；契约仍未 Freeze。`deleteRulePackage` 的路径漂移已在 EV-40 修复，并新增比对契约与注册路由集合的持续测试 |
| WebSocket 与 HTTP 恢复 | `/ws/v1` 推送任务、publication、安全吊销等事件；HTTP snapshot/查询仍是事实源 | EV-40 修复握手与信封字段；EV-54 锁定事件后的 HTTP snapshot；EV-60/EV-71/EV-80 覆盖断线、gap、长离线和吊销收敛；EV-81/EV-83 隔离旧通知、迟到成功/错误响应并覆盖一次 GET 中断恢复；EV-82 覆盖同 origin 服务长停机并按原端口恢复。带宽、随机延迟/丢包分布、代理、移动网络和反复崩溃等完整弱网矩阵仍未覆盖 |
| 命令行工具 `galleryctl` | 只有 `version`（查看版本）和 `health`（查看健康状态）两个命令 | 尚不能覆盖主要管理操作 |
| 后台任务 | 扫描、哈希、目录维护、崩溃恢复、备份、Overlay 重投影 | Web/PWA 提供任务列表/详情、attempt、取消与重试入口 |
| 内部服务（不对外暴露） | 外部工具调用框架（`internal/toolrunner`）——代码和调度已经就绪，但生产环境里尚未接入任何真实功能，解析器留空 | 客户端无法触达 |
| 网页/桌面界面 | 同源内嵌 Web/PWA；无桌面壳 | 可从 `galleryd` 使用 pre-alpha Web 基线；EV-103 的 Windows 便携测试 ZIP 已内嵌完整当前双前端，但不是安装发行版本 |

---

## 数据库迁移与冻结状态

| 项目 | 数量/情况 | 说明 |
|---|---|---|
| `control.db` 迁移文件总数 | 23 个（`00001_initialize.sql` 到 `00023_creator_natural_sort.sql`） | 在既有身份/授权/安全/规则事实基础上增加 Creator `sort_name_key` 与索引；启动时用权威 NaturalSortKey v2 原子回填并推进编码版本 |
| `catalog.db` 迁移文件总数 | 20 个（`00001_initialize.sql` 到 `00020_creator_source_cover_projections.sql`） | 除既有发布/快照/稳定引用/派生/媒体/封面/成员事实外，增加发布 mtime、规则呈现、Work 标量、三级聚合封面、排序协议 v2、FTS 同 rowid 搜索窄候选、候选验证封印、Overlay candidate 创建基线与 Creator/Source 封面窄候选 |
| 阶段 1 Schema Freeze（领域模型冻结） | 已执行（`00016_schema_freeze_phase1.sql`） | 冻结了作品/作者/媒体的唯一性约束、稳定引用规则；文件在网络共享盘环境下的最终唯一约束、大文件哈希计算的持久任务等仍属"兼容演进基线"，未最终冻结 |
| 阶段 2 规则生命周期迁移 | 已执行（`00017_rules_lifecycle.sql`） | 固化了规则包的不可变发布、草稿乐观锁等；单一生效绑定规则仍属"兼容演进基线" |
| 阶段 3 任务/正确性迁移 | 已执行（`00018`、`00019`） | 固化了任务/尝试模型、6 个资源池调度；真实大规模性能门禁未随迁移一起完成 |
| 阶段 4 Catalog 兼容迁移 | 已执行 `00010_query_dependency_fields.sql`～`00020_creator_source_cover_projections.sql` | v9→v10 查询依赖回填的提前完成缺陷已由 EV-33 修复；v10→v14 封面/成员/mtime/规则呈现见 EV-42/44/46；v15→v20 的 Work 标量、聚合封面、排序协议、搜索窄候选、验证封印与 Creator/Source 封面窄候选见 EV-50/51/52/87，物理 Schema 仍未 Freeze |
| 阶段 5 安全迁移 | 已执行（`00020_phase5_security.sql`） | 方向登记为 COMPATIBILITY_BASELINE；Argon2id、Session 时长等数值保持 PRE_FREEZE；空库和 v19 有数据升级有自动测试 |
| "已冻结"与"兼容演进基线"与"暂定（PRE_FREEZE）"的区别 | 见状态图例 | **已冻结**＝以后只能兼容式扩展，不能推翻重来；**兼容演进基线**＝方向已定，具体数值/边界还能调整；**暂定/PRE_FREEZE**＝连方向都可能因压力测试结果而调整 |

---

## 已实现但尚未冻结的事项

| 事项 | 当前值/状态 | 何时才会最终定案 |
|---|---|---|
| 排序权重（标题 3、作者 2、标签 1、文件名 0） | 结构已固化，数值暂定 | 阶段 4 正式压力测试 |
| 结果总数计算预算 | 三态协议（精确/下限/不计算）结构已固化，具体数值暂定 | 阶段 4 正式压力测试 |
| 分页游标有效期（cursor lease） | 5 分钟（暂定） | 阶段 4，需要并发游标垃圾回收和长查询的真实证据 |
| 目录库发布只读租约（publication read lease） | 2 分钟（暂定，仅显式快照模式需要） | 阶段 4，需要大文件/慢速磁盘读取证据 |
| 派生资源（缩略图）只读租约 | 5 分钟（暂定） | 阶段 4，同上 |
| 文件身份识别（真实 Windows FileID / Linux dev+inode） | 接口已预留，尚未接入真实实现（临时占位） | 领域 Schema 最终冻结门禁 / 阶段 7 |
| `container_signature`（容器签名，用于跳过判断的优化） | 已记录但尚未用于任何跳过判断（占位） | 领域 Schema 最终冻结门禁 |
| 扫描档案（scanProfile）默认选择逻辑 | 已实现，标注"有条件接受" | 阶段 3 真实规模复测（目前只做了抽样，未做全量） |
| 单一生效规则绑定（SourceRuleBinding） | 已实现，标注"兼容演进基线"，非最终冻结 | 出现多规则链/按来源路由需求时 |
| API 版本号 | `0.6.0-pre-alpha` | 阶段 4 PRE_FREEZE/性能与阶段 5 真实 LAN 门禁完成后的 API Freeze 审计 |
| 安全生命周期数值 | 配对 5 分钟、Session 绝对 30 天/空闲 24 小时、登录 15 分钟窗口 8 次失败后阻断 15 分钟，均 PRE_FREEZE | 真实 LAN 多设备与目标设备安全门禁 |
| Argon2id 成本 | PHC v19，`m=19456 KiB,t=2,p=1`，PRE_FREEZE | Windows 目标设备与并发登录基准 |
| 孤儿文件默认阈值（连续 3 次扫描未找到即视为孤儿） | 可调常量，未冻结 | 阶段 2 运行调优，需要真实误报/漏报比例证据 |

---

## 已知文档与代码漂移

| 类型 | 具体内容 | 说明 |
|---|---|---|
| 已修正文档漂移 | `internal/contract/api/openapi.yaml` 的描述与版本已由阶段 3 的 `0.5.0-pre-alpha` 更新为阶段 5 首轮契约 `0.6.0-pre-alpha` | EV-37 同步完成，不再是当前漂移 |
| 旧验证结论被新验证结论修正的案例 | "按需校验单个文件"功能：EV-30（范围有误）→EV-31（范围修正，执行细节未查）→EV-32（补充执行细节）→EV-33（独立复查，未发现新问题）→EV-34（发现 EV-33 遗漏的一个真实问题） | 文档本身清楚记录了这个修正过程，并明确写"以 EV-34 为准"；这是验证记录自我修正机制在起作用 |
| 早期措辞被后续文档主动修正 | 早期文档一度写"已完成真实 SSD/HDD 大数据集验收"，随后被 EV-26 主动改为更准确的"结构抽样/只读清单/有界样本验收" | 文档发现表述过于绝对后主动修正，早期版本的措辞不应被当作最终结论 |
| 测试文件存在但正式记录未给出完成结论的情况 | 已新增 Argon2id benchmark，但只在当前工作站取得 EV-38 数据且 CI 从不执行；全仓库仍无正式 Fuzz；`internal/toolrunner` 虽有测试和代码，生产环境尚无入口且其「有界输出/超时/强杀」测试从未真正触及上限 | 目标低端设备 Argon2id、Fuzz 与真实外部工具资源门禁仍不能算完成 |
| **本轮新发现的漂移（EV-39，2026-07-23；①②③⑤已在 EV-40 关闭）** | 上一版本此处写"未发现文档说完成但代码其实不存在的情况"，该结论已被推翻。实际发现：①「WebSocket 实时通道 ✅」与真实浏览器行为不符；②「Web 覆盖 Overlay/任务/治理主页面」在真实后端下多数写入口不渲染；③ 引用了不存在的 `web/tests/accessibility.test.tsx`；④ 测试规模写作 72 文件/257 函数/32 目录，实际为 87/317/41；⑤「约 70 个 HTTP 接口」实际为 100 条 OpenAPI 路径/120 条路由；⑥ 规范 04 描述的 `after_overlay_fact_version` 读己之写屏障与 `hash_pending` 状态在代码中完全不存在；⑦ 规范 06 与 `overlay.OverlayFieldCapabilities` 都声明 `progress` 可排序，但排序功能未实现 | 这些条目本身说明：仅靠静态阅读与"测试存在"判断完成度是不充分的，必须用真实浏览器/真实进程复核 |
| 门禁范围不可复现（已修复） | `npm ci` 之后 `web/node_modules/flatted/golang/pkg/flatted` 曾进入 Go 模块包图；EV-40 已把 `Check.ps1` 与 CI 的 Go 门禁改为显式包集合 `./cmd/... ./internal/... ./pkg/... ./tools/...` | 见 EV-39 `BLD-1`、EV-40 第 10 项 |

---

## 风险、限制与延期事项

| 类别 | 具体内容 |
|---|---|
| 正确性风险 | 阶段 4 的"按需校验单个文件"功能经历多轮验证才发现全部问题，说明涉及发布快照版本绑定这类跨模块一致性的功能容易隐藏缺陷，值得在未来的分享功能、多用户权限等同样涉及跨模块状态一致性的能力上保持关注 |
| 性能风险 | 真实机械硬盘（HDD）全量扫描此前实测约需 22 小时才能扫完 20 万文件（在改为增量模式前），虽已缓解，但正式的全量性能门禁至今尚未跑完，真实使用中大型库的完整扫描表现仍待验证 |
| 平台风险 | Linux 支持目前只验证过 WSL（Windows 内置的 Linux 兼容层）和 GitHub Actions 的 `ubuntu-latest`，尚未验证独立安装的原生 Linux 系统；macOS、Docker、真实网络共享盘（SMB/NAS）尚未验证 |
| 安全风险 | 阶段 5 账户/凭据/授权、匿名 Share、全资源矩阵、恶意输入与 WS 防滥用代码及合成测试已落地；EV-44 已关闭 Work 聚合查询逐成员授权缺口，EV-46 已关闭 `SEC-3`（媒体呈现改由服务端内联白名单决定，三条正文路径统一加 sandbox CSP）；真实 LAN 多设备/浏览器和目标设备 Argon2id 门禁仍缺，不能描述为完整 Security Gate 通过 |
| 产品/UI 缺口 | Web/PWA 代码基线已存在，EV-39 的实时通道与写入口阻断已由 EV-40 关闭；EV-54～EV-77 覆盖主要真实后端业务与治理持续链，EV-78～EV-91 又关闭窄屏导航焦点、Linux Chromium 320px Grid 溢出、弱网恢复、双入口重构、媒体背压、Source 作者分页和 Job 历史分页缺口。EV-92 的真实 Pixiv 证据属于独立 testlab，不是浏览器业务链；真实移动设备/触控、人工屏幕阅读器、全页面可访问性与正式可用性 Gate 均未完成 |
| 测试体系缺口 | EV-54～EV-91 已让主要业务/治理、作者与 Job 分页、断线/gap/连续网络切换、一次查询中断、同 origin 服务长停机、媒体背压、取消、强杀接管、恢复重启及双入口重构的 Chromium/Firefox 真实后端 E2E 进入运行器，并保持 mock smoke 与真实证据分层；其中 EV-91 只完成新增用例定向验证，尚未重跑完整链。EV-92～EV-94 已建立真实 Pixiv 有界预检、201 ms 内观察 discovery 取消终态、同 AppDirs 恢复重跑和零写入 guard；EV-108 让 Gank/Pawchive 各有一条成功链，EV-111 又让 Pawchive 12 目标以单 Job 12/12 完成并关闭逐目标重复处理，但成功链未触发活动 Hash 取消，全量扫描/哈希与其它存储类型仍未关闭。EV-45 已让阶段 4 testlab Correctness 进入普通 `go test`，EV-96～101 已建立真实双关系、十目标来源、可续跑 publication、fail-closed Query Reference 入口与 Query 原子分窗续跑；纠正后 100k 容量预检已完成，但正式 500k publication/Query 矩阵仍是人工门禁；全仓库仍无正式 Fuzz，部分平台包仍缺直接测试。带宽、随机延迟/丢包分布、代理/移动网络等其余弱网矩阵与真实存储崩溃恢复仍未进入持续门禁 |
| 发行缺口 | EV-103 已有未签名 Windows 便携 ZIP、三个 SBOM、摘要与 smoke；EV-104～107 再覆盖正常/损坏备份、真实 schema 23→24/反向拒绝及首次轮换失败；EV-109 关闭落位/回滚双失败可能创建空库的代码缺口；EV-110 证明真实候选落位失败可安全回滚。仍没有安装包、正式代码签名/时间戳、自动更新、CredentialStore、完整支持版本跨度、磁盘满/真实 OS 落位与回滚双失败/中断和平台支持证据 |
| 明确不进入 v1 的事项 | 原始文件写入/回收站、远程/公网访问、插件系统、原生手机客户端、压缩包/PDF/漫画容器格式解析、无限制单字中文搜索与拼音搜索、外部独立搜索引擎、自动导入其他同类产品数据 |

---

## 接下来的开发顺序

> 以下顺序基于当前文档中已经写明的路线和已知缺口整理，不代表对既定路线的更改。

| 顺序 | 阶段 | 需要做的事 | 排序依据 |
|---|---|---|---|
| 0 | 缺陷收口（EV-39 登记项已全部关闭，见 EV-40、EV-44、EV-45、EV-46） | EV-40 关闭 6 项 P1 及 `SEC-4`/`TEST-1`/`BLD-1`/`A11Y-1` 键盘部分；EV-44 关闭 `AUTHZ-1`/`QRY-1`；EV-45 关闭 `TEST-2`；EV-46 关闭 `MED-1`、`SEC-3`，并新发现修复 `LINK-1`（Windows 目录联接被识别为普通文件）、`TX-1`（WAL 读后写事务过期读快照）与迁移预算门禁不可复现 | 阻断性缺陷优先；`MED-1` 由 ADR-010 裁决完整性证据分层，`SEC-3` 由规范 08 新增呈现策略裁决 |
| 1 | 阶段 4 收尾 | 分窗执行正式 500k publication、Query 冷/热并发与 Degradation 性能门禁并完成 API 接口冻结 | Correctness 已持续化，EV-98 完成纠正后 100k 双关系容量预检，EV-99～101 已加固 publication/Query 正式入口与分窗恢复；正式多样本结果和接口数值仍未冻结 |
| 2 | 阶段 5 | 完成真实 LAN 多设备与目标低端设备 Argon2id 延迟/并发验证 | 同机 Chrome/Edge 和高性能工作站证据已取得，剩余缺口需要外部设备环境 |
| 3 | 阶段 6 | EV-92～EV-94 已完成真实 Pixiv Source 只读有界预检、discovery 取消与同 AppDirs 恢复重跑；下一步固定全量运行根、空间、续跑/失败恢复和监控报告方案，并继续覆盖其余弱网矩阵、全页面可访问性与真实设备 | 继续扩大真实后端 E2E；Pixiv 全量扫描保持低优先级，不能替代业务闭环和发布可用性门禁 |
| 4 | 阶段 7 | 在 EV-103～107、EV-109/110 的便携、恢复/回滚与真实 schema 23→24 基线上完成 Authenticode、支持版本跨度、磁盘满/真实 OS 落位与回滚双失败/中断及 Windows RC；随后推进 Linux 原生、macOS、Docker、SMB/NAS 和安装发行 | 当前关闭制品编排、正常/损坏备份、一条相邻历史边界、首次轮换失败、真实候选落位安全回滚及连续性 fail-closed 代码风险；签名、双失败/中断和平台支持仍是正式发行阻断项 |

---

## 术语表

| 术语 | 说明 |
|---|---|
| Canonical（规范化/权威的） | 指"系统认定的、去重之后唯一正确"的那一份数据，例如同一个作者可能在不同文件夹里叫不同名字，但系统里只有一个"权威作者记录" |
| Binding（绑定） | 把"扫描到的文件"和"系统里权威的作品/作者记录"关联起来的关系 |
| Overlay（覆盖层） | 用户自己整理的信息，比如收藏、标签、自定义封面、阅读进度——这些不会因为重新扫描而被覆盖丢失 |
| Catalog publication（目录库发布） | 每次扫描完成后，把新的数据"正式生效"这个动作，保证用户看到的始终是完整、一致的数据，不会出现扫描到一半的中间状态 |
| Rule IR（规则中间表示） | 用户写的规则被翻译成机器能高效执行的内部格式 |
| ContentBlob（内容块） | 按文件真实内容（而非文件名或路径）计算出的唯一标识，用来判断"这是不是同一份内容"，即使文件被改名或移动过 |
| FileLocation（文件位置） | 记录某个内容块具体存放在磁盘上哪个路径，路径变化不影响内容块本身的身份 |
| DerivedAsset（派生资源） | 从原始文件派生出来的内容，比如缩略图、转码后的预览图，都可以随时删除并重新生成 |
| Correctness Gate（正确性门禁） | 用模拟/构造出来的数据验证逻辑是否正确，不涉及真实大规模数据下的性能表现 |
| PRE_FREEZE（暂定/未冻结） | 文档中标注"这个数值或方案尚未最终定案，未来可能因压力测试结果而调整"的标签 |
| Schema Freeze（Schema 冻结） | 数据库表结构和唯一性规则被正式固定下来，以后只能兼容式增加，不能推翻重做 |
| 抽样验收 | 只挑选一部分真实数据（例如几千个文件）进行测试，而非验证用户全部几十万文件 |
| 全量性能门禁 | 使用真实用户可能拥有的完整数据规模（几十万甚至上百万文件）做的完整性能测试，目前尚未走完 |

---

## 维护与证据来源

本文档基于对仓库的一次基线审计生成，此后随开发进展持续更新。

| 项目 | 内容 |
|---|---|
| 初始审计日期 | 2026-07-21 |
| 初始审计基线 | `083877a9e022d5f0f58dc59a5e91fda9254ea7c3` |
| 初始可达提交数 | 115 |
| 最近一次全面审计 | 2026-07-23，基线 `ae46e0e6653ac9081ef604d279688bd451693954`，可达提交数 158，根提交 `c07cc0be1967e9fdfd4309c4532d284cd946cd54`；证据见 [验证记录 EV-39](Documents/证据/验证记录.md) |
| 后续维护方式 | 每完成一个阶段性的大型开发后同步更新本文档与 [README.md](README.md) 的对应章节；不采用仅在文末追加说明的方式维护 |

功能状态判断依据事实代码；测试/门禁状态判断依据 [Documents/证据/验证记录.md](Documents/证据/验证记录.md)。本文档与 [Documents/README.md](Documents/README.md) 下的规范、实施计划、ADR 存在冲突时，以后者为准，本文档应据此修正。
