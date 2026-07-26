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

// Config 是构建一次合成 Catalog publication 所需的全部参数。SourceRoot 非空时
// 通过 application.Resources 同步建立 control.db Library/Source；为空时保持
// testlabseed 原有的 catalog-only 高规模构建行为。
type Config struct {
	AppRoot    string
	SourceRoot string
	Scale      int
	BatchSize  int
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
	fileSystem := filesystem.OS{}
	if err := dirs.Ensure(fileSystem); err != nil {
		return corpus.Manifest{}, fmt.Errorf("ensure appdirs: %w", err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		return corpus.Manifest{}, fmt.Errorf("open storage: %w", err)
	}
	defer store.Close()

	systemClock := clock.System{}
	ids := identity.NewGenerator(systemClock)
	var libraryID, sourceID string
	if cfg.SourceRoot != "" {
		if err := os.MkdirAll(filepath.Clean(cfg.SourceRoot), 0o700); err != nil {
			return corpus.Manifest{}, fmt.Errorf("create synthetic source root: %w", err)
		}
		resources, err := application.NewResources(store.Control.SQL(), dirs, fileSystem, systemClock, ids)
		if err != nil {
			return corpus.Manifest{}, fmt.Errorf("new resources: %w", err)
		}
		library, err := resources.CreateLibrary(ctx, "Testlab Synthetic Library")
		if err != nil {
			return corpus.Manifest{}, fmt.Errorf("create synthetic library: %w", err)
		}
		source, err := resources.CreateSource(ctx, library.ID, "Testlab Synthetic Source", cfg.SourceRoot)
		if err != nil {
			return corpus.Manifest{}, fmt.Errorf("create synthetic source: %w", err)
		}
		libraryID, sourceID = library.ID, source.ID
	} else {
		libraryID, err = mustNewID(ids, domain.IDLibrary)
		if err != nil {
			return corpus.Manifest{}, err
		}
		sourceID, err = mustNewID(ids, domain.IDSource)
		if err != nil {
			return corpus.Manifest{}, err
		}
	}

	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), systemClock, ids)
	if err != nil {
		return corpus.Manifest{}, fmt.Errorf("new catalog store: %w", err)
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
				// creator_id，但每个作品各自的 occurrence 必须有独立 source_key。
				CreatorSourceKey: fmt.Sprintf("creator-occurrence/%08d", i),
				Tags:             []string{corpus.TagName(tagA), corpus.TagName(tagB)},
				Filenames:        []string{corpus.Filename(i)},
				WorkID:           workID,
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
