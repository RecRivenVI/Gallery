-- 把“媒体已被发现”与“媒体内容已完整确认”解耦：source_media 新增观察证据与内容确认状态，
-- 使快速索引（index）能以 located_unverified 发布媒体，日常增量扫描（incremental）能据此
-- 与既往已确认观察比较、跳过未变化文件的完整 SHA-256；显式 verify 档案不受影响，
-- 继续对选定范围重新计算完整摘要。content_blobs/file_locations 结构不变：仅在内容
-- 实际完成确认（content_verification_state='content_verified'）时才为该媒体写入对应行，
-- 未确认媒体不进入这两张表，因此不影响既有 ValidateCandidate 的 digest 格式校验。
ALTER TABLE source_media ADD COLUMN mtime_ns INTEGER NOT NULL DEFAULT 0;
ALTER TABLE source_media ADD COLUMN platform_identity_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE source_media ADD COLUMN platform_identity_value TEXT NOT NULL DEFAULT '';
ALTER TABLE source_media ADD COLUMN container_signature TEXT NOT NULL DEFAULT '';
ALTER TABLE source_media ADD COLUMN content_verification_state TEXT NOT NULL DEFAULT 'content_verified'
    CHECK (content_verification_state IN ('located_unverified', 'content_verified'));
ALTER TABLE source_media ADD COLUMN last_confirmed_algorithm TEXT NOT NULL DEFAULT '';
ALTER TABLE source_media ADD COLUMN last_confirmed_digest TEXT NOT NULL DEFAULT '';
ALTER TABLE source_media ADD COLUMN last_confirmed_at INTEGER;

-- incremental 复用查询按 (catalog_revision_id, source_id, relative_path) 命中既往观察，
-- 真实规模下必须走索引，否则退化为逐文件全表扫描。
CREATE INDEX source_media_identity_idx
ON source_media (catalog_revision_id, source_id, relative_path);
