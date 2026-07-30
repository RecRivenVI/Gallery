//go:build !windows

package main

import (
	"context"
	"fmt"
	"os"
	"time"
)

func holdControlWithoutDeleteSharing(string) (func() error, error) {
	return nil, fmt.Errorf("control 轮换阻断句柄只支持 Windows")
}

func isDeleteSharingViolation(error) bool { return false }

func accessDeniedMessage() string { return "access denied" }

func denyCurrentUserDeleteWithACL(string) (func() error, error) {
	return nil, fmt.Errorf("ACL 轮换阻断只支持 Windows")
}

type observedFileSnapshot struct {
	size      int64
	lastWrite int64
}

func snapshotObservedFile(path string) (observedFileSnapshot, error) {
	info, err := os.Stat(path)
	if err != nil {
		return observedFileSnapshot{}, err
	}
	return observedFileSnapshot{
		size:      info.Size(),
		lastWrite: info.ModTime().UnixNano(),
	}, nil
}

func watchObservedFileReplacement(ctx context.Context, path string) (<-chan error, error) {
	baseline, err := snapshotObservedFile(path)
	if err != nil {
		return nil, err
	}
	result := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			current, statErr := snapshotObservedFile(path)
			if statErr != nil && !os.IsNotExist(statErr) {
				result <- statErr
				return
			}
			if statErr == nil && current != baseline {
				result <- nil
				return
			}
			select {
			case <-ctx.Done():
				result <- ctx.Err()
				return
			case <-ticker.C:
			}
		}
	}()
	return result, nil
}

func watchNextFileWithoutDeleteSharing(context.Context, string) (<-chan pendingFileHold, func() error, error) {
	return nil, nil, fmt.Errorf("恢复候选落位阻断只支持 Windows")
}

func watchPathMissingThenReopenWithoutDeleteSharing(context.Context, string) (<-chan pendingFileHold, func() error, error) {
	return nil, nil, fmt.Errorf("恢复双 Rename 阻断只支持 Windows")
}
