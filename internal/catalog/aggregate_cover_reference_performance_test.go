package catalog_test

import (
	"context"
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
	"github.com/RecRivenVI/gallery/internal/storage"
)

// TestAuthorizedAggregateCoverReferencePerformance 显式测量受限主体的 Creator 聚合封面
// 重选，不属于普通 Correctness Gate。调用方必须登记硬件、规模、Source/Creator 分布、
// 缓存状态与资源预算；本夹具不构造真实媒体、Blob、Location 或 control.db 授权事实。
func TestAuthorizedAggregateCoverReferencePerformance(t *testing.T) {
	if os.Getenv("GALLERY_AGGREGATE_COVER_PERF") != "1" {
		t.Skip("设置 GALLERY_AGGREGATE_COVER_PERF=1 后运行受限聚合封面参考性能测量")
	}
	works := aggregateCoverPerfInt(t, "GALLERY_AGGREGATE_COVER_WORKS", 100_000, 1_000, 500_000)
	sources := aggregateCoverPerfInt(t, "GALLERY_AGGREGATE_COVER_SOURCES", 10, 2, 1_000)
	worksPerCreator := aggregateCoverPerfInt(t, "GALLERY_AGGREGATE_COVER_WORKS_PER_CREATOR", 10, 1, 10_000)
	if sources > works || worksPerCreator > works || worksPerCreator < sources || worksPerCreator%sources != 0 || works%worksPerCreator != 0 {
		t.Fatalf("规模不合法: works=%d sources=%d worksPerCreator=%d", works, sources, worksPerCreator)
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
	publicationID, sourceIDs, creatorCount, buildDuration := seedAggregateCoverPerformance(
		t, store, works, sources, worksPerCreator,
	)
	fixed := clock.Fixed{Time: time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)}
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), fixed, identity.NewGenerator(fixed))
	if err != nil {
		t.Fatal(err)
	}

	measure := func(name string, allowed, scopeIDs []string, wantCount int) (time.Duration, time.Duration) {
		t.Helper()
		if _, err := catalogStore.AggregateCoversForSourcesAt(ctx, publicationID, catalog.AggregateScopeCreator, allowed, scopeIDs...); err != nil {
			t.Fatalf("%s warmup: %v", name, err)
		}
		durations := make([]time.Duration, 31)
		for run := range durations {
			started := time.Now()
			covers, err := catalogStore.AggregateCoversForSourcesAt(ctx, publicationID, catalog.AggregateScopeCreator, allowed, scopeIDs...)
			durations[run] = time.Since(started)
			if err != nil || len(covers) != wantCount {
				t.Fatalf("%s run=%d covers=%d want=%d err=%v", name, run, len(covers), wantCount, err)
			}
		}
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		return durations[len(durations)/2], durations[(len(durations)*95+99)/100-1]
	}

	allowOneP50, allowOneP95 := measure("allow-one", sourceIDs[:1], nil, creatorCount)
	denyNonwinnerP50, denyNonwinnerP95 := measure("deny-nonwinner", sourceIDs[1:], nil, creatorCount)
	denyWinnerP50, denyWinnerP95 := measure("deny-winner", sourceIDs[:len(sourceIDs)-1], nil, creatorCount)
	targetCreatorID := fmt.Sprintf("ctr-authorized-reference-%08d", 0)
	targetOneP50, targetOneP95 := measure("deny-winner-target-one", sourceIDs[:len(sourceIDs)-1], []string{targetCreatorID}, 1)
	t.Logf("AUTHORIZED_AGGREGATE_COVER_PERFORMANCE works=%d creators=%d works_per_creator=%d sources=%d cache=warm concurrency=1 runs=31 build=%s allow_one_p50=%s allow_one_p95=%s deny_nonwinner_p50=%s deny_nonwinner_p95=%s deny_winner_p50=%s deny_winner_p95=%s deny_winner_target_one_p50=%s deny_winner_target_one_p95=%s",
		works, creatorCount, worksPerCreator, sources, buildDuration,
		allowOneP50, allowOneP95, denyNonwinnerP50, denyNonwinnerP95, denyWinnerP50, denyWinnerP95,
		targetOneP50, targetOneP95)
}

func aggregateCoverPerfInt(t *testing.T, name string, fallback, minimum, maximum int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		t.Fatalf("%s 必须在 %d..%d: %q", name, minimum, maximum, raw)
	}
	return value
}

func seedAggregateCoverPerformance(
	t *testing.T,
	store *storage.Store,
	works, sources, worksPerCreator int,
) (publicationID string, sourceIDs []string, creatorCount int, duration time.Duration) {
	t.Helper()
	ctx := context.Background()
	db := store.Catalog.SQL()
	const (
		catalogID     = "cat_018f47d2-5c16-7a44-a8a0-a00000000001"
		overlayID     = "ovr_018f47d2-5c16-7a44-a8a0-a00000000002"
		jobID         = "job_018f47d2-5c16-7a44-a8a0-a00000000003"
		queryID       = "qpub_018f47d2-5c16-7a44-a8a0-a00000000004"
		libraryID     = "lib-authorized-aggregate-reference"
		creatorPrefix = "ctr-authorized-reference-"
	)
	if _, err := db.ExecContext(ctx,
		"INSERT INTO catalog_revisions VALUES (?, ?, ?, 'published', 1, 1)", catalogID, jobID, "source-0000"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, published_at)
VALUES (?, ?, 1, 'published', 1, 1)`, overlayID, catalogID); err != nil {
		t.Fatal(err)
	}
	sourceIDs = make([]string, sources)
	for index := range sourceIDs {
		sourceIDs[index] = fmt.Sprintf("source-%04d", index)
		if _, err := db.ExecContext(ctx, `INSERT INTO catalog_revision_sources
(catalog_revision_id, source_id, library_id) VALUES (?, ?, ?)`, catalogID, sourceIDs[index], libraryID); err != nil {
			t.Fatal(err)
		}
	}

	creatorCount = (works + worksPerCreator - 1) / worksPerCreator
	started := time.Now()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	fail := func(err error) {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	creatorStatement, err := tx.PrepareContext(ctx, `INSERT INTO creator_projections
(catalog_revision_id, overlay_revision_id, creator_id, name, sort_name_key)
VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		fail(err)
	}
	for index := 0; index < creatorCount; index++ {
		creatorID := fmt.Sprintf("%s%08d", creatorPrefix, index)
		if _, err := creatorStatement.ExecContext(ctx, catalogID, overlayID, creatorID, creatorID, creatorID); err != nil {
			_ = creatorStatement.Close()
			fail(err)
		}
	}
	if err := creatorStatement.Close(); err != nil {
		fail(err)
	}

	workStatement, err := tx.PrepareContext(ctx, `INSERT INTO work_projections
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id, title, creator,
 tags_json, filenames_text, normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text,
 sort_title_key, hidden, search_title_norm, search_creator_norm, search_tags_norm, search_filenames_norm,
 cover_media_id, published_at_ns, published_at_raw, published_at_parser)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, '[]', '', '', '', '', ?, 0, '', '', '', '', ?, ?, 'raw', 'gallery-work-date-v1')`)
	if err != nil {
		fail(err)
	}
	relationStatement, err := tx.PrepareContext(ctx, `INSERT INTO work_creator_relations
(catalog_revision_id, overlay_revision_id, work_id, creator_id, role, ordinal)
VALUES (?, ?, ?, ?, 'author', 0)`)
	if err != nil {
		_ = workStatement.Close()
		fail(err)
	}
	mediaStatement, err := tx.PrepareContext(ctx, `INSERT INTO media_projections
(catalog_revision_id, overlay_revision_id, media_id, work_id, source_id, source_key, relative_path,
 media_kind, mime_type, size_bytes, algorithm, digest, location_status, content_verification_state,
 verified_at, ordinal, base_ordinal)
VALUES (?, ?, ?, ?, ?, ?, ?, 'image', 'image/jpeg', 1, 'sha256-v1', ?, 'present',
        'content_verified', 1, 0, 0)`)
	if err != nil {
		_ = workStatement.Close()
		_ = relationStatement.Close()
		fail(err)
	}
	for index := 0; index < works; index++ {
		workID := fmt.Sprintf("work-%09d", index)
		creatorID := fmt.Sprintf("%s%08d", creatorPrefix, index/worksPerCreator)
		sourceID := sourceIDs[index%sources]
		mediaID := fmt.Sprintf("media-%09d", index)
		if _, err := workStatement.ExecContext(ctx, catalogID, overlayID, workID, sourceID, workID,
			libraryID, workID, creatorID, workID, mediaID, int64(index+1)); err != nil {
			_ = workStatement.Close()
			_ = relationStatement.Close()
			_ = mediaStatement.Close()
			fail(err)
		}
		if _, err := relationStatement.ExecContext(ctx, catalogID, overlayID, workID, creatorID); err != nil {
			_ = workStatement.Close()
			_ = relationStatement.Close()
			_ = mediaStatement.Close()
			fail(err)
		}
		digest := fmt.Sprintf("%064x", index+1)
		if _, err := mediaStatement.ExecContext(ctx, catalogID, overlayID, mediaID, workID, sourceID,
			mediaID, mediaID, digest); err != nil {
			_ = workStatement.Close()
			_ = relationStatement.Close()
			_ = mediaStatement.Close()
			fail(err)
		}
	}
	if err := workStatement.Close(); err != nil {
		_ = relationStatement.Close()
		_ = mediaStatement.Close()
		fail(err)
	}
	if err := relationStatement.Close(); err != nil {
		_ = mediaStatement.Close()
		fail(err)
	}
	if err := mediaStatement.Close(); err != nil {
		fail(err)
	}
	if _, err := tx.ExecContext(ctx, `WITH ranked AS (
    SELECT r.catalog_revision_id,
           r.overlay_revision_id,
           r.creator_id,
           w.source_id,
           w.cover_media_id,
           w.published_at_ns,
           w.work_id,
           row_number() OVER (
               PARTITION BY r.catalog_revision_id, r.overlay_revision_id, r.creator_id, w.source_id
               ORDER BY w.published_at_ns DESC, w.work_id DESC
           ) AS rank_in_scope
    FROM work_creator_relations AS r
    JOIN work_projections AS w
      ON w.catalog_revision_id=r.catalog_revision_id
     AND w.overlay_revision_id=r.overlay_revision_id
     AND w.work_id=r.work_id
    WHERE r.catalog_revision_id=? AND r.overlay_revision_id=? AND w.cover_media_id<>''
)
INSERT INTO creator_source_cover_projections
(catalog_revision_id, overlay_revision_id, creator_id, source_id,
 cover_media_id, published_at_ns, work_id)
SELECT catalog_revision_id, overlay_revision_id, creator_id, source_id,
       cover_media_id, published_at_ns, work_id
FROM ranked WHERE rank_in_scope=1`, catalogID, overlayID); err != nil {
		fail(err)
	}
	for creatorIndex := 0; creatorIndex < creatorCount; creatorIndex++ {
		workIndex := (creatorIndex+1)*worksPerCreator - 1
		creatorID := fmt.Sprintf("%s%08d", creatorPrefix, creatorIndex)
		mediaID := fmt.Sprintf("media-%09d", workIndex)
		sourceID := sourceIDs[workIndex%sources]
		if _, err := tx.ExecContext(ctx, `INSERT INTO aggregate_cover_projections
(catalog_revision_id, overlay_revision_id, scope_kind, scope_id, cover_media_id, published_at_ns, source_id)
VALUES (?, ?, 'creator', ?, ?, ?, ?)`, catalogID, overlayID, creatorID, mediaID, int64(workIndex+1), sourceID); err != nil {
			fail(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO query_publications
(query_publication_id, catalog_revision_id, overlay_revision_id, job_id, control_watermark, created_at)
VALUES (?, ?, ?, ?, 1, 1)`, queryID, catalogID, overlayID, jobID); err != nil {
		fail(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO active_query_publication (singleton, query_publication_id) VALUES (1, ?)", queryID); err != nil {
		fail(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return queryID, sourceIDs, creatorCount, time.Since(started)
}
