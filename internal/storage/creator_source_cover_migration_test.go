package storage

import (
	"context"
	"database/sql"
	"io/fs"
	"testing"
)

func openCatalogThroughV19(t *testing.T) (*sql.DB, migration) {
	t.Helper()
	db, v19 := openCatalogThroughV18(t)
	if err := applyMigration(context.Background(), db, v19); err != nil {
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
	for _, item := range migrations {
		if item.version == 20 {
			return db, item
		}
	}
	t.Fatal("缺少 catalog v20 migration")
	return nil, migration{}
}

func TestCreatorSourceCoverMigrationBackfillsDeterministicCandidates(t *testing.T) {
	db, v20 := openCatalogThroughV19(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `INSERT INTO catalog_revisions
(catalog_revision_id, job_id, source_id, status, created_at, published_at)
VALUES ('cat', 'job', 'source-a', 'published', 1, 1);
INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, published_at)
VALUES ('overlay', 'cat', 1, 'published', 1, 1);
INSERT INTO creator_projections
(catalog_revision_id, overlay_revision_id, creator_id, name, sort_name_key)
VALUES ('cat', 'overlay', 'creator', 'Creator', 'creator');
INSERT INTO work_projections
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id, title, creator,
 tags_json, filenames_text, normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text,
 sort_title_key, hidden, search_title_norm, search_creator_norm, search_tags_norm, search_filenames_norm,
 cover_media_id, published_at_ns, published_at_raw, published_at_parser)
VALUES
('cat', 'overlay', 'work-a-old', 'source-a', 'a-old', 'library', 'A old', 'Creator',
 '[]', '', '', '', '', '', 0, '', '', '', '', 'media-a-old', 1, 'raw', 'gallery-work-date-v1'),
('cat', 'overlay', 'work-a-new', 'source-a', 'a-new', 'library', 'A new', 'Creator',
 '[]', '', '', '', '', '', 0, '', '', '', '', 'media-a-new', 3, 'raw', 'gallery-work-date-v1'),
('cat', 'overlay', 'work-b', 'source-b', 'b', 'library', 'B', 'Creator',
 '[]', '', '', '', '', '', 0, '', '', '', '', 'media-b', 2, 'raw', 'gallery-work-date-v1');
INSERT INTO media_projections
(catalog_revision_id, overlay_revision_id, media_id, work_id, source_id, source_key, relative_path,
 media_kind, mime_type, size_bytes, algorithm, digest, location_status, content_verification_state,
 verified_at, ordinal, base_ordinal)
VALUES
('cat', 'overlay', 'media-a-old', 'work-a-old', 'source-a', 'a-old.jpg', 'a-old.jpg',
 'image', 'image/jpeg', 1, 'sha256-v1', 'a', 'present', 'content_verified', 1, 0, 0),
('cat', 'overlay', 'media-a-new', 'work-a-new', 'source-a', 'a-new.jpg', 'a-new.jpg',
 'image', 'image/jpeg', 1, 'sha256-v1', 'b', 'present', 'content_verified', 1, 0, 0),
('cat', 'overlay', 'media-b', 'work-b', 'source-b', 'b.jpg', 'b.jpg',
 'image', 'image/jpeg', 1, 'sha256-v1', 'c', 'present', 'content_verified', 1, 0, 0);
INSERT INTO work_creator_relations
(catalog_revision_id, overlay_revision_id, work_id, creator_id, role, ordinal)
VALUES
('cat', 'overlay', 'work-a-old', 'creator', 'author', 0),
('cat', 'overlay', 'work-a-new', 'creator', 'author', 0),
('cat', 'overlay', 'work-b', 'creator', 'author', 0);
INSERT INTO aggregate_cover_projections
(catalog_revision_id, overlay_revision_id, scope_kind, scope_id, cover_media_id, published_at_ns)
VALUES ('cat', 'overlay', 'creator', 'creator', 'media-a-new', 3)`); err != nil {
		t.Fatal(err)
	}
	if err := applyMigration(ctx, db, v20); err != nil {
		t.Fatal(err)
	}

	rows, err := db.QueryContext(ctx, `SELECT source_id, cover_media_id, published_at_ns, work_id
FROM creator_source_cover_projections ORDER BY source_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type candidate struct {
		sourceID, mediaID, workID string
		published                 int64
	}
	var candidates []candidate
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.sourceID, &item.mediaID, &item.published, &item.workID); err != nil {
			t.Fatal(err)
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0] != (candidate{"source-a", "media-a-new", "work-a-new", 3}) ||
		candidates[1] != (candidate{"source-b", "media-b", "work-b", 2}) {
		t.Fatalf("v20 回填候选错误: %+v", candidates)
	}
	var aggregateSourceID string
	if err := db.QueryRowContext(ctx, `SELECT source_id FROM aggregate_cover_projections
WHERE catalog_revision_id='cat' AND overlay_revision_id='overlay' AND scope_kind='creator' AND scope_id='creator'`).
		Scan(&aggregateSourceID); err != nil {
		t.Fatal(err)
	}
	if aggregateSourceID != "source-a" {
		t.Fatalf("v20 全局聚合 Source 回填错误: %q", aggregateSourceID)
	}

	var version, migrationCount, foreignKeyErrors int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM gallery_schema_migrations WHERE version=20").Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM pragma_foreign_key_check").Scan(&foreignKeyErrors); err != nil {
		t.Fatal(err)
	}
	if version != 20 || migrationCount != 1 || foreignKeyErrors != 0 {
		t.Fatalf("v20 状态错误: version=%d migration=%d foreignKeys=%d", version, migrationCount, foreignKeyErrors)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM overlay_projection_revisions WHERE overlay_revision_id='overlay'"); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM creator_source_cover_projections").Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("Overlay revision 删除后 v20 候选未级联清理: %d", remaining)
	}
}
