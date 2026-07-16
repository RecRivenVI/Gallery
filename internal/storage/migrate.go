package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
)

var migrationNamePattern = regexp.MustCompile(`^(\d{5})_([a-z0-9][a-z0-9_-]*)\.sql$`)

type migration struct {
	version int64
	name    string
	sql     string
	sha256  string
}

// migrate 是 SQLite 专用的 forward-only runner：每个嵌入迁移独立事务，历史内容由 SHA-256 锁定。
func migrate(ctx context.Context, db *sql.DB, role Role) error {
	sub, err := fs.Sub(migrationFiles, "migrations/"+string(role))
	if err != nil {
		return err
	}
	migrations, err := readMigrations(sub)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS gallery_schema_migrations (
    version INTEGER PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) STRICT`); err != nil {
		return err
	}

	known := make(map[int64]migration, len(migrations))
	for _, item := range migrations {
		known[item.version] = item
	}
	rows, err := db.QueryContext(ctx, "SELECT version, name, sha256 FROM gallery_schema_migrations ORDER BY version")
	if err != nil {
		return err
	}
	for rows.Next() {
		var version int64
		var name, checksum string
		if err := rows.Scan(&version, &name, &checksum); err != nil {
			rows.Close()
			return err
		}
		item, ok := known[version]
		if !ok {
			rows.Close()
			return fmt.Errorf("数据库包含当前程序未知的 migration version %d", version)
		}
		if item.name != name || item.sha256 != checksum {
			rows.Close()
			return fmt.Errorf("migration %05d 内容或名称已改变", version)
		}
		delete(known, version)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	for _, item := range migrations {
		if _, pending := known[item.version]; !pending {
			continue
		}
		if err := applyMigration(ctx, db, item); err != nil {
			return fmt.Errorf("migration %05d_%s: %w", item.version, item.name, err)
		}
	}
	return nil
}

func readMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	items := make([]migration, 0, len(entries))
	seen := make(map[int64]struct{})
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		matches := migrationNamePattern.FindStringSubmatch(filepath.ToSlash(entry.Name()))
		if matches == nil {
			return nil, fmt.Errorf("无效 migration 文件名 %q", entry.Name())
		}
		version, err := strconv.ParseInt(matches[1], 10, 64)
		if err != nil || version < 1 {
			return nil, fmt.Errorf("无效 migration version")
		}
		if _, exists := seen[version]; exists {
			return nil, fmt.Errorf("重复 migration version %d", version)
		}
		seen[version] = struct{}{}
		content, err := fs.ReadFile(fsys, entry.Name())
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(content)
		items = append(items, migration{
			version: version, name: matches[2], sql: string(content), sha256: hex.EncodeToString(sum[:]),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].version < items[j].version })
	return items, nil
}

func applyMigration(ctx context.Context, db *sql.DB, item migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, item.sql); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO gallery_schema_migrations (version, name, sha256) VALUES (?, ?, ?)",
		item.version, item.name, item.sha256,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", item.version)); err != nil {
		return err
	}
	return tx.Commit()
}
