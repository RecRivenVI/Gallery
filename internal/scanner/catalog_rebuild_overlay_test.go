package scanner_test

import (
	"context"
	"testing"

	"github.com/RecRivenVI/gallery/internal/overlay"
)

// TestFavoriteProgressHiddenSurviveCatalogRebuild 覆盖 Documents 十的要求：Overlay 在
// Catalog 删除重建后必须保持正确。control.db 才是 Favorite/Progress/Hidden 的事实权威
// （work_overlays 表），Catalog 只是可重建的查询投影；本测试验证 catalog.db 被删除并
// 从零重新扫描后，新的 work_projections 快照仍正确反映重建前已经设置好的 Favorite/
// Progress/Hidden 覆盖值——它们经由 ApplyCatalogCandidateOverlays 从 control.db 重新
// 应用，不依赖旧 Catalog 的任何残留状态。
func TestFavoriteProgressHiddenSurviveCatalogRebuild(t *testing.T) {
	ctx := context.Background()
	f := newRebuildFixture(t)
	f.writeWork(t, "work-one", map[string][]byte{"media.bin": []byte("catalog rebuild overlay fixture content")})

	first, err := f.scanner.CreateScanWithProfile(ctx, f.source.ID, "owner", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.scanner.Execute(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := f.scanner.CreateScanWithProfile(ctx, f.source.ID, "owner", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.scanner.Execute(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	_, works, err := f.catalog.ListWorks(ctx)
	if err != nil || len(works) != 1 {
		t.Fatalf("扫描后应恰好一个 Work: %+v %v", works, err)
	}
	workID := works[0].ID

	overlayService, err := overlay.New(ctx, f.store.Control.SQL(), f.jobs, f.catalog, f.fixedClock, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := overlayService.Put(ctx, workID, "owner", overlay.Input{Favorite: true, Progress: 0.6, Hidden: true, TitleOverride: "Rebuild Override"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.StartJob {
		t.Fatalf("Favorite/Progress/Hidden/TitleOverride 写入应触发投影 Job: %+v", result)
	}
	if err := overlayService.Execute(ctx, result.ProjectionJobID); err != nil {
		t.Fatal(err)
	}

	assertProjectedOverlay := func(label string) {
		t.Helper()
		var favorite, hidden int
		var progress float64
		var title string
		if err := f.store.Catalog.SQL().QueryRowContext(ctx, `SELECT favorite, progress, hidden, title
FROM work_projections wp JOIN active_query_publication a ON a.singleton=1
JOIN query_publications q ON q.query_publication_id=a.query_publication_id
WHERE wp.catalog_revision_id=q.catalog_revision_id AND wp.overlay_revision_id=q.overlay_revision_id AND wp.work_id=?`,
			workID).Scan(&favorite, &progress, &hidden, &title); err != nil {
			t.Fatalf("%s: 查询投影失败: %v", label, err)
		}
		if favorite != 1 || progress != 0.6 || hidden != 1 || title != "Rebuild Override" {
			t.Fatalf("%s: Overlay 投影不正确: favorite=%d progress=%v hidden=%d title=%q", label, favorite, progress, hidden, title)
		}
	}
	assertProjectedOverlay("重建前")

	f.rebuildCatalog(t)

	rebuiltScan, err := f.scanner.CreateScanWithProfile(ctx, f.source.ID, "owner", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if profile := f.persistedScanProfile(t, rebuiltScan); profile != "incremental" {
		t.Fatalf("Catalog 重建后仍有持久领域历史，默认应选择 incremental，实际=%q", profile)
	}
	if err := f.scanner.Execute(ctx, rebuiltScan.ID); err != nil {
		t.Fatal(err)
	}
	assertProjectedOverlay("Catalog 重建后")
}
