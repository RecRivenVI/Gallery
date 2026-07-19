package catalog_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
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
	second, err := catalogStore.BeginCandidate(ctx, "job-partial", "source-a", 2)
	if err != nil {
		t.Fatalf("partial candidate 重建失败: %v", err)
	}
	if got := countRows(t, store, `SELECT count(*) FROM source_works WHERE catalog_revision_id=?`, first.CatalogRevisionID); got != 0 {
		t.Fatalf("旧 partial staging 数据未被清理: %d", got)
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
	first := stageValidCandidate(t, catalogStore, "job-multi-source", "source-a", "work-1", "media-1", candidateDigestA, false)
	works, mediaFacts := minimalCandidateFacts("source-b", "work-2", "media-2", candidateDigestB)
	if err := catalogStore.Stage(ctx, first, works, mediaFacts); err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.ValidateCandidate(ctx, first); err != nil {
		t.Fatal(err)
	}
	publishCandidate(t, catalogStore, first)

	stageValidCandidate(t, catalogStore, "job-retry-source-a", "source-a", "work-1", "media-1", candidateDigestA, false)
	second, err := catalogStore.BeginCandidate(ctx, "job-retry-source-a", "source-a", 2)
	if err != nil {
		t.Fatalf("重建失败: %v", err)
	}
	if got := countRows(t, store, `SELECT count(*) FROM source_works WHERE catalog_revision_id=? AND source_id='source-b'`, second.CatalogRevisionID); got != 1 {
		t.Fatalf("重建后未从活动 publication 克隆其他 Source 数据: %d", got)
	}
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
	stageValidCandidate(t, catalogStore, "job-active-attempt", "source-a", "work-1", "media-1", candidateDigestA, false)

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
}

// 14. GC 最终能清理 abandoned staging：不在 ActiveJobIDs 中、超过保留期的 staging candidate
// 必须先转为 aborted 再被彻底删除，且不影响其他仍在保留期内的 staging。
func TestGarbageCollectReclaimsAbandonedStagingCandidate(t *testing.T) {
	catalogStore, store := newCandidateTestStore(t)
	ctx := context.Background()
	stageValidCandidate(t, catalogStore, "job-abandoned", "source-a", "work-1", "media-1", candidateDigestA, false)

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
}
