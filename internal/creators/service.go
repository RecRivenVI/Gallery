// Package creators 实现 CanonicalCreator 的人工合并与撤销。合并是 control.db 中不可
// 重建的权威事实：canonical_creators.merged_into 记录有效创作者，creator_merges 与
// creator_merge_members 记录每次操作及其成员，便于精确撤销。creator_bindings 与
// work_creators 永不被合并改写，撤销只需清除 merged_into，因此原关系可靠恢复。
// 合并对查询投影的影响在投影阶段由 catalog.ApplyCreatorMerges 应用，复用既有的
// overlay_projection 持久 Job 与 watermark/supersede/重启恢复语义。
package creators

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/ports"
)

const maxMergeMembers = 200

type Creator struct {
	ID          string
	Name        string
	MergedInto  string
	EffectiveID string
	SourceCount int
	CreatedAt   time.Time
}

type SourceBinding struct {
	BindingID  string
	SourceID   string
	ProviderID string
	ExternalID string
	SourceKey  string
	Status     string
}

type MergeRecord struct {
	ID          string
	TargetID    string
	AbsorbedIDs []string
	Status      string
	CreatedBy   string
	CreatedAt   time.Time
	UndoneAt    *time.Time
}

type MergeResult struct {
	Merge           MergeRecord
	ProjectionJobID string
	StartJob        bool
}

// ProjectionStarter 抽象 overlay 投影 Job 的启动，避免 creators 反向依赖 overlay 包。
type ProjectionStarter interface {
	Start(jobID string)
}

type Service struct {
	context context.Context
	control *sql.DB
	jobs    *jobs.Store
	catalog *catalog.Store
	clock   ports.Clock
	ids     ports.IDGenerator
	starter ProjectionStarter
}

func New(ctx context.Context, control *sql.DB, jobStore *jobs.Store, catalogStore *catalog.Store, clock ports.Clock, ids ports.IDGenerator, starter ProjectionStarter) (*Service, error) {
	if ctx == nil || control == nil || jobStore == nil || catalogStore == nil || clock == nil || ids == nil {
		return nil, errors.New("Creators Service 缺少依赖")
	}
	return &Service{context: ctx, control: control, jobs: jobStore, catalog: catalogStore, clock: clock, ids: ids, starter: starter}, nil
}

func (s *Service) List(ctx context.Context) ([]Creator, error) {
	rows, err := s.control.QueryContext(ctx, `SELECT c.creator_id, c.name, c.merged_into, c.created_at,
(SELECT count(*) FROM creator_bindings b WHERE b.creator_id=c.creator_id AND b.status='active')
FROM canonical_creators c ORDER BY c.name, c.creator_id`)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	var result []Creator
	parents := make(map[string]string)
	for rows.Next() {
		var creator Creator
		var mergedInto sql.NullString
		var createdAt int64
		if err := rows.Scan(&creator.ID, &creator.Name, &mergedInto, &createdAt, &creator.SourceCount); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		creator.MergedInto = mergedInto.String
		creator.CreatedAt = time.Unix(createdAt, 0).UTC()
		if mergedInto.Valid {
			parents[creator.ID] = mergedInto.String
		}
		result = append(result, creator)
	}
	if err := rows.Err(); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	for index := range result {
		result[index].EffectiveID = resolveRoot(parents, result[index].ID)
	}
	return result, nil
}

// Get 返回单个 CanonicalCreator 及其来源 Binding 证据，供用户在合并前核对。
func (s *Service) Get(ctx context.Context, creatorID string) (Creator, []SourceBinding, error) {
	if _, err := domain.ParseID(domain.IDCanonicalCreator, creatorID); err != nil {
		return Creator{}, nil, fault.New(fault.CodeNotFound, false, nil)
	}
	var creator Creator
	var mergedInto sql.NullString
	var createdAt int64
	err := s.control.QueryRowContext(ctx, `SELECT creator_id, name, merged_into, created_at
FROM canonical_creators WHERE creator_id=?`, creatorID).Scan(&creator.ID, &creator.Name, &mergedInto, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Creator{}, nil, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return Creator{}, nil, fault.New(fault.CodeInternal, true, err)
	}
	creator.MergedInto = mergedInto.String
	creator.CreatedAt = time.Unix(createdAt, 0).UTC()
	creator.EffectiveID = creator.ID
	if mergedInto.Valid {
		parents, err := s.parentMap(ctx)
		if err != nil {
			return Creator{}, nil, err
		}
		creator.EffectiveID = resolveRoot(parents, creator.ID)
	}
	bindings, err := s.sourceBindings(ctx, creatorID)
	if err != nil {
		return Creator{}, nil, err
	}
	creator.SourceCount = 0
	for _, binding := range bindings {
		if binding.Status == "active" {
			creator.SourceCount++
		}
	}
	return creator, bindings, nil
}

func (s *Service) sourceBindings(ctx context.Context, creatorID string) ([]SourceBinding, error) {
	rows, err := s.control.QueryContext(ctx, `SELECT binding_id, source_id, provider_id, external_id, source_key, status
FROM creator_bindings WHERE creator_id=? ORDER BY source_id, source_key`, creatorID)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	result := make([]SourceBinding, 0)
	for rows.Next() {
		var binding SourceBinding
		if err := rows.Scan(&binding.BindingID, &binding.SourceID, &binding.ProviderID, &binding.ExternalID, &binding.SourceKey, &binding.Status); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		result = append(result, binding)
	}
	return result, rows.Err()
}

func (s *Service) ListMerges(ctx context.Context) ([]MergeRecord, error) {
	rows, err := s.control.QueryContext(ctx, `SELECT merge_id, target_creator_id, created_by, status, created_at, undone_at
FROM creator_merges ORDER BY created_at DESC, merge_id DESC`)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	var result []MergeRecord
	for rows.Next() {
		record, err := scanMerge(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	for index := range result {
		members, err := s.mergeMembers(ctx, result[index].ID)
		if err != nil {
			return nil, err
		}
		result[index].AbsorbedIDs = members
	}
	return result, nil
}

// Merge 把 absorbedIDs 全部并入 targetID：为每个被并者设置 merged_into=target，
// 记录 creator_merges 与成员，并在存在活动 publication 时排队 overlay 投影 Job 让
// 查询结果反映合并。目标与被并者必须都处于 live（merged_into 为空）。
func (s *Service) Merge(ctx context.Context, createdBy, targetID string, absorbedIDs []string) (MergeResult, error) {
	if strings.TrimSpace(createdBy) == "" {
		return MergeResult{}, fault.New(fault.CodeValidation, false, nil)
	}
	if _, err := domain.ParseID(domain.IDCanonicalCreator, targetID); err != nil {
		return MergeResult{}, fault.WithField(fault.CodeValidation, "targetCreatorId", nil)
	}
	if len(absorbedIDs) == 0 || len(absorbedIDs) > maxMergeMembers {
		return MergeResult{}, fault.WithField(fault.CodeValidation, "absorbedCreatorIds", nil)
	}
	seen := map[string]struct{}{targetID: {}}
	for _, id := range absorbedIDs {
		if _, err := domain.ParseID(domain.IDCanonicalCreator, id); err != nil {
			return MergeResult{}, fault.WithField(fault.CodeValidation, "absorbedCreatorIds", nil)
		}
		if _, duplicate := seen[id]; duplicate {
			return MergeResult{}, fault.WithField(fault.CodeValidation, "absorbedCreatorIds", nil)
		}
		seen[id] = struct{}{}
	}
	current, currentErr := s.catalog.Current(ctx)
	if currentErr != nil && !isCode(currentErr, fault.CodeNotFound) {
		return MergeResult{}, currentErr
	}
	tx, err := s.control.BeginTx(ctx, nil)
	if err != nil {
		return MergeResult{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	if err := requireLiveCreator(ctx, tx, targetID); err != nil {
		return MergeResult{}, err
	}
	for _, id := range absorbedIDs {
		if err := requireLiveCreator(ctx, tx, id); err != nil {
			return MergeResult{}, err
		}
	}
	now := s.clock.Now().UTC()
	var watermark int64
	if err := tx.QueryRowContext(ctx, `UPDATE gallery_control_sequence SET watermark=watermark+1
WHERE singleton=1 RETURNING watermark`).Scan(&watermark); err != nil {
		return MergeResult{}, fault.New(fault.CodeInternal, true, err)
	}
	for _, id := range absorbedIDs {
		result, err := tx.ExecContext(ctx, `UPDATE canonical_creators SET merged_into=?
WHERE creator_id=? AND merged_into IS NULL`, targetID, id)
		if err != nil {
			return MergeResult{}, fault.New(fault.CodeInternal, true, err)
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return MergeResult{}, fault.New(fault.CodeConflict, false, nil)
		}
	}
	mergeID, err := s.ids.New(domain.IDCreatorMerge)
	if err != nil {
		return MergeResult{}, fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO creator_merges
(merge_id, target_creator_id, created_by, status, created_at) VALUES (?, ?, ?, 'applied', ?)`,
		mergeID.String(), targetID, createdBy, now.Unix()); err != nil {
		return MergeResult{}, fault.New(fault.CodeInternal, true, err)
	}
	for _, id := range absorbedIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO creator_merge_members
(merge_id, absorbed_creator_id) VALUES (?, ?)`, mergeID.String(), id); err != nil {
			return MergeResult{}, fault.New(fault.CodeInternal, true, err)
		}
	}
	projectionJobID, startJob, err := s.enqueueProjection(ctx, tx, createdBy, current, currentErr, watermark)
	if err != nil {
		return MergeResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return MergeResult{}, fault.New(fault.CodeInternal, true, err)
	}
	s.maybeStart(startJob, projectionJobID)
	return MergeResult{
		Merge: MergeRecord{ID: mergeID.String(), TargetID: targetID, AbsorbedIDs: append([]string(nil), absorbedIDs...),
			Status: "applied", CreatedBy: createdBy, CreatedAt: now},
		ProjectionJobID: projectionJobID, StartJob: startJob,
	}, nil
}

// Undo 撤销一次已生效合并：清除各成员的 merged_into，标记 undone，并排队投影 Job
// 恢复原创作者。撤销不改写任何 Binding 或 work_creators，因此原关系可靠还原。
func (s *Service) Undo(ctx context.Context, createdBy, mergeID string) (MergeResult, error) {
	if strings.TrimSpace(createdBy) == "" {
		return MergeResult{}, fault.New(fault.CodeValidation, false, nil)
	}
	if _, err := domain.ParseID(domain.IDCreatorMerge, mergeID); err != nil {
		return MergeResult{}, fault.New(fault.CodeNotFound, false, nil)
	}
	current, currentErr := s.catalog.Current(ctx)
	if currentErr != nil && !isCode(currentErr, fault.CodeNotFound) {
		return MergeResult{}, currentErr
	}
	tx, err := s.control.BeginTx(ctx, nil)
	if err != nil {
		return MergeResult{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	var targetID, status string
	var createdAt int64
	err = tx.QueryRowContext(ctx, `SELECT target_creator_id, status, created_at FROM creator_merges
WHERE merge_id=?`, mergeID).Scan(&targetID, &status, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return MergeResult{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return MergeResult{}, fault.New(fault.CodeInternal, true, err)
	}
	if status != "applied" {
		return MergeResult{}, fault.New(fault.CodeConflict, false, nil)
	}
	members, err := mergeMembersTx(ctx, tx, mergeID)
	if err != nil {
		return MergeResult{}, err
	}
	now := s.clock.Now().UTC()
	var watermark int64
	if err := tx.QueryRowContext(ctx, `UPDATE gallery_control_sequence SET watermark=watermark+1
WHERE singleton=1 RETURNING watermark`).Scan(&watermark); err != nil {
		return MergeResult{}, fault.New(fault.CodeInternal, true, err)
	}
	for _, id := range members {
		result, err := tx.ExecContext(ctx, `UPDATE canonical_creators SET merged_into=NULL
WHERE creator_id=? AND merged_into=?`, id, targetID)
		if err != nil {
			return MergeResult{}, fault.New(fault.CodeInternal, true, err)
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return MergeResult{}, fault.New(fault.CodeConflict, false, nil)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE creator_merges SET status='undone', undone_at=?
WHERE merge_id=? AND status='applied'`, now.Unix(), mergeID); err != nil {
		return MergeResult{}, fault.New(fault.CodeInternal, true, err)
	}
	projectionJobID, startJob, err := s.enqueueProjection(ctx, tx, createdBy, current, currentErr, watermark)
	if err != nil {
		return MergeResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return MergeResult{}, fault.New(fault.CodeInternal, true, err)
	}
	s.maybeStart(startJob, projectionJobID)
	undoneAt := now
	return MergeResult{
		Merge: MergeRecord{ID: mergeID, TargetID: targetID, AbsorbedIDs: members, Status: "undone",
			CreatedBy: createdBy, CreatedAt: time.Unix(createdAt, 0).UTC(), UndoneAt: &undoneAt},
		ProjectionJobID: projectionJobID, StartJob: startJob,
	}, nil
}

func (s *Service) enqueueProjection(ctx context.Context, tx *sql.Tx, createdBy string, current catalog.Publication, currentErr error, watermark int64) (string, bool, error) {
	if currentErr != nil {
		return "", false, nil
	}
	enqueued, err := s.jobs.EnqueueOverlayProjectionTx(ctx, tx, createdBy, current.CatalogRevisionID, current.ID, watermark)
	if err != nil {
		return "", false, err
	}
	return enqueued.JobID, enqueued.Created, nil
}

func (s *Service) maybeStart(startJob bool, jobID string) {
	if startJob && jobID != "" && s.starter != nil {
		s.starter.Start(jobID)
	}
}

func (s *Service) parentMap(ctx context.Context) (map[string]string, error) {
	rows, err := s.control.QueryContext(ctx, `SELECT creator_id, merged_into FROM canonical_creators
WHERE merged_into IS NOT NULL`)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	parents := make(map[string]string)
	for rows.Next() {
		var id, parent string
		if err := rows.Scan(&id, &parent); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		parents[id] = parent
	}
	return parents, rows.Err()
}

func (s *Service) mergeMembers(ctx context.Context, mergeID string) ([]string, error) {
	rows, err := s.control.QueryContext(ctx, `SELECT absorbed_creator_id FROM creator_merge_members
WHERE merge_id=? ORDER BY absorbed_creator_id`, mergeID)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	return scanStrings(rows)
}

func mergeMembersTx(ctx context.Context, tx *sql.Tx, mergeID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT absorbed_creator_id FROM creator_merge_members
WHERE merge_id=? ORDER BY absorbed_creator_id`, mergeID)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	return scanStrings(rows)
}

func requireLiveCreator(ctx context.Context, tx *sql.Tx, creatorID string) error {
	var mergedInto sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT merged_into FROM canonical_creators WHERE creator_id=?`, creatorID).Scan(&mergedInto)
	if errors.Is(err, sql.ErrNoRows) {
		return fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if mergedInto.Valid {
		return fault.New(fault.CodeConflict, false, nil)
	}
	return nil
}

func scanMerge(rows *sql.Rows) (MergeRecord, error) {
	var record MergeRecord
	var createdAt int64
	var undoneAt sql.NullInt64
	if err := rows.Scan(&record.ID, &record.TargetID, &record.CreatedBy, &record.Status, &createdAt, &undoneAt); err != nil {
		return MergeRecord{}, fault.New(fault.CodeInternal, true, err)
	}
	record.CreatedAt = time.Unix(createdAt, 0).UTC()
	if undoneAt.Valid {
		undone := time.Unix(undoneAt.Int64, 0).UTC()
		record.UndoneAt = &undone
	}
	return record, nil
}

func scanStrings(rows *sql.Rows) ([]string, error) {
	result := make([]string, 0)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func isCode(err error, code fault.Code) bool {
	var structured *fault.Error
	return errors.As(err, &structured) && structured.Code == code
}
