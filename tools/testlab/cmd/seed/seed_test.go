package main

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/storage"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/corpus"
)

// TestRunSeedDedupesCreatorsAcrossBatchBoundaries 覆盖此前真实出现过的
// "UNIQUE constraint failed: source_creators.catalog_revision_id,
// source_creators.source_id, source_creators.source_key" 缺陷：Creator 在同一
// batch 内重复、跨 batch 重复，且 batch 大小与 corpus.CreatorCount（24）不对齐、
// 恰好对齐、互质等各种关系下都不应重现。
func TestRunSeedDedupesCreatorsAcrossBatchBoundaries(t *testing.T) {
	scales := []int{1, 23, 24, 25, 500, 1000, 10000}
	batchSizes := []int{1, 5, 7, 24, 100, 20000}

	for _, scale := range scales {
		for _, batch := range batchSizes {
			scale, batch := scale, batch
			t.Run(scaleBatchName(scale, batch), func(t *testing.T) {
				t.Parallel()
				ctx := context.Background()
				root := filepath.Join(t.TempDir(), "app")
				manifest, err := runSeed(ctx, seedConfig{AppRoot: root, Scale: scale, BatchSize: batch})
				if err != nil {
					t.Fatalf("runSeed(scale=%d,batch=%d) failed: %v", scale, batch, err)
				}
				if manifest.Scale != scale {
					t.Fatalf("manifest.Scale = %d, want %d", manifest.Scale, scale)
				}
				if len(manifest.CreatorIDs) != corpus.CreatorCount {
					t.Fatalf("len(CreatorIDs) = %d, want %d", len(manifest.CreatorIDs), corpus.CreatorCount)
				}
				assertCreatorRelationCounts(t, ctx, root, manifest, scale)
			})
		}
	}
}

func scaleBatchName(scale, batch int) string {
	return "scale=" + strconv.Itoa(scale) + "/batch=" + strconv.Itoa(batch)
}

// assertCreatorRelationCounts 直接对已发布 revision 做只读校验：每个 Work 恰好有
// 一条 work_creator_relations，creator_projections 里的去重后行数不超过
// corpus.CreatorCount，且总关系数等于 Work 数（不多不少，证明没有重复关系，也没
// 有 Work 因为 Creator 冲突而丢失）。
func assertCreatorRelationCounts(t *testing.T, ctx context.Context, appRoot string, manifest corpus.Manifest, scale int) {
	t.Helper()
	dirs := appdirs.UnderRoot(appRoot)
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatalf("reopen storage: %v", err)
	}
	defer store.Close()

	db := store.Catalog.SQL()
	var workCount, relationCount, creatorProjectionCount int
	row := db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM work_projections WHERE catalog_revision_id=?),
		(SELECT count(*) FROM work_creator_relations WHERE catalog_revision_id=?),
		(SELECT count(*) FROM creator_projections WHERE catalog_revision_id=?)`,
		manifest.CatalogRevisionID, manifest.CatalogRevisionID, manifest.CatalogRevisionID)
	if err := row.Scan(&workCount, &relationCount, &creatorProjectionCount); err != nil {
		t.Fatalf("query relation counts: %v", err)
	}
	if workCount != scale {
		t.Fatalf("work_projections count = %d, want %d", workCount, scale)
	}
	if relationCount != scale {
		t.Fatalf("work_creator_relations count = %d, want %d (每个 Work 恰好一条 primary 关系)", relationCount, scale)
	}
	expectedCreators := corpus.CreatorCount
	if scale < corpus.CreatorCount {
		expectedCreators = scale
	}
	if creatorProjectionCount != expectedCreators {
		t.Fatalf("creator_projections count = %d, want %d（去重后不应超过语料实际使用的创作者槽位数）", creatorProjectionCount, expectedCreators)
	}
}
