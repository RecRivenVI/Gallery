# ADR-003 Catalog 发布与恢复

- 状态：接受
- 日期：2026-07-16
- 规范：[扫描、Catalog 与任务](../规范/04-扫描-Catalog与任务.md)

## 问题

扫描可能持续很久，用户会在扫描期间浏览和翻页。系统还必须处理取消、进程崩溃、Catalog/control 两库不一致、搜索索引同步和旧快照 GC。默认提交模型需要避免长发布锁和混合代次。

## 决策

默认使用完整 staging snapshot 分批长期构建，完成后在短 SQLite 事务中为合法 `(catalog_revision, overlay_projection_revision)` 创建不可拆分的 `query_publication_id` 并切换 active publication。SourceWork/SourceCreator/SourceMedia、ContentBlob/FileLocation 和来源关系属于同一 `catalog_revision`；WorkProjection/CreatorProjection/MediaProjection、有效字段、SortKey 和同库 FTS5 还绑定匹配的 `overlay_projection_revision`。CanonicalWork/CanonicalCreator/CanonicalMedia 仍以 `control.db` 为权威，不作为 Catalog revision 的权威副本。

该选择接受较长候选构建和较高临时空间，以换取极短、稳定且与变化比例无关的 publication。具备查询能力的 Overlay 采用 control 同步写入、投影异步发布；每个投影绑定确定 Catalog revision 和单调 Overlay 输入水位。对外只允许通过 `query_publication_id` 选择合法组合，不能组合裸 revision。

跨库使用可恢复 Saga：control 创建 Job，Catalog 候选记录 job ID，Catalog publication 是扫描成功事实源，随后 control 投影 completed。启动 reconciliation 修复已发布但 Job 未完成的状态；Job completed 而无 publication 时标记 needs_repair。

**媒体/DerivedAsset 读取同样只能通过 `query_publication_id` 选择合法组合**（2026-07-20 阶段 4 Correctness 收尾明确）：此前媒体详情、正文、按需内容确认 Job 与 DerivedAsset 创建都硬编码解析"当前 active publication"，客户端凭旧 cursor 拿到的 Work 可能在后台新 publication 发布后读到另一代媒体事实，与本 ADR"对外只允许通过 `query_publication_id` 选择合法组合"的既有决策不一致。修正后这些端点接受可选 `queryPublicationId`：省略为 current 模式（读当前 active，永不被 GC，不需要额外租约）；显式提供为 snapshot 模式，复用既有游标租约表与 GC 保护判据为该请求建立短期 publication 读取租约，不存在或已过期返回 `CURSOR_EXPIRED`。DerivedAsset 输入 ContentBlob 在创建时从该快照解析并冻结，不在异步生成期间改用之后的 active publication。

**Candidate 归逻辑 Job 所有**（2026-07-20 由 EV-28 明确）：同一 `job_id` 任一时刻至多一个未发布候选，同一 Job 的多个 Attempt 共同推进或从头重建该候选，而不是各自持有独立候选。`BeginCandidate` 是幂等恢复入口：既有候选为 `staging`/`aborted` 时先删除（外键级联清理从属事实，显式清理不受级联管理的 FTS5 `work_search`）再重建；已 `published` 时拒绝重建，交由调用方按已有 publication 对账为 completed。`catalog_revisions.job_id`、`query_publications.job_id` 两处扁平 `UNIQUE` 约束均保持不变，未引入 schema 迁移；曾评估的"Candidate 归 Attempt 所有"（`(job_id, attempt_number)` 唯一）因需要重建被十余张表级联/限制引用的核心表且相对既有"同一 Job 最终最多一个 publication"语义没有额外收益，未被采用。

## 理由

- 百万作品合成证据下，generation-delta 的发布事务随变化量增长，50% 变化达到 20.774 秒；staging 的单次 publication 观测为 3–8 毫秒；
- staging 构建期间读者持续读取旧 revision 元组，发布停顿可预测；
- Catalog 查询投影和 FTS 同 revision 元组，避免作品可见而搜索不可见；
- 两库无法原子提交，显式 Saga 比假定跨库事务更可恢复；
- 实测数字和局限见 [EV-04](../证据/验证记录.md#ev-04-catalog-提交e2) 与 [EV-08](../证据/验证记录.md#ev-08-跨库游标和-personal-契约e1)；Candidate 所有权模型的裁决过程见 [EV-28](../证据/验证记录.md)。

## 替代方案

- **generation-delta 默认**：构建快、变化小时空间较低，但发布事务随变化比例变长，不满足短发布门禁。
- **in-place + scan ID**：容易把批次中间态暴露给读者，恢复和清理复杂。
- **整次扫描单大事务**：写锁、WAL 和崩溃窗口不可接受。
- **外部事务协调器**：对本地单进程过重，仍不能提供真正跨 SQLite 文件原子性。

## 影响

- 后台构建需要额外磁盘空间和明确的空间预检；
- 旧 Catalog/Overlay projection revision 由游标租约保护，GC/VACUUM 成为独立维护任务；
- 增量优化可以复用分区，但查询语义仍是完整 revision；
- Job 不是成功事实源，所有恢复逻辑必须检查 publication；
- HDD 的构建和维护窗口是发布门禁，但不改变默认模型。

## 重新审议条件

下列条件可触发重新比较分区 snapshot、结构共享、copy-on-write 或新的 delta publication：

- staging 峰值空间在目标设备上不可接受，或空间预检无法可靠避免构建中耗尽磁盘；
- HDD/NAS 的候选构建、GC 或 VACUUM 形成不可接受的维护窗口；
- 未变化 Source/Catalog 分区无法有效复用，单 Source 小改动仍需复制整个 Catalog；
- revision 租约使旧快照长期无法回收；
- FTS/完整投影使每次小改动都大规模重建；
- 大规模、具备查询能力的 Overlay 重投影频率超过 snapshot 模型承受范围。

触发条件不自动推翻完整快照语义。任何替代方案仍须在完整关系与搜索下满足正式 Reference Performance Gate，并证明查询、空间、GC、取消和崩溃恢复不退化；不得重新采用会暴露混合代次的原地更新。
