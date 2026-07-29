package seeding

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/storage"
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
	if result.PublicationMatrix == nil || result.PublicationMatrix.RelationsPerWork != 2 || len(result.PublicationMatrix.Ratios) != 2 {
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
		if sample.WorkCreatorRelationCount != 200 || sample.MediaProjectionCount != 100 || sample.SourceMediaCount != 100 ||
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

func TestRunPublicationMatrixResumesFromAtomicReportAndCleansDanglingCandidate(t *testing.T) {
	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	reportPath := filepath.Join(root, "publication-report.json")
	stopAfterFirst := errors.New("test checkpoint stop")
	cfg := PublicationMatrixConfig{
		AppRoot: appRoot, Scale: 100, Sources: 4, BatchSize: 17,
		PrimarySourceShare: 0.50, ChangeRatios: []float64{0.10}, SamplesPerRatio: 2, Tier: "preflight",
	}
	cfg.Checkpoint = func(current *report.Report) error {
		if err := current.Save(reportPath); err != nil {
			return err
		}
		if current.CompletedCombinations == 1 {
			return stopAfterFirst
		}
		return nil
	}
	partial, err := RunPublicationMatrix(context.Background(), cfg)
	if !errors.Is(err, stopAfterFirst) || partial.CompletedCombinations != 1 {
		t.Fatalf("首段没有在第一个样本后停止: completed=%d err=%v", partial.CompletedCombinations, err)
	}

	dirs := appdirs.UnderRoot(appRoot)
	store, err := storage.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), clock.System{}, identity.NewGenerator(clock.System{}))
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	var sourceID string
	publication, err := catalogStore.Current(context.Background())
	if err == nil {
		err = store.Catalog.SQL().QueryRow(`SELECT source_id FROM work_projections
WHERE catalog_revision_id=? AND overlay_revision_id=? AND source_key=?`,
			publication.CatalogRevisionID, publication.OverlayRevisionID, "stage4/work-00000000").Scan(&sourceID)
	}
	if err == nil {
		_, err = catalogStore.BeginCandidate(context.Background(), "job-dangling-resume", sourceID, 999)
	}
	if closeErr := store.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatalf("建立遗留 candidate: %v", err)
	}

	loaded, err := report.Load(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	originalContentBlobs := loaded.PublicationMatrix.Ratios[0].Runs[0].ContentBlobCount
	loaded.PublicationMatrix.Ratios[0].Runs[0].ContentBlobCount--
	if err := validatePublicationResumeReport(cfg, 50, &loaded); err == nil {
		t.Fatal("续跑报告不得接受被篡改的 ContentBlob 计数")
	}
	loaded.PublicationMatrix.Ratios[0].Runs[0].ContentBlobCount = originalContentBlobs
	cfg.Resume = &loaded
	cfg.Checkpoint = func(current *report.Report) error { return current.Save(reportPath) }
	resumed, err := RunPublicationMatrix(context.Background(), cfg)
	if err != nil {
		t.Fatalf("续跑 publication 矩阵: %v", err)
	}
	if resumed.CompletedCombinations != 2 || resumed.FailureCount != 0 ||
		resumed.PublicationMatrix.ResumeCount != 1 || resumed.PublicationMatrix.RecoveredStaging != 1 {
		t.Fatalf("续跑报告不完整: %+v", resumed.PublicationMatrix)
	}
	runs := resumed.PublicationMatrix.Ratios[0].Runs
	if len(runs) != 2 || runs[0].Run != 1 || runs[1].Run != 2 || runs[1].Revision <= runs[0].Revision {
		t.Fatalf("续跑样本没有无重无漏推进: %+v", runs)
	}
	finalReport, err := report.Load(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Resume = &finalReport
	verified, err := RunPublicationMatrix(context.Background(), cfg)
	if err != nil || verified.CompletedCombinations != 2 || verified.PublicationMatrix.ResumeCount != 1 {
		t.Fatalf("已完成报告没有重新核对 AppRoot 后无副作用返回: result=%+v err=%v", verified.PublicationMatrix, err)
	}
	bad := cfg
	bad.AppRoot = filepath.Join(root, "missing-app")
	if _, err := RunPublicationMatrix(context.Background(), bad); err == nil {
		t.Fatal("已完成报告不得绕过缺失 AppRoot 的复核")
	}
}
