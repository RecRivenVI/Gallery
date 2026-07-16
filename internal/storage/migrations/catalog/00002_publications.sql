CREATE TABLE catalog_revisions (
    catalog_revision_id TEXT PRIMARY KEY NOT NULL,
    job_id TEXT NOT NULL UNIQUE,
    source_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('staging', 'published', 'aborted')),
    created_at INTEGER NOT NULL,
    published_at INTEGER
) STRICT;

CREATE TABLE overlay_projection_revisions (
    overlay_revision_id TEXT PRIMARY KEY NOT NULL,
    catalog_revision_id TEXT NOT NULL UNIQUE REFERENCES catalog_revisions(catalog_revision_id) ON DELETE CASCADE,
    control_watermark INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('staging', 'published', 'aborted')),
    created_at INTEGER NOT NULL,
    published_at INTEGER
) STRICT;

CREATE TABLE source_works (
    catalog_revision_id TEXT NOT NULL REFERENCES catalog_revisions(catalog_revision_id) ON DELETE CASCADE,
    source_id TEXT NOT NULL,
    source_key TEXT NOT NULL,
    title TEXT NOT NULL,
    PRIMARY KEY (catalog_revision_id, source_id, source_key)
) STRICT;

CREATE TABLE source_media (
    catalog_revision_id TEXT NOT NULL REFERENCES catalog_revisions(catalog_revision_id) ON DELETE CASCADE,
    source_id TEXT NOT NULL,
    source_key TEXT NOT NULL,
    work_source_key TEXT NOT NULL,
    relative_path TEXT NOT NULL,
    media_kind TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    PRIMARY KEY (catalog_revision_id, source_id, source_key)
) STRICT;

CREATE TABLE content_blobs (
    catalog_revision_id TEXT NOT NULL REFERENCES catalog_revisions(catalog_revision_id) ON DELETE CASCADE,
    algorithm TEXT NOT NULL,
    digest TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    PRIMARY KEY (catalog_revision_id, algorithm, digest)
) STRICT;

CREATE TABLE file_locations (
    catalog_revision_id TEXT NOT NULL REFERENCES catalog_revisions(catalog_revision_id) ON DELETE CASCADE,
    source_id TEXT NOT NULL,
    source_key TEXT NOT NULL,
    location_key TEXT NOT NULL,
    relative_path TEXT NOT NULL,
    algorithm TEXT NOT NULL,
    digest TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('present', 'offline', 'missing', 'inaccessible')),
    PRIMARY KEY (catalog_revision_id, source_id, location_key)
) STRICT;

CREATE TABLE work_projections (
    catalog_revision_id TEXT NOT NULL REFERENCES catalog_revisions(catalog_revision_id) ON DELETE CASCADE,
    overlay_revision_id TEXT NOT NULL REFERENCES overlay_projection_revisions(overlay_revision_id) ON DELETE CASCADE,
    work_id TEXT NOT NULL,
    source_id TEXT NOT NULL,
    source_key TEXT NOT NULL,
    title TEXT NOT NULL,
    PRIMARY KEY (catalog_revision_id, overlay_revision_id, work_id)
) STRICT;

CREATE TABLE media_projections (
    catalog_revision_id TEXT NOT NULL REFERENCES catalog_revisions(catalog_revision_id) ON DELETE CASCADE,
    overlay_revision_id TEXT NOT NULL REFERENCES overlay_projection_revisions(overlay_revision_id) ON DELETE CASCADE,
    media_id TEXT NOT NULL,
    work_id TEXT NOT NULL,
    source_id TEXT NOT NULL,
    source_key TEXT NOT NULL,
    relative_path TEXT NOT NULL,
    media_kind TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    algorithm TEXT NOT NULL,
    digest TEXT NOT NULL,
    location_status TEXT NOT NULL,
    ordinal INTEGER NOT NULL,
    PRIMARY KEY (catalog_revision_id, overlay_revision_id, media_id)
) STRICT;

CREATE TABLE query_publications (
    query_publication_id TEXT PRIMARY KEY NOT NULL,
    catalog_revision_id TEXT NOT NULL REFERENCES catalog_revisions(catalog_revision_id) ON DELETE RESTRICT,
    overlay_revision_id TEXT NOT NULL REFERENCES overlay_projection_revisions(overlay_revision_id) ON DELETE RESTRICT,
    job_id TEXT NOT NULL UNIQUE,
    control_watermark INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE (catalog_revision_id, overlay_revision_id)
) STRICT;

CREATE TABLE active_query_publication (
    singleton INTEGER PRIMARY KEY NOT NULL CHECK (singleton = 1),
    query_publication_id TEXT NOT NULL REFERENCES query_publications(query_publication_id) ON DELETE RESTRICT
) STRICT;

CREATE INDEX work_projections_query_idx ON work_projections (catalog_revision_id, overlay_revision_id, title, work_id);
CREATE INDEX media_projections_work_idx ON media_projections (catalog_revision_id, overlay_revision_id, work_id, ordinal, media_id);
