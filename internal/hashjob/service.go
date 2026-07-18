package hashjob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/media"
)

type Request struct {
	SourceID             string `json:"sourceId"`
	RelativePath         string `json:"relativePath"`
	ExpectedSize         int64  `json:"expectedSize"`
	ExpectedModTimeNanos int64  `json:"expectedModTimeNanos"`
	HasExpectedIdentity  bool   `json:"hasExpectedIdentity"`
	ParentJobID          string `json:"parentJobId,omitempty"`
}

type Result struct {
	Blob         string `json:"blob"`
	Algorithm    string `json:"algorithm"`
	Digest       string `json:"digest"`
	Size         int64  `json:"size"`
	LocationKey  string `json:"locationKey"`
	RelativePath string `json:"relativePath"`
}

type Dispatcher interface {
	Submit(jobID string) bool
}

type Service struct {
	context           context.Context
	resources         *application.Resources
	jobs              *jobs.Store
	dispatcher        Dispatcher
	wait              sync.WaitGroup
	progressBytes     int64
	progressInterval  time.Duration
	heartbeatInterval time.Duration
}

func New(ctx context.Context, resources *application.Resources, jobStore *jobs.Store) (*Service, error) {
	if ctx == nil || resources == nil || jobStore == nil {
		return nil, fmt.Errorf("Hash Job Service 缺少依赖")
	}
	return &Service{context: ctx, resources: resources, jobs: jobStore,
		progressBytes: 16 << 20, progressInterval: time.Second, heartbeatInterval: 30 * time.Second}, nil
}

func (s *Service) SetDispatcher(dispatcher Dispatcher) { s.dispatcher = dispatcher }

// SetProgressPolicy 只用于运行配置与确定性测试；阈值属于 pre-freeze 调优项，不进入协议。
func (s *Service) SetProgressPolicy(bytes int64, interval, heartbeat time.Duration) {
	if bytes > 0 {
		s.progressBytes = bytes
	}
	if interval > 0 {
		s.progressInterval = interval
	}
	if heartbeat > 0 {
		s.heartbeatInterval = heartbeat
	}
}

func (s *Service) Create(ctx context.Context, request Request, createdBy string) (jobs.Job, error) {
	if request.SourceID == "" || strings.TrimSpace(createdBy) == "" {
		return jobs.Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	normalized, err := media.ValidateRelativePath(request.RelativePath)
	if err != nil {
		return jobs.Job{}, err
	}
	request.RelativePath = normalized
	if request.ExpectedSize < 0 {
		return jobs.Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return jobs.Job{}, fault.New(fault.CodeInternal, true, err)
	}
	// completed digest 只在同一父 Scan Job 内复用。没有父扫描上下文的独立 Hash 请求不设置
	// 幂等键，因此绝不会仅凭 path/size/mtime 跨扫描永久复用结果。
	key := ""
	if request.ParentJobID != "" {
		key = fmt.Sprintf("hash:sha256-v1:%s:%s:%s:%d:%d:%t", request.ParentJobID, request.SourceID,
			request.RelativePath, request.ExpectedSize, request.ExpectedModTimeNanos, request.HasExpectedIdentity)
	}
	return s.jobs.CreateWithOptions(ctx, "hash", request.SourceID, createdBy, jobs.CreateOptions{
		ResourceClass: jobs.ResourceHash, TargetResource: request.RelativePath, RequestJSON: payload,
		IdempotencyKey: key, MaxRetries: 3, RetryPolicyJSON: []byte(`{"kind":"exponential","baseMs":250,"maxMs":30000}`),
	})
}

func (s *Service) Start(jobID string) {
	if s.dispatcher != nil {
		s.dispatcher.Submit(jobID)
		return
	}
	s.wait.Add(1)
	go func() { defer s.wait.Done(); _ = s.Execute(s.context, jobID) }()
}

func (s *Service) Wait() { s.wait.Wait() }

func (s *Service) Reconcile(ctx context.Context) error {
	// 所有资源类别统一由 jobs.Reconciler 提交；保留方法作为兼容调用点。
	return nil
}

func (s *Service) Execute(ctx context.Context, jobID string) error {
	job, err := s.jobs.StartStage(ctx, jobID, "hashing")
	if err != nil {
		return err
	}
	var request Request
	if err := json.Unmarshal(job.RequestJSON, &request); err != nil {
		return s.fail(ctx, jobID, fault.New(fault.CodeValidation, false, err))
	}
	source, err := s.resources.GetSource(ctx, request.SourceID)
	if err != nil {
		return s.fail(ctx, jobID, err)
	}
	var progressErr error
	var latestBytes, persistedBytes int64
	lastPersistedAt := time.Now()
	heartbeatContext, stopHeartbeat := context.WithCancel(ctx)
	var heartbeatWait sync.WaitGroup
	heartbeatWait.Add(1)
	go func() {
		defer heartbeatWait.Done()
		ticker := time.NewTicker(s.heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatContext.Done():
				return
			case <-ticker.C:
				_, _ = s.jobs.Heartbeat(context.Background(), jobID)
			}
		}
	}()
	hashed, hashErr := media.HashSourceFileWithOptions(source.RootPath, request.RelativePath, media.HashOptions{
		Context: ctx, ExpectedSize: request.ExpectedSize, ExpectedModTimeNanos: request.ExpectedModTimeNanos,
		HasExpectedIdentity: request.HasExpectedIdentity,
		Progress: func(bytes int64) {
			latestBytes = bytes
			if progressErr != nil {
				return
			}
			if bytes-persistedBytes < s.progressBytes && time.Since(lastPersistedAt) < s.progressInterval {
				return
			}
			_, progressErr = s.jobs.ProgressDetailed(ctx, jobID, jobs.ProgressUpdate{Stage: "hashing", Current: bytes,
				Total: request.ExpectedSize, Bytes: bytes, Unit: "bytes", Estimated: request.ExpectedSize == 0})
			if progressErr == nil {
				persistedBytes, lastPersistedAt = bytes, time.Now()
			}
		},
	})
	stopHeartbeat()
	heartbeatWait.Wait()
	// 终态前强制刷新最后观测字节；取消请求可能使更新被拒绝，此时终态仍优先。
	if latestBytes > persistedBytes {
		_, flushErr := s.jobs.ProgressDetailed(context.Background(), jobID, jobs.ProgressUpdate{Stage: "hashing",
			Current: latestBytes, Total: request.ExpectedSize, Bytes: latestBytes, Unit: "bytes",
			Estimated: request.ExpectedSize == 0})
		if progressErr == nil && flushErr != nil {
			progressErr = flushErr
		}
	}
	if progressErr != nil {
		hashErr = progressErr
	}
	if hashErr != nil {
		current, _ := s.jobs.Get(context.Background(), jobID)
		if current.CancelRequested || errors.Is(ctx.Err(), context.Canceled) {
			if _, finalizeErr := s.jobs.FinalizeCancelled(context.Background(), jobID); finalizeErr == nil {
				return hashErr
			}
		}
		code, retryable := faultCode(hashErr), true
		if structured := new(fault.Error); errors.As(hashErr, &structured) {
			retryable = structured.Retryable
		}
		if code == fault.CodeContentChangedDuringHash || code == fault.CodeContentDisappeared {
			// 旧输入已失效，必须由 Scanner 重新发现，不能盲目重跑相同 stat 快照。
			retryable = false
		}
		_, _ = s.jobs.FailWithRetryable(context.Background(), jobID, string(code), retryable)
		return hashErr
	}
	result := Result{Blob: hashed.Blob.Algorithm + ":" + hashed.Blob.Digest, Algorithm: hashed.Blob.Algorithm,
		Digest: hashed.Blob.Digest, Size: hashed.Size, LocationKey: hashed.LocationKey, RelativePath: hashed.RelativePath}
	payload, err := json.Marshal(result)
	if err != nil {
		return s.fail(ctx, jobID, fault.New(fault.CodeInternal, true, err))
	}
	_, err = s.jobs.CompleteWithResult(ctx, jobID, payload)
	return err
}

func (s *Service) WaitResult(ctx context.Context, jobID string) (media.HashResult, error) {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		job, err := s.jobs.Get(ctx, jobID)
		if err != nil {
			return media.HashResult{}, err
		}
		switch job.Status {
		case jobs.StatusCompleted:
			var result Result
			if err := json.Unmarshal(job.ResultJSON, &result); err != nil {
				return media.HashResult{}, fault.New(fault.CodeInternal, true, err)
			}
			return media.HashResult{Blob: resultBlob(result), Size: result.Size, LocationKey: result.LocationKey, RelativePath: result.RelativePath}, nil
		case jobs.StatusFailed:
			if job.FailureRetryable && job.NextAttemptAt != nil {
				break
			}
			return media.HashResult{}, fault.New(fault.Code(job.IssueCode), job.FailureRetryable, nil)
		case jobs.StatusCancelled, jobs.StatusCancelling, jobs.StatusSuperseded:
			return media.HashResult{}, fault.New(fault.CodeProcessInterrupted, true, nil)
		}
		select {
		case <-ctx.Done():
			return media.HashResult{}, fault.New(fault.CodeProcessInterrupted, true, ctx.Err())
		case <-ticker.C:
		}
	}
}

func resultBlob(result Result) domain.ContentBlobRef {
	parsed, err := domain.ParseContentBlobRef(result.Algorithm, result.Digest)
	if err != nil {
		return domain.ContentBlobRef{}
	}
	return parsed
}

func (s *Service) fail(ctx context.Context, jobID string, err error) error {
	code, retryable := faultCode(err), true
	var structured *fault.Error
	if errors.As(err, &structured) {
		retryable = structured.Retryable
	}
	_, _ = s.jobs.FailWithRetryable(ctx, jobID, string(code), retryable)
	return err
}

func faultCode(err error) fault.Code {
	var structured *fault.Error
	if errors.As(err, &structured) {
		return structured.Code
	}
	return fault.CodeInternal
}
