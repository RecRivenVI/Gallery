ALTER TABLE work_bindings RENAME TO work_bindings_pre_inactive;

CREATE TABLE work_bindings (
    binding_id TEXT PRIMARY KEY NOT NULL,
    source_id TEXT NOT NULL REFERENCES sources(source_id) ON DELETE RESTRICT,
    provider_id TEXT NOT NULL DEFAULT '',
    external_id TEXT NOT NULL DEFAULT '',
    source_key TEXT NOT NULL,
    work_id TEXT NOT NULL REFERENCES canonical_works(work_id) ON DELETE RESTRICT,
    identity_version INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'inactive', 'orphaned', 'manual_unbound', 'conflict')),
    last_seen_generation INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

INSERT INTO work_bindings SELECT * FROM work_bindings_pre_inactive;

DROP TABLE work_bindings_pre_inactive;

CREATE UNIQUE INDEX work_bindings_one_active_key ON work_bindings (source_id, source_key) WHERE status = 'active';
CREATE INDEX work_bindings_external_idx ON work_bindings (source_id, provider_id, external_id, status, work_id) WHERE external_id <> '';
CREATE INDEX work_bindings_work_idx ON work_bindings (work_id, status, source_id, source_key);

ALTER TABLE media_bindings RENAME TO media_bindings_pre_inactive;

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
    status TEXT NOT NULL CHECK (status IN ('active', 'inactive', 'orphaned', 'manual_unbound', 'conflict')),
    last_seen_generation INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

INSERT INTO media_bindings SELECT * FROM media_bindings_pre_inactive;

DROP TABLE media_bindings_pre_inactive;

CREATE UNIQUE INDEX media_bindings_one_active_key ON media_bindings (source_id, source_key) WHERE status = 'active';
CREATE INDEX media_bindings_rule_idx ON media_bindings (source_id, work_id, rule_key, status, media_id) WHERE rule_key <> '';
CREATE INDEX media_bindings_blob_idx ON media_bindings (source_id, work_id, algorithm, digest, occurrence_ordinal, status, media_id) WHERE digest <> '';
CREATE INDEX media_bindings_media_idx ON media_bindings (media_id, status, source_id, source_key);

ALTER TABLE creator_bindings RENAME TO creator_bindings_pre_inactive;

CREATE TABLE creator_bindings (
    binding_id TEXT PRIMARY KEY NOT NULL,
    source_id TEXT NOT NULL REFERENCES sources(source_id) ON DELETE RESTRICT,
    provider_id TEXT NOT NULL DEFAULT '',
    external_id TEXT NOT NULL DEFAULT '',
    source_key TEXT NOT NULL,
    creator_id TEXT NOT NULL REFERENCES canonical_creators(creator_id) ON DELETE RESTRICT,
    identity_version INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'inactive', 'orphaned', 'manual_unbound', 'conflict')),
    last_seen_generation INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

INSERT INTO creator_bindings SELECT * FROM creator_bindings_pre_inactive;

DROP TABLE creator_bindings_pre_inactive;

CREATE UNIQUE INDEX creator_bindings_one_active_key ON creator_bindings (source_id, source_key) WHERE status = 'active';
CREATE INDEX creator_bindings_external_idx ON creator_bindings (source_id, provider_id, external_id, status, creator_id) WHERE external_id <> '';
CREATE INDEX creator_bindings_creator_idx ON creator_bindings (creator_id, status, source_id, source_key);
