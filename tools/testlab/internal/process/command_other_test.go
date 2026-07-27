//go:build !windows

package process

import (
	"errors"
	"syscall"
)

func processStillActive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return !errors.Is(err, syscall.ESRCH)
}
