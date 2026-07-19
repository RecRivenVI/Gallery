package overlay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	galleryquery "github.com/RecRivenVI/gallery/internal/query"
	"github.com/RecRivenVI/gallery/internal/querytext"
	"github.com/RecRivenVI/gallery/internal/storage"
)

const (
	testCatalogID = "cat_018f47d2-5c16-7a44-a8a0-000000000001"
	testOverlayID = "ovr_018f47d2-5c16-7a44-a8a0-000000000001"
	testScanJobID = "job_018f47d2-5c16-7a44-a8a0-000000000001"
	testPubID     = "qpub_018f47d2-5c16-7a44-a8a0-000000000001"
	testWorkID    = "wrk_018f47d2-5c16-7a44-a8a0-000000000001"
	testMedia1ID  = "med_018f47d2-5c16-7a44-a8a0-000000000001"
	testMedia2ID  = "med_018f47d2-5c16-7a44-a8a0-000000000002"
)

func TestOverlayFactProjectionAndLiveState(t *testing.T) {
	ctx, store, service, catalogStore, queryService := newFixture(t)
	input := Input{TitleOverride: "覆盖标题", ManualTags: []string{"手工", "手工"}, CustomCoverMediaID: testMedia2ID, Favorite: true, Progress: 0.5}
	created, err := service.Put(ctx, testWorkID, "owner", input)
	if err != nil || !created.StartJob || created.ProjectionStatus != "pending" || created.ProjectionJobID == "" {
		t.Fatalf("同步写入未产生 pending projection: %+v %v", created, err)
	}
	before, err := queryService.Search(ctx, galleryquery.Request{Limit: 20, AuthorizationScope: "owner"})
	if err != nil || len(before.Items) != 1 || before.Items[0].Title != "源标题" || before.QueryPublicationID != testPubID {
		t.Fatalf("异步发布前污染旧 publication: %+v %v", before, err)
	}
	if err := service.Execute(ctx, created.ProjectionJobID); err != nil {
		t.Fatal(err)
	}
	after, err := service.Get(ctx, testWorkID)
	if err != nil || after.ProjectionStatus != "published" || after.PublishedQueryPublicationID == testPubID || after.ProjectedWatermark != after.QueryWatermark {
		t.Fatalf("projection 状态错误: %+v %v", after, err)
	}
	current, err := queryService.Search(ctx, galleryquery.Request{Search: "覆盖", Tag: "手工", Limit: 20, AuthorizationScope: "owner"})
	if err != nil || len(current.Items) != 1 || current.Items[0].Title != "覆盖标题" || current.CatalogRevision != testCatalogID || current.OverlayProjectionRevision == testOverlayID {
		t.Fatalf("新双 revision 查询未切换: %+v %v", current, err)
	}
	old, err := queryService.Search(ctx, galleryquery.Request{QueryPublicationID: testPubID, Limit: 20, AuthorizationScope: "owner"})
	if err != nil || len(old.Items) != 1 || old.Items[0].Title != "源标题" {
		t.Fatalf("旧 publication 不再可读: %+v %v", old, err)
	}
	_, media, err := catalogStore.ListMediaForWork(ctx, testWorkID)
	if err != nil || len(media) != 2 || media[0].ID != testMedia2ID || media[0].Ordinal != -1 {
		t.Fatalf("自定义封面未影响投影顺序: %+v %v", media, err)
	}

	publicationBeforeLive, _ := catalogStore.Current(ctx)
	input.Favorite, input.Progress = false, 0.9
	live, err := service.Put(ctx, testWorkID, "owner", input)
	if err != nil || live.StartJob || live.ProjectionJobID != created.ProjectionJobID {
		t.Fatalf("live state 意外创建 projection: %+v %v", live, err)
	}
	publicationAfterLive, _ := catalogStore.Current(ctx)
	if publicationAfterLive.ID != publicationBeforeLive.ID {
		t.Fatal("Favorite/Progress 改变了 query publication")
	}

	cleared, err := service.Put(ctx, testWorkID, "owner", Input{Progress: 0.9})
	if err != nil || !cleared.StartJob {
		t.Fatalf("清除覆盖未排队: %+v %v", cleared, err)
	}
	if err := service.Execute(ctx, cleared.ProjectionJobID); err != nil {
		t.Fatal(err)
	}
	restored, err := queryService.Search(ctx, galleryquery.Request{Tag: "source", Limit: 20, AuthorizationScope: "owner"})
	if err != nil || len(restored.Items) != 1 || restored.Items[0].Title != "源标题" {
		t.Fatalf("清除覆盖未恢复 Source 基线: %+v %v", restored, err)
	}
	_, media, _ = catalogStore.ListMediaForWork(ctx, testWorkID)
	if media[0].ID != testMedia1ID || media[0].Ordinal != 0 {
		t.Fatalf("清除封面未恢复 base ordinal: %+v", media)
	}

	var pending int
	if err := store.Catalog.SQL().QueryRowContext(ctx, "SELECT count(*) FROM overlay_projection_revisions WHERE status='staging'").Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("残留 staging candidate=%d err=%v", pending, err)
	}
}

func TestContinuousChangesCoalesceAndSupersedeCandidate(t *testing.T) {
	ctx, store, service, _, queryService := newFixture(t)
	first, err := service.Put(ctx, testWorkID, "owner", Input{TitleOverride: "第一版"})
	if err != nil {
		t.Fatal(err)
	}
	injected := false
	service.faultInjector = func(stage string) error {
		if stage == "before_publish" && !injected {
			injected = true
			merged, err := service.Put(ctx, testWorkID, "owner", Input{TitleOverride: "第二版"})
			if err != nil {
				return err
			}
			if merged.ProjectionJobID != first.ProjectionJobID || merged.StartJob {
				return fmt.Errorf("连续写入未合并到同一 Job")
			}
		}
		return nil
	}
	if err := service.Execute(ctx, first.ProjectionJobID); err != nil {
		t.Fatal(err)
	}
	result, err := queryService.Search(ctx, galleryquery.Request{Search: "第二版", Limit: 20, AuthorizationScope: "owner"})
	if err != nil || len(result.Items) != 1 {
		t.Fatalf("最终 watermark 未发布: %+v %v", result, err)
	}
	var publications, superseded, staging int
	_ = store.Catalog.SQL().QueryRowContext(ctx, "SELECT count(*) FROM query_publications WHERE job_id=?", first.ProjectionJobID).Scan(&publications)
	_ = store.Catalog.SQL().QueryRowContext(ctx, "SELECT count(*) FROM overlay_projection_revisions WHERE projection_job_id=? AND status='superseded'", first.ProjectionJobID).Scan(&superseded)
	_ = store.Catalog.SQL().QueryRowContext(ctx, "SELECT count(*) FROM overlay_projection_revisions WHERE projection_job_id=? AND status='staging'", first.ProjectionJobID).Scan(&staging)
	if publications != 1 || superseded != 1 || staging != 0 {
		t.Fatalf("coalesce 结果 publications=%d superseded=%d staging=%d", publications, superseded, staging)
	}
}

func TestProjectionFailurePreservesOldPublicationAndCanRetry(t *testing.T) {
	ctx, _, service, catalogStore, queryService := newFixture(t)
	created, err := service.Put(ctx, testWorkID, "owner", Input{TitleOverride: "失败版本"})
	if err != nil {
		t.Fatal(err)
	}
	service.faultInjector = func(stage string) error {
		if stage == "before_publish" {
			return errors.New("injected")
		}
		return nil
	}
	if err := service.Execute(ctx, created.ProjectionJobID); err == nil {
		t.Fatal("注入故障未失败")
	}
	publication, _ := catalogStore.Current(ctx)
	state, _ := service.Get(ctx, testWorkID)
	old, _ := queryService.Search(ctx, galleryquery.Request{Limit: 20, AuthorizationScope: "owner"})
	if publication.ID != testPubID || state.ProjectionStatus != "failed" || len(old.Items) != 1 || old.Items[0].Title != "源标题" {
		t.Fatalf("失败污染了旧 publication: pub=%+v state=%+v query=%+v", publication, state, old)
	}
	service.faultInjector = nil
	retry, err := service.Put(ctx, testWorkID, "owner", Input{TitleOverride: "失败版本"})
	if err != nil || !retry.StartJob || retry.ProjectionJobID == created.ProjectionJobID {
		t.Fatalf("同事实重试未创建新 Job: %+v %v", retry, err)
	}
	if err := service.Execute(ctx, retry.ProjectionJobID); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileQueuedAndPublicationControlGap(t *testing.T) {
	ctx, store, service, _, _ := newFixture(t)
	created, err := service.Put(ctx, testWorkID, "owner", Input{TitleOverride: "已切 publication"})
	if err != nil {
		t.Fatal(err)
	}
	service.faultInjector = func(stage string) error {
		if stage == "after_publication" {
			return errors.New("simulate process death")
		}
		return nil
	}
	if err := service.Execute(ctx, created.ProjectionJobID); err == nil {
		t.Fatal("publication/control gap 未注入")
	}
	job, _ := service.jobs.Get(ctx, created.ProjectionJobID)
	state, _ := service.Get(ctx, testWorkID)
	if job.Status != jobs.StatusPublishing || state.ProjectionStatus != "pending" {
		t.Fatalf("故障点状态不符合恢复前提: job=%+v state=%+v", job, state)
	}
	restarted, err := New(ctx, store.Control.SQL(), service.jobs, service.catalog, service.clock, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	restarted.Wait()
	job, _ = restarted.jobs.Get(ctx, created.ProjectionJobID)
	state, _ = restarted.Get(ctx, testWorkID)
	if job.Status != jobs.StatusCompleted || state.ProjectionStatus != "published" {
		t.Fatalf("publication/control gap 未对账: job=%+v state=%+v", job, state)
	}
	var count int
	_ = store.Catalog.SQL().QueryRowContext(ctx, "SELECT count(*) FROM query_publications WHERE job_id=?", created.ProjectionJobID).Scan(&count)
	if count != 1 {
		t.Fatalf("恢复产生重复 publication: %d", count)
	}

	queued, err := restarted.Put(ctx, testWorkID, "owner", Input{TitleOverride: "queued 恢复"})
	if err != nil {
		t.Fatal(err)
	}
	secondRestart, _ := New(ctx, store.Control.SQL(), restarted.jobs, restarted.catalog, restarted.clock, nil)
	if err := secondRestart.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	secondRestart.Wait()
	// 无 publication 的 queued Job 只由中央 Recovery Service 的 ListRunnable/Submit 领取，
	// Reconcile 本身不再自行 Start，避免与中央循环对同一 Job 形成竞争领取窗口（见
	// killpoints_test.go 的 overlay_fact_preprojection 场景）；这里直接模拟中央循环调用
	// Execute 完成领取。
	queuedJob, _ := secondRestart.jobs.Get(ctx, queued.ProjectionJobID)
	if queuedJob.Status != jobs.StatusQueued {
		t.Fatalf("queued Job 不应被 Reconcile 自行领取: %+v", queuedJob)
	}
	if err := secondRestart.Execute(ctx, queued.ProjectionJobID); err != nil {
		t.Fatal(err)
	}
	queuedJob, _ = secondRestart.jobs.Get(ctx, queued.ProjectionJobID)
	if queuedJob.Status != jobs.StatusCompleted {
		t.Fatalf("queued Job 未在中央领取后完成: %+v", queuedJob)
	}
}

func newFixture(t *testing.T) (context.Context, *storage.Store, *Service, *catalog.Store, *galleryquery.Service) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 7, 16, 4, 0, 0, 0, time.UTC)
	fixed := clock.Fixed{Time: now}
	dirs := appdirs.UnderRoot(t.TempDir())
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.Control.SQL().ExecContext(ctx, `INSERT INTO canonical_works
(work_id, title, created_at) VALUES (?, '源标题', 1)`, testWorkID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Control.SQL().ExecContext(ctx, `INSERT INTO canonical_media
(media_id, work_id, role, ordinal, created_at) VALUES (?, ?, 'content', 0, 1), (?, ?, 'content', 1, 1)`,
		testMedia1ID, testWorkID, testMedia2ID, testWorkID); err != nil {
		t.Fatal(err)
	}
	document := querytext.BuildDocument("源标题", "作者", []string{"source"}, []string{"01.jpg", "02.jpg"})
	tags, _ := json.Marshal([]string{"source"})
	files, _ := json.Marshal([]string{"01.jpg", "02.jpg"})
	tx, err := store.Catalog.SQL().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO catalog_revisions VALUES (?, ?, 'src_test', 'published', 1, 2)`, []any{testCatalogID, testScanJobID}},
		{`INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, published_at)
VALUES (?, ?, 0, 'published', 1, 2)`, []any{testOverlayID, testCatalogID}},
		{`INSERT INTO query_publications VALUES (?, ?, ?, ?, 0, 2)`, []any{testPubID, testCatalogID, testOverlayID, testScanJobID}},
		{`INSERT INTO active_query_publication VALUES (1, ?)`, []any{testPubID}},
		{`INSERT INTO source_works
(catalog_revision_id, source_id, source_key, title, creator, tags_json, filenames_text)
VALUES (?, 'src_test', 'work-key', '源标题', '作者', ?, ?)`, []any{testCatalogID, string(tags), string(files)}},
		{`INSERT INTO work_projections
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id, title, creator,
 tags_json, filenames_text, normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text, sort_title_key, hidden)
VALUES (?, ?, ?, 'src_test', 'work-key', 'lib_test', '源标题', '作者', ?, ?, ?, ?, ?, ?, 0)`, []any{
			testCatalogID, testOverlayID, testWorkID, string(tags), string(files), document.NormalizedOriginal,
			document.CJKTokens, document.LatinTokens, document.SortTitleKey,
		}},
		{`INSERT INTO work_search VALUES (?, ?, ?, ?, ?, ?)`, []any{testCatalogID, testOverlayID, testWorkID, document.NormalizedOriginal, document.CJKTokens, document.LatinTokens}},
		{`INSERT INTO media_projections
(catalog_revision_id, overlay_revision_id, media_id, work_id, source_id, source_key, relative_path,
 media_kind, mime_type, size_bytes, algorithm, digest, location_status, ordinal, hidden, base_ordinal)
VALUES (?, ?, ?, ?, 'src_test', 'media-1', 'work/01.jpg', 'image', 'image/jpeg', 1, 'sha256-v1', ?, 'present', 0, 0, 0)`,
			[]any{testCatalogID, testOverlayID, testMedia1ID, testWorkID, fmt.Sprintf("%064d", 1)}},
		{`INSERT INTO media_projections
(catalog_revision_id, overlay_revision_id, media_id, work_id, source_id, source_key, relative_path,
 media_kind, mime_type, size_bytes, algorithm, digest, location_status, ordinal, hidden, base_ordinal)
VALUES (?, ?, ?, ?, 'src_test', 'media-2', 'work/02.jpg', 'image', 'image/jpeg', 1, 'sha256-v1', ?, 'present', 1, 0, 1)`,
			[]any{testCatalogID, testOverlayID, testMedia2ID, testWorkID, fmt.Sprintf("%064d", 2)}},
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	jobStore, err := jobs.NewStore(store.Control.SQL(), fixed, identity.NewGenerator(fixed))
	if err != nil {
		t.Fatal(err)
	}
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), fixed, identity.NewGenerator(fixed))
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(ctx, store.Control.SQL(), jobStore, catalogStore, fixed, nil)
	if err != nil {
		t.Fatal(err)
	}
	queryService, err := galleryquery.NewService(ctx, store.Control.SQL(), store.Catalog.SQL(), fixed, nil)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, store, service, catalogStore, queryService
}

// TestOverlayDependencySetMatchesQueryAffectingBehavior 锁定 OverlayDependencySet
// 这份服务端权威分类表与实际排队判据一致：Snapshot 字段单独变化必须触发
// queryAffectingFieldsChanged，Live 字段单独变化绝不触发。字段名新增/移除时本测试
// 会因未覆盖分支而失败，提醒同步更新分类表与本测试。
func TestOverlayDependencySetMatchesQueryAffectingBehavior(t *testing.T) {
	if len(OverlayDependencySet) != 6 {
		t.Fatalf("OverlayDependencySet 字段数 = %d，注册表变化后请同步更新本测试", len(OverlayDependencySet))
	}
	base := State{TitleOverride: "t", ManualTags: []string{"a"}, Hidden: false, CustomCoverMediaID: "med_x", Favorite: false, Progress: 0}
	sameInput := Input{TitleOverride: base.TitleOverride, ManualTags: base.ManualTags, Hidden: base.Hidden, CustomCoverMediaID: base.CustomCoverMediaID, Favorite: base.Favorite, Progress: base.Progress}
	if queryAffectingFieldsChanged(base, sameInput) {
		t.Fatal("无变化不应触发 query-affecting")
	}
	for field, class := range OverlayDependencySet {
		mutated := sameInput
		switch field {
		case "titleOverride":
			mutated.TitleOverride = "changed"
		case "manualTags":
			mutated.ManualTags = []string{"b"}
		case "hidden":
			mutated.Hidden = true
		case "customCoverMediaId":
			mutated.CustomCoverMediaID = "med_y"
		case "favorite":
			mutated.Favorite = true
		case "progress":
			mutated.Progress = 0.5
		default:
			t.Fatalf("测试未覆盖新字段 %q，请补充分支", field)
		}
		got := queryAffectingFieldsChanged(base, mutated)
		want := class == DependencySnapshot
		if got != want {
			t.Fatalf("字段 %s（%s）单独变化 queryAffecting=%v，want %v", field, class, got, want)
		}
	}
}
