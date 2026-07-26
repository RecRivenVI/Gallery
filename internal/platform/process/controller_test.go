package process_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/platform/process"
	"github.com/RecRivenVI/gallery/internal/ports"
)

// 本文件用真实进程覆盖 toolrunner 依赖的两条进程边界：
//   - Wait 必须有界返回，即使孙进程仍然持有继承来的 stdout/stderr 管道写端；
//   - 取消与强杀必须作用于整棵进程树，孙进程不能在 Wait 返回之后继续存活。
//
// 测试通过重新执行测试二进制自身构造进程树，不依赖任何外部工具，因此 Windows 与 Unix 使用
// 同一套用例。孙进程持续向心跳文件追加字节；「树已经死了」的判据是心跳在 Wait 返回之后停止
// 增长——这是跨平台可判定的存活性检查。

const (
	helperMarker  = "--gallery-process-helper"
	helperRunFlag = "-test.run=^TestProcessTreeHelper$"
	// flagTerminator 必不可少：辅助进程就是测试二进制本身，testing 在运行任何用例之前先
	// flag.Parse(os.Args[1:])，遇到未注册的 -gallery-process-helper 会直接 os.Exit(2)。
	// flag 把单独的 "--" 当作解析终止符，其后的实参原样留在 flag.Args() 里，因此角色实参
	// 必须放在它后面。少了这一个实参，辅助进程根本进不了 TestProcessTreeHelper，
	// 表现为「子进程 exit status 2、孙进程从未存在」。
	flagTerminator = "--"

	roleSpawnExit = "spawn-exit" // 派生孙进程后立刻退出：制造「子进程已退出但管道未关闭」
	roleSpawnHold = "spawn-hold" // 派生孙进程后自己也不退出：制造需要整树强杀的形态
	roleHold      = "hold"       // 持有管道并写心跳，直到被杀
)

// helperArgs 是重新执行测试二进制进入辅助进程角色的唯一实参构造入口，父进程与子进程都必须
// 用它，避免其中一条路径漏掉 flagTerminator。
func helperArgs(role, heartbeat string) []string {
	return []string{helperRunFlag, flagTerminator, helperMarker, role, heartbeat}
}

// TestProcessTreeHelper 是辅助进程入口。普通测试运行时立即跳过；只有被本包重新执行（带
// helperMarker）时才进入辅助进程角色。
func TestProcessTreeHelper(t *testing.T) {
	role, heartbeat, ok := helperRole()
	if !ok {
		t.Skip("辅助进程入口，仅在被本包重新执行时生效")
	}
	runHelper(role, heartbeat)
}

func helperRole() (string, string, bool) {
	for index, argument := range os.Args {
		if argument == helperMarker && index+2 < len(os.Args) {
			return os.Args[index+1], os.Args[index+2], true
		}
	}
	return "", "", false
}

func runHelper(role, heartbeat string) {
	switch role {
	case roleSpawnExit:
		if err := spawnHolder(heartbeat); err != nil {
			os.Exit(2)
		}
		// 直接子进程以成功状态退出，孙进程继续持有同一批管道写端。
		os.Exit(0)
	case roleSpawnHold:
		if err := spawnHolder(heartbeat); err != nil {
			os.Exit(2)
		}
		time.Sleep(2 * time.Minute)
		os.Exit(0)
	case roleHold:
		holdAndBeat(heartbeat)
		os.Exit(0)
	}
	os.Exit(3)
}

// spawnHolder 派生孙进程，并把自己的 stdout/stderr 原样交给它，使孙进程继承同一批管道写端。
func spawnHolder(heartbeat string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	command := exec.Command(executable, helperArgs(roleHold, heartbeat)...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Start()
}

func holdAndBeat(heartbeat string) {
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		file, err := os.OpenFile(heartbeat, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = file.Write([]byte{'.'})
			_ = file.Close()
		}
		time.Sleep(40 * time.Millisecond)
	}
}

// blackHole 是普通 io.Writer（不是 *os.File），因此 os/exec 必然为它创建管道与拷贝
// goroutine——这正是 toolrunner 的真实形态（digestWriter），也是 Wait 会被管道钉住的前提。
type blackHole struct {
	mu sync.Mutex
	n  int64
}

func (w *blackHole) Write(value []byte) (int, error) {
	w.mu.Lock()
	w.n += int64(len(value))
	w.mu.Unlock()
	return len(value), nil
}

func helperCommand(t *testing.T, role, heartbeat string) ports.Command {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("无法定位测试二进制: %v", err)
	}
	return ports.Command{
		Path:   executable,
		Args:   helperArgs(role, heartbeat),
		Stdout: &blackHole{},
		Stderr: &blackHole{},
	}
}

func heartbeatSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func waitForHeartbeat(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if heartbeatSize(t, path) > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("孙进程始终没有开始写心跳，测试前提不成立")
}

// assertHeartbeatStopped 断言孙进程确实已经死亡：给强杀一点生效时间之后，心跳不再增长。
func assertHeartbeatStopped(t *testing.T, path string) {
	t.Helper()
	if heartbeatSize(t, path) == 0 {
		t.Fatal("孙进程从未存活过，存活性断言无意义")
	}
	time.Sleep(400 * time.Millisecond)
	first := heartbeatSize(t, path)
	time.Sleep(600 * time.Millisecond)
	if second := heartbeatSize(t, path); second != first {
		t.Fatalf("孙进程在 Wait 返回之后仍在运行: 心跳 %d -> %d", first, second)
	}
}

// TestWaitIsBoundedWhenGrandchildKeepsPipesOpen 覆盖 WaitDelay 承重的那条缺口：直接子进程已经
// 正常退出，但它派生的孙进程仍持有 stdout/stderr 管道写端。没有 WaitDelay 时 cmd.Wait 会永久
// 阻塞；这里必须在 WaitDelay 之后返回 exec.ErrWaitDelay，并顺带清掉残留的孙进程。
func TestWaitIsBoundedWhenGrandchildKeepsPipesOpen(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过真实进程用例")
	}
	const waitDelay = 900 * time.Millisecond
	heartbeat := filepath.Join(t.TempDir(), "heartbeat")
	controller := process.Controller{WaitDelay: waitDelay}
	// 刻意不设置 context 超时：本例证明的是「子进程已退出但管道未关闭」这条独立路径。
	// context.Background().Done() 为 nil，os/exec 不会起 watchCtx，WaitDelay 计时只能由
	// 「Wait 观察到子进程已退出」触发，正是本例要证明的那条路径。
	running, err := controller.Start(context.Background(), helperCommand(t, roleSpawnExit, heartbeat))
	if err != nil {
		t.Fatalf("启动辅助进程失败: %v", err)
	}
	// 先确认孙进程真的活着：它此刻正持有继承来的管道写端，Wait 才有被钉住的前提。直接进入
	// Wait 会与孙进程启动竞争，可能在它写下第一个字节之前就把整棵树收掉。
	waitForHeartbeat(t, heartbeat)
	started := time.Now()
	waitErr := running.Wait()
	elapsed := time.Since(started)
	if elapsed > 30*time.Second {
		t.Fatalf("Wait 未在有界时间内返回: %v", elapsed)
	}
	if !errors.Is(waitErr, exec.ErrWaitDelay) {
		t.Fatalf("Wait 错误 = %v，期望 exec.ErrWaitDelay（说明管道是被 WaitDelay 强制关闭的）", waitErr)
	}
	if elapsed < waitDelay {
		t.Fatalf("Wait 在 WaitDelay 之前就返回了: %v", elapsed)
	}
	assertHeartbeatStopped(t, heartbeat)
}

// TestContextTimeoutKillsWholeProcessTree 覆盖执行超时这条边界：context 到期时整棵树必须被
// 终止。WaitDelay 特意设得远大于允许耗时，因此 Wait 快速返回只能来自树被杀掉、管道随之关闭，
// 而不是来自 WaitDelay 兜底。
func TestContextTimeoutKillsWholeProcessTree(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过真实进程用例")
	}
	heartbeat := filepath.Join(t.TempDir(), "heartbeat")
	controller := process.Controller{WaitDelay: 60 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	running, err := controller.Start(ctx, helperCommand(t, roleSpawnHold, heartbeat))
	if err != nil {
		t.Fatalf("启动辅助进程失败: %v", err)
	}
	waitForHeartbeat(t, heartbeat)
	started := time.Now()
	waitErr := running.Wait()
	elapsed := time.Since(started)
	if waitErr == nil {
		t.Fatal("超时之后 Wait 报告成功")
	}
	if elapsed > 20*time.Second {
		t.Fatalf("超时之后 Wait 未有界返回: %v（WaitDelay=60s，说明进程树没有被杀）", elapsed)
	}
	assertHeartbeatStopped(t, heartbeat)
}

// TestKillTerminatesWholeProcessTree 覆盖 toolrunner 溢出路径依赖的强杀：Kill 之后 Wait 必须
// 有界返回，且孙进程必须一起死亡。同样用远大于允许耗时的 WaitDelay 排除兜底路径。
func TestKillTerminatesWholeProcessTree(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过真实进程用例")
	}
	heartbeat := filepath.Join(t.TempDir(), "heartbeat")
	controller := process.Controller{WaitDelay: 60 * time.Second}
	running, err := controller.Start(context.Background(), helperCommand(t, roleSpawnHold, heartbeat))
	if err != nil {
		t.Fatalf("启动辅助进程失败: %v", err)
	}
	waitForHeartbeat(t, heartbeat)
	started := time.Now()
	if err := running.Kill(); err != nil {
		t.Fatalf("强杀失败: %v", err)
	}
	waitErr := running.Wait()
	elapsed := time.Since(started)
	if waitErr == nil {
		t.Fatal("强杀之后 Wait 报告成功")
	}
	if elapsed > 20*time.Second {
		t.Fatalf("强杀之后 Wait 未有界返回: %v（WaitDelay=60s，说明只杀掉了直接子进程）", elapsed)
	}
	assertHeartbeatStopped(t, heartbeat)
}

// TestStartRejectsEmptyPath 保持 ProcessController 只接受显式可执行路径的约束。
func TestStartRejectsEmptyPath(t *testing.T) {
	if _, err := (process.Controller{}).Start(context.Background(), ports.Command{}); err == nil {
		t.Fatal("空 path 未被拒绝")
	}
}
