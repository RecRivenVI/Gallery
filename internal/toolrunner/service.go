package toolrunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/ports"
)

const (
	DefaultOutputLimit      = 8 << 20
	DefaultMemoryLimitBytes = 512 << 20
	MaxMemoryLimitBytes     = 2 << 30
	MaxCPUTimeSeconds       = 3600
)

type Request struct {
	ToolID            string   `json:"toolId"`
	Args              []string `json:"args"`
	WorkingDir        string   `json:"workingDir,omitempty"`
	TimeoutSeconds    int64    `json:"timeoutSeconds"`
	MaxOutputBytes    int64    `json:"maxOutputBytes"`
	MaxMemoryBytes    int64    `json:"maxMemoryBytes"`
	MaxCPUTimeSeconds int64    `json:"maxCpuTimeSeconds"`
}

type Resolver interface {
	Available(toolID string) bool
	Resolve(ctx context.Context, toolID string, args []string, workingDir string) (ports.Command, error)
}

type SpaceGate interface {
	CheckSpace(ctx context.Context, operation string, additionalBytes int64) error
}

type Result struct {
	StdoutBytes  int64  `json:"stdoutBytes"`
	StderrBytes  int64  `json:"stderrBytes"`
	StdoutSHA256 string `json:"stdoutSha256"`
	StderrSHA256 string `json:"stderrSha256"`
}

type Service struct {
	jobs     *jobs.Store
	process  ports.ProcessController
	resolver Resolver
	temp     *jobs.TempStore
	space    SpaceGate
}

func New(jobStore *jobs.Store, controller ports.ProcessController, resolver Resolver) (*Service, error) {
	if jobStore == nil || controller == nil {
		return nil, fmt.Errorf("External Tool Service 缺少依赖")
	}
	return &Service{jobs: jobStore, process: controller, resolver: resolver}, nil
}

func (s *Service) Create(ctx context.Context, request Request, createdBy string) (jobs.Job, error) {
	if strings.TrimSpace(request.ToolID) == "" || request.ToolID != strings.TrimSpace(request.ToolID) ||
		strings.TrimSpace(createdBy) == "" {
		return jobs.Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	if err := normalizeRequest(&request); err != nil {
		return jobs.Job{}, err
	}
	if s.resolver == nil || !s.resolver.Available(request.ToolID) {
		return jobs.Job{}, fault.New(fault.CodeExternalToolUnavailable, false, nil)
	}
	if !s.process.SupportsLimits() {
		return jobs.Job{}, fault.New(fault.CodeExternalToolUnavailable, false,
			errors.New("当前平台不支持外部工具进程树硬限制"))
	}
	if s.space != nil {
		if err := s.space.CheckSpace(ctx, "external_tool", request.MaxOutputBytes*2); err != nil {
			return jobs.Job{}, err
		}
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return jobs.Job{}, fault.New(fault.CodeInternal, true, err)
	}
	return s.jobs.CreateWithOptions(ctx, "external_tool", "", createdBy, jobs.CreateOptions{
		ResourceClass: jobs.ResourceExternalTool, RequestJSON: payload, MaxRetries: 1,
	})
}

func (s *Service) Available() bool {
	return s != nil && s.resolver != nil && s.process != nil && s.process.SupportsLimits()
}

func (s *Service) SetTempStore(store *jobs.TempStore) { s.temp = store }

func (s *Service) SetSpaceGate(gate SpaceGate) { s.space = gate }

func (s *Service) Execute(ctx context.Context, jobID string) error {
	job, err := s.jobs.StartStage(ctx, jobID, "running_tool")
	if err != nil {
		return err
	}
	var request Request
	if err := json.Unmarshal(job.RequestJSON, &request); err != nil {
		return s.fail(ctx, jobID, fault.New(fault.CodeValidation, false, err))
	}
	// 旧版已排队 Job 没有两个硬限制字段。执行前补齐同一组安全默认值；畸形持久请求则
	// fail-closed，不能把反序列化出的零值解释成无限制。
	if err := normalizeRequest(&request); err != nil {
		return s.fail(ctx, jobID, err)
	}
	if !s.process.SupportsLimits() {
		return s.fail(ctx, jobID, fault.New(fault.CodeExternalToolUnavailable, false,
			errors.New("当前平台不支持外部工具进程树硬限制")))
	}
	if s.resolver == nil {
		return s.fail(ctx, jobID, fault.New(fault.CodeExternalToolUnavailable, false, errors.New("ToolDiscovery 未配置")))
	}
	if s.temp != nil {
		workingDirectory, tempErr := s.temp.Acquire(ctx, job, []string{"stdout", "stderr"})
		if tempErr != nil {
			return s.fail(ctx, jobID, tempErr)
		}
		request.WorkingDir = workingDirectory
	}
	command, err := s.resolver.Resolve(ctx, request.ToolID, request.Args, request.WorkingDir)
	if err != nil {
		return s.fail(ctx, jobID, err)
	}
	if command.Path == "" {
		return s.fail(ctx, jobID, fault.New(fault.CodeExternalToolFailed, false, errors.New("ToolDiscovery 返回空路径")))
	}
	// Resolver 只决定允许执行的路径和参数；资源预算来自持久 Job 快照，不能被 Resolver
	// 放宽或覆盖。
	command.Limits = ports.ProcessLimits{
		MemoryBytes: uint64(request.MaxMemoryBytes),
		CPUTime:     time.Duration(request.MaxCPUTimeSeconds) * time.Second,
	}
	limit := request.MaxOutputBytes
	if limit <= 0 {
		limit = DefaultOutputLimit
	}
	killer := &killSwitch{}
	stdout := &digestWriter{limit: limit, sum: sha256.New(), onOverflow: killer.trigger}
	stderr := &digestWriter{limit: limit, sum: sha256.New(), onOverflow: killer.trigger}
	command.Stdout, command.Stderr = stdout, stderr
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(request.TimeoutSeconds)*time.Second)
	defer cancel()
	killer.arm(cancel)
	process, err := s.process.Start(runCtx, command)
	if err != nil {
		return s.fail(ctx, jobID, fault.New(fault.CodeExternalToolFailed, true, err))
	}
	// 输出可能在 Start 返回之前就已经写满，attach 负责补发那种情况下漏掉的强杀。
	killer.attach(process)
	waitErr := process.Wait()
	if overflowed := stdout.overflowed() || stderr.overflowed(); waitErr != nil || overflowed {
		if waitErr == nil {
			waitErr = errOutputLimitExceeded
		}
		return s.fail(ctx, jobID, fault.New(fault.CodeExternalToolFailed, true, waitErr))
	}
	result, err := json.Marshal(Result{StdoutBytes: stdout.total(), StderrBytes: stderr.total(),
		StdoutSHA256: stdout.digest(), StderrSHA256: stderr.digest()})
	if err != nil {
		return s.fail(ctx, jobID, fault.New(fault.CodeInternal, true, err))
	}
	_, err = s.jobs.CompleteWithResult(ctx, jobID, result)
	return err
}

var errOutputLimitExceeded = errors.New("外部工具输出超过上限")

func normalizeRequest(request *Request) error {
	if strings.TrimSpace(request.ToolID) == "" || request.ToolID != strings.TrimSpace(request.ToolID) ||
		request.TimeoutSeconds <= 0 || request.TimeoutSeconds > MaxCPUTimeSeconds ||
		request.MaxOutputBytes < 0 || request.MaxMemoryBytes < 0 || request.MaxCPUTimeSeconds < 0 {
		return fault.New(fault.CodeValidation, false, nil)
	}
	if request.MaxOutputBytes == 0 {
		request.MaxOutputBytes = DefaultOutputLimit
	}
	if request.MaxMemoryBytes == 0 {
		request.MaxMemoryBytes = DefaultMemoryLimitBytes
	}
	if request.MaxCPUTimeSeconds == 0 {
		request.MaxCPUTimeSeconds = request.TimeoutSeconds
	}
	if request.MaxOutputBytes > 64<<20 || request.MaxMemoryBytes > MaxMemoryLimitBytes ||
		request.MaxCPUTimeSeconds > MaxCPUTimeSeconds || request.MaxCPUTimeSeconds > request.TimeoutSeconds {
		return fault.New(fault.CodeValidation, false, nil)
	}
	return nil
}

// digestWriter 只把数据喂给 sha256，从不缓冲，内存占用与输出体量无关（O(1)）。
//
// 超过上限时返回 io.ErrShortWrite，让 os/exec 的拷贝器停止并关闭读端；行为良好的子进程会
// 因管道破裂退出。但忽略写错误、或干脆不再写只自旋的子进程不会退出，所以这里还必须在写入侧
// 立即触发 onOverflow 强杀，而不是等 Wait 返回之后才发现溢出。
type digestWriter struct {
	limit      int64
	onOverflow func()

	mu       sync.Mutex
	n        int64
	sum      hash.Hash
	overflow bool
}

func (w *digestWriter) Write(value []byte) (int, error) {
	w.mu.Lock()
	if w.n+int64(len(value)) > w.limit {
		first := !w.overflow
		w.overflow = true
		w.mu.Unlock()
		if first && w.onOverflow != nil {
			w.onOverflow()
		}
		return 0, io.ErrShortWrite
	}
	n, err := w.sum.Write(value)
	w.n += int64(n)
	w.mu.Unlock()
	return n, err
}

func (w *digestWriter) overflowed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.overflow
}

func (w *digestWriter) total() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.n
}

func (w *digestWriter) digest() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return hex.EncodeToString(w.sum.Sum(nil))
}

// killSwitch 让输出上限在进程仍存活时就能生效，同时把 ports.Process.Kill 真正接进执行路径
// （在此之前 toolrunner 从不调用 Kill，唯一的杀进程路径是 exec.CommandContext 的取消）。
//
// 第一次溢出时它做两件事：强杀进程树，并取消运行 context。取消是必要的第二手——它让 os/exec
// 的 WaitDelay 开始计时，兜住「孙进程仍持有管道」导致 Wait 不返回的情况。
//
// 溢出可能发生在 Start 返回之前（进程一启动就写满输出），此时还拿不到 ports.Process，因此
// fired 与 attach 之间必须能互相补发。
type killSwitch struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	process ports.Process
	fired   bool
	killed  bool
}

func (k *killSwitch) arm(cancel context.CancelFunc) {
	k.mu.Lock()
	k.cancel = cancel
	k.mu.Unlock()
}

func (k *killSwitch) attach(process ports.Process) {
	k.mu.Lock()
	k.process = process
	fired := k.fired
	k.mu.Unlock()
	if fired {
		k.enforce()
	}
}

func (k *killSwitch) trigger() {
	k.mu.Lock()
	if k.fired {
		k.mu.Unlock()
		return
	}
	k.fired = true
	k.mu.Unlock()
	k.enforce()
}

// enforce 保证 Kill 至多调用一次；进程尚未 attach 时只取消 context，由 attach 补发强杀。
func (k *killSwitch) enforce() {
	k.mu.Lock()
	cancel := k.cancel
	process := k.process
	if process != nil && !k.killed {
		k.killed = true
	} else {
		process = nil
	}
	k.mu.Unlock()
	if process != nil {
		_ = process.Kill()
	}
	if cancel != nil {
		cancel()
	}
}

func (s *Service) fail(ctx context.Context, jobID string, err error) error {
	code, retryable := fault.CodeExternalToolFailed, true
	var structured *fault.Error
	if errors.As(err, &structured) {
		code, retryable = structured.Code, structured.Retryable
	}
	_, _ = s.jobs.FailWithRetryable(ctx, jobID, string(code), retryable)
	return err
}
