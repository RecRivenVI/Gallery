//go:build !windows

package process

import (
	"fmt"
	"os/exec"
	"syscall"

	"github.com/RecRivenVI/gallery/internal/ports"
)

const supportsLimits = false

// processGroup 在 Unix 上用独立进程组承载「进程树」。
//
// exec.CommandContext 的默认取消只向直接子进程发信号，孙进程会继续持有继承来的
// stdout/stderr 管道写端，使 Wait 长期阻塞。Setpgid 让子进程成为新进程组的组长，
// kill(-pgid) 因此可以一次终止它派生出的全部后代（后代自己调用 setpgid 脱离时除外）。
type processGroup struct{}

func newProcessGroup(limits ports.ProcessLimits) (*processGroup, error) {
	if limits.MemoryBytes != 0 || limits.CPUTime != 0 {
		return nil, fmt.Errorf("当前平台尚不支持无竞态的进程树 CPU/内存硬限制")
	}
	return &processGroup{}, nil
}

func (g *processGroup) prepare(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// adopt 无事可做：Setpgid 在 fork 时就已生效，不存在 Windows 那种「先启动、后纳入」的窗口。
func (g *processGroup) adopt(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return fmt.Errorf("process 尚未启动")
	}
	return nil
}

func (g *processGroup) kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return fmt.Errorf("process 尚未启动")
	}
	// 子进程是组长，pgid 等于它的 pid。
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}

// release 在 Wait 之后补一次进程组强杀，回收仍持有管道的孙进程。此时直接子进程已被回收，
// 理论上存在 pid 复用后误伤同号进程组的窗口；命中它需要 pid 空间整体回绕，实践中不可达，
// 而放弃这一步会让孤儿孙进程长期持有 Source 句柄和管道。
func (g *processGroup) release(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
