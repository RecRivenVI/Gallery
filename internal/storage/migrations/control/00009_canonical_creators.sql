CREATE TABLE canonical_creators (
    creator_id TEXT PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;

CREATE TABLE creator_bindings (
    binding_id TEXT PRIMARY KEY NOT NULL,
    source_id TEXT NOT NULL REFERENCES sources(source_id) ON DELETE RESTRICT,
    provider_id TEXT NOT NULL DEFAULT '',
    external_id TEXT NOT NULL DEFAULT '',
    source_key TEXT NOT NULL,
    creator_id TEXT NOT NULL REFERENCES canonical_creators(creator_id) ON DELETE RESTRICT,
    identity_version INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'orphaned', 'manual_unbound', 'conflict')),
    last_seen_generation INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX creator_bindings_one_active_key
ON creator_bindings (source_id, source_key)
WHERE status = 'active';

CREATE INDEX creator_bindings_external_idx
ON creator_bindings (source_id, provider_id, external_id, status, creator_id)
WHERE external_id <> '';

CREATE INDEX creator_bindings_creator_idx
ON creator_bindings (creator_id, status, source_id, source_key);

CREATE TABLE work_creators (
    work_id TEXT NOT NULL REFERENCES canonical_works(work_id) ON DELETE RESTRICT,
    creator_id TEXT NOT NULL REFERENCES canonical_creators(creator_id) ON DELETE RESTRICT,
    role TEXT NOT NULL,
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    created_at INTEGER NOT NULL,
    PRIMARY KEY (work_id, role, ordinal),
    UNIQUE (work_id, creator_id, role)
) STRICT;

CREATE INDEX work_creators_creator_idx
ON work_creators (creator_id, work_id, role, ordinal);
