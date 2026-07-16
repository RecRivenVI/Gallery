CREATE TABLE query_signing_keys (
    key_version INTEGER PRIMARY KEY NOT NULL,
    key_bytes BLOB NOT NULL CHECK (length(key_bytes) >= 32),
    created_at INTEGER NOT NULL
) STRICT;
