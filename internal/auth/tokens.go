package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
)

const APITokenSecretBytes = 32

type APIToken struct {
	ID           string
	PrincipalID  string
	Name         string
	SecretPrefix string
	Capabilities []string
	Scopes       []ResourceScope
	CreatedAt    time.Time
	ExpiresAt    *time.Time
	LastUsedAt   *time.Time
	RevokedAt    *time.Time
}

type APITokenCreated struct {
	Token  APIToken
	Secret string
}

func (p *Personal) CreateAPIToken(ctx context.Context, actor Session, name string, capabilities []string, scopes []ResourceScope, expiresAt *time.Time) (APITokenCreated, error) {
	if !p.IsActive(ctx, actor.ID) {
		return APITokenCreated{}, fault.New(fault.CodeUnauthenticated, false, nil)
	}
	if err := p.requirePrincipalCapability(ctx, actor.PrincipalID, "tokens.manage"); err != nil {
		return APITokenCreated{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 128 {
		return APITokenCreated{}, fault.WithField(fault.CodeValidation, "name", nil)
	}
	capabilities = normalizeCapabilities(capabilities)
	if len(capabilities) == 0 {
		return APITokenCreated{}, fault.WithField(fault.CodeValidation, "capabilities", nil)
	}
	if len(scopes) == 0 {
		scopes = []ResourceScope{{Kind: "global"}}
	}
	for _, scope := range scopes {
		if !validResourceScope(scope) {
			return APITokenCreated{}, fault.WithField(fault.CodeValidation, "scopes", nil)
		}
		for _, capability := range capabilities {
			allowed, err := p.AuthorizeSession(ctx, actor, capability, scope)
			if err != nil {
				return APITokenCreated{}, fault.New(fault.CodeInternal, true, err)
			}
			if !allowed {
				return APITokenCreated{}, fault.New(fault.CodeForbidden, false, nil)
			}
		}
	}
	now := p.clock.Now().UTC()
	if expiresAt != nil && !expiresAt.After(now) {
		return APITokenCreated{}, fault.WithField(fault.CodeValidation, "expiresAt", nil)
	}
	id, err := p.ids.New(domain.IDAPIToken)
	if err != nil {
		return APITokenCreated{}, fault.New(fault.CodeInternal, true, err)
	}
	secret, err := randomToken(p.random, APITokenSecretBytes)
	if err != nil {
		return APITokenCreated{}, fault.New(fault.CodeInternal, true, err)
	}
	capJSON, _ := json.Marshal(capabilities)
	scopeJSON, _ := json.Marshal(scopes)
	prefix := secret
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	var expires any
	if expiresAt != nil {
		expires = expiresAt.UTC().Unix()
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return APITokenCreated{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO api_tokens
(token_id, principal_id, secret_hash, secret_prefix, name, capabilities_json, scopes_json,
 principal_security_version, created_by, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id.String(), actor.PrincipalID, hashToken(secret), prefix, name,
		string(capJSON), string(scopeJSON), actor.SecurityVersion, actor.PrincipalID, now.Unix(), expires)
	if err != nil {
		return APITokenCreated{}, mapConstraint(err)
	}
	if err := p.auditTx(ctx, tx, actor.PrincipalID, "token.create", "api_token", id.String(), "success",
		map[string]any{"capabilities": capabilities, "scopes": scopes, "expiresAt": expiresAt}, now); err != nil {
		return APITokenCreated{}, err
	}
	if err := tx.Commit(); err != nil {
		return APITokenCreated{}, fault.New(fault.CodeInternal, true, err)
	}
	token := APIToken{ID: id.String(), PrincipalID: actor.PrincipalID, Name: name, SecretPrefix: prefix,
		Capabilities: capabilities, Scopes: scopes, CreatedAt: now, ExpiresAt: expiresAt}
	return APITokenCreated{Token: token, Secret: id.String() + "." + secret}, nil
}

func (p *Personal) AuthenticateAPIToken(ctx context.Context, value string) (Session, error) {
	id, secret, ok := strings.Cut(value, ".")
	if !ok || secret == "" {
		return Session{}, fault.New(fault.CodeTokenInvalid, false, nil)
	}
	if _, err := domain.ParseID(domain.IDAPIToken, id); err != nil {
		return Session{}, fault.New(fault.CodeTokenInvalid, false, nil)
	}
	var principalID, secretHash, capabilitiesJSON, scopesJSON, status string
	var createdAt, tokenSecurityVersion, principalSecurityVersion int64
	var expiresAt, revokedAt sql.NullInt64
	err := p.db.QueryRowContext(ctx, `SELECT t.principal_id, t.secret_hash, t.capabilities_json, t.scopes_json,
t.created_at, t.expires_at, t.revoked_at, t.principal_security_version, p.security_version, p.status
FROM api_tokens t JOIN security_principals p ON p.principal_id=t.principal_id WHERE t.token_id=?`, id).
		Scan(&principalID, &secretHash, &capabilitiesJSON, &scopesJSON, &createdAt, &expiresAt, &revokedAt,
			&tokenSecurityVersion, &principalSecurityVersion, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, fault.New(fault.CodeTokenInvalid, false, nil)
	}
	if err != nil {
		return Session{}, fault.New(fault.CodeInternal, true, err)
	}
	now := p.clock.Now().UTC()
	if subtle.ConstantTimeCompare([]byte(secretHash), []byte(hashToken(secret))) != 1 || revokedAt.Valid ||
		status != "active" || tokenSecurityVersion != principalSecurityVersion {
		return Session{}, fault.New(fault.CodeTokenInvalid, false, nil)
	}
	if expiresAt.Valid && now.Unix() >= expiresAt.Int64 {
		return Session{}, fault.New(fault.CodeTokenExpired, false, nil)
	}
	var requested []string
	var scopes []ResourceScope
	if json.Unmarshal([]byte(capabilitiesJSON), &requested) != nil || json.Unmarshal([]byte(scopesJSON), &scopes) != nil {
		return Session{}, fault.New(fault.CodeInternal, false, nil)
	}
	available, err := principalCapabilities(ctx, p.db, principalID)
	if err != nil {
		return Session{}, fault.New(fault.CodeInternal, true, err)
	}
	availableSet := make(map[string]struct{}, len(available))
	for _, capability := range available {
		availableSet[capability] = struct{}{}
	}
	var effective []string
	for _, capability := range requested {
		if _, ok := availableSet[capability]; ok {
			effective = append(effective, capability)
		}
	}
	_, _ = p.db.ExecContext(ctx, "UPDATE api_tokens SET last_used_at=? WHERE token_id=? AND revoked_at IS NULL", now.Unix(), id)
	return Session{ID: id, TokenID: id, PrincipalID: principalID, CreatedAt: time.Unix(createdAt, 0).UTC(),
		LastSeenAt: now, Capabilities: normalizeCapabilities(effective), AuthMethod: "api_token",
		SecurityVersion: tokenSecurityVersion, TokenScopes: scopes}, nil
}

func (p *Personal) ListAPITokens(ctx context.Context, principalID string) ([]APIToken, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT token_id, principal_id, name, secret_prefix, capabilities_json,
scopes_json, created_at, expires_at, last_used_at, revoked_at
FROM api_tokens WHERE principal_id=? ORDER BY created_at, token_id`, principalID)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	var result []APIToken
	for rows.Next() {
		var token APIToken
		var capabilitiesJSON, scopesJSON string
		var createdAt int64
		var expiresAt, lastUsedAt, revokedAt sql.NullInt64
		if err := rows.Scan(&token.ID, &token.PrincipalID, &token.Name, &token.SecretPrefix, &capabilitiesJSON,
			&scopesJSON, &createdAt, &expiresAt, &lastUsedAt, &revokedAt); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		if json.Unmarshal([]byte(capabilitiesJSON), &token.Capabilities) != nil || json.Unmarshal([]byte(scopesJSON), &token.Scopes) != nil {
			return nil, fault.New(fault.CodeInternal, false, nil)
		}
		token.CreatedAt = time.Unix(createdAt, 0).UTC()
		token.ExpiresAt = nullableTime(expiresAt)
		token.LastUsedAt = nullableTime(lastUsedAt)
		token.RevokedAt = nullableTime(revokedAt)
		result = append(result, token)
	}
	return result, rows.Err()
}

func (p *Personal) RevokeAPIToken(ctx context.Context, actor, tokenID string) error {
	if _, err := domain.ParseID(domain.IDAPIToken, tokenID); err != nil {
		return fault.New(fault.CodeNotFound, false, nil)
	}
	now := p.clock.Now().UTC()
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE api_tokens SET revoked_at=COALESCE(revoked_at, ?) WHERE token_id=? AND principal_id=?", now.Unix(), tokenID, actor)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return fault.New(fault.CodeNotFound, false, nil)
	}
	if err := p.auditTx(ctx, tx, actor, "token.revoke", "api_token", tokenID, "success", map[string]any{}, now); err != nil {
		return err
	}
	return tx.Commit()
}

func nullableTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	result := time.Unix(value.Int64, 0).UTC()
	return &result
}
