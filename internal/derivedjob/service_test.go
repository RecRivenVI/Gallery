package derivedjob_test

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func (resolver) Resolve(context.Context, string, string) (derived.Generator, error) {
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
