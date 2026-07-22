package query

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/tools/testlab/internal/environment"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/report"
)

// fakeWorksHandler 是一个最小可用的 /api/v1/works 伪造实现：delay 为 0 时立即返回
// 一个合法的空 WorkListResponse；delay 大于 0 时先等待 delay（或客户端提前取消）
// 再返回，用于确定性地制造"单请求超过预算"的场景，不需要真正的 galleryd。
func fakeWorksHandler(delay time.Duration, hits *int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		if hits != nil {
			atomic.AddInt64(hits, 1)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"queryPublicationId":  "qpub_00000000-0000-7000-8000-000000000000",
			"catalogRevision":     "crev_00000000-0000-7000-8000-000000000000",
			"sortProtocolVersion": 1,
			"rankProtocolVersion": 2,
			"total":               map[string]any{"mode": "exact", "value": 0, "protocolVersion": 1},
			"works":               []any{},
			"dependencySet":       []any{},
			"liveUserStateFields": []string{"favorite", "progress"},
		})
	}
}

func newFakeSession(t *testing.T, handler http.HandlerFunc) *environment.Session {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/works", handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	sess, err := environment.NewBareSession(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

func TestBuildFullMatrixCombinationCount(t *testing.T) {
	combos := buildFullMatrix([]int{20, 100, 200}, []int{1, 4, 16}, 30)
	want := 7 * 3 * 3 // 7 shapes x 3 limits x 3 concurrencies
	if len(combos) != want {
		t.Fatalf("len(combos) = %d, want %d", len(combos), want)
	}
	for _, c := range combos {
		if c.runs != 30 {
			t.Fatalf("combo %+v has runs=%d, want 30", c, c.runs)
		}
	}
}

func TestBuildDirectionalMatrixNeverExceedsOneRunForKnownSlowShapes(t *testing.T) {
	for _, c := range buildDirectionalMatrix() {
		switch c.shape.name {
		case "wide-cjk", "structured-and", "structured-or", "overlay-favorite":
			if c.runs > 1 || c.concurrency > 1 {
				t.Fatalf("known-slow shape %q must stay at runs<=1 concurrency<=1 in the directional matrix, got runs=%d concurrency=%d", c.shape.name, c.runs, c.concurrency)
			}
		}
	}
}

func TestRunOneCombinationCountsTimeoutsAsFailed(t *testing.T) {
	// 服务端对每个请求都睡眠远超单请求超时的时间；客户端应该在 perRequestTimeout
	// 后放弃，把它计入 failed（且计入 timedOut 子集），而不是无限等待。
	sess := newFakeSession(t, fakeWorksHandler(2*time.Second, nil))
	rep := &report.Report{}
	combo := combination{shape: perfShapes()["browse"], limit: 20, concurrency: 2, runs: 3}
	started := time.Now()
	runOneCombination(rep, sess, combo, 200*time.Millisecond, time.Now().Add(10*time.Second))
	elapsed := time.Since(started)
	if elapsed > 3*time.Second {
		t.Fatalf("runOneCombination took %s, expected to bail out promptly once each request's own timeout elapses", elapsed)
	}
	if len(rep.Latencies) != 1 {
		t.Fatalf("expected exactly one LatencySample, got %d", len(rep.Latencies))
	}
	sample := rep.Latencies[0]
	if sample.SuccessfulRuns != 0 || sample.FailedRuns != 3 || sample.TimedOutRuns != 3 || sample.NotAttemptedRuns != 0 {
		t.Fatalf("sample = %+v, want SuccessfulRuns=0 FailedRuns=3 TimedOutRuns=3 NotAttemptedRuns=0", sample)
	}
	if !sample.IdentityOK() {
		t.Fatalf("sample violates run-count identity: %+v", sample)
	}
}

func TestRunOneCombinationStopsPastCombinationDeadline(t *testing.T) {
	sess := newFakeSession(t, fakeWorksHandler(0, nil))
	rep := &report.Report{}
	combo := combination{shape: perfShapes()["browse"], limit: 20, concurrency: 1, runs: 1000}
	// 组合截止时间已经过去：不应该派发任何请求，全部计入 NotAttemptedRuns，不折叠进
	// FailedRuns——否则会破坏 successfulRuns+failedRuns==attemptedRuns 恒等式。
	runOneCombination(rep, sess, combo, 5*time.Second, time.Now().Add(-1*time.Millisecond))
	sample := rep.Latencies[0]
	if sample.AttemptedRuns != 0 {
		t.Fatalf("AttemptedRuns = %d, want 0 when the combination deadline has already passed", sample.AttemptedRuns)
	}
	if sample.FailedRuns != 0 {
		t.Fatalf("FailedRuns = %d, want 0 (nothing was dispatched, so nothing can have failed)", sample.FailedRuns)
	}
	if sample.NotAttemptedRuns != 1000 {
		t.Fatalf("NotAttemptedRuns = %d, want 1000", sample.NotAttemptedRuns)
	}
	if !sample.IdentityOK() {
		t.Fatalf("sample violates run-count identity: %+v", sample)
	}
}

func TestRunPerfMatrixAbortsOnScenarioTimeoutAndSavesPartial(t *testing.T) {
	sess := newFakeSession(t, fakeWorksHandler(50*time.Millisecond, nil))
	rep := &report.Report{}
	combos := []combination{
		{perfShapes()["browse"], 20, 1, 2},
		{perfShapes()["browse"], 100, 1, 2},
		{perfShapes()["browse"], 200, 1, 2},
	}
	timeouts := PerfTimeouts{PerRequest: time.Second, PerCombination: time.Second, Scenario: 60 * time.Millisecond}
	saveCount := 0
	RunPerfMatrix(rep, sess, combos, timeouts, func() { saveCount++ })

	if !rep.AbortedByTimeLimit {
		t.Fatal("expected AbortedByTimeLimit=true when the scenario budget is far smaller than the planned work")
	}
	if rep.AbortReason == "" {
		t.Fatal("expected a non-empty AbortReason when aborted")
	}
	if rep.PlannedCombinations != 3 {
		t.Fatalf("PlannedCombinations = %d, want 3", rep.PlannedCombinations)
	}
	if rep.CompletedCombinations >= rep.PlannedCombinations {
		t.Fatalf("CompletedCombinations = %d should be less than PlannedCombinations = %d when aborted", rep.CompletedCombinations, rep.PlannedCombinations)
	}
	if saveCount == 0 {
		t.Fatal("expected savePartial to be called at least once for completed combinations")
	}
	// 中止的矩阵绝不能被误判为"整体通过"：必须存在一条失败的 finding。
	foundAbortFinding := false
	for _, f := range rep.Findings {
		if f.Name == "perf/matrix-completed-without-time-abort" {
			foundAbortFinding = true
			if f.Pass {
				t.Fatal("perf/matrix-completed-without-time-abort must be Pass=false when AbortedByTimeLimit is true")
			}
		}
	}
	if !foundAbortFinding {
		t.Fatal("expected a perf/matrix-completed-without-time-abort finding")
	}
}

func TestRunPerfMatrixPartialReportIsParseable(t *testing.T) {
	sess := newFakeSession(t, fakeWorksHandler(0, nil))
	rep := &report.Report{}
	combos := buildFullMatrix([]int{20}, []int{1}, 2)[:2]
	timeouts := PerfTimeouts{PerRequest: time.Second, PerCombination: time.Second, Scenario: time.Minute}
	var lastSnapshot []byte
	RunPerfMatrix(rep, sess, combos, timeouts, func() {
		encoded, err := json.Marshal(rep)
		if err != nil {
			t.Fatalf("partial report not marshalable: %v", err)
		}
		lastSnapshot = encoded
	})
	if lastSnapshot == nil {
		t.Fatal("expected at least one partial snapshot")
	}
	var decoded map[string]any
	if err := json.Unmarshal(lastSnapshot, &decoded); err != nil {
		t.Fatalf("partial report JSON not parseable: %v", err)
	}
}
