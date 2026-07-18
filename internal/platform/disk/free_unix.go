//go:build !windows

package disk

import (
	"golang.org/x/sys/unix"
)

type OS struct{}

func (OS) FreeBytes(path string) (int64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}
