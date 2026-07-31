package query

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestPageInFlightGroupCoalescesAndClonesConcurrentPage(t *testing.T) {
	group := newPageInFlightGroup()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	type outcome struct {
		items []Work
		more  bool
		err   error
	}
	results := make(chan outcome, 8)
	for range 8 {
		go func() {
			items, more, err := group.do(context.Background(), "same", func(context.Context) ([]Work, bool, error) {
				if calls.Add(1) == 1 {
					close(started)
				}
				<-release
				instant := time.Unix(123, 0).UTC()
				return []Work{{ID: "wrk", Tags: []string{"tag"}, Matches: []FieldMatch{{Field: "title", Spans: []MatchSpan{{Start: 1, End: 2}}}}, PublishedAt: &instant}}, true, nil
			})
			results <- outcome{items: items, more: more, err: err}
		}()
	}
	<-started
	waitForPageWaiters(t, group, "same", 8)
	close(release)

	collected := make([]outcome, 0, 8)
	for range 8 {
		collected = append(collected, <-results)
	}
	if calls.Load() != 1 {
		t.Fatalf("相同在途页实际执行 %d 次", calls.Load())
	}
	for _, result := range collected {
		if result.err != nil || !result.more || len(result.items) != 1 || result.items[0].ID != "wrk" {
			t.Fatalf("合并结果错误: %+v", result)
		}
	}
	collected[0].items[0].Tags[0] = "mutated"
	collected[0].items[0].Matches[0].Spans[0].End = 99
	*collected[0].items[0].PublishedAt = time.Time{}
	if collected[1].items[0].Tags[0] != "tag" || collected[1].items[0].Matches[0].Spans[0].End != 2 || collected[1].items[0].PublishedAt.Unix() != 123 {
		t.Fatalf("调用方改写污染了其它 waiter: %+v", collected[1].items[0])
	}
}

func TestPageInFlightGroupKeepsBuildUntilLastWaiterCancels(t *testing.T) {
	group := newPageInFlightGroup()
	started := make(chan struct{})
	release := make(chan struct{})
	buildCancelled := make(chan struct{})
	build := func(ctx context.Context) ([]Work, bool, error) {
		select {
		case <-started:
		default:
			close(started)
		}
		select {
		case <-release:
			return []Work{{ID: "wrk"}}, false, nil
		case <-ctx.Done():
			close(buildCancelled)
			return nil, false, ctx.Err()
		}
	}

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() { _, _, err := group.do(firstCtx, "same", build); first <- err }()
	<-started
	go func() { _, _, err := group.do(secondCtx, "same", build); second <- err }()
	waitForPageWaiters(t, group, "same", 2)

	cancelFirst()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("首个 waiter 取消错误=%v", err)
	}
	select {
	case <-buildCancelled:
		t.Fatal("仍有第二个 waiter 时 build 被提前取消")
	default:
	}
	cancelSecond()
	if err := <-second; !errors.Is(err, context.Canceled) {
		t.Fatalf("第二个 waiter 取消错误=%v", err)
	}
	select {
	case <-buildCancelled:
	case <-time.After(time.Second):
		t.Fatal("最后一个 waiter 取消后 build 未停止")
	}
}

func TestPageInFlightGroupDoesNotRetainResultOrExceedLimit(t *testing.T) {
	group := newPageInFlightGroup()
	var calls atomic.Int32
	build := func(context.Context) ([]Work, bool, error) {
		calls.Add(1)
		return []Work{{ID: "wrk"}}, false, nil
	}
	for range 2 {
		if _, _, err := group.do(context.Background(), "same", build); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("完成的页面不应持久缓存，build calls=%d want=2", calls.Load())
	}

	group.entryLimit = 0
	if _, _, err := group.do(context.Background(), "overflow", build); err != nil {
		t.Fatal(err)
	}
	group.mu.Lock()
	entryCount := len(group.entries)
	group.mu.Unlock()
	if entryCount != 0 {
		t.Fatalf("达到硬上限仍登记 entry: %d", entryCount)
	}
}

func waitForPageWaiters(t *testing.T, group *pageInFlightGroup, key string, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		group.mu.Lock()
		entry := group.entries[key]
		got := 0
		if entry != nil {
			got = entry.waiters
		}
		group.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("等待同一页 waiter=%d 超时", want)
}
