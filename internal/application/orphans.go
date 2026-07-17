package application

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
)

// OrphanCandidate 是一个 Source-derived Binding 在连续多次成功扫描中缺失、达到保留窗口后
// 进入人工审查的记录。它指向仍然保留的 Canonical 实体：确认孤立、解绑或延长保留都不会删除
// Canonical、Overlay、收藏或进度。
type OrphanCandidate struct {
	BindingID          string
	EntityType         string
	SourceID           string
	SourceKey          string
	CanonicalID        string
	CanonicalLabel     string
	MissedScans        int
	RetentionThreshold int
	LastSeenGeneration int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type OrphanCandidateFilter struct {
	SourceID   string
	EntityType string
}

type OrphanCandidatePage struct {
	Items      []OrphanCandidate
	NextCursor string
}

// OrphanDecisionResult 汇报一次 orphan candidate 决策的结果，供 API 返回。
type OrphanDecisionResult struct {
	BindingID   string
	EntityType  string
	Decision    string
	NewStatus   string
	CanonicalID string
}

type orphanEntity struct {
	entityType   string
	table        string
	canonicalCol string
}

var orphanEntities = []orphanEntity{
	{"work", "work_bindings", "work_id"},
	{"media", "media_bindings", "media_id"},
	{"creator", "creator_bindings", "creator_id"},
}

func orphanEntityByType(entityType string) (orphanEntity, bool) {
	for _, entity := range orphanEntities {
		if entity.entityType == entityType {
			return entity, true
		}
	}
	return orphanEntity{}, false
}

// orphanEntityForBinding 从 Binding ID 的类型前缀推断它属于哪张 Binding 表，避免调用方额外
// 传入实体类型并保证二者一致。
func orphanEntityForBinding(bindingID string) (orphanEntity, error) {
	for kind, entity := range map[domain.IDKind]orphanEntity{
		domain.IDWorkBinding:    orphanEntities[0],
		domain.IDMediaBinding:   orphanEntities[1],
		domain.IDCreatorBinding: orphanEntities[2],
	} {
		if _, err := domain.ParseID(kind, bindingID); err == nil {
			return entity, nil
		}
	}
	return orphanEntity{}, fault.New(fault.CodeNotFound, false, nil)
}

// ListOrphanCandidates 跨三张 Binding 表列出处于 orphan_candidate 状态的记录，按 binding_id
// keyset 分页。label 由对应 Canonical 表联表读取，不含绝对路径或 metadata 原文。
func (r *Resources) ListOrphanCandidates(ctx context.Context, filter OrphanCandidateFilter, cursor string, limit int) (OrphanCandidatePage, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if filter.SourceID != "" {
		if _, err := domain.ParseID(domain.IDSource, filter.SourceID); err != nil {
			return OrphanCandidatePage{}, fault.WithField(fault.CodeValidation, "sourceId", nil)
		}
	}
	after := ""
	if cursor != "" {
		decoded, err := decodeOrphanCursor(cursor)
		if err != nil {
			return OrphanCandidatePage{}, err
		}
		after = decoded
	}
	targets := orphanEntities
	if filter.EntityType != "" {
		entity, ok := orphanEntityByType(filter.EntityType)
		if !ok {
			return OrphanCandidatePage{}, fault.WithField(fault.CodeValidation, "entityType", nil)
		}
		targets = []orphanEntity{entity}
	}
	var merged []OrphanCandidate
	for _, entity := range targets {
		items, err := r.orphanCandidatesFor(ctx, entity, filter.SourceID, after, limit+1)
		if err != nil {
			return OrphanCandidatePage{}, err
		}
		merged = append(merged, items...)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].BindingID < merged[j].BindingID })
	page := OrphanCandidatePage{Items: merged}
	if len(merged) > limit {
		page.Items = merged[:limit]
		page.NextCursor = encodeOrphanCursor(merged[limit-1].BindingID)
	}
	if page.Items == nil {
		page.Items = []OrphanCandidate{}
	}
	return page, nil
}

func (r *Resources) orphanCandidatesFor(ctx context.Context, entity orphanEntity, sourceID, after string, limit int) ([]OrphanCandidate, error) {
	labelColumn, join := orphanLabelColumn(entity)
	conditions := []string{"b.status='orphan_candidate'"}
	args := []any{}
	if sourceID != "" {
		conditions = append(conditions, "b.source_id=?")
		args = append(args, sourceID)
	}
	if after != "" {
		conditions = append(conditions, "b.binding_id>?")
		args = append(args, after)
	}
	// entity.table、canonicalCol、join 均取自封闭字面量集合，可安全拼接。
	query := "SELECT b.binding_id, b.source_id, b.source_key, b." + entity.canonicalCol +
		", b.missed_scans, b.retention_scans_override, b.last_seen_generation, b.created_at, b.updated_at, " +
		labelColumn + " FROM " + entity.table + " b " + join +
		" WHERE " + strings.Join(conditions, " AND ") + " ORDER BY b.binding_id LIMIT ?"
	args = append(args, limit)
	rows, err := r.control.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	var result []OrphanCandidate
	for rows.Next() {
		item := OrphanCandidate{EntityType: entity.entityType}
		var override sql.NullInt64
		var label sql.NullString
		var createdAt, updatedAt int64
		if err := rows.Scan(&item.BindingID, &item.SourceID, &item.SourceKey, &item.CanonicalID,
			&item.MissedScans, &override, &item.LastSeenGeneration, &createdAt, &updatedAt, &label); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		item.RetentionThreshold = defaultOrphanRetentionScans
		if override.Valid {
			item.RetentionThreshold = int(override.Int64)
		}
		item.CanonicalLabel = label.String
		item.CreatedAt = time.Unix(createdAt, 0).UTC()
		item.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		result = append(result, item)
	}
	return result, rows.Err()
}

func orphanLabelColumn(entity orphanEntity) (string, string) {
	switch entity.entityType {
	case "work":
		return "c.title", "JOIN canonical_works c ON c.work_id=b.work_id"
	case "creator":
		return "c.name", "JOIN canonical_creators c ON c.creator_id=b.creator_id"
	default:
		return "'occurrence ' || c.ordinal", "JOIN canonical_media c ON c.media_id=b.media_id"
	}
}

// DecideOrphanCandidate 对一个 orphan candidate 应用人工决策。四种决策都只改写 Binding 状态
// 与保留计数，绝不删除 Canonical、Overlay、收藏或进度：
//   - retain：保留派生关系，复位为 inactive 并清零缺失计数；
//   - extend：延长保留窗口（增加 retention_scans_override）并清零缺失计数；
//   - confirm_orphaned：确认长期孤立，转 orphaned，来源稳定键重现时仍复用原 Canonical 实体；
//   - unbind：解绑派生关系，转 manual_unbound，重现时创建新实体，可经 undo-unbind 恢复。
func (r *Resources) DecideOrphanCandidate(ctx context.Context, bindingID, decision string, extendScans int) (OrphanDecisionResult, error) {
	if decision != "retain" && decision != "extend" && decision != "confirm_orphaned" && decision != "unbind" {
		return OrphanDecisionResult{}, fault.WithField(fault.CodeValidation, "decision", nil)
	}
	if decision == "extend" && (extendScans < 1 || extendScans > 10000) {
		return OrphanDecisionResult{}, fault.WithField(fault.CodeValidation, "extendScans", nil)
	}
	entity, err := orphanEntityForBinding(bindingID)
	if err != nil {
		return OrphanDecisionResult{}, err
	}
	tx, err := r.control.BeginTx(ctx, nil)
	if err != nil {
		return OrphanDecisionResult{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	var status, canonicalID string
	var override sql.NullInt64
	err = tx.QueryRowContext(ctx, "SELECT status, "+entity.canonicalCol+", retention_scans_override FROM "+
		entity.table+" WHERE binding_id=?", bindingID).Scan(&status, &canonicalID, &override)
	if errors.Is(err, sql.ErrNoRows) {
		return OrphanDecisionResult{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return OrphanDecisionResult{}, fault.New(fault.CodeInternal, true, err)
	}
	if status != "orphan_candidate" {
		return OrphanDecisionResult{}, fault.New(fault.CodeConflict, false, nil)
	}
	now := r.clock.Now().UTC().Unix()
	newStatus := ""
	var update string
	var args []any
	switch decision {
	case "retain":
		newStatus, update = "inactive", "UPDATE "+entity.table+" SET status='inactive', missed_scans=0, updated_at=? WHERE binding_id=?"
		args = []any{now, bindingID}
	case "extend":
		base := int64(defaultOrphanRetentionScans)
		if override.Valid {
			base = override.Int64
		}
		newStatus = "inactive"
		update = "UPDATE " + entity.table + " SET status='inactive', missed_scans=0, retention_scans_override=?, updated_at=? WHERE binding_id=?"
		args = []any{base + int64(extendScans), now, bindingID}
	case "confirm_orphaned":
		newStatus, update = "orphaned", "UPDATE "+entity.table+" SET status='orphaned', updated_at=? WHERE binding_id=?"
		args = []any{now, bindingID}
	case "unbind":
		newStatus, update = "manual_unbound", "UPDATE "+entity.table+" SET status='manual_unbound', updated_at=? WHERE binding_id=?"
		args = []any{now, bindingID}
	}
	if _, err := tx.ExecContext(ctx, update, args...); err != nil {
		return OrphanDecisionResult{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := tx.Commit(); err != nil {
		return OrphanDecisionResult{}, fault.New(fault.CodeInternal, true, err)
	}
	return OrphanDecisionResult{BindingID: bindingID, EntityType: entity.entityType,
		Decision: decision, NewStatus: newStatus, CanonicalID: canonicalID}, nil
}

func encodeOrphanCursor(bindingID string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(bindingID))
}

func decodeOrphanCursor(cursor string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", fault.New(fault.CodeCursorInvalid, false, nil)
	}
	return string(raw), nil
}
