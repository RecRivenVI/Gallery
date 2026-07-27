package application_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/storage"
)

type ev59RuleFixture struct {
	ctx       context.Context
	resources *application.Resources
	store     *storage.Store
	canonical []byte
}

func newEV59RuleFixture(t *testing.T) ev59RuleFixture {
	t.Helper()
	ctx := context.Background()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := clock.Fixed{Time: time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)}
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, now, identity.NewGenerator(now))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := os.ReadFile(filepath.Join("..", "rules", "testdata", "minimal-rule-package.json"))
	if err != nil {
		t.Fatal(err)
	}
	return ev59RuleFixture{ctx: ctx, resources: resources, store: store, canonical: canonical}
}

func (f ev59RuleFixture) publish(t *testing.T, pkg application.RulePackage, content []byte, expectedDraftRevision int) application.RuleVersion {
	t.Helper()
	draft, err := f.resources.SaveRuleDraft(f.ctx, pkg.ID, content, "json", "", expectedDraftRevision, "owner")
	if err != nil {
		t.Fatal(err)
	}
	version, err := f.resources.PublishRuleDraft(f.ctx, pkg.ID, draft.Revision, "owner", "EV-59 test publish", true)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func requireEV59Code(t *testing.T, err error, code fault.Code) {
	t.Helper()
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != code {
		t.Fatalf("错误码=%v want=%s", err, code)
	}
}

func requireEV59Audit(t *testing.T, audits []application.RuleAudit, action, subjectType, subjectID string) application.RuleAudit {
	t.Helper()
	for _, audit := range audits {
		if audit.Action == action && audit.SubjectType == subjectType && audit.SubjectID == subjectID {
			return audit
		}
	}
	t.Fatalf("未找到审计 action=%s subjectType=%s subjectId=%s: %+v", action, subjectType, subjectID, audits)
	return application.RuleAudit{}
}

func TestEV59RollbackRejectsInvalidTargetsAndDeprecatedPackage(t *testing.T) {
	f := newEV59RuleFixture(t)
	pkg, err := f.resources.CreateRulePackage(f.ctx, "rset_018f47d2-5c16-7a44-a8a0-000000000001", "rollback guards", "", "owner")
	if err != nil {
		t.Fatal(err)
	}
	first := f.publish(t, pkg, f.canonical, 0)
	secondContent := []byte(strings.Replace(string(f.canonical), `"version": "0.1.0"`, `"version": "0.2.0"`, 1))
	second := f.publish(t, pkg, secondContent, 1)

	current, _ := f.resources.GetRulePackage(f.ctx, pkg.ID)
	_, err = f.resources.RollbackRulePackage(f.ctx, pkg.ID, second.SemanticHash, current.Revision, "owner", "same target", true)
	requireEV59Code(t, err, fault.CodeRuleRollbackBlocked)

	legacyContent := []byte(strings.Replace(string(secondContent), `"scope": "work_directory", "glob": "*"`, `"scope": "work_directory", "glob": "*/*"`, 1))
	legacy, err := f.resources.CreateRuleVersion(f.ctx, legacyContent)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.PackageID != "" {
		t.Fatalf("测试前提：legacy version 意外属于 package: %+v", legacy)
	}
	_, err = f.resources.RollbackRulePackage(f.ctx, pkg.ID, legacy.SemanticHash, current.Revision, "owner", "legacy target", true)
	requireEV59Code(t, err, fault.CodeRuleRollbackBlocked)

	otherContent := []byte(strings.Replace(string(f.canonical),
		`"rule_set_id": "rset_018f47d2-5c16-7a44-a8a0-000000000001"`,
		`"rule_set_id": "rset_018f47d2-5c16-7a44-a8a0-000000000002"`, 1))
	otherPackage, err := f.resources.CreateRulePackage(f.ctx, "rset_018f47d2-5c16-7a44-a8a0-000000000002", "other", "", "owner")
	if err != nil {
		t.Fatal(err)
	}
	other := f.publish(t, otherPackage, otherContent, 0)
	_, err = f.resources.RollbackRulePackage(f.ctx, pkg.ID, other.SemanticHash, current.Revision, "owner", "cross package", true)
	requireEV59Code(t, err, fault.CodeRuleRollbackBlocked)

	if _, err := f.store.Control.SQL().ExecContext(f.ctx, `UPDATE rule_versions SET executable=0 WHERE semantic_hash=?`, first.SemanticHash); err != nil {
		t.Fatal(err)
	}
	_, err = f.resources.RollbackRulePackage(f.ctx, pkg.ID, first.SemanticHash, current.Revision, "owner", "non executable", true)
	requireEV59Code(t, err, fault.CodeRuleRollbackBlocked)
	if _, err := f.store.Control.SQL().ExecContext(f.ctx, `UPDATE rule_versions SET executable=1 WHERE semantic_hash=?`, first.SemanticHash); err != nil {
		t.Fatal(err)
	}
	parameter, err := f.resources.CreateRuleParameterSet(f.ctx, "preserved", second.SemanticHash, []byte(`{}`), "owner")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "deprecated-source")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	library, _ := f.resources.CreateLibrary(f.ctx, "deprecated library")
	source, _ := f.resources.CreateSource(f.ctx, library.ID, "deprecated source", root)
	binding, err := f.resources.CreateSourceRuleBindingFromParameterSet(f.ctx, source.ID, parameter.ID, 10, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.resources.SetSourceRuleBindingStatus(f.ctx, binding.ID, application.RuleBindingPaused); err != nil {
		t.Fatal(err)
	}

	deprecated, err := f.resources.SetRulePackageStatus(f.ctx, pkg.ID, application.RulePackageDeprecated, "owner", "retire package", current.Revision)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.resources.SetRulePackageStatus(f.ctx, pkg.ID, application.RulePackageActive, "owner", "reactivate package", deprecated.Revision)
	requireEV59Code(t, err, fault.CodeRulePackageConflict)
	_, err = f.resources.RollbackRulePackage(f.ctx, pkg.ID, first.SemanticHash, deprecated.Revision, "owner", "deprecated package", true)
	requireEV59Code(t, err, fault.CodeRuleRollbackBlocked)
	unchanged, _ := f.resources.GetRulePackage(f.ctx, pkg.ID)
	if unchanged.CurrentSemanticHash != second.SemanticHash {
		t.Fatalf("失败 rollback 改变 current: %+v", unchanged)
	}
	_, err = f.resources.ValidateRuleDraft(f.ctx, pkg.ID, 2, "owner")
	requireEV59Code(t, err, fault.CodeConflict)
	_, err = f.resources.SaveRuleDraft(f.ctx, pkg.ID, secondContent, "json", second.SemanticHash, 2, "owner")
	requireEV59Code(t, err, fault.CodeConflict)
	_, err = f.resources.PublishRuleDraft(f.ctx, pkg.ID, 2, "owner", "blocked publish", true)
	requireEV59Code(t, err, fault.CodeConflict)
	_, err = f.resources.CreateRuleParameterSet(f.ctx, "blocked", second.SemanticHash, []byte(`{}`), "owner")
	requireEV59Code(t, err, fault.CodeRuleParameterConflict)
	_, err = f.resources.CopyRuleParameterSet(f.ctx, parameter.ID, "blocked copy", "owner")
	requireEV59Code(t, err, fault.CodeRuleParameterConflict)
	_, err = f.resources.UpdateRuleParameterSet(f.ctx, parameter.ID, []byte(`{}`), 1, "owner", true)
	requireEV59Code(t, err, fault.CodeRuleParameterConflict)
	_, err = f.resources.SetSourceRuleBindingStatus(f.ctx, binding.ID, application.RuleBindingActive)
	requireEV59Code(t, err, fault.CodeRuleVersionInUse)
	_, err = f.resources.CreateSourceRuleBindingFromParameterSet(f.ctx, source.ID, parameter.ID, 5, nil, nil)
	requireEV59Code(t, err, fault.CodeRuleVersionInUse)
}

func TestEV59VersionDeprecateRequiresReasonAndIsAuditIdempotent(t *testing.T) {
	f := newEV59RuleFixture(t)
	pkg, _ := f.resources.CreateRulePackage(f.ctx, "rset_018f47d2-5c16-7a44-a8a0-000000000001", "version deprecate", "", "owner")
	first := f.publish(t, pkg, f.canonical, 0)
	secondContent := []byte(strings.Replace(string(f.canonical), `"version": "0.1.0"`, `"version": "0.2.0"`, 1))
	_ = f.publish(t, pkg, secondContent, 1)

	_, err := f.resources.DeprecateRuleVersion(f.ctx, first.SemanticHash, "owner", "  ")
	requireEV59Code(t, err, fault.CodeValidation)
	_, err = f.resources.DeprecateRuleVersion(f.ctx, first.SemanticHash, "owner", strings.Repeat("x", 4097))
	requireEV59Code(t, err, fault.CodeValidation)
	if _, err := f.resources.DeprecateRuleVersion(f.ctx, first.SemanticHash, "owner", "retire v1"); err != nil {
		t.Fatal(err)
	}
	audits, err := f.resources.ListRuleAudits(f.ctx, pkg.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := len(audits)
	requireEV59Audit(t, audits, "deprecate", application.RuleAuditSubjectVersion, first.SemanticHash)
	if _, err := f.resources.DeprecateRuleVersion(f.ctx, first.SemanticHash, "owner", "repeat"); err != nil {
		t.Fatal(err)
	}
	after, _ := f.resources.ListRuleAudits(f.ctx, pkg.ID)
	if len(after) != count {
		t.Fatalf("重复弃用产生重复审计: before=%d after=%d", count, len(after))
	}
}

func TestEV59ParameterUpdateDeprecateAndExactSnapshot(t *testing.T) {
	f := newEV59RuleFixture(t)
	withParameter := []byte(strings.Replace(string(f.canonical),
		`"parameter_schema": {"type": "object", "additionalProperties": false}`,
		`"parameter_schema": {"type": "object", "properties": {"minimumSize": {"type": "integer", "minimum": 0}}, "additionalProperties": false}`, 1))
	pkg, _ := f.resources.CreateRulePackage(f.ctx, "rset_018f47d2-5c16-7a44-a8a0-000000000001", "parameters", "", "owner")
	version := f.publish(t, pkg, withParameter, 0)
	parameter, err := f.resources.CreateRuleParameterSet(f.ctx, "exact", version.SemanticHash, []byte(`{"minimumSize":9007199254740993123}`), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(parameter.Parameters), `9007199254740993123`) {
		t.Fatalf("精确参数被改写: %s", parameter.Parameters)
	}

	root := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	library, _ := f.resources.CreateLibrary(f.ctx, "parameter library")
	source, _ := f.resources.CreateSource(f.ctx, library.ID, "parameter source", root)
	binding, err := f.resources.CreateSourceRuleBindingFromParameterSet(f.ctx, source.ID, parameter.ID, 10, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = f.resources.UpdateRuleParameterSet(f.ctx, parameter.ID, []byte(`{"minimumSize":9007199254740993124}`), 1, "owner", false)
	requireEV59Code(t, err, fault.CodeRuleParameterConflict)
	unchanged, _ := f.resources.GetRuleParameterSet(f.ctx, parameter.ID)
	if unchanged.CurrentRevision != 1 {
		t.Fatalf("未确认更新改变 revision: %+v", unchanged)
	}
	updated, err := f.resources.UpdateRuleParameterSet(f.ctx, parameter.ID, []byte(`{"minimumSize":9007199254740993124}`), 1, "owner", true)
	if err != nil || updated.CurrentRevision != 2 || !strings.Contains(string(updated.Parameters), `9007199254740993124`) {
		t.Fatalf("确认更新失败: %+v %v", updated, err)
	}
	refreshed, _ := f.resources.GetSourceRuleBinding(f.ctx, binding.ID)
	if refreshed.ParameterRevision != 2 || refreshed.ParameterHash != updated.CurrentHash {
		t.Fatalf("Binding 未原子刷新: %+v", refreshed)
	}

	deprecated, err := f.resources.DeprecateRuleParameterSet(f.ctx, parameter.ID, 2, "owner", "retire parameters")
	if err != nil || deprecated.Status != application.RuleParameterDeprecated {
		t.Fatalf("参数集弃用失败: %+v %v", deprecated, err)
	}
	existing, _ := f.resources.GetSourceRuleBinding(f.ctx, binding.ID)
	if existing.Status != application.RuleBindingActive || existing.ParameterRevision != 2 {
		t.Fatalf("弃用隐式改写既有 Binding: %+v", existing)
	}
	_, err = f.resources.CreateSourceRuleBindingFromParameterSet(f.ctx, source.ID, parameter.ID, 5, nil, nil)
	requireEV59Code(t, err, fault.CodeRuleParameterConflict)
	_, err = f.resources.UpdateRuleParameterSet(f.ctx, parameter.ID, []byte(`{"minimumSize":2}`), 2, "owner", true)
	requireEV59Code(t, err, fault.CodeRuleParameterConflict)
	audits, _ := f.resources.ListRuleAudits(f.ctx, pkg.ID)
	requireEV59Audit(t, audits, "deprecate_parameter", application.RuleAuditSubjectParameter, parameter.ID)
}

func TestEV59ParameterUpdateAndDeprecateAreLinearized(t *testing.T) {
	f := newEV59RuleFixture(t)
	withParameter := []byte(strings.Replace(string(f.canonical),
		`"parameter_schema": {"type": "object", "additionalProperties": false}`,
		`"parameter_schema": {"type": "object", "properties": {"minimumSize": {"type": "integer"}}, "additionalProperties": false}`, 1))
	pkg, _ := f.resources.CreateRulePackage(f.ctx, "rset_018f47d2-5c16-7a44-a8a0-000000000001", "parameter race", "", "owner")
	version := f.publish(t, pkg, withParameter, 0)
	parameter, err := f.resources.CreateRuleParameterSet(f.ctx, "race", version.SemanticHash, []byte(`{"minimumSize":1}`), "owner")
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := f.resources.UpdateRuleParameterSet(f.ctx, parameter.ID, []byte(`{"minimumSize":2}`), 1, "owner", true)
		results <- err
	}()
	go func() {
		<-start
		_, err := f.resources.DeprecateRuleParameterSet(f.ctx, parameter.ID, 1, "owner", "race deprecate")
		results <- err
	}()
	close(start)
	firstErr, secondErr := <-results, <-results
	if (firstErr == nil) == (secondErr == nil) {
		t.Fatalf("并发更新/弃用应恰有一个成功: first=%v second=%v", firstErr, secondErr)
	}
	for _, err := range []error{firstErr, secondErr} {
		if err != nil {
			requireEV59Code(t, err, fault.CodeRuleParameterConflict)
		}
	}
	final, err := f.resources.GetRuleParameterSet(f.ctx, parameter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !((final.Status == application.RuleParameterActive && final.CurrentRevision == 2) ||
		(final.Status == application.RuleParameterDeprecated && final.CurrentRevision == 1)) {
		t.Fatalf("并发终态不是任一合法线性化结果: %+v", final)
	}
}
