package stage3_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/hashjob"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/recovery"
	"github.com/RecRivenVI/gallery/internal/scanner"
	"github.com/RecRivenVI/gallery/internal/storage"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/sourceguard"
)

// TestStage3ProductionContractsSmoke 只通过阶段 3 已公开的生产服务契约，验证三个扫描档案
// 的 publication 语义、同一持久 Job 的 Attempt 恢复，以及整个流程对 Source 零写入。
func TestStage3ProductionContractsSmoke(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "source")
	mediaBody := []byte("stage3 testlab synthetic media body\n")
	if err := os.MkdirAll(filepath.Join(sourceRoot, "work-one"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "work-one", "001.png"), mediaBody, 0o400); err != nil {
		t.Fatal(err)
	}
	sourceBefore, err := sourceguard.Walk(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}

	dirs := appdirs.UnderRoot(filepath.Join(root, "appdirs"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("关闭 testlab 数据库: %v", err)
		}
	})

	manualClock := clock.NewManual(time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC))
	ids := identity.NewGenerator(manualClock)
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, manualClock, ids)
	if err != nil {
		t.Fatal(err)
	}
	library, err := resources.CreateLibrary(ctx, "Stage 3 Testlab")
	if err != nil {
		t.Fatal(err)
	}
	source, err := resources.CreateSource(ctx, library.ID, "Synthetic Source", sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	rulePackage, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "rules", "Venera", "bounded-subdir-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	ruleVersion, err := resources.CreateRuleVersion(ctx, rulePackage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resources.CreateSourceRuleBinding(ctx, source.ID, ruleVersion.SemanticHash, []byte("{}"), 0); err != nil {
		t.Fatal(err)
	}

	jobStore, err := jobs.NewStore(store.Control.SQL(), manualClock, ids)
	if err != nil {
		t.Fatal(err)
	}
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), manualClock, ids)
	if err != nil {
		t.Fatal(err)
	}
	scanService, err := scanner.New(ctx, resources, jobStore, catalogStore, nil)
	if err != nil {
		t.Fatal(err)
	}
	scanService.SetClock(manualClock)
	hashService, err := hashjob.New(ctx, resources, jobStore)
	if err != nil {
		t.Fatal(err)
	}
	scanService.SetHashService(hashService)

	indexJob := executeScan(t, ctx, scanService, jobStore, source.ID, scanner.ScanProfileIndex)
	indexPublication, indexWork, indexMedia := currentSingleMedia(t, ctx, catalogStore)
	if indexJob.PublicationID != indexPublication.ID {
		t.Fatalf("index Job 与 active publication 不一致: job=%s active=%s", indexJob.PublicationID, indexPublication.ID)
	}
	if indexMedia.LocationStatus != "present" || indexMedia.ContentVerificationState != catalog.ContentVerificationStateLocatedUnverified ||
		indexMedia.Algorithm != "" || indexMedia.Digest != "" || !indexMedia.VerifiedAt.IsZero() {
		t.Fatalf("index 必须只发布已定位、未确认媒体: %+v", indexMedia)
	}

	manualClock.Advance(time.Hour)
	incrementalJob := executeScan(t, ctx, scanService, jobStore, source.ID, scanner.ScanProfileIncremental)
	incrementalPublication, incrementalWork, incrementalMedia := currentSingleMedia(t, ctx, catalogStore)
	wantDigestBytes := sha256.Sum256(mediaBody)
	wantDigest := hex.EncodeToString(wantDigestBytes[:])
	if incrementalJob.PublicationID != incrementalPublication.ID || incrementalPublication.ID == indexPublication.ID {
		t.Fatalf("incremental 未原子切换到新 publication: index=%s job=%s active=%s",
			indexPublication.ID, incrementalJob.PublicationID, incrementalPublication.ID)
	}
	if incrementalWork.ID != indexWork.ID || incrementalMedia.ID != indexMedia.ID {
		t.Fatalf("incremental 导致 Canonical 身份漂移: work %s -> %s, media %s -> %s",
			indexWork.ID, incrementalWork.ID, indexMedia.ID, incrementalMedia.ID)
	}
	if incrementalMedia.ContentVerificationState != catalog.ContentVerificationStateContentVerified ||
		incrementalMedia.Algorithm != "sha256-v1" || incrementalMedia.Digest != wantDigest ||
		!incrementalMedia.VerifiedAt.Equal(manualClock.Now()) {
		t.Fatalf("incremental 未发布完整 SHA-256 确认结果: %+v", incrementalMedia)
	}
	_, oldIndexMedia, err := catalogStore.ListMediaForWorkAt(ctx, indexPublication.ID, indexWork.ID)
	if err != nil || len(oldIndexMedia) != 1 || oldIndexMedia[0].ContentVerificationState != catalog.ContentVerificationStateLocatedUnverified ||
		oldIndexMedia[0].Digest != "" || !oldIndexMedia[0].VerifiedAt.IsZero() {
		t.Fatalf("新 publication 覆盖了旧 index 快照: media=%+v err=%v", oldIndexMedia, err)
	}

	incrementalVerifiedAt := incrementalMedia.VerifiedAt
	manualClock.Advance(time.Hour)
	verifyJob := executeScan(t, ctx, scanService, jobStore, source.ID, scanner.ScanProfileVerify)
	verifyPublication, verifyWork, verifyMedia := currentSingleMedia(t, ctx, catalogStore)
	if verifyJob.PublicationID != verifyPublication.ID || verifyPublication.ID == incrementalPublication.ID {
		t.Fatalf("verify 未原子切换到新 publication: incremental=%s job=%s active=%s",
			incrementalPublication.ID, verifyJob.PublicationID, verifyPublication.ID)
	}
	if verifyWork.ID != indexWork.ID || verifyMedia.ID != indexMedia.ID || verifyMedia.Digest != wantDigest {
		t.Fatalf("verify 重新确认后身份或摘要漂移: work=%+v media=%+v", verifyWork, verifyMedia)
	}
	if verifyMedia.ContentVerificationState != catalog.ContentVerificationStateContentVerified ||
		!verifyMedia.VerifiedAt.Equal(manualClock.Now()) || !verifyMedia.VerifiedAt.After(incrementalVerifiedAt) {
		t.Fatalf("verify 必须真正推进完整确认时间: incremental=%v verify=%v now=%v",
			incrementalVerifiedAt, verifyMedia.VerifiedAt, manualClock.Now())
	}
	_, oldIncrementalMedia, err := catalogStore.ListMediaForWorkAt(ctx, incrementalPublication.ID, incrementalWork.ID)
	if err != nil || len(oldIncrementalMedia) != 1 || !oldIncrementalMedia[0].VerifiedAt.Equal(incrementalVerifiedAt) {
		t.Fatalf("verify 覆盖了旧 incremental 快照: media=%+v err=%v", oldIncrementalMedia, err)
	}

	// 模拟进程在扫描 Attempt 运行中强杀：Recovery 必须先把旧 Attempt 收敛为 recovered，
	// 再在退避到期后以同一 Job ID 建立 Attempt 2，并可由原 Scanner 契约继续完成发布。
	recoveryJob, err := scanService.CreateScanWithProfile(ctx, source.ID, "stage3-testlab", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobStore.Start(ctx, recoveryJob.ID); err != nil {
		t.Fatal(err)
	}
	manualClock.Advance(3 * time.Minute)
	submitter := &recordingSubmitter{}
	reconciler, err := recovery.New(jobStore, submitter, time.Second, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := reconciler.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	failed, err := jobStore.Get(ctx, recoveryJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	failedAttempts, err := jobStore.ListAttempts(ctx, recoveryJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != jobs.StatusFailed || !failed.FailureRetryable || failed.Attempt != 1 || failed.NextAttemptAt == nil ||
		len(failedAttempts) != 1 || failedAttempts[0].Status != "recovered" ||
		failedAttempts[0].ErrorCode != scanner.IssueProcessInterrupted || !failedAttempts[0].ErrorRetryable {
		t.Fatalf("过期 Attempt 未收敛为可解释的 retryable 状态: job=%+v attempts=%+v", failed, failedAttempts)
	}
	if len(submitter.calls) != 0 {
		t.Fatalf("退避到期前不应提交恢复 Job: %+v", submitter.calls)
	}

	manualClock.Advance(2 * time.Second)
	if err := reconciler.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	queued, err := jobStore.Get(ctx, recoveryJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	queuedAttempts, err := jobStore.ListAttempts(ctx, recoveryJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if queued.ID != recoveryJob.ID || queued.Status != jobs.StatusQueued || queued.Attempt != 2 || queued.RetryOf != "" ||
		len(queuedAttempts) != 2 || queuedAttempts[0].Status != "recovered" || queuedAttempts[1].Status != "queued" {
		t.Fatalf("Recovery 未以同一 Job ID 建立 Attempt 2: job=%+v attempts=%+v", queued, queuedAttempts)
	}
	if len(submitter.calls) != 1 || submitter.calls[0].class != jobs.ResourceScan || submitter.calls[0].jobID != recoveryJob.ID {
		t.Fatalf("恢复 Job 未交回 scan 调度类别: %+v", submitter.calls)
	}
	if err := scanService.Execute(ctx, recoveryJob.ID); err != nil {
		t.Fatal(err)
	}
	recovered, err := jobStore.Get(ctx, recoveryJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	recoveredAttempts, err := jobStore.ListAttempts(ctx, recoveryJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != jobs.StatusCompleted || recovered.Attempt != 2 || recovered.PublicationID == "" ||
		len(recoveredAttempts) != 2 || recoveredAttempts[0].Status != "recovered" || recoveredAttempts[1].Status != "completed" {
		t.Fatalf("恢复后的 Attempt 2 未完成 publication: job=%+v attempts=%+v", recovered, recoveredAttempts)
	}
	_, recoveredWork, recoveredMedia := currentSingleMedia(t, ctx, catalogStore)
	if recoveredWork.ID != indexWork.ID || recoveredMedia.ID != indexMedia.ID || recoveredMedia.Digest != wantDigest ||
		!recoveredMedia.VerifiedAt.Equal(verifyMedia.VerifiedAt) {
		t.Fatalf("恢复后的 incremental 未复用稳定身份与已确认内容: work=%+v media=%+v", recoveredWork, recoveredMedia)
	}

	sourceAfter, err := sourceguard.Walk(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !sourceBefore.Equal(sourceAfter) {
		t.Fatalf("阶段 3 smoke 对 Source 产生了写入: before=%s after=%s", sourceBefore.GuardSHA256, sourceAfter.GuardSHA256)
	}
}

func executeScan(t *testing.T, ctx context.Context, service *scanner.Service, store *jobs.Store, sourceID, profile string) jobs.Job {
	t.Helper()
	job, err := service.CreateScanWithProfile(ctx, sourceID, "stage3-testlab", "", profile)
	if err != nil {
		t.Fatal(err)
	}
	var request struct {
		ScanProfile string `json:"scanProfile"`
	}
	if err := json.Unmarshal(job.RequestJSON, &request); err != nil || request.ScanProfile != profile {
		t.Fatalf("扫描档案未冻结进持久 Job: profile=%s request=%s err=%v", profile, job.RequestJSON, err)
	}
	if err := service.Execute(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	completed, err := store.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	attempts, err := store.ListAttempts(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != jobs.StatusCompleted || completed.PublicationID == "" || completed.Attempt != 1 ||
		len(attempts) != 1 || attempts[0].Status != "completed" {
		t.Fatalf("扫描 Job/Attempt 未随 publication 一起完成: job=%+v attempts=%+v", completed, attempts)
	}
	return completed
}

func currentSingleMedia(t *testing.T, ctx context.Context, store *catalog.Store) (catalog.Publication, catalog.Work, catalog.Media) {
	t.Helper()
	publication, works, err := store.ListWorks(ctx)
	if err != nil || len(works) != 1 {
		t.Fatalf("当前 publication 应恰有一个 Work: publication=%+v works=%+v err=%v", publication, works, err)
	}
	mediaPublication, mediaItems, err := store.ListMediaForWork(ctx, works[0].ID)
	if err != nil || len(mediaItems) != 1 {
		t.Fatalf("当前 Work 应恰有一个 Media: publication=%+v media=%+v err=%v", mediaPublication, mediaItems, err)
	}
	if mediaPublication.ID != publication.ID {
		t.Fatalf("Work/Media 查询未落在同一 publication: works=%s media=%s", publication.ID, mediaPublication.ID)
	}
	return publication, works[0], mediaItems[0]
}

type submittedCall struct {
	class string
	jobID string
}

type recordingSubmitter struct {
	calls []submittedCall
}

func (s *recordingSubmitter) Submit(class, jobID string) bool {
	s.calls = append(s.calls, submittedCall{class: class, jobID: jobID})
	return true
}
