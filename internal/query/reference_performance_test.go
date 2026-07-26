package query_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
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
	sourceCount := 1
	if raw := os.Getenv("GALLERY_REFERENCE_SOURCES"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 10_000 || parsed > sampleSize {
			t.Fatalf("GALLERY_REFERENCE_SOURCES 必须在 1..min(10000, works): %q", raw)
		}
		sourceCount = parsed
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

	buildDuration, publicationDuration := seedReferenceProjection(t, store, sampleSize, sourceCount)
	now := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	service, err := galleryquery.NewService(ctx, store.Control.SQL(), store.Catalog.SQL(), clock.Fixed{Time: now}, nil)
	if err != nil {
		t.Fatal(err)
	}
	scope := galleryquery.AuthorizationScope("reference", []string{"library.read"})

	measure := func(name string, request galleryquery.Request, authorize galleryquery.SourceSetAuthorizer) (time.Duration, time.Duration) {
		t.Helper()
		request.AuthorizationScope = scope
		request.AuthorizeSources = authorize
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

	membershipP50, membershipP95 := measureReferenceMembership(t, store.Catalog.SQL(), sourceCount)
	browseP50, browseP95 := measure("browse", galleryquery.Request{Limit: 100}, allowAllSources)
	browseNoTotalP50, browseNoTotalP95 := measure("browse-no-total", galleryquery.Request{Limit: 100, OmitTotal: true}, allowAllSources)
	selectiveP50, selectiveP95 := measure("selective-cjk", galleryquery.Request{Search: "特别作品", Limit: 100}, allowAllSources)
	filenameP50, filenameP95 := measure("filename-infix", galleryquery.Request{Search: "middle-0001", Limit: 100}, allowAllSources)
	partialMetrics := ""
	if sourceCount > 1 {
		denyOne := func(_ context.Context, _ []string, sourceIDs []string) ([]string, error) {
			return append([]string(nil), sourceIDs[1:]...), nil
		}
		allowOne := func(_ context.Context, _ []string, sourceIDs []string) ([]string, error) {
			return append([]string(nil), sourceIDs[:1]...), nil
		}
		denyP50, denyP95 := measure("browse-deny-one", galleryquery.Request{Limit: 100}, denyOne)
		allowP50, allowP95 := measure("browse-allow-one", galleryquery.Request{Limit: 100}, allowOne)
		partialMetrics = fmt.Sprintf(" browse_deny_one_p50=%s browse_deny_one_p95=%s browse_allow_one_p50=%s browse_allow_one_p95=%s",
			denyP50, denyP95, allowP50, allowP95)
	}
	var sqliteVersion string
	if err := store.Catalog.SQL().QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&sqliteVersion); err != nil {
		t.Fatal(err)
	}
	t.Logf("REFERENCE_PERFORMANCE sample=%d sources=%d sqlite=%s cache=warm concurrency=1 runs=31 build=%s store_publish=%s membership_p50=%s membership_p95=%s browse_p50=%s browse_p95=%s browse_no_total_p50=%s browse_no_total_p95=%s selective_cjk_p50=%s selective_cjk_p95=%s filename_p50=%s filename_p95=%s%s",
		sampleSize, sourceCount, sqliteVersion, buildDuration, publicationDuration, membershipP50, membershipP95,
		browseP50, browseP95, browseNoTotalP50, browseNoTotalP95, selectiveP50, selectiveP95,
		filenameP50, filenameP95, partialMetrics)
}

func seedReferenceProjection(t *testing.T, store *storage.Store, sampleSize, sourceCount int) (time.Duration, time.Duration) {
	t.Helper()
	ctx := context.Background()
	db := store.Catalog.SQL()
	const (
		catalogID = "cat_018f47d2-5c16-7a44-a8a0-900000000000"
		overlayID = "ovr_018f47d2-5c16-7a44-a8a0-900000000000"
		jobID     = "job_018f47d2-5c16-7a44-a8a0-900000000000"
	)
	if _, err := db.ExecContext(ctx,
		"INSERT INTO catalog_revisions VALUES (?, ?, ?, 'staging', 1, NULL)", catalogID, jobID, referenceSourceID(0)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at)
VALUES (?, ?, 0, 'staging', 1)`, overlayID, catalogID); err != nil {
		t.Fatal(err)
	}
	for sourceIndex := 0; sourceIndex < sourceCount; sourceIndex++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO catalog_revision_sources
(catalog_revision_id, source_id, library_id) VALUES (?, ?, ?)`, catalogID,
			referenceSourceID(sourceIndex), referenceLibraryID(sourceIndex)); err != nil {
			t.Fatal(err)
		}
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
VALUES (?, ?, ?, ?, ?, ?, ?, ?, '["reference"]', ?, ?, ?, ?, ?, 0)`)
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
		sourceIndex := index % sourceCount
		titlePrefix := "普通作品"
		if index%1000 == 0 {
			titlePrefix = "特别作品"
		}
		title := fmt.Sprintf("%s %06d", titlePrefix, index)
		filename := fmt.Sprintf("gallery-middle-%06d.jpg", index)
		workID := fmt.Sprintf("wrk_018f47d2-5c16-7a44-a8a0-%012d", index)
		document := querytext.BuildDocument(title, "Creator", []string{"reference"}, []string{filename})
		if _, err := projection.ExecContext(ctx, catalogID, overlayID, workID,
			referenceSourceID(sourceIndex), title, referenceLibraryID(sourceIndex), title, "Creator",
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

	fixed := clock.Fixed{Time: time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)}
	catalogStore, err := catalog.NewStore(db, fixed, identity.NewGenerator(fixed))
	if err != nil {
		t.Fatal(err)
	}
	started = time.Now()
	if _, err := catalogStore.Publish(ctx, catalog.Candidate{
		CatalogRevisionID: catalogID,
		OverlayRevisionID: overlayID,
		JobID:             jobID,
		SourceID:          referenceSourceID(0),
		ControlWatermark:  0,
	}); err != nil {
		t.Fatal(err)
	}
	return buildDuration, time.Since(started)
}

func referenceSourceID(index int) string { return fmt.Sprintf("src_reference_%05d", index) }

func referenceLibraryID(index int) string { return fmt.Sprintf("lib_reference_%03d", index%16) }

func measureReferenceMembership(t *testing.T, db *sql.DB, wantSources int) (time.Duration, time.Duration) {
	t.Helper()
	durations := make([]time.Duration, 31)
	for run := range durations {
		started := time.Now()
		rows, err := db.Query(`SELECT source_id FROM catalog_revision_sources
WHERE catalog_revision_id='cat_018f47d2-5c16-7a44-a8a0-900000000000' ORDER BY source_id`)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for rows.Next() {
			var sourceID string
			if err := rows.Scan(&sourceID); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			count++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if count != wantSources {
			t.Fatalf("membership rows=%d want=%d", count, wantSources)
		}
		durations[run] = time.Since(started)
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	return durations[len(durations)/2], durations[(len(durations)*95+99)/100-1]
}
