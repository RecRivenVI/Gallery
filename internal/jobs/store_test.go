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
