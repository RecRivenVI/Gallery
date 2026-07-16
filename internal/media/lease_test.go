package media_test

import (
	"context"
	"crypto/sha256"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/media"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/storage"
)

func TestBlobReadLeaseIsPersistedAndReleased(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	lease, err := media.AcquireBlobReadLease(ctx, store.Catalog.SQL(),
		clock.Fixed{Time: time.Date(2026, 7, 16, 7, 0, 0, 0, time.UTC)},
		domain.NewSHA256BlobRef(sha256.Sum256([]byte("blob"))), nil)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	_ = store.Catalog.SQL().QueryRowContext(ctx, "SELECT count(*) FROM blob_read_leases").Scan(&count)
	if count != 1 {
		t.Fatalf("读取 lease 未持久化: %d", count)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	_ = store.Catalog.SQL().QueryRowContext(ctx, "SELECT count(*) FROM blob_read_leases").Scan(&count)
	if count != 0 {
		t.Fatalf("读取 lease 未释放: %d", count)
	}
}
