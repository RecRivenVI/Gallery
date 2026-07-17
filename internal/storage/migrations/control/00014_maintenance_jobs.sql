-- maintenance 类 Job（control_backup、control_restore）不绑定 source_id，与 scan/overlay 共用
-- jobs 表和状态机，但需要各自的单活跃约束：任一类别在 queued/running/publishing 期间至多一个，
-- 避免并发备份或并发恢复相互破坏一致性。索引以 job_type 常量为条件，不改变既有 scan 约束。
CREATE UNIQUE INDEX jobs_one_active_control_backup
ON jobs (job_type)
WHERE job_type = 'control_backup' AND status IN ('queued', 'running', 'publishing');

CREATE UNIQUE INDEX jobs_one_active_control_restore
ON jobs (job_type)
WHERE job_type = 'control_restore' AND status IN ('queued', 'running', 'publishing');
