package watcher

import (
	"context"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/ports"
)

type Polling struct {
	interval  time.Duration
	maxEvents int
}

func NewPolling(interval time.Duration, maxEvents int) *Polling {
	if interval <= 0 {
		// Polling 是原生 Watcher 不可用时的低频完整校验 fallback，默认不得在 HDD/NAS
		// 上形成高频递归全树 I/O。
		interval = 5 * time.Minute
	}
	if maxEvents < 1 {
		maxEvents = 4096
	}
	return &Polling{interval: interval, maxEvents: maxEvents}
}

type stamp struct {
	size     int64
	mode     fs.FileMode
	modified int64
}

func (p *Polling) Watch(ctx context.Context, root string) (<-chan ports.WatchEvent, error) {
	before, err := snapshot(root)
	if err != nil {
		return nil, err
	}
	events := make(chan ports.WatchEvent, p.maxEvents)
	go func() {
		defer close(events)
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				after, scanErr := snapshot(root)
				if scanErr != nil {
					if !send(ctx, events, ports.WatchEvent{Kind: ports.WatchOverflow, Overflow: true, At: now.UTC()}) {
						return
					}
					continue
				}
				changes := diff(before, after, now.UTC())
				if len(changes) > p.maxEvents {
					changes = []ports.WatchEvent{{Kind: ports.WatchOverflow, Overflow: true, At: now.UTC()}}
				}
				for _, event := range changes {
					if !send(ctx, events, event) {
						return
					}
				}
				before = after
			}
		}
	}()
	return events, nil
}

func snapshot(root string) (map[string]stamp, error) {
	result := make(map[string]stamp)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// 与扫描器使用同一条链接判定：Windows junction 报告为 fs.ModeIrregular，只判断
		// fs.ModeSymlink 会让它作为一个普通文件进入快照，从而在每轮轮询中产生与真实媒体
		// 无关的 dirty hint。对非目录项返回 SkipDir 会连带跳过兄弟项，因此返回 nil。
		if filesystem.IsLink(entry.Type()) {
			return nil
		}
		if path == root || entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)] = stamp{size: info.Size(), mode: info.Mode(), modified: info.ModTime().UnixNano()}
		return nil
	})
	return result, err
}

func diff(before, after map[string]stamp, at time.Time) []ports.WatchEvent {
	result := make([]ports.WatchEvent, 0)
	for path, current := range after {
		previous, ok := before[path]
		if !ok {
			result = append(result, ports.WatchEvent{RelativePath: path, Kind: ports.WatchCreated, At: at})
			continue
		}
		if previous != current {
			result = append(result, ports.WatchEvent{RelativePath: path, Kind: ports.WatchModified, At: at})
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			result = append(result, ports.WatchEvent{RelativePath: path, Kind: ports.WatchRemoved, At: at})
		}
	}
	return result
}

func send(ctx context.Context, events chan<- ports.WatchEvent, event ports.WatchEvent) bool {
	select {
	case events <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

var _ ports.FileWatcher = (*Polling)(nil)
