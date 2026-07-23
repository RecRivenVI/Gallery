-- 阶段 5：账户、凭据、Session、资源授权、API Token、分享和安全审计。
-- 旧 Personal Session 在本次安全模型升级时主动失效：历史表把 CSRF secret 以明文保存，
-- 不能安全迁移到只存摘要的新结构。配对凭据本来只存 SHA-256 摘要，继续保留并按既有
-- 短时/单次消费语义工作。

CREATE TABLE security_principals (
    principal_id TEXT PRIMARY KEY NOT NULL,
    principal_kind TEXT NOT NULL CHECK (principal_kind IN ('personal_owner', 'local_user')),
    display_name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'disabled', 'deleted')),
    security_version INTEGER NOT NULL DEFAULT 1 CHECK (security_version >= 1),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

INSERT INTO security_principals
(principal_id, principal_kind, display_name, status, security_version, created_at, updated_at)
VALUES ('personal-owner', 'personal_owner', 'Personal Owner', 'active', 1, 0, 0);

ALTER TABLE sessions RENAME TO sessions_phase0_legacy;

CREATE TABLE sessions (
    session_id TEXT PRIMARY KEY NOT NULL,
    secret_hash TEXT NOT NULL CHECK (length(secret_hash) = 64),
    principal_id TEXT NOT NULL REFERENCES security_principals(principal_id) ON DELETE RESTRICT,
    csrf_hash TEXT NOT NULL CHECK (length(csrf_hash) = 64),
    auth_method TEXT NOT NULL CHECK (auth_method IN ('personal_pairing', 'password')),
    client_label TEXT NOT NULL DEFAULT '',
    principal_security_version INTEGER NOT NULL CHECK (principal_security_version >= 1),
    created_at INTEGER NOT NULL,
    absolute_expires_at INTEGER NOT NULL,
    idle_expires_at INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    revoked_at INTEGER,
    CHECK (idle_expires_at <= absolute_expires_at)
) STRICT;

DROP TABLE sessions_phase0_legacy;

CREATE INDEX sessions_active_expiry_idx
ON sessions (absolute_expires_at, idle_expires_at, revoked_at);
CREATE INDEX sessions_principal_idx
ON sessions (principal_id, revoked_at, created_at);

CREATE TABLE local_users (
    user_id TEXT PRIMARY KEY NOT NULL REFERENCES security_principals(principal_id) ON DELETE RESTRICT,
    username TEXT NOT NULL,
    username_normalized TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    password_algorithm TEXT NOT NULL CHECK (password_algorithm = 'argon2id'),
    password_parameters_version INTEGER NOT NULL CHECK (password_parameters_version >= 1),
    password_changed_at INTEGER NOT NULL,
    created_by TEXT NOT NULL REFERENCES security_principals(principal_id) ON DELETE RESTRICT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE security_state (
    singleton INTEGER PRIMARY KEY NOT NULL CHECK (singleton = 1),
    lan_owner_user_id TEXT REFERENCES local_users(user_id) ON DELETE RESTRICT,
    lan_initialized_at INTEGER,
    CHECK ((lan_owner_user_id IS NULL) = (lan_initialized_at IS NULL))
) STRICT;

INSERT INTO security_state (singleton, lan_owner_user_id, lan_initialized_at)
VALUES (1, NULL, NULL);

CREATE TABLE security_roles (
    role_id TEXT PRIMARY KEY NOT NULL,
    display_name TEXT NOT NULL,
    built_in INTEGER NOT NULL DEFAULT 1 CHECK (built_in IN (0, 1))
) STRICT;

CREATE TABLE security_role_capabilities (
    role_id TEXT NOT NULL REFERENCES security_roles(role_id) ON DELETE CASCADE,
    capability TEXT NOT NULL,
    PRIMARY KEY (role_id, capability)
) STRICT;

CREATE TABLE principal_roles (
    principal_id TEXT NOT NULL REFERENCES security_principals(principal_id) ON DELETE CASCADE,
    role_id TEXT NOT NULL REFERENCES security_roles(role_id) ON DELETE RESTRICT,
    assigned_by TEXT NOT NULL REFERENCES security_principals(principal_id) ON DELETE RESTRICT,
    assigned_at INTEGER NOT NULL,
    PRIMARY KEY (principal_id, role_id)
) STRICT;

INSERT INTO security_roles (role_id, display_name, built_in) VALUES
    ('owner', 'Owner', 1),
    ('operator', 'Operator', 1),
    ('viewer', 'Viewer', 1);

INSERT INTO security_role_capabilities (role_id, capability) VALUES
    ('viewer', 'bindings.read'),
    ('viewer', 'library.read'),
    ('viewer', 'media.read'),
    ('viewer', 'rules.read'),
    ('viewer', 'tokens.manage'),
    ('operator', 'bindings.read'),
    ('operator', 'library.read'),
    ('operator', 'media.read'),
    ('operator', 'media.derive'),
    ('operator', 'overlays.write'),
    ('operator', 'rules.read'),
    ('operator', 'scan.run'),
    ('operator', 'tokens.manage'),
    ('owner', 'admin.backup'),
    ('owner', 'admin.maintenance'),
    ('owner', 'admin.restore'),
    ('owner', 'audit.read'),
    ('owner', 'bindings.read'),
    ('owner', 'bindings.write'),
    ('owner', 'clients.manage'),
    ('owner', 'creators.write'),
    ('owner', 'library.read'),
    ('owner', 'library.write'),
    ('owner', 'media.derive'),
    ('owner', 'media.read'),
    ('owner', 'overlays.write'),
    ('owner', 'rules.debug'),
    ('owner', 'rules.publish'),
    ('owner', 'rules.read'),
    ('owner', 'rules.write'),
    ('owner', 'scan.run'),
    ('owner', 'shares.create'),
    ('owner', 'tokens.manage'),
    ('owner', 'users.manage');

INSERT INTO principal_roles (principal_id, role_id, assigned_by, assigned_at)
VALUES ('personal-owner', 'owner', 'personal-owner', 0);

CREATE TABLE authorization_grants (
    grant_id TEXT PRIMARY KEY NOT NULL,
    principal_id TEXT NOT NULL REFERENCES security_principals(principal_id) ON DELETE CASCADE,
    effect TEXT NOT NULL CHECK (effect IN ('allow', 'deny')),
    capability TEXT NOT NULL,
    scope_kind TEXT NOT NULL CHECK (scope_kind IN ('global', 'library', 'source')),
    scope_id TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL REFERENCES security_principals(principal_id) ON DELETE RESTRICT,
    created_at INTEGER NOT NULL,
    revoked_at INTEGER,
    CHECK ((scope_kind = 'global' AND scope_id = '') OR
           (scope_kind IN ('library', 'source') AND length(scope_id) > 0)),
    UNIQUE (principal_id, effect, capability, scope_kind, scope_id)
) STRICT;

CREATE INDEX authorization_grants_principal_idx
ON authorization_grants (principal_id, capability, revoked_at, scope_kind, scope_id);

CREATE TABLE api_tokens (
    token_id TEXT PRIMARY KEY NOT NULL,
    principal_id TEXT NOT NULL REFERENCES security_principals(principal_id) ON DELETE CASCADE,
    secret_hash TEXT NOT NULL UNIQUE CHECK (length(secret_hash) = 64),
    secret_prefix TEXT NOT NULL,
    name TEXT NOT NULL,
    capabilities_json TEXT NOT NULL,
    scopes_json TEXT NOT NULL,
    principal_security_version INTEGER NOT NULL CHECK (principal_security_version >= 1),
    created_by TEXT NOT NULL REFERENCES security_principals(principal_id) ON DELETE RESTRICT,
    created_at INTEGER NOT NULL,
    expires_at INTEGER,
    last_used_at INTEGER,
    revoked_at INTEGER
) STRICT;

CREATE INDEX api_tokens_principal_idx
ON api_tokens (principal_id, revoked_at, expires_at, created_at);

CREATE TABLE shares (
    share_id TEXT PRIMARY KEY NOT NULL,
    secret_hash TEXT NOT NULL UNIQUE CHECK (length(secret_hash) = 64),
    secret_prefix TEXT NOT NULL,
    created_by TEXT NOT NULL REFERENCES security_principals(principal_id) ON DELETE RESTRICT,
    scope_kind TEXT NOT NULL CHECK (scope_kind IN ('library', 'work', 'media')),
    scope_id TEXT NOT NULL,
    permissions_json TEXT NOT NULL,
    fixed_blob_algorithm TEXT,
    fixed_blob_digest TEXT,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    revoked_at INTEGER,
    CHECK ((fixed_blob_algorithm IS NULL AND fixed_blob_digest IS NULL) OR
           (fixed_blob_algorithm = 'sha256-v1' AND length(fixed_blob_digest) = 64))
) STRICT;

CREATE INDEX shares_created_by_idx
ON shares (created_by, revoked_at, expires_at, created_at);

CREATE TABLE security_audits (
    audit_id TEXT PRIMARY KEY NOT NULL,
    action TEXT NOT NULL,
    actor_principal_id TEXT,
    target_kind TEXT NOT NULL,
    target_id TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('success', 'rejected')),
    detail_json TEXT NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX security_audits_created_idx
ON security_audits (created_at, audit_id);
CREATE INDEX security_audits_target_idx
ON security_audits (target_kind, target_id, created_at);

CREATE TABLE login_rate_limits (
    subject_hash TEXT PRIMARY KEY NOT NULL CHECK (length(subject_hash) = 64),
    window_started_at INTEGER NOT NULL,
    failure_count INTEGER NOT NULL CHECK (failure_count >= 0),
    blocked_until INTEGER
) STRICT;

INSERT INTO schema_freeze (freeze_phase, subject, classification, note) VALUES
    ('phase5', 'security.principal_role_capability', 'COMPATIBILITY_BASELINE',
     'Principal 是授权主体，Role 只提供 capability 上限，服务端按资源 Grant 和凭据状态计算 effective'),
    ('phase5', 'security.argon2id_parameters', 'PRE_FREEZE',
     'Argon2id PHC 格式与参数版本已实装，成本参数待目标设备基准后冻结'),
    ('phase5', 'security.session_lifetimes', 'PRE_FREEZE',
     'Session 空闲和绝对过期数值待 LAN 多设备证据后冻结'),
    ('phase5', 'security.token_secret_storage', 'COMPATIBILITY_BASELINE',
     'API Token secret 只展示一次，control.db 仅保存 SHA-256 摘要、前缀、scope 和状态'),
    ('phase5', 'security.restore_invalidation', 'COMPATIBILITY_BASELINE',
     '恢复后 Session、API Token、pairing 与非终态安全操作失效，用户/角色/Grant/审计保留');
