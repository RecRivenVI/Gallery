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
	query := `SELECT issue_id, source_id, entity_type, source_key, work_source_key, provider_id, external_id,
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
	row := r.control.QueryRowContext(ctx, `SELECT issue_id, source_id, entity_type, source_key, work_source_key,
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
	if err := row.Scan(&issue.ID, &issue.SourceID, &issue.EntityType, &issue.SourceKey, &workSourceKey,
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
