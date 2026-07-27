package storage

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"
)

func openCatalogThroughV17(t *testing.T) (*sql.DB, migration) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "catalog.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(ctx, `CREATE TABLE gallery_schema_migrations (
version INTEGER PRIMARY KEY NOT NULL, name TEXT NOT NULL, sha256 TEXT NOT NULL,
applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))) STRICT`); err != nil {
		t.Fatal(err)
	}
	sub, err := fs.Sub(migrationFiles, "migrations/catalog")
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := readMigrations(sub)
	if err != nil {
		t.Fatal(err)
	}
	v18Index := -1
	for index, item := range migrations {
		if item.version == 18 {
			v18Index = index
			break
		}
	}
	if v18Index < 1 || migrations[v18Index-1].version != 17 {
		t.Fatalf("测试要求 v18 的直接前序为 v17: %+v", migrations)
	}
	for _, item := range migrations[:v18Index] {
		if err := applyMigration(ctx, db, item); err != nil {
			t.Fatal(err)
		}
	}
	return db, migrations[v18Index]
}

func seedV17WorkProjection(t *testing.T, db *sql.DB, workID, normalized string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO work_projections
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id, title, creator,
 tags_json, filenames_text, normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text,
 sort_title_key, hidden, favorite, progress, search_title_norm, search_creator_norm, search_tags_norm,
 search_filenames_norm, published_at_ns)
VALUES ('cat', 'ovr', ?, 'src', ?, 'lib', ?, 'Creator', '["tag"]', '["file.jpg"]', ?, 'cjk', 'latin',
 ?, 0, 1, 0.5, ?, 'creator', 'tag', 'file.jpg', 123)`, workID, workID, workID, normalized,
		"sort-"+workID, normalized); err != nil {
		t.Fatal(err)
	}
}

func insertV17SearchDocument(t *testing.T, db *sql.DB, workID, normalized string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO work_search
(catalog_revision_id, overlay_revision_id, work_id,
 normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text)
VALUES ('cat', 'ovr', ?, ?, 'cjk', 'latin')`, workID, normalized); err != nil {
		t.Fatal(err)
	}
}

func TestSearchCandidateMigrationUpgradesPopulatedV17CatalogAndSurvivesVacuum(t *testing.T) {
	db, v18 := openCatalogThroughV17(t)
	if _, err := db.Exec(`INSERT INTO catalog_revisions VALUES ('cat', 'job', 'src', 'published', 1, 2);
INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, published_at)
VALUES ('ovr', 'cat', 0, 'published', 1, 2)`); err != nil {
		t.Fatal(err)
	}
	for _, workID := range []string{"work-a", "work-b"} {
		seedV17WorkProjection(t, db, workID, "normalized-"+workID)
		insertV17SearchDocument(t, db, workID, "normalized-"+workID)
	}
	before := searchRowIDsByWork(t, db)
	if err := applyMigration(context.Background(), db, v18); err != nil {
		t.Fatal(err)
	}

	assertSearchCandidateMigrationState(t, db, before)
	if _, err := db.Exec("VACUUM"); err != nil {
		t.Fatal(err)
	}
	assertSearchCandidateMigrationState(t, db, before)
}

func searchRowIDsByWork(t *testing.T, db *sql.DB) map[string]int64 {
	t.Helper()
	rows, err := db.Query(`SELECT work_id, rowid FROM work_search ORDER BY work_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[string]int64{}
	for rows.Next() {
		var workID string
		var rowID int64
		if err := rows.Scan(&workID, &rowID); err != nil {
			t.Fatal(err)
		}
		result[workID] = rowID
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func assertSearchCandidateMigrationState(t *testing.T, db *sql.DB, before map[string]int64) {
	t.Helper()
	var count, version, migrationCount, cascadeColumns int
	if err := db.QueryRow(`SELECT count(*) FROM work_search_candidates c
JOIN work_search s ON s.rowid=c.search_rowid
JOIN work_projections w
  ON w.catalog_revision_id=c.catalog_revision_id
 AND w.overlay_revision_id=c.overlay_revision_id
 AND w.work_id=c.work_id
WHERE c.search_rowid=s.rowid
  AND c.normalized_original_text=w.normalized_original_text
  AND s.normalized_original_text=w.normalized_original_text
  AND c.sort_title_key=w.sort_title_key
  AND c.favorite=w.favorite AND c.progress=w.progress`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(before) {
		t.Fatalf("迁移后三方一致行=%d want=%d", count, len(before))
	}
	for workID, wantRowID := range before {
		var candidateRowID, ftsRowID int64
		if err := db.QueryRow(`SELECT c.search_rowid, s.rowid FROM work_search_candidates c
JOIN work_search s ON s.rowid=c.search_rowid WHERE c.work_id=?`, workID).Scan(&candidateRowID, &ftsRowID); err != nil {
			t.Fatal(err)
		}
		if candidateRowID != wantRowID || ftsRowID != wantRowID {
			t.Fatalf("%s rowid 漂移: candidate=%d fts=%d want=%d", workID, candidateRowID, ftsRowID, wantRowID)
		}
	}
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM gallery_schema_migrations WHERE version=18`).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM pragma_foreign_key_list('work_search_candidates')
WHERE "table"='work_projections' AND on_delete='CASCADE'`).Scan(&cascadeColumns); err != nil {
		t.Fatal(err)
	}
	if version != 18 || migrationCount != 1 || cascadeColumns != 3 {
		t.Fatalf("迁移登记/FK 错误: version=%d migration=%d cascadeColumns=%d", version, migrationCount, cascadeColumns)
	}
	var integrity string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity_check=%q err=%v", integrity, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&count); err != nil || count != 0 {
		t.Fatalf("foreign_key_check=%d err=%v", count, err)
	}
}

func TestSearchCandidateMigrationRejectsInvalidV17Catalog(t *testing.T) {
	tests := []struct {
		name string
		seed func(*testing.T, *sql.DB)
	}{
		{name: "missing-fts", seed: func(t *testing.T, db *sql.DB) {
			seedV17WorkProjection(t, db, "work", "good")
		}},
		{name: "orphan-fts", seed: func(t *testing.T, db *sql.DB) {
			insertV17SearchDocument(t, db, "orphan", "good")
		}},
		{name: "text-drift", seed: func(t *testing.T, db *sql.DB) {
			seedV17WorkProjection(t, db, "work", "good")
			insertV17SearchDocument(t, db, "work", "drift")
		}},
		{name: "equal-count-missing-and-orphan", seed: func(t *testing.T, db *sql.DB) {
			seedV17WorkProjection(t, db, "work", "good")
			insertV17SearchDocument(t, db, "other", "good")
		}},
		{name: "duplicate-business-key", seed: func(t *testing.T, db *sql.DB) {
			seedV17WorkProjection(t, db, "work", "good")
			insertV17SearchDocument(t, db, "work", "good")
			insertV17SearchDocument(t, db, "work", "good")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, v18 := openCatalogThroughV17(t)
			if _, err := db.Exec(`INSERT INTO catalog_revisions VALUES ('cat', 'job', 'src', 'published', 1, 2);
INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, published_at)
VALUES ('ovr', 'cat', 0, 'published', 1, 2)`); err != nil {
				t.Fatal(err)
			}
			test.seed(t, db)
			var beforeWorks, beforeSearch int
			_ = db.QueryRow("SELECT count(*) FROM work_projections").Scan(&beforeWorks)
			_ = db.QueryRow("SELECT count(*) FROM work_search").Scan(&beforeSearch)
			if err := applyMigration(context.Background(), db, v18); err == nil {
				t.Fatal("v18 接受了不完整或漂移的历史搜索投影")
			}
			var version, migrationCount, tableCount, afterWorks, afterSearch int
			_ = db.QueryRow("PRAGMA user_version").Scan(&version)
			_ = db.QueryRow("SELECT count(*) FROM gallery_schema_migrations WHERE version=18").Scan(&migrationCount)
			_ = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='work_search_candidates'`).Scan(&tableCount)
			_ = db.QueryRow("SELECT count(*) FROM work_projections").Scan(&afterWorks)
			_ = db.QueryRow("SELECT count(*) FROM work_search").Scan(&afterSearch)
			if version != 17 || migrationCount != 0 || tableCount != 0 || afterWorks != beforeWorks || afterSearch != beforeSearch {
				t.Fatalf("失败迁移未原子回滚: version=%d migration=%d table=%d works=%d/%d search=%d/%d",
					version, migrationCount, tableCount, afterWorks, beforeWorks, afterSearch, beforeSearch)
			}
		})
	}
}
