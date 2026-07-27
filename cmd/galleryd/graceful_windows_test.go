//go:build windows

package main

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// configureGracefulStopGroup 让子进程成为独立进程组。这是后续只向该子进程（而不是连带
// 测试进程自身）投递 CTRL_BREAK_EVENT 的前提：GenerateConsoleCtrlEvent 是按进程组投递的。
func configureGracefulStopGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

// requestGracefulStop 向子进程所在的进程组投递 CTRL_BREAK_EVENT。
//
// galleryd 用 signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM) 监听关闭信号，
// 而 Go 运行时在 Windows 上把 CTRL_C_EVENT 与 CTRL_BREAK_EVENT 都映射为 SIGINT
// （即 os.Interrupt）。以 CREATE_NEW_PROCESS_GROUP 启动的进程会屏蔽 CTRL_C，因此
// CTRL_BREAK 是这里唯一可用的、真正触发 server.Shutdown 优雅关闭路径的正常停止方式；
// 直接 Kill 走的是强杀路径，那属于 internal/recovery 的覆盖范围，不是本文件要断言的。
func requestGracefulStop(command *exec.Cmd) error {
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(command.Process.Pid))
}
