package derivedjob

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/derived"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/media"
	"github.com/RecRivenVI/gallery/internal/ports"
)

type Request struct {
	BlobAlgorithm    string `json:"blobAlgorithm"`
	BlobDigest       string `json:"blobDigest"`
	TransformID      string `json:"transformId"`
	TransformVersion string `json:"transformVersion"`
	Parameters       []byte `json:"parameters"`
	OverlayInputHash string `json:"overlayInputHash"`
}

// Resolver 把一次派生请求解析为具体的字节生成器。blob 是请求携带的 ContentBlob
// 引用——Resolver 实现负责在真正生成时（而不是解析时）通过服务端权威的
// catalog.Store.LocateBlobFile 定位可读源文件，不由调用方直接传入路径。
type Resolver interface {
	Resolve(ctx context.Context, transformID, transformVersion string, blob domain.ContentBlobRef) (derived.Generator, error)
}

type SpaceGate interface {
	CheckSpace(ctx context.Context, operation string, additionalBytes int64) error
}

type Service struct {
	jobs      *jobs.Store
	assets    *derived.Service
	resolver  Resolver
	space     SpaceGate
	catalogDB *sql.DB
	clock     ports.Clock
}

func New(jobStore *jobs.Store, assetService *derived.Service, resolver Resolver) (*Service, error) {
	if jobStore == nil || assetService == nil {
		return nil, fmt.Errorf("Derived Job Service 缺少依赖")
	}
	return &Service{jobs: jobStore, assets: assetService, resolver: resolver}, nil
}

// SetBlobLeaser 注入 catalog.db 连接与时钟，用于在创建与执行阶段为请求的 ContentBlob
// 建立 media.BlobReadLease：Job 排队等待调度与真正生成之间可能有任意长的窗口，若没有
// 显式租约，GC 可能在这段时间内回收该 digest 唯一剩余 occurrence 所在的 catalog_revision，
// 即便这个 Job 仍然需要它。未注入时（例如不依赖 Catalog GC 时序的单元测试）跳过租约，
// 只是不提供这层保护，不影响生成本身的正确性。
func (s *Service) SetBlobLeaser(catalogDB *sql.DB, clock ports.Clock) {
	s.catalogDB = catalogDB
	s.clock = clock
}

// acquireBlobLease 为 blob 建立一次一次性、按 TTL 自然过期的占位租约，不显式 Close：
// 它只需要覆盖"这一刻起一段时间内"该 digest 不被 GC 回收，不需要与调用方的生命周期
// 绑定释放（提前 Close 反而会在窗口未过时就失去保护）。
func (s *Service) acquireBlobLease(ctx context.Context, blob domain.ContentBlobRef) error {
	if s.catalogDB == nil {
		return nil
	}
	_, err := media.AcquireBlobReadLease(ctx, s.catalogDB, s.clock, blob, nil)
	return err
}

func (s *Service) Create(ctx context.Context, request Request, createdBy string) (jobs.Job, error) {
	if strings.TrimSpace(request.TransformID) == "" || strings.TrimSpace(request.TransformVersion) == "" || strings.TrimSpace(createdBy) == "" {
		return jobs.Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	blobRef, err := domain.ParseContentBlobRef(request.BlobAlgorithm, request.BlobDigest)
	if err != nil {
		return jobs.Job{}, fault.New(fault.CodeDerivedAssetInvalid, false, err)
	}
	if s.resolver == nil {
		return jobs.Job{}, fault.New(fault.CodeDerivedAssetUnavailable, false, nil)
	}
	if s.space != nil {
		if err := s.space.CheckSpace(ctx, "derived_asset", 0); err != nil {
			return jobs.Job{}, err
		}
	}
	// 在 Job 真正建立之前先为其输入 Blob 建立读取租约，覆盖创建到调度执行之间的等待
	// 窗口：见 acquireBlobLease 与 catalog.Store.LocateBlobFile 的说明。
	if err := s.acquireBlobLease(ctx, blobRef); err != nil {
		return jobs.Job{}, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return jobs.Job{}, fault.New(fault.CodeInternal, true, err)
	}
	return s.jobs.CreateWithOptions(ctx, "derived", "", createdBy, jobs.CreateOptions{ResourceClass: jobs.ResourceDerived, RequestJSON: payload})
}

func (s *Service) Available() bool { return s != nil && s.resolver != nil }

func (s *Service) SetSpaceGate(gate SpaceGate) { s.space = gate }

func (s *Service) Execute(ctx context.Context, jobID string) error {
	job, err := s.jobs.StartStage(ctx, jobID, "deriving")
	if err != nil {
		return err
	}
	var request Request
	if err := json.Unmarshal(job.RequestJSON, &request); err != nil {
		return s.fail(ctx, jobID, fault.New(fault.CodeDerivedAssetInvalid, false, err))
	}
	if s.resolver == nil {
		return s.fail(ctx, jobID, fault.New(fault.CodeDerivedAssetUnavailable, false, errors.New("Derived transform resolver 未配置")))
	}
	blobRef, err := domain.ParseContentBlobRef(request.BlobAlgorithm, request.BlobDigest)
	if err != nil {
		return s.fail(ctx, jobID, fault.New(fault.CodeDerivedAssetInvalid, false, err))
	}
	// 真正解析/生成之前刷新一次租约，覆盖 Job 排队等待调度与本次实际生成的窗口：见
	// acquireBlobLease 与 catalog.Store.LocateBlobFile 的说明。
	if err := s.acquireBlobLease(ctx, blobRef); err != nil {
		return s.fail(ctx, jobID, err)
	}
	generator, err := s.resolver.Resolve(ctx, request.TransformID, request.TransformVersion, blobRef)
	if err != nil {
		// 直接透传 Resolver 返回的结构化错误（例如 LocateBlobFile 在内容/Source 确实不再
		// 可解析时返回的 NOT_FOUND，或不支持的 transform 返回的 DERIVED_ASSET_INVALID），
		// 不得不分青红皂白统一改写成 retryable 的 DERIVED_ASSET_FAILED——那会让一个永久性
		// 失败被无谓地反复重试，也会让客户端无法区分"输入已经不存在"与"这次生成偶然失败"。
		// fail() 本身已经会在错误不是 *fault.Error 时安全回退到 DERIVED_ASSET_FAILED。
		return s.fail(ctx, jobID, err)
	}
	asset, err := s.assets.GetOrCreate(ctx, derived.Request{Blob: domain.ContentBlobRef{Algorithm: request.BlobAlgorithm, Digest: request.BlobDigest},
		TransformID: request.TransformID, TransformVersion: request.TransformVersion, Parameters: request.Parameters, OverlayInputHash: request.OverlayInputHash}, generator)
	if err != nil {
		return s.fail(ctx, jobID, err)
	}
	payload, err := json.Marshal(asset)
	if err != nil {
		return s.fail(ctx, jobID, fault.New(fault.CodeInternal, true, err))
	}
	_, err = s.jobs.CompleteWithResult(ctx, jobID, payload)
	return err
}

func (s *Service) fail(ctx context.Context, jobID string, err error) error {
	code, retryable := fault.CodeDerivedAssetFailed, true
	var structured *fault.Error
	if errors.As(err, &structured) {
		code, retryable = structured.Code, structured.Retryable
	}
	_, _ = s.jobs.FailWithRetryable(ctx, jobID, string(code), retryable)
	return err
}
