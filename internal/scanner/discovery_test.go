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
	works, err := discover(context.Background(), filepath.Join(fixtureRoot, "source"), ir, parameters)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 3 {
		t.Fatalf("Fantia 合成夹具作品数=%d，期望 3: %+v", len(works), works)
	}

	identityOccurrences := map[string]int{}
	sourceCreatorKeys := map[string]struct{}{}
	for _, work := range works {
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
			works, err := discover(context.Background(), filepath.Join(root, test.relativeRoot), ir, parameters)
			if err != nil {
				t.Fatal(err)
			}
			if len(works) != 1 || works[0].SourceKey != test.wantWorkKey || works[0].Title != test.wantTitle || len(works[0].Media) == 0 || works[0].Media[0].SourceKey != test.wantMedia || works[0].RuleCoverMediaSourceKey != test.wantMedia {
				t.Fatalf("规则驱动发现错误: %+v", works)
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
	works, err := discover(context.Background(), root, ir, parameters)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 1 || len(works[0].Media) != 2 {
		t.Fatalf("发现结果不完整: %+v", works)
	}
	if works[0].Media[0].SourceKey != "work/aaa.bin" || works[0].RuleCoverMediaSourceKey != "work/cover.bin" {
		t.Fatalf("封面没有映射到实际发现媒体的稳定 SourceKey: %+v", works[0])
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
