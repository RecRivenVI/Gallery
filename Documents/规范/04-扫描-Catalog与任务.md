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

发现阶段只读 Source。相对路径、FileID/inode、大小和快速指纹用于判断是否需要进一步读取；新内容或冲突候选在建立 ContentBlob 时必须由持久哈希任务完成完整 SHA-256。首次哈希成功前 SourceMedia 保持 staging `hash_pending`，默认阻塞受影响 Source publication；超大文件或网络盘只延长带进度、可取消的候选构建，不能降低身份强度。mtime 不得成为实体身份或唯一变化依据。具体失败、重试和续算语义见 [文件系统与媒体处理](08-文件系统与媒体处理.md)。

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

重试创建新的 Job/attempt，保留 `retry_of`；幂等键防止同一 Source/目标重复并发执行，但不复用失败 Job 的身份。

## 取消、崩溃和离线

- **构建中取消/崩溃**：旧 revision 元组继续服务；候选按 job ID 清理。
- **发布事务中崩溃**：重启后只能看到旧或新的完整 revision 元组，由 SQLite 原子性决定。
- **发布后 control 更新前崩溃**：reconciliation 补写 completed。
- **Source 暂不可达**：本次扫描失败或标记离线，不发布“全部删除”的 revision。
- **部分作品规则错误**：按规则配置的必需性隔离作品并生成结构化 issue；是否允许带 issue 发布必须由 Source policy 明确，默认身份字段错误阻塞该 Source publication。
- **DerivedAsset 失败**：不回滚 Catalog；媒体仍可使用原文件或占位。

## 增量与 Watcher

- Watcher 事件只是“可能变化”的提示，不是事实源；事件丢失必须由周期校验扫描收敛。
- 增量扫描可复用未变化 Source 分区或候选，但最终仍发布完整查询 revision。
- 目录签名精度必须到规则可观察的容器层；规则、metadata 或内容身份变化必须使相关候选失效。
- SourceRuleBinding 的 RuleVersion 或影响索引的参数变化必须经过 RuleImpact 决定重扫范围，不能靠 UI 猜测。

## 任务模型

所有长操作统一为持久 Job：扫描、Catalog GC/VACUUM、Blob 完整哈希、缩略图、转码、备份/恢复校验和大规模 Overlay 重投影。Job 至少提供：

- 稳定 job ID、attempt、类型、资源范围、创建者和 capability 上下文；
- queued/running/publishing/completed/failed/cancelled/needs_repair 状态；
- 单调进度序号、阶段、可取消性和结构化 issue；
- 重试关系、开始/结束时间、关联 publication/revision；
- 不含敏感绝对路径或 metadata 值的诊断摘要。

扫描、完整哈希和 ffmpeg 必须使用不同有界池；优先级和并发上限属于运行配置，不进入规则表达式。

## revision 保留与 GC

- 活动 `query_publication_id` 引用的 Catalog/Overlay projection revision 永不被 GC；
- 有效游标签发短期租约，租约绑定 `query_publication_id` 并保留其完整 revision 元组；
- 达到最大保留时间或空间门槛后，旧游标返回 `CURSOR_EXPIRED`，不得无限保留快照；
- GC 先确认无 publication/租约引用，再删除候选和旧 Catalog/Overlay projection revision；
- VACUUM 是显式维护任务，必须可取消或安排维护窗口，不能包含在发布路径。

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
