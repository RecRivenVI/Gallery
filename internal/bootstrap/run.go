package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/auth"
	"github.com/RecRivenVI/gallery/internal/backup"
	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/config"
	contractapi "github.com/RecRivenVI/gallery/internal/contract/api"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/contract/realtime"
	"github.com/RecRivenVI/gallery/internal/creators"
	"github.com/RecRivenVI/gallery/internal/derived"
	"github.com/RecRivenVI/gallery/internal/derived/thumbnail"
	"github.com/RecRivenVI/gallery/internal/derivedjob"
	"github.com/RecRivenVI/gallery/internal/hashjob"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/maintenance"
	"github.com/RecRivenVI/gallery/internal/overlay"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/descriptor"
	"github.com/RecRivenVI/gallery/internal/platform/disk"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/platform/lock"
	platformprocess "github.com/RecRivenVI/gallery/internal/platform/process"
	platformwatcher "github.com/RecRivenVI/gallery/internal/platform/watcher"
	"github.com/RecRivenVI/gallery/internal/recovery"
	"github.com/RecRivenVI/gallery/internal/scanner"
	"github.com/RecRivenVI/gallery/internal/storage"
	"github.com/RecRivenVI/gallery/internal/toolrunner"
	"github.com/RecRivenVI/gallery/internal/transport/httpapi"
	watcherservice "github.com/RecRivenVI/gallery/internal/watcher"
	"github.com/RecRivenVI/gallery/internal/webapp"
	version "github.com/RecRivenVI/gallery/pkg/galleryversion"
)

// 各资源类别的默认并发上限。scan 允许少量不同 Source 并行；overlay 单活跃投影 Job，取 1 与其
// 单活跃 Job 数据库约束一致。这些值属于运行配置的暂定实装决策，尚未冻结。
const (
	scanConcurrency         = 2
	hashConcurrency         = 2
	overlayConcurrency      = 1
	derivedConcurrency      = 1
	externalToolConcurrency = 1
	maintenanceConcurrency  = 1
	recoveryInterval        = 30 * time.Second
	jobLeaseTimeout         = 2 * time.Minute
	pollingWatcherInterval  = 5 * time.Minute
	sourceReconcileInterval = 30 * time.Second
)

// classDispatcher 把某个资源类别的 Submit 适配为服务侧的 Dispatcher，避免服务依赖调度器具体类型。
type classDispatcher struct {
	scheduler *jobs.Scheduler
	class     string
}

func (d classDispatcher) Submit(jobID string) bool { return d.scheduler.Submit(d.class, jobID) }

// Run 启动 galleryd 并在服务退出后返回。外部进程使用 runtime descriptor 发现服务；测试或
// 需要显式生命周期同步的内部调用方可使用 RunWithReady 获取同一份已发布 descriptor。
func Run(ctx context.Context, cfg config.Config, logger *slog.Logger) (err error) {
	return run(ctx, cfg, logger, nil)
}

// RunWithReady 与 Run 使用相同的启动、服务和关闭语义，并在 runtime descriptor 已原子发布、
// listener 已建立且服务进入可观察状态后发送 ready。ready 只用于进程内生命周期同步，不改变
// HTTP/API 契约；调用方应提供可接收的 channel，并在收到信号后再访问 descriptor 或服务。
func RunWithReady(ctx context.Context, cfg config.Config, logger *slog.Logger, ready chan<- descriptor.Descriptor) (err error) {
	return run(ctx, cfg, logger, ready)
}

func run(ctx context.Context, cfg config.Config, logger *slog.Logger, ready chan<- descriptor.Descriptor) (err error) {
	// serving 在进入服务循环前为 false。启动阶段若父 ctx 已被取消（正常关闭请求恰好在启动中到达），
	// 由 ctx 取消派生的错误视为干净关闭而非失败；进入服务循环后不再屏蔽，server.Shutdown 超时等
	// 真实错误照常返回。非取消派生的启动错误（如监听失败、迁移失败）也不受影响。
	serving := false
	defer func() {
		if err != nil && !serving && ctx.Err() != nil &&
			(errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			logger.Info("galleryd_startup_cancelled")
			err = nil
		}
	}()

	if err := cfg.Validate(); err != nil {
		return err
	}
	fileSystem := filesystem.OS{}
	if err := cfg.AppDirs.ValidateDisjoint(fileSystem, cfg.SourceRoots); err != nil {
		return err
	}
	if err := cfg.AppDirs.Ensure(fileSystem); err != nil {
		return err
	}
	// 在打开或迁移任何数据库之前取得 AppDirs 独占所有权。第二个实例在此失败，不会打开数据库、
	// 执行 migration、建立监听、重写 descriptor 或启动后台 Job。锁由操作系统在进程退出（含强杀）
	// 时释放，遗留锁文件不会永久阻止启动。
	ownership, err := lock.Acquire(filepath.Join(cfg.AppDirs.Runtime, "galleryd.lock"))
	if err != nil {
		if errors.Is(err, lock.ErrAlreadyLocked) {
			return fault.New(fault.CodeInstanceAlreadyRunning, false, err)
		}
		return fault.New(fault.CodeLockUnavailable, false, err)
	}
	defer ownership.Release()

	// 待应用恢复请求必须在打开任何数据库之前、于单写者锁保护下执行原子替换。恢复失败保留当前库
	// 并继续启动。
	restoreOutcome, err := backup.ApplyPendingRestore(ctx, cfg.AppDirs)
	if err != nil {
		return err
	}

	store, err := storage.Open(ctx, cfg.AppDirs)
	if err != nil {
		return err
	}
	defer store.Close()

	if restoreOutcome.Applied {
		if err := backup.FinalizeRestore(ctx, store.Control, clock.System{}.Now()); err != nil {
			return err
		}
		logger.Info("control_restore_applied", "backup", restoreOutcome.BackupID)
	}

	systemClock := clock.System{}
	personal, err := auth.NewPersonal(store.Control.SQL(), systemClock, identity.NewGenerator(systemClock), nil)
	if err != nil {
		return err
	}
	if cfg.Mode == config.ModeLAN && !config.IsLoopbackListen(cfg.Listen) {
		initialized, err := personal.LANInitialized(ctx)
		if err != nil {
			return err
		}
		if !initialized {
			return fault.New(fault.CodeLANOwnerRequired, false, fmt.Errorf("LAN Owner 尚未初始化；先以 loopback 启动 LAN 模式完成初始化"))
		}
	}
	// 非 loopback LAN 只有在 control.db 已确认存在 Owner 后才真正绑定，避免未初始化服务暴露。
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("监听失败: %w", err)
	}
	defer listener.Close()
	resources, err := application.NewResources(store.Control.SQL(), cfg.AppDirs, fileSystem, systemClock, identity.NewGenerator(systemClock))
	if err != nil {
		return err
	}
	jobStore, err := jobs.NewStore(store.Control.SQL(), systemClock, identity.NewGenerator(systemClock))
	if err != nil {
		return err
	}
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), systemClock, identity.NewGenerator(systemClock))
	if err != nil {
		return err
	}
	derivedService, err := derived.New(store.Catalog.SQL(), cfg.AppDirs.Cache, systemClock, nil)
	if err != nil {
		return err
	}
	if err := derivedService.Reconcile(ctx); err != nil {
		return err
	}
	hub := realtime.NewHub(systemClock)
	scannerService, err := scanner.New(ctx, resources, jobStore, catalogStore, hub)
	if err != nil {
		return err
	}
	hashService, err := hashjob.New(ctx, resources, jobStore)
	if err != nil {
		return err
	}
	maintenanceService, err := maintenance.New(ctx, store.Control.SQL(), catalogStore, jobStore, derivedService, cfg.AppDirs, disk.OS{}, systemClock)
	if err != nil {
		return err
	}
	derivedJobService, err := derivedjob.New(jobStore, derivedService, thumbnail.New(catalogStore, resources))
	if err != nil {
		return err
	}
	derivedJobService.SetBlobLeaser(store.Catalog.SQL(), systemClock)
	toolService, err := toolrunner.New(jobStore, platformprocess.Controller{}, nil)
	if err != nil {
		return err
	}
	jobTempStore, err := jobs.NewTempStore(store.Control.SQL(), cfg.AppDirs.Temp, systemClock)
	if err != nil {
		return err
	}
	toolService.SetTempStore(jobTempStore)
	watcherService, err := watcherservice.New(ctx, store.Control.SQL(), resources, jobStore, scannerService,
		platformwatcher.NewPolling(pollingWatcherInterval, 4096), systemClock, sourceReconcileInterval)
	if err != nil {
		return err
	}
	overlayService, err := overlay.New(ctx, store.Control.SQL(), jobStore, catalogStore, systemClock, hub)
	if err != nil {
		return err
	}
	maintenanceCoordinator := maintenance.NewCoordinator()
	maintenanceService.SetCoordinator(maintenanceCoordinator)
	scannerService.SetMaintenanceCoordinator(maintenanceCoordinator)
	overlayService.SetMaintenanceCoordinator(maintenanceCoordinator)
	backupService, err := backup.New(ctx, store.Control, jobStore, cfg.AppDirs, systemClock, identity.NewGenerator(systemClock), version.Version, hub)
	if err != nil {
		return err
	}
	scannerService.SetSpaceGate(maintenanceService)
	backupService.SetSpaceGate(maintenanceService)
	derivedJobService.SetSpaceGate(maintenanceService)
	toolService.SetSpaceGate(maintenanceService)
	// 中央有界调度器替代业务服务直接启动 goroutine：每类资源独立并发上限，取消随 context 传播，
	// 关闭时取消在执行 Job 并等待退出（未完成 Job 由启动 reconciliation 重新入队）。
	scheduler := jobs.NewScheduler(ctx)
	scheduler.Register(jobs.ResourceScan, scanConcurrency, scannerService.Execute)
	scheduler.Register(jobs.ResourceHash, hashConcurrency, hashService.Execute)
	scheduler.Register(jobs.ResourceOverlay, overlayConcurrency, overlayService.Execute)
	scheduler.Register(jobs.ResourceDerived, derivedConcurrency, derivedJobService.Execute)
	scheduler.Register(jobs.ResourceExternalTool, externalToolConcurrency, toolService.Execute)
	scheduler.Register(jobs.ResourceMaintenance, maintenanceConcurrency, func(runCtx context.Context, jobID string) error {
		job, getErr := jobStore.Get(runCtx, jobID)
		if getErr != nil {
			return getErr
		}
		if job.Type == "control_backup" || job.Type == "control_restore" {
			return backupService.Execute(runCtx, jobID)
		}
		return maintenanceService.Execute(runCtx, jobID)
	})
	scannerService.SetDispatcher(classDispatcher{scheduler: scheduler, class: jobs.ResourceScan})
	scannerService.SetHashService(hashService)
	hashService.SetDispatcher(classDispatcher{scheduler: scheduler, class: jobs.ResourceHash})
	overlayService.SetDispatcher(classDispatcher{scheduler: scheduler, class: jobs.ResourceOverlay})
	backupService.SetDispatcher(classDispatcher{scheduler: scheduler, class: jobs.ResourceMaintenance})
	defer scheduler.Shutdown()
	jobReconciler, err := recovery.New(jobStore, scheduler, recoveryInterval, jobLeaseTimeout)
	if err != nil {
		return err
	}
	recoveryContext, stopRecovery := context.WithCancel(ctx)
	defer func() {
		stopRecovery()
		jobReconciler.Wait()
	}()

	if err := scannerService.Reconcile(ctx); err != nil {
		return err
	}
	if err := overlayService.Reconcile(ctx); err != nil {
		return err
	}
	// migration 00010（v9→v10）只能给已有 revision 的 favorite/progress/search_*_norm 新列
	// 填入静态默认值，不会自动重新计算；升级后的服务在这里同步触发一次真实的 Overlay
	// 投影 Job 重建当前 active revision 的这些字段，避免在用户下一次恰好触碰某个 Overlay
	// 字段之前一直用默认零值静默提供错误的过滤/排序/高亮结果。只在从未触发过时执行一次
	// （见 catalog.Store.NeedsQueryDependencyBackfill），全新安装或空 Catalog 直接标记为
	// 无需回填。同步 Execute 而不是留给调度器异步执行，使得"迁移完成"与"这些字段已经
	// 正确物化"在服务真正开始对外提供查询之前是同一个原子前提；如果这次调用只是与一个
	// 已经存在的待处理投影 Job 合并（Created=false），说明有其它来源已经会驱动同一次
	// 重建，这里不重复抢占执行。
	needsQueryDependencyBackfill, err := catalogStore.NeedsQueryDependencyBackfill(ctx)
	if err != nil {
		return err
	}
	if needsQueryDependencyBackfill {
		backfillJob, _, err := overlayService.TriggerReprojection(ctx, "system:query-dependency-backfill")
		if err != nil {
			return err
		}
		// EnqueueOverlayProjectionTx 在同一 catalog revision 已有 queued/running/publishing
		// 的 overlay_projection Job 时返回 created=false 并把这个已存在的 Job 合并复用——
		// 它可能是另一个 Overlay 写入排队的、也可能是上一次进程崩溃遗留的 running 行。无论
		// 哪种情况，只要它还没有真正 completed，就不能提前写入回填完成标记：那会让"迁移
		// 完成"与"查询快照列已经正确物化"这两个必须同时成立的前提被拆开，一旦这个被合并
		// 的 Job 之后失败或永久卡住，服务会在不知情的情况下用默认零值对外提供查询。这里
		// 复用既有 Job/Retry/Execute/ReconcileAttempts 机制驱动它到终态，不新增第二套状态
		// 机；只有真正观察到 completed 才允许标记触发完成。
		if backfillJob.ID != "" {
			if err := driveOverlayProjectionJobToCompletion(ctx, jobStore, overlayService, backfillJob.ID, "system:query-dependency-backfill"); err != nil {
				return err
			}
		}
		if err := catalogStore.MarkQueryDependencyBackfillTriggered(ctx); err != nil {
			return err
		}
	}
	if err := backupService.Reconcile(ctx); err != nil {
		return err
	}
	if err := jobReconciler.ReconcileOnce(ctx); err != nil {
		return err
	}
	jobReconciler.Start(recoveryContext)
	watcherService.Start(ctx)
	creatorsService, err := creators.New(ctx, store.Control.SQL(), jobStore, catalogStore, systemClock, identity.NewGenerator(systemClock), overlayService)
	if err != nil {
		return err
	}
	webHandler := webapp.New(contractapi.ContractVersion, version.APIVersion)
	handler := httpapi.New(cfg.Mode, store, systemClock, personal, resources, jobStore, catalogStore, scannerService, overlayService, creatorsService, backupService, hub, logger,
		httpapi.Options{Maintenance: maintenanceService, Watcher: watcherService, Scheduler: scheduler, Derived: derivedService, DerivedJob: derivedJobService, AllowedHosts: []string{listener.Addr().String()}, Web: webHandler})
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
	}
	serveError := make(chan error, 1)
	go func() { serveError <- server.Serve(listener) }()

	// runtime descriptor 是外部可观察的就绪信号。只有在数据库、迁移、恢复、reconciliation 与全部
	// 服务装配完成、监听已开始服务后才发布，使「descriptor 存在」等价于「已进入服务状态」；此后
	// 收到的取消都会落到下面的 select 走优雅关闭路径，而不是在启动中被误报为失败。
	runtimeDescriptor, err := descriptor.New(listener.Addr().String())
	if err != nil {
		return err
	}
	descriptorPath, err := descriptor.Publish(cfg.AppDirs.Runtime, runtimeDescriptor)
	if err != nil {
		return err
	}
	defer descriptor.RemoveIfOwned(descriptorPath, runtimeDescriptor.StartupNonce)
	logger.Info("galleryd_started", "address", listener.Addr().String(), "mode", cfg.Mode)
	if ready != nil {
		select {
		case ready <- runtimeDescriptor:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	serving = true
	select {
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return err
		}
		scheduler.Shutdown()
		logger.Info("galleryd_stopped")
		return nil
	case err := <-serveError:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

// driveOverlayProjectionJobToCompletionMaxAttempts 是同步驱动一个 overlay_projection Job
// 到终态的最大循环次数：覆盖"queued 执行一次"“failed 但可重试立即 Retry”与"running/
// publishing 停留状态先 ReconcileAttempts 再重新观察"三类分支各自需要的几轮迭代，避免
// 因未预期的持续异常状态导致启动无界阻塞。
const driveOverlayProjectionJobToCompletionMaxAttempts = 8

// driveOverlayProjectionJobToCompletion 同步把一个 overlay_projection Job 驱动到 completed，
// 用于 v9→v10 查询快照列启动期一次性回填：该 Job 可能是本次调用刚创建的，也可能是
// EnqueueOverlayProjectionTx 合并到的一个既有 queued/running/publishing Job（例如上一次
// 进程崩溃遗留的 running 行，或另一个 Overlay 写入排队的 Job）。无论哪种来源，只有观察
// 到它真正 completed，才能证明"目标 schema generation 对应的查询投影已经成功发布"，调用
// 方才能据此写入回填完成标记；这里复用既有 Job Store 的 Get/Execute/Retry/ReconcileAttempts，
// 不引入独立于既有 Job 状态机之外的第二套等待或超时语义。
func driveOverlayProjectionJobToCompletion(ctx context.Context, jobStore *jobs.Store, overlayService *overlay.Service, jobID, actor string) error {
	for attempt := 0; attempt < driveOverlayProjectionJobToCompletionMaxAttempts; attempt++ {
		job, err := jobStore.Get(ctx, jobID)
		if err != nil {
			return err
		}
		switch job.Status {
		case jobs.StatusCompleted:
			return nil
		case jobs.StatusQueued:
			if err := overlayService.Execute(ctx, jobID); err != nil {
				return err
			}
		case jobs.StatusFailed, jobs.StatusNeedsRepair:
			if !job.FailureRetryable {
				return fault.New(fault.CodeInternal, false,
					fmt.Errorf("查询快照列回填 Job %s 永久失败: %s", jobID, job.IssueCode))
			}
			if _, err := jobStore.Retry(ctx, jobID, actor); err != nil {
				return err
			}
		default:
			// running/publishing/cancelling：此刻调度器与恢复循环均未启动，单线程 bootstrap
			// 不应有并发执行者持有这个 Attempt；出现即说明是上一次进程崩溃遗留的租约，交由
			// 既有 ReconcileAttempts 按租约超时收敛为可重试的 failed 后重新观察。
			if err := jobStore.ReconcileAttempts(ctx, jobLeaseTimeout); err != nil {
				return err
			}
		}
	}
	return fault.New(fault.CodeInternal, true,
		fmt.Errorf("查询快照列回填 Job %s 在 %d 次尝试后仍未完成", jobID, driveOverlayProjectionJobToCompletionMaxAttempts))
}
