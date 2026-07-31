package query

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	galleryquery "github.com/RecRivenVI/gallery/internal/query"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/corpus"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/environment"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/report"
)

func TestValidatePerfCorpusBindingMatchesCurrentAppRoot(t *testing.T) {
	sess := newFakeSession(t, fakeWorksHandler(0, nil))
	rep := &report.Report{}
	manifest := corpus.Manifest{
		QueryPublicationID: "qpub_00000000-0000-7000-8000-000000000000",
		CatalogRevisionID:  "crev_00000000-0000-7000-8000-000000000000",
	}
	if !ValidatePerfCorpusBinding(rep, sess, manifest) || rep.FailureCount != 0 {
		t.Fatalf("匹配的 AppRoot/manifest 被拒绝: findings=%+v", rep.Findings)
	}
	manifest.QueryPublicationID = "qpub_00000000-0000-7000-8000-000000000001"
	if ValidatePerfCorpusBinding(rep, sess, manifest) || rep.FailureCount != 1 {
		t.Fatalf("错配的 AppRoot/manifest 未被拒绝: findings=%+v", rep.Findings)
	}
}

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
	combos := buildFullMatrix([]int{20, 100, 200}, []int{1, 4, 16}, 30, 30, 1_000)
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

// TestBuildFullMatrixRaisesRunsOnlyForP99CarryingShapes 锁定 P99 样本量策略：承载 P99
// 的类别把 runs 提到 p99Runs，其余类别保持常规 runs（把 100 次重复加到已知秒级退化的
// 类别上只会重复证明同一个已知问题并挤爆时间预算）。
func TestBuildFullMatrixRaisesRunsOnlyForP99CarryingShapes(t *testing.T) {
	combos := buildFullMatrix([]int{20}, []int{1}, 30, report.MinSamplesForP99, 1_000)
	for _, c := range combos {
		if p99CarryingShapes[c.shape.name] {
			if !c.carriesP99 || c.runs != report.MinSamplesForP99 {
				t.Fatalf("承载 P99 的类别 %q 应有 carriesP99=true runs=%d，得到 carriesP99=%v runs=%d",
					c.shape.name, report.MinSamplesForP99, c.carriesP99, c.runs)
			}
			continue
		}
		if c.carriesP99 || c.runs != 30 {
			t.Fatalf("非 P99 类别 %q 不应被提高 runs：carriesP99=%v runs=%d", c.shape.name, c.carriesP99, c.runs)
		}
	}
}

// TestBuildFullMatrixKeepsP99FlagWhenRunsAreTooLow 覆盖"要求 P99 却给不够样本"的配置：
// 组合仍然被标记为承载 P99，从而在报告里以失败 finding 暴露，而不是悄悄产出一个其实
// 等于最大值的 p99。
func TestBuildFullMatrixKeepsP99FlagWhenRunsAreTooLow(t *testing.T) {
	for _, c := range buildFullMatrix([]int{20}, []int{1}, 30, 10, 1_000) {
		if p99CarryingShapes[c.shape.name] && (!c.carriesP99 || c.runs != 30) {
			t.Fatalf("p99Runs 低于常规 runs 时不应降低 runs，且必须保留 carriesP99: %+v", c)
		}
	}
}

// TestDirectionalMatrixNeverCarriesP99 复核精简采样矩阵不承载任何分位数门禁。
func TestDirectionalMatrixNeverCarriesP99(t *testing.T) {
	for _, c := range buildDirectionalMatrix(1_000) {
		if c.carriesP99 {
			t.Fatalf("directional 矩阵只提供方向性证据，不应承载 P99: %+v", c)
		}
	}
}

func TestBuildDirectionalMatrixNeverExceedsOneRunForKnownSlowShapes(t *testing.T) {
	for _, c := range buildDirectionalMatrix(1_000) {
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
	runOneCombination(rep, sess, combo, 200*time.Millisecond, time.Now().Add(10*time.Second), 0, report.CacheStateColdProcess)
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
	for _, finding := range rep.Findings {
		if finding.Name == "perf/browse-limit20-concurrency2-no-failed-runs" && !strings.Contains(finding.Detail, "requestDeadline=3") {
			t.Fatalf("请求超时被错误描述: %s", finding.Detail)
		}
	}
}

func TestRunOneCombinationStopsPastCombinationDeadline(t *testing.T) {
	sess := newFakeSession(t, fakeWorksHandler(0, nil))
	rep := &report.Report{}
	combo := combination{shape: perfShapes()["browse"], limit: 20, concurrency: 1, runs: 1000}
	// 组合截止时间已经过去：不应该派发任何请求，全部计入 NotAttemptedRuns，不折叠进
	// FailedRuns——否则会破坏 successfulRuns+failedRuns==attemptedRuns 恒等式。
	runOneCombination(rep, sess, combo, 5*time.Second, time.Now().Add(-1*time.Millisecond), 0, report.CacheStateColdProcess)
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
	for _, finding := range rep.Findings {
		if finding.Name == "perf/browse-limit20-concurrency1-no-failed-runs" && !strings.Contains(finding.Detail, "combinationDeadline=1000") {
			t.Fatalf("组合截止未被准确描述: %s", finding.Detail)
		}
	}
}

func TestRunPerfMatrixAbortsOnScenarioTimeoutAndSavesPartial(t *testing.T) {
	sess := newFakeSession(t, fakeWorksHandler(50*time.Millisecond, nil))
	rep := &report.Report{}
	combos := []combination{
		{shape: perfShapes()["browse"], limit: 20, concurrency: 1, runs: 2},
		{shape: perfShapes()["browse"], limit: 100, concurrency: 1, runs: 2},
		{shape: perfShapes()["browse"], limit: 200, concurrency: 1, runs: 2},
	}
	timeouts := PerfTimeouts{PerRequest: time.Second, PerCombination: time.Second, Scenario: 60 * time.Millisecond}
	saveCount := 0
	RunPerfMatrix(rep, sess, combos, timeouts, PerfOptions{ProcessColdAtStart: true}, func() error { saveCount++; return nil })

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

// TestRunOneCombinationExcludesWarmupFromSamples 是显式 warmup 阶段的核心断言：预热
// 请求必须真的发出（服务端计数），但绝不能进入 durations——否则每个组合最前面几个本来
// 就偏慢的样本会把分位数整体拉高，而那正是此前没有 warmup 时的实际状态。
func TestRunOneCombinationExcludesWarmupFromSamples(t *testing.T) {
	var hits int64
	sess := newFakeSession(t, fakeWorksHandler(0, &hits))
	rep := &report.Report{}
	combo := combination{shape: perfShapes()["browse"], limit: 20, concurrency: 1, runs: 4}
	runOneCombination(rep, sess, combo, 5*time.Second, time.Now().Add(10*time.Second), 3, report.CacheStateColdProcess)

	sample := rep.Latencies[0]
	if sample.WarmupRuns != 3 || sample.WarmupFailedRuns != 0 {
		t.Fatalf("warmup 计数 = %d/%d, want 3/0", sample.WarmupRuns, sample.WarmupFailedRuns)
	}
	// 预热不进入任何一类运行计数，恒等式因此仍然只覆盖测量请求。
	if sample.PlannedRuns != 4 || sample.AttemptedRuns != 4 || sample.SuccessfulRuns != 4 {
		t.Fatalf("预热污染了测量计数: %+v", sample)
	}
	if !sample.IdentityOK() {
		t.Fatalf("sample violates run-count identity: %+v", sample)
	}
	if got := atomic.LoadInt64(&hits); got != 7 {
		t.Fatalf("服务端收到 %d 次请求, want 7（3 次预热 + 4 次测量）；预热没有真的发出", got)
	}
	// 预热成功过，因此这是实测的 warm，而不是"进入组合时是冷的"。
	if sample.CacheState != report.CacheStateWarm {
		t.Fatalf("CacheState = %q, want %q", sample.CacheState, report.CacheStateWarm)
	}
}

// TestRunOneCombinationReportsUnknownCacheStateWhenWarmupFails 覆盖"要求预热但一次都
// 没成功"：既不能说热，也不能说测的是冷路径，只能 unknown，并且必须有一条失败 finding。
func TestRunOneCombinationReportsUnknownCacheStateWhenWarmupFails(t *testing.T) {
	sess := newFakeSession(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	rep := &report.Report{}
	combo := combination{shape: perfShapes()["browse"], limit: 20, concurrency: 1, runs: 2}
	runOneCombination(rep, sess, combo, 5*time.Second, time.Now().Add(10*time.Second), 2, report.CacheStateColdProcess)

	sample := rep.Latencies[0]
	if sample.CacheState != report.CacheStateUnknown {
		t.Fatalf("CacheState = %q, want %q", sample.CacheState, report.CacheStateUnknown)
	}
	if sample.WarmupFailedRuns != 2 {
		t.Fatalf("WarmupFailedRuns = %d, want 2", sample.WarmupFailedRuns)
	}
	assertFailedFinding(t, rep, "perf/browse-limit20-concurrency1-cache-state-measured")
}

// TestRunPerfMatrixLabelsCacheStatePerCombination 锁定缓存状态是按组合实测的：没有预热
// 时，第一个组合是 cold-process（进程未服务过查询），其后的组合只被前序组合偶然预热，
// 必须记为 warm-incidental —— 而不是像旧实现那样统统写成 warm。
func TestRunPerfMatrixLabelsCacheStatePerCombination(t *testing.T) {
	sess := newFakeSession(t, fakeWorksHandler(0, nil))
	rep := &report.Report{}
	combos := []combination{
		{shape: perfShapes()["browse"], limit: 20, concurrency: 1, runs: 2},
		{shape: perfShapes()["browse"], limit: 100, concurrency: 1, runs: 2},
		{shape: perfShapes()["browse"], limit: 200, concurrency: 1, runs: 2},
	}
	timeouts := PerfTimeouts{PerRequest: time.Second, PerCombination: time.Second, Scenario: time.Minute}
	RunPerfMatrix(rep, sess, combos, timeouts, PerfOptions{ProcessColdAtStart: true}, nil)

	if len(rep.Latencies) != 3 {
		t.Fatalf("样本数 = %d, want 3", len(rep.Latencies))
	}
	if rep.Latencies[0].CacheState != report.CacheStateColdProcess {
		t.Fatalf("首个组合 CacheState = %q, want %q", rep.Latencies[0].CacheState, report.CacheStateColdProcess)
	}
	for _, sample := range rep.Latencies[1:] {
		if sample.CacheState != report.CacheStateWarmIncidental {
			t.Fatalf("后续组合 CacheState = %q, want %q", sample.CacheState, report.CacheStateWarmIncidental)
		}
	}
}

// TestRunPerfMatrixReportsUnknownWhenProcessStateCannotBeAsserted 覆盖调用方无法断言
// 进程初始状态的情形（例如 all 场景在矩阵之前已经跑完全部 Correctness 断言）：第一个
// 组合只能是 unknown，不能顺手当成热的。
func TestRunPerfMatrixReportsUnknownWhenProcessStateCannotBeAsserted(t *testing.T) {
	sess := newFakeSession(t, fakeWorksHandler(0, nil))
	rep := &report.Report{}
	combos := []combination{{shape: perfShapes()["browse"], limit: 20, concurrency: 1, runs: 1}}
	timeouts := PerfTimeouts{PerRequest: time.Second, PerCombination: time.Second, Scenario: time.Minute}
	RunPerfMatrix(rep, sess, combos, timeouts, PerfOptions{ProcessColdAtStart: false}, nil)

	if rep.Latencies[0].CacheState != report.CacheStateUnknown {
		t.Fatalf("CacheState = %q, want %q", rep.Latencies[0].CacheState, report.CacheStateUnknown)
	}
	assertFailedFinding(t, rep, "perf/browse-limit20-concurrency1-cache-state-measured")
}

// TestRunPerfMatrixColdRestartLabelsEveryCombinationCold 覆盖冷路径：每个组合之前都重启
// galleryd，因此每个组合的首次请求都由从未服务过查询的新进程处理。
func TestRunPerfMatrixColdRestartLabelsEveryCombinationCold(t *testing.T) {
	sess := newFakeSession(t, fakeWorksHandler(0, nil))
	rep := &report.Report{}
	combos := []combination{
		{shape: perfShapes()["browse"], limit: 20, concurrency: 1, runs: 2},
		{shape: perfShapes()["browse"], limit: 100, concurrency: 1, runs: 2},
	}
	restarts := 0
	timeouts := PerfTimeouts{PerRequest: time.Second, PerCombination: time.Second, Scenario: time.Minute}
	RunPerfMatrix(rep, sess, combos, timeouts, PerfOptions{
		ColdRestart: func() (*environment.Session, error) { restarts++; return sess, nil },
	}, nil)

	if restarts != len(combos) {
		t.Fatalf("重启次数 = %d, want %d（每个组合前各一次）", restarts, len(combos))
	}
	for _, sample := range rep.Latencies {
		if sample.CacheState != report.CacheStateColdProcess {
			t.Fatalf("CacheState = %q, want %q", sample.CacheState, report.CacheStateColdProcess)
		}
	}
}

// TestRunPerfMatrixAbortsWhenColdRestartFails 复核冷路径失败不会退化成"继续测但标成冷"：
// 那会产出标注为冷、实际是热的样本，比没有结果更有害。
func TestRunPerfMatrixAbortsWhenColdRestartFails(t *testing.T) {
	sess := newFakeSession(t, fakeWorksHandler(0, nil))
	rep := &report.Report{}
	combos := []combination{
		{shape: perfShapes()["browse"], limit: 20, concurrency: 1, runs: 1},
		{shape: perfShapes()["browse"], limit: 100, concurrency: 1, runs: 1},
	}
	calls := 0
	timeouts := PerfTimeouts{PerRequest: time.Second, PerCombination: time.Second, Scenario: time.Minute}
	RunPerfMatrix(rep, sess, combos, timeouts, PerfOptions{
		ColdRestart: func() (*environment.Session, error) {
			calls++
			if calls == 2 {
				return nil, errors.New("模拟重启失败")
			}
			return sess, nil
		},
	}, nil)

	if len(rep.Latencies) != 1 {
		t.Fatalf("样本数 = %d, want 1（第二个组合的重启失败后不得继续测量）", len(rep.Latencies))
	}
	if !rep.AbortedByTimeLimit {
		t.Fatal("冷路径失败必须把整个矩阵标记为未完整执行")
	}
	assertFailedFinding(t, rep, "perf/cold-restart-succeeded")
}

// TestRunPerfMatrixRejectsColdRestartCombinedWithWarmup 复核自相矛盾的配置被显式暴露：
// 预热的作用正是消除冷启动效应，两者同时要求说明调用方配置错了。
func TestRunPerfMatrixRejectsColdRestartCombinedWithWarmup(t *testing.T) {
	sess := newFakeSession(t, fakeWorksHandler(0, nil))
	rep := &report.Report{}
	combos := []combination{{shape: perfShapes()["browse"], limit: 20, concurrency: 1, runs: 1}}
	timeouts := PerfTimeouts{PerRequest: time.Second, PerCombination: time.Second, Scenario: time.Minute}
	RunPerfMatrix(rep, sess, combos, timeouts, PerfOptions{
		WarmupRuns:  3,
		ColdRestart: func() (*environment.Session, error) { return sess, nil },
	}, nil)

	assertFailedFinding(t, rep, "perf/cache-state-configuration-consistent")
	if rep.Latencies[0].WarmupRuns != 0 {
		t.Fatalf("冲突配置下预热必须被关闭，得到 WarmupRuns=%d", rep.Latencies[0].WarmupRuns)
	}
	if rep.Latencies[0].CacheState != report.CacheStateColdProcess {
		t.Fatalf("CacheState = %q, want %q", rep.Latencies[0].CacheState, report.CacheStateColdProcess)
	}
}

// TestRunOneCombinationFailsP99CarryingComboWithTooFewSamples 复核"承载 P99 却样本不够"
// 一定以失败 finding 暴露：30 个样本下报出来的 p99 其实就是最大值。
func TestRunOneCombinationFailsP99CarryingComboWithTooFewSamples(t *testing.T) {
	sess := newFakeSession(t, fakeWorksHandler(0, nil))
	rep := &report.Report{}
	combo := combination{shape: perfShapes()["browse"], limit: 20, concurrency: 1, runs: 30, carriesP99: true}
	runOneCombination(rep, sess, combo, 5*time.Second, time.Now().Add(30*time.Second), 0, report.CacheStateColdProcess)

	sample := rep.Latencies[0]
	if sample.P99Estimable {
		t.Fatalf("30 个成功样本不应被标为 P99 可估: %+v", sample)
	}
	assertFailedFinding(t, rep, "perf/browse-limit20-concurrency1-p99-sample-size")
}

func TestReferenceFullMatrixBindsCandidateCountsAndP95Budgets(t *testing.T) {
	combos := buildFullMatrix([]int{20, 100, 200}, []int{1, 4, 16}, 30, report.MinSamplesForP99, corpus.ReferenceScale)
	wantCounts := map[string]int{
		"browse": 490_000, "selective-cjk": 500, "wide-cjk": 489_000,
		"filename-infix": 9_780, "structured-and": 50_000,
		"structured-or": 190_000, "overlay-favorite": 20_000,
	}
	if len(combos) != 63 {
		t.Fatalf("reference combinations=%d, want 63", len(combos))
	}
	for _, combo := range combos {
		if combo.candidateCount != wantCounts[combo.shape.name] {
			t.Fatalf("%s candidateCount=%d, want %d", combo.shape.name, combo.candidateCount, wantCounts[combo.shape.name])
		}
		if combo.p95BudgetMs <= 0 {
			t.Fatalf("reference combo lacks a P95 budget: %+v", combo)
		}
		wantOriginalVerification := combo.shape.name == "selective-cjk" || combo.shape.name == "wide-cjk" || combo.shape.name == "filename-infix"
		if combo.shape.originalTextVerification != wantOriginalVerification {
			t.Fatalf("%s originalTextVerification=%t, want %t", combo.shape.name, combo.shape.originalTextVerification, wantOriginalVerification)
		}
	}
	if profile := thresholdProfileFor(combos); profile != referenceP95ThresholdProfile {
		t.Fatalf("threshold profile=%q, want %q", profile, referenceP95ThresholdProfile)
	}
}

func TestNonReferenceMatricesDoNotClaimReferenceP95Thresholds(t *testing.T) {
	full := buildFullMatrix([]int{20}, []int{1}, 1, 1, 1_000)
	directional := buildDirectionalMatrix(corpus.ReferenceScale)
	for _, combos := range [][]combination{full, directional} {
		if profile := thresholdProfileFor(combos); profile != "" {
			t.Fatalf("non-reference matrix claimed threshold profile %q", profile)
		}
		for _, combo := range combos {
			if combo.p95BudgetMs != 0 {
				t.Fatalf("non-reference combo claimed P95 budget: %+v", combo)
			}
		}
	}
}

func TestRunOneCombinationEnforcesReferenceP95Budget(t *testing.T) {
	for _, test := range []struct {
		name     string
		delay    time.Duration
		wantPass bool
	}{
		{name: "pass", wantPass: true},
		{name: "fail", delay: 20 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			sess := newFakeSession(t, fakeWorksHandler(test.delay, nil))
			combo := combination{
				shape: perfShapes()["browse"], limit: 20, concurrency: 1, runs: 3,
				candidateCount: 490_000, p95BudgetMs: 10,
			}
			rep := &report.Report{}
			runOneCombination(rep, sess, combo, time.Second, time.Now().Add(10*time.Second), 0, report.CacheStateColdProcess)
			assertFindingPass(t, rep, "perf/browse-limit20-concurrency1-p95-budget", test.wantPass)
			if got := rep.Latencies[0]; got.CandidateCount != combo.candidateCount || got.P95BudgetMs != combo.p95BudgetMs {
				t.Fatalf("P95 threshold identity did not reach report: %+v", got)
			}
		})
	}
}

// TestRunPerfMatrixAlwaysPublishesInterpretationLimitations 复核每份性能报告都自带分位数
// 口径变更、缓存状态含义与执行计划断言缺口的说明；缺了它们，数字会被当成可与历史结果
// 直接比较的通过证据。
func TestRunPerfMatrixAlwaysPublishesInterpretationLimitations(t *testing.T) {
	sess := newFakeSession(t, fakeWorksHandler(0, nil))
	rep := &report.Report{}
	combos := []combination{{shape: perfShapes()["browse"], limit: 20, concurrency: 1, runs: 1}}
	timeouts := PerfTimeouts{PerRequest: time.Second, PerCombination: time.Second, Scenario: time.Minute}
	RunPerfMatrix(rep, sess, combos, timeouts, PerfOptions{ProcessColdAtStart: true, WarmupRuns: 1}, nil)

	for _, expected := range []string{"最近秩", "不代表冷存储读", "p99Estimable", "重复请求风暴", "EXPLAIN QUERY PLAN"} {
		found := false
		for _, limitation := range rep.Limitations {
			if strings.Contains(limitation, expected) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("性能报告缺少关于 %q 的限制说明: %v", expected, rep.Limitations)
		}
	}
}

func assertFailedFinding(t *testing.T, rep *report.Report, name string) {
	assertFindingPass(t, rep, name, false)
}

func assertFindingPass(t *testing.T, rep *report.Report, name string, want bool) {
	t.Helper()
	for _, finding := range rep.Findings {
		if finding.Name == name {
			if finding.Pass != want {
				t.Fatalf("finding %q pass=%t, want %t; detail=%s", name, finding.Pass, want, finding.Detail)
			}
			return
		}
	}
	names := make([]string, 0, len(rep.Findings))
	for _, finding := range rep.Findings {
		names = append(names, finding.Name)
	}
	t.Fatalf("没有找到 finding %q；现有: %v", name, names)
}

func TestRunPerfMatrixPartialReportIsParseable(t *testing.T) {
	sess := newFakeSession(t, fakeWorksHandler(0, nil))
	rep := &report.Report{}
	combos := buildFullMatrix([]int{20}, []int{1}, 2, 2, 1_000)[:2]
	timeouts := PerfTimeouts{PerRequest: time.Second, PerCombination: time.Second, Scenario: time.Minute}
	var lastSnapshot []byte
	RunPerfMatrix(rep, sess, combos, timeouts, PerfOptions{ProcessColdAtStart: true}, func() error {
		encoded, err := json.Marshal(rep)
		if err != nil {
			t.Fatalf("partial report not marshalable: %v", err)
		}
		lastSnapshot = encoded
		return nil
	})
	if lastSnapshot == nil {
		t.Fatal("expected at least one partial snapshot")
	}
	var decoded map[string]any
	if err := json.Unmarshal(lastSnapshot, &decoded); err != nil {
		t.Fatalf("partial report JSON not parseable: %v", err)
	}
}

func queryPerfResumeCheckpoint(combos []combination, timeouts PerfTimeouts, opts PerfOptions, completed int) *report.Report {
	definition := queryPerfMatrixDefinition(combos, timeouts, opts)
	rep := &report.Report{
		StartedAt:             "2026-07-29T00:00:00Z",
		PlannedCombinations:   len(combos),
		CompletedCombinations: completed,
		QueryPerfMatrix:       &definition,
		Limitations:           perfLimitations(opts, combos),
	}
	for i := 0; i < completed; i++ {
		combo := combos[i]
		durations := make([]time.Duration, combo.runs)
		for j := range durations {
			durations[j] = time.Duration(j+1) * time.Millisecond
		}
		cacheState := report.CacheStateWarm
		if opts.ColdRestart != nil {
			cacheState = report.CacheStateColdProcess
		}
		rep.Latencies = append(rep.Latencies, report.Summarize(report.Measurement{
			Category: combo.shape.name, Limit: combo.limit, Concurrency: combo.concurrency,
			PlannedRuns: combo.runs, AttemptedRuns: combo.runs, Durations: durations,
			WarmupRuns: opts.WarmupRuns, CacheState: cacheState,
			CarriesP99: combo.carriesP99, CandidateCount: combo.candidateCount,
			OriginalTextVerification: combo.shape.originalTextVerification, P95BudgetMs: combo.p95BudgetMs,
		}))
	}
	if completed < len(combos) {
		rep.AbortedByTimeLimit = true
		rep.AbortReason = "测试分窗到期"
		rep.Findings = append(rep.Findings,
			report.Finding{Name: queryPerfTerminalFinding, Pass: false, Detail: rep.AbortReason},
			report.Finding{Name: environmentGateFinding, Pass: true})
		rep.FailureCount = 1
	}
	return rep
}

func TestRunPerfMatrixResumesSuccessfulPrefixWithoutRepeating(t *testing.T) {
	var hits int64
	sess := newFakeSession(t, fakeWorksHandler(0, &hits))
	combos := []combination{
		{shape: perfShapes()["browse"], limit: 20, concurrency: 1, runs: 2},
		{shape: perfShapes()["browse"], limit: 100, concurrency: 1, runs: 2},
		{shape: perfShapes()["selective-cjk"], limit: 20, concurrency: 1, runs: 2},
	}
	timeouts := PerfTimeouts{PerRequest: time.Second, PerCombination: time.Second, Scenario: time.Minute}
	opts := PerfOptions{WarmupRuns: 1, PublicationFingerprint: "publication-fingerprint"}
	rep := queryPerfResumeCheckpoint(combos, timeouts, opts, 1)
	first := rep.Latencies[0]
	opts.Resume = true
	saves := 0
	if err := RunPerfMatrix(rep, sess, combos, timeouts, opts, func() error { saves++; return nil }); err != nil {
		t.Fatalf("resume 被拒绝: %v", err)
	}

	if got := atomic.LoadInt64(&hits); got != 6 {
		t.Fatalf("续跑发出了 %d 个请求，want 6（只执行剩余 2 组合，各 1 warmup + 2 measured）", got)
	}
	if len(rep.Latencies) != 3 || rep.CompletedCombinations != 3 || rep.Latencies[0] != first {
		t.Fatalf("续跑没有保留精确前缀或完成剩余组合: completed=%d latencies=%+v", rep.CompletedCombinations, rep.Latencies)
	}
	if rep.QueryPerfMatrix.ResumeCount != 1 || rep.QueryPerfMatrix.LastResumedAt == "" {
		t.Fatalf("resume metadata=%+v", rep.QueryPerfMatrix)
	}
	if rep.AbortedByTimeLimit || rep.FailureCount != 0 {
		t.Fatalf("成功续跑仍标记失败: aborted=%v failures=%d", rep.AbortedByTimeLimit, rep.FailureCount)
	}
	foundTerminal := 0
	for _, finding := range rep.Findings {
		if finding.Name == queryPerfTerminalFinding {
			foundTerminal++
			if !finding.Pass {
				t.Fatalf("最终 terminal finding 未通过: %+v", finding)
			}
		}
		if finding.Name == environmentGateFinding {
			t.Fatal("旧窗口的 environment terminal finding 必须在续跑准备时移除")
		}
	}
	if foundTerminal != 1 || saves != 4 {
		t.Fatalf("terminal findings=%d checkpoints=%d, want 1/4", foundTerminal, saves)
	}
}

func TestRunPerfMatrixResumeRejectsDefinitionDriftWithoutMutation(t *testing.T) {
	sess := newFakeSession(t, fakeWorksHandler(0, nil))
	combos := []combination{{shape: perfShapes()["browse"], limit: 20, concurrency: 1, runs: 2}}
	timeouts := PerfTimeouts{PerRequest: time.Second, PerCombination: time.Second, Scenario: time.Minute}
	opts := PerfOptions{WarmupRuns: 1, PublicationFingerprint: "publication-a"}
	rep := queryPerfResumeCheckpoint(combos, timeouts, opts, 0)
	wantFingerprint := rep.QueryPerfMatrix.Fingerprint
	wantFailures := rep.FailureCount

	drifted := PerfOptions{Resume: true, WarmupRuns: 2, PublicationFingerprint: "publication-a"}
	if err := RunPerfMatrix(rep, sess, combos, timeouts, drifted, nil); err == nil {
		t.Fatal("warmup 漂移必须拒绝续跑")
	}
	if rep.QueryPerfMatrix.Fingerprint != wantFingerprint || rep.QueryPerfMatrix.ResumeCount != 0 || rep.FailureCount != wantFailures {
		t.Fatalf("被拒绝的续跑修改了断点: %+v", rep)
	}

	publicationDrift := PerfOptions{Resume: true, WarmupRuns: 1, PublicationFingerprint: "publication-b"}
	if err := RunPerfMatrix(rep, sess, combos, timeouts, publicationDrift, nil); err == nil {
		t.Fatal("publication 漂移必须拒绝续跑")
	}
}

func TestQueryPerfFingerprintAllowsNewScenarioWindowOnly(t *testing.T) {
	combos := []combination{{shape: perfShapes()["browse"], limit: 20, concurrency: 1, runs: 2}}
	opts := PerfOptions{WarmupRuns: 1, PublicationFingerprint: "publication-fingerprint"}
	base := PerfTimeouts{PerRequest: time.Second, PerCombination: time.Minute, Scenario: 10 * time.Minute}
	longerWindow := base
	longerWindow.Scenario = time.Hour
	if queryPerfMatrixDefinition(combos, base, opts).Fingerprint != queryPerfMatrixDefinition(combos, longerWindow, opts).Fingerprint {
		t.Fatal("scenario timeout 只定义续跑窗口，不应改变矩阵指纹")
	}
	driftedCombination := base
	driftedCombination.PerCombination = 2 * time.Minute
	if queryPerfMatrixDefinition(combos, base, opts).Fingerprint == queryPerfMatrixDefinition(combos, driftedCombination, opts).Fingerprint {
		t.Fatal("单组合超时会改变样本含义，必须改变矩阵指纹")
	}
}

func TestQueryPerfFingerprintBindsTotalBudget(t *testing.T) {
	combos := []combination{{shape: perfShapes()["browse"], limit: 20, concurrency: 1, runs: 2}}
	timeouts := PerfTimeouts{PerRequest: time.Second, PerCombination: time.Minute, Scenario: time.Minute}
	opts := PerfOptions{WarmupRuns: 1, PublicationFingerprint: "publication-fingerprint"}
	original := galleryquery.TotalBudget
	defer func() { galleryquery.TotalBudget = original }()
	base := queryPerfMatrixDefinition(combos, timeouts, opts)
	galleryquery.TotalBudget = original - 1
	drifted := queryPerfMatrixDefinition(combos, timeouts, opts)
	if base.Fingerprint == drifted.Fingerprint || base.TotalBudget == drifted.TotalBudget {
		t.Fatalf("TotalBudget 漂移未进入矩阵身份: base=%+v drifted=%+v", base, drifted)
	}
}

func TestRunPerfMatrixResumesColdProcessPrefixWithRestartPerRemainingCombination(t *testing.T) {
	sess := newFakeSession(t, fakeWorksHandler(0, nil))
	combos := []combination{
		{shape: perfShapes()["browse"], limit: 20, concurrency: 1, runs: 2},
		{shape: perfShapes()["browse"], limit: 100, concurrency: 1, runs: 2},
		{shape: perfShapes()["browse"], limit: 200, concurrency: 1, runs: 2},
	}
	timeouts := PerfTimeouts{PerRequest: time.Second, PerCombination: time.Second, Scenario: time.Minute}
	restarts := 0
	opts := PerfOptions{PublicationFingerprint: "publication-fingerprint", ColdRestart: func() (*environment.Session, error) {
		restarts++
		return sess, nil
	}}
	rep := queryPerfResumeCheckpoint(combos, timeouts, opts, 1)
	opts.Resume = true
	if err := RunPerfMatrix(rep, sess, combos, timeouts, opts, nil); err != nil {
		t.Fatalf("cold-process resume 被拒绝: %v", err)
	}
	if restarts != 2 {
		t.Fatalf("cold-process resume 重启次数=%d, want 2（只覆盖剩余组合）", restarts)
	}
	for _, sample := range rep.Latencies {
		if sample.CacheState != report.CacheStateColdProcess {
			t.Fatalf("cold-process 续跑混入其它缓存状态: %+v", sample)
		}
	}
}

func TestRunPerfMatrixResumeRejectsFailedCompletedCombination(t *testing.T) {
	sess := newFakeSession(t, fakeWorksHandler(0, nil))
	combos := []combination{
		{shape: perfShapes()["browse"], limit: 20, concurrency: 1, runs: 2},
		{shape: perfShapes()["browse"], limit: 100, concurrency: 1, runs: 2},
	}
	timeouts := PerfTimeouts{PerRequest: time.Second, PerCombination: time.Second, Scenario: time.Minute}
	opts := PerfOptions{WarmupRuns: 1, PublicationFingerprint: "publication-fingerprint"}
	rep := queryPerfResumeCheckpoint(combos, timeouts, opts, 1)
	rep.Latencies[0].FailedRuns = 1
	rep.Latencies[0].SuccessfulRuns = 1

	opts.Resume = true
	if err := RunPerfMatrix(rep, sess, combos, timeouts, opts, nil); err == nil {
		t.Fatal("包含失败请求的已完成组合不能通过续跑被洗成成功")
	}
	if rep.QueryPerfMatrix.ResumeCount != 0 {
		t.Fatal("拒绝前缀后不应增加 resumeCount")
	}
}

func TestRunPerfMatrixResumeRejectsCorruptTerminalAccounting(t *testing.T) {
	sess := newFakeSession(t, fakeWorksHandler(0, nil))
	combos := []combination{
		{shape: perfShapes()["browse"], limit: 20, concurrency: 1, runs: 2},
		{shape: perfShapes()["browse"], limit: 100, concurrency: 1, runs: 2},
	}
	timeouts := PerfTimeouts{PerRequest: time.Second, PerCombination: time.Second, Scenario: time.Minute}
	opts := PerfOptions{WarmupRuns: 1, PublicationFingerprint: "publication-fingerprint"}
	rep := queryPerfResumeCheckpoint(combos, timeouts, opts, 1)
	rep.FailureCount = 0
	opts.Resume = true
	if err := RunPerfMatrix(rep, sess, combos, timeouts, opts, nil); err == nil || !strings.Contains(err.Error(), "failureCount") {
		t.Fatalf("损坏的 failureCount 未被拒绝: %v", err)
	}
}

func TestRunPerfMatrixResumeFinalizesCompleteCheckpointAndNoOpsFinishedReport(t *testing.T) {
	var hits int64
	sess := newFakeSession(t, fakeWorksHandler(0, &hits))
	combos := []combination{{shape: perfShapes()["browse"], limit: 20, concurrency: 1, runs: 2}}
	timeouts := PerfTimeouts{PerRequest: time.Second, PerCombination: time.Second, Scenario: time.Minute}
	opts := PerfOptions{WarmupRuns: 1, PublicationFingerprint: "publication-fingerprint"}
	rep := queryPerfResumeCheckpoint(combos, timeouts, opts, 1)
	// 模拟最后一个组合断点已经落盘、进程却在写 terminal finding 前被终止。
	rep.AbortedByTimeLimit = false
	rep.AbortReason = ""
	rep.Findings = nil
	rep.FailureCount = 0
	opts.Resume = true
	if err := RunPerfMatrix(rep, sess, combos, timeouts, opts, nil); err != nil {
		t.Fatalf("完整前缀终态补写失败: %v", err)
	}
	if atomic.LoadInt64(&hits) != 0 || rep.QueryPerfMatrix.ResumeCount != 1 || rep.FailureCount != 0 {
		t.Fatalf("补写终态不应重跑请求: hits=%d metadata=%+v failures=%d", hits, rep.QueryPerfMatrix, rep.FailureCount)
	}
	resumes := rep.QueryPerfMatrix.ResumeCount
	started := rep.StartedAt
	if err := RunPerfMatrix(rep, sess, combos, timeouts, opts, func() error { t.Fatal("完整报告 no-op 不应保存断点"); return nil }); err != nil {
		t.Fatalf("完整报告重复 resume 应 no-op: %v", err)
	}
	if rep.QueryPerfMatrix.ResumeCount != resumes || rep.StartedAt != started || atomic.LoadInt64(&hits) != 0 {
		t.Fatal("完整报告重复 resume 修改了执行事实")
	}
}

func TestRunPerfMatrixPropagatesInitialCheckpointFailure(t *testing.T) {
	sess := newFakeSession(t, fakeWorksHandler(0, nil))
	combos := []combination{{shape: perfShapes()["browse"], limit: 20, concurrency: 1, runs: 1}}
	timeouts := PerfTimeouts{PerRequest: time.Second, PerCombination: time.Second, Scenario: time.Minute}
	want := errors.New("模拟磁盘已满")
	err := RunPerfMatrix(&report.Report{}, sess, combos, timeouts, PerfOptions{WarmupRuns: 1}, func() error { return want })
	if err == nil || !strings.Contains(err.Error(), "原子保存查询性能断点") {
		t.Fatalf("checkpoint error=%v", err)
	}
}
