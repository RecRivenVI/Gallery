package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/rules"
)

func TestFantiaCreatorStableIdentityIsSharedWithoutDisplayNameMerging(t *testing.T) {
	fixtureRoot := filepath.Join("..", "..", "tests", "fixtures", "creator-aggregation", "fantia")
	packageJSON, err := os.ReadFile(filepath.Join(fixtureRoot, "rule-package.json"))
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := rules.CompilePackage(packageJSON)
	if err != nil {
		t.Fatal(err)
	}
	ir, _, parameters, err := rules.CompileBinding(compiled, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := discover(context.Background(), filepath.Join(fixtureRoot, "source"), ir, parameters)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Works) != 3 {
		t.Fatalf("Fantia 合成夹具作品数=%d，期望 3: %+v", len(result.Works), result.Works)
	}

	identityOccurrences := map[string]int{}
	sourceCreatorKeys := map[string]struct{}{}
	for _, work := range result.Works {
		creator := creatorReference(work)
		identityOccurrences[creator.ExternalID]++
		sourceCreatorKeys[creator.SourceKey] = struct{}{}
		if creator.Name != "同名创作者" || creator.ProviderID != "" {
			t.Fatalf("Creator 来源事实错误: %+v", creator)
		}
	}
	if len(sourceCreatorKeys) != 3 {
		t.Fatalf("逐作品 SourceCreator occurrence 未保持独立: %+v", sourceCreatorKeys)
	}
	if identityOccurrences["fantia:fanclub:900004"] != 2 || identityOccurrences["fantia:fanclub:900005"] != 1 || len(identityOccurrences) != 2 {
		t.Fatalf("规则驱动 Creator 稳定身份错误: %+v", identityOccurrences)
	}
}

func TestRuleIRDiscoversDifferentDirectoryAndMetadataShapes(t *testing.T) {
	root := filepath.Join("..", "..", "tests", "fixtures", "architecture-proof")
	tests := []struct {
		name, relativeRoot, workGlob, metadataFile, mediaGlob string
		selectors, expressions, condition                     string
		wantWorkKey, wantTitle, wantMedia                     string
	}{
		{
			name: "nested-post", relativeRoot: "layout-a", workGlob: "*", metadataFile: "metadata.json", mediaGlob: "*.jpg",
			selectors: `
    {"id":"title","kind":"selector","config":{"target":"title","pointers":["/post/title"],"required":true}},
    {"id":"identity","kind":"stable_key","config":{"target":"work","pointer":"/post/id","prefix":"origin:"}},`,
			wantWorkKey: "origin:alpha-1", wantTitle: "Alpha 标题", wantMedia: "origin:alpha-1/cover.jpg",
		},
		{
			name: "two-level-array-condition", relativeRoot: "layout-b", workGlob: "*/*", metadataFile: "post.json", mediaGlob: "*.png",
			selectors: `
    {"id":"title","kind":"selector","config":{"target":"title","pointers":["/title"],"required":true}},
    {"id":"identity","kind":"stable_key","config":{"target":"work","pointer":"/id","prefix":"origin:"}},`,
			expressions: `{"id":"allowed","purpose":"predicate","expression":"metadata.labels.exists(x, x == 'allow')"}`,
			condition:   `,"condition":"allowed"`, wantWorkKey: "origin:beta-9", wantTitle: "Beta 作品", wantMedia: "origin:beta-9/01.png",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			compiled, err := rules.CompilePackage([]byte(ruleForDiscovery(test.workGlob, test.metadataFile, test.mediaGlob, test.selectors, test.expressions, test.condition)))
			if err != nil {
				t.Fatal(err)
			}
			ir, _, parameters, err := rules.CompileBinding(compiled, []byte(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			result, err := discover(context.Background(), filepath.Join(root, test.relativeRoot), ir, parameters)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Works) != 1 || result.Works[0].SourceKey != test.wantWorkKey || result.Works[0].Title != test.wantTitle || len(result.Works[0].Media) == 0 || result.Works[0].Media[0].SourceKey != test.wantMedia || result.Works[0].RuleCoverMediaSourceKey != test.wantMedia {
				t.Fatalf("规则驱动发现错误: %+v", result.Works)
			}
		})
	}
}

func TestDiscoveryMapsRuleCoverPathToDiscoveredStableSourceKey(t *testing.T) {
	root := t.TempDir()
	workRoot := filepath.Join(root, "work")
	if err := os.MkdirAll(workRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"aaa.bin", "cover.bin"} {
		if err := os.WriteFile(filepath.Join(workRoot, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	packageJSON := ruleForDiscovery("*", "", "*.bin", "", "", "")
	packageJSON = strings.Replace(packageJSON,
		`{"id":"media","kind":"media_classify","config":{"glob":"*.bin","kind":"image","mime":"application/octet-stream"}}`,
		`{"id":"media","kind":"media_classify","config":{"glob":"*.bin","kind":"image","mime":"application/octet-stream"}},
    {"id":"cover","kind":"cover_candidate","config":{"glob":"cover.*","score":100}}`, 1)
	compiled, err := rules.CompilePackage([]byte(packageJSON))
	if err != nil {
		t.Fatal(err)
	}
	ir, _, parameters, err := rules.CompileBinding(compiled, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := discover(context.Background(), root, ir, parameters)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Works) != 1 || len(result.Works[0].Media) != 2 {
		t.Fatalf("发现结果不完整: %+v", result.Works)
	}
	if result.Works[0].Media[0].SourceKey != "work/aaa.bin" || result.Works[0].RuleCoverMediaSourceKey != "work/cover.bin" {
		t.Fatalf("封面没有映射到实际发现媒体的稳定 SourceKey: %+v", result.Works[0])
	}
}

func TestRuleIRReportsMissingRequiredMetadata(t *testing.T) {
	packageJSON := ruleForDiscovery("*", "metadata.json", "*.bin", `
    {"id":"title","kind":"selector","config":{"target":"title","pointers":["/title"],"required":true}},`, "", "")
	compiled, err := rules.CompilePackage([]byte(packageJSON))
	if err != nil {
		t.Fatal(err)
	}
	ir, _, parameters, err := rules.CompileBinding(compiled, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = discover(context.Background(), filepath.Join("..", "..", "tests", "fixtures", "architecture-proof", "layout-c"), ir, parameters)
	if err == nil {
		t.Fatal("缺失必需 metadata 未阻塞规则执行")
	}
}

func ruleForDiscovery(workGlob, metadataFile, mediaGlob, selectors, expressions, condition string) string {
	return fmt.Sprintf(`{
  "rule_set_id":"rset_018f47d2-5c16-7a44-a8a0-000000000088","version":"1.0.0",
  "schema_version":1,"normalization_algorithm_version":"gallery-canonical-json-v1","compiler_requirement":"gallery-rule-compiler-v1","cel_profile_version":"gallery-cel-v1",
  "parameter_schema":{"type":"object","additionalProperties":false},"provider_namespaces":[],
  "primitives":[
    {"id":"work","kind":"path_match","config":{"scope":"work_directory","glob":%q,"title":"directory_name","stable_key":"relative_path","metadata_file":%q}},%s
    {"id":"media","kind":"media_classify","config":{"glob":%q,"kind":"image","mime":"application/octet-stream"%s}}
  ],
  "cel_expressions":[%s],"tests":[{"id":"discovery"}],"extensions":{}
}`, workGlob, metadataFile, selectors, mediaGlob, condition, expressions)
}

// TestDiscoverSkipsUnreadableWorkWithoutAbortingScan 锁定「单个作品的 metadata 缺陷只跳过该作品」。
//
// 旧实现在这里直接返回错误，filepath.WalkDir 因此中止，整个 Source 的扫描以 RULE_EVAL_ERROR 失败。
// 真实来源上实测到该后果：某平台 11,686 个作品目录中有 19 个缺少 metadata 文件，剩下 11,667 个
// 完全正常的作品一个都索引不出来，而失败信息里没有任何线索指向是哪一类问题。
func TestDiscoverSkipsUnreadableWorkWithoutAbortingScan(t *testing.T) {
	root := t.TempDir()
	ir, parameters := metadataRequiringIR(t)

	// 三个作品：正常、缺 metadata、metadata 不是合法 JSON。
	for _, item := range []struct{ dir, metadata string }{
		{"作者/正常作品", `{"title":"正常"}`},
		{"作者/缺失作品", ""},
		{"作者/损坏作品", "{ 这不是 JSON"},
	} {
		dir := filepath.Join(root, filepath.FromSlash(item.dir))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "01.jpg"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if item.metadata != "" {
			if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(item.metadata), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	result, err := discover(context.Background(), root, ir, parameters)
	if err != nil {
		t.Fatalf("单个作品的 metadata 缺陷中断了整次扫描: %v", err)
	}
	if len(result.Works) != 1 {
		t.Fatalf("正常作品未被索引: 作品数=%d", len(result.Works))
	}
	if result.SkippedWorks != 2 {
		t.Fatalf("跳过计数=%d，期望 2（缺失 + 损坏）", result.SkippedWorks)
	}
	if result.SkippedReason == "" {
		t.Fatal("跳过原因为空：跳过必须可解释，不能静默丢失")
	}
}

// metadataRequiringIR 构造一个声明了 metadata 文件的最小规则 IR，使 discover 走必须读取
// metadata 的分支。
func metadataRequiringIR(t *testing.T) (rules.RuleIR, []byte) {
	t.Helper()
	const packageJSON = `{
  "rule_set_id":"rset_018f47d2-5c16-7a44-a8a0-0000000000f1","version":"1.0.0",
  "schema_version":1,"normalization_algorithm_version":"gallery-canonical-json-v1",
  "compiler_requirement":"gallery-rule-compiler-v1","cel_profile_version":"gallery-cel-v1",
  "parameter_schema":{"type":"object","additionalProperties":false},"provider_namespaces":[],
  "primitives":[
    {"id":"work","kind":"path_match","config":{"scope":"work_directory","glob":"*/*",
      "title":"directory_name","stable_key":"relative_path","metadata_file":"metadata.json"}},
    {"id":"media","kind":"media_classify","config":{"glob":"*.jpg","kind":"image","mime":"image/jpeg"}}
  ],
  "cel_expressions":[],"tests":[{"id":"t"}],"extensions":{}
}`
	compiled, err := rules.CompilePackage([]byte(packageJSON))
	if err != nil {
		t.Fatal(err)
	}
	ir, _, parameters, err := rules.CompileBinding(compiled, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	return ir, parameters
}
