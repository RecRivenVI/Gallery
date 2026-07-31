package rules_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/rules"
)

func TestEveryPrimitiveKindHasAuthoritativeEditorSchema(t *testing.T) {
	var document struct {
		Defs       map[string]json.RawMessage `json:"$defs"`
		Properties struct {
			Primitives struct {
				Items struct {
					Properties struct {
						Kind struct {
							Enum []string `json:"enum"`
						} `json:"kind"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"primitives"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(rules.RulePackageSchema(), &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Properties.Primitives.Items.Properties.Kind.Enum) == 0 {
		t.Fatal("primitive kind 词表为空")
	}
	for _, kind := range document.Properties.Primitives.Items.Properties.Kind.Enum {
		name := "primitive_config_" + kind
		raw, ok := document.Defs[name]
		if !ok {
			t.Errorf("primitive %q 缺少 %s 编辑 Schema", kind, name)
			continue
		}
		var definition struct {
			Type       string                     `json:"type"`
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(raw, &definition); err != nil {
			t.Errorf("primitive %q 编辑 Schema 无法解析: %v", kind, err)
		} else if definition.Type != "object" || len(definition.Properties) == 0 {
			t.Errorf("primitive %q 编辑 Schema 不是含字段的 object: %+v", kind, definition)
		}
	}
}

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

func TestCompilePackageFinalCanonicalHonorsSizeLimit(t *testing.T) {
	withoutPadding := rulePackageWithPadding(t, 0)
	base, err := rules.CompilePackage(withoutPadding)
	if err != nil {
		t.Fatal(err)
	}
	paddingAtLimit := rules.MaxRulePackageBytes - len(base.Canonical)
	if paddingAtLimit <= 0 {
		t.Fatalf("最小规则包已超过大小上限: %d", len(base.Canonical))
	}

	atLimitInput := rulePackageWithPadding(t, paddingAtLimit)
	atLimit, err := rules.CompilePackage(atLimitInput)
	if err != nil {
		t.Fatalf("最终 canonical 恰好位于上限时被拒绝: %v", err)
	}
	if len(atLimit.Canonical) != rules.MaxRulePackageBytes {
		t.Fatalf("最终 canonical 大小 = %d, want %d", len(atLimit.Canonical), rules.MaxRulePackageBytes)
	}

	overLimitInput := rulePackageWithPadding(t, paddingAtLimit+1)
	if len(overLimitInput) >= rules.MaxRulePackageBytes {
		t.Fatalf("测试输入应仍低于入口上限: %d", len(overLimitInput))
	}
	if _, err := rules.ImportRulePackage("json", overLimitInput); err != nil {
		t.Fatalf("追加 hash 前的规范输入应仍可导入: %v", err)
	}
	if _, err := rules.CompilePackage(overLimitInput); err == nil || !strings.Contains(err.Error(), "大小超限") {
		t.Fatalf("最终 canonical 超限未拒绝: %v", err)
	}
}

func rulePackageWithPadding(t *testing.T, padding int) []byte {
	t.Helper()
	base, err := os.ReadFile(filepath.Join("testdata", "minimal-rule-package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(base, &root); err != nil {
		t.Fatal(err)
	}
	root["extensions"] = map[string]any{
		"example.padding": map[string]any{"padding": strings.Repeat("x", padding)},
	}
	result, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCompilePackageRejectsInvalidParameterSchemaBeforeBinding(t *testing.T) {
	base, err := os.ReadFile(filepath.Join("testdata", "minimal-rule-package.json"))
	if err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"type": "object", "additionalProperties": false}`)
	for _, test := range []struct {
		name   string
		schema string
	}{
		{name: "external ref", schema: `{"$ref":"file:///C:/forbidden/schema.json"}`},
		{name: "invalid type", schema: `{"type":"not-a-json-schema-type"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := bytes.Replace(base, original, []byte(test.schema), 1)
			if bytes.Equal(input, base) {
				t.Fatal("测试未替换 parameter_schema")
			}
			_, err := rules.CompilePackage(input)
			if err == nil {
				t.Fatal("非法 parameter_schema 未在 RulePackage 编译阶段拒绝")
			}
			if field := rules.ErrorField(err); field != "/parameter_schema" {
				t.Fatalf("错误字段 = %q, want /parameter_schema; err=%v", field, err)
			}
		})
	}

	selfContained := bytes.Replace(base, original, []byte(`{
  "$defs":{"name":{"type":"string","default":"匿名"}},
  "type":"object","additionalProperties":false,
  "properties":{"name":{"$ref":"#/$defs/name"}}
}`), 1)
	if _, err := rules.CompilePackage(selfContained); err != nil {
		t.Fatalf("自包含 parameter_schema 被拒绝: %v", err)
	}
}
