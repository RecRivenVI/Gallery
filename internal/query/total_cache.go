package query

import (
	"context"
	"sync"
)

const totalResultCacheLimit = 256

// totalResultCache 只缓存不可变 publication 上、同一授权 Source 集合与同一查询语义的
// Total。首次并发 miss 由一个请求计算，其余请求等待同一结果，避免 16 个相同请求同时
// 重复扫描 FTS/结构化过滤。条目数严格有界；错误和取消不缓存。
type totalResultCache struct {
	mu         sync.Mutex
	clock      uint64
	entries    map[string]*totalResultCacheEntry
	entryLimit int
}

type totalResultCacheEntry struct {
	ready    chan struct{}
	building bool
	lastUsed uint64
	result   TotalInfo
	err      error
}

func newTotalResultCache() *totalResultCache {
	return &totalResultCache{entries: make(map[string]*totalResultCacheEntry), entryLimit: totalResultCacheLimit}
}

func (c *totalResultCache) get(ctx context.Context, key string, build func(context.Context) (TotalInfo, error)) (TotalInfo, error) {
	c.mu.Lock()
	c.clock++
	entry, exists := c.entries[key]
	if exists {
		entry.lastUsed = c.clock
		ready := entry.ready
		c.mu.Unlock()
		select {
		case <-ready:
		case <-ctx.Done():
			return TotalInfo{}, ctx.Err()
		}
		return cloneTotalInfo(entry.result), entry.err
	}
	if !c.makeRoomLocked() {
		c.mu.Unlock()
		result, err := build(ctx)
		return cloneTotalInfo(result), err
	}

	entry = &totalResultCacheEntry{ready: make(chan struct{}), building: true, lastUsed: c.clock}
	c.entries[key] = entry
	c.mu.Unlock()

	result, err := build(ctx)
	c.mu.Lock()
	entry.result = cloneTotalInfo(result)
	entry.err = err
	entry.building = false
	close(entry.ready)
	if err != nil {
		if c.entries[key] == entry {
			delete(c.entries, key)
		}
	}
	c.mu.Unlock()
	return cloneTotalInfo(result), err
}

func (c *totalResultCache) makeRoomLocked() bool {
	if len(c.entries) < c.entryLimit {
		return true
	}
	var oldestKey string
	var oldest *totalResultCacheEntry
	for key, entry := range c.entries {
		if entry.building {
			continue
		}
		if oldest == nil || entry.lastUsed < oldest.lastUsed {
			oldestKey, oldest = key, entry
		}
	}
	if oldest == nil {
		return false
	}
	delete(c.entries, oldestKey)
	return true
}

func cloneTotalInfo(value TotalInfo) TotalInfo {
	result := value
	if value.Value != nil {
		copied := *value.Value
		result.Value = &copied
	}
	return result
}
