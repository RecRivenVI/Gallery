// Command publication-perf 通过生产 catalog.Store 执行完整的
// 500k/10 Source/1%/10%/50% publication 变化矩阵。默认是小规模
// preflight；只有 -tier reference 且全部正式参数满足时，报告才可用于
// Reference Performance Gate。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/RecRivenVI/gallery/tools/testlab/internal/report"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/seeding"
)

func main() {
	appRoot := flag.String("approot", "", "独立测试 AppDirs 父目录（必须不存在或为空）")
	reportOut := flag.String("report-out", "", "原子写入的脱敏 JSON 报告路径（必须位于 AppRoot 之外）")
	scale := flag.Int("scale", 1_000, "WorkProjection 总数")
	sources := flag.Int("sources", 10, "Source 总数")
	primaryShare := flag.Float64("primary-share", 0.50, "主 Source 占全库 WorkProjection 的份额")
	ratiosText := flag.String("ratios", "0.01,0.10,0.50", "以全库 WorkProjection 为分母的逗号分隔变化比例")
	samples := flag.Int("samples", 1, "每个变化比例的完整候选/发布样本数")
	batch := flag.Int("batch", 20_000, "单次 Stage 输入批大小")
	tier := flag.String("tier", "preflight", "规模等级：preflight/reference")
	timeout := flag.Duration("timeout", 0, "整体运行超时；0 表示不设墙钟上限")
	flag.Parse()

	if strings.TrimSpace(*appRoot) == "" || strings.TrimSpace(*reportOut) == "" {
		log.Fatal("必须同时指定 -approot 与 -report-out")
	}
	if inside, err := pathInside(*reportOut, *appRoot); err != nil {
		log.Fatalf("核对报告路径: %v", err)
	} else if inside {
		log.Fatal("-report-out 不得位于 -approot 内，否则会污染空间测量")
	}
	ratios, err := parseRatios(*ratiosText)
	if err != nil {
		log.Fatalf("解析 -ratios: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(*reportOut), 0o700); err != nil {
		log.Fatalf("建立报告目录: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}
	lastCompleted := -1
	result, err := seeding.RunPublicationMatrix(ctx, seeding.PublicationMatrixConfig{
		AppRoot: *appRoot, Scale: *scale, Sources: *sources, BatchSize: *batch,
		PrimarySourceShare: *primaryShare, ChangeRatios: ratios, SamplesPerRatio: *samples, Tier: *tier,
		Checkpoint: func(current *report.Report) error {
			if err := current.Save(*reportOut); err != nil {
				return err
			}
			if current.CompletedCombinations != lastCompleted {
				lastCompleted = current.CompletedCombinations
				fmt.Printf("publication-perf: checkpoint %d/%d\n", current.CompletedCombinations, current.PlannedCombinations)
			}
			return nil
		},
	})
	if err != nil {
		log.Fatalf("publication 变化矩阵失败: %v", err)
	}
	fmt.Printf("publication-perf: tier=%s scale=%d sources=%d completed=%d/%d failures=%d\n",
		result.Tier, result.Scale, result.Corpus.SourceCount, result.CompletedCombinations,
		result.PlannedCombinations, result.FailureCount)
	if result.FailureCount != 0 {
		log.Fatalf("publication 变化矩阵有 %d 项失败，详见脱敏报告", result.FailureCount)
	}
}

func parseRatios(raw string) ([]float64, error) {
	parts := strings.Split(raw, ",")
	ratios := make([]float64, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("存在空比例")
		}
		ratio, err := strconv.ParseFloat(part, 64)
		if err != nil {
			return nil, fmt.Errorf("%q 不是浮点数: %w", part, err)
		}
		ratios = append(ratios, ratio)
	}
	return ratios, nil
}

func pathInside(candidate, root string) (bool, error) {
	absCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return false, err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false, err
	}
	if !strings.EqualFold(filepath.VolumeName(absCandidate), filepath.VolumeName(absRoot)) {
		return false, nil
	}
	relative, err := filepath.Rel(absRoot, absCandidate)
	if err != nil {
		return false, err
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))), nil
}
