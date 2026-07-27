-- 排序协议 v2：补齐发布日期与 Progress 的全范围/Library/Source keyset 索引，并登记
-- NaturalSortKey 文本段编码升级。索引属于可重建 Catalog；用户事实没有进入本迁移。
--
-- natural_sort_key_encoding=1 表示迁移后的既有行仍使用旧编码。storage.Open 会在服务开始
-- 提供查询前，按 title/name 从同一快照事实同步重算 work/creator 的排序键，然后原子改为 2。
-- 全新数据库没有历史行，也会走同一条空回填路径，避免新旧安装产生不同状态。
INSERT INTO gallery_catalog_meta (key, value) VALUES ('natural_sort_key_encoding', '1')
ON CONFLICT(key) DO NOTHING;

CREATE INDEX work_projections_published_idx
ON work_projections (
    catalog_revision_id,
    overlay_revision_id,
    hidden,
    published_at_ns,
    work_id
);

CREATE INDEX work_projections_progress_idx
ON work_projections (
    catalog_revision_id,
    overlay_revision_id,
    hidden,
    progress,
    work_id
);

CREATE INDEX work_projections_library_progress_idx
ON work_projections (
    catalog_revision_id,
    overlay_revision_id,
    library_id,
    hidden,
    progress,
    work_id
);

CREATE INDEX work_projections_source_progress_idx
ON work_projections (
    catalog_revision_id,
    overlay_revision_id,
    source_id,
    hidden,
    progress,
    work_id,
    library_id
);
