package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/storage"
)

const (
	restorePendingFile = "restore-pending.json"
	restoreLastFile    = "restore-last.json"
	incomingSuffix     = ".incoming"
	preRestorePrefix   = "control.db.pre-restore-"
)

// RestoreReport 是恢复前的验证结论（Dry Run）。它不修改任何状态，供高危恢复操作在实际执行前
// 确认备份可用、版本兼容并可迁移到当前 Schema。
type RestoreReport struct {
	BackupID             string `json:"backupId"`
	Compatible           bool   `json:"compatible"`
	BackupSchemaVersion  int64  `json:"backupSchemaVersion"`
	CurrentSchemaVersion int64  `json:"currentSchemaVersion"`
	WillMigrate          bool   `json:"willMigrate"`
	ChecksumVerified     bool   `json:"checksumVerified"`
	IntegrityOK          bool   `json:"integrityOk"`
	InvariantsOK         bool   `json:"invariantsOk"`
	Detail               string `json:"detail"`
}

// RestoreOutcome 描述一次启动期恢复应用的结果，供 bootstrap 决定是否执行恢复后清理。
type RestoreOutcome struct {
	Applied     bool
	BackupID    string
	RotatedPath string
}

type restoreRequest struct {
	BackupID    string    `json:"backupId"`
	RequestedBy string    `json:"requestedBy"`
	RequestedAt time.Time `json:"requestedAt"`
}

type restoreLast struct {
	BackupID   string    `json:"backupId"`
	Applied    bool      `json:"applied"`
	Detail     string    `json:"detail"`
	FinishedAt time.Time `json:"finishedAt"`
}

// Verify 对指定备份执行恢复前验证：检查 manifest、role、checksum 与版本兼容性，并在隔离临时目录
// 打开、迁移与完整性/外键校验一份副本。它绝不触碰当前 control.db，也不写入任何标记。
func (s *Service) Verify(ctx context.Context, backupID string) (RestoreReport, error) {
	manifest, err := s.Get(ctx, backupID)
	if err != nil {
		return RestoreReport{BackupID: backupID}, err
	}
	report, err := verifyBackupFiles(ctx, s.backupRoot(), s.dirs.Temp, manifest)
	report.BackupID = backupID
	return report, err
}

// RequestRestore 验证备份并登记一次待应用恢复请求。实际的原子替换在下次 galleryd 启动、持有
// AppDirs 单写者锁且当前 control.db 未被打开时执行，因此调用方需要重启服务。恢复一旦应用，将
// 丢弃自备份以来的 control 变更。
func (s *Service) RequestRestore(ctx context.Context, requestedBy, backupID string) (RestoreReport, error) {
	report, err := s.Verify(ctx, backupID)
	if err != nil {
		return report, err
	}
	request := restoreRequest{BackupID: backupID, RequestedBy: requestedBy, RequestedAt: s.clock.Now().UTC()}
	data, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		return report, fault.New(fault.CodeRestoreFailed, false, err)
	}
	if err := os.MkdirAll(s.dirs.State, 0o700); err != nil {
		return report, fault.New(fault.CodeRestoreFailed, false, err)
	}
	if err := os.WriteFile(filepath.Join(s.dirs.State, restorePendingFile), data, 0o600); err != nil {
		return report, fault.New(fault.CodeRestoreFailed, false, err)
	}
	return report, nil
}

// verifyBackupFiles 是不依赖运行中 Service 的纯验证：它可在启动期（无打开数据库）复用。
func verifyBackupFiles(ctx context.Context, backupRoot, tempRoot string, manifest Manifest) (RestoreReport, error) {
	report := RestoreReport{}
	if _, err := domain.ParseID(domain.IDControlBackup, manifest.BackupID); err != nil {
		return report, fault.New(fault.CodeBackupCorrupt, false, fmt.Errorf("备份 ID 无效"))
	}
	if manifest.Role != string(storage.RoleControl) {
		return report, fault.New(fault.CodeBackupIncompatible, false, fmt.Errorf("备份 role 非 control"))
	}
	dbPath := filepath.Join(backupRoot, manifest.BackupID, databaseFileName)
	size, checksum, err := fileChecksum(dbPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return report, fault.New(fault.CodeBackupCorrupt, false, fmt.Errorf("备份数据库缺失"))
		}
		return report, fault.New(fault.CodeBackupCorrupt, false, err)
	}
	if size != manifest.Database.SizeBytes || checksum != manifest.Database.Checksum {
		return report, fault.New(fault.CodeBackupCorrupt, false, fmt.Errorf("备份 checksum 或大小不符"))
	}
	report.ChecksumVerified = true

	embedded, err := storage.EmbeddedSchemaState(storage.RoleControl)
	if err != nil {
		return report, fault.New(fault.CodeRestoreFailed, false, err)
	}
	report.BackupSchemaVersion = manifest.SchemaVersion
	report.CurrentSchemaVersion = embedded.Version
	if manifest.SchemaVersion > embedded.Version {
		return report, fault.New(fault.CodeBackupIncompatible, false, fmt.Errorf("备份来自更高不兼容 Schema 版本"))
	}
	report.WillMigrate = manifest.SchemaVersion < embedded.Version

	if err := os.MkdirAll(tempRoot, 0o700); err != nil {
		return report, fault.New(fault.CodeRestoreFailed, false, err)
	}
	stagingDir, err := os.MkdirTemp(tempRoot, "restore-verify-")
	if err != nil {
		return report, fault.New(fault.CodeRestoreFailed, false, err)
	}
	defer os.RemoveAll(stagingDir)
	stagedDB := filepath.Join(stagingDir, "staged.db")
	if err := copyFile(dbPath, stagedDB); err != nil {
		return report, fault.New(fault.CodeRestoreFailed, false, err)
	}
	if err := openStageAndCheck(ctx, stagedDB); err != nil {
		return report, err
	}
	report.IntegrityOK = true
	report.InvariantsOK = true
	report.Compatible = true
	report.Detail = "备份可恢复；应用需重启 galleryd"
	return report, nil
}

// openStageAndCheck 在隔离副本上执行 forward 迁移，并做完整性与外键不变量检查。迁移失败视为
// 备份与当前程序不兼容。
func openStageAndCheck(ctx context.Context, path string) error {
	db, err := storage.OpenControlAt(ctx, path)
	if err != nil {
		var structured *fault.Error
		if errors.As(err, &structured) && structured.Code == fault.CodeMigrationFailed {
			return fault.New(fault.CodeBackupIncompatible, false, err)
		}
		return fault.New(fault.CodeBackupCorrupt, false, err)
	}
	defer db.Close()
	var integrity string
	if err := db.SQL().QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return fault.New(fault.CodeBackupCorrupt, false, err)
	}
	if integrity != "ok" {
		return fault.New(fault.CodeBackupCorrupt, false, fmt.Errorf("备份完整性检查失败"))
	}
	rows, err := db.SQL().QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fault.New(fault.CodeBackupCorrupt, false, err)
	}
	defer rows.Close()
	if rows.Next() {
		return fault.New(fault.CodeBackupCorrupt, false, fmt.Errorf("备份存在外键不变量违规"))
	}
	return rows.Err()
}

// ApplyPendingRestore 在 galleryd 启动、打开任何数据库之前调用。若存在待应用恢复请求，它在隔离
// 临时目录验证并迁移备份，产出干净候选，再原子替换当前 control.db（旧库轮换保留）。恢复失败一律
// 保留当前 control.db 并继续启动，绝不因坏备份使进程无法启动。它必须在持有 AppDirs 单写者锁、
// 且当前 control.db 尚未被打开时调用。
func ApplyPendingRestore(ctx context.Context, dirs appdirs.Dirs) (RestoreOutcome, error) {
	markerPath := filepath.Join(dirs.State, restorePendingFile)
	data, err := os.ReadFile(markerPath)
	if errors.Is(err, os.ErrNotExist) {
		return RestoreOutcome{}, nil
	}
	if err != nil {
		return RestoreOutcome{}, err
	}
	var request restoreRequest
	unmarshalErr := json.Unmarshal(data, &request)
	_, idErr := domain.ParseID(domain.IDControlBackup, request.BackupID)
	if unmarshalErr != nil || idErr != nil {
		recordRestoreOutcome(dirs, request.BackupID, false, "恢复请求标记损坏")
		_ = os.Remove(markerPath)
		return RestoreOutcome{}, nil
	}
	outcome, applyErr := applyRestore(ctx, dirs, request.BackupID)
	if applyErr != nil {
		recordRestoreOutcome(dirs, request.BackupID, false, applyErr.Error())
		_ = os.Remove(markerPath)
		return RestoreOutcome{}, nil // 保留当前库并继续启动。
	}
	recordRestoreOutcome(dirs, request.BackupID, true, "已原子替换 control.db")
	_ = os.Remove(markerPath)
	return outcome, nil
}

func applyRestore(ctx context.Context, dirs appdirs.Dirs, backupID string) (RestoreOutcome, error) {
	backupRoot := filepath.Join(dirs.State, "backups")
	manifest, err := readManifest(filepath.Join(backupRoot, backupID, manifestFileName))
	if err != nil {
		return RestoreOutcome{}, fmt.Errorf("读取备份 manifest: %w", err)
	}
	if manifest.BackupID != backupID {
		return RestoreOutcome{}, fault.New(fault.CodeBackupCorrupt, false, fmt.Errorf("备份 manifest 身份与目录不一致"))
	}
	if _, err := verifyBackupFiles(ctx, backupRoot, dirs.Temp, manifest); err != nil {
		return RestoreOutcome{}, err
	}

	// 在隔离临时目录迁移备份副本，再 VACUUM 出干净单文件候选。
	if err := os.MkdirAll(dirs.Temp, 0o700); err != nil {
		return RestoreOutcome{}, err
	}
	stagingDir, err := os.MkdirTemp(dirs.Temp, "restore-apply-")
	if err != nil {
		return RestoreOutcome{}, err
	}
	defer os.RemoveAll(stagingDir)
	stagedDB := filepath.Join(stagingDir, "staged.db")
	if err := copyFile(filepath.Join(backupRoot, backupID, databaseFileName), stagedDB); err != nil {
		return RestoreOutcome{}, err
	}
	staged, err := storage.OpenControlAt(ctx, stagedDB)
	if err != nil {
		return RestoreOutcome{}, err
	}
	controlPath := filepath.Join(dirs.Data, "control.db")
	incoming := controlPath + incomingSuffix
	_ = os.Remove(incoming)
	if _, err := staged.SQL().ExecContext(ctx, "VACUUM main INTO ?", filepath.ToSlash(incoming)); err != nil {
		_ = staged.Close()
		return RestoreOutcome{}, err
	}
	if err := staged.Close(); err != nil {
		_ = os.Remove(incoming)
		return RestoreOutcome{}, err
	}

	// 原子替换：先轮换当前库，再落位候选；任一步失败都回滚到当前库。
	rotated := filepath.Join(dirs.Data, fmt.Sprintf("%s%d.bak", preRestorePrefix, time.Now().UnixNano()))
	rotatedCurrent := false
	if _, statErr := os.Stat(controlPath); statErr == nil {
		if err := os.Rename(controlPath, rotated); err != nil {
			_ = os.Remove(incoming)
			return RestoreOutcome{}, fmt.Errorf("轮换当前 control.db: %w", err)
		}
		rotatedCurrent = true
	}
	// 移除旧库的 WAL/SHM，避免新库被过期日志遮蔽。
	_ = os.Remove(controlPath + "-wal")
	_ = os.Remove(controlPath + "-shm")
	if err := os.Rename(incoming, controlPath); err != nil {
		if rotatedCurrent {
			_ = os.Rename(rotated, controlPath) // 回滚，保留当前库可用。
		}
		_ = os.Remove(incoming)
		return RestoreOutcome{}, fmt.Errorf("落位恢复候选: %w", err)
	}
	return RestoreOutcome{Applied: true, BackupID: backupID, RotatedPath: rotated}, nil
}

// FinalizeRestore 在恢复应用后、数据库重新打开时执行恢复后清理：使无法验证的运行时安全状态
// （Session、一次性配对）失效，并把备份中残留的非终态 Job 收敛为失败，确保 Job/Session/publication
// 与新库一致。
func FinalizeRestore(ctx context.Context, control *storage.Database, now time.Time) error {
	tx, err := control.SQL().BeginTx(ctx, nil)
	if err != nil {
		return fault.New(fault.CodeRestoreFailed, false, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions"); err != nil {
		return fault.New(fault.CodeRestoreFailed, false, err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM pairing_attempts"); err != nil {
		return fault.New(fault.CodeRestoreFailed, false, err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE api_tokens SET revoked_at=COALESCE(revoked_at, ?)", now.UTC().Unix()); err != nil {
		return fault.New(fault.CodeRestoreFailed, false, err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE shares SET revoked_at=COALESCE(revoked_at, ?)", now.UTC().Unix()); err != nil {
		return fault.New(fault.CodeRestoreFailed, false, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO security_audits
(audit_id, actor_principal_id, action, target_kind, target_id, outcome, detail_json, created_at)
VALUES (?, NULL, 'restore.finalize', 'control', 'control.db', 'success',
        '{"credentialsInvalidated":true}', ?)`, fmt.Sprintf("saud_restore_%d", now.UTC().UnixNano()), now.UTC().Unix()); err != nil {
		return fault.New(fault.CodeRestoreFailed, false, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET status='failed', stage='restore_invalidated',
issue_code='RESTORE_INVALIDATED', finished_at=?, progress_sequence=progress_sequence+1, updated_at=?
WHERE status IN ('queued', 'running', 'publishing')`, now.UTC().Unix(), now.UTC().Unix()); err != nil {
		return fault.New(fault.CodeRestoreFailed, false, err)
	}
	if err := tx.Commit(); err != nil {
		return fault.New(fault.CodeRestoreFailed, false, err)
	}
	return nil
}

func recordRestoreOutcome(dirs appdirs.Dirs, backupID string, applied bool, detail string) {
	data, err := json.MarshalIndent(restoreLast{BackupID: backupID, Applied: applied, Detail: detail, FinishedAt: time.Now().UTC()}, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dirs.State, restoreLastFile), data, 0o600)
}

func copyFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
