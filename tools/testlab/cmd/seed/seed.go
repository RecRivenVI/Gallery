// 本文件只保留 testlabseed 命令对可复用 seeding 包的薄适配；生产式合成
// Catalog 构建逻辑位于 tools/testlab/internal/seeding，供命令与阶段 4 自动化
// smoke 共用，避免 CLI 与 go test 入口各自维护一份语料写入实现。
package main

import (
	"context"

	"github.com/RecRivenVI/gallery/tools/testlab/internal/corpus"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/seeding"
)

type seedConfig = seeding.Config

func runSeed(ctx context.Context, cfg seedConfig) (corpus.Manifest, error) {
	return seeding.Run(ctx, cfg)
}
