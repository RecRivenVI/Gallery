package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"golang.org/x/sys/windows"
)

// holdControlWithoutDeleteSharing 以允许其它进程读写、但不共享删除/重命名的方式持有
// 当前 control.db。它只用于 Windows 临时 AppDirs 的真实恢复门禁；galleryd 仍能打开
// 当前库，但 applyRestore 的第一步 Rename 必须收到操作系统 sharing violation。
func holdControlWithoutDeleteSharing(path string) (func() error, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		name,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("建立 control 轮换阻断句柄: %w", err)
	}
	return func() error { return windows.CloseHandle(handle) }, nil
}

// watchNextFileWithoutDeleteSharing 先启动只针对精确候选路径的打开循环，再等待恢复
// 候选由真实 galleryd 创建。句柄允许 SQLite 继续读写，但不共享删除/重命名，因此
// 随后的 incoming -> control.db Rename 必须由 Windows 拒绝。持续尝试仅存在于这个
// Windows 测试探针，并受调用方 context 和进程 CPU affinity 约束。
// 调用方持有返回的 release，直到 galleryd 已完成安全回滚并发布 descriptor。
func watchNextFileWithoutDeleteSharing(
	ctx context.Context,
	path string,
) (<-chan pendingFileHold, func() error, error) {
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return nil, nil, fmt.Errorf("定位恢复候选目录: %w", err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("恢复候选父路径不是目录")
	}

	watchCtx, cancel := context.WithCancel(ctx)
	result := make(chan pendingFileHold, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if err := watchCtx.Err(); err != nil {
				result <- pendingFileHold{err: err}
				return
			}
			release, openErr := holdControlWithoutDeleteSharing(path)
			if openErr == nil {
				result <- pendingFileHold{release: release}
				return
			}
			if errors.Is(openErr, windows.ERROR_FILE_NOT_FOUND) ||
				errors.Is(openErr, windows.ERROR_PATH_NOT_FOUND) ||
				errors.Is(openErr, windows.ERROR_SHARING_VIOLATION) {
				runtime.Gosched()
				continue
			}
			result <- pendingFileHold{err: openErr}
			return
		}
	}()
	var stopOnce sync.Once
	stop := func() error {
		stopOnce.Do(func() {
			cancel()
			<-done
		})
		return nil
	}
	return result, stop, nil
}
