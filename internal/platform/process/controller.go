package process

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/RecRivenVI/gallery/internal/ports"
)

type Controller struct{}

func (Controller) Start(ctx context.Context, command ports.Command) (ports.Process, error) {
	if command.Path == "" {
		return nil, fmt.Errorf("process path 不能为空")
	}
	cmd := exec.CommandContext(ctx, command.Path, command.Args...)
	cmd.Dir = command.Dir
	cmd.Stdin = command.Stdin
	cmd.Stdout = command.Stdout
	cmd.Stderr = command.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &running{command: cmd}, nil
}

type running struct{ command *exec.Cmd }

func (p *running) Wait() error { return p.command.Wait() }

func (p *running) Kill() error {
	if p.command.Process == nil {
		return fmt.Errorf("process 尚未启动")
	}
	return p.command.Process.Kill()
}
