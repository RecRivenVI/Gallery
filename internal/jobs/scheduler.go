package jobs

import (
	"context"
	"sync"
)

// Runner 执行一个已入库 Job 的实际工作。它必须尊重传入 context 的取消，并在完成或失败时把
// 终态写回 Job Store；Scheduler 只负责有界调度与生命周期，不解释业务结果。
type Runner func(ctx context.Context, jobID string) error

type scheduledItem struct {
	jobID  string
	ctx    context.Context
	cancel context.CancelFunc
}

type schedulerClass struct {
	queue     chan scheduledItem
	runner    Runner
	limit     int
	running   int
	submitted uint64
	completed uint64
	cancelled uint64
}

// ClassSnapshot 是诊断用的资源池快照。Queued/Running 只反映调度器内存队列，不替代持久
// Job 状态；进程重启后的事实仍由 Job Store reconciliation 提供。
type ClassSnapshot struct {
	Class     string
	Limit     int
	Queued    int
	Running   int
	Submitted uint64
	Completed uint64
	Cancelled uint64
}

// Scheduler 是中央有界 Job 调度器：每个资源类别拥有独立队列和 worker，防止扫描、哈希、派生、
// 外部工具或维护任务互相占满并发槽。它不引入外部队列；崩溃后未完成的 Job 由启动 reconciliation
// 重新入队。
type Scheduler struct {
	rootCtx    context.Context
	rootCancel context.CancelFunc

	mu       sync.Mutex
	closed   bool
	classes  map[string]*schedulerClass
	inflight map[string]*scheduledItem
	wg       sync.WaitGroup
}

func NewScheduler(ctx context.Context) *Scheduler {
	rootCtx, rootCancel := context.WithCancel(ctx)
	return &Scheduler{rootCtx: rootCtx, rootCancel: rootCancel, classes: make(map[string]*schedulerClass), inflight: make(map[string]*scheduledItem)}
}

// Register 注册资源类别及并发上限。队列容量是并发上限的固定倍数，保持有界；入队超过容量时
// Submit 会放弃本次调度，持久 Job 仍保持 queued，可由下一次 reconciliation 再次提交。
func (s *Scheduler) Register(class string, limit int, runner Runner) {
	if class == "" || runner == nil {
		return
	}
	if limit < 1 {
		limit = 1
	}
	queueSize := limit * 64
	if queueSize < 16 {
		queueSize = 16
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if old, exists := s.classes[class]; exists {
		// 正式启动只注册一次；重复注册时保留已启动的 worker，避免遗失队列中的 Job。
		if old.runner != nil {
			s.mu.Unlock()
			return
		}
	}
	target := &schedulerClass{queue: make(chan scheduledItem, queueSize), runner: runner, limit: limit}
	s.classes[class] = target
	for i := 0; i < limit; i++ {
		s.wg.Add(1)
		go s.worker(target)
	}
	s.mu.Unlock()
}

func (s *Scheduler) worker(target *schedulerClass) {
	defer s.wg.Done()
	for {
		select {
		case item := <-target.queue:
			if item.ctx.Err() != nil {
				s.finish(target, item, false)
				continue
			}
			s.mu.Lock()
			target.running++
			s.mu.Unlock()
			err := target.runner(item.ctx, item.jobID)
			s.mu.Lock()
			if target.running > 0 {
				target.running--
			}
			s.mu.Unlock()
			s.finish(target, item, err == nil && item.ctx.Err() == nil)
		case <-s.rootCtx.Done():
			// 清理当前类别仍在队列中的 item，使 Cancel/Running 不在关闭后永久泄漏。
			for {
				select {
				case item := <-target.queue:
					s.finish(target, item, false)
				default:
					return
				}
			}
		}
	}
}

func (s *Scheduler) finish(target *schedulerClass, item scheduledItem, completed bool) {
	s.mu.Lock()
	if current, ok := s.inflight[item.jobID]; ok && current.ctx == item.ctx {
		delete(s.inflight, item.jobID)
	}
	if completed {
		target.completed++
	} else {
		target.cancelled++
	}
	s.mu.Unlock()
	item.cancel()
}

// Submit 不阻塞；同一 jobID 在调度器内只能有一个排队或运行实例。
func (s *Scheduler) Submit(class, jobID string) {
	if jobID == "" {
		return
	}
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
	item := scheduledItem{jobID: jobID, ctx: jobCtx, cancel: cancel}
	s.inflight[jobID] = &item
	target.submitted++
	s.mu.Unlock()
	select {
	case target.queue <- item:
	case <-s.rootCtx.Done():
		s.mu.Lock()
		delete(s.inflight, jobID)
		target.cancelled++
		s.mu.Unlock()
		cancel()
	}
}

// Cancel 取消内存中的 context；调用方仍需通过 Job Store 写入持久取消请求。
func (s *Scheduler) Cancel(jobID string) bool {
	s.mu.Lock()
	item, ok := s.inflight[jobID]
	s.mu.Unlock()
	if ok {
		item.cancel()
	}
	return ok
}

func (s *Scheduler) Running(jobID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.inflight[jobID]
	return ok
}

func (s *Scheduler) Snapshot() []ClassSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]ClassSnapshot, 0, len(s.classes))
	for class, target := range s.classes {
		result = append(result, ClassSnapshot{Class: class, Limit: target.limit, Queued: len(target.queue), Running: target.running,
			Submitted: target.submitted, Completed: target.completed, Cancelled: target.cancelled})
	}
	return result
}

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
