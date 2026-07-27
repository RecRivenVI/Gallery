package rules_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/rules"
)

const encodingJSONMaxNestingDepth = 10000

func nestedArrayText(depth int) []byte {
	return []byte(strings.Repeat("[", depth) + strings.Repeat("]", depth))
}

func nestedObjectText(depth int) []byte {
	return []byte(strings.Repeat(`{"a":`, depth) + "1" + strings.Repeat("}", depth))
}

func TestEncodingJSONEnforcesMaxNestingDepth(t *testing.T) {
	var value any
	if err := json.Unmarshal(nestedArrayText(encodingJSONMaxNestingDepth), &value); err != nil {
		t.Fatalf("encoding/json 应当接受 %d 层嵌套: %v", encodingJSONMaxNestingDepth, err)
	}
	err := json.Unmarshal(nestedArrayText(encodingJSONMaxNestingDepth+1), &value)
	if err == nil || !strings.Contains(err.Error(), "exceeded max depth") {
		t.Fatalf("encoding/json 未按预期拒绝超深输入: %v", err)
	}
}

// Token API 自身仍不限制深度；Gallery 的显式规则深度门禁不能依赖标准库实现细节。
func TestDecoderTokenAPIHasNoDepthGuard(t *testing.T) {
	const probeDepth = encodingJSONMaxNestingDepth * 2
	decoder := json.NewDecoder(bytes.NewReader(nestedArrayText(probeDepth)))
	maximum, depth := 0, 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Token() 在深度 %d 失败: %v", depth, err)
		}
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '[', '{':
				depth++
				if depth > maximum {
					maximum = depth
				}
			case ']', '}':
				depth--
			}
		}
	}
	if maximum != probeDepth {
		t.Fatalf("Token() 最大深度 %d，期望 %d", maximum, probeDepth)
	}
}

func TestRulesPipelineEnforcesMaxNestingDepth(t *testing.T) {
	for _, probe := range [][]byte{
		nestedArrayText(rules.MaxRuleNestingDepth),
		nestedObjectText(rules.MaxRuleNestingDepth),
	} {
		if _, err := rules.CanonicalJSON(probe); err != nil {
			t.Fatalf("规则管线应接受 %d 层嵌套: %v", rules.MaxRuleNestingDepth, err)
		}
		if _, err := rules.NormalizeWithSchema(probe, []byte(`{}`)); err != nil {
			t.Fatalf("Schema 归一化应接受 %d 层嵌套: %v", rules.MaxRuleNestingDepth, err)
		}
	}

	cases := []struct {
		name string
		run  func() error
	}{
		{"CanonicalJSON/数组", func() error {
			_, err := rules.CanonicalJSON(nestedArrayText(rules.MaxRuleNestingDepth + 1))
			return err
		}},
		{"CanonicalJSON/对象", func() error {
			_, err := rules.CanonicalJSON(nestedObjectText(rules.MaxRuleNestingDepth + 1))
			return err
		}},
		{"NormalizeWithSchema/输入", func() error {
			_, err := rules.NormalizeWithSchema(nestedArrayText(rules.MaxRuleNestingDepth+1), []byte(`{}`))
			return err
		}},
		{"NormalizeWithSchema/Schema", func() error {
			_, err := rules.NormalizeWithSchema([]byte(`{}`), nestedObjectText(rules.MaxRuleNestingDepth+1))
			return err
		}},
		{"ImportRulePackage/JSON", func() error {
			_, err := rules.ImportRulePackage("json", nestedArrayText(rules.MaxRuleNestingDepth+1))
			return err
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assertRuleDepthRejected(t, testCase.run())
		})
	}
}

func TestRuleImportFormatsShareNestingDepthGuard(t *testing.T) {
	depth := rules.MaxRuleNestingDepth + 1
	cases := map[string][]byte{
		"json": nestedArrayText(depth),
		"yaml": []byte("a: " + strings.Repeat("[", depth) + strings.Repeat("]", depth) + "\n"),
		"toml": []byte("a = " + strings.Repeat("[", depth) + strings.Repeat("]", depth) + "\n"),
	}
	for format, input := range cases {
		t.Run(format, func(t *testing.T) {
			_, err := rules.ImportRulePackage(format, input)
			assertRuleDepthRejected(t, err)
		})
	}
}

func assertRuleDepthRejected(t *testing.T, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "规则结构嵌套超过") {
		t.Fatalf("超深规则必须由统一深度门禁拒绝，实际 %v", err)
	}
}
