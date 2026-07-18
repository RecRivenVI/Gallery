package maintenance

import "sync"

// Coordinator 把 Catalog publication 与排他维护操作放进同一进程内互斥边界。
// publication 可并发；checkpoint、VACUUM 与 GC 使用排他锁。
type Coordinator struct {
	mu sync.RWMutex
}

func NewCoordinator() *Coordinator { return &Coordinator{} }

func (c *Coordinator) AcquirePublication() func() {
	if c == nil {
		return func() {}
	}
	c.mu.RLock()
	return c.mu.RUnlock
}

func (c *Coordinator) AcquireMaintenance() func() {
	if c == nil {
		return func() {}
	}
	c.mu.Lock()
	return c.mu.Unlock
}
