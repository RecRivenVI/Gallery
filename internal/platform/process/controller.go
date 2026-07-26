package process

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/RecRivenVI/gallery/internal/ports"
)

// DefaultWaitDelay 是 exec.Cmd.WaitDelay 的默认取值。
//
// 调用方传入的 Stdout/Stderr 是普通 io.Writer（不是 *os.File），os/exec 因此必然创建
// os.Pipe 与拷贝 goroutine，Wait 要等这些 goroutine 把管道读完才返回。子进程派生的孙进程
// 会继承同一批管道写端，杀死直接子进程并不会关闭这些句柄：没有 WaitDelay 时 Wait 可能在
// context 早已超时、子进程早已被杀之后仍然永久阻塞，钉死调用方的 worker、临时工作目录和
// 资源池名额。WaitDelay 让 os/exec 在 context 结束或子进程退出之后最多再等这么久，就强制
// 关闭管道并返回 exec.ErrWaitDelay，把 Wait 变成有界操作。
const DefaultWaitDelay = 5 * time.Second

// Controller 是 ports.ProcessController 的平台实现。
//
// 两条边界在这里落地：Wait 有界（WaitDelay），以及取消/强杀作用于整棵进程树而不是只作用于
// 直接子进程（见 processGroup 的平台实现）。
type Controller struct {
	// WaitDelay 覆盖 DefaultWaitDelay。<=0 表示使用默认值；只有测试和确有需要的工具才应设置。
	WaitDelay time.Duration
}

func (c Controller) Start(ctx context.Context, command ports.Command) (ports.Process, error) {
	if command.Path == "" {
		return nil, fmt.Errorf("process path 不能为空")
	}
	cmd := exec.CommandContext(ctx, command.Path, command.Args...)
	cmd.Dir = command.Dir
	cmd.Stdin = command.Stdin
	cmd.Stdout = command.Stdout
	cmd.Stderr = command.Stderr
	waitDelay := c.WaitDelay
	if waitDelay <= 0 {
		waitDelay = DefaultWaitDelay
	}
	cmd.WaitDelay = waitDelay

	group := newProcessGroup()
	group.prepare(cmd)
	// exec.CommandContext 的默认 Cancel 只杀直接子进程，孙进程会继续持有管道。改为终止整棵树。
	cmd.Cancel = func() error { return group.kill(cmd) }
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if err := group.adopt(cmd); err != nil {
		_ = group.kill(cmd)
		group.release(cmd)
		_ = cmd.Wait()
		return nil, err
	}
	// adopt 之前 context 可能已经结束：那一次 Cancel 作用在还没有成员的容器上，必须补一次强杀。
	if ctx.Err() != nil {
		_ = group.kill(cmd)
	}
	return &running{command: cmd, group: group}, nil
}

type running struct {
	command *exec.Cmd
	group   *processGroup
}

// Wait 等待直接子进程退出并回收管道。WaitDelay 保证即使孙进程仍持有管道，本调用也在有界
// 时间内返回；返回之后 release 收尾终止容器内可能残留的后代进程。
func (p *running) Wait() error {
	err := p.command.Wait()
	p.group.release(p.command)
	return err
}

// Kill 终止整棵进程树。调用之后 Wait 会在有界时间内返回：树内所有进程死亡即关闭全部管道
// 写端；万一仍有句柄残留，WaitDelay 兜底。
func (p *running) Kill() error {
	if p.command.Process == nil {
		return fmt.Errorf("process 尚未启动")
	}
	return p.group.kill(p.command)
}
