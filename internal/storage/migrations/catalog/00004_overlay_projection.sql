ALTER TABLE media_projections ADD COLUMN base_ordinal INTEGER NOT NULL DEFAULT 0;
ALTER TABLE overlay_projection_revisions ADD COLUMN projection_job_id TEXT;

UPDATE media_projections SET base_ordinal = ordinal;

CREATE INDEX query_publications_catalog_watermark_idx
ON query_publications (catalog_revision_id, control_watermark, created_at, query_publication_id);

CREATE INDEX overlay_projection_job_idx
ON overlay_projection_revisions (projection_job_id)
WHERE projection_job_id IS NOT NULL;
