ALTER TABLE source_works
ADD COLUMN rule_cover_media_source_key TEXT NOT NULL DEFAULT '';

ALTER TABLE work_projections
ADD COLUMN rule_cover_media_id TEXT NOT NULL DEFAULT '';

ALTER TABLE work_projections
ADD COLUMN cover_media_id TEXT NOT NULL DEFAULT '';

-- 旧 Catalog 没有保存 cover_candidate 的选择结果，无法在不读取 Source 的前提下精确
-- 恢复。以下临时映射只各扫描一次既有 projection，再通过 WITHOUT ROWID 主键做有界
-- join/update；禁止为每个 Work 重跑一遍 media_projections 的相关子查询。
CREATE TEMP TABLE _gallery_rule_cover_source_map (
    catalog_revision_id TEXT NOT NULL,
    source_id TEXT NOT NULL,
    work_source_key TEXT NOT NULL,
    rule_cover_media_source_key TEXT NOT NULL,
    PRIMARY KEY (catalog_revision_id, source_id, work_source_key)
) WITHOUT ROWID;

-- explain:rule-cover-source-map-begin
INSERT INTO _gallery_rule_cover_source_map
(catalog_revision_id, source_id, work_source_key, rule_cover_media_source_key)
SELECT catalog_revision_id, source_id, work_source_key, media_source_key
FROM (
    SELECT w.catalog_revision_id,
           w.source_id,
           w.source_key AS work_source_key,
           m.source_key AS media_source_key,
           row_number() OVER (
               PARTITION BY w.catalog_revision_id, w.source_id, w.source_key
               ORDER BY m.base_ordinal, m.media_id, m.overlay_revision_id
           ) AS candidate_rank
    FROM work_projections AS w
    JOIN media_projections AS m
      ON m.catalog_revision_id = w.catalog_revision_id
     AND m.overlay_revision_id = w.overlay_revision_id
     AND m.work_id = w.work_id
     AND m.source_id = w.source_id
)
WHERE candidate_rank = 1;
-- explain:rule-cover-source-map-end

UPDATE source_works AS sw
SET rule_cover_media_source_key = r.rule_cover_media_source_key
FROM _gallery_rule_cover_source_map AS r
WHERE r.catalog_revision_id = sw.catalog_revision_id
  AND r.source_id = sw.source_id
  AND r.work_source_key = sw.source_key;

CREATE TEMP TABLE _gallery_rule_cover_work_map (
    catalog_revision_id TEXT NOT NULL,
    overlay_revision_id TEXT NOT NULL,
    work_id TEXT NOT NULL,
    rule_cover_media_id TEXT NOT NULL,
    PRIMARY KEY (catalog_revision_id, overlay_revision_id, work_id)
) WITHOUT ROWID;

-- explain:rule-cover-work-map-begin
INSERT INTO _gallery_rule_cover_work_map
(catalog_revision_id, overlay_revision_id, work_id, rule_cover_media_id)
SELECT w.catalog_revision_id, w.overlay_revision_id, w.work_id, min(m.media_id)
FROM work_projections AS w
JOIN _gallery_rule_cover_source_map AS r
  ON r.catalog_revision_id = w.catalog_revision_id
 AND r.source_id = w.source_id
 AND r.work_source_key = w.source_key
JOIN media_projections AS m
  ON m.catalog_revision_id = w.catalog_revision_id
 AND m.overlay_revision_id = w.overlay_revision_id
 AND m.work_id = w.work_id
 AND m.source_id = w.source_id
 AND m.source_key = r.rule_cover_media_source_key
GROUP BY w.catalog_revision_id, w.overlay_revision_id, w.work_id;
-- explain:rule-cover-work-map-end

CREATE TEMP TABLE _gallery_custom_cover_map (
    catalog_revision_id TEXT NOT NULL,
    overlay_revision_id TEXT NOT NULL,
    work_id TEXT NOT NULL,
    custom_cover_media_id TEXT NOT NULL,
    PRIMARY KEY (catalog_revision_id, overlay_revision_id, work_id)
) WITHOUT ROWID;

-- 旧实现可能在异常重放后留下多个 ordinal=-1；按 media_id 取确定性结果。
INSERT INTO _gallery_custom_cover_map
(catalog_revision_id, overlay_revision_id, work_id, custom_cover_media_id)
SELECT catalog_revision_id, overlay_revision_id, work_id, min(media_id)
FROM media_projections
WHERE ordinal = -1
GROUP BY catalog_revision_id, overlay_revision_id, work_id;

UPDATE work_projections AS w
SET rule_cover_media_id = r.rule_cover_media_id,
    cover_media_id = coalesce(c.custom_cover_media_id, r.rule_cover_media_id)
FROM _gallery_rule_cover_work_map AS r
LEFT JOIN _gallery_custom_cover_map AS c
  ON c.catalog_revision_id = r.catalog_revision_id
 AND c.overlay_revision_id = r.overlay_revision_id
 AND c.work_id = r.work_id
WHERE r.catalog_revision_id = w.catalog_revision_id
  AND r.overlay_revision_id = w.overlay_revision_id
  AND r.work_id = w.work_id;

-- 封面选择已经进入显式列，媒体顺序只表达内容顺序，并保持 OpenAPI 的非负契约。
UPDATE media_projections
SET base_ordinal = 0
WHERE base_ordinal < 0;

UPDATE media_projections
SET ordinal = base_ordinal
WHERE ordinal < 0;

DROP TABLE _gallery_custom_cover_map;
DROP TABLE _gallery_rule_cover_work_map;
DROP TABLE _gallery_rule_cover_source_map;
