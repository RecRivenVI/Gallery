-- 阶段 3 Correctness：同一逻辑 Job 的多 Attempt、退避调度与活动 Attempt 唯一性。
-- retry_of 保留为 v18 及更早数据的兼容来源字段；新重试不再创建子 Job。
ALTER TABLE jobs ADD COLUMN next_attempt_at INTEGER;
ALTER TABLE jobs ADD COLUMN retry_requested_by TEXT;

CREATE INDEX jobs_runnable_due_idx
ON jobs (status, next_attempt_at, resource_class, created_at, job_id);

-- v18 只在 Attempt 开始运行时写 job_attempts。升级时为尚无 Attempt 历史的 Job 补一条
-- 可解释记录；已有历史保持原样。
INSERT INTO job_attempts
(attempt_id, job_id, attempt, resource_class, status, started_at, heartbeat_at, finished_at,
 lease_owner, lease_expires_at, error_code, error_retryable, progress_sequence, result_json,
 created_at, updated_at)
SELECT
    job_id || ':' || attempt,
    job_id,
    attempt,
    resource_class,
    CASE status
        WHEN 'queued' THEN 'queued'
        WHEN 'running' THEN 'running'
        WHEN 'publishing' THEN 'running'
        WHEN 'completed' THEN 'completed'
        WHEN 'cancelled' THEN 'cancelled'
        ELSE 'failed'
    END,
    started_at,
    heartbeat_at,
    finished_at,
    lease_owner,
    lease_expires_at,
    issue_code,
    failure_retryable,
    progress_sequence,
    result_json,
    created_at,
    updated_at
FROM jobs
WHERE NOT EXISTS (
    SELECT 1 FROM job_attempts a
    WHERE a.job_id = jobs.job_id AND a.attempt = jobs.attempt
);

CREATE UNIQUE INDEX job_attempts_one_active_per_job
ON job_attempts (job_id)
WHERE status IN ('queued', 'running');

CREATE TABLE job_temp_directories (
    job_id TEXT NOT NULL REFERENCES jobs(job_id) ON DELETE CASCADE,
    attempt INTEGER NOT NULL,
    task_type TEXT NOT NULL,
    relative_path TEXT NOT NULL,
    manifest_version INTEGER NOT NULL,
    expected_outputs_json TEXT NOT NULL DEFAULT '[]',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (job_id, attempt),
    UNIQUE (relative_path),
    FOREIGN KEY (job_id, attempt) REFERENCES job_attempts(job_id, attempt) ON DELETE CASCADE
) STRICT;

CREATE INDEX job_temp_directories_updated_idx
ON job_temp_directories (updated_at, job_id, attempt);
