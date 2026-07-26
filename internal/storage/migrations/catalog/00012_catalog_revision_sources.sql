-- 查询授权需要读取 Catalog revision 的不可变 Source 成员集合，不能为每一页扫描全部
-- WorkProjection。每行代表一个参与该 revision 的 Source；Library 与 projection 一起冻结，
-- 历史游标不依赖 control 当前状态推断成员关系。
CREATE TABLE catalog_revision_sources (
    catalog_revision_id TEXT NOT NULL REFERENCES catalog_revisions(catalog_revision_id) ON DELETE CASCADE,
    source_id TEXT NOT NULL,
    library_id TEXT NOT NULL,
    PRIMARY KEY (catalog_revision_id, source_id)
) STRICT;

CREATE INDEX catalog_revision_sources_library_idx
ON catalog_revision_sources (catalog_revision_id, library_id, source_id);

-- DISTINCT 只允许出现在这次一次性迁移中。若历史 projection 把同一 revision 的同一
-- Source 映射到多个 Library，主键会拒绝有歧义的回填；不得用 MIN/OR IGNORE 静默掩盖。
INSERT INTO catalog_revision_sources (catalog_revision_id, source_id, library_id)
SELECT DISTINCT catalog_revision_id, source_id, library_id
FROM work_projections;

-- 原 scope 索引的 Library→Source 次序既不能支持 Source 驱动的授权子集，也没有把
-- hidden/sort key 连成 browse 顺序。替换为两棵专用索引：全允许与小 deny 继续使用既有
-- query_idx；小 allowed、显式 Source 和显式 Library 分别使用下列索引。Source 索引末尾
-- 携带 library_id，使发布门禁可沿同一 covering index 聚合不可变成员，不增加第三棵树。
-- 相对 v11 只净增一棵 WorkProjection B-tree。
DROP INDEX work_projections_scope_idx;

CREATE INDEX work_projections_source_query_idx
ON work_projections (
    catalog_revision_id,
    overlay_revision_id,
    source_id,
    hidden,
    sort_title_key,
    work_id,
    library_id
);

CREATE INDEX work_projections_library_query_idx
ON work_projections (
    catalog_revision_id,
    overlay_revision_id,
    library_id,
    hidden,
    sort_title_key,
    work_id
);
