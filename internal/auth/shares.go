package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
)

type Share struct {
	ID                 string
	CreatedBy          string
	ScopeKind          string
	ScopeID            string
	Permissions        []string
	SecretPrefix       string
	FixedBlobAlgorithm string
	FixedBlobDigest    string
	CreatedAt          time.Time
	ExpiresAt          time.Time
	RevokedAt          *time.Time
}

type ShareCreated struct {
	Share  Share
	Secret string
}

func (p *Personal) CreateShare(ctx context.Context, actor Session, scopeKind, scopeID string, permissions []string, fixedAlgorithm, fixedDigest string, expiresAt time.Time) (ShareCreated, error) {
	if !p.IsActive(ctx, actor.ID) {
		return ShareCreated{}, fault.New(fault.CodeUnauthenticated, false, nil)
	}
	if err := p.requirePrincipalCapability(ctx, actor.PrincipalID, "shares.create"); err != nil {
		return ShareCreated{}, err
	}
	if scopeKind != "library" && scopeKind != "work" && scopeKind != "media" {
		return ShareCreated{}, fault.WithField(fault.CodeValidation, "scopeKind", nil)
	}
	var scopeIDErr error
	switch scopeKind {
	case "library":
		_, scopeIDErr = domain.ParseID(domain.IDLibrary, scopeID)
	case "work":
		_, scopeIDErr = domain.ParseID(domain.IDCanonicalWork, scopeID)
	case "media":
		_, scopeIDErr = domain.ParseID(domain.IDCanonicalMedia, scopeID)
	}
	if scopeIDErr != nil || !expiresAt.After(p.clock.Now().UTC()) {
		return ShareCreated{}, fault.WithField(fault.CodeValidation, "scopeId", nil)
	}
	permissions = normalizeCapabilities(permissions)
	if len(permissions) == 0 {
		return ShareCreated{}, fault.WithField(fault.CodeValidation, "permissions", nil)
	}
	for _, permission := range permissions {
		if permission != "view" && permission != "download" {
			return ShareCreated{}, fault.WithField(fault.CodeValidation, "permissions", nil)
		}
	}
	decodedDigest, digestErr := hex.DecodeString(fixedDigest)
	if (fixedAlgorithm == "") != (fixedDigest == "") ||
		(fixedAlgorithm != "" && (scopeKind != "media" || fixedAlgorithm != "sha256-v1" || len(decodedDigest) != 32 || digestErr != nil || fixedDigest != strings.ToLower(fixedDigest))) {
		return ShareCreated{}, fault.WithField(fault.CodeValidation, "fixedBlob", nil)
	}
	id, err := p.ids.New(domain.IDShare)
	if err != nil {
		return ShareCreated{}, fault.New(fault.CodeInternal, true, err)
	}
	secret, err := randomToken(p.random, APITokenSecretBytes)
	if err != nil {
		return ShareCreated{}, fault.New(fault.CodeInternal, true, err)
	}
	prefix := secret
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	permissionsJSON, _ := json.Marshal(permissions)
	now := p.clock.Now().UTC()
	var algorithm, digest any
	if fixedAlgorithm != "" {
		algorithm, digest = fixedAlgorithm, fixedDigest
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return ShareCreated{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO shares
(share_id, secret_hash, secret_prefix, created_by, scope_kind, scope_id, permissions_json,
 fixed_blob_algorithm, fixed_blob_digest, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id.String(), hashToken(secret), prefix, actor.PrincipalID,
		scopeKind, scopeID, string(permissionsJSON), algorithm, digest, now.Unix(), expiresAt.UTC().Unix())
	if err != nil {
		return ShareCreated{}, mapConstraint(err)
	}
	if err := p.auditTx(ctx, tx, actor.PrincipalID, "share.create", "share", id.String(), "success",
		map[string]any{"scopeKind": scopeKind, "scopeId": scopeID, "permissions": permissions, "fixed": fixedAlgorithm != "", "expiresAt": expiresAt.UTC()}, now); err != nil {
		return ShareCreated{}, err
	}
	if err := tx.Commit(); err != nil {
		return ShareCreated{}, fault.New(fault.CodeInternal, true, err)
	}
	share := Share{ID: id.String(), CreatedBy: actor.PrincipalID, ScopeKind: scopeKind, ScopeID: scopeID,
		Permissions: permissions, SecretPrefix: prefix, FixedBlobAlgorithm: fixedAlgorithm,
		FixedBlobDigest: fixedDigest, CreatedAt: now, ExpiresAt: expiresAt.UTC()}
	return ShareCreated{Share: share, Secret: id.String() + "." + secret}, nil
}

func (p *Personal) ResolveShare(ctx context.Context, credential string) (Share, error) {
	id, secret, ok := strings.Cut(credential, ".")
	if !ok || secret == "" {
		return Share{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if _, err := domain.ParseID(domain.IDShare, id); err != nil {
		return Share{}, fault.New(fault.CodeNotFound, false, nil)
	}
	share, secretHash, err := p.getShare(ctx, id)
	if err != nil {
		return Share{}, err
	}
	if subtle.ConstantTimeCompare([]byte(secretHash), []byte(hashToken(secret))) != 1 || share.RevokedAt != nil || !p.clock.Now().UTC().Before(share.ExpiresAt) {
		return Share{}, fault.New(fault.CodeNotFound, false, nil)
	}
	return share, nil
}

func (p *Personal) RevokeShare(ctx context.Context, actor, shareID string) error {
	if _, err := domain.ParseID(domain.IDShare, shareID); err != nil {
		return fault.New(fault.CodeNotFound, false, nil)
	}
	now := p.clock.Now().UTC()
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE shares SET revoked_at=COALESCE(revoked_at, ?) WHERE share_id=? AND created_by=?", now.Unix(), shareID, actor)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return fault.New(fault.CodeNotFound, false, nil)
	}
	if err := p.auditTx(ctx, tx, actor, "share.revoke", "share", shareID, "success", map[string]any{}, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (p *Personal) ListShares(ctx context.Context, createdBy string) ([]Share, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT share_id FROM shares
WHERE created_by=? ORDER BY created_at, share_id`, createdBy)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	result := make([]Share, 0, len(ids))
	for _, id := range ids {
		share, _, err := p.getShare(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, share)
	}
	return result, nil
}

func (p *Personal) getShare(ctx context.Context, id string) (Share, string, error) {
	var share Share
	var secretHash, permissionsJSON string
	var algorithm, digest sql.NullString
	var createdAt, expiresAt int64
	var revokedAt sql.NullInt64
	err := p.db.QueryRowContext(ctx, `SELECT share_id, secret_hash, secret_prefix, created_by, scope_kind,
scope_id, permissions_json, fixed_blob_algorithm, fixed_blob_digest, created_at, expires_at, revoked_at
FROM shares WHERE share_id=?`, id).Scan(&share.ID, &secretHash, &share.SecretPrefix, &share.CreatedBy,
		&share.ScopeKind, &share.ScopeID, &permissionsJSON, &algorithm, &digest, &createdAt, &expiresAt, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Share{}, "", fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return Share{}, "", fault.New(fault.CodeInternal, true, err)
	}
	if err := json.Unmarshal([]byte(permissionsJSON), &share.Permissions); err != nil {
		return Share{}, "", fault.New(fault.CodeInternal, false, err)
	}
	share.FixedBlobAlgorithm, share.FixedBlobDigest = algorithm.String, digest.String
	share.CreatedAt, share.ExpiresAt = time.Unix(createdAt, 0).UTC(), time.Unix(expiresAt, 0).UTC()
	share.RevokedAt = nullableTime(revokedAt)
	return share, secretHash, nil
}

type SecurityAudit struct {
	ID         string         `json:"id"`
	Action     string         `json:"action"`
	ActorID    string         `json:"actorId"`
	TargetKind string         `json:"targetKind"`
	TargetID   string         `json:"targetId"`
	Outcome    string         `json:"outcome"`
	Detail     map[string]any `json:"detail"`
	CreatedAt  time.Time      `json:"createdAt"`
}

func (p *Personal) ListSecurityAudits(ctx context.Context, limit int) ([]SecurityAudit, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := p.db.QueryContext(ctx, `SELECT audit_id, action, COALESCE(actor_principal_id,''), target_kind,
target_id, outcome, detail_json, created_at FROM security_audits
ORDER BY created_at DESC, audit_id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	var result []SecurityAudit
	for rows.Next() {
		var item SecurityAudit
		var detailJSON string
		var createdAt int64
		if err := rows.Scan(&item.ID, &item.Action, &item.ActorID, &item.TargetKind, &item.TargetID,
			&item.Outcome, &detailJSON, &createdAt); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		if err := json.Unmarshal([]byte(detailJSON), &item.Detail); err != nil {
			return nil, fault.New(fault.CodeInternal, false, err)
		}
		item.CreatedAt = time.Unix(createdAt, 0).UTC()
		result = append(result, item)
	}
	return result, rows.Err()
}
