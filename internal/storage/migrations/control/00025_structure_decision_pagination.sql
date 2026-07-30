-- Source 结构决策按 newest-first keyset 分页。无筛选、仅 Source 以及 Source+状态三条读取路径
-- 分别保留排序前缀，避免为了返回固定一页而排序全部历史决策。
CREATE INDEX source_structure_decisions_list_idx
ON source_structure_decisions (created_at DESC, decision_id DESC);

CREATE INDEX source_structure_decisions_source_list_idx
ON source_structure_decisions (source_id, created_at DESC, decision_id DESC);

CREATE INDEX source_structure_decisions_source_status_list_idx
ON source_structure_decisions (source_id, status, created_at DESC, decision_id DESC);
