PRAGMA defer_foreign_keys = ON;

ALTER TABLE source_works ADD COLUMN creator TEXT NOT NULL DEFAULT '';
ALTER TABLE source_works ADD COLUMN tags_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE source_works ADD COLUMN filenames_text TEXT NOT NULL DEFAULT '';

CREATE TABLE overlay_projection_revisions_new (
    overlay_revision_id TEXT PRIMARY KEY NOT NULL,
    catalog_revision_id TEXT NOT NULL REFERENCES catalog_revisions(catalog_revision_id) ON DELETE CASCADE,
    control_watermark INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('staging', 'published', 'aborted', 'superseded')),
    created_at INTEGER NOT NULL,
    published_at INTEGER,
    UNIQUE (catalog_revision_id, overlay_revision_id)
) STRICT;

INSERT INTO overlay_projection_revisions_new
SELECT overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, published_at
FROM overlay_projection_revisions;

CREATE TABLE work_projections_new (
    catalog_revision_id TEXT NOT NULL REFERENCES catalog_revisions(catalog_revision_id) ON DELETE CASCADE,
    overlay_revision_id TEXT NOT NULL REFERENCES overlay_projection_revisions_new(overlay_revision_id) ON DELETE CASCADE,
    work_id TEXT NOT NULL,
    source_id TEXT NOT NULL,
    source_key TEXT NOT NULL,
    library_id TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL,
    creator TEXT NOT NULL DEFAULT '',
    tags_json TEXT NOT NULL DEFAULT '[]',
    filenames_text TEXT NOT NULL DEFAULT '',
    normalized_original_text TEXT NOT NULL DEFAULT '',
    cjk_bigram_token_text TEXT NOT NULL DEFAULT '',
    latin_trigram_token_text TEXT NOT NULL DEFAULT '',
    sort_title_key TEXT NOT NULL DEFAULT '',
    hidden INTEGER NOT NULL DEFAULT 0 CHECK (hidden IN (0, 1)),
    PRIMARY KEY (catalog_revision_id, overlay_revision_id, work_id)
) STRICT;

INSERT INTO work_projections_new
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, title,
 normalized_original_text, sort_title_key)
SELECT catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, title,
       lower(title), hex(CAST(lower(title) AS BLOB))
FROM work_projections;

CREATE TABLE media_projections_new (
    catalog_revision_id TEXT NOT NULL REFERENCES catalog_revisions(catalog_revision_id) ON DELETE CASCADE,
    overlay_revision_id TEXT NOT NULL REFERENCES overlay_projection_revisions_new(overlay_revision_id) ON DELETE CASCADE,
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
    hidden INTEGER NOT NULL DEFAULT 0 CHECK (hidden IN (0, 1)),
    PRIMARY KEY (catalog_revision_id, overlay_revision_id, media_id)
) STRICT;

INSERT INTO media_projections_new
(catalog_revision_id, overlay_revision_id, media_id, work_id, source_id, source_key,
 relative_path, media_kind, mime_type, size_bytes, algorithm, digest, location_status, ordinal)
SELECT catalog_revision_id, overlay_revision_id, media_id, work_id, source_id, source_key,
       relative_path, media_kind, mime_type, size_bytes, algorithm, digest, location_status, ordinal
FROM media_projections;

CREATE TABLE query_publications_new (
    query_publication_id TEXT PRIMARY KEY NOT NULL,
    catalog_revision_id TEXT NOT NULL REFERENCES catalog_revisions(catalog_revision_id) ON DELETE RESTRICT,
    overlay_revision_id TEXT NOT NULL,
    job_id TEXT NOT NULL UNIQUE,
    control_watermark INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    FOREIGN KEY (catalog_revision_id, overlay_revision_id)
        REFERENCES overlay_projection_revisions_new(catalog_revision_id, overlay_revision_id) ON DELETE RESTRICT,
    UNIQUE (catalog_revision_id, overlay_revision_id)
) STRICT;

INSERT INTO query_publications_new SELECT * FROM query_publications;

CREATE TABLE active_query_publication_new (
    singleton INTEGER PRIMARY KEY NOT NULL CHECK (singleton = 1),
    query_publication_id TEXT NOT NULL REFERENCES query_publications_new(query_publication_id) ON DELETE RESTRICT
) STRICT;

INSERT INTO active_query_publication_new SELECT * FROM active_query_publication;

DROP TABLE active_query_publication;
DROP TABLE query_publications;
DROP TABLE media_projections;
DROP TABLE work_projections;
DROP TABLE overlay_projection_revisions;

ALTER TABLE overlay_projection_revisions_new RENAME TO overlay_projection_revisions;
ALTER TABLE work_projections_new RENAME TO work_projections;
ALTER TABLE media_projections_new RENAME TO media_projections;
ALTER TABLE query_publications_new RENAME TO query_publications;
ALTER TABLE active_query_publication_new RENAME TO active_query_publication;

CREATE INDEX work_projections_query_idx
ON work_projections (catalog_revision_id, overlay_revision_id, hidden, sort_title_key, work_id);
CREATE INDEX work_projections_scope_idx
ON work_projections (catalog_revision_id, overlay_revision_id, library_id, source_id, work_id);
CREATE INDEX media_projections_work_idx
ON media_projections (catalog_revision_id, overlay_revision_id, work_id, ordinal, media_id);
CREATE INDEX overlay_projection_catalog_idx
ON overlay_projection_revisions (catalog_revision_id, control_watermark, overlay_revision_id);

CREATE VIRTUAL TABLE work_search USING fts5(
    catalog_revision_id UNINDEXED,
    overlay_revision_id UNINDEXED,
    work_id UNINDEXED,
    normalized_original_text,
    cjk_bigram_token_text,
    latin_trigram_token_text,
    tokenize = 'unicode61'
);

INSERT INTO work_search
(catalog_revision_id, overlay_revision_id, work_id, normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text)
SELECT catalog_revision_id, overlay_revision_id, work_id, normalized_original_text, '', ''
FROM work_projections;

CREATE TABLE query_publication_leases (
    lease_id TEXT PRIMARY KEY NOT NULL,
    query_publication_id TEXT NOT NULL REFERENCES query_publications(query_publication_id) ON DELETE CASCADE,
    authorization_scope_hash TEXT NOT NULL,
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX query_publication_leases_expiry_idx
ON query_publication_leases (expires_at, query_publication_id);
