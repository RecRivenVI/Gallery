// Command testlabseed 直接构建并发布一个合法的 Catalog query_publication_id 快照，
// 不经过真实文件系统扫描。这是阶段无关正式压力测试的"数据生成路径"：为了在合理
// 时间内构建 1k~500k（推荐规模）乃至非推荐诊断规模的 WorkProjection，复用生产
// internal/catalog.Store 的 BeginCandidate/Stage/ApplyCatalogCandidateOverlays/
// ValidateCandidate/Publish 序列（与 internal/scanner 的真实发布路径完全一致），
// 只是跳过发现文件、执行规则和计算真实 SHA-256 这几步。"被测查询路径"由
// testlabprobe 通过真实 galleryd 与公开 REST API 执行，两条路径职责不同，不得
// 混淆。
//
// 证据边界（必须在使用结果前确认）：本工具单独证明正式查询服务对合法 Catalog
// publication 的行为、HTTP/OpenAPI 查询路径、FTS/过滤/排序/ranking/total/cursor、
// publication 查询读取性能。它本身不证明真实扫描、control.db 领域所有权、
// Library/Source 创建流程、真实 capability 资源裁剪、真实媒体读取、文件身份、
// Source 只读或完整跨库恢复——这些维度由 stages/stage4/media 的场景通过真实
// Source 目录与真实扫描单独验证。
//
// 所有对外可见的 Library/Source/Work/Media/Creator ID 都通过与生产完全一致的
// internal/platform/identity.Generator 生成（真实 UUIDv7 + 类型前缀），不使用
// 可预测的手写字符串：cursor.schema.json 对 lastCanonicalWorkId 等字段有严格的
// UUIDv7 pattern 校验，手写 ID 会在命中数超过一页、必须签发游标时被拒绝为
// CURSOR_INVALID。"确定性 seed"只保证内容、数量、关系和分布可复现，不保证 ID
// 字符串本身可复现（生产 ID 本来就不可预测）。
//
// 规模策略：标准调用不得生成 ≥1,000,000 规模；显式传入
// -allow-nonrecommended-scale 才能突破该上限，此时输出的 manifest 与控制台都会
// 标注 NONRECOMMENDED_SCALE，不构成标准 Gate。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
)

const nonrecommendedScaleThreshold = 1_000_000

func main() {
	appRoot := flag.String("approot", "", "目标 AppDirs 父目录（必须为空或不存在）")
	scale := flag.Int("scale", 1000, "生成的 WorkProjection 数量")
	batchSize := flag.Int("batch", 20000, "单次 Stage 调用的批大小，控制峰值内存")
	manifestPath := flag.String("manifest-out", "", "写入生成语料统计摘要 JSON 的路径（必需，probe 从中读取真实生成的 ID）")
	tier := flag.String("tier", "", "本次运行的规模等级标签（smoke/integration/preflight/reference/nonrecommended），仅写入 manifest 供报告标注，不影响生成逻辑")
	allowNonrecommended := flag.Bool("allow-nonrecommended-scale", false, "显式允许生成 >=1,000,000 的非推荐诊断规模")
	flag.Parse()

	if *manifestPath == "" {
		log.Fatal("必须指定 -manifest-out")
	}
	if *scale >= nonrecommendedScaleThreshold && !*allowNonrecommended {
		log.Fatalf("scale=%d 属于非推荐规模（>=%d），必须显式传入 -allow-nonrecommended-scale 才能运行；标准命令、CI 与默认脚本不得生成此规模", *scale, nonrecommendedScaleThreshold)
	}
	if *scale >= nonrecommendedScaleThreshold {
		fmt.Println("NONRECOMMENDED_SCALE：本次规模不属于正式支持规模，不构成标准 Gate")
	}

	manifest, err := runSeed(context.Background(), seedConfig{AppRoot: *appRoot, Scale: *scale, BatchSize: *batchSize})
	if err != nil {
		log.Fatalf("testlabseed 失败: %v", err)
	}
	manifest.Tier = *tier
	manifest.Nonrecommended = *scale >= nonrecommendedScaleThreshold

	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		log.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(*manifestPath, encoded, 0o644); err != nil {
		log.Fatalf("write manifest: %v", err)
	}
	fmt.Printf("testlabseed: scale=%d tier=%s stageMs=%d overlayMs=%d publishMs=%d totalMs=%d manifest=%s\n",
		manifest.Scale, manifest.Tier, manifest.StageDurationMs, manifest.OverlayDurationMs, manifest.PublishDurationMs,
		manifest.TotalDurationMs, *manifestPath)
}
