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

// Dispatcher 把 Job 交给中央有界调度器执行。未注入时 Service 回退到自管理 goroutine（供不涉及
// 调度器的单元测试使用）。
type Dispatcher interface {
	Submit(jobID string)
}

type Service struct {
	context    context.Context
	resources  *application.Resources
	jobs       *jobs.Store
	catalog    *catalog.Store
	notifier   Notifier
	wait       sync.WaitGroup
	dispatcher Dispatcher
}

// SetDispatcher 注入中央调度器；注入后 Start 通过调度器领取执行并接受其 context 取消。
func (s *Service) SetDispatcher(d Dispatcher) { s.dispatcher = d }

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
	binding, err := s.resources.BindingForSource(ctx, sourceID)
	if err != nil {
		return jobs.Job{}, err
	}
	version, err := s.resources.GetRuleVersion(ctx, binding.SemanticHash)
	if err != nil {
		return jobs.Job{}, err
	}
	snapshot := &jobs.RuleExecutionSnapshot{
		SemanticHash: binding.SemanticHash, Parameters: append([]byte(nil), binding.Parameters...),
		ParametersHash: application.RuleParameterHash(binding.Parameters), RuleIRHash: binding.RuleIRHash,
		CompilerVersion: rules.CompilerVersion, CELProfileVersion: rules.CELProfileVersion,
		ExtensionRegistryVersion: version.IR.ExtensionRegistryVersion,
	}
	job, err := s.jobs.CreateScanWithRuleSnapshot(ctx, sourceID, createdBy, "", snapshot)
	if err == nil {
		s.notifier.JobChanged(job)
	}
	return job, err
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
	var binding application.SourceRuleBinding
	if job.RuleSemanticHash != "" {
		version, snapshotErr := s.resources.GetRuleVersion(ctx, job.RuleSemanticHash)
		if snapshotErr != nil {
			return s.fail(ctx, job.ID, snapshotErr)
		}
		compiled, compileErr := rules.CompilePackage(version.Canonical)
		if compileErr != nil {
			return s.fail(ctx, job.ID, compileErr)
		}
		ir, irHash, parameters, compileErr := rules.CompileBinding(compiled, job.RuleParameters)
		if compileErr != nil || irHash != job.RuleIRHash {
			if compileErr == nil {
				compileErr = fmt.Errorf("Job 规则快照 RuleIRHash 不匹配")
			}
			return s.fail(ctx, job.ID, compileErr)
		}
		binding = application.SourceRuleBinding{SourceID: source.ID, SemanticHash: job.RuleSemanticHash, Parameters: parameters, RuleIRHash: irHash, IR: ir}
	} else {
		binding, err = s.resources.BindingForSource(ctx, source.ID)
		if err != nil {
			return s.fail(ctx, job.ID, err)
		}
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
		mediaItems := make([]application.DiscoveredMedia, len(work.Media))
		for index := range work.Media {
			mediaItems[index] = application.DiscoveredMedia{SourceKey: work.Media[index].SourceKey,
				RuleKey: work.Media[index].RuleKey, Algorithm: work.Media[index].Hash.Blob.Algorithm,
				Digest: work.Media[index].Hash.Blob.Digest, Ordinal: index}
		}
		canonicalInput = append(canonicalInput, application.DiscoveredWork{SourceKey: work.SourceKey,
			ProviderID: work.ProviderID, ExternalID: work.ExternalID, Title: work.Title,
			Creator: creatorReference(work), Media: mediaItems})
	}
	canonical, err := s.resources.EnsureCanonical(ctx, source.ID, canonicalInput)
	if err != nil {
		return s.fail(ctx, job.ID, err)
	}
	overlays, creatorMerges, controlWatermark, err := s.resources.QueryOverlaySnapshot(ctx, nil)
	if err != nil {
		return s.fail(ctx, job.ID, err)
	}
	candidate, err := s.catalog.BeginCandidate(ctx, job.ID, source.ID, controlWatermark)
	if err != nil {
		return s.fail(ctx, job.ID, err)
	}
	works := make([]catalog.WorkFact, 0, len(discovered))
	mediaFacts := make([]catalog.MediaFact, 0, total)
	for _, work := range discovered {
		canonicalWork := canonical[work.SourceKey]
		filenames := make([]string, 0, len(work.Media))
		for _, item := range work.Media {
			filenames = append(filenames, path.Base(item.RelativePath))
		}
		workFact := catalog.WorkFact{SourceID: source.ID, LibraryID: source.LibraryID,
			SourceKey: work.SourceKey, ProviderID: work.ProviderID, ExternalID: work.ExternalID,
			SourceTitle: canonicalWork.Title, SourceTags: work.Tags,
			Title: canonicalWork.Title, Creator: work.Creator, Tags: work.Tags,
			Filenames: filenames, WorkID: canonicalWork.ID}
		if len(canonicalWork.Creators) > 0 {
			creator := creatorReference(work)
			workFact.Creator = canonicalWork.Creators[0].Name
			workFact.CreatorID = canonicalWork.Creators[0].ID
			workFact.CreatorSourceKey = creator.SourceKey
			workFact.CreatorProviderID = creator.ProviderID
			workFact.CreatorExternalID = creator.ExternalID
			workFact.SourceCreatorName = work.Creator
		}
		works = append(works, workFact)
		for _, item := range work.Media {
			canonicalMedia := canonicalWork.Media[item.SourceKey]
			mediaFacts = append(mediaFacts, catalog.MediaFact{
				SourceID: source.ID, SourceKey: item.SourceKey, WorkSourceKey: work.SourceKey, RuleKey: item.RuleKey,
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
	overlayFacts := make(map[string]catalog.OverlayFact, len(overlays))
	for workID, value := range overlays {
		overlayFacts[workID] = catalog.OverlayFact{TitleOverride: value.TitleOverride, ManualTags: value.ManualTags,
			Hidden: value.Hidden, CustomCoverMediaID: value.CustomCoverMediaID}
	}
	if err := s.catalog.ApplyCatalogCandidateOverlays(ctx, candidate, overlayFacts); err != nil {
		_ = s.catalog.AbortCandidate(ctx, job.ID)
		return s.fail(ctx, job.ID, err)
	}
	if err := s.catalog.ApplyCreatorMerges(ctx, candidate.CatalogRevisionID, candidate.OverlayRevisionID, creatorMerges); err != nil {
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
	if err := s.resources.MarkOverlaySnapshotPublished(ctx, publication.ControlWatermark, publication.ID); err != nil {
		return err
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
		if job.Type != "scan" {
			continue
		}
		if job.Status == jobs.StatusQueued {
			s.Start(job.ID)
			continue
		}
		publication, publicationErr := s.catalog.PublicationForJob(ctx, job.ID)
		if publicationErr == nil && (job.Status == jobs.StatusRunning || job.Status == jobs.StatusPublishing) {
			if markErr := s.resources.MarkOverlaySnapshotPublished(ctx, publication.ControlWatermark, publication.ID); markErr != nil {
				return markErr
			}
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
		if job.Type != "scan" {
			continue
		}
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
	SourceKey, ProviderID, ExternalID, Title, Creator string
	Tags                                              []string
	Media                                             []discoveredMedia
}
type discoveredMedia struct {
	SourceKey, RuleKey, RelativePath, Kind, MIME string
	Hash                                         media.HashResult
}

func creatorReference(work discoveredWork) application.DiscoveredCreator {
	if work.Creator == "" {
		return application.DiscoveredCreator{}
	}
	workReference := work.SourceKey
	if work.ExternalID != "" {
		workReference = "origin:" + work.ProviderID + ":" + work.ExternalID
	}
	return application.DiscoveredCreator{
		SourceKey:  workReference + "/creator:primary:0",
		ProviderID: work.ProviderID,
		Name:       work.Creator,
	}
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
		work := discoveredWork{SourceKey: evaluated.Work.StableKey, ProviderID: evaluated.Work.ProviderID, ExternalID: evaluated.Work.ExternalID,
			Title: evaluated.Work.Title, Creator: evaluated.Work.Creator, Tags: evaluated.Work.Tags}
		for _, item := range evaluated.Work.Media {
			mediaRelative := path.Join(relative, item.Path)
			if _, err := media.ValidateRelativePath(mediaRelative); err != nil {
				return err
			}
			work.Media = append(work.Media, discoveredMedia{SourceKey: path.Join(work.SourceKey, item.StableKey),
				RuleKey: item.StableKey, RelativePath: mediaRelative, Kind: item.Kind, MIME: item.MIME})
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
