package bootstrap

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/overlay"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/querytext"
	"github.com/RecRivenVI/gallery/internal/storage"
)

const (
	driveTestCatalogID = "cat_018f47d2-5c16-7a44-a8a0-0000000dba01"
	driveTestOverlayID = "ovr_018f47d2-5c16-7a44-a8a0-0000000dba01"
	driveTestScanJobID = "job_018f47d2-5c16-7a44-a8a0-0000000dba01"
	driveTestPubID     = "qpub_018f47d2-5c16-7a44-a8a0-0000000dba01"
	driveTestWorkID    = "wrk_018f47d2-5c16-7a44-a8a0-0000000dba01"
)

// newDriveFixture 搭建一个拥有单个真实 active publication 的最小 overlay 环境，仅用于
// 白盒验证 driveOverlayProjectionJobToCompletion——不通过完整 bootstrap.Run，直接构造
// jobs.Store/catalog.Store/overlay.Service，以便手工把一个 overlay_projection Job 摆进
// 任意状态（queued/failed 可重试/failed 永久/running 陈旧租约），观察函数是否正确驱动
// 它到 completed，或在真正永久失败时如实报错而不是静默放行。
func newDriveFixture(t *testing.T) (context.Context, *sql.DB, *jobs.Store, *overlay.Service) {
	t.Helper()
	ctx := context.Background()
	fixed := clock.Fixed{Time: time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)}
	dirs := appdirs.UnderRoot(t.TempDir())
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.Control.SQL().ExecContext(ctx,
		`INSERT INTO canonical_works (work_id, title, created_at) VALUES (?, '源标题', 1)`, driveTestWorkID); err != nil {
		t.Fatal(err)
	}
	document := querytext.BuildDocument("源标题", "作者", []string{"source"}, []string{"01.jpg"})
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO catalog_revisions VALUES (?, ?, 'src_test', 'published', 1, 2)`, []any{driveTestCatalogID, driveTestScanJobID}},
		{`INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, published_at)
VALUES (?, ?, 0, 'published', 1, 2)`, []any{driveTestOverlayID, driveTestCatalogID}},
		{`INSERT INTO query_publications VALUES (?, ?, ?, ?, 0, 2)`, []any{driveTestPubID, driveTestCatalogID, driveTestOverlayID, driveTestScanJobID}},
		{`INSERT INTO active_query_publication VALUES (1, ?)`, []any{driveTestPubID}},
		{`INSERT INTO source_works (catalog_revision_id, source_id, source_key, title, creator, tags_json, filenames_text)
VALUES (?, 'src_test', 'work-key', '源标题', '作者', '["source"]', '["01.jpg"]')`, []any{driveTestCatalogID}},
		{`INSERT INTO work_projections
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id, title, creator,
 tags_json, filenames_text, normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text, sort_title_key, hidden)
VALUES (?, ?, ?, 'src_test', 'work-key', 'lib_test', '源标题', '作者', '["source"]', '["01.jpg"]', ?, ?, ?, ?, 0)`, []any{
			driveTestCatalogID, driveTestOverlayID, driveTestWorkID,
			document.NormalizedOriginal, document.CJKTokens, document.LatinTokens, document.SortTitleKey,
		}},
		{`INSERT INTO work_search VALUES (?, ?, ?, ?, ?, ?)`, []any{
			driveTestCatalogID, driveTestOverlayID, driveTestWorkID, document.NormalizedOriginal, document.CJKTokens, document.LatinTokens,
		}},
	}
	for _, statement := range statements {
		if _, err := store.Catalog.SQL().ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	jobStore, err := jobs.NewStore(store.Control.SQL(), fixed, identity.NewGenerator(fixed))
	if err != nil {
		t.Fatal(err)
	}
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), fixed, identity.NewGenerator(fixed))
	if err != nil {
		t.Fatal(err)
	}
	overlayService, err := overlay.New(ctx, store.Control.SQL(), jobStore, catalogStore, fixed, nil)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, store.Control.SQL(), jobStore, overlayService
}

// TestDriveOverlayProjectionJobToCompletionRetriesTransientFailureUntilCompleted 覆盖
// EnqueueOverlayProjectionTx 把回填请求合并到一个既有 Job、而那个 Job 恰好处于可重试
// failed 状态（例如另一次 Overlay 写入触发的投影遭遇了瞬时错误）的场景：调用方不能
// 因为“已经存在一个 Job”就直接认为回填完成，必须驱动它经历 Retry→Execute 直到真正
// 到达 completed。
func TestDriveOverlayProjectionJobToCompletionRetriesTransientFailureUntilCompleted(t *testing.T) {
	ctx, _, jobStore, overlayService := newDriveFixture(t)
	job, created, err := overlayService.TriggerReprojection(ctx, "system:query-dependency-backfill")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("首次排队应该真实创建 Job")
	}
	// 模拟这个 Job 被其它路径（例如真实执行中的瞬时 I/O 抖动）标记为可重试失败，模拟
	// "合并到的既有 Job 当前恰好是 failed 但还没耗尽重试次数"的状态。
	if _, err := jobStore.FailWithRetryable(ctx, job.ID, "SIMULATED_TRANSIENT", true); err != nil {
		t.Fatal(err)
	}
	failed, err := jobStore.Get(ctx, job.ID)
	if err != nil || failed.Status != jobs.StatusFailed || !failed.FailureRetryable {
		t.Fatalf("前置状态应为可重试的 failed: %+v %v", failed, err)
	}
	if err := driveOverlayProjectionJobToCompletion(ctx, jobStore, overlayService, job.ID, "system:query-dependency-backfill"); err != nil {
		t.Fatalf("应该驱动到 completed 而不是报错: %v", err)
	}
	final, err := jobStore.Get(ctx, job.ID)
	if err != nil || final.Status != jobs.StatusCompleted {
		t.Fatalf("最终应该真正 completed，不能停留在中间状态: %+v %v", final, err)
	}
}

// TestDriveOverlayProjectionJobToCompletionRejectsPermanentFailure 覆盖"合并到的既有
// Job 已经不可重试失败"的场景：这必须让调用方（bootstrap）如实收到错误、从而不会
// 写入回填完成标记，而不是被静默当作已完成处理。
func TestDriveOverlayProjectionJobToCompletionRejectsPermanentFailure(t *testing.T) {
	ctx, _, jobStore, overlayService := newDriveFixture(t)
	job, created, err := overlayService.TriggerReprojection(ctx, "system:query-dependency-backfill")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("首次排队应该真实创建 Job")
	}
	if _, err := jobStore.FailWithRetryable(ctx, job.ID, "SIMULATED_PERMANENT", false); err != nil {
		t.Fatal(err)
	}
	if err := driveOverlayProjectionJobToCompletion(ctx, jobStore, overlayService, job.ID, "system:query-dependency-backfill"); err == nil {
		t.Fatal("永久失败的 Job 不应该被当作回填已完成")
	}
}

// TestDriveOverlayProjectionJobToCompletionRecoversStaleRunningAttempt 覆盖"合并到的
// 既有 Job 是上一次进程崩溃遗留、租约已过期的 running 行"场景：此时既没有调度器也没有
// 恢复循环在运行，必须先用既有 ReconcileAttempts 把它收敛为可重试的 failed，再正常
// Retry/Execute 到 completed，不能因为看到 running 就误判为"有其它执行者正在处理"
// 而放弃等待、提前放行。
func TestDriveOverlayProjectionJobToCompletionRecoversStaleRunningAttempt(t *testing.T) {
	ctx, controlDB, jobStore, overlayService := newDriveFixture(t)
	job, created, err := overlayService.TriggerReprojection(ctx, "system:query-dependency-backfill")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("首次排队应该真实创建 Job")
	}
	if _, err := jobStore.StartStage(ctx, job.ID, "reprojecting"); err != nil {
		t.Fatal(err)
	}
	// 把这次 Attempt 的心跳与租约强行改到远早于任何 leaseTimeout 的纪元时间点，模拟进程在
	// 执行中途被杀、从未再更新心跳的陈旧 running 行——此刻既没有调度器也没有恢复循环
	// 在运行，唯一能收敛它的只有 driveOverlayProjectionJobToCompletion 内部调用的
	// ReconcileAttempts。
	if _, err := controlDB.ExecContext(ctx,
		`UPDATE job_attempts SET heartbeat_at=0, lease_expires_at=0 WHERE job_id=? AND status='running'`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := driveOverlayProjectionJobToCompletion(ctx, jobStore, overlayService, job.ID, "system:query-dependency-backfill"); err != nil {
		t.Fatalf("应该能从陈旧 running 状态恢复并驱动到 completed: %v", err)
	}
	final, err := jobStore.Get(ctx, job.ID)
	if err != nil || final.Status != jobs.StatusCompleted {
		t.Fatalf("最终应该真正 completed: %+v %v", final, err)
	}
}
