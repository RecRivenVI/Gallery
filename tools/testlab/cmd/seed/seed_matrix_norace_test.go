//go:build !race

package main

// exhaustiveSeedMatrix 为 true 时，TestRunSeedDedupesCreatorsAcrossBatchBoundariesExtended
// 额外运行大规模（500/1000/10000）批次组合：非 race 构建下这些组合总耗时是秒级，
// 用于补充验证同一去重算法在更大数据量下仍然成立，而不依赖 race instrumentation
// 的额外开销。
const exhaustiveSeedMatrix = true
