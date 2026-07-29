package sourcelab

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/tools/testlab/internal/environment"
)

func TestWaitForJobCancelsAtBound(t *testing.T) {
	var cancelled atomic.Bool
	var cancelRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/jobs/job-1":
			status := "running"
			if cancelled.Load() {
				status = "cancelled"
			}
			writeTestJob(t, writer, status)
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/jobs/job-1/cancel":
			cancelRequests.Add(1)
			cancelled.Store(true)
			writer.WriteHeader(http.StatusAccepted)
			writeTestJob(t, writer, "cancelled")
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	session, err := environment.NewBareSession(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	job, stopped, err := waitForJob(session, "job-1", 10*time.Millisecond, true)
	if err != nil {
		t.Fatalf("有界等待失败: %v", err)
	}
	if !stopped || job == nil || job.Status != "cancelled" {
		t.Fatalf("有界等待没有以取消终态收敛: stopped=%v job=%+v", stopped, job)
	}
	if cancelRequests.Load() != 1 {
		t.Fatalf("取消请求数 = %d, want 1", cancelRequests.Load())
	}
}

func TestWaitForJobKeepsOrdinaryTimeoutNonDestructive(t *testing.T) {
	var cancelRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPost {
			cancelRequests.Add(1)
		}
		writeTestJob(t, writer, "running")
	}))
	defer server.Close()

	session, err := environment.NewBareSession(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, stopped, err := waitForJob(session, "job-1", 10*time.Millisecond, false); err == nil || stopped {
		t.Fatalf("普通超时应返回非取消错误: stopped=%v err=%v", stopped, err)
	}
	if cancelRequests.Load() != 0 {
		t.Fatalf("普通超时不应发取消请求，实际=%d", cancelRequests.Load())
	}
}

func writeTestJob(t *testing.T, writer http.ResponseWriter, status string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	job := map[string]any{
		"attempt": 1, "createdAt": now, "id": "job-1", "stage": "hash", "status": status,
		"type": "hash", "updatedAt": now,
		"progress": map[string]any{"current": 0, "sequence": 0, "total": 1},
	}
	if err := json.NewEncoder(writer).Encode(job); err != nil {
		t.Errorf("写入测试 Job: %v", err)
	}
}
