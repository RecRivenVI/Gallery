CREATE TABLE derived_assets (
    asset_key TEXT PRIMARY KEY NOT NULL,
    blob_algorithm TEXT NOT NULL,
    blob_digest TEXT NOT NULL,
    transform_id TEXT NOT NULL,
    transform_version TEXT NOT NULL,
    parameters_hash TEXT NOT NULL,
    overlay_input_hash TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('generating', 'ready', 'failed', 'obsolete')),
    relative_path TEXT NOT NULL,
    output_digest TEXT NOT NULL DEFAULT '',
    output_size INTEGER NOT NULL DEFAULT 0,
    output_mime TEXT NOT NULL DEFAULT '',
    pinned INTEGER NOT NULL DEFAULT 0 CHECK (pinned IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    last_accessed_at INTEGER NOT NULL
) STRICT;

CREATE INDEX derived_assets_blob_idx
ON derived_assets (blob_algorithm, blob_digest, status, asset_key);

CREATE INDEX derived_assets_gc_idx
ON derived_assets (status, pinned, updated_at, asset_key);

CREATE TABLE derived_asset_leases (
    lease_id TEXT PRIMARY KEY NOT NULL,
    asset_key TEXT NOT NULL REFERENCES derived_assets(asset_key) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX derived_asset_leases_expiry_idx
ON derived_asset_leases (asset_key, expires_at);

CREATE TABLE blob_read_leases (
    lease_id TEXT PRIMARY KEY NOT NULL,
    blob_algorithm TEXT NOT NULL,
    blob_digest TEXT NOT NULL,
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX blob_read_leases_expiry_idx
ON blob_read_leases (blob_algorithm, blob_digest, expires_at);
