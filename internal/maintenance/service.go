package maintenance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/derived"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/ports"
)

type Request struct {
	RetentionSeconds int64 `json:"retentionSeconds"`
	DryRun           bool  `json:"dryRun"`
	RequiredBytes    int64 `json:"requiredBytes"`
}

type SpaceReport struct {
	Path          string
	RequiredBytes int64
	FreeBytes     int64
	Sufficient    bool
}

type GCReport struct {
	Catalog        catalog.GCResult
	DerivedRemoved int
	TempRemoved    int
	DryRun         bool
}

type Service struct {
	context context.Context
	control *sql.DB
	catalog *catalog.Store
	jobs    *jobs.Store
	derived *derived.Service
	dirs    appdirs.Dirs
	space   ports.SpaceChecker
	clock   ports.Clock
}

func New(ctx context.Context, control *sql.DB, catalogStore *catalog.Store, jobStore *jobs.Store, derivedService *derived.Service, dirs appdirs.Dirs, space ports.SpaceChecker, clock ports.Clock) (*Service, error) {
	if ctx == nil || control == nil || catalogStore == nil || jobStore == nil || clock == nil {
		return nil, fmt.Errorf("Maintenance Service 缺少依赖")
	}
	return &Service{context: ctx, control: control, catalog: catalogStore, jobs: jobStore, derived: derivedService, dirs: dirs, space: space, clock: clock}, nil
}

func (s *Service) CreateGC(ctx context.Context, createdBy string, request Request) (jobs.Job, error) {
	if request.RetentionSeconds < 0 || request.RequiredBytes < 0 {
		return jobs.Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return jobs.Job{}, fault.New(fault.CodeInternal, true, err)
	}
	job, err := s.jobs.CreateMaintenance(ctx, "catalog_gc", createdBy)
	if err != nil {
		return jobs.Job{}, err
	}
	if _, err := s.jobs.SetRequest(ctx, job.ID, payload); err != nil {
		return jobs.Job{}, err
	}
	return s.jobs.Get(ctx, job.ID)
}

func (s *Service) Execute(ctx context.Context, jobID string) error {
	job, err := s.jobs.StartStage(ctx, jobID, "maintenance")
	if err != nil {
		return err
	}
	switch job.Type {
	case "catalog_gc":
		var request Request
		if err := json.Unmarshal(job.RequestJSON, &request); err != nil {
			return s.fail(ctx, jobID, fault.New(fault.CodeValidation, false, err))
		}
		if request.RequiredBytes > 0 {
			if _, err := s.Preflight(ctx, request.RequiredBytes); err != nil {
				return s.fail(ctx, jobID, err)
			}
		}
		if _, err := s.RunGC(ctx, time.Duration(request.RetentionSeconds)*time.Second, request.DryRun); err != nil {
			return s.fail(ctx, jobID, err)
		}
	case "catalog_checkpoint":
		if err := s.Checkpoint(ctx); err != nil {
			return s.fail(ctx, jobID, err)
		}
	case "catalog_vacuum":
		if err := s.Vacuum(ctx); err != nil {
			return s.fail(ctx, jobID, err)
		}
	case "derived_gc":
		if _, err := s.RunGC(ctx, 0, false); err != nil {
			return s.fail(ctx, jobID, err)
		}
	default:
		return s.fail(ctx, jobID, fault.New(fault.CodeValidation, false, nil))
	}
	_, err = s.jobs.CompleteMaintenance(ctx, jobID)
	return err
}

func (s *Service) Reconcile(ctx context.Context, start func(string)) error {
	items, err := s.jobs.ListByStatuses(ctx, jobs.StatusQueued, jobs.StatusRunning, jobs.StatusPublishing)
	if err != nil {
		return err
	}
	for _, job := range items {
		if job.Type != "catalog_gc" && job.Type != "catalog_checkpoint" && job.Type != "catalog_vacuum" && job.Type != "derived_gc" {
			continue
		}
		if job.Status == jobs.StatusQueued && start != nil {
			start(job.ID)
		}
		if job.Status == jobs.StatusRunning || job.Status == jobs.StatusPublishing {
			_, _ = s.jobs.FailWithRetryable(ctx, job.ID, string(fault.CodeProcessInterrupted), true)
		}
	}
	return nil
}

func (s *Service) RunGC(ctx context.Context, retention time.Duration, dryRun bool) (GCReport, error) {
	active, err := s.jobs.ListByStatuses(ctx, jobs.StatusQueued, jobs.StatusRunning, jobs.StatusPublishing, jobs.StatusCancelling)
	if err != nil {
		return GCReport{}, err
	}
	activeIDs := make([]string, 0, len(active))
	for _, job := range active {
		activeIDs = append(activeIDs, job.ID)
	}
	result, err := s.catalog.GarbageCollectWithOptions(ctx, catalog.GCOptions{Retention: retention, ActiveJobIDs: activeIDs, DryRun: dryRun})
	if err != nil {
		return GCReport{}, err
	}
	report := GCReport{Catalog: result, DryRun: dryRun}
	if !dryRun && s.derived != nil {
		report.DerivedRemoved, err = s.derived.SweepObsolete(ctx, s.clock.Now().UTC().Add(-retention))
		if err != nil {
			return GCReport{}, err
		}
		report.TempRemoved, err = sweepTemp(s.dirs.Temp)
		if err != nil {
			return GCReport{}, err
		}
	}
	return report, nil
}

func (s *Service) Preflight(ctx context.Context, requiredBytes int64) (SpaceReport, error) {
	if requiredBytes < 0 {
		return SpaceReport{}, fault.New(fault.CodeValidation, false, nil)
	}
	path := s.dirs.Data
	if s.space == nil {
		return SpaceReport{Path: path, RequiredBytes: requiredBytes, Sufficient: true}, nil
	}
	free, err := s.space.FreeBytes(path)
	if err != nil {
		return SpaceReport{}, fault.New(fault.CodeInternal, true, err)
	}
	report := SpaceReport{Path: path, RequiredBytes: requiredBytes, FreeBytes: free, Sufficient: free >= requiredBytes}
	if !report.Sufficient {
		return report, fault.New(fault.CodeDiskSpaceInsufficient, true, nil)
	}
	return report, nil
}

func (s *Service) Checkpoint(ctx context.Context) error {
	if _, err := s.control.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return maintenanceFault(err)
	}
	return s.catalog.Checkpoint(ctx)
}

func (s *Service) Vacuum(ctx context.Context) error {
	if _, err := s.control.ExecContext(ctx, "VACUUM"); err != nil {
		return maintenanceFault(err)
	}
	return s.catalog.Vacuum(ctx)
}

func maintenanceFault(err error) error {
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "busy") || strings.Contains(lower, "locked") {
		return fault.New(fault.CodeMaintenanceBlocked, true, err)
	}
	return fault.New(fault.CodeInternal, true, err)
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

func sweepTemp(root string) (int, error) {
	if root == "" {
		return 0, nil
	}
	removed := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".tmp") || strings.HasPrefix(name, "gallery-") {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			removed++
		}
		return nil
	})
	return removed, err
}
