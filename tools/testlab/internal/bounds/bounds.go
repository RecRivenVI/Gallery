// Package bounds 定义真实 Source 验证的显式边界与「因边界停止」的如实报告。
//
// 有界运行的价值全在于**区分两种结束**：跑完了，和撞到边界停下了。把后者报告成前者
// 会让一份只覆盖了几百个目录的运行看起来像是覆盖了整个来源。因此本包不提供「静默截断」
// 的入口：任何一次触顶都会留下 Reason，调用方必须把它带进报告。
package bounds

import (
	"fmt"
	"time"
)

// 停止原因。Reason 为空表示自然跑完。
const (
	ReasonNone          = ""
	ReasonMaxDirs       = "max_directories"
	ReasonMaxFiles      = "max_files"
	ReasonWallClock     = "max_wall_clock"
	ReasonMaxHashFiles  = "max_hash_files"
	ReasonMaxHashBytes  = "max_hash_bytes"
	ReasonMaxMediaItems = "max_media_items"
)

// Limits 是一次有界运行的显式上限。零值表示该维度不设限。
type Limits struct {
	MaxDirs      int
	MaxFiles     int
	MaxWallClock time.Duration
}

// Unlimited 报告本组上限是否完全不设限。真实 Source 上必须至少设一项；全部为零的
// "有界模式"是名不副实的。
func (l Limits) Unlimited() bool {
	return l.MaxDirs <= 0 && l.MaxFiles <= 0 && l.MaxWallClock <= 0
}

// Describe 给出可写进报告的边界描述（只含数字与时长，不含任何路径）。
func (l Limits) Describe() string {
	return fmt.Sprintf("maxDirs=%d maxFiles=%d maxWallClock=%s", l.MaxDirs, l.MaxFiles, l.MaxWallClock)
}

// Budget 跟踪一次有界运行的消耗。它不是线程安全的：调用方在单个遍历循环内使用。
type Budget struct {
	limits   Limits
	deadline time.Time
	now      func() time.Time

	Dirs   int
	Files  int
	Reason string
}

// Start 按当前时刻开启一次预算。now 可注入，便于测试墙钟边界而不真的等待。
func (l Limits) Start(now func() time.Time) *Budget {
	if now == nil {
		now = time.Now
	}
	budget := &Budget{limits: l, now: now}
	if l.MaxWallClock > 0 {
		budget.deadline = now().Add(l.MaxWallClock)
	}
	return budget
}

// Stopped 报告预算是否已经因某条边界停止。
func (b *Budget) Stopped() bool { return b.Reason != ReasonNone }

// Elapsed 报告距离开始已经过去多久；未设墙钟上限时返回 0。
func (b *Budget) Deadline() (time.Time, bool) { return b.deadline, !b.deadline.IsZero() }

// CheckWallClock 只检查墙钟，不消耗计数配额。长任务应在每次轮询时调用。
func (b *Budget) CheckWallClock() bool {
	if b.Stopped() {
		return false
	}
	if !b.deadline.IsZero() && !b.now().Before(b.deadline) {
		b.Reason = ReasonWallClock
		return false
	}
	return true
}

// AddDir 记入一个目录，返回是否还可以继续。
func (b *Budget) AddDir() bool {
	if !b.CheckWallClock() {
		return false
	}
	if b.limits.MaxDirs > 0 && b.Dirs >= b.limits.MaxDirs {
		b.Reason = ReasonMaxDirs
		return false
	}
	b.Dirs++
	return true
}

// AddFile 记入一个文件，返回是否还可以继续。
func (b *Budget) AddFile() bool {
	if !b.CheckWallClock() {
		return false
	}
	if b.limits.MaxFiles > 0 && b.Files >= b.limits.MaxFiles {
		b.Reason = ReasonMaxFiles
		return false
	}
	b.Files++
	return true
}

// Outcome 是一次有界运行的结论。Completed 为假时 Reason 必然非空。
type Outcome struct {
	Completed bool   `json:"completed"`
	Reason    string `json:"stoppedByBound,omitempty"`
	Dirs      int    `json:"directories"`
	Files     int    `json:"files"`
	ElapsedMs int64  `json:"elapsedMs"`
}

// Outcome 结算本次预算。elapsed 由调用方测得。
func (b *Budget) Outcome(elapsed time.Duration) Outcome {
	return Outcome{
		Completed: !b.Stopped(), Reason: b.Reason,
		Dirs: b.Dirs, Files: b.Files, ElapsedMs: elapsed.Milliseconds(),
	}
}

// Summary 给出一句可写进报告的结论：跑完，还是因为哪条边界停下。
func (o Outcome) Summary() string {
	if o.Completed {
		return fmt.Sprintf("completed dirs=%d files=%d elapsedMs=%d", o.Dirs, o.Files, o.ElapsedMs)
	}
	return fmt.Sprintf("stopped-by-bound reason=%s dirs=%d files=%d elapsedMs=%d", o.Reason, o.Dirs, o.Files, o.ElapsedMs)
}
