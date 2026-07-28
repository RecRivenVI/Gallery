package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestValidateBrowserProject(t *testing.T) {
	for _, project := range []string{"chromium", "firefox"} {
		if err := validateBrowserProject(project); err != nil {
			t.Fatalf("项目 %s 应受支持: %v", project, err)
		}
	}
	for _, project := range []string{"", "chrome", "edge", "../firefox"} {
		if err := validateBrowserProject(project); err == nil {
			t.Fatalf("项目 %q 应被拒绝", project)
		}
	}
}

func TestRetainDiagnosticsPreservesDistinctLogNames(t *testing.T) {
	logsRoot := t.TempDir()
	diagnosticsRoot := t.TempDir()
	first := filepath.Join(logsRoot, "galleryd.log")
	restored := filepath.Join(logsRoot, "galleryd-restored.log")
	if err := os.WriteFile(first, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(restored, []byte("restored"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := retainDiagnostics(first, diagnosticsRoot); err != nil {
		t.Fatal(err)
	}
	if err := retainDiagnostics(restored, diagnosticsRoot); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"galleryd.log":          "first",
		"galleryd-restored.log": "restored",
	} {
		got, err := os.ReadFile(filepath.Join(diagnosticsRoot, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("%s 内容=%q，期望 %q", name, got, want)
		}
	}
}

func TestSeedGovernanceFixturesUsesApplicationStateMachines(t *testing.T) {
	testRoot := t.TempDir()
	appRoot := filepath.Join(testRoot, "app")
	sourceRoot := filepath.Join(testRoot, "sources")
	fixtures, err := seedGovernanceFixtures(context.Background(), appRoot, sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"issue source":         fixtures.IssueSourceID,
		"issue":                fixtures.IssueID,
		"bind issue":           fixtures.IssueBindID,
		"bind target":          fixtures.IssueBindTargetID,
		"separate issue":       fixtures.IssueSeparateID,
		"lifecycle source":     fixtures.LifecycleSourceID,
		"lifecycle superseded": fixtures.LifecycleSupersededID,
		"lifecycle stale":      fixtures.LifecycleStaleID,
		"pagination source":    fixtures.PaginationSourceID,
		"structure source":     fixtures.StructureSourceID,
		"structure issue":      fixtures.StructureIssueID,
		"merge source":         fixtures.MergeSourceID,
		"merge issue":          fixtures.MergeIssueID,
		"merge target":         fixtures.MergeTargetWorkID,
		"consumed decision":    fixtures.ConsumedDecisionID,
		"orphan source":        fixtures.OrphanSourceID,
		"orphan binding":       fixtures.OrphanBindingID,
		"orphan unbind":        fixtures.OrphanUnbindBindingID,
		"orphan creator":       fixtures.OrphanCreatorBindingID,
		"orphan media":         fixtures.OrphanMediaBindingID,
		"media source":         fixtures.MediaSourceID,
		"media source key":     fixtures.MediaSourceKey,
	} {
		if value == "" {
			t.Fatalf("%s 未建立", name)
		}
	}
	if fixtures.PaginationIssueCount != 51 {
		t.Fatalf("分页问题数量=%d，期望 51", fixtures.PaginationIssueCount)
	}
	statePath := filepath.Join(testRoot, "governance-state.json")
	if err := writeGovernanceFixtureState(statePath, fixtures); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var decoded governanceFixtureState
	if err := json.Unmarshal(content, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, fixtures) {
		t.Fatalf("治理夹具状态往返不一致: got=%+v want=%+v", decoded, fixtures)
	}
}
