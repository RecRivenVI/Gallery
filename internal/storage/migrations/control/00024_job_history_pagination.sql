-- Job 历史使用不可变的 created_at + job_id newest-first keyset 浏览。全量和单一
-- 持久状态各有一条窄索引；cancelling/superseded 等派生状态继续在同一候选索引上追加
-- cancel_requested/stage 过滤，不另复制公开状态事实。
CREATE INDEX jobs_created_page_idx
ON jobs (created_at DESC, job_id DESC);

CREATE INDEX jobs_status_created_page_idx
ON jobs (status, created_at DESC, job_id DESC);
