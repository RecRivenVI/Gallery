package toolrunner_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/ports"
	"github.com/RecRivenVI/gallery/internal/storage"
	"github.com/RecRivenVI/gallery/internal/toolrunner"
)

type resolver struct{}

func (resolver) Resolve(context.Context, string, []string, string) (ports.Command, error) {
	return ports.Command{Path: "allowed-tool", Args: []string{"--version"}}, nil
}

type capturingResolver struct{ workingDir string }

func (r *capturingResolver) Resolve(_ context.Context, _ string, _ []string, workingDir string) (ports.Command, error) {
	r.workingDir = workingDir
	return ports.Command{Path: "allowed-tool", Args: []string{"--version"}}, nil
}

type processController struct{}

func (processController) Start(_ context.Context, command ports.Command) (ports.Process, error) {
	_, _ = io.WriteString(command.Stdout, "stdout\n")
	_, _ = io.WriteString(command.Stderr, "stderr\n")
	return process{}, nil
}

type process struct{}

func (process) Wait() error { return nil }
func (process) Kill() error { return nil }

func TestExecutePersistsBoundedToolOutputDigest(t *testing.T) {
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 3, 0, 0, 0, time.UTC)}
	jobStore, err := jobs.NewStore(store.Control.SQL(), now, identity.NewGenerator(now))
	if err != nil {
		t.Fatal(err)
	}
	service, err := toolrunner.New(jobStore, processController{}, resolver{})
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.Create(context.Background(), toolrunner.Request{ToolID: "ffprobe", Args: []string{"--version"}, TimeoutSeconds: 2, MaxOutputBytes: 1024}, "owner")
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
	if completed.Status != jobs.StatusCompleted {
		t.Fatalf("外部工具 Job 未完成: %+v", completed)
	}
	var result toolrunner.Result
	if err := json.Unmarshal(completed.ResultJSON, &result); err != nil {
		t.Fatal(err)
	}
	if result.StdoutBytes != int64(len("stdout\n")) || result.StderrBytes != int64(len("stderr\n")) || result.StdoutSHA256 == "" || result.StderrSHA256 == "" {
		t.Fatalf("外部工具输出摘要不完整: %+v", result)
	}
}

func TestToolAvailabilityAndOwnedWorkingDirectory(t *testing.T) {
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
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 4, 0, 0, 0, time.UTC)}
	jobStore, _ := jobs.NewStore(store.Control.SQL(), now, identity.NewGenerator(now))
	unavailable, _ := toolrunner.New(jobStore, processController{}, nil)
	if unavailable.Available() {
		t.Fatal("未配置 ToolDiscovery 时错误报告为可用")
	}
	if _, err := unavailable.Create(ctx, toolrunner.Request{ToolID: "ffprobe", TimeoutSeconds: 2}, "owner"); err == nil {
		t.Fatal("未配置 ToolDiscovery 仍创建了必然失败的 Job")
	}
	var count int
	if err := store.Control.SQL().QueryRowContext(ctx, "SELECT COUNT(*) FROM jobs").Scan(&count); err != nil || count != 0 {
		t.Fatalf("不可用请求污染了 Job 表: count=%d err=%v", count, err)
	}

	resolver := &capturingResolver{}
	service, _ := toolrunner.New(jobStore, processController{}, resolver)
	tempStore, _ := jobs.NewTempStore(store.Control.SQL(), dirs.Temp, now)
	service.SetTempStore(tempStore)
	job, err := service.Create(ctx, toolrunner.Request{ToolID: "ffprobe", TimeoutSeconds: 2}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	expectedRoot := filepath.Join(dirs.Temp, "jobs") + string(filepath.Separator)
	if !strings.HasPrefix(filepath.Clean(resolver.workingDir)+string(filepath.Separator), expectedRoot) {
		t.Fatalf("外部工具未使用 Job 所有的工作目录: %q", resolver.workingDir)
	}
	if _, err := os.Stat(filepath.Join(resolver.workingDir, "manifest.json")); err != nil {
		t.Fatalf("外部工具工作目录缺少 manifest: %v", err)
	}
}
