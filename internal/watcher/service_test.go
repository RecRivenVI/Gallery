package watcher_test

import (
	"context"
	"errors"
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

type watchResult struct {
	events chan ports.WatchEvent
	err    error
}

type scriptedWatcher struct {
	calls   chan string
	results chan watchResult
	stops   chan string
}

func (w *scriptedWatcher) Watch(ctx context.Context, root string) (<-chan ports.WatchEvent, error) {
	select {
	case w.calls <- root:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case result := <-w.results:
		go func() {
			<-ctx.Done()
			w.stops <- root
		}()
		return result.events, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

var _ ports.FileWatcher = (*scriptedWatcher)(nil)

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

func TestWatcherManagerAddsRestartsRebuildsAndStopsSources(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 6, 0, 0, 0, time.UTC)}
	ids := identity.NewGenerator(now)
	resources, _ := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, now, ids)
	library, _ := resources.CreateLibrary(ctx, "watch-manager")
	firstRoot := filepath.Join(t.TempDir(), "source-a")
	if err := os.MkdirAll(firstRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	first, _ := resources.CreateSource(ctx, library.ID, "source-a", firstRoot)
	jobStore, _ := jobs.NewStore(store.Control.SQL(), now, ids)
	scanner := &fakeScanner{jobs: jobStore}
	watcher := &scriptedWatcher{calls: make(chan string, 16), results: make(chan watchResult, 16), stops: make(chan string, 16)}
	firstEvents := make(chan ports.WatchEvent)
	watcher.results <- watchResult{events: firstEvents}
	service, err := watcherservice.New(ctx, store.Control.SQL(), resources, jobStore, scanner, watcher, now, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	service.SetRetryPolicy(5*time.Millisecond, 20*time.Millisecond)
	service.Start(ctx)
	if got := waitWatchCall(t, watcher.calls); got != firstRoot {
		t.Fatalf("启动时 Watcher root=%q", got)
	}

	secondRoot := filepath.Join(t.TempDir(), "source-b")
	if err := os.MkdirAll(secondRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	secondEvents := make(chan ports.WatchEvent)
	watcher.results <- watchResult{events: secondEvents}
	second, err := resources.CreateSource(ctx, library.ID, "source-b", secondRoot)
	if err != nil {
		t.Fatal(err)
	}
	if got := waitWatchCall(t, watcher.calls); got != secondRoot {
		t.Fatalf("运行时新增 Source 未启动 Watcher: %q", got)
	}

	// channel 关闭后标记不可用并按退避重启同一 Source。
	restartedEvents := make(chan ports.WatchEvent)
	watcher.results <- watchResult{err: errors.New("synthetic watcher failure")}
	watcher.results <- watchResult{events: restartedEvents}
	close(firstEvents)
	if got := waitWatchCall(t, watcher.calls); got != firstRoot {
		t.Fatalf("Watcher 关闭后首次重试 root=%q", got)
	}
	if got := waitWatchCall(t, watcher.calls); got != firstRoot {
		t.Fatalf("Watcher 失败后退避重启 root=%q", got)
	}
	waitWatcherAvailable(t, ctx, service, first.ID)

	// 根变更会取消旧实例并为同一 Source 重建。
	changedRoot := filepath.Join(t.TempDir(), "source-a-moved")
	if err := os.MkdirAll(changedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	changedEvents := make(chan ports.WatchEvent)
	watcher.results <- watchResult{events: changedEvents}
	if _, err := store.Control.SQL().ExecContext(ctx, "UPDATE sources SET root_path=? WHERE source_id=?", changedRoot, first.ID); err != nil {
		t.Fatal(err)
	}
	if got := waitWatchCall(t, watcher.calls); got != changedRoot {
		t.Fatalf("Source 根变更未重建 Watcher: %q", got)
	}

	// 删除 Source 会停止对应实例；服务取消后全部 goroutine 必须退出。
	if _, err := store.Control.SQL().ExecContext(ctx, "DELETE FROM jobs WHERE source_id=?", second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Control.SQL().ExecContext(ctx, "DELETE FROM sources WHERE source_id=?", second.ID); err != nil {
		t.Fatal(err)
	}
	waitWatchStop(t, watcher.stops, secondRoot)
	cancel()
	done := make(chan struct{})
	go func() {
		service.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Watcher Manager 关闭后 goroutine 未退出")
	}
	close(secondEvents)
	close(restartedEvents)
	close(changedEvents)
}

func TestWatcherAvailabilityIsNotLostDuringPeriodicReconciliation(t *testing.T) {
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
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 7, 0, 0, 0, time.UTC)}
	ids := identity.NewGenerator(now)
	resources, _ := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, now, ids)
	library, _ := resources.CreateLibrary(ctx, "watch-state-race")
	root := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	source, _ := resources.CreateSource(ctx, library.ID, "source", root)
	jobStore, _ := jobs.NewStore(store.Control.SQL(), now, ids)
	scanner := &fakeScanner{jobs: jobStore}
	service, err := watcherservice.New(ctx, store.Control.SQL(), resources, jobStore, scanner, nil, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.HandleEvent(ctx, source.ID, ports.WatchEvent{Kind: ports.WatchModified, At: now.Now()}); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	done := make(chan error, 2)
	go func() {
		<-start
		done <- service.ReconcileSource(ctx, source.ID)
	}()
	go func() {
		<-start
		done <- service.HandleEvent(ctx, source.ID, ports.WatchEvent{Kind: ports.WatchModified, At: now.Now()})
	}()
	close(start)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	state, err := service.GetState(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "online" || !state.Dirty && state.CurrentJobID == "" {
		t.Fatalf("并发状态更新丢失: %+v", state)
	}
}

func waitWatcherAvailable(t *testing.T, ctx context.Context, service *watcherservice.Service, sourceID string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := service.GetState(ctx, sourceID)
		if err == nil && state.WatcherAvailable {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("Watcher 未恢复 available: %+v %v", state, err)
		case <-ticker.C:
		}
	}
}

func waitWatchStop(t *testing.T, stops <-chan string, want string) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case root := <-stops:
			if root == want {
				return
			}
		case <-timer.C:
			t.Fatalf("等待 Watcher 停止超时: %s", want)
		}
	}
}

func waitWatchCall(t *testing.T, calls <-chan string) string {
	t.Helper()
	select {
	case root := <-calls:
		return root
	case <-time.After(5 * time.Second):
		t.Fatal("等待 Watcher 调用超时")
		return ""
	}
}
