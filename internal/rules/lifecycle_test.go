package rules_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/rules"
)

func TestSchemaDefaultsAndParameterNormalizationAreMaterialized(t *testing.T) {
	explicit := readRulePackage(t)
	omitted := explicit
	for _, fragment := range []string{
		`  "schema_version": 1,` + "\n", `  "normalization_algorithm_version": "gallery-canonical-json-v1",` + "\n",
		`  "compiler_requirement": "gallery-rule-compiler-v1",` + "\n", `  "cel_profile_version": "gallery-cel-v1",` + "\n",
		`  "parameter_schema": {"type": "object", "additionalProperties": false},` + "\n", `  "provider_namespaces": [],` + "\n",
		`  "cel_expressions": [],` + "\n",
	} {
		omitted = bytes.Replace(omitted, []byte(fragment), nil, 1)
	}
	left, err := rules.CompilePackage(explicit)
	if err != nil {
		t.Fatal(err)
	}
	right, err := rules.CompilePackage(omitted)
	if err != nil {
		t.Fatal(err)
	}
	if left.PackageHash != right.PackageHash || left.SemanticHash != right.SemanticHash {
		t.Fatal("缺省字段与显式默认值没有收敛到同一身份")
	}

	withParameters := bytes.Replace(explicit,
		[]byte(`{"type": "object", "additionalProperties": false}`),
		[]byte(`{"type":"object","additionalProperties":false,"properties":{"name":{"type":"string","default":"é","x-gallery-normalization":"nfc"},"pointer":{"type":"string","default":"/é"}}}`), 1)
	compiled, err := rules.CompilePackage(withParameters)
	if err != nil {
		t.Fatal(err)
	}
	_, _, parameters, err := rules.CompileBinding(compiled, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(parameters, []byte(`"name":"é"`)) || !bytes.Contains(parameters, []byte(`"pointer":"/é"`)) {
		t.Fatalf("Schema 字符串策略未按字段执行: %s", parameters)
	}
}

func TestLifecycleDryRunTraceCELAndImpact(t *testing.T) {
	lifecycle, err := rules.NewLifecycle()
	if err != nil {
		t.Fatal(err)
	}
	packageJSON := complexRulePackage()
	first, err := lifecycle.Compile(packageJSON, []byte(`{"minimumSize":1}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := lifecycle.Compile(packageJSON, []byte(`{"minimumSize":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if first.CacheHit || !second.CacheHit || first.RuleIRHash != second.RuleIRHash {
		t.Fatalf("编译缓存语义错误: first=%v second=%v", first.CacheHit, second.CacheHit)
	}
	result, err := lifecycle.DryRun(context.Background(), packageJSON, []byte(`{"minimumSize":1}`), rules.DryRunInput{
		Path: "layout-b/work-7", Metadata: map[string]any{
			"post": map[string]any{"id": "7", "title": "标题七"}, "creator": map[string]any{"name": "作者"},
			"tags": []any{"alpha", "beta"}, "flags": []any{"allow"},
		},
		Files: []rules.DryRunFile{{Path: "cover.jpg", Size: 20}, {Path: "02.hidden.jpg", Size: 10}, {Path: "skip.bin", Size: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Work.StableKey != "provider:7" || result.Work.Title != "标题七" || result.Work.Creator != "作者" || len(result.Work.Tags) != 2 {
		t.Fatalf("selector/fallback/stable key 未生效: %+v", result.Work)
	}
	if len(result.Work.Media) != 2 || result.Work.Media[0].Path != "cover.jpg" && result.Work.Media[1].Path != "cover.jpg" || result.Work.CoverPath != "cover.jpg" {
		t.Fatalf("媒体分类/排序/封面错误: %+v", result.Work.Media)
	}
	if len(result.Trace) == 0 {
		t.Fatal("Dry Run 未生成 Trace")
	}
	traceJSON, _ := json.Marshal(result.Trace)
	if bytes.Contains(traceJSON, []byte("标题七")) || bytes.Contains(traceJSON, []byte("作者")) {
		t.Fatalf("Trace 泄露 metadata 值: %s", traceJSON)
	}

	changed := bytes.Replace(packageJSON, []byte(`"glob":"*.jpg"`), []byte(`"glob":"*.png"`), 1)
	impact, err := lifecycle.Impact(packageJSON, changed)
	if err != nil {
		t.Fatal(err)
	}
	if !impact.FullRescan || !impact.Reproject || len(impact.Actions) == 0 {
		t.Fatalf("RuleImpact 未识别媒体语义变化: %+v", impact)
	}
}

func TestCELProfileRejectsUnknownHostFunction(t *testing.T) {
	invalid := bytes.Replace(complexRulePackage(), []byte(`file.size >= params.minimumSize`), []byte(`read_file(file.path)`), 1)
	_, err := rules.CompilePackage(invalid)
	if err == nil || !strings.Contains(err.Error(), "undeclared reference") {
		t.Fatalf("未知 host function 未被 CEL 编译拒绝: %v", err)
	}
}

func TestUIMetadataDoesNotChangeRuntimeIdentity(t *testing.T) {
	base := readRulePackage(t)
	withUI := bytes.Replace(base, []byte(`"extensions": {}`), []byte(`"extensions": {}, "ui_metadata": {"group":"basic","help":"synthetic"}`), 1)
	left, err := rules.CompilePackage(base)
	if err != nil {
		t.Fatal(err)
	}
	right, err := rules.CompilePackage(withUI)
	if err != nil {
		t.Fatal(err)
	}
	if left.SemanticHash != right.SemanticHash || left.PackageHash == right.PackageHash {
		t.Fatalf("UI 元数据身份分类错误: left=%+v right=%+v", left, right)
	}
}

func readRulePackage(t *testing.T) []byte {
	t.Helper()
	value, err := os.ReadFile(filepath.Join("testdata", "minimal-rule-package.json"))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func complexRulePackage() []byte {
	return []byte(`{
  "rule_set_id":"rset_018f47d2-5c16-7a44-a8a0-000000000099","version":"1.0.0",
  "schema_version":1,"normalization_algorithm_version":"gallery-canonical-json-v1","compiler_requirement":"gallery-rule-compiler-v1","cel_profile_version":"gallery-cel-v1",
  "parameter_schema":{"type":"object","additionalProperties":false,"required":["minimumSize"],"properties":{"minimumSize":{"type":"integer","minimum":0}}},
  "provider_namespaces":["example"],
  "primitives":[
    {"id":"work","kind":"path_match","config":{"scope":"work_directory","glob":"*","title":"directory_name","stable_key":"relative_path"}},
    {"id":"title","kind":"selector","config":{"target":"title","pointers":["/post/title","/title"],"required":true}},
    {"id":"fields","kind":"metadata_map","config":{"fields":{"creator":["/creator/name"],"tags":["/tags"]}}},
    {"id":"identity","kind":"stable_key","config":{"target":"work","pointer":"/post/id","prefix":"provider:"}},
    {"id":"image","kind":"media_classify","config":{"glob":"*.jpg","kind":"image","mime":"image/jpeg","condition":"eligible"}},
    {"id":"order","kind":"media_order","config":{"by":"path","direction":"asc"}},
    {"id":"hidden","kind":"condition","config":{"scope":"media","expression":"hidden","effect":"hide"}},
    {"id":"cover","kind":"cover_candidate","config":{"glob":"cover.*","score":100}}
  ],
  "cel_expressions":[
    {"id":"eligible","purpose":"predicate","expression":"file.size >= params.minimumSize && metadata.flags.exists(x, x == 'allow')"},
    {"id":"hidden","purpose":"predicate","expression":"file.path.endsWith('.hidden.jpg')"}
  ],
  "tests":[{"id":"layout-b"}],"extensions":{"example.optional":{"preserved":true}}
}`)
}
