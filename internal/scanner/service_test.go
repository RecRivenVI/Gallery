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
	"github.com/RecRivenVI/gallery/internal/overlay"
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

func TestCatalogDeleteRebuildPreservesCanonicalOverlayAndMediaURL(t *testing.T) {
	fixture := []byte("catalog rebuild stable media")
	_, jobStore, catalogStore, service, source, store := setup(t, fixture)
	ctx := context.Background()
	firstJob, err := service.CreateScan(ctx, source.ID, "personal-owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, firstJob.ID); err != nil {
		t.Fatal(err)
	}
	_, works, err := catalogStore.ListWorks(ctx)
	if err != nil || len(works) != 1 {
		t.Fatalf("首次扫描失败: %+v %v", works, err)
	}
	workID := works[0].ID
	_, mediaItems, err := catalogStore.ListMediaForWork(ctx, workID)
	if err != nil || len(mediaItems) != 1 {
		t.Fatalf("首次媒体投影失败: %+v %v", mediaItems, err)
	}
	mediaID := mediaItems[0].ID
	var oldCreatorID string
	if err := store.Control.SQL().QueryRowContext(ctx, `SELECT creator_id FROM creator_bindings
WHERE source_id=? AND status='active'`, source.ID).Scan(&oldCreatorID); err != nil {
		t.Fatalf("首次扫描未建立 CreatorBinding: %v", err)
	}
	stableMediaURL := "/api/v1/media/" + mediaID + "/content"
	fixedClock := clock.Fixed{Time: time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)}
	overlayService, err := overlay.New(ctx, store.Control.SQL(), jobStore, catalogStore, fixedClock, nil)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := overlayService.Put(ctx, workID, "personal-owner", overlay.Input{TitleOverride: "重建后标题", Favorite: true, Progress: 0.6})
	if err != nil {
		t.Fatal(err)
	}
	if err := overlayService.Execute(ctx, changed.ProjectionJobID); err != nil {
		t.Fatal(err)
	}
	oldPublication, err := catalogStore.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	controlPath := filepath.Join(filepath.Dir(source.RootPath), "app", "data", "control.db")
	catalogPath := filepath.Join(filepath.Dir(source.RootPath), "app", "data", "catalog.db")
	beforeSource := sha256.Sum256(mustRead(t, filepath.Join(source.RootPath, "work-one", "media.bin")))
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(controlPath); err != nil {
		t.Fatalf("control.db 意外缺失: %v", err)
	}
	for _, path := range []string{catalogPath, catalogPath + "-wal", catalogPath + "-shm"} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}

	dirs := appdirs.UnderRoot(filepath.Join(filepath.Dir(source.RootPath), "app"))
	reopened, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	generator := identity.NewGenerator(fixedClock)
	rebuiltResources, _ := application.NewResources(reopened.Control.SQL(), dirs, filesystem.OS{}, fixedClock, generator)
	rebuiltJobs, _ := jobs.NewStore(reopened.Control.SQL(), fixedClock, generator)
	rebuiltCatalog, _ := catalog.NewStore(reopened.Catalog.SQL(), fixedClock, generator)
	rebuiltScanner, _ := scanner.New(ctx, rebuiltResources, rebuiltJobs, rebuiltCatalog, nil)
	rebuildJob, err := rebuiltScanner.CreateScan(ctx, source.ID, "personal-owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := rebuiltScanner.Execute(ctx, rebuildJob.ID); err != nil {
		t.Fatal(err)
	}
	newPublication, rebuiltWorks, err := rebuiltCatalog.ListWorks(ctx)
	if err != nil || len(rebuiltWorks) != 1 || rebuiltWorks[0].ID != workID || rebuiltWorks[0].Title != "重建后标题" {
		t.Fatalf("Catalog 重建后 Work/Overlay 漂移: pub=%+v works=%+v err=%v", newPublication, rebuiltWorks, err)
	}
	_, rebuiltMedia, err := rebuiltCatalog.ListMediaForWork(ctx, workID)
	if err != nil || len(rebuiltMedia) != 1 || rebuiltMedia[0].ID != mediaID || "/api/v1/media/"+rebuiltMedia[0].ID+"/content" != stableMediaURL {
		t.Fatalf("Catalog 重建后媒体 URL 身份漂移: %+v %v", rebuiltMedia, err)
	}
	if newPublication.ID == oldPublication.ID || newPublication.CatalogRevisionID == oldPublication.CatalogRevisionID {
		t.Fatal("Catalog 重建复用了已删除的 revision/publication 身份")
	}
	rebuiltOverlay, _ := overlay.New(ctx, reopened.Control.SQL(), rebuiltJobs, rebuiltCatalog, fixedClock, nil)
	state, err := rebuiltOverlay.Get(ctx, workID)
	if err != nil || !state.Favorite || state.Progress != 0.6 || state.ProjectionStatus != "published" || state.PublishedQueryPublicationID != newPublication.ID {
		t.Fatalf("Catalog 重建后用户事实/投影状态漂移: %+v %v", state, err)
	}
	var workCount, creatorCount, mediaCount int
	_ = reopened.Control.SQL().QueryRowContext(ctx, "SELECT count(*) FROM canonical_works").Scan(&workCount)
	_ = reopened.Control.SQL().QueryRowContext(ctx, "SELECT count(*) FROM canonical_creators").Scan(&creatorCount)
	_ = reopened.Control.SQL().QueryRowContext(ctx, "SELECT count(*) FROM canonical_media").Scan(&mediaCount)
	if workCount != 1 || creatorCount != 1 || mediaCount != 1 {
		t.Fatalf("重建重复创建 Canonical 实体: works=%d creators=%d media=%d", workCount, creatorCount, mediaCount)
	}
	var creatorID string
	if err := reopened.Control.SQL().QueryRowContext(ctx, `SELECT creator_id FROM creator_bindings
WHERE source_id=? AND status='active'`, source.ID).Scan(&creatorID); err != nil {
		t.Fatalf("CreatorBinding 未恢复: %v", err)
	}
	if creatorID != oldCreatorID {
		t.Fatalf("Catalog 重建后 CanonicalCreator 身份漂移: old=%s new=%s", oldCreatorID, creatorID)
	}
	var projectedCreatorID string
	if err := reopened.Catalog.SQL().QueryRowContext(ctx, `SELECT creator_id FROM work_creator_relations
WHERE catalog_revision_id=? AND overlay_revision_id=? AND work_id=?`, newPublication.CatalogRevisionID,
		newPublication.OverlayRevisionID, workID).Scan(&projectedCreatorID); err != nil || projectedCreatorID != creatorID {
		t.Fatalf("CreatorProjection/关系未以稳定 ID 恢复: control=%s catalog=%s err=%v", creatorID, projectedCreatorID, err)
	}
	afterSource := sha256.Sum256(mustRead(t, filepath.Join(source.RootPath, "work-one", "media.bin")))
	if beforeSource != afterSource {
		t.Fatal("Catalog 重建修改了 Source")
	}
}

func TestBlobLocationOccurrencesAndContentReplacement(t *testing.T) {
	fixture := []byte("shared blob bytes")
	_, _, catalogStore, service, source, store := setup(t, fixture)
	defer store.Close()
	ctx := context.Background()
	duplicatePath := filepath.Join(source.RootPath, "work-one", "duplicate.bin")
	if err := os.WriteFile(duplicatePath, fixture, 0o400); err != nil {
		t.Fatal(err)
	}
	job, err := service.CreateScan(ctx, source.ID, "personal-owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	publication, works, err := catalogStore.ListWorks(ctx)
	if err != nil || len(works) != 1 || works[0].MediaCount != 2 {
		t.Fatalf("重复 occurrence 扫描错误: %+v %+v %v", publication, works, err)
	}
	_, mediaBefore, err := catalogStore.ListMediaForWork(ctx, works[0].ID)
	if err != nil || len(mediaBefore) != 2 || mediaBefore[0].ID == mediaBefore[1].ID || mediaBefore[0].Digest != mediaBefore[1].Digest {
		t.Fatalf("相同 Blob 未保持两个 CanonicalMedia occurrence: %+v %v", mediaBefore, err)
	}
	var blobCount, locationCount int
	_ = store.Catalog.SQL().QueryRowContext(ctx, "SELECT count(*) FROM content_blobs WHERE catalog_revision_id=?", publication.CatalogRevisionID).Scan(&blobCount)
	_ = store.Catalog.SQL().QueryRowContext(ctx, "SELECT count(*) FROM file_locations WHERE catalog_revision_id=?", publication.CatalogRevisionID).Scan(&locationCount)
	if blobCount != 1 || locationCount != 2 {
		t.Fatalf("Blob/Location 基数错误: blobs=%d locations=%d", blobCount, locationCount)
	}
	var duplicateID string
	for _, item := range mediaBefore {
		if item.RelativePath == "work-one/duplicate.bin" {
			duplicateID = item.ID
		}
	}
	if err := os.Chmod(duplicatePath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(duplicatePath, []byte("replacement blob bytes"), 0o400); err != nil {
		t.Fatal(err)
	}
	replacementJob, err := service.CreateScan(ctx, source.ID, "personal-owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, replacementJob.ID); err != nil {
		t.Fatal(err)
	}
	replacementPublication, _, _ := catalogStore.ListWorks(ctx)
	_, mediaAfter, err := catalogStore.ListMediaForWork(ctx, works[0].ID)
	if err != nil || len(mediaAfter) != 2 {
		t.Fatal(err)
	}
	var replacementID, replacementDigest string
	for _, item := range mediaAfter {
		if item.RelativePath == "work-one/duplicate.bin" {
			replacementID, replacementDigest = item.ID, item.Digest
		}
	}
	if replacementID != duplicateID || replacementDigest == mediaBefore[0].Digest {
		t.Fatalf("路径内容替换未保持 Media/建立新 Blob: before=%+v after=%+v", mediaBefore, mediaAfter)
	}
	_ = store.Catalog.SQL().QueryRowContext(ctx, "SELECT count(*) FROM content_blobs WHERE catalog_revision_id=?", replacementPublication.CatalogRevisionID).Scan(&blobCount)
	if blobCount != 2 {
		t.Fatalf("替换后当前 Catalog Blob 数=%d", blobCount)
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
	if err := os.WriteFile(filepath.Join(sourceRoot, "work-one", "metadata.json"), []byte(`{"creator":{"name":"Synthetic Creator"}}`), 0o400); err != nil {
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
