package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	wantVersions := map[Role]int{RoleControl: 20, RoleCatalog: 14}
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

func TestCatalogRevisionSourcesMigrationUpgradesPopulatedV11Catalog(t *testing.T) {
	prepareV11 := func(t *testing.T, projections string) string {
		t.Helper()
		ctx := context.Background()
		path := filepath.Join(t.TempDir(), "catalog.db")
		db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(ON)")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `CREATE TABLE gallery_schema_migrations (
version INTEGER PRIMARY KEY NOT NULL, name TEXT NOT NULL, sha256 TEXT NOT NULL,
applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))) STRICT`); err != nil {
			db.Close()
			t.Fatal(err)
		}
		sub, err := fs.Sub(migrationFiles, "migrations/catalog")
		if err != nil {
			db.Close()
			t.Fatal(err)
		}
		migrations, err := readMigrations(sub)
		if err != nil {
			db.Close()
			t.Fatal(err)
		}
		for _, item := range migrations[:11] {
			if err := applyMigration(ctx, db, item); err != nil {
				db.Close()
				t.Fatal(err)
			}
		}
		if _, err := db.ExecContext(ctx, `
INSERT INTO catalog_revisions VALUES ('cat_old', 'job_old', 'src_a', 'published', 1, 2);
INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, published_at)
VALUES ('ovr_old', 'cat_old', 0, 'published', 1, 2);`+projections); err != nil {
			db.Close()
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("backfills-exact-source-library-membership", func(t *testing.T) {
		path := prepareV11(t, `
INSERT INTO work_projections
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id, title, creator,
 tags_json, filenames_text, normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text,
 sort_title_key, hidden)
VALUES
('cat_old', 'ovr_old', 'work-a1', 'src_a', 'a1', 'lib_a', 'A1', '', '[]', '', 'a1', '', '', 'a1', 0),
('cat_old', 'ovr_old', 'work-a2', 'src_a', 'a2', 'lib_a', 'A2', '', '[]', '', 'a2', '', '', 'a2', 0),
('cat_old', 'ovr_old', 'work-b1', 'src_b', 'b1', 'lib_b', 'B1', '', '[]', '', 'b1', '', '', 'b1', 0);`)
		upgraded, err := openDatabase(context.Background(), RoleCatalog, path)
		if err != nil {
			t.Fatal(err)
		}
		defer upgraded.Close()
		var version, count int
		if err := upgraded.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
			t.Fatal(err)
		}
		if err := upgraded.db.QueryRow(`SELECT count(*) FROM catalog_revision_sources`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if version != 14 || count != 2 {
			t.Fatalf("v11 升级结果: version=%d membership=%d", version, count)
		}
		var libraryID string
		if err := upgraded.db.QueryRow(`SELECT library_id FROM catalog_revision_sources
WHERE catalog_revision_id='cat_old' AND source_id='src_b'`).Scan(&libraryID); err != nil {
			t.Fatal(err)
		}
		if libraryID != "lib_b" {
			t.Fatalf("src_b library=%q", libraryID)
		}
	})

	t.Run("rejects-ambiguous-historical-library", func(t *testing.T) {
		path := prepareV11(t, `
INSERT INTO work_projections
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id, title, creator,
 tags_json, filenames_text, normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text,
 sort_title_key, hidden)
VALUES
('cat_old', 'ovr_old', 'work-a1', 'src_a', 'a1', 'lib_a', 'A1', '', '[]', '', 'a1', '', '', 'a1', 0),
('cat_old', 'ovr_old', 'work-a2', 'src_a', 'a2', 'lib_other', 'A2', '', '[]', '', 'a2', '', '', 'a2', 0);`)
		_, err := openDatabase(context.Background(), RoleCatalog, path)
		var structured *fault.Error
		if !errors.As(err, &structured) || structured.Code != fault.CodeMigrationFailed {
			t.Fatalf("有歧义 membership 升级错误 = %v", err)
		}
	})
}

// TestMediaProjectionMTimeMigrationUpgradesPopulatedV12Catalog 验证 00013 迁移把发布时刻
// 的 mtime 证据从 source_media 精确回填进 media_projections：(catalog_revision_id,
// source_id, source_key) 是 source_media 的主键，因此回填必须是一对一映射；没有对应
// source_media 行的历史投影保留 0，读取端据此退回 size 与整文件 digest 复算，不伪造证据。
func TestMediaProjectionMTimeMigrationUpgradesPopulatedV12Catalog(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "catalog.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE gallery_schema_migrations (
version INTEGER PRIMARY KEY NOT NULL, name TEXT NOT NULL, sha256 TEXT NOT NULL,
applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))) STRICT`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	sub, err := fs.Sub(migrationFiles, "migrations/catalog")
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	migrations, err := readMigrations(sub)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	for _, item := range migrations[:12] {
		if err := applyMigration(ctx, db, item); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO catalog_revisions VALUES ('cat_old', 'job_old', 'src_a', 'published', 1, 2);
INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, published_at)
VALUES ('ovr_old', 'cat_old', 0, 'published', 1, 2);
INSERT INTO source_media
(catalog_revision_id, source_id, source_key, work_source_key, relative_path, media_kind, mime_type,
 size_bytes, rule_key, mtime_ns)
VALUES ('cat_old', 'src_a', 'media-a', 'work-a', 'a/1.jpg', 'image', 'image/jpeg', 11, 'rule', 1730000000123456789);
INSERT INTO media_projections
(catalog_revision_id, overlay_revision_id, media_id, work_id, source_id, source_key, relative_path,
 media_kind, mime_type, size_bytes, algorithm, digest, location_status, ordinal, base_ordinal)
VALUES
('cat_old', 'ovr_old', 'med-a', 'work-a', 'src_a', 'media-a', 'a/1.jpg', 'image', 'image/jpeg', 11,
 'sha256-v1', '`+strings.Repeat("a", 64)+`', 'present', 0, 0),
('cat_old', 'ovr_old', 'med-orphan', 'work-a', 'src_a', 'media-missing', 'a/2.jpg', 'image', 'image/jpeg', 12,
 'sha256-v1', '`+strings.Repeat("b", 64)+`', 'present', 1, 1);`); err != nil {
		db.Close()
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
	var version int
	if err := upgraded.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 14 {
		t.Fatalf("v12 升级后 user_version = %d", version)
	}
	var backfilled, orphan int64
	if err := upgraded.db.QueryRow(`SELECT mtime_ns FROM media_projections WHERE media_id='med-a'`).Scan(&backfilled); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.db.QueryRow(`SELECT mtime_ns FROM media_projections WHERE media_id='med-orphan'`).Scan(&orphan); err != nil {
		t.Fatal(err)
	}
	if backfilled != 1730000000123456789 {
		t.Fatalf("mtime 未从 source_media 精确回填: got=%d", backfilled)
	}
	if orphan != 0 {
		t.Fatalf("缺少 source_media 对应行的历史投影必须保留 0，不得伪造证据: got=%d", orphan)
	}
}

// TestMediaVerificationMigrationUpgradesPopulatedV8Catalog 验证 00009 迁移把历史上借用
// media_projections.location_status='located_unverified' 表达的行正确拆分为独立的
// content_verification_state（位置本身仍是 present）与 verified_at（从 source_media 的
// last_confirmed_at 回填已确认媒体的真实确认时间），不产生伪造时间。
func TestMediaVerificationMigrationUpgradesPopulatedV8Catalog(t *testing.T) {
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
	for _, item := range migrations[:8] {
		if err := applyMigration(ctx, db, item); err != nil {
			t.Fatal(err)
		}
	}
	_, err = db.ExecContext(ctx, `
INSERT INTO catalog_revisions VALUES ('cat_old', 'job_old', 'src_old', 'published', 1, 2);
INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, published_at)
VALUES ('ovr_old', 'cat_old', 0, 'published', 1, 2);
INSERT INTO query_publications VALUES ('qpub_old', 'cat_old', 'ovr_old', 'job_old', 0, 2);
INSERT INTO active_query_publication VALUES (1, 'qpub_old');
INSERT INTO source_media
(catalog_revision_id, source_id, source_key, work_source_key, relative_path, media_kind, mime_type, size_bytes,
 rule_key, mtime_ns, platform_identity_kind, platform_identity_value, container_signature, content_verification_state,
 last_confirmed_algorithm, last_confirmed_digest, last_confirmed_at)
VALUES
('cat_old', 'src_old', 'work-key/med-unverified', 'work-key', 'work-key/one.bin', 'image', 'application/octet-stream', 100,
 'r1', 0, '', '', '', 'located_unverified', '', '', NULL),
('cat_old', 'src_old', 'work-key/med-verified', 'work-key', 'work-key/two.bin', 'image', 'application/octet-stream', 200,
 'r2', 0, '', '', '', 'content_verified', 'sha256-v1', '11223344556677889900112233445566778899001122334455667788990011', 1700000000);
INSERT INTO media_projections
(catalog_revision_id, overlay_revision_id, media_id, work_id, source_id, source_key, relative_path,
 media_kind, mime_type, size_bytes, algorithm, digest, location_status, ordinal, hidden, base_ordinal)
VALUES
('cat_old', 'ovr_old', 'med_unverified', 'wrk_old', 'src_old', 'work-key/med-unverified', 'work-key/one.bin',
 'image', 'application/octet-stream', 100, '', '', 'located_unverified', 0, 0, 0),
('cat_old', 'ovr_old', 'med_verified', 'wrk_old', 'src_old', 'work-key/med-verified', 'work-key/two.bin',
 'image', 'application/octet-stream', 200, 'sha256-v1', '11223344556677889900112233445566778899001122334455667788990011', 'present', 1, 0, 1);`)
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

	var unverifiedLocation, unverifiedState string
	var unverifiedAt sql.NullInt64
	if err := upgraded.db.QueryRowContext(ctx, `SELECT location_status, content_verification_state, verified_at
FROM media_projections WHERE media_id='med_unverified'`).Scan(&unverifiedLocation, &unverifiedState, &unverifiedAt); err != nil {
		t.Fatal(err)
	}
	if unverifiedLocation != "present" {
		t.Fatalf("借用 located_unverified 的 location_status 未拆分回 present: %q", unverifiedLocation)
	}
	if unverifiedState != "located_unverified" {
		t.Fatalf("content_verification_state 未从历史 location_status 迁移: %q", unverifiedState)
	}
	if unverifiedAt.Valid {
		t.Fatalf("located_unverified 媒体不应有 verified_at: %+v", unverifiedAt)
	}

	var verifiedLocation, verifiedState string
	var verifiedAt sql.NullInt64
	if err := upgraded.db.QueryRowContext(ctx, `SELECT location_status, content_verification_state, verified_at
FROM media_projections WHERE media_id='med_verified'`).Scan(&verifiedLocation, &verifiedState, &verifiedAt); err != nil {
		t.Fatal(err)
	}
	if verifiedLocation != "present" || verifiedState != "content_verified" {
		t.Fatalf("已确认媒体的位置/确认状态错误: location=%q state=%q", verifiedLocation, verifiedState)
	}
	if !verifiedAt.Valid || verifiedAt.Int64 != 1700000000 {
		t.Fatalf("verified_at 未从 source_media.last_confirmed_at 回填: %+v", verifiedAt)
	}
}

func TestRuleCoverProjectionMigrationUpgradesPopulatedV10Catalog(t *testing.T) {
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
	for _, item := range migrations[:10] {
		if err := applyMigration(ctx, db, item); err != nil {
			t.Fatal(err)
		}
	}
	_, err = db.ExecContext(ctx, `
INSERT INTO catalog_revisions VALUES ('cat_old', 'job_old', 'src_old', 'published', 1, 2);
INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, published_at)
VALUES ('ovr_old', 'cat_old', 0, 'published', 1, 2);
INSERT INTO query_publications VALUES ('qpub_old', 'cat_old', 'ovr_old', 'job_old', 0, 2);
INSERT INTO active_query_publication VALUES (1, 'qpub_old');
INSERT INTO source_works
(catalog_revision_id, source_id, source_key, title, creator, tags_json, filenames_text)
VALUES ('cat_old', 'src_old', 'work-key', '旧标题', '', '[]', '["01.jpg","02.jpg"]');
INSERT INTO work_projections
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id, title, creator,
 tags_json, filenames_text, normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text,
 sort_title_key, hidden, favorite, progress, search_title_norm, search_creator_norm, search_tags_norm,
 search_filenames_norm)
VALUES ('cat_old', 'ovr_old', 'work-old', 'src_old', 'work-key', 'lib-old', '旧标题', '',
 '[]', '["01.jpg","02.jpg"]', '旧标题', '', '', '旧标题', 0, 0, 0, '旧标题', '', '', '');
INSERT INTO media_projections
(catalog_revision_id, overlay_revision_id, media_id, work_id, source_id, source_key, relative_path,
 media_kind, mime_type, size_bytes, algorithm, digest, location_status, ordinal, hidden, base_ordinal)
VALUES
('cat_old', 'ovr_old', 'media-first', 'work-old', 'src_old', 'work-key/01.jpg', 'work-key/01.jpg',
 'image', 'image/jpeg', 1, 'sha256-v1', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'present', 0, 0, 0),
('cat_old', 'ovr_old', 'media-custom', 'work-old', 'src_old', 'work-key/02.jpg', 'work-key/02.jpg',
 'image', 'image/jpeg', 1, 'sha256-v1', 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 'present', -1, 0, 1);`)
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
	var sourceKey, ruleCover, effectiveCover string
	if err := upgraded.db.QueryRowContext(ctx, `SELECT sw.rule_cover_media_source_key,
w.rule_cover_media_id, w.cover_media_id
FROM source_works sw JOIN work_projections w
 ON w.catalog_revision_id=sw.catalog_revision_id AND w.source_id=sw.source_id AND w.source_key=sw.source_key
WHERE w.work_id='work-old'`).Scan(&sourceKey, &ruleCover, &effectiveCover); err != nil {
		t.Fatal(err)
	}
	if sourceKey != "work-key/01.jpg" || ruleCover != "media-first" || effectiveCover != "media-custom" {
		t.Fatalf("旧封面语义迁移错误: source=%q rule=%q effective=%q", sourceKey, ruleCover, effectiveCover)
	}
	var negative int
	if err := upgraded.db.QueryRowContext(ctx, `SELECT count(*) FROM media_projections
WHERE ordinal<0 OR base_ordinal<0`).Scan(&negative); err != nil {
		t.Fatal(err)
	}
	if negative != 0 {
		t.Fatalf("迁移后仍有负 ordinal: %d", negative)
	}
	var restoredOrdinal int
	if err := upgraded.db.QueryRowContext(ctx, `SELECT ordinal FROM media_projections
WHERE media_id='media-custom'`).Scan(&restoredOrdinal); err != nil {
		t.Fatal(err)
	}
	if restoredOrdinal != 1 {
		t.Fatalf("旧自定义封面媒体未恢复 base ordinal: %d", restoredOrdinal)
	}
}

func TestRuleCoverProjectionMigrationHasBoundedPlanAtSyntheticScale(t *testing.T) {
	const (
		workCount    = 1200
		mediaPerWork = 3
	)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "catalog-scale.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// TEMP 映射与 EXPLAIN 必须在同一 SQLite connection 上；正式 migration 本身也在
	// 单事务的同一 connection 内执行。
	db.SetMaxOpenConns(1)
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
	for _, item := range migrations[:10] {
		if err := applyMigration(ctx, db, item); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO catalog_revisions VALUES ('cat_scale', 'job_scale', 'src_scale', 'published', 1, 2);
INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, published_at)
VALUES ('ovr_scale', 'cat_scale', 0, 'published', 1, 2);`); err != nil {
		t.Fatal(err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	sourceStatement, err := tx.PrepareContext(ctx, `INSERT INTO source_works
(catalog_revision_id, source_id, source_key, title) VALUES ('cat_scale', 'src_scale', ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	workStatement, err := tx.PrepareContext(ctx, `INSERT INTO work_projections
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, title)
VALUES ('cat_scale', 'ovr_scale', ?, 'src_scale', ?, ?)`)
	if err != nil {
		_ = sourceStatement.Close()
		_ = tx.Rollback()
		t.Fatal(err)
	}
	mediaStatement, err := tx.PrepareContext(ctx, `INSERT INTO media_projections
(catalog_revision_id, overlay_revision_id, media_id, work_id, source_id, source_key, relative_path,
 media_kind, mime_type, size_bytes, algorithm, digest, location_status, ordinal, base_ordinal)
VALUES ('cat_scale', 'ovr_scale', ?, ?, 'src_scale', ?, ?,
 'image', 'image/jpeg', 1, 'sha256-v1', ?, 'present', ?, ?)`)
	if err != nil {
		_ = workStatement.Close()
		_ = sourceStatement.Close()
		_ = tx.Rollback()
		t.Fatal(err)
	}
	for workIndex := 0; workIndex < workCount; workIndex++ {
		workID := fmt.Sprintf("work-%06d", workIndex)
		workSourceKey := fmt.Sprintf("source-work-%06d", workIndex)
		if _, err := sourceStatement.ExecContext(ctx, workSourceKey, workID); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if _, err := workStatement.ExecContext(ctx, workID, workSourceKey, workID); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		for mediaIndex := 0; mediaIndex < mediaPerWork; mediaIndex++ {
			mediaID := fmt.Sprintf("media-%06d-%02d", workIndex, mediaIndex)
			mediaSourceKey := fmt.Sprintf("%s/%02d.jpg", workSourceKey, mediaIndex)
			digest := fmt.Sprintf("%064x", workIndex*mediaPerWork+mediaIndex+1)
			ordinal := mediaIndex
			if workIndex%10 == 0 && mediaIndex == 1 {
				ordinal = -1
			}
			if _, err := mediaStatement.ExecContext(ctx, mediaID, workID, mediaSourceKey, mediaSourceKey,
				digest, ordinal, mediaIndex); err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
		}
	}
	_ = mediaStatement.Close()
	_ = workStatement.Close()
	_ = sourceStatement.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, `CREATE TEMP TABLE _gallery_rule_cover_source_map (
catalog_revision_id TEXT NOT NULL, source_id TEXT NOT NULL, work_source_key TEXT NOT NULL,
rule_cover_media_source_key TEXT NOT NULL,
PRIMARY KEY (catalog_revision_id, source_id, work_source_key)) WITHOUT ROWID`); err != nil {
		t.Fatal(err)
	}
	// queryPlan 取出被 explain 标记包围的那条语句的 EXPLAIN QUERY PLAN。标记写在 SQL 里，
	// 使「哪条语句必须保持有界」这件事在迁移文件本身可审计，而不是靠测试里复制一份 SQL。
	queryPlan := func(name string) string {
		t.Helper()
		planBegin, planEnd := "-- explain:"+name+"-begin", "-- explain:"+name+"-end"
		begin := strings.Index(migrations[10].sql, planBegin)
		end := strings.Index(migrations[10].sql, planEnd)
		if begin < 0 || end <= begin {
			t.Fatalf("00011 缺少可审计的 %s EXPLAIN 标记", name)
		}
		statement := strings.TrimSpace(migrations[10].sql[begin+len(planBegin) : end])
		statement = strings.TrimSuffix(statement, ";")
		rows, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+statement)
		if err != nil {
			t.Fatalf("%s 计划查询失败: %v", name, err)
		}
		var planLines []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			planLines = append(planLines, detail)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		return strings.Join(planLines, "\n")
	}

	// 结构性断言是本测试的正式门禁：它与机器速度无关，且精确表达 00011 必须维持的性质
	// ——每张放大表只被单侧扫描一次，另一侧走索引查找，任何一处退回逐 Work 的相关子查询
	// 都会把行访问量从 3,600 推到 4,320,000 量级。
	sourceMapPlan := queryPlan("rule-cover-source-map")
	indexedMediaByWork := strings.Contains(sourceMapPlan, "media_projections_work_idx")
	singleMediaScanWithWorkLookup := strings.Contains(sourceMapPlan, "SCAN m USING INDEX") && strings.Contains(sourceMapPlan, "SEARCH w USING INDEX")
	if strings.Contains(strings.ToUpper(sourceMapPlan), "CORRELATED") || (!indexedMediaByWork && !singleMediaScanWithWorkLookup) {
		t.Fatalf("00011 规则封面映射必须是单侧一次扫描加另一侧索引查找，不得退回相关子查询:\n%s", sourceMapPlan)
	}

	if _, err := db.ExecContext(ctx, `CREATE TEMP TABLE _gallery_rule_cover_work_map (
catalog_revision_id TEXT NOT NULL, overlay_revision_id TEXT NOT NULL, work_id TEXT NOT NULL,
rule_cover_media_id TEXT NOT NULL,
PRIMARY KEY (catalog_revision_id, overlay_revision_id, work_id)) WITHOUT ROWID`); err != nil {
		t.Fatal(err)
	}
	// 第二条放大语句：三表 join 把每个 Work 的规则封面 SourceKey 解析回 MediaID。它同样
	// 必须靠索引查找 media_projections，而不是为每个 Work 重扫一遍。
	workMapPlan := queryPlan("rule-cover-work-map")
	if strings.Contains(strings.ToUpper(workMapPlan), "CORRELATED") {
		t.Fatalf("00011 规则封面 MediaID 解析退回相关子查询:\n%s", workMapPlan)
	}
	if !strings.Contains(workMapPlan, "SEARCH m USING INDEX") {
		t.Fatalf("00011 规则封面 MediaID 解析未对 media_projections 使用索引查找:\n%s", workMapPlan)
	}
	if _, err := db.ExecContext(ctx, "DROP TABLE _gallery_rule_cover_work_map"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "DROP TABLE _gallery_rule_cover_source_map"); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	if err := applyMigration(ctx, db, migrations[10]); err != nil {
		t.Fatal(err)
	}
	duration := time.Since(started)
	// 这里只记录耗时，不作为通过条件。
	//
	// 原实现用固定 5 秒墙钟作断言，在共享 CI runner 上连续两次假阳性：同一段迁移在本机
	// NVMe 上约 0.04 秒，在 GitHub Windows runner 上实测 22.95 秒（run 30206908399）与
	// 3.51 秒（run 30207590271）。改用同机整表改写作校准也不成立——runner 上校准值
	// 4.48 毫秒与本机相当，而迁移仍要 3.51 秒：这段迁移的主要成本是窗口函数排序器的
	// 临时文件 I/O，不是主库页写入，因此主库改写无法代表它。
	//
	// 更根本的问题是，把一个不记录硬件、存储、缓存状态的绝对秒数写进可移植单元测试，
	// 本身就与《测试与发布门禁》「性能结论必须记录硬件、OS、存储、样本、缓存状态」
	// 相冲突。因此本测试保留并强化与机器无关的 EXPLAIN 结构断言作为正式门禁，迁移耗时
	// 的数值预算移交阶段 4 Reference Performance Gate 在登记环境上测定。
	t.Logf("00011 合成迁移: %d Work/%d Media 耗时 %s（记录值，不构成通过条件）",
		workCount, workCount*mediaPerWork, duration)
	var sourceCovers, ruleCovers, customCovers, negativeOrdinals int
	if err := db.QueryRowContext(ctx, `SELECT
(SELECT count(*) FROM source_works WHERE rule_cover_media_source_key<>''),
(SELECT count(*) FROM work_projections WHERE rule_cover_media_id<>''),
(SELECT count(*) FROM work_projections WHERE cover_media_id<>rule_cover_media_id),
(SELECT count(*) FROM media_projections WHERE ordinal<0 OR base_ordinal<0)`).Scan(
		&sourceCovers, &ruleCovers, &customCovers, &negativeOrdinals); err != nil {
		t.Fatal(err)
	}
	if sourceCovers != workCount || ruleCovers != workCount || customCovers != workCount/10 || negativeOrdinals != 0 {
		t.Fatalf("规模迁移结果错误: source=%d rule=%d custom=%d negative=%d",
			sourceCovers, ruleCovers, customCovers, negativeOrdinals)
	}
	t.Logf("00011 有界计划:\n规则封面映射:\n%s\n规则封面 MediaID 解析:\n%s", sourceMapPlan, workMapPlan)
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

func TestSchemaFreezeMigrationUpgradesPopulatedV15Control(t *testing.T) {
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
	for _, item := range migrations[:15] {
		if err := applyMigration(ctx, db, item); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO libraries (library_id, name, created_at)
VALUES ('lib_existing', '既有库', 1);
INSERT INTO sources (source_id, library_id, display_name, root_path, root_key, created_at)
VALUES ('src_existing', 'lib_existing', '既有 Source', 'synthetic-root', 'synthetic-root-key', 2);
INSERT INTO canonical_works (work_id, title, created_at)
VALUES ('wrk_existing', '既有作品', 3);
INSERT INTO binding_issues
(issue_id, source_id, entity_type, structure_kind, source_key, work_source_key,
 provider_id, external_id, code, candidate_fingerprint, candidate_count, status,
 version, created_at, updated_at)
VALUES ('issue_existing', 'src_existing', 'work', 'split', 'wk-parent', '',
 'provider', 'external', 'source_work_split', 'sf1:existing', 2, 'open',
 1, 4, 5);
INSERT INTO source_structure_decisions
(decision_id, issue_id, source_id, kind, action, fingerprint,
 origin_source_keys, origin_work_ids, new_source_keys, target_source_key, target_work_id,
 decided_by, status, version, created_at, updated_at)
VALUES ('decision_existing', 'issue_existing', 'src_existing', 'split', 'split_create_new', 'sf1:existing',
 '["wk-parent"]', '["wrk_existing"]', '["wk-child"]', '', '',
 'owner', 'applied', 1, 6, 7);`); err != nil {
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
	var version int
	if err := upgraded.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 20 {
		t.Fatalf("v15 数据升级后的 user_version = %d", version)
	}
	var issueFingerprint, decisionFingerprint string
	if err := upgraded.db.QueryRowContext(ctx, `SELECT candidate_fingerprint FROM binding_issues
WHERE issue_id='issue_existing'`).Scan(&issueFingerprint); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.db.QueryRowContext(ctx, `SELECT fingerprint FROM source_structure_decisions
WHERE decision_id='decision_existing'`).Scan(&decisionFingerprint); err != nil {
		t.Fatal(err)
	}
	if issueFingerprint != "sf1:existing" || decisionFingerprint != "sf1:existing" {
		t.Fatalf("既有结构证据在 v16 升级中被改写: issue=%q decision=%q", issueFingerprint, decisionFingerprint)
	}
	var freezeCount int
	if err := upgraded.db.QueryRowContext(ctx, `SELECT count(*) FROM schema_freeze
WHERE freeze_phase='phase1'`).Scan(&freezeCount); err != nil {
		t.Fatal(err)
	}
	if freezeCount != 17 {
		t.Fatalf("v16 未登记完整阶段 1 冻结项: %d", freezeCount)
	}
}

func TestStage5SecurityMigrationUpgradesPopulatedV19Control(t *testing.T) {
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
	for _, item := range migrations[:19] {
		if err := applyMigration(ctx, db, item); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO libraries (library_id, name, created_at)
VALUES ('lib_existing', '既有 Library', 1);
INSERT INTO sessions
(session_id, secret_hash, principal_id, csrf_token, created_at, expires_at, last_seen_at)
VALUES ('ses_00000000-0000-7000-8000-000000000001', 'old-secret-hash', 'personal-owner',
        'legacy-plaintext-csrf', 1, 9999999999, 1);`); err != nil {
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
	var version int
	if err := upgraded.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 20 {
		t.Fatalf("v19 数据升级后的 user_version = %d", version)
	}
	var libraryName string
	if err := upgraded.db.QueryRowContext(ctx, "SELECT name FROM libraries WHERE library_id='lib_existing'").Scan(&libraryName); err != nil || libraryName != "既有 Library" {
		t.Fatalf("阶段 5 migration 未保留既有产品事实: name=%q err=%v", libraryName, err)
	}
	var sessions, ownerRoles int
	if err := upgraded.db.QueryRowContext(ctx, "SELECT count(*) FROM sessions").Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 {
		t.Fatalf("含明文 CSRF 的旧 Session 未在安全升级时作废: %d", sessions)
	}
	if err := upgraded.db.QueryRowContext(ctx, `SELECT count(*) FROM principal_roles
WHERE principal_id='personal-owner' AND role_id='owner'`).Scan(&ownerRoles); err != nil || ownerRoles != 1 {
		t.Fatalf("Personal owner 未映射到新 Principal/Role: count=%d err=%v", ownerRoles, err)
	}
	var freezeCount int
	if err := upgraded.db.QueryRowContext(ctx, "SELECT count(*) FROM schema_freeze WHERE freeze_phase='phase5'").Scan(&freezeCount); err != nil || freezeCount != 5 {
		t.Fatalf("阶段 5 PRE_FREEZE 登记不完整: count=%d err=%v", freezeCount, err)
	}
}

func TestStage3CorrectnessMigrationPreservesV18RetryChildren(t *testing.T) {
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
	sub, _ := fs.Sub(migrationFiles, "migrations/control")
	migrations, _ := readMigrations(sub)
	for _, item := range migrations[:18] {
		if err := applyMigration(ctx, db, item); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO jobs
(job_id, job_type, source_id, created_by, status, stage, issue_code, retry_of,
 progress_sequence, attempt, resource_class, max_retries, failure_retryable, created_at, updated_at)
VALUES
('job_00000000-0000-7000-8000-000000000001', 'hash', NULL, 'owner', 'failed', 'failed', 'OLD_FAILURE', NULL,
 1, 1, 'hash', 2, 1, 1, 1),
('job_00000000-0000-7000-8000-000000000002', 'hash', NULL, 'owner', 'completed', 'completed', NULL,
 'job_00000000-0000-7000-8000-000000000001', 2, 1, 'hash', 2, 0, 2, 2)`); err != nil {
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
	var retryOf string
	if err := upgraded.db.QueryRowContext(ctx, `SELECT retry_of FROM jobs
WHERE job_id='job_00000000-0000-7000-8000-000000000002'`).Scan(&retryOf); err != nil {
		t.Fatal(err)
	}
	if retryOf != "job_00000000-0000-7000-8000-000000000001" {
		t.Fatalf("v18 retry 子 Job 来源丢失: %q", retryOf)
	}
	var attempts int
	if err := upgraded.db.QueryRowContext(ctx, "SELECT count(*) FROM job_attempts").Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("v19 未为既有 Job 补齐可解释 Attempt: %d", attempts)
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

// TestPhase1SchemaFreezeRecorded 断言阶段 1 Schema Freeze Gate 已把身份与唯一约束分类登记为可查询
// 产品事实，且被冻结的核心 active Binding 唯一索引与结构决策 fingerprint 唯一索引真实存在。
func TestPhase1SchemaFreezeRecorded(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	control := store.Control.SQL()

	want := map[string]string{
		"binding.active_source_key_unique":              "FROZEN",
		"binding.nonactive_history_multi":               "FROZEN",
		"binding.manual_unbound_excludes_auto":          "FROZEN",
		"binding.multi_source_isolation":                "FROZEN",
		"binding.source_rebuild_recovery":               "FROZEN",
		"binding.orphan_candidate_in_resolution":        "COMPATIBILITY_BASELINE",
		"binding.provider_external_id_conflict":         "PRE_FREEZE",
		"binding.rule_version_identity_namespace":       "DEFERRED",
		"canonical_work.identity_by_persistent_id":      "FROZEN",
		"canonical_work.origin_model":                   "PRE_FREEZE",
		"canonical_media.work_ordinal_unique":           "FROZEN",
		"canonical_media.same_blob_multi_occurrence":    "FROZEN",
		"media_binding.blob_evidence_recandidate":       "COMPATIBILITY_BASELINE",
		"binding_issue.fingerprint_dedup":               "FROZEN",
		"binding_issue.active_uniqueness":               "COMPATIBILITY_BASELINE",
		"structure_decision.fingerprint_unique_applied": "FROZEN",
		"structure_decision.undo_conflict_on_consumed":  "FROZEN",
	}
	rows, err := control.QueryContext(ctx, `SELECT subject, classification FROM schema_freeze
WHERE freeze_phase='phase1' ORDER BY subject`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := make(map[string]string, len(want))
	for rows.Next() {
		var subject, classification string
		if err := rows.Scan(&subject, &classification); err != nil {
			t.Fatal(err)
		}
		got[subject] = classification
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("阶段 1 冻结项数量漂移: want=%d got=%d", len(want), len(got))
	}
	for subject, classification := range want {
		if got[subject] != classification {
			t.Fatalf("阶段 1 冻结分类漂移: subject=%s want=%s got=%s", subject, classification, got[subject])
		}
	}
	// 被冻结的关键唯一索引必须存在于 schema。
	for _, index := range []string{
		"work_bindings_one_active_key", "media_bindings_one_active_key",
		"creator_bindings_one_active_key", "source_structure_decisions_fingerprint_idx",
	} {
		var name string
		err := control.QueryRowContext(ctx, `SELECT name FROM sqlite_master
WHERE type='index' AND name=?`, index).Scan(&name)
		if err != nil {
			t.Fatalf("冻结索引缺失 %s: %v", index, err)
		}
	}
}

func TestPhase2RuleFreezeRecorded(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	control := store.Control.SQL()
	want := map[string]string{
		"rule.package_canonical_json_control_owner": "FROZEN",
		"rule.version_immutable":                    "FROZEN",
		"rule.draft_optimistic_revision":            "FROZEN",
		"rule.job_execution_snapshot":               "FROZEN",
		"rule.ui_metadata_nonsemantic":              "COMPATIBILITY_BASELINE",
		"rule.extension_registry":                   "COMPATIBILITY_BASELINE",
		"rule.source_binding_single_effective":      "COMPATIBILITY_BASELINE",
		"rule.parameter_revision_and_override":      "COMPATIBILITY_BASELINE",
		"rule.impact_dependency_categories":         "COMPATIBILITY_BASELINE",
		"orphan.default_threshold_3":                "COMPATIBILITY_BASELINE",
		"orphan.retention_scans_override":           "COMPATIBILITY_BASELINE",
		"source_structure.missing_blob_evidence":    "COMPATIBILITY_BASELINE",
		"source_structure.split_bind_existing":      "DEFERRED",
		"source_structure.action_set":               "COMPATIBILITY_BASELINE",
		"rule_version.identity_namespace":           "COMPATIBILITY_BASELINE",
	}
	rows, err := control.QueryContext(ctx, `SELECT subject, classification FROM schema_freeze WHERE freeze_phase='phase2'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := make(map[string]string, len(want))
	for rows.Next() {
		var subject, classification string
		if err := rows.Scan(&subject, &classification); err != nil {
			t.Fatal(err)
		}
		got[subject] = classification
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("阶段 2 冻结项数量错误: got=%d want=%d", len(got), len(want))
	}
	for subject, classification := range want {
		if got[subject] != classification {
			t.Fatalf("阶段 2 冻结项 %s = %q，期望 %q", subject, got[subject], classification)
		}
	}
	var indexCount int
	if err := control.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='index' AND name='source_rule_bindings_effective_idx'`).Scan(&indexCount); err != nil {
		t.Fatal(err)
	}
	if indexCount != 1 {
		t.Fatal("规则 Binding 生效索引未创建")
	}
}
