package overlay

import (
	"context"
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

// newEmptyOverlayFixture 搭建一个干净、从未发布过任何 Catalog publication 的 overlay
// Service，模拟全新安装或从未成功扫描过任何 Source 的状态。
func newEmptyOverlayFixture(t *testing.T) (context.Context, *storage.Store, *Service) {
	t.Helper()
	ctx := context.Background()
	fixed := clock.Fixed{Time: time.Date(2026, 7, 20, 4, 0, 0, 0, time.UTC)}
	dirs := appdirs.UnderRoot(t.TempDir())
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
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
	return ctx, store, service
}

// TestTriggerReprojectionBackfillsQueryDependencyFieldsForLegacyRevision 覆盖阶段 4
// 收尾的核心缺口：catalog migration 00010（v9→v10）只能给已有 work_projections 行的
// favorite/progress/search_*_norm 新列填入 ALTER TABLE 的静态默认值（0 或空字符串），不会自动
// 重新计算。newFixture 构造的 revision 正好模拟这个"刚升级、尚未被任何 Overlay 写入
// 触碰过"的状态：work_projections.favorite/progress/search_*_norm 全部是表默认值，
// 而 control.db 的 work_overlays 已经真实存在 favorite=true 的既有用户事实（模拟迁移
// 前就已经收藏过这个作品）。TriggerReprojection 必须能在不依赖任何用户主动触碰某个
// Overlay 字段的前提下，把这些字段正确回填到当前 active revision，否则升级后重启的
// 服务会用默认零值静默丢弃这次过滤，返回错误结果。
func TestTriggerReprojectionBackfillsQueryDependencyFieldsForLegacyRevision(t *testing.T) {
	ctx, store, service, catalogStore, queryService := newFixture(t)

	// 模拟迁移前已经存在、且早已完成投影的 Overlay 事实：control.db 的 work_overlays
	// 直接写入 favorite=1，但 catalog.db 对应 revision 的 work_projections.favorite 仍是
	// migration 00010 ALTER TABLE 的默认值 0——这正是升级后尚未回填的真实状态。
	if _, err := store.Control.SQL().ExecContext(ctx, `INSERT INTO work_overlays
(work_id, title_override, manual_tags_json, hidden, custom_cover_media_id, favorite, progress,
 fact_watermark, query_watermark, projected_watermark, projection_status, projection_job_id,
 published_query_publication_id, issue_code, updated_at)
VALUES (?, '', '[]', 0, NULL, 1, 0.7, 1, 1, 1, 'published', NULL, ?, NULL, 1)`,
		testWorkID, testPubID); err != nil {
		t.Fatal(err)
	}

	favoriteFilter := `{"field":"overlay.favorite","op":"eq","value":true}`
	before, err := queryService.Search(ctx, galleryquery.Request{Filter: favoriteFilter, Limit: 20, AuthorizationScope: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Items) != 0 {
		t.Fatalf("回填前：work_projections.favorite 仍是迁移默认值 0，favorite=true 过滤本不应命中任何作品，实际命中 %+v", before.Items)
	}

	job, created, err := service.TriggerReprojection(ctx, "system:query-dependency-backfill")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("存在 active publication 时应该真实排队一个投影 Job")
	}
	if err := service.Execute(ctx, job.ID); err != nil {
		t.Fatal(err)
	}

	after, err := queryService.Search(ctx, galleryquery.Request{Filter: favoriteFilter, Limit: 20, AuthorizationScope: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Items) != 1 || after.Items[0].ID != testWorkID || !after.Items[0].Favorite || after.Items[0].Progress != 0.7 {
		t.Fatalf("回填后应该正确反映 control.db 的既有 favorite/progress 事实: %+v", after.Items)
	}

	current, err := catalogStore.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var creatorNorm, tagsNorm, filenamesNorm, titleNorm string
	if err := store.Catalog.SQL().QueryRowContext(ctx, `SELECT search_title_norm, search_creator_norm, search_tags_norm, search_filenames_norm
FROM work_projections WHERE catalog_revision_id=? AND overlay_revision_id=? AND work_id=?`,
		current.CatalogRevisionID, current.OverlayRevisionID, testWorkID).Scan(&titleNorm, &creatorNorm, &tagsNorm, &filenamesNorm); err != nil {
		t.Fatal(err)
	}
	wantDocument := querytext.BuildDocument("源标题", "作者", []string{"source"}, []string{"01.jpg", "02.jpg"})
	if titleNorm != wantDocument.TitleNorm || creatorNorm != wantDocument.CreatorNorm ||
		tagsNorm != wantDocument.TagsNorm || filenamesNorm != wantDocument.FilenamesNorm {
		t.Fatalf("search_*_norm 应该从该 revision 已有的 title/creator/tags/filenames 权威重新计算，而不是继续留空: title=%q creator=%q tags=%q filenames=%q",
			titleNorm, creatorNorm, tagsNorm, filenamesNorm)
	}
}

// TestTriggerReprojectionNoopWithoutActivePublication 覆盖空 Catalog（全新安装，或
// 从未成功扫描过任何 Source）的回填路径：不应该报错，也不应该凭空构造一个不存在的
// publication；后续真正扫描发布数据时天然使用已经包含新列的写入路径，不需要任何回填。
func TestTriggerReprojectionNoopWithoutActivePublication(t *testing.T) {
	ctx, _, service := newEmptyOverlayFixture(t)
	job, created, err := service.TriggerReprojection(ctx, "system:query-dependency-backfill")
	if err != nil {
		t.Fatal(err)
	}
	if created || job.ID != "" {
		t.Fatalf("没有 active publication 时不应该排队 Job: created=%v job=%+v", created, job)
	}
}
