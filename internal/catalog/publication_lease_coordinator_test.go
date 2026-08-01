package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/storage"
)

func TestPublicationLeaseCoordinatorDefersVerifiesAndFlushes(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	dbs, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer dbs.Close()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	store, err := NewStore(dbs.Catalog.SQL(), clock.Fixed{Time: now}, gcNoIDs{})
	if err != nil {
		t.Fatal(err)
	}
	insertLeasePublicationFixture(t, dbs.Catalog.SQL())
	leases := store.PublicationLeases()
	leases.BeginDeferred()
	if err := leases.Create(ctx, "lease-pending", "publication-old", "scope-a",
		now.Add(5*time.Minute).Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	if err := leases.Create(ctx, "lease-closed", "publication-old", "scope-a",
		now.Add(5*time.Minute).Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	if err := leases.Delete(ctx, "lease-closed"); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := dbs.Catalog.SQL().QueryRowContext(ctx,
		"SELECT count(*) FROM query_publication_leases").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("deferred lease 在 flush 前已落盘: %d", rows)
	}
	valid, err := leases.Verify(ctx, "lease-pending", "publication-old", "scope-a", now.Unix())
	if err != nil || !valid {
		t.Fatalf("内存 lease 验证失败: valid=%v err=%v", valid, err)
	}
	valid, err = leases.Verify(ctx, "lease-pending", "publication-old", "scope-b", now.Unix())
	if err != nil || valid {
		t.Fatalf("错误授权 scope 通过: valid=%v err=%v", valid, err)
	}
	if err := leases.FlushAndEnd(ctx); err != nil {
		t.Fatal(err)
	}
	if err := dbs.Catalog.SQL().QueryRowContext(ctx,
		"SELECT count(*) FROM query_publication_leases").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("flush 后 lease 行数=%d，期望 1", rows)
	}
	valid, err = leases.Verify(ctx, "lease-pending", "publication-old", "scope-a", now.Unix())
	if err != nil || !valid {
		t.Fatalf("持久 lease 验证失败: valid=%v err=%v", valid, err)
	}

	leases.BeginDeferred()
	if err := leases.Delete(ctx, "lease-pending"); err != nil {
		t.Fatal(err)
	}
	if err := leases.FlushAndEnd(ctx); err != nil {
		t.Fatal(err)
	}
	if err := dbs.Catalog.SQL().QueryRowContext(ctx,
		"SELECT count(*) FROM query_publication_leases").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("deferred delete 未落盘: %d", rows)
	}
}

func TestDeferredPublicationLeaseProtectsGarbageCollect(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	dbs, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer dbs.Close()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	store, err := NewStore(dbs.Catalog.SQL(), clock.Fixed{Time: now}, gcNoIDs{})
	if err != nil {
		t.Fatal(err)
	}
	insertLeasePublicationFixture(t, dbs.Catalog.SQL())
	leases := store.PublicationLeases()
	leases.BeginDeferred()
	if err := leases.Create(ctx, "lease-pending", "publication-old", "scope-a",
		now.Add(5*time.Minute).Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	result, err := store.GarbageCollect(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Publications != 0 {
		t.Fatalf("GC 回收了内存 lease 保护的 publication: %+v", result)
	}
	var publications int
	if err := dbs.Catalog.SQL().QueryRowContext(ctx,
		"SELECT count(*) FROM query_publications WHERE query_publication_id='publication-old'").Scan(&publications); err != nil {
		t.Fatal(err)
	}
	if publications != 1 {
		t.Fatalf("内存 lease 保护后 publication 数量=%d", publications)
	}
	if err := leases.FlushAndEnd(ctx); err != nil {
		t.Fatal(err)
	}
}

func insertLeasePublicationFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `INSERT INTO catalog_revisions
(catalog_revision_id, job_id, source_id, status, created_at, published_at)
VALUES ('catalog-old', 'job-old', 'source-old', 'published', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, published_at)
VALUES ('overlay-old', 'catalog-old', 1, 'published', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO query_publications
(query_publication_id, catalog_revision_id, overlay_revision_id, job_id, control_watermark, created_at)
VALUES ('publication-old', 'catalog-old', 'overlay-old', 'job-old', 1, 1)`); err != nil {
		t.Fatal(err)
	}
}
