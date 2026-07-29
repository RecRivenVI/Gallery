package main

import (
	"fmt"

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
