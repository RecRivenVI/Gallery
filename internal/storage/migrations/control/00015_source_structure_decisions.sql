-- SourceWork 拆分/合并复用现有 binding_issues 体系承载人工审查：新增一个 structure_kind 判别列区分
-- 普通 Binding 冲突与结构（拆分/合并）审查。issue 的 resolution 汇总仍复用既有取值（bind_existing /
-- create_new / keep_separate / dismissed），精确的拆分/合并动作与溯源保存在 source_structure_decisions，
-- 因此无需放宽 CHECK 或重建 binding_issues，避免破坏 binding_issue_candidates 的外键。
-- 此处不冻结任何唯一约束，仅扩展审查与决策记录。

ALTER TABLE binding_issues ADD COLUMN structure_kind TEXT
    CHECK (structure_kind IS NULL OR structure_kind IN ('split', 'merge'));

CREATE INDEX binding_issues_structure_idx
ON binding_issues (source_id, structure_kind, status, source_key)
WHERE structure_kind IS NOT NULL;

-- source_structure_decisions 保存一次拆分或合并的人工决策。它以稳定来源事实（origin/新 source_key、
-- origin CanonicalWork ID）表达，不引用 Catalog row ID；扫描据此 pre-seed 的 Binding 复用既有解析
-- 机制，Catalog 全量重建后可依据本表重放。action 记录精确动作，version 支持乐观并发与撤销状态。
CREATE TABLE source_structure_decisions (
    decision_id TEXT PRIMARY KEY NOT NULL,
    issue_id TEXT NOT NULL REFERENCES binding_issues(issue_id) ON DELETE RESTRICT,
    source_id TEXT NOT NULL REFERENCES sources(source_id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('split', 'merge')),
    action TEXT NOT NULL CHECK (action IN (
        'split_inherit', 'split_keep_same', 'split_create_new',
        'merge_bind_existing', 'merge_create_new')),
    fingerprint TEXT NOT NULL,
    origin_source_keys TEXT NOT NULL DEFAULT '[]',
    origin_work_ids TEXT NOT NULL DEFAULT '[]',
    new_source_keys TEXT NOT NULL DEFAULT '[]',
    target_source_key TEXT NOT NULL DEFAULT '',
    target_work_id TEXT NOT NULL DEFAULT '',
    decided_by TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('applied', 'undone')),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    undone_at INTEGER
) STRICT;

CREATE INDEX source_structure_decisions_source_idx
ON source_structure_decisions (source_id, status, kind, created_at, decision_id);

CREATE UNIQUE INDEX source_structure_decisions_fingerprint_idx
ON source_structure_decisions (source_id, fingerprint)
WHERE status = 'applied';

-- 记录决策 pre-seed 的每一条 Binding，供撤销时精确回退。seed_kind 区分继承（seed_inherit，inactive
-- 指向 target CanonicalWork）与排除（seed_exclude，manual_unbound 排除某 CanonicalWork）。
CREATE TABLE source_structure_decision_bindings (
    decision_id TEXT NOT NULL REFERENCES source_structure_decisions(decision_id) ON DELETE RESTRICT,
    entity_type TEXT NOT NULL CHECK (entity_type IN ('work', 'media', 'creator')),
    binding_id TEXT NOT NULL,
    seed_kind TEXT NOT NULL CHECK (seed_kind IN ('seed_inherit', 'seed_exclude')),
    PRIMARY KEY (decision_id, entity_type, binding_id)
) STRICT;

CREATE INDEX source_structure_decision_bindings_binding_idx
ON source_structure_decision_bindings (binding_id, decision_id);
