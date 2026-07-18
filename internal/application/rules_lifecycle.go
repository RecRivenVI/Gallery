package application

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/rules"
)

const (
	RulePackageActive       = "active"
	RulePackageDeprecated   = "deprecated"
	RulePackageDeleted      = "deleted"
	RuleDraftState          = "draft"
	RuleDraftValidated      = "validated"
	RuleDraftInvalid        = "invalid"
	RuleVersionPublished    = "published"
	RuleVersionDeprecated   = "deprecated"
	RuleParameterActive     = "active"
	RuleParameterDeprecated = "deprecated"
	RuleBindingActive       = "active"
	RuleBindingPaused       = "paused"
	RuleBindingInvalid      = "invalid"
)

type RulePackage struct {
	ID                      string
	RuleSetID               string
	Name                    string
	Description             string
	Status                  string
	CurrentSemanticHash     string
	LatestValidSemanticHash string
	DraftID                 string
	ExtensionRequirements   []byte
	CreatedBy               string
	CreatedAt               time.Time
	UpdatedAt               time.Time
	Revision                int
}

type RuleDraft struct {
	ID               string
	PackageID        string
	BaseSemanticHash string
	Content          []byte
	SourceFormat     string
	ValidationStatus string
	Diagnostics      []byte
	Revision         int
	SavedBy          string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type RuleDraftValidation struct {
	Draft       RuleDraft
	Valid       bool
	Diagnostics []rules.ImportDiagnostic
	Validation  *rules.ValidationResult
}

type RuleParameterSet struct {
	ID              string
	Name            string
	SemanticHash    string
	CurrentRevision int
	CurrentHash     string
	Status          string
	Parameters      []byte
	CreatedBy       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type RuleAudit struct {
	ID               string
	PackageID        string
	Action           string
	FromSemanticHash string
	ToSemanticHash   string
	Reason           string
	ActorID          string
	CreatedAt        time.Time
}

type RuleVersionListOptions struct {
	PackageID string
	Status    string
}

func (r *Resources) CreateRulePackage(ctx context.Context, ruleSetID, name, description, actor string) (RulePackage, error) {
	if ruleSetID == "" {
		id, err := r.ids.New(domain.IDRuleSet)
		if err != nil {
			return RulePackage{}, fault.New(fault.CodeInternal, true, err)
		}
		ruleSetID = id.String()
	}
	if _, err := domain.ParseID(domain.IDRuleSet, ruleSetID); err != nil {
		return RulePackage{}, fault.WithField(fault.CodeValidation, "ruleSetId", err)
	}
	name = strings.TrimSpace(name)
	actor = strings.TrimSpace(actor)
	if name == "" || len([]rune(name)) > 256 || actor == "" || len([]rune(actor)) > 256 {
		return RulePackage{}, fault.WithField(fault.CodeValidation, "name", nil)
	}
	if len([]rune(description)) > 4096 {
		return RulePackage{}, fault.WithField(fault.CodeValidation, "description", nil)
	}
	id, err := r.ids.New(domain.IDRulePackage)
	if err != nil {
		return RulePackage{}, fault.New(fault.CodeInternal, true, err)
	}
	now := r.clock.Now().UTC()
	_, err = r.control.ExecContext(ctx, `
INSERT INTO rule_packages
(package_id, rule_set_id, name, description, status, extension_requirements_json,
 created_by, created_at, updated_at, revision)
VALUES (?, ?, ?, ?, ?, '{}', ?, ?, ?, 1)`, id.String(), ruleSetID, name, description,
		RulePackageActive, actor, now.Unix(), now.Unix())
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return RulePackage{}, fault.New(fault.CodeConflict, false, err)
		}
		return RulePackage{}, fault.New(fault.CodeInternal, true, err)
	}
	return r.GetRulePackage(ctx, id.String())
}

func (r *Resources) ListRulePackages(ctx context.Context) ([]RulePackage, error) {
	rows, err := r.control.QueryContext(ctx, `
SELECT package_id, rule_set_id, name, description, status, current_semantic_hash,
       latest_valid_semantic_hash, draft_id, extension_requirements_json, created_by,
       created_at, updated_at, revision
FROM rule_packages ORDER BY name, package_id`)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	result := []RulePackage{}
	for rows.Next() {
		item, err := scanRulePackage(rows)
		if err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	return result, nil
}

func (r *Resources) GetRulePackage(ctx context.Context, packageID string) (RulePackage, error) {
	if _, err := domain.ParseID(domain.IDRulePackage, packageID); err != nil {
		return RulePackage{}, fault.New(fault.CodeNotFound, false, nil)
	}
	row := r.control.QueryRowContext(ctx, `
SELECT package_id, rule_set_id, name, description, status, current_semantic_hash,
       latest_valid_semantic_hash, draft_id, extension_requirements_json, created_by,
       created_at, updated_at, revision
FROM rule_packages WHERE package_id = ?`, packageID)
	result, err := scanRulePackage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return RulePackage{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return RulePackage{}, fault.New(fault.CodeInternal, true, err)
	}
	return result, nil
}

func (r *Resources) SetRulePackageStatus(ctx context.Context, packageID, status, actor, reason string, expectedRevision int) (RulePackage, error) {
	if status != RulePackageActive && status != RulePackageDeprecated && status != RulePackageDeleted {
		return RulePackage{}, fault.WithField(fault.CodeValidation, "status", nil)
	}
	now := r.clock.Now().UTC()
	tx, err := r.control.BeginTx(ctx, nil)
	if err != nil {
		return RulePackage{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	var item RulePackage
	var current, latest, draft sql.NullString
	var extensionRequirements string
	var createdAt, updatedAt int64
	if err := tx.QueryRowContext(ctx, `SELECT package_id, rule_set_id, name, description, status, current_semantic_hash,
latest_valid_semantic_hash, draft_id, extension_requirements_json, created_by, created_at, updated_at, revision
FROM rule_packages WHERE package_id=?`, packageID).Scan(&item.ID, &item.RuleSetID, &item.Name, &item.Description, &item.Status, &current, &latest, &draft,
		&extensionRequirements, &item.CreatedBy, &createdAt, &updatedAt, &item.Revision); errors.Is(err, sql.ErrNoRows) {
		return RulePackage{}, fault.New(fault.CodeNotFound, false, nil)
	} else if err != nil {
		return RulePackage{}, fault.New(fault.CodeInternal, true, err)
	}
	item.CurrentSemanticHash, item.LatestValidSemanticHash, item.DraftID = current.String, latest.String, draft.String
	item.ExtensionRequirements = []byte(extensionRequirements)
	item.CreatedAt, item.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
	if status == RulePackageDeleted && item.CurrentSemanticHash != "" {
		return RulePackage{}, fault.New(fault.CodeRuleVersionInUse, false, nil)
	}
	if expectedRevision >= 0 && item.Revision != expectedRevision {
		return RulePackage{}, rulePackageConflict(expectedRevision, item.Revision)
	}
	result, err := tx.ExecContext(ctx, `UPDATE rule_packages SET status=?, revision=revision+1, updated_at=? WHERE package_id=? AND revision=?`, status, now.Unix(), packageID, item.Revision)
	if err != nil {
		return RulePackage{}, fault.New(fault.CodeInternal, true, err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return RulePackage{}, rulePackageConflict(item.Revision, item.Revision+1)
	}
	if err := r.appendRuleAuditTx(ctx, tx, packageID, "status_"+status, item.CurrentSemanticHash, item.CurrentSemanticHash, reason, actorOrSystem(actor), now); err != nil {
		return RulePackage{}, err
	}
	if err := tx.Commit(); err != nil {
		return RulePackage{}, fault.New(fault.CodeInternal, true, err)
	}
	return r.GetRulePackage(ctx, packageID)
}

func (r *Resources) ListRuleAudits(ctx context.Context, packageID string) ([]RuleAudit, error) {
	if _, err := r.GetRulePackage(ctx, packageID); err != nil {
		return nil, err
	}
	rows, err := r.control.QueryContext(ctx, `SELECT audit_id, package_id, action, from_semantic_hash, to_semantic_hash, reason, actor_id, created_at FROM rule_audits WHERE package_id=? ORDER BY created_at, audit_id`, packageID)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	result := []RuleAudit{}
	for rows.Next() {
		var item RuleAudit
		var fromHash, toHash sql.NullString
		var createdAt int64
		if err := rows.Scan(&item.ID, &item.PackageID, &item.Action, &fromHash, &toHash, &item.Reason, &item.ActorID, &createdAt); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		item.FromSemanticHash, item.ToSemanticHash = fromHash.String, toHash.String
		item.CreatedAt = time.Unix(createdAt, 0).UTC()
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	return result, nil
}

func (r *Resources) SaveRuleDraft(ctx context.Context, packageID string, content []byte, format, baseSemanticHash string, expectedRevision int, actor string) (RuleDraft, error) {
	if _, err := domain.ParseID(domain.IDRulePackage, packageID); err != nil {
		return RuleDraft{}, fault.New(fault.CodeNotFound, false, nil)
	}
	actor = strings.TrimSpace(actor)
	if actor == "" || len(content) == 0 || len(content) > rules.MaxRulePackageBytes {
		return RuleDraft{}, fault.WithField(fault.CodeValidation, "content", nil)
	}
	format = normalizeRuleFormat(format)
	if format == "" {
		return RuleDraft{}, fault.WithField(fault.CodeValidation, "format", nil)
	}
	canonical, status, diagnostics := draftValidation(content, format)
	if len(canonical) == 0 {
		canonical = append([]byte(nil), content...)
	}
	diagnosticsJSON, _ := json.Marshal(diagnostics)
	now := r.clock.Now().UTC()
	tx, err := r.control.BeginTx(ctx, nil)
	if err != nil {
		return RuleDraft{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	if err := requireRulePackageTx(ctx, tx, packageID); err != nil {
		return RuleDraft{}, err
	}
	if baseSemanticHash != "" {
		var exists int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM rule_versions WHERE semantic_hash=? AND package_id=?`, baseSemanticHash, packageID).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return RuleDraft{}, fault.WithField(fault.CodeValidation, "baseSemanticHash", fmt.Errorf("版本不属于规则包"))
		}
		if err != nil {
			return RuleDraft{}, fault.New(fault.CodeInternal, true, err)
		}
	}
	var existingID string
	var currentRevision int
	err = tx.QueryRowContext(ctx, `SELECT draft_id, revision FROM rule_drafts WHERE package_id = ?`, packageID).Scan(&existingID, &currentRevision)
	if errors.Is(err, sql.ErrNoRows) {
		if expectedRevision > 0 {
			return RuleDraft{}, ruleDraftConflict(expectedRevision, 0)
		}
		id, idErr := r.ids.New(domain.IDRuleDraft)
		if idErr != nil {
			return RuleDraft{}, fault.New(fault.CodeInternal, true, idErr)
		}
		existingID = id.String()
		_, err = tx.ExecContext(ctx, `
INSERT INTO rule_drafts
(draft_id, package_id, base_semantic_hash, content_json, source_format, validation_status,
 diagnostics_json, revision, saved_by, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?)`, existingID, packageID, nullableText(baseSemanticHash), string(canonical),
			format, status, string(diagnosticsJSON), actor, now.Unix(), now.Unix())
		if err != nil {
			return RuleDraft{}, fault.New(fault.CodeConflict, false, err)
		}
		_, err = tx.ExecContext(ctx, `UPDATE rule_packages SET draft_id=?, revision=revision+1, updated_at=? WHERE package_id=?`, existingID, now.Unix(), packageID)
	} else if err != nil {
		return RuleDraft{}, fault.New(fault.CodeInternal, true, err)
	} else {
		if expectedRevision >= 0 && expectedRevision != currentRevision {
			return RuleDraft{}, ruleDraftConflict(expectedRevision, currentRevision)
		}
		newRevision := currentRevision + 1
		_, err = tx.ExecContext(ctx, `
UPDATE rule_drafts SET base_semantic_hash=?, content_json=?, source_format=?, validation_status=?,
 diagnostics_json=?, revision=?, saved_by=?, updated_at=? WHERE package_id=? AND revision=?`,
			nullableText(baseSemanticHash), string(canonical), format, status, string(diagnosticsJSON), newRevision,
			actor, now.Unix(), packageID, currentRevision)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE rule_packages SET revision=revision+1, updated_at=? WHERE package_id=?`, now.Unix(), packageID)
		}
	}
	if err != nil {
		return RuleDraft{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := tx.Commit(); err != nil {
		return RuleDraft{}, fault.New(fault.CodeInternal, true, err)
	}
	return r.GetRuleDraft(ctx, packageID)
}

func (r *Resources) GetRuleDraft(ctx context.Context, packageID string) (RuleDraft, error) {
	if _, err := domain.ParseID(domain.IDRulePackage, packageID); err != nil {
		return RuleDraft{}, fault.New(fault.CodeNotFound, false, nil)
	}
	row := r.control.QueryRowContext(ctx, `
SELECT draft_id, package_id, base_semantic_hash, content_json, source_format, validation_status,
       diagnostics_json, revision, saved_by, created_at, updated_at
FROM rule_drafts WHERE package_id = ?`, packageID)
	result, err := scanRuleDraft(row)
	if errors.Is(err, sql.ErrNoRows) {
		return RuleDraft{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return RuleDraft{}, fault.New(fault.CodeInternal, true, err)
	}
	return result, nil
}

func (r *Resources) ValidateRuleDraft(ctx context.Context, packageID string, expectedRevision int, actor string) (RuleDraftValidation, error) {
	draft, err := r.GetRuleDraft(ctx, packageID)
	if err != nil {
		return RuleDraftValidation{}, err
	}
	canonical, status, diagnostics := draftValidation(draft.Content, draft.SourceFormat)
	var validation *rules.ValidationResult
	if status == RuleDraftValidated {
		value := rules.ValidationResult{}
		value.CanonicalJSON = canonical
		compiled, compileErr := rules.CompilePackage(canonical)
		if compileErr != nil {
			status = RuleDraftInvalid
			diagnostics = append(diagnostics, rules.ImportDiagnostic{Path: rules.ErrorField(compileErr), Message: compileErr.Error()})
		} else {
			value.PackageHash, value.SemanticHash = compiled.PackageHash, compiled.SemanticHash
			validation = &value
		}
	}
	diagnosticsJSON, _ := json.Marshal(diagnostics)
	now := r.clock.Now().UTC()
	if strings.TrimSpace(actor) == "" {
		actor = "system"
	}
	content := canonical
	if len(content) == 0 {
		// 失败校验也必须保留用户正在编辑的原始草稿，不能因重新验证失败而
		// 把可恢复的输入替换成空内容。
		content = append([]byte(nil), draft.Content...)
	}
	query := `UPDATE rule_drafts SET content_json=?, validation_status=?, diagnostics_json=?, revision=revision+1, saved_by=?, updated_at=? WHERE package_id=?`
	args := []any{string(content), status, string(diagnosticsJSON), actor, now.Unix(), packageID}
	if expectedRevision >= 0 {
		query += " AND revision=?"
		args = append(args, expectedRevision)
	}
	result, err := r.control.ExecContext(ctx, query, args...)
	if err != nil {
		return RuleDraftValidation{}, fault.New(fault.CodeInternal, true, err)
	}
	if expectedRevision >= 0 {
		if count, _ := result.RowsAffected(); count != 1 {
			return RuleDraftValidation{}, ruleDraftConflict(expectedRevision, draft.Revision)
		}
	}
	draft, err = r.GetRuleDraft(ctx, packageID)
	if err != nil {
		return RuleDraftValidation{}, err
	}
	return RuleDraftValidation{Draft: draft, Valid: status == RuleDraftValidated, Diagnostics: diagnostics, Validation: validation}, nil
}

func (r *Resources) PublishRuleDraft(ctx context.Context, packageID string, expectedRevision int, actor, reason string) (RuleVersion, error) {
	draft, err := r.GetRuleDraft(ctx, packageID)
	if err != nil {
		return RuleVersion{}, err
	}
	if expectedRevision >= 0 && draft.Revision != expectedRevision {
		return RuleVersion{}, ruleDraftConflict(expectedRevision, draft.Revision)
	}
	compiled, err := rules.CompilePackage(draft.Content)
	if err != nil {
		return RuleVersion{}, fault.WithField(fault.CodeRulePublishBlocked, "draft", err)
	}
	if err := validateRuleVersionIdentity(compiled); err != nil {
		return RuleVersion{}, err
	}
	irJSON, err := rules.CanonicalJSON(mustJSON(compiled.IR))
	if err != nil {
		return RuleVersion{}, fault.New(fault.CodeRuleCompile, false, err)
	}
	parts := packageParts(compiled.Canonical)
	now := r.clock.Now().UTC()
	tx, err := r.control.BeginTx(ctx, nil)
	if err != nil {
		return RuleVersion{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	if err := requireRulePackageTx(ctx, tx, packageID); err != nil {
		return RuleVersion{}, err
	}
	var currentRevision int
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM rule_drafts WHERE package_id=?`, packageID).Scan(&currentRevision); err != nil {
		return RuleVersion{}, fault.New(fault.CodeInternal, true, err)
	}
	if expectedRevision >= 0 && currentRevision != expectedRevision {
		return RuleVersion{}, ruleDraftConflict(expectedRevision, currentRevision)
	}
	var previousHash sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT current_semantic_hash FROM rule_packages WHERE package_id=?`, packageID).Scan(&previousHash); err != nil {
		return RuleVersion{}, fault.New(fault.CodeInternal, true, err)
	}
	_, err = tx.ExecContext(ctx, `
INSERT OR IGNORE INTO rule_versions
(semantic_hash, rule_set_id, version, package_hash, canonical_json, compiler_version, rule_ir_hash,
 compiled_ir_json, created_at, package_id, status, normalization_algorithm_version, cel_profile_version,
 parameter_schema_json, tests_json, extensions_json, parent_semantic_hash, created_by, published_at, executable)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`, compiled.SemanticHash, compiled.RuleSetID,
		compiled.Version, compiled.PackageHash, string(compiled.Canonical), rules.CompilerVersion, compiled.RuleIRHash,
		string(irJSON), now.Unix(), packageID, RuleVersionPublished, rules.NormalizationAlgorithmVersion,
		rules.CELProfileVersion, string(compiled.ParameterSchema), string(parts.tests), string(parts.extensions),
		nullableText(draft.BaseSemanticHash), actorOrSystem(actor), now.Unix())
	if err != nil {
		return RuleVersion{}, fault.New(fault.CodeConflict, false, err)
	}
	if err := insertRuleTests(ctx, tx, compiled.SemanticHash, parts.tests, now.Unix(), r); err != nil {
		return RuleVersion{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE rule_packages SET current_semantic_hash=?, latest_valid_semantic_hash=?,
 extension_requirements_json=?, revision=revision+1, updated_at=? WHERE package_id=?`, compiled.SemanticHash,
		compiled.SemanticHash, string(parts.extensions), now.Unix(), packageID)
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE rule_drafts SET validation_status='validated', diagnostics_json='[]',
 revision=revision+1, saved_by=?, updated_at=? WHERE package_id=? AND revision=?`, actorOrSystem(actor), now.Unix(), packageID, currentRevision)
	}
	if err == nil {
		id, idErr := r.ids.New(domain.IDRuleAudit)
		if idErr != nil {
			return RuleVersion{}, fault.New(fault.CodeInternal, true, idErr)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO rule_audits
(audit_id, package_id, action, from_semantic_hash, to_semantic_hash, reason, actor_id, created_at)
VALUES (?, ?, 'publish', ?, ?, ?, ?, ?)`, id.String(), packageID, nullableText(previousHash.String),
			compiled.SemanticHash, reason, actorOrSystem(actor), now.Unix())
	}
	if err != nil {
		return RuleVersion{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := tx.Commit(); err != nil {
		return RuleVersion{}, fault.New(fault.CodeInternal, true, err)
	}
	return r.GetRuleVersion(ctx, compiled.SemanticHash)
}

func (r *Resources) DeprecateRuleVersion(ctx context.Context, semanticHash, actor, reason string) (RuleVersion, error) {
	version, err := r.GetRuleVersion(ctx, semanticHash)
	if err != nil {
		return RuleVersion{}, err
	}
	var current string
	if err := r.control.QueryRowContext(ctx, `SELECT COALESCE(current_semantic_hash,'') FROM rule_packages WHERE current_semantic_hash=? LIMIT 1`, semanticHash).Scan(&current); err == nil && current != "" {
		return RuleVersion{}, fault.New(fault.CodeRuleVersionInUse, false, nil)
	}
	now := r.clock.Now().UTC()
	_, err = r.control.ExecContext(ctx, `UPDATE rule_versions SET status=?, deprecated_at=? WHERE semantic_hash=? AND status<>?`, RuleVersionDeprecated, now.Unix(), semanticHash, RuleVersionDeprecated)
	if err != nil {
		return RuleVersion{}, fault.New(fault.CodeInternal, true, err)
	}
	if version.PackageID != "" {
		if err := r.appendRuleAudit(ctx, version.PackageID, "deprecate", semanticHash, "", reason, actorOrSystem(actor), now); err != nil {
			return RuleVersion{}, err
		}
	}
	return r.GetRuleVersion(ctx, semanticHash)
}

func (r *Resources) RollbackRulePackage(ctx context.Context, packageID, targetSemanticHash string, expectedRevision int, actor, reason string, confirm bool) (RuleVersion, error) {
	pkg, err := r.GetRulePackage(ctx, packageID)
	if err != nil {
		return RuleVersion{}, err
	}
	target, err := r.GetRuleVersion(ctx, targetSemanticHash)
	if err != nil {
		return RuleVersion{}, err
	}
	if target.PackageID != "" && target.PackageID != packageID {
		return RuleVersion{}, fault.New(fault.CodeConflict, false, nil)
	}
	if pkg.CurrentSemanticHash != "" && pkg.CurrentSemanticHash != targetSemanticHash {
		current, getErr := r.GetRuleVersion(ctx, pkg.CurrentSemanticHash)
		if getErr != nil {
			return RuleVersion{}, getErr
		}
		life, lifeErr := rules.NewLifecycle()
		if lifeErr != nil {
			return RuleVersion{}, fault.New(fault.CodeInternal, false, lifeErr)
		}
		diff, diffErr := life.DiffRulePackages(current.Canonical, target.Canonical)
		if diffErr != nil {
			return RuleVersion{}, fault.WithField(fault.CodeRuleRollbackBlocked, "targetSemanticHash", diffErr)
		}
		if !diff.ParameterCompatible {
			return RuleVersion{}, fault.New(fault.CodeRuleRollbackBlocked, false, fmt.Errorf("目标版本参数 Schema 不兼容，需要新建参数实例"))
		}
		if diff.BindingReview && !confirm {
			return RuleVersion{}, fault.New(fault.CodeRuleRollbackBlocked, false, fmt.Errorf("回滚影响需要人工确认"))
		}
	}
	if strings.TrimSpace(reason) == "" {
		return RuleVersion{}, fault.WithField(fault.CodeValidation, "reason", nil)
	}
	now := r.clock.Now().UTC()
	tx, err := r.control.BeginTx(ctx, nil)
	if err != nil {
		return RuleVersion{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	var revision int
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM rule_packages WHERE package_id=?`, packageID).Scan(&revision); err != nil {
		return RuleVersion{}, fault.New(fault.CodeInternal, true, err)
	}
	if expectedRevision >= 0 && revision != expectedRevision {
		return RuleVersion{}, rulePackageConflict(expectedRevision, revision)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE rule_packages SET current_semantic_hash=?, revision=revision+1, updated_at=? WHERE package_id=? AND revision=?`, targetSemanticHash, now.Unix(), packageID, revision); err != nil {
		return RuleVersion{}, fault.New(fault.CodeInternal, true, err)
	}
	id, idErr := r.ids.New(domain.IDRuleAudit)
	if idErr != nil {
		return RuleVersion{}, fault.New(fault.CodeInternal, true, idErr)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO rule_audits
(audit_id, package_id, action, from_semantic_hash, to_semantic_hash, reason, actor_id, created_at)
VALUES (?, ?, 'rollback', ?, ?, ?, ?, ?)`, id.String(), packageID, pkg.CurrentSemanticHash, targetSemanticHash, reason, actorOrSystem(actor), now.Unix()); err != nil {
		return RuleVersion{}, fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE rule_versions SET status='published', deprecated_at=NULL WHERE semantic_hash=?`, targetSemanticHash); err != nil {
		return RuleVersion{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := tx.Commit(); err != nil {
		return RuleVersion{}, fault.New(fault.CodeInternal, true, err)
	}
	return r.GetRuleVersion(ctx, targetSemanticHash)
}

func (r *Resources) DiffRuleVersions(ctx context.Context, oldHash, newHash string) (rules.RuleVersionDiff, error) {
	left, err := r.GetRuleVersion(ctx, oldHash)
	if err != nil {
		return rules.RuleVersionDiff{}, err
	}
	right, err := r.GetRuleVersion(ctx, newHash)
	if err != nil {
		return rules.RuleVersionDiff{}, err
	}
	life, err := rules.NewLifecycle()
	if err != nil {
		return rules.RuleVersionDiff{}, fault.New(fault.CodeInternal, false, err)
	}
	result, err := life.DiffRulePackages(left.Canonical, right.Canonical)
	if err != nil {
		return rules.RuleVersionDiff{}, fault.New(fault.CodeRuleImpact, false, err)
	}
	return result, nil
}

func (r *Resources) CreateRuleParameterSet(ctx context.Context, name, semanticHash string, parameters []byte, actor string) (RuleParameterSet, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 256 {
		return RuleParameterSet{}, fault.WithField(fault.CodeValidation, "name", nil)
	}
	version, err := r.GetRuleVersion(ctx, semanticHash)
	if err != nil {
		return RuleParameterSet{}, err
	}
	compiled, err := rules.CompilePackage(version.Canonical)
	if err != nil {
		return RuleParameterSet{}, fault.New(fault.CodeRuleSchemaInvalid, false, err)
	}
	_, _, canonical, err := rules.CompileBinding(compiled, parameters)
	if err != nil {
		return RuleParameterSet{}, fault.New(fault.CodeRuleParameterInvalid, false, err)
	}
	id, err := r.ids.New(domain.IDRuleParameter)
	if err != nil {
		return RuleParameterSet{}, fault.New(fault.CodeInternal, true, err)
	}
	now := r.clock.Now().UTC()
	hash := parameterHash(canonical)
	tx, err := r.control.BeginTx(ctx, nil)
	if err != nil {
		return RuleParameterSet{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO rule_parameter_sets
(parameter_id, name, semantic_hash, current_revision, current_hash, status, created_by, created_at, updated_at)
VALUES (?, ?, ?, 1, ?, 'active', ?, ?, ?)`, id.String(), name, semanticHash, hash, actorOrSystem(actor), now.Unix(), now.Unix()); err != nil {
		return RuleParameterSet{}, fault.New(fault.CodeConflict, false, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO rule_parameter_revisions
(parameter_id, revision, parameters_json, parameters_hash, created_by, created_at) VALUES (?, 1, ?, ?, ?, ?)`, id.String(), string(canonical), hash, actorOrSystem(actor), now.Unix()); err != nil {
		return RuleParameterSet{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := tx.Commit(); err != nil {
		return RuleParameterSet{}, fault.New(fault.CodeInternal, true, err)
	}
	return r.GetRuleParameterSet(ctx, id.String())
}

func (r *Resources) GetRuleParameterSet(ctx context.Context, parameterID string) (RuleParameterSet, error) {
	if _, err := domain.ParseID(domain.IDRuleParameter, parameterID); err != nil {
		return RuleParameterSet{}, fault.New(fault.CodeNotFound, false, nil)
	}
	var result RuleParameterSet
	var createdAt, updatedAt int64
	var parameters string
	err := r.control.QueryRowContext(ctx, `SELECT s.parameter_id, s.name, s.semantic_hash, s.current_revision,
 s.current_hash, s.status, s.created_by, s.created_at, s.updated_at, r.parameters_json
FROM rule_parameter_sets s JOIN rule_parameter_revisions r ON r.parameter_id=s.parameter_id AND r.revision=s.current_revision
WHERE s.parameter_id=?`, parameterID).Scan(&result.ID, &result.Name, &result.SemanticHash, &result.CurrentRevision,
		&result.CurrentHash, &result.Status, &result.CreatedBy, &createdAt, &updatedAt, &parameters)
	if errors.Is(err, sql.ErrNoRows) {
		return RuleParameterSet{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return RuleParameterSet{}, fault.New(fault.CodeInternal, true, err)
	}
	result.Parameters = []byte(parameters)
	result.CreatedAt, result.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
	return result, nil
}

func (r *Resources) UpdateRuleParameterSet(ctx context.Context, parameterID string, parameters []byte, expectedRevision int, actor string) (RuleParameterSet, error) {
	set, err := r.GetRuleParameterSet(ctx, parameterID)
	if err != nil {
		return RuleParameterSet{}, err
	}
	if set.Status != RuleParameterActive {
		return RuleParameterSet{}, fault.New(fault.CodeRuleParameterConflict, false, nil)
	}
	version, err := r.GetRuleVersion(ctx, set.SemanticHash)
	if err != nil {
		return RuleParameterSet{}, err
	}
	compiled, err := rules.CompilePackage(version.Canonical)
	if err != nil {
		return RuleParameterSet{}, fault.New(fault.CodeRuleSchemaInvalid, false, err)
	}
	_, _, canonical, err := rules.CompileBinding(compiled, parameters)
	if err != nil {
		return RuleParameterSet{}, fault.New(fault.CodeRuleParameterInvalid, false, err)
	}
	if expectedRevision >= 0 && set.CurrentRevision != expectedRevision {
		return RuleParameterSet{}, ruleParameterConflict(expectedRevision, set.CurrentRevision)
	}
	now := r.clock.Now().UTC()
	hash := parameterHash(canonical)
	newRevision := set.CurrentRevision + 1
	tx, err := r.control.BeginTx(ctx, nil)
	if err != nil {
		return RuleParameterSet{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	query := `UPDATE rule_parameter_sets SET current_revision=?, current_hash=?, updated_at=? WHERE parameter_id=? AND current_revision=?`
	result, err := tx.ExecContext(ctx, query, newRevision, hash, now.Unix(), parameterID, set.CurrentRevision)
	if err != nil {
		return RuleParameterSet{}, fault.New(fault.CodeInternal, true, err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return RuleParameterSet{}, ruleParameterConflict(set.CurrentRevision, set.CurrentRevision+1)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO rule_parameter_revisions
(parameter_id, revision, parameters_json, parameters_hash, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?)`, parameterID, newRevision, string(canonical), hash, actorOrSystem(actor), now.Unix()); err != nil {
		return RuleParameterSet{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := refreshParameterBindings(ctx, tx, parameterID, set.SemanticHash, compiled, canonical, newRevision, hash, now.Unix()); err != nil {
		return RuleParameterSet{}, err
	}
	if err := tx.Commit(); err != nil {
		return RuleParameterSet{}, fault.New(fault.CodeInternal, true, err)
	}
	return r.GetRuleParameterSet(ctx, parameterID)
}

// ImpactRuleParameterSet 在修改共享参数前返回绑定范围和执行影响。绑定本身在
// UpdateRuleParameterSet 中以同一事务刷新，已经入队的 Job 仍使用自己的快照。
func (r *Resources) ImpactRuleParameterSet(ctx context.Context, parameterID string, parameters []byte) (rules.ImpactResult, error) {
	set, err := r.GetRuleParameterSet(ctx, parameterID)
	if err != nil {
		return rules.ImpactResult{}, err
	}
	version, err := r.GetRuleVersion(ctx, set.SemanticHash)
	if err != nil {
		return rules.ImpactResult{}, err
	}
	life, err := rules.NewLifecycle()
	if err != nil {
		return rules.ImpactResult{}, fault.New(fault.CodeInternal, false, err)
	}
	impact, err := life.ImpactParameters(version.Canonical, set.Parameters, parameters)
	if err != nil {
		return rules.ImpactResult{}, fault.WithField(fault.CodeRuleParameterInvalid, "parameters", err)
	}
	rows, err := r.control.QueryContext(ctx, `SELECT DISTINCT source_id FROM source_rule_bindings WHERE parameter_id=? ORDER BY source_id`, parameterID)
	if err != nil {
		return rules.ImpactResult{}, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	for rows.Next() {
		var sourceID string
		if err := rows.Scan(&sourceID); err != nil {
			return rules.ImpactResult{}, fault.New(fault.CodeInternal, true, err)
		}
		impact.AffectedSources = append(impact.AffectedSources, sourceID)
	}
	if err := rows.Err(); err != nil {
		return rules.ImpactResult{}, fault.New(fault.CodeInternal, true, err)
	}
	if len(impact.AffectedSources) > 1 {
		impact.ReasonCodes = append(impact.ReasonCodes, "shared_parameter_multiple_sources")
		sort.Strings(impact.ReasonCodes)
	}
	return impact, nil
}

func refreshParameterBindings(ctx context.Context, tx *sql.Tx, parameterID, semanticHash string, compiled rules.CompiledPackage, parameters []byte, revision int, parameterIdentity string, updatedAt int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT binding_id, override_json FROM source_rule_bindings WHERE parameter_id=? AND semantic_hash=?`, parameterID, semanticHash)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	type bindingOverride struct {
		id       string
		override string
	}
	var bindings []bindingOverride
	for rows.Next() {
		var item bindingOverride
		if err := rows.Scan(&item.id, &item.override); err != nil {
			_ = rows.Close()
			return fault.New(fault.CodeInternal, true, err)
		}
		bindings = append(bindings, item)
	}
	if err := rows.Close(); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if err := rows.Err(); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	for _, binding := range bindings {
		effective, err := mergeRuleParameters(parameters, []byte(binding.override))
		if err != nil {
			return fault.WithField(fault.CodeRuleParameterInvalid, "override", err)
		}
		ir, irHash, canonicalParameters, err := rules.CompileBinding(compiled, effective)
		if err != nil {
			return fault.WithField(fault.CodeRuleParameterInvalid, "parameters", err)
		}
		irJSON, err := rules.CanonicalJSON(mustJSON(ir))
		if err != nil {
			return fault.New(fault.CodeRuleCompile, false, err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE source_rule_bindings SET parameters_json=?, parameter_revision=?, parameter_hash=?, rule_ir_hash=?, compiled_ir_json=?, updated_at=? WHERE binding_id=? AND parameter_id=?`, string(canonicalParameters), revision, parameterIdentity, irHash, string(irJSON), updatedAt, binding.id, parameterID); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
	}
	return nil
}

func (r *Resources) CopyRuleParameterSet(ctx context.Context, parameterID, name, actor string) (RuleParameterSet, error) {
	set, err := r.GetRuleParameterSet(ctx, parameterID)
	if err != nil {
		return RuleParameterSet{}, err
	}
	return r.CreateRuleParameterSet(ctx, name, set.SemanticHash, set.Parameters, actor)
}

func (r *Resources) DeprecateRuleParameterSet(ctx context.Context, parameterID string) (RuleParameterSet, error) {
	if _, err := r.GetRuleParameterSet(ctx, parameterID); err != nil {
		return RuleParameterSet{}, err
	}
	if _, err := r.control.ExecContext(ctx, `UPDATE rule_parameter_sets SET status='deprecated', updated_at=? WHERE parameter_id=?`, r.clock.Now().UTC().Unix(), parameterID); err != nil {
		return RuleParameterSet{}, fault.New(fault.CodeInternal, true, err)
	}
	return r.GetRuleParameterSet(ctx, parameterID)
}

func (r *Resources) ListRuleVersions(ctx context.Context, options RuleVersionListOptions) ([]RuleVersion, error) {
	query := `SELECT semantic_hash FROM rule_versions WHERE 1=1`
	args := []any{}
	if options.PackageID != "" {
		query += " AND package_id=?"
		args = append(args, options.PackageID)
	}
	if options.Status != "" {
		query += " AND status=?"
		args = append(args, options.Status)
	}
	query += " ORDER BY created_at DESC, semantic_hash"
	rows, err := r.control.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	var hashes []string
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		hashes = append(hashes, hash)
	}
	result := make([]RuleVersion, 0, len(hashes))
	for _, hash := range hashes {
		version, err := r.GetRuleVersion(ctx, hash)
		if err != nil {
			return nil, err
		}
		result = append(result, version)
	}
	return result, nil
}

// CompileRulePackage 使用 control.db 中的编译缓存。缓存键同时包含 semantic、参数身份、
// compiler/CEL/extension registry 版本；命中时只恢复编译 IR，不把缓存误当作不可重建事实。
func (r *Resources) CompileRulePackage(ctx context.Context, input, parameters []byte) (rules.CompileResult, error) {
	compiled, err := rules.CompilePackage(input)
	if err != nil {
		return rules.CompileResult{}, fault.New(fault.CodeRuleSchemaInvalid, false, err)
	}
	ir, irHash, canonicalParameters, err := rules.CompileBinding(compiled, parameters)
	if err != nil {
		return rules.CompileResult{}, fault.New(fault.CodeRuleParameterInvalid, false, err)
	}
	parameterIdentity := parameterHash(canonicalParameters)
	cacheKey := compiled.SemanticHash + "\x00" + parameterIdentity + "\x00" + rules.CompilerVersion + "\x00" + rules.CELProfileVersion + "\x00" + rules.ExtensionRegistryVersion
	var cachedIR string
	err = r.control.QueryRowContext(ctx, `SELECT compiled_ir_json FROM rule_compilation_cache WHERE cache_key=?`, cacheKey).Scan(&cachedIR)
	if err == nil {
		cached, decodeErr := rules.DecodeIR([]byte(cachedIR))
		if decodeErr == nil {
			ir = cached
			return rules.CompileResult{
				ValidationResult: rules.ValidationResult{CanonicalJSON: append([]byte(nil), compiled.Canonical...), PackageHash: compiled.PackageHash, SemanticHash: compiled.SemanticHash},
				RuleIRHash:       irHash, CanonicalParameters: canonicalParameters, IR: ir, CacheHit: true,
			}, nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return rules.CompileResult{}, fault.New(fault.CodeInternal, true, err)
	}
	irJSON, err := rules.CanonicalJSON(mustJSON(ir))
	if err != nil {
		return rules.CompileResult{}, fault.New(fault.CodeRuleCompile, false, err)
	}
	_, err = r.control.ExecContext(ctx, `INSERT OR IGNORE INTO rule_compilation_cache
(cache_key, semantic_hash, parameter_hash, rule_ir_hash, compiler_version, cel_profile_version,
 extension_registry_version, compiled_ir_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, cacheKey,
		compiled.SemanticHash, parameterIdentity, irHash, rules.CompilerVersion, rules.CELProfileVersion,
		rules.ExtensionRegistryVersion, string(irJSON), r.clock.Now().UTC().Unix())
	if err != nil {
		return rules.CompileResult{}, fault.New(fault.CodeInternal, true, err)
	}
	return rules.CompileResult{
		ValidationResult: rules.ValidationResult{CanonicalJSON: append([]byte(nil), compiled.Canonical...), PackageHash: compiled.PackageHash, SemanticHash: compiled.SemanticHash},
		RuleIRHash:       irHash, CanonicalParameters: canonicalParameters, IR: ir,
	}, nil
}

func (r *Resources) SetSourceRuleBindingStatus(ctx context.Context, bindingID, status string) (SourceRuleBinding, error) {
	if status != RuleBindingActive && status != RuleBindingPaused && status != RuleBindingInvalid {
		return SourceRuleBinding{}, fault.WithField(fault.CodeValidation, "status", nil)
	}
	if _, err := r.GetSourceRuleBinding(ctx, bindingID); err != nil {
		return SourceRuleBinding{}, err
	}
	if _, err := r.control.ExecContext(ctx, `UPDATE source_rule_bindings SET status=?, updated_at=? WHERE binding_id=?`, status, r.clock.Now().UTC().Unix(), bindingID); err != nil {
		return SourceRuleBinding{}, fault.New(fault.CodeInternal, true, err)
	}
	return r.GetSourceRuleBinding(ctx, bindingID)
}

// CreateSourceRuleBindingFromParameterSet 将参数集的 semantic/revision/hash 一起冻结到
// Binding；后续参数集更新不会改变已入队或已创建 Binding 的执行身份。
func (r *Resources) CreateSourceRuleBindingFromParameterSet(ctx context.Context, sourceID, parameterID string, priority int, override, condition []byte) (SourceRuleBinding, error) {
	set, err := r.GetRuleParameterSet(ctx, parameterID)
	if err != nil {
		return SourceRuleBinding{}, err
	}
	if set.Status != RuleParameterActive {
		return SourceRuleBinding{}, fault.New(fault.CodeRuleParameterConflict, false, nil)
	}
	canonicalOverride, err := canonicalObjectOrEmpty(override)
	if err != nil {
		return SourceRuleBinding{}, fault.WithField(fault.CodeRuleParameterInvalid, "override", err)
	}
	canonicalCondition, err := canonicalObjectOrEmpty(condition)
	if err != nil {
		return SourceRuleBinding{}, fault.WithField(fault.CodeRuleParameterInvalid, "condition", err)
	}
	return r.createSourceRuleBinding(ctx, sourceID, set.SemanticHash, set.Parameters, priority, parameterID,
		set.CurrentRevision, set.CurrentHash, canonicalOverride, canonicalCondition)
}

func (r *Resources) ListSourceRuleBindings(ctx context.Context, sourceID string) ([]SourceRuleBinding, error) {
	if _, err := r.GetSource(ctx, sourceID); err != nil {
		return nil, err
	}
	rows, err := r.control.QueryContext(ctx, `SELECT binding_id FROM source_rule_bindings WHERE source_id=? ORDER BY priority, binding_id`, sourceID)
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
	result := make([]SourceRuleBinding, 0, len(ids))
	for _, id := range ids {
		binding, err := r.GetSourceRuleBinding(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, binding)
	}
	return result, nil
}

func draftValidation(content []byte, format string) ([]byte, string, []rules.ImportDiagnostic) {
	imported, err := rules.ImportRulePackage(format, content)
	if err != nil {
		return append([]byte(nil), content...), RuleDraftInvalid, []rules.ImportDiagnostic{{Path: rules.ErrorField(err), Message: err.Error()}}
	}
	compiled, err := rules.CompilePackage(imported.CanonicalJSON)
	if err != nil {
		return imported.CanonicalJSON, RuleDraftInvalid, []rules.ImportDiagnostic{{Path: rules.ErrorField(err), Message: err.Error()}}
	}
	return compiled.Canonical, RuleDraftValidated, []rules.ImportDiagnostic{}
}

func normalizeRuleFormat(format string) string {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "yml" {
		return "yaml"
	}
	if format == "json" || format == "yaml" || format == "toml" {
		return format
	}
	return ""
}

func scanRulePackage(scanner interface{ Scan(...any) error }) (RulePackage, error) {
	var result RulePackage
	var current, latest, draft sql.NullString
	var extensions string
	var createdAt, updatedAt int64
	err := scanner.Scan(&result.ID, &result.RuleSetID, &result.Name, &result.Description, &result.Status, &current, &latest, &draft, &extensions, &result.CreatedBy, &createdAt, &updatedAt, &result.Revision)
	result.CurrentSemanticHash, result.LatestValidSemanticHash, result.DraftID = current.String, latest.String, draft.String
	result.ExtensionRequirements = []byte(extensions)
	result.CreatedAt, result.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
	return result, err
}

func scanRuleDraft(scanner interface{ Scan(...any) error }) (RuleDraft, error) {
	var result RuleDraft
	var base sql.NullString
	var content, diagnostics string
	var createdAt, updatedAt int64
	err := scanner.Scan(&result.ID, &result.PackageID, &base, &content, &result.SourceFormat, &result.ValidationStatus, &diagnostics, &result.Revision, &result.SavedBy, &createdAt, &updatedAt)
	result.BaseSemanticHash, result.Content, result.Diagnostics = base.String, []byte(content), []byte(diagnostics)
	result.CreatedAt, result.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
	return result, err
}

func requireRulePackageTx(ctx context.Context, tx *sql.Tx, packageID string) error {
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM rule_packages WHERE package_id=?`, packageID).Scan(&status); errors.Is(err, sql.ErrNoRows) {
		return fault.New(fault.CodeNotFound, false, nil)
	} else if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	} else if status != RulePackageActive {
		return fault.New(fault.CodeConflict, false, fmt.Errorf("规则包不是 active 状态"))
	}
	return nil
}

func ruleDraftConflict(expected, actual int) *fault.Error {
	return fault.WithField(fault.CodeRuleDraftConflict, "revision", fmt.Errorf("草稿 revision 冲突，expected=%d actual=%d", expected, actual))
}

func rulePackageConflict(expected, actual int) *fault.Error {
	return fault.WithField(fault.CodeRulePackageConflict, "revision", fmt.Errorf("规则包 revision 冲突，expected=%d actual=%d", expected, actual))
}

func ruleParameterConflict(expected, actual int) *fault.Error {
	return fault.WithField(fault.CodeRuleParameterConflict, "revision", fmt.Errorf("参数 revision 冲突，expected=%d actual=%d", expected, actual))
}

func nullableText(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func actorOrSystem(value string) string {
	if strings.TrimSpace(value) == "" {
		return "system"
	}
	return value
}

func canonicalObjectOrEmpty(value []byte) ([]byte, error) {
	if len(value) == 0 {
		return []byte("{}"), nil
	}
	canonical, err := rules.CanonicalJSON(value)
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if err := json.Unmarshal(canonical, &object); err != nil || object == nil {
		return nil, fmt.Errorf("必须是 JSON 对象")
	}
	return canonical, nil
}

// mergeRuleParameters 只允许 Binding 对参数实例做一层受控字段覆盖。覆盖后的对象仍
// 会经过 RuleVersion parameter schema 校验，且不会改写共享参数实例本身。
func mergeRuleParameters(parameters, override []byte) ([]byte, error) {
	base, err := canonicalObjectOrEmpty(parameters)
	if err != nil {
		return nil, err
	}
	canonicalOverride, err := canonicalObjectOrEmpty(override)
	if err != nil {
		return nil, err
	}
	var baseFields, overrideFields map[string]json.RawMessage
	if err := json.Unmarshal(base, &baseFields); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(canonicalOverride, &overrideFields); err != nil {
		return nil, err
	}
	for key, value := range overrideFields {
		baseFields[key] = append([]byte(nil), value...)
	}
	merged, err := json.Marshal(baseFields)
	if err != nil {
		return nil, err
	}
	return rules.CanonicalJSON(merged)
}

func canonicalBindingCondition(value []byte) ([]byte, error) {
	canonical, err := canonicalObjectOrEmpty(value)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &fields); err != nil {
		return nil, err
	}
	for key, raw := range fields {
		if key != "sourceId" && key != "libraryId" && key != "displayName" {
			return nil, fmt.Errorf("不支持的 Binding condition 字段 %q", key)
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("Binding condition 字段 %q 必须是非空字符串", key)
		}
	}
	return canonical, nil
}

func bindingConditionMatches(condition []byte, source Source) (bool, error) {
	canonical, err := canonicalBindingCondition(condition)
	if err != nil {
		return false, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &fields); err != nil {
		return false, err
	}
	for key, raw := range fields {
		var expected string
		if err := json.Unmarshal(raw, &expected); err != nil {
			return false, err
		}
		var actual string
		switch key {
		case "sourceId":
			actual = source.ID
		case "libraryId":
			actual = source.LibraryID
		case "displayName":
			actual = source.DisplayName
		}
		if actual != expected {
			return false, nil
		}
	}
	return true, nil
}

type packagePartsResult struct {
	tests      json.RawMessage
	extensions json.RawMessage
}

func packageParts(input []byte) packagePartsResult {
	result := packagePartsResult{tests: json.RawMessage("[]"), extensions: json.RawMessage("{}")}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(input, &object); err != nil {
		return result
	}
	if value, ok := object["tests"]; ok {
		result.tests = append([]byte(nil), value...)
	}
	if value, ok := object["extensions"]; ok {
		result.extensions = append([]byte(nil), value...)
	}
	return result
}

func insertRuleTests(ctx context.Context, tx *sql.Tx, semanticHash string, tests []byte, createdAt int64, resources *Resources) error {
	var values []json.RawMessage
	if err := json.Unmarshal(tests, &values); err != nil {
		return fault.New(fault.CodeRuleSchemaInvalid, false, err)
	}
	for ordinal, value := range values {
		id, err := resources.ids.New(domain.IDRuleTestCase)
		if err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO rule_test_cases
(test_case_id, semantic_hash, ordinal, case_json, expected_json, created_at) VALUES (?, ?, ?, ?, '{}', ?)`, id.String(), semanticHash, ordinal, string(value), createdAt); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
	}
	return nil
}

func validateRuleVersionIdentity(compiled rules.CompiledPackage) error {
	if compiled.RuleSetID == "" || compiled.Version == "" || compiled.SemanticHash == "" {
		return fault.New(fault.CodeRuleSchemaInvalid, false, fmt.Errorf("RuleVersion 身份不完整"))
	}
	return nil
}

func parameterHash(canonical []byte) string {
	hash := sha256.Sum256(append([]byte("gallery-rule-parameters\x00v1\x00"), canonical...))
	return hex.EncodeToString(hash[:])
}

// RuleParameterHash 暴露参数快照的稳定身份，供 Job/Scanner 在不访问数据库内部字段的
// 前提下记录同一份参数的执行身份。
func RuleParameterHash(canonical []byte) string { return parameterHash(canonical) }

func (r *Resources) appendRuleAudit(ctx context.Context, packageID, action, fromHash, toHash, reason, actor string, now time.Time) error {
	id, err := r.ids.New(domain.IDRuleAudit)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if _, err := r.control.ExecContext(ctx, `INSERT INTO rule_audits
(audit_id, package_id, action, from_semantic_hash, to_semantic_hash, reason, actor_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id.String(), packageID, action, nullableText(fromHash), nullableText(toHash), reason, actorOrSystem(actor), now.Unix()); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return nil
}

func (r *Resources) appendRuleAuditTx(ctx context.Context, tx *sql.Tx, packageID, action, fromHash, toHash, reason, actor string, now time.Time) error {
	// 状态变更、revision 和审计必须在同一 control.db 事务中提交，避免出现
	// 已改变状态但没有可追溯操作记录的半完成生命周期动作。
	id, err := r.ids.New(domain.IDRuleAudit)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO rule_audits
(audit_id, package_id, action, from_semantic_hash, to_semantic_hash, reason, actor_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id.String(), packageID, action, nullableText(fromHash), nullableText(toHash), reason, actorOrSystem(actor), now.Unix()); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return nil
}
