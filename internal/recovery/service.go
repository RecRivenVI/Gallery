package recovery

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/RecRivenVI/gallery/internal/jobs"
)

// Submitter 是持久 Job 恢复循环所需的最小 Scheduler 端口。返回 false 时 Job 保持 queued，
// 下一轮继续提交。
type Submitter interface {
	Submit(class, jobID string) bool
}

// Service 统一回收过期租约、推进到期 retry，并把所有资源类别的 queued Job 交回 Scheduler。
// 它不解释业务结果；Scan/Overlay 的 publication Saga 仍由各自服务对账。
type Service struct {
	store        *jobs.Store
	scheduler    Submitter
	interval     time.Duration
	leaseTimeout time.Duration
	wait         sync.WaitGroup
}

func New(store *jobs.Store, scheduler Submitter, interval, leaseTimeout time.Duration) (*Service, error) {
	if store == nil || scheduler == nil {
		return nil, fmt.Errorf("Job Recovery Service 缺少依赖")
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if leaseTimeout <= 0 {
		leaseTimeout = 2 * time.Minute
	}
	return &Service{store: store, scheduler: scheduler, interval: interval, leaseTimeout: leaseTimeout}, nil
}

func (s *Service) ReconcileOnce(ctx context.Context) error {
	if err := s.store.ReconcileAttempts(ctx, s.leaseTimeout); err != nil {
		return err
	}
	if _, err := s.store.RequeueDueFailures(ctx); err != nil {
		return err
	}
	queued, err := s.store.ListRunnable(ctx)
	if err != nil {
		return err
	}
	for _, job := range queued {
		s.scheduler.Submit(job.ResourceClass, job.ID)
	}
	return nil
}

func (s *Service) Start(ctx context.Context) {
	s.wait.Add(1)
	go func() {
		defer s.wait.Done()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = s.ReconcileOnce(ctx)
			}
		}
	}()
}

func (s *Service) Wait() { s.wait.Wait() }
