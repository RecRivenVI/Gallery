//go:build windows

package process

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// configureProcessGroup 让子进程成为自己独立的进程组：这是后续能只对该子进程（而不
// 是连带父进程自己）投递 CTRL_BREAK_EVENT 的前提条件。
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

// requestGracefulStop 向子进程所在的进程组投递 CTRL_BREAK_EVENT。galleryd 的
// bootstrap.Run 用 signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM) 监听关闭
// 信号；Go 运行时在 Windows 上把 CTRL_BREAK_EVENT 视为可被 os/signal 观察到的终止
// 信号，因此这是本工具可用的、真正触发 server.Shutdown 优雅关闭路径的正常停止方式，
// 而不是直接强杀（强杀路径已有独立的 internal/recovery 强杀恢复测试覆盖）。
func requestGracefulStop(cmd *exec.Cmd) error {
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(cmd.Process.Pid))
}
