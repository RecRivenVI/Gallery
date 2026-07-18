package rules_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/rules"
)

func TestImportFormatsAndExactNumberIdentity(t *testing.T) {
	jsonInput := []byte(`{"value":9007199254740993123,"text":"画廊"}`)
	jsonResult, err := rules.ImportRulePackage("json", jsonInput)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(jsonResult.CanonicalJSON, []byte("9007199254740993123")) {
		t.Fatalf("大整数发生精度丢失: %s", jsonResult.CanonicalJSON)
	}
	yamlResult, err := rules.ImportRulePackage("yaml", []byte("value: 9007199254740993123\ntext: 画廊\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(jsonResult.CanonicalJSON, yamlResult.CanonicalJSON) {
		t.Fatalf("JSON/YAML 未收敛: json=%s yaml=%s", jsonResult.CanonicalJSON, yamlResult.CanonicalJSON)
	}
	tomlResult, err := rules.ImportRulePackage("toml", []byte("value = 9007199254740993123\ntext = \"画廊\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(jsonResult.CanonicalJSON, tomlResult.CanonicalJSON) {
		t.Fatalf("JSON/TOML 未收敛: json=%s toml=%s", jsonResult.CanonicalJSON, tomlResult.CanonicalJSON)
	}
	if _, err := rules.ImportRulePackage("json", []byte(`{"value":1,"value":2}`)); err == nil {
		t.Fatal("JSON 重复键未拒绝")
	}
	if _, err := rules.ImportRulePackage("yaml", []byte("value: 1\nvalue: 2\n")); err == nil {
		t.Fatal("YAML 重复键未拒绝")
	}
	if _, err := rules.ImportRulePackage("yaml", []byte("---\nvalue: 1\n---\nvalue: 2\n")); err == nil {
		t.Fatal("YAML 多文档未拒绝")
	}
	if _, err := rules.ImportRulePackage("yaml", []byte("base: &base\n  value: 1\ncopy: *base\n")); err == nil {
		t.Fatal("YAML alias 未拒绝")
	}
	if _, err := rules.ImportRulePackage("toml", []byte("value = 1.5\n")); err == nil {
		t.Fatal("TOML 浮点精度风险未拒绝")
	}
}

func TestRuleDiffExplainAndSemanticExtensionExecution(t *testing.T) {
	lifecycle, err := rules.NewLifecycle()
	if err != nil {
		t.Fatal(err)
	}
	base := complexRulePackage()
	withExtension := []byte(strings.Replace(string(base), `"extensions":{"example.optional":{"preserved":true}}`, `"extensions":{"gallery.identity":{"semantic":true,"version":"1","payload":{"stable_key_prefix":"ext:"}}}`, 1))
	result, err := lifecycle.DryRun(context.Background(), withExtension, []byte(`{"minimumSize":1}`), rules.DryRunInput{
		Path: "work", Metadata: map[string]any{"post": map[string]any{"id": "7", "title": "标题"}, "flags": []any{"allow"}},
		Files: []rules.DryRunFile{{Path: "cover.jpg", Size: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Work.StableKey != "ext:provider:7" {
		t.Fatalf("semantic extension 未执行: %+v", result.Work)
	}
	trace, err := lifecycle.Explain(context.Background(), withExtension, []byte(`{"minimumSize":1}`), rules.DryRunInput{Path: "work", Metadata: map[string]any{"post": map[string]any{"id": "7", "title": "标题"}, "flags": []any{"allow"}}, Files: []rules.DryRunFile{{Path: "cover.jpg", Size: 2}}})
	if err != nil || len(trace.Fields) == 0 || trace.RuleVersion == "" {
		t.Fatalf("Explain 结果错误: %+v %v", trace, err)
	}
	diff, err := lifecycle.DiffRulePackages(base, withExtension)
	if err != nil || len(diff.Entries) == 0 || diff.Category != "RESCAN_FULL" {
		t.Fatalf("diff 未分类 semantic extension: %+v %v", diff, err)
	}
	uiPackage := []byte(strings.Replace(string(base), `"tests":[`, `"ui_metadata":{"title":"表单标题","fields":{"/primitives/0":{"order":1}}},"tests":[`, 1))
	uiDiff, err := lifecycle.DiffRulePackages(base, uiPackage)
	if err != nil || uiDiff.Category != "NO_ACTION" || uiDiff.OldSemanticHash != uiDiff.NewSemanticHash {
		t.Fatalf("UI metadata 被错误分类: %+v %v", uiDiff, err)
	}
	legacyExtension := []byte(strings.Replace(string(base), `"preserved":true`, `"preserved":false`, 1))
	legacyDiff, err := lifecycle.DiffRulePackages(base, legacyExtension)
	if err != nil || legacyDiff.Category != "NO_ACTION" {
		t.Fatalf("未知 nonsemantic extension 被错误分类: %+v %v", legacyDiff, err)
	}
	coverChange := []byte(strings.Replace(string(base), `"score":100`, `"score":90`, 1))
	coverDiff, err := lifecycle.DiffRulePackages(base, coverChange)
	if err != nil || coverDiff.Category != "REPROJECT" {
		t.Fatalf("封面变化未分类为重投影: %+v %v", coverDiff, err)
	}
	parameterImpact, err := lifecycle.ImpactParameters(base, []byte(`{"minimumSize":1}`), []byte(`{"minimumSize":2}`))
	if err != nil || parameterImpact.Category != "BINDING_REVIEW" || !parameterImpact.FullRescan || !parameterImpact.ManualConfirm {
		t.Fatalf("参数影响分类错误: %+v %v", parameterImpact, err)
	}
}

func TestDryRunRejectsWindowsAndTraversalPaths(t *testing.T) {
	lifecycle, err := rules.NewLifecycle()
	if err != nil {
		t.Fatal(err)
	}
	for _, samplePath := range []string{"C:/secret", "C:secret", "../secret", "work\\file", "/absolute"} {
		_, err := lifecycle.DryRun(context.Background(), readRulePackage(t), []byte(`{}`), rules.DryRunInput{Path: samplePath, Metadata: map[string]any{}, Files: []rules.DryRunFile{}})
		if err == nil {
			t.Fatalf("危险样本路径未拒绝: %q", samplePath)
		}
	}
}

func TestImpactAndDiffClassifyPartialMediaRescan(t *testing.T) {
	lifecycle, err := rules.NewLifecycle()
	if err != nil {
		t.Fatal(err)
	}
	base := complexRulePackage()
	changed := []byte(strings.Replace(string(base), `"direction":"asc"`, `"direction":"desc"`, 1))
	impact, err := lifecycle.Impact(base, changed)
	if err != nil {
		t.Fatal(err)
	}
	if impact.Category != "RESCAN_PARTIAL" || !impact.PartialRescan || impact.FullRescan || !containsString(impact.Actions, "partial_rescan") {
		t.Fatalf("媒体顺序影响分类错误: %+v", impact)
	}
	diff, err := lifecycle.DiffRulePackages(base, changed)
	if err != nil {
		t.Fatal(err)
	}
	if diff.Category != "RESCAN_PARTIAL" || len(diff.Entries) == 0 || !diff.Entries[0].RequiresRescan {
		t.Fatalf("媒体顺序 diff 分类错误: %+v", diff)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
