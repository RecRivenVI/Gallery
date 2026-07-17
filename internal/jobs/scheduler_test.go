package jobs_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/jobs"
)

func TestSchedulerConcurrencyLimit(t *testing.T) {
	scheduler := jobs.NewScheduler(context.Background())
	defer scheduler.Shutdown()
	var current, peak int32
	release := make(chan struct{})
	started := make(chan struct{}, 8)
	done := make(chan struct{}, 8)
	scheduler.Register("scan", 2, func(ctx context.Context, jobID string) error {
		now := atomic.AddInt32(&current, 1)
		for {
			old := atomic.LoadInt32(&peak)
			if now <= old || atomic.CompareAndSwapInt32(&peak, old, now) {
				break
			}
		}
		started <- struct{}{}
		<-release
		atomic.AddInt32(&current, -1)
		done <- struct{}{}
		return nil
	})
	for i := 0; i < 5; i++ {
		scheduler.Submit("scan", jobName(i))
	}
	<-started
	<-started
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&current); got > 2 {
		t.Fatalf("并发超过上限: %d", got)
	}
	close(release)
	for i := 0; i < 5; i++ {
		<-done
	}
	if peak > 2 {
		t.Fatalf("峰值并发超过上限: %d", peak)
	}
}

func TestSchedulerClassIsolation(t *testing.T) {
	scheduler := jobs.NewScheduler(context.Background())
	defer scheduler.Shutdown()
	blockScan := make(chan struct{})
	scanStarted := make(chan struct{})
	scheduler.Register("scan", 1, func(ctx context.Context, jobID string) error {
		close(scanStarted)
		<-blockScan
		return nil
	})
	maintenanceRan := make(chan struct{})
	scheduler.Register("maintenance", 1, func(ctx context.Context, jobID string) error {
		close(maintenanceRan)
		return nil
	})
	scheduler.Submit("scan", "scan-1")
	<-scanStarted
	// scan 类别被长任务占满时，maintenance 类别仍应独立执行。
	scheduler.Submit("maintenance", "maint-1")
	select {
	case <-maintenanceRan:
	case <-time.After(2 * time.Second):
		t.Fatal("忙碌的 scan 类别饿死了 maintenance 类别")
	}
	close(blockScan)
}

func TestSchedulerDedupeNoDoubleExecution(t *testing.T) {
	scheduler := jobs.NewScheduler(context.Background())
	defer scheduler.Shutdown()
	var runs int32
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	scheduler.Register("scan", 4, func(ctx context.Context, jobID string) error {
		atomic.AddInt32(&runs, 1)
		once.Do(func() { close(started) })
		<-release
		return nil
	})
	scheduler.Submit("scan", "same-job")
	<-started
	// 同一 Job 在执行中重复提交必须被忽略。
	scheduler.Submit("scan", "same-job")
	scheduler.Submit("scan", "same-job")
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&runs); got != 1 {
		t.Fatalf("同一 Job 被重复执行: %d", got)
	}
	close(release)
}

func TestSchedulerCancelRunning(t *testing.T) {
	scheduler := jobs.NewScheduler(context.Background())
	defer scheduler.Shutdown()
	started := make(chan struct{})
	observed := make(chan struct{})
	scheduler.Register("scan", 1, func(ctx context.Context, jobID string) error {
		close(started)
		<-ctx.Done()
		close(observed)
		return ctx.Err()
	})
	scheduler.Submit("scan", "cancel-me")
	<-started
	if !scheduler.Cancel("cancel-me") {
		t.Fatal("Cancel 未找到在执行的 Job")
	}
	select {
	case <-observed:
	case <-time.After(2 * time.Second):
		t.Fatal("取消未传播到在执行的 Job")
	}
}

func TestSchedulerCancelQueued(t *testing.T) {
	scheduler := jobs.NewScheduler(context.Background())
	defer scheduler.Shutdown()
	release := make(chan struct{})
	firstStarted := make(chan struct{})
	var secondRan int32
	scheduler.Register("scan", 1, func(ctx context.Context, jobID string) error {
		if jobID == "first" {
			close(firstStarted)
			<-release
			return nil
		}
		atomic.AddInt32(&secondRan, 1)
		return nil
	})
	scheduler.Submit("scan", "first")
	<-firstStarted
	// second 在等待 token 时被取消，永不执行。
	scheduler.Submit("scan", "second")
	if !scheduler.Cancel("second") {
		t.Fatal("Cancel 未找到排队的 Job")
	}
	close(release)
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&secondRan); got != 0 {
		t.Fatalf("被取消的排队 Job 仍执行: %d", got)
	}
}

func TestSchedulerShutdownCancelsAndRejects(t *testing.T) {
	scheduler := jobs.NewScheduler(context.Background())
	started := make(chan struct{})
	exited := make(chan struct{})
	scheduler.Register("scan", 1, func(ctx context.Context, jobID string) error {
		close(started)
		<-ctx.Done()
		close(exited)
		return ctx.Err()
	})
	scheduler.Submit("scan", "running")
	<-started
	scheduler.Shutdown()
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown 未取消在执行的 Job")
	}
	// Shutdown 后提交被忽略。
	var ran int32
	scheduler.Register("scan2", 1, func(ctx context.Context, jobID string) error {
		atomic.AddInt32(&ran, 1)
		return nil
	})
	scheduler.Submit("scan2", "late")
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&ran); got != 0 {
		t.Fatalf("Shutdown 后仍执行新 Job: %d", got)
	}
}

func jobName(i int) string {
	return "job-" + string(rune('a'+i))
}
