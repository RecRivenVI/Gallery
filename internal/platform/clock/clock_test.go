package clock_test

import (
	"sync"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/ports"
)

// 本包是全仓库唯一的时间入口：Job 租约到期、退避重试、Session 过期、publication 租约与
// UUIDv7 的时间前缀都从这里取时刻。因此这里断言的是三件事——System 的时区与单调读性质、
// Fixed 的完全确定性、Manual 的线程安全与可逆推进，以及三者都满足 ports.Clock。
var (
	_ ports.Clock = clock.System{}
	_ ports.Clock = clock.Fixed{}
	_ ports.Clock = (*clock.Manual)(nil)
)

// TestSystemNowIsUTC 断言 System 返回的时刻位于 UTC。
//
// 全仓库的持久化时间戳与 UUIDv7 前缀都假定这一点。如果这里返回本地时区，格式化后的
// 时间戳会随运行机器的时区变化，而同一份 control.db 在不同机器上读出的语义就不一致了。
func TestSystemNowIsUTC(t *testing.T) {
	now := clock.System{}.Now()
	if now.Location() != time.UTC {
		t.Fatalf("System.Now 返回的时区是 %v，不是 UTC", now.Location())
	}
	if now.IsZero() {
		t.Fatal("System.Now 返回零值时刻")
	}
}

// TestSystemNowCarriesNoMonotonicReading 断言 System.Now 不携带单调时钟读数，并说明
// 由此产生的边界。
//
// `time.Now().UTC()` 会剥离单调读数，这不是可有可无的细节：两个 Clock.Now() 相减得到的
// 差值因此是**墙钟差**，会被 NTP 步进、休眠唤醒和用户改系统时间影响，可能为负。
// 依赖"至少经过了 N 秒"的逻辑（租约超时、退避）必须能容忍这一点；真正需要不受墙钟影响的
// 计时应当直接使用 time.Since/time.Timer，而不是把它错当成本 Clock 的保证。这条断言把
// 这个边界固定下来：如果哪天有人去掉 .UTC() 让单调读数回来，语义会静默改变，这里会先失败。
func TestSystemNowCarriesNoMonotonicReading(t *testing.T) {
	now := clock.System{}.Now()
	// Round(0) 的唯一作用就是剥离单调读数；剥离前后完全相等即证明本来就没有。
	if now != now.Round(0) {
		t.Fatalf("System.Now 携带了单调时钟读数：%s", now)
	}
}

// TestSystemNowAdvances 断言 System 真的跟随真实时间前进，且不倒退。
func TestSystemNowAdvances(t *testing.T) {
	first := clock.System{}.Now()
	time.Sleep(2 * time.Millisecond)
	second := clock.System{}.Now()
	if second.Before(first) {
		t.Fatalf("System.Now 倒退：%s 之后是 %s", first, second)
	}
	if !second.After(first) {
		t.Fatalf("休眠 2 毫秒后 System.Now 没有前进：%s", first)
	}
}

// TestFixedIsFullyDeterministic 断言 Fixed 每次返回完全相同的时刻，包括时区与纳秒。
// 依赖固定时钟做确定性断言的测试（例如规则身份、cursor 编码）建立在这一点上。
func TestFixedIsFullyDeterministic(t *testing.T) {
	moment := time.Date(2026, 7, 27, 3, 4, 5, 678_901_234, time.UTC)
	fixed := clock.Fixed{Time: moment}
	for i := 0; i < 3; i++ {
		if got := fixed.Now(); !got.Equal(moment) || got != moment {
			t.Fatalf("第 %d 次调用返回 %s，期望 %s", i, got, moment)
		}
	}
	// Fixed 不做任何规范化：给什么时区就返回什么时区。调用方若需要 UTC 必须自己传入，
	// 而不能指望 Fixed 代为转换。
	local := time.Date(2026, 7, 27, 3, 4, 5, 0, time.FixedZone("test", 8*60*60))
	if got := (clock.Fixed{Time: local}).Now(); got.Location() != local.Location() {
		t.Fatalf("Fixed 改写了时区：%v", got.Location())
	}
}

// TestManualStartsAtGivenTimeAndAdvances 断言 Manual 从给定起点开始，只在显式 Advance
// 时前进，且完全不受真实时间影响。
func TestManualStartsAtGivenTimeAndAdvances(t *testing.T) {
	start := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	manual := clock.NewManual(start)
	if got := manual.Now(); !got.Equal(start) {
		t.Fatalf("初始时刻是 %s，期望 %s", got, start)
	}
	time.Sleep(2 * time.Millisecond)
	if got := manual.Now(); !got.Equal(start) {
		t.Fatalf("真实时间流逝后 Manual 自行前进到 %s", got)
	}
	manual.Advance(90 * time.Second)
	if got, want := manual.Now(), start.Add(90*time.Second); !got.Equal(want) {
		t.Fatalf("Advance 后是 %s，期望 %s", got, want)
	}
	// 负向 Advance 必须真实回拨。验证时钟回拨行为的测试依赖这一点，Manual 不得把它
	// 悄悄夹取为不后退。
	manual.Advance(-30 * time.Second)
	if got, want := manual.Now(), start.Add(60*time.Second); !got.Equal(want) {
		t.Fatalf("负向 Advance 后是 %s，期望 %s", got, want)
	}
}

// TestManualIsSafeForConcurrentUse 断言 Manual 在并发读写下既无数据竞争，也不丢失推进量。
// Manual 的存在意义就是"让多个协作组件共享同一条可控时间线"，因此并发安全是它的核心契约；
// 本用例在 -race 下同时充当竞态回归护栏。
func TestManualIsSafeForConcurrentUse(t *testing.T) {
	start := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	manual := clock.NewManual(start)

	// goroutine 数固定且小：要证明的是"并发推进不丢失、并发读不撕裂"，这只需要多个
	// goroutine 同时在场，不需要按核数放大。总推进量由每个 goroutine 的循环次数承担。
	const advancers = 4
	const stepsPerAdvancer = 1000
	const readers = 2

	var group sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < readers; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if got := manual.Now(); got.Before(start) {
					t.Errorf("并发读取到早于起点的时刻 %s", got)
					return
				}
			}
		}()
	}
	for i := 0; i < advancers; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for step := 0; step < stepsPerAdvancer; step++ {
				manual.Advance(time.Millisecond)
			}
		}()
	}
	// 先等推进者完成，再停读者，保证读者始终与推进并发。
	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()
	time.Sleep(time.Millisecond)
	close(stop)
	<-done

	want := start.Add(advancers * stepsPerAdvancer * time.Millisecond)
	if got := manual.Now(); !got.Equal(want) {
		t.Fatalf("并发推进后是 %s，期望 %s：存在丢失的 Advance", got, want)
	}
}

// TestManualPreservesLocation 断言 Manual 不改写起点时区，使基于 Manual 的断言可以直接
// 与期望时刻做 == 比较而不必先做时区归一。
func TestManualPreservesLocation(t *testing.T) {
	start := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	manual := clock.NewManual(start)
	manual.Advance(time.Hour)
	if got := manual.Now(); got.Location() != time.UTC {
		t.Fatalf("Advance 后时区变为 %v", got.Location())
	}
}
