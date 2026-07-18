ALTER TABLE rule_versions ADD COLUMN package_id TEXT;
ALTER TABLE rule_versions ADD COLUMN status TEXT NOT NULL DEFAULT 'published';
ALTER TABLE rule_versions ADD COLUMN normalization_algorithm_version TEXT NOT NULL DEFAULT 'gallery-canonical-json-v1';
ALTER TABLE rule_versions ADD COLUMN cel_profile_version TEXT NOT NULL DEFAULT 'gallery-cel-v1';
ALTER TABLE rule_versions ADD COLUMN parameter_schema_json TEXT NOT NULL DEFAULT '{"type":"object","additionalProperties":false}';
ALTER TABLE rule_versions ADD COLUMN tests_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE rule_versions ADD COLUMN extensions_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE rule_versions ADD COLUMN parent_semantic_hash TEXT;
ALTER TABLE rule_versions ADD COLUMN created_by TEXT NOT NULL DEFAULT 'system';
ALTER TABLE rule_versions ADD COLUMN published_at INTEGER;
ALTER TABLE rule_versions ADD COLUMN deprecated_at INTEGER;
ALTER TABLE rule_versions ADD COLUMN executable INTEGER NOT NULL DEFAULT 1;
ALTER TABLE rule_versions ADD COLUMN compile_error TEXT;

CREATE TABLE rule_packages (
    package_id TEXT PRIMARY KEY NOT NULL,
    rule_set_id TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('active', 'deprecated', 'deleted')),
    current_semantic_hash TEXT,
    latest_valid_semantic_hash TEXT,
    draft_id TEXT,
    extension_requirements_json TEXT NOT NULL DEFAULT '{}',
    created_by TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1
) STRICT;

CREATE TABLE rule_drafts (
    draft_id TEXT PRIMARY KEY NOT NULL,
    package_id TEXT NOT NULL UNIQUE REFERENCES rule_packages(package_id) ON DELETE RESTRICT,
    base_semantic_hash TEXT,
    content_json TEXT NOT NULL,
    source_format TEXT NOT NULL CHECK (source_format IN ('json', 'yaml', 'toml')),
    validation_status TEXT NOT NULL CHECK (validation_status IN ('draft', 'validated', 'invalid')),
    diagnostics_json TEXT NOT NULL DEFAULT '[]',
    revision INTEGER NOT NULL DEFAULT 1,
    saved_by TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE rule_parameter_sets (
    parameter_id TEXT PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    semantic_hash TEXT NOT NULL REFERENCES rule_versions(semantic_hash) ON DELETE RESTRICT,
    current_revision INTEGER NOT NULL DEFAULT 1,
    current_hash TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'deprecated')),
    created_by TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (semantic_hash, name)
) STRICT;

CREATE TABLE rule_parameter_revisions (
    parameter_id TEXT NOT NULL REFERENCES rule_parameter_sets(parameter_id) ON DELETE RESTRICT,
    revision INTEGER NOT NULL,
    parameters_json TEXT NOT NULL,
    parameters_hash TEXT NOT NULL,
    created_by TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (parameter_id, revision)
) STRICT;

CREATE TABLE rule_test_cases (
    test_case_id TEXT PRIMARY KEY NOT NULL,
    semantic_hash TEXT NOT NULL REFERENCES rule_versions(semantic_hash) ON DELETE RESTRICT,
    ordinal INTEGER NOT NULL,
    case_json TEXT NOT NULL,
    expected_json TEXT NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL,
    UNIQUE (semantic_hash, ordinal)
) STRICT;

CREATE TABLE rule_examples (
    example_id TEXT PRIMARY KEY NOT NULL,
    package_id TEXT NOT NULL REFERENCES rule_packages(package_id) ON DELETE RESTRICT,
    name TEXT NOT NULL,
    category TEXT NOT NULL,
    fixture_json TEXT NOT NULL,
    expected_json TEXT NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL,
    UNIQUE (package_id, name)
) STRICT;

CREATE TABLE rule_audits (
    audit_id TEXT PRIMARY KEY NOT NULL,
    package_id TEXT NOT NULL REFERENCES rule_packages(package_id) ON DELETE RESTRICT,
    action TEXT NOT NULL,
    from_semantic_hash TEXT,
    to_semantic_hash TEXT,
    reason TEXT NOT NULL DEFAULT '',
    actor_id TEXT NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;

CREATE TABLE rule_compilation_cache (
    cache_key TEXT PRIMARY KEY NOT NULL,
    semantic_hash TEXT NOT NULL,
    parameter_hash TEXT NOT NULL,
    rule_ir_hash TEXT NOT NULL,
    compiler_version TEXT NOT NULL,
    cel_profile_version TEXT NOT NULL,
    extension_registry_version TEXT NOT NULL,
    compiled_ir_json TEXT NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;

ALTER TABLE source_rule_bindings ADD COLUMN parameter_id TEXT;
ALTER TABLE source_rule_bindings ADD COLUMN parameter_revision INTEGER NOT NULL DEFAULT 0;
ALTER TABLE source_rule_bindings ADD COLUMN parameter_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE source_rule_bindings ADD COLUMN override_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE source_rule_bindings ADD COLUMN condition_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE source_rule_bindings ADD COLUMN status TEXT NOT NULL DEFAULT 'active';
ALTER TABLE source_rule_bindings ADD COLUMN updated_at INTEGER NOT NULL DEFAULT 0;

ALTER TABLE jobs ADD COLUMN rule_semantic_hash TEXT;
ALTER TABLE jobs ADD COLUMN rule_parameters_json TEXT;
ALTER TABLE jobs ADD COLUMN rule_parameters_hash TEXT;
ALTER TABLE jobs ADD COLUMN rule_ir_hash TEXT;
ALTER TABLE jobs ADD COLUMN compiler_version TEXT;
ALTER TABLE jobs ADD COLUMN cel_profile_version TEXT;
ALTER TABLE jobs ADD COLUMN extension_registry_version TEXT;

CREATE INDEX rule_versions_package_idx ON rule_versions (package_id, created_at, semantic_hash);
CREATE INDEX rule_versions_status_idx ON rule_versions (status, created_at, semantic_hash);
CREATE INDEX rule_parameter_revisions_hash_idx ON rule_parameter_revisions (parameters_hash);
CREATE INDEX rule_audits_package_idx ON rule_audits (package_id, created_at, audit_id);
CREATE INDEX source_rule_bindings_effective_idx ON source_rule_bindings (source_id, status, priority, binding_id);

-- 阶段 2 Freeze Gate 的规则身份、保存和执行输入分类。规则产品闭环已落地，仍未有足够
-- 真实规模与多 Provider 证据的选择保留为兼容基线或延后，不把当前实现伪装成永久物理承诺。
INSERT INTO schema_freeze (freeze_phase, subject, classification, note) VALUES
    ('phase2', 'rule.package_canonical_json_control_owner', 'FROZEN',
     'RulePackage、RuleDraft、RuleVersion、参数 revision、测试和审计的不可重建事实只进入 control.db'),
    ('phase2', 'rule.version_immutable', 'FROZEN',
     '已发布 RuleVersion 只读；编辑和回滚产生新的发布动作或切换当前引用，不覆盖历史内容'),
    ('phase2', 'rule.draft_optimistic_revision', 'FROZEN',
     '草稿保存、验证、发布按 revision CAS，冲突返回 RULE_DRAFT_CONFLICT'),
    ('phase2', 'rule.job_execution_snapshot', 'FROZEN',
     '扫描 Job 冻结 RuleVersion、规范化参数、参数 hash、Rule IR 和 compiler/Profile/extension registry 版本'),
    ('phase2', 'rule.ui_metadata_nonsemantic', 'COMPATIBILITY_BASELINE',
     'ui_metadata 参与 package_hash 但不参与 semantic_hash；最终表单元数据命名空间待 UI 门禁'),
    ('phase2', 'rule.extension_registry', 'COMPATIBILITY_BASELINE',
     'gallery.identity v1 已实现受限行为；注册表和新增 semantic extension 仍需真实 Source 门禁'),
    ('phase2', 'rule.source_binding_single_effective', 'COMPATIBILITY_BASELINE',
     '同一 Source 按 active、条件匹配、priority、binding_id 稳定选择一条；多规则链和 Provider 路由未冻结'),
    ('phase2', 'rule.parameter_revision_and_override', 'COMPATIBILITY_BASELINE',
     '参数集以 revision/hash 身份，Binding 事务性刷新并允许一层受限 override；参数命名空间最终策略待后续门禁'),
    ('phase2', 'rule.impact_dependency_categories', 'COMPATIBILITY_BASELINE',
     'NO_ACTION/REPROJECT/RESCAN_PARTIAL/RESCAN_FULL/BINDING_REVIEW/INVALID 的输出字段与调度联动仍可演进'),
    ('phase2', 'orphan.default_threshold_3', 'COMPATIBILITY_BASELINE',
     '阶段 2 复议未改变 orphan 默认阈值 3，正式规模门禁前保持可配置演进'),
    ('phase2', 'orphan.retention_scans_override', 'COMPATIBILITY_BASELINE',
     'retention_scans_override 继续作为兼容基线，未升格为永久保留承诺'),
    ('phase2', 'source_structure.missing_blob_evidence', 'COMPATIBILITY_BASELINE',
     '缺少 ContentBlob 证据时继续走人工审查，不扩展为静默自动合并'),
    ('phase2', 'source_structure.split_bind_existing', 'DEFERRED',
     'split.bind_existing 不在规则闭环中预埋为默认行为，待真实结构证据与可撤销模型'),
    ('phase2', 'source_structure.action_set', 'COMPATIBILITY_BASELINE',
     '拆分/合并决策动作集合保持现有人工决策兼容基线，新增动作需单独门禁'),
    ('phase2', 'rule_version.identity_namespace', 'COMPATIBILITY_BASELINE',
     'semantic_hash 仍是 RuleVersion 运行身份；跨 RuleSet、extension 和参数命名空间的最终策略未永久冻结')
ON CONFLICT (freeze_phase, subject) DO UPDATE SET classification=excluded.classification, note=excluded.note;
