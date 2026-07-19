package scanner

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/RecRivenVI/gallery/internal/rules"
)

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
			if len(works) != 1 || works[0].SourceKey != test.wantWorkKey || works[0].Title != test.wantTitle || len(works[0].Media) == 0 || works[0].Media[0].SourceKey != test.wantMedia {
				t.Fatalf("规则驱动发现错误: %+v", works)
			}
		})
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
