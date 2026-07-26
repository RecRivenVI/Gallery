package catalog_test

import (
	"context"
	"testing"

	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/storage"
)

// TestBadgeAndRuleHiddenSurviveOverlayRepublish 锁定规则派生展示事实与用户 Overlay 的
// 分离：角标与规则隐藏随 publication 冻结、随 Overlay 重发布原样继承，而用户 Overlay 的
// 隐藏与收藏独立存在，二者互不覆盖。
//
// 这两条性质是「用户事实不可被重扫覆盖」与「规则是 Source 差异的唯一解释入口」在展示层的
// 交汇点：若把规则隐藏并进 Overlay 的 hidden 列，重扫会抹掉用户选择；若让客户端自行推导
// 角标条件，规则就不再是唯一解释入口。
func TestBadgeAndRuleHiddenSurviveOverlayRepublish(t *testing.T) {
	ctx := context.Background()
	catalogStore, store := newCandidateTestStore(t)
	candidate, err := catalogStore.BeginCandidate(ctx, "job-badge-base", "source-a", 1)
	if err != nil {
		t.Fatal(err)
	}
	badges := []domain.Badge{
		{ID: "r18", Order: 1, Position: "cover_top_left", Label: "R-18", Color: "#ffffff", Background: "#773333"},
		{ID: "image", Order: 2, Position: "cover_top_right", Label: "图片", ColorLight: "#17181a"},
	}
	works := []catalog.WorkFact{{
		SourceID: "source-a", LibraryID: "library-a", SourceKey: "work-a", Title: "A", WorkID: "work-a",
		RuleCoverMediaSourceKey: "work-a/01.jpg", RuleCoverMediaID: "media-a1", Badges: badges,
	}, {
		SourceID: "source-a", LibraryID: "library-a", SourceKey: "work-b", Title: "B", WorkID: "work-b",
		RuleCoverMediaSourceKey: "work-b/01.jpg", RuleCoverMediaID: "media-b1",
	}}
	visible := coverMediaFact("source-a", "work-a", "work-a/01.jpg", "media-a1", 0, candidateDigestA)
	concealed := coverMediaFact("source-a", "work-a", "work-a/cover.jpg", "media-a2", 1, candidateDigestB)
	concealed.RuleHidden = true
	plain := coverMediaFact("source-a", "work-b", "work-b/01.jpg", "media-b1", 0,
		"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
	if err := catalogStore.Stage(ctx, candidate, works, []catalog.MediaFact{visible, concealed, plain}); err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ValidateCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	base := publishCandidate(t, catalogStore, candidate)

	_, work, err := catalogStore.GetWorkAt(ctx, base.ID, "work-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(work.Badges) != 2 || work.Badges[0].ID != "r18" || work.Badges[1].ID != "image" {
		t.Fatalf("角标未随 publication 冻结或顺序丢失: %+v", work.Badges)
	}
	if work.Badges[0].Background != "#773333" || work.Badges[1].ColorLight != "#17181a" {
		t.Fatalf("角标样式未随快照下发: %+v", work.Badges)
	}
	_, emptyBadges, err := catalogStore.GetWorkAt(ctx, base.ID, "work-b")
	if err != nil || len(emptyBadges.Badges) != 0 {
		t.Fatalf("无角标作品应返回空序列: %+v %v", emptyBadges.Badges, err)
	}
	assertRuleHidden(t, store, base, map[string]bool{"media-a1": false, "media-a2": true, "media-b1": false})

	// 用户 Overlay 重发布：规则派生事实必须原样继承，用户事实独立生效。
	overlay, err := catalogStore.BeginOverlayCandidate(ctx, "job-badge-overlay", base.CatalogRevisionID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ApplyOverlayFacts(ctx, overlay, map[string]catalog.OverlayFact{
		"work-a": {Hidden: true, Favorite: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ValidateOverlayCandidate(ctx, overlay); err != nil {
		t.Fatal(err)
	}
	republished, err := catalogStore.PublishOverlay(ctx, overlay)
	if err != nil {
		t.Fatal(err)
	}
	_, afterOverlay, err := catalogStore.GetWorkAt(ctx, republished.ID, "work-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(afterOverlay.Badges) != 2 || afterOverlay.Badges[0].ID != "r18" {
		t.Fatalf("Overlay 重发布丢失了规则派生角标: %+v", afterOverlay.Badges)
	}
	if !afterOverlay.Favorite {
		t.Fatal("用户 Overlay 事实未生效")
	}
	assertRuleHidden(t, store, republished, map[string]bool{"media-a1": false, "media-a2": true, "media-b1": false})
}

// assertRuleHidden 直接读投影列，确认规则隐藏与 Overlay 的 hidden 是两列独立事实：
// rule_hidden 按规则结论，hidden 只由用户 Overlay 决定，规则隐藏不得渗进后者。
func assertRuleHidden(t *testing.T, store *storage.Store, publication catalog.Publication, want map[string]bool) {
	t.Helper()
	rows, err := store.Catalog.SQL().Query(`SELECT media_id, rule_hidden, hidden FROM media_projections
WHERE catalog_revision_id=? AND overlay_revision_id=?`, publication.CatalogRevisionID, publication.OverlayRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var mediaID string
		var ruleHidden, overlayHidden int
		if err := rows.Scan(&mediaID, &ruleHidden, &overlayHidden); err != nil {
			t.Fatal(err)
		}
		seen[mediaID] = ruleHidden != 0
		if overlayHidden != 0 {
			t.Fatalf("规则隐藏渗入了用户 Overlay 的 hidden 列: media=%s", mediaID)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for mediaID, expected := range want {
		if seen[mediaID] != expected {
			t.Fatalf("媒体 %s 的 rule_hidden = %v want %v（全部：%+v）", mediaID, seen[mediaID], expected, seen)
		}
	}
}
