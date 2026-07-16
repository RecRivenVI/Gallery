package rules_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/RecRivenVI/gallery/internal/rules"
)

func TestMinimalRulePackageAndForbiddenScriptField(t *testing.T) {
	validator, err := rules.NewRulePackageValidator()
	if err != nil {
		t.Fatal(err)
	}
	valid, err := os.ReadFile(filepath.Join("testdata", "minimal-rule-package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validator.ValidateJSON(valid); err != nil {
		t.Fatalf("最小规则包无效: %v", err)
	}
	invalid := bytes.Replace(valid, []byte(`"extensions": {}`), []byte(`"javascript": "run()", "extensions": {}`), 1)
	if err := validator.ValidateJSON(invalid); err == nil {
		t.Fatal("规则包接受了任意 JavaScript 字段")
	}
}

func TestCELProfileV1LimitsAreFrozen(t *testing.T) {
	profile := rules.CELProfileV1
	if profile.ExpressionBytes != 4096 || profile.ASTNodes != 256 || profile.Cost != 10000 || profile.ExecutionMillis != 10 {
		t.Fatalf("CEL Profile v1 限额漂移: %+v", profile)
	}
}

func TestPackageHashesSeparateDistributionFromRuntimeSemantics(t *testing.T) {
	valid, err := os.ReadFile(filepath.Join("testdata", "minimal-rule-package.json"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := rules.CompilePackage(valid)
	if err != nil {
		t.Fatal(err)
	}
	changedTests := bytes.Replace(valid, []byte(`"one-work-one-media"`), []byte(`"same-runtime-new-test"`), 1)
	second, err := rules.CompilePackage(changedTests)
	if err != nil {
		t.Fatal(err)
	}
	if first.PackageHash == second.PackageHash || first.SemanticHash != second.SemanticHash {
		t.Fatal("tests-only 修改未正确区分 package_hash 与 semantic_hash")
	}
	if first.IR.WorkDirectoryGlob != "*" || first.IR.MediaGlob != "*.bin" {
		t.Fatalf("最小规则未通过正式编译路径: %+v", first.IR)
	}
	for _, number := range []string{"1", "1.0", "1e0"} {
		canonical, err := rules.CanonicalJSON([]byte(number))
		if err != nil || string(canonical) != "1" {
			t.Fatalf("数字 %s 未精确规范化: %s %v", number, canonical, err)
		}
	}
}
