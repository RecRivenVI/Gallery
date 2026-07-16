ALTER TABLE canonical_creators ADD COLUMN merged_into TEXT
    REFERENCES canonical_creators(creator_id) ON DELETE RESTRICT;

CREATE INDEX canonical_creators_merged_idx
ON canonical_creators (merged_into, creator_id)
WHERE merged_into IS NOT NULL;

CREATE TABLE creator_merges (
    merge_id TEXT PRIMARY KEY NOT NULL,
    target_creator_id TEXT NOT NULL REFERENCES canonical_creators(creator_id) ON DELETE RESTRICT,
    created_by TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('applied', 'undone')),
    created_at INTEGER NOT NULL,
    undone_at INTEGER
) STRICT;

CREATE INDEX creator_merges_target_idx
ON creator_merges (target_creator_id, status, created_at);

CREATE TABLE creator_merge_members (
    merge_id TEXT NOT NULL REFERENCES creator_merges(merge_id) ON DELETE RESTRICT,
    absorbed_creator_id TEXT NOT NULL REFERENCES canonical_creators(creator_id) ON DELETE RESTRICT,
    PRIMARY KEY (merge_id, absorbed_creator_id)
) STRICT;

CREATE INDEX creator_merge_members_absorbed_idx
ON creator_merge_members (absorbed_creator_id, merge_id);
