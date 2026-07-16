package catalog_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/media"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	galleryquery "github.com/RecRivenVI/gallery/internal/query"
	"github.com/RecRivenVI/gallery/internal/querytext"
	"github.com/RecRivenVI/gallery/internal/storage"
)

func TestGarbageCollectHonorsCursorAndBlobReadLeases(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	fixed := clock.Fixed{Time: now}
	dirs := appdirs.UnderRoot(t.TempDir())
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), fixed, fixedIDs{})
	if err != nil {
		t.Fatal(err)
	}

	oldPublication, oldCatalog, oldDigest := seedGCPublication(t, store, 1, true)
	queryService, err := galleryquery.NewService(ctx, store.Control.SQL(), store.Catalog.SQL(), fixed, nil)
	if err != nil {
		t.Fatal(err)
	}
	scope := galleryquery.AuthorizationScope("owner", []string{"library.read"})
	page, err := queryService.Search(ctx, galleryquery.Request{Limit: 1, AuthorizationScope: scope})
	if err != nil || page.NextCursor == "" {
		t.Fatalf("未签发旧 publication 游标: %+v %v", page, err)
	}
	blobLease, err := media.AcquireBlobReadLease(ctx, store.Catalog.SQL(), fixed,
		domain.ContentBlobRef{Algorithm: domain.BlobAlgorithmSHA256V1, Digest: oldDigest}, nil)
	if err != nil {
		t.Fatal(err)
	}
	seedGCPublication(t, store, 2, false)
	if _, err := store.Catalog.SQL().ExecContext(ctx,
		"INSERT INTO catalog_revisions VALUES ('cat_018f47d2-5c16-7a44-a8a0-999999999999', 'job_018f47d2-5c16-7a44-a8a0-999999999999', 'src_gc', 'staging', 1, NULL)"); err != nil {
		t.Fatal(err)
	}

	result, err := catalogStore.GarbageCollect(ctx, 0)
	if err != nil || result.Publications != 0 {
		t.Fatalf("活动租约未保护旧快照: %+v %v", result, err)
	}
	assertPublicationCount(t, store, oldPublication, 1)

	if _, err := store.Catalog.SQL().ExecContext(ctx,
		"UPDATE query_publication_leases SET expires_at=? WHERE query_publication_id=?",
		now.Add(-time.Second).Unix(), oldPublication); err != nil {
		t.Fatal(err)
	}
	result, err = catalogStore.GarbageCollect(ctx, 0)
	if err != nil || result.ExpiredQueryLeases != 1 || result.Publications != 0 {
		t.Fatalf("Blob 读取租约未保护旧快照: %+v %v", result, err)
	}
	assertPublicationCount(t, store, oldPublication, 1)

	if err := blobLease.Close(); err != nil {
		t.Fatal(err)
	}
	result, err = catalogStore.GarbageCollect(ctx, 0)
	if err != nil || result.Publications != 1 || result.OverlayRevisions != 1 || result.CatalogRevisions != 1 {
		t.Fatalf("过期旧快照未完整回收: %+v %v", result, err)
	}
	assertPublicationCount(t, store, oldPublication, 0)
	var count int
	if err := store.Catalog.SQL().QueryRowContext(ctx,
		"SELECT count(*) FROM catalog_revisions WHERE status='staging'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("GC 误删活动 staging: count=%d err=%v", count, err)
	}
	if err := store.Catalog.SQL().QueryRowContext(ctx,
		"SELECT count(*) FROM catalog_revisions WHERE catalog_revision_id=?", oldCatalog).Scan(&count); err != nil || count != 0 {
		t.Fatalf("旧 Catalog revision 残留: count=%d err=%v", count, err)
	}
	if err := store.Catalog.SQL().QueryRowContext(ctx,
		"SELECT count(*) FROM work_search WHERE catalog_revision_id=?", oldCatalog).Scan(&count); err != nil || count != 0 {
		t.Fatalf("旧 FTS 行残留: count=%d err=%v", count, err)
	}
	_, err = queryService.Search(ctx, galleryquery.Request{Limit: 1, Cursor: page.NextCursor, AuthorizationScope: scope})
	assertFaultCode(t, err, fault.CodeCursorExpired)
}

func seedGCPublication(t *testing.T, store *storage.Store, sequence int, twoWorks bool) (string, string, string) {
	t.Helper()
	ctx := context.Background()
	catalogID := fmt.Sprintf("cat_018f47d2-5c16-7a44-a8a0-%012d", sequence)
	overlayID := fmt.Sprintf("ovr_018f47d2-5c16-7a44-a8a0-%012d", sequence)
	jobID := fmt.Sprintf("job_018f47d2-5c16-7a44-a8a0-%012d", sequence)
	publicationID := fmt.Sprintf("qpub_018f47d2-5c16-7a44-a8a0-%012d", sequence)
	digest := fmt.Sprintf("%064x", sequence)
	if _, err := store.Catalog.SQL().ExecContext(ctx,
		"INSERT INTO catalog_revisions VALUES (?, ?, 'src_gc', 'published', ?, ?)",
		catalogID, jobID, sequence, sequence); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Catalog.SQL().ExecContext(ctx, `INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, published_at)
VALUES (?, ?, ?, 'published', ?, ?)`, overlayID, catalogID, sequence, sequence, sequence); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Catalog.SQL().ExecContext(ctx,
		"INSERT INTO query_publications VALUES (?, ?, ?, ?, ?, ?)",
		publicationID, catalogID, overlayID, jobID, sequence, sequence); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Catalog.SQL().ExecContext(ctx,
		"INSERT INTO content_blobs VALUES (?, 'sha256-v1', ?, 1)", catalogID, digest); err != nil {
		t.Fatal(err)
	}
	workCount := 1
	if twoWorks {
		workCount = 2
	}
	for index := 0; index < workCount; index++ {
		workID := fmt.Sprintf("wrk_018f47d2-5c16-7a44-a8a0-%012d", sequence*10+index)
		title := fmt.Sprintf("work-%d-%d", sequence, index)
		tags, _ := json.Marshal([]string{"gc"})
		document := querytext.BuildDocument(title, "", []string{"gc"}, nil)
		if _, err := store.Catalog.SQL().ExecContext(ctx, `INSERT INTO work_projections
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id, title, creator,
 tags_json, filenames_text, normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text,
 sort_title_key, hidden)
VALUES (?, ?, ?, 'src_gc', ?, 'lib_gc', ?, '', ?, '[]', ?, ?, ?, ?, 0)`,
			catalogID, overlayID, workID, title, title, string(tags), document.NormalizedOriginal,
			document.CJKTokens, document.LatinTokens, document.SortTitleKey); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Catalog.SQL().ExecContext(ctx,
			"INSERT INTO work_search VALUES (?, ?, ?, ?, ?, ?)", catalogID, overlayID, workID,
			document.NormalizedOriginal, document.CJKTokens, document.LatinTokens); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Catalog.SQL().ExecContext(ctx, `INSERT INTO active_query_publication VALUES (1, ?)
ON CONFLICT(singleton) DO UPDATE SET query_publication_id=excluded.query_publication_id`, publicationID); err != nil {
		t.Fatal(err)
	}
	return publicationID, catalogID, digest
}

type fixedIDs struct{}

func (fixedIDs) New(domain.IDKind) (domain.ID, error) {
	return domain.ID{}, errors.New("GC 不应生成 ID")
}

func assertPublicationCount(t *testing.T, store *storage.Store, publicationID string, want int) {
	t.Helper()
	var count int
	if err := store.Catalog.SQL().QueryRow(
		"SELECT count(*) FROM query_publications WHERE query_publication_id=?", publicationID).Scan(&count); err != nil || count != want {
		t.Fatalf("publication count=%d want=%d err=%v", count, want, err)
	}
}

func assertFaultCode(t *testing.T, err error, code fault.Code) {
	t.Helper()
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != code {
		t.Fatalf("错误 code=%s 实际=%v", code, err)
	}
}
