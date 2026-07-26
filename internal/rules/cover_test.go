package rules_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/RecRivenVI/gallery/internal/rules"
)

// coverRulePackage 构造一个只关注隐藏与封面语义的最小规则包。extraPrimitives 由各用例
// 追加 media_hidden / cover_candidate / cover_disable_marker。
func coverRulePackage(extraPrimitives string) string {
	return fmt.Sprintf(`{
  "rule_set_id":"rset_018f47d2-5c16-7a44-a8a0-0000000000c0","version":"1.0.0",
  "schema_version":1,"normalization_algorithm_version":"gallery-canonical-json-v1","compiler_requirement":"gallery-rule-compiler-v1","cel_profile_version":"gallery-cel-v1",
  "parameter_schema":{"type":"object","additionalProperties":false},"provider_namespaces":[],
  "primitives":[
    {"id":"work","kind":"path_match","config":{"scope":"work_directory","glob":"*","title":"directory_name","stable_key":"relative_path"}},
    {"id":"image","kind":"media_classify","config":{"glob":"*.jpg","kind":"image","mime":"image/jpeg"}}%s
  ],
  "cel_expressions":[],"tests":[{"id":"cover"}],"extensions":{}
}`, extraPrimitives)
}

func evaluateCover(t *testing.T, extraPrimitives string, filenames ...string) rules.DryRunResult {
	t.Helper()
	compiled, err := rules.CompilePackage([]byte(coverRulePackage(extraPrimitives)))
	if err != nil {
		t.Fatalf("编译规则包失败: %v", err)
	}
	ir, _, parameters, err := rules.CompileBinding(compiled, []byte(`{}`))
	if err != nil {
		t.Fatalf("绑定编译失败: %v", err)
	}
	lifecycle, err := rules.NewLifecycle()
	if err != nil {
		t.Fatal(err)
	}
	sample := rules.DryRunInput{Path: "work", Metadata: map[string]any{}}
	for _, name := range filenames {
		sample.Files = append(sample.Files, rules.DryRunFile{Path: name, Size: 1})
	}
	result, err := lifecycle.EvaluateIR(context.Background(), ir, parameters, sample)
	if err != nil {
		t.Fatalf("规则执行失败: %v", err)
	}
	return result
}

func mediaHiddenByPath(result rules.DryRunResult) map[string]bool {
	hidden := map[string]bool{}
	for _, item := range result.Work.Media {
		hidden[item.Path] = item.Hidden
	}
	return hidden
}

// TestMediaHiddenGlobsHideWithoutRemoving 覆盖真实规则的 hidden_name_globs：命中的媒体
// 仍然是媒体（保留身份与内容确认），只是默认不展示。
func TestMediaHiddenGlobsHideWithoutRemoving(t *testing.T) {
	const hiddenPrimitive = `,
    {"id":"hide","kind":"media_hidden","config":{"globs":[".*","cover.*",".cover.*"]}}`
	result := evaluateCover(t, hiddenPrimitive, "01.jpg", "cover.jpg", ".hidden.jpg", "02.jpg")
	if len(result.Work.Media) != 4 {
		t.Fatalf("隐藏不得从媒体列表中移除文件: %+v", result.Work.Media)
	}
	hidden := mediaHiddenByPath(result)
	for path, want := range map[string]bool{
		"01.jpg": false, "02.jpg": false, "cover.jpg": true, ".hidden.jpg": true,
	} {
		if hidden[path] != want {
			t.Fatalf("%s 隐藏状态 = %v want %v（全部：%+v）", path, hidden[path], want, hidden)
		}
	}
}

// TestExplicitCoverWinsOverNaturalFallback 覆盖真实规则「cover.* 既隐藏又是封面」这一
// 组合意图：cover.jpg 不出现在可见媒体中，但仍然是该作品的封面。
func TestExplicitCoverWinsOverNaturalFallback(t *testing.T) {
	const primitives = `,
    {"id":"hide","kind":"media_hidden","config":{"globs":["cover.*"]}},
    {"id":"explicit","kind":"cover_candidate","config":{"glob":"cover.*","priority":100}}`
	result := evaluateCover(t, primitives, "01.jpg", "cover.jpg", "02.jpg")
	if result.Work.CoverPath != "cover.jpg" {
		t.Fatalf("显式封面未生效: %q", result.Work.CoverPath)
	}
	if !mediaHiddenByPath(result)["cover.jpg"] {
		t.Fatal("显式封面文件应同时保持隐藏")
	}
}

// TestCoverFallsBackToFirstVisibleMediaInNaturalOrder 覆盖 leaf_fallback:
// first_natural_media：没有显式候选时取第一张可见媒体，且顺序是自然序而不是字典序。
func TestCoverFallsBackToFirstVisibleMediaInNaturalOrder(t *testing.T) {
	const primitives = `,
    {"id":"hide","kind":"media_hidden","config":{"globs":[".*"]}}`
	result := evaluateCover(t, primitives, "10.jpg", "2.jpg", ".thumb.jpg")
	if result.Work.CoverPath != "2.jpg" {
		t.Fatalf("自然序回退错误: %q（媒体顺序 %+v）", result.Work.CoverPath, result.Work.Media)
	}
	// 隐藏媒体不得成为回退封面。
	hiddenOnly := evaluateCover(t, primitives, ".thumb.jpg")
	if hiddenOnly.Work.CoverPath != "" {
		t.Fatalf("只有隐藏媒体时不应回退出封面: %q", hiddenOnly.Work.CoverPath)
	}
}

// TestCoverPriorityRanksCandidates 覆盖 Gank 那类「多条候选按优先级择一」的规则。
func TestCoverPriorityRanksCandidates(t *testing.T) {
	const primitives = `,
    {"id":"low","kind":"cover_candidate","config":{"glob":"01.jpg","priority":10}},
    {"id":"high","kind":"cover_candidate","config":{"glob":"1.jpg","priority":200}}`
	result := evaluateCover(t, primitives, "01.jpg", "1.jpg", "02.jpg")
	if result.Work.CoverPath != "1.jpg" {
		t.Fatalf("高优先级候选未胜出: %q", result.Work.CoverPath)
	}
}

// TestCoverCandidateMediaTypeExcludesNonStaticImages 证明 media_type 约束真正参与判定：
// 需要静态图片的候选不会选中动图。
func TestCoverCandidateMediaTypeExcludesNonStaticImages(t *testing.T) {
	packageJSON := `{
  "rule_set_id":"rset_018f47d2-5c16-7a44-a8a0-0000000000c1","version":"1.0.0",
  "schema_version":1,"normalization_algorithm_version":"gallery-canonical-json-v1","compiler_requirement":"gallery-rule-compiler-v1","cel_profile_version":"gallery-cel-v1",
  "parameter_schema":{"type":"object","additionalProperties":false},"provider_namespaces":[],
  "primitives":[
    {"id":"work","kind":"path_match","config":{"scope":"work_directory","glob":"*","title":"directory_name","stable_key":"relative_path"}},
    {"id":"gif","kind":"media_classify","config":{"glob":"*.gif","kind":"image","mime":"image/gif"}},
    {"id":"jpg","kind":"media_classify","config":{"glob":"*.jpg","kind":"image","mime":"image/jpeg"}},
    {"id":"static","kind":"cover_candidate","config":{"glob":"1.*","media_type":"static_image","priority":200}}
  ],
  "cel_expressions":[],"tests":[{"id":"cover"}],"extensions":{}
}`
	compiled, err := rules.CompilePackage([]byte(packageJSON))
	if err != nil {
		t.Fatal(err)
	}
	ir, _, parameters, err := rules.CompileBinding(compiled, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := rules.NewLifecycle()
	if err != nil {
		t.Fatal(err)
	}
	// 1.gif 命中 glob 但不是静态图片；2.jpg 不命中 glob。因此没有候选胜出，回退第一张可见媒体。
	result, err := lifecycle.EvaluateIR(context.Background(), ir, parameters, rules.DryRunInput{
		Path: "work", Metadata: map[string]any{},
		Files: []rules.DryRunFile{{Path: "1.gif", Size: 1}, {Path: "2.jpg", Size: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Work.CoverPath != "1.gif" {
		t.Fatalf("动图不应被 static_image 候选选中，应回退第一张可见媒体: %q", result.Work.CoverPath)
	}
}

// TestCoverDisableMarkerSuppressesCoverEntirely 覆盖 `.nocover`：标记存在时既不选显式
// 候选也不回退，封面为空。
func TestCoverDisableMarkerSuppressesCoverEntirely(t *testing.T) {
	const primitives = `,
    {"id":"explicit","kind":"cover_candidate","config":{"glob":"cover.*","priority":100}},
    {"id":"nocover","kind":"cover_disable_marker","config":{"filename":".nocover"}}`
	withMarker := evaluateCover(t, primitives, "01.jpg", "cover.jpg", ".nocover")
	if withMarker.Work.CoverPath != "" {
		t.Fatalf("`.nocover` 未禁用封面: %q", withMarker.Work.CoverPath)
	}
	withoutMarker := evaluateCover(t, primitives, "01.jpg", "cover.jpg")
	if withoutMarker.Work.CoverPath != "cover.jpg" {
		t.Fatalf("无标记时显式封面应生效: %q", withoutMarker.Work.CoverPath)
	}
}

// TestNewPrimitivesRejectInvalidConfiguration 锁定编译期校验：空 glob、非法 glob 与
// 带路径分隔符的标记文件名都必须在编译时被拒绝，而不是在扫描时静默失效。
func TestNewPrimitivesRejectInvalidConfiguration(t *testing.T) {
	for _, item := range []struct{ name, primitives string }{
		{"空 globs", `,{"id":"h","kind":"media_hidden","config":{"globs":[]}}`},
		{"空 glob 项", `,{"id":"h","kind":"media_hidden","config":{"globs":[""]}}`},
		{"非法 glob", `,{"id":"h","kind":"media_hidden","config":{"globs":["[a-"]}}`},
		{"标记名含分隔符", `,{"id":"n","kind":"cover_disable_marker","config":{"filename":"a/b"}}`},
		{"标记名为空", `,{"id":"n","kind":"cover_disable_marker","config":{"filename":""}}`},
		{"负优先级", `,{"id":"c","kind":"cover_candidate","config":{"glob":"c.*","priority":-1}}`},
	} {
		t.Run(item.name, func(t *testing.T) {
			if _, err := rules.CompilePackage([]byte(coverRulePackage(item.primitives))); err == nil {
				t.Fatal("非法配置未在编译期被拒绝")
			}
		})
	}
}
