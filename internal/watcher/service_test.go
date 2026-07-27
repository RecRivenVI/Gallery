package watcher_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/domain"
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

type watchLifecycle struct {
	root       string
	generation int
	state      string
}

type scriptedWatcher struct {
	mu          sync.Mutex
	scripts     map[string]chan watchResult
	generations map[string]int
	lifecycle   []watchLifecycle
	changed     chan struct{}
}

func newScriptedWatcher() *scriptedWatcher {
	return &scriptedWatcher{
		scripts:     make(map[string]chan watchResult),
		generations: make(map[string]int),
		changed:     make(chan struct{}),
	}
}

func (w *scriptedWatcher) queue(root string, result watchResult) {
	w.mu.Lock()
	scripts := w.scripts[root]
	if scripts == nil {
		scripts = make(chan watchResult, 16)
		w.scripts[root] = scripts
	}
	w.mu.Unlock()
	scripts <- result
}

func (w *scriptedWatcher) Watch(ctx context.Context, root string) (<-chan ports.WatchEvent, error) {
	w.mu.Lock()
	w.generations[root]++
	generation := w.generations[root]
	scripts := w.scripts[root]
	if scripts == nil {
		scripts = make(chan watchResult, 16)
		w.scripts[root] = scripts
	}
	w.recordLocked(watchLifecycle{root: root, generation: generation, state: "created"})
	w.mu.Unlock()

	select {
	case result := <-scripts:
		if result.err != nil {
			w.record(watchLifecycle{root: root, generation: generation, state: "failed"})
			return nil, result.err
		}
		proxy := make(chan ports.WatchEvent)
		w.record(watchLifecycle{root: root, generation: generation, state: "running"})
		go w.forward(ctx, root, generation, result.events, proxy)
		return proxy, nil
	case <-ctx.Done():
		w.record(watchLifecycle{root: root, generation: generation, state: "stopped"})
		return nil, ctx.Err()
	}
}

func (w *scriptedWatcher) forward(ctx context.Context, root string, generation int, input <-chan ports.WatchEvent, output chan<- ports.WatchEvent) {
	defer close(output)
	for {
		select {
		case <-ctx.Done():
			w.record(watchLifecycle{root: root, generation: generation, state: "stopped"})
			return
		case event, ok := <-input:
			if !ok {
				w.record(watchLifecycle{root: root, generation: generation, state: "channel_closed"})
				return
			}
			select {
			case output <- event:
			case <-ctx.Done():
				w.record(watchLifecycle{root: root, generation: generation, state: "stopped"})
				return
			}
		}
	}
}

func (w *scriptedWatcher) record(event watchLifecycle) {
	w.mu.Lock()
	w.recordLocked(event)
	w.mu.Unlock()
}

func (w *scriptedWatcher) recordLocked(event watchLifecycle) {
	w.lifecycle = append(w.lifecycle, event)
	close(w.changed)
	w.changed = make(chan struct{})
}

func (w *scriptedWatcher) wait(t *testing.T, root string, generation int, state string) watchLifecycle {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		w.mu.Lock()
		for _, event := range w.lifecycle {
			if event.root == root && event.generation == generation && event.state == state {
				w.mu.Unlock()
				return event
			}
		}
		changed := w.changed
		snapshot := append([]watchLifecycle(nil), w.lifecycle...)
		w.mu.Unlock()
		select {
		case <-changed:
		case <-timer.C:
			t.Fatalf("等待 Watcher 生命周期超时: root=%q generation=%d state=%s events=%+v", root, generation, state, snapshot)
		}
	}
}

func (w *scriptedWatcher) createdCount(root string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	count := 0
	for _, event := range w.lifecycle {
		if event.root == root && event.state == "created" {
			count++
		}
	}
	return count
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

type watcherFixture struct {
	ctx       context.Context
	now       *clock.Manual
	ids       identity.Generator
	resources *application.Resources
	source    application.Source
	jobs      *jobs.Store
	fake      *fakeScanner
	service   *watcherservice.Service
}

func newWatcherFixture(t *testing.T, name string) watcherFixture {
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
	t.Cleanup(func() { _ = store.Close() })
	now := clock.NewManual(time.Date(2026, 7, 28, 2, 0, 0, 0, time.UTC))
	ids := identity.NewGenerator(now)
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, now, ids)
	if err != nil {
		t.Fatal(err)
	}
	library, err := resources.CreateLibrary(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	source, err := resources.CreateSource(ctx, library.ID, name+"-source", root)
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
	return watcherFixture{ctx: ctx, now: now, ids: ids, resources: resources, source: source, jobs: jobStore, fake: fake, service: service}
}

func completeScan(t *testing.T, fixture watcherFixture, jobID string) string {
	t.Helper()
	if _, err := fixture.jobs.Start(fixture.ctx, jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.jobs.BeginPublishing(fixture.ctx, jobID); err != nil {
		t.Fatal(err)
	}
	publicationID, err := fixture.ids.New(domain.IDQueryPublication)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.jobs.Complete(fixture.ctx, jobID, publicationID.String()); err != nil {
		t.Fatal(err)
	}
	return publicationID.String()
}

func TestTrackScanPreventsDuplicateAfterExplicitCompletion(t *testing.T) {
	fixture := newWatcherFixture(t, "tracked-explicit")
	job, err := fixture.jobs.CreateScan(fixture.ctx, fixture.source.ID, "personal-owner", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.TrackScan(fixture.ctx, job); err != nil {
		t.Fatal(err)
	}
	state, err := fixture.service.GetState(fixture.ctx, fixture.source.ID)
	if err != nil || state.Status != "online" || state.Dirty || state.CurrentJobID != job.ID {
		t.Fatalf("显式扫描未正确关联 Watcher: %+v err=%v", state, err)
	}
	publicationID := completeScan(t, fixture, job.ID)
	if err := fixture.service.ReconcileSource(fixture.ctx, fixture.source.ID); err != nil {
		t.Fatal(err)
	}
	state, err = fixture.service.GetState(fixture.ctx, fixture.source.ID)
	if err != nil || state.Dirty || state.CurrentJobID != "" || state.CurrentPublicationID != publicationID || fixture.fake.count != 0 {
		t.Fatalf("显式扫描完成后发生重复收敛: %+v count=%d err=%v", state, fixture.fake.count, err)
	}
}

func TestTrackScanPreservesEventArrivingDuringScan(t *testing.T) {
	fixture := newWatcherFixture(t, "tracked-event")
	job, err := fixture.jobs.CreateScan(fixture.ctx, fixture.source.ID, "personal-owner", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.TrackScan(fixture.ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.HandleEvent(fixture.ctx, fixture.source.ID, ports.WatchEvent{Kind: ports.WatchModified, At: fixture.now.Now()}); err != nil {
		t.Fatal(err)
	}
	completeScan(t, fixture, job.ID)
	if err := fixture.service.ReconcileSource(fixture.ctx, fixture.source.ID); err != nil {
		t.Fatal(err)
	}
	state, err := fixture.service.GetState(fixture.ctx, fixture.source.ID)
	if err != nil || state.CurrentJobID == "" || fixture.fake.count != 1 {
		t.Fatalf("扫描期间事件未触发后继收敛: %+v count=%d err=%v", state, fixture.fake.count, err)
	}
}

func TestTrackScanPreservesEventArrivingBeforeAssociation(t *testing.T) {
	fixture := newWatcherFixture(t, "tracked-association-gap")
	job, err := fixture.jobs.CreateScan(fixture.ctx, fixture.source.ID, "personal-owner", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.HandleEvent(fixture.ctx, fixture.source.ID, ports.WatchEvent{Kind: ports.WatchModified, At: fixture.now.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.TrackScan(fixture.ctx, job); err != nil {
		t.Fatal(err)
	}
	state, err := fixture.service.GetState(fixture.ctx, fixture.source.ID)
	if err != nil || !state.Dirty || state.CurrentJobID != job.ID {
		t.Fatalf("Job 创建至 Watcher 关联窗口内的事件被覆盖: %+v err=%v", state, err)
	}
	completeScan(t, fixture, job.ID)
	if err := fixture.service.ReconcileSource(fixture.ctx, fixture.source.ID); err != nil {
		t.Fatal(err)
	}
	state, err = fixture.service.GetState(fixture.ctx, fixture.source.ID)
	if err != nil || state.CurrentJobID == "" || fixture.fake.count != 1 {
		t.Fatalf("关联窗口事件未触发后继收敛: %+v count=%d err=%v", state, fixture.fake.count, err)
	}
}

func TestTrackScanRetryConsumesEventBeforeCurrentAttempt(t *testing.T) {
	fixture := newWatcherFixture(t, "tracked-retry")
	job, err := fixture.jobs.CreateScan(fixture.ctx, fixture.source.ID, "personal-owner", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.TrackScan(fixture.ctx, job); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.jobs.Start(fixture.ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.jobs.FailWithRetryable(fixture.ctx, job.ID, "TRANSIENT", true); err != nil {
		t.Fatal(err)
	}
	fixture.now.Advance(time.Minute)
	if err := fixture.service.HandleEvent(fixture.ctx, fixture.source.ID, ports.WatchEvent{Kind: ports.WatchModified, At: fixture.now.Now()}); err != nil {
		t.Fatal(err)
	}
	fixture.now.Advance(time.Minute)
	retry, err := fixture.jobs.Retry(fixture.ctx, job.ID, "personal-owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.TrackScan(fixture.ctx, retry); err != nil {
		t.Fatal(err)
	}
	state, err := fixture.service.GetState(fixture.ctx, fixture.source.ID)
	if err != nil || state.Dirty || state.CurrentJobID != job.ID {
		t.Fatalf("Retry 未消费本次 Attempt 开始前的事件: %+v err=%v", state, err)
	}
}

func TestReconcileAdoptsUntrackedActiveScan(t *testing.T) {
	fixture := newWatcherFixture(t, "adopt-active")
	job, err := fixture.jobs.CreateScan(fixture.ctx, fixture.source.ID, "recovery", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.jobs.Start(fixture.ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.ReconcileSource(fixture.ctx, fixture.source.ID); err != nil {
		t.Fatal(err)
	}
	state, err := fixture.service.GetState(fixture.ctx, fixture.source.ID)
	if err != nil || state.Dirty || state.CurrentJobID != job.ID || fixture.fake.count != 0 {
		t.Fatalf("未接管恢复中的扫描: %+v count=%d err=%v", state, fixture.fake.count, err)
	}
}

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
	defer cancel()
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
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, now, ids)
	if err != nil {
		t.Fatal(err)
	}
	library, err := resources.CreateLibrary(ctx, "watch-manager")
	if err != nil {
		t.Fatal(err)
	}
	firstRoot := filepath.Join(t.TempDir(), "source-a")
	if err := os.MkdirAll(firstRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	first, err := resources.CreateSource(ctx, library.ID, "source-a", firstRoot)
	if err != nil {
		t.Fatal(err)
	}
	jobStore, err := jobs.NewStore(store.Control.SQL(), now, ids)
	if err != nil {
		t.Fatal(err)
	}
	scanner := &fakeScanner{jobs: jobStore}
	watcher := newScriptedWatcher()
	firstEvents := make(chan ports.WatchEvent)
	watcher.queue(first.RootPath, watchResult{events: firstEvents})
	service, err := watcherservice.New(ctx, store.Control.SQL(), resources, jobStore, scanner, watcher, now, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	service.SetRetryPolicy(5*time.Millisecond, 20*time.Millisecond)
	service.Start(ctx)
	watcher.wait(t, first.RootPath, 1, "created")
	watcher.wait(t, first.RootPath, 1, "running")

	secondRoot := filepath.Join(t.TempDir(), "source-b")
	if err := os.MkdirAll(secondRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	secondEvents := make(chan ports.WatchEvent)
	second, err := resources.CreateSource(ctx, library.ID, "source-b", secondRoot)
	if err != nil {
		t.Fatal(err)
	}
	watcher.queue(second.RootPath, watchResult{events: secondEvents})
	watcher.wait(t, second.RootPath, 1, "created")
	watcher.wait(t, second.RootPath, 1, "running")

	// channel 关闭后标记不可用并按退避重启同一 Source。
	restartedEvents := make(chan ports.WatchEvent)
	watcher.queue(first.RootPath, watchResult{err: errors.New("synthetic watcher failure")})
	close(firstEvents)
	watcher.wait(t, first.RootPath, 1, "channel_closed")
	watcher.wait(t, first.RootPath, 2, "created")
	watcher.wait(t, first.RootPath, 2, "failed")
	waitWatcherState(t, ctx, service, first.ID, "unavailable", func(state watcherservice.State) bool {
		return !state.WatcherAvailable
	})
	watcher.queue(first.RootPath, watchResult{events: restartedEvents})
	watcher.wait(t, first.RootPath, 3, "created")
	watcher.wait(t, first.RootPath, 3, "running")
	waitWatcherState(t, ctx, service, first.ID, "available", func(state watcherservice.State) bool {
		return state.WatcherAvailable
	})

	// 根变更会取消旧实例并为同一 Source 重建。
	changedRoot := filepath.Join(t.TempDir(), "source-a-moved")
	if err := os.MkdirAll(changedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	changedRoot, err = filepath.EvalSymlinks(changedRoot)
	if err != nil {
		t.Fatal(err)
	}
	changedEvents := make(chan ports.WatchEvent)
	watcher.queue(changedRoot, watchResult{events: changedEvents})
	if _, err := store.Control.SQL().ExecContext(ctx, "UPDATE sources SET root_path=? WHERE source_id=?", changedRoot, first.ID); err != nil {
		t.Fatal(err)
	}
	watcher.wait(t, first.RootPath, 3, "stopped")
	watcher.wait(t, changedRoot, 1, "created")
	watcher.wait(t, changedRoot, 1, "running")
	waitWatcherState(t, ctx, service, first.ID, "根变更后 available", func(state watcherservice.State) bool {
		return state.WatcherAvailable
	})

	// 删除 Source 会停止对应实例；服务取消后全部 goroutine 必须退出。
	if _, err := store.Control.SQL().ExecContext(ctx, "DELETE FROM jobs WHERE source_id=?", second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Control.SQL().ExecContext(ctx, "DELETE FROM sources WHERE source_id=?", second.ID); err != nil {
		t.Fatal(err)
	}
	watcher.wait(t, second.RootPath, 1, "stopped")
	cancel()
	waitServiceDone(t, service)
	watcher.wait(t, changedRoot, 1, "stopped")
	if got := watcher.createdCount(first.RootPath); got != 3 {
		t.Fatalf("旧 root Watcher generation 数量=%d，期望 3", got)
	}
	if got := watcher.createdCount(second.RootPath); got != 1 {
		t.Fatalf("已删除 Source 又被重启: generations=%d", got)
	}
	if got := watcher.createdCount(changedRoot); got != 1 {
		t.Fatalf("新 root Watcher generation 数量=%d，期望 1", got)
	}
	close(secondEvents)
	close(restartedEvents)
	close(changedEvents)
}

func TestWatcherManagerStopsRetryDuringShutdown(t *testing.T) {
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
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 6, 30, 0, 0, time.UTC)}
	ids := identity.NewGenerator(now)
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, now, ids)
	if err != nil {
		t.Fatal(err)
	}
	library, err := resources.CreateLibrary(ctx, "watch-shutdown")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	source, err := resources.CreateSource(ctx, library.ID, "source", root)
	if err != nil {
		t.Fatal(err)
	}
	jobStore, err := jobs.NewStore(store.Control.SQL(), now, ids)
	if err != nil {
		t.Fatal(err)
	}
	watcher := newScriptedWatcher()
	watcher.queue(source.RootPath, watchResult{err: errors.New("synthetic watcher failure")})
	service, err := watcherservice.New(ctx, store.Control.SQL(), resources, jobStore, &fakeScanner{jobs: jobStore}, watcher, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	service.SetRetryPolicy(time.Hour, time.Hour)
	service.Start(ctx)
	watcher.wait(t, source.RootPath, 1, "failed")
	waitWatcherState(t, ctx, service, source.ID, "retry pending", func(state watcherservice.State) bool {
		return !state.WatcherAvailable
	})
	cancel()
	waitServiceDone(t, service)
	if got := watcher.createdCount(source.RootPath); got != 1 {
		t.Fatalf("shutdown 后创建了新 Watcher generation: %d", got)
	}
}

func TestWatcherManagerDoesNotDependOnSourceStartOrder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 6, 45, 0, 0, time.UTC)}
	ids := identity.NewGenerator(now)
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, now, ids)
	if err != nil {
		t.Fatal(err)
	}
	library, err := resources.CreateLibrary(ctx, "watch-order")
	if err != nil {
		t.Fatal(err)
	}
	createSource := func(name string) application.Source {
		t.Helper()
		root := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		source, err := resources.CreateSource(ctx, library.ID, name, root)
		if err != nil {
			t.Fatal(err)
		}
		return source
	}
	first := createSource("source-a")
	second := createSource("source-b")
	jobStore, err := jobs.NewStore(store.Control.SQL(), now, ids)
	if err != nil {
		t.Fatal(err)
	}
	watcher := newScriptedWatcher()
	firstEvents := make(chan ports.WatchEvent)
	secondEvents := make(chan ports.WatchEvent)
	watcher.queue(second.RootPath, watchResult{events: secondEvents})
	watcher.queue(first.RootPath, watchResult{events: firstEvents})
	service, err := watcherservice.New(ctx, store.Control.SQL(), resources, jobStore, &fakeScanner{jobs: jobStore}, watcher, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	service.Start(ctx)
	watcher.wait(t, second.RootPath, 1, "running")
	watcher.wait(t, first.RootPath, 1, "running")
	cancel()
	waitServiceDone(t, service)
	close(firstEvents)
	close(secondEvents)
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

func waitWatcherState(t *testing.T, ctx context.Context, service *watcherservice.Service, sourceID, description string, matches func(watcherservice.State) bool) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := service.GetState(ctx, sourceID)
		if err == nil && matches(state) {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("Watcher 状态未达到 %s: %+v %v", description, state, err)
		case <-ticker.C:
		}
	}
}

func waitServiceDone(t *testing.T, service *watcherservice.Service) {
	t.Helper()
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
}
