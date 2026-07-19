package jobs_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/recovery"
	"github.com/RecRivenVI/gallery/internal/storage"
)

type mutableClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *mutableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mutableClock) Advance(value time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(value)
	c.mu.Unlock()
}

type recordingSubmitter struct {
	mu      sync.Mutex
	accept  bool
	records []string
}

func (s *recordingSubmitter) Submit(class, jobID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, class+":"+jobID)
	return s.accept
}

func (s *recordingSubmitter) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

func TestPersistentJobTransitionsAndActiveScanConflict(t *testing.T) {
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	fixedClock := clock.Fixed{Time: time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)}
	generator := identity.NewGenerator(fixedClock)
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, fixedClock, generator)
	if err != nil {
		t.Fatal(err)
	}
	library, err := resources.CreateLibrary(context.Background(), "jobs")
	if err != nil {
		t.Fatal(err)
	}
	sourceID := createSource(t, resources, library.ID)
	jobStore, err := jobs.NewStore(store.Control.SQL(), fixedClock, generator)
	if err != nil {
		t.Fatal(err)
	}
	job, err := jobStore.CreateScan(context.Background(), sourceID, "personal-owner", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = jobStore.CreateScan(context.Background(), sourceID, "personal-owner", "")
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeScanAlreadyRunning {
		t.Fatalf("同 Source 并发扫描未冲突: %v", err)
	}
	job, err = jobStore.Start(context.Background(), job.ID)
	if err != nil || job.Status != jobs.StatusRunning {
		t.Fatalf("Start: %+v %v", job, err)
	}
	job, err = jobStore.Progress(context.Background(), job.ID, "hashing", 4, 10)
	if err != nil || job.ProgressSequence != 3 {
		t.Fatalf("Progress: %+v %v", job, err)
	}
	job, err = jobStore.BeginPublishing(context.Background(), job.ID)
	if err != nil || job.Status != jobs.StatusPublishing {
		t.Fatalf("Publishing: %+v %v", job, err)
	}
	publication, err := generator.New(domain.IDQueryPublication)
	if err != nil {
		t.Fatal(err)
	}
	job, err = jobStore.Complete(context.Background(), job.ID, publication.String())
	if err != nil || job.Status != jobs.StatusCompleted {
		t.Fatalf("Complete: %+v %v", job, err)
	}
	reloaded, err := jobStore.Get(context.Background(), job.ID)
	if err != nil || reloaded.PublicationID != publication.String() {
		t.Fatalf("Reload: %+v %v", reloaded, err)
	}
}

func TestScanJobPersistsRuleExecutionSnapshot(t *testing.T) {
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)}
	generator := identity.NewGenerator(now)
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, now, generator)
	if err != nil {
		t.Fatal(err)
	}
	library, err := resources.CreateLibrary(context.Background(), "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	sourceID := createSource(t, resources, library.ID)
	jobStore, err := jobs.NewStore(store.Control.SQL(), now, generator)
	if err != nil {
		t.Fatal(err)
	}
	job, err := jobStore.CreateScanWithRuleSnapshot(context.Background(), sourceID, "owner", "", &jobs.RuleExecutionSnapshot{
		SemanticHash: "semantic", Parameters: []byte(`{"minimumSize":1}`), ParametersHash: "parameters", RuleIRHash: "ir",
		CompilerVersion: "compiler", CELProfileVersion: "cel", ExtensionRegistryVersion: "extensions",
	})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := jobStore.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.RuleSemanticHash != "semantic" || string(reloaded.RuleParameters) != `{"minimumSize":1}` || reloaded.RuleIRHash != "ir" || reloaded.ExtensionRegistryVersion != "extensions" {
		t.Fatalf("规则执行快照未持久化: %+v", reloaded)
	}
}

func TestJobAttemptCancellationRetryAndMonotonicProgress(t *testing.T) {
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC)}
	ids := identity.NewGenerator(now)
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, now, ids)
	if err != nil {
		t.Fatal(err)
	}
	library, err := resources.CreateLibrary(context.Background(), "job-attempt")
	if err != nil {
		t.Fatal(err)
	}
	source, err := createSourceForAttemptTest(t, resources, library.ID)
	if err != nil {
		t.Fatal(err)
	}
	jobStore, err := jobs.NewStore(store.Control.SQL(), now, ids)
	if err != nil {
		t.Fatal(err)
	}
	job, err := jobStore.CreateScanWithOptions(context.Background(), source, "owner", "", nil, jobs.CreateOptions{MaxRetries: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobStore.Start(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := jobStore.ProgressDetailed(context.Background(), job.ID, jobs.ProgressUpdate{Stage: "hashing", Current: 8, Total: 10, Bytes: 80, Unit: "bytes"}); err != nil {
		t.Fatal(err)
	}
	if _, err := jobStore.ProgressDetailed(context.Background(), job.ID, jobs.ProgressUpdate{Stage: "hashing", Current: 7, Total: 10, Bytes: 70, Unit: "bytes"}); err == nil {
		t.Fatal("进度回退未被拒绝")
	} else {
		var structured *fault.Error
		if !errors.As(err, &structured) || structured.Code != fault.CodeJobProgressRegression {
			t.Fatalf("进度回退错误码错误: %v", err)
		}
	}
	failed, err := jobStore.FailWithRetryable(context.Background(), job.ID, "TRANSIENT_TEST", true)
	if err != nil || failed.Status != jobs.StatusFailed || !failed.FailureRetryable {
		t.Fatalf("可重试失败未收敛: %+v %v", failed, err)
	}
	attempts, err := jobStore.ListAttempts(context.Background(), job.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "failed" {
		t.Fatalf("失败 attempt 未落库: %+v %v", attempts, err)
	}
	retry, err := jobStore.Retry(context.Background(), job.ID, "owner")
	if err != nil || retry.ID != job.ID || retry.RetryOf != "" || retry.Attempt != 2 || retry.Status != jobs.StatusQueued {
		t.Fatalf("重试 attempt 错误: %+v %v", retry, err)
	}
	attempts, err = jobStore.ListAttempts(context.Background(), job.ID)
	if err != nil || len(attempts) != 2 || attempts[0].Status != "failed" || attempts[1].Status != "queued" {
		t.Fatalf("同一 Job 的 Attempt 历史错误: %+v %v", attempts, err)
	}
}

func TestMaintenanceJobHasOneActiveInstancePerType(t *testing.T) {
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 2, 0, 0, 0, time.UTC)}
	jobStore, err := jobs.NewStore(store.Control.SQL(), now, identity.NewGenerator(now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobStore.CreateMaintenance(context.Background(), "catalog_gc", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := jobStore.CreateMaintenance(context.Background(), "catalog_gc", "owner"); err == nil {
		t.Fatal("同类维护 Job 未被单活跃约束拒绝")
	} else {
		var structured *fault.Error
		if !errors.As(err, &structured) || structured.Code != fault.CodeJobStateConflict {
			t.Fatalf("维护 Job 冲突错误码错误: %v", err)
		}
	}
}

func TestLeaseRecoveryWaitsForExpiryAndRetriesSameJob(t *testing.T) {
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
	now := &mutableClock{now: time.Date(2026, 7, 18, 3, 0, 0, 0, time.UTC)}
	ids := identity.NewGenerator(now)
	jobStore, err := jobs.NewStore(store.Control.SQL(), now, ids)
	if err != nil {
		t.Fatal(err)
	}
	job, err := jobStore.CreateWithOptions(ctx, "hash", "", "owner", jobs.CreateOptions{
		ResourceClass: jobs.ResourceHash, MaxRetries: 2,
		RetryPolicyJSON: []byte(`{"kind":"fixed","baseMs":1000,"maxMs":1000}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobStore.StartStage(ctx, job.ID, "hashing"); err != nil {
		t.Fatal(err)
	}
	submitter := &recordingSubmitter{accept: true}
	reconciler, err := recovery.New(jobStore, submitter, time.Hour, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now.Advance(time.Minute)
	if err := reconciler.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	stillRunning, _ := jobStore.Get(ctx, job.ID)
	if stillRunning.Status != jobs.StatusRunning || submitter.count() != 0 {
		t.Fatalf("尚未到期的 Attempt 被回收: %+v submits=%d", stillRunning, submitter.count())
	}
	now.Advance(2 * time.Minute)
	if err := reconciler.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, _ := jobStore.Get(ctx, job.ID)
	if recovered.Status != jobs.StatusFailed || recovered.NextAttemptAt == nil {
		t.Fatalf("过期 Attempt 未进入带退避的 retryable 终态: %+v", recovered)
	}
	attempts, _ := jobStore.ListAttempts(ctx, job.ID)
	if len(attempts) != 1 || attempts[0].Status != "recovered" {
		t.Fatalf("过期 Attempt 历史错误: %+v", attempts)
	}
	now.Advance(2 * time.Second)
	if err := reconciler.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	retried, _ := jobStore.Get(ctx, job.ID)
	if retried.ID != job.ID || retried.Attempt != 2 || retried.Status != jobs.StatusQueued || submitter.count() != 1 {
		t.Fatalf("同一 Job 自动重试错误: %+v submits=%d", retried, submitter.count())
	}
	attempts, _ = jobStore.ListAttempts(ctx, job.ID)
	if len(attempts) != 2 || attempts[1].Status != "queued" {
		t.Fatalf("新 Attempt 未保留历史: %+v", attempts)
	}
}

func TestReconcilerRetriesQueuedJobAfterSchedulerRejects(t *testing.T) {
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
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 4, 0, 0, 0, time.UTC)}
	jobStore, _ := jobs.NewStore(store.Control.SQL(), now, identity.NewGenerator(now))
	job, err := jobStore.CreateWithOptions(ctx, "external_tool", "", "owner",
		jobs.CreateOptions{ResourceClass: jobs.ResourceExternalTool})
	if err != nil {
		t.Fatal(err)
	}
	submitter := &recordingSubmitter{accept: false}
	reconciler, _ := recovery.New(jobStore, submitter, time.Hour, time.Minute)
	if err := reconciler.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	first, _ := jobStore.Get(ctx, job.ID)
	if first.Status != jobs.StatusQueued || submitter.count() != 1 {
		t.Fatalf("拒绝提交后持久 Job 状态错误: %+v submits=%d", first, submitter.count())
	}
	submitter.accept = true
	if err := reconciler.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if submitter.count() != 2 {
		t.Fatalf("后续 reconciliation 未重提 queued Job: %d", submitter.count())
	}
}

func TestRecoverySubmitsEveryResourceClassAndSkipsCancelledJob(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, _ := storage.Open(ctx, dirs)
	defer store.Close()
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 5, 0, 0, 0, time.UTC)}
	jobStore, _ := jobs.NewStore(store.Control.SQL(), now, identity.NewGenerator(now))
	resources := []struct{ jobType, class string }{
		{"scan", jobs.ResourceScan}, {"hash", jobs.ResourceHash}, {"overlay_projection", jobs.ResourceOverlay},
		{"derived_asset", jobs.ResourceDerived}, {"external_tool", jobs.ResourceExternalTool},
		{"catalog_gc", jobs.ResourceMaintenance}, {"control_backup", jobs.ResourceMaintenance},
	}
	for index, resource := range resources {
		if _, err := jobStore.CreateWithOptions(ctx, fmt.Sprintf("%s_%d", resource.jobType, index), "", "owner",
			jobs.CreateOptions{ResourceClass: resource.class}); err != nil {
			t.Fatal(err)
		}
	}
	cancelled, _ := jobStore.CreateWithOptions(ctx, "cancelled", "", "owner",
		jobs.CreateOptions{ResourceClass: jobs.ResourceHash})
	if _, err := jobStore.Cancel(ctx, cancelled.ID); err != nil {
		t.Fatal(err)
	}
	submitter := &recordingSubmitter{accept: true}
	reconciler, _ := recovery.New(jobStore, submitter, time.Hour, time.Minute)
	if err := reconciler.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if submitter.count() != len(resources) {
		t.Fatalf("任务类型提交数量错误: want=%d got=%d", len(resources), submitter.count())
	}
}

func TestAutomaticRetryStopsAtConfiguredMaximum(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, _ := storage.Open(ctx, dirs)
	defer store.Close()
	now := &mutableClock{now: time.Date(2026, 7, 18, 6, 0, 0, 0, time.UTC)}
	jobStore, _ := jobs.NewStore(store.Control.SQL(), now, identity.NewGenerator(now))
	job, _ := jobStore.CreateWithOptions(ctx, "hash", "", "owner", jobs.CreateOptions{
		ResourceClass: jobs.ResourceHash, MaxRetries: 1,
		RetryPolicyJSON: []byte(`{"kind":"fixed","baseMs":1,"maxMs":1}`),
	})
	_, _ = jobStore.StartStage(ctx, job.ID, "hashing")
	_, _ = jobStore.FailWithRetryable(ctx, job.ID, "TRANSIENT", true)
	now.Advance(time.Millisecond)
	if _, err := jobStore.RequeueDueFailures(ctx); err != nil {
		t.Fatal(err)
	}
	second, _ := jobStore.StartStage(ctx, job.ID, "hashing")
	if second.Attempt != 2 {
		t.Fatalf("未创建第二个 Attempt: %+v", second)
	}
	failed, err := jobStore.FailWithRetryable(ctx, job.ID, "TRANSIENT", true)
	if err != nil {
		t.Fatal(err)
	}
	if failed.NextAttemptAt != nil {
		t.Fatalf("达到最大重试次数后仍安排退避: %+v", failed)
	}
	now.Advance(time.Hour)
	requeued, err := jobStore.RequeueDueFailures(ctx)
	if err != nil || len(requeued) != 0 {
		t.Fatalf("达到最大重试次数后仍自动重试: %+v %v", requeued, err)
	}
}

func TestRequeueDueFailuresSkipsSourceWithAnotherActiveScan(t *testing.T) {
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
	now := &mutableClock{now: time.Date(2026, 7, 18, 6, 0, 0, 0, time.UTC)}
	generator := identity.NewGenerator(now)
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, now, generator)
	if err != nil {
		t.Fatal(err)
	}
	library, err := resources.CreateLibrary(ctx, "jobs")
	if err != nil {
		t.Fatal(err)
	}
	sourceID := createSource(t, resources, library.ID)
	jobStore, err := jobs.NewStore(store.Control.SQL(), now, generator)
	if err != nil {
		t.Fatal(err)
	}
	// job 先失败并进入退避等待，随后另一个 Job 成为同一 Source 的活跃扫描。
	first, err := jobStore.CreateScan(ctx, sourceID, "personal-owner", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobStore.Start(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := jobStore.FailWithRetryable(ctx, first.ID, "TRANSIENT_TEST", true); err != nil {
		t.Fatal(err)
	}
	now.Advance(time.Hour)
	second, err := jobStore.CreateScan(ctx, sourceID, "personal-owner", "")
	if err != nil {
		t.Fatalf("同一 Source 的第二次扫描在首次失败后应当允许: %v", err)
	}
	// 启动 reconciliation/周期恢复调用 RequeueDueFailures 时，试图把已失败的 first 重新置为
	// queued 会与仍然活跃的 second 冲突；此前该冲突以 CodeInternal 传播，导致恢复流程（进而
	// galleryd 启动本身）整体失败，而不是被优雅跳过。
	requeued, err := jobStore.RequeueDueFailures(ctx)
	if err != nil {
		t.Fatalf("RequeueDueFailures 因活跃 Source 冲突整体失败: %v", err)
	}
	if len(requeued) != 0 {
		t.Fatalf("冲突的 Job 不应被重新排队: %+v", requeued)
	}
	reloadedFirst, err := jobStore.Get(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloadedFirst.Status != jobs.StatusFailed || reloadedFirst.Attempt != 1 {
		t.Fatalf("跳过的 Job 状态被意外修改: %+v", reloadedFirst)
	}
	reloadedSecond, err := jobStore.Get(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloadedSecond.Status != jobs.StatusQueued {
		t.Fatalf("活跃 Job 被意外修改: %+v", reloadedSecond)
	}
}

func createSourceForAttemptTest(t *testing.T, resources *application.Resources, libraryID string) (string, error) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "source")
	if err := (filesystem.OS{}).MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	source, err := resources.CreateSource(context.Background(), libraryID, "source", root)
	return source.ID, err
}

func createSource(t *testing.T, resources *application.Resources, libraryID string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "source")
	if err := (filesystem.OS{}).MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	source, err := resources.CreateSource(context.Background(), libraryID, "source", root)
	if err != nil {
		t.Fatal(err)
	}
	return source.ID
}
