-- 阶段 1 Schema Freeze Gate。基于 Walking Skeleton、Architecture Proof、CanonicalCreator 合并/撤销、
-- Work/Media Binding 修复/撤销、inactive/orphan 生命周期、control 备份恢复、Catalog 全量重建决策恢复，
-- 以及 SourceWork 拆分/合并/撤销的证据，对身份与唯一约束逐项分类。本迁移不改变任何既有表结构，只把
-- 分类结果登记为可查询、受 forward-only checksum 保护的产品事实，供后续阶段判断哪些是兼容承诺、哪些
-- 仍可演进。分类含义：FROZEN=身份不变量，视为兼容承诺；COMPATIBILITY_BASELINE=当前作为稳定基线但
-- 未升格为永久承诺；PRE_FREEZE=实现已就位但物理形态尚可调整；DEFERRED=留待后续阶段决策。

CREATE TABLE schema_freeze (
    freeze_phase TEXT NOT NULL,
    subject TEXT NOT NULL,
    classification TEXT NOT NULL CHECK (classification IN
        ('FROZEN', 'COMPATIBILITY_BASELINE', 'PRE_FREEZE', 'DEFERRED')),
    note TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (freeze_phase, subject)
) STRICT;

INSERT INTO schema_freeze (freeze_phase, subject, classification, note) VALUES
    -- Binding 身份
    ('phase1', 'binding.active_source_key_unique', 'FROZEN',
     '每个 (source_id, source_key) 至多一个 active Binding，work/media/creator 三表一致'),
    ('phase1', 'binding.nonactive_history_multi', 'FROZEN',
     'inactive/orphan_candidate/orphaned/manual_unbound 允许同键多条历史记录，是拆分合并与撤销的基础'),
    ('phase1', 'binding.manual_unbound_excludes_auto', 'FROZEN',
     'manual_unbound 阻止相同自动 Binding，拆分合并排除决策依赖此语义'),
    ('phase1', 'binding.multi_source_isolation', 'FROZEN',
     '所有 Binding 解析按 source_id 隔离，其他 Source 结构变化互不影响'),
    ('phase1', 'binding.source_rebuild_recovery', 'FROZEN',
     'Binding 以稳定 Source 引用恢复，Catalog 删除重建后不依赖 row ID'),
    ('phase1', 'binding.orphan_candidate_in_resolution', 'COMPATIBILITY_BASELINE',
     'orphan_candidate/orphaned 参与候选解析但不进入 active 唯一索引，保留窗口阈值未冻结'),
    ('phase1', 'binding.provider_external_id_conflict', 'PRE_FREEZE',
     'external_id 为非唯一索引，冲突走 issue 而非 DB 约束；最终 external ID 策略待规则产品化'),
    ('phase1', 'binding.rule_version_identity_namespace', 'DEFERRED',
     'RuleVersion 变化当前不进入身份命名空间；多 SourceRuleBinding 组合待阶段 2'),
    -- CanonicalWork 身份
    ('phase1', 'canonical_work.identity_by_persistent_id', 'FROZEN',
     'CanonicalWork 仅以持久 Canonical ID 标识，无自然唯一键；同内容多 Work、多 Origin 允许'),
    ('phase1', 'canonical_work.origin_model', 'PRE_FREEZE',
     'WorkOrigin 独立表尚未建立，Provider/external ID 暂存于 Binding；Origin 有效字段策略待阶段 2'),
    -- CanonicalMedia 身份
    ('phase1', 'canonical_media.work_ordinal_unique', 'FROZEN',
     '(work_id, ordinal) 唯一；拆分与重排通过取下一空闲 ordinal 迁移，不复用被占 ordinal'),
    ('phase1', 'canonical_media.same_blob_multi_occurrence', 'FROZEN',
     '同一 ContentBlob 可被多个 CanonicalMedia occurrence 使用，SHA-256 相同不自动合并 occurrence'),
    ('phase1', 'media_binding.blob_evidence_recandidate', 'COMPATIBILITY_BASELINE',
     '媒体按 source_key→rule_key→blob digest 顺序重候选；FileLocation 唯一约束待文件身份门禁'),
    -- issue 与决策
    ('phase1', 'binding_issue.fingerprint_dedup', 'FROZEN',
     '相同候选指纹不重复产生 issue，证据变化 superseded，来源消失 stale'),
    ('phase1', 'binding_issue.active_uniqueness', 'COMPATIBILITY_BASELINE',
     '同 (source_id, entity_type, source_key[, code]) 至多一个 open/dismissed issue 由应用层保证'),
    ('phase1', 'structure_decision.fingerprint_unique_applied', 'FROZEN',
     'source_structure_decisions 对 (source_id, fingerprint) WHERE status=applied 唯一，撤销后释放'),
    ('phase1', 'structure_decision.undo_conflict_on_consumed', 'FROZEN',
     '已被扫描消费（新 source_key 产生 active Binding）的拆分/合并决策撤销返回 CONFLICT');
