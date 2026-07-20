package scanner_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/hashjob"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/scanner"
	"github.com/RecRivenVI/gallery/internal/storage"
)

const multiMediaCount = 20

// setupMultiMedia 构造单个 Source、单个 Work、multiMediaCount 个媒体的合成夹具，用于
// 验证单媒体按需确认只强制目标媒体重新哈希，不把整个 Source 拖入完整重新验证。
func setupMultiMedia(t *testing.T) (*application.Resources, *jobs.Store, *catalog.Store, *scanner.Service, application.Source, *storage.Store) {
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
	for index := 0; index < multiMediaCount; index++ {
		name := fmt.Sprintf("media-%02d.bin", index)
		content := fmt.Appendf(nil, "multi-media fixture content for item %02d", index)
		if err := os.WriteFile(filepath.Join(sourceRoot, "work-one", name), content, 0o400); err != nil {
			t.Fatal(err)
		}
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
	hashService, err := hashjob.New(context.Background(), resources, jobStore)
	if err != nil {
		t.Fatal(err)
	}
	service.SetHashService(hashService)
	return resources, jobStore, catalogStore, service, source, store
}

func countHashJobs(t *testing.T, store *storage.Store) int {
	t.Helper()
	var count int
	if err := store.Control.SQL().QueryRowContext(context.Background(),
		"SELECT count(*) FROM jobs WHERE job_type='hash'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// TestCreateVerificationScanOnlyForcesTargetMedia 覆盖 Documents 三.4 的核心断言：20 个
// 媒体全部以 index 发布为 located_unverified 后，只请求确认其中一个必须只为该媒体建立
// 强制 Hash Job，其余 19 个媒体不得产生 Hash Job、必须继续保持 located_unverified，且
// Source 摘要（媒体总数、其余媒体标识）在确认前后完全一致。
func TestCreateVerificationScanOnlyForcesTargetMedia(t *testing.T) {
	_, _, catalogStore, service, source, store := setupMultiMedia(t)
	defer store.Close()
	ctx := context.Background()

	indexJob, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIndex)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, indexJob.ID); err != nil {
		t.Fatal(err)
	}
	_, works, err := catalogStore.ListWorks(ctx)
	if err != nil || len(works) != 1 {
		t.Fatalf("期望恰好一个 Work: %+v %v", works, err)
	}
	publicationBefore, mediaBefore, err := catalogStore.ListMediaForWork(ctx, works[0].ID)
	if err != nil || len(mediaBefore) != multiMediaCount {
		t.Fatalf("期望 %d 个媒体: got=%d err=%v", multiMediaCount, len(mediaBefore), err)
	}
	for _, item := range mediaBefore {
		if item.ContentVerificationState != catalog.ContentVerificationStateLocatedUnverified {
			t.Fatalf("index 发布后所有媒体都应是 located_unverified: %+v", item)
		}
	}
	if countHashJobs(t, store) != 0 {
		t.Fatalf("index 发布不应建立任何 Hash Job")
	}

	target := mediaBefore[7]
	verifyJob, err := service.CreateVerificationScan(ctx, source.ID, "personal-owner", "", []scanner.VerificationTarget{
		{MediaID: target.ID, SourceID: source.ID, RelativePath: target.RelativePath, ObservationFingerprint: "test-fingerprint"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, verifyJob.ID); err != nil {
		t.Fatal(err)
	}

	if countHashJobs(t, store) != 1 {
		t.Fatalf("目标化确认应恰好建立一个 Hash Job，实际=%d", countHashJobs(t, store))
	}

	publicationAfter, mediaAfter, err := catalogStore.ListMediaForWork(ctx, works[0].ID)
	if err != nil || len(mediaAfter) != multiMediaCount {
		t.Fatalf("确认后媒体总数应保持 %d: got=%d err=%v", multiMediaCount, len(mediaAfter), err)
	}
	verifiedCount, unverifiedCount := 0, 0
	for _, item := range mediaAfter {
		if item.ID == target.ID {
			if item.ContentVerificationState != catalog.ContentVerificationStateContentVerified {
				t.Fatalf("目标媒体应成为 content_verified: %+v", item)
			}
			if item.Digest == "" {
				t.Fatalf("目标媒体应有真实 digest: %+v", item)
			}
			verifiedCount++
			continue
		}
		if item.ContentVerificationState != catalog.ContentVerificationStateLocatedUnverified {
			t.Fatalf("非目标媒体不应被确认，仍应是 located_unverified: %+v", item)
		}
		unverifiedCount++
	}
	if verifiedCount != 1 || unverifiedCount != multiMediaCount-1 {
		t.Fatalf("期望恰好 1 个已确认、%d 个未确认媒体，实际 verified=%d unverified=%d", multiMediaCount-1, verifiedCount, unverifiedCount)
	}

	// 旧 publication 仍可读取原（全部 located_unverified）状态。
	_, oldItem, err := catalogStore.GetMediaAt(ctx, publicationBefore.ID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if oldItem.ContentVerificationState != catalog.ContentVerificationStateLocatedUnverified {
		t.Fatalf("旧 publication 应仍读取到确认前状态: %+v", oldItem)
	}
	// 新 publication 能读取目标正文（已确认状态）。
	_, newItem, err := catalogStore.GetMediaAt(ctx, publicationAfter.ID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if newItem.ContentVerificationState != catalog.ContentVerificationStateContentVerified {
		t.Fatalf("新 publication 应读取到确认后状态: %+v", newItem)
	}

	// Source 摘要（媒体数量与非目标媒体标识集合）前后完全一致。
	beforeIDs := map[string]struct{}{}
	for _, item := range mediaBefore {
		if item.ID != target.ID {
			beforeIDs[item.ID] = struct{}{}
		}
	}
	afterIDs := map[string]struct{}{}
	for _, item := range mediaAfter {
		if item.ID != target.ID {
			afterIDs[item.ID] = struct{}{}
		}
	}
	if len(beforeIDs) != len(afterIDs) {
		t.Fatalf("非目标媒体集合大小发生变化: before=%d after=%d", len(beforeIDs), len(afterIDs))
	}
	for id := range beforeIDs {
		if _, ok := afterIDs[id]; !ok {
			t.Fatalf("非目标媒体 %s 在确认后消失", id)
		}
	}
}

// TestCreateVerificationScanReusesRunningJobForSameObservation 覆盖幂等表：同 observation
// 的运行中 Job 必须被复用，不建立第二个 Job。
func TestCreateVerificationScanReusesRunningJobForSameObservation(t *testing.T) {
	_, jobStore, catalogStore, service, source, store := setupMultiMedia(t)
	defer store.Close()
	ctx := context.Background()

	indexJob, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIndex)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, indexJob.ID); err != nil {
		t.Fatal(err)
	}
	_, works, err := catalogStore.ListWorks(ctx)
	if err != nil || len(works) != 1 {
		t.Fatal(err)
	}
	_, mediaItems, err := catalogStore.ListMediaForWork(ctx, works[0].ID)
	if err != nil || len(mediaItems) != multiMediaCount {
		t.Fatal(err)
	}
	target := mediaItems[3]
	targets := []scanner.VerificationTarget{{MediaID: target.ID, SourceID: source.ID, RelativePath: target.RelativePath}}

	first, err := service.CreateVerificationScan(ctx, source.ID, "personal-owner", "idempotency-key-fixed", targets)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateVerificationScan(ctx, source.ID, "personal-owner", "idempotency-key-fixed", targets)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("同 observation 的重复请求应复用同一 Job: first=%s second=%s", first.ID, second.ID)
	}
	all, err := jobStore.ListByStatuses(ctx, jobs.StatusQueued, jobs.StatusRunning, jobs.StatusPublishing, jobs.StatusCompleted)
	if err != nil {
		t.Fatal(err)
	}
	scanJobs := 0
	for _, job := range all {
		if job.Type == "scan" {
			scanJobs++
		}
	}
	if scanJobs != 2 { // index + 一个 verification scan（未被重复创建）
		t.Fatalf("重复请求不应新建第二个 verification scan Job，实际 scan Job 数=%d", scanJobs)
	}
}

// TestCreateVerificationScanDoesNotReuseAfterObservationChanges 覆盖幂等表：observation
// 变化（本例中改变幂等键，模拟服务端按新观察派生出不同 key）后不复用旧 Job。
func TestCreateVerificationScanDoesNotReuseAfterObservationChanges(t *testing.T) {
	_, _, catalogStore, service, source, store := setupMultiMedia(t)
	defer store.Close()
	ctx := context.Background()

	indexJob, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIndex)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, indexJob.ID); err != nil {
		t.Fatal(err)
	}
	_, works, err := catalogStore.ListWorks(ctx)
	if err != nil || len(works) != 1 {
		t.Fatal(err)
	}
	_, mediaItems, err := catalogStore.ListMediaForWork(ctx, works[0].ID)
	if err != nil || len(mediaItems) != multiMediaCount {
		t.Fatal(err)
	}
	target := mediaItems[5]
	targets := []scanner.VerificationTarget{{MediaID: target.ID, SourceID: source.ID, RelativePath: target.RelativePath}}

	first, err := service.CreateVerificationScan(ctx, source.ID, "personal-owner", "observation-key-v1", targets)
	if err != nil {
		t.Fatal(err)
	}
	// Source 单活跃扫描约束下，第二个请求必须等第一个 Job 进入终态后才能建立新 Job；
	// 这里先让第一个 Job 完成，再验证不同 observation 的幂等键不会命中同一 Job。
	if err := service.Execute(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateVerificationScan(ctx, source.ID, "personal-owner", "observation-key-v2", targets)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatalf("不同 observation 的幂等键不应命中同一 Job")
	}
}

// TestVerificationScanCompetesWithNormalScanUnderSingleActiveScanConstraint 覆盖恢复
// 矩阵要求："确认 Job 与普通 Source scan 竞争时不破坏 Source 单活跃扫描约束"：目标化
// 确认 Job 复用与普通扫描完全相同的 jobs 表与 ResourceScan 资源类，因此同一 Source
// 同时只能有一个未终结的 scan 类 Job，无论它是普通扫描还是目标化确认。
func TestVerificationScanCompetesWithNormalScanUnderSingleActiveScanConstraint(t *testing.T) {
	_, _, catalogStore, service, source, store := setupMultiMedia(t)
	defer store.Close()
	ctx := context.Background()

	indexJob, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIndex)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, indexJob.ID); err != nil {
		t.Fatal(err)
	}
	_, works, err := catalogStore.ListWorks(ctx)
	if err != nil || len(works) != 1 {
		t.Fatal(err)
	}
	_, mediaItems, err := catalogStore.ListMediaForWork(ctx, works[0].ID)
	if err != nil || len(mediaItems) != multiMediaCount {
		t.Fatal(err)
	}
	target := mediaItems[0]

	// 建立一个从未 Execute（保持 queued/未终结）的普通扫描 Job，占住该 Source 的单活跃
	// 扫描槽位；随后的目标化确认请求必须因为 Source 已有未终结 scan Job 而被数据库层
	// 唯一约束拒绝为 SCAN_ALREADY_RUNNING，不能绕过这个既有约束单独建立第二个 Job。
	blocking, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if blocking.Status != jobs.StatusQueued {
		t.Fatalf("占位扫描 Job 应处于 queued: %+v", blocking)
	}

	_, err = service.CreateVerificationScan(ctx, source.ID, "personal-owner", "", []scanner.VerificationTarget{
		{MediaID: target.ID, SourceID: source.ID, RelativePath: target.RelativePath},
	})
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeScanAlreadyRunning {
		t.Fatalf("普通扫描占位期间目标化确认应被拒绝为 SCAN_ALREADY_RUNNING: %v", err)
	}

	// 占位扫描完成终结后，目标化确认必须能正常建立。
	if err := service.Execute(ctx, blocking.ID); err != nil {
		t.Fatal(err)
	}
	verifyJob, err := service.CreateVerificationScan(ctx, source.ID, "personal-owner", "", []scanner.VerificationTarget{
		{MediaID: target.ID, SourceID: source.ID, RelativePath: target.RelativePath},
	})
	if err != nil {
		t.Fatalf("占位扫描终结后目标化确认应可正常建立: %v", err)
	}
	if err := service.Execute(ctx, verifyJob.ID); err != nil {
		t.Fatal(err)
	}
}

// TestCreateVerificationScanRejectsUnpublishedSource 覆盖前置条件：Source 尚无
// publication 时不能建立目标化确认 Job（单媒体确认只对已发布 Catalog 中的已知媒体有意义）。
func TestCreateVerificationScanRejectsUnpublishedSource(t *testing.T) {
	_, _, _, service, source, store := setupMultiMedia(t)
	defer store.Close()
	ctx := context.Background()

	_, err := service.CreateVerificationScan(ctx, source.ID, "personal-owner", "", []scanner.VerificationTarget{
		{MediaID: "med_018f47d2-5c16-7a44-a8a0-000000000001", SourceID: source.ID, RelativePath: "work-one/media-00.bin"},
	})
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeConflict {
		t.Fatalf("未发布 Source 的目标化确认应返回结构化 CONFLICT: %v", err)
	}
}

// TestCreateVerificationScanRecoversAfterRestart 覆盖恢复语义：目标化确认 Job 在中断后
// 可以通过 Reconcile/Retry 恢复完成，不遗留悬挂 running 状态。
func TestCreateVerificationScanRecoversAfterRestart(t *testing.T) {
	_, jobStore, catalogStore, service, source, store := setupMultiMedia(t)
	defer store.Close()
	ctx := context.Background()

	indexJob, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIndex)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, indexJob.ID); err != nil {
		t.Fatal(err)
	}
	_, works, err := catalogStore.ListWorks(ctx)
	if err != nil || len(works) != 1 {
		t.Fatal(err)
	}
	_, mediaItems, err := catalogStore.ListMediaForWork(ctx, works[0].ID)
	if err != nil || len(mediaItems) != multiMediaCount {
		t.Fatal(err)
	}
	target := mediaItems[11]
	verifyJob, err := service.CreateVerificationScan(ctx, source.ID, "personal-owner", "", []scanner.VerificationTarget{
		{MediaID: target.ID, SourceID: source.ID, RelativePath: target.RelativePath},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 模拟强杀：Job 已 Start 但从未 Execute 完成（保持 queued，与真实重启后仅有 queued
	// 记录、从未产生 Attempt 的情形一致）。Reconcile 不应报错，随后重试执行必须仍然只
	// 强制目标媒体，最终完成确认。
	if err := service.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, verifyJob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := jobStore.Get(ctx, verifyJob.ID); err != nil {
		t.Fatal(err)
	}
	_, mediaAfter, err := catalogStore.ListMediaForWork(ctx, works[0].ID)
	if err != nil || len(mediaAfter) != multiMediaCount {
		t.Fatal(err)
	}
	for _, item := range mediaAfter {
		if item.ID == target.ID {
			if item.ContentVerificationState != catalog.ContentVerificationStateContentVerified {
				t.Fatalf("重启恢复后目标媒体应完成确认: %+v", item)
			}
		} else if item.ContentVerificationState != catalog.ContentVerificationStateLocatedUnverified {
			t.Fatalf("重启恢复后非目标媒体不应被确认: %+v", item)
		}
	}
}
