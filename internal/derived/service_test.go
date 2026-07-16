package derived_test

import (
	"context"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/derived"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/storage"
)

func TestDerivedKeySingleflightVersionOverlayTakeoverAndLeaseGC(t *testing.T) {
	ctx := context.Background()
	now := clock.Fixed{Time: time.Date(2026, 7, 16, 6, 0, 0, 0, time.UTC)}
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	service, err := derived.New(store.Catalog.SQL(), dirs.Cache, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	blobSum := sha256.Sum256([]byte("source blob"))
	overlayHash, err := derived.OverlayInputHash([]byte(`{"crop":{"x":1,"y":2}}`))
	if err != nil {
		t.Fatal(err)
	}
	request := derived.Request{Blob: domain.NewSHA256BlobRef(blobSum), TransformID: "thumbnail",
		TransformVersion: "1", Parameters: []byte(`{"height":200,"width":300}`), OverlayInputHash: overlayHash}
	var generated atomic.Int32
	generator := func(_ context.Context, output io.Writer) (string, error) {
		generated.Add(1)
		time.Sleep(10 * time.Millisecond)
		_, err := output.Write([]byte("thumbnail bytes"))
		return "image/webp", err
	}
	const callers = 8
	assets := make([]derived.Asset, callers)
	errorsSeen := make([]error, callers)
	var wait sync.WaitGroup
	for index := range callers {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			assets[index], errorsSeen[index] = service.GetOrCreate(ctx, request, generator)
		}(index)
	}
	wait.Wait()
	for index := range callers {
		if errorsSeen[index] != nil || assets[index].Key != assets[0].Key {
			t.Fatalf("singleflight 结果不一致: index=%d asset=%+v err=%v", index, assets[index], errorsSeen[index])
		}
	}
	if generated.Load() != 1 {
		t.Fatalf("同 key 重复生成 %d 次", generated.Load())
	}
	canonicalOrder, err := service.GetOrCreate(ctx, derived.Request{Blob: request.Blob,
		TransformID: request.TransformID, TransformVersion: request.TransformVersion,
		Parameters: []byte(`{"width":300,"height":200}`), OverlayInputHash: overlayHash}, generator)
	if err != nil || canonicalOrder.Key != assets[0].Key || generated.Load() != 1 {
		t.Fatalf("参数规范 JSON 未收敛 key: %+v %v generated=%d", canonicalOrder, err, generated.Load())
	}
	version2, err := service.GetOrCreate(ctx, derived.Request{Blob: request.Blob, TransformID: "thumbnail",
		TransformVersion: "2", Parameters: request.Parameters, OverlayInputHash: overlayHash}, generator)
	if err != nil || version2.Key == assets[0].Key {
		t.Fatalf("transform version 未失效旧 key: %+v %v", version2, err)
	}
	changedOverlay, _ := derived.OverlayInputHash([]byte(`{"crop":{"x":2,"y":2}}`))
	overlayAsset, err := service.GetOrCreate(ctx, derived.Request{Blob: request.Blob, TransformID: "thumbnail",
		TransformVersion: "1", Parameters: request.Parameters, OverlayInputHash: changedOverlay}, generator)
	if err != nil || overlayAsset.Key == assets[0].Key {
		t.Fatalf("相关 Overlay 输入未进入 key: %+v %v", overlayAsset, err)
	}
	if generated.Load() != 3 {
		t.Fatalf("版本/Overlay key 生成次数错误: %d", generated.Load())
	}

	assetPath := filepath.Join(dirs.Cache, filepath.FromSlash(assets[0].RelativePath))
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dirs.Data, "catalog.db"), filepath.Join(dirs.Data, "catalog.db-wal"), filepath.Join(dirs.Data, "catalog.db-shm")} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	reopened, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recoveredService, err := derived.New(reopened.Catalog.SQL(), dirs.Cache, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	beforeTakeover := generated.Load()
	recovered, err := recoveredService.GetOrCreate(ctx, request, generator)
	if err != nil || !recovered.TakenOver || recovered.Key != assets[0].Key || generated.Load() != beforeTakeover {
		t.Fatalf("Catalog 丢失后 manifest takeover 失败: %+v %v generated=%d", recovered, err, generated.Load())
	}
	lease, err := recoveredService.Open(ctx, recovered.Key)
	if err != nil {
		t.Fatal(err)
	}
	if err := recoveredService.MarkObsolete(ctx, recovered.Key); err != nil {
		t.Fatal(err)
	}
	removed, err := recoveredService.SweepObsolete(ctx, now.Time.Add(time.Second))
	if err != nil || removed != 0 {
		t.Fatalf("GC 删除了活跃读取: removed=%d err=%v", removed, err)
	}
	content, err := io.ReadAll(lease.File)
	if err != nil || string(content) != "thumbnail bytes" {
		t.Fatalf("租约期间资产不可读: %q %v", content, err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	removed, err = recoveredService.SweepObsolete(ctx, now.Time.Add(time.Second))
	if err != nil || removed != 1 {
		t.Fatalf("租约释放后 GC 未清除 obsolete key: removed=%d err=%v", removed, err)
	}
	if _, err := os.Stat(assetPath); !os.IsNotExist(err) {
		t.Fatalf("GC 后资产仍存在: %v", err)
	}
}

func TestCorruptManifestOrOutputCannotBeTakenOver(t *testing.T) {
	ctx := context.Background()
	now := clock.Fixed{Time: time.Date(2026, 7, 16, 6, 30, 0, 0, time.UTC)}
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	service, _ := derived.New(store.Catalog.SQL(), dirs.Cache, now, nil)
	sum := sha256.Sum256([]byte("blob"))
	request := derived.Request{Blob: domain.NewSHA256BlobRef(sum), TransformID: "thumbnail", TransformVersion: "1", Parameters: []byte(`{}`)}
	asset, err := service.GetOrCreate(ctx, request, func(_ context.Context, output io.Writer) (string, error) {
		_, err := output.Write([]byte("valid"))
		return "image/webp", err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirs.Cache, filepath.FromSlash(asset.RelativePath)), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dirs.Data, "catalog.db"), filepath.Join(dirs.Data, "catalog.db-wal"), filepath.Join(dirs.Data, "catalog.db-shm")} {
		_ = os.Remove(path)
	}
	reopened, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recoveredService, _ := derived.New(reopened.Catalog.SQL(), dirs.Cache, now, nil)
	var generated atomic.Int32
	rebuilt, err := recoveredService.GetOrCreate(ctx, request, func(_ context.Context, output io.Writer) (string, error) {
		generated.Add(1)
		_, err := output.Write([]byte("regenerated"))
		return "image/webp", err
	})
	if err != nil || rebuilt.TakenOver || generated.Load() != 1 || rebuilt.OutputSize != int64(len("regenerated")) {
		t.Fatalf("损坏输出被错误接管: %+v %v generated=%d", rebuilt, err, generated.Load())
	}
}
