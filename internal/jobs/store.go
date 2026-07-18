package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/ports"
)

type Status string

const (
	StatusQueued      Status = "queued"
	StatusRunning     Status = "running"
	StatusPublishing  Status = "publishing"
	StatusCompleted   Status = "completed"
	StatusFailed      Status = "failed"
	StatusCancelled   Status = "cancelled"
	StatusNeedsRepair Status = "needs_repair"
)

type Job struct {
	ID                       string
	Type                     string
	SourceID                 string
	CreatedBy                string
	Status                   Status
	Stage                    string
	ProgressCurrent          int64
	ProgressTotal            int64
	ProgressSequence         uint64
	IssueCode                string
	PublicationID            string
	RetryOf                  string
	Attempt                  int
	CreatedAt                time.Time
	StartedAt                *time.Time
	FinishedAt               *time.Time
	UpdatedAt                time.Time
	TargetWatermark          int64
	TargetCatalogID          string
	BasePublicationID        string
	RuleSemanticHash         string
	RuleParameters           []byte
	RuleParametersHash       string
	RuleIRHash               string
	CompilerVersion          string
	CELProfileVersion        string
	ExtensionRegistryVersion string
}

// RuleExecutionSnapshot 是扫描 Job 入队时冻结的规则执行输入。运行期间不得重新读取
// SourceRuleBinding 作为事实来源，否则用户修改 Binding 会让同一个 Job 跨代执行。
type RuleExecutionSnapshot struct {
	SemanticHash             string
	Parameters               []byte
	ParametersHash           string
	RuleIRHash               string
	CompilerVersion          string
	CELProfileVersion        string
	ExtensionRegistryVersion string
}

type OverlayEnqueueResult struct {
	JobID   string
	Created bool
}

type Store struct {
	db    *sql.DB
	clock ports.Clock
	ids   ports.IDGenerator
}

func NewStore(db *sql.DB, clock ports.Clock, ids ports.IDGenerator) (*Store, error) {
	if db == nil || clock == nil || ids == nil {
		return nil, fmt.Errorf("Job Store 缺少依赖")
	}
	return &Store{db: db, clock: clock, ids: ids}, nil
}

func (s *Store) CreateScan(ctx context.Context, sourceID, createdBy, retryOf string) (Job, error) {
	return s.CreateScanWithRuleSnapshot(ctx, sourceID, createdBy, retryOf, nil)
}

func (s *Store) CreateScanWithRuleSnapshot(ctx context.Context, sourceID, createdBy, retryOf string, snapshot *RuleExecutionSnapshot) (Job, error) {
	if _, err := domain.ParseID(domain.IDSource, sourceID); err != nil || strings.TrimSpace(createdBy) == "" {
		return Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	if snapshot != nil && (snapshot.SemanticHash == "" || snapshot.RuleIRHash == "" || len(snapshot.Parameters) == 0) {
		return Job{}, fault.WithField(fault.CodeValidation, "ruleSnapshot", nil)
	}
	attempt := 1
	if retryOf != "" {
		previous, err := s.Get(ctx, retryOf)
		if err != nil {
			return Job{}, err
		}
		if previous.SourceID != sourceID || (previous.Status != StatusFailed && previous.Status != StatusCancelled && previous.Status != StatusNeedsRepair) {
			return Job{}, fault.New(fault.CodeJobStateConflict, false, nil)
		}
		attempt = previous.Attempt + 1
	}
	id, err := s.ids.New(domain.IDJob)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	now := s.clock.Now().UTC()
	job := Job{
		ID: id.String(), Type: "scan", SourceID: sourceID, CreatedBy: createdBy,
		Status: StatusQueued, Stage: "queued", ProgressSequence: 1, RetryOf: retryOf,
		Attempt: attempt, CreatedAt: now, UpdatedAt: now,
	}
	if snapshot != nil {
		job.RuleSemanticHash = snapshot.SemanticHash
		job.RuleParameters = append([]byte(nil), snapshot.Parameters...)
		job.RuleParametersHash = snapshot.ParametersHash
		job.RuleIRHash = snapshot.RuleIRHash
		job.CompilerVersion = snapshot.CompilerVersion
		job.CELProfileVersion = snapshot.CELProfileVersion
		job.ExtensionRegistryVersion = snapshot.ExtensionRegistryVersion
	}
	var retry any
	if retryOf != "" {
		retry = retryOf
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO jobs
(job_id, job_type, source_id, created_by, status, stage, progress_sequence, retry_of, attempt, created_at, updated_at,
 rule_semantic_hash, rule_parameters_json, rule_parameters_hash, rule_ir_hash, compiler_version, cel_profile_version, extension_registry_version)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, job.ID, job.Type, job.SourceID, job.CreatedBy,
		job.Status, job.Stage, job.ProgressSequence, retry, job.Attempt, now.Unix(), now.Unix(),
		nullableString(job.RuleSemanticHash), nullableBytes(job.RuleParameters), nullableString(job.RuleParametersHash),
		nullableString(job.RuleIRHash), nullableString(job.CompilerVersion), nullableString(job.CELProfileVersion), nullableString(job.ExtensionRegistryVersion))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Job{}, fault.New(fault.CodeScanAlreadyRunning, true, nil)
		}
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	return job, nil
}

// maintenanceTypes 是不绑定 source_id、复用 jobs 状态机的维护类 Job 类型集合。它们各自持有
// 数据库层的单活跃约束，避免并发备份或并发恢复相互破坏一致性。
var maintenanceTypes = map[string]struct{}{"control_backup": {}, "control_restore": {}}

// CreateMaintenance 入库一个维护类 Job（control_backup / control_restore）。source_id 为 NULL；
// 若同类别已有活跃 Job，数据库单活跃约束触发 JOB_STATE_CONFLICT。
func (s *Store) CreateMaintenance(ctx context.Context, jobType, createdBy string) (Job, error) {
	if _, ok := maintenanceTypes[jobType]; !ok || strings.TrimSpace(createdBy) == "" {
		return Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	id, err := s.ids.New(domain.IDJob)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	now := s.clock.Now().UTC()
	job := Job{
		ID: id.String(), Type: jobType, CreatedBy: createdBy,
		Status: StatusQueued, Stage: "queued", ProgressSequence: 1, Attempt: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO jobs (job_id, job_type, created_by, status, stage, progress_sequence, attempt, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, job.ID, job.Type, job.CreatedBy,
		job.Status, job.Stage, job.ProgressSequence, job.Attempt, now.Unix(), now.Unix())
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Job{}, fault.New(fault.CodeJobStateConflict, true, nil)
		}
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	return job, nil
}

// CompleteMaintenance 把维护类 Job 从 running 收敛为 completed。维护类 Job 不产生
// query publication，因此不写 publication_id。
func (s *Store) CompleteMaintenance(ctx context.Context, id string) (Job, error) {
	now := s.clock.Now().UTC()
	return s.transition(ctx, id, StatusRunning, StatusCompleted, "completed", `finished_at = ?,`, []any{now.Unix()}, now)
}

// EnqueueOverlayProjectionTx 与 Overlay fact 使用同一个 control.db 事务：同一 Catalog
// 上的连续写入合并为更高 watermark，Catalog 已切换时显式 supersede 旧请求。
func (s *Store) EnqueueOverlayProjectionTx(ctx context.Context, tx *sql.Tx, createdBy, catalogRevisionID, basePublicationID string, targetWatermark int64) (OverlayEnqueueResult, error) {
	if tx == nil || strings.TrimSpace(createdBy) == "" || catalogRevisionID == "" || basePublicationID == "" || targetWatermark < 1 {
		return OverlayEnqueueResult{}, fault.New(fault.CodeValidation, false, nil)
	}
	var activeID, activeCatalog string
	err := tx.QueryRowContext(ctx, `SELECT job_id, target_catalog_revision_id FROM jobs
WHERE job_type='overlay_projection' AND status IN ('queued', 'running', 'publishing')
ORDER BY created_at, job_id LIMIT 1`).Scan(&activeID, &activeCatalog)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return OverlayEnqueueResult{}, fault.New(fault.CodeInternal, true, err)
	}
	now := s.clock.Now().UTC()
	if err == nil && activeCatalog == catalogRevisionID {
		if _, err := tx.ExecContext(ctx, `UPDATE jobs SET target_watermark=max(target_watermark, ?),
progress_sequence=progress_sequence+1, updated_at=? WHERE job_id=?`, targetWatermark, now.Unix(), activeID); err != nil {
			return OverlayEnqueueResult{}, fault.New(fault.CodeInternal, true, err)
		}
		return OverlayEnqueueResult{JobID: activeID}, nil
	}
	if err == nil {
		if _, err := tx.ExecContext(ctx, `UPDATE jobs SET status='cancelled', stage='superseded',
issue_code='OVERLAY_SUPERSEDED', finished_at=?, progress_sequence=progress_sequence+1, updated_at=?
WHERE job_id=?`, now.Unix(), now.Unix(), activeID); err != nil {
			return OverlayEnqueueResult{}, fault.New(fault.CodeInternal, true, err)
		}
	}
	id, err := s.ids.New(domain.IDJob)
	if err != nil {
		return OverlayEnqueueResult{}, fault.New(fault.CodeInternal, true, err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO jobs
(job_id, job_type, source_id, created_by, status, stage, progress_sequence, attempt,
 created_at, updated_at, target_watermark, target_catalog_revision_id, base_query_publication_id)
VALUES (?, 'overlay_projection', NULL, ?, 'queued', 'queued', 1, 1, ?, ?, ?, ?, ?)`,
		id.String(), createdBy, now.Unix(), now.Unix(), targetWatermark, catalogRevisionID, basePublicationID)
	if err != nil {
		return OverlayEnqueueResult{}, fault.New(fault.CodeInternal, true, err)
	}
	return OverlayEnqueueResult{JobID: id.String(), Created: true}, nil
}

func (s *Store) Get(ctx context.Context, id string) (Job, error) {
	if _, err := domain.ParseID(domain.IDJob, id); err != nil {
		return Job{}, fault.New(fault.CodeNotFound, false, nil)
	}
	var job Job
	var sourceID, issueCode, publicationID, retryOf, targetCatalogID, basePublicationID sql.NullString
	var ruleSemanticHash, ruleParameters, ruleParametersHash, ruleIRHash, compilerVersion, celProfileVersion, extensionRegistryVersion sql.NullString
	var startedAt, finishedAt, targetWatermark sql.NullInt64
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, `
SELECT job_id, job_type, source_id, created_by, status, stage,
       progress_current, progress_total, progress_sequence, issue_code, publication_id,
       retry_of, attempt, created_at, started_at, finished_at, updated_at,
       target_watermark, target_catalog_revision_id, base_query_publication_id,
       rule_semantic_hash, rule_parameters_json, rule_parameters_hash, rule_ir_hash, compiler_version,
       cel_profile_version, extension_registry_version
FROM jobs WHERE job_id = ?`, id).Scan(
		&job.ID, &job.Type, &sourceID, &job.CreatedBy, &job.Status, &job.Stage,
		&job.ProgressCurrent, &job.ProgressTotal, &job.ProgressSequence, &issueCode, &publicationID,
		&retryOf, &job.Attempt, &createdAt, &startedAt, &finishedAt, &updatedAt,
		&targetWatermark, &targetCatalogID, &basePublicationID, &ruleSemanticHash, &ruleParameters,
		&ruleParametersHash, &ruleIRHash, &compilerVersion, &celProfileVersion, &extensionRegistryVersion,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	job.SourceID, job.IssueCode, job.PublicationID, job.RetryOf = sourceID.String, issueCode.String, publicationID.String, retryOf.String
	job.TargetWatermark = targetWatermark.Int64
	job.TargetCatalogID, job.BasePublicationID = targetCatalogID.String, basePublicationID.String
	job.RuleSemanticHash, job.RuleParameters, job.RuleParametersHash = ruleSemanticHash.String, []byte(ruleParameters.String), ruleParametersHash.String
	job.RuleIRHash, job.CompilerVersion, job.CELProfileVersion, job.ExtensionRegistryVersion = ruleIRHash.String, compilerVersion.String, celProfileVersion.String, extensionRegistryVersion.String
	job.CreatedAt, job.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
	job.StartedAt = nullableTime(startedAt)
	job.FinishedAt = nullableTime(finishedAt)
	return job, nil
}

func (s *Store) Start(ctx context.Context, id string) (Job, error) {
	return s.StartStage(ctx, id, "discovering")
}

func (s *Store) StartStage(ctx context.Context, id, stage string) (Job, error) {
	if stage == "" {
		return Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	now := s.clock.Now().UTC()
	return s.transition(ctx, id, StatusQueued, StatusRunning, stage, `started_at = ?,`, []any{now.Unix()}, now)
}

func (s *Store) RequeueInterruptedOverlay(ctx context.Context, id string) (Job, error) {
	now := s.clock.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE jobs SET status='queued', stage='recovery_queued',
started_at=NULL, issue_code=NULL, progress_sequence=progress_sequence+1, updated_at=?
WHERE job_id=? AND job_type='overlay_projection' AND status IN ('running', 'publishing')`, now.Unix(), id)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := requireOne(result); err != nil {
		return Job{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) ResumeOverlayProjection(ctx context.Context, id string) (Job, error) {
	now := s.clock.Now().UTC()
	return s.transition(ctx, id, StatusPublishing, StatusRunning, "reprojecting", "", nil, now)
}

func (s *Store) RetargetOverlayProjection(ctx context.Context, id, catalogRevisionID, basePublicationID string) (Job, error) {
	if catalogRevisionID == "" || basePublicationID == "" {
		return Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	now := s.clock.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE jobs SET target_catalog_revision_id=?,
base_query_publication_id=?, stage='retargeting', progress_sequence=progress_sequence+1, updated_at=?
WHERE job_id=? AND job_type='overlay_projection' AND status='running'`,
		catalogRevisionID, basePublicationID, now.Unix(), id)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := requireOne(result); err != nil {
		return Job{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) CancelOverlayAsSuperseded(ctx context.Context, id string) (Job, error) {
	now := s.clock.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE jobs SET status='cancelled', stage='superseded',
issue_code='OVERLAY_SUPERSEDED', finished_at=?, progress_sequence=progress_sequence+1, updated_at=?
WHERE job_id=? AND job_type='overlay_projection' AND status IN ('queued', 'running', 'publishing')`,
		now.Unix(), now.Unix(), id)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := requireOne(result); err != nil {
		return Job{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) Progress(ctx context.Context, id, stage string, current, total int64) (Job, error) {
	if stage == "" || current < 0 || total < 0 || (total > 0 && current > total) {
		return Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	now := s.clock.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
UPDATE jobs SET stage = ?, progress_current = ?, progress_total = ?,
                progress_sequence = progress_sequence + 1, updated_at = ?
WHERE job_id = ? AND status = ?`, stage, current, total, now.Unix(), id, StatusRunning)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := requireOne(result); err != nil {
		return Job{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) BeginPublishing(ctx context.Context, id string) (Job, error) {
	now := s.clock.Now().UTC()
	return s.transition(ctx, id, StatusRunning, StatusPublishing, "publishing", "", nil, now)
}

func (s *Store) Complete(ctx context.Context, id, publicationID string) (Job, error) {
	if _, err := domain.ParseID(domain.IDQueryPublication, publicationID); err != nil {
		return Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	now := s.clock.Now().UTC()
	return s.transition(ctx, id, StatusPublishing, StatusCompleted, "completed", `publication_id = ?, finished_at = ?,`, []any{publicationID, now.Unix()}, now)
}

func (s *Store) RecoverCompleted(ctx context.Context, id, publicationID string) (Job, error) {
	if _, err := domain.ParseID(domain.IDQueryPublication, publicationID); err != nil {
		return Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	now := s.clock.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
UPDATE jobs SET status = ?, stage = 'completed', publication_id = ?, finished_at = ?,
                progress_sequence = progress_sequence + 1, updated_at = ?
WHERE job_id = ? AND status IN (?, ?)`, StatusCompleted, publicationID, now.Unix(), now.Unix(), id, StatusRunning, StatusPublishing)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := requireOne(result); err != nil {
		return Job{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) Fail(ctx context.Context, id, issueCode string) (Job, error) {
	if issueCode == "" {
		return Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	now := s.clock.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
UPDATE jobs SET status = ?, stage = 'failed', issue_code = ?, finished_at = ?,
                progress_sequence = progress_sequence + 1, updated_at = ?
WHERE job_id = ? AND status IN (?, ?, ?)`, StatusFailed, issueCode, now.Unix(), now.Unix(), id, StatusQueued, StatusRunning, StatusPublishing)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := requireOne(result); err != nil {
		return Job{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) MarkNeedsRepair(ctx context.Context, id, issueCode string) (Job, error) {
	if issueCode == "" {
		return Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	now := s.clock.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
UPDATE jobs SET status = ?, stage = 'needs_repair', issue_code = ?,
                progress_sequence = progress_sequence + 1, updated_at = ?
WHERE job_id = ? AND status = ?`, StatusNeedsRepair, issueCode, now.Unix(), id, StatusCompleted)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := requireOne(result); err != nil {
		return Job{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) Cancel(ctx context.Context, id string) (Job, error) {
	now := s.clock.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
UPDATE jobs SET status = ?, stage = 'cancelled', finished_at = ?,
                progress_sequence = progress_sequence + 1, updated_at = ?
WHERE job_id = ? AND status IN (?, ?)`, StatusCancelled, now.Unix(), now.Unix(), id, StatusQueued, StatusRunning)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := requireOne(result); err != nil {
		return Job{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) ListByStatuses(ctx context.Context, statuses ...Status) ([]Job, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(statuses))
	args := make([]any, len(statuses))
	for index, status := range statuses {
		placeholders[index], args[index] = "?", status
	}
	rows, err := s.db.QueryContext(ctx, "SELECT job_id FROM jobs WHERE status IN ("+strings.Join(placeholders, ",")+") ORDER BY created_at, job_id", args...)
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
	result := make([]Job, 0, len(ids))
	for _, id := range ids {
		job, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, job)
	}
	return result, nil
}

func (s *Store) transition(ctx context.Context, id string, from, to Status, stage, assignments string, values []any, now time.Time) (Job, error) {
	query := "UPDATE jobs SET status = ?, stage = ?, " + assignments + " progress_sequence = progress_sequence + 1, updated_at = ? WHERE job_id = ? AND status = ?"
	args := []any{to, stage}
	args = append(args, values...)
	args = append(args, now.Unix(), id, from)
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := requireOne(result); err != nil {
		return Job{}, err
	}
	return s.Get(ctx, id)
}

func requireOne(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if count != 1 {
		return fault.New(fault.CodeJobStateConflict, false, nil)
	}
	return nil
}

func nullableTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	result := time.Unix(value.Int64, 0).UTC()
	return &result
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return string(value)
}
