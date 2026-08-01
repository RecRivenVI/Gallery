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
	"sync"
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

// Notifier 接收维护 Job 的持久状态变化，供 WebSocket 仅作为 HTTP 快照失效提示。
type Notifier interface {
	JobChanged(jobs.Job)
}

type nopNotifier struct{}

func (nopNotifier) JobChanged(jobs.Job) {}

type Service struct {
	context  context.Context
	control  *sql.DB
	catalog  *catalog.Store
	jobs     *jobs.Store
	derived  *derived.Service
	dirs     appdirs.Dirs
	space    ports.SpaceChecker
	clock    ports.Clock
	temp     *jobs.TempStore
	coord    *Coordinator
	notifier Notifier
	// heartbeatInterval 必须明显短于 Job Store 当前 2 分钟运行租约；具体值仍是
	// PRE_FREEZE 运行策略，和 Hash Job 一样可由启动配置/确定性测试收紧。
	heartbeatInterval time.Duration
}

func New(ctx context.Context, control *sql.DB, catalogStore *catalog.Store, jobStore *jobs.Store, derivedService *derived.Service, dirs appdirs.Dirs, space ports.SpaceChecker, clock ports.Clock, notifier Notifier) (*Service, error) {
	if ctx == nil || control == nil || catalogStore == nil || jobStore == nil || clock == nil {
		return nil, fmt.Errorf("Maintenance Service 缺少依赖")
	}
	if notifier == nil {
		notifier = nopNotifier{}
	}
	tempStore, err := jobs.NewTempStore(control, dirs.Temp, clock)
	if err != nil {
		return nil, err
	}
	return &Service{context: ctx, control: control, catalog: catalogStore, jobs: jobStore, derived: derivedService,
		dirs: dirs, space: space, clock: clock, temp: tempStore, coord: NewCoordinator(), notifier: notifier,
		heartbeatInterval: 30 * time.Second}, nil
}

func (s *Service) SetCoordinator(coordinator *Coordinator) {
	if coordinator != nil {
		s.coord = coordinator
	}
}

// SetHeartbeatPolicy 只用于运行配置和确定性测试；租约/心跳数值属于 PRE_FREEZE
// 调优项，不进入公开维护协议。
func (s *Service) SetHeartbeatPolicy(interval time.Duration) {
	if interval > 0 {
		s.heartbeatInterval = interval
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
	created, err := s.jobs.Get(ctx, job.ID)
	if err == nil {
		s.notifier.JobChanged(created)
	}
	return created, err
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
	created, err := s.jobs.Get(ctx, job.ID)
	if err == nil {
		s.notifier.JobChanged(created)
	}
	return created, err
}

func (s *Service) Execute(ctx context.Context, jobID string) error {
	job, err := s.jobs.StartStage(ctx, jobID, "maintenance")
	if err != nil {
		return err
	}
	s.notifier.JobChanged(job)
	if err := s.progress(ctx, jobID, "preflight", 0); err != nil {
		return s.fail(ctx, jobID, err)
	}

	var run func() error
	switch job.Type {
	case "catalog_gc":
		var request Request
		if err := json.Unmarshal(job.RequestJSON, &request); err != nil {
			return s.fail(ctx, jobID, fault.New(fault.CodeValidation, false, err))
		}
		run = func() error {
			_, err := s.RunGC(ctx, time.Duration(request.RetentionSeconds)*time.Second, request.DryRun)
			return err
		}
	case "catalog_checkpoint":
		run = func() error { return s.Checkpoint(ctx) }
	case "catalog_vacuum":
		run = func() error { return s.Vacuum(ctx) }
	case "derived_gc":
		run = func() error {
			_, err := s.RunGC(ctx, 0, false)
			return err
		}
	default:
		return s.fail(ctx, jobID, fault.New(fault.CodeValidation, false, nil))
	}
	// 创建任务时的空间估算只用于向用户说明和拒绝明显不足。真正取得维护互斥前必须
	// 再做一次同操作的服务端预检；Derived GC 也不能依赖可能已经过期的创建时快照。
	if _, err := s.Estimate(ctx, job.Type); err != nil {
		return s.fail(ctx, jobID, err)
	}
	if err := s.progress(ctx, jobID, "executing", 1); err != nil {
		return s.fail(ctx, jobID, err)
	}
	if err := s.runWithHeartbeat(ctx, jobID, run); err != nil {
		return s.fail(ctx, jobID, err)
	}
	if err := s.progress(ctx, jobID, "finalizing", 2); err != nil {
		return s.fail(ctx, jobID, err)
	}
	if current, getErr := s.jobs.Get(context.Background(), jobID); getErr == nil && current.CancelRequested {
		return s.fail(ctx, jobID, fault.New(fault.CodeProcessInterrupted, true, ctx.Err()))
	}
	completed, err := s.jobs.CompleteMaintenance(ctx, jobID)
	if err != nil {
		return s.fail(ctx, jobID, err)
	}
	s.notifier.JobChanged(completed)
	return nil
}

// runWithHeartbeat 覆盖 GC/VACUUM 这种长时间没有逐页进度回调的区间。旧实现只在
// entering executing 时续租一次，500k GC 超过两分钟后会被中央恢复器当成孤儿
// Attempt 回收，即使底层 SQL 仍在正常工作。心跳只更新 control.db，不接触 Source，
// 也不伪造 Catalog 操作的字节或百分比进度。
func (s *Service) runWithHeartbeat(ctx context.Context, jobID string, run func() error) error {
	heartbeatCtx, stop := context.WithCancel(ctx)
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		ticker := time.NewTicker(s.heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				// 使用可取消的维护 context，避免主操作结束后被一个仍在等待
				// SQLite busy handler 的后台心跳拖住 Execute 收敛。
				_, _ = s.jobs.Heartbeat(heartbeatCtx, jobID)
			}
		}
	}()
	err := run()
	stop()
	wait.Wait()
	return err
}

// progress 将维护任务的粗粒度阶段持久化并立即发出失效提示。SQLite 的 VACUUM/
// checkpoint 与当前 GC 端口没有可靠的逐页进度回调，因此这里只声明 estimated 的阶段
// 进度，不伪造字节数或百分比；0/2、1/2、2/2 仍由 Job Store 保证严格单调。
func (s *Service) progress(ctx context.Context, jobID, stage string, current int64) error {
	job, err := s.jobs.ProgressDetailed(ctx, jobID, jobs.ProgressUpdate{
		Stage: stage, Current: current, Total: 2, Unit: "phases", Estimated: true,
	})
	if err != nil {
		return err
	}
	s.notifier.JobChanged(job)
	return nil
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
	var busy, logFrames, checkpointedFrames int
	if err := s.control.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(
		&busy, &logFrames, &checkpointedFrames); err != nil {
		return maintenanceFault(err)
	}
	if busy != 0 {
		return fault.New(fault.CodeMaintenanceBlocked, true, fmt.Errorf(
			"control checkpoint busy: log=%d checkpointed=%d", logFrames, checkpointedFrames))
	}
	return s.catalog.Checkpoint(ctx)
}

func (s *Service) Vacuum(ctx context.Context) error {
	release := s.coord.AcquireMaintenance()
	defer release()
	if _, err := s.control.ExecContext(ctx, "VACUUM"); err != nil {
		return maintenanceFault(err)
	}
	leases := s.catalog.PublicationLeases()
	leases.BeginDeferred()
	vacuumErr := s.catalog.Vacuum(ctx)
	// 即使请求 context 在 full VACUUM 期间取消，也必须尽力把已经返回给客户端的
	// cursor/显式快照 lease 落盘；否则维护锁一释放，后续 GC 会看不到保护事实。
	flushCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	flushErr := leases.FlushAndEnd(flushCtx)
	cancel()
	if vacuumErr != nil || flushErr != nil {
		return maintenanceFault(errors.Join(vacuumErr, flushErr))
	}
	// WAL 模式下 full VACUUM 的重写结果可能全部位于 catalog.db-wal；lease 已持久化后
	// 再做 TRUNCATE checkpoint，确保空间回收事实落到主文件并避免重启时携带巨型 WAL。
	return s.catalog.Checkpoint(ctx)
}

func maintenanceFault(err error) error {
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "busy") || strings.Contains(lower, "locked") {
		return fault.New(fault.CodeMaintenanceBlocked, true, err)
	}
	return fault.New(fault.CodeInternal, true, err)
}

func (s *Service) fail(ctx context.Context, jobID string, err error) error {
	current, _ := s.jobs.Get(context.Background(), jobID)
	if current.CancelRequested {
		if current.Status == jobs.StatusCancelled {
			return err
		}
		if cancelled, finalizeErr := s.jobs.FinalizeCancelled(context.Background(), jobID); finalizeErr == nil {
			s.notifier.JobChanged(cancelled)
			return err
		} else {
			err = errors.Join(err, finalizeErr)
		}
	}
	code, retryable := faultCode(err), true
	var structured *fault.Error
	if errors.As(err, &structured) {
		retryable = structured.Retryable
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		code, retryable = fault.CodeProcessInterrupted, true
	}
	if failed, failErr := s.jobs.FailWithRetryable(context.Background(), jobID, string(code), retryable); failErr != nil {
		return errors.Join(err, failErr)
	} else {
		s.notifier.JobChanged(failed)
	}
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
