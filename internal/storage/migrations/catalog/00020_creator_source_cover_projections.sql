-- Creator/Library 聚合封面的逐主体授权不能只保存全局胜出项：当胜出媒体所在 Source
-- 被 deny、落在 Token scope 外或缺少 media.read 时，请求必须在同一 publication 内
-- 回退到下一条获授权候选。请求期重新连接全部 WorkProjection 与 Creator 关系在 100k
-- 合成规模已超过交互预算，因此持久化每个 Creator 在每个 Source 内的唯一最佳候选。
--
-- 这仍是可重建的 publication 派生事实，不混入 control.db 授权。一个 Creator/Source
-- 无论有多少 Work 最多一行；work_id 保留全局 tie-break，授权查询可在任意 Source 子集
-- 上精确复现 `published_at_ns DESC, work_id DESC`。
ALTER TABLE aggregate_cover_projections ADD COLUMN source_id TEXT NOT NULL DEFAULT '';

-- 既有 publication 的全局聚合行必须能直接判定胜出媒体所属 Source。MediaProjection
-- 在一个 revision 内按 CanonicalMedia 唯一，因此这里的回填没有路径猜测或当前态依赖。
UPDATE aggregate_cover_projections AS a
SET source_id=COALESCE((
    SELECT m.source_id
    FROM media_projections AS m
    WHERE m.catalog_revision_id=a.catalog_revision_id
      AND m.overlay_revision_id=a.overlay_revision_id
      AND m.media_id=a.cover_media_id
), '');

CREATE TABLE creator_source_cover_projections (
    catalog_revision_id TEXT NOT NULL,
    overlay_revision_id TEXT NOT NULL,
    creator_id TEXT NOT NULL,
    source_id TEXT NOT NULL,
    cover_media_id TEXT NOT NULL,
    published_at_ns INTEGER NOT NULL DEFAULT 0,
    work_id TEXT NOT NULL,
    PRIMARY KEY (catalog_revision_id, overlay_revision_id, creator_id, source_id),
    FOREIGN KEY (catalog_revision_id, overlay_revision_id, creator_id)
        REFERENCES creator_projections(catalog_revision_id, overlay_revision_id, creator_id) ON DELETE CASCADE,
    FOREIGN KEY (catalog_revision_id, overlay_revision_id, cover_media_id)
        REFERENCES media_projections(catalog_revision_id, overlay_revision_id, media_id) ON DELETE CASCADE
) STRICT;

WITH ranked AS (
    SELECT r.catalog_revision_id,
           r.overlay_revision_id,
           r.creator_id,
           w.source_id,
           w.cover_media_id,
           w.published_at_ns,
           w.work_id,
           row_number() OVER (
               PARTITION BY r.catalog_revision_id, r.overlay_revision_id, r.creator_id, w.source_id
               ORDER BY w.published_at_ns DESC, w.work_id DESC
           ) AS rank_in_scope
    FROM work_creator_relations AS r
    JOIN work_projections AS w
      ON w.catalog_revision_id=r.catalog_revision_id
     AND w.overlay_revision_id=r.overlay_revision_id
     AND w.work_id=r.work_id
    WHERE w.cover_media_id<>''
)
INSERT INTO creator_source_cover_projections
(catalog_revision_id, overlay_revision_id, creator_id, source_id,
 cover_media_id, published_at_ns, work_id)
SELECT catalog_revision_id, overlay_revision_id, creator_id, source_id,
       cover_media_id, published_at_ns, work_id
FROM ranked WHERE rank_in_scope=1;

-- 小 allowed 集合从 Source 驱动，只读取允许平台内的窄候选。
CREATE INDEX creator_source_cover_source_idx
ON creator_source_cover_projections (
    catalog_revision_id,
    overlay_revision_id,
    source_id,
    creator_id,
    published_at_ns DESC,
    work_id DESC,
    cover_media_id
);

-- 小 deny 集合按 Creator 与正式 tie-break 顺序流式读取，避免再次建立 Work/关系窗口。
CREATE INDEX creator_source_cover_rank_idx
ON creator_source_cover_projections (
    catalog_revision_id,
    overlay_revision_id,
    creator_id,
    published_at_ns DESC,
    work_id DESC,
    source_id,
    cover_media_id
);
