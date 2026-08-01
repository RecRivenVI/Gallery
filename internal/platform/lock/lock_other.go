//go:build !windows

package lock

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// lockFile 在 Unix 上使用 flock 取得非阻塞独占锁。锁与打开文件描述关联，进程退出即释放。
func lockFile(file *os.File) error {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return ErrAlreadyLocked
	}
	return err
}

func unlockFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
