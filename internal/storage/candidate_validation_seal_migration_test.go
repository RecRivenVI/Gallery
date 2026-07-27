package storage

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"
)

func openCatalogThroughV18(t *testing.T) (*sql.DB, migration) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "catalog.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
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
	v19Index := -1
	for index, item := range migrations {
		if item.version == 19 {
			v19Index = index
			break
		}
	}
	if v19Index < 1 || migrations[v19Index-1].version != 18 {
		t.Fatalf("测试要求 v19 的直接前序为 v18: %+v", migrations)
	}
	for _, item := range migrations[:v19Index] {
		if err := applyMigration(ctx, db, item); err != nil {
			t.Fatal(err)
		}
	}
	return db, migrations[v19Index]
}

func TestCandidateValidationSealMigrationAddsConstrainedCascadeTable(t *testing.T) {
	db, v19 := openCatalogThroughV18(t)
	if _, err := db.Exec(`INSERT INTO catalog_revisions
(catalog_revision_id, job_id, source_id, status, created_at)
VALUES ('cat', 'job', 'source', 'staging', 1);
INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, projection_job_id)
VALUES ('overlay', 'cat', 1, 'staging', 1, 'job')`); err != nil {
		t.Fatal(err)
	}
	if err := applyMigration(context.Background(), db, v19); err != nil {
		t.Fatal(err)
	}
	var sealCount int
	if err := db.QueryRow("SELECT count(*) FROM candidate_validation_seals").Scan(&sealCount); err != nil {
		t.Fatal(err)
	}
	if sealCount != 0 {
		t.Fatalf("v18 staging candidate 被迁移错误地视为已验证: %d", sealCount)
	}
	var historicalBase string
	if err := db.QueryRow(`SELECT base_overlay_revision_id FROM overlay_projection_revisions
WHERE overlay_revision_id='overlay'`).Scan(&historicalBase); err != nil {
		t.Fatal(err)
	}
	if historicalBase != "" {
		t.Fatalf("v18 历史 Overlay 被伪造创建基线: %q", historicalBase)
	}
	if _, err := db.Exec(`INSERT INTO candidate_validation_seals
(catalog_revision_id, overlay_revision_id, candidate_kind, validation_version, validated_at)
VALUES ('cat', 'overlay', 'catalog', 1, 2)`); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, kind string
		version    int
	}{
		{name: "invalid-kind", kind: "unknown", version: 1},
		{name: "invalid-version", kind: "overlay", version: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := db.Exec(`UPDATE candidate_validation_seals
SET candidate_kind=?, validation_version=? WHERE catalog_revision_id='cat' AND overlay_revision_id='overlay'`,
				test.kind, test.version); err == nil {
				t.Fatal("候选验证封印约束接受了无效值")
			}
		})
	}
	if _, err := db.Exec(`INSERT INTO candidate_validation_seals
(catalog_revision_id, overlay_revision_id, candidate_kind, validation_version, validated_at)
VALUES ('cat', 'overlay-missing', 'catalog', 1, 2)`); err == nil {
		t.Fatal("候选验证封印接受了错配的 catalog/overlay 外键")
	}
	var version, migrationCount, foreignKeyErrors int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM gallery_schema_migrations WHERE version=19`).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if version != 19 || migrationCount != 1 {
		t.Fatalf("迁移登记错误: version=%d migration=%d", version, migrationCount)
	}
	if err := db.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&foreignKeyErrors); err != nil {
		t.Fatal(err)
	}
	if foreignKeyErrors != 0 {
		t.Fatalf("v19 foreign_key_check=%d", foreignKeyErrors)
	}
	if _, err := db.Exec(`INSERT INTO catalog_revisions
(catalog_revision_id, job_id, source_id, status, created_at)
VALUES ('cat-2', 'job-2', 'source-2', 'staging', 1);
INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, projection_job_id,
 base_overlay_revision_id)
VALUES ('overlay-2', 'cat-2', 1, 'staging', 1, 'job-2', 'overlay');
INSERT INTO candidate_validation_seals
(catalog_revision_id, overlay_revision_id, candidate_kind, validation_version, validated_at)
VALUES ('cat-2', 'overlay-2', 'catalog', 1, 2)`); err != nil {
		t.Fatal(err)
	}
	var persistedBase string
	if err := db.QueryRow(`SELECT base_overlay_revision_id FROM overlay_projection_revisions
WHERE overlay_revision_id='overlay-2'`).Scan(&persistedBase); err != nil {
		t.Fatal(err)
	}
	if persistedBase != "overlay" {
		t.Fatalf("v19 Overlay 创建基线未持久化: %q", persistedBase)
	}
	if _, err := db.Exec(`DELETE FROM catalog_revisions WHERE catalog_revision_id='cat-2'`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM candidate_validation_seals WHERE catalog_revision_id='cat-2'`).Scan(&sealCount); err != nil {
		t.Fatal(err)
	}
	if sealCount != 0 {
		t.Fatalf("Catalog revision 删除后封印未级联清理: %d", sealCount)
	}
	if _, err := db.Exec(`DELETE FROM overlay_projection_revisions WHERE overlay_revision_id='overlay'`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM candidate_validation_seals").Scan(&sealCount); err != nil {
		t.Fatal(err)
	}
	if sealCount != 0 {
		t.Fatalf("Overlay revision 删除后封印未级联清理: %d", sealCount)
	}
}
