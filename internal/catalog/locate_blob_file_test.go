package catalog_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/storage"
)

// seedRevisionWithOccurrence 构造一个完整、独立的 catalog revision（含 overlay revision、
// content_blobs、media_projections、file_locations），可选把它设为 active。用于验证
// DerivedAsset 输入解析不依赖"这个 digest 恰好在当前 active revision"这件事——只要它在
// 任意一个仍未被 GC 回收的 revision 中还有 present occurrence 就必须能被找到。
func seedRevisionWithOccurrence(t *testing.T, store *storage.Store, suffix, sourceID, relativePath, digest string, makeActive bool) {
	t.Helper()
	ctx := context.Background()
	catalogID := "cat_018f47d2-5c16-7a44-a8a0-" + suffix
	overlayID := "ovr_018f47d2-5c16-7a44-a8a0-" + suffix
	jobID := "job_018f47d2-5c16-7a44-a8a0-" + suffix
	publicationID := "qpub_018f47d2-5c16-7a44-a8a0-" + suffix
	workID := "wrk_018f47d2-5c16-7a44-a8a0-" + suffix
	mediaID := "med_018f47d2-5c16-7a44-a8a0-" + suffix
	if _, err := store.Catalog.SQL().ExecContext(ctx,
		"INSERT INTO catalog_revisions VALUES (?, ?, ?, 'published', 1, 1)", catalogID, jobID, sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Catalog.SQL().ExecContext(ctx, `INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, published_at)
VALUES (?, ?, 0, 'published', 1, 1)`, overlayID, catalogID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Catalog.SQL().ExecContext(ctx,
		"INSERT INTO query_publications VALUES (?, ?, ?, ?, 0, 1)", publicationID, catalogID, overlayID, jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Catalog.SQL().ExecContext(ctx,
		"INSERT INTO content_blobs VALUES (?, 'sha256-v1', ?, 1)", catalogID, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Catalog.SQL().ExecContext(ctx, `INSERT INTO work_projections
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id, title, creator,
 tags_json, filenames_text, normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text,
 sort_title_key, hidden)
VALUES (?, ?, ?, ?, 'work', 'lib', 'work', '', '[]', '', '', '', '', '', 0)`,
		catalogID, overlayID, workID, sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Catalog.SQL().ExecContext(ctx, `INSERT INTO media_projections
(catalog_revision_id, overlay_revision_id, media_id, work_id, source_id, source_key, relative_path,
 media_kind, mime_type, size_bytes, algorithm, digest, location_status, content_verification_state, verified_at, ordinal, base_ordinal)
VALUES (?, ?, ?, ?, ?, 'media', ?, 'image', 'application/octet-stream', 1, 'sha256-v1', ?, 'present', 'content_verified', 1, 0, 0)`,
		catalogID, overlayID, mediaID, workID, sourceID, relativePath, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Catalog.SQL().ExecContext(ctx, `INSERT INTO file_locations
(catalog_revision_id, source_id, source_key, location_key, relative_path, algorithm, digest, status)
VALUES (?, ?, 'media', ?, ?, 'sha256-v1', ?, 'present')`,
		catalogID, sourceID, "loc_"+suffix, relativePath, digest); err != nil {
		t.Fatal(err)
	}
	if makeActive {
		if _, err := store.Catalog.SQL().ExecContext(ctx, `INSERT INTO active_query_publication VALUES (1, ?)
ON CONFLICT(singleton) DO UPDATE SET query_publication_id=excluded.query_publication_id`, publicationID); err != nil {
			t.Fatal(err)
		}
	}
}

// TestLocateBlobFileResolvesAcrossNonActiveRevisions 覆盖阶段 4 收尾的核心缺口：
// DerivedAsset 输入按 ContentBlob 内容寻址,不应该被"这个 digest 当前是否恰好出现在
// active publication"限制。这里构造两个 revision：旧的（非 active）持有目标 digest 的
// 唯一 occurrence，新的（active）完全不包含这个 digest（模拟排队等待期间 active
// publication 已经切换、且新 revision 里这个内容暂时/永久没有被再次发现的情形）。
// LocateBlobFile 必须仍能从旧 revision 解析出源文件位置，不得因为它不是 active 就返回
// NOT_FOUND——那会让一个针对旧 publication 成功创建的 Job 在纯粹因为 active 切换后
// 无法完成，即使内容仍然可读。
func TestLocateBlobFileResolvesAcrossNonActiveRevisions(t *testing.T) {
	ctx := context.Background()
	fixed := clock.Fixed{Time: time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)}
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

	targetDigest := "3333333333333333333333333333333333333333333333333333333333333333"[:64]
	otherDigest := "4444444444444444444444444444444444444444444444444444444444444444"[:64]
	seedRevisionWithOccurrence(t, store, "000000000201", "src_old", "work/old-media.bin", targetDigest, false)
	seedRevisionWithOccurrence(t, store, "000000000202", "src_new", "work/new-media.bin", otherDigest, true)

	sourceID, relativePath, size, err := catalogStore.LocateBlobFile(ctx, "sha256-v1", targetDigest)
	if err != nil {
		t.Fatalf("跨 revision 解析应该成功: %v", err)
	}
	if sourceID != "src_old" || relativePath != "work/old-media.bin" || size != 1 {
		t.Fatalf("解析结果不正确: sourceID=%s relativePath=%s size=%d", sourceID, relativePath, size)
	}
}

// TestLocateBlobFileReturnsNotFoundWhenDigestNeverPresent 是上一个测试的对照基线：
// 一个从未在任何 revision 出现过的 digest 必须返回结构化 NOT_FOUND，证明跨 revision
// 解析不会退化成"永远返回某个 occurrence"。
func TestLocateBlobFileReturnsNotFoundWhenDigestNeverPresent(t *testing.T) {
	ctx := context.Background()
	fixed := clock.Fixed{Time: time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)}
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
	seedRevisionWithOccurrence(t, store, "000000000203", "src_only", "work/only-media.bin",
		"5555555555555555555555555555555555555555555555555555555555555555"[:64], true)

	_, _, _, err = catalogStore.LocateBlobFile(ctx, "sha256-v1", "6666666666666666666666666666666666666666666666666666666666666666"[:64])
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeNotFound {
		t.Fatalf("从未出现过的 digest 应返回结构化 NOT_FOUND: %v", err)
	}
}
