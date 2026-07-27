ALTER TABLE rule_audits ADD COLUMN subject_type TEXT NOT NULL DEFAULT 'package'
    CHECK (subject_type IN ('package', 'version', 'parameter_set'));
ALTER TABLE rule_audits ADD COLUMN subject_id TEXT NOT NULL DEFAULT '';

UPDATE rule_audits
SET subject_type = 'package', subject_id = package_id
WHERE subject_id = '';

UPDATE rule_audits
SET subject_type = 'version', subject_id = to_semantic_hash
WHERE action = 'publish' AND to_semantic_hash IS NOT NULL;

UPDATE rule_audits
SET subject_type = 'version', subject_id = from_semantic_hash
WHERE action = 'deprecate' AND from_semantic_hash IS NOT NULL;

CREATE INDEX rule_audits_subject_idx
    ON rule_audits (subject_type, subject_id, created_at, audit_id);
