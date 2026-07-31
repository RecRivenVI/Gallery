package query

import (
	"context"
	"database/sql"
	"hash/maphash"
	"strings"
	"sync"
)

const exactValueIndexCacheLimit = 2

// exactValueIndexCache 是 publication 级、进程内的“某个规范化字段值是否存在”集合。
// 它只保存不可逆的随机种子 64-bit hash，不保存标题、Creator、Tag 或文件名原文；hash
// 碰撞最多产生一次保守回退，绝不会把真实 exact 命中误判为不存在。缓存有严格条目上限，
// 历史游标触发的 publication 轮换不能无界占用内存。
type exactValueIndexCache struct {
	mu         sync.Mutex
	seed       maphash.Seed
	clock      uint64
	entries    map[string]*exactValueIndexEntry
	entryLimit int
}

type exactValueIndexEntry struct {
	ready    chan struct{}
	building bool
	lastUsed uint64
	values   map[uint64]struct{}
	err      error
}

func newExactValueIndexCache() *exactValueIndexCache {
	return &exactValueIndexCache{
		seed: maphash.MakeSeed(), entries: make(map[string]*exactValueIndexEntry),
		entryLimit: exactValueIndexCacheLimit,
	}
}

func exactValueIndexKey(pub publication) string {
	return pub.CatalogRevision + "\x00" + pub.OverlayRevision
}

func (c *exactValueIndexCache) contains(ctx context.Context, db *sql.DB, pub publication, normalizedValue string) (bool, error) {
	key := exactValueIndexKey(pub)
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
			return false, ctx.Err()
		}
		if entry.err != nil {
			return false, entry.err
		}
		_, found := entry.values[maphash.String(c.seed, normalizedValue)]
		return found, nil
	}

	entry = &exactValueIndexEntry{ready: make(chan struct{}), building: true, lastUsed: c.clock}
	c.entries[key] = entry
	c.trimLocked()
	c.mu.Unlock()

	values, err := buildExactValueIndex(ctx, db, pub, c.seed)
	c.mu.Lock()
	entry.values = values
	entry.err = err
	entry.building = false
	close(entry.ready)
	if err != nil {
		if c.entries[key] == entry {
			delete(c.entries, key)
		}
	} else {
		c.trimLocked()
	}
	c.mu.Unlock()
	if err != nil {
		return false, err
	}
	_, found := values[maphash.String(c.seed, normalizedValue)]
	return found, nil
}

func (c *exactValueIndexCache) trimLocked() {
	for len(c.entries) > c.entryLimit {
		var oldestKey string
		var oldest *exactValueIndexEntry
		for key, entry := range c.entries {
			if entry.building {
				continue
			}
			if oldest == nil || entry.lastUsed < oldest.lastUsed {
				oldestKey, oldest = key, entry
			}
		}
		if oldest == nil {
			return
		}
		delete(c.entries, oldestKey)
	}
}

func buildExactValueIndex(ctx context.Context, db *sql.DB, pub publication, seed maphash.Seed) (map[uint64]struct{}, error) {
	rows, err := db.QueryContext(ctx, `SELECT search_title_norm, search_creator_norm,
search_tags_norm, search_filenames_norm
FROM work_search_candidates
WHERE catalog_revision_id=? AND overlay_revision_id=? AND hidden=0`, pub.CatalogRevision, pub.OverlayRevision)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make(map[uint64]struct{})
	add := func(value string) {
		if value != "" {
			values[maphash.String(seed, value)] = struct{}{}
		}
	}
	for rows.Next() {
		var title, creator, tags, filenames string
		if err := rows.Scan(&title, &creator, &tags, &filenames); err != nil {
			return nil, err
		}
		add(title)
		add(creator)
		for _, value := range strings.Split(tags, "\x1f") {
			add(value)
		}
		for _, value := range strings.Split(filenames, "\x1f") {
			add(value)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return values, nil
}
