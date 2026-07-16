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
