//go:build !windows

package process

import (
	"errors"
	"os/exec"
	"syscall"
)

type commandTree struct {
	processGroupID int
}

func newCommandTree(cmd *exec.Cmd) (*commandTree, error) {
	attributes := syscall.SysProcAttr{}
	if cmd.SysProcAttr != nil {
		attributes = *cmd.SysProcAttr
	}
	attributes.Setpgid = true
	cmd.SysProcAttr = &attributes
	return &commandTree{}, nil
}

func (t *commandTree) attach(cmd *exec.Cmd) error {
	t.processGroupID = cmd.Process.Pid
	return nil
}

func (t *commandTree) graceful(_ *exec.Cmd) error {
	return t.signal(syscall.SIGTERM)
}

func (t *commandTree) force(_ *exec.Cmd) error {
	return t.signal(syscall.SIGKILL)
}

func (t *commandTree) signal(value syscall.Signal) error {
	if t.processGroupID == 0 {
		return nil
	}
	err := syscall.Kill(-t.processGroupID, value)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func (t *commandTree) empty() (bool, error) {
	if t.processGroupID == 0 {
		return true, nil
	}
	err := syscall.Kill(-t.processGroupID, 0)
	if errors.Is(err, syscall.ESRCH) {
		return true, nil
	}
	return false, err
}

func (*commandTree) close() error { return nil }
