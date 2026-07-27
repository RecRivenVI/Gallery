package process

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

const (
	commandGracefulStopTimeout = 3 * time.Second
	commandForceKillTimeout    = 5 * time.Second
)

// RunCommandContext 运行可能派生子进程的验证命令，并把整棵进程树纳入同一个生命周期。
// Unix 使用独立进程组；Windows 使用 KILL_ON_JOB_CLOSE Job Object。ctx 取消时先请求整组
// 正常停止，再以二级上限强制终止整棵树；即使直接子进程先退出，也会回收遗留后代。
func RunCommandContext(ctx context.Context, cmd *exec.Cmd) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tree, err := newCommandTree(cmd)
	if err != nil {
		return fmt.Errorf("建立命令进程树: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = tree.close()
		}
	}()
	if err := cmd.Start(); err != nil {
		return errors.Join(err, tree.close())
	}
	if err := tree.attach(cmd); err != nil {
		_ = cmd.Process.Kill()
		waitErr := cmd.Wait()
		closed = true
		return errors.Join(fmt.Errorf("把命令加入进程树: %w", err), waitErr, tree.close())
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case waitErr := <-done:
		cleanupErr := finishCommandTree(tree)
		closed = true
		return errors.Join(waitErr, cleanupErr)
	case <-ctx.Done():
		gracefulErr := tree.graceful(cmd)
		if gracefulErr == nil {
			select {
			case waitErr := <-done:
				cleanupErr := finishCommandTree(tree)
				closed = true
				return errors.Join(ctx.Err(), waitErr, cleanupErr)
			case <-time.After(commandGracefulStopTimeout):
			}
		}

		forceErr := tree.force(cmd)
		var waitErr error
		select {
		case waitErr = <-done:
		case <-time.After(commandForceKillTimeout):
			waitErr = fmt.Errorf("强制终止命令树后等待直接子进程超时（%s）", commandForceKillTimeout)
		}
		emptyErr := waitCommandTreeEmpty(tree, commandForceKillTimeout)
		closed = true
		return errors.Join(ctx.Err(), gracefulErr, forceErr, waitErr, emptyErr, tree.close())
	}
}

func finishCommandTree(tree *commandTree) error {
	forceErr := tree.force(nil)
	emptyErr := waitCommandTreeEmpty(tree, commandForceKillTimeout)
	return errors.Join(forceErr, emptyErr, tree.close())
}

func waitCommandTreeEmpty(tree *commandTree, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		empty, err := tree.empty()
		if err != nil {
			return err
		}
		if empty {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等待命令进程树清空超时（%s）", timeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
