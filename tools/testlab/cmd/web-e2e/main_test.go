package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/storage"
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

func TestValidateRunModes(t *testing.T) {
	for _, modes := range [][]bool{
		{false, false, false, false},
		{true, false, false, false},
		{false, true, false, false},
		{false, false, true, false},
		{false, false, false, true},
	} {
		if err := validateRunModes(modes...); err != nil {
			t.Fatalf("合法模式 %v 被拒绝: %v", modes, err)
		}
	}
	for _, modes := range [][]bool{
		{true, true, false, false},
		{true, false, true, false},
		{true, false, false, true},
		{false, true, true, false},
		{false, true, false, true},
		{false, false, true, true},
		{true, true, true, true},
	} {
		if err := validateRunModes(modes...); err == nil {
			t.Fatalf("冲突模式 %v 未被拒绝", modes)
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
		"keep same source":     fixtures.KeepSameSourceID,
		"keep same issue":      fixtures.KeepSameIssueID,
		"keep same work":       fixtures.KeepSameOriginalWorkID,
		"create new source":    fixtures.CreateNewSourceID,
		"create new issue":     fixtures.CreateNewIssueID,
		"create new work":      fixtures.CreateNewOriginalWorkID,
		"merge new source":     fixtures.MergeNewSourceID,
		"merge new issue":      fixtures.MergeNewIssueID,
		"consumed decision":    fixtures.ConsumedDecisionID,
		"orphan source":        fixtures.OrphanSourceID,
		"orphan binding":       fixtures.OrphanBindingID,
		"orphan unbind":        fixtures.OrphanUnbindBindingID,
		"orphan creator":       fixtures.OrphanCreatorBindingID,
		"orphan media":         fixtures.OrphanMediaBindingID,
		"orphan reappear":      fixtures.OrphanReappearUnbindID,
		"orphan original work": fixtures.OrphanOriginalWorkID,
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

func TestAdvanceGovernanceFixturesConsumesDecisionsAndReappearsOrphans(t *testing.T) {
	ctx := context.Background()
	testRoot := t.TempDir()
	appRoot := filepath.Join(testRoot, "app")
	fixtures, err := seedGovernanceFixtures(ctx, appRoot, filepath.Join(testRoot, "sources"))
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(testRoot, "governance-state.json")
	if err := writeGovernanceFixtureState(statePath, fixtures); err != nil {
		t.Fatal(err)
	}

	dirs := appdirs.UnderRoot(appRoot)
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	systemClock := clock.System{}
	resources, err := application.NewResources(
		store.Control.SQL(), dirs, filesystem.OS{}, systemClock, identity.NewGenerator(systemClock),
	)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	for _, decision := range []struct {
		issueID string
		action  string
	}{
		{fixtures.KeepSameIssueID, "split_keep_same"},
		{fixtures.CreateNewIssueID, "split_create_new"},
		{fixtures.MergeNewIssueID, "merge_create_new"},
	} {
		issue, getErr := resources.GetBindingIssue(ctx, decision.issueID)
		if getErr != nil {
			_ = store.Close()
			t.Fatal(getErr)
		}
		if _, resolveErr := resources.ResolveSourceStructureIssue(
			ctx, issue.ID, "owner", decision.action, "", "", issue.Version,
		); resolveErr != nil {
			_ = store.Close()
			t.Fatal(resolveErr)
		}
	}
	for _, decision := range []struct {
		bindingID string
		action    string
		extend    int
	}{
		{fixtures.OrphanBindingID, "extend", 2},
		{fixtures.OrphanCreatorBindingID, "confirm_orphaned", 0},
		{fixtures.OrphanMediaBindingID, "retain", 0},
		{fixtures.OrphanReappearUnbindID, "unbind", 0},
	} {
		if _, decideErr := resources.DecideOrphanCandidate(
			ctx, decision.bindingID, decision.action, decision.extend,
		); decideErr != nil {
			_ = store.Close()
			t.Fatal(decideErr)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	advanced, err := advanceGovernanceFixtures(ctx, appRoot, statePath)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"keep same decision":        advanced.KeepSameDecisionID,
		"create new decision":       advanced.CreateNewDecisionID,
		"merge new decision":        advanced.MergeNewDecisionID,
		"reappeared work":           advanced.OrphanReappearedWorkID,
		"reappeared creator":        advanced.OrphanReappearedCreatorID,
		"reappeared media":          advanced.OrphanReappearedMediaID,
		"unbound replacement work":  advanced.OrphanUnboundNewWorkID,
		"unbound replacement media": advanced.OrphanUnboundNewMediaID,
	} {
		if value == "" {
			t.Fatalf("%s 未建立", name)
		}
	}
	if advanced.OrphanReappearedWorkID != advanced.OrphanOriginalWorkID ||
		advanced.OrphanReappearedCreatorID != advanced.OrphanOriginalCreatorID ||
		advanced.OrphanReappearedMediaID != advanced.OrphanOriginalMediaID {
		t.Fatal("非 manual_unbound 孤儿重现未复用原 Canonical 身份")
	}
	if advanced.OrphanUnboundNewWorkID == advanced.OrphanUnboundOldWorkID ||
		advanced.OrphanUnboundNewMediaID == advanced.OrphanUnboundOldMediaID {
		t.Fatal("manual_unbound 孤儿重现未建立新的 Canonical 身份")
	}
}
