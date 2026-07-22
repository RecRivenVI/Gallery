package report

import (
	"path/filepath"
	"testing"
	"time"
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

func TestPercentileGoldenValues(t *testing.T) {
	values := []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	cases := []struct {
		p    float64
		want float64
	}{
		{0.50, 50},
		{0.95, 90},
		{0.99, 90},
		{1.0, 100},
	}
	for _, c := range cases {
		got := percentile(values, c.p)
		if got != c.want {
			t.Errorf("percentile(values, %.2f) = %v, want %v", c.p, got, c.want)
		}
	}
}

func TestPercentileEmptyInput(t *testing.T) {
	if got := percentile(nil, 0.95); got != 0 {
		t.Fatalf("percentile(nil, 0.95) = %v, want 0", got)
	}
}

// TestSummarizeIdentityHoldsWithNoLoss 覆盖恒等式在"全部派发的请求都有明确结局"
// 时成立：planned=attempted=5（组合截止时间充裕，全部派发），2 个成功、3 个失败
// （其中 1 个是超时），0 个未派发。
func TestSummarizeIdentityHoldsWithNoLoss(t *testing.T) {
	durations := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}
	sample := Summarize("browse", 20, 4, 5, 5, durations, 3, 1, 0, "warm", 7, "exact", 7)
	if sample.PlannedRuns != 5 || sample.AttemptedRuns != 5 || sample.SuccessfulRuns != 2 || sample.FailedRuns != 3 || sample.TimedOutRuns != 1 || sample.NotAttemptedRuns != 0 {
		t.Fatalf("unexpected sample: %+v", sample)
	}
	if !sample.IdentityOK() {
		t.Fatalf("expected identity to hold: %+v", sample)
	}
}

// TestSummarizeIdentityHoldsWithNotAttempted 覆盖恒等式在"组合截止时间提前耗尽，
// 部分次数从未派发"时仍然成立：planned=10，只派发了 6 次（4 次因截止时间未派发），
// 派发的 6 次里 4 个成功、2 个失败。
func TestSummarizeIdentityHoldsWithNotAttempted(t *testing.T) {
	durations := []time.Duration{5 * time.Millisecond, 6 * time.Millisecond, 7 * time.Millisecond, 8 * time.Millisecond}
	sample := Summarize("wide-cjk", 20, 1, 10, 6, durations, 2, 0, 4, "warm", 3, "lower_bound", 10001)
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
