package query

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

func TestTotalResultCacheCoalescesConcurrentMissAndClonesResult(t *testing.T) {
	cache := newTotalResultCache()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	results := make(chan TotalInfo, 8)
	errs := make(chan error, 8)
	for range 8 {
		go func() {
			result, err := cache.get(context.Background(), "same", func(context.Context) (TotalInfo, error) {
				if calls.Add(1) == 1 {
					close(started)
				}
				<-release
				value := int64(42)
				return TotalInfo{Mode: TotalModeExact, Value: &value, ProtocolVersion: TotalProtocolVersion}, nil
			})
			results <- result
			errs <- err
		}()
	}
	<-started
	close(release)
	for range 8 {
		result, err := <-results, <-errs
		if err != nil || result.Value == nil || *result.Value != 42 {
			t.Fatalf("并发缓存结果错误: result=%+v err=%v", result, err)
		}
		*result.Value = 99
	}
	if calls.Load() != 1 {
		t.Fatalf("相同 miss 实际执行 %d 次", calls.Load())
	}
	result, err := cache.get(context.Background(), "same", func(context.Context) (TotalInfo, error) {
		t.Fatal("命中缓存时不应再次执行 builder")
		return TotalInfo{}, nil
	})
	if err != nil || result.Value == nil || *result.Value != 42 {
		t.Fatalf("缓存值被调用方指针改写: result=%+v err=%v", result, err)
	}
}

func TestTotalResultCacheDoesNotRetainErrorsAndEvictsOldest(t *testing.T) {
	cache := newTotalResultCache()
	cache.entryLimit = 2
	wantErr := errors.New("transient")
	if _, err := cache.get(context.Background(), "retry", func(context.Context) (TotalInfo, error) {
		return TotalInfo{}, wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("首次错误=%v", err)
	}
	value := int64(1)
	if result, err := cache.get(context.Background(), "retry", func(context.Context) (TotalInfo, error) {
		return TotalInfo{Mode: TotalModeExact, Value: &value}, nil
	}); err != nil || result.Value == nil || *result.Value != 1 {
		t.Fatalf("错误后未重新建立: result=%+v err=%v", result, err)
	}
	for _, key := range []string{"second", "third"} {
		if _, err := cache.get(context.Background(), key, func(context.Context) (TotalInfo, error) {
			return TotalInfo{Mode: TotalModeOmitted}, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.entries) != 2 {
		t.Fatalf("缓存条目数=%d want=2", len(cache.entries))
	}
	if _, exists := cache.entries["retry"]; exists {
		t.Fatal("最旧条目未被淘汰")
	}
}

func TestTotalResultCacheDoesNotExceedLimitWhenAllEntriesBuild(t *testing.T) {
	cache := newTotalResultCache()
	cache.entryLimit = 1
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := cache.get(context.Background(), "first", func(context.Context) (TotalInfo, error) {
			close(started)
			<-release
			return TotalInfo{Mode: TotalModeOmitted}, nil
		})
		done <- err
	}()
	<-started

	if _, err := cache.get(context.Background(), "uncached", func(context.Context) (TotalInfo, error) {
		return TotalInfo{Mode: TotalModeOmitted}, nil
	}); err != nil {
		t.Fatal(err)
	}
	cache.mu.Lock()
	entryCount := len(cache.entries)
	_, cachedOverflow := cache.entries["uncached"]
	cache.mu.Unlock()

	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if entryCount != 1 {
		t.Fatalf("构建中的缓存条目数=%d want=1", entryCount)
	}
	if cachedOverflow {
		t.Fatal("达到硬上限时仍缓存了新键")
	}
}
