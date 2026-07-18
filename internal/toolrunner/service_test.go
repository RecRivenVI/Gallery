package toolrunner_test

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
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
