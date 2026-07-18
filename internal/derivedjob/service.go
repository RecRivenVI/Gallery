package derivedjob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/derived"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/jobs"
)

type Request struct {
	BlobAlgorithm    string `json:"blobAlgorithm"`
	BlobDigest       string `json:"blobDigest"`
	TransformID      string `json:"transformId"`
	TransformVersion string `json:"transformVersion"`
	Parameters       []byte `json:"parameters"`
	OverlayInputHash string `json:"overlayInputHash"`
}

type Resolver interface {
	Resolve(ctx context.Context, transformID, transformVersion string) (derived.Generator, error)
}

type Service struct {
	jobs     *jobs.Store
	assets   *derived.Service
	resolver Resolver
}

func New(jobStore *jobs.Store, assetService *derived.Service, resolver Resolver) (*Service, error) {
	if jobStore == nil || assetService == nil {
		return nil, fmt.Errorf("Derived Job Service 缺少依赖")
	}
	return &Service{jobs: jobStore, assets: assetService, resolver: resolver}, nil
}

func (s *Service) Create(ctx context.Context, request Request, createdBy string) (jobs.Job, error) {
	if strings.TrimSpace(request.TransformID) == "" || strings.TrimSpace(request.TransformVersion) == "" || strings.TrimSpace(createdBy) == "" {
		return jobs.Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	if _, err := domain.ParseContentBlobRef(request.BlobAlgorithm, request.BlobDigest); err != nil {
		return jobs.Job{}, fault.New(fault.CodeDerivedAssetInvalid, false, err)
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return jobs.Job{}, fault.New(fault.CodeInternal, true, err)
	}
	return s.jobs.CreateWithOptions(ctx, "derived", "", createdBy, jobs.CreateOptions{ResourceClass: jobs.ResourceDerived, RequestJSON: payload})
}

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
		return s.fail(ctx, jobID, fault.New(fault.CodeDerivedAssetFailed, false, errors.New("Derived transform resolver 未配置")))
	}
	generator, err := s.resolver.Resolve(ctx, request.TransformID, request.TransformVersion)
	if err != nil {
		return s.fail(ctx, jobID, fault.New(fault.CodeDerivedAssetFailed, true, err))
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
