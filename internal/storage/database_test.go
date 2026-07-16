package storage

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
)

func openTestStore(t *testing.T) (*Store, appdirs.Dirs) {
	t.Helper()
	dirs := appdirs.UnderRoot(t.TempDir())
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, dirs
}

func TestIndependentWALMigrationsAndBackup(t *testing.T) {
	store, dirs := openTestStore(t)
	wantVersions := map[Role]int{RoleControl: 8, RoleCatalog: 5}
	for _, database := range []*Database{store.Control, store.Catalog} {
		var version int
		if err := database.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
			t.Fatal(err)
		}
		if version != wantVersions[database.role] {
			t.Fatalf("%s user_version = %d", database.role, version)
		}
	}

	backup := filepath.Join(dirs.State, "backup", "control.db")
	if err := store.Control.Backup(context.Background(), backup); err != nil {
		t.Fatal(err)
	}
	if err := verifyBackup(context.Background(), backup, RoleControl); err != nil {
		t.Fatal(err)
	}
	if err := store.Control.Backup(context.Background(), backup); err == nil {
		t.Fatal("备份静默覆盖了已有文件")
	}

	backupDB, err := sql.Open("sqlite", "file:"+filepath.ToSlash(backup)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer backupDB.Close()
	if _, err := backupDB.Exec("SELECT * FROM gallery_catalog_meta"); err == nil {
		t.Fatal("control 备份混入 catalog 生命周期")
	}
}

func TestMigrationChecksumDetectsHistoryRewrite(t *testing.T) {
	store, dirs := openTestStore(t)
	if _, err := store.Control.db.Exec("UPDATE gallery_schema_migrations SET sha256 = 'tampered' WHERE version = 1"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := Open(context.Background(), dirs)
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeMigrationFailed {
		t.Fatalf("migration 篡改错误 = %v", err)
	}
}

func TestQuerySnapshotMigrationUpgradesPopulatedV2Catalog(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "catalog.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
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
	for _, item := range migrations[:2] {
		if err := applyMigration(ctx, db, item); err != nil {
			t.Fatal(err)
		}
	}
	_, err = db.ExecContext(ctx, `
INSERT INTO catalog_revisions VALUES ('cat_old', 'job_old', 'src_old', 'published', 1, 2);
INSERT INTO overlay_projection_revisions VALUES ('ovr_old', 'cat_old', 0, 'published', 1, 2);
INSERT INTO query_publications VALUES ('qpub_old', 'cat_old', 'ovr_old', 'job_old', 0, 2);
INSERT INTO active_query_publication VALUES (1, 'qpub_old');
INSERT INTO source_works VALUES ('cat_old', 'src_old', 'work-key', '旧标题');
INSERT INTO work_projections VALUES ('cat_old', 'ovr_old', 'wrk_old', 'src_old', 'work-key', '旧标题');`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := openDatabase(ctx, RoleCatalog, path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	var title, normalized string
	if err := upgraded.db.QueryRowContext(ctx, `SELECT w.title, s.normalized_original_text
FROM work_projections w JOIN work_search s
ON s.catalog_revision_id=w.catalog_revision_id AND s.overlay_revision_id=w.overlay_revision_id AND s.work_id=w.work_id
WHERE w.work_id='wrk_old'`).Scan(&title, &normalized); err != nil {
		t.Fatal(err)
	}
	if title != "旧标题" || normalized == "" {
		t.Fatalf("升级未保留并索引既有 projection: title=%q normalized=%q", title, normalized)
	}
	if _, err := upgraded.db.ExecContext(ctx, `
INSERT INTO catalog_revisions VALUES ('cat_new', 'job_new', 'src_new', 'published', 3, 4);
INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, published_at)
VALUES ('ovr_new', 'cat_new', 0, 'published', 3, 4);
INSERT INTO query_publications VALUES ('qpub_bad', 'cat_old', 'ovr_new', 'job_bad', 0, 4)`); err == nil {
		t.Fatal("schema 接受了不合法的 catalog/overlay revision 组合")
	}
}

func TestOverlayMigrationUpgradesPopulatedV6Control(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE gallery_schema_migrations (
version INTEGER PRIMARY KEY NOT NULL, name TEXT NOT NULL, sha256 TEXT NOT NULL,
applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))) STRICT`); err != nil {
		t.Fatal(err)
	}
	sub, err := fs.Sub(migrationFiles, "migrations/control")
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := readMigrations(sub)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range migrations[:6] {
		if err := applyMigration(ctx, db, item); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO jobs
(job_id, job_type, source_id, created_by, status, stage, created_at, updated_at)
VALUES ('job_existing', 'scan', NULL, 'owner', 'completed', 'completed', 1, 2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO canonical_works
(work_id, title, created_at) VALUES ('wrk_existing', '既有作品', 1)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := openDatabase(ctx, RoleControl, path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	var jobType string
	var target sql.NullInt64
	if err := upgraded.db.QueryRowContext(ctx, `SELECT job_type, target_watermark
FROM jobs WHERE job_id='job_existing'`).Scan(&jobType, &target); err != nil {
		t.Fatal(err)
	}
	if jobType != "scan" || target.Valid {
		t.Fatalf("既有 Job 升级错误: type=%s target=%v", jobType, target)
	}
	if _, err := upgraded.db.ExecContext(ctx, `INSERT INTO work_overlays
(work_id, fact_watermark, projection_status, updated_at)
VALUES ('wrk_existing', 1, 'published', 2)`); err != nil {
		t.Fatalf("升级后 Overlay 表不可写: %v", err)
	}
}

func TestFailedMigrationRollsBackItsTransaction(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "rollback.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE gallery_schema_migrations (
        version INTEGER PRIMARY KEY, name TEXT NOT NULL, sha256 TEXT NOT NULL,
        applied_at TEXT NOT NULL DEFAULT 'test') STRICT`); err != nil {
		t.Fatal(err)
	}
	item := migration{version: 2, name: "broken", sha256: "test", sql: "CREATE TABLE must_rollback (id INTEGER); INVALID SQL;"}
	if err := applyMigration(context.Background(), db, item); err == nil {
		t.Fatal("损坏 migration 意外成功")
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name = 'must_rollback'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("失败 migration 留下了部分表")
	}
}

func TestMigrationFileNamesAreStrict(t *testing.T) {
	_, err := readMigrations(fs.FS(os.DirFS(t.TempDir())))
	if err != nil {
		t.Fatal(err)
	}
}
