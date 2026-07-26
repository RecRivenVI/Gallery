-- 规则派生的作品标量事实进入 publication 快照。
--
-- 这四列都是 **Source-derived** 事实，随重扫重新计算，与用户 Overlay 严格分离。它们此前在规则
-- 求值层被静默丢弃（编译期只校验 target 非空、求值期无兜底分支），十个真实来源夹具中八个声明了
-- description 与 source_url、Pawchive 另有 date，全部没有落地。
--
--   * description / source_url：来源自述的作品描述与原始链接。
--
--   * published_at_ns / published_at_raw / published_at_parser：作品发布时间的**三元组**。
--     《查询、搜索与排序》要求时间排序「必须先解析为明确 instant，并保留原始时间和解析规则版本」，
--     因此单个 INTEGER 列不满足规范：published_at_ns 供排序与聚合使用，published_at_raw 保留来源
--     原文使解析结果永远可被人工复核，published_at_parser 标识产生该时刻的解析规则版本——解析规则
--     一旦变化，历史投影里由旧规则得到的时刻必须能被识别出来并按需重扫，而不是让两代解析结果在同
--     一个排序里静默混用。
--
-- published_at_ns = 0 表示「该作品没有可用发布时间」，与「时间恰为 Unix 纪元」在语义上不区分；
-- 后者在真实媒体库中不会出现，而为它增加一个可空列会让每一处排序都要处理 NULL 三态。
--
-- 历史 revision 没有这些事实，且**不做任何回填**：凭旧投影猜测规则结论会产生与规则不一致的事实，
-- 而这些字段恰恰是「规则是 Source 差异的唯一解释入口」的产物。它们只能通过重扫精确恢复。
ALTER TABLE source_works ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE source_works ADD COLUMN source_url TEXT NOT NULL DEFAULT '';
ALTER TABLE source_works ADD COLUMN published_at_ns INTEGER NOT NULL DEFAULT 0;
ALTER TABLE source_works ADD COLUMN published_at_raw TEXT NOT NULL DEFAULT '';
ALTER TABLE source_works ADD COLUMN published_at_parser TEXT NOT NULL DEFAULT '';

ALTER TABLE work_projections ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE work_projections ADD COLUMN source_url TEXT NOT NULL DEFAULT '';
ALTER TABLE work_projections ADD COLUMN published_at_ns INTEGER NOT NULL DEFAULT 0;
ALTER TABLE work_projections ADD COLUMN published_at_raw TEXT NOT NULL DEFAULT '';
ALTER TABLE work_projections ADD COLUMN published_at_parser TEXT NOT NULL DEFAULT '';

-- 按发布时间排序需要与既有 sort_title_key 索引同构的 keyset 覆盖索引：末尾的 work_id 是查询协议
-- 固定的 tie-break，使同一时刻的作品有确定顺序，游标续页不漏项也不重复。
--
-- 相对 v14 净增两棵 work_projections B-tree（Library 维度与 Source 维度各一），与既有
-- work_projections_library_query_idx / work_projections_source_query_idx 的列序保持一致，
-- 只把 sort_title_key 换成 published_at_ns。
CREATE INDEX work_projections_library_published_idx
ON work_projections (
    catalog_revision_id,
    overlay_revision_id,
    library_id,
    hidden,
    published_at_ns,
    work_id
);

CREATE INDEX work_projections_source_published_idx
ON work_projections (
    catalog_revision_id,
    overlay_revision_id,
    source_id,
    hidden,
    published_at_ns,
    work_id,
    library_id
);
