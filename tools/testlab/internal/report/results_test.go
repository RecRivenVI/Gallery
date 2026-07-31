package report

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/tools/testlab/internal/hostfacts"
)

func TestSanitizeDetailRedactsAbsolutePathsAndAddresses(t *testing.T) {
	cases := []string{
		`open C:\Users\RavenYin\AppData\Local\Temp\testlab\appdirs: access denied`,
		`dial tcp 127.0.0.1:54321: connection refused`,
		`request to http://localhost:8080/api/v1/works failed`,
		`\\?\Volume{...}\testlab\appdirs`,
	}
	for _, input := range cases {
		got := sanitizeDetail(input)
		if containsSensitiveMarker(got) {
			t.Errorf("sanitizeDetail(%q) = %q still contains a sensitive marker", input, got)
		}
	}
}

func TestReportSaveRejectsResidualSensitiveContent(t *testing.T) {
	report := &Report{SchemaVersion: 2, Scenario: "correctness"}
	// 直接构造一个绕过 Add()/sanitizeDetail 的 finding，模拟"某处遗漏调用脱敏"的情形，
	// 验证 Save 前的最终防线仍然会拒绝写入。
	report.Findings = append(report.Findings, Finding{Name: "leak", Pass: false, Detail: `C:\secret\path`})
	path := filepath.Join(t.TempDir(), "report.json")
	if err := report.Save(path); err == nil {
		t.Fatal("expected Save to reject a report containing an absolute path")
	}
}

func TestReportSaveRejectsSensitiveCorpusSourceAlias(t *testing.T) {
	report := &Report{SchemaVersion: 2, Scenario: "perf", Corpus: &CorpusFacts{
		Scale: 10, SourceCount: 1, SourceAliases: []string{`C:\private\source`},
	}}
	if err := report.Save(filepath.Join(t.TempDir(), "report.json")); err == nil {
		t.Fatal("expected Save to reject a source alias containing an absolute path")
	}
}

// TestReportSaveRejectsSensitiveEnvironmentFacts 复核环境事实同样受敏感内容防线约束：
// 存储介质判定天然接触卷路径与设备路径，一旦某个采集分支把它们带进 Facts，必须在写盘
// 之前被拒绝，而不是随结果文件一起提交出去。
func TestReportSaveRejectsSensitiveEnvironmentFacts(t *testing.T) {
	report := &Report{SchemaVersion: 2, Scenario: "perf"}
	report.Environment = &hostfacts.Facts{
		OSVersion: "Windows 11", CPUModel: "CPU",
		Storage: hostfacts.Storage{Medium: hostfacts.MediumSSD, MediumEvidence: `\\?\Volume{0}\ seekPenalty=false`},
	}
	if err := report.Save(filepath.Join(t.TempDir(), "report.json")); err == nil {
		t.Fatal("expected Save to reject an environment fact containing a device path")
	}
}

func TestReportSaveAcceptsMeasuredEnvironmentFacts(t *testing.T) {
	report := &Report{SchemaVersion: 2, Scenario: "perf", StorageClass: "ssd"}
	report.Environment = &hostfacts.Facts{
		OSFamily: "windows", OSVersion: "Windows 11 Pro 24H2 10.0.28000", CPUModel: "Example CPU",
		CPULogicalCores: 32, MemoryTotalBytes: 137438953472, SQLiteVersion: "3.50.0",
		Storage: hostfacts.Storage{Medium: hostfacts.MediumSSD, MediumEvidence: "PhysicalDrive3 seekPenalty=false", LogicalDrive: "D", PhysicalDrive: "D"},
	}
	if err := report.Save(filepath.Join(t.TempDir(), "report.json")); err != nil {
		t.Fatalf("Save() 拒绝了一份合法的实测环境事实: %v", err)
	}
}

func TestReportAddSanitizesBeforeStoring(t *testing.T) {
	report := &Report{}
	report.Add("check", false, `dial tcp 127.0.0.1:12345: refused`)
	if containsSensitiveMarker(report.Findings[0].Detail) {
		t.Fatalf("Add() stored unsanitized detail: %q", report.Findings[0].Detail)
	}
}

func TestReportSaveAcceptsCleanReport(t *testing.T) {
	report := &Report{SchemaVersion: 2, Scenario: "correctness", ScenarioAlias: "smoke-1k", StorageClass: "ssd"}
	report.Add("filter/tag", true, "")
	report.Add("filter/library.id", false, "status=400 code=CURSOR_INVALID")
	path := filepath.Join(t.TempDir(), "report.json")
	if err := report.Save(path); err != nil {
		t.Fatalf("Save() failed on a clean report: %v", err)
	}
}

func TestReportLoadReadsAtomicCheckpoint(t *testing.T) {
	want := &Report{SchemaVersion: 2, Scenario: "stage4-publication-change-matrix", Scale: 100,
		PlannedCombinations: 2, CompletedCombinations: 1,
		QueryPerfMatrix: &QueryPerfMatrix{Fingerprint: "matrix", PublicationFingerprint: "publication", CacheMode: "warm",
			WarmupRuns: 3, PerRequestTimeoutMs: 30_000, PerCombinationTimeoutMs: 300_000, ResumeCount: 1, LastResumedAt: "2026-07-29T00:00:00Z"}}
	path := filepath.Join(t.TempDir(), "report.json")
	if err := want.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Scenario != want.Scenario || got.Scale != want.Scale || got.CompletedCombinations != 1 ||
		got.QueryPerfMatrix == nil || *got.QueryPerfMatrix != *want.QueryPerfMatrix {
		t.Fatalf("Load()=%+v", got)
	}
}

// TestPercentileNearestRankGoldenValues 锁定最近秩定义：秩 = ceil(p·n)，取升序第
// 「秩」个样本。n=10 时 P95 的秩是 ceil(9.5)=10，即最大值——旧实现（floor(p·n)−1）
// 在这里返回 90，低整整一名。
func TestPercentileNearestRankGoldenValues(t *testing.T) {
	values := []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	cases := []struct {
		perMille int
		want     float64
	}{
		{perMilleP50, 50},
		{perMilleP95, 100},
		{perMilleP99, 100},
		{1000, 100},
	}
	for _, c := range cases {
		got := percentileNearestRank(values, c.perMille)
		if got != c.want {
			t.Errorf("percentileNearestRank(values, %d) = %v, want %v", c.perMille, got, c.want)
		}
	}
}

// TestPercentileNearestRankIsExactAtFloatBoundaries 是浮点误差的反向对照：0.95 在
// 二进制下不可精确表示，float64 写法的 math.Ceil(0.95*20) 会返回 20 而不是 19。整数
// 千分比实现必须在这些边界上返回精确的 ceil(p·n)。
func TestPercentileNearestRankIsExactAtFloatBoundaries(t *testing.T) {
	values := make([]float64, 20)
	for i := range values {
		values[i] = float64(i + 1)
	}
	if got := percentileNearestRank(values, perMilleP95); got != 19 {
		t.Fatalf("n=20 时 P95 的最近秩应为第 19 个样本（值 19），得到 %v", got)
	}
	fifty := make([]float64, 50)
	for i := range fifty {
		fifty[i] = float64(i + 1)
	}
	if got := percentileNearestRank(fifty, perMilleP50); got != 25 {
		t.Fatalf("n=50 时 P50 的最近秩应为第 25 个样本（值 25），得到 %v", got)
	}
}

// TestPercentileNearestRankRaisesTailVersusOldFloorDefinition 明确记录本轮口径变更对
// 历史数字的影响：同一批 30 个样本下，新口径的 P95/P99 都比旧口径 `floor(p·n)−1`
// 高一名，且 P99 直接落在最大值上——这正是「30 个样本估不了 P99」的直接证据。
func TestPercentileNearestRankRaisesTailVersusOldFloorDefinition(t *testing.T) {
	values := make([]float64, 30)
	for i := range values {
		values[i] = float64(i + 1)
	}
	oldFloorDefinition := func(p float64) float64 {
		index := int(p*float64(len(values))) - 1
		if index < 0 {
			index = 0
		}
		return values[index]
	}
	if got, old := percentileNearestRank(values, perMilleP95), oldFloorDefinition(0.95); got != 29 || old != 28 {
		t.Fatalf("n=30 时 P95 新口径应为 29（旧口径 28），得到 new=%v old=%v", got, old)
	}
	if got, old := percentileNearestRank(values, perMilleP99), oldFloorDefinition(0.99); got != 30 || old != 29 {
		t.Fatalf("n=30 时 P99 新口径应落在最大值 30（旧口径 29），得到 new=%v old=%v", got, old)
	}
}

func TestPercentileEmptyInput(t *testing.T) {
	if got := percentileNearestRank(nil, perMilleP95); got != 0 {
		t.Fatalf("percentileNearestRank(nil, P95) = %v, want 0", got)
	}
}

// TestSummarizeMarksP99UnestimableBelowMinimumSamples 锁定样本量判据：30 个样本不足以
// 估计 P99，必须被标为不可估；达到 MinSamplesForP99 才可估。
func TestSummarizeMarksP99UnestimableBelowMinimumSamples(t *testing.T) {
	build := func(n int) LatencySample {
		durations := make([]time.Duration, n)
		for i := range durations {
			durations[i] = time.Duration(i+1) * time.Millisecond
		}
		return Summarize(Measurement{
			Category: "browse", Limit: 20, Concurrency: 1,
			PlannedRuns: n, AttemptedRuns: n, Durations: durations,
			CacheState: CacheStateWarm, CarriesP99: true,
		})
	}
	if sample := build(30); sample.P99Estimable {
		t.Fatalf("30 个样本不应被标为 P99 可估: %+v", sample)
	}
	if sample := build(MinSamplesForP99); !sample.P99Estimable {
		t.Fatalf("%d 个样本应被标为 P99 可估: %+v", MinSamplesForP99, sample)
	}
	if sample := build(30); sample.PercentileMethod != PercentileMethodNearestRank {
		t.Fatalf("PercentileMethod = %q, want %q", sample.PercentileMethod, PercentileMethodNearestRank)
	}
}

// TestSummarizeNeverInventsWarmCacheState 是本轮修复的回归防线：调用方没有给出实测
// 缓存状态时，结果必须是 unknown，绝不能像旧实现那样默认写成 warm。
func TestSummarizeNeverInventsWarmCacheState(t *testing.T) {
	sample := Summarize(Measurement{Category: "browse", PlannedRuns: 1, AttemptedRuns: 1})
	if sample.CacheState != CacheStateUnknown {
		t.Fatalf("CacheState = %q, want %q", sample.CacheState, CacheStateUnknown)
	}
}

// TestSummarizeIdentityHoldsWithNoLoss 覆盖恒等式在"全部派发的请求都有明确结局"
// 时成立：planned=attempted=5（组合截止时间充裕，全部派发），2 个成功、3 个失败
// （其中 1 个是超时），0 个未派发。
func TestSummarizeIdentityHoldsWithNoLoss(t *testing.T) {
	durations := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}
	sample := Summarize(Measurement{
		Category: "browse", Limit: 20, Concurrency: 4,
		PlannedRuns: 5, AttemptedRuns: 5, Durations: durations,
		FailedRuns: 3, TimedOutRuns: 1, NotAttemptedRuns: 0,
		CacheState: CacheStateWarm, CandidateCount: 490_000,
		OriginalTextVerification: true, P95BudgetMs: 150,
		HitCount: 7, TotalMode: "exact", TotalValue: 7,
	})
	if sample.PlannedRuns != 5 || sample.AttemptedRuns != 5 || sample.SuccessfulRuns != 2 || sample.FailedRuns != 3 || sample.TimedOutRuns != 1 || sample.NotAttemptedRuns != 0 {
		t.Fatalf("unexpected sample: %+v", sample)
	}
	if !sample.IdentityOK() {
		t.Fatalf("expected identity to hold: %+v", sample)
	}
	if sample.CandidateCount != 490_000 || !sample.OriginalTextVerification || sample.P95BudgetMs != 150 {
		t.Fatalf("threshold identity was not preserved: %+v", sample)
	}
}

// TestSummarizeIdentityHoldsWithNotAttempted 覆盖恒等式在"组合截止时间提前耗尽，
// 部分次数从未派发"时仍然成立：planned=10，只派发了 6 次（4 次因截止时间未派发），
// 派发的 6 次里 4 个成功、2 个失败。
func TestSummarizeIdentityHoldsWithNotAttempted(t *testing.T) {
	durations := []time.Duration{5 * time.Millisecond, 6 * time.Millisecond, 7 * time.Millisecond, 8 * time.Millisecond}
	sample := Summarize(Measurement{
		Category: "wide-cjk", Limit: 20, Concurrency: 1,
		PlannedRuns: 10, AttemptedRuns: 6, Durations: durations,
		FailedRuns: 2, TimedOutRuns: 0, NotAttemptedRuns: 4,
		CacheState: CacheStateWarm, HitCount: 3, TotalMode: "lower_bound", TotalValue: 10001,
	})
	if !sample.IdentityOK() {
		t.Fatalf("expected identity to hold: %+v", sample)
	}
	if sample.SuccessfulRuns+sample.FailedRuns != sample.AttemptedRuns {
		t.Fatalf("successful+failed != attempted: %+v", sample)
	}
	if sample.AttemptedRuns+sample.NotAttemptedRuns != sample.PlannedRuns {
		t.Fatalf("attempted+notAttempted != planned: %+v", sample)
	}
}

// TestSummarizeIdentityCaughtWhenViolated 是恒等式检查本身的对照测试：构造一个
// 违反恒等式的样本（合计数与 attempted 不符），确认 IdentityOK 能识别出来，防止
// 未来重构悄悄破坏这条不变量而没有任何测试报警。
func TestSummarizeIdentityCaughtWhenViolated(t *testing.T) {
	broken := LatencySample{PlannedRuns: 10, AttemptedRuns: 6, SuccessfulRuns: 4, FailedRuns: 4, NotAttemptedRuns: 4}
	if broken.IdentityOK() {
		t.Fatalf("expected a deliberately broken sample to fail IdentityOK: %+v", broken)
	}
}
