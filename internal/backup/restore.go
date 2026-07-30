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
	restorePendingFile   = "restore-pending.json"
	restoreLastFile      = "restore-last.json"
	restorePhaseFinalize = "placed_pending_finalize"
	incomingSuffix       = ".incoming"
	preRestorePrefix     = "control.db.pre-restore-"
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
	Phase       string    `json:"phase,omitempty"`
}

type restoreLast struct {
	BackupID   string    `json:"backupId"`
	Applied    bool      `json:"applied"`
	Detail     string    `json:"detail"`
	FinishedAt time.Time `json:"finishedAt"`
}

// restoreContinuityError 表示恢复替换已经越过“当前库仍在原位”的边界，且无法确认
// control.db 已恢复到可继续启动的状态。普通坏备份/候选落位失败可以记录后继续使用旧库；
// 这类错误则必须阻止 bootstrap 打开缺失路径并意外创建空 control.db。类型保持包内，
// 不进入 API 契约。
type restoreContinuityError struct{ cause error }

func (e *restoreContinuityError) Error() string { return e.cause.Error() }
func (e *restoreContinuityError) Unwrap() error { return e.cause }

type restoreFileOps struct {
	stat   func(string) (os.FileInfo, error)
	rename func(string, string) error
	remove func(string) error
}

var osRestoreFileOps = restoreFileOps{stat: os.Stat, rename: os.Rename, remove: os.Remove}

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
	if err := writeStateFile(filepath.Join(s.dirs.State, restorePendingFile), data); err != nil {
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
// 临时目录验证并迁移备份，产出干净候选，再原子替换当前 control.db（旧库轮换保留）。成功落位后
// pending 会进入待 FinalizeRestore 阶段，只有恢复后安全收尾与结果记录都成功才消费，确保进程在
// 落位与收尾之间中断时下次启动仍会继续收尾而不会重复应用备份。恢复失败时，只有确认当前
// control.db 仍是普通文件才允许消费 pending 并继续启动；当前库不可用时必须保留恢复请求并
// fail-closed，禁止后续存储打开意外创建空库。它必须在持有 AppDirs 单写者锁、且当前 control.db
// 尚未被打开时调用。
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
	if unmarshalErr != nil || idErr != nil || (request.Phase != "" && request.Phase != restorePhaseFinalize) {
		if err := handleRestoreApplyFailure(dirs, markerPath, request.BackupID, errors.New("恢复请求标记损坏")); err != nil {
			return RestoreOutcome{}, err
		}
		return RestoreOutcome{}, nil
	}
	if request.Phase == restorePhaseFinalize {
		controlInfo, controlErr := os.Stat(filepath.Join(dirs.Data, databaseFileName))
		if controlErr != nil || !controlInfo.Mode().IsRegular() {
			availabilityErr := describeControlAvailability(controlInfo, controlErr)
			if recordErr := recordRestoreOutcome(dirs, request.BackupID, false, availabilityErr.Error()); recordErr != nil {
				availabilityErr = errors.Join(availabilityErr, fmt.Errorf("记录恢复结果: %w", recordErr))
			}
			return RestoreOutcome{}, fault.New(fault.CodeRestoreFailed, false, availabilityErr)
		}
		return RestoreOutcome{Applied: true, BackupID: request.BackupID}, nil
	}
	outcome, applyErr := applyRestore(ctx, dirs, request.BackupID)
	if applyErr != nil {
		if err := handleRestoreApplyFailure(dirs, markerPath, request.BackupID, applyErr); err != nil {
			return RestoreOutcome{}, err
		}
		return RestoreOutcome{}, nil // 保留当前库并继续启动。
	}
	request.Phase = restorePhaseFinalize
	data, err = json.MarshalIndent(request, "", "  ")
	if err != nil {
		return RestoreOutcome{}, fault.New(fault.CodeRestoreFailed, false, fmt.Errorf("编码恢复阶段: %w", err))
	}
	if err := writeStateFile(markerPath, data); err != nil {
		return RestoreOutcome{}, fault.New(fault.CodeRestoreFailed, false, fmt.Errorf("持久化恢复阶段: %w", err))
	}
	return outcome, nil
}

// CompletePendingRestore 在 FinalizeRestore 成功提交后持久记录恢复结果并消费 pending。调用失败时
// pending 保留，下一次启动会从待 finalize 阶段恢复；FinalizeRestore 的事务本身可重复执行。
func CompletePendingRestore(dirs appdirs.Dirs, outcome RestoreOutcome) error {
	if !outcome.Applied || outcome.BackupID == "" {
		return fault.New(fault.CodeRestoreFailed, false, fmt.Errorf("恢复结果缺少已应用身份"))
	}
	if err := recordRestoreOutcome(dirs, outcome.BackupID, true, "已原子替换 control.db 并完成恢复后清理"); err != nil {
		return fault.New(fault.CodeRestoreFailed, false, fmt.Errorf("记录恢复结果: %w", err))
	}
	if err := os.Remove(filepath.Join(dirs.State, restorePendingFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fault.New(fault.CodeRestoreFailed, false, fmt.Errorf("消费恢复请求: %w", err))
	}
	return nil
}

func handleRestoreApplyFailure(dirs appdirs.Dirs, markerPath, backupID string, applyErr error) error {
	var continuityErr *restoreContinuityError
	if errors.As(applyErr, &continuityErr) {
		// 当前库可能只剩轮换副本；保留 pending 供修复文件系统条件后的下一次
		// 启动重试，并 fail-closed 阻止 storage.Open 创建空 control.db。
		if recordErr := recordRestoreOutcome(dirs, backupID, false, applyErr.Error()); recordErr != nil {
			applyErr = errors.Join(applyErr, fmt.Errorf("记录恢复结果: %w", recordErr))
		}
		return fault.New(fault.CodeRestoreFailed, false, applyErr)
	}
	controlInfo, controlErr := os.Stat(filepath.Join(dirs.Data, databaseFileName))
	if controlErr != nil || !controlInfo.Mode().IsRegular() {
		availabilityErr := describeControlAvailability(controlInfo, controlErr)
		// 失败发生在轮换前也不能一概继续：若没有可证明可用的当前库，storage.Open
		// 同样会在缺失路径创建空库。保留 pending，让文件系统条件修复后重试。
		failClosedErr := errors.Join(applyErr, availabilityErr)
		if recordErr := recordRestoreOutcome(dirs, backupID, false, failClosedErr.Error()); recordErr != nil {
			failClosedErr = errors.Join(failClosedErr, fmt.Errorf("记录恢复结果: %w", recordErr))
		}
		return fault.New(fault.CodeRestoreFailed, false, failClosedErr)
	}
	if err := recordRestoreOutcome(dirs, backupID, false, applyErr.Error()); err != nil {
		return fault.New(fault.CodeRestoreFailed, false, errors.Join(applyErr, fmt.Errorf("记录恢复结果: %w", err)))
	}
	if err := os.Remove(markerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fault.New(fault.CodeRestoreFailed, false, errors.Join(applyErr, fmt.Errorf("消费恢复请求: %w", err)))
	}
	return nil
}

func describeControlAvailability(info os.FileInfo, err error) error {
	if err == nil && info != nil && !info.Mode().IsRegular() {
		return fmt.Errorf("当前 control.db 不是普通文件")
	}
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("当前 control.db 不存在")
	}
	if err != nil {
		return fmt.Errorf("检查当前 control.db: %w", err)
	}
	return fmt.Errorf("当前 control.db 不可用")
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

	// 原子替换：先轮换当前库，再落位候选；只有确认旧库已回到原路径时，失败后才允许
	// 继续启动。回滚也失败时必须阻止 bootstrap 打开缺失路径并创建空 control.db。
	rotated := filepath.Join(dirs.Data, fmt.Sprintf("%s%d.bak", preRestorePrefix, time.Now().UnixNano()))
	rotatedCurrent, err := placeRestoreCandidate(controlPath, incoming, rotated, osRestoreFileOps)
	if err != nil {
		return RestoreOutcome{}, err
	}
	rotatedPath := ""
	if rotatedCurrent {
		rotatedPath = rotated
	}
	return RestoreOutcome{Applied: true, BackupID: backupID, RotatedPath: rotatedPath}, nil
}

func placeRestoreCandidate(controlPath, incoming, rotated string, ops restoreFileOps) (bool, error) {
	rotatedCurrent := false
	if _, statErr := ops.stat(controlPath); statErr == nil {
		if err := ops.rename(controlPath, rotated); err != nil {
			_ = ops.remove(incoming)
			return false, fmt.Errorf("轮换当前 control.db: %w", err)
		}
		rotatedCurrent = true
	} else if !errors.Is(statErr, os.ErrNotExist) {
		_ = ops.remove(incoming)
		return false, fmt.Errorf("检查当前 control.db: %w", statErr)
	}

	rollback := func(stageErr error) error {
		if rotatedCurrent {
			if rollbackErr := ops.rename(rotated, controlPath); rollbackErr == nil {
				_ = ops.remove(incoming)
				return stageErr
			} else {
				stageErr = errors.Join(stageErr, fmt.Errorf("回滚当前 control.db: %w", rollbackErr))
			}
		}
		_ = ops.remove(incoming)
		// 没有旧库可回滚，或旧库回滚失败：controlPath 的连续性无法证明。
		return &restoreContinuityError{cause: stageErr}
	}

	// 旧库已经离开原路径后，WAL/SHM 清理也属于替换协议的一部分；不能忽略失败后
	// 继续落位候选，否则过期日志可能遮蔽新主库。
	for _, sidecar := range []string{controlPath + "-wal", controlPath + "-shm"} {
		if err := ops.remove(sidecar); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, rollback(fmt.Errorf("清理旧 control.db sidecar: %w", err))
		}
	}
	if err := ops.rename(incoming, controlPath); err != nil {
		return false, rollback(fmt.Errorf("落位恢复候选: %w", err))
	}
	return rotatedCurrent, nil
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

func recordRestoreOutcome(dirs appdirs.Dirs, backupID string, applied bool, detail string) error {
	data, err := json.MarshalIndent(restoreLast{BackupID: backupID, Applied: applied, Detail: detail, FinishedAt: time.Now().UTC()}, "", "  ")
	if err != nil {
		return err
	}
	return writeStateFile(filepath.Join(dirs.State, restoreLastFile), data)
}

func writeStateFile(path string, data []byte) (err error) {
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if err != nil {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err = temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err = temporary.Write(data); err != nil {
		return err
	}
	if err = temporary.Sync(); err != nil {
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	if err = os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return nil
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
