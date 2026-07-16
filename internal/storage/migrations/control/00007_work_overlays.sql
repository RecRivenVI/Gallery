ALTER TABLE jobs ADD COLUMN target_watermark INTEGER;
ALTER TABLE jobs ADD COLUMN target_catalog_revision_id TEXT;
ALTER TABLE jobs ADD COLUMN base_query_publication_id TEXT;

CREATE UNIQUE INDEX jobs_one_active_overlay_projection
ON jobs (job_type)
WHERE job_type = 'overlay_projection' AND status IN ('queued', 'running', 'publishing');

CREATE TABLE gallery_control_sequence (
    singleton INTEGER PRIMARY KEY NOT NULL CHECK (singleton = 1),
    watermark INTEGER NOT NULL CHECK (watermark >= 0)
) STRICT;

INSERT INTO gallery_control_sequence (singleton, watermark) VALUES (1, 0);

CREATE TABLE work_overlays (
    work_id TEXT PRIMARY KEY NOT NULL REFERENCES canonical_works(work_id) ON DELETE RESTRICT,
    title_override TEXT NOT NULL DEFAULT '',
    manual_tags_json TEXT NOT NULL DEFAULT '[]',
    hidden INTEGER NOT NULL DEFAULT 0 CHECK (hidden IN (0, 1)),
    custom_cover_media_id TEXT REFERENCES canonical_media(media_id) ON DELETE RESTRICT,
    favorite INTEGER NOT NULL DEFAULT 0 CHECK (favorite IN (0, 1)),
    progress REAL NOT NULL DEFAULT 0 CHECK (progress >= 0 AND progress <= 1),
    fact_watermark INTEGER NOT NULL,
    query_watermark INTEGER NOT NULL DEFAULT 0,
    projected_watermark INTEGER NOT NULL DEFAULT 0,
    projection_status TEXT NOT NULL CHECK (projection_status IN ('pending', 'published', 'failed')),
    projection_job_id TEXT REFERENCES jobs(job_id) ON DELETE RESTRICT,
    published_query_publication_id TEXT,
    issue_code TEXT,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE INDEX work_overlays_projection_idx
ON work_overlays (projection_status, query_watermark, work_id);
