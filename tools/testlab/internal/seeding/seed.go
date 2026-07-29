// Package seeding 通过生产 control/catalog 服务构建 testlab 的确定性合成
// publication。命令行 seed 与阶段 4 自动化 smoke 必须共用本包；自动化 smoke
// 可显式要求同步建立 control.db Library/Source 事实，高规模 catalog-only seed 则
// 保持原有低开销行为。
package seeding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/ports"
	"github.com/RecRivenVI/gallery/internal/storage"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/corpus"
)

// SourcesEnvVar 是 Source 数量的兼容环境变量入口。正式 CLI 已提供 -sources；保留本
// 入口只为旧脚本兼容，解析结果仍会打印到 stderr 并写入 Manifest。
const SourcesEnvVar = "GALLERY_TESTLAB_SEED_SOURCES"

// Config 是构建一次合成 Catalog publication 所需的全部参数。SourceRoot 非空时
// 通过 application.Resources 同步建立 control.db Library/Source；为空时保持
// testlabseed 原有的 catalog-only 高规模构建行为。
type Config struct {
	AppRoot    string
	SourceRoot string
	Scale      int
	BatchSize  int

	// Sources 是把语料分布到的 Source 数量，0 时先查兼容 SourcesEnvVar，仍未指定则为 1；负数无效。
	// 每个 Source 各自走一遍完整的 BeginCandidate/Stage/Overlay/Validate/Publish 周期，
	// 因此从第二个 Source 开始，BeginCandidate 会真正执行 cloneUnchangedSources 把此前
	// 全部 Source 的投影（含 FTS5 索引）搬进新 revision；最后一个 Source 搬运的比例是
	// (N-1)/N，与"重扫其中一个 Source"这一生产形态的最重情形一致。
	Sources int
}

type sourceIndexProvider func(slot, scale, sources int) []int

type stagedIdentity struct {
	workID  string
	mediaID string
}

type mutationProfile struct {
	revision     int
	changedWorks int
}

type baselineOptions struct {
	sourceIndices sourceIndexProvider
	captureSource int
}

type baselineState struct {
	libraryID    string
	sourceIDs    []string
	creatorIDs   []string
	overlayFacts map[string]catalog.OverlayFact
	identities   map[int]stagedIdentity
}

// resolveSources 按 Config.Sources → 环境变量 → 默认 1 的顺序确定 Source 数量。
func resolveSources(configured int) (int, error) {
	if configured > 0 {
		return configured, nil
	}
	if configured < 0 {
		return 0, fmt.Errorf("Source 数量必须为非负整数: %d", configured)
	}
	raw := strings.TrimSpace(os.Getenv(SourcesEnvVar))
	if raw == "" {
		return 1, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s=%q 不是正整数", SourcesEnvVar, raw)
	}
	fmt.Fprintf(os.Stderr, "testlabseed: 按 %s 把语料分布到 %d 个 Source\n", SourcesEnvVar, parsed)
	return parsed, nil
}

func mustNewID(ids ports.IDGenerator, kind domain.IDKind) (string, error) {
	id, err := ids.New(kind)
	if err != nil {
		return "", fmt.Errorf("generate %s id: %w", kind, err)
	}
	return id.String(), nil
}

func mimeForKind(kind string) string {
	if kind == "video" {
		return "video/mp4"
	}
	return "image/jpeg"
}

// Run 打开（或创建）cfg.AppRoot 下的 AppDirs，通过生产 application.Resources 与
// catalog.Store 序列构建一组彼此一致的 control/Catalog 事实和合法
// query_publication_id，返回可写入 manifest 的完整结果。批处理边界
// （BatchSize）故意与创作者槽位周期（corpus.CreatorCount）不对齐测试，用于验证
// 跨批次 Creator 去重不依赖批大小与周期数的关系。
//
// Overlay facts 不能按批次分别调用 ApplyCatalogCandidateOverlays：该生产函数对
// 整个 revision 重新查询全部 baseWork 并对每一行套用 facts[workID]（缺失时套用
// 零值），也就是说它本身就是“整个 revision 一次性全量处理”的语义，分批调用会
// 把尚未到达的批次静默置零、也会把已处理批次的 Hidden/Favorite/Progress 在下一次
// 调用时覆盖回默认值。因此这里仍在内存中累积完整 facts map、只调用一次；500k
// 规模下该 map 只有数十 MB。若未来需要真正分批 Overlay 应用，必须先扩展生产
// ApplyCatalogCandidateOverlays 的语义，不由测试工具伪装实现。
func Run(ctx context.Context, cfg Config) (corpus.Manifest, error) {
	manifest, _, err := runBaseline(ctx, cfg, baselineOptions{captureSource: -1})
	return manifest, err
}

func runBaseline(ctx context.Context, cfg Config, options baselineOptions) (corpus.Manifest, baselineState, error) {
	if cfg.AppRoot == "" {
		return corpus.Manifest{}, baselineState{}, fmt.Errorf("AppRoot 不能为空")
	}
	if cfg.Scale <= 0 {
		return corpus.Manifest{}, baselineState{}, fmt.Errorf("Scale 必须为正整数")
	}
	sources, err := resolveSources(cfg.Sources)
	if err != nil {
		return corpus.Manifest{}, baselineState{}, err
	}
	if sources > cfg.Scale {
		// 空 Source 的候选会被 ValidateCandidate 以 workCount==0 拒绝；提前给出可读
		// 原因，而不是让调用方拿到一个不透明的 CATALOG_CANDIDATE_INVALID。
		return corpus.Manifest{}, baselineState{}, fmt.Errorf("Source 数量 %d 超过语料规模 %d：会产生没有任何作品的 Source", sources, cfg.Scale)
	}
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 20000
	}

	dirs := appdirs.UnderRoot(cfg.AppRoot)
	fileSystem := filesystem.OS{}
	if err := dirs.Ensure(fileSystem); err != nil {
		return corpus.Manifest{}, baselineState{}, fmt.Errorf("ensure appdirs: %w", err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		return corpus.Manifest{}, baselineState{}, fmt.Errorf("open storage: %w", err)
	}
	defer store.Close()

	systemClock := clock.System{}
	ids := identity.NewGenerator(systemClock)
	libraryID, sourceIDs, err := createOwners(ctx, store, dirs, fileSystem, systemClock, ids, cfg.SourceRoot, sources)
	if err != nil {
		return corpus.Manifest{}, baselineState{}, err
	}

	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), systemClock, ids)
	if err != nil {
		return corpus.Manifest{}, baselineState{}, fmt.Errorf("new catalog store: %w", err)
	}

	creatorIDs := make([]string, corpus.CreatorCount)
	for slot := range creatorIDs {
		creatorIDs[slot], err = mustNewID(ids, domain.IDCanonicalCreator)
		if err != nil {
			return corpus.Manifest{}, baselineState{}, err
		}
	}

	n := cfg.Scale
	started := time.Now()
	// Overlay 事实必须**跨 Source 累积**：ApplyCatalogCandidateOverlays 对整个 revision
	// 的全部 baseWork 逐行套用 facts[workID]，缺失时套用零值。第二个 Source 的候选里已经
	// 包含 cloneUnchangedSources 搬进来的前序 Source 作品，只传本 Source 的事实会把它们的
	// Hidden/Favorite/Progress 全部抹回默认值。
	overlayFacts := make(map[string]catalog.OverlayFact, n)
	identities := make(map[int]stagedIdentity)
	firstJobID := ""
	var stageTotal, overlayTotal, validationTotal, publishTotal time.Duration
	beginDurations := make([]int64, 0, sources)
	validationDurations := make([]int64, 0, sources)
	publishDurations := make([]int64, 0, sources)
	visibleCounts := make([]int, 0, sources)
	var publication catalog.Publication

	for slot := 0; slot < sources; slot++ {
		jobID, err := mustNewID(ids, domain.IDJob)
		if err != nil {
			return corpus.Manifest{}, baselineState{}, err
		}
		if slot == 0 {
			firstJobID = jobID
		}

		// BeginCandidate 是 cloneUnchangedSources 的唯一入口：从第二个 Source 起，这一步
		// 会把此前全部 Source 的 source_*/projections/FTS5/blob/location 行全量搬进新
		// revision。单独计时使这条路径的代价在 manifest 里可见。
		beginStarted := time.Now()
		candidate, err := catalogStore.BeginCandidate(ctx, jobID, sourceIDs[slot], 0)
		if err != nil {
			return corpus.Manifest{}, baselineState{}, fmt.Errorf("begin candidate (source %d/%d): %w", slot+1, sources, err)
		}
		beginDurations = append(beginDurations, time.Since(beginStarted).Milliseconds())

		stageStarted := time.Now()
		if err := stageSourceWorks(ctx, catalogStore, candidate, stageParams{
			libraryID: libraryID, sourceID: sourceIDs[slot], slot: slot, sources: sources,
			scale: n, batchSize: batchSize, ids: ids, creatorIDs: creatorIDs, overlayFacts: overlayFacts,
			sourceIndices: options.sourceIndices, identities: identities, captureIdentities: slot == options.captureSource,
		}); err != nil {
			return corpus.Manifest{}, baselineState{}, err
		}
		stageTotal += time.Since(stageStarted)
		visibleCounts = append(visibleCounts, visibleCountForSource(n, sources, slot, options.sourceIndices))

		overlayStarted := time.Now()
		if err := catalogStore.ApplyCatalogCandidateOverlays(ctx, candidate, overlayFacts); err != nil {
			return corpus.Manifest{}, baselineState{}, fmt.Errorf("apply overlay facts (source %d/%d): %w", slot+1, sources, err)
		}
		overlayTotal += time.Since(overlayStarted)

		validationStarted := time.Now()
		if err := catalogStore.ValidateCandidate(ctx, candidate); err != nil {
			return corpus.Manifest{}, baselineState{}, fmt.Errorf("validate candidate (source %d/%d): %w", slot+1, sources, err)
		}
		validationElapsed := time.Since(validationStarted)
		validationTotal += validationElapsed
		validationDurations = append(validationDurations, validationElapsed.Milliseconds())
		publishStarted := time.Now()
		publication, err = catalogStore.Publish(ctx, candidate)
		if err != nil {
			return corpus.Manifest{}, baselineState{}, fmt.Errorf("publish candidate (source %d/%d): %w", slot+1, sources, err)
		}
		publishElapsed := time.Since(publishStarted)
		publishTotal += publishElapsed
		publishDurations = append(publishDurations, publishElapsed.Milliseconds())
	}

	if err := catalogStore.MarkQueryDependencyBackfillTriggered(ctx); err != nil {
		return corpus.Manifest{}, baselineState{}, fmt.Errorf("mark query dependency backfill: %w", err)
	}

	manifest := corpus.Manifest{
		SchemaVersion: 2, Scale: n, LibraryID: libraryID, SourceID: sourceIDs[0], JobID: firstJobID,
		CreatorIDs: creatorIDs, QueryPublicationID: publication.ID, CatalogRevisionID: publication.CatalogRevisionID,
		StageDurationMs: stageTotal.Milliseconds(), OverlayDurationMs: overlayTotal.Milliseconds(),
		ValidationDurationMs: validationTotal.Milliseconds(),
		PublishDurationMs:    publishTotal.Milliseconds(), TotalDurationMs: time.Since(started).Milliseconds(),
		Stats:                       corpus.ComputeStats(n),
		Sources:                     sources,
		SourceIDs:                   sourceIDs,
		SourceVisibleWorkCounts:     visibleCounts,
		SourceBeginDurationsMs:      beginDurations,
		SourceValidationDurationsMs: validationDurations,
		SourcePublishDurationsMs:    publishDurations,
	}
	return manifest, baselineState{
		libraryID: libraryID, sourceIDs: sourceIDs, creatorIDs: creatorIDs,
		overlayFacts: overlayFacts, identities: identities,
	}, nil
}

func visibleCountForSource(scale, sources, slot int, provider sourceIndexProvider) int {
	count := 0
	for _, index := range indicesForSource(slot, scale, sources, provider) {
		if !corpus.Hidden(index) {
			count++
		}
	}
	return count
}

func indicesForSource(slot, scale, sources int, provider sourceIndexProvider) []int {
	if provider != nil {
		return provider(slot, scale, sources)
	}
	indices := make([]int, 0, scale/sources+1)
	for index := slot; index < scale; index += sources {
		indices = append(indices, index)
	}
	return indices
}

// createOwners 建立本次语料的 Library 与全部 Source。SourceRoot 非空时走生产
// application.Resources（因此 Source 根之间、Source 与 AppDirs 之间的不相交校验都真实
// 生效），每个 Source 使用 SourceRoot 下自己的子目录；为空时保持 catalog-only 的高规模
// 构建行为，只生成 ID 不建立 control 事实。
func createOwners(ctx context.Context, store *storage.Store, dirs appdirs.Dirs, fileSystem filesystem.OS,
	systemClock clock.System, ids ports.IDGenerator, sourceRoot string, sources int) (string, []string, error) {
	sourceIDs := make([]string, 0, sources)
	if sourceRoot == "" {
		libraryID, err := mustNewID(ids, domain.IDLibrary)
		if err != nil {
			return "", nil, err
		}
		for slot := 0; slot < sources; slot++ {
			sourceID, err := mustNewID(ids, domain.IDSource)
			if err != nil {
				return "", nil, err
			}
			sourceIDs = append(sourceIDs, sourceID)
		}
		return libraryID, sourceIDs, nil
	}

	if err := os.MkdirAll(filepath.Clean(sourceRoot), 0o700); err != nil {
		return "", nil, fmt.Errorf("create synthetic source root: %w", err)
	}
	resources, err := application.NewResources(store.Control.SQL(), dirs, fileSystem, systemClock, ids)
	if err != nil {
		return "", nil, fmt.Errorf("new resources: %w", err)
	}
	library, err := resources.CreateLibrary(ctx, "Testlab Synthetic Library")
	if err != nil {
		return "", nil, fmt.Errorf("create synthetic library: %w", err)
	}
	for slot := 0; slot < sources; slot++ {
		root := sourceRoot
		if sources > 1 {
			// 多 Source 时每个 Source 必须有互不包含的根：CreateSource 会对全部已登记
			// 根加上 AppDirs 做不相交校验，复用同一个根会被生产路径直接拒绝。
			root = filepath.Join(sourceRoot, fmt.Sprintf("source-%02d", slot))
			if err := os.MkdirAll(root, 0o700); err != nil {
				return "", nil, fmt.Errorf("create synthetic source root %d: %w", slot, err)
			}
		}
		source, err := resources.CreateSource(ctx, library.ID, fmt.Sprintf("Testlab Synthetic Source %02d", slot), root)
		if err != nil {
			return "", nil, fmt.Errorf("create synthetic source %d: %w", slot, err)
		}
		sourceIDs = append(sourceIDs, source.ID)
	}
	return library.ID, sourceIDs, nil
}

// stageParams 是 stageSourceWorks 的输入。
type stageParams struct {
	libraryID         string
	sourceID          string
	slot              int
	sources           int
	scale             int
	batchSize         int
	ids               ports.IDGenerator
	creatorIDs        []string
	overlayFacts      map[string]catalog.OverlayFact
	sourceIndices     sourceIndexProvider
	identities        map[int]stagedIdentity
	captureIdentities bool
	requireIdentities bool
	mutation          mutationProfile
}

// stageSourceWorks 分批写入属于第 slot 个 Source 的全部作品与媒体。
//
// 批处理边界（batchSize）故意与创作者槽位周期（corpus.CreatorCount）不对齐测试，用于
// 验证跨批次 Creator 去重不依赖批大小与周期数的关系。普通 seed 使用
// corpus.SourceIndex 取模交错；publication 变化矩阵显式传入加权分配，使单次
// 生产 Source publication 能覆盖全库 1%/10%/50% 变化。
func stageSourceWorks(ctx context.Context, catalogStore *catalog.Store, candidate catalog.Candidate, p stageParams) error {
	indices := indicesForSource(p.slot, p.scale, p.sources, p.sourceIndices)
	for start := 0; start < len(indices); start += p.batchSize {
		end := start + p.batchSize
		if end > len(indices) {
			end = len(indices)
		}
		works := make([]catalog.WorkFact, 0, end-start)
		media := make([]catalog.MediaFact, 0, end-start)
		for _, i := range indices[start:end] {
			identity, exists := p.identities[i]
			if !exists {
				if p.requireIdentities {
					return fmt.Errorf("Source %d 的稳定身份缺失: index=%d", p.slot, i)
				}
				workID, err := mustNewID(p.ids, domain.IDCanonicalWork)
				if err != nil {
					return err
				}
				mediaID, err := mustNewID(p.ids, domain.IDCanonicalMedia)
				if err != nil {
					return err
				}
				identity = stagedIdentity{workID: workID, mediaID: mediaID}
				if p.captureIdentities {
					p.identities[i] = identity
				}
			}
			changed := p.mutation.revision > 0 && i < p.mutation.changedWorks
			title := corpus.Title(i)
			if changed {
				title = fmt.Sprintf("%s · publication-change-%04d", title, p.mutation.revision)
			}
			creatorSlot := corpus.CreatorIndex(i)
			tagA, tagB := corpus.TagSlots(i)
			works = append(works, catalog.WorkFact{
				SourceID: p.sourceID, LibraryID: p.libraryID, SourceKey: corpus.SourceKey(i),
				ProviderID: corpus.ProviderID(corpus.ProviderIndex(i)),
				Title:      title,
				Creator:    corpus.CreatorName(creatorSlot), CreatorID: p.creatorIDs[creatorSlot],
				// source_creators 是逐条 Source-derived 事实，唯一键为
				// (catalog_revision_id, source_id, source_key)；多个作品共享同一个
				// creator_id，但每个作品各自的 occurrence 必须有独立 source_key。
				CreatorSourceKey: fmt.Sprintf("creator-occurrence/%08d", i),
				Tags:             []string{corpus.TagName(tagA), corpus.TagName(tagB)},
				Filenames:        []string{corpus.Filename(i)},
				WorkID:           identity.workID,
			})

			verified := corpus.ContentVerified(i)
			state := catalog.ContentVerificationStateLocatedUnverified
			var algorithm, digest, locationKey string
			var size int64
			if verified {
				state = catalog.ContentVerificationStateContentVerified
				blobRevision := 0
				if changed {
					blobRevision = p.mutation.revision
				}
				blobSeed := fmt.Sprintf("testlab-synthetic-blob-%d", i)
				if blobRevision > 0 {
					blobSeed = fmt.Sprintf("%s-r%d", blobSeed, blobRevision)
				}
				sum := sha256.Sum256([]byte(blobSeed))
				algorithm = "sha256-v1"
				digest = hex.EncodeToString(sum[:])
				locationKey = corpus.SourceKey(i)
				size = int64(4096 + i%65536)
			}
			mediaFact := catalog.MediaFact{
				SourceID: p.sourceID, SourceKey: fmt.Sprintf("%s/media", corpus.SourceKey(i)),
				WorkSourceKey: corpus.SourceKey(i),
				RelativePath:  fmt.Sprintf("testlab/work-%08d/%s", i, corpus.Filename(i)),
				Kind:          corpus.MediaKind(i), MIME: mimeForKind(corpus.MediaKind(i)), Size: size,
				Algorithm: algorithm, Digest: digest, LocationKey: locationKey,
				MediaID: identity.mediaID, WorkID: identity.workID, Ordinal: 0,
				ContentVerificationState: state,
				MTimeNanos:               int64(1_700_000_000_000_000_000 + int64(i)*1000),
			}
			if changed {
				mediaFact.MTimeNanos += int64(p.mutation.revision)
			}
			if verified {
				mediaFact.LastConfirmedAlgorithm = algorithm
				mediaFact.LastConfirmedDigest = digest
				mediaFact.LastConfirmedAt = time.Unix(1_700_000_000, 0).UTC()
				if changed {
					mediaFact.LastConfirmedAt = mediaFact.LastConfirmedAt.Add(time.Duration(p.mutation.revision) * time.Second)
				}
			}
			media = append(media, mediaFact)

			p.overlayFacts[identity.workID] = catalog.OverlayFact{
				Hidden: corpus.Hidden(i), Favorite: corpus.Favorite(i), Progress: corpus.Progress(i),
			}
		}
		if err := catalogStore.Stage(ctx, candidate, works, media); err != nil {
			return fmt.Errorf("stage source %d batch [%d,%d): %w", p.slot, start, end, err)
		}
	}
	return nil
}
