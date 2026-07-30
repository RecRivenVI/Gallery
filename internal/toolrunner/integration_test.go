package toolrunner_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/platform/process"
	"github.com/RecRivenVI/gallery/internal/ports"
	"github.com/RecRivenVI/gallery/internal/toolrunner"
)

// 本文件把 toolrunner.Service 接到真实的 platform/process 控制器上，用真实的长跑进程树跑完
// Execute 的两条收敛路径。service_test.go 的假控制器能精确断言「Kill 被调用了几次」，但证明不了
// 真实进程会不会死；本文件反过来：不看调用次数，只看真实进程树的存活性与真实耗时。
//
// 被执行的「外部工具」是测试二进制自身重新执行，因此不依赖 ffmpeg 等任何外部工具。完整
// toolrunner 用例当前只在实现进程树硬限制的 Windows 上运行；跨平台进程树取消仍由
// internal/platform/process 的共享用例覆盖。工具进程会再派生一个孙进程，孙进程继承同一批 stdout/stderr 管道写端并
// 持续写心跳文件——「整棵树都死了」的判据就是心跳在 Execute 返回之后停止增长。

const (
	toolHelperMarker  = "--gallery-toolrunner-helper"
	toolHelperRunFlag = "-test.run=^TestToolRunnerHelper$"
	// toolFlagTerminator 与 platform/process 的测试同理：辅助进程是测试二进制本身，testing 会
	// 先 flag.Parse，未注册的实参会让它在进入用例之前就 os.Exit(2)。单独的 "--" 终止 flag 解析。
	toolFlagTerminator = "--"

	toolRoleFlood = "flood" // 派生孙进程后把 stdout 刷爆上限，并且忽略写错误永不退出
	toolRoleQuiet = "quiet" // 派生孙进程后既不输出也不退出：只有执行超时能结束它
	toolRoleHold  = "hold"  // 孙进程：持有继承来的管道并写心跳，直到被杀
)

func toolHelperArgs(role, heartbeat string) []string {
	return []string{toolHelperRunFlag, toolFlagTerminator, toolHelperMarker, role, heartbeat}
}

// TestToolRunnerHelper 是辅助进程入口，普通测试运行时立即跳过。
func TestToolRunnerHelper(t *testing.T) {
	role, heartbeat, ok := toolHelperRole()
	if !ok {
		t.Skip("辅助进程入口，仅在被本包重新执行时生效")
	}
	runToolHelper(role, heartbeat)
}

func toolHelperRole() (string, string, bool) {
	for index, argument := range os.Args {
		if argument == toolHelperMarker && index+2 < len(os.Args) {
			return os.Args[index+1], os.Args[index+2], true
		}
	}
	return "", "", false
}

func runToolHelper(role, heartbeat string) {
	switch role {
	case toolRoleFlood:
		if err := spawnToolHolder(heartbeat); err != nil {
			os.Exit(2)
		}
		// 先等孙进程真的活起来再刷爆输出。否则「写满上限」会在几毫秒内发生，而孙进程要等
		// 十几 MB 的测试二进制加载完才写下第一个字节——强杀往往早于它出生，父测试就会看到
		// 一个空心跳文件，误判成「孙进程从未存活」。等待之后，溢出发生时孙进程必然已在持有
		// 继承来的管道写端，这正是本用例要覆盖的形态。
		if !awaitToolHeartbeat(heartbeat) {
			os.Exit(4)
		}
		floodStdout()
		os.Exit(0)
	case toolRoleQuiet:
		if err := spawnToolHolder(heartbeat); err != nil {
			os.Exit(2)
		}
		if !awaitToolHeartbeat(heartbeat) {
			os.Exit(4)
		}
		time.Sleep(2 * time.Minute)
		os.Exit(0)
	case toolRoleHold:
		holdAndBeatTool(heartbeat)
		os.Exit(0)
	}
	os.Exit(3)
}

// awaitToolHeartbeat 在工具进程内等待孙进程写下第一个字节，确保「孙进程已持有管道」这个前提在
// 触发上限之前就成立。
func awaitToolHeartbeat(heartbeat string) bool {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(heartbeat); err == nil && info.Size() > 0 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// spawnToolHolder 派生孙进程并把自己的 stdout/stderr 原样交给它，使孙进程继承同一批管道写端。
func spawnToolHolder(heartbeat string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	command := exec.Command(executable, toolHelperArgs(toolRoleHold, heartbeat)...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Start()
}

// floodStdout 模拟行为不良的工具：忽略 io.ErrShortWrite / 管道破裂，继续写、永不主动退出。
// digestWriter 越界之后 os/exec 会关闭管道读端，这里的写入随即开始失败，但进程不因此结束——
// 所以只有 toolrunner 主动强杀才能让它停下来。
func floodStdout() {
	payload := make([]byte, 64<<10)
	for index := range payload {
		payload[index] = 'x'
	}
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		_, _ = os.Stdout.Write(payload)
		time.Sleep(time.Millisecond)
	}
}

func holdAndBeatTool(heartbeat string) {
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

// binaryResolver 把 ToolDiscovery 解析结果固定为「重新执行测试二进制」。生产解析器仍然是 nil，
// 本文件不改变那条 fail-closed 行为，只是在测试里显式提供一个可执行路径。
type binaryResolver struct {
	role      string
	heartbeat string
}

func (binaryResolver) Available(string) bool { return true }

func (r binaryResolver) Resolve(_ context.Context, _ string, _ []string, _ string) (ports.Command, error) {
	executable, err := os.Executable()
	if err != nil {
		return ports.Command{}, err
	}
	return ports.Command{Path: executable, Args: toolHelperArgs(r.role, r.heartbeat)}, nil
}

func toolHeartbeatSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func waitForToolHeartbeat(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if toolHeartbeatSize(path) > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("工具孙进程始终没有开始写心跳，测试前提不成立")
}

// assertToolTreeDead 断言整棵工具进程树确实死了：心跳不再增长。
func assertToolTreeDead(t *testing.T, path string) {
	t.Helper()
	if toolHeartbeatSize(path) == 0 {
		t.Fatal("工具孙进程从未存活过，存活性断言无意义")
	}
	time.Sleep(400 * time.Millisecond)
	first := toolHeartbeatSize(path)
	time.Sleep(600 * time.Millisecond)
	if second := toolHeartbeatSize(path); second != first {
		t.Fatalf("Execute 返回之后工具进程树仍在运行: 心跳 %d -> %d", first, second)
	}
}

func assertToolJobFailed(t *testing.T, store *jobs.Store, jobID string) {
	t.Helper()
	failed, err := store.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != jobs.StatusFailed || failed.IssueCode != "EXTERNAL_TOOL_FAILED" {
		t.Fatalf("Job 终态 = %s / %s，期望 failed / EXTERNAL_TOOL_FAILED", failed.Status, failed.IssueCode)
	}
	if len(failed.ResultJSON) != 0 {
		t.Fatalf("失败 Job 不应留下结果摘要: %s", failed.ResultJSON)
	}
}

// TestIntegrationOutputOverflowKillsRealProcessTree 用真实进程覆盖输出上限这条边界。
//
// 判定力来自两个刻意拉开的数量级：请求超时 60 秒、WaitDelay 30 秒，而断言只允许 20 秒。因此
// Execute 快速返回既不可能来自执行超时，也不可能来自 WaitDelay 兜底，只可能来自溢出触发的强杀
// 真的把进程树打掉了。孙进程的心跳停止进一步排除「只杀了直接子进程」。
func TestIntegrationOutputOverflowKillsRealProcessTree(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("当前只有 Windows Job Object 实现了外部工具进程树硬限制")
	}
	if testing.Short() {
		t.Skip("短模式跳过真实进程用例")
	}
	heartbeat := filepath.Join(t.TempDir(), "heartbeat")
	jobStore, _, _ := newJobStore(t, 10)
	controller := process.Controller{WaitDelay: 30 * time.Second}
	service, err := toolrunner.New(jobStore, controller, binaryResolver{role: toolRoleFlood, heartbeat: heartbeat})
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.Create(context.Background(), toolrunner.Request{
		ToolID: "ffprobe", TimeoutSeconds: 60, MaxOutputBytes: 1024,
	}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	execErr := service.Execute(context.Background(), job.ID)
	elapsed := time.Since(started)
	if execErr == nil {
		t.Fatal("真实工具写爆输出上限却让 Execute 报告成功")
	}
	if elapsed > 20*time.Second {
		t.Fatalf("溢出之后 Execute 耗时 %v：超时=60s、WaitDelay=30s，说明收敛不是强杀带来的", elapsed)
	}
	waitForToolHeartbeat(t, heartbeat)
	assertToolTreeDead(t, heartbeat)
	assertToolJobFailed(t, jobStore, job.ID)
}

// TestIntegrationTimeoutKillsRealProcessTree 用真实进程覆盖执行超时这条边界：工具既不输出也不
// 退出，孙进程一直持有 stdout/stderr 管道写端。WaitDelay 同样远大于允许耗时，因此 Execute 有界
// 返回只能来自 context 到期时整棵树被杀，而不是 WaitDelay 强行关管道。
func TestIntegrationTimeoutKillsRealProcessTree(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("当前只有 Windows Job Object 实现了外部工具进程树硬限制")
	}
	if testing.Short() {
		t.Skip("短模式跳过真实进程用例")
	}
	heartbeat := filepath.Join(t.TempDir(), "heartbeat")
	jobStore, _, _ := newJobStore(t, 11)
	controller := process.Controller{WaitDelay: 30 * time.Second}
	service, err := toolrunner.New(jobStore, controller, binaryResolver{role: toolRoleQuiet, heartbeat: heartbeat})
	if err != nil {
		t.Fatal(err)
	}
	// 超时给到 3 秒而不是 1 秒：工具进程要先等孙进程启动完毕，1 秒的窗口在负载高的机器上会
	// 让 context 早于孙进程出生就到期，前提便不再成立。
	const timeoutSeconds = 3
	job, err := service.Create(context.Background(), toolrunner.Request{
		ToolID: "ffprobe", TimeoutSeconds: timeoutSeconds, MaxOutputBytes: 1024,
	}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	execErr := service.Execute(context.Background(), job.ID)
	elapsed := time.Since(started)
	if execErr == nil {
		t.Fatal("执行超时却让 Execute 报告成功")
	}
	if elapsed < timeoutSeconds*time.Second {
		t.Fatalf("Execute 在请求超时之前就返回了: %v", elapsed)
	}
	if elapsed > 20*time.Second {
		t.Fatalf("超时之后 Execute 耗时 %v（WaitDelay=30s），说明进程树没有在 context 到期时被杀", elapsed)
	}
	waitForToolHeartbeat(t, heartbeat)
	assertToolTreeDead(t, heartbeat)
	assertToolJobFailed(t, jobStore, job.ID)
}
