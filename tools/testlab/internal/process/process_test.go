package process

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestTestlabProcessHelper 不是真正的测试：当 TESTLAB_PROCESS_HELPER 环境变量被
// 设置时，它把当前测试二进制变成一个可控的伪造子进程，供下面的测试驱动
// StartGalleryd 的通用生命周期逻辑，而不必每次都真正编译/启动完整的 galleryd。
// 行为与 internal/recovery/killpoints_test.go 的 TestKillpointHelperProcess 复用
// 同一模式。
func TestTestlabProcessHelper(t *testing.T) {
	switch os.Getenv("TESTLAB_PROCESS_HELPER") {
	case "":
		return
	case "exit-immediately":
		os.Exit(3)
	case "sleep-without-descriptor":
		time.Sleep(5 * time.Second)
		os.Exit(0)
	case "spawn-tree-child":
		child := exec.Command(os.Args[0], "-test.run=TestTestlabProcessHelper")
		child.Env = append(os.Environ(), "TESTLAB_PROCESS_HELPER=tree-child")
		if err := child.Start(); err != nil {
			os.Exit(4)
		}
		if err := os.WriteFile(os.Getenv("TESTLAB_TREE_CHILD_PID"), []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			os.Exit(5)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "tree-child":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	default:
		t.Fatalf("未知 TESTLAB_PROCESS_HELPER: %s", os.Getenv("TESTLAB_PROCESS_HELPER"))
	}
}

func helperCommand(t *testing.T, mode string) *exec.Cmd {
	t.Helper()
	exePath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exePath, "-test.run=TestTestlabProcessHelper")
	cmd.Env = append(os.Environ(), "TESTLAB_PROCESS_HELPER="+mode)
	return cmd
}

// startGeneric 复用 StartGalleryd 的描述符等待/提前退出检测逻辑，但驱动任意命令而不是
// 真正的 galleryd 二进制，用于在不实际编译/启动 galleryd 的情况下测试生命周期管理本身。
func startGeneric(t *testing.T, cmd *exec.Cmd, appRoot, logPath string, timeout time.Duration) (*Process, error) {
	t.Helper()
	configureProcessGroup(cmd)
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, err
	}
	proc := &Process{cmd: cmd, AppRoot: appRoot, logFile: logFile, exited: make(chan struct{})}
	go func() {
		proc.waitErr = cmd.Wait()
		close(proc.exited)
	}()
	descriptorPath := filepath.Join(appRoot, "run", "galleryd.json")
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-proc.exited:
			logFile.Close()
			return nil, proc.waitErr
		default:
		}
		if _, statErr := os.Stat(descriptorPath); statErr == nil {
			return proc, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	proc.forceKill()
	<-proc.exited
	logFile.Close()
	return nil, errTimeout
}

var errTimeout = &timeoutError{}

type timeoutError struct{}

func (*timeoutError) Error() string { return "timed out waiting for descriptor" }

func TestStartGenericDetectsEarlyExit(t *testing.T) {
	appRoot := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "helper.log")
	_, err := startGeneric(t, helperCommand(t, "exit-immediately"), appRoot, logPath, 3*time.Second)
	if err == nil {
		t.Fatal("expected an error when the child process exits before writing the descriptor")
	}
}

func TestStartGenericTimesOutAndKillsProcess(t *testing.T) {
	appRoot := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "helper.log")
	started := time.Now()
	_, err := startGeneric(t, helperCommand(t, "sleep-without-descriptor"), appRoot, logPath, 500*time.Millisecond)
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("expected a timeout error when the descriptor never appears")
	}
	if elapsed > 4*time.Second {
		t.Fatalf("startGeneric took %s, expected to return promptly after its own timeout and force-kill the child", elapsed)
	}
}

func TestStartGallerydWithSourceRootsRejectsEmptyRootBeforeStarting(t *testing.T) {
	_, err := StartGallerydWithSourceRoots("not-executed", t.TempDir(), filepath.Join(t.TempDir(), "log"), time.Second, "")
	if err == nil {
		t.Fatal("空 Source 根必须在启动子进程前被拒绝")
	}
}

func TestStartGallerydLANContextHonoursPreCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := StartGallerydLANContext(ctx, "not-executed", t.TempDir(), filepath.Join(t.TempDir(), "log"), time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("LAN 启动未保留预取消原因: %v", err)
	}
}

func TestReadOwnedDescriptorRejectsStaleRestartDescriptor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "galleryd.json")
	if err := os.WriteFile(path, []byte(`{"address":"127.0.0.1:49152","pid":41,"startupNonce":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if value, ok := readOwnedDescriptor(path, 42); ok {
		t.Fatalf("上一进程遗留的 descriptor 被错误接受: %+v", value)
	}
	if err := os.WriteFile(path, []byte(`{"address":"127.0.0.1:49153","pid":42,"startupNonce":""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if value, ok := readOwnedDescriptor(path, 42); ok {
		t.Fatalf("缺少启动 nonce 的 descriptor 被错误接受: %+v", value)
	}
	if err := os.WriteFile(path, []byte(`{"address":"127.0.0.1:49154","pid":42,"startupNonce":"current"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	value, ok := readOwnedDescriptor(path, 42)
	if !ok || value.Address != "127.0.0.1:49154" || value.PID != 42 || value.StartupNonce != "current" {
		t.Fatalf("当前进程 descriptor 未被接受: %+v ok=%t", value, ok)
	}
}

func TestStopOnAlreadyExitedNonzeroProcessReportsFailure(t *testing.T) {
	appRoot := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "helper.log")
	cmd := helperCommand(t, "exit-immediately")
	configureProcessGroup(cmd)
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	proc := &Process{cmd: cmd, AppRoot: appRoot, logFile: logFile, exited: make(chan struct{})}
	go func() {
		proc.waitErr = cmd.Wait()
		close(proc.exited)
	}()
	<-proc.exited
	outcome := proc.Stop()
	if outcome.ExitedGracefully || outcome.ForcedKill || outcome.Err == nil {
		t.Fatalf("非零退出不能被报告为优雅停止: %+v", outcome)
	}
}

func TestKillReportsIntentionalForcedTermination(t *testing.T) {
	appRoot := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "helper.log")
	cmd := helperCommand(t, "sleep-without-descriptor")
	configureProcessGroup(cmd)
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	proc := &Process{cmd: cmd, AppRoot: appRoot, logFile: logFile, exited: make(chan struct{})}
	go func() {
		proc.waitErr = cmd.Wait()
		close(proc.exited)
	}()
	outcome := proc.Kill()
	if !outcome.ForcedKill || outcome.RequestedGraceful || outcome.ExitedGracefully || outcome.Err != nil {
		t.Fatalf("显式强杀结果错误: %+v", outcome)
	}
	select {
	case <-proc.exited:
	default:
		t.Fatal("Kill 返回时子进程仍未退出")
	}
}

func TestRunCommandContextCancelsEntireProcessTree(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "child.pid")
	cmd := helperCommand(t, "spawn-tree-child")
	cmd.Env = append(cmd.Env, "TESTLAB_TREE_CHILD_PID="+pidPath)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunCommandContext(ctx, cmd) }()

	var childPID int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		content, err := os.ReadFile(pidPath)
		if err == nil {
			childPID, err = strconv.Atoi(strings.TrimSpace(string(content)))
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if childPID == 0 {
		cancel()
		t.Fatal("父进程没有登记派生子进程 PID")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("取消命令树错误 = %v，期望 context.Canceled", err)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("取消命令树超时")
	}

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && processStillActive(childPID) {
		time.Sleep(20 * time.Millisecond)
	}
	if processStillActive(childPID) {
		t.Fatalf("派生子进程 %d 在命令树取消后仍存活", childPID)
	}
}
