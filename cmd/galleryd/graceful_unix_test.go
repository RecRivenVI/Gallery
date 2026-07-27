//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// configureGracefulStopGroup 在非 Windows 平台无需特殊进程组设置：信号可以直接投递给
// 目标进程本身。
func configureGracefulStopGroup(command *exec.Cmd) {}

// requestGracefulStop 直接向子进程发送 SIGINT，对应 galleryd 的
// signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM) 关闭路径。
func requestGracefulStop(command *exec.Cmd) error {
	return command.Process.Signal(syscall.SIGINT)
}
