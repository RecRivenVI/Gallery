-- 阶段 3：任务尝试、资源分类、取消/恢复和 Source 收敛状态。
-- 历史 jobs.status 保持原有 CHECK 值；应用层从 cancel_requested 与 stage 映射
-- cancelling/superseded，避免重建被多个 control 表引用的 jobs 表。
ALTER TABLE jobs ADD COLUMN resource_class TEXT NOT NULL DEFAULT 'scan';
ALTER TABLE jobs ADD COLUMN target_resource TEXT;
ALTER TABLE jobs ADD COLUMN request_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE jobs ADD COLUMN idempotency_key TEXT;
ALTER TABLE jobs ADD COLUMN max_retries INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN retry_policy_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE jobs ADD COLUMN cancel_requested INTEGER NOT NULL DEFAULT 0 CHECK (cancel_requested IN (0, 1));
ALTER TABLE jobs ADD COLUMN cancel_requested_at INTEGER;
ALTER TABLE jobs ADD COLUMN heartbeat_at INTEGER;
ALTER TABLE jobs ADD COLUMN lease_owner TEXT;
ALTER TABLE jobs ADD COLUMN lease_expires_at INTEGER;
ALTER TABLE jobs ADD COLUMN result_json TEXT;
ALTER TABLE jobs ADD COLUMN failure_retryable INTEGER NOT NULL DEFAULT 0 CHECK (failure_retryable IN (0, 1));
ALTER TABLE jobs ADD COLUMN progress_phase TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN progress_unit TEXT NOT NULL DEFAULT 'items';
ALTER TABLE jobs ADD COLUMN progress_message TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN progress_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN progress_entities INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN progress_estimated INTEGER NOT NULL DEFAULT 0 CHECK (progress_estimated IN (0, 1));
ALTER TABLE jobs ADD COLUMN last_error_at INTEGER;

UPDATE jobs SET resource_class = CASE job_type
    WHEN 'scan' THEN 'scan'
    WHEN 'overlay_projection' THEN 'overlay'
    ELSE 'maintenance'
END;

CREATE UNIQUE INDEX jobs_idempotency_key_idx
ON jobs (idempotency_key)
WHERE idempotency_key IS NOT NULL AND idempotency_key <> '';

CREATE INDEX jobs_resource_status_idx
ON jobs (resource_class, status, created_at, job_id);

CREATE UNIQUE INDEX jobs_one_active_maintenance_type
ON jobs (job_type)
WHERE job_type IN ('control_backup', 'control_restore', 'catalog_gc', 'catalog_checkpoint', 'catalog_vacuum', 'derived_gc')
  AND status IN ('queued', 'running', 'publishing');

CREATE TABLE job_attempts (
    attempt_id TEXT PRIMARY KEY NOT NULL,
    job_id TEXT NOT NULL REFERENCES jobs(job_id) ON DELETE CASCADE,
    attempt INTEGER NOT NULL,
    resource_class TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'completed', 'failed', 'cancelled', 'recovered')),
    started_at INTEGER,
    heartbeat_at INTEGER,
    finished_at INTEGER,
    lease_owner TEXT,
    lease_expires_at INTEGER,
    error_code TEXT,
    error_retryable INTEGER NOT NULL DEFAULT 0 CHECK (error_retryable IN (0, 1)),
    progress_sequence INTEGER NOT NULL DEFAULT 1,
    result_json TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (job_id, attempt)
) STRICT;

CREATE INDEX job_attempts_active_idx
ON job_attempts (status, heartbeat_at, attempt_id)
WHERE status IN ('queued', 'running');

CREATE TABLE source_scan_states (
    source_id TEXT PRIMARY KEY NOT NULL REFERENCES sources(source_id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('unknown', 'online', 'offline', 'degraded', 'permission_denied', 'identity_changed')),
    dirty INTEGER NOT NULL DEFAULT 0 CHECK (dirty IN (0, 1)),
    watcher_available INTEGER NOT NULL DEFAULT 0 CHECK (watcher_available IN (0, 1)),
    watcher_overflow INTEGER NOT NULL DEFAULT 0 CHECK (watcher_overflow IN (0, 1)),
    last_event_at INTEGER,
    last_checked_at INTEGER,
    current_job_id TEXT REFERENCES jobs(job_id) ON DELETE SET NULL,
    pending_hash_count INTEGER NOT NULL DEFAULT 0,
    blocking_issue_code TEXT,
    current_publication_id TEXT,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE INDEX source_scan_states_status_idx
ON source_scan_states (status, dirty, updated_at, source_id);
