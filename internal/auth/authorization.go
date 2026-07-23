package auth

import (
	"context"
	"database/sql"
	"errors"
	"sort"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
)

func (p *Personal) requirePrincipalCapability(ctx context.Context, principalID, capability string) error {
	var status string
	if err := p.db.QueryRowContext(ctx, "SELECT status FROM security_principals WHERE principal_id=?", principalID).Scan(&status); err != nil || status != "active" {
		return fault.New(fault.CodeForbidden, false, nil)
	}
	allowed, err := p.Authorize(ctx, principalID, capability, ResourceScope{Kind: "global"})
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if !allowed {
		return fault.New(fault.CodeForbidden, false, nil)
	}
	return nil
}

type ResourceScope struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
}

type Grant struct {
	ID          string
	PrincipalID string
	Effect      string
	Capability  string
	Scope       ResourceScope
	CreatedBy   string
	Revoked     bool
}

func validResourceScope(scope ResourceScope) bool {
	switch scope.Kind {
	case "global":
		return scope.ID == ""
	case "library":
		_, err := domain.ParseID(domain.IDLibrary, scope.ID)
		return err == nil
	case "source":
		_, err := domain.ParseID(domain.IDSource, scope.ID)
		return err == nil
	default:
		return false
	}
}

func principalCapabilities(ctx context.Context, db *sql.DB, principalID string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT rc.capability
FROM principal_roles pr
JOIN security_role_capabilities rc ON rc.role_id=pr.role_id
WHERE pr.principal_id=? ORDER BY rc.capability`, principalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var capability string
		if err := rows.Scan(&capability); err != nil {
			return nil, err
		}
		result = append(result, capability)
	}
	return result, rows.Err()
}

func (p *Personal) Authorize(ctx context.Context, principalID, capability string, scope ResourceScope) (bool, error) {
	capabilities, err := principalCapabilities(ctx, p.db, principalID)
	if err != nil {
		return false, err
	}
	available := false
	for _, value := range capabilities {
		if value == capability {
			available = true
			break
		}
	}
	if !available {
		return false, nil
	}

	var owner int
	if err := p.db.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM principal_roles WHERE principal_id=? AND role_id='owner')`, principalID).Scan(&owner); err != nil {
		return false, err
	}
	rows, err := p.db.QueryContext(ctx, `SELECT effect, scope_kind, scope_id
FROM authorization_grants
WHERE principal_id=? AND capability=? AND revoked_at IS NULL`, principalID, capability)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	allowed := owner != 0
	for rows.Next() {
		var effect, kind, id string
		if err := rows.Scan(&effect, &kind, &id); err != nil {
			return false, err
		}
		matches, err := p.scopeMatches(ctx, ResourceScope{Kind: kind, ID: id}, scope)
		if err != nil {
			return false, err
		}
		if !matches {
			continue
		}
		if effect == "deny" {
			return false, nil
		}
		allowed = true
	}
	return allowed, rows.Err()
}

func (p *Personal) AuthorizeSession(ctx context.Context, session Session, capability string, scope ResourceScope) (bool, error) {
	if !HasCapability(session, capability) {
		return false, nil
	}
	allowed, err := p.Authorize(ctx, session.PrincipalID, capability, scope)
	if err != nil || !allowed || session.TokenID == "" {
		return allowed, err
	}
	for _, tokenScope := range session.TokenScopes {
		matches, matchErr := p.scopeMatches(ctx, tokenScope, scope)
		if matchErr != nil {
			return false, matchErr
		}
		if matches {
			return true, nil
		}
	}
	return false, nil
}

func (p *Personal) scopeMatches(ctx context.Context, grant, requested ResourceScope) (bool, error) {
	if grant.Kind == "global" {
		return true, nil
	}
	if requested.Kind == "" || requested.Kind == "global" {
		return false, nil
	}
	if grant.Kind == requested.Kind {
		return grant.ID == requested.ID, nil
	}
	if grant.Kind == "library" && requested.Kind == "source" {
		var libraryID string
		err := p.db.QueryRowContext(ctx, "SELECT library_id FROM sources WHERE source_id=?", requested.ID).Scan(&libraryID)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return libraryID == grant.ID, err
	}
	return false, nil
}

func normalizeCapabilities(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
