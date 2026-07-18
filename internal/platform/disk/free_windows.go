//go:build windows

package disk

import (
	"syscall"

	"golang.org/x/sys/windows"
)

type OS struct{}

func (OS) FreeBytes(path string) (int64, error) {
	value, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(value, &free, &total, &totalFree); err != nil {
		return 0, err
	}
	return int64(free), nil
}
