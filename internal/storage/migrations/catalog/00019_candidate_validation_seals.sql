-- Candidate validation seal：把完整候选校验与短 publication 事务明确分离。
--
-- staging snapshot 的完整一致性校验、聚合封面重算可能扫描整个 revision；它们必须在
-- publication 指针切换之前完成，但不能留在目标 P95 <250 ms 的发布事务里。校验成功后
-- 写入本表；Publish/PublishOverlay 只确认匹配 revision 的封印、候选状态与 Overlay CAS，
-- 再创建 query_publication_id 并切换 active 指针。
--
-- 本表只保存可重建 Catalog 的校验结果，不是安全凭据。封印同时是候选完成标记：所有
-- 生产候选写路径必须在修改 projection/FTS/成员/关系的同一个 IMMEDIATE 事务中确认
-- 目标仍是匹配 Job/Source/watermark 的 staging candidate 且没有封印；验证成功后旧
-- Candidate 的任何内容写入一律拒绝，不能原地撤销封印后改写。启动期自然排序协议回填
-- 是唯一系统级例外，它会在同一事务中删除遗留封印并完成全表重建。
--
-- Overlay candidate 的基线同样必须是持久事实，不能只信任调用方携带的 Candidate，
-- 也不能在 Apply/Validate 时用当前 active publication 反推：后者可能已被另一个合法
-- Overlay candidate 推进。历史 revision 没有可恢复的创建基线，保留空字符串；只有
-- v19 起新建的 staging Overlay candidate 写入实际 base，Publish 再以 active CAS 判断漂移。
ALTER TABLE overlay_projection_revisions
ADD COLUMN base_overlay_revision_id TEXT NOT NULL DEFAULT '';

CREATE TABLE candidate_validation_seals (
    catalog_revision_id TEXT NOT NULL,
    overlay_revision_id TEXT NOT NULL,
    candidate_kind TEXT NOT NULL CHECK (candidate_kind IN ('catalog', 'overlay')),
    validation_version INTEGER NOT NULL CHECK (validation_version = 1),
    validated_at INTEGER NOT NULL,
    PRIMARY KEY (catalog_revision_id, overlay_revision_id),
    FOREIGN KEY (catalog_revision_id, overlay_revision_id)
        REFERENCES overlay_projection_revisions(catalog_revision_id, overlay_revision_id)
        ON DELETE CASCADE
) STRICT;
