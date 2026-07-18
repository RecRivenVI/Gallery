package hashjob_test

import (
	"bytes"
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

func TestHashIdempotencyIsLimitedToParentScanJob(t *testing.T) {
	ctx, service, jobStore, sourceID, path, cleanup := newHashFixture(t, []byte("same-size-content-A"))
	defer cleanup()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	request := hashjob.Request{SourceID: sourceID, RelativePath: "work/media.bin", ExpectedSize: info.Size(),
		ExpectedModTimeNanos: info.ModTime().UnixNano(), HasExpectedIdentity: true, ParentJobID: "job_parent-scan-a"}
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
	request.ParentJobID = "job_parent-scan-b"
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
	request.ParentJobID = "job_parent-scan-c"
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
