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

// coreSeedDedupeScales/coreSeedDedupeBatches 是跨 race 与非 race 构建都运行的核心
// 边界矩阵（corpus.CreatorCount 当前为 24）：
//
//	scale: 1（scale<batch 的极端情形）、23（batch 周期-1）、24（恰好一个周期）、
//	       25（batch 周期+1，产生跨批次复用）
//	batch: 1、7（均 < 周期，逐条/小批粒度）、24（恰好等于周期）、100（> 周期，
//	       大批一次性覆盖并产生批内复用）
//
// 4×4=16 个组合覆盖：同批次内 Creator 重复（如 scale=25,batch=100）、跨批次
// Creator 重复（如 scale=25,batch=24）、batch 小于/等于/大于周期、scale 小于/
// 等于/不等于 batch 的整数倍、末批不完整（如 scale=25,batch=24 的第二批只有 1
// 条）。这些组合本身耗时是毫秒到秒级，race instrumentation 下也保持在可预期范围
// 内，不依赖运行速度或调度顺序。
var coreSeedDedupeScales = []int{1, 23, 24, 25}
var coreSeedDedupeBatches = []int{1, 7, 24, 100}

// extendedSeedDedupeScales/extendedSeedDedupeBatches 补充验证同一去重算法在更大
// 数据量下仍然成立，但不引入新的边界语义——真实大规模端到端行为由 tools/testlab
// 的 1k/10k/100k/500k 正式流水线负责，这里只是同一断言在更大 scale 下的重复
// 执行，因此只在 exhaustiveSeedMatrix 为 true（非 race 构建）时运行，见
// seed_matrix_race_test.go / seed_matrix_norace_test.go。
var extendedSeedDedupeScales = []int{500, 1000, 10000}
var extendedSeedDedupeBatches = []int{1, 5, 7, 24, 100, 20000}

// TestRunSeedDedupesCreatorsAcrossBatchBoundaries 覆盖此前真实出现过的
// "UNIQUE constraint failed: source_creators.catalog_revision_id,
// source_creators.source_id, source_creators.source_key" 缺陷：Creator 在同一
// batch 内重复、跨 batch 重复，且 batch 大小与 corpus.CreatorCount（24）不对齐、
// 恰好对齐、互质等各种关系下都不应重现。本测试只运行核心边界矩阵，在 race 与
// 非 race 构建下都执行；大规模量级的重复验证见
// TestRunSeedDedupesCreatorsAcrossBatchBoundariesExtended。
func TestRunSeedDedupesCreatorsAcrossBatchBoundaries(t *testing.T) {
	runSeedDedupeMatrix(t, coreSeedDedupeScales, coreSeedDedupeBatches, true)
}

// TestRunSeedDedupesCreatorsAcrossBatchBoundariesExtended 在非 race 构建下补充
// 验证 500/1000/10000 规模的同一去重算法；race 构建下这些组合的耗时会被显著
// 放大且不提供新的正确性边界（核心矩阵已覆盖全部去重语义），因此按
// exhaustiveSeedMatrix（由 race 构建约束控制，非环境变量）跳过，而不是永久跳过
// 或仅在 CI 中跳过——本地非 race 构建同样会运行本测试。大规模组合顺序执行
// （不调用 t.Parallel），避免与并行度无关的资源放大问题。
func TestRunSeedDedupesCreatorsAcrossBatchBoundariesExtended(t *testing.T) {
	if !exhaustiveSeedMatrix {
		t.Skip("race 构建下跳过大规模扩展矩阵：核心边界矩阵已覆盖全部去重算法边界，大规模端到端行为由 tools/testlab 的 1k/10k/100k/500k 正式流水线验证")
	}
	runSeedDedupeMatrix(t, extendedSeedDedupeScales, extendedSeedDedupeBatches, false)
}

func runSeedDedupeMatrix(t *testing.T, scales, batchSizes []int, parallel bool) {
	t.Helper()
	for _, scale := range scales {
		for _, batch := range batchSizes {
			scale, batch := scale, batch
			t.Run(scaleBatchName(scale, batch), func(t *testing.T) {
				if parallel {
					t.Parallel()
				}
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
