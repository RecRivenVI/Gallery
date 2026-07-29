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

func TestWaitForActiveHashAndCancelRequiresParentAndChildCancellation(t *testing.T) {
	var cancelled atomic.Bool
	var cancelRequests atomic.Int32
	var hashReadsAfterCancel atomic.Int32
	createdAt := time.Now().UTC().Add(-time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		status := "running"
		if cancelled.Load() {
			status = "cancelled"
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/jobs":
			_ = json.NewEncoder(writer).Encode(map[string]any{"jobs": []any{
				testJob("hash-1", "hash", "source-1", status, createdAt),
			}})
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/jobs/scan-1":
			_ = json.NewEncoder(writer).Encode(testJob("scan-1", "scan", "source-1", status, createdAt))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/jobs/hash-1":
			hashStatus := status
			if cancelled.Load() && hashReadsAfterCancel.Add(1) == 1 {
				hashStatus = "cancelling"
			}
			_ = json.NewEncoder(writer).Encode(testJob("hash-1", "hash", "source-1", hashStatus, createdAt))
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/jobs/scan-1/cancel":
			cancelRequests.Add(1)
			cancelled.Store(true)
			writer.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(writer).Encode(testJob("scan-1", "scan", "source-1", "cancelled", createdAt))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	session, err := environment.NewBareSession(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := waitForActiveHashAndCancel(session, "scan-1", "source-1", createdAt, time.Second)
	if err != nil {
		t.Fatalf("活动 Hash 取消失败: %v", err)
	}
	if !outcome.ActiveHashObserved || outcome.ParentStatus != "cancelled" || outcome.HashStatus != "cancelled" {
		t.Fatalf("活动 Hash 取消未收敛: %+v", outcome)
	}
	if cancelRequests.Load() != 1 {
		t.Fatalf("父 Scan 取消请求数 = %d, want 1", cancelRequests.Load())
	}
}

func TestWaitForActiveHashAndCancelDoesNotMisreportDiscoveryTimeout(t *testing.T) {
	var cancelled atomic.Bool
	var cancelRequests atomic.Int32
	createdAt := time.Now().UTC().Add(-time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		status := "running"
		if cancelled.Load() {
			status = "cancelled"
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/jobs":
			_ = json.NewEncoder(writer).Encode(map[string]any{"jobs": []any{
				testJob("scan-1", "scan", "source-1", status, createdAt),
			}})
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/jobs/scan-1":
			_ = json.NewEncoder(writer).Encode(testJob("scan-1", "scan", "source-1", status, createdAt))
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/jobs/scan-1/cancel":
			cancelRequests.Add(1)
			cancelled.Store(true)
			writer.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(writer).Encode(testJob("scan-1", "scan", "source-1", "cancelled", createdAt))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	session, err := environment.NewBareSession(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := waitForActiveHashAndCancel(session, "scan-1", "source-1", createdAt, 10*time.Millisecond); err == nil {
		t.Fatal("只观察到 discovery/Scan 超时时必须失败，不得冒充活动 Hash 取消")
	}
	if cancelRequests.Load() != 1 || !cancelled.Load() {
		t.Fatalf("失败后父 Scan 未安全清理: requests=%d cancelled=%v", cancelRequests.Load(), cancelled.Load())
	}
}

func testJob(id, jobType, sourceID, status string, createdAt time.Time) map[string]any {
	return map[string]any{
		"attempt": 1, "createdAt": createdAt.Format(time.RFC3339Nano), "id": id, "sourceId": sourceID,
		"stage": jobType, "status": status, "type": jobType, "updatedAt": time.Now().UTC().Format(time.RFC3339Nano),
		"progress": map[string]any{"current": 0, "sequence": 1, "total": 1},
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
