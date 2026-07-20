package derivedjob_test

import (
	"context"
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
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/storage"
)

type resolver struct{}

func (resolver) Resolve(context.Context, string, string, domain.ContentBlobRef) (derived.Generator, error) {
	return func(_ context.Context, output io.Writer) (string, error) {
		_, err := io.WriteString(output, "derived-output")
		return "image/png", err
	}, nil
}

func TestExecutePublishesDerivedAssetThroughPersistentJob(t *testing.T) {
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 4, 0, 0, 0, time.UTC)}
	ids := identity.NewGenerator(now)
	jobStore, err := jobs.NewStore(store.Control.SQL(), now, ids)
	if err != nil {
		t.Fatal(err)
	}
	assets, err := derived.New(store.Catalog.SQL(), dirs.Cache, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	service, err := derivedjob.New(jobStore, assets, resolver{})
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.Create(context.Background(), derivedjob.Request{
		BlobAlgorithm: "sha256-v1", BlobDigest: "0000000000000000000000000000000000000000000000000000000000000000",
		TransformID: "thumbnail", TransformVersion: "v1", Parameters: []byte(`{"width":128}`),
	}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	completed, err := jobStore.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != jobs.StatusCompleted || len(completed.ResultJSON) == 0 {
		t.Fatalf("Derived Job 未完成或无结果: %+v", completed)
	}
	if _, err := assets.GetOrCreate(context.Background(), derived.Request{
		Blob:             domain.ContentBlobRef{Algorithm: "sha256-v1", Digest: "0000000000000000000000000000000000000000000000000000000000000000"},
		TransformID:      "thumbnail",
		TransformVersion: "v1",
		Parameters:       []byte(`{"width":128}`),
	}, resolverGenerator()); err != nil {
		t.Fatalf("已发布 DerivedAsset 不可复用: %v", err)
	}
}

func TestCreateRejectsUnavailableResolverWithoutPersistingJob(t *testing.T) {
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
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 5, 0, 0, 0, time.UTC)}
	jobStore, _ := jobs.NewStore(store.Control.SQL(), now, identity.NewGenerator(now))
	assets, _ := derived.New(store.Catalog.SQL(), dirs.Cache, now, nil)
	service, _ := derivedjob.New(jobStore, assets, nil)
	if service.Available() {
		t.Fatal("未配置 resolver 时错误报告为可用")
	}
	_, err = service.Create(ctx, derivedjob.Request{
		BlobAlgorithm: "sha256-v1", BlobDigest: strings.Repeat("0", 64),
		TransformID: "thumbnail", TransformVersion: "v1", Parameters: []byte(`{}`),
	}, "owner")
	if err == nil {
		t.Fatal("未配置 resolver 仍创建了必然失败的 Job")
	}
	var count int
	if err := store.Control.SQL().QueryRowContext(ctx, "SELECT COUNT(*) FROM jobs").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("不可用请求污染了 Job 表: %d", count)
	}
}

func resolverGenerator() derived.Generator {
	return func(_ context.Context, output io.Writer) (string, error) {
		_, err := io.WriteString(output, "derived-output")
		return "image/png", err
	}
}

// failingResolver 模拟 catalog.Store.LocateBlobFile 或 transform 校验在真正生成时返回的
// 结构化失败，用于验证 Execute 是否原样透传该错误的 Code/Retryable，而不是不分青红皂白
// 统一改写成 retryable 的 DERIVED_ASSET_FAILED。
type failingResolver struct{ err error }

func (f failingResolver) Resolve(context.Context, string, string, domain.ContentBlobRef) (derived.Generator, error) {
	return nil, f.err
}

// TestExecutePropagatesResolverFaultCode 覆盖阶段 4 收尾的核心缺口：resolver.Resolve
// 失败时，Job 的最终 IssueCode/FailureRetryable 必须反映 Resolver 实际返回的结构化错误
// （例如内容已经不可解析时的 NOT_FOUND，non-retryable），不得统一压成一个总是 retryable
// 的通用 DERIVED_ASSET_FAILED——那会让一个永久性失败被无谓地反复重试，也会让客户端
// 无法区分"输入已经不存在"与"这次生成偶然失败"。
func TestExecutePropagatesResolverFaultCode(t *testing.T) {
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
	now := clock.Fixed{Time: time.Date(2026, 7, 20, 5, 0, 0, 0, time.UTC)}
	jobStore, err := jobs.NewStore(store.Control.SQL(), now, identity.NewGenerator(now))
	if err != nil {
		t.Fatal(err)
	}
	assets, err := derived.New(store.Catalog.SQL(), dirs.Cache, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	service, err := derivedjob.New(jobStore, assets, failingResolver{err: fault.New(fault.CodeNotFound, false, nil)})
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.Create(ctx, derivedjob.Request{
		BlobAlgorithm: "sha256-v1", BlobDigest: strings.Repeat("a", 64),
		TransformID: "thumbnail", TransformVersion: "v1", Parameters: []byte(`{}`),
	}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, job.ID); err == nil {
		t.Fatal("Resolver 失败时 Execute 应返回错误")
	}
	failed, err := jobStore.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != jobs.StatusFailed {
		t.Fatalf("Job 应进入 failed: %+v", failed)
	}
	if failed.IssueCode != string(fault.CodeNotFound) {
		t.Fatalf("IssueCode 应原样透传 Resolver 的 NOT_FOUND，实际=%s", failed.IssueCode)
	}
	if failed.FailureRetryable {
		t.Fatalf("NOT_FOUND 应保持 Resolver 声明的 non-retryable，不应被统一改写为 retryable")
	}
}

// TestCreateAcquiresBlobReadLeaseProtectingPendingJob 覆盖阶段 4 收尾的核心缺口：
// DerivedAsset Job 创建后、真正 Execute 之前，它引用的 ContentBlob 所在 catalog_revision
// 不得被并发 GarbageCollect 回收——即使该 revision 早已不是 active、也已经过了保留期。
// 这里直接构造一个满足 GC 其它全部回收条件（非 active、超过保留期、无游标租约）的旧
// revision，证明只有真正调用 Create 建立了 media.BlobReadLease 的 digest 才会被保护；
// 未被任何 Job 引用的对照 digest 所在 revision 照常回收。
func TestCreateAcquiresBlobReadLeaseProtectingPendingJob(t *testing.T) {
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
	now := clock.Fixed{Time: time.Date(2026, 7, 20, 6, 0, 0, 0, time.UTC)}
	ids := identity.NewGenerator(now)
	jobStore, err := jobs.NewStore(store.Control.SQL(), now, ids)
	if err != nil {
		t.Fatal(err)
	}
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), now, ids)
	if err != nil {
		t.Fatal(err)
	}
	assets, err := derived.New(store.Catalog.SQL(), dirs.Cache, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	protectedDigest := strings.Repeat("1", 64)
	unreferencedDigest := strings.Repeat("2", 64)
	// 一个早已不是 active、超过任意保留期、没有查询游标租约的旧 revision——GC 唯一可能
	// 跳过它的理由只剩下 blob_read_leases。两个 digest 都放进同一 revision，只为其中
	// 一个通过 derivedjob.Create 建立租约。
	if _, err := store.Catalog.SQL().ExecContext(ctx,
		"INSERT INTO catalog_revisions VALUES ('cat_018f47d2-5c16-7a44-a8a0-000000000099', 'job_018f47d2-5c16-7a44-a8a0-000000000099', 'src_derived_gc', 'published', 1, 1)"); err != nil {
		t.Fatal(err)
	}
	for _, digest := range []string{protectedDigest, unreferencedDigest} {
		if _, err := store.Catalog.SQL().ExecContext(ctx,
			"INSERT INTO content_blobs VALUES ('cat_018f47d2-5c16-7a44-a8a0-000000000099', 'sha256-v1', ?, 1)", digest); err != nil {
			t.Fatal(err)
		}
	}
	service, err := derivedjob.New(jobStore, assets, resolver{})
	if err != nil {
		t.Fatal(err)
	}
	service.SetBlobLeaser(store.Catalog.SQL(), now)
	if _, err := service.Create(ctx, derivedjob.Request{
		BlobAlgorithm: "sha256-v1", BlobDigest: protectedDigest,
		TransformID: "thumbnail", TransformVersion: "v1", Parameters: []byte(`{}`),
	}, "owner"); err != nil {
		t.Fatal(err)
	}
	result, err := catalogStore.GarbageCollect(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.CatalogRevisions != 0 {
		t.Fatalf("持有租约的 revision 不应被回收: %+v", result)
	}
	var stillPresent int
	if err := store.Catalog.SQL().QueryRowContext(ctx,
		"SELECT count(*) FROM catalog_revisions WHERE catalog_revision_id='cat_018f47d2-5c16-7a44-a8a0-000000000099'").Scan(&stillPresent); err != nil {
		t.Fatal(err)
	}
	if stillPresent != 1 {
		t.Fatal("revision 应仍然存在：租约保护了它，即便其中还有一个未被引用的 digest")
	}
}
