package application

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
)

// BindingIssue 是扫描无法唯一确定 Canonical Binding 时持久化的人工审查记录。它引用
// Source-derived 稳定键与候选 Canonical 实体，供用户查看证据并选择修复方式。
type BindingIssue struct {
	ID               string
	SourceID         string
	EntityType       string
	StructureKind    string
	SourceKey        string
	WorkSourceKey    string
	ProviderID       string
	ExternalID       string
	Code             string
	CandidateCount   int
	Status           string
	Resolution       string
	ResolvedTargetID string
	ResolvedBy       string
	Version          int
	CreatedAt        time.Time
	UpdatedAt        time.Time
	ResolvedAt       *time.Time
	Candidates       []BindingIssueCandidate
}

type BindingIssueCandidate struct {
	CandidateID   string
	CandidateKind string
	MatchSignal   string
	MatchValue    string
	Label         string
}

type BindingIssueFilter struct {
	SourceID   string
	EntityType string
	Status     string
}

type BindingIssuePage struct {
	Items      []BindingIssue
	NextCursor string
}

type bindingIssueInput struct {
	sourceID       string
	entityType     string
	sourceKey      string
	workSourceKey  string
	providerID     string
	externalID     string
	candidateIDs   []string
	candidateKind  string
	matchSignal    string
	matchValue     string
	candidateCount int
}

// recordBindingIssue 在扫描事务内持久化或复用一个 Binding issue，然后提交事务并返回
// BINDING_REVIEW_REQUIRED。相同证据（候选指纹一致）复用现有 open/dismissed issue，尊重
// 用户的忽略决定，不重复产生；证据变化时将旧 issue 标为 superseded 并新建 open issue。
func (r *Resources) recordBindingIssue(ctx context.Context, tx *sql.Tx, in bindingIssueInput, now int64) error {
	count := in.candidateCount
	if count == 0 {
		count = len(in.candidateIDs)
	}
	fingerprint := strings.Join(in.candidateIDs, "\x00")
	var existingID, existingFingerprint, existingStatus string
	err := tx.QueryRowContext(ctx, `SELECT issue_id, candidate_fingerprint, status FROM binding_issues
WHERE source_id=? AND entity_type=? AND source_key=? AND status IN ('open', 'dismissed')
ORDER BY created_at DESC, issue_id DESC LIMIT 1`, in.sourceID, in.entityType, in.sourceKey).
		Scan(&existingID, &existingFingerprint, &existingStatus)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fault.New(fault.CodeInternal, true, err)
	}
	if err == nil {
		if existingFingerprint == fingerprint {
			if _, err := tx.ExecContext(ctx, `UPDATE binding_issues SET updated_at=? WHERE issue_id=?`, now, existingID); err != nil {
				return fault.New(fault.CodeInternal, true, err)
			}
			return r.commitBindingIssue(tx)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE binding_issues SET status='superseded', updated_at=? WHERE issue_id=?`, now, existingID); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
	}
	id, err := r.ids.New(domain.IDBindingIssue)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO binding_issues
(issue_id, source_id, entity_type, source_key, work_source_key, provider_id, external_id, code,
 candidate_fingerprint, candidate_count, status, version, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'open', 1, ?, ?)`,
		id.String(), in.sourceID, in.entityType, in.sourceKey, in.workSourceKey, in.providerID, in.externalID,
		string(fault.CodeBindingReviewRequired), fingerprint, count, now, now); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	for ordinal, candidateID := range in.candidateIDs {
		label, err := candidateLabel(ctx, tx, in.candidateKind, candidateID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO binding_issue_candidates
(issue_id, ordinal, candidate_id, candidate_kind, match_signal, match_value, label)
VALUES (?, ?, ?, ?, ?, ?, ?)`, id.String(), ordinal, candidateID, in.candidateKind,
			in.matchSignal, in.matchValue, label); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
	}
	return r.commitBindingIssue(tx)
}

// commitBindingIssue 提交承载 issue 的扫描事务，使 issue 在扫描失败后依然持久，然后返回
// 结构化 BINDING_REVIEW_REQUIRED。
func (r *Resources) commitBindingIssue(tx *sql.Tx) error {
	if err := tx.Commit(); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return fault.WithField(fault.CodeBindingReviewRequired, "sourceKey", nil)
}

// reconcileOpenIssues 在扫描成功后收敛遗留 open issue：source_key 本次被发现说明冲突已
// 消除，标为 resolved；未被发现说明其来源候选已消失，标为 stale。
func (r *Resources) reconcileOpenIssues(ctx context.Context, tx *sql.Tx, sourceID string, seenKeys map[string]struct{}, now int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT issue_id, source_key FROM binding_issues
WHERE source_id=? AND status='open'`, sourceID)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	type openIssue struct{ id, sourceKey string }
	var open []openIssue
	for rows.Next() {
		var item openIssue
		if err := rows.Scan(&item.id, &item.sourceKey); err != nil {
			rows.Close()
			return fault.New(fault.CodeInternal, true, err)
		}
		open = append(open, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fault.New(fault.CodeInternal, true, err)
	}
	if err := rows.Close(); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	for _, item := range open {
		status := "stale"
		var resolvedAt any
		if _, seen := seenKeys[item.sourceKey]; seen {
			status, resolvedAt = "resolved", now
		}
		if _, err := tx.ExecContext(ctx, `UPDATE binding_issues SET status=?, resolved_at=?, updated_at=? WHERE issue_id=?`,
			status, resolvedAt, now, item.id); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
	}
	return nil
}

func candidateLabel(ctx context.Context, tx *sql.Tx, kind, id string) (string, error) {
	var query string
	switch kind {
	case "work":
		query = "SELECT title FROM canonical_works WHERE work_id=?"
	case "creator":
		query = "SELECT name FROM canonical_creators WHERE creator_id=?"
	case "media":
		query = "SELECT 'occurrence ' || ordinal FROM canonical_media WHERE media_id=?"
	default:
		return "", nil
	}
	var label string
	if err := tx.QueryRowContext(ctx, query, id).Scan(&label); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fault.New(fault.CodeInternal, true, err)
	}
	return label, nil
}

// ListBindingIssues 按 Source、实体类型和状态过滤，以 (created_at, issue_id) keyset 分页。
func (r *Resources) ListBindingIssues(ctx context.Context, filter BindingIssueFilter, cursor string, limit int) (BindingIssuePage, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	conditions := []string{"1=1"}
	args := []any{}
	if filter.SourceID != "" {
		if _, err := domain.ParseID(domain.IDSource, filter.SourceID); err != nil {
			return BindingIssuePage{}, fault.WithField(fault.CodeValidation, "sourceId", nil)
		}
		conditions = append(conditions, "source_id=?")
		args = append(args, filter.SourceID)
	}
	if filter.EntityType != "" {
		if filter.EntityType != "work" && filter.EntityType != "creator" && filter.EntityType != "media" {
			return BindingIssuePage{}, fault.WithField(fault.CodeValidation, "entityType", nil)
		}
		conditions = append(conditions, "entity_type=?")
		args = append(args, filter.EntityType)
	}
	if filter.Status != "" {
		if !validIssueStatus(filter.Status) {
			return BindingIssuePage{}, fault.WithField(fault.CodeValidation, "status", nil)
		}
		conditions = append(conditions, "status=?")
		args = append(args, filter.Status)
	}
	if cursor != "" {
		createdAt, issueID, err := decodeIssueCursor(cursor)
		if err != nil {
			return BindingIssuePage{}, err
		}
		conditions = append(conditions, "(created_at > ? OR (created_at = ? AND issue_id > ?))")
		args = append(args, createdAt, createdAt, issueID)
	}
	query := `SELECT issue_id, source_id, entity_type, COALESCE(structure_kind, ''), source_key, work_source_key, provider_id, external_id,
code, candidate_count, status, resolution, resolved_target_id, resolved_by, version, created_at, updated_at, resolved_at
FROM binding_issues WHERE ` + strings.Join(conditions, " AND ") +
		` ORDER BY created_at, issue_id LIMIT ?`
	args = append(args, limit+1)
	rows, err := r.control.QueryContext(ctx, query, args...)
	if err != nil {
		return BindingIssuePage{}, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	var items []BindingIssue
	for rows.Next() {
		issue, err := scanBindingIssue(rows)
		if err != nil {
			return BindingIssuePage{}, err
		}
		items = append(items, issue)
	}
	if err := rows.Err(); err != nil {
		return BindingIssuePage{}, fault.New(fault.CodeInternal, true, err)
	}
	page := BindingIssuePage{Items: items}
	if len(items) > limit {
		last := items[limit-1]
		page.Items = items[:limit]
		page.NextCursor = encodeIssueCursor(last.CreatedAt.Unix(), last.ID)
	}
	return page, nil
}

// GetBindingIssue 返回单个 issue 及其候选证据。
func (r *Resources) GetBindingIssue(ctx context.Context, issueID string) (BindingIssue, error) {
	if _, err := domain.ParseID(domain.IDBindingIssue, issueID); err != nil {
		return BindingIssue{}, fault.New(fault.CodeNotFound, false, nil)
	}
	row := r.control.QueryRowContext(ctx, `SELECT issue_id, source_id, entity_type, COALESCE(structure_kind, ''), source_key, work_source_key,
provider_id, external_id, code, candidate_count, status, resolution, resolved_target_id, resolved_by,
version, created_at, updated_at, resolved_at FROM binding_issues WHERE issue_id=?`, issueID)
	issue, err := scanBindingIssueRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return BindingIssue{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return BindingIssue{}, err
	}
	candidates, err := r.issueCandidates(ctx, issueID)
	if err != nil {
		return BindingIssue{}, err
	}
	issue.Candidates = candidates
	return issue, nil
}

func (r *Resources) issueCandidates(ctx context.Context, issueID string) ([]BindingIssueCandidate, error) {
	rows, err := r.control.QueryContext(ctx, `SELECT candidate_id, candidate_kind, match_signal, match_value, label
FROM binding_issue_candidates WHERE issue_id=? ORDER BY ordinal`, issueID)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	result := make([]BindingIssueCandidate, 0)
	for rows.Next() {
		var candidate BindingIssueCandidate
		if err := rows.Scan(&candidate.CandidateID, &candidate.CandidateKind, &candidate.MatchSignal,
			&candidate.MatchValue, &candidate.Label); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		result = append(result, candidate)
	}
	return result, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanBindingIssue(rows *sql.Rows) (BindingIssue, error) {
	return scanBindingIssueRow(rows)
}

func scanBindingIssueRow(row rowScanner) (BindingIssue, error) {
	var issue BindingIssue
	var resolution, resolvedTarget, resolvedBy sql.NullString
	var workSourceKey, providerID, externalID sql.NullString
	var createdAt, updatedAt int64
	var resolvedAt sql.NullInt64
	if err := row.Scan(&issue.ID, &issue.SourceID, &issue.EntityType, &issue.StructureKind, &issue.SourceKey, &workSourceKey,
		&providerID, &externalID, &issue.Code, &issue.CandidateCount, &issue.Status, &resolution,
		&resolvedTarget, &resolvedBy, &issue.Version, &createdAt, &updatedAt, &resolvedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return BindingIssue{}, err
		}
		return BindingIssue{}, fault.New(fault.CodeInternal, true, err)
	}
	issue.WorkSourceKey, issue.ProviderID, issue.ExternalID = workSourceKey.String, providerID.String, externalID.String
	issue.Resolution, issue.ResolvedTargetID, issue.ResolvedBy = resolution.String, resolvedTarget.String, resolvedBy.String
	issue.CreatedAt = time.Unix(createdAt, 0).UTC()
	issue.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	if resolvedAt.Valid {
		resolved := time.Unix(resolvedAt.Int64, 0).UTC()
		issue.ResolvedAt = &resolved
	}
	return issue, nil
}

func validIssueStatus(status string) bool {
	switch status {
	case "open", "resolved", "dismissed", "superseded", "stale":
		return true
	}
	return false
}

func encodeIssueCursor(createdAt int64, issueID string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(createdAt, 10) + ":" + issueID))
}

func decodeIssueCursor(cursor string) (int64, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, "", fault.New(fault.CodeCursorInvalid, false, nil)
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) != 2 {
		return 0, "", fault.New(fault.CodeCursorInvalid, false, nil)
	}
	createdAt, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, "", fault.New(fault.CodeCursorInvalid, false, nil)
	}
	if _, err := domain.ParseID(domain.IDBindingIssue, parts[1]); err != nil {
		return 0, "", fault.New(fault.CodeCursorInvalid, false, nil)
	}
	return createdAt, parts[1], nil
}

func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// ResolveBindingIssue 人工修复一个 Binding issue：bind_existing 把来源稳定键绑定到某个
// 候选 Canonical 实体，create_new/keep_separate 排除全部候选使下一次扫描创建新实体。
// 修复效果以 control.db 的 Binding 变更表达，下一次扫描据此重建投影；不改写 Canonical
// 用户事实。乐观 version 防止并发覆盖。
func (r *Resources) ResolveBindingIssue(ctx context.Context, issueID, resolvedBy, decision, targetID string, version int) (BindingIssue, error) {
	if strings.TrimSpace(resolvedBy) == "" {
		return BindingIssue{}, fault.New(fault.CodeValidation, false, nil)
	}
	if decision != "bind_existing" && decision != "create_new" && decision != "keep_separate" {
		return BindingIssue{}, fault.WithField(fault.CodeValidation, "decision", nil)
	}
	if _, err := domain.ParseID(domain.IDBindingIssue, issueID); err != nil {
		return BindingIssue{}, fault.New(fault.CodeNotFound, false, nil)
	}
	tx, err := r.control.BeginTx(ctx, nil)
	if err != nil {
		return BindingIssue{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	sourceID, entityType, sourceKey, status, currentVersion, err := lockIssue(ctx, tx, issueID)
	if err != nil {
		return BindingIssue{}, err
	}
	if currentVersion != version {
		return BindingIssue{}, fault.New(fault.CodeConflict, false, nil)
	}
	if status != "open" && status != "dismissed" {
		return BindingIssue{}, fault.New(fault.CodeConflict, false, nil)
	}
	candidates, err := candidateIDs(ctx, tx, issueID)
	if err != nil {
		return BindingIssue{}, err
	}
	now := r.clock.Now().UTC().Unix()
	switch decision {
	case "bind_existing":
		if !containsString(candidates, targetID) {
			return BindingIssue{}, fault.WithField(fault.CodeValidation, "targetId", nil)
		}
		if err := r.bindActive(ctx, tx, entityType, sourceID, sourceKey, targetID, now); err != nil {
			return BindingIssue{}, err
		}
	default:
		for _, candidate := range candidates {
			if err := r.excludeCandidate(ctx, tx, entityType, sourceID, sourceKey, candidate, now); err != nil {
				return BindingIssue{}, err
			}
		}
	}
	var target any
	if decision == "bind_existing" {
		target = targetID
	}
	if _, err := tx.ExecContext(ctx, `UPDATE binding_issues SET status='resolved', resolution=?,
resolved_target_id=?, resolved_by=?, version=version+1, updated_at=?, resolved_at=? WHERE issue_id=? AND version=?`,
		decision, target, resolvedBy, now, now, issueID, version); err != nil {
		return BindingIssue{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := tx.Commit(); err != nil {
		return BindingIssue{}, fault.New(fault.CodeInternal, true, err)
	}
	return r.GetBindingIssue(ctx, issueID)
}

// DismissBindingIssue 忽略/延后一个 open issue：状态转为 dismissed。它不添加任何 Binding
// 变更，因此扫描仍会因未解决的身份歧义失败，但相同证据不再反复产生新 issue（复用被忽略
// 的记录）。
func (r *Resources) DismissBindingIssue(ctx context.Context, issueID, resolvedBy string, version int) (BindingIssue, error) {
	return r.transitionIssue(ctx, issueID, resolvedBy, version, []string{"open"}, `UPDATE binding_issues
SET status='dismissed', resolution='dismissed', resolved_by=?, version=version+1, updated_at=? WHERE issue_id=? AND version=?`)
}

// ReopenBindingIssue 重新打开一个被忽略、过时或被替代的 issue，清除解决信息。
func (r *Resources) ReopenBindingIssue(ctx context.Context, issueID, resolvedBy string, version int) (BindingIssue, error) {
	return r.transitionIssue(ctx, issueID, resolvedBy, version, []string{"dismissed", "stale", "superseded"}, `UPDATE binding_issues
SET status='open', resolution=NULL, resolved_target_id=NULL, resolved_by=?, resolved_at=NULL,
version=version+1, updated_at=? WHERE issue_id=? AND version=?`)
}

func (r *Resources) transitionIssue(ctx context.Context, issueID, resolvedBy string, version int, allowed []string, update string) (BindingIssue, error) {
	if strings.TrimSpace(resolvedBy) == "" {
		return BindingIssue{}, fault.New(fault.CodeValidation, false, nil)
	}
	if _, err := domain.ParseID(domain.IDBindingIssue, issueID); err != nil {
		return BindingIssue{}, fault.New(fault.CodeNotFound, false, nil)
	}
	tx, err := r.control.BeginTx(ctx, nil)
	if err != nil {
		return BindingIssue{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	_, _, _, status, currentVersion, err := lockIssue(ctx, tx, issueID)
	if err != nil {
		return BindingIssue{}, err
	}
	if currentVersion != version {
		return BindingIssue{}, fault.New(fault.CodeConflict, false, nil)
	}
	if !containsString(allowed, status) {
		return BindingIssue{}, fault.New(fault.CodeConflict, false, nil)
	}
	now := r.clock.Now().UTC().Unix()
	if _, err := tx.ExecContext(ctx, update, resolvedBy, now, issueID, version); err != nil {
		return BindingIssue{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := tx.Commit(); err != nil {
		return BindingIssue{}, fault.New(fault.CodeInternal, true, err)
	}
	return r.GetBindingIssue(ctx, issueID)
}

// UnbindMedia 手动解绑一个 SourceMedia：其 active MediaBinding 转为 manual_unbound，下一次
// 扫描将为该来源媒体建立新的 CanonicalMedia occurrence，不影响其他 Source Binding 与用户事实。
func (r *Resources) UnbindMedia(ctx context.Context, sourceID, sourceKey string) (string, error) {
	tx, err := r.control.BeginTx(ctx, nil)
	if err != nil {
		return "", fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	var mediaID string
	if err := tx.QueryRowContext(ctx, `SELECT media_id FROM media_bindings
WHERE source_id=? AND source_key=? AND status='active'`, sourceID, sourceKey).Scan(&mediaID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fault.New(fault.CodeNotFound, false, nil)
		}
		return "", fault.New(fault.CodeInternal, true, err)
	}
	now := r.clock.Now().UTC().Unix()
	if _, err := tx.ExecContext(ctx, `UPDATE media_bindings SET status='manual_unbound', updated_at=?
WHERE source_id=? AND source_key=? AND status='active'`, now, sourceID, sourceKey); err != nil {
		return "", fault.New(fault.CodeInternal, true, err)
	}
	if err := tx.Commit(); err != nil {
		return "", fault.New(fault.CodeInternal, true, err)
	}
	return mediaID, nil
}

// UndoManualUnbind 撤销一次针对某来源稳定键的手动 Work 解绑，把 manual_unbound Binding 及其
// 媒体 Binding 恢复为 active。若解绑后已有新的 active Binding 依赖该稳定键（例如已经重扫拆分），
// 或存在多个 manual_unbound 无法确定唯一目标，则返回结构化冲突。
func (r *Resources) UndoManualUnbind(ctx context.Context, sourceID, sourceKey string) (string, error) {
	tx, err := r.control.BeginTx(ctx, nil)
	if err != nil {
		return "", fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	var activeCount int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM work_bindings
WHERE source_id=? AND source_key=? AND status='active'`, sourceID, sourceKey).Scan(&activeCount); err != nil {
		return "", fault.New(fault.CodeInternal, true, err)
	}
	if activeCount > 0 {
		return "", fault.New(fault.CodeConflict, false, nil)
	}
	rows, err := tx.QueryContext(ctx, `SELECT binding_id, work_id FROM work_bindings
WHERE source_id=? AND source_key=? AND status='manual_unbound'`, sourceID, sourceKey)
	if err != nil {
		return "", fault.New(fault.CodeInternal, true, err)
	}
	type unbound struct{ bindingID, workID string }
	var candidates []unbound
	for rows.Next() {
		var item unbound
		if err := rows.Scan(&item.bindingID, &item.workID); err != nil {
			rows.Close()
			return "", fault.New(fault.CodeInternal, true, err)
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", fault.New(fault.CodeInternal, true, err)
	}
	if err := rows.Close(); err != nil {
		return "", fault.New(fault.CodeInternal, true, err)
	}
	if len(candidates) == 0 {
		return "", fault.New(fault.CodeNotFound, false, nil)
	}
	if len(candidates) > 1 {
		return "", fault.New(fault.CodeConflict, false, nil)
	}
	restored := candidates[0]
	now := r.clock.Now().UTC().Unix()
	if _, err := tx.ExecContext(ctx, `UPDATE work_bindings SET status='active', updated_at=? WHERE binding_id=?`,
		now, restored.bindingID); err != nil {
		return "", fault.New(fault.CodeInternal, true, err)
	}
	if err := r.restoreMediaBindings(ctx, tx, sourceID, restored.workID, now); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", fault.New(fault.CodeInternal, true, err)
	}
	return restored.workID, nil
}

func (r *Resources) restoreMediaBindings(ctx context.Context, tx *sql.Tx, sourceID, workID string, now int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT binding_id, source_key FROM media_bindings
WHERE source_id=? AND work_id=? AND status='manual_unbound'`, sourceID, workID)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	type unboundMedia struct{ bindingID, sourceKey string }
	var media []unboundMedia
	for rows.Next() {
		var item unboundMedia
		if err := rows.Scan(&item.bindingID, &item.sourceKey); err != nil {
			rows.Close()
			return fault.New(fault.CodeInternal, true, err)
		}
		media = append(media, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fault.New(fault.CodeInternal, true, err)
	}
	if err := rows.Close(); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	for _, item := range media {
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM media_bindings
WHERE source_id=? AND source_key=? AND status='active'`, sourceID, item.sourceKey).Scan(&active); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
		if active > 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE media_bindings SET status='active', updated_at=? WHERE binding_id=?`,
			now, item.bindingID); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
	}
	return nil
}

// bindActive 为来源稳定键建立一个指向 target 的 active Binding，供扫描据此唯一解析。
func (r *Resources) bindActive(ctx context.Context, tx *sql.Tx, entityType, sourceID, sourceKey, targetID string, now int64) error {
	switch entityType {
	case "work":
		id, err := r.ids.New(domain.IDWorkBinding)
		if err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO work_bindings
(binding_id, source_id, source_key, work_id, identity_version, status, last_seen_generation, created_at, updated_at)
VALUES (?, ?, ?, ?, 1, 'active', 0, ?, ?)`, id.String(), sourceID, sourceKey, targetID, now, now); err != nil {
			return bindConflict(err)
		}
	case "creator":
		id, err := r.ids.New(domain.IDCreatorBinding)
		if err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO creator_bindings
(binding_id, source_id, source_key, creator_id, identity_version, status, last_seen_generation, created_at, updated_at)
VALUES (?, ?, ?, ?, 1, 'active', 0, ?, ?)`, id.String(), sourceID, sourceKey, targetID, now, now); err != nil {
			return bindConflict(err)
		}
	case "media":
		workID, err := mediaWorkID(ctx, tx, targetID)
		if err != nil {
			return err
		}
		id, err := r.ids.New(domain.IDMediaBinding)
		if err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO media_bindings
(binding_id, source_id, source_key, media_id, work_id, identity_version, status, last_seen_generation, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, 1, 'active', 0, ?, ?)`, id.String(), sourceID, sourceKey, targetID, workID, now, now); err != nil {
			return bindConflict(err)
		}
	}
	return nil
}

// excludeCandidate 以 manual_unbound Binding 排除某候选，使扫描不再把该来源稳定键解析到它。
func (r *Resources) excludeCandidate(ctx context.Context, tx *sql.Tx, entityType, sourceID, sourceKey, candidateID string, now int64) error {
	switch entityType {
	case "work":
		id, err := r.ids.New(domain.IDWorkBinding)
		if err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO work_bindings
(binding_id, source_id, source_key, work_id, identity_version, status, last_seen_generation, created_at, updated_at)
VALUES (?, ?, ?, ?, 1, 'manual_unbound', 0, ?, ?)`, id.String(), sourceID, sourceKey, candidateID, now, now); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
	case "creator":
		id, err := r.ids.New(domain.IDCreatorBinding)
		if err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO creator_bindings
(binding_id, source_id, source_key, creator_id, identity_version, status, last_seen_generation, created_at, updated_at)
VALUES (?, ?, ?, ?, 1, 'manual_unbound', 0, ?, ?)`, id.String(), sourceID, sourceKey, candidateID, now, now); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
	case "media":
		workID, err := mediaWorkID(ctx, tx, candidateID)
		if err != nil {
			return err
		}
		id, err := r.ids.New(domain.IDMediaBinding)
		if err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO media_bindings
(binding_id, source_id, source_key, media_id, work_id, identity_version, status, last_seen_generation, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, 1, 'manual_unbound', 0, ?, ?)`, id.String(), sourceID, sourceKey, candidateID, workID, now, now); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
	}
	return nil
}

func mediaWorkID(ctx context.Context, tx *sql.Tx, mediaID string) (string, error) {
	var workID string
	if err := tx.QueryRowContext(ctx, `SELECT work_id FROM canonical_media WHERE media_id=?`, mediaID).Scan(&workID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fault.WithField(fault.CodeValidation, "targetId", nil)
		}
		return "", fault.New(fault.CodeInternal, true, err)
	}
	return workID, nil
}

func lockIssue(ctx context.Context, tx *sql.Tx, issueID string) (sourceID, entityType, sourceKey, status string, version int, err error) {
	scanErr := tx.QueryRowContext(ctx, `SELECT source_id, entity_type, source_key, status, version
FROM binding_issues WHERE issue_id=?`, issueID).Scan(&sourceID, &entityType, &sourceKey, &status, &version)
	if errors.Is(scanErr, sql.ErrNoRows) {
		return "", "", "", "", 0, fault.New(fault.CodeNotFound, false, nil)
	}
	if scanErr != nil {
		return "", "", "", "", 0, fault.New(fault.CodeInternal, true, scanErr)
	}
	return sourceID, entityType, sourceKey, status, version, nil
}

func candidateIDs(ctx context.Context, tx *sql.Tx, issueID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT candidate_id FROM binding_issue_candidates
WHERE issue_id=? ORDER BY ordinal`, issueID)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func bindConflict(err error) error {
	if strings.Contains(strings.ToLower(err.Error()), "unique") {
		return fault.New(fault.CodeConflict, false, nil)
	}
	return fault.New(fault.CodeInternal, true, err)
}
