package catalog_test

import (
	"context"
	"testing"

	"github.com/RecRivenVI/gallery/internal/catalog"
)

// aggregateWork 构造一个带创作者、封面与发布时间的作品事实。
func aggregateWork(sourceID, libraryID, workID, creatorID, creatorName string, publishedAtNanos int64) catalog.WorkFact {
	return catalog.WorkFact{
		SourceID: sourceID, LibraryID: libraryID, SourceKey: workID, Title: workID, WorkID: workID,
		// SourceCreator 是**逐作品**的 occurrence，因此 source_key 必须按作品唯一；
		// 共享的是 CreatorID 这一 Canonical 身份，不是 occurrence 键。
		Creator: creatorName, CreatorID: creatorID, CreatorSourceKey: creatorID + "@" + workID,
		CreatorProviderID: "", CreatorExternalID: creatorID, SourceCreatorName: creatorName,
		RuleCoverMediaSourceKey: workID + "/01.jpg", RuleCoverMediaID: workID + "-m1",
		PublishedAtNanos: publishedAtNanos, PublishedAtRaw: "raw", PublishedAtParser: "gallery-work-date-v1",
	}
}

// TestAggregateCoversCascadeThroughCreatorSourceAndLibrary 覆盖三级聚合的核心语义：
// 作者取其最新作品、平台取其最新作者、资料库取其最新平台，逐级复用下层已算好的代表时刻。
func TestAggregateCoversCascadeThroughCreatorSourceAndLibrary(t *testing.T) {
	ctx := context.Background()
	catalogStore, _ := newCandidateTestStore(t)
	candidate, err := catalogStore.BeginCandidate(ctx, "job-aggregate", "source-a", 1)
	if err != nil {
		t.Fatal(err)
	}
	// 同一作者两件作品：较新的一件应当代表该作者。
	works := []catalog.WorkFact{
		aggregateWork("source-a", "library-a", "work-old", "creator-1", "创作者一", 1000),
		aggregateWork("source-a", "library-a", "work-new", "creator-1", "创作者一", 5000),
		// 另一位作者，时间更早：Source 级应当选中较新的 creator-1。
		aggregateWork("source-a", "library-a", "work-other", "creator-2", "创作者二", 2000),
	}
	media := []catalog.MediaFact{
		coverMediaFact("source-a", "work-old", "work-old/01.jpg", "work-old-m1", 0, candidateDigestA),
		coverMediaFact("source-a", "work-new", "work-new/01.jpg", "work-new-m1", 0, candidateDigestB),
		coverMediaFact("source-a", "work-other", "work-other/01.jpg", "work-other-m1", 0,
			"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"),
	}
	if err := catalogStore.Stage(ctx, candidate, works, media); err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ValidateCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	publication := publishCandidate(t, catalogStore, candidate)

	assertCover := func(scopeKind, scopeID, wantCover string) {
		t.Helper()
		_, cover, err := catalogStore.AggregateCoverAt(ctx, publication.ID, scopeKind, scopeID)
		if err != nil {
			t.Fatal(err)
		}
		if cover.CoverMediaID != wantCover {
			t.Fatalf("%s/%s 聚合封面 = %q want %q", scopeKind, scopeID, cover.CoverMediaID, wantCover)
		}
	}
	// 作者取最新作品。
	assertCover(catalog.AggregateScopeCreator, "creator-1", "work-new-m1")
	assertCover(catalog.AggregateScopeCreator, "creator-2", "work-other-m1")
	// 平台取最新作者（creator-1 的 5000 > creator-2 的 2000）。
	assertCover(catalog.AggregateScopeSource, "source-a", "work-new-m1")
	// 资料库取最新平台。
	assertCover(catalog.AggregateScopeLibrary, "library-a", "work-new-m1")
}

// TestAggregateCoversAreDeterministicAcrossRepublish 锁定 tie-break 的确定性：没有发布时间的作品
// published_at_ns 同为 0，若缺少稳定 tie-break，聚合封面会在两次发布之间漂移，破坏 revision 快照
// 的一致承诺。
func TestAggregateCoversAreDeterministicAcrossRepublish(t *testing.T) {
	ctx := context.Background()
	catalogStore, _ := newCandidateTestStore(t)
	build := func(jobID string, watermark int64) string {
		t.Helper()
		candidate, err := catalogStore.BeginCandidate(ctx, jobID, "source-a", watermark)
		if err != nil {
			t.Fatal(err)
		}
		works := []catalog.WorkFact{
			aggregateWork("source-a", "library-a", "work-a", "creator-1", "创作者一", 0),
			aggregateWork("source-a", "library-a", "work-b", "creator-1", "创作者一", 0),
			aggregateWork("source-a", "library-a", "work-c", "creator-1", "创作者一", 0),
		}
		media := []catalog.MediaFact{
			coverMediaFact("source-a", "work-a", "work-a/01.jpg", "work-a-m1", 0, candidateDigestA),
			coverMediaFact("source-a", "work-b", "work-b/01.jpg", "work-b-m1", 0, candidateDigestB),
			coverMediaFact("source-a", "work-c", "work-c/01.jpg", "work-c-m1", 0,
				"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"),
		}
		if err := catalogStore.Stage(ctx, candidate, works, media); err != nil {
			t.Fatal(err)
		}
		if err := catalogStore.ValidateCandidate(ctx, candidate); err != nil {
			t.Fatal(err)
		}
		publication := publishCandidate(t, catalogStore, candidate)
		_, cover, err := catalogStore.AggregateCoverAt(ctx, publication.ID, catalog.AggregateScopeCreator, "creator-1")
		if err != nil {
			t.Fatal(err)
		}
		return cover.CoverMediaID
	}
	first := build("job-deterministic-1", 1)
	second := build("job-deterministic-2", 2)
	if first == "" {
		t.Fatal("全部作品无发布时间时仍应选出一个确定封面")
	}
	if first != second {
		t.Fatalf("聚合封面在两次发布之间漂移: %q -> %q", first, second)
	}
}

// TestAggregateCoversRecomputeAfterSingleSourceRescan 覆盖调查指出的跨 Source 语义陷阱：
// 作者与资料库天然横跨多个 Source，单 Source 重扫时聚合封面必须整体重算，不能沿用
// 「按 source_id 过滤继承」的旧值。
func TestAggregateCoversRecomputeAfterSingleSourceRescan(t *testing.T) {
	ctx := context.Background()
	catalogStore, _ := newCandidateTestStore(t)

	stage := func(jobID, sourceID string, watermark int64, works []catalog.WorkFact, media []catalog.MediaFact) catalog.Publication {
		t.Helper()
		candidate, err := catalogStore.BeginCandidate(ctx, jobID, sourceID, watermark)
		if err != nil {
			t.Fatal(err)
		}
		if err := catalogStore.Stage(ctx, candidate, works, media); err != nil {
			t.Fatal(err)
		}
		if err := catalogStore.ValidateCandidate(ctx, candidate); err != nil {
			t.Fatal(err)
		}
		return publishCandidate(t, catalogStore, candidate)
	}

	// source-a 先发布，其作品时间较早。
	first := stage("job-cross-a", "source-a", 1,
		[]catalog.WorkFact{aggregateWork("source-a", "library-a", "work-a", "creator-1", "创作者一", 1000)},
		[]catalog.MediaFact{coverMediaFact("source-a", "work-a", "work-a/01.jpg", "work-a-m1", 0, candidateDigestA)})
	_, libraryCover, err := catalogStore.AggregateCoverAt(ctx, first.ID, catalog.AggregateScopeLibrary, "library-a")
	if err != nil {
		t.Fatal(err)
	}
	if libraryCover.CoverMediaID != "work-a-m1" {
		t.Fatalf("首次发布的资料库封面 = %q", libraryCover.CoverMediaID)
	}

	// 再扫描 source-b，其作品时间更新。资料库级聚合必须改选 source-b——若聚合行按
	// source_id 简单继承，这里会停留在 work-a-m1。
	second := stage("job-cross-b", "source-b", 2,
		[]catalog.WorkFact{aggregateWork("source-b", "library-a", "work-b", "creator-2", "创作者二", 9000)},
		[]catalog.MediaFact{coverMediaFact("source-b", "work-b", "work-b/01.jpg", "work-b-m1", 0, candidateDigestB)})

	_, updated, err := catalogStore.AggregateCoverAt(ctx, second.ID, catalog.AggregateScopeLibrary, "library-a")
	if err != nil {
		t.Fatal(err)
	}
	if updated.CoverMediaID != "work-b-m1" {
		t.Fatalf("单 Source 重扫后资料库聚合封面未重算: %q", updated.CoverMediaID)
	}
	// 未被重扫的 source-a 自身的聚合行必须仍然存在且正确。
	_, kept, err := catalogStore.AggregateCoverAt(ctx, second.ID, catalog.AggregateScopeSource, "source-a")
	if err != nil {
		t.Fatal(err)
	}
	if kept.CoverMediaID != "work-a-m1" {
		t.Fatalf("未重扫 Source 的聚合封面丢失: %q", kept.CoverMediaID)
	}
}

// TestAggregateCoversFollowCustomCover 证明聚合取的是**有效**封面：用户为最新作品设置的
// CustomCover 会自然向上传播到作者、平台与资料库，使聚合封面与作品列表看到的封面一致。
func TestAggregateCoversFollowCustomCover(t *testing.T) {
	ctx := context.Background()
	catalogStore, _ := newCandidateTestStore(t)
	candidate, err := catalogStore.BeginCandidate(ctx, "job-custom-aggregate", "source-a", 1)
	if err != nil {
		t.Fatal(err)
	}
	works := []catalog.WorkFact{aggregateWork("source-a", "library-a", "work-a", "creator-1", "创作者一", 5000)}
	media := []catalog.MediaFact{
		coverMediaFact("source-a", "work-a", "work-a/01.jpg", "work-a-m1", 0, candidateDigestA),
		coverMediaFact("source-a", "work-a", "work-a/02.jpg", "work-a-m2", 1, candidateDigestB),
	}
	if err := catalogStore.Stage(ctx, candidate, works, media); err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ValidateCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	base := publishCandidate(t, catalogStore, candidate)
	_, ruleCover, err := catalogStore.AggregateCoverAt(ctx, base.ID, catalog.AggregateScopeCreator, "creator-1")
	if err != nil {
		t.Fatal(err)
	}
	if ruleCover.CoverMediaID != "work-a-m1" {
		t.Fatalf("规则封面未成为聚合封面: %q", ruleCover.CoverMediaID)
	}

	overlay, err := catalogStore.BeginOverlayCandidate(ctx, "job-custom-overlay", base.CatalogRevisionID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ApplyOverlayFacts(ctx, overlay, map[string]catalog.OverlayFact{
		"work-a": {CustomCoverMediaID: "work-a-m2"},
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
	_, customCover, err := catalogStore.AggregateCoverAt(ctx, republished.ID, catalog.AggregateScopeCreator, "creator-1")
	if err != nil {
		t.Fatal(err)
	}
	if customCover.CoverMediaID != "work-a-m2" {
		t.Fatalf("CustomCover 未向上传播到聚合封面: %q", customCover.CoverMediaID)
	}
	// 旧 publication 的聚合封面不得被污染。
	_, historical, err := catalogStore.AggregateCoverAt(ctx, base.ID, catalog.AggregateScopeCreator, "creator-1")
	if err != nil {
		t.Fatal(err)
	}
	if historical.CoverMediaID != "work-a-m1" {
		t.Fatalf("历史 publication 的聚合封面被污染: %q", historical.CoverMediaID)
	}
}

// TestAggregateCoversSkipWorksWithoutCover 证明没有封面的作品不会代表其作用域——否则会得到
// 一个空封面的聚合行，比没有聚合行更糟。
func TestAggregateCoversSkipWorksWithoutCover(t *testing.T) {
	ctx := context.Background()
	catalogStore, _ := newCandidateTestStore(t)
	candidate, err := catalogStore.BeginCandidate(ctx, "job-nocover-aggregate", "source-a", 1)
	if err != nil {
		t.Fatal(err)
	}
	// 最新的作品没有封面，较早的有；作者聚合应当落到较早那件。
	withoutCover := catalog.WorkFact{
		SourceID: "source-a", LibraryID: "library-a", SourceKey: "work-new", Title: "新", WorkID: "work-new",
		Creator: "创作者一", CreatorID: "creator-1", CreatorSourceKey: "creator-1@work-new",
		CreatorExternalID: "creator-1", SourceCreatorName: "创作者一",
		PublishedAtNanos: 9000, PublishedAtRaw: "raw", PublishedAtParser: "gallery-work-date-v1",
	}
	works := []catalog.WorkFact{
		withoutCover,
		aggregateWork("source-a", "library-a", "work-old", "creator-1", "创作者一", 1000),
	}
	media := []catalog.MediaFact{
		coverMediaFact("source-a", "work-old", "work-old/01.jpg", "work-old-m1", 0, candidateDigestA),
	}
	if err := catalogStore.Stage(ctx, candidate, works, media); err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ValidateCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	publication := publishCandidate(t, catalogStore, candidate)
	_, cover, err := catalogStore.AggregateCoverAt(ctx, publication.ID, catalog.AggregateScopeCreator, "creator-1")
	if err != nil {
		t.Fatal(err)
	}
	if cover.CoverMediaID != "work-old-m1" {
		t.Fatalf("无封面的最新作品不应代表作者: %q", cover.CoverMediaID)
	}
}
