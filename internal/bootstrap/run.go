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
	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/config"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/contract/realtime"
	"github.com/RecRivenVI/gallery/internal/creators"
	"github.com/RecRivenVI/gallery/internal/derived"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/overlay"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/descriptor"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/platform/lock"
	"github.com/RecRivenVI/gallery/internal/scanner"
	"github.com/RecRivenVI/gallery/internal/storage"
	"github.com/RecRivenVI/gallery/internal/transport/httpapi"
)

// 各资源类别的默认并发上限。scan 允许少量不同 Source 并行；overlay 单活跃投影 Job，取 1 与其
// 单活跃 Job 数据库约束一致。这些值属于运行配置的暂定实装决策，尚未冻结。
const (
	scanConcurrency    = 2
	overlayConcurrency = 1
)

// classDispatcher 把某个资源类别的 Submit 适配为服务侧的 Dispatcher，避免服务依赖调度器具体类型。
type classDispatcher struct {
	scheduler *jobs.Scheduler
	class     string
}

func (d classDispatcher) Submit(jobID string) { d.scheduler.Submit(d.class, jobID) }

func Run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
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

	store, err := storage.Open(ctx, cfg.AppDirs)
	if err != nil {
		return err
	}
	defer store.Close()

	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("监听失败: %w", err)
	}
	defer listener.Close()

	runtimeDescriptor, err := descriptor.New(listener.Addr().String())
	if err != nil {
		return err
	}
	descriptorPath, err := descriptor.Publish(cfg.AppDirs.Runtime, runtimeDescriptor)
	if err != nil {
		return err
	}
	defer descriptor.RemoveIfOwned(descriptorPath, runtimeDescriptor.StartupNonce)

	systemClock := clock.System{}
	personal, err := auth.NewPersonal(store.Control.SQL(), systemClock, identity.NewGenerator(systemClock), nil)
	if err != nil {
		return err
	}
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
	overlayService, err := overlay.New(ctx, store.Control.SQL(), jobStore, catalogStore, systemClock, hub)
	if err != nil {
		return err
	}
	// 中央有界调度器替代业务服务直接启动 goroutine：每类资源独立并发上限，取消随 context 传播，
	// 关闭时取消在执行 Job 并等待退出（未完成 Job 由启动 reconciliation 重新入队）。
	scheduler := jobs.NewScheduler(ctx)
	scheduler.Register("scan", scanConcurrency, scannerService.Execute)
	scheduler.Register("overlay", overlayConcurrency, overlayService.Execute)
	scannerService.SetDispatcher(classDispatcher{scheduler: scheduler, class: "scan"})
	overlayService.SetDispatcher(classDispatcher{scheduler: scheduler, class: "overlay"})
	defer scheduler.Shutdown()

	if err := scannerService.Reconcile(ctx); err != nil {
		return err
	}
	if err := overlayService.Reconcile(ctx); err != nil {
		return err
	}
	creatorsService, err := creators.New(ctx, store.Control.SQL(), jobStore, catalogStore, systemClock, identity.NewGenerator(systemClock), overlayService)
	if err != nil {
		return err
	}
	handler := httpapi.New(cfg.Mode, store, systemClock, personal, resources, jobStore, catalogStore, scannerService, overlayService, creatorsService, hub, logger)
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
	}
	serveError := make(chan error, 1)
	go func() { serveError <- server.Serve(listener) }()
	logger.Info("galleryd_started", "address", listener.Addr().String(), "mode", cfg.Mode)

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
