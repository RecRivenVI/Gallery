package bootstrap_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/RecRivenVI/gallery/internal/config"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/querytext"
	"github.com/RecRivenVI/gallery/internal/storage"
)

// TestBootstrapBackfillsQueryDependencyFieldsOnceForLegacyRevision 覆盖阶段 4 收尾的
// 核心缺口在完整 bootstrap 生命周期下的行为：catalog migration 00010（v9→v10）只能给
// 已有 work_projections 行的 favorite/progress/search_*_norm 新列填入静态默认值，不会
// 自动重新计算。这里直接用原始 SQL 构造一个"迁移后、从未被任何 Overlay 写入触碰过"的
// revision（模拟真实存量升级：control.db 已有 favorite=true 的既有事实，catalog.db 对应
// 列仍是默认值），验证真实 `bootstrap.Run` 启动路径会在开始对外提供服务之前自动完成一次
// 回填，且第二次启动是幂等的（不重复排队、不报错、结果保持稳定）。
func TestBootstrapBackfillsQueryDependencyFieldsOnceForLegacyRevision(t *testing.T) {
	root := t.TempDir()
	dirs := appdirs.UnderRoot(filepath.Join(root, "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	seedLegacyPreBackfillRevision(t, dirs)

	cfg := config.Config{Mode: config.ModePersonal, Listen: "127.0.0.1:0", AppDirs: dirs}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cancel, done, _ := startGalleryd(t, cfg, logger)
	stopGalleryd(t, cancel, done)

	assertBackfillConverged(t, dirs)

	// 第二次启动：迁移已经应用过、回填标记已经写入，必须是幂等的空操作，不重复排队、
	// 不报错，结果保持稳定。
	cancel, done, _ = startGalleryd(t, cfg, logger)
	stopGalleryd(t, cancel, done)
	assertBackfillConverged(t, dirs)
}

const backfillTestWorkID = "wrk_018f47d2-5c16-7a44-a8a0-00000000ba0f"

// seedLegacyPreBackfillRevision 打开一次数据库（触发全部迁移，包括 00010），随后用原始
// SQL 直接构造一个 published 的 catalog revision——work_projections 的 favorite/progress/
// search_*_norm 全部留空（migration 00010 ALTER TABLE ADD COLUMN 对既有行的默认值），
// 同时在 control.db 写入一条早于本次进程存在的 work_overlays 事实（favorite=true），
// 模拟"迁移前就已经收藏过这个作品，但 Catalog 侧从未被投影"的真实存量状态。
func seedLegacyPreBackfillRevision(t *testing.T, dirs appdirs.Dirs) {
	t.Helper()
	ctx := context.Background()
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	document := querytext.BuildDocument("legacy title", "legacy creator", []string{"legacy-tag"}, []string{"legacy.jpg"})
	tags, _ := json.Marshal([]string{"legacy-tag"})
	files, _ := json.Marshal([]string{"legacy.jpg"})
	statements := []struct {
		db    string
		query string
		args  []any
	}{
		{"control", `INSERT INTO canonical_works (work_id, title, created_at) VALUES (?, 'legacy title', 1)`, []any{backfillTestWorkID}},
		{"catalog", `INSERT INTO catalog_revisions VALUES ('cat_018f47d2-5c16-7a44-a8a0-00000000ba0f', 'job_018f47d2-5c16-7a44-a8a0-00000000ba0f', 'src_legacy', 'published', 1, 1)`, nil},
		{"catalog", `INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, published_at)
VALUES ('ovr_018f47d2-5c16-7a44-a8a0-00000000ba0f', 'cat_018f47d2-5c16-7a44-a8a0-00000000ba0f', 0, 'published', 1, 1)`, nil},
		{"catalog", `INSERT INTO query_publications VALUES
('qpub_018f47d2-5c16-7a44-a8a0-00000000ba0f', 'cat_018f47d2-5c16-7a44-a8a0-00000000ba0f', 'ovr_018f47d2-5c16-7a44-a8a0-00000000ba0f', 'job_018f47d2-5c16-7a44-a8a0-00000000ba0f', 0, 1)`, nil},
		{"catalog", `INSERT INTO active_query_publication VALUES (1, 'qpub_018f47d2-5c16-7a44-a8a0-00000000ba0f')`, nil},
		{"catalog", `INSERT INTO source_works
(catalog_revision_id, source_id, source_key, title, creator, tags_json, filenames_text)
VALUES ('cat_018f47d2-5c16-7a44-a8a0-00000000ba0f', 'src_legacy', 'legacy-work', 'legacy title', 'legacy creator', ?, ?)`,
			[]any{string(tags), string(files)}},
		{"catalog", `INSERT INTO work_projections
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id, title, creator,
 tags_json, filenames_text, normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text, sort_title_key, hidden)
VALUES ('cat_018f47d2-5c16-7a44-a8a0-00000000ba0f', 'ovr_018f47d2-5c16-7a44-a8a0-00000000ba0f', ?, 'src_legacy', 'legacy-work',
 'lib_legacy', 'legacy title', 'legacy creator', ?, ?, ?, ?, ?, ?, 0)`,
			[]any{backfillTestWorkID, string(tags), string(files), document.NormalizedOriginal, document.CJKTokens, document.LatinTokens, document.SortTitleKey}},
		{"catalog", `INSERT INTO work_search VALUES
('cat_018f47d2-5c16-7a44-a8a0-00000000ba0f', 'ovr_018f47d2-5c16-7a44-a8a0-00000000ba0f', ?, ?, ?, ?)`,
			[]any{backfillTestWorkID, document.NormalizedOriginal, document.CJKTokens, document.LatinTokens}},
		{"control", `INSERT INTO work_overlays
(work_id, title_override, manual_tags_json, hidden, custom_cover_media_id, favorite, progress,
 fact_watermark, query_watermark, projected_watermark, projection_status, projection_job_id,
 published_query_publication_id, issue_code, updated_at)
VALUES (?, '', '[]', 0, NULL, 1, 0.4, 1, 1, 1, 'published', NULL, 'qpub_018f47d2-5c16-7a44-a8a0-00000000ba0f', NULL, 1)`,
			[]any{backfillTestWorkID}},
	}
	for _, statement := range statements {
		db := store.Catalog.SQL()
		if statement.db == "control" {
			db = store.Control.SQL()
		}
		if _, err := db.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("构造存量 revision 失败 query=%s: %v", statement.query, err)
		}
	}
}

// assertBackfillConverged 验证回填后 work_projections 正确反映 control.db 的既有
// favorite/progress 事实，且 search_*_norm 已经从该 revision 已有的 title/creator/tags/
// filenames 重新计算，不再是迁移默认的空字符串。
func assertBackfillConverged(t *testing.T, dirs appdirs.Dirs) {
	t.Helper()
	ctx := context.Background()
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var value string
	if err := store.Catalog.SQL().QueryRowContext(ctx,
		"SELECT value FROM gallery_catalog_meta WHERE key='query_dependency_backfill_triggered'").Scan(&value); err != nil {
		t.Fatalf("回填标记未写入: %v", err)
	}
	var favorite int
	var progress float64
	var titleNorm, creatorNorm, tagsNorm, filenamesNorm string
	err = store.Catalog.SQL().QueryRowContext(ctx, `SELECT wp.favorite, wp.progress,
 wp.search_title_norm, wp.search_creator_norm, wp.search_tags_norm, wp.search_filenames_norm
FROM work_projections wp
JOIN active_query_publication a ON a.singleton=1
JOIN query_publications q ON q.query_publication_id=a.query_publication_id
WHERE wp.catalog_revision_id=q.catalog_revision_id AND wp.overlay_revision_id=q.overlay_revision_id AND wp.work_id=?`,
		backfillTestWorkID).Scan(&favorite, &progress, &titleNorm, &creatorNorm, &tagsNorm, &filenamesNorm)
	if err != nil {
		t.Fatal(err)
	}
	if favorite != 1 || progress != 0.4 {
		t.Fatalf("回填后应反映 control.db 既有 favorite/progress 事实: favorite=%d progress=%v", favorite, progress)
	}
	wantDocument := querytext.BuildDocument("legacy title", "legacy creator", []string{"legacy-tag"}, []string{"legacy.jpg"})
	if titleNorm != wantDocument.TitleNorm || creatorNorm != wantDocument.CreatorNorm ||
		tagsNorm != wantDocument.TagsNorm || filenamesNorm != wantDocument.FilenamesNorm {
		t.Fatalf("search_*_norm 未正确回填: title=%q creator=%q tags=%q filenames=%q", titleNorm, creatorNorm, tagsNorm, filenamesNorm)
	}
}
