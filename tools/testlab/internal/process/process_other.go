//go:build !windows

package process

import (
	"os/exec"
	"syscall"
)

// configureProcessGroup 在非 Windows 平台上无需特殊进程组设置：SIGTERM 可以直接
// 发给目标进程本身。
func configureProcessGroup(cmd *exec.Cmd) {}

// requestGracefulStop 发送 SIGTERM，与 bootstrap.Run 的
// signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM) 关闭路径一致。
func requestGracefulStop(cmd *exec.Cmd) error {
	return cmd.Process.Signal(syscall.SIGTERM)
}
