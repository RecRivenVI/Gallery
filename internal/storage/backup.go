package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
)

// Backupper 明确区分 control 与 catalog 的独立备份生命周期。
type Backupper interface {
	Backup(ctx context.Context, destination string) error
}

func (d *Database) Backup(ctx context.Context, destination string) (resultErr error) {
	defer func() {
		if resultErr == nil {
			return
		}
		var structured *fault.Error
		if !errors.As(resultErr, &structured) {
			resultErr = fault.New(fault.CodeBackupFailed, false, resultErr)
		}
	}()
	if d == nil || d.db == nil {
		return fmt.Errorf("数据库未打开")
	}
	absDestination, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	absSource, err := filepath.Abs(d.path)
	if err != nil {
		return err
	}
	if filepath.Clean(absDestination) == filepath.Clean(absSource) {
		return fmt.Errorf("备份目标不能是源数据库")
	}
	if _, err := os.Stat(absDestination); err == nil {
		return fmt.Errorf("备份目标已存在")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absDestination), 0o700); err != nil {
		return err
	}
	if _, err := d.db.ExecContext(ctx, "VACUUM main INTO ?", filepath.ToSlash(absDestination)); err != nil {
		return err
	}
	if err := verifyBackup(ctx, absDestination, d.role); err != nil {
		_ = os.Remove(absDestination)
		return err
	}
	return nil
}

func verifyBackup(ctx context.Context, path string, role Role) error {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&_pragma=foreign_keys(ON)")
	if err != nil {
		return err
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("备份完整性检查失败")
	}
	table := "gallery_" + string(role) + "_meta"
	var storedRole string
	query := "SELECT value FROM " + table + " WHERE key = 'database_role'" // table 只来自封闭 Role 枚举。
	if err := db.QueryRowContext(ctx, query).Scan(&storedRole); err != nil {
		return err
	}
	if storedRole != string(role) {
		return fmt.Errorf("备份数据库 role 不匹配")
	}
	return nil
}
