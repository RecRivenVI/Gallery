package maintenance_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/derived"
	"github.com/RecRivenVI/gallery/internal/derivedjob"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/maintenance"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/ports"
	"github.com/RecRivenVI/gallery/internal/storage"
)

type spaceChecker struct{ free int64 }

func (s spaceChecker) FreeBytes(string) (int64, error) { return s.free, nil }

func TestPreflightRejectsInsufficientAppDirsSpace(t *testing.T) {
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 5, 0, 0, 0, time.UTC)}
	ids := identity.NewGenerator(now)
	jobStore, err := jobs.NewStore(store.Control.SQL(), now, ids)
	if err != nil {
		t.Fatal(err)
	}
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), now, ids)
	if err != nil {
		t.Fatal(err)
	}
	service, err := maintenance.New(context.Background(), store.Control.SQL(), catalogStore, jobStore, nil, dirs, spaceChecker{free: 10}, now)
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Preflight(context.Background(), 11)
	if err == nil || report.Sufficient {
		t.Fatalf("空间不足未被拒绝: %+v %v", report, err)
	}
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeDiskSpaceInsufficient {
		t.Fatalf("空间不足错误码错误: %v", err)
	}
	if report.FreeBytes != 10 || report.RequiredBytes != 11 {
		t.Fatalf("空间报告不完整: %+v", report)
	}
	estimate, estimateErr := service.Estimate(context.Background(), "catalog_vacuum")
	if estimateErr == nil || estimate.Operation != "catalog_vacuum" || !estimate.Conservative ||
		estimate.RequiredBytes <= 11 || estimate.FreeBytes != 10 {
		t.Fatalf("服务端保守估算错误: %+v %v", estimate, estimateErr)
	}
	if _, createErr := service.CreateGC(context.Background(), "owner", maintenance.Request{}); createErr == nil {
		t.Fatal("空间不足仍创建了维护 Job")
	}
	var count int
	if err := store.Control.SQL().QueryRow("SELECT COUNT(*) FROM jobs").Scan(&count); err != nil || count != 0 {
		t.Fatalf("空间预检失败污染了 Job 表: count=%d err=%v", count, err)
	}
}

var _ ports.SpaceChecker = spaceChecker{}

type maintenanceGCResolver struct{}

func (maintenanceGCResolver) Resolve(context.Context, string, string, domain.ContentBlobRef) (derived.Generator, error) {
	return func(_ context.Context, output io.Writer) (string, error) {
		_, err := io.WriteString(output, "derived-output")
		return "image/png", err
	}, nil
}

// maintenanceGCFailingResolver 模拟 Resolve 在真正生成时失败，用于把 Derived Job 驱动进
// failed 状态而不真正写出结果，覆盖退避等待期与永久失败两类保护边界。
type maintenanceGCFailingResolver struct{ err error }

func (f maintenanceGCFailingResolver) Resolve(context.Context, string, string, domain.ContentBlobRef) (derived.Generator, error) {
	return nil, f.err
}

// insertGCEligibleRevision 构造一个满足 GC 全部其它回收条件（非 active、超过任意保留期、
// 无查询游标租约）的旧 catalog_revision，并写入一个持有目标 digest 的 ContentBlob 行。
// 唯一可能阻止回收的因素只剩下 blob_read_leases 或调用方显式声明的 ProtectedBlobs。
func insertGCEligibleRevision(t *testing.T, db *sql.DB, revisionID, jobID, digest string) {
	t.Helper()
	if _, err := db.Exec(
		"INSERT INTO catalog_revisions VALUES (?, ?, 'src_derived_gc', 'published', 1, 1)", revisionID, jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		"INSERT INTO content_blobs VALUES (?, 'sha256-v1', ?, 1)", revisionID, digest); err != nil {
		t.Fatal(err)
	}
}

func revisionPresent(t *testing.T, db *sql.DB, revisionID string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(
		"SELECT count(*) FROM catalog_revisions WHERE catalog_revision_id=?", revisionID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count == 1
}

// TestRunGCProtectsFixedShareBlobUntilRevoked 覆盖固定媒体 Share 的核心语义：只要
// Share 仍有效，它固定的完整摘要就必须跨 Catalog revision 保留；吊销后则恢复正常 GC。
func TestRunGCProtectsFixedShareBlobUntilRevoked(t *testing.T) {
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
	clk := clock.NewManual(time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC))
	ids := identity.NewGenerator(clk)
	jobStore, err := jobs.NewStore(store.Control.SQL(), clk, ids)
	if err != nil {
		t.Fatal(err)
	}
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), clk, ids)
	if err != nil {
		t.Fatal(err)
	}
	maintenanceService, err := maintenance.New(ctx, store.Control.SQL(), catalogStore, jobStore, nil, dirs, spaceChecker{free: 1 << 30}, clk)
	if err != nil {
		t.Fatal(err)
	}

	const revisionID = "cat_018f47d2-5c16-7a44-a8a0-0000000000b1"
	digest := strings.Repeat("b", 64)
	insertGCEligibleRevision(t, store.Catalog.SQL(), revisionID, "job_018f47d2-5c16-7a44-a8a0-0000000000b1", digest)
	if _, err := store.Control.SQL().ExecContext(ctx, `INSERT INTO shares
(share_id, secret_hash, secret_prefix, created_by, scope_kind, scope_id,
 permissions_json, fixed_blob_algorithm, fixed_blob_digest, created_at, expires_at)
VALUES (?, ?, ?, 'personal-owner', 'media', ?, '["view"]', 'sha256-v1', ?, ?, ?)`,
		"shr_018f47d2-5c16-7a44-a8a0-0000000000b1", strings.Repeat("a", 64), "share-test",
		"med_018f47d2-5c16-7a44-a8a0-0000000000b1", digest, clk.Now().Unix(), clk.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}

	if _, err := maintenanceService.RunGC(ctx, 0, false); err != nil {
		t.Fatal(err)
	}
	if !revisionPresent(t, store.Catalog.SQL(), revisionID) {
		t.Fatal("有效固定 Share 引用的 Blob 所在 revision 不应被 GC 回收")
	}

	if _, err := store.Control.SQL().ExecContext(ctx,
		"UPDATE shares SET revoked_at=? WHERE share_id=?", clk.Now().Unix(), "shr_018f47d2-5c16-7a44-a8a0-0000000000b1"); err != nil {
		t.Fatal(err)
	}
	if _, err := maintenanceService.RunGC(ctx, 0, false); err != nil {
		t.Fatal(err)
	}
	if revisionPresent(t, store.Catalog.SQL(), revisionID) {
		t.Fatal("固定 Share 吊销且无其它引用后不应继续阻止其 Blob 所在 revision 被回收")
	}
}

// TestRunGCProtectsBlobForQueuedDerivedJobBeyondLeaseTTL 覆盖阶段 4 收尾复核发现的核心
// 缺口：media.BlobReadLease 只在 Create 与 Execute 开始时各建立一次固定 5 分钟 TTL 的
// 一次性租约，不依赖"当前 transform 通常很快执行"的假设时，一个 Job 排队等待调度的
// 时间完全可能超过这个 TTL——此时旧机制下租约已经过期，但 Job 依然 queued、随时可能被
// 执行，其引用的 ContentBlob 所在 revision 不应被 GC 回收。
func TestRunGCProtectsBlobForQueuedDerivedJobBeyondLeaseTTL(t *testing.T) {
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
	clk := clock.NewManual(time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC))
	ids := identity.NewGenerator(clk)
	jobStore, err := jobs.NewStore(store.Control.SQL(), clk, ids)
	if err != nil {
		t.Fatal(err)
	}
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), clk, ids)
	if err != nil {
		t.Fatal(err)
	}
	assets, err := derived.New(store.Catalog.SQL(), dirs.Cache, clk, nil)
	if err != nil {
		t.Fatal(err)
	}
	derivedJobs, err := derivedjob.New(jobStore, assets, maintenanceGCResolver{})
	if err != nil {
		t.Fatal(err)
	}
	derivedJobs.SetBlobLeaser(store.Catalog.SQL(), clk)
	const revisionID = "cat_018f47d2-5c16-7a44-a8a0-0000000000a1"
	digest := strings.Repeat("4", 64)
	insertGCEligibleRevision(t, store.Catalog.SQL(), revisionID, "job_018f47d2-5c16-7a44-a8a0-0000000000a1", digest)
	job, err := derivedJobs.Create(ctx, derivedjob.Request{
		BlobAlgorithm: "sha256-v1", BlobDigest: digest, TransformID: "thumbnail", TransformVersion: "v1", Parameters: []byte(`{}`),
	}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != jobs.StatusQueued {
		t.Fatalf("新建 Derived Job 应为 queued: %+v", job)
	}
	// 排队等待调度的时间超过固定 5 分钟 BlobReadLeaseDuration：Job 从未 Execute，Create 时
	// 建立的租约早已过期。
	clk.Advance(10 * time.Minute)
	maintenanceService, err := maintenance.New(ctx, store.Control.SQL(), catalogStore, jobStore, assets, dirs, spaceChecker{free: 1 << 30}, clk)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := maintenanceService.RunGC(ctx, 0, false); err != nil {
		t.Fatal(err)
	}
	if !revisionPresent(t, store.Catalog.SQL(), revisionID) {
		t.Fatal("排队中的 Derived Job 引用的 Blob 所在 revision 不应在固定租约过期后被 GC 回收")
	}
}

// TestRunGCProtectsBlobForRetryPendingDerivedJobBeyondLeaseTTL 覆盖退避等待窗口：Job 因
// 可重试错误失败后短暂处于 status=failed，尚未耗尽重试次数、也尚未到达 next_attempt_at，
// 随时可能被 RequeueDueFailures 重新排队执行。这段等待同样可能超过固定 Blob 租约 TTL。
func TestRunGCProtectsBlobForRetryPendingDerivedJobBeyondLeaseTTL(t *testing.T) {
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
	clk := clock.NewManual(time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC))
	ids := identity.NewGenerator(clk)
	jobStore, err := jobs.NewStore(store.Control.SQL(), clk, ids)
	if err != nil {
		t.Fatal(err)
	}
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), clk, ids)
	if err != nil {
		t.Fatal(err)
	}
	assets, err := derived.New(store.Catalog.SQL(), dirs.Cache, clk, nil)
	if err != nil {
		t.Fatal(err)
	}
	derivedJobs, err := derivedjob.New(jobStore, assets, maintenanceGCFailingResolver{err: errors.New("transient upstream failure")})
	if err != nil {
		t.Fatal(err)
	}
	derivedJobs.SetBlobLeaser(store.Catalog.SQL(), clk)
	const revisionID = "cat_018f47d2-5c16-7a44-a8a0-0000000000a2"
	digest := strings.Repeat("5", 64)
	insertGCEligibleRevision(t, store.Catalog.SQL(), revisionID, "job_018f47d2-5c16-7a44-a8a0-0000000000a2", digest)
	job, err := derivedJobs.Create(ctx, derivedjob.Request{
		BlobAlgorithm: "sha256-v1", BlobDigest: digest, TransformID: "thumbnail", TransformVersion: "v1", Parameters: []byte(`{}`),
	}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := derivedJobs.Execute(ctx, job.ID); err == nil {
		t.Fatal("失败 Resolver 下 Execute 应返回错误")
	}
	failed, err := jobStore.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != jobs.StatusFailed || !failed.FailureRetryable || failed.Attempt > failed.MaxRetries {
		t.Fatalf("Job 应处于尚未耗尽重试次数的可重试 failed 状态: %+v", failed)
	}
	// 推进时钟越过退避等待与固定租约 TTL，但不调用 RequeueDueFailures，模拟仍在退避
	// 窗口内（未到 next_attempt_at）而尚未被重新排队执行的状态。
	clk.Advance(10 * time.Minute)
	maintenanceService, err := maintenance.New(ctx, store.Control.SQL(), catalogStore, jobStore, assets, dirs, spaceChecker{free: 1 << 30}, clk)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := maintenanceService.RunGC(ctx, 0, false); err != nil {
		t.Fatal(err)
	}
	if !revisionPresent(t, store.Catalog.SQL(), revisionID) {
		t.Fatal("退避等待中的 Derived Job 引用的 Blob 所在 revision 不应在固定租约过期后被 GC 回收")
	}
}

// TestRunGCReclaimsBlobAfterDerivedJobPermanentlyFails 是前两个保护测试的对照回归：一旦
// Job 真正进入不可恢复的终态（non-retryable failed），不应被这项新增保护永久锁住资源，
// 其引用的 Blob 在没有其它引用时必须仍能被正常回收。
func TestRunGCReclaimsBlobAfterDerivedJobPermanentlyFails(t *testing.T) {
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
	clk := clock.NewManual(time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC))
	ids := identity.NewGenerator(clk)
	jobStore, err := jobs.NewStore(store.Control.SQL(), clk, ids)
	if err != nil {
		t.Fatal(err)
	}
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), clk, ids)
	if err != nil {
		t.Fatal(err)
	}
	assets, err := derived.New(store.Catalog.SQL(), dirs.Cache, clk, nil)
	if err != nil {
		t.Fatal(err)
	}
	derivedJobs, err := derivedjob.New(jobStore, assets, maintenanceGCFailingResolver{err: fault.New(fault.CodeNotFound, false, nil)})
	if err != nil {
		t.Fatal(err)
	}
	derivedJobs.SetBlobLeaser(store.Catalog.SQL(), clk)
	const revisionID = "cat_018f47d2-5c16-7a44-a8a0-0000000000a3"
	digest := strings.Repeat("6", 64)
	insertGCEligibleRevision(t, store.Catalog.SQL(), revisionID, "job_018f47d2-5c16-7a44-a8a0-0000000000a3", digest)
	job, err := derivedJobs.Create(ctx, derivedjob.Request{
		BlobAlgorithm: "sha256-v1", BlobDigest: digest, TransformID: "thumbnail", TransformVersion: "v1", Parameters: []byte(`{}`),
	}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := derivedJobs.Execute(ctx, job.ID); err == nil {
		t.Fatal("失败 Resolver 下 Execute 应返回错误")
	}
	failed, err := jobStore.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != jobs.StatusFailed || failed.FailureRetryable {
		t.Fatalf("Job 应处于不可重试的永久 failed 状态: %+v", failed)
	}
	clk.Advance(10 * time.Minute)
	maintenanceService, err := maintenance.New(ctx, store.Control.SQL(), catalogStore, jobStore, assets, dirs, spaceChecker{free: 1 << 30}, clk)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := maintenanceService.RunGC(ctx, 0, false); err != nil {
		t.Fatal(err)
	}
	if revisionPresent(t, store.Catalog.SQL(), revisionID) {
		t.Fatal("永久失败且无其它引用的 Derived Job 不应无限期阻止其 Blob 所在 revision 被回收")
	}
}
