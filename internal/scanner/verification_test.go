package scanner_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/hashjob"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/scanner"
	"github.com/RecRivenVI/gallery/internal/storage"
)

// stepClock 是仅供本文件确定性测试使用的可推进时钟，用于验证"复用摘要保留旧确认时间、
// 只有真正重新完成哈希才推进确认时间"的语义，而不依赖真实墙钟在两次扫描之间恰好流逝。
type stepClock struct {
	mu  sync.Mutex
	now time.Time
}

func newStepClock(start time.Time) *stepClock { return &stepClock{now: start} }

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *stepClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// setupWithHash 在 setup 基础上注入真实的持久 Hash Job Service，使 job_type='hash' 计数
// 能够真实反映"是否重新读取并哈希了媒体正文"，而不是落入不产生 Hash Job 记录的同步
// fallback 路径。
func setupWithHash(t *testing.T, fixture []byte) (*application.Resources, *jobs.Store, *catalog.Store, *scanner.Service, application.Source, *storage.Store) {
	t.Helper()
	resources, jobStore, catalogStore, service, source, store := setup(t, fixture)
	hashService, err := hashjob.New(context.Background(), resources, jobStore)
	if err != nil {
		t.Fatal(err)
	}
	service.SetHashService(hashService)
	return resources, jobStore, catalogStore, service, source, store
}

func TestIndexProfilePublishesLocatedUnverifiedWithoutHashing(t *testing.T) {
	fixture := []byte("index profile does not read media content into a digest")
	_, _, catalogStore, service, source, store := setupWithHash(t, fixture)
	defer store.Close()
	ctx := context.Background()

	job, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIndex)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, job.ID); err != nil {
		t.Fatal(err)
	}

	publication, works, err := catalogStore.ListWorks(ctx)
	if err != nil || len(works) != 1 {
		t.Fatalf("index 档案未发布 Work: %+v %v", works, err)
	}
	_, mediaItems, err := catalogStore.ListMediaForWork(ctx, works[0].ID)
	if err != nil || len(mediaItems) != 1 {
		t.Fatalf("index 档案未发布 Media: %+v %v", mediaItems, err)
	}
	if mediaItems[0].LocationStatus != "present" {
		t.Fatalf("index 档案媒体的位置应仍为 present: %+v", mediaItems[0])
	}
	if mediaItems[0].ContentVerificationState != catalog.ContentVerificationStateLocatedUnverified {
		t.Fatalf("index 档案媒体应为 located_unverified: %+v", mediaItems[0])
	}
	if !mediaItems[0].VerifiedAt.IsZero() {
		t.Fatalf("index 档案媒体不应有 verifiedAt: %+v", mediaItems[0])
	}
	if mediaItems[0].Digest != "" || mediaItems[0].Algorithm != "" {
		t.Fatalf("index 档案不得建立伪造 digest: %+v", mediaItems[0])
	}
	var blobCount int
	if err := store.Catalog.SQL().QueryRowContext(ctx, "SELECT count(*) FROM content_blobs WHERE catalog_revision_id=?", publication.CatalogRevisionID).Scan(&blobCount); err != nil {
		t.Fatal(err)
	}
	if blobCount != 0 {
		t.Fatalf("index 档案不应写入 content_blobs: blobCount=%d", blobCount)
	}
	var hashJobCount int
	if err := store.Control.SQL().QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE job_type='hash'").Scan(&hashJobCount); err != nil {
		t.Fatal(err)
	}
	if hashJobCount != 0 {
		t.Fatalf("index 档案不应建立任何 Hash Job: hashJobCount=%d", hashJobCount)
	}
}

func TestIncrementalProfileReusesUnchangedDigestWithoutRehash(t *testing.T) {
	fixture := []byte("incremental profile reuses this content across an unchanged rescan")
	_, _, catalogStore, service, source, store := setupWithHash(t, fixture)
	defer store.Close()
	ctx := context.Background()

	first, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	_, worksBefore, err := catalogStore.ListWorks(ctx)
	if err != nil || len(worksBefore) != 1 {
		t.Fatal(err)
	}
	_, mediaBefore, err := catalogStore.ListMediaForWork(ctx, worksBefore[0].ID)
	if err != nil || len(mediaBefore) != 1 || mediaBefore[0].Digest == "" {
		t.Fatalf("首次 incremental 扫描未建立已确认 digest: %+v %v", mediaBefore, err)
	}

	second, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	_, worksAfter, err := catalogStore.ListWorks(ctx)
	if err != nil || len(worksAfter) != 1 {
		t.Fatal(err)
	}
	_, mediaAfter, err := catalogStore.ListMediaForWork(ctx, worksAfter[0].ID)
	if err != nil || len(mediaAfter) != 1 {
		t.Fatal(err)
	}
	if mediaAfter[0].Digest != mediaBefore[0].Digest {
		t.Fatalf("未变化文件的 digest 应保持一致: before=%s after=%s", mediaBefore[0].Digest, mediaAfter[0].Digest)
	}
	if mediaAfter[0].LocationStatus != "present" {
		t.Fatalf("复用已确认 digest 后媒体应为 present: %+v", mediaAfter[0])
	}
	// 无变化的第二次扫描不应为该文件再次建立 Hash Job；控制库应仍只有首次扫描产生的那一个。
	var hashJobCount int
	if err := store.Control.SQL().QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE job_type='hash'").Scan(&hashJobCount); err != nil {
		t.Fatal(err)
	}
	if hashJobCount != 1 {
		t.Fatalf("无变化 incremental 重扫不应新建 Hash Job，实际 hashJobCount=%d", hashJobCount)
	}
}

func TestIncrementalProfileRehashesWhenSizeChanges(t *testing.T) {
	fixture := []byte("incremental profile detects a size change and re-verifies")
	_, _, catalogStore, service, source, store := setupWithHash(t, fixture)
	defer store.Close()
	ctx := context.Background()

	first, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	_, worksBefore, err := catalogStore.ListWorks(ctx)
	if err != nil || len(worksBefore) != 1 {
		t.Fatal(err)
	}
	_, mediaBefore, err := catalogStore.ListMediaForWork(ctx, worksBefore[0].ID)
	if err != nil || len(mediaBefore) != 1 {
		t.Fatal(err)
	}

	mediaPath := filepath.Join(source.RootPath, "work-one", "media.bin")
	if err := os.Chmod(mediaPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mediaPath, append(fixture, []byte(" plus extra bytes changing the size")...), 0o400); err != nil {
		t.Fatal(err)
	}

	second, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	_, worksAfter, err := catalogStore.ListWorks(ctx)
	if err != nil || len(worksAfter) != 1 {
		t.Fatal(err)
	}
	_, mediaAfter, err := catalogStore.ListMediaForWork(ctx, worksAfter[0].ID)
	if err != nil || len(mediaAfter) != 1 {
		t.Fatal(err)
	}
	if mediaAfter[0].Digest == mediaBefore[0].Digest {
		t.Fatalf("大小变化后 digest 应重新确认: before=%s after=%s", mediaBefore[0].Digest, mediaAfter[0].Digest)
	}
	var hashJobCount int
	if err := store.Control.SQL().QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE job_type='hash'").Scan(&hashJobCount); err != nil {
		t.Fatal(err)
	}
	if hashJobCount != 2 {
		t.Fatalf("大小变化应触发第二个 Hash Job，实际 hashJobCount=%d", hashJobCount)
	}
}

func TestIncrementalProfileRehashesWhenMTimeChangesWithSameSize(t *testing.T) {
	fixture := []byte("incremental profile detects a same-size mtime-only change")
	_, _, catalogStore, service, source, store := setupWithHash(t, fixture)
	defer store.Close()
	ctx := context.Background()

	first, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	_, worksBefore, err := catalogStore.ListWorks(ctx)
	if err != nil || len(worksBefore) != 1 {
		t.Fatal(err)
	}
	_, mediaBefore, err := catalogStore.ListMediaForWork(ctx, worksBefore[0].ID)
	if err != nil || len(mediaBefore) != 1 {
		t.Fatal(err)
	}

	// 同大小但内容不同、时间戳被人为改动，模拟"同大小、恢复 mtime"以外的、更常见的
	// "同大小但 mtime 真实前进"场景；size 相同因此必须依赖 mtime 证据触发重新验证。
	mediaPath := filepath.Join(source.RootPath, "work-one", "media.bin")
	replacement := []byte(fixture)
	replacement[0] ^= 0xFF
	if err := os.Chmod(mediaPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mediaPath, replacement, 0o400); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(mediaPath, future, future); err != nil {
		t.Fatal(err)
	}

	second, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	_, worksAfter, err := catalogStore.ListWorks(ctx)
	if err != nil || len(worksAfter) != 1 {
		t.Fatal(err)
	}
	_, mediaAfter, err := catalogStore.ListMediaForWork(ctx, worksAfter[0].ID)
	if err != nil || len(mediaAfter) != 1 {
		t.Fatal(err)
	}
	if len(fixture) != len(replacement) {
		t.Fatal("测试前置条件错误：大小应保持一致")
	}
	if mediaAfter[0].Digest == mediaBefore[0].Digest {
		t.Fatal("mtime 变化后应重新确认 digest（本用例中 mtime 与内容一并改变属预期覆盖面）")
	}
}

func TestVerifyProfileAlwaysRehashesRegardlessOfPriorConfirmation(t *testing.T) {
	fixture := []byte("verify profile ignores prior confirmation and rehashes unconditionally")
	_, _, catalogStore, service, source, store := setupWithHash(t, fixture)
	defer store.Close()
	ctx := context.Background()

	first, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, first.ID); err != nil {
		t.Fatal(err)
	}

	// 文件完全未变化；incremental 档案会复用，而 verify 档案必须无条件重新哈希。
	second, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileVerify)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	_, works, err := catalogStore.ListWorks(ctx)
	if err != nil || len(works) != 1 {
		t.Fatal(err)
	}
	_, mediaItems, err := catalogStore.ListMediaForWork(ctx, works[0].ID)
	if err != nil || len(mediaItems) != 1 || mediaItems[0].Digest == "" {
		t.Fatalf("verify 档案应产生已确认 digest: %+v %v", mediaItems, err)
	}
	var hashJobCount int
	if err := store.Control.SQL().QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE job_type='hash'").Scan(&hashJobCount); err != nil {
		t.Fatal(err)
	}
	if hashJobCount != 2 {
		t.Fatalf("verify 档案应对未变化文件也重新建立 Hash Job，实际 hashJobCount=%d", hashJobCount)
	}
}

func TestDefaultScanProfileSelectsIndexWhenSourceHasNoPublication(t *testing.T) {
	fixture := []byte("default profile selects index for a never-published source")
	_, _, catalogStore, service, source, store := setupWithHash(t, fixture)
	defer store.Close()
	ctx := context.Background()

	job, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", "")
	if err != nil {
		t.Fatal(err)
	}
	var persisted scanProfileRequest
	if err := json.Unmarshal(job.RequestJSON, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.ScanProfile != scanner.ScanProfileIndex {
		t.Fatalf("尚无 publication 的 Source 未显式指定档案时应自动选择 index，实际持久化 %q", persisted.ScanProfile)
	}
	if err := service.Execute(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	_, works, err := catalogStore.ListWorks(ctx)
	if err != nil || len(works) != 1 {
		t.Fatal(err)
	}
	_, mediaItems, err := catalogStore.ListMediaForWork(ctx, works[0].ID)
	if err != nil || len(mediaItems) != 1 || mediaItems[0].ContentVerificationState != catalog.ContentVerificationStateLocatedUnverified {
		t.Fatalf("默认自动选择的 index 未发布 located_unverified 媒体: %+v %v", mediaItems, err)
	}
}

// TestDefaultScanProfileSelectsIncrementalWhenSourceAlreadyPublished 同时覆盖「index → 默认
// incremental 能正常完成内容确认」：Source 首次以 index 发布后，未显式指定档案的下一次
// 扫描必须自动选择 incremental 并对 located_unverified 媒体建立 Hash Job 完成确认。
func TestDefaultScanProfileSelectsIncrementalWhenSourceAlreadyPublished(t *testing.T) {
	fixture := []byte("default profile selects incremental once a source has already published")
	_, _, catalogStore, service, source, store := setupWithHash(t, fixture)
	defer store.Close()
	ctx := context.Background()

	first, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIndex)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, first.ID); err != nil {
		t.Fatal(err)
	}

	second, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", "")
	if err != nil {
		t.Fatal(err)
	}
	var persisted scanProfileRequest
	if err := json.Unmarshal(second.RequestJSON, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.ScanProfile != scanner.ScanProfileIncremental {
		t.Fatalf("已有 publication 的 Source 未显式指定档案时应自动选择 incremental，实际持久化 %q", persisted.ScanProfile)
	}
	if err := service.Execute(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	_, works, err := catalogStore.ListWorks(ctx)
	if err != nil || len(works) != 1 {
		t.Fatal(err)
	}
	_, mediaItems, err := catalogStore.ListMediaForWork(ctx, works[0].ID)
	if err != nil || len(mediaItems) != 1 || mediaItems[0].ContentVerificationState != catalog.ContentVerificationStateContentVerified || mediaItems[0].Digest == "" {
		t.Fatalf("index → 默认 incremental 未完成内容确认: %+v %v", mediaItems, err)
	}
	var hashJobCount int
	if err := store.Control.SQL().QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE job_type='hash'").Scan(&hashJobCount); err != nil {
		t.Fatal(err)
	}
	if hashJobCount != 1 {
		t.Fatalf("首次 located_unverified 媒体在默认 incremental 下应建立一个 Hash Job，实际 hashJobCount=%d", hashJobCount)
	}
}

func TestInvalidScanProfileValueReturnsValidationErrorWithoutCreatingJob(t *testing.T) {
	fixture := []byte("an invalid scan profile value must be rejected without creating any job")
	_, jobStore, _, service, source, store := setupWithHash(t, fixture)
	defer store.Close()
	ctx := context.Background()

	_, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", "incrementall")
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeValidation {
		t.Fatalf("拼写错误的 scanProfile 应返回结构化 VALIDATION_ERROR: %v", err)
	}
	all, err := jobStore.ListByStatuses(ctx, jobs.StatusQueued, jobs.StatusRunning, jobs.StatusPublishing,
		jobs.StatusCompleted, jobs.StatusFailed, jobs.StatusCancelled, jobs.StatusNeedsRepair)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("非法 scanProfile 不应创建任何 Job: %+v", all)
	}
}

// TestExplicitIndexRejectedWhenSourceAlreadyPublished 覆盖「已有 publication 后显式 index
// 被拒绝」与「拒绝后原 publication、Binding、Overlay 均不变化」：阶段 1 的 SourceWork 拆分/
// 合并检测依赖 ContentBlob digest 证据，index 不产生完整哈希，对已发布 Source 允许显式
// index 会绕过该结构审查，因此必须拒绝且不创建 Job、不修改 Binding/Catalog。
func TestExplicitIndexRejectedWhenSourceAlreadyPublished(t *testing.T) {
	fixture := []byte("explicit index on an already published source must be rejected")
	resources, jobStore, catalogStore, service, source, store := setupWithHash(t, fixture)
	defer store.Close()
	ctx := context.Background()

	first, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	publicationBefore, err := catalogStore.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bindingBefore, err := resources.BindingForSource(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIndex)
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeConflict {
		t.Fatalf("已有 publication 的 Source 显式 index 应被拒绝为结构化冲突: %v", err)
	}

	publicationAfter, err := catalogStore.Current(ctx)
	if err != nil || publicationAfter.ID != publicationBefore.ID || publicationAfter.OverlayRevisionID != publicationBefore.OverlayRevisionID {
		t.Fatalf("拒绝后 publication/Overlay 发生变化: before=%+v after=%+v err=%v", publicationBefore, publicationAfter, err)
	}
	bindingAfter, err := resources.BindingForSource(ctx, source.ID)
	if err != nil || bindingAfter.SemanticHash != bindingBefore.SemanticHash || bindingAfter.RuleIRHash != bindingBefore.RuleIRHash {
		t.Fatalf("拒绝后 Binding 发生变化: before=%+v after=%+v err=%v", bindingBefore, bindingAfter, err)
	}
	all, err := jobStore.ListByStatuses(ctx, jobs.StatusQueued, jobs.StatusRunning, jobs.StatusPublishing,
		jobs.StatusCompleted, jobs.StatusFailed, jobs.StatusCancelled, jobs.StatusNeedsRepair)
	if err != nil {
		t.Fatal(err)
	}
	scanJobs := 0
	for _, job := range all {
		if job.Type == "scan" {
			scanJobs++
		}
	}
	if scanJobs != 1 {
		t.Fatalf("被拒绝的显式 index 请求不应创建新 Job，实际 scan Job 数=%d", scanJobs)
	}
}

// TestIncrementalDoesNotReuseDigestAfterFileChangesBetweenDiscoveryAndReuse 是 TOCTOU 回归
// 测试：discovery 阶段的 Stat 与准备复用既往摘要之间存在窗口，文件在此窗口内被替换时不得
// 错误复用旧 digest，必须降级为新的 Hash Job 并确认当前真实内容。
func TestIncrementalDoesNotReuseDigestAfterFileChangesBetweenDiscoveryAndReuse(t *testing.T) {
	fixture := []byte("toctou fixture before the reuse decision replaces this content")
	_, _, catalogStore, service, source, store := setupWithHash(t, fixture)
	defer store.Close()
	ctx := context.Background()

	first, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	_, worksBefore, err := catalogStore.ListWorks(ctx)
	if err != nil || len(worksBefore) != 1 {
		t.Fatal(err)
	}
	_, mediaBefore, err := catalogStore.ListMediaForWork(ctx, worksBefore[0].ID)
	if err != nil || len(mediaBefore) != 1 || mediaBefore[0].Digest == "" {
		t.Fatalf("首次扫描未建立已确认 digest: %+v %v", mediaBefore, err)
	}

	mediaPath := filepath.Join(source.RootPath, "work-one", "media.bin")
	replacement := []byte("replaced strictly between discovery and the reuse decision window!!")
	if len(replacement) == len(fixture) {
		t.Fatal("测试前置条件错误：替换内容大小需与原文件不同，以避免与已知同大小限制混淆")
	}
	service.SetPreReuseHook(func(relativePath string) {
		if relativePath != "work-one/media.bin" {
			return
		}
		if err := os.Chmod(mediaPath, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(mediaPath, replacement, 0o400); err != nil {
			t.Fatal(err)
		}
	})

	second, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	_, worksAfter, err := catalogStore.ListWorks(ctx)
	if err != nil || len(worksAfter) != 1 {
		t.Fatal(err)
	}
	_, mediaAfter, err := catalogStore.ListMediaForWork(ctx, worksAfter[0].ID)
	if err != nil || len(mediaAfter) != 1 {
		t.Fatal(err)
	}
	if mediaAfter[0].Digest == mediaBefore[0].Digest {
		t.Fatalf("discovery 之后、复用之前文件已变化，不应复用旧 digest: before=%s after=%s", mediaBefore[0].Digest, mediaAfter[0].Digest)
	}
	expectedDigest := domain.NewSHA256BlobRef(sha256.Sum256(replacement)).Digest
	if mediaAfter[0].Digest != expectedDigest {
		t.Fatalf("重新确认的 digest 应反映复用决策窗口内的真实当前内容: got=%s want=%s", mediaAfter[0].Digest, expectedDigest)
	}
	var hashJobCount int
	if err := store.Control.SQL().QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE job_type='hash'").Scan(&hashJobCount); err != nil {
		t.Fatal(err)
	}
	if hashJobCount != 2 {
		t.Fatalf("复用前证据变化应降级为新的 Hash Job，实际 hashJobCount=%d", hashJobCount)
	}
}

// TestIncrementalReuseKeepsPriorConfirmationTimeVerifyAdvancesIt 覆盖「复用保持旧时间、
// verify 更新确认时间」：无变化的 incremental 重扫不得把 verifiedAt/last_confirmed_at 推进
// 到复用发生的时刻；显式 verify 重新完成完整哈希后必须把确认时间推进到本次确认的时刻。
func TestIncrementalReuseKeepsPriorConfirmationTimeVerifyAdvancesIt(t *testing.T) {
	fixture := []byte("confirmation time must be preserved across an unchanged incremental rescan")
	_, _, catalogStore, service, source, store := setupWithHash(t, fixture)
	defer store.Close()
	ctx := context.Background()

	testClock := newStepClock(time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC))
	service.SetClock(testClock)

	first, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	_, worksBefore, err := catalogStore.ListWorks(ctx)
	if err != nil || len(worksBefore) != 1 {
		t.Fatal(err)
	}
	_, mediaBefore, err := catalogStore.ListMediaForWork(ctx, worksBefore[0].ID)
	if err != nil || len(mediaBefore) != 1 || mediaBefore[0].VerifiedAt.IsZero() {
		t.Fatalf("首次确认未记录 verifiedAt: %+v %v", mediaBefore, err)
	}
	firstConfirmedAt := mediaBefore[0].VerifiedAt

	// 时钟前进，模拟真实世界流逝的时间；无变化的复用扫描不应把确认时间推进到这个新时刻。
	testClock.Advance(24 * time.Hour)

	second, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	_, worksAfter, err := catalogStore.ListWorks(ctx)
	if err != nil || len(worksAfter) != 1 {
		t.Fatal(err)
	}
	_, mediaAfter, err := catalogStore.ListMediaForWork(ctx, worksAfter[0].ID)
	if err != nil || len(mediaAfter) != 1 {
		t.Fatal(err)
	}
	if !mediaAfter[0].VerifiedAt.Equal(firstConfirmedAt) {
		t.Fatalf("无变化 incremental 复用不应更新确认时间: before=%v after=%v", firstConfirmedAt, mediaAfter[0].VerifiedAt)
	}

	third, err := service.CreateScanWithProfile(ctx, source.ID, "personal-owner", "", scanner.ScanProfileVerify)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, third.ID); err != nil {
		t.Fatal(err)
	}
	_, worksThird, err := catalogStore.ListWorks(ctx)
	if err != nil || len(worksThird) != 1 {
		t.Fatal(err)
	}
	_, mediaThird, err := catalogStore.ListMediaForWork(ctx, worksThird[0].ID)
	if err != nil || len(mediaThird) != 1 {
		t.Fatal(err)
	}
	if !mediaThird[0].VerifiedAt.Equal(testClock.Now()) || mediaThird[0].VerifiedAt.Equal(firstConfirmedAt) {
		t.Fatalf("verify 档案应把确认时间推进到重新完成哈希的时刻: got=%v firstConfirmedAt=%v want=%v",
			mediaThird[0].VerifiedAt, firstConfirmedAt, testClock.Now())
	}
}

type scanProfileRequest struct {
	ScanProfile string `json:"scanProfile,omitempty"`
}
