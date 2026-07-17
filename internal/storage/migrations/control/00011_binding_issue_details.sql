ALTER TABLE binding_issues RENAME TO binding_issues_legacy;

CREATE TABLE binding_issues (
    issue_id TEXT PRIMARY KEY NOT NULL,
    source_id TEXT NOT NULL REFERENCES sources(source_id) ON DELETE RESTRICT,
    entity_type TEXT NOT NULL CHECK (entity_type IN ('work', 'creator', 'media')),
    source_key TEXT NOT NULL,
    work_source_key TEXT NOT NULL DEFAULT '',
    provider_id TEXT NOT NULL DEFAULT '',
    external_id TEXT NOT NULL DEFAULT '',
    code TEXT NOT NULL,
    candidate_fingerprint TEXT NOT NULL DEFAULT '',
    candidate_count INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('open', 'resolved', 'dismissed', 'superseded', 'stale')),
    resolution TEXT CHECK (resolution IN ('bind_existing', 'create_new', 'keep_separate', 'dismissed')),
    resolved_target_id TEXT,
    resolved_by TEXT,
    version INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    resolved_at INTEGER
) STRICT;

INSERT INTO binding_issues
(issue_id, source_id, entity_type, source_key, provider_id, external_id, code,
 candidate_count, status, version, created_at, updated_at, resolved_at)
SELECT issue_id, source_id, 'work', source_key, provider_id, external_id, code,
       candidate_count, status, 1, created_at, created_at, resolved_at
FROM binding_issues_legacy;

DROP TABLE binding_issues_legacy;

CREATE INDEX binding_issues_list_idx
ON binding_issues (source_id, status, entity_type, created_at, issue_id);

CREATE INDEX binding_issues_key_idx
ON binding_issues (source_id, entity_type, source_key, status);

CREATE TABLE binding_issue_candidates (
    issue_id TEXT NOT NULL REFERENCES binding_issues(issue_id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    candidate_id TEXT NOT NULL,
    candidate_kind TEXT NOT NULL CHECK (candidate_kind IN ('work', 'creator', 'media')),
    match_signal TEXT NOT NULL,
    match_value TEXT NOT NULL DEFAULT '',
    label TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (issue_id, ordinal)
) STRICT;

CREATE INDEX binding_issue_candidates_target_idx
ON binding_issue_candidates (candidate_id, candidate_kind, issue_id);
