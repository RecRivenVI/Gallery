package toolrunner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/ports"
	"github.com/RecRivenVI/gallery/internal/storage"
	"github.com/RecRivenVI/gallery/internal/toolrunner"
)

type resolver struct{}

func (resolver) Available(string) bool { return true }

func (resolver) Resolve(context.Context, string, []string, string) (ports.Command, error) {
	return ports.Command{Path: "allowed-tool", Args: []string{"--version"}}, nil
}

type capturingResolver struct{ workingDir string }

func (*capturingResolver) Available(string) bool { return true }

func (r *capturingResolver) Resolve(_ context.Context, _ string, _ []string, workingDir string) (ports.Command, error) {
	r.workingDir = workingDir
	return ports.Command{Path: "allowed-tool", Args: []string{"--version"}}, nil
}

type selectiveResolver struct{ available string }

func (r selectiveResolver) Available(toolID string) bool { return toolID == r.available }
func (selectiveResolver) Resolve(context.Context, string, []string, string) (ports.Command, error) {
	return ports.Command{Path: "allowed-tool"}, nil
}

type unavailableAtResolve struct{}

func (unavailableAtResolve) Available(string) bool { return true }
func (unavailableAtResolve) Resolve(context.Context, string, []string, string) (ports.Command, error) {
	return ports.Command{}, fault.New(fault.CodeExternalToolUnavailable, false, errors.New("工具摘要已变化"))
}

// exitingController 模拟正常退出的工具：写出少量输出后进程结束。
type exitingController struct{}

func (exitingController) SupportsLimits() bool { return true }

func (exitingController) Start(_ context.Context, command ports.Command) (ports.Process, error) {
	_, _ = io.WriteString(command.Stdout, "stdout\n")
	_, _ = io.WriteString(command.Stderr, "stderr\n")
	return exited{}, nil
}

type recordingController struct {
	command ports.Command
}

func (*recordingController) SupportsLimits() bool { return true }

func (c *recordingController) Start(_ context.Context, command ports.Command) (ports.Process, error) {
	c.command = command
	return exited{}, nil
}

type unsupportedLimitController struct{ exitingController }

func (unsupportedLimitController) SupportsLimits() bool { return false }

type exited struct{}

func (exited) Wait() error { return nil }
func (exited) Kill() error { return nil }

// livingProcess 是可观察的假进程：记录 Kill 调用次数，并且 Wait 真的阻塞，只有被强杀或
// 运行 context 结束才返回。旧假进程的 Wait 硬编码 return nil、Kill 硬编码 return nil，
// 因此三条边界（输出上限、执行超时、强杀）一条都没被触及。
type livingProcess struct {
	ctx    context.Context
	killed chan struct{}
	once   sync.Once

	mu    sync.Mutex
	kills int
}

func newLivingProcess(ctx context.Context) *livingProcess {
	return &livingProcess{ctx: ctx, killed: make(chan struct{})}
}

func (p *livingProcess) Kill() error {
	p.mu.Lock()
	p.kills++
	p.mu.Unlock()
	p.once.Do(func() { close(p.killed) })
	return nil
}

func (p *livingProcess) Wait() error {
	select {
	case <-p.killed:
		return errors.New("signal: killed")
	case <-p.ctx.Done():
		return p.ctx.Err()
	}
}

func (p *livingProcess) killCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.kills
}

// floodController 模拟「忽略写错误、把输出刷爆上限」的工具：进程在 Start 返回之后仍然存活，
// 写满上限后只有强杀才能让它停下来。
type floodController struct {
	chunk   int
	repeat  int
	mu      sync.Mutex
	process *livingProcess
	flooded chan struct{}
}

func (*floodController) SupportsLimits() bool { return true }

func newFloodController(chunk, repeat int) *floodController {
	return &floodController{chunk: chunk, repeat: repeat, flooded: make(chan struct{})}
}

func (c *floodController) Start(ctx context.Context, command ports.Command) (ports.Process, error) {
	process := newLivingProcess(ctx)
	c.mu.Lock()
	c.process = process
	c.mu.Unlock()
	go func() {
		payload := bytes.Repeat([]byte("x"), c.chunk)
		for i := 0; i < c.repeat; i++ {
			// 行为不良的工具忽略 io.ErrShortWrite 继续写。
			_, _ = command.Stdout.Write(payload)
		}
		close(c.flooded)
		// 只有强杀或运行 context 结束才让它退出。
		select {
		case <-process.killed:
		case <-ctx.Done():
		}
	}()
	return process, nil
}

func (c *floodController) started() *livingProcess {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.process
}

// eagerFloodController 在 Start 返回之前就把输出写满，覆盖「溢出早于进程可见」的补发路径。
type eagerFloodController struct {
	chunk   int
	repeat  int
	mu      sync.Mutex
	process *livingProcess
}

func (*eagerFloodController) SupportsLimits() bool { return true }

func (c *eagerFloodController) Start(ctx context.Context, command ports.Command) (ports.Process, error) {
	payload := bytes.Repeat([]byte("x"), c.chunk)
	for i := 0; i < c.repeat; i++ {
		_, _ = command.Stderr.Write(payload)
	}
	process := newLivingProcess(ctx)
	c.mu.Lock()
	c.process = process
	c.mu.Unlock()
	return process, nil
}

func (c *eagerFloodController) started() *livingProcess {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.process
}

// hangController 模拟既不输出也不退出的工具：Wait 只有在运行 context 到期时才返回。
type hangController struct {
	mu      sync.Mutex
	process *livingProcess
}

func (*hangController) SupportsLimits() bool { return true }

func (c *hangController) Start(ctx context.Context, _ ports.Command) (ports.Process, error) {
	process := newLivingProcess(ctx)
	c.mu.Lock()
	c.process = process
	c.mu.Unlock()
	return process, nil
}

func (c *hangController) started() *livingProcess {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.process
}

func newJobStore(t *testing.T, hour int) (*jobs.Store, *storage.Store, appdirs.Dirs) {
	t.Helper()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := clock.Fixed{Time: time.Date(2026, 7, 18, hour, 0, 0, 0, time.UTC)}
	jobStore, err := jobs.NewStore(store.Control.SQL(), now, identity.NewGenerator(now))
	if err != nil {
		t.Fatal(err)
	}
	return jobStore, store, dirs
}

func TestExecutePersistsBoundedToolOutputDigest(t *testing.T) {
	jobStore, _, _ := newJobStore(t, 3)
	service, err := toolrunner.New(jobStore, exitingController{}, resolver{})
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.Create(context.Background(), toolrunner.Request{ToolID: "ffprobe", Args: []string{"--version"}, TimeoutSeconds: 2, MaxOutputBytes: 1024}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	completed, err := jobStore.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != jobs.StatusCompleted {
		t.Fatalf("外部工具 Job 未完成: %+v", completed)
	}
	var result toolrunner.Result
	if err := json.Unmarshal(completed.ResultJSON, &result); err != nil {
		t.Fatal(err)
	}
	if result.StdoutBytes != int64(len("stdout\n")) || result.StderrBytes != int64(len("stderr\n")) || result.StdoutSHA256 == "" || result.StderrSHA256 == "" {
		t.Fatalf("外部工具输出摘要不完整: %+v", result)
	}
}

func TestCreateFreezesAndExecuteEnforcesProcessLimits(t *testing.T) {
	jobStore, _, _ := newJobStore(t, 13)
	controller := &recordingController{}
	service, err := toolrunner.New(jobStore, controller, resolver{})
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.Create(context.Background(), toolrunner.Request{
		ToolID: "ffprobe", TimeoutSeconds: 9, MaxOutputBytes: 1024,
	}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	var frozen toolrunner.Request
	if err := json.Unmarshal(job.RequestJSON, &frozen); err != nil {
		t.Fatal(err)
	}
	if frozen.MaxMemoryBytes != toolrunner.DefaultMemoryLimitBytes || frozen.MaxCPUTimeSeconds != 9 {
		t.Fatalf("Job 未冻结默认进程限制: %+v", frozen)
	}
	if err := service.Execute(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if controller.command.Limits.MemoryBytes != uint64(toolrunner.DefaultMemoryLimitBytes) ||
		controller.command.Limits.CPUTime != 9*time.Second {
		t.Fatalf("执行命令未携带冻结限制: %+v", controller.command.Limits)
	}
}

func TestExecuteAddsLimitsToLegacyPersistedRequest(t *testing.T) {
	jobStore, _, _ := newJobStore(t, 15)
	controller := &recordingController{}
	service, err := toolrunner.New(jobStore, controller, resolver{})
	if err != nil {
		t.Fatal(err)
	}
	legacyPayload := json.RawMessage(`{"toolId":"ffprobe","args":["-version"],"timeoutSeconds":7,"maxOutputBytes":1024}`)
	job, err := jobStore.CreateWithOptions(context.Background(), "external_tool", "", "owner", jobs.CreateOptions{
		ResourceClass: jobs.ResourceExternalTool, RequestJSON: legacyPayload, MaxRetries: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if controller.command.Limits.MemoryBytes != uint64(toolrunner.DefaultMemoryLimitBytes) ||
		controller.command.Limits.CPUTime != 7*time.Second {
		t.Fatalf("旧 Job 未补齐安全限制: %+v", controller.command.Limits)
	}
}

// TestExecuteKillsProcessWhenOutputExceedsLimit 真正把输出上限打满：假进程写 limit+1 字节
// 之后仍然存活且不理会写错误，Execute 必须在进程存活期间立即调用 Kill，而不是等到超时。
func TestExecuteKillsProcessWhenOutputExceedsLimit(t *testing.T) {
	const limit = 1024
	jobStore, _, _ := newJobStore(t, 5)
	controller := newFloodController(limit/2+1, 2) // 2 * (limit/2+1) = limit+2 > limit
	service, err := toolrunner.New(jobStore, controller, resolver{})
	if err != nil {
		t.Fatal(err)
	}
	// 超时给到 60 秒：如果强杀失效，Execute 只能靠超时返回，下面的耗时断言会失败。
	job, err := service.Create(context.Background(), toolrunner.Request{ToolID: "ffprobe", TimeoutSeconds: 60, MaxOutputBytes: limit}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	execErr := service.Execute(context.Background(), job.ID)
	elapsed := time.Since(started)
	if execErr == nil {
		t.Fatal("输出溢出没有让 Execute 失败")
	}
	if elapsed > 10*time.Second {
		t.Fatalf("溢出后 Execute 未立即收敛，耗时 %v（说明是超时而不是强杀让它返回）", elapsed)
	}
	select {
	case <-controller.flooded:
	case <-time.After(5 * time.Second):
		t.Fatal("假进程没有写满上限")
	}
	process := controller.started()
	if process == nil {
		t.Fatal("控制器未记录进程")
	}
	if process.killCount() != 1 {
		t.Fatalf("溢出时 Kill 调用次数 = %d，期望恰好 1 次", process.killCount())
	}
	failed, err := jobStore.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != jobs.StatusFailed || failed.IssueCode != "EXTERNAL_TOOL_FAILED" {
		t.Fatalf("溢出 Job 终态 = %s / %s", failed.Status, failed.IssueCode)
	}
	if len(failed.ResultJSON) != 0 {
		t.Fatalf("溢出 Job 不应留下结果摘要: %s", failed.ResultJSON)
	}
}

// TestExecuteKillsProcessWhenOverflowPrecedesStart 覆盖溢出发生在 Start 返回之前的情况：
// 此时还拿不到 ports.Process，强杀必须在拿到进程之后补发，否则进程会一直活到超时。
func TestExecuteKillsProcessWhenOverflowPrecedesStart(t *testing.T) {
	const limit = 512
	jobStore, _, _ := newJobStore(t, 6)
	controller := &eagerFloodController{chunk: limit, repeat: 2}
	service, err := toolrunner.New(jobStore, controller, resolver{})
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.Create(context.Background(), toolrunner.Request{ToolID: "ffprobe", TimeoutSeconds: 60, MaxOutputBytes: limit}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := service.Execute(context.Background(), job.ID); err == nil {
		t.Fatal("输出溢出没有让 Execute 失败")
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("Start 前溢出未被补发强杀，耗时 %v", elapsed)
	}
	process := controller.started()
	if process == nil || process.killCount() != 1 {
		t.Fatalf("Start 前溢出的 Kill 调用次数不正确: %+v", process)
	}
	failed, err := jobStore.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != jobs.StatusFailed || failed.IssueCode != "EXTERNAL_TOOL_FAILED" {
		t.Fatalf("溢出 Job 终态 = %s / %s", failed.Status, failed.IssueCode)
	}
}

// TestExecuteReturnsBoundedWhenProcessNeverExits 真正让 runCtx 过期：假进程的 Wait 一直阻塞
// 到运行 context 结束。Execute 必须在超时之后有界返回，并把 Job 落到失败终态。
func TestExecuteReturnsBoundedWhenProcessNeverExits(t *testing.T) {
	jobStore, _, _ := newJobStore(t, 7)
	controller := &hangController{}
	service, err := toolrunner.New(jobStore, controller, resolver{})
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.Create(context.Background(), toolrunner.Request{ToolID: "ffprobe", TimeoutSeconds: 1, MaxOutputBytes: 1024}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	execErr := service.Execute(context.Background(), job.ID)
	elapsed := time.Since(started)
	if execErr == nil {
		t.Fatal("超时没有让 Execute 失败")
	}
	if elapsed < time.Second {
		t.Fatalf("Execute 在超时之前就返回了: %v", elapsed)
	}
	if elapsed > 15*time.Second {
		t.Fatalf("Execute 没有在超时后有界返回: %v", elapsed)
	}
	if process := controller.started(); process == nil {
		t.Fatal("控制器未记录进程")
	}
	failed, err := jobStore.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != jobs.StatusFailed || failed.IssueCode != "EXTERNAL_TOOL_FAILED" {
		t.Fatalf("超时 Job 终态 = %s / %s", failed.Status, failed.IssueCode)
	}
}

// TestExecuteHonorsCallerCancellation 证明调用方取消会立即传导到运行 context，不需要等到
// 请求自身的超时。
func TestExecuteHonorsCallerCancellation(t *testing.T) {
	jobStore, _, _ := newJobStore(t, 8)
	controller := &hangController{}
	service, err := toolrunner.New(jobStore, controller, resolver{})
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.Create(context.Background(), toolrunner.Request{ToolID: "ffprobe", TimeoutSeconds: 3600, MaxOutputBytes: 1024}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Execute(ctx, job.ID) }()
	deadline := time.Now().Add(5 * time.Second)
	for controller.started() == nil {
		if time.Now().After(deadline) {
			t.Fatal("外部工具进程始终没有启动")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("取消后 Execute 仍报告成功")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("取消之后 Execute 没有返回")
	}
}

// TestCreateRejectsBoundaryViolations 覆盖 Create 侧声明边界：执行超时/CPU 时间必须落在
// 1~3600 秒，CPU 不得超过墙钟超时，单流输出不得超过 64 MiB，进程树内存不得超过 2 GiB。
func TestCreateRejectsBoundaryViolations(t *testing.T) {
	jobStore, store, _ := newJobStore(t, 9)
	service, err := toolrunner.New(jobStore, exitingController{}, resolver{})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		request toolrunner.Request
	}{
		{"超时为零", toolrunner.Request{ToolID: "ffprobe", TimeoutSeconds: 0}},
		{"超时为负", toolrunner.Request{ToolID: "ffprobe", TimeoutSeconds: -1}},
		{"超时超过上限", toolrunner.Request{ToolID: "ffprobe", TimeoutSeconds: 3601}},
		{"输出上限为负", toolrunner.Request{ToolID: "ffprobe", TimeoutSeconds: 2, MaxOutputBytes: -1}},
		{"输出上限超过硬上限", toolrunner.Request{ToolID: "ffprobe", TimeoutSeconds: 2, MaxOutputBytes: (64 << 20) + 1}},
		{"内存上限为负", toolrunner.Request{ToolID: "ffprobe", TimeoutSeconds: 2, MaxMemoryBytes: -1}},
		{"内存上限超过硬上限", toolrunner.Request{ToolID: "ffprobe", TimeoutSeconds: 2, MaxMemoryBytes: toolrunner.MaxMemoryLimitBytes + 1}},
		{"CPU 上限为负", toolrunner.Request{ToolID: "ffprobe", TimeoutSeconds: 2, MaxCPUTimeSeconds: -1}},
		{"CPU 上限超过墙钟", toolrunner.Request{ToolID: "ffprobe", TimeoutSeconds: 2, MaxCPUTimeSeconds: 3}},
		{"缺少 ToolID", toolrunner.Request{TimeoutSeconds: 2}},
	}
	for _, item := range cases {
		if _, err := service.Create(context.Background(), item.request, "owner"); err == nil {
			t.Fatalf("%s 未被拒绝", item.name)
		}
	}
	var count int
	if err := store.Control.SQL().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM jobs").Scan(&count); err != nil || count != 0 {
		t.Fatalf("越界请求污染了 Job 表: count=%d err=%v", count, err)
	}
	// 上边界本身必须被接受，避免把边界检查写成 off-by-one。
	if _, err := service.Create(context.Background(), toolrunner.Request{
		ToolID: "ffprobe", TimeoutSeconds: 3600, MaxOutputBytes: 64 << 20,
		MaxMemoryBytes: toolrunner.MaxMemoryLimitBytes, MaxCPUTimeSeconds: toolrunner.MaxCPUTimeSeconds,
	}, "owner"); err != nil {
		t.Fatalf("边界内请求被拒绝: %v", err)
	}
}

func TestCreateRejectsPlatformWithoutHardLimitsBeforePersistingJob(t *testing.T) {
	jobStore, store, _ := newJobStore(t, 14)
	service, err := toolrunner.New(jobStore, unsupportedLimitController{}, resolver{})
	if err != nil {
		t.Fatal(err)
	}
	if service.Available() {
		t.Fatal("不支持进程树硬限制的平台错误报告外部工具可用")
	}
	_, err = service.Create(context.Background(), toolrunner.Request{ToolID: "ffprobe", TimeoutSeconds: 2}, "owner")
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeExternalToolUnavailable {
		t.Fatalf("不支持硬限制的平台错误 = %v", err)
	}
	var count int
	if err := store.Control.SQL().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM jobs").Scan(&count); err != nil || count != 0 {
		t.Fatalf("不支持硬限制的平台污染了 Job 表: count=%d err=%v", count, err)
	}
}

func TestToolAvailabilityAndOwnedWorkingDirectory(t *testing.T) {
	ctx := context.Background()
	jobStore, store, dirs := newJobStore(t, 4)
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 4, 0, 0, 0, time.UTC)}
	unavailable, _ := toolrunner.New(jobStore, exitingController{}, nil)
	if unavailable.Available() {
		t.Fatal("未配置 ToolDiscovery 时错误报告为可用")
	}
	if _, err := unavailable.Create(ctx, toolrunner.Request{ToolID: "ffprobe", TimeoutSeconds: 2}, "owner"); err == nil {
		t.Fatal("未配置 ToolDiscovery 仍创建了必然失败的 Job")
	}
	selective, _ := toolrunner.New(jobStore, exitingController{}, selectiveResolver{available: "ffprobe"})
	if _, err := selective.Create(ctx, toolrunner.Request{ToolID: "ffmpeg", TimeoutSeconds: 2}, "owner"); err == nil {
		t.Fatal("未通过 ToolDiscovery 的具体工具仍创建了必然失败的 Job")
	}
	var count int
	if err := store.Control.SQL().QueryRowContext(ctx, "SELECT COUNT(*) FROM jobs").Scan(&count); err != nil || count != 0 {
		t.Fatalf("不可用请求污染了 Job 表: count=%d err=%v", count, err)
	}

	resolver := &capturingResolver{}
	service, _ := toolrunner.New(jobStore, exitingController{}, resolver)
	tempStore, _ := jobs.NewTempStore(store.Control.SQL(), dirs.Temp, now)
	service.SetTempStore(tempStore)
	job, err := service.Create(ctx, toolrunner.Request{ToolID: "ffprobe", TimeoutSeconds: 2}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	expectedRoot := filepath.Join(dirs.Temp, "jobs") + string(filepath.Separator)
	if !strings.HasPrefix(filepath.Clean(resolver.workingDir)+string(filepath.Separator), expectedRoot) {
		t.Fatalf("外部工具未使用 Job 所有的工作目录: %q", resolver.workingDir)
	}
	if _, err := os.Stat(filepath.Join(resolver.workingDir, "manifest.json")); err != nil {
		t.Fatalf("外部工具工作目录缺少 manifest: %v", err)
	}
}

func TestExecutePreservesToolDiscoveryUnavailableCode(t *testing.T) {
	ctx := context.Background()
	jobStore, _, _ := newJobStore(t, 12)
	service, err := toolrunner.New(jobStore, exitingController{}, unavailableAtResolve{})
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.Create(ctx, toolrunner.Request{ToolID: "ffprobe", TimeoutSeconds: 2}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	err = service.Execute(ctx, job.ID)
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeExternalToolUnavailable {
		t.Fatalf("执行期工具替换错误 = %v", err)
	}
	failed, err := jobStore.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != jobs.StatusFailed || failed.IssueCode != string(fault.CodeExternalToolUnavailable) {
		t.Fatalf("Job 终态 = %s / %s", failed.Status, failed.IssueCode)
	}
}
