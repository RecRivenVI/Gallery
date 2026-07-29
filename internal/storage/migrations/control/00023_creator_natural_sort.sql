-- 用户侧 Creator 浏览必须与查询排序协议 v2 使用同一 NaturalSortKey，不能依赖 SQLite
-- 默认 BINARY collation。排序键是 name 的可重建派生值；迁移先加入列并登记旧编码，
-- storage.Open 会在服务对外可用前以 Go 权威实现同步回填，再把版本推进到 2。
ALTER TABLE canonical_creators ADD COLUMN sort_name_key TEXT NOT NULL DEFAULT '';

CREATE INDEX canonical_creators_sort_idx
ON canonical_creators (sort_name_key, creator_id);

CREATE INDEX canonical_creators_live_sort_idx
ON canonical_creators (sort_name_key, creator_id)
WHERE merged_into IS NULL;

INSERT INTO gallery_control_meta (key, value)
VALUES ('creator_natural_sort_key_encoding', '1');
