-- FTS5 默认 crisismerge=16 会在某次普通 DELETE 达到阈值时立即合并同层全部
-- segment；500k × 多 revision 的真实 GC 可因此产生分钟级单写事务。Gallery 的
-- Catalog GC 自 v21 起只在分批删除窗口临时关闭 automerge/提高 crisismerge，并在
-- 删除后显式执行分页 merge；正常运行继续保持 SQLite 默认的 4/16，使日常写入仍
-- 有增量合并与应急合并两层保险。
INSERT INTO work_search(work_search, rank) VALUES('automerge', 4);
INSERT INTO work_search(work_search, rank) VALUES('crisismerge', 16);

-- overlay_revision_id 在父表中全局唯一，现有 child key 却都以 catalog_revision_id
-- 开头。SQLite 执行 overlay root 的 ON DELETE 检查时不能反向使用这些复合索引，
-- 500k × 10 revision 会为每个 root 扫描数百万剩余行。补齐单列前缀索引，既服务
-- 外键检查/级联，也避免 GC 已显式清空目标 child 后仍出现分钟级 root DELETE。
CREATE INDEX work_projections_overlay_gc_idx
ON work_projections (overlay_revision_id);

CREATE INDEX media_projections_overlay_gc_idx
ON media_projections (overlay_revision_id);

CREATE INDEX aggregate_cover_projections_overlay_gc_idx
ON aggregate_cover_projections (overlay_revision_id);
