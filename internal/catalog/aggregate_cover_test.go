package catalog_test

import (
	"context"
	"strings"
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

// TestAggregateCoversKeepSourceMediaWithinSource 锁定跨 Source 资源边界：同一 CanonicalCreator
// 可以横跨多个 Source，Creator 的全局代表封面可以来自最新作品所在 Source；但每个 Source 的
// 聚合封面必须来自自身媒体。否则只获 Source A 授权的列表 DTO 会携带 Source B 的 Media ID，且
// 仅含该共享 Creator 的 Source A 会错误丢失自身封面。
func TestAggregateCoversKeepSourceMediaWithinSource(t *testing.T) {
	ctx := context.Background()
	catalogStore, store := newCandidateTestStore(t)
	stage := func(jobID, sourceID string, watermark int64, work catalog.WorkFact, media catalog.MediaFact) catalog.Publication {
		t.Helper()
		candidate, err := catalogStore.BeginCandidate(ctx, jobID, sourceID, watermark)
		if err != nil {
			t.Fatal(err)
		}
		if err := catalogStore.Stage(ctx, candidate, []catalog.WorkFact{work}, []catalog.MediaFact{media}); err != nil {
			t.Fatal(err)
		}
		if err := catalogStore.ValidateCandidate(ctx, candidate); err != nil {
			t.Fatal(err)
		}
		return publishCandidate(t, catalogStore, candidate)
	}

	stage("job-shared-a", "source-a", 1,
		aggregateWork("source-a", "library-a", "work-a", "creator-shared", "共享作者", 1000),
		coverMediaFact("source-a", "work-a", "work-a/01.jpg", "work-a-m1", 0, candidateDigestA))
	publication := stage("job-shared-b", "source-b", 2,
		aggregateWork("source-b", "library-a", "work-b", "creator-shared", "共享作者", 9000),
		coverMediaFact("source-b", "work-b", "work-b/01.jpg", "work-b-m1", 0, candidateDigestB))

	assertCover := func(scopeKind, scopeID, want string) {
		t.Helper()
		_, cover, err := catalogStore.AggregateCoverAt(ctx, publication.ID, scopeKind, scopeID)
		if err != nil {
			t.Fatal(err)
		}
		if cover.CoverMediaID != want {
			t.Fatalf("%s/%s 聚合封面 = %q want %q", scopeKind, scopeID, cover.CoverMediaID, want)
		}
	}

	// Creator 是跨 Source 的全局实体，取全局最新作品。
	assertCover(catalog.AggregateScopeCreator, "creator-shared", "work-b-m1")
	// Source 是授权资源边界，只能引用自身媒体。
	assertCover(catalog.AggregateScopeSource, "source-a", "work-a-m1")
	assertCover(catalog.AggregateScopeSource, "source-b", "work-b-m1")
	// Library 复用 Source 代表时刻，仍取更新的 source-b。
	assertCover(catalog.AggregateScopeLibrary, "library-a", "work-b-m1")

	resolved, sourceIDs, err := catalogStore.SourceIDsAt(ctx, publication.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ID != publication.ID || strings.Join(sourceIDs, ",") != "source-a,source-b" {
		t.Fatalf("publication Source 成员错误: publication=%q sources=%v", resolved.ID, sourceIDs)
	}

	// 逐主体授权把全局胜出的 source-b 排除后，Creator 与 Library 都必须回退到仍然
	// 可见的 source-a 候选，而不是泄露 source-b 的 CanonicalMedia ID 或直接丢失封面。
	for _, scopeKind := range []string{catalog.AggregateScopeCreator, catalog.AggregateScopeLibrary} {
		covers, err := catalogStore.AggregateCoversForSourcesAt(ctx, publication.ID, scopeKind, []string{"source-a"})
		if err != nil {
			t.Fatal(err)
		}
		var scopeID string
		if scopeKind == catalog.AggregateScopeCreator {
			scopeID = "creator-shared"
		} else {
			scopeID = "library-a"
		}
		if got := covers[scopeID].CoverMediaID; got != "work-a-m1" {
			t.Fatalf("%s 授权回退封面 = %q want work-a-m1", scopeKind, got)
		}
	}

	for _, scopeKind := range []string{catalog.AggregateScopeCreator, catalog.AggregateScopeLibrary} {
		covers, err := catalogStore.AggregateCoversForSourcesAt(ctx, publication.ID, scopeKind, nil)
		if err != nil {
			t.Fatal(err)
		}
		if covers == nil || len(covers) != 0 {
			t.Fatalf("%s 空授权集合必须 fail-closed，得到 %#v", scopeKind, covers)
		}
	}

	rows, err := store.Catalog.SQL().QueryContext(ctx, `SELECT source_id, cover_media_id, work_id
FROM creator_source_cover_projections
WHERE catalog_revision_id=? AND overlay_revision_id=? AND creator_id='creator-shared'
ORDER BY source_id`, publication.CatalogRevisionID, publication.OverlayRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var candidates []string
	for rows.Next() {
		var sourceID, mediaID, workID string
		if err := rows.Scan(&sourceID, &mediaID, &workID); err != nil {
			t.Fatal(err)
		}
		candidates = append(candidates, sourceID+"/"+mediaID+"/"+workID)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(candidates, ",") != "source-a/work-a-m1/work-a,source-b/work-b-m1/work-b" {
		t.Fatalf("Creator/Source 持久窄候选错误: %v", candidates)
	}
}

func TestCreatorAggregateCoversForMergedBrowseGroups(t *testing.T) {
	ctx := context.Background()
	catalogStore, _ := newCandidateTestStore(t)
	stage := func(jobID, sourceID string, watermark int64, work catalog.WorkFact, media catalog.MediaFact) catalog.Publication {
		t.Helper()
		candidate, err := catalogStore.BeginCandidate(ctx, jobID, sourceID, watermark)
		if err != nil {
			t.Fatal(err)
		}
		if err := catalogStore.Stage(ctx, candidate, []catalog.WorkFact{work}, []catalog.MediaFact{media}); err != nil {
			t.Fatal(err)
		}
		if err := catalogStore.ValidateCandidate(ctx, candidate); err != nil {
			t.Fatal(err)
		}
		return publishCandidate(t, catalogStore, candidate)
	}
	stage("job-group-a", "source-a", 1,
		aggregateWork("source-a", "library-a", "work-a", "creator-a", "作者甲", 1000),
		coverMediaFact("source-a", "work-a", "work-a/01.jpg", "work-a-m1", 0, candidateDigestA))
	publication := stage("job-group-b", "source-b", 2,
		aggregateWork("source-b", "library-a", "work-b", "creator-b", "作者乙", 9000),
		coverMediaFact("source-b", "work-b", "work-b/01.jpg", "work-b-m1", 0, candidateDigestB))
	groups := []catalog.CreatorAggregateGroup{{
		ScopeID: "creator-a", CreatorIDs: []string{"creator-a", "creator-b"},
	}}

	covers, err := catalogStore.CreatorAggregateCoversForGroupsAt(ctx, publication.ID,
		[]string{"source-a", "source-b"}, groups)
	if err != nil || covers["creator-a"].CoverMediaID != "work-b-m1" {
		t.Fatalf("合并组全局封面错误: covers=%+v err=%v", covers, err)
	}
	covers, err = catalogStore.CreatorAggregateCoversForGroupsAt(ctx, publication.ID,
		[]string{"source-a"}, groups)
	if err != nil || covers["creator-a"].CoverMediaID != "work-a-m1" {
		t.Fatalf("合并组 Source 授权回退错误: covers=%+v err=%v", covers, err)
	}
	covers, err = catalogStore.CreatorAggregateCoversForGroupsAt(ctx, publication.ID, nil, groups)
	if err != nil || covers == nil || len(covers) != 0 {
		t.Fatalf("合并组空授权集合未 fail-closed: covers=%#v err=%v", covers, err)
	}
}

// TestAggregateCreatorSourcePlanMaterializesCandidatesOnce 对生产 SQL 建立结构性计划门禁：
// Work/Creator 关系与 WorkProjection 的基础连接只允许出现在持久窄候选构建阶段一次，Creator
// 和 Source 两个窗口随后只扫描 creator_source_cover_projections。固定墙钟不适合作为可移植
// 单元测试，因此这里只锁定渐进结构，不对执行毫秒数作断言。
func TestAggregateCreatorSourcePlanMaterializesCandidatesOnce(t *testing.T) {
	ctx := context.Background()
	_, store := newCandidateTestStore(t)
	explain := func(statement string, args ...any) []string {
		t.Helper()
		rows, err := store.Catalog.SQL().QueryContext(ctx, "EXPLAIN QUERY PLAN "+statement, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var details []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return details
	}

	candidateDetails := explain(catalog.AggregateCreatorSourceCandidateStatementForTest(), "cat-plan", "ovr-plan")
	candidatePlan := strings.Join(candidateDetails, "\n")
	if got := countTableAccess(candidateDetails, "r"); got != 1 {
		t.Fatalf("候选构建的 work_creator_relations 访问次数 = %d want 1:\n%s", got, candidatePlan)
	}
	if got := countTableAccess(candidateDetails, "w"); got != 1 {
		t.Fatalf("候选构建的 work_projections 访问次数 = %d want 1:\n%s", got, candidatePlan)
	}
	if strings.Contains(catalog.AggregateCreatorSourceCandidateStatementForTest(), "aggregate_cover_projections AS a") {
		t.Fatal("候选构建恢复了从全局 Creator 聚合行向全部作品二次扇出的路径")
	}

	aggregateStatement := catalog.AggregateCreatorSourceStatementForTest()
	aggregateDetails := explain(aggregateStatement, "cat-plan", "ovr-plan", "cat-plan", "ovr-plan")
	aggregatePlan := strings.Join(aggregateDetails, "\n")
	if strings.Contains(aggregateStatement, "work_projections") || strings.Contains(aggregateStatement, "work_creator_relations") {
		t.Fatal("Creator/Source 全局聚合重新引入了 WorkProjection 或 Creator 关系")
	}
	if got := strings.Count(aggregateStatement, "FROM creator_source_cover_projections"); got != 2 {
		t.Fatalf("Creator/Source 窗口读取窄候选次数 = %d want 2", got)
	}
	if got := countTableAccess(aggregateDetails, "creator_source_cover_projections"); got != 2 {
		t.Fatalf("生产计划的窄候选访问次数 = %d want 2:\n%s", got, aggregatePlan)
	}
}

func countTableAccess(details []string, alias string) int {
	count := 0
	for _, detail := range details {
		fields := strings.Fields(detail)
		if len(fields) < 2 || (fields[0] != "SCAN" && fields[0] != "SEARCH") {
			continue
		}
		if fields[1] == alias {
			count++
		}
	}
	return count
}

// TestAuthorizedAggregateCoverPlansStayOnNarrowProjection 锁定请求期授权重选只读取 v20
// 窄投影：小 allowed 必须从 Source 索引驱动，小 deny 必须沿 Creator rank 索引做相关
// LIMIT 1；两条路径都不得重新连接 WorkProjection 或 Creator 关系。
func TestAuthorizedAggregateCoverPlansStayOnNarrowProjection(t *testing.T) {
	_, store := newCandidateTestStore(t)
	for _, test := range []struct {
		name, statement, index string
		args                   []any
	}{
		{"small-allowed", catalog.CreatorCoversSmallAllowedStatementForTest(), "creator_source_cover_source_idx",
			[]any{`["source-a"]`, "cat-plan", "ovr-plan"}},
		{"small-denied", catalog.CreatorCoversSmallDeniedStatementForTest(), "creator_source_cover_rank_idx",
			[]any{`["source-a"]`, "cat-plan", "ovr-plan"}},
		{"small-allowed-for-scopes", catalog.CreatorCoversSmallAllowedForScopesStatementForTest(), "creator_source_cover_source_idx",
			[]any{`["source-a"]`, `["creator-a"]`, "cat-plan", "ovr-plan"}},
		{"small-denied-for-scopes", catalog.CreatorCoversSmallDeniedForScopesStatementForTest(), "creator_source_cover_rank_idx",
			[]any{`["source-a"]`, `["creator-a"]`, "cat-plan", "ovr-plan"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			rows, err := store.Catalog.SQL().Query("EXPLAIN QUERY PLAN "+test.statement, test.args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var details []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				details = append(details, detail)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			plan := strings.Join(details, "\n")
			if !strings.Contains(plan, test.index) {
				t.Fatalf("生产计划未使用 %s:\n%s", test.index, plan)
			}
			if strings.Contains(test.statement, "work_projections") || strings.Contains(test.statement, "work_creator_relations") {
				t.Fatalf("授权重选重新引入宽事实表:\n%s", test.statement)
			}
			if strings.Contains(test.name, "denied") && !strings.Contains(plan, "CORRELATED SCALAR SUBQUERY") {
				t.Fatalf("小 deny 未按 Creator 执行相关 LIMIT 1:\n%s", plan)
			}
		})
	}
}

// TestAggregateCoversOnlyMaterializeRequestedScopes 锁定列表身份裁剪和详情查询的批量边界：
// Catalog 必须只返回调用方明确请求的 scope ID，同时在小 allowed 与小 deny 路径保持正式回退语义。
func TestAggregateCoversOnlyMaterializeRequestedScopes(t *testing.T) {
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

	stage("job-scoped-a", "source-a", 1,
		[]catalog.WorkFact{aggregateWork("source-a", "library-target", "work-target-a", "creator-target", "目标作者", 1_000)},
		[]catalog.MediaFact{coverMediaFact("source-a", "work-target-a", "work-target-a/01.jpg", "work-target-a-m1", 0, candidateDigestA)})
	stage("job-scoped-b", "source-b", 2,
		[]catalog.WorkFact{aggregateWork("source-b", "library-target", "work-target-b", "creator-target", "目标作者", 3_000)},
		[]catalog.MediaFact{coverMediaFact("source-b", "work-target-b", "work-target-b/01.jpg", "work-target-b-m1", 0,
			candidateDigestB)})
	publication := stage("job-scoped-c", "source-c", 3,
		[]catalog.WorkFact{aggregateWork("source-c", "library-other", "work-other-c", "creator-other", "无关作者", 8_000)},
		[]catalog.MediaFact{coverMediaFact("source-c", "work-other-c", "work-other-c/01.jpg", "work-other-c-m1", 0,
			"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")})

	assertOnly := func(label string, covers map[string]catalog.AggregateCover, scopeID, mediaID string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if len(covers) != 1 || covers[scopeID].CoverMediaID != mediaID {
			t.Fatalf("%s = %+v want only %s/%s", label, covers, scopeID, mediaID)
		}
	}

	covers, err := catalogStore.AggregateCoversAt(ctx, publication.ID, catalog.AggregateScopeCreator, "creator-target")
	assertOnly("全成员定向 Creator", covers, "creator-target", "work-target-b-m1", err)
	covers, err = catalogStore.AggregateCoversAt(ctx, publication.ID, catalog.AggregateScopeLibrary, "library-target")
	assertOnly("全成员定向 Library", covers, "library-target", "work-target-b-m1", err)
	covers, err = catalogStore.AggregateCoversForSourcesAt(ctx, publication.ID, catalog.AggregateScopeCreator,
		[]string{"source-a"}, "creator-target")
	assertOnly("小 allowed 定向 Creator", covers, "creator-target", "work-target-a-m1", err)
	covers, err = catalogStore.AggregateCoversForSourcesAt(ctx, publication.ID, catalog.AggregateScopeCreator,
		[]string{"source-a", "source-c"}, "creator-target")
	assertOnly("小 deny 定向 Creator", covers, "creator-target", "work-target-a-m1", err)
	covers, err = catalogStore.AggregateCoversForSourcesAt(ctx, publication.ID, catalog.AggregateScopeLibrary,
		[]string{"source-a", "source-c"}, "library-target")
	assertOnly("定向 Library", covers, "library-target", "work-target-a-m1", err)

	covers, err = catalogStore.AggregateCoversForSourcesAt(ctx, publication.ID, catalog.AggregateScopeCreator,
		[]string{"source-a", "source-c"}, "creator-missing")
	if err != nil || covers == nil || len(covers) != 0 {
		t.Fatalf("不存在的 scope 必须返回非 nil 空结果: covers=%#v err=%v", covers, err)
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
