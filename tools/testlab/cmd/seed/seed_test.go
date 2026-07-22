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

// seedDedupeScenario 是扩展矩阵里一个显式命名的代表性场景：每个场景必须有独立的
// 覆盖价值（见下方 extendedSeedDedupeScenarios 各项注释），不得只是改变数值的
// 重复组合。parallel 由场景自身决定，而不是整批统一开关：多个 scale=10000 场景
// 同时并行可能在较弱的 CI 机器上产生明显的 SQLite 文件 I/O 竞争，因此这些场景各自
// 标记为串行，其余小规模场景之间仍然并行执行。
type seedDedupeScenario struct {
	name     string
	scale    int
	batch    int
	parallel bool
}

// extendedSeedDedupeScenarios 补充验证同一去重算法在更大数据量下仍然成立，但不
// 用笛卡尔积重复核心矩阵已经证明过的边界语义（同批次/跨批次复用、batch 与周期
// 24 的大小关系、末批不完整、scale 是否为 batch 整数倍——这些都已由
// coreSeedDedupeScales × coreSeedDedupeBatches 在毫秒到秒级验证过）。真实大规模
// 端到端行为由 tools/testlab 的 1k/10k/100k/500k 正式流水线负责，这里只证明去重
// 算法本身在更大数据量下不失效，因此只在 exhaustiveSeedMatrix 为 true（非 race
// 构建）时运行，见 seed_matrix_race_test.go / seed_matrix_norace_test.go。
var extendedSeedDedupeScenarios = []seedDedupeScenario{
	{
		// 500/7≈71 个批次，验证"跨多个批次持续复用全部 24 个 Creator 槽位"在核心
		// 矩阵最大 scale=25（最多 1-2 个批次）之外、明显更多批次跨越时不泄漏状态。
		name: "scale=500/batch=7/many-small-batches-mid-scale", scale: 500, batch: 7, parallel: true,
	},
	{
		// batch 恰好等于 Creator 周期，1000/24≈42 个完整周期的批次连续执行（核心
		// 矩阵的 scale=24,batch=24 只验证了单个周期），验证多周期重复不引入漂移。
		name: "scale=1000/batch=24/batch-equals-period-many-cycles", scale: 1000, batch: 24, parallel: true,
	},
	{
		// batch 远大于 scale，整个 scale 落在同一个批次内一次性 Stage（核心矩阵的
		// batch=100 只在 scale<=25 时验证过这个形状），在更大规模下确认单批次路径
		// 仍然正确。
		name: "scale=1000/batch=20000/single-oversized-batch-larger-scale", scale: 1000, batch: 20000, parallel: true,
	},
	{
		// 两个 scale=10000 场景之一：batch=1 产生 10,000 个批次（最极端的批次
		// 数量），验证极限批次数量下 Creator 去重和跨批次一致性不因批次数量本身
		// 而失效。与另一个 10k 场景都标记为非并行，避免两个大规模 SQLite AppDirs
		// 同时写入造成的 I/O 竞争。
		name: "scale=10000/batch=1/max-batch-count-at-scale", scale: 10000, batch: 1, parallel: false,
	},
	{
		// 两个 scale=10000 场景之二：100 个中等大小批次，代表比 batch=1 更典型的
		// 批大小选择，与上一场景共同覆盖"批次数量差异巨大时是否仍然一致"。同样
		// 标记为非并行。
		name: "scale=10000/batch=100/typical-multi-batch-at-scale", scale: 10000, batch: 100, parallel: false,
	},
}

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

// TestRunSeedDedupesCreatorsAcrossBatchBoundariesExtended 在非 race 构建下运行
// extendedSeedDedupeScenarios 列出的少量代表性大规模场景；race 构建下这些场景的
// 耗时会被显著放大且不提供新的正确性边界（核心矩阵已覆盖全部去重语义），因此按
// exhaustiveSeedMatrix（由 race 构建约束控制，非环境变量）跳过，而不是永久跳过
// 或仅在 CI 中跳过——本地非 race 构建同样会运行本测试。
func TestRunSeedDedupesCreatorsAcrossBatchBoundariesExtended(t *testing.T) {
	if !exhaustiveSeedMatrix {
		t.Skip("race 构建下跳过大规模扩展场景：核心边界矩阵已覆盖全部去重算法边界，大规模端到端行为由 tools/testlab 的 1k/10k/100k/500k 正式流水线验证")
	}
	runSeedDedupeScenarios(t, extendedSeedDedupeScenarios)
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
				runSeedDedupeCase(t, scale, batch)
			})
		}
	}
}

func runSeedDedupeScenarios(t *testing.T, scenarios []seedDedupeScenario) {
	t.Helper()
	for _, scenario := range scenarios {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			if scenario.parallel {
				t.Parallel()
			}
			runSeedDedupeCase(t, scenario.scale, scenario.batch)
		})
	}
}

// runSeedDedupeCase 是核心矩阵与扩展场景共用的单次断言：每次调用使用独立的
// t.TempDir()（自动隔离、自动清理，不共享数据库、全局生成器或端口），不依赖执行
// 顺序，因此可以安全并行。
func runSeedDedupeCase(t *testing.T, scale, batch int) {
	t.Helper()
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
