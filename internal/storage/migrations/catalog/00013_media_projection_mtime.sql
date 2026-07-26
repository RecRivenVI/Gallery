-- 媒体正文读取从「每请求整文件复制到 AppDirs 并复算完整 SHA-256」改为流式区间读取
-- （见 ADR-010）。流式读取必须能在发送任何字节之前判定「已发布的 ContentBlob 是否仍然
-- 成立」，否则内容变化只能在响应中途被发现并以中断连接告知客户端。
--
-- 判定所需的 mtime 证据必须来自 publication 快照本身：读取时回查 source_media 会跨越
-- 快照边界，也无法保证读到的 mtime 与该 revision 的 media_projections 属于同一代次。
-- 因此把发布时刻的 mtime 固化进 media_projections，与 size/algorithm/digest 一起构成
-- 同一代次的完整身份证据。
ALTER TABLE media_projections ADD COLUMN mtime_ns INTEGER NOT NULL DEFAULT 0;

-- source_media 与 media_projections 同为 revision 内事实，且 (catalog_revision_id,
-- source_id, source_key) 是 source_media 的主键，因此回填是精确一对一映射，不需要
-- DISTINCT/MIN 这类会掩盖歧义的收敛。找不到对应行时保留 0：读取端把 0 解释为「该
-- revision 没有发布 mtime 证据」并只退回到 size 与整文件 digest 复算，不伪造证据，也
-- 不因此放行任何未经校验的字节。重扫后新 revision 自然携带精确 mtime。
UPDATE media_projections SET mtime_ns = COALESCE((
    SELECT m.mtime_ns FROM source_media m
    WHERE m.catalog_revision_id = media_projections.catalog_revision_id
      AND m.source_id = media_projections.source_id
      AND m.source_key = media_projections.source_key
), 0);
