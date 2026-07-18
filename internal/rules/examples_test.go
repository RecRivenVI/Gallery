package rules_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/RecRivenVI/gallery/internal/rules"
)

func TestBuiltInRuleExamplesCompile(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "examples", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("内置示例数量 = %d", len(files))
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			content, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			compiled, err := rules.CompilePackage(content)
			if err != nil || compiled.SemanticHash == "" || compiled.RuleIRHash == "" {
				t.Fatalf("示例编译失败: %v", err)
			}
		})
	}
}
