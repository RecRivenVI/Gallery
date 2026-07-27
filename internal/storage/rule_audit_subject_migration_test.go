package storage

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"
)

func TestRuleAuditSubjectMigrationBackfillsExistingActions(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
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
	v22Index := -1
	for index, item := range migrations {
		if item.version == 22 {
			v22Index = index
			break
		}
	}
	if v22Index < 1 || migrations[v22Index-1].version != 21 {
		t.Fatalf("测试要求 v22 的直接前序为 v21: %+v", migrations)
	}
	for _, item := range migrations[:v22Index] {
		if err := applyMigration(ctx, db, item); err != nil {
			t.Fatal(err)
		}
	}
	const packageID = "rpack_00000000-0000-7000-8000-000000000001"
	const firstHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const secondHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := db.ExecContext(ctx, `INSERT INTO rule_packages
(package_id, rule_set_id, name, description, status, extension_requirements_json, created_by, created_at, updated_at, revision)
VALUES (?, 'rset_00000000-0000-7000-8000-000000000001', 'audit migration', '', 'active', '{}', 'owner', 1, 1, 1)`, packageID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO rule_audits
(audit_id, package_id, action, from_semantic_hash, to_semantic_hash, reason, actor_id, created_at)
VALUES
('raudit_00000000-0000-7000-8000-000000000001', ?, 'publish', NULL, ?, 'publish', 'owner', 1),
('raudit_00000000-0000-7000-8000-000000000002', ?, 'rollback', ?, ?, 'rollback', 'owner', 2),
('raudit_00000000-0000-7000-8000-000000000003', ?, 'deprecate', ?, NULL, 'deprecate', 'owner', 3)`,
		packageID, firstHash, packageID, secondHash, firstHash, packageID, secondHash); err != nil {
		t.Fatal(err)
	}
	if err := applyMigration(ctx, db, migrations[v22Index]); err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(ctx, `SELECT action, subject_type, subject_id FROM rule_audits ORDER BY created_at`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := []struct{ action, subjectType, subjectID string }{
		{"publish", "version", firstHash},
		{"rollback", "package", packageID},
		{"deprecate", "version", secondHash},
	}
	count := 0
	for rows.Next() {
		index := count
		if index >= len(want) {
			t.Fatal("迁移产生额外审计行")
		}
		var action, subjectType, subjectID string
		if err := rows.Scan(&action, &subjectType, &subjectID); err != nil {
			t.Fatal(err)
		}
		if got := (struct{ action, subjectType, subjectID string }{action, subjectType, subjectID}); got != want[index] {
			t.Fatalf("审计回填[%d]=%+v want=%+v", index, got, want[index])
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != len(want) {
		t.Fatalf("迁移审计行数=%d want=%d", count, len(want))
	}
	var indexCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='index' AND name='rule_audits_subject_idx'`).Scan(&indexCount); err != nil || indexCount != 1 {
		t.Fatalf("subject index 缺失: count=%d err=%v", indexCount, err)
	}
}
