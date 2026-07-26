package query_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/storage"
)

func TestAuthorizationQueryPlansKeepIndexedOuterScanAndMembershipLookup(t *testing.T) {
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

	membershipPlan := explainPlan(t, db, `SELECT source_id FROM catalog_revision_sources
WHERE catalog_revision_id=? ORDER BY source_id`, "cat")
	assertPlanContains(t, membershipPlan, "COVERING INDEX sqlite_autoindex_catalog_revision_sources_1")
	assertPlanExcludes(t, membershipPlan, "TEMP B-TREE")

	libraryMembershipPlan := explainPlan(t, db, `SELECT source_id FROM catalog_revision_sources
WHERE catalog_revision_id=? AND library_id=? ORDER BY source_id`, "cat", "lib")
	assertPlanContains(t, libraryMembershipPlan, "COVERING INDEX catalog_revision_sources_library_idx")
	assertPlanExcludes(t, libraryMembershipPlan, "TEMP B-TREE")

	allAllowedPlan := explainProductionBrowsePlan(t, db, " INDEXED BY work_projections_query_idx", "", "cat", "ovr", 101)
	assertPlanContains(t, allAllowedPlan, "work_projections_query_idx")
	assertPlanExcludes(t, allAllowedPlan, "TEMP B-TREE")
	assertCorrelatedCount(t, allAllowedPlan, 1)

	deepPagePlan := explainProductionDeepBrowsePlan(t, db, "cat", "ovr",
		"title-005000", "title-005000", "work-005000", 101)
	assertPlanContains(t, deepPagePlan, "work_projections_query_idx")
	assertPlanExcludes(t, deepPagePlan, "TEMP B-TREE")
	assertCorrelatedCount(t, deepPagePlan, 1)

	for _, test := range []struct {
		name           string
		fromSuffix     string
		predicate      string
		wantIndex      string
		forbidTempSort bool
	}{
		{name: "multi-allowed-list", fromSuffix: " INDEXED BY work_projections_query_idx", predicate: "w.source_id IN (SELECT value FROM json_each(?))", wantIndex: "work_projections_query_idx", forbidTempSort: true},
		{name: "denied-list", fromSuffix: " INDEXED BY work_projections_query_idx", predicate: "w.source_id NOT IN (SELECT value FROM json_each(?))", wantIndex: "work_projections_query_idx", forbidTempSort: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := explainProductionBrowsePlan(t, db, test.fromSuffix, test.predicate, "cat", "ovr", `["src-000","src-001"]`, 101)
			assertPlanContains(t, plan, test.wantIndex)
			assertPlanContains(t, plan, "LIST SUBQUERY")
			assertCorrelatedCount(t, plan, 1)
			if test.forbidTempSort {
				assertPlanExcludes(t, plan, "TEMP B-TREE")
			}
		})
	}

	for _, test := range []struct {
		name, fromSuffix, predicate, value, wantIndex string
	}{
		{name: "singleton-allowed", fromSuffix: " INDEXED BY work_projections_source_query_idx", predicate: "w.source_id=?", value: "src-000", wantIndex: "work_projections_source_query_idx"},
		{name: "explicit-source", fromSuffix: " INDEXED BY work_projections_source_query_idx", predicate: "w.source_id=?", value: "src-000", wantIndex: "work_projections_source_query_idx"},
		{name: "explicit-library", fromSuffix: " INDEXED BY work_projections_library_query_idx", predicate: "w.library_id=?", value: "lib-00", wantIndex: "work_projections_library_query_idx"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := explainProductionBrowsePlan(t, db, test.fromSuffix, test.predicate, "cat", "ovr", test.value, 101)
			assertPlanContains(t, plan, test.wantIndex)
			assertPlanExcludes(t, plan, "TEMP B-TREE")
			assertCorrelatedCount(t, plan, 1)
		})
	}

	t.Run("library-partial-allow-prefers-source-index", func(t *testing.T) {
		plan := explainProductionBrowsePlan(t, db, " INDEXED BY work_projections_source_query_idx",
			"w.source_id=? AND w.library_id=?", "cat", "ovr", "src-000", "lib-00", 101)
		assertPlanContains(t, plan, "work_projections_source_query_idx")
		assertPlanExcludes(t, plan, "TEMP B-TREE")
		assertCorrelatedCount(t, plan, 1)
	})
	t.Run("library-deny-keeps-library-index", func(t *testing.T) {
		plan := explainProductionBrowsePlan(t, db, " INDEXED BY work_projections_library_query_idx",
			"w.source_id<>? AND w.library_id=?", "cat", "ovr", "src-000", "lib-00", 101)
		assertPlanContains(t, plan, "work_projections_library_query_idx")
		assertPlanExcludes(t, plan, "TEMP B-TREE")
		assertCorrelatedCount(t, plan, 1)
	})

	for _, test := range []struct {
		name, fromSuffix, predicate, value, wantIndex string
	}{
		{name: "total-all", fromSuffix: " INDEXED BY work_projections_query_idx", wantIndex: "work_projections_query_idx"},
		{name: "total-denied", fromSuffix: " INDEXED BY work_projections_source_query_idx", predicate: "w.source_id<>?", value: "src-000", wantIndex: "work_projections_source_query_idx"},
		{name: "total-positive-allowed", fromSuffix: " INDEXED BY work_projections_source_query_idx", predicate: "w.source_id IN (SELECT value FROM json_each(?))", value: `["src-000","src-001"]`, wantIndex: "work_projections_source_query_idx"},
		{name: "total-explicit-source", fromSuffix: " INDEXED BY work_projections_source_query_idx", predicate: "w.source_id=?", value: "src-000", wantIndex: "work_projections_source_query_idx"},
		{name: "total-explicit-library", fromSuffix: " INDEXED BY work_projections_library_query_idx", predicate: "w.library_id=?", value: "lib-00", wantIndex: "work_projections_library_query_idx"},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := []any{"cat", "ovr"}
			if test.predicate != "" {
				args = append(args, test.value)
			}
			args = append(args, 10_001)
			plan := explainProductionTotalPlan(t, db, test.fromSuffix, test.predicate, args...)
			assertPlanContains(t, plan, test.wantIndex)
			assertPlanExcludes(t, plan, "TEMP B-TREE")
			if strings.Contains(test.predicate, "json_each") {
				assertPlanContains(t, plan, "LIST SUBQUERY")
			}
		})
	}
	t.Run("total-library-partial-allow-prefers-source-index", func(t *testing.T) {
		plan := explainProductionTotalPlan(t, db, " INDEXED BY work_projections_source_query_idx",
			"w.source_id=? AND w.library_id=?", "cat", "ovr", "src-000", "lib-00", 10_001)
		assertPlanContains(t, plan, "work_projections_source_query_idx")
		assertPlanExcludes(t, plan, "TEMP B-TREE")
	})
}

func seedAuthorizationPlanStats(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
INSERT INTO catalog_revisions VALUES ('cat', 'job', 'src-000', 'staging', 1, NULL);
INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at)
VALUES ('ovr', 'cat', 0, 'staging', 1);
WITH RECURSIVE seq(n) AS (
  VALUES(0) UNION ALL SELECT n+1 FROM seq WHERE n<9999
)
INSERT INTO work_projections
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id, title, creator,
 tags_json, filenames_text, normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text,
 sort_title_key, hidden)
SELECT 'cat', 'ovr', printf('work-%06d', n), printf('src-%03d', n%100), printf('key-%06d', n),
       printf('lib-%02d', n%10), printf('title-%06d', n), '', '[]', '', '', '', '', printf('title-%06d', n), 0
FROM seq;
ANALYZE work_projections;`)
	if err != nil {
		t.Fatal(err)
	}
}

func explainProductionBrowsePlan(t *testing.T, db *sql.DB, fromSuffix, authorizationPredicate string, args ...any) string {
	t.Helper()
	where := "w.catalog_revision_id=? AND w.overlay_revision_id=? AND w.hidden=0"
	if authorizationPredicate != "" {
		where += " AND " + authorizationPredicate
	}
	return explainPlan(t, db, `WITH scored AS (
SELECT w.work_id, w.title, w.creator, w.tags_json, w.filenames_text, w.sort_title_key,
w.favorite, w.progress, w.cover_media_id,
(SELECT count(*) FROM media_projections m
 WHERE m.catalog_revision_id=w.catalog_revision_id AND m.overlay_revision_id=w.overlay_revision_id
   AND m.work_id=w.work_id AND m.hidden=0) AS media_count,
0 AS rank_tier
FROM work_projections w`+fromSuffix+` WHERE `+where+`
)
SELECT work_id, title, creator, tags_json, filenames_text, sort_title_key, favorite, progress,
cover_media_id, media_count, rank_tier FROM scored
ORDER BY sort_title_key, work_id LIMIT ?`, args...)
}

func explainProductionTotalPlan(t *testing.T, db *sql.DB, fromSuffix, authorizationPredicate string, args ...any) string {
	t.Helper()
	where := "w.catalog_revision_id=? AND w.overlay_revision_id=? AND w.hidden=0"
	if authorizationPredicate != "" {
		where += " AND " + authorizationPredicate
	}
	return explainPlan(t, db, `SELECT count(*) FROM (
SELECT 1 FROM work_projections w`+fromSuffix+` WHERE `+where+` LIMIT ?)`, args...)
}

func explainProductionDeepBrowsePlan(t *testing.T, db *sql.DB, args ...any) string {
	t.Helper()
	return explainPlan(t, db, `WITH scored AS (
SELECT w.work_id, w.title, w.creator, w.tags_json, w.filenames_text, w.sort_title_key,
w.favorite, w.progress, w.cover_media_id,
(SELECT count(*) FROM media_projections m
 WHERE m.catalog_revision_id=w.catalog_revision_id AND m.overlay_revision_id=w.overlay_revision_id
   AND m.work_id=w.work_id AND m.hidden=0) AS media_count,
0 AS rank_tier
FROM work_projections w INDEXED BY work_projections_query_idx
WHERE w.catalog_revision_id=? AND w.overlay_revision_id=? AND w.hidden=0
)
SELECT work_id, title, creator, tags_json, filenames_text, sort_title_key, favorite, progress,
cover_media_id, media_count, rank_tier FROM scored
WHERE (sort_title_key>? OR (sort_title_key=? AND work_id>?))
ORDER BY sort_title_key, work_id LIMIT ?`, args...)
}

func explainPlan(t *testing.T, db *sql.DB, statement string, args ...any) string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+statement, args...)
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
	return strings.Join(details, "\n")
}

func assertPlanContains(t *testing.T, plan, fragment string) {
	t.Helper()
	if !strings.Contains(plan, fragment) {
		t.Fatalf("query plan 缺少 %q:\n%s", fragment, plan)
	}
}

func assertPlanExcludes(t *testing.T, plan, fragment string) {
	t.Helper()
	if strings.Contains(plan, fragment) {
		t.Fatalf("query plan 不应包含 %q:\n%s", fragment, plan)
	}
}

func assertCorrelatedCount(t *testing.T, plan string, want int) {
	t.Helper()
	if count := strings.Count(plan, "CORRELATED"); count != want {
		t.Fatalf("query plan correlated subquery=%d want=%d:\n%s", count, want, plan)
	}
}
