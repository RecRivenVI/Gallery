package maintenance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/derived"
	"github.com/RecRivenVI/gallery/internal/derivedjob"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/ports"
)

type Request struct {
	RetentionSeconds int64       `json:"retentionSeconds"`
	DryRun           bool        `json:"dryRun"`
	Operation        string      `json:"operation"`
	Space            SpaceReport `json:"space"`
}

type SpaceReport struct {
	Operation     string `json:"operation"`
	Path          string `json:"-"`
	RequiredBytes int64  `json:"requiredBytes"`
	FreeBytes     int64  `json:"availableBytes"`
	Sufficient    bool   `json:"sufficient"`
	Conservative  bool   `json:"conservative"`
}

type GCReport struct {
	Catalog            catalog.GCResult
	DerivedRemoved     int
	TempRemoved        int
	TempOrphansRemoved int
	DryRun             bool
}

type Service struct {
	context context.Context
	control *sql.DB
	catalog *catalog.Store
	jobs    *jobs.Store
	derived *derived.Service
	dirs    appdirs.Dirs
	space   ports.SpaceChecker
	clock   ports.Clock
	temp    *jobs.TempStore
	coord   *Coordinator
}

func New(ctx context.Context, control *sql.DB, catalogStore *catalog.Store, jobStore *jobs.Store, derivedService *derived.Service, dirs appdirs.Dirs, space ports.SpaceChecker, clock ports.Clock) (*Service, error) {
	if ctx == nil || control == nil || catalogStore == nil || jobStore == nil || clock == nil {
		return nil, fmt.Errorf("Maintenance Service 缺少依赖")
	}
	tempStore, err := jobs.NewTempStore(control, dirs.Temp, clock)
	if err != nil {
		return nil, err
	}
	return &Service{context: ctx, control: control, catalog: catalogStore, jobs: jobStore, derived: derivedService,
		dirs: dirs, space: space, clock: clock, temp: tempStore, coord: NewCoordinator()}, nil
}

func (s *Service) SetCoordinator(coordinator *Coordinator) {
	if coordinator != nil {
		s.coord = coordinator
	}
}

func (s *Service) CreateGC(ctx context.Context, createdBy string, request Request) (jobs.Job, error) {
	if request.RetentionSeconds < 0 {
		return jobs.Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	space, err := s.Estimate(ctx, "catalog_gc")
	if err != nil {
		return jobs.Job{}, err
	}
	request.Operation, request.Space = "catalog_gc", space
	payload, err := json.Marshal(request)
	if err != nil {
		return jobs.Job{}, fault.New(fault.CodeInternal, true, err)
	}
	job, err := s.jobs.CreateMaintenance(ctx, "catalog_gc", createdBy)
	if err != nil {
		return jobs.Job{}, err
	}
	if _, err := s.jobs.SetRequest(ctx, job.ID, payload); err != nil {
		return jobs.Job{}, err
	}
	return s.jobs.Get(ctx, job.ID)
}

func (s *Service) Create(ctx context.Context, jobType, createdBy string) (jobs.Job, error) {
	space, err := s.Estimate(ctx, jobType)
	if err != nil {
		return jobs.Job{}, err
	}
	job, err := s.jobs.CreateMaintenance(ctx, jobType, createdBy)
	if err != nil {
		return jobs.Job{}, err
	}
	payload, _ := json.Marshal(Request{Operation: jobType, Space: space})
	if _, err := s.jobs.SetRequest(ctx, job.ID, payload); err != nil {
		return jobs.Job{}, err
	}
	return s.jobs.Get(ctx, job.ID)
}

func (s *Service) Execute(ctx context.Context, jobID string) error {
	job, err := s.jobs.StartStage(ctx, jobID, "maintenance")
	if err != nil {
		return err
	}
	switch job.Type {
	case "catalog_gc":
		var request Request
		if err := json.Unmarshal(job.RequestJSON, &request); err != nil {
			return s.fail(ctx, jobID, fault.New(fault.CodeValidation, false, err))
		}
		if _, err := s.Estimate(ctx, "catalog_gc"); err != nil {
			return s.fail(ctx, jobID, err)
		}
		if _, err := s.RunGC(ctx, time.Duration(request.RetentionSeconds)*time.Second, request.DryRun); err != nil {
			return s.fail(ctx, jobID, err)
		}
	case "catalog_checkpoint":
		if _, err := s.Estimate(ctx, "catalog_checkpoint"); err != nil {
			return s.fail(ctx, jobID, err)
		}
		if err := s.Checkpoint(ctx); err != nil {
			return s.fail(ctx, jobID, err)
		}
	case "catalog_vacuum":
		if _, err := s.Estimate(ctx, "catalog_vacuum"); err != nil {
			return s.fail(ctx, jobID, err)
		}
		if err := s.Vacuum(ctx); err != nil {
			return s.fail(ctx, jobID, err)
		}
	case "derived_gc":
		if _, err := s.RunGC(ctx, 0, false); err != nil {
			return s.fail(ctx, jobID, err)
		}
	default:
		return s.fail(ctx, jobID, fault.New(fault.CodeValidation, false, nil))
	}
	_, err = s.jobs.CompleteMaintenance(ctx, jobID)
	return err
}

func (s *Service) Reconcile(ctx context.Context, start func(string)) error {
	// queued 提交与 running 租约回收由 jobs.Reconciler 统一处理。
	return nil
}

func (s *Service) RunGC(ctx context.Context, retention time.Duration, dryRun bool) (GCReport, error) {
	release := s.coord.AcquireMaintenance()
	defer release()
	active, err := s.jobs.ListByStatuses(ctx, jobs.StatusQueued, jobs.StatusRunning, jobs.StatusPublishing, jobs.StatusCancelling)
	if err != nil {
		return GCReport{}, err
	}
	activeIDs := make([]string, 0, len(active))
	for _, job := range active {
		activeIDs = append(activeIDs, job.ID)
	}
	protectedBlobs, err := s.protectedDerivedBlobs(ctx, active)
	if err != nil {
		return GCReport{}, err
	}
	shareBlobs, err := s.protectedShareBlobs(ctx)
	if err != nil {
		return GCReport{}, err
	}
	protectedBlobs = append(protectedBlobs, shareBlobs...)
	result, err := s.catalog.GarbageCollectWithOptions(ctx, catalog.GCOptions{Retention: retention, ActiveJobIDs: activeIDs, ProtectedBlobs: protectedBlobs, DryRun: dryRun})
	if err != nil {
		return GCReport{}, err
	}
	report := GCReport{Catalog: result, DryRun: dryRun}
	if !dryRun && s.derived != nil {
		report.DerivedRemoved, err = s.derived.SweepObsolete(ctx, s.clock.Now().UTC().Add(-retention))
		if err != nil {
			return GCReport{}, err
		}
	}
	if !dryRun {
		grace := retention
		if grace < 24*time.Hour {
			grace = 24 * time.Hour
		}
		tempReport, sweepErr := s.temp.Sweep(ctx, grace, 7*24*time.Hour)
		err = sweepErr
		if err != nil {
			return GCReport{}, err
		}
		report.TempRemoved, report.TempOrphansRemoved = tempReport.TerminalRemoved, tempReport.OrphanRemoved
	}
	return report, nil
}

func (s *Service) Preflight(ctx context.Context, requiredBytes int64) (SpaceReport, error) {
	if requiredBytes < 0 {
		return SpaceReport{}, fault.New(fault.CodeValidation, false, nil)
	}
	path := s.dirs.Data
	if s.space == nil {
		return SpaceReport{Path: path, RequiredBytes: requiredBytes, Sufficient: true, Conservative: true}, nil
	}
	free, err := s.space.FreeBytes(path)
	if err != nil {
		return SpaceReport{}, fault.New(fault.CodeInternal, true, err)
	}
	report := SpaceReport{Path: path, RequiredBytes: requiredBytes, FreeBytes: free, Sufficient: free >= requiredBytes, Conservative: true}
	if !report.Sufficient {
		return report, fault.New(fault.CodeDiskSpaceInsufficient, true, nil)
	}
	return report, nil
}

// Estimate 由服务端按操作类型生成保守空间预算；客户端不能提供 requiredBytes 绕过门禁。
func (s *Service) Estimate(ctx context.Context, operation string) (SpaceReport, error) {
	controlSize := fileSize(filepath.Join(s.dirs.Data, "control.db"))
	catalogSize := fileSize(filepath.Join(s.dirs.Data, "catalog.db"))
	controlWAL := fileSize(filepath.Join(s.dirs.Data, "control.db-wal"))
	catalogWAL := fileSize(filepath.Join(s.dirs.Data, "catalog.db-wal"))
	var required int64
	switch operation {
	case "catalog_gc", "derived_gc":
		required = 4 << 20
	case "derived_asset":
		required = 64 << 20
	case "external_tool":
		required = 128 << 20
	case "catalog_checkpoint":
		required = controlWAL + catalogWAL + (4 << 20)
	case "catalog_vacuum":
		required = controlSize + catalogSize + controlWAL + catalogWAL + (16 << 20)
	case "catalog_staging":
		required = catalogSize + catalogWAL + catalogSize/4 + (16 << 20)
	case "control_backup":
		required = controlSize + controlWAL + (4 << 20)
	default:
		return SpaceReport{}, fault.New(fault.CodeValidation, false, nil)
	}
	report, err := s.Preflight(ctx, required)
	report.Operation = operation
	return report, err
}

func (s *Service) CheckSpace(ctx context.Context, operation string, additionalBytes int64) error {
	if additionalBytes < 0 {
		return fault.New(fault.CodeValidation, false, nil)
	}
	report, err := s.Estimate(ctx, operation)
	if err != nil {
		return err
	}
	_, err = s.Preflight(ctx, report.RequiredBytes+additionalBytes)
	return err
}

// protectedDerivedBlobs 收集当前仍可能被 DerivedAsset Job 使用的 ContentBlob 摘要：
// Job 排队等待调度、正在真正生成、或已失败但仍处于退避等待期（尚未耗尽重试次数）都算
// 在内。media.BlobReadLease 只提供固定 TTL 的时间保护，无法覆盖"排队等待超过 TTL"、
// "退避等待超过 TTL"或"单次生成耗时超过 TTL"——这些场景下 Job 依然可能在之后重新读取
// 同一 Blob，因此 GC 必须额外参照 Job 表本身的非终态状态，而不能只信任租约是否过期。
// active 是调用方已经为 staging candidate 保护查询过的同一批非终态 Job，这里直接复用、
// 再补上一次针对 derived 类型的退避等待期查询，不重复扫描整张 Job 表。
func (s *Service) protectedDerivedBlobs(ctx context.Context, active []jobs.Job) ([]domain.ContentBlobRef, error) {
	retrying, err := s.jobs.ListRetryPending(ctx, jobs.ResourceDerived)
	if err != nil {
		return nil, err
	}
	blobs := make([]domain.ContentBlobRef, 0, len(active)+len(retrying))
	appendDerivedBlob := func(job jobs.Job) {
		if job.Type != jobs.ResourceDerived || len(job.RequestJSON) == 0 {
			return
		}
		var request derivedjob.Request
		if err := json.Unmarshal(job.RequestJSON, &request); err != nil {
			return
		}
		if request.BlobAlgorithm == "" || request.BlobDigest == "" {
			return
		}
		blobs = append(blobs, domain.ContentBlobRef{Algorithm: request.BlobAlgorithm, Digest: request.BlobDigest})
	}
	for _, job := range active {
		appendDerivedBlob(job)
	}
	for _, job := range retrying {
		appendDerivedBlob(job)
	}
	return blobs, nil
}

// protectedShareBlobs 把未过期、未吊销的固定 Blob 分享纳入既有 Catalog GC 保护集合。
// Share 是 control.db 的持久事实，不能依赖短期读取 lease 才避免其引用的已发布 revision 被回收。
func (s *Service) protectedShareBlobs(ctx context.Context) ([]domain.ContentBlobRef, error) {
	rows, err := s.control.QueryContext(ctx, `SELECT DISTINCT fixed_blob_algorithm, fixed_blob_digest
FROM shares WHERE revoked_at IS NULL AND expires_at>? AND fixed_blob_algorithm IS NOT NULL`, s.clock.Now().UTC().Unix())
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	var result []domain.ContentBlobRef
	for rows.Next() {
		var blob domain.ContentBlobRef
		if err := rows.Scan(&blob.Algorithm, &blob.Digest); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		if _, err := domain.ParseContentBlobRef(blob.Algorithm, blob.Digest); err != nil {
			return nil, fault.New(fault.CodeInternal, false, err)
		}
		result = append(result, blob)
	}
	if err := rows.Err(); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	return result, nil
}

func (s *Service) Checkpoint(ctx context.Context) error {
	release := s.coord.AcquireMaintenance()
	defer release()
	if _, err := s.control.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return maintenanceFault(err)
	}
	return s.catalog.Checkpoint(ctx)
}

func (s *Service) Vacuum(ctx context.Context) error {
	release := s.coord.AcquireMaintenance()
	defer release()
	if _, err := s.control.ExecContext(ctx, "VACUUM"); err != nil {
		return maintenanceFault(err)
	}
	return s.catalog.Vacuum(ctx)
}

func maintenanceFault(err error) error {
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "busy") || strings.Contains(lower, "locked") {
		return fault.New(fault.CodeMaintenanceBlocked, true, err)
	}
	return fault.New(fault.CodeInternal, true, err)
}

func (s *Service) fail(ctx context.Context, jobID string, err error) error {
	code, retryable := faultCode(err), true
	var structured *fault.Error
	if errors.As(err, &structured) {
		retryable = structured.Retryable
	}
	_, _ = s.jobs.FailWithRetryable(ctx, jobID, string(code), retryable)
	return err
}

func faultCode(err error) fault.Code {
	var structured *fault.Error
	if errors.As(err, &structured) {
		return structured.Code
	}
	return fault.CodeInternal
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return 0
	}
	return info.Size()
}
