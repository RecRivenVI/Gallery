package jobs_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/storage"
)

type tempClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *tempClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *tempClock) Advance(value time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(value)
	c.mu.Unlock()
}

func TestTempStorePreservesActiveAttemptAndSweepsTerminalAttempt(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	databases, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer databases.Close()
	now := &tempClock{now: time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)}
	jobStore, _ := jobs.NewStore(databases.Control.SQL(), now, identity.NewGenerator(now))
	tempStore, _ := jobs.NewTempStore(databases.Control.SQL(), dirs.Temp, now)
	job, err := jobStore.CreateWithOptions(ctx, "external_tool", "", "owner",
		jobs.CreateOptions{ResourceClass: jobs.ResourceExternalTool})
	if err != nil {
		t.Fatal(err)
	}
	job, err = jobStore.StartStage(ctx, job.ID, "running")
	if err != nil {
		t.Fatal(err)
	}
	directory, err := tempStore.Acquire(ctx, job, []string{"output.bin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "manifest.json")); err != nil {
		t.Fatalf("manifest 未原子发布: %v", err)
	}
	if report, err := tempStore.Sweep(ctx, 0, 0); err != nil || report.TerminalRemoved != 0 {
		t.Fatalf("活动 Attempt 被清理: %+v %v", report, err)
	}
	if _, err := jobStore.CompleteRunning(ctx, job.ID, nil); err != nil {
		t.Fatal(err)
	}
	report, err := tempStore.Sweep(ctx, 0, 0)
	if err != nil || report.TerminalRemoved != 1 {
		t.Fatalf("终态目录未清理: %+v %v", report, err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("终态目录仍存在: %v", err)
	}
	if repeat, err := tempStore.Sweep(ctx, 0, 0); err != nil || repeat != (jobs.TempSweepReport{}) {
		t.Fatalf("重复清理不幂等: %+v %v", repeat, err)
	}
}

func TestTempStoreUsesLongerGraceForCorruptAndOrphanDirectories(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	databases, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer databases.Close()
	now := &tempClock{now: time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)}
	jobStore, _ := jobs.NewStore(databases.Control.SQL(), now, identity.NewGenerator(now))
	tempStore, _ := jobs.NewTempStore(databases.Control.SQL(), dirs.Temp, now)
	job, _ := jobStore.CreateWithOptions(ctx, "external_tool", "", "owner",
		jobs.CreateOptions{ResourceClass: jobs.ResourceExternalTool})
	job, _ = jobStore.StartStage(ctx, job.ID, "running")
	directory, _ := tempStore.Acquire(ctx, job, nil)
	if _, err := jobStore.CompleteRunning(ctx, job.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "manifest.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	now.Advance(25 * time.Hour)
	if report, err := tempStore.Sweep(ctx, 24*time.Hour, 7*24*time.Hour); err != nil || report.OrphanRemoved != 0 {
		t.Fatalf("损坏 manifest 未等待更长 grace: %+v %v", report, err)
	}
	now.Advance(7 * 24 * time.Hour)
	if report, err := tempStore.Sweep(ctx, 24*time.Hour, 7*24*time.Hour); err != nil || report.OrphanRemoved != 1 {
		t.Fatalf("损坏 manifest 未按 orphan grace 清理: %+v %v", report, err)
	}

	orphan := filepath.Join(dirs.Temp, "jobs", "orphan-job", "1")
	if err := os.MkdirAll(orphan, 0o700); err != nil {
		t.Fatal(err)
	}
	old := now.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatal(err)
	}
	if report, err := tempStore.Sweep(ctx, 24*time.Hour, 7*24*time.Hour); err != nil || report.OrphanRemoved != 1 {
		t.Fatalf("未登记强杀残留未清理: %+v %v", report, err)
	}
}

func TestTempStoreRejectsEscapingExpectedOutput(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	databases, _ := storage.Open(ctx, dirs)
	defer databases.Close()
	now := &tempClock{now: time.Now().UTC()}
	jobStore, _ := jobs.NewStore(databases.Control.SQL(), now, identity.NewGenerator(now))
	tempStore, _ := jobs.NewTempStore(databases.Control.SQL(), dirs.Temp, now)
	job, _ := jobStore.CreateWithOptions(ctx, "external_tool", "", "owner",
		jobs.CreateOptions{ResourceClass: jobs.ResourceExternalTool})
	job, _ = jobStore.StartStage(ctx, job.ID, "running")
	if _, err := tempStore.Acquire(ctx, job, []string{"../outside"}); err == nil ||
		!strings.Contains(err.Error(), "PATH_ESCAPE") {
		t.Fatalf("越界输出未拒绝: %v", err)
	}
}
