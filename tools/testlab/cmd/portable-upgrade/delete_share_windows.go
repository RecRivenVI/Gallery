package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var reopenFileProc = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReOpenFile")

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

func isDeleteSharingViolation(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}

func watchObservedFileReplacement(ctx context.Context, path string) (<-chan error, error) {
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if err != nil {
		return nil, fmt.Errorf("定位状态观察目录: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("状态观察父路径不是目录")
	}
	name, err := windows.UTF16PtrFromString(parent)
	if err != nil {
		return nil, err
	}
	directory, err := windows.CreateFile(
		name,
		windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OVERLAPPED,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("打开状态观察目录: %w", err)
	}
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		_ = windows.CloseHandle(directory)
		return nil, fmt.Errorf("建立状态观察事件: %w", err)
	}
	buffer := make([]byte, 4096)
	overlapped := &windows.Overlapped{HEvent: event}
	if err := windows.ReadDirectoryChanges(
		directory,
		&buffer[0],
		uint32(len(buffer)),
		false,
		windows.FILE_NOTIFY_CHANGE_FILE_NAME,
		nil,
		overlapped,
		0,
	); err != nil && !errors.Is(err, windows.ERROR_IO_PENDING) {
		_ = windows.CloseHandle(event)
		_ = windows.CloseHandle(directory)
		return nil, fmt.Errorf("订阅恢复状态文件替换: %w", err)
	}

	result := make(chan error, 1)
	go func() {
		defer windows.CloseHandle(event)
		defer windows.CloseHandle(directory)
		for {
			if err := ctx.Err(); err != nil {
				_ = windows.CancelIoEx(directory, overlapped)
				result <- err
				return
			}
			state, waitErr := windows.WaitForSingleObject(event, 25)
			if waitErr != nil {
				result <- fmt.Errorf("等待恢复状态文件替换: %w", waitErr)
				return
			}
			if state == uint32(windows.WAIT_TIMEOUT) {
				continue
			}
			if state != windows.WAIT_OBJECT_0 {
				result <- fmt.Errorf("恢复状态文件替换等待返回未知状态: %d", state)
				return
			}
			var bytesTransferred uint32
			if err := windows.GetOverlappedResult(directory, overlapped, &bytesTransferred, false); err != nil {
				result <- fmt.Errorf("读取恢复状态文件替换结果: %w", err)
				return
			}
			if notificationRenamedPath(buffer, bytesTransferred, filepath.Base(path)) {
				result <- nil
				return
			}
			if err := windows.ResetEvent(event); err != nil {
				result <- fmt.Errorf("重置恢复状态观察事件: %w", err)
				return
			}
			overlapped = &windows.Overlapped{HEvent: event}
			if err := windows.ReadDirectoryChanges(
				directory,
				&buffer[0],
				uint32(len(buffer)),
				false,
				windows.FILE_NOTIFY_CHANGE_FILE_NAME,
				nil,
				overlapped,
				0,
			); err != nil && !errors.Is(err, windows.ERROR_IO_PENDING) {
				result <- fmt.Errorf("续订恢复状态文件替换: %w", err)
				return
			}
		}
	}()
	return result, nil
}

func notificationRenamedPath(buffer []byte, bytesTransferred uint32, target string) bool {
	headerSize := uint32(unsafe.Offsetof(windows.FileNotifyInformation{}.FileName))
	for offset := uint32(0); offset+headerSize <= bytesTransferred; {
		info := (*windows.FileNotifyInformation)(unsafe.Pointer(&buffer[offset]))
		nameBytes := info.FileNameLength
		if nameBytes%2 != 0 || offset+headerSize+nameBytes > bytesTransferred {
			return false
		}
		name := windows.UTF16ToString(unsafe.Slice(&info.FileName, int(nameBytes/2)))
		if info.Action == windows.FILE_ACTION_RENAMED_NEW_NAME && strings.EqualFold(name, target) {
			return true
		}
		if info.NextEntryOffset == 0 || offset+info.NextEntryOffset <= offset {
			return false
		}
		offset += info.NextEntryOffset
	}
	return false
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
				result <- pendingFileHold{path: path, release: release}
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

func openRenameTrackingHandle(path string) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	handle, err := windows.CreateFile(
		name,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("建立允许首次轮换的跟踪句柄: %w", err)
	}
	return handle, nil
}

func reopenWithoutDeleteSharing(handle windows.Handle) (windows.Handle, error) {
	value, _, callErr := reopenFileProc.Call(
		uintptr(handle),
		uintptr(windows.GENERIC_READ),
		uintptr(windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE),
		0,
	)
	reopened := windows.Handle(value)
	if reopened == windows.InvalidHandle {
		return windows.InvalidHandle, fmt.Errorf("以不共享删除方式重开已轮换旧库: %w", callErr)
	}
	return reopened, nil
}

// watchPathMissingThenReopenWithoutDeleteSharing 先以共享删除的句柄跟踪当前库，因此
// galleryd 的首次 control.db -> rotated Rename 仍能成功。原路径第一次消失后，探针
// 通过 ReOpenFile 对同一已轮换文件建立不共享删除的第二句柄，使随后的 rotated ->
// control.db 回滚必须收到真实 sharing violation。
func watchPathMissingThenReopenWithoutDeleteSharing(
	ctx context.Context,
	watchedPath string,
) (<-chan pendingFileHold, func() error, error) {
	info, err := os.Stat(watchedPath)
	if err != nil {
		return nil, nil, fmt.Errorf("定位双 Rename 阻断文件: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("双 Rename 阻断路径不是普通文件")
	}
	tracking, err := openRenameTrackingHandle(watchedPath)
	if err != nil {
		return nil, nil, err
	}

	watchCtx, cancel := context.WithCancel(ctx)
	result := make(chan pendingFileHold, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if err := watchCtx.Err(); err != nil {
				_ = windows.CloseHandle(tracking)
				result <- pendingFileHold{err: err}
				return
			}
			_, statErr := os.Stat(watchedPath)
			if statErr == nil {
				runtime.Gosched()
				continue
			}
			if !errors.Is(statErr, os.ErrNotExist) {
				_ = windows.CloseHandle(tracking)
				result <- pendingFileHold{err: fmt.Errorf("观察当前库轮换: %w", statErr)}
				return
			}
			reopened, reopenErr := reopenWithoutDeleteSharing(tracking)
			if reopenErr != nil {
				if isDeleteSharingViolation(reopenErr) {
					if _, verifyErr := os.Stat(watchedPath); errors.Is(verifyErr, os.ErrNotExist) {
						runtime.Gosched()
						continue
					}
				}
				_ = windows.CloseHandle(tracking)
				result <- pendingFileHold{err: reopenErr}
				return
			}
			if _, verifyErr := os.Stat(watchedPath); !errors.Is(verifyErr, os.ErrNotExist) {
				_ = windows.CloseHandle(reopened)
				_ = windows.CloseHandle(tracking)
				result <- pendingFileHold{err: fmt.Errorf("未在旧库回滚前建立阻断句柄")}
				return
			}
			result <- pendingFileHold{
				path: watchedPath,
				release: func() error {
					reopenCloseErr := windows.CloseHandle(reopened)
					trackingCloseErr := windows.CloseHandle(tracking)
					return errors.Join(reopenCloseErr, trackingCloseErr)
				},
			}
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
