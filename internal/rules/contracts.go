package rules

import (
	_ "embed"
	"fmt"

	contractschema "github.com/RecRivenVI/gallery/internal/contract/schema"
)

const (
	RuleSchemaVersion             = 1
	NormalizationAlgorithmVersion = "gallery-canonical-json-v1"
	CompilerVersion               = "gallery-rule-compiler-v1"
	CELProfileVersion             = "gallery-cel-v1"
)

type CELProfile struct {
	ExpressionBytes int
	ASTNodes        int
	ArrayElements   int
	InputJSONBytes  int
	RegexCharacters int
	Cost            int
	ExecutionMillis int
}

var CELProfileV1 = CELProfile{
	ExpressionBytes: 4096, ASTNodes: 256, ArrayElements: 10000,
	InputJSONBytes: 4 * 1024 * 1024, RegexCharacters: 512, Cost: 10000, ExecutionMillis: 10,
}

//go:embed rule-package.schema.json
var rulePackageSchema []byte

func RulePackageSchema() []byte { return append([]byte(nil), rulePackageSchema...) }

func NewRulePackageValidator() (*contractschema.Validator, error) {
	validator, err := contractschema.Compile("rule-package.schema.json", rulePackageSchema)
	if err != nil {
		return nil, fmt.Errorf("初始化规则包 Schema: %w", err)
	}
	return validator, nil
}
