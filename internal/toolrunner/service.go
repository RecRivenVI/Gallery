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
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/ports"
)

const DefaultOutputLimit = 8 << 20

type Request struct {
	ToolID         string   `json:"toolId"`
	Args           []string `json:"args"`
	WorkingDir     string   `json:"workingDir,omitempty"`
	TimeoutSeconds int64    `json:"timeoutSeconds"`
	MaxOutputBytes int64    `json:"maxOutputBytes"`
}

type Resolver interface {
	Resolve(ctx context.Context, toolID string, args []string, workingDir string) (ports.Command, error)
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
}

func New(jobStore *jobs.Store, controller ports.ProcessController, resolver Resolver) (*Service, error) {
	if jobStore == nil || controller == nil {
		return nil, fmt.Errorf("External Tool Service 缺少依赖")
	}
	return &Service{jobs: jobStore, process: controller, resolver: resolver}, nil
}

func (s *Service) Create(ctx context.Context, request Request, createdBy string) (jobs.Job, error) {
	if strings.TrimSpace(request.ToolID) == "" || strings.TrimSpace(createdBy) == "" || request.TimeoutSeconds <= 0 || request.TimeoutSeconds > 3600 {
		return jobs.Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	if request.MaxOutputBytes <= 0 {
		request.MaxOutputBytes = DefaultOutputLimit
	}
	if request.MaxOutputBytes > 64<<20 {
		return jobs.Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return jobs.Job{}, fault.New(fault.CodeInternal, true, err)
	}
	return s.jobs.CreateWithOptions(ctx, "external_tool", "", createdBy, jobs.CreateOptions{
		ResourceClass: jobs.ResourceExternalTool, RequestJSON: payload, MaxRetries: 1,
	})
}

func (s *Service) Execute(ctx context.Context, jobID string) error {
	job, err := s.jobs.StartStage(ctx, jobID, "running_tool")
	if err != nil {
		return err
	}
	var request Request
	if err := json.Unmarshal(job.RequestJSON, &request); err != nil {
		return s.fail(ctx, jobID, fault.New(fault.CodeValidation, false, err))
	}
	if s.resolver == nil {
		return s.fail(ctx, jobID, fault.New(fault.CodeExternalToolFailed, false, errors.New("ToolDiscovery 未配置")))
	}
	command, err := s.resolver.Resolve(ctx, request.ToolID, request.Args, request.WorkingDir)
	if err != nil || command.Path == "" {
		return s.fail(ctx, jobID, fault.New(fault.CodeExternalToolFailed, false, err))
	}
	limit := request.MaxOutputBytes
	if limit <= 0 {
		limit = DefaultOutputLimit
	}
	stdout, stderr := &digestWriter{limit: limit, sum: sha256.New()}, &digestWriter{limit: limit, sum: sha256.New()}
	command.Stdout, command.Stderr = stdout, stderr
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(request.TimeoutSeconds)*time.Second)
	defer cancel()
	process, err := s.process.Start(runCtx, command)
	if err != nil {
		return s.fail(ctx, jobID, fault.New(fault.CodeExternalToolFailed, true, err))
	}
	waitErr := process.Wait()
	if waitErr != nil || stdout.overflow || stderr.overflow {
		return s.fail(ctx, jobID, fault.New(fault.CodeExternalToolFailed, true, waitErr))
	}
	result, err := json.Marshal(Result{StdoutBytes: stdout.n, StderrBytes: stderr.n,
		StdoutSHA256: hex.EncodeToString(stdout.sum.Sum(nil)), StderrSHA256: hex.EncodeToString(stderr.sum.Sum(nil))})
	if err != nil {
		return s.fail(ctx, jobID, fault.New(fault.CodeInternal, true, err))
	}
	_, err = s.jobs.CompleteWithResult(ctx, jobID, result)
	return err
}

type digestWriter struct {
	limit    int64
	n        int64
	sum      hash.Hash
	overflow bool
}

func (w *digestWriter) Write(value []byte) (int, error) {
	if w.n+int64(len(value)) > w.limit {
		w.overflow = true
		return 0, io.ErrShortWrite
	}
	n, err := w.sum.Write(value)
	w.n += int64(n)
	return n, err
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
