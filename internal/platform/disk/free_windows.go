//go:build windows

package disk

import (
	"errors"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

type OS struct{}

// FreeBytes 返回 path 所在卷对当前调用者可用的剩余字节数。
//
// 单位契约与 free_unix.go 一致，必须是字节：调用方按 MaxOutputBytes*2、control.db 大小等
// 字节预算做空间前置闸，任何以 KiB/簇为单位的返回都会把可用空间高报数千倍。
//
// 输入契约同样必须与 free_unix.go 一致。Unix 的 statfs 接受目录和普通文件，而
// GetDiskFreeSpaceEx 只接受目录：直接传入文件路径会得到 ERROR_DIRECTORY。ports.SpaceChecker
// 没有声明"仅限目录"，因此这种差异会表现为同一份调用代码在 Linux 正常、在 Windows（v1
// 正式目标平台）稳定失败，并把 GC/VACUUM/备份恢复变成永久拒绝。这里在且仅在
// ERROR_DIRECTORY 时退回到所在目录：普通文件必然与其父目录同卷，因此测量对象不变；而
// 路径不存在等情况返回的是 ERROR_PATH_NOT_FOUND/ERROR_FILE_NOT_FOUND，不会被这条分支
// 吞掉，仍然作为错误上报而不是静默的 0。
func (OS) FreeBytes(path string) (int64, error) {
	free, err := freeBytesOfDirectory(path)
	if errors.Is(err, windows.ERROR_DIRECTORY) {
		if parent := filepath.Dir(path); parent != path {
			return freeBytesOfDirectory(parent)
		}
	}
	return free, err
}

func freeBytesOfDirectory(path string) (int64, error) {
	value, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(value, &free, &total, &totalFree); err != nil {
		return 0, err
	}
	// GetDiskFreeSpaceEx 的输出是 uint64。转成 int64 前必须显式检查高位：一旦某个卷（或
	// 未来的伪造/虚拟卷）报告超过 2^63-1 字节，无检查的转换会得到负数，让所有
	// `free >= requiredBytes` 判定恒假，把空间闸变成永久拒绝。
	if free > 1<<63-1 {
		return 1<<63 - 1, nil
	}
	return int64(free), nil
}
