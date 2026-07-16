CREATE TABLE jobs (
    job_id TEXT PRIMARY KEY NOT NULL,
    job_type TEXT NOT NULL,
    source_id TEXT REFERENCES sources(source_id) ON DELETE RESTRICT,
    created_by TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'publishing', 'completed', 'failed', 'cancelled', 'needs_repair')),
    stage TEXT NOT NULL,
    progress_current INTEGER NOT NULL DEFAULT 0,
    progress_total INTEGER NOT NULL DEFAULT 0,
    progress_sequence INTEGER NOT NULL DEFAULT 1,
    issue_code TEXT,
    publication_id TEXT,
    retry_of TEXT REFERENCES jobs(job_id) ON DELETE RESTRICT,
    attempt INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL,
    started_at INTEGER,
    finished_at INTEGER,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX jobs_one_active_scan_per_source
ON jobs (source_id)
WHERE job_type = 'scan' AND status IN ('queued', 'running', 'publishing');

CREATE INDEX jobs_status_updated_idx ON jobs (status, updated_at, job_id);
