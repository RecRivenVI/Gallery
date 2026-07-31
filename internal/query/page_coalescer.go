package query

import (
	"context"
	"slices"
	"sync"
)

const pageInFlightLimit = 256

// pageInFlightGroup 只合并当前仍在执行的完全相同页查询，不在请求完成后缓存页面。
// 页查询绑定 immutable publication，但单页负载可能很大；只做 in-flight 合并既能避免
// 多标签页/重连风暴同时重复排序同一候选集，也不会让 256 个历史页面长期占用内存。
//
// build 使用独立于任一单个请求取消的 context；只要仍有一个 waiter，最先取消的请求
// 就不能使其它合法请求失败。全部 waiter 都取消后立即取消 SQL。entry 数量有硬上限，
// 上限外的不同查询直接按各自请求 context 执行，不会临时突破 map 上限。
type pageInFlightGroup struct {
	mu         sync.Mutex
	entries    map[string]*pageInFlightEntry
	entryLimit int
}

type pageInFlightEntry struct {
	ready   chan struct{}
	cancel  context.CancelFunc
	waiters int
	done    bool
	items   []Work
	more    bool
	err     error
}

func newPageInFlightGroup() *pageInFlightGroup {
	return &pageInFlightGroup{entries: make(map[string]*pageInFlightEntry), entryLimit: pageInFlightLimit}
}

func (g *pageInFlightGroup) do(ctx context.Context, key string, build func(context.Context) ([]Work, bool, error)) ([]Work, bool, error) {
	g.mu.Lock()
	if entry, exists := g.entries[key]; exists {
		entry.waiters++
		g.mu.Unlock()
		return g.wait(ctx, key, entry)
	}
	if len(g.entries) >= g.entryLimit {
		g.mu.Unlock()
		items, more, err := build(ctx)
		return cloneWorks(items), more, err
	}

	buildCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	entry := &pageInFlightEntry{ready: make(chan struct{}), cancel: cancel, waiters: 1}
	g.entries[key] = entry
	g.mu.Unlock()

	go g.run(key, entry, buildCtx, build)
	return g.wait(ctx, key, entry)
}

func (g *pageInFlightGroup) run(key string, entry *pageInFlightEntry, ctx context.Context, build func(context.Context) ([]Work, bool, error)) {
	items, more, err := build(ctx)
	g.mu.Lock()
	entry.items, entry.more, entry.err, entry.done = items, more, err, true
	if g.entries[key] == entry {
		delete(g.entries, key)
	}
	close(entry.ready)
	entry.cancel()
	g.mu.Unlock()
}

func (g *pageInFlightGroup) wait(ctx context.Context, key string, entry *pageInFlightEntry) ([]Work, bool, error) {
	select {
	case <-entry.ready:
		return cloneWorks(entry.items), entry.more, entry.err
	case <-ctx.Done():
		g.mu.Lock()
		if !entry.done {
			entry.waiters--
			if entry.waiters == 0 {
				if g.entries[key] == entry {
					delete(g.entries, key)
				}
				entry.cancel()
			}
		}
		g.mu.Unlock()
		return nil, false, ctx.Err()
	}
}

func cloneWorks(items []Work) []Work {
	if items == nil {
		return nil
	}
	cloned := make([]Work, len(items))
	for index, item := range items {
		cloned[index] = item
		cloned[index].Tags = slices.Clone(item.Tags)
		cloned[index].Badges = slices.Clone(item.Badges)
		if item.Matches != nil {
			cloned[index].Matches = make([]FieldMatch, len(item.Matches))
			for matchIndex, match := range item.Matches {
				cloned[index].Matches[matchIndex] = match
				cloned[index].Matches[matchIndex].Spans = slices.Clone(match.Spans)
			}
		}
		if item.PublishedAt != nil {
			instant := *item.PublishedAt
			cloned[index].PublishedAt = &instant
		}
	}
	return cloned
}
