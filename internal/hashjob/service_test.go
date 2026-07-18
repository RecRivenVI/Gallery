package hashjob_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
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
