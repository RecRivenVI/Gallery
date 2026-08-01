package maintenanceperf

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/tools/testlab/internal/report"
)

func TestOverlappingBrowseSamplesUsesRequestIntervals(t *testing.T) {
	base := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	samples := []browseSample{
		{started: base.Add(-2 * time.Second), finished: base.Add(-time.Second)},
		{started: base.Add(-time.Millisecond), finished: base.Add(time.Millisecond)},
		{started: base.Add(500 * time.Millisecond), finished: base.Add(700 * time.Millisecond)},
		{started: base.Add(time.Second), finished: base.Add(2 * time.Second)},
	}
	got := overlappingBrowseSamples(samples, base, base.Add(time.Second))
	if len(got) != 2 {
		t.Fatalf("overlappingBrowseSamples len=%d, want 2", len(got))
	}
}

func TestFillBrowseSummaryKeepsFailuresAndNearestRankP95(t *testing.T) {
	observations := make([]browseSample, 0, 21)
	for i := 1; i <= 20; i++ {
		observations = append(observations, browseSample{success: true, duration: time.Duration(i) * time.Millisecond})
	}
	observations = append(observations, browseSample{timedOut: true})
	var got report.MaintenanceRunSample
	fillBrowseSummary(&got, observations)
	if !got.DuringObserved || got.DuringAttempts != 21 || got.DuringSuccessful != 20 || got.DuringFailed != 1 || got.DuringTimedOut != 1 {
		t.Fatalf("unexpected browse summary: %+v", got)
	}
	if got.DuringP50Ms != 10 || got.DuringP95Ms != 19 || got.DuringMaxMs != 20 {
		t.Fatalf("unexpected nearest-rank summary: %+v", got)
	}
}

func TestSummarizeMaintenanceDurations(t *testing.T) {
	result := report.MaintenanceOperationResult{Operation: OperationCatalogGC, PlannedRuns: 3, CompletedRuns: 2,
		Runs: []report.MaintenanceRunSample{
			{DurationMs: 30, FinalStatus: "completed"},
			{DurationMs: 10, FinalStatus: "completed"},
			{DurationMs: 999, FinalStatus: "failed"},
		}}
	got := summarize(result)
	if got.DurationMinMs != 10 || got.DurationP50Ms != 10 || got.DurationP95Ms != 30 || got.DurationMaxMs != 30 {
		t.Fatalf("unexpected duration summary: %+v", got)
	}
}

func TestMaintenanceFingerprintBindsRunAndSamplingBudgets(t *testing.T) {
	base := Options{GCRuns: 1, CheckpointRuns: 1, VacuumRuns: 1, QueryInterval: 10 * time.Millisecond,
		QueryTimeout: time.Second, OperationTimeout: time.Minute, PublicationFingerprint: "publication"}
	a := fingerprint("pub", base)
	base.VacuumRuns = 2
	b := fingerprint("pub", base)
	if a == b {
		t.Fatal("fingerprint did not change when vacuum runs changed")
	}
	base.HistoricalPublication = true
	c := fingerprint("pub", base)
	if b == c {
		t.Fatal("fingerprint did not change when publication role changed")
	}
}

func TestDataBytesCountsOnlyRegularFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a"), []byte("123"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "b"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := dataBytes(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != 8 {
		t.Fatalf("dataBytes=%d, want 8", got)
	}
}
