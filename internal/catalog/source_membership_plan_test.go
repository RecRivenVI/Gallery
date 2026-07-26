package catalog

import (
	"context"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/storage"
)

func TestValidateSourceMembershipUsesCoveringGroupedPlan(t *testing.T) {
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
	if _, err := db.ExecContext(ctx, `
INSERT INTO catalog_revisions VALUES ('cat', 'job', 'src-000', 'staging', 1, NULL);
INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at)
VALUES ('ovr', 'cat', 0, 'staging', 1);
WITH RECURSIVE seq(n) AS (VALUES(0) UNION ALL SELECT n+1 FROM seq WHERE n<99)
INSERT INTO catalog_revision_sources (catalog_revision_id, source_id, library_id)
SELECT 'cat', printf('src-%03d', n), printf('lib-%02d', n%10) FROM seq;
WITH RECURSIVE seq(n) AS (VALUES(0) UNION ALL SELECT n+1 FROM seq WHERE n<9999)
INSERT INTO work_projections
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id, title, creator,
 tags_json, filenames_text, normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text,
 sort_title_key, hidden)
SELECT 'cat', 'ovr', printf('work-%06d', n), printf('src-%03d', n%100), printf('key-%06d', n),
       printf('lib-%02d', n%10), printf('title-%06d', n), '', '[]', '', '', '', '', printf('title-%06d', n), 0
FROM seq;
ANALYZE work_projections;
ANALYZE catalog_revision_sources;`); err != nil {
		t.Fatal(err)
	}

	rows, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+validateSourceMembershipSQL,
		"cat", "ovr", "cat", "cat")
	if err != nil {
		t.Fatal(err)
	}
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(details, "\n")
	if !strings.Contains(plan, "COVERING INDEX work_projections_source_query_idx") {
		t.Fatalf("成员校验未沿 Source covering index 聚合:\n%s", plan)
	}
	if !strings.Contains(plan, "catalog_revision_sources_library_idx") {
		t.Fatalf("成员校验未使用成员表 covering index:\n%s", plan)
	}
	if strings.Contains(plan, "TEMP B-TREE") {
		t.Fatalf("成员校验不应为 GROUP BY 建临时排序树:\n%s", plan)
	}
	if err := validateSourceMembership(ctx, db, "cat", "ovr"); err != nil {
		t.Fatalf("匹配的合成成员集合应通过生产校验: %v", err)
	}
}
