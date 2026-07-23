package backup_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/backup"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/storage"
)

func manifestPath(dirs appdirs.Dirs, backupID string) string {
	return filepath.Join(dirs.State, "backups", backupID, "manifest.json")
}

func rewriteManifest(t *testing.T, dirs appdirs.Dirs, backupID string, mutate func(*backup.Manifest)) {
	t.Helper()
	data, err := os.ReadFile(manifestPath(dirs, backupID))
	if err != nil {
		t.Fatal(err)
	}
	var manifest backup.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	mutate(&manifest)
	out, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath(dirs, backupID), out, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyAcceptsHealthyBackup(t *testing.T) {
	h := newHarness(t)
	seedControl(t, h.store)
	manifest := runBackup(t, h)
	report, err := h.svc.Verify(context.Background(), manifest.BackupID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !report.Compatible || !report.ChecksumVerified || !report.IntegrityOK || !report.InvariantsOK {
		t.Fatalf("健康备份验证未通过: %+v", report)
	}
	if report.WillMigrate {
		t.Fatalf("同版本备份不应需要迁移: %+v", report)
	}
}

func TestVerifyRejectsCorruptDatabase(t *testing.T) {
	h := newHarness(t)
	manifest := runBackup(t, h)
	dbPath := filepath.Join(h.dirs.State, "backups", manifest.BackupID, "control.db")
	if err := os.WriteFile(dbPath, append([]byte("corrupt"), make([]byte, 16)...), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := h.svc.Verify(context.Background(), manifest.BackupID)
	assertCode(t, err, fault.CodeBackupCorrupt)
}

func TestVerifyRejectsChecksumMismatch(t *testing.T) {
	h := newHarness(t)
	manifest := runBackup(t, h)
	rewriteManifest(t, h.dirs, manifest.BackupID, func(m *backup.Manifest) {
		m.Database.Checksum = "0000000000000000000000000000000000000000000000000000000000000000"
	})
	_, err := h.svc.Verify(context.Background(), manifest.BackupID)
	assertCode(t, err, fault.CodeBackupCorrupt)
}

func TestVerifyRejectsManifestIdentityMismatchBeforePathResolution(t *testing.T) {
	h := newHarness(t)
	manifest := runBackup(t, h)
	rewriteManifest(t, h.dirs, manifest.BackupID, func(m *backup.Manifest) {
		m.BackupID = "../../outside"
	})
	_, err := h.svc.Verify(context.Background(), manifest.BackupID)
	assertCode(t, err, fault.CodeBackupCorrupt)
}

func TestVerifyRejectsFutureSchemaVersion(t *testing.T) {
	h := newHarness(t)
	manifest := runBackup(t, h)
	rewriteManifest(t, h.dirs, manifest.BackupID, func(m *backup.Manifest) { m.SchemaVersion = 9999 })
	_, err := h.svc.Verify(context.Background(), manifest.BackupID)
	assertCode(t, err, fault.CodeBackupIncompatible)
}

func TestVerifyRejectsFutureManifestAndKeepsV1Readable(t *testing.T) {
	h := newHarness(t)
	manifest := runBackup(t, h)
	rewriteManifest(t, h.dirs, manifest.BackupID, func(m *backup.Manifest) { m.ManifestVersion = backup.ManifestVersion + 1 })
	_, err := h.svc.Verify(context.Background(), manifest.BackupID)
	assertCode(t, err, fault.CodeBackupCorrupt)

	h = newHarness(t)
	manifest = runBackup(t, h)
	rewriteManifest(t, h.dirs, manifest.BackupID, func(m *backup.Manifest) {
		m.ManifestVersion = 1
		m.Security.Shares = ""
	})
	if _, err := h.svc.Verify(context.Background(), manifest.BackupID); err != nil {
		t.Fatalf("v1 manifest 不再可读: %v", err)
	}
}

func TestApplyPendingRestoreReplacesControlAndInvalidatesRuntime(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	seedControl(t, h.store)
	// 在备份前放入一个非终态 Job，验证恢复后被作废。
	if _, err := h.store.Control.SQL().Exec(`INSERT INTO jobs
(job_id, job_type, source_id, created_by, status, stage, progress_sequence, attempt, created_at, updated_at)
VALUES ('job_00000000-0000-7000-8000-0000000000aa', 'scan', NULL, 'owner', 'running', 'hashing', 1, 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Control.SQL().Exec(`INSERT INTO pairing_attempts
(credential_hash, created_at, expires_at) VALUES
('cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc', 1, 9999999999);
INSERT INTO api_tokens
(token_id, principal_id, secret_hash, secret_prefix, name, capabilities_json, scopes_json,
 principal_security_version, created_by, created_at)
VALUES ('tok_00000000-0000-7000-8000-000000000001', 'personal-owner',
 'dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd', 'safe', 'backup-token',
 '["library.read"]', '[{"kind":"global"}]', 1, 'personal-owner', 1);
INSERT INTO shares
(share_id, secret_hash, secret_prefix, created_by, scope_kind, scope_id, permissions_json, created_at, expires_at)
VALUES ('shr_00000000-0000-7000-8000-000000000001',
 'eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee', 'safe', 'personal-owner',
 'library', 'lib_00000000-0000-7000-8000-000000000001', '["view"]', 1, 9999999999)`); err != nil {
		t.Fatal(err)
	}
	manifest := runBackup(t, h)

	// 备份后修改当前库：这些变更应在恢复后被丢弃。
	if _, err := h.store.Control.SQL().Exec(
		`INSERT INTO canonical_works (work_id, title, created_at) VALUES ('wrk_after', '备份后作品', 2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Control.SQL().Exec(`INSERT INTO sessions
(session_id, secret_hash, principal_id, csrf_hash, auth_method, client_label,
 principal_security_version, created_at, absolute_expires_at, idle_expires_at, last_seen_at)
VALUES ('ses_after', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
 'personal-owner', 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
 'personal_pairing', '', 1, 2, 9999999999, 9999999999, 2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.RequestRestore(ctx, "personal-owner", manifest.BackupID); err != nil {
		t.Fatal(err)
	}
	// 模拟关闭：单写者锁下关闭当前 store，再应用待恢复请求。
	if err := h.store.Close(); err != nil {
		t.Fatal(err)
	}

	outcome, err := backup.ApplyPendingRestore(ctx, h.dirs)
	if err != nil {
		t.Fatalf("ApplyPendingRestore: %v", err)
	}
	if !outcome.Applied || outcome.BackupID != manifest.BackupID {
		t.Fatalf("恢复未应用: %+v", outcome)
	}
	if _, err := os.Stat(filepath.Join(h.dirs.State, "restore-pending.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("恢复应用后未清除待应用标记: %v", err)
	}
	if outcome.RotatedPath == "" {
		t.Fatal("恢复未轮换保留旧 control.db")
	}
	if _, err := os.Stat(outcome.RotatedPath); err != nil {
		t.Fatalf("轮换旧库不存在: %v", err)
	}

	reopened, err := storage.Open(ctx, h.dirs)
	if err != nil {
		t.Fatalf("恢复后重开数据库: %v", err)
	}
	defer reopened.Close()
	if err := backup.FinalizeRestore(ctx, reopened.Control, time.Date(2026, 7, 17, 1, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("FinalizeRestore: %v", err)
	}

	var backupWork, afterWork int
	if err := reopened.Control.SQL().QueryRow("SELECT count(*) FROM canonical_works WHERE work_id='wrk_backup'").Scan(&backupWork); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Control.SQL().QueryRow("SELECT count(*) FROM canonical_works WHERE work_id='wrk_after'").Scan(&afterWork); err != nil {
		t.Fatal(err)
	}
	if backupWork != 1 || afterWork != 0 {
		t.Fatalf("恢复未回到备份状态: backup=%d after=%d", backupWork, afterWork)
	}
	var sessionCount int
	if err := reopened.Control.SQL().QueryRow("SELECT count(*) FROM sessions").Scan(&sessionCount); err != nil {
		t.Fatal(err)
	}
	if sessionCount != 0 {
		t.Fatalf("恢复后 Session 未作废: %d", sessionCount)
	}
	var pairingCount, revokedTokens, revokedShares, restoreAudits int
	if err := reopened.Control.SQL().QueryRow("SELECT count(*) FROM pairing_attempts").Scan(&pairingCount); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Control.SQL().QueryRow("SELECT count(*) FROM api_tokens WHERE revoked_at IS NOT NULL").Scan(&revokedTokens); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Control.SQL().QueryRow("SELECT count(*) FROM shares WHERE revoked_at IS NOT NULL").Scan(&revokedShares); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Control.SQL().QueryRow("SELECT count(*) FROM security_audits WHERE action='restore.finalize'").Scan(&restoreAudits); err != nil {
		t.Fatal(err)
	}
	if pairingCount != 0 || revokedTokens != 1 || revokedShares != 1 || restoreAudits != 1 {
		t.Fatalf("恢复后安全状态错误: pairing=%d tokens=%d shares=%d audits=%d", pairingCount, revokedTokens, revokedShares, restoreAudits)
	}
	var ruleCount int
	if err := reopened.Control.SQL().QueryRow("SELECT count(*) FROM rule_packages WHERE package_id='rpack_018f47d2-5c16-7a44-a8a0-000000000010'").Scan(&ruleCount); err != nil {
		t.Fatal(err)
	}
	if ruleCount != 1 {
		t.Fatalf("恢复后 RulePackage 不存在: %d", ruleCount)
	}
	var jobStatus, issue string
	if err := reopened.Control.SQL().QueryRow(
		"SELECT status, coalesce(issue_code,'') FROM jobs WHERE job_id='job_00000000-0000-7000-8000-0000000000aa'").Scan(&jobStatus, &issue); err != nil {
		t.Fatal(err)
	}
	if jobStatus != "failed" || issue != "RESTORE_INVALIDATED" {
		t.Fatalf("恢复后非终态 Job 未作废: status=%s issue=%s", jobStatus, issue)
	}
}

func TestApplyPendingRestoreKeepsCurrentOnBadBackup(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	seedControl(t, h.store)
	manifest := runBackup(t, h)
	if _, err := h.svc.RequestRestore(ctx, "personal-owner", manifest.BackupID); err != nil {
		t.Fatal(err)
	}
	// 请求登记后、应用前损坏备份：应用必须放弃恢复并保留当前库。
	dbPath := filepath.Join(h.dirs.State, "backups", manifest.BackupID, "control.db")
	if err := os.WriteFile(dbPath, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.store.Close(); err != nil {
		t.Fatal(err)
	}
	outcome, err := backup.ApplyPendingRestore(ctx, h.dirs)
	if err != nil {
		t.Fatalf("ApplyPendingRestore 不应因坏备份返回致命错误: %v", err)
	}
	if outcome.Applied {
		t.Fatal("坏备份被错误应用")
	}
	reopened, err := storage.Open(ctx, h.dirs)
	if err != nil {
		t.Fatalf("坏备份后当前库不可用: %v", err)
	}
	defer reopened.Close()
	var work int
	if err := reopened.Control.SQL().QueryRow("SELECT count(*) FROM canonical_works WHERE work_id='wrk_backup'").Scan(&work); err != nil {
		t.Fatal(err)
	}
	if work != 1 {
		t.Fatalf("坏备份影响了当前 control.db: %d", work)
	}
}

func TestApplyPendingRestoreRejectsTraversalMarkerAndKeepsCurrent(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	seedControl(t, h.store)
	if err := os.WriteFile(filepath.Join(h.dirs.State, "restore-pending.json"),
		[]byte(`{"backupId":"../../outside","requestedBy":"attacker","requestedAt":"2026-07-23T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.store.Close(); err != nil {
		t.Fatal(err)
	}
	outcome, err := backup.ApplyPendingRestore(ctx, h.dirs)
	if err != nil || outcome.Applied {
		t.Fatalf("路径逃逸恢复标记未被安全拒绝: outcome=%+v err=%v", outcome, err)
	}
	if _, err := os.Stat(filepath.Join(h.dirs.State, "restore-pending.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("恶意恢复标记未被消费移除: %v", err)
	}
	reopened, err := storage.Open(ctx, h.dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var count int
	if err := reopened.Control.SQL().QueryRow("SELECT count(*) FROM canonical_works WHERE work_id='wrk_backup'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("恶意恢复标记影响当前 control.db: count=%d err=%v", count, err)
	}
}

func assertCode(t *testing.T, err error, code fault.Code) {
	t.Helper()
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != code {
		t.Fatalf("期望错误码 %s，实际 %v", code, err)
	}
}
