package catalog_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/storage"
)

// newCandidateTestStore 搭建一个干净的 catalog.db，供本文件内的 Candidate 所有权语义测试
// 复用；每个测试使用独立的 t.TempDir()，互不干扰。
func newCandidateTestStore(t *testing.T) (*catalog.Store, *storage.Store) {
	t.Helper()
	ctx := context.Background()
	fixed := clock.Fixed{Time: time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)}
	dirs := appdirs.UnderRoot(t.TempDir())
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), fixed, identity.NewGenerator(fixed))
	if err != nil {
		t.Fatal(err)
	}
	return catalogStore, store
}

const (
	candidateDigestA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	candidateDigestB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// minimalCandidateFacts 构造一个能通过 ValidateCandidate 的最小合法候选：一个 Work、一个
// 已确认媒体，摘要使用固定的合法 sha256-v1 格式占位值。
func minimalCandidateFacts(sourceID, workID, mediaID, digest string) ([]catalog.WorkFact, []catalog.MediaFact) {
	works := []catalog.WorkFact{{
		SourceID: sourceID, LibraryID: "lib-" + sourceID, SourceKey: "work-one",
		SourceTitle: "work-one", Title: "work-one", WorkID: workID,
	}}
	mediaFacts := []catalog.MediaFact{{
		SourceID: sourceID, SourceKey: "work-one/media.bin", WorkSourceKey: "work-one",
		RuleKey: "media.bin", RelativePath: "work-one/media.bin", Kind: "image", MIME: "application/octet-stream",
		Size: 1, Algorithm: "sha256-v1", Digest: digest, LocationKey: "loc-" + mediaID,
		MediaID: mediaID, WorkID: workID, Ordinal: 0,
	}}
	return works, mediaFacts
}

// stageValidCandidate 建立、Stage 并（可选）Validate 一个候选，返回 Candidate 供调用方
// 继续推进到 Publish 或直接留在 staging/validated 阶段模拟中断。
func stageValidCandidate(t *testing.T, catalogStore *catalog.Store, jobID, sourceID, workID, mediaID, digest string, validate bool) catalog.Candidate {
	t.Helper()
	ctx := context.Background()
	candidate, err := catalogStore.BeginCandidate(ctx, jobID, sourceID, 1)
	if err != nil {
		t.Fatalf("BeginCandidate(%s) 失败: %v", jobID, err)
	}
	works, mediaFacts := minimalCandidateFacts(sourceID, workID, mediaID, digest)
	if err := catalogStore.Stage(ctx, candidate, works, mediaFacts); err != nil {
		t.Fatalf("Stage(%s) 失败: %v", jobID, err)
	}
	if validate {
		if err := catalogStore.ValidateCandidate(ctx, candidate); err != nil {
			t.Fatalf("ValidateCandidate(%s) 失败: %v", jobID, err)
		}
	}
	return candidate
}

func publishCandidate(t *testing.T, catalogStore *catalog.Store, candidate catalog.Candidate) catalog.Publication {
	t.Helper()
	publication, err := catalogStore.Publish(context.Background(), candidate)
	if err != nil {
		t.Fatalf("Publish 失败: %v", err)
	}
	return publication
}

func countRows(t *testing.T, store *storage.Store, query string, args ...any) int {
	t.Helper()
	var count int
	if err := store.Catalog.SQL().QueryRowContext(context.Background(), query, args...).Scan(&count); err != nil {
		t.Fatalf("统计查询失败 query=%s: %v", query, err)
	}
	return count
}

func TestStagePersistsMultipleWorkCreatorRelations(t *testing.T) {
	catalogStore, store := newCandidateTestStore(t)
	ctx := context.Background()
	candidate, err := catalogStore.BeginCandidate(ctx, "job-multi-creator", "source-a", 1)
	if err != nil {
		t.Fatal(err)
	}
	works, mediaFacts := minimalCandidateFacts("source-a", "work-1", "media-1", candidateDigestA)
	works[0].Creator = "主作者"
	works[0].CreatorID = "creator-primary"
	works[0].CreatorSourceKey = "work-one/creator:primary:0"
	works[0].CreatorRelations = []catalog.WorkCreatorFact{{
		CreatorID: "creator-assistant", CreatorName: "协作者",
		CreatorSourceKey: "work-one/creator:assistant:0", Role: "assistant", Ordinal: 0,
	}}
	if err := catalogStore.Stage(ctx, candidate, works, mediaFacts); err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ValidateCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, store, `SELECT count(*) FROM work_creator_relations
WHERE catalog_revision_id=? AND overlay_revision_id=? AND work_id=?`,
		candidate.CatalogRevisionID, candidate.OverlayRevisionID, "work-1"); got != 2 {
		t.Fatalf("WorkCreator 关系数=%d, want 2", got)
	}
	if got := countRows(t, store, `SELECT count(*) FROM work_creator_relations
WHERE catalog_revision_id=? AND overlay_revision_id=? AND work_id=? AND role='assistant' AND ordinal=0 AND creator_id='creator-assistant'`,
		candidate.CatalogRevisionID, candidate.OverlayRevisionID, "work-1"); got != 1 {
		t.Fatalf("assistant 关系数=%d, want 1", got)
	}
	if got := countRows(t, store, `SELECT count(*) FROM source_creators
WHERE catalog_revision_id=? AND source_id='source-a'`, candidate.CatalogRevisionID); got != 2 {
		t.Fatalf("SourceCreator 数=%d, want 2", got)
	}
}

func TestStageRejectsDuplicateWorkCreatorRoleOrdinal(t *testing.T) {
	catalogStore, _ := newCandidateTestStore(t)
	ctx := context.Background()
	candidate, err := catalogStore.BeginCandidate(ctx, "job-duplicate-creator", "source-a", 1)
	if err != nil {
		t.Fatal(err)
	}
	works, mediaFacts := minimalCandidateFacts("source-a", "work-1", "media-1", candidateDigestA)
	works[0].Creator = "主作者"
	works[0].CreatorID = "creator-primary"
	works[0].CreatorSourceKey = "work-one/creator:primary:0"
	works[0].CreatorRelations = []catalog.WorkCreatorFact{{
		CreatorID: "creator-other", CreatorName: "另一主作者",
		CreatorSourceKey: "work-one/creator:primary:other", Role: "primary", Ordinal: 0,
	}}
	err = catalogStore.Stage(ctx, candidate, works, mediaFacts)
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeCatalogCandidateInvalid {
		t.Fatalf("重复 role/ordinal error=%v", err)
	}
}

// 1. BeginCandidate 首次创建成功。
func TestBeginCandidateFirstCallCreatesStagingRevision(t *testing.T) {
	catalogStore, store := newCandidateTestStore(t)
	candidate, err := catalogStore.BeginCandidate(context.Background(), "job-first", "source-a", 1)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.CatalogRevisionID == "" || candidate.OverlayRevisionID == "" {
		t.Fatalf("candidate revision id 为空: %+v", candidate)
	}
	if got := countRows(t, store, `SELECT count(*) FROM catalog_revisions WHERE job_id=? AND status='staging'`, "job-first"); got != 1 {
		t.Fatalf("首次创建后 staging 行数=%d", got)
	}
}

// 2. 同一 Job 对未发布空 candidate（无任何 Stage 数据）再次调用 BeginCandidate。
func TestBeginCandidateResetsEmptyStagingCandidate(t *testing.T) {
	catalogStore, store := newCandidateTestStore(t)
	ctx := context.Background()
	first, err := catalogStore.BeginCandidate(ctx, "job-empty", "source-a", 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := catalogStore.BeginCandidate(ctx, "job-empty", "source-a", 2)
	if err != nil {
		t.Fatalf("空 staging candidate 重建失败: %v", err)
	}
	if second.CatalogRevisionID == first.CatalogRevisionID {
		t.Fatal("重建后仍复用旧 catalog_revision_id")
	}
	if got := countRows(t, store, `SELECT count(*) FROM catalog_revisions WHERE job_id=?`, "job-empty"); got != 1 {
		t.Fatalf("重建后残留多行: %d", got)
	}
	if got := countRows(t, store, `SELECT count(*) FROM catalog_revisions WHERE catalog_revision_id=?`, first.CatalogRevisionID); got != 0 {
		t.Fatalf("旧 catalog_revision 未被清理: %d", got)
	}
}

// 3. 同一 Job 对 partial candidate（已 Stage 未 Validate）恢复。
func TestBeginCandidateResetsPartialStagingCandidate(t *testing.T) {
	catalogStore, store := newCandidateTestStore(t)
	ctx := context.Background()
	first := stageValidCandidate(t, catalogStore, "job-partial", "source-a", "work-1", "media-1", candidateDigestA, false)
	if got := countRows(t, store, `SELECT count(*) FROM source_works WHERE catalog_revision_id=?`, first.CatalogRevisionID); got != 1 {
		t.Fatalf("partial candidate 未写入预期 staging 数据: %d", got)
	}
	if got := countRows(t, store, `SELECT count(*) FROM work_search WHERE catalog_revision_id=?`, first.CatalogRevisionID); got != 1 {
		t.Fatalf("partial candidate 的 FTS 行数=%d", got)
	}
	if got := countRows(t, store, `SELECT count(*) FROM work_search_candidates WHERE catalog_revision_id=?`, first.CatalogRevisionID); got != 1 {
		t.Fatalf("partial candidate 的窄候选行数=%d", got)
	}
	second, err := catalogStore.BeginCandidate(ctx, "job-partial", "source-a", 2)
	if err != nil {
		t.Fatalf("partial candidate 重建失败: %v", err)
	}
	if got := countRows(t, store, `SELECT count(*) FROM source_works WHERE catalog_revision_id=?`, first.CatalogRevisionID); got != 0 {
		t.Fatalf("旧 partial staging 数据未被清理: %d", got)
	}
	if got := countRows(t, store, `SELECT count(*) FROM catalog_revision_sources WHERE catalog_revision_id=?`, first.CatalogRevisionID); got != 0 {
		t.Fatalf("旧 partial staging membership 未被清理: %d", got)
	}
	if got := countRows(t, store, `SELECT count(*) FROM work_search WHERE catalog_revision_id=?`, first.CatalogRevisionID); got != 0 {
		t.Fatalf("旧 partial staging FTS 未被清理: %d", got)
	}
	if got := countRows(t, store, `SELECT count(*) FROM work_search_candidates WHERE catalog_revision_id=?`, first.CatalogRevisionID); got != 0 {
		t.Fatalf("旧 partial staging 窄候选未被清理: %d", got)
	}
	works, mediaFacts := minimalCandidateFacts("source-a", "work-1", "media-1", candidateDigestA)
	if err := catalogStore.Stage(ctx, second, works, mediaFacts); err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ValidateCandidate(ctx, second); err != nil {
		t.Fatalf("重建后的 candidate 未通过 Validate: %v", err)
	}
}

// 4. 同一 Job 对 validated candidate（已 Validate 未 Publish）恢复。
func TestBeginCandidateResetsValidatedCandidate(t *testing.T) {
	catalogStore, store := newCandidateTestStore(t)
	ctx := context.Background()
	first := stageValidCandidate(t, catalogStore, "job-validated", "source-a", "work-1", "media-1", candidateDigestA, true)
	second, err := catalogStore.BeginCandidate(ctx, "job-validated", "source-a", 2)
	if err != nil {
		t.Fatalf("validated candidate 重建失败: %v", err)
	}
	if got := countRows(t, store, `SELECT count(*) FROM catalog_revisions WHERE catalog_revision_id=?`, first.CatalogRevisionID); got != 0 {
		t.Fatalf("旧 validated candidate 未被清理: %d", got)
	}
	if got := countRows(t, store, `SELECT count(*) FROM work_search WHERE catalog_revision_id=?`, first.CatalogRevisionID); got != 0 {
		t.Fatalf("旧 validated candidate 的 FTS 行未被清理: %d", got)
	}
	if got := countRows(t, store, `SELECT count(*) FROM work_search_candidates WHERE catalog_revision_id=?`, first.CatalogRevisionID); got != 0 {
		t.Fatalf("旧 validated candidate 的窄候选行未被清理: %d", got)
	}
	if second.CatalogRevisionID == first.CatalogRevisionID {
		t.Fatal("重建后仍复用旧 catalog_revision_id")
	}
}

// 5. 同一 Job 已经 publication 时再次调用 BeginCandidate，必须拒绝重建并可对账。
func TestBeginCandidateRejectsRebuildAfterPublication(t *testing.T) {
	catalogStore, store := newCandidateTestStore(t)
	ctx := context.Background()
	candidate := stageValidCandidate(t, catalogStore, "job-published", "source-a", "work-1", "media-1", candidateDigestA, true)
	publication := publishCandidate(t, catalogStore, candidate)

	_, err := catalogStore.BeginCandidate(ctx, "job-published", "source-a", 3)
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeCatalogCandidatePublished {
		t.Fatalf("已发布 Job 再次 BeginCandidate 未返回稳定错误: %v", err)
	}
	if got := countRows(t, store, `SELECT count(*) FROM catalog_revisions WHERE job_id=?`, "job-published"); got != 1 {
		t.Fatalf("已发布 Job 的 catalog_revisions 行数异常: %d", got)
	}
	if got := countRows(t, store, `SELECT count(*) FROM query_publications WHERE job_id=?`, "job-published"); got != 1 {
		t.Fatalf("已发布 Job 的 publication 行数异常: %d", got)
	}
	reconciled, err := catalogStore.PublicationForJob(ctx, "job-published")
	if err != nil || reconciled.ID != publication.ID {
		t.Fatalf("PublicationForJob 未能定位既有 publication: %+v %v", reconciled, err)
	}
}

// 6. 同一 Job 最多一个 query publication：数据库 UNIQUE 约束是最终事实来源。
func TestQueryPublicationsJobIDIsUniqueAtDatabaseLevel(t *testing.T) {
	catalogStore, store := newCandidateTestStore(t)
	ctx := context.Background()
	candidate := stageValidCandidate(t, catalogStore, "job-unique-pub", "source-a", "work-1", "media-1", candidateDigestA, true)
	publishCandidate(t, catalogStore, candidate)

	_, err := store.Catalog.SQL().ExecContext(ctx, `INSERT INTO query_publications
(query_publication_id, catalog_revision_id, overlay_revision_id, job_id, control_watermark, created_at)
VALUES ('qpub_duplicate', ?, ?, ?, 1, 1)`, candidate.CatalogRevisionID, candidate.OverlayRevisionID, "job-unique-pub")
	if err == nil {
		t.Fatal("query_publications.job_id 唯一约束未生效")
	}
}

// 7. 不同 Job 可以各自建立 candidate，互不影响。
func TestBeginCandidateIsolatesDifferentJobs(t *testing.T) {
	catalogStore, store := newCandidateTestStore(t)
	ctx := context.Background()
	candidateA, err := catalogStore.BeginCandidate(ctx, "job-a", "source-a", 1)
	if err != nil {
		t.Fatal(err)
	}
	candidateB, err := catalogStore.BeginCandidate(ctx, "job-b", "source-b", 1)
	if err != nil {
		t.Fatal(err)
	}
	if candidateA.CatalogRevisionID == candidateB.CatalogRevisionID {
		t.Fatal("不同 Job 的 candidate 复用了同一 catalog_revision_id")
	}
	if got := countRows(t, store, `SELECT count(*) FROM catalog_revisions`); got != 2 {
		t.Fatalf("两个独立 Job 的 candidate 行数异常: %d", got)
	}
}

// 8. Reset 只删除目标 Job 的 staging，不影响其他 Job 的 staging。
func TestBeginCandidateResetOnlyAffectsTargetJob(t *testing.T) {
	catalogStore, store := newCandidateTestStore(t)
	ctx := context.Background()
	candidateA := stageValidCandidate(t, catalogStore, "job-reset-a", "source-a", "work-a", "media-a", candidateDigestA, false)
	candidateB := stageValidCandidate(t, catalogStore, "job-reset-b", "source-b", "work-b", "media-b", candidateDigestB, false)

	if _, err := catalogStore.BeginCandidate(ctx, "job-reset-a", "source-a", 2); err != nil {
		t.Fatalf("重建 job-reset-a 失败: %v", err)
	}
	if got := countRows(t, store, `SELECT count(*) FROM source_works WHERE catalog_revision_id=?`, candidateA.CatalogRevisionID); got != 0 {
		t.Fatalf("job-reset-a 的旧 staging 未被清理: %d", got)
	}
	if got := countRows(t, store, `SELECT count(*) FROM source_works WHERE catalog_revision_id=?`, candidateB.CatalogRevisionID); got != 1 {
		t.Fatalf("job-reset-b 的 staging 被无关重建波及: %d", got)
	}
	if got := countRows(t, store, `SELECT count(*) FROM catalog_revisions WHERE job_id=?`, "job-reset-b"); got != 1 {
		t.Fatalf("job-reset-b 的 candidate 行被误删: %d", got)
	}
}

// 9. Reset 不影响 active publication。
func TestBeginCandidateResetDoesNotAffectActivePublication(t *testing.T) {
	catalogStore, _ := newCandidateTestStore(t)
	ctx := context.Background()
	published := stageValidCandidate(t, catalogStore, "job-active-pub", "source-a", "work-1", "media-1", candidateDigestA, true)
	activePublication := publishCandidate(t, catalogStore, published)

	stageValidCandidate(t, catalogStore, "job-retry", "source-b", "work-2", "media-2", candidateDigestB, false)
	if _, err := catalogStore.BeginCandidate(ctx, "job-retry", "source-b", 2); err != nil {
		t.Fatalf("重建 job-retry 失败: %v", err)
	}

	current, err := catalogStore.Current(ctx)
	if err != nil || current.ID != activePublication.ID {
		t.Fatalf("重建无关 Job 的 candidate 影响了 active publication: %+v %v", current, err)
	}
}

// 10. Reset 不影响其他 Source：重建后 cloneUnchangedSources 仍正确带入其他 Source 的既有数据。
func TestBeginCandidateResetStillClonesOtherSources(t *testing.T) {
	catalogStore, store := newCandidateTestStore(t)
	ctx := context.Background()
	first := stageValidCandidate(t, catalogStore, "job-source-a", "source-a", "work-1", "media-1", candidateDigestA, true)
	publishCandidate(t, catalogStore, first)
	secondSource := stageValidCandidate(t, catalogStore, "job-source-b", "source-b", "work-2", "media-2", candidateDigestB, true)
	publishCandidate(t, catalogStore, secondSource)

	stageValidCandidate(t, catalogStore, "job-retry-source-a", "source-a", "work-1", "media-1", candidateDigestA, false)
	second, err := catalogStore.BeginCandidate(ctx, "job-retry-source-a", "source-a", 2)
	if err != nil {
		t.Fatalf("重建失败: %v", err)
	}
	if got := countRows(t, store, `SELECT count(*) FROM source_works WHERE catalog_revision_id=? AND source_id='source-b'`, second.CatalogRevisionID); got != 1 {
		t.Fatalf("重建后未从活动 publication 克隆其他 Source 数据: %d", got)
	}
	if got := countRows(t, store, `SELECT count(*) FROM catalog_revision_sources
WHERE catalog_revision_id=? AND source_id='source-b' AND library_id='lib-source-b'`, second.CatalogRevisionID); got != 1 {
		t.Fatalf("重建后未克隆其他 Source membership: %d", got)
	}
}

func TestPublishRejectsInexactCatalogRevisionSourceMembership(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, store *storage.Store, candidate catalog.Candidate)
	}{
		{
			name: "missing",
			mutate: func(t *testing.T, store *storage.Store, candidate catalog.Candidate) {
				if _, err := store.Catalog.SQL().Exec(`DELETE FROM catalog_revision_sources
WHERE catalog_revision_id=? AND source_id=?`, candidate.CatalogRevisionID, candidate.SourceID); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "library-mismatch",
			mutate: func(t *testing.T, store *storage.Store, candidate catalog.Candidate) {
				if _, err := store.Catalog.SQL().Exec(`UPDATE catalog_revision_sources SET library_id='wrong-library'
WHERE catalog_revision_id=? AND source_id=?`, candidate.CatalogRevisionID, candidate.SourceID); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "surplus",
			mutate: func(t *testing.T, store *storage.Store, candidate catalog.Candidate) {
				if _, err := store.Catalog.SQL().Exec(`INSERT INTO catalog_revision_sources
(catalog_revision_id, source_id, library_id) VALUES (?, 'source-extra', 'lib-extra')`, candidate.CatalogRevisionID); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalogStore, store := newCandidateTestStore(t)
			candidate := stageValidCandidate(t, catalogStore, "job-membership-"+test.name,
				"source-a", "work-1", "media-1", candidateDigestA, false)
			test.mutate(t, store, candidate)
			if err := catalogStore.ValidateCandidate(context.Background(), candidate); !hasFaultCode(err, fault.CodeCatalogCandidateInvalid) {
				t.Fatalf("ValidateCandidate 未拒绝 %s membership: %v", test.name, err)
			}
			if _, err := catalogStore.Publish(context.Background(), candidate); !hasFaultCode(err, fault.CodeCatalogCandidateInvalid) {
				t.Fatalf("Publish 绕过 Validate 时未拒绝 %s membership: %v", test.name, err)
			}
		})
	}
}

func TestStageRejectsCrossChunkLibraryMismatch(t *testing.T) {
	catalogStore, store := newCandidateTestStore(t)
	ctx := context.Background()
	candidate := stageValidCandidate(t, catalogStore, "job-library-mismatch", "source-a",
		"work-1", "media-1", candidateDigestA, false)
	works, _ := minimalCandidateFacts("source-a", "work-2", "media-2", candidateDigestB)
	works[0].LibraryID = "lib-other"
	if err := catalogStore.Stage(ctx, candidate, works, nil); !hasFaultCode(err, fault.CodeCatalogCandidateInvalid) {
		t.Fatalf("跨 chunk Library 不一致未被拒绝: %v", err)
	}
	if got := countRows(t, store, `SELECT count(*) FROM catalog_revision_sources
WHERE catalog_revision_id=? AND source_id='source-a' AND library_id='lib-source-a'`, candidate.CatalogRevisionID); got != 1 {
		t.Fatalf("失败 Stage 改写了既有 membership: %d", got)
	}
}

func TestPublishOverlayRejectsMissingCatalogRevisionSourceMembership(t *testing.T) {
	catalogStore, store := newCandidateTestStore(t)
	ctx := context.Background()
	baseCandidate := stageValidCandidate(t, catalogStore, "job-overlay-base", "source-a",
		"work-1", "media-1", candidateDigestA, true)
	base := publishCandidate(t, catalogStore, baseCandidate)
	overlayCandidate, err := catalogStore.BeginOverlayCandidate(ctx, "job-overlay-membership", base.CatalogRevisionID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ApplyOverlayFacts(ctx, overlayCandidate, map[string]catalog.OverlayFact{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Catalog.SQL().Exec(`DELETE FROM catalog_revision_sources
WHERE catalog_revision_id=?`, base.CatalogRevisionID); err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ValidateOverlayCandidate(ctx, overlayCandidate); !hasFaultCode(err, fault.CodeCatalogCandidateInvalid) {
		t.Fatalf("ValidateOverlayCandidate 未拒绝缺失 membership: %v", err)
	}
	if _, err := catalogStore.PublishOverlay(ctx, overlayCandidate); !hasFaultCode(err, fault.CodeCatalogCandidateInvalid) {
		t.Fatalf("PublishOverlay 绕过 Validate 时未拒绝缺失 membership: %v", err)
	}
}

func hasFaultCode(err error, code fault.Code) bool {
	var structured *fault.Error
	return errors.As(err, &structured) && structured.Code == code
}

// 11. Attempt 2 再次中断后 Attempt 3 仍可恢复：连续多次 BeginCandidate 均保持幂等。
func TestBeginCandidateRecoversAcrossRepeatedInterruptions(t *testing.T) {
	catalogStore, store := newCandidateTestStore(t)
	ctx := context.Background()
	var lastRevisionID string
	for attempt := 1; attempt <= 3; attempt++ {
		candidate, err := catalogStore.BeginCandidate(ctx, "job-repeated", "source-a", int64(attempt))
		if err != nil {
			t.Fatalf("第 %d 次 BeginCandidate 失败: %v", attempt, err)
		}
		if candidate.CatalogRevisionID == lastRevisionID {
			t.Fatalf("第 %d 次 BeginCandidate 未生成新 revision", attempt)
		}
		lastRevisionID = candidate.CatalogRevisionID
		works, mediaFacts := minimalCandidateFacts("source-a", "work-1", "media-1", candidateDigestA)
		if err := catalogStore.Stage(ctx, candidate, works, mediaFacts); err != nil {
			t.Fatalf("第 %d 次 Stage 失败: %v", attempt, err)
		}
		// 模拟每次 Attempt 都在 Validate 之前被强杀，从不推进到 Publish。
	}
	if got := countRows(t, store, `SELECT count(*) FROM catalog_revisions WHERE job_id=?`, "job-repeated"); got != 1 {
		t.Fatalf("反复中断后残留多行 candidate: %d", got)
	}
}

// 13. GC 不删除活动 Attempt 的 candidate：即使已超过保留期，只要 job_id 在 ActiveJobIDs 中
// 就必须跳过，不能被误判为遗弃 staging。
func TestGarbageCollectSkipsActiveJobCandidate(t *testing.T) {
	catalogStore, store := newCandidateTestStore(t)
	ctx := context.Background()
	candidate := stageValidCandidate(t, catalogStore, "job-active-attempt", "source-a", "work-1", "media-1", candidateDigestA, false)
	var beforeCandidateRowID, beforeFTSRowID int64
	if err := store.Catalog.SQL().QueryRow(`SELECT c.search_rowid, s.rowid
FROM work_search_candidates c JOIN work_search s ON s.rowid=c.search_rowid
WHERE c.catalog_revision_id=?`, candidate.CatalogRevisionID).Scan(&beforeCandidateRowID, &beforeFTSRowID); err != nil {
		t.Fatal(err)
	}

	result, err := catalogStore.GarbageCollectWithOptions(ctx, catalog.GCOptions{
		Retention: 0, ActiveJobIDs: []string{"job-active-attempt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SkippedActive != 1 || result.StagingAborted != 0 {
		t.Fatalf("活动 Job 的 candidate 未被跳过: %+v", result)
	}
	if got := countRows(t, store, `SELECT count(*) FROM catalog_revisions WHERE job_id=? AND status='staging'`, "job-active-attempt"); got != 1 {
		t.Fatalf("活动 Job 的 staging candidate 被 GC 误删: %d", got)
	}
	var afterCandidateRowID, afterFTSRowID int64
	if err := store.Catalog.SQL().QueryRow(`SELECT c.search_rowid, s.rowid
FROM work_search_candidates c JOIN work_search s ON s.rowid=c.search_rowid
WHERE c.catalog_revision_id=?`, candidate.CatalogRevisionID).Scan(&afterCandidateRowID, &afterFTSRowID); err != nil {
		t.Fatal(err)
	}
	if afterCandidateRowID != beforeCandidateRowID || afterFTSRowID != beforeFTSRowID {
		t.Fatalf("活动 Candidate 的搜索映射被 GC 改写: before=%d/%d after=%d/%d",
			beforeCandidateRowID, beforeFTSRowID, afterCandidateRowID, afterFTSRowID)
	}
}

// 14. GC 最终能清理 abandoned staging：不在 ActiveJobIDs 中、超过保留期的 staging candidate
// 必须先转为 aborted 再被彻底删除，且不影响其他仍在保留期内的 staging。
func TestGarbageCollectReclaimsAbandonedStagingCandidate(t *testing.T) {
	catalogStore, store := newCandidateTestStore(t)
	ctx := context.Background()
	candidate := stageValidCandidate(t, catalogStore, "job-abandoned", "source-a", "work-1", "media-1", candidateDigestA, false)

	result, err := catalogStore.GarbageCollectWithOptions(ctx, catalog.GCOptions{Retention: 0})
	if err != nil {
		t.Fatal(err)
	}
	if result.StagingAborted != 1 {
		t.Fatalf("遗弃 staging 未被回收: %+v", result)
	}
	if got := countRows(t, store, `SELECT count(*) FROM catalog_revisions WHERE job_id=?`, "job-abandoned"); got != 0 {
		t.Fatalf("遗弃 staging 未被彻底删除: %d", got)
	}
	if got := countRows(t, store, `SELECT count(*) FROM work_search WHERE catalog_revision_id=?`, candidate.CatalogRevisionID); got != 0 {
		t.Fatalf("遗弃 staging 的 FTS 行残留: %d", got)
	}
	if got := countRows(t, store, `SELECT count(*) FROM work_search_candidates WHERE catalog_revision_id=?`, candidate.CatalogRevisionID); got != 0 {
		t.Fatalf("遗弃 staging 的窄候选行残留: %d", got)
	}
}

func TestValidateCandidateRejectsSearchProjectionDriftWithoutWritingSeal(t *testing.T) {
	tests := []struct {
		name    string
		corrupt func(*testing.T, *storage.Store, catalog.Candidate)
	}{
		{name: "missing-candidate", corrupt: func(t *testing.T, store *storage.Store, candidate catalog.Candidate) {
			if _, err := store.Catalog.SQL().Exec(`DELETE FROM work_search_candidates WHERE catalog_revision_id=?`, candidate.CatalogRevisionID); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "missing-fts", corrupt: func(t *testing.T, store *storage.Store, candidate catalog.Candidate) {
			if _, err := store.Catalog.SQL().Exec(`DELETE FROM work_search WHERE catalog_revision_id=?`, candidate.CatalogRevisionID); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "candidate-fact-drift", corrupt: func(t *testing.T, store *storage.Store, candidate catalog.Candidate) {
			if _, err := store.Catalog.SQL().Exec(`UPDATE work_search_candidates SET source_key='drift' WHERE catalog_revision_id=?`, candidate.CatalogRevisionID); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "fts-text-drift", corrupt: func(t *testing.T, store *storage.Store, candidate catalog.Candidate) {
			if _, err := store.Catalog.SQL().Exec(`UPDATE work_search SET normalized_original_text='drift' WHERE catalog_revision_id=?`, candidate.CatalogRevisionID); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalogStore, store := newCandidateTestStore(t)
			candidate := stageValidCandidate(t, catalogStore, "job-validate-search", "source-a",
				"work-1", "media-1", candidateDigestA, false)
			test.corrupt(t, store, candidate)
			if err := catalogStore.ValidateCandidate(context.Background(), candidate); !hasFaultCode(err, fault.CodeCatalogCandidateInvalid) {
				t.Fatalf("ValidateCandidate 未拒绝搜索投影漂移: %v", err)
			}
			if got := countRows(t, store, `SELECT count(*) FROM candidate_validation_seals
WHERE catalog_revision_id=? AND overlay_revision_id=?`, candidate.CatalogRevisionID, candidate.OverlayRevisionID); got != 0 {
				t.Fatalf("损坏 Candidate 仍写入验证封印: %d", got)
			}
		})
	}
}

func TestPublishRequiresCandidateValidationSeal(t *testing.T) {
	catalogStore, store := newCandidateTestStore(t)
	ctx := context.Background()
	candidate := stageValidCandidate(t, catalogStore, "job-seal-required", "source-a",
		"work-1", "media-1", candidateDigestA, false)
	if _, err := catalogStore.Publish(ctx, candidate); !hasFaultCode(err, fault.CodeCatalogCandidateInvalid) {
		t.Fatalf("缺少验证封印时 Publish 未 fail-closed: %v", err)
	}
	if got := countRows(t, store, `SELECT count(*) FROM query_publications WHERE job_id=?`, candidate.JobID); got != 0 {
		t.Fatalf("缺少封印仍创建了 publication: %d", got)
	}
	if err := catalogStore.ValidateCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, store, `SELECT count(*) FROM candidate_validation_seals
WHERE catalog_revision_id=? AND overlay_revision_id=? AND candidate_kind='catalog' AND validation_version=1`,
		candidate.CatalogRevisionID, candidate.OverlayRevisionID); got != 1 {
		t.Fatalf("完整验证后封印数=%d", got)
	}
	publishCandidate(t, catalogStore, candidate)
}

func TestCatalogValidationSealFinalizesCandidateMutations(t *testing.T) {
	catalogStore, store := newCandidateTestStore(t)
	ctx := context.Background()
	candidate := stageValidCandidate(t, catalogStore, "job-seal-stage", "source-a",
		"work-1", "media-1", candidateDigestA, true)
	works, mediaFacts := minimalCandidateFacts("source-a", "work-2", "media-2", candidateDigestB)
	works[0].SourceKey = "work-two"
	works[0].SourceTitle = "work-two"
	works[0].Title = "work-two"
	mediaFacts[0].SourceKey = "work-two/media.bin"
	mediaFacts[0].WorkSourceKey = "work-two"
	mediaFacts[0].RelativePath = "work-two/media.bin"
	mediaFacts[0].LocationKey = "loc-media-2"
	if err := catalogStore.Stage(ctx, candidate, works, mediaFacts); !hasFaultCode(err, fault.CodeCatalogCandidateInvalid) {
		t.Fatalf("封印后的 Stage 未被拒绝: %v", err)
	}
	if got := countRows(t, store, `SELECT count(*) FROM candidate_validation_seals
WHERE catalog_revision_id=? AND overlay_revision_id=?`, candidate.CatalogRevisionID, candidate.OverlayRevisionID); got != 1 {
		t.Fatalf("被拒绝的 Stage 改变了验证封印: %d", got)
	}
	if got := countRows(t, store, `SELECT count(*) FROM work_projections WHERE catalog_revision_id=?`, candidate.CatalogRevisionID); got != 1 {
		t.Fatalf("被拒绝的 Stage 改变了 WorkProjection: %d", got)
	}
	publishCandidate(t, catalogStore, candidate)
	if err := catalogStore.Stage(ctx, candidate, works, mediaFacts); !hasFaultCode(err, fault.CodeCatalogCandidateInvalid) {
		t.Fatalf("已发布 Candidate 仍可 Stage: %v", err)
	}
	if err := catalogStore.ApplyCatalogCandidateOverlays(ctx, candidate, map[string]catalog.OverlayFact{
		"work-1": {Favorite: true},
	}); !hasFaultCode(err, fault.CodeCatalogCandidateInvalid) {
		t.Fatalf("已发布 Candidate 仍可重投影 Overlay: %v", err)
	}
	if err := catalogStore.ApplyCatalogCandidateCreatorMerges(ctx, candidate, []domain.CreatorMergePair{{
		Absorbed: "creator-old", Target: "creator-new", TargetName: "Creator New",
	}}); !hasFaultCode(err, fault.CodeCatalogCandidateInvalid) {
		t.Fatalf("已发布 Candidate 仍可改写 Creator 投影: %v", err)
	}
	if got := countRows(t, store, `SELECT favorite FROM work_projections
WHERE catalog_revision_id=? AND overlay_revision_id=? AND work_id='work-1'`,
		candidate.CatalogRevisionID, candidate.OverlayRevisionID); got != 0 {
		t.Fatalf("已发布快照被原地改写: favorite=%d", got)
	}
}

func TestCatalogMutationGuardsRejectWrongIdentityAndAbortedCandidate(t *testing.T) {
	catalogStore, store := newCandidateTestStore(t)
	ctx := context.Background()
	candidate := stageValidCandidate(t, catalogStore, "job-guard", "source-a",
		"work-1", "media-1", candidateDigestA, false)
	works, mediaFacts := minimalCandidateFacts("source-a", "work-2", "media-2", candidateDigestB)
	works[0].SourceKey, works[0].SourceTitle, works[0].Title = "work-two", "work-two", "work-two"
	mediaFacts[0].SourceKey, mediaFacts[0].WorkSourceKey = "work-two/media.bin", "work-two"
	mediaFacts[0].RelativePath, mediaFacts[0].LocationKey = "work-two/media.bin", "loc-media-2"

	wrong := []catalog.Candidate{candidate, candidate, candidate}
	wrong[0].JobID = "job-wrong"
	wrong[1].SourceID = "source-wrong"
	wrong[2].ControlWatermark++
	for index, item := range wrong {
		if err := catalogStore.Stage(ctx, item, works, mediaFacts); !hasFaultCode(err, fault.CodeCatalogCandidateInvalid) {
			t.Fatalf("错误身份 %d 未被拒绝: %v", index, err)
		}
	}
	if got := countRows(t, store, `SELECT count(*) FROM work_projections WHERE catalog_revision_id=?`, candidate.CatalogRevisionID); got != 1 {
		t.Fatalf("错误身份写入改变了 Candidate: %d", got)
	}
	if err := catalogStore.AbortCandidate(ctx, candidate.JobID); err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.Stage(ctx, candidate, works, mediaFacts); !hasFaultCode(err, fault.CodeCatalogCandidateInvalid) {
		t.Fatalf("aborted Candidate 仍可 Stage: %v", err)
	}
}

func TestFailedCandidateRevalidationDoesNotRestoreOldSeal(t *testing.T) {
	catalogStore, store := newCandidateTestStore(t)
	ctx := context.Background()
	candidate := stageValidCandidate(t, catalogStore, "job-revalidate-seal", "source-a",
		"work-1", "media-1", candidateDigestA, true)
	if _, err := store.Catalog.SQL().Exec(`DELETE FROM work_search_candidates WHERE catalog_revision_id=?`, candidate.CatalogRevisionID); err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ValidateCandidate(ctx, candidate); !hasFaultCode(err, fault.CodeCatalogCandidateInvalid) {
		t.Fatalf("显式重验未拒绝损坏候选: %v", err)
	}
	if got := countRows(t, store, `SELECT count(*) FROM candidate_validation_seals
WHERE catalog_revision_id=? AND overlay_revision_id=?`, candidate.CatalogRevisionID, candidate.OverlayRevisionID); got != 0 {
		t.Fatalf("失败重验后旧封印复活: %d", got)
	}
	if _, err := catalogStore.Publish(ctx, candidate); !hasFaultCode(err, fault.CodeCatalogCandidateInvalid) {
		t.Fatalf("失败重验后仍可发布: %v", err)
	}
}

func TestConcurrentCandidateRevalidationIsIdempotent(t *testing.T) {
	catalogStore, store := newCandidateTestStore(t)
	candidate := stageValidCandidate(t, catalogStore, "job-concurrent-revalidate", "source-a",
		"work-1", "media-1", candidateDigestA, true)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for range 2 {
		go func() {
			ready.Done()
			<-start
			errs <- catalogStore.ValidateCandidate(context.Background(), candidate)
		}()
	}
	ready.Wait()
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("并发重验返回错误: %v", err)
		}
	}
	if got := countRows(t, store, `SELECT count(*) FROM candidate_validation_seals
WHERE catalog_revision_id=? AND overlay_revision_id=?`, candidate.CatalogRevisionID, candidate.OverlayRevisionID); got != 1 {
		t.Fatalf("并发重验后的封印数=%d", got)
	}
}

func TestOverlayValidationSealFinalizesCandidateMutations(t *testing.T) {
	catalogStore, store := newCandidateTestStore(t)
	ctx := context.Background()
	base := stageValidCandidate(t, catalogStore, "job-overlay-base", "source-a", "work-1", "media-1", candidateDigestA, true)
	publishCandidate(t, catalogStore, base)
	overlay, err := catalogStore.BeginOverlayCandidate(ctx, "job-overlay-seal", base.CatalogRevisionID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ApplyOverlayFacts(ctx, overlay, map[string]catalog.OverlayFact{}); err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ValidateOverlayCandidate(ctx, overlay); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, store, `SELECT count(*) FROM candidate_validation_seals
WHERE catalog_revision_id=? AND overlay_revision_id=? AND candidate_kind='overlay' AND validation_version=1`,
		overlay.CatalogRevisionID, overlay.OverlayRevisionID); got != 1 {
		t.Fatalf("Overlay 完整验证后封印数=%d", got)
	}
	if err := catalogStore.ApplyOverlayFacts(ctx, overlay, map[string]catalog.OverlayFact{
		"work-1": {Favorite: true},
	}); !hasFaultCode(err, fault.CodeCatalogCandidateInvalid) {
		t.Fatalf("封印后的 Overlay 重投影未被拒绝: %v", err)
	}
	if got := countRows(t, store, `SELECT count(*) FROM candidate_validation_seals
WHERE catalog_revision_id=? AND overlay_revision_id=?`, overlay.CatalogRevisionID, overlay.OverlayRevisionID); got != 1 {
		t.Fatalf("被拒绝的 Overlay 重投影改变了封印: %d", got)
	}
	if got := countRows(t, store, `SELECT favorite FROM work_projections
WHERE catalog_revision_id=? AND overlay_revision_id=? AND work_id='work-1'`,
		overlay.CatalogRevisionID, overlay.OverlayRevisionID); got != 0 {
		t.Fatalf("封印后的 Overlay Candidate 被原地改写: favorite=%d", got)
	}
	if _, err := catalogStore.PublishOverlay(ctx, overlay); err != nil {
		t.Fatalf("封印后的 Overlay 发布失败: %v", err)
	}
	if err := catalogStore.ApplyOverlayFacts(ctx, overlay, map[string]catalog.OverlayFact{
		"work-1": {Favorite: true},
	}); !hasFaultCode(err, fault.CodeCatalogCandidateInvalid) {
		t.Fatalf("已发布 Overlay Candidate 仍可重投影: %v", err)
	}
}

func TestOverlayMutationGuardRejectsWrongIdentity(t *testing.T) {
	catalogStore, _ := newCandidateTestStore(t)
	ctx := context.Background()
	base := stageValidCandidate(t, catalogStore, "job-overlay-guard-base", "source-a",
		"work-1", "media-1", candidateDigestA, true)
	publishCandidate(t, catalogStore, base)
	overlay, err := catalogStore.BeginOverlayCandidate(ctx, "job-overlay-guard", base.CatalogRevisionID, 2)
	if err != nil {
		t.Fatal(err)
	}
	wrong := []catalog.OverlayCandidate{overlay, overlay, overlay}
	wrong[0].JobID = "job-wrong"
	wrong[1].ControlWatermark++
	wrong[2].BaseOverlayRevisionID = "ovr_wrong"
	for index, item := range wrong {
		if err := catalogStore.ApplyOverlayFacts(ctx, item, map[string]catalog.OverlayFact{}); !hasFaultCode(err, fault.CodeCatalogCandidateInvalid) {
			t.Fatalf("错误 Overlay 身份 %d 未被拒绝: %v", index, err)
		}
	}
	if err := catalogStore.ApplyOverlayFacts(ctx, overlay, map[string]catalog.OverlayFact{}); err != nil {
		t.Fatalf("合法 Overlay Candidate 被拒绝: %v", err)
	}
	if err := catalogStore.ValidateOverlayCandidate(ctx, overlay); err != nil {
		t.Fatalf("合法 Overlay Candidate 验证失败: %v", err)
	}
	wrongBase := overlay
	wrongBase.BaseOverlayRevisionID = "ovr_wrong"
	if _, err := catalogStore.PublishOverlay(ctx, wrongBase); !hasFaultCode(err, fault.CodeCatalogCandidateInvalid) {
		t.Fatalf("PublishOverlay 接受了调用方替换的 base 身份: %v", err)
	}
	if err := catalogStore.FinishOverlayCandidate(ctx, wrongBase, "aborted"); !hasFaultCode(err, fault.CodeCatalogCandidateInvalid) {
		t.Fatalf("FinishOverlayCandidate 接受了调用方替换的 base 身份: %v", err)
	}
	wrongCatalog := overlay
	wrongCatalog.CatalogRevisionID = "cat_wrong"
	if _, err := catalogStore.PublishOverlay(ctx, wrongCatalog); !hasFaultCode(err, fault.CodeCatalogCandidateInvalid) {
		t.Fatalf("PublishOverlay 接受了调用方替换的 catalog 身份: %v", err)
	}
}

func TestOverlayActiveDriftIsDeferredToPublishCAS(t *testing.T) {
	catalogStore, storageStore := newCandidateTestStore(t)
	ctx := context.Background()
	base := stageValidCandidate(t, catalogStore, "job-overlay-drift-base", "source-a",
		"work-1", "media-1", candidateDigestA, true)
	basePublication := publishCandidate(t, catalogStore, base)

	stale, err := catalogStore.BeginOverlayCandidate(ctx, "job-overlay-drift-stale", base.CatalogRevisionID, 2)
	if err != nil {
		t.Fatal(err)
	}
	winner, err := catalogStore.BeginOverlayCandidate(ctx, "job-overlay-drift-winner", base.CatalogRevisionID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if stale.BaseOverlayRevisionID != basePublication.OverlayRevisionID ||
		winner.BaseOverlayRevisionID != basePublication.OverlayRevisionID {
		t.Fatalf("并行候选没有固定同一创建基线: stale=%q winner=%q base=%q",
			stale.BaseOverlayRevisionID, winner.BaseOverlayRevisionID, basePublication.OverlayRevisionID)
	}
	if err := catalogStore.ApplyOverlayFacts(ctx, winner, map[string]catalog.OverlayFact{}); err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ValidateOverlayCandidate(ctx, winner); err != nil {
		t.Fatal(err)
	}
	winnerPublication, err := catalogStore.PublishOverlay(ctx, winner)
	if err != nil {
		t.Fatal(err)
	}
	gcResult, err := catalogStore.GarbageCollect(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if gcResult.Publications != 0 || countRows(t, storageStore,
		`SELECT count(*) FROM query_publications WHERE query_publication_id=?`, basePublication.ID) != 1 {
		t.Fatalf("GC 回收了 staging Overlay 持久引用的 base publication: %+v", gcResult)
	}

	if err := catalogStore.ApplyOverlayFacts(ctx, stale, map[string]catalog.OverlayFact{}); err != nil {
		t.Fatalf("active 漂移错误地使合法旧基线候选无法继续构造: %v", err)
	}
	if err := catalogStore.ValidateOverlayCandidate(ctx, stale); err != nil {
		t.Fatalf("active 漂移错误地使合法旧基线候选无法验证: %v", err)
	}
	if _, err := catalogStore.PublishOverlay(ctx, stale); !hasFaultCode(err, fault.CodeConflict) {
		t.Fatalf("旧基线候选发布未由 active CAS 返回 CONFLICT: %v", err)
	}
	current, err := catalogStore.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current.ID != winnerPublication.ID || current.OverlayRevisionID != winner.OverlayRevisionID {
		t.Fatalf("旧基线候选越过 CAS 改变 active publication: %+v", current)
	}
	if got := countRows(t, storageStore, `SELECT count(*) FROM candidate_validation_seals
WHERE catalog_revision_id=? AND overlay_revision_id=? AND candidate_kind='overlay'`,
		stale.CatalogRevisionID, stale.OverlayRevisionID); got != 1 {
		t.Fatalf("CAS 冲突不应破坏已经完成的候选封印: %d", got)
	}
	if err := catalogStore.FinishOverlayCandidate(ctx, stale, "superseded"); err != nil {
		t.Fatal(err)
	}
	gcResult, err = catalogStore.GarbageCollect(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if gcResult.Publications != 1 || countRows(t, storageStore,
		`SELECT count(*) FROM query_publications WHERE query_publication_id=?`, basePublication.ID) != 0 {
		t.Fatalf("terminal candidate 释放后 base publication 未恢复可回收: %+v", gcResult)
	}
}

func TestGarbageCollectReclaimsTerminalOverlaySearchProjection(t *testing.T) {
	tests := []struct {
		name   string
		finish func(context.Context, *catalog.Store, catalog.OverlayCandidate) error
	}{
		{name: "aborted", finish: func(ctx context.Context, store *catalog.Store, candidate catalog.OverlayCandidate) error {
			return store.FinishOverlayCandidate(ctx, candidate, "aborted")
		}},
		{name: "superseded", finish: func(ctx context.Context, store *catalog.Store, candidate catalog.OverlayCandidate) error {
			return store.FinishOverlayCandidate(ctx, candidate, "superseded")
		}},
		{name: "aborted-by-job", finish: func(ctx context.Context, store *catalog.Store, candidate catalog.OverlayCandidate) error {
			return store.AbortOverlayCandidatesForJob(ctx, candidate.JobID)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalogStore, storageStore := newCandidateTestStore(t)
			ctx := context.Background()
			base := stageValidCandidate(t, catalogStore, "job-terminal-base", "source-a", "work-1", "media-1", candidateDigestA, true)
			publishCandidate(t, catalogStore, base)
			overlay, err := catalogStore.BeginOverlayCandidate(ctx, "job-terminal-overlay", base.CatalogRevisionID, 2)
			if err != nil {
				t.Fatal(err)
			}
			if err := catalogStore.ApplyOverlayFacts(ctx, overlay, map[string]catalog.OverlayFact{}); err != nil {
				t.Fatal(err)
			}
			if got := countRows(t, storageStore, `SELECT count(*) FROM work_search_candidates
WHERE catalog_revision_id=? AND overlay_revision_id=?`, overlay.CatalogRevisionID, overlay.OverlayRevisionID); got != 1 {
				t.Fatalf("terminal 前窄候选=%d", got)
			}
			if got := countRows(t, storageStore, `SELECT count(*) FROM work_search
WHERE catalog_revision_id=? AND overlay_revision_id=?`, overlay.CatalogRevisionID, overlay.OverlayRevisionID); got != 1 {
				t.Fatalf("terminal 前 FTS=%d", got)
			}
			if err := test.finish(ctx, catalogStore, overlay); err != nil {
				t.Fatal(err)
			}
			if err := catalogStore.ApplyOverlayFacts(ctx, overlay, map[string]catalog.OverlayFact{
				"work-1": {Favorite: true},
			}); !hasFaultCode(err, fault.CodeCatalogCandidateInvalid) {
				t.Fatalf("terminal Overlay Candidate 仍可重投影: %v", err)
			}
			result, err := catalogStore.GarbageCollect(ctx, 0)
			if err != nil {
				t.Fatal(err)
			}
			if result.OverlayRevisions != 1 || result.CatalogRevisions != 0 || result.Publications != 0 {
				t.Fatalf("terminal Overlay GC 结果错误: %+v", result)
			}
			for _, table := range []string{"overlay_projection_revisions", "work_projections", "work_search_candidates", "work_search"} {
				if got := countRows(t, storageStore, `SELECT count(*) FROM `+table+`
WHERE catalog_revision_id=? AND overlay_revision_id=?`, overlay.CatalogRevisionID, overlay.OverlayRevisionID); got != 0 {
					t.Fatalf("%s 残留 terminal Overlay 行=%d", table, got)
				}
			}
			if got := countRows(t, storageStore, `SELECT count(*) FROM work_search_candidates
WHERE catalog_revision_id=? AND overlay_revision_id=?`, base.CatalogRevisionID, base.OverlayRevisionID); got != 1 {
				t.Fatalf("活动 publication 窄候选被误删: %d", got)
			}
			if got := countRows(t, storageStore, `SELECT count(*) FROM work_search
WHERE catalog_revision_id=? AND overlay_revision_id=?`, base.CatalogRevisionID, base.OverlayRevisionID); got != 1 {
				t.Fatalf("活动 publication FTS 被误删: %d", got)
			}
		})
	}
}
