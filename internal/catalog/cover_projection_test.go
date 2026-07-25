package catalog_test

import (
	"context"
	"errors"
	"testing"

	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/storage"
)

func TestCoverProjectionUsesExplicitSnapshotField(t *testing.T) {
	ctx := context.Background()
	catalogStore, store := newCandidateTestStore(t)
	candidate, err := catalogStore.BeginCandidate(ctx, "job-cover-base", "source-a", 1)
	if err != nil {
		t.Fatal(err)
	}
	works := []catalog.WorkFact{
		{
			SourceID: "source-a", LibraryID: "library-a", SourceKey: "work-a", Title: "A", WorkID: "work-a",
			RuleCoverMediaSourceKey: "work-a/02.jpg", RuleCoverMediaID: "media-a2",
		},
		{
			SourceID: "source-a", LibraryID: "library-a", SourceKey: "work-b", Title: "B", WorkID: "work-b",
			RuleCoverMediaSourceKey: "work-b/01.jpg", RuleCoverMediaID: "media-b1",
		},
	}
	media := []catalog.MediaFact{
		coverMediaFact("source-a", "work-a", "work-a/01.jpg", "media-a1", 0, candidateDigestA),
		coverMediaFact("source-a", "work-a", "work-a/02.jpg", "media-a2", 1, candidateDigestB),
		coverMediaFact("source-a", "work-b", "work-b/01.jpg", "media-b1", 0,
			"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"),
	}
	if err := catalogStore.Stage(ctx, candidate, works, media); err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ValidateCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	base := publishCandidate(t, catalogStore, candidate)

	_, work, err := catalogStore.GetWorkAt(ctx, base.ID, "work-a")
	if err != nil || work.CoverMediaID != "media-a2" || work.Favorite || work.Progress != 0 {
		t.Fatalf("规则选择的非首媒体未成为显式封面: %+v %v", work, err)
	}
	assertNonnegativeMediaOrder(t, store, base.CatalogRevisionID, base.OverlayRevisionID)

	custom, err := catalogStore.BeginOverlayCandidate(ctx, "job-cover-custom", base.CatalogRevisionID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ApplyOverlayFacts(ctx, custom, map[string]catalog.OverlayFact{
		"work-a": {CustomCoverMediaID: "media-a1", Favorite: true, Progress: 0.75},
	}); err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ValidateOverlayCandidate(ctx, custom); err != nil {
		t.Fatal(err)
	}
	customPublication, err := catalogStore.PublishOverlay(ctx, custom)
	if err != nil {
		t.Fatal(err)
	}
	_, work, err = catalogStore.GetWorkAt(ctx, customPublication.ID, "work-a")
	if err != nil || work.CoverMediaID != "media-a1" || !work.Favorite || work.Progress != 0.75 {
		t.Fatalf("新 publication 未返回 CustomCover/Favorite/Progress 快照: %+v %v", work, err)
	}
	_, historical, err := catalogStore.GetWorkAt(ctx, base.ID, "work-a")
	if err != nil || historical.CoverMediaID != "media-a2" || historical.Favorite || historical.Progress != 0 {
		t.Fatalf("旧 query publication 的封面/Favorite/Progress 快照被污染: %+v %v", historical, err)
	}
	assertNonnegativeMediaOrder(t, store, custom.CatalogRevisionID, custom.OverlayRevisionID)

	cleared, err := catalogStore.BeginOverlayCandidate(ctx, "job-cover-clear", base.CatalogRevisionID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ApplyOverlayFacts(ctx, cleared, nil); err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ValidateOverlayCandidate(ctx, cleared); err != nil {
		t.Fatal(err)
	}
	clearedPublication, err := catalogStore.PublishOverlay(ctx, cleared)
	if err != nil {
		t.Fatal(err)
	}
	_, work, err = catalogStore.GetWorkAt(ctx, clearedPublication.ID, "work-a")
	if err != nil || work.CoverMediaID != "media-a2" || work.Favorite || work.Progress != 0 {
		t.Fatalf("清除 Overlay 后未回退规则封面和默认用户状态: %+v %v", work, err)
	}

	invalid, err := catalogStore.BeginOverlayCandidate(ctx, "job-cover-invalid", base.CatalogRevisionID, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ApplyOverlayFacts(ctx, invalid, map[string]catalog.OverlayFact{
		"work-a": {CustomCoverMediaID: "media-missing"},
	}); err != nil {
		t.Fatal(err)
	}
	var projected string
	if err := store.Catalog.SQL().QueryRowContext(ctx, `SELECT cover_media_id FROM work_projections
WHERE catalog_revision_id=? AND overlay_revision_id=? AND work_id='work-a'`,
		invalid.CatalogRevisionID, invalid.OverlayRevisionID).Scan(&projected); err != nil {
		t.Fatal(err)
	}
	if projected != "media-a2" {
		t.Fatalf("已删除的 CustomCover 未安全回退规则封面: %q", projected)
	}
	if _, err := store.Catalog.SQL().ExecContext(ctx, `UPDATE work_projections SET cover_media_id='media-b1'
WHERE catalog_revision_id=? AND overlay_revision_id=? AND work_id='work-a'`,
		invalid.CatalogRevisionID, invalid.OverlayRevisionID); err != nil {
		t.Fatal(err)
	}
	err = catalogStore.ValidateOverlayCandidate(ctx, invalid)
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeCatalogCandidateInvalid {
		t.Fatalf("跨 Work 有效封面未被候选门禁拒绝: %v", err)
	}
}

func TestCatalogCloneResetsThenReplaysEffectiveCover(t *testing.T) {
	ctx := context.Background()
	catalogStore, store := newCandidateTestStore(t)
	baseCandidate, err := catalogStore.BeginCandidate(ctx, "job-clone-base", "source-a", 1)
	if err != nil {
		t.Fatal(err)
	}
	baseWorks := []catalog.WorkFact{{
		SourceID: "source-a", LibraryID: "library-a", SourceKey: "work-a", Title: "A", WorkID: "work-a",
		RuleCoverMediaSourceKey: "work-a/02.jpg", RuleCoverMediaID: "media-a2",
	}}
	baseMedia := []catalog.MediaFact{
		coverMediaFact("source-a", "work-a", "work-a/01.jpg", "media-a1", 0, candidateDigestA),
		coverMediaFact("source-a", "work-a", "work-a/02.jpg", "media-a2", 1, candidateDigestB),
	}
	if err := catalogStore.Stage(ctx, baseCandidate, baseWorks, baseMedia); err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ValidateCandidate(ctx, baseCandidate); err != nil {
		t.Fatal(err)
	}
	base := publishCandidate(t, catalogStore, baseCandidate)
	custom, err := catalogStore.BeginOverlayCandidate(ctx, "job-clone-custom", base.CatalogRevisionID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ApplyOverlayFacts(ctx, custom, map[string]catalog.OverlayFact{
		"work-a": {CustomCoverMediaID: "media-a1"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ValidateOverlayCandidate(ctx, custom); err != nil {
		t.Fatal(err)
	}
	if _, err := catalogStore.PublishOverlay(ctx, custom); err != nil {
		t.Fatal(err)
	}

	cloned, err := catalogStore.BeginCandidate(ctx, "job-clone-other-source", "source-b", 3)
	if err != nil {
		t.Fatal(err)
	}
	var ruleCover, effectiveCover string
	if err := store.Catalog.SQL().QueryRowContext(ctx, `SELECT rule_cover_media_id, cover_media_id
FROM work_projections WHERE catalog_revision_id=? AND overlay_revision_id=? AND work_id='work-a'`,
		cloned.CatalogRevisionID, cloned.OverlayRevisionID).Scan(&ruleCover, &effectiveCover); err != nil {
		t.Fatal(err)
	}
	if ruleCover != "media-a2" || effectiveCover != "media-a2" {
		t.Fatalf("克隆未先恢复 Source-derived 规则封面: rule=%q effective=%q", ruleCover, effectiveCover)
	}
	assertNonnegativeMediaOrder(t, store, cloned.CatalogRevisionID, cloned.OverlayRevisionID)

	otherWorks := []catalog.WorkFact{{
		SourceID: "source-b", LibraryID: "library-b", SourceKey: "work-b", Title: "B", WorkID: "work-b",
		RuleCoverMediaSourceKey: "work-b/01.jpg", RuleCoverMediaID: "media-b1",
	}}
	otherMedia := []catalog.MediaFact{
		coverMediaFact("source-b", "work-b", "work-b/01.jpg", "media-b1", 0,
			"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"),
	}
	if err := catalogStore.Stage(ctx, cloned, otherWorks, otherMedia); err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ApplyCatalogCandidateOverlays(ctx, cloned, map[string]catalog.OverlayFact{
		"work-a": {CustomCoverMediaID: "media-a1"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ValidateCandidate(ctx, cloned); err != nil {
		t.Fatal(err)
	}
	if err := store.Catalog.SQL().QueryRowContext(ctx, `SELECT cover_media_id FROM work_projections
WHERE catalog_revision_id=? AND overlay_revision_id=? AND work_id='work-a'`,
		cloned.CatalogRevisionID, cloned.OverlayRevisionID).Scan(&effectiveCover); err != nil {
		t.Fatal(err)
	}
	if effectiveCover != "media-a1" {
		t.Fatalf("克隆后的 Overlay 全量重放未恢复 CustomCover: %q", effectiveCover)
	}
	assertNonnegativeMediaOrder(t, store, cloned.CatalogRevisionID, cloned.OverlayRevisionID)
}

func TestCoverValidationRejectsNegativeMediaOrdinal(t *testing.T) {
	ctx := context.Background()
	catalogStore, _ := newCandidateTestStore(t)
	candidate, err := catalogStore.BeginCandidate(ctx, "job-negative-ordinal", "source-a", 1)
	if err != nil {
		t.Fatal(err)
	}
	works := []catalog.WorkFact{{
		SourceID: "source-a", LibraryID: "library-a", SourceKey: "work-a", Title: "A", WorkID: "work-a",
		RuleCoverMediaSourceKey: "work-a/01.jpg", RuleCoverMediaID: "media-a1",
	}}
	media := []catalog.MediaFact{
		coverMediaFact("source-a", "work-a", "work-a/01.jpg", "media-a1", -1, candidateDigestA),
	}
	if err := catalogStore.Stage(ctx, candidate, works, media); err != nil {
		t.Fatal(err)
	}
	err = catalogStore.ValidateCandidate(ctx, candidate)
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeCatalogCandidateInvalid {
		t.Fatalf("负 ordinal/base_ordinal 未被候选门禁拒绝: %v", err)
	}
}

func coverMediaFact(sourceID, workID, sourceKey, mediaID string, ordinal int, digest string) catalog.MediaFact {
	return catalog.MediaFact{
		SourceID: sourceID, SourceKey: sourceKey, WorkSourceKey: workID, RuleKey: sourceKey,
		RelativePath: sourceKey, Kind: "image", MIME: "image/jpeg", Size: 1,
		Algorithm: "sha256-v1", Digest: digest, LocationKey: "location-" + mediaID,
		MediaID: mediaID, WorkID: workID, Ordinal: ordinal,
	}
}

func assertNonnegativeMediaOrder(t *testing.T, store *storage.Store, catalogRevisionID, overlayRevisionID string) {
	t.Helper()
	var negatives int
	if err := store.Catalog.SQL().QueryRow(`SELECT count(*) FROM media_projections
WHERE catalog_revision_id=? AND overlay_revision_id=? AND (ordinal<0 OR base_ordinal<0)`,
		catalogRevisionID, overlayRevisionID).Scan(&negatives); err != nil {
		t.Fatal(err)
	}
	if negatives != 0 {
		t.Fatalf("封面投影仍借用负 ordinal: %d", negatives)
	}
}
