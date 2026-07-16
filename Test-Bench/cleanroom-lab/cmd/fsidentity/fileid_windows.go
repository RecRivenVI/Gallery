//go:build windows

package main

import (
	"fmt"
	"syscall"
)

// Windows 下取真实 NTFS FileID(VolumeSerial + FileIndexHigh/Low),用于同卷移动关联。
// 打开文件句柄并 GetFileInformationByHandle。
func fileIDByPath(path string) string {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "?"
	}
	h, err := syscall.CreateFile(p, 0, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "?"
	}
	defer syscall.CloseHandle(h)
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &info); err != nil {
		return "?"
	}
	return fmt.Sprintf("%08x:%08x%08x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow)
}
