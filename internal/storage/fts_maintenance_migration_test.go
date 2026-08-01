package storage

import (
	"context"
	"database/sql"
	"io/fs"
	"strings"
	"testing"
)

func openCatalogThroughV20(t *testing.T) (*sql.DB, migration) {
	t.Helper()
	db, v20 := openCatalogThroughV19(t)
	if err := applyMigration(context.Background(), db, v20); err != nil {
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
	for _, item := range migrations {
		if item.version == 21 {
			return db, item
		}
	}
	t.Fatal("缺少 catalog v21 migration")
	return nil, migration{}
}

func TestFTSMaintenanceMigrationPinsBoundedMergePolicy(t *testing.T) {
	db, v21 := openCatalogThroughV20(t)
	defer db.Close()
	if err := applyMigration(context.Background(), db, v21); err != nil {
		t.Fatal(err)
	}
	var automerge, threshold int
	if err := db.QueryRow("SELECT v FROM work_search_config WHERE k='automerge'").Scan(&automerge); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT v FROM work_search_config WHERE k='crisismerge'").Scan(&threshold); err != nil {
		t.Fatal(err)
	}
	if automerge != 4 || threshold != 16 {
		t.Fatalf("FTS policy automerge=%d crisismerge=%d", automerge, threshold)
	}
	for table, index := range map[string]string{
		"work_projections":            "work_projections_overlay_gc_idx",
		"media_projections":           "media_projections_overlay_gc_idx",
		"aggregate_cover_projections": "aggregate_cover_projections_overlay_gc_idx",
	} {
		var columns string
		if err := db.QueryRow(`SELECT group_concat(name, ',') FROM pragma_index_info(?)`, index).Scan(&columns); err != nil {
			t.Fatal(err)
		}
		if columns != "overlay_revision_id" {
			t.Fatalf("%s 的 %s columns=%q", table, index, columns)
		}
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	planRows, err := db.Query(`EXPLAIN QUERY PLAN
DELETE FROM overlay_projection_revisions WHERE catalog_revision_id=? AND overlay_revision_id=?`, "catalog", "overlay")
	if err != nil {
		t.Fatal(err)
	}
	defer planRows.Close()
	wanted := map[string]bool{
		"work_projections":            false,
		"media_projections":           false,
		"aggregate_cover_projections": false,
	}
	for planRows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := planRows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		for table := range wanted {
			if strings.Contains(detail, "SCAN "+table) {
				t.Fatalf("overlay root DELETE 仍全扫 %s: %s", table, detail)
			}
			if strings.Contains(detail, "SEARCH "+table) {
				wanted[table] = true
			}
		}
	}
	if err := planRows.Err(); err != nil {
		t.Fatal(err)
	}
	for table, found := range wanted {
		if !found {
			t.Fatalf("overlay root DELETE 计划没有索引搜索 %s", table)
		}
	}
}
