package jobs

import (
	"context"
	"sync"
)

// Runner 执行一个已入库 Job 的实际工作。它必须尊重传入 context 的取消，并在完成或失败时把
// 终态写回 Job Store；Scheduler 只负责有界调度与生命周期，不解释业务结果。
type Runner func(ctx context.Context, jobID string) error

// Scheduler 是最小的有界 Job 调度器：Job 先入库，再由中央调度按资源类别的独立并发上限领取执行。
// 它替代业务服务直接无限制启动 goroutine，提供每类别并发隔离、context 取消传播、重复领取防护
// 与 graceful shutdown。它不引入外部队列或多进程 worker；崩溃后未完成的 Job 由各服务的启动
// reconciliation 重新入队。
type Scheduler struct {
	rootCtx    context.Context
	rootCancel context.CancelFunc

	mu       sync.Mutex
	closed   bool
	classes  map[string]*schedulerClass
	inflight map[string]context.CancelFunc
	wg       sync.WaitGroup
}

type schedulerClass struct {
	tokens chan struct{}
	runner Runner
}

// NewScheduler 创建一个调度器，其根 context 派生自传入 ctx；ctx 取消或 Shutdown 都会取消所有
// 在执行的 Job。
func NewScheduler(ctx context.Context) *Scheduler {
	rootCtx, rootCancel := context.WithCancel(ctx)
	return &Scheduler{
		rootCtx:    rootCtx,
		rootCancel: rootCancel,
		classes:    make(map[string]*schedulerClass),
		inflight:   make(map[string]context.CancelFunc),
	}
}

// Register 注册一个资源类别及其并发上限与 Runner。limit 至少为 1。必须在 Submit 之前完成注册。
func (s *Scheduler) Register(class string, limit int, runner Runner) {
	if limit < 1 {
		limit = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.classes[class] = &schedulerClass{tokens: make(chan struct{}, limit), runner: runner}
}

// Submit 将一个已入库 Job 交给对应资源类别执行。相同 jobID 若已在执行中则忽略（重复领取防护），
// 未知类别或已 Shutdown 时忽略。Submit 不阻塞：goroutine 会在类别并发上限内等待令牌，取消在等待
// 期间即可生效。
func (s *Scheduler) Submit(class, jobID string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	target, ok := s.classes[class]
	if !ok {
		s.mu.Unlock()
		return
	}
	if _, running := s.inflight[jobID]; running {
		s.mu.Unlock()
		return
	}
	jobCtx, cancel := context.WithCancel(s.rootCtx)
	s.inflight[jobID] = cancel
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			delete(s.inflight, jobID)
			s.mu.Unlock()
			cancel()
		}()
		select {
		case target.tokens <- struct{}{}:
		case <-jobCtx.Done():
			return
		}
		defer func() { <-target.tokens }()
		if jobCtx.Err() != nil {
			return
		}
		_ = target.runner(jobCtx, jobID)
	}()
}

// Cancel 取消一个在执行或等待令牌的 Job 的 context，使其尽快退出。返回该 Job 当前是否在调度器
// 掌管中。Job 的持久状态由调用方通过 Job Store 另行写入。
func (s *Scheduler) Cancel(jobID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel, ok := s.inflight[jobID]; ok {
		cancel()
		return true
	}
	return false
}

// Running 返回某 Job 当前是否在调度器掌管中，供测试与诊断使用。
func (s *Scheduler) Running(jobID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.inflight[jobID]
	return ok
}

// Shutdown 停止接收新 Job，取消所有在执行的 Job 并等待其 goroutine 退出。可重复调用。
func (s *Scheduler) Shutdown() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	s.rootCancel()
	s.wg.Wait()
}
