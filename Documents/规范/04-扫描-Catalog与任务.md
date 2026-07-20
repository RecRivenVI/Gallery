# 扫描、Catalog 与任务

> 类型：规范。扫描可见性、Catalog publication、任务恢复和 GC 以本文为唯一权威来源。

## 目标语义

扫描是“构建并发布新的查询快照”，不是边遍历边修改当前列表。无论全量还是增量，读者始终看到一个完整 `(catalog_revision, overlay_projection_revision)`；取消、错误或崩溃不得把候选的一部分暴露出来。

## 扫描阶段

```text
validate Source/SourceRuleBinding
  → create Job attempt
  → discover containers and files
  → evaluate Rule IR and collect Trace
  → resolve Source identities and Binding candidates
  → build SourceWork/SourceCreator/SourceMedia、ContentBlob/FileLocation
  → build matched WorkProjection/CreatorProjection/MediaProjection、SortKey and FTS
  → validate candidate invariants
  → create query_publication_id and switch the active publication in a short transaction
  → project completion to control.db
  → schedule derived assets and maintenance
```

发现阶段只读 Source。相对路径、FileID/inode、大小和 mtime 用于判断是否需要进一步读取，是常见、廉价且有价值的组合证据，但都只能产生候选，不能单独确认内容；新内容或冲突候选在建立 ContentBlob 时必须由持久哈希任务完成完整 SHA-256，任何时候都不得以这些线索伪造或替代 digest。`verify` 档案与默认 `incremental` 档案对新增或疑似变化媒体，首次哈希成功前 SourceMedia 保持 staging `hash_pending`，默认阻塞受影响 Source publication；超大文件或网络盘只延长带进度、可取消的候选构建，不能降低身份强度。`index` 档案是唯一例外，允许媒体以 `located_unverified` 状态正式发布而不建立 ContentBlob，详见下一节。具体失败、重试和续算语义见 [文件系统与媒体处理](08-文件系统与媒体处理.md)。

## 扫描档案与内容确认状态

扫描把“媒体已被发现”和“媒体内容已完整确认”拆成两件事，媒体在 SourceMedia/MediaProjection 上有明确的 `content_verification_state`：

```text
located_unverified   已发现、metadata 已解析、可浏览基本信息，尚无已确认 ContentBlob
content_verified     已通过完整 SHA-256 确认，ContentBlob/FileLocation 记录存在
```

`located_unverified` 与 `content_verified` 都是发布出的合法查询状态，不是构建中的半发布 candidate；ValidateCandidate 与短事务发布不因 `located_unverified` 媒体存在而拒绝发布，也不允许一次发布内混合不同 revision。

扫描请求携带 `scanProfile`，三者互不冒充：

| scanProfile | 语义 | 是否读取媒体正文 |
| --- | --- | --- |
| `index` | 首次快速建立可浏览 Catalog；发现容器、解析 metadata，媒体一律以 `located_unverified` 发布，不建立 Hash Job | 否 |
| `incremental`（默认） | 按 Source、规范化相对路径、大小、mtime（可用时含平台文件身份线索）组合证据，与既往 `content_verified` 观察比较；证据一致则复用既往摘要，不新增 Hash Job；新增或证据不一致的媒体建立 Hash Job 完整确认 | 仅新增/变化部分 |
| `verify` | 忽略既往观察，对本次扫描到的媒体强制重新建立 Hash Job、重新完整哈希，用于显式、低频的完整性校验 | 是，全部 |

`incremental` 复用只在满足下列全部条件时发生：同一 `source_id`；规范化相对路径相同；`size_bytes` 相同；`mtime_ns` 相同；既往 SourceMedia 处于 `content_verified`。任一条件不满足（含首次出现、`RuleVersion` 变化不影响此判断——digest 只描述字节内容）都视为需要重新确认。降级范围限定到具体文件，不因单个文件的证据变化而对整个 Source 强制完整重新哈希。

一条 SourceMedia observation（`size_bytes`、`mtime_ns`、location key 与可用的平台身份字段）必须来自同一次最终定位观察，不得把 discovery 阶段的旧 Stat 与准备发布前重新定位得到的新 Stat 拼接成混合记录：`index` 不承诺内容稳定、不计算 digest，但仍必须在最终 `LocateSourceFile` 成功后同步刷新全部这些字段；`incremental` 复用前重新定位发现证据已变化时，同样以这次重新定位的结果作为后续完整哈希的期望身份，不得混用已经过期的 discovery 观察。

未显式指定 `scanProfile` 时按下表自动选择，两种情况下持久化的都是最终决定的具体档案值，不保存含糊的 `auto`：

| 状态 | 默认档案 |
| --- | --- |
| 无当前 publication，且 control.db 无该 Source 的持久领域历史 | `index` |
| 有当前 publication | `incremental` |
| 无当前 publication，但 control.db 有持久领域历史 | `incremental` |

持久领域历史指 `work_bindings`/`media_bindings`/`creator_bindings`/Binding issue/SourceWork 拆分或合并决策中与该 Source 相关的任意历史状态；Source 行本身、SourceRuleBinding 或从未成功解析过 Canonical 的空 Source 不计入。Catalog 可以随时删除重建，但这些记录只存在于 control.db：仅凭"当前无 publication"把重建后的 Source 当作全新 Source 并自动选择 `index`，会让本次扫描不产生任何 ContentBlob digest，从而悄悄绕过阶段 1 依赖完整哈希证据的 SourceWork 拆分/合并结构审查。因此显式 `index` 只在"无 publication 且无持久领域历史"时被接受；已有 publication，或 Catalog 已丢失但仍有持久领域历史时显式请求 `index`，都返回结构化 `CONFLICT`，不创建 Job、不修改 Binding/Catalog。

`index`/`incremental` 未确认媒体不写入 `content_blobs`/`file_locations`，只在 SourceMedia 记录发现时的观察证据（`mtime_ns`、`platform_identity_kind`/`platform_identity_value`、`container_signature`）与 `content_verification_state`；`MediaProjection` 同样携带独立的 `content_verification_state`（`located_unverified`/`content_verified`）和 `verified_at`，与只表达位置可用性的 `location_status`（`present`/`offline`/`missing`/`inaccessible`）是两个正交字段——内容未确认不得写进 `location_status`，位置在线但内容未确认时 `location_status` 仍为 `present`。媒体从 `located_unverified` 提升为 `content_verified` 只能通过持久 Hash Job 成功完成，绝不使用 mtime、size 或路径伪造 digest；`verified_at` 只在真正完成完整哈希时推进，`incremental` 复用既往摘要不得更新该时间。

`platform_identity_kind`/`platform_identity_value` 是可选的平台文件身份线索槽位；当前实现尚未接入真实 Windows FileID 或 `dev+inode`（见 [跨平台与客户端](09-跨平台与客户端.md) 的 `FileIdentityProvider` 端口现状），只使用路径、大小、mtime 的组合证据，接入真实平台身份是后续文件身份门禁的一部分，不阻塞本节语义。`container_signature` 是为容器级（整个作品目录）跳过预留的版本化签名字段，本轮只记录、不作为复用判断的门槛，未来可在不改变实体边界的前提下扩展为目录级跳过优化。

## 单媒体目标化按需内容确认

"单媒体按需确认"（客户端打开一张 `located_unverified` 媒体、请求立即确认其内容）不得触发所属整个 Source 的 `verify` 档案——对十万、二十万级媒体的 Source，这会把一次单媒体请求悄悄放大为数小时级全量重新哈希。最终模型是**目标化 Scan Job**：复用既有 Scanner/Hash Job/Catalog publication 管线，而不是新增第二套 Job/publication 状态机。

按需确认是"当前 publication 操作"：省略 `queryPublicationId` 解析当前 active publication；显式提供时必须精确解析到当前 active publication，仍然存在但已经不是 active 的历史 publication 一律拒绝为结构化 `CONFLICT`，不创建 Job、不修改 Binding/Catalog/Overlay/Source。媒体身份（`mediaId`/`sourceId`/`relativePath`）与冻结 observation 必须来自同一个已确认为 active 的 publication，不得混用请求 publication 与执行时刻另行读取的 active publication。

扫描请求快照新增可选 `verificationTargets` 列表，每项冻结 `mediaId`、`sourceId`、规范化 `relativePath`、实际使用的 `queryPublicationId` 与请求时刻的 `observationFingerprint`：

```json
{
  "scanProfile": "incremental",
  "verificationTargets": [
    {"mediaId": "...", "sourceId": "...", "relativePath": "...", "queryPublicationId": "...", "observationFingerprint": "..."}
  ]
}
```

语义：

- Source discovery 与规则解析仍完整执行，保持 SourceWork 拆分/合并结构审查正确；
- `verificationTargets` 中列出的媒体无条件跳过 incremental 既往摘要复用短路，强制建立完整 Hash Job；
- **不在** `verificationTargets` 中、且既往观察仍是 `located_unverified` 的媒体，即使本次 `scanProfile` 是 `incremental`，也不得因为"这次扫描附带了 target"而被顺带强制哈希——它们按 `index` 档案的既有规则继续保持未确认、不产生 Hash Job；
- 新增媒体或既往已确认但真实发生变化（size/mtime 不再匹配）的非目标媒体，仍按正常 `incremental` 规则处理（新增/变化即建立 Hash Job），不因为存在 target 而改变其余媒体的正确性；
- 该扫描仍走同一条 discovery → candidate → publish 流水线，因此发布出的仍是一个完整、单一的新 `query_publication_id`，旧 publication 继续可读；
- 冻结字段不只是持久化，执行阶段必须真正验证，且必须读取冻结的 `queryPublicationId` 对应的 publication，不得读取执行时刻恰好 active 的 publication：目标的 `observationFingerprint` 与执行时刻的新鲜观察不一致（文件在排队期间被替换/截断/改动，或冻结 publication 描述的确认状态已经过期）、目标未出现在本次 discovery 结果中（被移动、改名或删除）、目标相对路径实际解析出的 CanonicalMedia 与冻结的 `mediaId` 不一致，三者任一成立都必须让整个 Job 失败，不得静默确认成一个不是请求方原本指定的对象；多个 target 时任一验证失败都不得让 Job 整体成功。前两者是 Job 真正开始读取内容之前的前置身份校验，统一为不可重试的结构化 `VERIFICATION_TARGET_MISMATCH`（目标消失沿用既有 `CONTENT_DISAPPEARED`，同样不可重试）；`CONTENT_CHANGED_DURING_HASH`（retryable）保留给完整 Hash 读取过程中真正并发发生的内容变化，不用于此处的前置校验，避免对已经失效的请求快照做无意义的自动重试。

调用前置条件：Source 必须已有 publication（单媒体确认只对已发布 Catalog 中的已知媒体有意义），因此该请求总是显式使用 `incremental` 档案，不落入 `index`/首次扫描判定；Source 尚无 publication 时返回结构化 `CONFLICT`，不创建 Job。

幂等身份至少绑定：协议版本、实际使用的 `queryPublicationId`、CanonicalMedia ID、Source ID、规范化相对路径、observation 指纹（size、mtime、当前内容确认状态）。重复请求语义：

| 状态 | 结果 |
| --- | --- |
| 同 publication、同 observation 的 queued/running Job | 返回同一 Job |
| 同 observation 已完成，且当前媒体已确认 | 稳定 `CONFLICT`（在 `GetMediaAt` 已确认状态时提前返回，不再走到 Job 创建） |
| observation 已变化（size/mtime/确认状态不同） | 幂等键随之不同，不复用旧 Job |
| active publication 已切换（即使媒体 ID、路径和 observation 恰好相同） | 幂等键随实际使用的 `queryPublicationId` 变化，不误复用旧 publication 的 Job |
| 显式 `queryPublicationId` 仍存在但已经不是当前 active | 结构化 `CONFLICT`，不创建 Job |
| 显式 `queryPublicationId` 不存在或已被 GC | `CURSOR_EXPIRED` |

目标化确认 Job 复用与普通扫描完全相同的 `jobs` 表与 `ResourceScan` 资源类，因此同一 Source 同时只能有一个未终结的 scan 类 Job——无论它是普通扫描还是目标化确认，均受既有"Source 单活跃扫描"数据库唯一约束保护，不引入并行的第二套约束。

## 默认提交模型

默认采用 **staging snapshot + publish pointer**：

- 长时间分批构建新的完整可查询 revision 元组；活动读者继续使用旧元组；
- SourceWork、SourceCreator、SourceMedia、ContentBlob、FileLocation 和来源关系属于同一 `catalog_revision`；
- WorkProjection、CreatorProjection、MediaProjection、SortKey 和同库 FTS5 必须绑定同一 `catalog_revision + overlay_projection_revision`，不得把 CanonicalWork/CanonicalCreator/CanonicalMedia 复制成 Catalog 权威事实；
- 发布事务只做候选校验结果确认、创建合法 `query_publication_id` 和活动 publication 指针切换；参考机目标 P95 小于 250 ms，该数字不是所有设备的正确性要求；
- SQLite 原子提交决定旧或新 revision，不存在半发布；
- GC/VACUUM 在发布事务外执行；旧 Catalog/Overlay projection revision 受游标租约和最小保留策略保护。

该选择明确接受较长候选构建和较高临时空间，以换取极短、稳定且与变化比例无关的 publication。实现可以使用结构共享或分区复用降低完整快照构建成本，但对查询层必须表现为完整 revision 元组，不能重新引入混合代次。

## Catalog revision 与 Overlay projection revision

内部查询快照由不可分割的二元组组成：

```text
(catalog_revision, overlay_projection_revision)
```

对外只使用不可猜测、不可拆分的 `query_publication_id` 引用该二元组。`catalog.db` 的 publication 记录至少保存 `query_publication_id`、两类 revision、control Overlay 输入水位、创建时间和状态；只有同一发布事务成功写入的合法组合才能获得 ID。客户端不得提交两个裸 revision 任意组合，公共 API、游标、WebSocket publication 事件和租约均以 `query_publication_id` 为选择句柄。两类原始 revision 可作为只读诊断元数据返回，但不能单独选择查询快照。

- `catalog_revision` 冻结 Source-derived 事实、ContentBlob/FileLocation 和来源侧关系；
- `overlay_projection_revision` 冻结基于某个确定 `catalog_revision` 与一组具备查询能力的 Overlay 输入生成的有效字段、可见性、关系、SortKey 和 FTS；它必须记录所依赖的 Catalog revision 与 control Overlay 输入水位；
- 新 Catalog 发布时，候选必须带有与之匹配的 Overlay projection，发布事务为该组合创建新 `query_publication_id` 并切换活动指针；
- Overlay 是否影响某次查询快照，取决于该事实是否参与**当前查询**的过滤、排序、搜索、可见性或集合判断，而不是字段的永久分类。Schema 只声明字段可参与哪些查询能力，query planner 生成本次 `overlay_dependency_set`；
- 能进入任一查询依赖的 Overlay 写入不改写 `catalog_revision`，而是异步构建新的 `overlay_projection_revision`。新 projection publication 为同一 Catalog revision 创建新的 `query_publication_id`；未依赖该字段的当前查询无需把它当作集合变化；
- 仅作为当前响应附加展示、未进入 `overlay_dependency_set` 的值可以从 `control.db` 实时读取，但不得反向改变当前页的集合和顺序；
- 任一 Overlay projection 只能与其声明的 Catalog revision 配对。不存在“新 Catalog + 旧投影”或“一页内切换投影”的合法读路径。

## Overlay 写入、投影与读己之写

Overlay 写入采用“**control 同步提交、查询投影异步发布**”：

1. 写请求在 `control.db` 同步提交不可重建事实并取得单调 `overlay_fact_version`；响应和直接 Overlay/实体读取立即体现新值。
2. 若字段具备查询能力，创建或合并可重试的重投影 Job；默认列表查询可以继续读取旧 `query_publication_id`，不得把 control 新值临时拼入集合判断。
3. 需要列表/搜索读己之写的客户端携带服务端返回的 `after_overlay_fact_version` 屏障，或等待对应 Job/WS 事件。服务端只能返回覆盖该水位的 `query_publication_id`；在有界等待内未完成时返回稳定的 retryable pending/failed code，不能静默返回旧集合。
4. 每次 publication 记录覆盖到的 Overlay 输入水位。连续修改按水位合并；过时候选标为 superseded，发布 CAS 必须阻止较旧水位在较新水位之后发布。重试幂等，不回滚已经提交的 Overlay 事实。
5. 投影失败时活动 `query_publication_id` 保持不变，事实仍可直接读取；状态明确区分 saved/pending/failed/superseded/published，并提供 job ID、失败 code 和重试入口。

前端可以在编辑控件和实时 `userState` 中乐观显示已保存值，但必须展示“正在更新列表/更新失败”，不得自行插入、删除或重排当前页。收到包含新 `query_publication_id` 的 publication 事件后，从第一页刷新依赖该 Overlay 的查询。

## Job 与 publication 的事实关系

两个数据库不具备跨库原子事务，采用可恢复 Saga：

```text
control: Job queued → running → publishing
catalog: candidate(job_id) → publication(query_publication_id, catalog_revision, overlay_projection_revision)
control: Job projection → completed(query_publication_id)
startup: reconcile publications ↔ jobs
```

**Catalog publication 是扫描成功的权威事实**，因为它决定读者实际看到什么。`control.db.jobs` 是工作流和运维投影，不得反过来伪造 publication。

## 启动 reconciliation

| 观察到的状态 | 启动处理 |
| --- | --- |
| Catalog 已发布，Job 仍 running/publishing | 将 Job 修正为 completed，记录 query publication 和恢复事件 |
| Job completed，但 Catalog 无对应 publication | 标记 `needs_repair / CATALOG_PUBLICATION_MISSING`，不得暴露候选或伪造成功 |
| 候选存在但未发布 | 标记失败/中止，按保留策略回收 |
| 活动 revision 完整，后处理失败 | Catalog 保持 completed；创建独立可重试 DerivedAsset Job |
| 外部搜索投影落后 | 标记 `search_pending`；v1 同库 FTS 不应出现此状态 |

Job 是逻辑任务，Attempt 是该 Job 的一次实际执行。重试保持原 Job ID、增加 attempt number、保留全部历史 Attempt，并沿用请求、幂等身份和规则快照；`retry_of` 只保留为 v18 及更早子 Job 的兼容来源字段，新重试不再创建子 Job。只有 retryable 终态可重试，取消、完成和超过最大次数的 Job 不会自动重试。

**Candidate 归逻辑 Job 所有，不归某次 Attempt 所有**：同一 `job_id` 任一时刻至多一个 `catalog_revisions` 候选行；多个 Attempt 共同推进或从头重建该候选，而不是各自拥有独立候选。`BeginCandidate(jobID, sourceID, watermark)` 是幂等恢复入口，调用前先按 `job_id` 查询既有候选状态：不存在则正常创建；存在且为 `staging`（进行中）或 `aborted`（已被前一次 Attempt 或 Reconcile 标记中止）则在同一事务内先删除该行（外键级联清理全部从属 Source-derived 事实与查询投影，并显式清理不受外键管理的 FTS5 `work_search` 行），再插入全新 revision，不复用可能部分写入的 staging 结果；存在且已 `published` 说明该 Job 已经真正完成发布、只是 control 侧尚未收到 `completed`（见下方启动 reconciliation），此时必须拒绝再次构建或再次发布，只允许调用方按已有 publication 对账为 completed。`catalog_revisions.job_id`、`query_publications.job_id` 均保持扁平 `UNIQUE`：前者表达"同一 Job 任一时刻至多一行候选"（由 `BeginCandidate` 的幂等重置维护），后者表达"同一 Job 最终最多一个 publication"。

## Catalog schema 新增查询快照列的升级回填

forward-only migration 给 `work_projections`/`media_projections` 等既有表新增查询相关快照列（例如 `favorite`/`progress`/`search_*_norm`）时，只能用 `ALTER TABLE ADD COLUMN` 的静态默认值填充已发布 revision 的既有行；这些默认值对已经存在的用户事实和 Source-derived 文本而言通常是错误的（例如已收藏作品的 `favorite` 被回填成 `0`），必须在服务真正开始对外提供查询之前收敛，不能依赖用户此后恰好触发一次无关的写入才顺带修复。

标准做法是复用既有 Overlay 投影管线触发一次不改变任何 `work_overlays` 事实、只重建当前 active revision 整个查询投影的 Job（`overlay.Service.TriggerReprojection`）：`ApplyOverlayFacts` 本身已经对 revision 内每一个 Work 重新计算这些字段——快照类 Overlay 字段（如 `favorite`/`progress`）的权威来源是 control.db 的既有事实，可从中安全重新计算；纯文本派生字段（如 `search_*_norm`）的权威来源是同一 revision 里已经存在的 `source_works` 原始文本，同样可以安全重新计算，不需要重新扫描、也不伪造任何无法从既有数据推导的事实。启动流程在完成迁移与既有 Job/Overlay reconciliation 之后、开始监听服务请求之前，检查是否已经为本次新增列完成过这次回填（持久标记）；没有 active publication（全新安装）视为无需回填。

持久标记表达的是"这次回填已经确认完成"，不是"已经排队"或"已经尝试过"：`EnqueueOverlayProjectionTx` 可能把这次请求合并到一个既有的 `queued`/`running`/`publishing` overlay_projection Job（例如与另一个无关的 Overlay 写入排队竞争、或是上一次进程崩溃遗留的行）而不是新建一个，这种情况下不能因为"Job 已经存在"就提前写入完成标记；启动流程必须先把这个 Job（无论是否本次创建）驱动到真正的 `completed`——沿用既有 `Retry`/`Execute`/`ReconcileAttempts` 收敛陈旧租约与可重试失败，不新增等待或超时语义——才允许写入标记。若该 Job 排队、执行或重试期间遇到不可恢复的失败，启动本身失败并保持标记未写入，不得把"曾经触发"误当作"已经完成"而放行服务；下一次启动会重新观察到未回填、重新执行整个流程。这一模式不引入第二套 Job/状态机：完成判定完全基于既有 Job 状态机的终态，中断后由既有 Job 恢复循环正常收敛，重复触发会与既有非终态 Job 安全合并。

## 取消、崩溃和离线

- **构建中取消/崩溃**：旧 revision 元组继续服务；候选按 job ID 清理。
- **发布事务中崩溃**：重启后只能看到旧或新的完整 revision 元组，由 SQLite 原子性决定。
- **发布后 control 更新前崩溃**：reconciliation 补写 completed。
- **Source 暂不可达**：本次扫描失败或标记离线，不发布“全部删除”的 revision。
- **部分作品规则错误**：按规则配置的必需性隔离作品并生成结构化 issue；是否允许带 issue 发布必须由 Source policy 明确，默认身份字段错误阻塞该 Source publication。
- **DerivedAsset 失败**：不回滚 Catalog；媒体仍可使用原文件或占位。

## 增量与 Watcher

- Watcher 事件只是“可能变化”的提示，不是事实源；事件丢失必须由周期校验扫描收敛。
- 当前平台 adapter 提供只读 polling Watcher fallback，正式 bootstrap 默认周期为五分钟而非高频全树遍历；Watcher Manager 动态发现新增、删除和根变更 Source，channel 关闭或错误时标记 dirty/unavailable 并退避重启。它只更新 Source dirty/overflow 状态，不直接发布 Catalog；周期收敛负责在线/离线、事件丢失、失败重试、重复扫描抑制和当前 Job 关联，真实 OS watcher 与网络挂载行为另按平台门禁验证。
- 增量扫描可复用未变化 Source 分区或候选，但最终仍发布完整查询 revision。
- 目录签名精度必须到规则可观察的容器层；规则、metadata 或内容身份变化必须使相关候选失效。
- SourceRuleBinding 的 RuleVersion 或影响索引的参数变化必须经过 RuleImpact 决定重扫范围，不能靠 UI 猜测。
- 新扫描或新 RuleVersion 下 SourceWork 结构发生拆分（一个原 SourceWork 的媒体分散到多个新 SourceWork）或合并（多个原 SourceWork 的媒体汇聚到一个新 SourceWork）时，扫描按稳定来源证据（ContentBlob digest 集合关系）检测，无法安全自动处理即复用 Binding issue（`SOURCE_WORK_SPLIT/MERGE_REVIEW_REQUIRED`）阻塞该 Source publication，等待人工决策；不得据结构变化静默改写 Canonical 用户事实。语义详见 [领域模型与数据所有权](03-领域模型与数据所有权.md#sourcework-拆分与合并)。

## 任务模型

所有长操作统一为持久 Job：扫描、Catalog GC/VACUUM、Blob 完整哈希、缩略图、转码、备份/恢复校验和大规模 Overlay 重投影。Job 至少提供：

- 稳定 job ID、attempt、类型、资源范围、创建者和 capability 上下文；
- queued/running/publishing/completed/failed/cancelled/needs_repair 状态；对外还可观察 `cancelling`/`superseded` 语义，数据库历史状态保持向后兼容并由应用层映射；
- 单调进度序号、阶段、可取消性和结构化 issue；
- 重试关系、开始/结束时间、关联 publication/revision；
- 字节/实体进度、幂等键、心跳租约、retryable 失败和 attempt 明细；
- 规则扫描必须额外冻结 `RuleVersion.semantic_hash`、规范化参数和 `parameter_hash`、`rule_ir_hash`、compiler version、CEL Profile version、extension registry version；重试和恢复只复用该快照，不重新读取当前 SourceRuleBinding；
- 不含敏感绝对路径或 metadata 值的诊断摘要。

扫描、完整哈希、Overlay、DerivedAsset、外部工具和维护必须使用不同有界池；提交队列满时必须立即返回、清除内存 inflight 标记并让持久 Job 保持 queued，由低频 reconciliation 重提。中央恢复器周期回收过期 running Attempt，按持久 retry policy 和 `next_attempt_at` 在同一 Job 下建立新 Attempt，并覆盖所有任务类型。优先级、并发上限、租约和退避常量属于运行配置，不进入规则表达式。完整 Hash Job 只有在前后身份复核成功后才返回 ContentBlob，Source 扫描在此之前不发布受影响候选。

## revision 保留与 GC

- 活动 `query_publication_id` 引用的 Catalog/Overlay projection revision 永不被 GC；
- 有效游标签发短期租约，租约绑定 `query_publication_id` 并保留其完整 revision 元组；
- 达到最大保留时间或空间门槛后，旧游标返回 `CURSOR_EXPIRED`，不得无限保留快照；
- GC 先确认无 publication/租约引用，再删除候选和旧 Catalog/Overlay projection revision；
- VACUUM 是显式维护任务，必须可取消或安排维护窗口，不能包含在发布路径；GC 具备 active Job candidate 保护、dry-run 和由服务端按操作生成的保守空间预检。VACUUM、checkpoint、GC 与 Catalog publication 使用显式进程内维护锁，不以 SQLite 最终锁冲突代替编排；control restore 只在下次启动、持有 AppDirs 单写者锁且打开数据库之前执行。

## 验收指标

- Correctness Gate：构建、取消、进程强杀、发布前后强杀均不暴露混合 revision，Catalog 与 FTS 查询始终使用同一 revision 元组；
- Reference Performance Gate：在完整记录硬件、OS、SQLite、缓存状态、并发和样本的官方参考机上，百万作品、1%/10%/50% 变化时发布事务 P95 均小于 250 ms；
- reconciliation 对两种跨库不一致状态产生确定结果；
- Degradation Gate：HDD、SSD、网络盘的构建和维护窗口分别记录；慢设备可以更慢，但旧 revision 元组可浏览、任务可取消、有进度、空间不足时发布前失败，且不能改变一致性语义。

## 重新审议触发器

staging snapshot 保持默认方案，但下列任一条件在目标设备和正式数据上持续成立时，必须重新比较分区 snapshot、结构共享、copy-on-write 或新的 delta publication：

- staging 峰值空间不可接受，或空间预检无法可靠避免构建中耗尽磁盘；
- HDD/NAS 的构建、GC 或 VACUUM 形成不可接受的维护窗口；
- 未变化 Source/Catalog 分区无法有效复用，单 Source 小改动仍需复制整个 Catalog；
- 游标租约使旧快照长期无法回收；
- FTS 和完整查询投影使小改动也必须大规模重建；
- 具备查询能力的 Overlay 重投影频率或规模超过 snapshot 模型承受范围。

触发重新评估不等于接受新方案。候选仍必须证明 publication 短且稳定、崩溃可恢复、旧 revision 元组可读，并且绝不暴露混合代次；原地更新不是回退选项。

当前 SSD 原型数字见 [验证记录](../证据/验证记录.md)。实现顺序和门禁见 [v1 实施计划](../指南/01-v1实施计划.md) 与 [测试与发布门禁](../指南/02-测试与发布门禁.md)。对应决策见 [ADR-003](../ADR/ADR-003-Catalog发布与恢复.md)。
