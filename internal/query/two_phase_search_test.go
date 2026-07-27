package query_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	contractquery "github.com/RecRivenVI/gallery/internal/contract/query"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	galleryquery "github.com/RecRivenVI/gallery/internal/query"
	"github.com/RecRivenVI/gallery/internal/storage"
)

// planNode 是 EXPLAIN QUERY PLAN 的一行，保留 id/parent 以便判断某个节点落在计划树的
// 哪一棵子树下——这正是"相关子查询在排序器之下还是分页之后"的唯一可靠判据，扁平的
// detail 字符串无法区分两者。
type planNode struct {
	id, parent int
	detail     string
}

func explainPlanNodes(t *testing.T, db *sql.DB, statement string, args ...any) []planNode {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+statement, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN 失败: %v\n%s", err, statement)
	}
	defer rows.Close()
	var nodes []planNode
	for rows.Next() {
		var node planNode
		var unused int
		if err := rows.Scan(&node.id, &node.parent, &unused, &node.detail); err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return nodes
}

func formatPlanNodes(nodes []planNode) string {
	var builder strings.Builder
	for _, node := range nodes {
		fmt.Fprintf(&builder, "id=%d parent=%d %s\n", node.id, node.parent, node.detail)
	}
	return builder.String()
}

// subtreeOf 返回以 root 为根的全部后代节点 id（含 root 自身）。
func subtreeOf(nodes []planNode, root int) map[int]bool {
	inside := map[int]bool{root: true}
	// EXPLAIN QUERY PLAN 输出按前序排列，父节点总是先于子节点出现，单趟即可闭包。
	for _, node := range nodes {
		if inside[node.parent] {
			inside[node.id] = true
		}
	}
	return inside
}

// TestSearchQueryPlanSortsNarrowRowsAndProjectsAfterPagination 是搜索形态缺失已久的
// 计划回归：`ORDER BY rank_tier DESC` 依赖运行期查询串，不可能有索引，因此排序器必然
// 吞下全部候选行；能改的只有"进排序器的是什么"。本测试锁定两条结构性事实：
//
//  1. 排序器（TEMP B-TREE）位于 page 分页阶段内部，而不是外层投影；
//  2. media_count 相关子查询恰好出现一次，且**不在** page 子树内——即它在分页之后对
//     limit+1 行求值，而不是逐候选行求值。
//
// 断言直接建立在生产语句上（BuildPageStatementForTest），不是 SQL 副本，避免改写实现
// 后测试仍然对着旧形态通过。
func TestSearchQueryPlanSortsNarrowRowsAndProjectsAfterPagination(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(t.TempDir())
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.Catalog.SQL()
	seedAuthorizationPlanStats(t, db)
	seedPlanSearchDocuments(t, db)

	sources := []string{"src-000"}
	for _, test := range []struct {
		name   string
		claims contractquery.CursorClaims
	}{
		{name: "first-page"},
		{name: "keyset-page", claims: contractquery.CursorClaims{
			LastSortKey: "title-005000", LastRankTier: 23, LastCanonicalWorkID: "work-005000",
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			statement, args, err := galleryquery.BuildPageStatementForTest(ctx, "cat", "ovr", sources, sources,
				galleryquery.Request{Search: "title-005000", SortDirection: "asc", Limit: 100}, test.claims)
			if err != nil {
				t.Fatal(err)
			}
			nodes := explainPlanNodes(t, db, statement, args...)
			plan := formatPlanNodes(nodes)

			pageNode := -1
			for _, node := range nodes {
				if strings.HasSuffix(node.detail, " page") {
					pageNode = node.id
				}
			}
			if pageNode < 0 {
				t.Fatalf("SQLite 把 page 分页 CTE 展平了，两阶段改写未生效:\n%s", plan)
			}
			pageSubtree := subtreeOf(nodes, pageNode)

			sorters := 0
			for _, node := range nodes {
				if !strings.Contains(node.detail, "USE TEMP B-TREE FOR ORDER BY") {
					continue
				}
				sorters++
				if !pageSubtree[node.id] {
					t.Fatalf("排序器不在 page 分页阶段内部:\n%s", plan)
				}
			}
			if sorters != 1 {
				t.Fatalf("排序器数量=%d want=1:\n%s", sorters, plan)
			}

			correlated := 0
			for _, node := range nodes {
				if !strings.Contains(node.detail, "CORRELATED") {
					continue
				}
				correlated++
				if pageSubtree[node.id] {
					t.Fatalf("media_count 相关子查询仍在排序器之下逐候选行求值:\n%s", plan)
				}
			}
			if correlated != 1 {
				t.Fatalf("相关子查询数量=%d want=1:\n%s", correlated, plan)
			}
		})
	}

	// 反向对照：改写前的单阶段形态没有 page 分页阶段，media_count 相关子查询与排序器
	// 同处一层，即逐候选行求值。它证明上面的结构判据不是恒真——如果有人把生产语句改回
	// 单阶段，上面的断言一定会失败。
	t.Run("negative-control-single-phase", func(t *testing.T) {
		statement, args, err := galleryquery.BuildSinglePhaseSearchStatementForTest(ctx, "cat", "ovr", sources, sources,
			galleryquery.Request{Search: "title-005000", SortDirection: "asc", Limit: 100}, contractquery.CursorClaims{})
		if err != nil {
			t.Fatal(err)
		}
		nodes := explainPlanNodes(t, db, statement, args...)
		plan := formatPlanNodes(nodes)
		for _, node := range nodes {
			if strings.HasSuffix(node.detail, " page") {
				t.Fatalf("单阶段参照实现不应出现 page 分页阶段:\n%s", plan)
			}
		}
		sorterParent, correlatedParent := -1, -2
		for _, node := range nodes {
			if strings.Contains(node.detail, "USE TEMP B-TREE FOR ORDER BY") {
				sorterParent = node.parent
			}
			if strings.Contains(node.detail, "CORRELATED") {
				correlatedParent = node.parent
			}
		}
		if sorterParent != correlatedParent {
			t.Fatalf("单阶段参照实现里相关子查询与排序器应同处一层: sorter=%d correlated=%d\n%s",
				sorterParent, correlatedParent, plan)
		}
	})
}

// TestBrowseQueryPlanKeepsCoveringIndexWithoutSorter 用生产语句复核无搜索浏览形态没有
// 因为搜索路径的两阶段改写而退化：它必须继续走覆盖索引、没有排序器，且相关子查询只有
// 一次。authorization_plan_test.go 中同名断言建立在手写 SQL 副本上，这里补上对真实语句的
// 覆盖。
func TestBrowseQueryPlanKeepsCoveringIndexWithoutSorter(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(t.TempDir())
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.Catalog.SQL()
	seedAuthorizationPlanStats(t, db)

	sources := []string{"src-000"}
	for _, test := range []struct {
		name   string
		claims contractquery.CursorClaims
	}{
		{name: "first-page"},
		{name: "keyset-page", claims: contractquery.CursorClaims{
			LastSortKey: "title-005000", LastCanonicalWorkID: "work-005000",
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			statement, args, err := galleryquery.BuildPageStatementForTest(ctx, "cat", "ovr", sources, sources,
				galleryquery.Request{SortDirection: "asc", Limit: 100}, test.claims)
			if err != nil {
				t.Fatal(err)
			}
			plan := formatPlanNodes(explainPlanNodes(t, db, statement, args...))
			assertPlanContains(t, plan, "work_projections_query_idx")
			assertPlanExcludes(t, plan, "TEMP B-TREE")
			assertCorrelatedCount(t, plan, 1)
		})
	}
}

// TestTwoPhaseSearchPageMatchesSinglePhaseRowsAndOrder 是两阶段改写的差分证明：对同一
// 语料、同一请求、同一 keyset，两阶段生产语句与改写前的单阶段参照实现必须返回逐行相同的
// (work_id, sort_title_key, rank_tier) 序列。逐页推进使用与 Service.Search 完全一致的
// 方式从上一页最后一行取游标三元组，因此它同时锁定了游标语义没有变化。
func TestTwoPhaseSearchPageMatchesSinglePhaseRowsAndOrder(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(t.TempDir())
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	seedPublication(t, store, "810", twoPhaseCorpus())
	db := store.Catalog.SQL()
	const catalogRevision = "cat_018f47d2-5c16-7a44-a8a0-000000000810"
	const overlayRevision = "ovr_018f47d2-5c16-7a44-a8a0-000000000810"
	candidates := []string{"src_other", "src_test"}

	for _, test := range []struct {
		name    string
		request galleryquery.Request
		allowed []string
	}{
		{name: "asc-all", request: galleryquery.Request{Search: "apple", SortDirection: "asc", Limit: 2}, allowed: candidates},
		{name: "desc-all", request: galleryquery.Request{Search: "apple", SortDirection: "desc", Limit: 2}, allowed: candidates},
		{name: "asc-limit1", request: galleryquery.Request{Search: "apple", SortDirection: "asc", Limit: 1}, allowed: candidates},
		{name: "partial-authorization", request: galleryquery.Request{Search: "apple", SortDirection: "asc", Limit: 3}, allowed: []string{"src_test"}},
		{name: "structured-filter", request: galleryquery.Request{
			Search: "apple", SortDirection: "asc", Limit: 2,
			Filter: `{"field":"overlay.favorite","op":"eq","value":true}`,
		}, allowed: candidates},
		{name: "cjk", request: galleryquery.Request{Search: "苹果", SortDirection: "asc", Limit: 2}, allowed: candidates},
	} {
		t.Run(test.name, func(t *testing.T) {
			var claims contractquery.CursorClaims
			pages := 0
			totalRows := 0
			for {
				statement, args, err := galleryquery.BuildPageStatementForTest(ctx,
					catalogRevision, overlayRevision, candidates, test.allowed, test.request, claims)
				if err != nil {
					t.Fatal(err)
				}
				oracleStatement, oracleArgs, err := galleryquery.BuildSinglePhaseSearchStatementForTest(ctx,
					catalogRevision, overlayRevision, candidates, test.allowed, test.request, claims)
				if err != nil {
					t.Fatal(err)
				}
				got := collectPageKeys(t, db, statement, args...)
				want := collectPageKeys(t, db, oracleStatement, oracleArgs...)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("第 %d 页两阶段结果与单阶段参照不一致:\n两阶段=%v\n单阶段=%v", pages, got, want)
				}
				totalRows += len(got)
				pages++
				if len(got) <= test.request.Limit {
					break
				}
				// 与 Service.Search 一致：多取的第 limit+1 行只用于判断"还有下一页"，
				// 游标取自实际返回的最后一行。
				last := got[test.request.Limit-1]
				claims = contractquery.CursorClaims{
					LastSortKey: last.sortKey, LastRankTier: last.rankTier, LastCanonicalWorkID: last.workID,
				}
				if pages > 50 {
					t.Fatal("分页未收敛")
				}
			}
			if totalRows == 0 {
				t.Fatal("差分语料没有命中任何行，测试无效")
			}
		})
	}
}

// TestSearchJoinBackKeepsSnapshotPayload 复核分页后回表取到的宽负载仍然是同一行的快照
// 事实：mediaCount 来自 media_projections 相关子查询，favorite/progress/coverMediaId
// 来自 work_projections 的同一主键行。回表写错 JOIN 条件会在这里暴露。
func TestSearchJoinBackKeepsSnapshotPayload(t *testing.T) {
	store, _ := richFixture(t)
	service := newFixtureService(t, store)
	scope := galleryquery.AuthorizationScope("owner", []string{"library.read"})

	result, err := service.Search(context.Background(), authorizedRequest(galleryquery.Request{
		Search: "work", SortDirection: "asc", Limit: 20, AuthorizationScope: scope,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if titles := resultTitles(result); !reflect.DeepEqual(titles, []string{"Alpha Work", "Beta Work", "Gamma Work"}) {
		t.Fatalf("搜索命中 = %v", titles)
	}
	for _, item := range result.Items {
		if item.MediaCount != 1 {
			t.Fatalf("%s mediaCount=%d want=1", item.Title, item.MediaCount)
		}
	}
	alpha := result.Items[0]
	if !alpha.Favorite || alpha.Progress != 0.25 || alpha.CoverMediaID == "" {
		t.Fatalf("回表负载错位: %+v", alpha)
	}
	beta := result.Items[1]
	if beta.Favorite || beta.Progress != 0.75 || beta.CoverMediaID != "" {
		t.Fatalf("回表负载错位: %+v", beta)
	}
}

// TestSearchCursorPaginationCoversEveryHitOnce 是端到端游标复核：搜索形态逐页推进必须
// 覆盖全部命中且不重复，两个排序方向都成立。
func TestSearchCursorPaginationCoversEveryHitOnce(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(t.TempDir())
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	corpus := twoPhaseCorpus()
	seedPublication(t, store, "811", corpus)
	service, err := galleryquery.NewService(ctx, store.Control.SQL(), store.Catalog.SQL(),
		clock.Fixed{Time: time.Date(2026, 7, 27, 1, 0, 0, 0, time.UTC)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	scope := galleryquery.AuthorizationScope("owner", []string{"library.read"})

	for _, direction := range []string{"asc", "desc"} {
		t.Run(direction, func(t *testing.T) {
			single, err := service.Search(ctx, authorizedRequest(galleryquery.Request{
				Search: "apple", SortDirection: direction, Limit: 100, AuthorizationScope: scope,
			}))
			if err != nil {
				t.Fatal(err)
			}
			if len(single.Items) == 0 {
				t.Fatal("语料未命中，测试无效")
			}

			request := authorizedRequest(galleryquery.Request{
				Search: "apple", SortDirection: direction, Limit: 2, AuthorizationScope: scope,
			})
			var paged []string
			for {
				page, err := service.Search(ctx, request)
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range page.Items {
					paged = append(paged, item.ID)
				}
				if page.NextCursor == "" {
					break
				}
				request.Cursor = page.NextCursor
			}
			var wantIDs []string
			for _, item := range single.Items {
				wantIDs = append(wantIDs, item.ID)
			}
			if !reflect.DeepEqual(paged, wantIDs) {
				t.Fatalf("%s 逐页结果与整页结果不一致:\n分页=%v\n整页=%v", direction, paged, wantIDs)
			}
			if hasDuplicate(paged) {
				t.Fatalf("%s 分页出现重复: %v", direction, paged)
			}
		})
	}
}

type pageKey struct {
	workID   string
	sortKey  string
	rankTier int
}

// collectPageKeys 从任意分页语句中按列名取出 (work_id, sort_title_key, rank_tier)，
// 使差分比较不依赖两条语句的投影宽度。
func collectPageKeys(t *testing.T, db *sql.DB, statement string, args ...any) []pageKey {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), statement, args...)
	if err != nil {
		t.Fatalf("执行分页语句失败: %v\n%s", err, statement)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	index := map[string]int{}
	for position, name := range columns {
		index[name] = position
	}
	for _, required := range []string{"work_id", "sort_title_key", "rank_tier"} {
		if _, ok := index[required]; !ok {
			t.Fatalf("分页语句缺少列 %q: %v", required, columns)
		}
	}
	keys := []pageKey{}
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for position := range values {
			pointers[position] = &values[position]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		key := pageKey{}
		if raw, ok := values[index["work_id"]].(string); ok {
			key.workID = raw
		}
		if raw, ok := values[index["sort_title_key"]].(string); ok {
			key.sortKey = raw
		}
		if raw, ok := values[index["rank_tier"]].(int64); ok {
			key.rankTier = int(raw)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return keys
}

// twoPhaseCorpus 覆盖差分校验需要的全部形态：四个字段各自命中、同一 Work 多字段命中、
// 同 rank_tier 内的 sort_title_key 并列（同名标题）、CJK，以及跨 Source 的授权分组。
func twoPhaseCorpus() []seedWork {
	return []seedWork{
		{title: "apple", favorite: true},
		{title: "apple", sourceID: "src_other"},
		{title: "apple juice", favorite: true},
		{title: "apple pie", sourceID: "src_other"},
		{title: "banana with apple", creator: "Apple Farm"},
		{title: "unrelated", creator: "apple seller", favorite: true},
		{title: "tagged", tags: []string{"apple", "fruit"}},
		{title: "filed", filenames: []string{"an-apple-a-day.bin"}},
		{title: "multi apple", creator: "apple", tags: []string{"apple"}, filenames: []string{"apple.bin"}, favorite: true},
		{title: "苹果作品", creator: "苹果作者", tags: []string{"苹果"}},
		{title: "苹果作品", sourceID: "src_other"},
		{title: "无关作品"},
	}
}

// seedPlanSearchDocuments 为计划测试补上 work_search 行，使 FTS 驱动关系与生产形态一致。
func seedPlanSearchDocuments(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
WITH RECURSIVE seq(n) AS (
  VALUES(0) UNION ALL SELECT n+1 FROM seq WHERE n<9999
)
INSERT INTO work_search
(catalog_revision_id, overlay_revision_id, work_id, normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text)
SELECT 'cat', 'ovr', printf('work-%06d', n), printf('title-%06d', n), '', ''
FROM seq;`)
	if err != nil {
		t.Fatal(err)
	}
}
