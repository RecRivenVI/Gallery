package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
)

// SchemaState 概括某个数据库已应用 migration 的版本与内容身份。version 是已应用的最高
// migration version；checksum 是全部已应用 migration「version:sha256」序列的组合 SHA-256，
// 用于在备份/恢复时判定内容一致而不暴露具体 migration 明细。
type SchemaState struct {
	Version  int64
	Checksum string
}

// ReadSchemaState 从给定连接的 gallery_schema_migrations 表读取已应用 migration 概况。
func ReadSchemaState(ctx context.Context, db *sql.DB) (SchemaState, error) {
	rows, err := db.QueryContext(ctx, "SELECT version, sha256 FROM gallery_schema_migrations ORDER BY version")
	if err != nil {
		return SchemaState{}, err
	}
	defer rows.Close()
	var records [][2]string
	var maxVersion int64
	for rows.Next() {
		var version int64
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return SchemaState{}, err
		}
		if version > maxVersion {
			maxVersion = version
		}
		records = append(records, [2]string{fmt.Sprintf("%d", version), checksum})
	}
	if err := rows.Err(); err != nil {
		return SchemaState{}, err
	}
	return SchemaState{Version: maxVersion, Checksum: combineChecksum(records)}, nil
}

// EmbeddedSchemaState 返回当前程序为某 role 内嵌的最高 migration version 与组合校验和，
// 供恢复时判定备份是否来自未来不兼容版本。
func EmbeddedSchemaState(role Role) (SchemaState, error) {
	if role != RoleControl && role != RoleCatalog {
		return SchemaState{}, fmt.Errorf("未知数据库 role")
	}
	sub, err := fs.Sub(migrationFiles, "migrations/"+string(role))
	if err != nil {
		return SchemaState{}, err
	}
	migrations, err := readMigrations(sub)
	if err != nil {
		return SchemaState{}, err
	}
	var records [][2]string
	var maxVersion int64
	for _, item := range migrations {
		if item.version > maxVersion {
			maxVersion = item.version
		}
		records = append(records, [2]string{fmt.Sprintf("%d", item.version), item.sha256})
	}
	return SchemaState{Version: maxVersion, Checksum: combineChecksum(records)}, nil
}

func combineChecksum(records [][2]string) string {
	sort.Slice(records, func(i, j int) bool { return records[i][0] < records[j][0] })
	hasher := sha256.New()
	for _, record := range records {
		hasher.Write([]byte(record[0]))
		hasher.Write([]byte{':'})
		hasher.Write([]byte(record[1]))
		hasher.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hasher.Sum(nil))
}
