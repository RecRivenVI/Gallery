package jobs_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/storage"
)

func TestPersistentJobTransitionsAndActiveScanConflict(t *testing.T) {
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	fixedClock := clock.Fixed{Time: time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)}
	generator := identity.NewGenerator(fixedClock)
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, fixedClock, generator)
	if err != nil {
		t.Fatal(err)
	}
	library, err := resources.CreateLibrary(context.Background(), "jobs")
	if err != nil {
		t.Fatal(err)
	}
	sourceID := createSource(t, resources, library.ID)
	jobStore, err := jobs.NewStore(store.Control.SQL(), fixedClock, generator)
	if err != nil {
		t.Fatal(err)
	}
	job, err := jobStore.CreateScan(context.Background(), sourceID, "personal-owner", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = jobStore.CreateScan(context.Background(), sourceID, "personal-owner", "")
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeScanAlreadyRunning {
		t.Fatalf("同 Source 并发扫描未冲突: %v", err)
	}
	job, err = jobStore.Start(context.Background(), job.ID)
	if err != nil || job.Status != jobs.StatusRunning {
		t.Fatalf("Start: %+v %v", job, err)
	}
	job, err = jobStore.Progress(context.Background(), job.ID, "hashing", 4, 10)
	if err != nil || job.ProgressSequence != 3 {
		t.Fatalf("Progress: %+v %v", job, err)
	}
	job, err = jobStore.BeginPublishing(context.Background(), job.ID)
	if err != nil || job.Status != jobs.StatusPublishing {
		t.Fatalf("Publishing: %+v %v", job, err)
	}
	publication, err := generator.New(domain.IDQueryPublication)
	if err != nil {
		t.Fatal(err)
	}
	job, err = jobStore.Complete(context.Background(), job.ID, publication.String())
	if err != nil || job.Status != jobs.StatusCompleted {
		t.Fatalf("Complete: %+v %v", job, err)
	}
	reloaded, err := jobStore.Get(context.Background(), job.ID)
	if err != nil || reloaded.PublicationID != publication.String() {
		t.Fatalf("Reload: %+v %v", reloaded, err)
	}
}

func TestScanJobPersistsRuleExecutionSnapshot(t *testing.T) {
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)}
	generator := identity.NewGenerator(now)
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, now, generator)
	if err != nil {
		t.Fatal(err)
	}
	library, err := resources.CreateLibrary(context.Background(), "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	sourceID := createSource(t, resources, library.ID)
	jobStore, err := jobs.NewStore(store.Control.SQL(), now, generator)
	if err != nil {
		t.Fatal(err)
	}
	job, err := jobStore.CreateScanWithRuleSnapshot(context.Background(), sourceID, "owner", "", &jobs.RuleExecutionSnapshot{
		SemanticHash: "semantic", Parameters: []byte(`{"minimumSize":1}`), ParametersHash: "parameters", RuleIRHash: "ir",
		CompilerVersion: "compiler", CELProfileVersion: "cel", ExtensionRegistryVersion: "extensions",
	})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := jobStore.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.RuleSemanticHash != "semantic" || string(reloaded.RuleParameters) != `{"minimumSize":1}` || reloaded.RuleIRHash != "ir" || reloaded.ExtensionRegistryVersion != "extensions" {
		t.Fatalf("规则执行快照未持久化: %+v", reloaded)
	}
}

func TestJobAttemptCancellationRetryAndMonotonicProgress(t *testing.T) {
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC)}
	ids := identity.NewGenerator(now)
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, now, ids)
	if err != nil {
		t.Fatal(err)
	}
	library, err := resources.CreateLibrary(context.Background(), "job-attempt")
	if err != nil {
		t.Fatal(err)
	}
	source, err := createSourceForAttemptTest(t, resources, library.ID)
	if err != nil {
		t.Fatal(err)
	}
	jobStore, err := jobs.NewStore(store.Control.SQL(), now, ids)
	if err != nil {
		t.Fatal(err)
	}
	job, err := jobStore.CreateScanWithOptions(context.Background(), source, "owner", "", nil, jobs.CreateOptions{MaxRetries: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobStore.Start(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := jobStore.ProgressDetailed(context.Background(), job.ID, jobs.ProgressUpdate{Stage: "hashing", Current: 8, Total: 10, Bytes: 80, Unit: "bytes"}); err != nil {
		t.Fatal(err)
	}
	if _, err := jobStore.ProgressDetailed(context.Background(), job.ID, jobs.ProgressUpdate{Stage: "hashing", Current: 7, Total: 10, Bytes: 70, Unit: "bytes"}); err == nil {
		t.Fatal("进度回退未被拒绝")
	} else {
		var structured *fault.Error
		if !errors.As(err, &structured) || structured.Code != fault.CodeJobProgressRegression {
			t.Fatalf("进度回退错误码错误: %v", err)
		}
	}
	cancelling, err := jobStore.RequestCancel(context.Background(), job.ID)
	if err != nil || cancelling.Status != jobs.StatusCancelling {
		t.Fatalf("运行中 Job 未进入 cancelling: %+v %v", cancelling, err)
	}
	cancelled, err := jobStore.FinalizeCancelled(context.Background(), job.ID)
	if err != nil || cancelled.Status != jobs.StatusCancelled {
		t.Fatalf("取消终态未收敛: %+v %v", cancelled, err)
	}
	attempts, err := jobStore.ListAttempts(context.Background(), job.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "cancelled" {
		t.Fatalf("取消 attempt 未落库: %+v %v", attempts, err)
	}
	retry, err := jobStore.Retry(context.Background(), job.ID, "owner")
	if err != nil || retry.RetryOf != job.ID || retry.Attempt != 2 {
		t.Fatalf("重试 attempt 错误: %+v %v", retry, err)
	}
}

func TestMaintenanceJobHasOneActiveInstancePerType(t *testing.T) {
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 2, 0, 0, 0, time.UTC)}
	jobStore, err := jobs.NewStore(store.Control.SQL(), now, identity.NewGenerator(now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobStore.CreateMaintenance(context.Background(), "catalog_gc", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := jobStore.CreateMaintenance(context.Background(), "catalog_gc", "owner"); err == nil {
		t.Fatal("同类维护 Job 未被单活跃约束拒绝")
	} else {
		var structured *fault.Error
		if !errors.As(err, &structured) || structured.Code != fault.CodeJobStateConflict {
			t.Fatalf("维护 Job 冲突错误码错误: %v", err)
		}
	}
}

func createSourceForAttemptTest(t *testing.T, resources *application.Resources, libraryID string) (string, error) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "source")
	if err := (filesystem.OS{}).MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	source, err := resources.CreateSource(context.Background(), libraryID, "source", root)
	return source.ID, err
}

func createSource(t *testing.T, resources *application.Resources, libraryID string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "source")
	if err := (filesystem.OS{}).MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	source, err := resources.CreateSource(context.Background(), libraryID, "source", root)
	if err != nil {
		t.Fatal(err)
	}
	return source.ID
}
