package process

import (
	"os"
	"os/exec"
	"path/filepath"
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

func TestStopOnAlreadyExitedProcessReportsGraceful(t *testing.T) {
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
	if !outcome.ExitedGracefully || outcome.ForcedKill {
		t.Fatalf("Stop() on an already-exited process should report ExitedGracefully without forcing a kill, got %+v", outcome)
	}
}
