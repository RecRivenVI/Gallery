-- 搜索窄候选投影：FTS5 先召回 work_search.rowid，分页前的授权、结构化过滤、Ranking、
-- 排序和 keyset 全部在这张窄表完成；只有 limit+1 行会回 work_projections 读取响应负载。
--
-- search_rowid 与 FTS5 文档 rowid 一一对应。它不是普通表 work_projections 的物理 rowid：
-- 后者可能被 VACUUM 改写，不能成为持久身份。稳定业务身份仍是 revision + work_id，复合
-- 外键保证 WorkProjection 删除时候选行级联删除；FTS5 虚表不接受外键，发布验证负责检查
-- rowid、业务键和事实三方一致。
CREATE TABLE work_search_candidates (
    search_rowid INTEGER PRIMARY KEY NOT NULL,
    catalog_revision_id TEXT NOT NULL,
    overlay_revision_id TEXT NOT NULL,
    work_id TEXT NOT NULL,
    source_id TEXT NOT NULL,
    source_key TEXT NOT NULL,
    library_id TEXT NOT NULL,
    tags_json TEXT NOT NULL,
    normalized_original_text TEXT NOT NULL,
    sort_title_key TEXT NOT NULL,
    published_at_ns INTEGER NOT NULL,
    hidden INTEGER NOT NULL CHECK (hidden IN (0, 1)),
    favorite INTEGER NOT NULL CHECK (favorite IN (0, 1)),
    progress REAL NOT NULL CHECK (progress >= 0 AND progress <= 1),
    search_title_norm TEXT NOT NULL,
    search_creator_norm TEXT NOT NULL,
    search_tags_norm TEXT NOT NULL,
    search_filenames_norm TEXT NOT NULL,
    FOREIGN KEY (catalog_revision_id, overlay_revision_id, work_id)
        REFERENCES work_projections(catalog_revision_id, overlay_revision_id, work_id) ON DELETE CASCADE,
    UNIQUE (catalog_revision_id, overlay_revision_id, work_id)
) STRICT;

-- 现有 FTS 文档原位取得 rowid；不重建 FTS，也不猜测 Source-derived 事实。历史孤儿 FTS 行
-- 没有对应 WorkProjection，不进入候选表，后续 publication 一致性门禁会 fail-closed。
INSERT INTO work_search_candidates
(search_rowid, catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id,
 tags_json, normalized_original_text, sort_title_key, published_at_ns, hidden, favorite, progress,
 search_title_norm, search_creator_norm, search_tags_norm, search_filenames_norm)
SELECT s.rowid, w.catalog_revision_id, w.overlay_revision_id, w.work_id, w.source_id, w.source_key,
       w.library_id, w.tags_json, w.normalized_original_text, w.sort_title_key, w.published_at_ns,
       w.hidden, w.favorite, w.progress,
       w.search_title_norm, w.search_creator_norm, w.search_tags_norm, w.search_filenames_norm
FROM work_search AS s
JOIN work_projections AS w
  ON w.catalog_revision_id=s.catalog_revision_id
 AND w.overlay_revision_id=s.overlay_revision_id
 AND w.work_id=s.work_id;

-- 旧 active publication 在启动时不会重新走 Candidate validator；迁移必须自己证明三方是
-- exact bijection，不能让历史孤儿/缺行被 INNER JOIN 静默跳过后以“较少的搜索结果”启动。
-- SQLite 的 RAISE() 只能用于 trigger，使用一次性 CHECK guard 让任何非零差异使整个 migration
-- 事务失败并回滚。重复业务键已由上面的 UNIQUE 约束直接拒绝。
CREATE TABLE work_search_candidate_migration_guard (
    invalid_count INTEGER NOT NULL CHECK (invalid_count = 0)
) STRICT;

INSERT INTO work_search_candidate_migration_guard (invalid_count)
SELECT abs(
    (SELECT count(*) FROM work_projections) -
    (SELECT count(*) FROM work_search_candidates)
) + abs(
    (SELECT count(*) FROM work_search) -
    (SELECT count(*) FROM work_search_candidates)
) + (
    SELECT count(*)
    FROM work_search AS s
    JOIN work_projections AS w
      ON w.catalog_revision_id=s.catalog_revision_id
     AND w.overlay_revision_id=s.overlay_revision_id
     AND w.work_id=s.work_id
    WHERE s.normalized_original_text IS NOT w.normalized_original_text
       OR s.cjk_bigram_token_text IS NOT w.cjk_bigram_token_text
       OR s.latin_trigram_token_text IS NOT w.latin_trigram_token_text
);

DROP TABLE work_search_candidate_migration_guard;
