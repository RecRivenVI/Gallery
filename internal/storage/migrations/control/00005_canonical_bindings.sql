CREATE TABLE canonical_works (
    work_id TEXT PRIMARY KEY NOT NULL,
    title TEXT NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;

CREATE TABLE canonical_media (
    media_id TEXT PRIMARY KEY NOT NULL,
    work_id TEXT NOT NULL REFERENCES canonical_works(work_id) ON DELETE RESTRICT,
    role TEXT NOT NULL,
    ordinal INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE (work_id, ordinal)
) STRICT;

CREATE TABLE work_bindings (
    source_id TEXT NOT NULL REFERENCES sources(source_id) ON DELETE RESTRICT,
    source_key TEXT NOT NULL,
    work_id TEXT NOT NULL REFERENCES canonical_works(work_id) ON DELETE RESTRICT,
    identity_version INTEGER NOT NULL,
    active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    created_at INTEGER NOT NULL,
    PRIMARY KEY (source_id, source_key)
) STRICT;

CREATE TABLE media_bindings (
    source_id TEXT NOT NULL REFERENCES sources(source_id) ON DELETE RESTRICT,
    source_key TEXT NOT NULL,
    media_id TEXT NOT NULL REFERENCES canonical_media(media_id) ON DELETE RESTRICT,
    work_id TEXT NOT NULL REFERENCES canonical_works(work_id) ON DELETE RESTRICT,
    identity_version INTEGER NOT NULL,
    active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    created_at INTEGER NOT NULL,
    PRIMARY KEY (source_id, source_key)
) STRICT;

CREATE INDEX media_bindings_media_idx ON media_bindings (media_id, source_id, source_key);
