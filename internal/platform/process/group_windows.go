//go:build windows

package process

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processGroup 在 Windows 上用 Job Object 承载「进程树」。
//
// exec.CommandContext 的默认取消只调用 TerminateProcess，杀不到孙进程；孙进程继承的
// stdout/stderr 管道写端会一直保持打开，Wait 因此可能长期阻塞。Job Object 加
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE 让整棵树在 TerminateJobObject 或最后一个 Job 句柄
// 关闭时一起结束。
//
// 子进程以 CREATE_SUSPENDED 创建：先纳入 Job 再恢复执行，避免它在被纳入之前就派生出不属于
// 该 Job 的孙进程。Windows 8 起支持嵌套 Job，因此宿主进程本身已在某个 Job 内也不影响。
type processGroup struct {
	mu  sync.Mutex
	job windows.Handle
}

func newProcessGroup() *processGroup { return &processGroup{} }

func (g *processGroup) prepare(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
}

// adopt 在子进程执行第一条指令之前把它放进 Job，然后恢复它。任何一步失败都返回错误，由
// Controller 负责强杀并回收这个仍处于挂起状态的进程——不做静默降级，因为降级会让「杀进程树」
// 这条边界在无人察觉的情况下失效。
func (g *processGroup) adopt(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return fmt.Errorf("process 尚未启动")
	}
	job, err := createKillOnCloseJob()
	if err != nil {
		return err
	}
	if err := assignProcessToJob(job, uint32(cmd.Process.Pid)); err != nil {
		_ = windows.CloseHandle(job)
		return err
	}
	g.mu.Lock()
	g.job = job
	g.mu.Unlock()
	return resumeProcess(uint32(cmd.Process.Pid))
}

func (g *processGroup) kill(cmd *exec.Cmd) error {
	g.mu.Lock()
	job := g.job
	g.mu.Unlock()
	if job != 0 {
		if err := windows.TerminateJobObject(job, 1); err == nil {
			return nil
		}
	}
	if cmd.Process == nil {
		return fmt.Errorf("process 尚未启动")
	}
	return cmd.Process.Kill()
}

// release 关闭 Job 句柄。因为设置了 JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE，这一步同时终止仍留
// 在 Job 内的残余后代进程，等价于收尾强杀。
func (g *processGroup) release(_ *exec.Cmd) {
	g.mu.Lock()
	job := g.job
	g.job = 0
	g.mu.Unlock()
	if job != 0 {
		_ = windows.CloseHandle(job)
	}
}

func createKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("创建 Job Object 失败: %w", err)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("设置 Job Object 限制失败: %w", err)
	}
	return job, nil
}

func assignProcessToJob(job windows.Handle, pid uint32) error {
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return fmt.Errorf("打开子进程句柄失败: %w", err)
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	if err := windows.AssignProcessToJobObject(job, handle); err != nil {
		return fmt.Errorf("子进程加入 Job Object 失败: %w", err)
	}
	return nil
}

// resumeProcess 恢复挂起创建的子进程。CREATE_SUSPENDED 的进程只有一条主线程，这里按
// OwnerProcessID 从线程快照中筛出它并 ResumeThread。
func resumeProcess(pid uint32) error {
	snapshot, err := threadSnapshot()
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()
	var entry windows.ThreadEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	resumed := 0
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, openErr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if openErr != nil {
			return fmt.Errorf("打开子进程线程失败: %w", openErr)
		}
		_, resumeErr := windows.ResumeThread(thread)
		_ = windows.CloseHandle(thread)
		if resumeErr != nil {
			return fmt.Errorf("恢复子进程线程失败: %w", resumeErr)
		}
		resumed++
	}
	if resumed == 0 {
		return fmt.Errorf("未找到子进程 %d 的可恢复线程", pid)
	}
	return nil
}

// threadSnapshot 对 ERROR_BAD_LENGTH 重试：CreateToolhelp32Snapshot 在系统线程表变动时会用
// 这个错误要求调用方重试，属于文档化的正常情况。
func threadSnapshot() (windows.Handle, error) {
	var lastErr error
	for attempt := 0; attempt < 8; attempt++ {
		snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
		if err == nil {
			return snapshot, nil
		}
		lastErr = err
		if err != windows.ERROR_BAD_LENGTH {
			break
		}
	}
	return 0, fmt.Errorf("枚举线程快照失败: %w", lastErr)
}
