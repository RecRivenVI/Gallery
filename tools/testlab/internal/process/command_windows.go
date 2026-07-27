//go:build windows

package process

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type commandTree struct {
	job windows.Handle
}

type jobObjectBasicAccountingInformation struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

func newCommandTree(cmd *exec.Cmd) (*commandTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
	); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	attributes := syscall.SysProcAttr{}
	if cmd.SysProcAttr != nil {
		attributes = *cmd.SysProcAttr
	}
	// 先挂起创建，再在 attach 中纳入 Job Object 并恢复。若只在 cmd.Start 返回后绑定，
	// 子进程可能已经派生出不会被 Job 追溯纳管的后代。
	attributes.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_SUSPENDED
	cmd.SysProcAttr = &attributes
	return &commandTree{job: job}, nil
}

func (t *commandTree) attach(cmd *exec.Cmd) error {
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(t.job, process); err != nil {
		return err
	}
	return resumeProcess(uint32(cmd.Process.Pid))
}

// resumeProcess 恢复以 CREATE_SUSPENDED 创建的唯一主线程。os/exec 不暴露 CreateProcess
// 返回的线程句柄，因此按 OwnerProcessID 从系统线程快照中定位；此时目标尚未执行，不可能
// 已经派生出第二条线程或子进程。
func resumeProcess(pid uint32) error {
	snapshot, err := threadSnapshot()
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ThreadEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, openErr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if openErr != nil {
			return fmt.Errorf("打开挂起命令主线程: %w", openErr)
		}
		_, resumeErr := windows.ResumeThread(thread)
		_ = windows.CloseHandle(thread)
		if resumeErr != nil {
			return fmt.Errorf("恢复挂起命令主线程: %w", resumeErr)
		}
		return nil
	}
	return fmt.Errorf("未找到挂起命令 %d 的主线程", pid)
}

func threadSnapshot() (windows.Handle, error) {
	var lastErr error
	for attempt := 0; attempt < 8; attempt++ {
		snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
		if err == nil {
			return snapshot, nil
		}
		lastErr = err
		if !errors.Is(err, windows.ERROR_BAD_LENGTH) {
			break
		}
	}
	return 0, fmt.Errorf("枚举命令线程快照: %w", lastErr)
}

func (*commandTree) graceful(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(cmd.Process.Pid))
}

func (t *commandTree) force(_ *exec.Cmd) error {
	if t.job == 0 {
		return nil
	}
	err := windows.TerminateJobObject(t.job, 1)
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		empty, queryErr := t.empty()
		if queryErr == nil && empty {
			return nil
		}
	}
	return err
}

func (t *commandTree) empty() (bool, error) {
	if t.job == 0 {
		return true, nil
	}
	var accounting jobObjectBasicAccountingInformation
	err := windows.QueryInformationJobObject(
		t.job,
		windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&accounting)),
		uint32(unsafe.Sizeof(accounting)),
		nil,
	)
	return accounting.ActiveProcesses == 0, err
}

func (t *commandTree) close() error {
	if t.job == 0 {
		return nil
	}
	err := windows.CloseHandle(t.job)
	t.job = 0
	return err
}
