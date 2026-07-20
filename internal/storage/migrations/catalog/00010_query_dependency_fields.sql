-- 阶段 4 Correctness 收尾：把 Favorite/Progress 纳入查询快照投影，使它们能像
-- Hidden/TitleOverride 一样参与过滤与排序（snapshot 语义），同时新增可重建的字段化
-- 搜索投影列，取代仅靠 normalized_original_text 首个换行段近似恢复标题边界的做法，
-- 为 Creator/Tag/文件名字段级 ranking 与高亮提供稳定、独立的比较列。

ALTER TABLE work_projections ADD COLUMN favorite INTEGER NOT NULL DEFAULT 0;
ALTER TABLE work_projections ADD COLUMN progress REAL NOT NULL DEFAULT 0;

-- search_*_norm 是可重建的规范化字段投影：title/creator 各为单值，tags/filenames 为
-- 多值列表，以 U+001F（INFORMATION SEPARATOR ONE，普通用户文本不可输入）连接，前后
-- 各补一个分隔符，使 SQL 侧可以用 instr() 判断"某个具体取值"是否整体相等或作为前缀，
-- 不需要转义 LIKE 通配符。
ALTER TABLE work_projections ADD COLUMN search_title_norm TEXT NOT NULL DEFAULT '';
ALTER TABLE work_projections ADD COLUMN search_creator_norm TEXT NOT NULL DEFAULT '';
ALTER TABLE work_projections ADD COLUMN search_tags_norm TEXT NOT NULL DEFAULT '';
ALTER TABLE work_projections ADD COLUMN search_filenames_norm TEXT NOT NULL DEFAULT '';
