package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"sync"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/media"
	"github.com/RecRivenVI/gallery/internal/rules"
)

const IssueProcessInterrupted = "PROCESS_INTERRUPTED"

type Notifier interface {
	JobChanged(jobs.Job)
	PublicationPublished(catalog.Publication)
}

type nopNotifier struct{}

func (nopNotifier) JobChanged(jobs.Job)                      {}
func (nopNotifier) PublicationPublished(catalog.Publication) {}

type Service struct {
	context   context.Context
	resources *application.Resources
	jobs      *jobs.Store
	catalog   *catalog.Store
	notifier  Notifier
	wait      sync.WaitGroup
}

func New(ctx context.Context, resources *application.Resources, jobStore *jobs.Store, catalogStore *catalog.Store, notifier Notifier) (*Service, error) {
	if ctx == nil || resources == nil || jobStore == nil || catalogStore == nil {
		return nil, fmt.Errorf("Scanner 缺少依赖")
	}
	if notifier == nil {
		notifier = nopNotifier{}
	}
	return &Service{context: ctx, resources: resources, jobs: jobStore, catalog: catalogStore, notifier: notifier}, nil
}

func (s *Service) CreateScan(ctx context.Context, sourceID, createdBy string) (jobs.Job, error) {
	if _, err := s.resources.GetSource(ctx, sourceID); err != nil {
		return jobs.Job{}, err
	}
	if _, err := s.resources.BindingForSource(ctx, sourceID); err != nil {
		return jobs.Job{}, err
	}
	job, err := s.jobs.CreateScan(ctx, sourceID, createdBy, "")
	if err == nil {
		s.notifier.JobChanged(job)
	}
	return job, err
}

func (s *Service) Start(jobID string) {
	s.wait.Add(1)
	go func() { defer s.wait.Done(); _ = s.Execute(s.context, jobID) }()
}

func (s *Service) Wait() { s.wait.Wait() }

func (s *Service) Execute(ctx context.Context, jobID string) error {
	job, err := s.jobs.Start(ctx, jobID)
	if err != nil {
		return err
	}
	s.notifier.JobChanged(job)
	source, err := s.resources.GetSource(ctx, job.SourceID)
	if err != nil {
		return s.fail(ctx, job.ID, err)
	}
	binding, err := s.resources.BindingForSource(ctx, source.ID)
	if err != nil {
		return s.fail(ctx, job.ID, err)
	}
	discovered, err := discover(ctx, source.RootPath, binding.IR, binding.Parameters)
	if err != nil {
		return s.fail(ctx, job.ID, err)
	}
	if len(discovered) == 0 {
		return s.fail(ctx, job.ID, fault.New(fault.CodeRuleEval, false, nil))
	}
	total := int64(0)
	for _, work := range discovered {
		total += int64(len(work.Media))
	}
	if total == 0 {
		return s.fail(ctx, job.ID, fault.New(fault.CodeRuleEval, false, nil))
	}
	current := int64(0)
	for workIndex := range discovered {
		for mediaIndex := range discovered[workIndex].Media {
			select {
			case <-ctx.Done():
				return s.fail(ctx, job.ID, fault.New(fault.CodeProcessInterrupted, true, ctx.Err()))
			default:
			}
			item := &discovered[workIndex].Media[mediaIndex]
			hashed, hashErr := media.HashSourceFile(source.RootPath, item.RelativePath, nil)
			if hashErr != nil {
				return s.fail(ctx, job.ID, hashErr)
			}
			item.Hash = hashed
			current++
			job, err = s.jobs.Progress(ctx, job.ID, "hashing", current, total)
			if err != nil {
				return err
			}
			s.notifier.JobChanged(job)
		}
	}
	canonicalInput := make([]application.DiscoveredWork, 0, len(discovered))
	for _, work := range discovered {
		mediaKeys := make([]string, len(work.Media))
		for index := range work.Media {
			mediaKeys[index] = work.Media[index].SourceKey
		}
		canonicalInput = append(canonicalInput, application.DiscoveredWork{SourceKey: work.SourceKey, Title: work.Title, MediaKeys: mediaKeys})
	}
	canonical, err := s.resources.EnsureCanonical(ctx, source.ID, canonicalInput)
	if err != nil {
		return s.fail(ctx, job.ID, err)
	}
	candidate, err := s.catalog.BeginCandidate(ctx, job.ID, source.ID, s.resources.ControlWatermark())
	if err != nil {
		return s.fail(ctx, job.ID, err)
	}
	works := make([]catalog.WorkFact, 0, len(discovered))
	mediaFacts := make([]catalog.MediaFact, 0, total)
	for _, work := range discovered {
		canonicalWork := canonical[work.SourceKey]
		works = append(works, catalog.WorkFact{SourceID: source.ID, SourceKey: work.SourceKey, Title: canonicalWork.Title, WorkID: canonicalWork.ID})
		for _, item := range work.Media {
			canonicalMedia := canonicalWork.Media[item.SourceKey]
			mediaFacts = append(mediaFacts, catalog.MediaFact{
				SourceID: source.ID, SourceKey: item.SourceKey, WorkSourceKey: work.SourceKey,
				RelativePath: item.Hash.RelativePath, Kind: item.Kind, MIME: item.MIME, Size: item.Hash.Size,
				Algorithm: item.Hash.Blob.Algorithm, Digest: item.Hash.Blob.Digest, LocationKey: item.Hash.LocationKey,
				MediaID: canonicalMedia.ID, WorkID: canonicalWork.ID, Ordinal: canonicalMedia.Ordinal,
			})
		}
	}
	if err := s.catalog.Stage(ctx, candidate, works, mediaFacts); err != nil {
		_ = s.catalog.AbortCandidate(ctx, job.ID)
		return s.fail(ctx, job.ID, err)
	}
	if err := s.catalog.ValidateCandidate(ctx, candidate); err != nil {
		_ = s.catalog.AbortCandidate(ctx, job.ID)
		return s.fail(ctx, job.ID, err)
	}
	job, err = s.jobs.BeginPublishing(ctx, job.ID)
	if err != nil {
		_ = s.catalog.AbortCandidate(ctx, job.ID)
		return err
	}
	s.notifier.JobChanged(job)
	publication, err := s.catalog.Publish(ctx, candidate)
	if err != nil {
		_ = s.catalog.AbortCandidate(ctx, job.ID)
		return s.fail(ctx, job.ID, err)
	}
	s.notifier.PublicationPublished(publication)
	job, err = s.jobs.Complete(ctx, job.ID, publication.ID)
	if err != nil {
		return err
	}
	s.notifier.JobChanged(job)
	return nil
}

func (s *Service) Reconcile(ctx context.Context) error {
	nonterminal, err := s.jobs.ListByStatuses(ctx, jobs.StatusQueued, jobs.StatusRunning, jobs.StatusPublishing)
	if err != nil {
		return err
	}
	for _, job := range nonterminal {
		publication, publicationErr := s.catalog.PublicationForJob(ctx, job.ID)
		if publicationErr == nil && (job.Status == jobs.StatusRunning || job.Status == jobs.StatusPublishing) {
			recovered, recoverErr := s.jobs.RecoverCompleted(ctx, job.ID, publication.ID)
			if recoverErr != nil {
				return recoverErr
			}
			s.notifier.JobChanged(recovered)
			continue
		}
		if publicationErr != nil && !isNotFound(publicationErr) {
			return publicationErr
		}
		_ = s.catalog.AbortCandidate(ctx, job.ID)
		failed, failErr := s.jobs.Fail(ctx, job.ID, IssueProcessInterrupted)
		if failErr != nil {
			return failErr
		}
		s.notifier.JobChanged(failed)
	}
	completed, err := s.jobs.ListByStatuses(ctx, jobs.StatusCompleted)
	if err != nil {
		return err
	}
	for _, job := range completed {
		if _, publicationErr := s.catalog.PublicationForJob(ctx, job.ID); isNotFound(publicationErr) {
			repaired, repairErr := s.jobs.MarkNeedsRepair(ctx, job.ID, string(fault.CodeCatalogPublicationAbsent))
			if repairErr != nil {
				return repairErr
			}
			s.notifier.JobChanged(repaired)
		} else if publicationErr != nil {
			return publicationErr
		}
	}
	return nil
}

func (s *Service) fail(ctx context.Context, jobID string, cause error) error {
	code := faultCode(cause)
	failed, err := s.jobs.Fail(ctx, jobID, string(code))
	if err == nil {
		s.notifier.JobChanged(failed)
	}
	return cause
}

func faultCode(err error) fault.Code {
	var structured *fault.Error
	if errors.As(err, &structured) {
		return structured.Code
	}
	return fault.CodeInternal
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	var structured *fault.Error
	return errors.As(err, &structured) && structured.Code == fault.CodeNotFound
}

type discoveredWork struct {
	SourceKey, Title string
	Media            []discoveredMedia
}
type discoveredMedia struct {
	SourceKey, RelativePath, Kind, MIME string
	Hash                                media.HashResult
}

func discover(ctx context.Context, root string, ir rules.RuleIR, parameters []byte) ([]discoveredWork, error) {
	lifecycle, err := rules.NewLifecycle()
	if err != nil {
		return nil, fault.New(fault.CodeRuleEval, false, err)
	}
	var result []discoveredWork
	err = filepath.WalkDir(root, func(onDisk string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() || onDisk == root {
			return nil
		}
		relativeOS, err := filepath.Rel(root, onDisk)
		if err != nil {
			return err
		}
		relative := filepath.ToSlash(relativeOS)
		matched, err := path.Match(ir.WorkDirectoryGlob, relative)
		if err != nil {
			return err
		}
		if !matched {
			return nil
		}
		children, err := os.ReadDir(onDisk)
		if err != nil {
			return err
		}
		sample := rules.DryRunInput{Path: relative, Metadata: map[string]any{}, Files: []rules.DryRunFile{}}
		if ir.MetadataFile != "" {
			metadataPath := filepath.Join(onDisk, filepath.FromSlash(ir.MetadataFile))
			info, statErr := os.Stat(metadataPath)
			if statErr != nil || info.Size() > int64(rules.CELProfileV1.InputJSONBytes) {
				return fmt.Errorf("metadata 不可用或超限")
			}
			content, readErr := os.ReadFile(metadataPath)
			if readErr != nil {
				return readErr
			}
			if err := json.Unmarshal(content, &sample.Metadata); err != nil {
				return fmt.Errorf("metadata 损坏: %w", err)
			}
		}
		for _, child := range children {
			if child.IsDir() || child.Type()&fs.ModeSymlink != 0 || child.Name() == ir.MetadataFile {
				continue
			}
			info, err := child.Info()
			if err != nil {
				return err
			}
			sample.Files = append(sample.Files, rules.DryRunFile{Path: child.Name(), Size: info.Size()})
		}
		evaluated, err := lifecycle.EvaluateIR(ctx, ir, parameters, sample)
		if err != nil {
			return err
		}
		if evaluated.Work.Ignored {
			return filepath.SkipDir
		}
		work := discoveredWork{SourceKey: evaluated.Work.StableKey, Title: evaluated.Work.Title}
		for _, item := range evaluated.Work.Media {
			mediaRelative := path.Join(relative, item.Path)
			if _, err := media.ValidateRelativePath(mediaRelative); err != nil {
				return err
			}
			work.Media = append(work.Media, discoveredMedia{SourceKey: path.Join(work.SourceKey, item.StableKey), RelativePath: mediaRelative, Kind: item.Kind, MIME: item.MIME})
		}
		if len(work.Media) > 0 {
			result = append(result, work)
		}
		return filepath.SkipDir
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fault.New(fault.CodeSourceUnavailable, true, err)
		}
		return nil, fault.New(fault.CodeRuleEval, false, err)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].SourceKey < result[j].SourceKey })
	return result, nil
}

func pathOnDisk(root, relative string) string {
	return root + string(os.PathSeparator) + path.Clean(relative)
}
