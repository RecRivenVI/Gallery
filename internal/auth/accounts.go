package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	loginWindow        = 15 * time.Minute
	loginBlockDuration = 15 * time.Minute
	loginFailureLimit  = 8
	// loginPeerFailureLimit 是只按对端的失败上限。按 (用户名, 对端) 计数的桶只能挡住针对
	// 单个账户的爆破：攻击者换一个用户名就换一个全新的桶，因此同一个对端可以无限量地喷洒
	// 用户名，每一次都触发完整的 Argon2 验证。这个桶补上喷洒方向的上限，取值明显宽于单
	// 账户上限，避免正常多账户设备被一两次输错锁死。PRE_FREEZE。
	loginPeerFailureLimit = 64
	UsernameMaxRunes      = 128
)

type User struct {
	ID              string
	Username        string
	DisplayName     string
	Status          string
	Roles           []string
	SecurityVersion int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type CreateUserInput struct {
	Username    string
	DisplayName string
	Password    string
	Roles       []string
	Grants      []GrantInput
}

type GrantInput struct {
	Effect     string
	Capability string
	Scope      ResourceScope
}

func NormalizeUsername(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || !utf8.ValidString(trimmed) || utf8.RuneCountInString(trimmed) > UsernameMaxRunes {
		return "", fault.WithField(fault.CodeValidation, "username", nil)
	}
	for _, r := range trimmed {
		if unicode.IsControl(r) {
			return "", fault.WithField(fault.CodeValidation, "username", nil)
		}
	}
	return cases.Fold().String(norm.NFKC.String(trimmed)), nil
}

func (p *Personal) LANInitialized(ctx context.Context) (bool, error) {
	var owner sql.NullString
	if err := p.db.QueryRowContext(ctx, "SELECT lan_owner_user_id FROM security_state WHERE singleton=1").Scan(&owner); err != nil {
		return false, err
	}
	return owner.Valid && owner.String != "", nil
}

func (p *Personal) InitializeLANOwner(ctx context.Context, input CreateUserInput) (User, error) {
	input.Roles = []string{"owner"}
	passwordHash, usernameNormalized, err := p.prepareUser(ctx, input)
	if err != nil {
		return User{}, err
	}
	now := p.clock.Now().UTC()
	userID, err := p.ids.New(domain.IDUser)
	if err != nil {
		return User{}, fault.New(fault.CodeInternal, true, err)
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	user, err := p.insertUserTx(ctx, tx, userID.String(), usernameNormalized, passwordHash, input, userID.String(), now)
	if err != nil {
		return User{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE security_state
SET lan_owner_user_id=?, lan_initialized_at=?
WHERE singleton=1 AND lan_owner_user_id IS NULL`, userID.String(), now.Unix())
	if err != nil {
		return User{}, fault.New(fault.CodeInternal, true, err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return User{}, fault.New(fault.CodeLANAlreadyInitialized, false, nil)
	}
	if err := p.auditTx(ctx, tx, user.ID, "owner.initialize", "user", user.ID, "success", map[string]any{"mode": "lan"}, now); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, mapConstraint(err)
	}
	return user, nil
}

func (p *Personal) CreateUser(ctx context.Context, actor string, input CreateUserInput) (User, error) {
	if err := p.requirePrincipalCapability(ctx, actor, "users.manage"); err != nil {
		return User{}, err
	}
	passwordHash, usernameNormalized, err := p.prepareUser(ctx, input)
	if err != nil {
		return User{}, err
	}
	now := p.clock.Now().UTC()
	userID, err := p.ids.New(domain.IDUser)
	if err != nil {
		return User{}, fault.New(fault.CodeInternal, true, err)
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	user, err := p.insertUserTx(ctx, tx, userID.String(), usernameNormalized, passwordHash, input, actor, now)
	if err != nil {
		return User{}, err
	}
	if err := p.auditTx(ctx, tx, actor, "user.create", "user", user.ID, "success", map[string]any{"roles": user.Roles}, now); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, mapConstraint(err)
	}
	return user, nil
}

func (p *Personal) prepareUser(ctx context.Context, input CreateUserInput) (string, string, error) {
	usernameNormalized, err := NormalizeUsername(input.Username)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(input.DisplayName) == "" || utf8.RuneCountInString(input.DisplayName) > 256 {
		return "", "", fault.WithField(fault.CodeValidation, "displayName", nil)
	}
	// InitializeLANOwner 同样经过这里，而 POST /api/v1/lan/owner 是未认证端点：即使 LAN
	// Owner 已经初始化，散列也发生在冲突检查之前，因此这次调用必须同样受闸门约束。
	passwordHash, err := p.hashPassword(ctx, input.Password, "password")
	if err != nil {
		return "", "", err
	}
	roles := normalizeCapabilities(input.Roles)
	if len(roles) == 0 {
		return "", "", fault.WithField(fault.CodeValidation, "roles", nil)
	}
	for _, role := range roles {
		if role != "owner" && role != "operator" && role != "viewer" {
			return "", "", fault.WithField(fault.CodeValidation, "roles", nil)
		}
	}
	return passwordHash, usernameNormalized, nil
}

func (p *Personal) insertUserTx(ctx context.Context, tx *sql.Tx, userID, usernameNormalized, passwordHash string, input CreateUserInput, actor string, now time.Time) (User, error) {
	if _, err := tx.ExecContext(ctx, `INSERT INTO security_principals
(principal_id, principal_kind, display_name, status, security_version, created_at, updated_at)
VALUES (?, 'local_user', ?, 'active', 1, ?, ?)`, userID, strings.TrimSpace(input.DisplayName), now.Unix(), now.Unix()); err != nil {
		return User{}, mapConstraint(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO local_users
(user_id, username, username_normalized, password_hash, password_algorithm,
 password_parameters_version, password_changed_at, created_by, created_at, updated_at)
VALUES (?, ?, ?, ?, 'argon2id', ?, ?, ?, ?, ?)`, userID, strings.TrimSpace(input.Username), usernameNormalized,
		passwordHash, PasswordParametersVersion, now.Unix(), actor, now.Unix(), now.Unix()); err != nil {
		return User{}, mapConstraint(err)
	}
	roles := normalizeCapabilities(input.Roles)
	for _, role := range roles {
		if _, err := tx.ExecContext(ctx, `INSERT INTO principal_roles
(principal_id, role_id, assigned_by, assigned_at) VALUES (?, ?, ?, ?)`, userID, role, actor, now.Unix()); err != nil {
			return User{}, mapConstraint(err)
		}
	}
	for _, grant := range input.Grants {
		if _, err := p.insertGrantTx(ctx, tx, actor, userID, grant, now); err != nil {
			return User{}, err
		}
	}
	return User{ID: userID, Username: strings.TrimSpace(input.Username), DisplayName: strings.TrimSpace(input.DisplayName), Status: "active", Roles: roles, SecurityVersion: 1, CreatedAt: now, UpdatedAt: now}, nil
}

func (p *Personal) Login(ctx context.Context, username, password, clientLabel, rateSubject string) (Session, string, error) {
	normalized, err := NormalizeUsername(username)
	if err != nil || len(password) > PasswordMaxBytes {
		return Session{}, "", fault.New(fault.CodeInvalidCredentials, false, nil)
	}
	now := p.clock.Now().UTC()
	accountKey := loginAccountRateKey(normalized, rateSubject)
	peerKey := loginPeerRateKey(rateSubject)
	blocked, err := p.loginBlocked(ctx, now, accountKey, peerKey)
	if err != nil {
		return Session{}, "", fault.New(fault.CodeInternal, true, err)
	}
	if blocked {
		return Session{}, "", fault.New(fault.CodeRateLimited, true, nil)
	}
	var userID, encoded, status string
	var securityVersion int64
	err = p.db.QueryRowContext(ctx, `SELECT u.user_id, u.password_hash, p.status, p.security_version
FROM local_users u JOIN security_principals p ON p.principal_id=u.user_id
WHERE u.username_normalized=?`, normalized).Scan(&userID, &encoded, &status, &securityVersion)
	known := err == nil
	if errors.Is(err, sql.ErrNoRows) {
		// 不存在的用户也要跑完整验证，否则响应时间就是用户名枚举预言机。这条路径必须保留，
		// 它同时意味着未认证请求可以无条件触发 Argon2，因此散列本身必须在闸门之内。
		encoded = p.dummyPasswordHash
	} else if err != nil {
		return Session{}, "", fault.New(fault.CodeInternal, true, err)
	}
	valid, needsRehash, gateErr := p.verifyPassword(ctx, encoded, password)
	if gateErr != nil {
		return Session{}, "", gateErr
	}
	if !known || !valid || status != "active" {
		_ = p.recordLoginFailure(ctx, now, accountKey, peerKey)
		return Session{}, "", fault.New(fault.CodeInvalidCredentials, false, nil)
	}
	if needsRehash {
		// 参数升级是尽力而为：名额耗尽时跳过本次重算，下次登录再升级。
		if upgraded, hashErr := p.hashPassword(ctx, password, "password"); hashErr == nil {
			_, _ = p.db.ExecContext(ctx, `UPDATE local_users SET password_hash=?, password_parameters_version=?, updated_at=? WHERE user_id=?`, upgraded, PasswordParametersVersion, now.Unix(), userID)
		}
	}
	// 只清除 (用户名, 对端) 桶。按对端的喷洒计数不能被「攻击者自己持有的某个有效账户登录
	// 成功」重置，否则整条按对端的限流一行代码就能绕开。
	_, _ = p.db.ExecContext(ctx, "DELETE FROM login_rate_limits WHERE subject_hash=?", accountKey)
	return p.createSession(ctx, userID, securityVersion, "password", clientLabel)
}

// loginAccountRateKey 是「同一账户 + 同一对端」的失败计数键，挡单账户爆破。
func loginAccountRateKey(normalizedUsername, subject string) string {
	return hashToken(normalizedUsername + "\x00" + subject)
}

// loginPeerRateKey 是只按对端的失败计数键，挡换用户名就换桶的喷洒。NormalizeUsername
// 拒绝所有控制字符，因此以 \x00 开头的前缀不可能与任何账户键的原像相撞；login_rate_limits
// 的主键本来就是不透明的 subject_hash，两类桶共用同一张表不需要改 schema。
func loginPeerRateKey(subject string) string {
	return hashToken("\x00gallery-login-peer-v1\x00" + subject)
}

func (p *Personal) createSession(ctx context.Context, principalID string, securityVersion int64, method, clientLabel string) (Session, string, error) {
	now := p.clock.Now().UTC()
	id, err := p.ids.New(domain.IDSession)
	if err != nil {
		return Session{}, "", fault.New(fault.CodeInternal, true, err)
	}
	secret, err := randomToken(p.random, sessionTokenBytes)
	if err != nil {
		return Session{}, "", fault.New(fault.CodeInternal, true, err)
	}
	csrf := csrfToken(secret)
	abs := now.Add(SessionLifetime)
	idle := minTime(now.Add(SessionIdleLifetime), abs)
	if utf8.RuneCountInString(clientLabel) > 256 {
		return Session{}, "", fault.WithField(fault.CodeValidation, "clientLabel", nil)
	}
	_, err = p.db.ExecContext(ctx, `INSERT INTO sessions
(session_id, secret_hash, principal_id, csrf_hash, auth_method, client_label,
 principal_security_version, created_at, absolute_expires_at, idle_expires_at, last_seen_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id.String(), hashToken(secret), principalID, hashToken(csrf), method,
		clientLabel, securityVersion, now.Unix(), abs.Unix(), idle.Unix(), now.Unix())
	if err != nil {
		return Session{}, "", fault.New(fault.CodeInternal, true, err)
	}
	capabilities, err := principalCapabilities(ctx, p.db, principalID)
	if err != nil {
		return Session{}, "", fault.New(fault.CodeInternal, true, err)
	}
	session := Session{ID: id.String(), PrincipalID: principalID, CSRFToken: csrf, CreatedAt: now,
		ExpiresAt: abs, LastSeenAt: now, Capabilities: capabilities, AuthMethod: method,
		ClientLabel: clientLabel, SecurityVersion: securityVersion}
	return session, session.ID + "." + secret, nil
}

func (p *Personal) SetUserStatus(ctx context.Context, actor, userID, status string) error {
	if err := p.requirePrincipalCapability(ctx, actor, "users.manage"); err != nil {
		return err
	}
	if status != "active" && status != "disabled" && status != "deleted" {
		return fault.WithField(fault.CodeValidation, "status", nil)
	}
	if _, err := domain.ParseID(domain.IDUser, userID); err != nil {
		return fault.New(fault.CodeNotFound, false, nil)
	}
	now := p.clock.Now().UTC()
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	var isLANOwner int
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM security_state WHERE lan_owner_user_id=?)", userID).Scan(&isLANOwner); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if isLANOwner != 0 && status != "active" {
		return fault.New(fault.CodeConflict, false, nil)
	}
	result, err := tx.ExecContext(ctx, `UPDATE security_principals
SET status=?, security_version=security_version+1, updated_at=?
WHERE principal_id=? AND principal_kind='local_user'`, status, now.Unix(), userID)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return fault.New(fault.CodeNotFound, false, nil)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE sessions SET revoked_at=COALESCE(revoked_at, ?) WHERE principal_id=?", now.Unix(), userID); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE api_tokens SET revoked_at=COALESCE(revoked_at, ?) WHERE principal_id=?", now.Unix(), userID); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if err := p.auditTx(ctx, tx, actor, "user.status", "user", userID, "success", map[string]any{"status": status}, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return nil
}

func (p *Personal) ChangePassword(ctx context.Context, actor Session, currentPassword, newPassword string) error {
	if !p.IsActive(ctx, actor.ID) {
		return fault.New(fault.CodeUnauthenticated, false, nil)
	}
	if actor.TokenID != "" || actor.AuthMethod != "password" {
		return fault.New(fault.CodeForbidden, false, nil)
	}
	var encoded string
	if err := p.db.QueryRowContext(ctx, "SELECT password_hash FROM local_users WHERE user_id=?", actor.PrincipalID).Scan(&encoded); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fault.New(fault.CodeNotFound, false, nil)
		}
		return fault.New(fault.CodeInternal, true, err)
	}
	valid, _, gateErr := p.verifyPassword(ctx, encoded, currentPassword)
	if gateErr != nil {
		return gateErr
	}
	if !valid {
		return fault.New(fault.CodeInvalidCredentials, false, nil)
	}
	newHash, err := p.hashPassword(ctx, newPassword, "newPassword")
	if err != nil {
		return err
	}
	now := p.clock.Now().UTC()
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE local_users
SET password_hash=?, password_parameters_version=?, password_changed_at=?, updated_at=?
WHERE user_id=?`, newHash, PasswordParametersVersion, now.Unix(), now.Unix(), actor.PrincipalID); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE security_principals
SET security_version=security_version+1, updated_at=? WHERE principal_id=?`, now.Unix(), actor.PrincipalID); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE sessions SET revoked_at=COALESCE(revoked_at, ?) WHERE principal_id=?", now.Unix(), actor.PrincipalID); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE api_tokens SET revoked_at=COALESCE(revoked_at, ?) WHERE principal_id=?", now.Unix(), actor.PrincipalID); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if err := p.auditTx(ctx, tx, actor.PrincipalID, "password.change", "user", actor.PrincipalID, "success", map[string]any{}, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return nil
}

func (p *Personal) CreateGrant(ctx context.Context, actor, principalID string, input GrantInput) (Grant, error) {
	if err := p.requirePrincipalCapability(ctx, actor, "users.manage"); err != nil {
		return Grant{}, err
	}
	if _, err := domain.ParseID(domain.IDUser, principalID); err != nil {
		return Grant{}, fault.New(fault.CodeNotFound, false, nil)
	}
	now := p.clock.Now().UTC()
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return Grant{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	grant, err := p.insertGrantTx(ctx, tx, actor, principalID, input, now)
	if err != nil {
		return Grant{}, err
	}
	if err := p.auditTx(ctx, tx, actor, "grant.create", "grant", grant.ID, "success",
		map[string]any{"principalId": principalID, "effect": input.Effect, "capability": input.Capability, "scope": input.Scope}, now); err != nil {
		return Grant{}, err
	}
	if err := invalidatePrincipalCredentialsTx(ctx, tx, principalID, now); err != nil {
		return Grant{}, err
	}
	if err := tx.Commit(); err != nil {
		return Grant{}, mapConstraint(err)
	}
	return grant, nil
}

func (p *Personal) RevokeGrant(ctx context.Context, actor, grantID string) error {
	if err := p.requirePrincipalCapability(ctx, actor, "users.manage"); err != nil {
		return err
	}
	if _, err := domain.ParseID(domain.IDGrant, grantID); err != nil {
		return fault.New(fault.CodeNotFound, false, nil)
	}
	now := p.clock.Now().UTC()
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	var principalID string
	if err := tx.QueryRowContext(ctx, "SELECT principal_id FROM authorization_grants WHERE grant_id=?", grantID).Scan(&principalID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fault.New(fault.CodeNotFound, false, nil)
		}
		return fault.New(fault.CodeInternal, true, err)
	}
	result, err := tx.ExecContext(ctx, "UPDATE authorization_grants SET revoked_at=COALESCE(revoked_at, ?) WHERE grant_id=?", now.Unix(), grantID)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return fault.New(fault.CodeNotFound, false, nil)
	}
	if err := p.auditTx(ctx, tx, actor, "grant.revoke", "grant", grantID, "success", map[string]any{}, now); err != nil {
		return err
	}
	if err := invalidatePrincipalCredentialsTx(ctx, tx, principalID, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (p *Personal) ListGrants(ctx context.Context, actor, principalID string) ([]Grant, error) {
	if err := p.requirePrincipalCapability(ctx, actor, "users.manage"); err != nil {
		return nil, err
	}
	if _, err := domain.ParseID(domain.IDUser, principalID); err != nil {
		return nil, fault.New(fault.CodeNotFound, false, nil)
	}
	var exists int
	if err := p.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM security_principals WHERE principal_id=?", principalID).Scan(&exists); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	if exists == 0 {
		return nil, fault.New(fault.CodeNotFound, false, nil)
	}
	rows, err := p.db.QueryContext(ctx, `SELECT grant_id, principal_id, effect, capability,
scope_kind, scope_id, created_by, revoked_at IS NOT NULL
FROM authorization_grants WHERE principal_id=?
ORDER BY revoked_at IS NOT NULL, capability, scope_kind, scope_id, grant_id`, principalID)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	result := make([]Grant, 0)
	for rows.Next() {
		var item Grant
		if err := rows.Scan(&item.ID, &item.PrincipalID, &item.Effect, &item.Capability,
			&item.Scope.Kind, &item.Scope.ID, &item.CreatedBy, &item.Revoked); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	return result, nil
}

func invalidatePrincipalCredentialsTx(ctx context.Context, tx *sql.Tx, principalID string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `UPDATE security_principals
SET security_version=security_version+1, updated_at=? WHERE principal_id=?`, now.Unix(), principalID); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE sessions SET revoked_at=COALESCE(revoked_at, ?) WHERE principal_id=?", now.Unix(), principalID); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE api_tokens SET revoked_at=COALESCE(revoked_at, ?) WHERE principal_id=?", now.Unix(), principalID); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return nil
}

func (p *Personal) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT u.user_id, u.username, p.display_name, p.status,
p.security_version, p.created_at, p.updated_at
FROM local_users u JOIN security_principals p ON p.principal_id=u.user_id
ORDER BY u.username_normalized, u.user_id`)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	var result []User
	for rows.Next() {
		var user User
		var createdAt, updatedAt int64
		if err := rows.Scan(&user.ID, &user.Username, &user.DisplayName, &user.Status, &user.SecurityVersion, &createdAt, &updatedAt); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		user.CreatedAt = time.Unix(createdAt, 0).UTC()
		user.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		roleRows, err := p.db.QueryContext(ctx, "SELECT role_id FROM principal_roles WHERE principal_id=? ORDER BY role_id", user.ID)
		if err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		for roleRows.Next() {
			var role string
			if err := roleRows.Scan(&role); err != nil {
				roleRows.Close()
				return nil, fault.New(fault.CodeInternal, true, err)
			}
			user.Roles = append(user.Roles, role)
		}
		if err := roleRows.Close(); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		result = append(result, user)
	}
	return result, rows.Err()
}

func (p *Personal) insertGrantTx(ctx context.Context, tx *sql.Tx, actor, principalID string, input GrantInput, now time.Time) (Grant, error) {
	if input.Effect != "allow" && input.Effect != "deny" {
		return Grant{}, fault.WithField(fault.CodeValidation, "effect", nil)
	}
	if input.Capability == "" || !validResourceScope(input.Scope) {
		return Grant{}, fault.WithField(fault.CodeValidation, "scope", nil)
	}
	var available int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM principal_roles pr
JOIN security_role_capabilities rc ON rc.role_id=pr.role_id
WHERE pr.principal_id=? AND rc.capability=?)`, principalID, input.Capability).Scan(&available); err != nil {
		return Grant{}, fault.New(fault.CodeInternal, true, err)
	}
	if available == 0 {
		return Grant{}, fault.New(fault.CodeForbidden, false, nil)
	}
	id, err := p.ids.New(domain.IDGrant)
	if err != nil {
		return Grant{}, fault.New(fault.CodeInternal, true, err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO authorization_grants
(grant_id, principal_id, effect, capability, scope_kind, scope_id, created_by, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id.String(), principalID, input.Effect, input.Capability,
		input.Scope.Kind, input.Scope.ID, actor, now.Unix())
	if err != nil {
		return Grant{}, mapConstraint(err)
	}
	return Grant{ID: id.String(), PrincipalID: principalID, Effect: input.Effect, Capability: input.Capability,
		Scope: input.Scope, CreatedBy: actor}, nil
}

func (p *Personal) auditTx(ctx context.Context, tx *sql.Tx, actor, action, targetKind, targetID, outcome string, detail any, now time.Time) error {
	id, err := p.ids.New(domain.IDSecurityAudit)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if len(encoded) > 16*1024 {
		return fault.New(fault.CodeInternal, false, fmt.Errorf("安全审计 detail 超限"))
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO security_audits
(audit_id, action, actor_principal_id, target_kind, target_id, outcome, detail_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id.String(), action, nullableActor(actor), targetKind, targetID, outcome, string(encoded), now.Unix())
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return nil
}

func nullableActor(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// loginBlocked 只要有一个桶处于封禁期就判定为封禁：按账户的桶挡爆破，按对端的桶挡喷洒。
func (p *Personal) loginBlocked(ctx context.Context, now time.Time, keys ...string) (bool, error) {
	for _, key := range keys {
		var blocked sql.NullInt64
		err := p.db.QueryRowContext(ctx, "SELECT blocked_until FROM login_rate_limits WHERE subject_hash=?", key).Scan(&blocked)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return false, err
		}
		if blocked.Valid && now.Unix() < blocked.Int64 {
			return true, nil
		}
	}
	return false, nil
}

func (p *Personal) recordLoginFailure(ctx context.Context, now time.Time, accountKey, peerKey string) error {
	if err := p.recordLoginFailureFor(ctx, accountKey, loginFailureLimit, now); err != nil {
		return err
	}
	return p.recordLoginFailureFor(ctx, peerKey, loginPeerFailureLimit, now)
}

func (p *Personal) recordLoginFailureFor(ctx context.Context, key string, limit int, now time.Time) error {
	windowStart := now.Add(-loginWindow).Unix()
	_, err := p.db.ExecContext(ctx, `INSERT INTO login_rate_limits
(subject_hash, window_started_at, failure_count, blocked_until) VALUES (?, ?, 1, NULL)
ON CONFLICT(subject_hash) DO UPDATE SET
 window_started_at=CASE WHEN login_rate_limits.window_started_at < ? THEN excluded.window_started_at ELSE login_rate_limits.window_started_at END,
 failure_count=CASE WHEN login_rate_limits.window_started_at < ? THEN 1 ELSE login_rate_limits.failure_count+1 END,
 blocked_until=CASE
   WHEN (CASE WHEN login_rate_limits.window_started_at < ? THEN 1 ELSE login_rate_limits.failure_count+1 END) >= ?
   THEN ? ELSE login_rate_limits.blocked_until END`, key, now.Unix(), windowStart, windowStart, windowStart,
		limit, now.Add(loginBlockDuration).Unix())
	return err
}

func mapConstraint(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
		return fault.New(fault.CodeConflict, false, err)
	}
	return fault.New(fault.CodeInternal, true, err)
}
