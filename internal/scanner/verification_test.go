package scanner_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/hashjob"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/scanner"
	"github.com/RecRivenVI/gallery/internal/storage"
)

// setupWithHash 在 setup 基础上注入真实的持久 Hash Job Service，使 job_type='hash' 计数
// 能够真实反映"是否重新读取并哈希了媒体正文"，而不是落入不产生 Hash Job 记录的同步
// fallback 路径。
func setupWithHash(t *testing.T, fixture []byte) (*application.Resources, *jobs.Store, *catalog.Store, *scanner.Service, application.Source, *storage.Store) {
	t.Helper()
	resources, jobStore, catalogStore, service, source, store := setup(t, fixture)
	hashService, err := hashjob.New(context.Background(), resources, jobStore)
	if err != nil {
		t.Fatal(err)
	}
	service.SetHashService(hashService)
	return resources, jobStore, catalogStore, service, source, store
}

func TestIndexProfilePublishesLocatedUnverifiedWithoutHashing(t *testing.T) {
	fixture := []byte("index profile does not read media content into a digest")
	_, _, catalogStore, service, source, store := setupWithHash(t, fixture)
	defer store.Close()
	ctx := context.Background()

	job, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIndex)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, job.ID); err != nil {
		t.Fatal(err)
	}

	publication, works, err := catalogStore.ListWorks(ctx)
	if err != nil || len(works) != 1 {
		t.Fatalf("index 档案未发布 Work: %+v %v", works, err)
	}
	_, mediaItems, err := catalogStore.ListMediaForWork(ctx, works[0].ID)
	if err != nil || len(mediaItems) != 1 {
		t.Fatalf("index 档案未发布 Media: %+v %v", mediaItems, err)
	}
	if mediaItems[0].LocationStatus != catalog.ContentVerificationStateLocatedUnverified {
		t.Fatalf("index 档案媒体应为 located_unverified: %+v", mediaItems[0])
	}
	if mediaItems[0].Digest != "" || mediaItems[0].Algorithm != "" {
		t.Fatalf("index 档案不得建立伪造 digest: %+v", mediaItems[0])
	}
	var blobCount int
	if err := store.Catalog.SQL().QueryRowContext(ctx, "SELECT count(*) FROM content_blobs WHERE catalog_revision_id=?", publication.CatalogRevisionID).Scan(&blobCount); err != nil {
		t.Fatal(err)
	}
	if blobCount != 0 {
		t.Fatalf("index 档案不应写入 content_blobs: blobCount=%d", blobCount)
	}
	var hashJobCount int
	if err := store.Control.SQL().QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE job_type='hash'").Scan(&hashJobCount); err != nil {
		t.Fatal(err)
	}
	if hashJobCount != 0 {
		t.Fatalf("index 档案不应建立任何 Hash Job: hashJobCount=%d", hashJobCount)
	}
}

func TestIncrementalProfileReusesUnchangedDigestWithoutRehash(t *testing.T) {
	fixture := []byte("incremental profile reuses this content across an unchanged rescan")
	_, _, catalogStore, service, source, store := setupWithHash(t, fixture)
	defer store.Close()
	ctx := context.Background()

	first, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	_, worksBefore, err := catalogStore.ListWorks(ctx)
	if err != nil || len(worksBefore) != 1 {
		t.Fatal(err)
	}
	_, mediaBefore, err := catalogStore.ListMediaForWork(ctx, worksBefore[0].ID)
	if err != nil || len(mediaBefore) != 1 || mediaBefore[0].Digest == "" {
		t.Fatalf("首次 incremental 扫描未建立已确认 digest: %+v %v", mediaBefore, err)
	}

	second, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	_, worksAfter, err := catalogStore.ListWorks(ctx)
	if err != nil || len(worksAfter) != 1 {
		t.Fatal(err)
	}
	_, mediaAfter, err := catalogStore.ListMediaForWork(ctx, worksAfter[0].ID)
	if err != nil || len(mediaAfter) != 1 {
		t.Fatal(err)
	}
	if mediaAfter[0].Digest != mediaBefore[0].Digest {
		t.Fatalf("未变化文件的 digest 应保持一致: before=%s after=%s", mediaBefore[0].Digest, mediaAfter[0].Digest)
	}
	if mediaAfter[0].LocationStatus != "present" {
		t.Fatalf("复用已确认 digest 后媒体应为 present: %+v", mediaAfter[0])
	}
	// 无变化的第二次扫描不应为该文件再次建立 Hash Job；控制库应仍只有首次扫描产生的那一个。
	var hashJobCount int
	if err := store.Control.SQL().QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE job_type='hash'").Scan(&hashJobCount); err != nil {
		t.Fatal(err)
	}
	if hashJobCount != 1 {
		t.Fatalf("无变化 incremental 重扫不应新建 Hash Job，实际 hashJobCount=%d", hashJobCount)
	}
}

func TestIncrementalProfileRehashesWhenSizeChanges(t *testing.T) {
	fixture := []byte("incremental profile detects a size change and re-verifies")
	_, _, catalogStore, service, source, store := setupWithHash(t, fixture)
	defer store.Close()
	ctx := context.Background()

	first, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	_, worksBefore, err := catalogStore.ListWorks(ctx)
	if err != nil || len(worksBefore) != 1 {
		t.Fatal(err)
	}
	_, mediaBefore, err := catalogStore.ListMediaForWork(ctx, worksBefore[0].ID)
	if err != nil || len(mediaBefore) != 1 {
		t.Fatal(err)
	}

	mediaPath := filepath.Join(source.RootPath, "work-one", "media.bin")
	if err := os.Chmod(mediaPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mediaPath, append(fixture, []byte(" plus extra bytes changing the size")...), 0o400); err != nil {
		t.Fatal(err)
	}

	second, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	_, worksAfter, err := catalogStore.ListWorks(ctx)
	if err != nil || len(worksAfter) != 1 {
		t.Fatal(err)
	}
	_, mediaAfter, err := catalogStore.ListMediaForWork(ctx, worksAfter[0].ID)
	if err != nil || len(mediaAfter) != 1 {
		t.Fatal(err)
	}
	if mediaAfter[0].Digest == mediaBefore[0].Digest {
		t.Fatalf("大小变化后 digest 应重新确认: before=%s after=%s", mediaBefore[0].Digest, mediaAfter[0].Digest)
	}
	var hashJobCount int
	if err := store.Control.SQL().QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE job_type='hash'").Scan(&hashJobCount); err != nil {
		t.Fatal(err)
	}
	if hashJobCount != 2 {
		t.Fatalf("大小变化应触发第二个 Hash Job，实际 hashJobCount=%d", hashJobCount)
	}
}

func TestIncrementalProfileRehashesWhenMTimeChangesWithSameSize(t *testing.T) {
	fixture := []byte("incremental profile detects a same-size mtime-only change")
	_, _, catalogStore, service, source, store := setupWithHash(t, fixture)
	defer store.Close()
	ctx := context.Background()

	first, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	_, worksBefore, err := catalogStore.ListWorks(ctx)
	if err != nil || len(worksBefore) != 1 {
		t.Fatal(err)
	}
	_, mediaBefore, err := catalogStore.ListMediaForWork(ctx, worksBefore[0].ID)
	if err != nil || len(mediaBefore) != 1 {
		t.Fatal(err)
	}

	// 同大小但内容不同、时间戳被人为改动，模拟"同大小、恢复 mtime"以外的、更常见的
	// "同大小但 mtime 真实前进"场景；size 相同因此必须依赖 mtime 证据触发重新验证。
	mediaPath := filepath.Join(source.RootPath, "work-one", "media.bin")
	replacement := []byte(fixture)
	replacement[0] ^= 0xFF
	if err := os.Chmod(mediaPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mediaPath, replacement, 0o400); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(mediaPath, future, future); err != nil {
		t.Fatal(err)
	}

	second, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	_, worksAfter, err := catalogStore.ListWorks(ctx)
	if err != nil || len(worksAfter) != 1 {
		t.Fatal(err)
	}
	_, mediaAfter, err := catalogStore.ListMediaForWork(ctx, worksAfter[0].ID)
	if err != nil || len(mediaAfter) != 1 {
		t.Fatal(err)
	}
	if len(fixture) != len(replacement) {
		t.Fatal("测试前置条件错误：大小应保持一致")
	}
	if mediaAfter[0].Digest == mediaBefore[0].Digest {
		t.Fatal("mtime 变化后应重新确认 digest（本用例中 mtime 与内容一并改变属预期覆盖面）")
	}
}

func TestVerifyProfileAlwaysRehashesRegardlessOfPriorConfirmation(t *testing.T) {
	fixture := []byte("verify profile ignores prior confirmation and rehashes unconditionally")
	_, _, catalogStore, service, source, store := setupWithHash(t, fixture)
	defer store.Close()
	ctx := context.Background()

	first, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, first.ID); err != nil {
		t.Fatal(err)
	}

	// 文件完全未变化；incremental 档案会复用，而 verify 档案必须无条件重新哈希。
	second, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileVerify)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	_, works, err := catalogStore.ListWorks(ctx)
	if err != nil || len(works) != 1 {
		t.Fatal(err)
	}
	_, mediaItems, err := catalogStore.ListMediaForWork(ctx, works[0].ID)
	if err != nil || len(mediaItems) != 1 || mediaItems[0].Digest == "" {
		t.Fatalf("verify 档案应产生已确认 digest: %+v %v", mediaItems, err)
	}
	var hashJobCount int
	if err := store.Control.SQL().QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE job_type='hash'").Scan(&hashJobCount); err != nil {
		t.Fatal(err)
	}
	if hashJobCount != 2 {
		t.Fatalf("verify 档案应对未变化文件也重新建立 Hash Job，实际 hashJobCount=%d", hashJobCount)
	}
}
