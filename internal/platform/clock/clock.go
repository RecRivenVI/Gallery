package clock

import (
	"sync"
	"time"
)

type System struct{}

func (System) Now() time.Time { return time.Now().UTC() }

type Fixed struct{ Time time.Time }

func (f Fixed) Now() time.Time { return f.Time }

// Manual 是可推进的线程安全时钟，供需要确定性验证退避到期的测试在同一时间线上共享。
// 生产代码始终使用 System；测试通过 Advance 显式前进时间，不依赖真实 time.Sleep 等待。
type Manual struct {
	mu  sync.Mutex
	now time.Time
}

func NewManual(start time.Time) *Manual {
	return &Manual{now: start}
}

func (m *Manual) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.now
}

func (m *Manual) Advance(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.now = m.now.Add(d)
}
