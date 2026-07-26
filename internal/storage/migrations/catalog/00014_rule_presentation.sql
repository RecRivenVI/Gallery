-- 规则派生的展示事实进入 publication 快照。
--
-- 这两列都是 **Source-derived** 事实，随重扫重新计算，与用户 Overlay 严格分离：
--
--   * media_projections.rule_hidden 表达「规则说这个文件默认不展示」（hidden_name_globs、
--     condition{effect:hide}）。它不能复用既有的 hidden 列——那一列是用户 Overlay 的
--     隐藏事实，属于不可被重扫覆盖的用户事实。两者合并会让重扫抹掉用户的隐藏选择，或者
--     让用户取消隐藏后规则隐藏一并失效。
--
--   * work_projections.badges_json 表达规则派生的角标序列（R-18、AI 生成、图片、视频）。
--     角标完全由规则与该作品的 metadata/媒体构成决定，因此属于 Catalog 可重建事实，不进
--     control.db。存为规范 JSON 数组，顺序即展示顺序（规则按 order 再按 id 排好）。
--
-- 历史 revision 没有这些事实：rule_hidden 保持 0、badges_json 保持空数组。它们只能通过
-- 重扫精确恢复，不做近似回填——凭旧投影猜测规则结论会产生与规则不一致的展示事实。
ALTER TABLE media_projections ADD COLUMN rule_hidden INTEGER NOT NULL DEFAULT 0 CHECK (rule_hidden IN (0, 1));

ALTER TABLE work_projections ADD COLUMN badges_json TEXT NOT NULL DEFAULT '[]';
