package scanner_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/scanner"
	"github.com/RecRivenVI/gallery/internal/storage"
)

func TestWalkingSkeletonScanPublishesAndFailurePreservesOldPublication(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "..", "tests", "fixtures", "walking-skeleton", "work-one", "media.bin"))
	if err != nil {
		t.Fatal(err)
	}
	resources, jobStore, catalogStore, service, source, store := setup(t, fixture)
	defer store.Close()
	before := sha256.Sum256(mustRead(t, filepath.Join(source.RootPath, "work-one", "media.bin")))

	job, err := service.CreateScan(context.Background(), source.ID, "personal-owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	completed, err := jobStore.Get(context.Background(), job.ID)
	if err != nil || completed.Status != jobs.StatusCompleted || completed.PublicationID == "" {
		t.Fatalf("扫描 Job 未完成: %+v %v", completed, err)
	}
	publication, works, err := catalogStore.ListWorks(context.Background())
	if err != nil || len(works) != 1 || works[0].Title != "work-one" || works[0].MediaCount != 1 {
		t.Fatalf("WorkProjection 错误: %+v %+v %v", publication, works, err)
	}
	_, mediaItems, err := catalogStore.ListMediaForWork(context.Background(), works[0].ID)
	if err != nil || len(mediaItems) != 1 {
		t.Fatalf("MediaProjection 错误: %+v %v", mediaItems, err)
	}
	expected := sha256.Sum256(fixture)
	if mediaItems[0].Digest != domain.NewSHA256BlobRef(expected).Digest || mediaItems[0].RelativePath != "work-one/media.bin" {
		t.Fatalf("Blob/FileLocation 错误: %+v", mediaItems[0])
	}
	after := sha256.Sum256(mustRead(t, filepath.Join(source.RootPath, "work-one", "media.bin")))
	if before != after {
		t.Fatal("扫描修改了只读 Source")
	}
	if _, err := resources.GetSource(context.Background(), source.ID); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filepath.Join(source.RootPath, "work-one", "media.bin")); err != nil {
		t.Fatal(err)
	}
	failedJob, err := service.CreateScan(context.Background(), source.ID, "personal-owner")
	if err != nil {
		t.Fatal(err)
	}
	err = service.Execute(context.Background(), failedJob.ID)
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeRuleEval {
		t.Fatalf("空候选错误 = %v", err)
	}
	failed, err := jobStore.Get(context.Background(), failedJob.ID)
	if err != nil || failed.Status != jobs.StatusFailed || failed.IssueCode != string(fault.CodeRuleEval) {
		t.Fatalf("失败 Job 错误: %+v %v", failed, err)
	}
	stillCurrent, err := catalogStore.Current(context.Background())
	if err != nil || stillCurrent.ID != publication.ID {
		t.Fatalf("失败扫描替换了旧 publication: %+v %v", stillCurrent, err)
	}
}

func TestReconciliationRepairsBothCrossDatabaseStates(t *testing.T) {
	fixture := []byte("reconciliation fixture")
	_, jobStore, catalogStore, service, source, store := setup(t, fixture)
	defer store.Close()

	publishedJob, err := jobStore.CreateScan(context.Background(), source.ID, "personal-owner", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobStore.Start(context.Background(), publishedJob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := jobStore.BeginPublishing(context.Background(), publishedJob.ID); err != nil {
		t.Fatal(err)
	}
	candidate, err := catalogStore.BeginCandidate(context.Background(), publishedJob.ID, source.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	workID := newID(t, domain.IDCanonicalWork)
	mediaID := newID(t, domain.IDCanonicalMedia)
	if err := catalogStore.Stage(context.Background(), candidate,
		[]catalog.WorkFact{{SourceID: source.ID, SourceKey: "work-one", Title: "work-one", WorkID: workID}},
		[]catalog.MediaFact{{SourceID: source.ID, SourceKey: "work-one/media.bin", WorkSourceKey: "work-one", RelativePath: "work-one/media.bin", Kind: "image", MIME: "application/octet-stream", Size: int64(len(fixture)), Algorithm: "sha256-v1", Digest: domain.NewSHA256BlobRef(sha256.Sum256(fixture)).Digest, LocationKey: "location", MediaID: mediaID, WorkID: workID}},
	); err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ValidateCandidate(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	publication, err := catalogStore.Publish(context.Background(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered, err := jobStore.Get(context.Background(), publishedJob.ID)
	if err != nil || recovered.Status != jobs.StatusCompleted || recovered.PublicationID != publication.ID {
		t.Fatalf("已发布 Job 未恢复 completed: %+v %v", recovered, err)
	}

	missingJob, err := jobStore.CreateScan(context.Background(), source.ID, "personal-owner", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobStore.Start(context.Background(), missingJob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := jobStore.BeginPublishing(context.Background(), missingJob.ID); err != nil {
		t.Fatal(err)
	}
	missingPublication := newID(t, domain.IDQueryPublication)
	if _, err := jobStore.Complete(context.Background(), missingJob.ID, missingPublication); err != nil {
		t.Fatal(err)
	}
	if err := service.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	repair, err := jobStore.Get(context.Background(), missingJob.ID)
	if err != nil || repair.Status != jobs.StatusNeedsRepair || repair.IssueCode != string(fault.CodeCatalogPublicationAbsent) {
		t.Fatalf("缺 publication 的 completed Job 未标 needs_repair: %+v %v", repair, err)
	}
}

func setup(t *testing.T, fixture []byte) (*application.Resources, *jobs.Store, *catalog.Store, *scanner.Service, application.Source, *storage.Store) {
	t.Helper()
	root := t.TempDir()
	dirs := appdirs.UnderRoot(filepath.Join(root, "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	fixedClock := clock.Fixed{Time: time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)}
	generator := identity.NewGenerator(fixedClock)
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, fixedClock, generator)
	if err != nil {
		t.Fatal(err)
	}
	library, err := resources.CreateLibrary(context.Background(), "Walking Skeleton")
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot := filepath.Join(root, "source")
	if err := os.MkdirAll(filepath.Join(sourceRoot, "work-one"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "work-one", "media.bin"), fixture, 0o400); err != nil {
		t.Fatal(err)
	}
	source, err := resources.CreateSource(context.Background(), library.ID, "Synthetic", sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	rulePackage, err := os.ReadFile(filepath.Join("..", "rules", "testdata", "minimal-rule-package.json"))
	if err != nil {
		t.Fatal(err)
	}
	version, err := resources.CreateRuleVersion(context.Background(), rulePackage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resources.CreateSourceRuleBinding(context.Background(), source.ID, version.SemanticHash, []byte("{}"), 0); err != nil {
		t.Fatal(err)
	}
	jobStore, err := jobs.NewStore(store.Control.SQL(), fixedClock, generator)
	if err != nil {
		t.Fatal(err)
	}
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), fixedClock, generator)
	if err != nil {
		t.Fatal(err)
	}
	service, err := scanner.New(context.Background(), resources, jobStore, catalogStore, nil)
	if err != nil {
		t.Fatal(err)
	}
	return resources, jobStore, catalogStore, service, source, store
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func newID(t *testing.T, kind domain.IDKind) string {
	t.Helper()
	id, err := identity.NewGenerator(clock.Fixed{Time: time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)}).New(kind)
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}
