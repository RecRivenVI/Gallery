package storage

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	_ "modernc.org/sqlite"
)

type Role string

const (
	RoleControl Role = "control"
	RoleCatalog Role = "catalog"
)

//go:embed migrations/control/*.sql migrations/catalog/*.sql
var migrationFiles embed.FS

type Database struct {
	role Role
	path string
	db   *sql.DB
}

func (d *Database) Role() Role   { return d.role }
func (d *Database) SQL() *sql.DB { return d.db }

func (d *Database) Close() error {
	if d == nil || d.db == nil {
		return nil
	}
	return d.db.Close()
}

type Store struct {
	Control *Database
	Catalog *Database
}

func Open(ctx context.Context, dirs appdirs.Dirs) (*Store, error) {
	control, err := openDatabase(ctx, RoleControl, filepath.Join(dirs.Data, "control.db"))
	if err != nil {
		return nil, err
	}
	catalog, err := openDatabase(ctx, RoleCatalog, filepath.Join(dirs.Data, "catalog.db"))
	if err != nil {
		_ = control.Close()
		return nil, err
	}
	return &Store{Control: control, Catalog: catalog}, nil
}

// OpenControlAt 打开指定路径的 control 数据库并执行 forward 迁移，供备份验证与恢复暂存使用。
// 调用方负责 Close。它绝不隐式创建 catalog.db，也不参与 AppDirs 单写者锁；恢复流程必须在持有
// 单写者锁、且当前 control.db 未被其他连接打开时使用。
func OpenControlAt(ctx context.Context, path string) (*Database, error) {
	return openDatabase(ctx, RoleControl, path)
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	var errs []error
	if err := s.Catalog.Close(); err != nil {
		errs = append(errs, err)
	}
	if err := s.Control.Close(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func openDatabase(ctx context.Context, role Role, path string) (*Database, error) {
	if role != RoleControl && role != RoleCatalog {
		return nil, fault.New(fault.CodeDatabaseOpen, false, fmt.Errorf("未知数据库 role"))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fault.New(fault.CodeDatabaseOpen, false, err)
	}
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fault.New(fault.CodeDatabaseOpen, false, err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fault.New(fault.CodeDatabaseOpen, true, err)
	}
	if err := migrate(ctx, db, role); err != nil {
		_ = db.Close()
		return nil, fault.New(fault.CodeMigrationFailed, false, err)
	}
	if err := verifyDatabase(ctx, db, role); err != nil {
		_ = db.Close()
		return nil, fault.New(fault.CodeDatabaseOpen, false, err)
	}
	return &Database{role: role, path: path, db: db}, nil
}

func verifyDatabase(ctx context.Context, db *sql.DB, role Role) error {
	var journalMode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		return err
	}
	if !strings.EqualFold(journalMode, "wal") {
		return fmt.Errorf("数据库未启用 WAL")
	}
	table := "gallery_" + string(role) + "_meta"
	var storedRole string
	query := "SELECT value FROM " + table + " WHERE key = 'database_role'" // table 只来自封闭 Role 枚举。
	if err := db.QueryRowContext(ctx, query).Scan(&storedRole); err != nil {
		return err
	}
	if storedRole != string(role) {
		return fmt.Errorf("数据库 role 不匹配")
	}
	return nil
}
