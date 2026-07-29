package seeding

import (
	"context"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/storage"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/corpus"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/hostfacts"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/report"
)

const (
	ReferencePublicationScale       = 500_000
	ReferencePublicationSources     = 10
	ReferencePrimarySourceShare     = 0.50
	MinReferencePublicationSamples  = 20
	ReferencePublishP95Milliseconds = 250.0
)

var ReferencePublicationRatios = []float64{0.01, 0.10, 0.50}

// PublicationMatrixConfig 描述一次完整 Catalog publication 变化矩阵。
// Checkpoint 在 baseline 和每个样本完成后收到可原子持久化的当前报告，使数小时的
// reference 运行即使被中断也不会只剩一份空结果。
type PublicationMatrixConfig struct {
	AppRoot            string
	Scale              int
	Sources            int
	BatchSize          int
	PrimarySourceShare float64
	ChangeRatios       []float64
	SamplesPerRatio    int
	Tier               string
	Checkpoint         func(*report.Report) error
}

func weightedSourceIndices(primaryWorks int) sourceIndexProvider {
	return func(slot, scale, sources int) []int {
		if slot == 0 {
			indices := make([]int, primaryWorks)
			for index := range indices {
				indices[index] = index
			}
			return indices
		}
		indices := make([]int, 0, (scale-primaryWorks)/(sources-1)+1)
		for index := primaryWorks + slot - 1; index < scale; index += sources - 1 {
			indices = append(indices, index)
		}
		return indices
	}
}

func normalizePublicationMatrixConfig(cfg PublicationMatrixConfig) (PublicationMatrixConfig, int, error) {
	if strings.TrimSpace(cfg.AppRoot) == "" {
		return cfg, 0, fmt.Errorf("AppRoot 不能为空")
	}
	if cfg.Scale <= 0 {
		return cfg, 0, fmt.Errorf("Scale 必须为正整数")
	}
	if cfg.Sources < 2 || cfg.Sources > cfg.Scale {
		return cfg, 0, fmt.Errorf("Source 数量必须在 2..Scale 之间: %d", cfg.Sources)
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 20_000
	}
	if math.IsNaN(cfg.PrimarySourceShare) || math.IsInf(cfg.PrimarySourceShare, 0) ||
		cfg.PrimarySourceShare <= 0 || cfg.PrimarySourceShare >= 1 {
		return cfg, 0, fmt.Errorf("主 Source 份额必须在 0..1 之间: %g", cfg.PrimarySourceShare)
	}
	primaryWorks := int(math.Round(float64(cfg.Scale) * cfg.PrimarySourceShare))
	if primaryWorks <= 0 || cfg.Scale-primaryWorks < cfg.Sources-1 {
		return cfg, 0, fmt.Errorf("主 Source 分布会使其它 Source 产生空候选: primary=%d scale=%d sources=%d", primaryWorks, cfg.Scale, cfg.Sources)
	}
	if cfg.SamplesPerRatio <= 0 {
		return cfg, 0, fmt.Errorf("每个变化比例的样本数必须为正整数")
	}
	if len(cfg.ChangeRatios) == 0 {
		return cfg, 0, fmt.Errorf("至少需要一个变化比例")
	}
	cfg.ChangeRatios = append([]float64(nil), cfg.ChangeRatios...)
	sort.Float64s(cfg.ChangeRatios)
	for index, ratio := range cfg.ChangeRatios {
		if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio <= 0 || ratio > cfg.PrimarySourceShare {
			return cfg, 0, fmt.Errorf("变化比例 %g 必须大于 0 且不超过主 Source 份额 %g", ratio, cfg.PrimarySourceShare)
		}
		if index > 0 && almostEqual(ratio, cfg.ChangeRatios[index-1]) {
			return cfg, 0, fmt.Errorf("变化比例重复: %g", ratio)
		}
		changed := int(math.Round(float64(cfg.Scale) * ratio))
		if changed <= 0 || changed > primaryWorks {
			return cfg, 0, fmt.Errorf("变化比例 %g 无法在当前规模下得到合法作品数", ratio)
		}
	}
	cfg.Tier = strings.ToLower(strings.TrimSpace(cfg.Tier))
	if cfg.Tier == "" {
		cfg.Tier = "preflight"
	}
	if cfg.Tier != "preflight" && cfg.Tier != "reference" {
		return cfg, 0, fmt.Errorf("tier 必须是 preflight 或 reference: %q", cfg.Tier)
	}
	if cfg.Tier == "reference" {
		if cfg.Scale != ReferencePublicationScale || cfg.Sources != ReferencePublicationSources ||
			!almostEqual(cfg.PrimarySourceShare, ReferencePrimarySourceShare) ||
			cfg.SamplesPerRatio < MinReferencePublicationSamples || !sameRatios(cfg.ChangeRatios, ReferencePublicationRatios) {
			return cfg, 0, fmt.Errorf("reference 必须使用 scale=%d sources=%d primary-share=%.2f ratios=0.01,0.10,0.50 samples>=%d",
				ReferencePublicationScale, ReferencePublicationSources, ReferencePrimarySourceShare, MinReferencePublicationSamples)
		}
	}
	return cfg, primaryWorks, nil
}

func almostEqual(left, right float64) bool { return math.Abs(left-right) < 1e-9 }

func sameRatios(left, right []float64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !almostEqual(left[index], right[index]) {
			return false
		}
	}
	return true
}

func ensureEmptyPublicationRoot(root string) error {
	items, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取 publication AppRoot: %w", err)
	}
	if len(items) != 0 {
		return fmt.Errorf("publication AppRoot 必须不存在或为空目录")
	}
	return nil
}

func durationMS(value time.Duration) float64 { return float64(value.Nanoseconds()) / 1_000_000 }

func treeBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}

// RunPublicationMatrix 构建一个十 Source 可扩展的完整基线，并只重扫持有足够作品的
// 主 Source。变化比例以整个 active publication 的 WorkProjection 数为分母；这保证
// 1%/10%/50% 都是全局比例，而不是把「主 Source 内 50%」误写成「全库 50%」。
func RunPublicationMatrix(ctx context.Context, raw PublicationMatrixConfig) (report.Report, error) {
	runStarted := time.Now()
	cfg, primaryWorks, err := normalizePublicationMatrixConfig(raw)
	if err != nil {
		return report.Report{}, err
	}
	if err := ensureEmptyPublicationRoot(cfg.AppRoot); err != nil {
		return report.Report{}, err
	}
	sourceIndices := weightedSourceIndices(primaryWorks)
	baseline, state, err := runBaseline(ctx, Config{
		AppRoot: cfg.AppRoot, Scale: cfg.Scale, BatchSize: cfg.BatchSize, Sources: cfg.Sources,
	}, baselineOptions{sourceIndices: sourceIndices, captureSource: 0})
	if err != nil {
		return report.Report{}, fmt.Errorf("构建 publication baseline: %w", err)
	}
	baselineBytes, err := treeBytes(cfg.AppRoot)
	if err != nil {
		return report.Report{}, fmt.Errorf("统计 baseline 空间: %w", err)
	}
	dirs := appdirs.UnderRoot(cfg.AppRoot)
	facts := hostfacts.Collect(dirs.Data)
	result := report.Report{
		SchemaVersion: 2, Scenario: "stage4-publication-change-matrix", Tier: cfg.Tier,
		Transport: "production-catalog-store", Scale: cfg.Scale, Environment: &facts,
		Corpus: &report.CorpusFacts{
			Scale: cfg.Scale, SourceCount: cfg.Sources, ClonedSourceCountOnLastPublish: cfg.Sources - 1,
			SourceBeginDurationsMs:      baseline.SourceBeginDurationsMs,
			SourceValidationDurationsMs: baseline.SourceValidationDurationsMs,
			SourcePublishDurationsMs:    baseline.SourcePublishDurationsMs,
		},
		PublicationMatrix: &report.PublicationMatrix{
			ChangeRatioBasis: "active-publication-work-projections", PrimarySourceShare: cfg.PrimarySourceShare,
			PrimarySourceWorks: primaryWorks, SamplesPerRatio: cfg.SamplesPerRatio, Concurrency: 1,
			CacheState: report.CacheStateWarm, TargetPublishP95Ms: ReferencePublishP95Milliseconds,
			PercentileMethod: report.PercentileMethodNearestRank, RelationsPerWork: 2,
			Baseline: report.PublicationBaseline{
				StageMs: float64(baseline.StageDurationMs), OverlayMs: float64(baseline.OverlayDurationMs),
				ValidationMs: float64(baseline.ValidationDurationMs), PublishMs: float64(baseline.PublishDurationMs),
				TotalMs: float64(baseline.TotalDurationMs), Bytes: baselineBytes,
			},
		},
		StartedAt:           runStarted.UTC().Format(time.RFC3339),
		PlannedCombinations: len(cfg.ChangeRatios) * cfg.SamplesPerRatio,
		Limitations: []string{
			"未主动清空操作系统文件缓存；cacheState=warm 只表示 baseline 和完整验证已预热进程与 SQLite 页缓存。",
			"本矩阵使用合成完整 SourceMedia/ContentBlob/FileLocation/FTS 和两条每作品关系，不代表真实 HDD/SMB/NAS 或完整哈希吞吐。",
		},
	}
	if cfg.Tier != "reference" {
		result.Limitations = append(result.Limitations,
			"preflight 规模只验证矩阵语义与报告管道，墙钟结果不构成 Reference Performance Gate 结论。")
	}
	if len(state.identities) != primaryWorks || len(state.overlayFacts) != cfg.Scale {
		return result, fmt.Errorf("baseline 身份/覆盖事实不完整: identities=%d/%d overlays=%d/%d",
			len(state.identities), primaryWorks, len(state.overlayFacts), cfg.Scale)
	}
	if cfg.Checkpoint != nil {
		if err := cfg.Checkpoint(&result); err != nil {
			return result, fmt.Errorf("保存 baseline 报告: %w", err)
		}
	}

	store, err := storage.Open(ctx, dirs)
	if err != nil {
		return result, fmt.Errorf("重新打开 publication storage: %w", err)
	}
	defer store.Close()
	systemClock := clock.System{}
	ids := identity.NewGenerator(systemClock)
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), systemClock, ids)
	if err != nil {
		return result, fmt.Errorf("建立 publication Catalog Store: %w", err)
	}

	revision := 0
	expectedVerified := 0
	for index := 0; index < cfg.Scale; index++ {
		if corpus.ContentVerified(index) {
			expectedVerified++
		}
	}
	for _, ratio := range cfg.ChangeRatios {
		changedWorks := int(math.Round(float64(cfg.Scale) * ratio))
		ratioResult := report.PublicationRatioResult{
			ChangeRatio: ratio, ChangedWorks: changedWorks, PlannedRuns: cfg.SamplesPerRatio,
		}
		result.PublicationMatrix.Ratios = append(result.PublicationMatrix.Ratios, ratioResult)
		ratioIndex := len(result.PublicationMatrix.Ratios) - 1
		result.Latencies = append(result.Latencies, report.LatencySample{
			Category: fmt.Sprintf("publication/change-%g", ratio), Concurrency: 1,
			PlannedRuns: cfg.SamplesPerRatio, NotAttemptedRuns: cfg.SamplesPerRatio,
			CacheState: report.CacheStateWarm, PercentileMethod: report.PercentileMethodNearestRank,
		})
		latencyIndex := len(result.Latencies) - 1
		publishDurations := make([]time.Duration, 0, cfg.SamplesPerRatio)
		for run := 1; run <= cfg.SamplesPerRatio; run++ {
			revision++
			previous, err := catalogStore.Current(ctx)
			if err != nil {
				return result, fmt.Errorf("读取变化前 publication: %w", err)
			}
			oldReadable := publicationWorkCount(ctx, store, previous.ID) == cfg.Scale
			sampleStarted := time.Now()
			bytesBefore, err := treeBytes(cfg.AppRoot)
			if err != nil {
				return result, fmt.Errorf("统计样本前空间: %w", err)
			}
			jobID, err := mustNewID(ids, domain.IDJob)
			if err != nil {
				return result, err
			}
			beginStarted := time.Now()
			candidate, err := catalogStore.BeginCandidate(ctx, jobID, state.sourceIDs[0], int64(revision))
			if err != nil {
				return result, fmt.Errorf("begin publication ratio=%g run=%d: %w", ratio, run, err)
			}
			beginDuration := time.Since(beginStarted)
			oldReadable = oldReadable && publicationWorkCount(ctx, store, previous.ID) == cfg.Scale

			stageStarted := time.Now()
			if err := stageSourceWorks(ctx, catalogStore, candidate, stageParams{
				libraryID: state.libraryID, sourceID: state.sourceIDs[0], slot: 0, sources: cfg.Sources,
				scale: cfg.Scale, batchSize: cfg.BatchSize, ids: ids, creatorIDs: state.creatorIDs,
				overlayFacts: state.overlayFacts, sourceIndices: sourceIndices, identities: state.identities,
				requireIdentities: true, mutation: mutationProfile{revision: revision, changedWorks: changedWorks},
			}); err != nil {
				return result, fmt.Errorf("stage publication ratio=%g run=%d: %w", ratio, run, err)
			}
			stageDuration := time.Since(stageStarted)
			oldReadable = oldReadable && publicationWorkCount(ctx, store, previous.ID) == cfg.Scale

			overlayStarted := time.Now()
			if err := catalogStore.ApplyCatalogCandidateOverlays(ctx, candidate, state.overlayFacts); err != nil {
				return result, fmt.Errorf("overlay publication ratio=%g run=%d: %w", ratio, run, err)
			}
			overlayDuration := time.Since(overlayStarted)
			oldReadable = oldReadable && publicationWorkCount(ctx, store, previous.ID) == cfg.Scale

			validationStarted := time.Now()
			if err := catalogStore.ValidateCandidate(ctx, candidate); err != nil {
				return result, fmt.Errorf("validate publication ratio=%g run=%d: %w", ratio, run, err)
			}
			validationDuration := time.Since(validationStarted)
			oldReadable = oldReadable && publicationWorkCount(ctx, store, previous.ID) == cfg.Scale
			candidateBytes, err := treeBytes(cfg.AppRoot)
			if err != nil {
				return result, fmt.Errorf("统计候选峰值空间: %w", err)
			}

			publishStarted := time.Now()
			publication, err := catalogStore.Publish(ctx, candidate)
			publishDuration := time.Since(publishStarted)
			if err != nil {
				return result, fmt.Errorf("publish ratio=%g run=%d: %w", ratio, run, err)
			}
			publishedBytes, err := treeBytes(cfg.AppRoot)
			if err != nil {
				return result, fmt.Errorf("统计发布后空间: %w", err)
			}
			oldReadable = oldReadable && publicationWorkCount(ctx, store, previous.ID) == cfg.Scale
			shape, err := activeProjectionCounts(ctx, store, publication, revision)
			if err != nil {
				return result, fmt.Errorf("复核 publication ratio=%g run=%d: %w", ratio, run, err)
			}
			if shape.works != cfg.Scale || shape.workCreatorRelations != cfg.Scale || shape.mediaProjections != cfg.Scale ||
				shape.sourceMedia != cfg.Scale || shape.contentBlobs != expectedVerified || shape.fileLocations != expectedVerified ||
				shape.ftsDocuments != cfg.Scale || shape.searchCandidates != cfg.Scale || shape.sources != cfg.Sources ||
				shape.changed != changedWorks {
				return result, fmt.Errorf("publication 事实不完整 ratio=%g run=%d works=%d relations=%d media=%d sourceMedia=%d blobs=%d locations=%d fts=%d candidates=%d sources=%d changed=%d",
					ratio, run, shape.works, shape.workCreatorRelations, shape.mediaProjections, shape.sourceMedia,
					shape.contentBlobs, shape.fileLocations, shape.ftsDocuments, shape.searchCandidates, shape.sources, shape.changed)
			}

			gcStarted := time.Now()
			if _, err := catalogStore.GarbageCollect(ctx, 0); err != nil {
				return result, fmt.Errorf("GC ratio=%g run=%d: %w", ratio, run, err)
			}
			gcDuration := time.Since(gcStarted)
			gcBytes, err := treeBytes(cfg.AppRoot)
			if err != nil {
				return result, fmt.Errorf("统计 GC 后空间: %w", err)
			}
			checkpointStarted := time.Now()
			if err := catalogStore.Checkpoint(ctx); err != nil {
				return result, fmt.Errorf("checkpoint ratio=%g run=%d: %w", ratio, run, err)
			}
			checkpointDuration := time.Since(checkpointStarted)
			bytesAfter, err := treeBytes(cfg.AppRoot)
			if err != nil {
				return result, fmt.Errorf("统计样本后空间: %w", err)
			}
			bytesPeak := max(bytesBefore, candidateBytes, publishedBytes, gcBytes, bytesAfter)

			sample := report.PublicationBuildSample{
				Run: run, Revision: revision, BeginMs: durationMS(beginDuration), StageMs: durationMS(stageDuration),
				OverlayMs: durationMS(overlayDuration), ValidationMs: durationMS(validationDuration),
				PublishMs: durationMS(publishDuration), GCMS: durationMS(gcDuration),
				CheckpointMs: durationMS(checkpointDuration), TotalMs: durationMS(time.Since(sampleStarted)),
				BytesBefore: bytesBefore, BytesPeak: bytesPeak, BytesAfter: bytesAfter,
				OldSnapshotReadableAcrossBuild: oldReadable,
				ActiveWorkCount:                shape.works,
				WorkCreatorRelationCount:       shape.workCreatorRelations,
				MediaProjectionCount:           shape.mediaProjections,
				SourceMediaCount:               shape.sourceMedia,
				ContentBlobCount:               shape.contentBlobs,
				FileLocationCount:              shape.fileLocations,
				FTSDocumentCount:               shape.ftsDocuments,
				SearchCandidateCount:           shape.searchCandidates,
				SourceCount:                    shape.sources,
				ChangedProjectionCount:         shape.changed,
			}
			currentRatio := &result.PublicationMatrix.Ratios[ratioIndex]
			currentRatio.Runs = append(currentRatio.Runs, sample)
			currentRatio.CompletedRuns = len(currentRatio.Runs)
			publishDurations = append(publishDurations, publishDuration)
			summary := report.Summarize(report.Measurement{
				Category: fmt.Sprintf("publication/change-%g", ratio), Concurrency: 1,
				PlannedRuns: cfg.SamplesPerRatio, AttemptedRuns: len(publishDurations),
				NotAttemptedRuns: cfg.SamplesPerRatio - len(publishDurations), Durations: publishDurations,
				CacheState: report.CacheStateWarm,
			})
			currentRatio.PublishP50Ms = summary.P50Ms
			currentRatio.PublishP95Ms = summary.P95Ms
			currentRatio.PublishMinMs = summary.MinMs
			currentRatio.PublishMaxMs = summary.MaxMs
			currentRatio.PublishTargetOK = currentRatio.CompletedRuns == currentRatio.PlannedRuns && summary.P95Ms < ReferencePublishP95Milliseconds
			result.Latencies[latencyIndex] = summary
			result.CompletedCombinations++
			if cfg.Checkpoint != nil {
				if err := cfg.Checkpoint(&result); err != nil {
					return result, fmt.Errorf("保存 publication 部分报告: %w", err)
				}
			}
		}
	}

	result.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	result.Add("publication/baseline/complete-shape",
		len(state.identities) == primaryWorks && len(state.overlayFacts) == cfg.Scale,
		fmt.Sprintf("scale=%d sources=%d primary=%d relationsPerWork=2", cfg.Scale, cfg.Sources, primaryWorks))
	for _, ratioResult := range result.PublicationMatrix.Ratios {
		allReadable := true
		for _, sample := range ratioResult.Runs {
			allReadable = allReadable && sample.OldSnapshotReadableAcrossBuild
		}
		result.Add(fmt.Sprintf("publication/change-%g/exact-global-count", ratioResult.ChangeRatio),
			ratioResult.CompletedRuns == ratioResult.PlannedRuns,
			fmt.Sprintf("changed=%d runs=%d/%d", ratioResult.ChangedWorks, ratioResult.CompletedRuns, ratioResult.PlannedRuns))
		result.Add(fmt.Sprintf("publication/change-%g/old-snapshot-readable", ratioResult.ChangeRatio), allReadable,
			fmt.Sprintf("runs=%d", ratioResult.CompletedRuns))
		if cfg.Tier == "reference" {
			result.Add(fmt.Sprintf("publication/change-%g/p95-under-250ms", ratioResult.ChangeRatio), ratioResult.PublishTargetOK,
				fmt.Sprintf("p95Ms=%.3f samples=%d", ratioResult.PublishP95Ms, ratioResult.CompletedRuns))
		}
	}
	if cfg.Tier == "reference" {
		result.Add("environment/reference-fields-complete", facts.Complete(),
			fmt.Sprintf("missing=%s", strings.Join(facts.MissingFields(), ",")))
	}
	if cfg.Checkpoint != nil {
		if err := cfg.Checkpoint(&result); err != nil {
			return result, fmt.Errorf("保存 publication 最终报告: %w", err)
		}
	}
	return result, nil
}

func publicationWorkCount(ctx context.Context, store *storage.Store, publicationID string) int {
	var count int
	err := store.Catalog.SQL().QueryRowContext(ctx, `SELECT count(*)
FROM query_publications q JOIN work_projections w
  ON w.catalog_revision_id=q.catalog_revision_id AND w.overlay_revision_id=q.overlay_revision_id
WHERE q.query_publication_id=?`, publicationID).Scan(&count)
	if err != nil {
		return -1
	}
	return count
}

type publicationShape struct {
	works                int
	workCreatorRelations int
	mediaProjections     int
	sourceMedia          int
	contentBlobs         int
	fileLocations        int
	ftsDocuments         int
	searchCandidates     int
	sources              int
	changed              int
}

func activeProjectionCounts(ctx context.Context, store *storage.Store, publication catalog.Publication, revision int) (publicationShape, error) {
	marker := fmt.Sprintf("publication-change-%04d", revision)
	var shape publicationShape
	err := store.Catalog.SQL().QueryRowContext(ctx, `SELECT
  (SELECT count(*) FROM work_projections WHERE catalog_revision_id=? AND overlay_revision_id=?),
  (SELECT count(*) FROM work_creator_relations WHERE catalog_revision_id=? AND overlay_revision_id=?),
  (SELECT count(*) FROM media_projections WHERE catalog_revision_id=? AND overlay_revision_id=?),
  (SELECT count(*) FROM source_media WHERE catalog_revision_id=?),
  (SELECT count(*) FROM content_blobs WHERE catalog_revision_id=?),
  (SELECT count(*) FROM file_locations WHERE catalog_revision_id=?),
  (SELECT count(*) FROM work_search WHERE catalog_revision_id=? AND overlay_revision_id=?),
  (SELECT count(*) FROM work_search_candidates WHERE catalog_revision_id=? AND overlay_revision_id=?),
  (SELECT count(*) FROM catalog_revision_sources WHERE catalog_revision_id=?),
  (SELECT count(*) FROM work_projections WHERE catalog_revision_id=? AND overlay_revision_id=? AND instr(title, ?)>0)`,
		publication.CatalogRevisionID, publication.OverlayRevisionID,
		publication.CatalogRevisionID, publication.OverlayRevisionID,
		publication.CatalogRevisionID, publication.OverlayRevisionID,
		publication.CatalogRevisionID,
		publication.CatalogRevisionID,
		publication.CatalogRevisionID,
		publication.CatalogRevisionID, publication.OverlayRevisionID,
		publication.CatalogRevisionID, publication.OverlayRevisionID,
		publication.CatalogRevisionID,
		publication.CatalogRevisionID, publication.OverlayRevisionID, marker,
	).Scan(&shape.works, &shape.workCreatorRelations, &shape.mediaProjections, &shape.sourceMedia,
		&shape.contentBlobs, &shape.fileLocations, &shape.ftsDocuments, &shape.searchCandidates,
		&shape.sources, &shape.changed)
	return shape, err
}
