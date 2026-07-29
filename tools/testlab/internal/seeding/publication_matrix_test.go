package seeding

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/RecRivenVI/gallery/tools/testlab/internal/report"
)

func TestNormalizePublicationMatrixConfigRequiresFormalReferenceShape(t *testing.T) {
	valid := PublicationMatrixConfig{
		AppRoot: "test-root", Scale: ReferencePublicationScale, Sources: ReferencePublicationSources,
		PrimarySourceShare: ReferencePrimarySourceShare, ChangeRatios: []float64{0.50, 0.01, 0.10},
		SamplesPerRatio: MinReferencePublicationSamples, Tier: "reference",
	}
	normalized, primaryWorks, err := normalizePublicationMatrixConfig(valid)
	if err != nil {
		t.Fatalf("正式 reference 形状被拒绝: %v", err)
	}
	if primaryWorks != 250_000 || !sameRatios(normalized.ChangeRatios, ReferencePublicationRatios) {
		t.Fatalf("正式 reference 形状未规范化: primary=%d ratios=%v", primaryWorks, normalized.ChangeRatios)
	}

	invalid := valid
	invalid.SamplesPerRatio = MinReferencePublicationSamples - 1
	if _, _, err := normalizePublicationMatrixConfig(invalid); err == nil {
		t.Fatal("样本数不足的 reference 应被拒绝")
	}
	invalid = valid
	invalid.PrimarySourceShare = 0.49
	if _, _, err := normalizePublicationMatrixConfig(invalid); err == nil {
		t.Fatal("主 Source 份额不合法的 reference 应被拒绝")
	}
}

func TestWeightedSourceAssignerMakesGlobalFiftyPercentPossible(t *testing.T) {
	const scale, sources, primary = 100, 4, 50
	provider := weightedSourceIndices(primary)
	counts := make([]int, sources)
	seen := make(map[int]bool, scale)
	for slot := 0; slot < sources; slot++ {
		for _, index := range provider(slot, scale, sources) {
			if index < 0 || index >= scale || seen[index] {
				t.Fatalf("分配到非法或重复下标: index=%d slot=%d", index, slot)
			}
			seen[index] = true
			counts[slot]++
		}
	}
	if len(seen) != scale {
		t.Fatalf("加权分配遗漏下标: got=%d want=%d", len(seen), scale)
	}
	if counts[0] != primary {
		t.Fatalf("主 Source 作品数=%d want=%d", counts[0], primary)
	}
	for slot := 1; slot < sources; slot++ {
		if counts[slot] == 0 {
			t.Fatalf("Source %d 被分配为空: %v", slot, counts)
		}
	}
}

func TestRunPublicationMatrixUsesGlobalRatiosAndCompleteCandidates(t *testing.T) {
	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	reportPath := filepath.Join(root, "publication-report.json")
	checkpointCount := 0
	result, err := RunPublicationMatrix(context.Background(), PublicationMatrixConfig{
		AppRoot: appRoot, Scale: 100, Sources: 4, BatchSize: 17,
		PrimarySourceShare: 0.50, ChangeRatios: []float64{0.10, 0.50},
		SamplesPerRatio: 1, Tier: "preflight",
		Checkpoint: func(current *report.Report) error {
			checkpointCount++
			return current.Save(reportPath)
		},
	})
	if err != nil {
		t.Fatalf("执行 publication 变化矩阵: %v", err)
	}
	if result.FailureCount != 0 {
		t.Fatalf("矩阵报告包含失败: %+v", result.Findings)
	}
	if checkpointCount != 4 { // baseline + 两个样本 + 最终报告
		t.Fatalf("checkpoint 次数=%d want=4", checkpointCount)
	}
	if result.CompletedCombinations != 2 || result.PlannedCombinations != 2 {
		t.Fatalf("矩阵组合计数不完整: %d/%d", result.CompletedCombinations, result.PlannedCombinations)
	}
	if result.PublicationMatrix == nil || len(result.PublicationMatrix.Ratios) != 2 {
		t.Fatalf("publicationMatrix 不完整: %+v", result.PublicationMatrix)
	}
	wants := []int{10, 50}
	for index, ratio := range result.PublicationMatrix.Ratios {
		if ratio.ChangedWorks != wants[index] || ratio.CompletedRuns != 1 || len(ratio.Runs) != 1 {
			t.Fatalf("变化比例 %d 报告不完整: %+v", index, ratio)
		}
		sample := ratio.Runs[0]
		if sample.ActiveWorkCount != 100 || sample.ChangedProjectionCount != wants[index] || !sample.OldSnapshotReadableAcrossBuild {
			t.Fatalf("样本 %d 没有证明完整候选/旧快照: %+v", index, sample)
		}
		if sample.WorkCreatorRelationCount != 100 || sample.MediaProjectionCount != 100 || sample.SourceMediaCount != 100 ||
			sample.ContentBlobCount != 66 || sample.FileLocationCount != 66 || sample.FTSDocumentCount != 100 ||
			sample.SearchCandidateCount != 100 || sample.SourceCount != 4 {
			t.Fatalf("样本 %d 的媒体/关系/FTS 形状不完整: %+v", index, sample)
		}
		// 极小预检下 Windows 单调钟可能把极短 Publish 量化为 0；单元测试
		// 只锁定样本被记录，不用机器速度决定通过与否。
		if sample.PublishMs < 0 || sample.BytesPeak < sample.BytesBefore || sample.BytesPeak < sample.BytesAfter {
			t.Fatalf("样本 %d 的发布/空间测量无效: %+v", index, sample)
		}
	}
	if len(result.Latencies) != 2 || result.Latencies[0].SuccessfulRuns != 1 || result.Latencies[1].SuccessfulRuns != 1 {
		t.Fatalf("发布分位样本未与原始运行对齐: %+v", result.Latencies)
	}
}
