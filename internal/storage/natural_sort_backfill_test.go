package storage

import (
	"context"
	"testing"

	"github.com/RecRivenVI/gallery/internal/querytext"
)

func TestNaturalSortKeyEncodingBackfillIsAtomicAndIdempotent(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	db := store.Catalog.db

	if _, err := db.ExecContext(ctx, `
INSERT INTO catalog_revisions VALUES ('cat', 'job', 'src', 'staging', 1, NULL);
INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at)
VALUES ('ovr', 'cat', 0, 'staging', 1);
INSERT INTO work_projections
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id, title, creator,
 tags_json, filenames_text, normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text,
 sort_title_key, hidden)
VALUES ('cat', 'ovr', 'work', 'src', 'key', 'lib', 'ab', '', '[]', '', '', '', '', 'OLD', 0);
INSERT INTO work_search_candidates
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id,
 tags_json, normalized_original_text, sort_title_key, published_at_ns, hidden, favorite, progress,
 search_title_norm, search_creator_norm, search_tags_norm, search_filenames_norm)
SELECT catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id,
       tags_json, normalized_original_text, sort_title_key, published_at_ns, hidden, favorite, progress,
       search_title_norm, search_creator_norm, search_tags_norm, search_filenames_norm
FROM work_projections WHERE catalog_revision_id='cat' AND overlay_revision_id='ovr' AND work_id='work';
INSERT INTO work_search
(rowid, catalog_revision_id, overlay_revision_id, work_id,
 normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text)
SELECT c.search_rowid, w.catalog_revision_id, w.overlay_revision_id, w.work_id,
       w.normalized_original_text, w.cjk_bigram_token_text, w.latin_trigram_token_text
FROM work_search_candidates c JOIN work_projections w
  ON w.catalog_revision_id=c.catalog_revision_id
 AND w.overlay_revision_id=c.overlay_revision_id
 AND w.work_id=c.work_id;
INSERT INTO creator_projections
(catalog_revision_id, overlay_revision_id, creator_id, name, sort_name_key)
VALUES ('cat', 'ovr', 'creator', 'Alice', 'OLD');
UPDATE gallery_catalog_meta SET value='1' WHERE key='natural_sort_key_encoding';`); err != nil {
		t.Fatal(err)
	}

	if err := ensureNaturalSortKeyEncoding(ctx, db); err != nil {
		t.Fatal(err)
	}
	var workKey, candidateKey, creatorKey, version string
	if err := db.QueryRowContext(ctx, "SELECT sort_title_key FROM work_projections WHERE work_id='work'").Scan(&workKey); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT sort_name_key FROM creator_projections WHERE creator_id='creator'").Scan(&creatorKey); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT sort_title_key FROM work_search_candidates WHERE work_id='work'").Scan(&candidateKey); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx,
		"SELECT value FROM gallery_catalog_meta WHERE key='natural_sort_key_encoding'").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if workKey != querytext.NaturalSortKey("ab") || candidateKey != workKey || creatorKey != querytext.NaturalSortKey("Alice") || version != "2" {
		t.Fatalf("排序键回填不完整: work=%q candidate=%q creator=%q version=%q", workKey, candidateKey, creatorKey, version)
	}
	if err := ensureNaturalSortKeyEncoding(ctx, db); err != nil {
		t.Fatalf("重复回填应为 no-op: %v", err)
	}
}
