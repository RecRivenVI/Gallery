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
	StatusCancelling  Status = "cancelling"
	StatusSuperseded  Status = "superseded"
)

const (
	ResourceScan         = "scan"
	ResourceHash         = "hash"
	ResourceOverlay      = "overlay"
	ResourceDerived      = "derived"
	ResourceExternalTool = "external-tool"
	ResourceMaintenance  = "maintenance"
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
	ResourceClass            string
	TargetResource           string
	RequestJSON              []byte
	IdempotencyKey           string
	MaxRetries               int
	RetryPolicyJSON          []byte
	CancelRequested          bool
	CancelRequestedAt        *time.Time
	HeartbeatAt              *time.Time
	LeaseOwner               string
	LeaseExpiresAt           *time.Time
	ResultJSON               []byte
	FailureRetryable         bool
	ProgressPhase            string
	ProgressUnit             string
	ProgressMessage          string
	ProgressBytes            int64
	ProgressEntities         int64
	ProgressEstimated        bool
	LastErrorAt              *time.Time
}

type Attempt struct {
	ID               string
	JobID            string
	Attempt          int
	ResourceClass    string
	Status           string
	StartedAt        *time.Time
	HeartbeatAt      *time.Time
	FinishedAt       *time.Time
	LeaseOwner       string
	LeaseExpiresAt   *time.Time
	ErrorCode        string
	ErrorRetryable   bool
	ProgressSequence uint64
	ResultJSON       []byte
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type CreateOptions struct {
	ResourceClass   string
	TargetResource  string
	RequestJSON     []byte
	IdempotencyKey  string
	MaxRetries      int
	RetryPolicyJSON []byte
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
	return s.CreateScanWithOptions(ctx, sourceID, createdBy, retryOf, snapshot, CreateOptions{ResourceClass: ResourceScan})
}

func (s *Store) CreateScanWithOptions(ctx context.Context, sourceID, createdBy, retryOf string, snapshot *RuleExecutionSnapshot, options CreateOptions) (Job, error) {
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
		if previous.SourceID != sourceID || (previous.Status != StatusFailed && previous.Status != StatusCancelled && previous.Status != StatusSuperseded && previous.Status != StatusNeedsRepair) {
			return Job{}, fault.New(fault.CodeJobStateConflict, false, nil)
		}
		attempt = previous.Attempt + 1
	}
	if options.ResourceClass == "" {
		options.ResourceClass = ResourceScan
	}
	if options.MaxRetries < 0 {
		return Job{}, fault.WithField(fault.CodeValidation, "maxRetries", nil)
	}
	if existing, found, err := s.findByIdempotency(ctx, options.IdempotencyKey); err != nil {
		return Job{}, err
	} else if found {
		return existing, nil
	}
	id, err := s.ids.New(domain.IDJob)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	now := s.clock.Now().UTC()
	job := Job{
		ID: id.String(), Type: "scan", SourceID: sourceID, CreatedBy: createdBy,
		Status: StatusQueued, Stage: "queued", ProgressSequence: 1, RetryOf: retryOf,
		Attempt: attempt, CreatedAt: now, UpdatedAt: now, ResourceClass: options.ResourceClass,
		TargetResource: options.TargetResource, RequestJSON: defaultJSON(options.RequestJSON),
		IdempotencyKey: options.IdempotencyKey, MaxRetries: options.MaxRetries,
		RetryPolicyJSON: defaultJSON(options.RetryPolicyJSON), ProgressUnit: "items",
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
 rule_semantic_hash, rule_parameters_json, rule_parameters_hash, rule_ir_hash, compiler_version, cel_profile_version, extension_registry_version,
 resource_class, target_resource, request_json, idempotency_key, max_retries, retry_policy_json, progress_unit)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, job.ID, job.Type, job.SourceID, job.CreatedBy,
		job.Status, job.Stage, job.ProgressSequence, retry, job.Attempt, now.Unix(), now.Unix(),
		nullableString(job.RuleSemanticHash), nullableBytes(job.RuleParameters), nullableString(job.RuleParametersHash),
		nullableString(job.RuleIRHash), nullableString(job.CompilerVersion), nullableString(job.CELProfileVersion), nullableString(job.ExtensionRegistryVersion),
		job.ResourceClass, nullableString(job.TargetResource), string(job.RequestJSON), nullableString(job.IdempotencyKey),
		job.MaxRetries, nullableBytes(job.RetryPolicyJSON), job.ProgressUnit)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Job{}, fault.New(fault.CodeScanAlreadyRunning, true, nil)
		}
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	return job, nil
}

// CreateWithOptions 入库不绑定规则快照的通用持久 Job。Hash、Derived、外部工具和维护任务
// 使用同一状态机，业务输入只保存在 request_json，不把工作状态留在 goroutine 内存中。
func (s *Store) CreateWithOptions(ctx context.Context, jobType, sourceID, createdBy string, options CreateOptions) (Job, error) {
	if strings.TrimSpace(jobType) == "" || strings.TrimSpace(createdBy) == "" {
		return Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	if sourceID != "" {
		if _, err := domain.ParseID(domain.IDSource, sourceID); err != nil {
			return Job{}, fault.New(fault.CodeValidation, false, nil)
		}
	}
	if options.ResourceClass == "" {
		options.ResourceClass = resourceClassForType(jobType)
	}
	if options.MaxRetries < 0 {
		return Job{}, fault.WithField(fault.CodeValidation, "maxRetries", nil)
	}
	if existing, found, err := s.findByIdempotency(ctx, options.IdempotencyKey); err != nil {
		return Job{}, err
	} else if found {
		return existing, nil
	}
	id, err := s.ids.New(domain.IDJob)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	now := s.clock.Now().UTC()
	job := Job{ID: id.String(), Type: jobType, SourceID: sourceID, CreatedBy: createdBy,
		Status: StatusQueued, Stage: "queued", ProgressSequence: 1, Attempt: 1,
		CreatedAt: now, UpdatedAt: now, ResourceClass: options.ResourceClass,
		TargetResource: options.TargetResource, RequestJSON: defaultJSON(options.RequestJSON),
		IdempotencyKey: options.IdempotencyKey, MaxRetries: options.MaxRetries,
		RetryPolicyJSON: defaultJSON(options.RetryPolicyJSON), ProgressUnit: "items"}
	_, err = s.db.ExecContext(ctx, `INSERT INTO jobs
(job_id, job_type, source_id, created_by, status, stage, progress_sequence, attempt, created_at, updated_at,
 resource_class, target_resource, request_json, idempotency_key, max_retries, retry_policy_json, progress_unit)
VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, job.ID, job.Type, job.SourceID, job.CreatedBy,
		job.Status, job.Stage, job.ProgressSequence, job.Attempt, now.Unix(), now.Unix(), job.ResourceClass,
		nullableString(job.TargetResource), string(job.RequestJSON), nullableString(job.IdempotencyKey), job.MaxRetries,
		string(job.RetryPolicyJSON), job.ProgressUnit)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			if existing, found, lookupErr := s.findByIdempotency(ctx, options.IdempotencyKey); lookupErr == nil && found {
				return existing, nil
			}
			return Job{}, fault.New(fault.CodeJobStateConflict, true, nil)
		}
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	return job, nil
}

func resourceClassForType(jobType string) string {
	switch jobType {
	case "scan":
		return ResourceScan
	case "hash":
		return ResourceHash
	case "overlay_projection":
		return ResourceOverlay
	case "derived":
		return ResourceDerived
	case "external_tool":
		return ResourceExternalTool
	default:
		return ResourceMaintenance
	}
}

func (s *Store) findByIdempotency(ctx context.Context, key string) (Job, bool, error) {
	if strings.TrimSpace(key) == "" {
		return Job{}, false, nil
	}
	var id string
	err := s.db.QueryRowContext(ctx, "SELECT job_id FROM jobs WHERE idempotency_key=?", key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, fault.New(fault.CodeInternal, true, err)
	}
	job, err := s.Get(ctx, id)
	return job, err == nil, err
}

// maintenanceTypes 是不绑定 source_id、复用 jobs 状态机的维护类 Job 类型集合。它们各自持有
// 数据库层的单活跃约束，避免并发备份或并发恢复相互破坏一致性。
var maintenanceTypes = map[string]struct{}{
	"control_backup": {}, "control_restore": {}, "catalog_gc": {}, "catalog_checkpoint": {},
	"catalog_vacuum": {}, "derived_gc": {},
}

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
		CreatedAt: now, UpdatedAt: now, ResourceClass: ResourceMaintenance, ProgressUnit: "items",
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO jobs (job_id, job_type, created_by, status, stage, progress_sequence, attempt, created_at, updated_at, resource_class, progress_unit)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, job.ID, job.Type, job.CreatedBy,
		job.Status, job.Stage, job.ProgressSequence, job.Attempt, now.Unix(), now.Unix(), job.ResourceClass, job.ProgressUnit)
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
	job, err := s.transition(ctx, id, StatusRunning, StatusCompleted, "completed", `finished_at = ?, heartbeat_at=NULL, lease_expires_at=NULL,`, []any{now.Unix()}, now)
	if err == nil {
		_, _ = s.db.ExecContext(ctx, `UPDATE job_attempts SET status='completed', finished_at=?, updated_at=?
WHERE job_id=? AND status='running'`, now.Unix(), now.Unix(), id)
	}
	return job, err
}

func (s *Store) SetRequest(ctx context.Context, id string, payload []byte) (Job, error) {
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE jobs SET request_json=?, updated_at=?
WHERE job_id=? AND status='queued'`, string(payload), s.clock.Now().UTC().Unix(), id)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := requireOne(result); err != nil {
		return Job{}, err
	}
	return s.Get(ctx, id)
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
 created_at, updated_at, target_watermark, target_catalog_revision_id, base_query_publication_id, resource_class, progress_unit)
VALUES (?, 'overlay_projection', NULL, ?, 'queued', 'queued', 1, 1, ?, ?, ?, ?, ?, 'overlay', 'items')`,
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
	var resourceClass, targetResource, requestJSON, idempotencyKey, retryPolicyJSON, leaseOwner, resultJSON sql.NullString
	var progressPhase, progressUnit, progressMessage sql.NullString
	var startedAt, finishedAt, targetWatermark, cancelRequestedAt, heartbeatAt, leaseExpiresAt, lastErrorAt sql.NullInt64
	var maxRetries, cancelRequested, failureRetryable, progressEstimated sql.NullInt64
	var progressBytes, progressEntities sql.NullInt64
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, `
SELECT job_id, job_type, source_id, created_by, status, stage,
       progress_current, progress_total, progress_sequence, issue_code, publication_id,
       retry_of, attempt, created_at, started_at, finished_at, updated_at,
       target_watermark, target_catalog_revision_id, base_query_publication_id,
       rule_semantic_hash, rule_parameters_json, rule_parameters_hash, rule_ir_hash, compiler_version,
       cel_profile_version, extension_registry_version,
       resource_class, target_resource, request_json, idempotency_key, max_retries, retry_policy_json,
       cancel_requested, cancel_requested_at, heartbeat_at, lease_owner, lease_expires_at, result_json,
       failure_retryable, progress_phase, progress_unit, progress_message, progress_bytes, progress_entities,
       progress_estimated, last_error_at
FROM jobs WHERE job_id = ?`, id).Scan(
		&job.ID, &job.Type, &sourceID, &job.CreatedBy, &job.Status, &job.Stage,
		&job.ProgressCurrent, &job.ProgressTotal, &job.ProgressSequence, &issueCode, &publicationID,
		&retryOf, &job.Attempt, &createdAt, &startedAt, &finishedAt, &updatedAt,
		&targetWatermark, &targetCatalogID, &basePublicationID, &ruleSemanticHash, &ruleParameters,
		&ruleParametersHash, &ruleIRHash, &compilerVersion, &celProfileVersion, &extensionRegistryVersion,
		&resourceClass, &targetResource, &requestJSON, &idempotencyKey, &maxRetries, &retryPolicyJSON,
		&cancelRequested, &cancelRequestedAt, &heartbeatAt, &leaseOwner, &leaseExpiresAt, &resultJSON,
		&failureRetryable, &progressPhase, &progressUnit, &progressMessage, &progressBytes, &progressEntities,
		&progressEstimated, &lastErrorAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	rawStatus := job.Status
	if rawStatus == StatusRunning && cancelRequested.Int64 != 0 {
		job.Status = StatusCancelling
	} else if rawStatus == StatusCancelled && job.Stage == "superseded" {
		job.Status = StatusSuperseded
	}
	job.SourceID, job.IssueCode, job.PublicationID, job.RetryOf = sourceID.String, issueCode.String, publicationID.String, retryOf.String
	job.TargetWatermark = targetWatermark.Int64
	job.TargetCatalogID, job.BasePublicationID = targetCatalogID.String, basePublicationID.String
	job.RuleSemanticHash, job.RuleParameters, job.RuleParametersHash = ruleSemanticHash.String, []byte(ruleParameters.String), ruleParametersHash.String
	job.RuleIRHash, job.CompilerVersion, job.CELProfileVersion, job.ExtensionRegistryVersion = ruleIRHash.String, compilerVersion.String, celProfileVersion.String, extensionRegistryVersion.String
	job.ResourceClass, job.TargetResource, job.RequestJSON, job.IdempotencyKey = resourceClass.String, targetResource.String, []byte(requestJSON.String), idempotencyKey.String
	job.MaxRetries, job.RetryPolicyJSON = int(maxRetries.Int64), []byte(retryPolicyJSON.String)
	job.CancelRequested, job.FailureRetryable = cancelRequested.Int64 != 0, failureRetryable.Int64 != 0
	job.LeaseOwner, job.ResultJSON = leaseOwner.String, []byte(resultJSON.String)
	job.ProgressPhase, job.ProgressUnit, job.ProgressMessage = progressPhase.String, progressUnit.String, progressMessage.String
	job.ProgressBytes, job.ProgressEntities, job.ProgressEstimated = progressBytes.Int64, progressEntities.Int64, progressEstimated.Int64 != 0
	job.CreatedAt, job.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
	job.StartedAt = nullableTime(startedAt)
	job.FinishedAt = nullableTime(finishedAt)
	job.CancelRequestedAt, job.HeartbeatAt, job.LeaseExpiresAt, job.LastErrorAt = nullableTime(cancelRequestedAt), nullableTime(heartbeatAt), nullableTime(leaseExpiresAt), nullableTime(lastErrorAt)
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE jobs SET status='running', stage=?, started_at=COALESCE(started_at, ?),
heartbeat_at=?, lease_expires_at=?, progress_sequence=progress_sequence+1, updated_at=?
WHERE job_id=? AND status='queued' AND cancel_requested=0`, stage, now.Unix(), now.Unix(), now.Add(2*time.Minute).Unix(), now.Unix(), id)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := requireOne(result); err != nil {
		return Job{}, err
	}
	var attempt int
	var resourceClass, jobType string
	if err := tx.QueryRowContext(ctx, "SELECT attempt, resource_class, job_type FROM jobs WHERE job_id=?", id).Scan(&attempt, &resourceClass, &jobType); err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if resourceClass == ResourceScan && jobType != "scan" {
		resourceClass = resourceClassForType(jobType)
		_, _ = tx.ExecContext(ctx, "UPDATE jobs SET resource_class=? WHERE job_id=?", resourceClass, id)
	}
	attemptID := fmt.Sprintf("%s:%d", id, attempt)
	if _, err := tx.ExecContext(ctx, `INSERT INTO job_attempts
(attempt_id, job_id, attempt, resource_class, status, started_at, heartbeat_at, lease_expires_at, created_at, updated_at)
VALUES (?, ?, ?, ?, 'running', ?, ?, ?, ?, ?)
ON CONFLICT(job_id, attempt) DO UPDATE SET status='running', started_at=COALESCE(job_attempts.started_at, excluded.started_at),
heartbeat_at=excluded.heartbeat_at, lease_expires_at=excluded.lease_expires_at, updated_at=excluded.updated_at`,
		attemptID, id, attempt, resourceClass, now.Unix(), now.Unix(), now.Add(2*time.Minute).Unix(), now.Unix(), now.Unix()); err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := tx.Commit(); err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	return s.Get(ctx, id)
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
	return s.ProgressDetailed(ctx, id, ProgressUpdate{Stage: stage, Current: current, Total: total})
}

type ProgressUpdate struct {
	Stage     string
	Current   int64
	Total     int64
	Bytes     int64
	Entities  int64
	Estimated bool
	Unit      string
	Message   string
}

func (s *Store) ProgressDetailed(ctx context.Context, id string, update ProgressUpdate) (Job, error) {
	if update.Stage == "" || update.Current < 0 || update.Total < 0 || update.Bytes < 0 || update.Entities < 0 ||
		(update.Total > 0 && update.Current > update.Total) {
		return Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	if update.Unit == "" {
		update.Unit = "items"
	}
	var previousCurrent, previousTotal, previousBytes, previousEntities int64
	err := s.db.QueryRowContext(ctx, `SELECT progress_current, progress_total, progress_bytes, progress_entities
FROM jobs WHERE job_id=? AND status='running' AND cancel_requested=0`, id).Scan(&previousCurrent, &previousTotal, &previousBytes, &previousEntities)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, fault.New(fault.CodeJobStateConflict, false, nil)
	}
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if update.Current < previousCurrent || (previousTotal > 0 && update.Total > 0 && update.Total < previousTotal) || update.Bytes < previousBytes || update.Entities < previousEntities {
		return Job{}, fault.New(fault.CodeJobProgressRegression, false, nil)
	}
	now := s.clock.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
UPDATE jobs SET stage = ?, progress_current = ?, progress_total = ?,
                progress_sequence = progress_sequence + 1, progress_phase=?, progress_unit=?, progress_message=?,
                progress_bytes=?, progress_entities=?, progress_estimated=?, heartbeat_at=?, lease_expires_at=?, updated_at = ?
WHERE job_id = ? AND status = 'running' AND cancel_requested=0`, update.Stage, update.Current, update.Total,
		update.Stage, update.Unit, update.Message, update.Bytes, update.Entities, boolInt(update.Estimated), now.Unix(), now.Add(2*time.Minute).Unix(), now.Unix(), id)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := requireOne(result); err != nil {
		return Job{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) Heartbeat(ctx context.Context, id string) (Job, error) {
	now := s.clock.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE jobs SET heartbeat_at=?, lease_expires_at=?, updated_at=?
WHERE job_id=? AND status IN ('running', 'publishing') AND cancel_requested=0`, now.Unix(), now.Add(2*time.Minute).Unix(), now.Unix(), id)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := requireOne(result); err != nil {
		return Job{}, err
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE job_attempts SET heartbeat_at=?, lease_expires_at=?, updated_at=?
WHERE job_id=? AND status='running'`, now.Unix(), now.Add(2*time.Minute).Unix(), now.Unix(), id)
	return s.Get(ctx, id)
}

func (s *Store) CompleteRunning(ctx context.Context, id string, resultJSON []byte) (Job, error) {
	now := s.clock.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE jobs SET status='completed', stage='completed', result_json=?,
finished_at=?, heartbeat_at=NULL, lease_expires_at=NULL, progress_sequence=progress_sequence+1, updated_at=?
WHERE job_id=? AND status='running' AND cancel_requested=0`, nullableBytes(resultJSON), now.Unix(), now.Unix(), id)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := requireOne(result); err != nil {
		return Job{}, err
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE job_attempts SET status='completed', finished_at=?, result_json=?, updated_at=?
WHERE job_id=? AND status='running'`, now.Unix(), nullableBytes(resultJSON), now.Unix(), id)
	return s.Get(ctx, id)
}

func (s *Store) CompleteWithResult(ctx context.Context, id string, resultJSON []byte) (Job, error) {
	return s.CompleteRunning(ctx, id, resultJSON)
}

// RequestCancel 对运行中的 Job 只写入取消请求，真正的资源释放由 runner 在观察 context 后
// 调用 FinalizeCancelled；排队 Job 可以立即进入终态。
func (s *Store) RequestCancel(ctx context.Context, id string) (Job, error) {
	now := s.clock.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE jobs SET status=CASE WHEN status='queued' THEN 'cancelled' ELSE status END,
stage=CASE WHEN status='queued' THEN 'cancelled' ELSE 'cancelling' END,
cancel_requested=1, cancel_requested_at=?, finished_at=CASE WHEN status='queued' THEN ? ELSE finished_at END,
progress_sequence=progress_sequence+1, updated_at=?
WHERE job_id=? AND status IN ('queued', 'running', 'publishing')`, now.Unix(), now.Unix(), now.Unix(), id)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := requireOne(result); err != nil {
		return Job{}, err
	}
	if job, getErr := s.Get(ctx, id); getErr == nil && job.Status == StatusCancelled {
		_, _ = s.db.ExecContext(ctx, `UPDATE job_attempts SET status='cancelled', finished_at=?, updated_at=?
WHERE job_id=? AND status IN ('queued', 'running')`, now.Unix(), now.Unix(), id)
	}
	return s.Get(ctx, id)
}

func (s *Store) FinalizeCancelled(ctx context.Context, id string) (Job, error) {
	now := s.clock.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE jobs SET status='cancelled', stage='cancelled', finished_at=?,
heartbeat_at=NULL, lease_expires_at=NULL, progress_sequence=progress_sequence+1, updated_at=?
WHERE job_id=? AND cancel_requested=1 AND status IN ('running', 'publishing')`, now.Unix(), now.Unix(), id)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := requireOne(result); err != nil {
		return Job{}, err
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE job_attempts SET status='cancelled', finished_at=?, updated_at=?
WHERE job_id=? AND status='running'`, now.Unix(), now.Unix(), id)
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
	_, _ = s.db.ExecContext(ctx, `UPDATE job_attempts SET status='recovered', finished_at=?, updated_at=?
WHERE job_id=? AND status='running'`, now.Unix(), now.Unix(), id)
	return s.Get(ctx, id)
}

func (s *Store) Fail(ctx context.Context, id, issueCode string) (Job, error) {
	return s.FailWithRetryable(ctx, id, issueCode, false)
}

func (s *Store) FailWithRetryable(ctx context.Context, id, issueCode string, retryable bool) (Job, error) {
	if issueCode == "" {
		return Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	now := s.clock.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
UPDATE jobs SET status = ?, stage = 'failed', issue_code = ?, finished_at = ?,
                failure_retryable=?, last_error_at=?, heartbeat_at=NULL, lease_expires_at=NULL,
                progress_sequence = progress_sequence + 1, updated_at = ?
WHERE job_id = ? AND status IN (?, ?, ?)`, StatusFailed, issueCode, now.Unix(), boolInt(retryable), now.Unix(), now.Unix(), id, StatusQueued, StatusRunning, StatusPublishing)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := requireOne(result); err != nil {
		return Job{}, err
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE job_attempts SET status='failed', error_code=?, error_retryable=?, finished_at=?, updated_at=?
WHERE job_id=? AND status IN ('queued', 'running')`, issueCode, boolInt(retryable), now.Unix(), now.Unix(), id)
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
	return s.RequestCancel(ctx, id)
}

func (s *Store) Retry(ctx context.Context, id, createdBy string) (Job, error) {
	previous, err := s.Get(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if previous.Status != StatusFailed && previous.Status != StatusCancelled && previous.Status != StatusNeedsRepair && previous.Status != StatusSuperseded {
		return Job{}, fault.New(fault.CodeJobStateConflict, false, nil)
	}
	if previous.MaxRetries > 0 && previous.Attempt >= previous.MaxRetries+1 {
		return Job{}, fault.New(fault.CodeJobRetryExhausted, false, nil)
	}
	if strings.TrimSpace(createdBy) == "" {
		createdBy = previous.CreatedBy
	}
	options := CreateOptions{ResourceClass: previous.ResourceClass, TargetResource: previous.TargetResource,
		RequestJSON: previous.RequestJSON, IdempotencyKey: "", MaxRetries: previous.MaxRetries, RetryPolicyJSON: previous.RetryPolicyJSON}
	var next Job
	if previous.Type == "scan" {
		var snapshot *RuleExecutionSnapshot
		if previous.RuleSemanticHash != "" && len(previous.RuleParameters) > 0 {
			snapshot = &RuleExecutionSnapshot{SemanticHash: previous.RuleSemanticHash, Parameters: previous.RuleParameters,
				ParametersHash: previous.RuleParametersHash, RuleIRHash: previous.RuleIRHash, CompilerVersion: previous.CompilerVersion,
				CELProfileVersion: previous.CELProfileVersion, ExtensionRegistryVersion: previous.ExtensionRegistryVersion}
		}
		next, err = s.CreateScanWithOptions(ctx, previous.SourceID, createdBy, previous.ID, snapshot, options)
	} else {
		next, err = s.CreateWithOptions(ctx, previous.Type, previous.SourceID, createdBy, options)
		if err == nil {
			_, err = s.db.ExecContext(ctx, "UPDATE jobs SET retry_of=?, attempt=? WHERE job_id=?", previous.ID, previous.Attempt+1, next.ID)
			if err == nil {
				next, err = s.Get(ctx, next.ID)
			}
		}
	}
	if err != nil {
		return Job{}, err
	}
	return next, nil
}

func (s *Store) ListAttempts(ctx context.Context, jobID string) ([]Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT attempt_id, job_id, attempt, resource_class, status,
started_at, heartbeat_at, finished_at, lease_owner, lease_expires_at, error_code, error_retryable,
progress_sequence, result_json, created_at, updated_at FROM job_attempts WHERE job_id=? ORDER BY attempt`, jobID)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	result := []Attempt{}
	for rows.Next() {
		var item Attempt
		var startedAt, heartbeatAt, finishedAt, leaseExpiresAt sql.NullInt64
		var leaseOwner, errorCode, resultJSON sql.NullString
		var retryable, sequence int64
		var createdAt, updatedAt int64
		if err := rows.Scan(&item.ID, &item.JobID, &item.Attempt, &item.ResourceClass, &item.Status,
			&startedAt, &heartbeatAt, &finishedAt, &leaseOwner, &leaseExpiresAt, &errorCode, &retryable,
			&sequence, &resultJSON, &createdAt, &updatedAt); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		item.StartedAt, item.HeartbeatAt, item.FinishedAt, item.LeaseExpiresAt = nullableTime(startedAt), nullableTime(heartbeatAt), nullableTime(finishedAt), nullableTime(leaseExpiresAt)
		item.LeaseOwner, item.ErrorCode, item.ErrorRetryable, item.ProgressSequence, item.ResultJSON = leaseOwner.String, errorCode.String, retryable != 0, uint64(sequence), []byte(resultJSON.String)
		item.CreatedAt, item.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	return result, nil
}

// ReconcileAttempts 收敛租约超时的运行尝试。进程强杀后 runner 不会再写终态，启动时由
// 这个方法把 Job 交回可解释的失败/取消状态，随后上层按 retryable 决定是否重新入队。
func (s *Store) ReconcileAttempts(ctx context.Context, leaseTimeout time.Duration) error {
	if leaseTimeout <= 0 {
		leaseTimeout = 2 * time.Minute
	}
	cutoff := s.clock.Now().UTC().Add(-leaseTimeout).Unix()
	rows, err := s.db.QueryContext(ctx, `SELECT job_id FROM job_attempts WHERE status='running' AND
(heartbeat_at IS NULL OR heartbeat_at < ? OR (lease_expires_at IS NOT NULL AND lease_expires_at < ?))`, cutoff, s.clock.Now().UTC().Unix())
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fault.New(fault.CodeInternal, true, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	for _, id := range ids {
		job, getErr := s.Get(ctx, id)
		if getErr != nil {
			return getErr
		}
		if job.Status == StatusCancelling {
			if _, err := s.FinalizeCancelled(ctx, id); err != nil && !isJobStateConflict(err) {
				return err
			}
			continue
		}
		_, _ = s.db.ExecContext(ctx, `UPDATE job_attempts SET status='recovered', error_code='PROCESS_INTERRUPTED',
error_retryable=1, finished_at=?, updated_at=? WHERE job_id=? AND status='running'`, s.clock.Now().UTC().Unix(), s.clock.Now().UTC().Unix(), id)
		if job.Status == StatusRunning || job.Status == StatusPublishing {
			if _, err := s.FailWithRetryable(ctx, id, "PROCESS_INTERRUPTED", true); err != nil && !isJobStateConflict(err) {
				return err
			}
		}
	}
	return nil
}

func (s *Store) ListByStatuses(ctx context.Context, statuses ...Status) ([]Job, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, "SELECT job_id FROM jobs ORDER BY created_at, job_id")
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
	wanted := make(map[Status]struct{}, len(statuses))
	for _, status := range statuses {
		wanted[status] = struct{}{}
	}
	result := make([]Job, 0, len(ids))
	for _, id := range ids {
		job, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if _, ok := wanted[job.Status]; ok {
			result = append(result, job)
		}
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

func defaultJSON(value []byte) []byte {
	if len(value) == 0 {
		return []byte("{}")
	}
	return append([]byte(nil), value...)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func isJobStateConflict(err error) bool {
	var structured *fault.Error
	return errors.As(err, &structured) && structured.Code == fault.CodeJobStateConflict
}
