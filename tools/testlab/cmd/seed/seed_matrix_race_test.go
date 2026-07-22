//go:build race

package main

// exhaustiveSeedMatrix 为 false 时，TestRunSeedDedupesCreatorsAcrossBatchBoundariesExtended
// 只运行核心边界矩阵已经覆盖过的断言逻辑一次、不再展开大规模（500/1000/10000）
// 组合：race instrumentation 下这些大规模组合的耗时会被显著放大（实测 scale=10000
// 的单个组合可达 100+ 秒，是 CI race Job 600 秒默认超时的直接成因），而它们相对
// 核心矩阵不提供新的去重算法边界覆盖，只是同一算法在更大数据量下的重复验证——
// 真实规模（1k/10k/100k/500k）下的端到端行为由 tools/testlab 的正式规模验证流水线
// 负责，不需要在 race 单元测试里重复构建大规模 Catalog。
const exhaustiveSeedMatrix = false
