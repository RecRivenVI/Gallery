package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/ports"
)

const (
	CookieName          = "gallery_session"
	PersonalOwnerID     = "personal-owner"
	PairingLifetime     = 5 * time.Minute
	SessionLifetime     = 30 * 24 * time.Hour // 兼容别名：绝对有效期，阶段 5 参数仍为 PRE_FREEZE。
	SessionIdleLifetime = 24 * time.Hour
	pairingTokenBytes   = 32
	sessionTokenBytes   = 32
)

var PersonalOwnerCapabilities = []string{
	"admin.backup",
	"admin.maintenance",
	"admin.restore",
	"bindings.read",
	"bindings.write",
	"clients.manage",
	"creators.write",
	"files.browse",
	"library.read",
	"library.write",
	"media.derive",
	"media.read",
	"overlays.write",
	"rules.read",
	"rules.write",
	"rules.publish",
	"rules.debug",
	"scan.run",
	"shares.create",
	"tokens.manage",
	"users.manage",
	"audit.read",
}

type Session struct {
	ID              string
	PrincipalID     string
	CSRFToken       string
	CreatedAt       time.Time
	ExpiresAt       time.Time
	LastSeenAt      time.Time
	RevokedAt       *time.Time
	Capabilities    []string
	AuthMethod      string
	ClientLabel     string
	SecurityVersion int64
	TokenID         string
	TokenScopes     []ResourceScope
}

type PairingAttempt struct {
	Credential string
	ExpiresAt  time.Time
}

type Personal struct {
	db                *sql.DB
	clock             ports.Clock
	ids               ports.IDGenerator
	random            io.Reader
	bootstrapCSRF     string
	dummyPasswordHash string
	passwordGate      *PasswordGate
}

// SecurityOptions 只在需要偏离默认安全参数时提供，零值等价于默认值。
type SecurityOptions struct {
	// PasswordConcurrency 与 PasswordGateWait 控制 Argon2 并发闸门；为非正值时使用
	// DefaultPasswordConcurrency 与 defaultPasswordGateWait。只在测试中显式收窄，用于
	// 确定性地触发名额耗尽。
	PasswordConcurrency int
	PasswordGateWait    time.Duration
}

func NewPersonal(db *sql.DB, clock ports.Clock, ids ports.IDGenerator, random io.Reader, options ...SecurityOptions) (*Personal, error) {
	if db == nil || clock == nil || ids == nil {
		return nil, fmt.Errorf("Personal auth 缺少依赖")
	}
	if random == nil {
		random = rand.Reader
	}
	var option SecurityOptions
	if len(options) > 0 {
		option = options[0]
	}
	csrf, err := randomToken(random, sessionTokenBytes)
	if err != nil {
		return nil, err
	}
	// 这次散列发生在进程启动、任何请求可达之前，是全过程唯一不经闸门的 Argon2 调用。
	dummyPasswordHash, err := HashPassword("gallery-dummy-password-never-used", random)
	if err != nil {
		return nil, err
	}
	return &Personal{db: db, clock: clock, ids: ids, random: random, bootstrapCSRF: csrf,
		dummyPasswordHash: dummyPasswordHash,
		passwordGate:      NewPasswordGate(option.PasswordConcurrency, option.PasswordGateWait)}, nil
}

func (p *Personal) BootstrapCSRF() string { return p.bootstrapCSRF }

// PasswordGate 暴露 Argon2 并发闸门，供门禁与回归测试观测实际并发散列数。
func (p *Personal) PasswordGate() *PasswordGate { return p.passwordGate }

// hashPassword 在持有 Argon2 名额的前提下计算口令散列。名额耗尽或请求取消时返回闸门的
// 结构化错误，不与「口令不合法」混同。
func (p *Personal) hashPassword(ctx context.Context, password, field string) (string, error) {
	release, gateErr := p.passwordGate.Acquire(ctx)
	if gateErr != nil {
		return "", gateErr
	}
	defer release()
	encoded, err := HashPassword(password, p.random)
	if err != nil {
		return "", fault.WithField(fault.CodeValidation, field, err)
	}
	return encoded, nil
}

// verifyPassword 在持有 Argon2 名额的前提下验证口令。散列格式非法等价于验证失败；只有
// 闸门错误（名额耗尽、请求取消）才作为错误返回，因此调用方不会把限流当成凭据错误。
func (p *Personal) verifyPassword(ctx context.Context, encoded, password string) (valid, needsRehash bool, gateErr error) {
	release, gateErr := p.passwordGate.Acquire(ctx)
	if gateErr != nil {
		return false, false, gateErr
	}
	defer release()
	valid, needsRehash, verifyErr := VerifyPassword(encoded, password)
	if verifyErr != nil {
		return false, false, nil
	}
	return valid, needsRehash, nil
}

func (p *Personal) AvailableCapabilities() []string {
	return append([]string(nil), PersonalOwnerCapabilities...)
}

func (p *Personal) AvailableCapabilitiesForPrincipal(ctx context.Context, principalID string) ([]string, error) {
	capabilities, err := principalCapabilities(ctx, p.db, principalID)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	return capabilities, nil
}

func (p *Personal) CreatePairingAttempt(ctx context.Context) (PairingAttempt, error) {
	credential, err := randomToken(p.random, pairingTokenBytes)
	if err != nil {
		return PairingAttempt{}, fault.New(fault.CodeInternal, true, err)
	}
	now := p.clock.Now().UTC()
	expires := now.Add(PairingLifetime)
	if _, err := p.db.ExecContext(ctx,
		"INSERT INTO pairing_attempts (credential_hash, created_at, expires_at) VALUES (?, ?, ?)",
		hashToken(credential), now.Unix(), expires.Unix(),
	); err != nil {
		return PairingAttempt{}, fault.New(fault.CodeInternal, true, err)
	}
	return PairingAttempt{Credential: credential, ExpiresAt: expires}, nil
}

func (p *Personal) Exchange(ctx context.Context, credential string) (Session, string, error) {
	if credential == "" {
		return Session{}, "", fault.New(fault.CodePairingInvalid, false, nil)
	}
	now := p.clock.Now().UTC()
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, "", fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	var expiresAt int64
	var usedAt sql.NullInt64
	err = tx.QueryRowContext(ctx,
		"SELECT expires_at, used_at FROM pairing_attempts WHERE credential_hash = ?",
		hashToken(credential),
	).Scan(&expiresAt, &usedAt)
	if errors.Is(err, sql.ErrNoRows) || usedAt.Valid {
		return Session{}, "", fault.New(fault.CodePairingInvalid, false, nil)
	}
	if err != nil {
		return Session{}, "", fault.New(fault.CodeInternal, true, err)
	}
	if now.Unix() >= expiresAt {
		return Session{}, "", fault.New(fault.CodePairingExpired, false, nil)
	}
	result, err := tx.ExecContext(ctx,
		"UPDATE pairing_attempts SET used_at = ? WHERE credential_hash = ? AND used_at IS NULL",
		now.Unix(), hashToken(credential),
	)
	if err != nil {
		return Session{}, "", fault.New(fault.CodeInternal, true, err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return Session{}, "", fault.New(fault.CodePairingInvalid, false, err)
	}

	id, err := p.ids.New(domain.IDSession)
	if err != nil {
		return Session{}, "", fault.New(fault.CodeInternal, true, err)
	}
	secret, err := randomToken(p.random, sessionTokenBytes)
	if err != nil {
		return Session{}, "", fault.New(fault.CodeInternal, true, err)
	}
	csrf := csrfToken(secret)
	session := Session{
		ID: id.String(), PrincipalID: PersonalOwnerID, CSRFToken: csrf,
		CreatedAt: now, ExpiresAt: now.Add(SessionLifetime), LastSeenAt: now,
		Capabilities: append([]string(nil), PersonalOwnerCapabilities...), AuthMethod: "personal_pairing", SecurityVersion: 1,
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO sessions
(session_id, secret_hash, principal_id, csrf_hash, auth_method, client_label,
 principal_security_version, created_at, absolute_expires_at, idle_expires_at, last_seen_at)
VALUES (?, ?, ?, ?, ?, '', ?, ?, ?, ?, ?)`, session.ID, hashToken(secret), session.PrincipalID,
		hashToken(csrf), session.AuthMethod, session.SecurityVersion, session.CreatedAt.Unix(),
		session.ExpiresAt.Unix(), minTime(session.CreatedAt.Add(SessionIdleLifetime), session.ExpiresAt).Unix(), session.LastSeenAt.Unix()); err != nil {
		return Session{}, "", fault.New(fault.CodeInternal, true, err)
	}
	if err := tx.Commit(); err != nil {
		return Session{}, "", fault.New(fault.CodeInternal, true, err)
	}
	return session, session.ID + "." + secret, nil
}

func (p *Personal) Authenticate(ctx context.Context, cookieValue string) (Session, error) {
	id, secret, ok := strings.Cut(cookieValue, ".")
	if !ok || secret == "" {
		return Session{}, fault.New(fault.CodeUnauthenticated, false, nil)
	}
	if _, err := domain.ParseID(domain.IDSession, id); err != nil {
		return Session{}, fault.New(fault.CodeUnauthenticated, false, nil)
	}
	var session Session
	var secretHash, csrfHash, status string
	var createdAt, expiresAt, idleExpiresAt, lastSeenAt, sessionSecurityVersion, principalSecurityVersion int64
	var revokedAt sql.NullInt64
	err := p.db.QueryRowContext(ctx, `
SELECT s.session_id, s.secret_hash, s.principal_id, s.csrf_hash, s.auth_method, s.client_label,
       s.principal_security_version, s.created_at, s.absolute_expires_at, s.idle_expires_at,
       s.last_seen_at, s.revoked_at, p.status, p.security_version
FROM sessions s JOIN security_principals p ON p.principal_id=s.principal_id
WHERE s.session_id = ?`, id).Scan(
		&session.ID, &secretHash, &session.PrincipalID, &csrfHash, &session.AuthMethod, &session.ClientLabel,
		&sessionSecurityVersion, &createdAt, &expiresAt, &idleExpiresAt, &lastSeenAt, &revokedAt,
		&status, &principalSecurityVersion,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, fault.New(fault.CodeUnauthenticated, false, nil)
	}
	if err != nil {
		return Session{}, fault.New(fault.CodeInternal, true, err)
	}
	csrf := csrfToken(secret)
	now := p.clock.Now().UTC()
	if subtle.ConstantTimeCompare([]byte(secretHash), []byte(hashToken(secret))) != 1 ||
		subtle.ConstantTimeCompare([]byte(csrfHash), []byte(hashToken(csrf))) != 1 || revokedAt.Valid ||
		status != "active" || sessionSecurityVersion != principalSecurityVersion ||
		now.Unix() >= expiresAt || now.Unix() >= idleExpiresAt {
		return Session{}, fault.New(fault.CodeUnauthenticated, false, nil)
	}
	session.CSRFToken = csrf
	session.SecurityVersion = sessionSecurityVersion
	session.CreatedAt = time.Unix(createdAt, 0).UTC()
	session.ExpiresAt = time.Unix(expiresAt, 0).UTC()
	session.LastSeenAt = time.Unix(lastSeenAt, 0).UTC()
	capabilities, err := principalCapabilities(ctx, p.db, session.PrincipalID)
	if err != nil {
		return Session{}, fault.New(fault.CodeInternal, true, err)
	}
	session.Capabilities = capabilities
	if revokedAt.Valid {
		value := time.Unix(revokedAt.Int64, 0).UTC()
		session.RevokedAt = &value
	}
	newIdle := minTime(now.Add(SessionIdleLifetime), session.ExpiresAt)
	_, _ = p.db.ExecContext(ctx, "UPDATE sessions SET last_seen_at = ?, idle_expires_at = ? WHERE session_id = ? AND revoked_at IS NULL", now.Unix(), newIdle.Unix(), session.ID)
	session.LastSeenAt = now
	return session, nil
}

func (p *Personal) ListSessions(ctx context.Context) ([]Session, error) {
	rows, err := p.db.QueryContext(ctx, `
SELECT session_id, principal_id, auth_method, client_label, principal_security_version,
       created_at, absolute_expires_at, last_seen_at, revoked_at
FROM sessions ORDER BY created_at, session_id`)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	var result []Session
	for rows.Next() {
		var session Session
		var createdAt, expiresAt, lastSeenAt int64
		var revokedAt sql.NullInt64
		if err := rows.Scan(&session.ID, &session.PrincipalID, &session.AuthMethod, &session.ClientLabel,
			&session.SecurityVersion, &createdAt, &expiresAt, &lastSeenAt, &revokedAt); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		session.CreatedAt = time.Unix(createdAt, 0).UTC()
		session.ExpiresAt = time.Unix(expiresAt, 0).UTC()
		session.LastSeenAt = time.Unix(lastSeenAt, 0).UTC()
		if revokedAt.Valid {
			value := time.Unix(revokedAt.Int64, 0).UTC()
			session.RevokedAt = &value
		}
		result = append(result, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	return result, nil
}

func (p *Personal) Revoke(ctx context.Context, actor, id string) error {
	if _, err := domain.ParseID(domain.IDSession, id); err != nil {
		return fault.New(fault.CodeNotFound, false, nil)
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx,
		"UPDATE sessions SET revoked_at = COALESCE(revoked_at, ?) WHERE session_id = ?",
		p.clock.Now().Unix(), id,
	)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return fault.New(fault.CodeNotFound, false, err)
	}
	if err := p.auditTx(ctx, tx, actor, "session.revoke", "session", id, "success", map[string]any{}, p.clock.Now().UTC()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return nil
}

func (p *Personal) IsActive(ctx context.Context, id string) bool {
	if _, err := domain.ParseID(domain.IDSession, id); err != nil {
		if _, tokenErr := domain.ParseID(domain.IDAPIToken, id); tokenErr != nil {
			return false
		}
		var expiresAt, revokedAt sql.NullInt64
		var tokenVersion, principalVersion int64
		var status string
		err := p.db.QueryRowContext(ctx, `SELECT t.expires_at, t.revoked_at, t.principal_security_version,
p.security_version, p.status FROM api_tokens t
JOIN security_principals p ON p.principal_id=t.principal_id WHERE t.token_id=?`, id).
			Scan(&expiresAt, &revokedAt, &tokenVersion, &principalVersion, &status)
		now := p.clock.Now().Unix()
		return err == nil && !revokedAt.Valid && (!expiresAt.Valid || now < expiresAt.Int64) &&
			status == "active" && tokenVersion == principalVersion
	}
	var expiresAt, idleExpiresAt, sessionSecurityVersion, principalSecurityVersion int64
	var revokedAt sql.NullInt64
	var status string
	err := p.db.QueryRowContext(ctx, `SELECT s.absolute_expires_at, s.idle_expires_at, s.revoked_at,
s.principal_security_version, p.security_version, p.status
FROM sessions s JOIN security_principals p ON p.principal_id=s.principal_id WHERE s.session_id=?`, id).
		Scan(&expiresAt, &idleExpiresAt, &revokedAt, &sessionSecurityVersion, &principalSecurityVersion, &status)
	now := p.clock.Now().Unix()
	return err == nil && !revokedAt.Valid && status == "active" && sessionSecurityVersion == principalSecurityVersion && now < expiresAt && now < idleExpiresAt
}

func HasCapability(session Session, capability string) bool {
	for _, available := range session.Capabilities {
		if available == capability {
			return true
		}
	}
	return false
}

func randomToken(reader io.Reader, size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := io.ReadFull(reader, buffer); err != nil {
		return "", fmt.Errorf("生成安全随机值: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func hashToken(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func csrfToken(sessionSecret string) string { return hashToken("gallery-csrf-v1\x00" + sessionSecret) }

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
