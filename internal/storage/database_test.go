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
	wantVersions := map[Role]int{RoleControl: 20, RoleCatalog: 10}
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
