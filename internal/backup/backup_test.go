package backup_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/backup"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/storage"
)

type harness struct {
	svc   *backup.Service
	store *storage.Store
	dirs  appdirs.Dirs
	jobs  *jobs.Store
}

func newHarness(t *testing.T) harness {
	t.Helper()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	fixedClock := clock.Fixed{Time: time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)}
	generator := identity.NewGenerator(fixedClock)
	jobStore, err := jobs.NewStore(store.Control.SQL(), fixedClock, generator)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := backup.New(context.Background(), store.Control, jobStore, dirs, fixedClock, generator, "test-1.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	return harness{svc: svc, store: store, dirs: dirs, jobs: jobStore}
}

func seedControl(t *testing.T, store *storage.Store) {
	t.Helper()
	if _, err := store.Control.SQL().Exec(
		`INSERT INTO canonical_works (work_id, title, created_at) VALUES ('wrk_backup', '备份作品', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Control.SQL().Exec(`INSERT INTO sessions
(session_id, secret_hash, principal_id, csrf_hash, auth_method, client_label,
 principal_security_version, created_at, absolute_expires_at, idle_expires_at, last_seen_at)
VALUES ('ses_seed', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
 'personal-owner', 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
 'personal_pairing', '', 1, 1, 9999999999, 9999999999, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Control.SQL().Exec(`INSERT INTO rule_packages
(package_id, rule_set_id, name, description, status, created_by, created_at, updated_at, revision)
VALUES ('rpack_018f47d2-5c16-7a44-a8a0-000000000010', 'rset_018f47d2-5c16-7a44-a8a0-000000000010', '备份规则包', '', 'active', 'owner', 1, 1, 1);
INSERT INTO rule_drafts
(draft_id, package_id, content_json, source_format, validation_status, diagnostics_json, revision, saved_by, created_at, updated_at)
VALUES ('rdraft_018f47d2-5c16-7a44-a8a0-000000000010', 'rpack_018f47d2-5c16-7a44-a8a0-000000000010', '{}', 'json', 'invalid', '[{"path":"/","message":"synthetic"}]', 1, 'owner', 1, 1)`); err != nil {
		t.Fatal(err)
	}
}

func runBackup(t *testing.T, h harness) backup.Manifest {
	t.Helper()
	job, err := h.svc.CreateBackup(context.Background(), "personal-owner")
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	if err := h.svc.Execute(context.Background(), job.ID); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	completed, err := h.jobs.Get(context.Background(), job.ID)
	if err != nil || completed.Status != jobs.StatusCompleted {
		t.Fatalf("备份 Job 未完成: %+v %v", completed, err)
	}
	list, err := h.svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("List 备份数 = %d", len(list))
	}
	return list[0]
}

func TestBackupProducesConsistentRestorableCopy(t *testing.T) {
	h := newHarness(t)
	seedControl(t, h.store)
	manifest := runBackup(t, h)

	if manifest.Role != string(storage.RoleControl) || manifest.ManifestVersion != backup.ManifestVersion {
		t.Fatalf("manifest 基本字段错误: %+v", manifest)
	}
	if manifest.SchemaVersion != 20 {
		t.Fatalf("manifest schemaVersion = %d，应等于 control 最高 migration", manifest.SchemaVersion)
	}
	if manifest.Database.ChecksumAlgorithm != "sha256" || manifest.Database.FileName != "control.db" {
		t.Fatalf("manifest 文件条目错误: %+v", manifest.Database)
	}
	if manifest.Security.Sessions != "included-hashed" || manifest.Security.APITokens != "included-hashed-invalidated-on-restore" ||
		manifest.Security.Shares != "included-hashed-invalidated-on-restore" {
		t.Fatalf("安全范围声明错误: %+v", manifest.Security)
	}

	dbPath := filepath.Join(h.dirs.State, "backups", manifest.BackupID, "control.db")
	size, checksum, err := fileSum(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if size != manifest.Database.SizeBytes || checksum != manifest.Database.Checksum {
		t.Fatalf("checksum/size 与 manifest 不符: 文件(%d,%s) manifest(%d,%s)", size, checksum, manifest.Database.SizeBytes, manifest.Database.Checksum)
	}

	backupDB, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer backupDB.Close()
	var integrity string
	if err := backupDB.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("备份完整性: %q %v", integrity, err)
	}
	var title string
	if err := backupDB.QueryRow("SELECT title FROM canonical_works WHERE work_id='wrk_backup'").Scan(&title); err != nil || title != "备份作品" {
		t.Fatalf("不可重建事实未进入备份: %q %v", title, err)
	}
	var sessionCount int
	if err := backupDB.QueryRow("SELECT count(*) FROM sessions WHERE session_id='ses_seed'").Scan(&sessionCount); err != nil || sessionCount != 1 {
		t.Fatalf("session 状态未按声明进入备份: %d %v", sessionCount, err)
	}
	var ruleName, draftStatus string
	if err := backupDB.QueryRow("SELECT name FROM rule_packages WHERE package_id='rpack_018f47d2-5c16-7a44-a8a0-000000000010'").Scan(&ruleName); err != nil || ruleName != "备份规则包" {
		t.Fatalf("RulePackage 未进入备份: %q %v", ruleName, err)
	}
	if err := backupDB.QueryRow("SELECT validation_status FROM rule_drafts WHERE draft_id='rdraft_018f47d2-5c16-7a44-a8a0-000000000010'").Scan(&draftStatus); err != nil || draftStatus != "invalid" {
		t.Fatalf("RuleDraft 状态未进入备份: %q %v", draftStatus, err)
	}
}

func TestBackupExcludesCatalogLifecycle(t *testing.T) {
	h := newHarness(t)
	manifest := runBackup(t, h)
	dir := filepath.Join(h.dirs.State, "backups", manifest.BackupID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, entry := range entries {
		names[entry.Name()] = true
	}
	if !names["control.db"] || !names["manifest.json"] {
		t.Fatalf("备份目录缺少必需文件: %v", names)
	}
	if names["catalog.db"] {
		t.Fatal("control 备份误纳入 catalog.db")
	}
	backupDB, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "control.db"))+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer backupDB.Close()
	if _, err := backupDB.Exec("SELECT * FROM gallery_catalog_meta"); err == nil {
		t.Fatal("control 备份混入 catalog 生命周期表")
	}
}

func TestConcurrentBackupConflicts(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.CreateBackup(context.Background(), "personal-owner"); err != nil {
		t.Fatal(err)
	}
	_, err := h.svc.CreateBackup(context.Background(), "personal-owner")
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeJobStateConflict {
		t.Fatalf("并发备份未冲突: %v", err)
	}
}

func TestGetUnknownBackupReturnsNotFound(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.Get(context.Background(), "bkp_00000000-0000-7000-8000-000000000000")
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeBackupNotFound {
		t.Fatalf("未知备份错误码 = %v", err)
	}
}

func TestCancelledContextPublishesNoBackupAndKeepsControl(t *testing.T) {
	h := newHarness(t)
	seedControl(t, h.store)
	job, err := h.svc.CreateBackup(context.Background(), "personal-owner")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.svc.Execute(ctx, job.ID); err == nil {
		t.Fatal("已取消 context 下备份意外成功")
	}
	list, err := h.svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("取消后仍发布了备份: %d", len(list))
	}
	var title string
	if err := h.store.Control.SQL().QueryRow("SELECT title FROM canonical_works WHERE work_id='wrk_backup'").Scan(&title); err != nil || title != "备份作品" {
		t.Fatalf("备份取消影响了当前 control.db: %q %v", title, err)
	}
}

func TestReconcileLeavesUnexpiredBackupForCentralLeaseRecovery(t *testing.T) {
	h := newHarness(t)
	if _, err := h.store.Control.SQL().Exec(`INSERT INTO jobs
(job_id, job_type, source_id, created_by, status, stage, progress_sequence, attempt, created_at, updated_at)
VALUES ('job_00000000-0000-7000-8000-000000000001', 'control_backup', NULL, 'personal-owner', 'running', 'copying', 1, 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	activeStaging := filepath.Join(h.dirs.State, "backups",
		".staging-job_00000000-0000-7000-8000-000000000001--bkp_00000000-0000-7000-8000-000000000001")
	orphanStaging := filepath.Join(h.dirs.State, "backups", ".staging-leftover")
	if err := os.MkdirAll(activeStaging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(orphanStaging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err := h.jobs.Get(context.Background(), "job_00000000-0000-7000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != jobs.StatusRunning {
		t.Fatalf("尚未过期的备份 Attempt 被提前判死: %+v", job)
	}
	if _, err := os.Stat(activeStaging); err != nil {
		t.Fatalf("Reconcile 删除了活动备份 staging: %v", err)
	}
	if _, err := os.Stat(orphanStaging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Reconcile 未清理遗留 staging 目录: %v", err)
	}
}

func fileSum(path string) (int64, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, "", err
	}
	sum := sha256.Sum256(data)
	return int64(len(data)), hex.EncodeToString(sum[:]), nil
}
