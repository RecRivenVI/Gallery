package query_test

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	galleryquery "github.com/RecRivenVI/gallery/internal/query"
	"github.com/RecRivenVI/gallery/internal/querytext"
	"github.com/RecRivenVI/gallery/internal/storage"
)

// TestReferenceQueryPerformance 是显式启用的参考机测量，不属于普通 Correctness Gate。
// 调用方必须在验证记录中补齐硬件、存储、OS、缓存状态和并发信息。
func TestReferenceQueryPerformance(t *testing.T) {
	if os.Getenv("GALLERY_REFERENCE_PERF") != "1" {
		t.Skip("设置 GALLERY_REFERENCE_PERF=1 后运行参考性能测量")
	}
	sampleSize := 100_000
	if raw := os.Getenv("GALLERY_REFERENCE_WORKS"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1_000 || parsed > 1_000_000 {
			t.Fatalf("GALLERY_REFERENCE_WORKS 必须在 1000..1000000: %q", raw)
		}
		sampleSize = parsed
	}

	ctx := context.Background()
	dirs := appdirs.UnderRoot(t.TempDir())
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	buildDuration, publicationDuration := seedReferenceProjection(t, store, sampleSize)
	now := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	service, err := galleryquery.NewService(ctx, store.Control.SQL(), store.Catalog.SQL(), clock.Fixed{Time: now}, nil)
	if err != nil {
		t.Fatal(err)
	}
	scope := galleryquery.AuthorizationScope("reference", []string{"library.read"})

	measure := func(name string, request galleryquery.Request) (time.Duration, time.Duration) {
		t.Helper()
		request.AuthorizationScope = scope
		if _, err := service.Search(ctx, request); err != nil {
			t.Fatalf("%s warmup: %v", name, err)
		}
		durations := make([]time.Duration, 31)
		for index := range durations {
			started := time.Now()
			result, err := service.Search(ctx, request)
			durations[index] = time.Since(started)
			if err != nil || len(result.Items) == 0 {
				t.Fatalf("%s run %d: items=%d err=%v", name, index, len(result.Items), err)
			}
		}
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		return durations[len(durations)/2], durations[(len(durations)*95+99)/100-1]
	}

	browseP50, browseP95 := measure("browse", galleryquery.Request{Limit: 100})
	selectiveP50, selectiveP95 := measure("selective-cjk", galleryquery.Request{Search: "特别作品", Limit: 100})
	filenameP50, filenameP95 := measure("filename-infix", galleryquery.Request{Search: "middle-0001", Limit: 100})
	var sqliteVersion string
	if err := store.Catalog.SQL().QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&sqliteVersion); err != nil {
		t.Fatal(err)
	}
	t.Logf("REFERENCE_PERFORMANCE sample=%d sqlite=%s cache=warm concurrency=1 runs=31 build=%s publication=%s browse_p50=%s browse_p95=%s selective_cjk_p50=%s selective_cjk_p95=%s filename_p50=%s filename_p95=%s",
		sampleSize, sqliteVersion, buildDuration, publicationDuration, browseP50, browseP95,
		selectiveP50, selectiveP95, filenameP50, filenameP95)
}

func seedReferenceProjection(t *testing.T, store *storage.Store, sampleSize int) (time.Duration, time.Duration) {
	t.Helper()
	ctx := context.Background()
	db := store.Catalog.SQL()
	const (
		catalogID     = "cat_018f47d2-5c16-7a44-a8a0-900000000000"
		overlayID     = "ovr_018f47d2-5c16-7a44-a8a0-900000000000"
		jobID         = "job_018f47d2-5c16-7a44-a8a0-900000000000"
		publicationID = "qpub_018f47d2-5c16-7a44-a8a0-900000000000"
	)
	if _, err := db.ExecContext(ctx,
		"INSERT INTO catalog_revisions VALUES (?, ?, 'src_reference', 'staging', 1, NULL)", catalogID, jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at)
VALUES (?, ?, 0, 'staging', 1)`, overlayID, catalogID); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := tx.PrepareContext(ctx, `INSERT INTO work_projections
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id, title, creator,
 tags_json, filenames_text, normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text,
 sort_title_key, hidden)
VALUES (?, ?, ?, 'src_reference', ?, 'lib_reference', ?, ?, '["reference"]', ?, ?, ?, ?, ?, 0)`)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	search, err := tx.PrepareContext(ctx, "INSERT INTO work_search VALUES (?, ?, ?, ?, ?, ?)")
	if err != nil {
		projection.Close()
		tx.Rollback()
		t.Fatal(err)
	}
	for index := 0; index < sampleSize; index++ {
		titlePrefix := "普通作品"
		if index%1000 == 0 {
			titlePrefix = "特别作品"
		}
		title := fmt.Sprintf("%s %06d", titlePrefix, index)
		filename := fmt.Sprintf("gallery-middle-%06d.jpg", index)
		workID := fmt.Sprintf("wrk_018f47d2-5c16-7a44-a8a0-%012d", index)
		document := querytext.BuildDocument(title, "Creator", []string{"reference"}, []string{filename})
		if _, err := projection.ExecContext(ctx, catalogID, overlayID, workID, title, title, "Creator",
			filename, document.NormalizedOriginal, document.CJKTokens, document.LatinTokens,
			document.SortTitleKey); err != nil {
			projection.Close()
			search.Close()
			tx.Rollback()
			t.Fatal(err)
		}
		if _, err := search.ExecContext(ctx, catalogID, overlayID, workID, document.NormalizedOriginal,
			document.CJKTokens, document.LatinTokens); err != nil {
			projection.Close()
			search.Close()
			tx.Rollback()
			t.Fatal(err)
		}
	}
	projection.Close()
	search.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	buildDuration := time.Since(started)

	started = time.Now()
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE catalog_revisions SET status='published', published_at=2 WHERE catalog_revision_id=?", catalogID); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE overlay_projection_revisions SET status='published', published_at=2 WHERE overlay_revision_id=?", overlayID); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO query_publications VALUES (?, ?, ?, ?, 0, 2)", publicationID, catalogID, overlayID, jobID); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO active_query_publication VALUES (1, ?)", publicationID); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return buildDuration, time.Since(started)
}
