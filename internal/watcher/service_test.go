package watcher_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/ports"
	"github.com/RecRivenVI/gallery/internal/storage"
	watcherservice "github.com/RecRivenVI/gallery/internal/watcher"
)

type fakeScanner struct {
	jobs  *jobs.Store
	count int
	last  string
}

func (f *fakeScanner) CreateScan(ctx context.Context, sourceID, createdBy string) (jobs.Job, error) {
	f.count++
	job, err := f.jobs.CreateScan(ctx, sourceID, createdBy, "")
	if err == nil {
		f.last = job.ID
	}
	return job, err
}

func (f *fakeScanner) Start(jobID string) { f.last = jobID }

func TestWatcherStateCoalescesEventsAndTracksOfflineSource(t *testing.T) {
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
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 2, 0, 0, 0, time.UTC)}
	ids := identity.NewGenerator(now)
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, now, ids)
	if err != nil {
		t.Fatal(err)
	}
	library, err := resources.CreateLibrary(ctx, "watcher")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	source, err := resources.CreateSource(ctx, library.ID, "watch-source", root)
	if err != nil {
		t.Fatal(err)
	}
	jobStore, err := jobs.NewStore(store.Control.SQL(), now, ids)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeScanner{jobs: jobStore}
	service, err := watcherservice.New(ctx, store.Control.SQL(), resources, jobStore, fake, nil, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ReconcileSource(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	state, err := service.GetState(ctx, source.ID)
	if err != nil || state.Status != "online" || state.CurrentJobID == "" || fake.count != 1 {
		t.Fatalf("首次收敛状态错误: %+v count=%d err=%v", state, fake.count, err)
	}
	if err := service.HandleEvent(ctx, source.ID, ports.WatchEvent{Kind: ports.WatchModified, RelativePath: "work/media.bin", At: now.Now()}); err != nil {
		t.Fatal(err)
	}
	state, err = service.GetState(ctx, source.ID)
	if err != nil || !state.Dirty || state.LastEventAt == nil {
		t.Fatalf("事件未合并为 dirty hint: %+v %v", state, err)
	}
	if err := service.ReconcileSource(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	if fake.count != 1 {
		t.Fatal("活动扫描期间重复事件不应创建第二个 Scan Job")
	}
	if _, err := jobStore.Fail(ctx, fake.last, "TEST_FAILURE"); err != nil {
		t.Fatal(err)
	}
	if err := service.ReconcileSource(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	if fake.count != 2 {
		t.Fatal("失败扫描后的 dirty 状态未重新收敛")
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := service.ReconcileSource(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	state, err = service.GetState(ctx, source.ID)
	if err != nil || state.Status != "offline" || !state.Dirty {
		t.Fatalf("Source 离线状态错误: %+v %v", state, err)
	}
}
