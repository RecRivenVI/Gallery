package hashjob_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/hashjob"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/storage"
)

func TestPersistentHashJobStoresAttemptProgressAndResult(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)}
	ids := identity.NewGenerator(now)
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, now, ids)
	if err != nil {
		t.Fatal(err)
	}
	library, err := resources.CreateLibrary(ctx, "hash-job")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(filepath.Join(root, "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("persistent hash payload")
	path := filepath.Join(root, "work", "media.bin")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := resources.CreateSource(ctx, library.ID, "hash-source", root)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	jobStore, err := jobs.NewStore(store.Control.SQL(), now, ids)
	if err != nil {
		t.Fatal(err)
	}
	service, err := hashjob.New(ctx, resources, jobStore)
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.Create(ctx, hashjob.Request{SourceID: source.ID, RelativePath: "work/media.bin", ExpectedSize: info.Size(), ExpectedModTimeNanos: info.ModTime().UnixNano(), HasExpectedIdentity: true}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	service.Start(job.ID)
	result, err := service.WaitResult(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Blob.Algorithm != domain.BlobAlgorithmSHA256V1 || result.Size != int64(len(payload)) || result.RelativePath != "work/media.bin" {
		t.Fatalf("哈希结果错误: %+v", result)
	}
	completed, err := jobStore.Get(ctx, job.ID)
	if err != nil || completed.Status != jobs.StatusCompleted || completed.ProgressBytes != int64(len(payload)) {
		t.Fatalf("Hash Job 未保存终态和字节进度: %+v %v", completed, err)
	}
	attempts, err := jobStore.ListAttempts(ctx, job.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "completed" {
		t.Fatalf("Hash attempt 未收敛: %+v %v", attempts, err)
	}
}

func TestHashIdempotencyIsLimitedToParentScanJob(t *testing.T) {
	ctx, service, jobStore, sourceID, path, cleanup := newHashFixture(t, []byte("same-size-content-A"))
	defer cleanup()
	parents := make([]jobs.Job, 3)
	for index := range parents {
		parent, err := jobStore.CreateWithOptions(ctx, "scan_parent", "", "owner", jobs.CreateOptions{ResourceClass: jobs.ResourceScan})
		if err != nil {
			t.Fatal(err)
		}
		parents[index] = parent
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	request := hashjob.Request{SourceID: sourceID, RelativePath: "work/media.bin", ExpectedSize: info.Size(),
		ExpectedModTimeNanos: info.ModTime().UnixNano(), HasExpectedIdentity: true, ParentJobID: parents[0].ID}
	first, err := service.Create(ctx, request, "owner")
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := service.Create(ctx, request, "owner")
	if err != nil || duplicate.ID != first.ID {
		t.Fatalf("同一父 Scan Job 未复用 Hash Job: first=%s duplicate=%s err=%v", first.ID, duplicate.ID, err)
	}
	service.Start(first.ID)
	firstResult, err := service.WaitResult(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	originalTime := info.ModTime()
	replacement := append([]byte(nil), []byte("same-size-content-A")...)
	replacement[len(replacement)/2] ^= 0x01
	if len(replacement) != int(info.Size()) {
		t.Fatal("测试替换内容长度不一致")
	}
	if err := os.WriteFile(path, replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, originalTime, originalTime); err != nil {
		t.Fatal(err)
	}
	request.ParentJobID = parents[1].ID
	second, err := service.Create(ctx, request, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Fatal("不同父 Scan Job 跨扫描复用了 completed digest")
	}
	service.Start(second.ID)
	secondResult, err := service.WaitResult(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if firstResult.Blob.Digest == secondResult.Blob.Digest {
		t.Fatal("同大小、恢复 mtime 的中间字节替换未得到新 digest")
	}
	pathReplacement := append([]byte(nil), replacement...)
	pathReplacement[1] ^= 0x02
	replacementPath := path + ".replacement"
	if err := os.WriteFile(replacementPath, pathReplacement, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacementPath, originalTime, originalTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacementPath, path); err != nil {
		t.Fatal(err)
	}
	request.ParentJobID = parents[2].ID
	third, err := service.Create(ctx, request, "owner")
	if err != nil {
		t.Fatal(err)
	}
	service.Start(third.ID)
	thirdResult, err := service.WaitResult(ctx, third.ID)
	if err != nil {
		t.Fatal(err)
	}
	if third.ID == second.ID || thirdResult.Blob.Digest == secondResult.Blob.Digest {
		t.Fatal("同大小、同 mtime 的路径替换跨 Scan 复用了旧 digest")
	}
	firstStored, _ := jobStore.Get(ctx, first.ID)
	secondStored, _ := jobStore.Get(ctx, second.ID)
	if firstStored.Status != jobs.StatusCompleted || secondStored.Status != jobs.StatusCompleted {
		t.Fatalf("两个扫描上下文未各自完成完整哈希: first=%s second=%s", firstStored.Status, secondStored.Status)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, pathReplacement) {
		t.Fatalf("Hash Job 修改了 Source: %v %v", got, err)
	}
}

func TestHashProgressIsCoalescedAndFinalBytesAreExact(t *testing.T) {
	payload := bytes.Repeat([]byte{0x5a}, 5<<20)
	ctx, service, jobStore, sourceID, path, cleanup := newHashFixture(t, payload)
	defer cleanup()
	service.SetProgressPolicy(2<<20, time.Hour, time.Hour)
	info, _ := os.Stat(path)
	job, err := service.Create(ctx, hashjob.Request{SourceID: sourceID, RelativePath: "work/media.bin",
		ExpectedSize: info.Size(), ExpectedModTimeNanos: info.ModTime().UnixNano(), HasExpectedIdentity: true}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	service.Start(job.ID)
	if _, err := service.WaitResult(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	completed, _ := jobStore.Get(ctx, job.ID)
	if completed.ProgressBytes != int64(len(payload)) {
		t.Fatalf("最终进度字节错误: %d", completed.ProgressBytes)
	}
	// 初始 sequence=1、Start=1、2MiB/4MiB/终态前刷新共 3 次、Complete=1。
	if completed.ProgressSequence > 6 {
		t.Fatalf("进度仍按每个 1 MiB 分块写 SQLite: sequence=%d", completed.ProgressSequence)
	}
}

type cancelRecordingDispatcher struct {
	cancelled chan string
}

func (d *cancelRecordingDispatcher) Submit(string) bool { return true }
func (d *cancelRecordingDispatcher) Cancel(jobID string) bool {
	d.cancelled <- jobID
	return true
}

func TestCancelPersistsRequestAndCancelsDispatcherContext(t *testing.T) {
	ctx, service, jobStore, sourceID, path, cleanup := newHashFixture(t, []byte("cancel hash payload"))
	defer cleanup()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.Create(ctx, hashjob.Request{SourceID: sourceID, RelativePath: "work/media.bin",
		ExpectedSize: info.Size(), ExpectedModTimeNanos: info.ModTime().UnixNano(), HasExpectedIdentity: true,
		ParentJobID: "job_parent-cancel"}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobStore.StartStage(ctx, job.ID, "hashing"); err != nil {
		t.Fatal(err)
	}
	dispatcher := &cancelRecordingDispatcher{cancelled: make(chan string, 1)}
	service.SetDispatcher(dispatcher)
	cancelling, err := service.Cancel(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelling.Status != jobs.StatusCancelling || !cancelling.CancelRequested {
		t.Fatalf("Hash Job 未持久进入 cancelling: %+v", cancelling)
	}
	select {
	case cancelledID := <-dispatcher.cancelled:
		if cancelledID != job.ID {
			t.Fatalf("取消了错误的调度项: %s", cancelledID)
		}
	case <-time.After(time.Second):
		t.Fatal("Hash Job 持久取消后未取消调度器 context")
	}
	final, err := jobStore.FinalizeCancelled(ctx, job.ID)
	if err != nil || final.Status != jobs.StatusCancelled {
		t.Fatalf("Hash Job 未能收敛 cancelled: %+v %v", final, err)
	}
	// 终态取消幂等返回，不应再次触发 dispatcher 或改写历史。
	if unchanged, err := service.Cancel(ctx, job.ID); err != nil || unchanged.Status != jobs.StatusCancelled {
		t.Fatalf("终态 Hash Job 取消不幂等: %+v %v", unchanged, err)
	}
	select {
	case duplicate := <-dispatcher.cancelled:
		t.Fatalf("终态 Hash Job 再次取消了调度项: %s", duplicate)
	default:
	}
}

func TestCancelStopsRetryPendingHashJob(t *testing.T) {
	ctx, service, jobStore, sourceID, path, cleanup := newHashFixture(t, []byte("retry pending hash payload"))
	defer cleanup()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.Create(ctx, hashjob.Request{SourceID: sourceID, RelativePath: "work/media.bin",
		ExpectedSize: info.Size(), ExpectedModTimeNanos: info.ModTime().UnixNano(), HasExpectedIdentity: true,
		ParentJobID: "job_parent-retry-cancel"}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobStore.StartStage(ctx, job.ID, "hashing"); err != nil {
		t.Fatal(err)
	}
	failed, err := jobStore.FailWithRetryable(ctx, job.ID, "TRANSIENT_TEST", true)
	if err != nil || failed.NextAttemptAt == nil {
		t.Fatalf("Hash Job 未进入 retry backoff: %+v %v", failed, err)
	}
	dispatcher := &cancelRecordingDispatcher{cancelled: make(chan string, 1)}
	service.SetDispatcher(dispatcher)
	cancelled, err := service.Cancel(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != jobs.StatusCancelled || cancelled.NextAttemptAt != nil || cancelled.FailureRetryable {
		t.Fatalf("retry-pending Hash Job 未被取消: %+v", cancelled)
	}
	select {
	case cancelledID := <-dispatcher.cancelled:
		if cancelledID != job.ID {
			t.Fatalf("取消了错误的 retry-pending Hash Job: %s", cancelledID)
		}
	case <-time.After(time.Second):
		t.Fatal("retry-pending Hash Job 未通知调度器取消")
	}
	attempts, err := jobStore.ListAttempts(ctx, job.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "failed" || attempts[0].ErrorCode != "TRANSIENT_TEST" {
		t.Fatalf("取消 retry-pending Hash Job 改写了 Attempt 历史: %+v %v", attempts, err)
	}
}

func TestCancelledParentStopsRecoveredHashBeforeSourceRead(t *testing.T) {
	ctx, service, jobStore, sourceID, path, cleanup := newHashFixture(t, []byte("must not be read"))
	defer cleanup()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := jobStore.CreateWithOptions(ctx, "scan", sourceID, "owner", jobs.CreateOptions{ResourceClass: jobs.ResourceScan})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobStore.RequestCancel(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	child, err := service.Create(ctx, hashjob.Request{SourceID: sourceID, RelativePath: "work/media.bin",
		ExpectedSize: info.Size(), ExpectedModTimeNanos: info.ModTime().UnixNano(), HasExpectedIdentity: true,
		ParentJobID: parent.ID}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	err = service.Execute(ctx, child.ID)
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeProcessInterrupted {
		t.Fatalf("已取消父 Job 的 Hash 未返回 PROCESS_INTERRUPTED: %v", err)
	}
	stored, getErr := jobStore.Get(ctx, child.ID)
	if getErr != nil || stored.Status != jobs.StatusCancelled || stored.ProgressBytes != 0 {
		t.Fatalf("已取消父 Job 的 Hash 未在零读取下收敛: %+v %v", stored, getErr)
	}
	attempts, listErr := jobStore.ListAttempts(ctx, child.ID)
	if listErr != nil || len(attempts) != 1 || attempts[0].Status != "cancelled" {
		t.Fatalf("Hash 子 Attempt 未收敛 cancelled: %+v %v", attempts, listErr)
	}
}

func TestLocalStartAfterWaitRemainsRecoverable(t *testing.T) {
	ctx, service, jobStore, sourceID, path, cleanup := newHashFixture(t, []byte("shutdown gate payload"))
	defer cleanup()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	service.Wait()
	job, err := service.Create(ctx, hashjob.Request{SourceID: sourceID, RelativePath: "work/media.bin",
		ExpectedSize: info.Size(), ExpectedModTimeNanos: info.ModTime().UnixNano(), HasExpectedIdentity: true}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	service.Start(job.ID)
	stored, err := jobStore.Get(ctx, job.ID)
	if err != nil || stored.Status != jobs.StatusFailed || stored.IssueCode != string(fault.CodeProcessInterrupted) ||
		!stored.FailureRetryable || stored.NextAttemptAt == nil {
		t.Fatalf("drain 后的新 Hash Job 未保留为可恢复中断: %+v %v", stored, err)
	}
}

func TestRunningHashObservesParentCancellationAndStopsLocalFallback(t *testing.T) {
	payload := bytes.Repeat([]byte{0x5a}, 32<<20)
	ctx, service, jobStore, sourceID, path, cleanup := newHashFixture(t, payload)
	defer cleanup()
	service.SetProgressPolicy(1<<20, time.Nanosecond, time.Hour)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := jobStore.CreateWithOptions(ctx, "scan_parent", "", "owner", jobs.CreateOptions{ResourceClass: jobs.ResourceScan})
	if err != nil {
		t.Fatal(err)
	}
	child, err := service.Create(ctx, hashjob.Request{SourceID: sourceID, RelativePath: "work/media.bin",
		ExpectedSize: info.Size(), ExpectedModTimeNanos: info.ModTime().UnixNano(), HasExpectedIdentity: true,
		ParentJobID: parent.ID}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	service.Start(child.ID)
	waitForHashProgress(t, jobStore, child.ID)
	if _, err := jobStore.RequestCancel(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	waitCtx, cancelWait := context.WithTimeout(ctx, 5*time.Second)
	defer cancelWait()
	if _, err := service.WaitResult(waitCtx, child.ID); err == nil {
		t.Fatal("运行中父取消后 Hash 子 Job 错误完成")
	}
	service.Wait()
	stored, err := jobStore.Get(ctx, child.ID)
	if err != nil || stored.Status != jobs.StatusCancelled || stored.ProgressBytes >= int64(len(payload)) {
		t.Fatalf("运行中父取消未停止本地 Hash: %+v %v", stored, err)
	}
	attempts, err := jobStore.ListAttempts(ctx, child.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "cancelled" {
		t.Fatalf("运行中父取消未收敛 Hash Attempt: %+v %v", attempts, err)
	}
}

func TestHashShutdownWithoutPersistentCancelRemainsRetryable(t *testing.T) {
	payload := bytes.Repeat([]byte{0x6b}, 32<<20)
	ctx, service, jobStore, sourceID, path, cleanup := newHashFixture(t, payload)
	defer cleanup()
	service.SetProgressPolicy(1<<20, time.Nanosecond, time.Hour)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.Create(ctx, hashjob.Request{SourceID: sourceID, RelativePath: "work/media.bin",
		ExpectedSize: info.Size(), ExpectedModTimeNanos: info.ModTime().UnixNano(), HasExpectedIdentity: true}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancelRun := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- service.Execute(runCtx, job.ID) }()
	waitForHashProgress(t, jobStore, job.ID)
	cancelRun()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Hash shutdown 错误地返回成功")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Hash shutdown 后 Execute 未退出")
	}
	stored, err := jobStore.Get(ctx, job.ID)
	if err != nil || stored.Status != jobs.StatusFailed || stored.CancelRequested ||
		stored.IssueCode != string(fault.CodeProcessInterrupted) || !stored.FailureRetryable || stored.NextAttemptAt == nil {
		t.Fatalf("Hash shutdown 未保留可恢复 PROCESS_INTERRUPTED: %+v %v", stored, err)
	}
	attempts, err := jobStore.ListAttempts(ctx, job.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "failed" ||
		attempts[0].ErrorCode != string(fault.CodeProcessInterrupted) || !attempts[0].ErrorRetryable {
		t.Fatalf("Hash shutdown Attempt 错误: %+v %v", attempts, err)
	}
}

func TestCancelRacingCompletionIsIdempotent(t *testing.T) {
	ctx, service, jobStore, sourceID, path, cleanup := newHashFixture(t, []byte("cancel complete race"))
	defer cleanup()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	for iteration := 0; iteration < 100; iteration++ {
		job, err := service.Create(ctx, hashjob.Request{SourceID: sourceID, RelativePath: "work/media.bin",
			ExpectedSize: info.Size(), ExpectedModTimeNanos: info.ModTime().UnixNano(), HasExpectedIdentity: true}, "owner")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := jobStore.StartStage(ctx, job.ID, "hashing"); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		cancelResult := make(chan error, 1)
		completeResult := make(chan error, 1)
		go func() {
			<-start
			_, err := service.Cancel(ctx, job.ID)
			cancelResult <- err
		}()
		go func() {
			<-start
			_, err := jobStore.CompleteWithResult(ctx, job.ID, []byte(`{}`))
			completeResult <- err
		}()
		close(start)
		if err := <-cancelResult; err != nil {
			t.Fatalf("第 %d 轮 Cancel 将完成竞态误报为失败: %v", iteration, err)
		}
		_ = <-completeResult
		stored, err := jobStore.Get(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Status == jobs.StatusCancelling {
			stored, err = jobStore.FinalizeCancelled(ctx, job.ID)
			if err != nil {
				t.Fatal(err)
			}
		}
		if stored.Status != jobs.StatusCompleted && stored.Status != jobs.StatusCancelled {
			t.Fatalf("第 %d 轮出现非法竞态终态: %+v", iteration, stored)
		}
		attempts, err := jobStore.ListAttempts(ctx, job.ID)
		if err != nil || len(attempts) != 1 ||
			(attempts[0].Status != "completed" && attempts[0].Status != "cancelled") {
			t.Fatalf("第 %d 轮 Attempt 未与 Job 一致: %+v %v", iteration, attempts, err)
		}
	}
}

func waitForHashProgress(t *testing.T, jobStore *jobs.Store, jobID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, err := jobStore.Get(context.Background(), jobID)
		if err != nil {
			t.Fatal(err)
		}
		if job.ProgressBytes > 0 {
			return
		}
		if job.Status == jobs.StatusCompleted || job.Status == jobs.StatusFailed || job.Status == jobs.StatusCancelled {
			t.Fatalf("Hash 在测试可注入取消前已终止: %+v", job)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("Hash 未报告可观察进度")
}

func newHashFixture(t *testing.T, payload []byte) (context.Context, *hashjob.Service, *jobs.Store, string, string, func()) {
	t.Helper()
	ctx := context.Background()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 5, 0, 0, 0, time.UTC)}
	ids := identity.NewGenerator(now)
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, now, ids)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	library, err := resources.CreateLibrary(ctx, "hash-fixture")
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(filepath.Join(root, "work"), 0o700); err != nil {
		store.Close()
		t.Fatal(err)
	}
	filePath := filepath.Join(root, "work", "media.bin")
	if err := os.WriteFile(filePath, payload, 0o600); err != nil {
		store.Close()
		t.Fatal(err)
	}
	source, err := resources.CreateSource(ctx, library.ID, "hash-source", root)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	jobStore, err := jobs.NewStore(store.Control.SQL(), now, ids)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	service, err := hashjob.New(ctx, resources, jobStore)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	return ctx, service, jobStore, source.ID, filePath, func() { _ = store.Close() }
}
