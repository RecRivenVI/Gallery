CREATE TABLE source_creators (
    catalog_revision_id TEXT NOT NULL REFERENCES catalog_revisions(catalog_revision_id) ON DELETE CASCADE,
    source_id TEXT NOT NULL,
    source_key TEXT NOT NULL,
    provider_id TEXT NOT NULL DEFAULT '',
    external_id TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    PRIMARY KEY (catalog_revision_id, source_id, source_key)
) STRICT;

CREATE INDEX source_creators_external_idx
ON source_creators (catalog_revision_id, source_id, provider_id, external_id)
WHERE external_id <> '';

CREATE TABLE creator_projections (
    catalog_revision_id TEXT NOT NULL REFERENCES catalog_revisions(catalog_revision_id) ON DELETE CASCADE,
    overlay_revision_id TEXT NOT NULL,
    creator_id TEXT NOT NULL,
    name TEXT NOT NULL,
    sort_name_key TEXT NOT NULL,
    PRIMARY KEY (catalog_revision_id, overlay_revision_id, creator_id),
    FOREIGN KEY (catalog_revision_id, overlay_revision_id)
        REFERENCES overlay_projection_revisions(catalog_revision_id, overlay_revision_id) ON DELETE CASCADE
) STRICT;

CREATE TABLE work_creator_relations (
    catalog_revision_id TEXT NOT NULL,
    overlay_revision_id TEXT NOT NULL,
    work_id TEXT NOT NULL,
    creator_id TEXT NOT NULL,
    role TEXT NOT NULL,
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    PRIMARY KEY (catalog_revision_id, overlay_revision_id, work_id, role, ordinal),
    FOREIGN KEY (catalog_revision_id, overlay_revision_id, work_id)
        REFERENCES work_projections(catalog_revision_id, overlay_revision_id, work_id) ON DELETE CASCADE,
    FOREIGN KEY (catalog_revision_id, overlay_revision_id, creator_id)
        REFERENCES creator_projections(catalog_revision_id, overlay_revision_id, creator_id) ON DELETE CASCADE
) STRICT;

CREATE INDEX work_creator_relations_creator_idx
ON work_creator_relations (catalog_revision_id, overlay_revision_id, creator_id, work_id);
