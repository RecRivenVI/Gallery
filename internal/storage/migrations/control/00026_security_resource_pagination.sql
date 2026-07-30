-- 阶段 6：安全管理资源的有界 keyset 读取。
--
-- 保留阶段 5 已公开的排序：Session/Token/Share 按创建时间与 ID 升序，账户按
-- 规范化用户名与 ID 升序，Grant 按有效状态、capability、scope 与 ID 升序。

CREATE INDEX sessions_list_idx
ON sessions (created_at, session_id);

CREATE INDEX api_tokens_principal_list_idx
ON api_tokens (principal_id, created_at, token_id);

CREATE INDEX shares_creator_list_idx
ON shares (created_by, created_at, share_id);

CREATE INDEX local_users_list_idx
ON local_users (username_normalized, user_id);

CREATE INDEX authorization_grants_principal_list_idx
ON authorization_grants (
    principal_id,
    (revoked_at IS NOT NULL),
    capability,
    scope_kind,
    scope_id,
    grant_id
);
