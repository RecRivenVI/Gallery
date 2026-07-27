package scanner_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/hashjob"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/media"
	"github.com/RecRivenVI/gallery/internal/scanner"
)

func TestScanDelegatesFullHashToPersistentHashJob(t *testing.T) {
	fixture := []byte("scanner persistent hash delegation")
	resources, jobStore, _, service, source, store := setup(t, fixture)
	defer store.Close()
	hashService, err := hashjob.New(context.Background(), resources, jobStore)
	if err != nil {
		t.Fatal(err)
	}
	service.SetHashService(hashService)
	// 首次扫描无既往 publication 时默认自动选 index（不建立 Hash Job）；本测试要验证
	// Hash Job 委托链路，因此显式请求 incremental。
	job, err := service.CreateScanWithProfile(context.Background(), source.ID, "personal-owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	var hashJobID string
	if err := store.Control.SQL().QueryRowContext(context.Background(), "SELECT job_id FROM jobs WHERE job_type='hash' AND request_json LIKE ?", "%work-one/media.bin%").Scan(&hashJobID); err != nil {
		t.Fatal(err)
	}
	hashJob, err := jobStore.Get(context.Background(), hashJobID)
	if err != nil || hashJob.Status != jobs.StatusCompleted || hashJob.ProgressBytes != int64(len(fixture)) {
		t.Fatalf("扫描未产生已完成持久 Hash Job: %+v %v", hashJob, err)
	}
	attempts, err := jobStore.ListAttempts(context.Background(), hashJobID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "completed" {
		t.Fatalf("Hash Job attempt 未完成: %+v %v", attempts, err)
	}
}

type blockingHashJobs struct {
	store     *jobs.Store
	started   chan string
	cancelled chan string
	cancelErr error
}

func (b *blockingHashJobs) Create(ctx context.Context, request hashjob.Request, createdBy string) (jobs.Job, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return jobs.Job{}, err
	}
	return b.store.CreateWithOptions(ctx, "hash", request.SourceID, createdBy, jobs.CreateOptions{
		ResourceClass: jobs.ResourceHash, TargetResource: request.RelativePath, RequestJSON: payload,
		IdempotencyKey: "hash-cancel:" + request.ParentJobID,
	})
}

func (b *blockingHashJobs) Start(jobID string) {
	if _, err := b.store.StartStage(context.Background(), jobID, "hashing"); err != nil {
		panic(err)
	}
	b.started <- jobID
}

func (*blockingHashJobs) WaitResult(ctx context.Context, _ string) (media.HashResult, error) {
	<-ctx.Done()
	return media.HashResult{}, fault.New(fault.CodeProcessInterrupted, true, ctx.Err())
}

func (b *blockingHashJobs) Cancel(ctx context.Context, jobID string) (jobs.Job, error) {
	if b.cancelErr != nil {
		return jobs.Job{}, b.cancelErr
	}
	if _, err := b.store.RequestCancel(ctx, jobID); err != nil {
		return jobs.Job{}, err
	}
	final, err := b.store.FinalizeCancelled(ctx, jobID)
	if err == nil {
		b.cancelled <- jobID
	}
	return final, err
}

func TestCancellingScanSurfacesHashDescendantCancelFailure(t *testing.T) {
	resources, jobStore, _, service, source, store := setup(t, []byte("scanner cancellation failure"))
	defer store.Close()
	_ = resources
	cancelFailure := errors.New("hash cancel persistence failed")
	hashes := &blockingHashJobs{
		store: jobStore, started: make(chan string, 1), cancelled: make(chan string, 1), cancelErr: cancelFailure,
	}
	service.SetHashService(hashes)
	parent, err := service.CreateScanWithProfile(context.Background(), source.ID, "personal-owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancelRun := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Execute(runCtx, parent.ID) }()
	select {
	case <-hashes.started:
	case <-time.After(3 * time.Second):
		t.Fatal("扫描未启动 Hash 子 Job")
	}
	if _, err := jobStore.RequestCancel(context.Background(), parent.ID); err != nil {
		t.Fatal(err)
	}
	cancelRun()
	select {
	case err := <-done:
		if !errors.Is(err, cancelFailure) {
			t.Fatalf("Scanner 静默吞掉了 Hash 子 Job 取消失败: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("取消扫描后 Execute 未退出")
	}
	stored, err := jobStore.Get(context.Background(), parent.ID)
	if err != nil || stored.Status != jobs.StatusCancelled {
		t.Fatalf("父 Scan 未保持显式取消终态: %+v %v", stored, err)
	}
}

func TestScanShutdownDoesNotPersistUserCancellationToHashDescendant(t *testing.T) {
	resources, jobStore, _, service, source, store := setup(t, []byte("scanner shutdown recovery"))
	defer store.Close()
	_ = resources
	hashes := &blockingHashJobs{store: jobStore, started: make(chan string, 1), cancelled: make(chan string, 1)}
	service.SetHashService(hashes)
	parent, err := service.CreateScanWithProfile(context.Background(), source.ID, "personal-owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancelRun := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Execute(runCtx, parent.ID) }()
	var childID string
	select {
	case childID = <-hashes.started:
	case <-time.After(3 * time.Second):
		t.Fatal("扫描未启动 Hash 子 Job")
	}
	cancelRun()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("进程中断错误地返回成功")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("进程中断后 Scanner Execute 未退出")
	}
	select {
	case cancelledID := <-hashes.cancelled:
		t.Fatalf("进程关闭被错误持久化为用户取消，子 Job=%s", cancelledID)
	default:
	}
	parentStored, err := jobStore.Get(context.Background(), parent.ID)
	if err != nil || parentStored.Status != jobs.StatusFailed || parentStored.CancelRequested ||
		parentStored.IssueCode != string(fault.CodeProcessInterrupted) || !parentStored.FailureRetryable || parentStored.NextAttemptAt == nil {
		t.Fatalf("Scanner shutdown 未保留可恢复 PROCESS_INTERRUPTED: %+v %v", parentStored, err)
	}
	childStored, err := jobStore.Get(context.Background(), childID)
	if err != nil || childStored.Status != jobs.StatusRunning || childStored.CancelRequested {
		t.Fatalf("Scanner shutdown 错误终止了独立 Hash 子 Job: %+v %v", childStored, err)
	}
}

func TestCancellingScanCancelsActiveHashDescendant(t *testing.T) {
	resources, jobStore, _, service, source, store := setup(t, []byte("scanner cancellation source"))
	defer store.Close()
	_ = resources
	hashes := &blockingHashJobs{store: jobStore, started: make(chan string, 1), cancelled: make(chan string, 1)}
	service.SetHashService(hashes)
	parent, err := service.CreateScanWithProfile(context.Background(), source.ID, "personal-owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancelRun := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Execute(runCtx, parent.ID) }()

	var childID string
	select {
	case childID = <-hashes.started:
	case <-time.After(3 * time.Second):
		t.Fatal("扫描未启动 Hash 子 Job")
	}
	if _, err := jobStore.RequestCancel(context.Background(), parent.ID); err != nil {
		t.Fatal(err)
	}
	cancelRun()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("取消扫描错误地返回成功")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("取消扫描后 Execute 未退出")
	}
	select {
	case cancelledID := <-hashes.cancelled:
		if cancelledID != childID {
			t.Fatalf("取消了错误的 Hash 子 Job: %s", cancelledID)
		}
	case <-time.After(time.Second):
		t.Fatal("父 Scan 取消未传播到活动 Hash 子 Job")
	}
	parentStored, err := jobStore.Get(context.Background(), parent.ID)
	if err != nil || parentStored.Status != jobs.StatusCancelled {
		t.Fatalf("父 Scan 未收敛 cancelled: %+v %v", parentStored, err)
	}
	childStored, err := jobStore.Get(context.Background(), childID)
	if err != nil || childStored.Status != jobs.StatusCancelled {
		t.Fatalf("Hash 子 Job 未收敛 cancelled: %+v %v", childStored, err)
	}
	attempts, err := jobStore.ListAttempts(context.Background(), childID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "cancelled" {
		t.Fatalf("Hash 子 Attempt 未收敛 cancelled: %+v %v", attempts, err)
	}
	var publications int
	if err := store.Catalog.SQL().QueryRow("SELECT count(*) FROM query_publications WHERE job_id=?", parent.ID).Scan(&publications); err != nil {
		t.Fatal(err)
	}
	if publications != 0 {
		t.Fatalf("取消扫描仍发布了候选: %d", publications)
	}
}
