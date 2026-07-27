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
	combos := buildFullMatrix([]int{20, 100, 200}, []int{1, 4, 16}, 30, 30)
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
	combos := buildFullMatrix([]int{20}, []int{1}, 30, report.MinSamplesForP99)
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
	for _, c := range buildFullMatrix([]int{20}, []int{1}, 30, 10) {
		if p99CarryingShapes[c.shape.name] && (!c.carriesP99 || c.runs != 30) {
			t.Fatalf("p99Runs 低于常规 runs 时不应降低 runs，且必须保留 carriesP99: %+v", c)
		}
	}
}

// TestDirectionalMatrixNeverCarriesP99 复核精简采样矩阵不承载任何分位数门禁。
func TestDirectionalMatrixNeverCarriesP99(t *testing.T) {
	for _, c := range buildDirectionalMatrix() {
		if c.carriesP99 {
			t.Fatalf("directional 矩阵只提供方向性证据，不应承载 P99: %+v", c)
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
	RunPerfMatrix(rep, sess, combos, timeouts, PerfOptions{ProcessColdAtStart: true}, func() { saveCount++ })

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

// TestRunPerfMatrixAlwaysPublishesInterpretationLimitations 复核每份性能报告都自带分位数
// 口径变更、缓存状态含义与执行计划断言缺口的说明；缺了它们，数字会被当成可与历史结果
// 直接比较的通过证据。
func TestRunPerfMatrixAlwaysPublishesInterpretationLimitations(t *testing.T) {
	sess := newFakeSession(t, fakeWorksHandler(0, nil))
	rep := &report.Report{}
	combos := []combination{{shape: perfShapes()["browse"], limit: 20, concurrency: 1, runs: 1}}
	timeouts := PerfTimeouts{PerRequest: time.Second, PerCombination: time.Second, Scenario: time.Minute}
	RunPerfMatrix(rep, sess, combos, timeouts, PerfOptions{ProcessColdAtStart: true, WarmupRuns: 1}, nil)

	for _, expected := range []string{"最近秩", "不代表冷存储读", "p99Estimable", "EXPLAIN QUERY PLAN"} {
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
	t.Helper()
	for _, finding := range rep.Findings {
		if finding.Name == name {
			if finding.Pass {
				t.Fatalf("finding %q 应当失败", name)
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
	combos := buildFullMatrix([]int{20}, []int{1}, 2, 2)[:2]
	timeouts := PerfTimeouts{PerRequest: time.Second, PerCombination: time.Second, Scenario: time.Minute}
	var lastSnapshot []byte
	RunPerfMatrix(rep, sess, combos, timeouts, PerfOptions{ProcessColdAtStart: true}, func() {
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
