CREATE TABLE pairing_attempts (
    credential_hash TEXT PRIMARY KEY NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at INTEGER
) STRICT;

CREATE TABLE sessions (
    session_id TEXT PRIMARY KEY NOT NULL,
    secret_hash TEXT NOT NULL,
    principal_id TEXT NOT NULL,
    csrf_token TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    revoked_at INTEGER
) STRICT;

CREATE INDEX sessions_active_expiry_idx ON sessions (expires_at, revoked_at);
