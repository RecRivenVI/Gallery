-- location_status 此前借用 'located_unverified' 值同时表达"位置可用性"与"内容确认状态"，
-- 与规范要求的正交语义冲突。media_projections 新增独立的 content_verification_state 和
-- verified_at，location_status 恢复为只表达位置可用性（present/offline/missing/inaccessible）。
ALTER TABLE media_projections ADD COLUMN content_verification_state TEXT NOT NULL DEFAULT 'content_verified'
    CHECK (content_verification_state IN ('located_unverified', 'content_verified'));
ALTER TABLE media_projections ADD COLUMN verified_at INTEGER;

-- 数据升级：把历史上借用 location_status='located_unverified' 表达的行迁移为
-- content_verification_state='located_unverified' 且 location_status='present'（文件位置
-- 本身是存在的，只是内容尚未确认），其余行保持 content_verified 并从 source_media 回填
-- 既往真实确认时间。
UPDATE media_projections SET content_verification_state = 'located_unverified'
WHERE location_status = 'located_unverified';

UPDATE media_projections SET location_status = 'present'
WHERE location_status = 'located_unverified';

UPDATE media_projections SET verified_at = (
    SELECT sm.last_confirmed_at FROM source_media sm
    WHERE sm.catalog_revision_id = media_projections.catalog_revision_id
      AND sm.source_id = media_projections.source_id
      AND sm.source_key = media_projections.source_key
)
WHERE content_verification_state = 'content_verified';
