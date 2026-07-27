package query_test

import (
	"database/sql"
	"testing"
)

// insertSearchFixtureDocument 复刻生产写入顺序：普通表先分配稳定的 search_rowid，
// FTS5 再显式复用该 rowid。测试夹具不得绕过候选投影，否则搜索路径会得到假阴性。
func insertSearchFixtureDocument(t *testing.T, db *sql.DB, catalogRevisionID, overlayRevisionID, workID string) {
	t.Helper()
	result, err := db.Exec(`INSERT INTO work_search_candidates
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id,
 tags_json, normalized_original_text, sort_title_key, published_at_ns, hidden, favorite, progress,
 search_title_norm, search_creator_norm, search_tags_norm, search_filenames_norm)
SELECT catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id,
       tags_json, normalized_original_text, sort_title_key, published_at_ns, hidden, favorite, progress,
       search_title_norm, search_creator_norm, search_tags_norm, search_filenames_norm
FROM work_projections
WHERE catalog_revision_id=? AND overlay_revision_id=? AND work_id=?`,
		catalogRevisionID, overlayRevisionID, workID)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("搜索候选夹具写入行数=%d want=1", rows)
	}
	searchRowID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO work_search
(rowid, catalog_revision_id, overlay_revision_id, work_id,
 normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text)
SELECT ?, w.catalog_revision_id, w.overlay_revision_id, w.work_id,
       w.normalized_original_text, w.cjk_bigram_token_text, w.latin_trigram_token_text
FROM work_projections w
WHERE w.catalog_revision_id=? AND w.overlay_revision_id=? AND w.work_id=?`,
		searchRowID, catalogRevisionID, overlayRevisionID, workID); err != nil {
		t.Fatal(err)
	}
}
