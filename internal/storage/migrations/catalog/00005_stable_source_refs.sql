ALTER TABLE source_works ADD COLUMN provider_id TEXT NOT NULL DEFAULT '';
ALTER TABLE source_works ADD COLUMN external_id TEXT NOT NULL DEFAULT '';
ALTER TABLE source_media ADD COLUMN rule_key TEXT NOT NULL DEFAULT '';

CREATE INDEX source_works_external_idx
ON source_works (catalog_revision_id, source_id, provider_id, external_id)
WHERE external_id <> '';
