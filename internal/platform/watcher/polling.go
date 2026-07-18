package watcher

import (
	"context"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/RecRivenVI/gallery/internal/ports"
)

type Polling struct {
	interval  time.Duration
	maxEvents int
}

func NewPolling(interval time.Duration, maxEvents int) *Polling {
	if interval <= 0 {
		interval = 2 * time.Second
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
		if entry.Type()&fs.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
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
