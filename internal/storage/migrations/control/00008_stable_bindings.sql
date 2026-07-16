ALTER TABLE work_bindings RENAME TO work_bindings_legacy;

CREATE TABLE work_bindings (
    binding_id TEXT PRIMARY KEY NOT NULL,
    source_id TEXT NOT NULL REFERENCES sources(source_id) ON DELETE RESTRICT,
    provider_id TEXT NOT NULL DEFAULT '',
    external_id TEXT NOT NULL DEFAULT '',
    source_key TEXT NOT NULL,
    work_id TEXT NOT NULL REFERENCES canonical_works(work_id) ON DELETE RESTRICT,
    identity_version INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'orphaned', 'manual_unbound', 'conflict')),
    last_seen_generation INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

INSERT INTO work_bindings
(binding_id, source_id, source_key, work_id, identity_version, status, created_at, updated_at)
SELECT 'legacy-work-' || lower(hex(source_id || char(0) || source_key)), source_id, source_key,
       work_id, identity_version, CASE active WHEN 1 THEN 'active' ELSE 'orphaned' END,
       created_at, created_at
FROM work_bindings_legacy;

DROP TABLE work_bindings_legacy;

CREATE UNIQUE INDEX work_bindings_one_active_key
ON work_bindings (source_id, source_key)
WHERE status = 'active';

CREATE INDEX work_bindings_external_idx
ON work_bindings (source_id, provider_id, external_id, status, work_id)
WHERE external_id <> '';

CREATE INDEX work_bindings_work_idx
ON work_bindings (work_id, status, source_id, source_key);

ALTER TABLE media_bindings RENAME TO media_bindings_legacy;

CREATE TABLE media_bindings (
    binding_id TEXT PRIMARY KEY NOT NULL,
    source_id TEXT NOT NULL REFERENCES sources(source_id) ON DELETE RESTRICT,
    source_key TEXT NOT NULL,
    rule_key TEXT NOT NULL DEFAULT '',
    media_id TEXT NOT NULL REFERENCES canonical_media(media_id) ON DELETE RESTRICT,
    work_id TEXT NOT NULL REFERENCES canonical_works(work_id) ON DELETE RESTRICT,
    algorithm TEXT NOT NULL DEFAULT '',
    digest TEXT NOT NULL DEFAULT '',
    occurrence_ordinal INTEGER NOT NULL DEFAULT 0,
    identity_version INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'orphaned', 'manual_unbound', 'conflict')),
    last_seen_generation INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

INSERT INTO media_bindings
(binding_id, source_id, source_key, media_id, work_id, occurrence_ordinal,
 identity_version, status, created_at, updated_at)
SELECT 'legacy-media-' || lower(hex(source_id || char(0) || source_key)), source_id, source_key,
       media_id, work_id, 0, identity_version,
       CASE active WHEN 1 THEN 'active' ELSE 'orphaned' END, created_at, created_at
FROM media_bindings_legacy;

DROP TABLE media_bindings_legacy;

CREATE UNIQUE INDEX media_bindings_one_active_key
ON media_bindings (source_id, source_key)
WHERE status = 'active';

CREATE INDEX media_bindings_rule_idx
ON media_bindings (source_id, work_id, rule_key, status, media_id)
WHERE rule_key <> '';

CREATE INDEX media_bindings_blob_idx
ON media_bindings (source_id, work_id, algorithm, digest, occurrence_ordinal, status, media_id)
WHERE digest <> '';

CREATE INDEX media_bindings_media_idx
ON media_bindings (media_id, status, source_id, source_key);

CREATE TABLE gallery_binding_sequence (
    singleton INTEGER PRIMARY KEY NOT NULL CHECK (singleton = 1),
    generation INTEGER NOT NULL CHECK (generation >= 0)
) STRICT;

INSERT INTO gallery_binding_sequence (singleton, generation) VALUES (1, 0);

CREATE TABLE binding_issues (
    issue_id TEXT PRIMARY KEY NOT NULL,
    source_id TEXT NOT NULL REFERENCES sources(source_id) ON DELETE RESTRICT,
    source_key TEXT NOT NULL,
    provider_id TEXT NOT NULL DEFAULT '',
    external_id TEXT NOT NULL DEFAULT '',
    code TEXT NOT NULL,
    candidate_count INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('open', 'resolved')),
    created_at INTEGER NOT NULL,
    resolved_at INTEGER
) STRICT;

CREATE INDEX binding_issues_open_idx
ON binding_issues (source_id, status, code, source_key);
