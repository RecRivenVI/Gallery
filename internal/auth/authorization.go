package auth

import (
	"context"
	"database/sql"
	"encoding/json"
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

// EffectiveCapabilities 返回 Session 在给定作用域上实际可用的 capability。Session 中的
// Capabilities 只是角色预设（以及 API Token 创建时快照）的上限；显式 Grant、deny 优先级
// 和 Token scope 仍必须逐项经过与公开服务方法相同的 AuthorizeSession 判定。
func (p *Personal) EffectiveCapabilities(ctx context.Context, session Session, scope ResourceScope) ([]string, error) {
	effective := make([]string, 0, len(session.Capabilities))
	for _, capability := range normalizeCapabilities(session.Capabilities) {
		allowed, err := p.AuthorizeSession(ctx, session, capability, scope)
		if err != nil {
			return nil, err
		}
		if allowed {
			effective = append(effective, capability)
		}
	}
	return effective, nil
}

// AuthorizeSessionSources 对候选 Source 批量执行 all-of capability 授权，返回按 Source ID
// 排序且去重后的允许集合。它与逐个调用 AuthorizeSession 保持相同语义，但把当前角色、
// active Grant 和 Source->Library 映射固定在同一个短读事务快照内，避免列表查询按 Source
// 产生控制库 N+1 查询和跨 Source 的混合授权快照。
func (p *Personal) AuthorizeSessionSources(ctx context.Context, session Session, requiredCapabilities, candidateSourceIDs []string) ([]string, error) {
	capabilities := normalizeCapabilities(requiredCapabilities)
	sourceIDs := normalizeCapabilities(candidateSourceIDs)
	allowedSourceIDs := make([]string, 0, len(sourceIDs))
	if len(capabilities) == 0 || len(sourceIDs) == 0 {
		return allowedSourceIDs, nil
	}

	capabilitiesJSON, err := json.Marshal(capabilities)
	if err != nil {
		return nil, err
	}
	sourceIDsJSON, err := json.Marshal(sourceIDs)
	if err != nil {
		return nil, err
	}

	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// 保持固定三次控制库读取：角色能力、相关 active Grant、候选 Source 的 Library 映射。
	// LEFT JOIN 让 owner 标记不依赖 owner 角色当前是否仍有 capability 行；标量 Authorize
	// 同样先独立计算 role capability 上限，再独立判断 owner baseline。
	roleCapabilities := make(map[string]struct{})
	owner := false
	rows, err := tx.QueryContext(ctx, `SELECT pr.role_id, COALESCE(rc.capability, '')
FROM principal_roles pr
LEFT JOIN security_role_capabilities rc ON rc.role_id=pr.role_id
WHERE pr.principal_id=?
ORDER BY pr.role_id, rc.capability`, session.PrincipalID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var roleID, capability string
		if err := rows.Scan(&roleID, &capability); err != nil {
			rows.Close()
			return nil, err
		}
		if roleID == "owner" {
			owner = true
		}
		if capability != "" {
			roleCapabilities[capability] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	grants := make(map[string]*sourceCapabilityGrants, len(capabilities))
	rows, err = tx.QueryContext(ctx, `SELECT effect, capability, scope_kind, scope_id
FROM authorization_grants
WHERE principal_id=? AND revoked_at IS NULL
  AND capability IN (SELECT CAST(value AS TEXT) FROM json_each(?))
ORDER BY capability, scope_kind, scope_id, effect`, session.PrincipalID, string(capabilitiesJSON))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var effect, capability, kind, id string
		if err := rows.Scan(&effect, &capability, &kind, &id); err != nil {
			rows.Close()
			return nil, err
		}
		decision := grants[capability]
		if decision == nil {
			decision = newSourceCapabilityGrants()
			grants[capability] = decision
		}
		if effect == "deny" {
			decision.deny.add(ResourceScope{Kind: kind, ID: id})
		} else {
			decision.allow.add(ResourceScope{Kind: kind, ID: id})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	sourceLibraries := make(map[string]string, len(sourceIDs))
	rows, err = tx.QueryContext(ctx, `SELECT CAST(requested.value AS TEXT), COALESCE(s.library_id, '')
FROM json_each(?) requested
LEFT JOIN sources s ON s.source_id=CAST(requested.value AS TEXT)
ORDER BY CAST(requested.key AS INTEGER)`, string(sourceIDsJSON))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var sourceID, libraryID string
		if err := rows.Scan(&sourceID, &libraryID); err != nil {
			rows.Close()
			return nil, err
		}
		sourceLibraries[sourceID] = libraryID
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	sessionCapabilities := make(map[string]struct{}, len(session.Capabilities))
	for _, capability := range session.Capabilities {
		sessionCapabilities[capability] = struct{}{}
	}
	tokenScopes := newSourceScopeSet()
	for _, scope := range session.TokenScopes {
		tokenScopes.add(scope)
	}

	for _, sourceID := range sourceIDs {
		libraryID := sourceLibraries[sourceID]
		allowed := true
		for _, capability := range capabilities {
			if _, ok := sessionCapabilities[capability]; !ok {
				allowed = false
				break
			}
			if _, ok := roleCapabilities[capability]; !ok {
				allowed = false
				break
			}
			decision := grants[capability]
			if decision != nil && decision.deny.matches(sourceID, libraryID) {
				allowed = false
				break
			}
			if !owner && (decision == nil || !decision.allow.matches(sourceID, libraryID)) {
				allowed = false
				break
			}
			if session.TokenID != "" && !tokenScopes.matches(sourceID, libraryID) {
				allowed = false
				break
			}
		}
		if allowed {
			allowedSourceIDs = append(allowedSourceIDs, sourceID)
		}
	}
	return allowedSourceIDs, nil
}

type sourceCapabilityGrants struct {
	allow sourceScopeSet
	deny  sourceScopeSet
}

func newSourceCapabilityGrants() *sourceCapabilityGrants {
	return &sourceCapabilityGrants{allow: newSourceScopeSet(), deny: newSourceScopeSet()}
}

type sourceScopeSet struct {
	global    bool
	libraries map[string]struct{}
	sources   map[string]struct{}
}

func newSourceScopeSet() sourceScopeSet {
	return sourceScopeSet{libraries: make(map[string]struct{}), sources: make(map[string]struct{})}
}

func (s *sourceScopeSet) add(scope ResourceScope) {
	switch scope.Kind {
	case "global":
		s.global = true
	case "library":
		s.libraries[scope.ID] = struct{}{}
	case "source":
		s.sources[scope.ID] = struct{}{}
	}
}

func (s sourceScopeSet) matches(sourceID, libraryID string) bool {
	if s.global {
		return true
	}
	if _, ok := s.sources[sourceID]; ok {
		return true
	}
	if libraryID == "" {
		return false
	}
	_, ok := s.libraries[libraryID]
	return ok
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
