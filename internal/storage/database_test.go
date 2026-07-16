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
	wantVersions := map[Role]int{RoleControl: 3, RoleCatalog: 1}
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
