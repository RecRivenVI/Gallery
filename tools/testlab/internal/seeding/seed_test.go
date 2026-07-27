package seeding

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/storage"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/corpus"
)

const multiSourceScale = 600

// TestMultiSourceSeedExercisesCloneUnchangedSources 是本轮针对 catalog.cloneUnchangedSources
// 的覆盖证明。
//
// 为什么需要它：那 12 条全量搬运语句的 WHERE 条件都是 `source_id<>?`，因此**只有当活动
// publication 里存在别的 Source 时才会搬运任何一行**。此前 testlab 的全部语料（含 500,000
// 规模的正式实测）都只有一个 Source，这条路径在实测中一条语句都没有执行过——而它正是
// "重扫其中一个 Source 时按比例复制其余全部 Source 的投影与 FTS5 索引"的路径，是发布代价
// 的大头之一。
//
// 断言直接建立在 catalog.db 的行计数上：最终 revision 必须同时包含全部 Source 的
// source_*、projections 与 work_search 行，而每次 Stage 只写入当前 Source 的那一份，
// 其余只可能来自 cloneUnchangedSources。
func TestMultiSourceSeedExercisesCloneUnchangedSources(t *testing.T) {
	const sources = 3
	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	manifest, err := Run(context.Background(), Config{
		AppRoot: appRoot, SourceRoot: filepath.Join(root, "sources"),
		Scale: multiSourceScale, BatchSize: 97, Sources: sources,
	})
	if err != nil {
		t.Fatalf("构建多 Source 语料: %v", err)
	}

	if manifest.Sources != sources || len(manifest.SourceIDs) != sources {
		t.Fatalf("manifest.Sources=%d len(SourceIDs)=%d, want %d", manifest.Sources, len(manifest.SourceIDs), sources)
	}
	if manifest.SourceCount() != sources {
		t.Fatalf("manifest.SourceCount() = %d, want %d", manifest.SourceCount(), sources)
	}
	seen := map[string]struct{}{}
	for _, sourceID := range manifest.SourceIDs {
		if sourceID == "" {
			t.Fatal("manifest 中出现空 Source ID")
		}
		if _, duplicate := seen[sourceID]; duplicate {
			t.Fatalf("manifest 中出现重复 Source ID: %s", sourceID)
		}
		seen[sourceID] = struct{}{}
	}
	visibleSum := 0
	for _, count := range manifest.SourceVisibleWorkCounts {
		visibleSum += count
	}
	if visibleSum != manifest.Stats.VisibleN {
		t.Fatalf("逐 Source 可见计数合计 %d != 语料 VisibleN %d", visibleSum, manifest.Stats.VisibleN)
	}
	// 每个 Source 都必须真的产生过一次 BeginCandidate 与一次 Publish。
	if len(manifest.SourceBeginDurationsMs) != sources || len(manifest.SourceValidationDurationsMs) != sources || len(manifest.SourcePublishDurationsMs) != sources {
		t.Fatalf("逐 Source 计时条数不足: begin=%d validation=%d publish=%d, want %d",
			len(manifest.SourceBeginDurationsMs), len(manifest.SourceValidationDurationsMs), len(manifest.SourcePublishDurationsMs), sources)
	}

	db := openCatalog(t, appRoot)
	revision := manifest.CatalogRevisionID

	// 最终 revision 的成员表必须包含全部 Source。第一个 Source 的成员行由它自己的
	// Stage 写入，其余全部来自 cloneUnchangedSources 的第 1 条语句。
	if got := countRows(t, db, `SELECT count(*) FROM catalog_revision_sources WHERE catalog_revision_id=?`, revision); got != sources {
		t.Fatalf("catalog_revision_sources 行数 = %d, want %d", got, sources)
	}
	// 最终 revision 必须持有整份语料。最后一次 Stage 只写入 1/3，其余 2/3 只可能是
	// 被搬运过来的——这正是 (N-1)/N 的搬运比例。
	if got := countRows(t, db, `SELECT count(*) FROM work_projections WHERE catalog_revision_id=?`, revision); got != multiSourceScale {
		t.Fatalf("work_projections 行数 = %d, want %d（缺失说明 cloneUnchangedSources 没有搬运其余 Source）", got, multiSourceScale)
	}
	if got := countRows(t, db, `SELECT count(DISTINCT source_id) FROM work_projections WHERE catalog_revision_id=?`, revision); got != sources {
		t.Fatalf("work_projections 中的 Source 数 = %d, want %d", got, sources)
	}
	if got := countRows(t, db, `SELECT count(*) FROM source_works WHERE catalog_revision_id=?`, revision); got != multiSourceScale {
		t.Fatalf("source_works 行数 = %d, want %d", got, multiSourceScale)
	}
	if got := countRows(t, db, `SELECT count(*) FROM source_media WHERE catalog_revision_id=?`, revision); got != multiSourceScale {
		t.Fatalf("source_media 行数 = %d, want %d", got, multiSourceScale)
	}
	if got := countRows(t, db, `SELECT count(*) FROM media_projections WHERE catalog_revision_id=?`, revision); got != multiSourceScale {
		t.Fatalf("media_projections 行数 = %d, want %d", got, multiSourceScale)
	}
	// work_search 是 FTS5 虚表，外键级联不覆盖它，搬运语句是独立的一条；它最容易在
	// 重构中被漏掉，因此单独断言。
	if got := countRows(t, db, `SELECT count(*) FROM work_search WHERE catalog_revision_id=?`, revision); got != multiSourceScale {
		t.Fatalf("work_search 行数 = %d, want %d（FTS5 索引没有被搬运）", got, multiSourceScale)
	}

	// Overlay 事实必须跨 Source 存活：ApplyCatalogCandidateOverlays 会对整个 revision 的
	// 每一行套用 facts[workID]，缺失时套用零值。如果 seeding 只传当前 Source 的事实，
	// 先发布的 Source 的 Favorite/Hidden 会在后续发布中被静默抹平。
	expectedFavorite, expectedHidden := 0, 0
	for i := 0; i < multiSourceScale; i++ {
		if corpus.SourceIndex(i, sources) != 0 {
			continue
		}
		if corpus.Favorite(i) {
			expectedFavorite++
		}
		if corpus.Hidden(i) {
			expectedHidden++
		}
	}
	firstSource := manifest.SourceIDs[0]
	if got := countRows(t, db, `SELECT count(*) FROM work_projections WHERE catalog_revision_id=? AND source_id=? AND favorite=1`, revision, firstSource); got != expectedFavorite {
		t.Fatalf("首个 Source 的 favorite 行数 = %d, want %d（后续 Source 的发布把它抹平了）", got, expectedFavorite)
	}
	if got := countRows(t, db, `SELECT count(*) FROM work_projections WHERE catalog_revision_id=? AND source_id=? AND hidden=1`, revision, firstSource); got != expectedHidden {
		t.Fatalf("首个 Source 的 hidden 行数 = %d, want %d", got, expectedHidden)
	}
}

// TestSingleSourceSeedNeverClonesAnything 是上面那条测试的反向对照：单 Source 语料下
// 最终 revision 只有一个 Source，搬运语句没有任何可搬运对象。它同时锁定默认行为没有
// 因为多 Source 支持而改变。
func TestSingleSourceSeedNeverClonesAnything(t *testing.T) {
	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	manifest, err := Run(context.Background(), Config{
		AppRoot: appRoot, SourceRoot: filepath.Join(root, "sources"), Scale: 200, BatchSize: 64,
	})
	if err != nil {
		t.Fatalf("构建单 Source 语料: %v", err)
	}
	if manifest.SourceCount() != 1 {
		t.Fatalf("默认 Source 数 = %d, want 1", manifest.SourceCount())
	}
	if manifest.SourceID != manifest.SourceIDs[0] {
		t.Fatalf("SourceID=%s 与 SourceIDs[0]=%s 不一致", manifest.SourceID, manifest.SourceIDs[0])
	}
	db := openCatalog(t, appRoot)
	if got := countRows(t, db, `SELECT count(*) FROM catalog_revision_sources WHERE catalog_revision_id=?`, manifest.CatalogRevisionID); got != 1 {
		t.Fatalf("单 Source 语料的成员行数 = %d, want 1", got)
	}
}

// TestSeedRejectsMoreSourcesThanWorks 复核越界配置给出可读原因，而不是让 Catalog 以
// 不透明的 CATALOG_CANDIDATE_INVALID 拒绝一个空 Source 候选。
func TestSeedRejectsMoreSourcesThanWorks(t *testing.T) {
	root := t.TempDir()
	_, err := Run(context.Background(), Config{
		AppRoot: filepath.Join(root, "app"), Scale: 2, Sources: 5,
	})
	if err == nil {
		t.Fatal("Source 数超过语料规模时应当失败")
	}
}

// TestResolveSourcesPrefersConfigOverEnvironment 锁定解析顺序，并确认非法环境变量不会
// 被静默忽略——静默忽略会让一次本以为多 Source 的运行悄悄退回单 Source。
func TestResolveSourcesPrefersConfigOverEnvironment(t *testing.T) {
	t.Setenv(SourcesEnvVar, "4")
	if got, err := resolveSources(2); err != nil || got != 2 {
		t.Fatalf("显式 Sources 应优先于环境变量: got=%d err=%v", got, err)
	}
	if _, err := resolveSources(-1); err == nil {
		t.Fatal("负 Source 数量未被拒绝")
	}
	if got, err := resolveSources(0); err != nil || got != 4 {
		t.Fatalf("未指定 Sources 时应采用环境变量: got=%d err=%v", got, err)
	}
	t.Setenv(SourcesEnvVar, "not-a-number")
	if _, err := resolveSources(0); err == nil {
		t.Fatal("非法环境变量必须报错，不能静默退回 1")
	}
	t.Setenv(SourcesEnvVar, "")
	if got, err := resolveSources(0); err != nil || got != 1 {
		t.Fatalf("未设置时应默认 1: got=%d err=%v", got, err)
	}
}

func openCatalog(t *testing.T, appRoot string) *sql.DB {
	t.Helper()
	store, err := storage.Open(context.Background(), appdirs.UnderRoot(appRoot))
	if err != nil {
		t.Fatalf("重新打开 catalog.db: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store.Catalog.SQL()
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&count); err != nil {
		t.Fatalf("查询 %q 失败: %v", query, err)
	}
	return count
}
