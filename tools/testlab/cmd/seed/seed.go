// 本文件包含 testlabseed 的可测试核心逻辑；main.go 只负责命令行参数解析。
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

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

// seedConfig 是构建一次合成 Catalog publication 所需的全部参数。
type seedConfig struct {
	AppRoot   string
	Scale     int
	BatchSize int
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

// runSeed 打开（或创建）cfg.AppRoot 下的 AppDirs，通过生产 internal/catalog.Store
// 序列构建并发布一个合法的 Catalog query_publication_id 快照，返回可写入 manifest
// 的完整结果。批处理边界（BatchSize）故意与创作者槽位周期（corpus.CreatorCount）
// 不对齐测试，用于在单元测试中验证跨批次 Creator 去重不依赖批大小与周期数的关系。
//
// Overlay facts 不能按批次分别调用 ApplyCatalogCandidateOverlays：该生产函数对
// 整个 revision 重新查询全部 baseWork 并对每一行套用 facts[workID]（缺失时套用
// 零值），也就是说它本身就是"整个 revision 一次性全量处理"的语义，分批调用会
// 把尚未到达的批次静默置零、也会把已处理批次的 Hidden/Favorite/Progress 在下一次
// 调用时被覆盖回默认值。这正是 checkpoint 诊断中记录的 10M 构建瓶颈根因
// （PERFORMANCE_FINDING，本轮不改动生产代码）；因此这里仍然在内存中累积完整的
// facts map、只调用一次，不假装分批调用是安全的。500k 规模下该 map 只有数十 MB
// （每条目 ~30 字节 workID + 24 字节结构体 + map 开销），不构成实际内存压力；
// 若未来需要真正分批 Overlay 应用，必须先扩展 ApplyCatalogCandidateOverlays 本身
// 支持增量语义，属于生产代码变更，不在本轮测试框架职责范围内。
func runSeed(ctx context.Context, cfg seedConfig) (corpus.Manifest, error) {
	if cfg.AppRoot == "" {
		return corpus.Manifest{}, fmt.Errorf("AppRoot 不能为空")
	}
	if cfg.Scale <= 0 {
		return corpus.Manifest{}, fmt.Errorf("Scale 必须为正整数")
	}
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 20000
	}

	dirs := appdirs.UnderRoot(cfg.AppRoot)
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		return corpus.Manifest{}, fmt.Errorf("ensure appdirs: %w", err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		return corpus.Manifest{}, fmt.Errorf("open storage: %w", err)
	}
	defer store.Close()

	systemClock := clock.System{}
	ids := identity.NewGenerator(systemClock)
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), systemClock, ids)
	if err != nil {
		return corpus.Manifest{}, fmt.Errorf("new catalog store: %w", err)
	}

	libraryID, err := mustNewID(ids, domain.IDLibrary)
	if err != nil {
		return corpus.Manifest{}, err
	}
	sourceID, err := mustNewID(ids, domain.IDSource)
	if err != nil {
		return corpus.Manifest{}, err
	}
	jobID, err := mustNewID(ids, domain.IDJob)
	if err != nil {
		return corpus.Manifest{}, err
	}

	creatorIDs := make([]string, corpus.CreatorCount)
	for slot := range creatorIDs {
		creatorIDs[slot], err = mustNewID(ids, domain.IDCanonicalCreator)
		if err != nil {
			return corpus.Manifest{}, err
		}
	}

	started := time.Now()
	candidate, err := catalogStore.BeginCandidate(ctx, jobID, sourceID, 0)
	if err != nil {
		return corpus.Manifest{}, fmt.Errorf("begin candidate: %w", err)
	}

	n := cfg.Scale
	overlayFacts := make(map[string]catalog.OverlayFact, n)
	for start := 0; start < n; start += batchSize {
		end := start + batchSize
		if end > n {
			end = n
		}
		works := make([]catalog.WorkFact, 0, end-start)
		media := make([]catalog.MediaFact, 0, end-start)
		for i := start; i < end; i++ {
			workID, err := mustNewID(ids, domain.IDCanonicalWork)
			if err != nil {
				return corpus.Manifest{}, err
			}
			mediaID, err := mustNewID(ids, domain.IDCanonicalMedia)
			if err != nil {
				return corpus.Manifest{}, err
			}
			creatorSlot := corpus.CreatorIndex(i)
			tagA, tagB := corpus.TagSlots(i)
			work := catalog.WorkFact{
				SourceID: sourceID, LibraryID: libraryID, SourceKey: corpus.SourceKey(i),
				ProviderID: corpus.ProviderID(corpus.ProviderIndex(i)),
				Title:      corpus.Title(i),
				Creator:    corpus.CreatorName(creatorSlot), CreatorID: creatorIDs[creatorSlot],
				// source_creators 是逐条 Source-derived 事实，唯一键为
				// (catalog_revision_id, source_id, source_key)；多个作品共享同一个
				// creator_id（去重发生在 creator_projections 的 INSERT OR IGNORE），
				// 但每个作品各自的 source_creators occurrence 必须有独立 source_key，
				// 不依赖 batch 边界或 i 与创作者槽位周期的关系——见 seed_test.go 的
				// TestRunSeedDedupesCreatorsAcrossBatchBoundaries。
				CreatorSourceKey: fmt.Sprintf("creator-occurrence/%08d", i),
				Tags:             []string{corpus.TagName(tagA), corpus.TagName(tagB)},
				Filenames:        []string{corpus.Filename(i)},
				WorkID:           workID,
				// Hidden 不在这里设置：它是 Overlay 权威字段，ApplyCatalogCandidateOverlays
				// 会在 Stage 之后对本 revision 每个作品重新计算 hidden/favorite/progress
				// 并整体覆盖，只信任下面的 overlayFacts。
			}
			works = append(works, work)

			verified := corpus.ContentVerified(i)
			state := catalog.ContentVerificationStateLocatedUnverified
			var algorithm, digest, locationKey string
			var size int64
			if verified {
				state = catalog.ContentVerificationStateContentVerified
				sum := sha256.Sum256([]byte(fmt.Sprintf("testlab-synthetic-blob-%d", i)))
				algorithm = "sha256-v1"
				digest = hex.EncodeToString(sum[:])
				locationKey = corpus.SourceKey(i)
				size = int64(4096 + i%65536)
			}
			mediaFact := catalog.MediaFact{
				SourceID: sourceID, SourceKey: fmt.Sprintf("%s/media", corpus.SourceKey(i)),
				WorkSourceKey: corpus.SourceKey(i),
				RelativePath:  fmt.Sprintf("testlab/work-%08d/%s", i, corpus.Filename(i)),
				Kind:          corpus.MediaKind(i), MIME: mimeForKind(corpus.MediaKind(i)), Size: size,
				Algorithm: algorithm, Digest: digest, LocationKey: locationKey,
				MediaID: mediaID, WorkID: workID, Ordinal: 0,
				ContentVerificationState: state,
				MTimeNanos:               int64(1_700_000_000_000_000_000 + int64(i)*1000),
			}
			if verified {
				mediaFact.LastConfirmedAlgorithm = algorithm
				mediaFact.LastConfirmedDigest = digest
				mediaFact.LastConfirmedAt = time.Unix(1_700_000_000, 0).UTC()
			}
			media = append(media, mediaFact)

			overlayFacts[workID] = catalog.OverlayFact{
				Hidden: corpus.Hidden(i), Favorite: corpus.Favorite(i), Progress: corpus.Progress(i),
			}
		}
		if err := catalogStore.Stage(ctx, candidate, works, media); err != nil {
			return corpus.Manifest{}, fmt.Errorf("stage batch [%d,%d): %w", start, end, err)
		}
	}
	stageDuration := time.Since(started)

	overlayStarted := time.Now()
	if err := catalogStore.ApplyCatalogCandidateOverlays(ctx, candidate, overlayFacts); err != nil {
		return corpus.Manifest{}, fmt.Errorf("apply overlay facts: %w", err)
	}
	overlayDuration := time.Since(overlayStarted)

	if err := catalogStore.ValidateCandidate(ctx, candidate); err != nil {
		return corpus.Manifest{}, fmt.Errorf("validate candidate: %w", err)
	}
	publishStarted := time.Now()
	publication, err := catalogStore.Publish(ctx, candidate)
	if err != nil {
		return corpus.Manifest{}, fmt.Errorf("publish candidate: %w", err)
	}
	publishDuration := time.Since(publishStarted)

	if err := catalogStore.MarkQueryDependencyBackfillTriggered(ctx); err != nil {
		return corpus.Manifest{}, fmt.Errorf("mark query dependency backfill: %w", err)
	}

	return corpus.Manifest{
		SchemaVersion: 2, Scale: n, LibraryID: libraryID, SourceID: sourceID, JobID: jobID,
		CreatorIDs: creatorIDs, QueryPublicationID: publication.ID, CatalogRevisionID: publication.CatalogRevisionID,
		StageDurationMs: stageDuration.Milliseconds(), OverlayDurationMs: overlayDuration.Milliseconds(),
		PublishDurationMs: publishDuration.Milliseconds(), TotalDurationMs: time.Since(started).Milliseconds(),
		Stats: corpus.ComputeStats(n),
	}, nil
}
