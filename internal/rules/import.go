package rules

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"
	"go.yaml.in/yaml/v3"
)

const MaxRulePackageBytes = 8 * 1024 * 1024

type ImportDiagnostic struct {
	Path    string `json:"path"`
	Message string `json:"message"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
}

type ImportResult struct {
	Format        string             `json:"format"`
	CanonicalJSON []byte             `json:"canonicalJson"`
	Diagnostics   []ImportDiagnostic `json:"diagnostics"`
}

// ImportRulePackage 只负责把显式 JSON/YAML/TOML 输入转换为同一份规范 JSON。
// 它不把导入格式写入执行事实，也不把未知 extension 投影成 Go 结构，因此未知
// namespace 会随 canonical JSON 原样进入保存、diff、回滚和再导出流程。
func ImportRulePackage(format string, input []byte) (ImportResult, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		format = "json"
	}
	if len(input) == 0 || len(input) > MaxRulePackageBytes {
		return ImportResult{}, fmt.Errorf("规则包大小超限")
	}
	var value any
	var err error
	switch format {
	case "json":
		value, err = decodeAny(input)
	case "yaml", "yml":
		format = "yaml"
		value, err = decodeYAML(input)
	case "toml":
		value, err = decodeTOML(input)
	default:
		return ImportResult{}, fmt.Errorf("不支持的规则导入格式 %q", format)
	}
	if err != nil {
		return ImportResult{}, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ImportResult{}, fmt.Errorf("导入值无法转换为 JSON: %w", err)
	}
	canonical, err := NormalizeWithSchema(encoded, RulePackageSchema())
	if err != nil {
		return ImportResult{}, fmt.Errorf("导入规范化: %w", err)
	}
	return ImportResult{Format: format, CanonicalJSON: canonical, Diagnostics: []ImportDiagnostic{}}, nil
}

func decodeYAML(input []byte) (any, error) {
	var document yaml.Node
	decoder := yaml.NewDecoder(strings.NewReader(string(input)))
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("解析 YAML: %w", err)
	}
	if document.Kind == 0 || len(document.Content) == 0 {
		return nil, fmt.Errorf("YAML 文档为空")
	}
	if len(document.Content) > 1 {
		return nil, fmt.Errorf("YAML 只允许一个文档")
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err == nil {
		return nil, fmt.Errorf("YAML 只允许一个文档")
	} else if err != io.EOF {
		return nil, fmt.Errorf("解析 YAML 尾部: %w", err)
	}
	return yamlNodeValue(document.Content[0], "")
}

func yamlNodeValue(node *yaml.Node, pointer string) (any, error) {
	if node == nil {
		return nil, fmt.Errorf("YAML 节点为空")
	}
	if node.Anchor != "" || node.Kind == yaml.AliasNode {
		return nil, fmt.Errorf("YAML 锚点和别名不允许: %s", pointer)
	}
	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) != 1 {
			return nil, fmt.Errorf("YAML 文档结构无效")
		}
		return yamlNodeValue(node.Content[0], pointer)
	case yaml.MappingNode:
		result := make(map[string]any, len(node.Content)/2)
		seen := make(map[string]struct{}, len(node.Content)/2)
		for index := 0; index < len(node.Content); index += 2 {
			key := node.Content[index]
			if key.Tag != "!!str" && key.Tag != "" {
				return nil, fmt.Errorf("YAML 对象键必须是字符串: %s", pointer)
			}
			name := key.Value
			if _, exists := seen[name]; exists {
				return nil, fmt.Errorf("YAML 对象包含重复键 %q: %s", name, pointer)
			}
			seen[name] = struct{}{}
			childPointer := pointer + "/" + escapePointer(name)
			value, err := yamlNodeValue(node.Content[index+1], childPointer)
			if err != nil {
				return nil, err
			}
			result[name] = value
		}
		return result, nil
	case yaml.SequenceNode:
		result := make([]any, 0, len(node.Content))
		for index, child := range node.Content {
			value, err := yamlNodeValue(child, fmt.Sprintf("%s/%d", pointer, index))
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		return result, nil
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!null":
			return nil, nil
		case "!!bool":
			value, err := strconv.ParseBool(strings.ToLower(node.Value))
			if err != nil {
				return nil, fmt.Errorf("YAML 布尔值无效: %s", pointer)
			}
			return value, nil
		case "!!int":
			value := strings.ReplaceAll(node.Value, "_", "")
			if _, err := normalizeNumber(value); err != nil {
				return nil, fmt.Errorf("YAML 整数必须是十进制 JSON 数字: %s", pointer)
			}
			return json.Number(value), nil
		case "!!float":
			value := strings.ReplaceAll(node.Value, "_", "")
			if _, err := normalizeNumber(value); err != nil {
				return nil, fmt.Errorf("YAML 浮点值必须是有限十进制数字: %s", pointer)
			}
			return json.Number(value), nil
		case "!!str", "":
			return node.Value, nil
		default:
			return nil, fmt.Errorf("YAML 类型 %q 不可无损转换为 JSON: %s", node.Tag, pointer)
		}
	default:
		return nil, fmt.Errorf("YAML 节点类型无效: %s", pointer)
	}
}

func decodeTOML(input []byte) (any, error) {
	var value map[string]any
	if err := toml.Unmarshal(input, &value); err != nil {
		return nil, fmt.Errorf("解析 TOML: %w", err)
	}
	return tomlJSONValue(value, "")
}

func tomlJSONValue(value any, pointer string) (any, error) {
	switch typed := value.(type) {
	case nil, bool, string:
		return typed, nil
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return nil, err
		}
		return json.Number(string(encoded)), nil
	case float32, float64:
		// TOML 浮点通常通过 float64 解码，无法证明任意精度身份；拒绝而不是静默漂移。
		return nil, fmt.Errorf("TOML 浮点值不能无损转换为规则 JSON: %s", pointer)
	case time.Time:
		return nil, fmt.Errorf("TOML 日期/时间类型不允许隐式转换: %s", pointer)
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			converted, err := tomlJSONValue(item, fmt.Sprintf("%s/%d", pointer, index))
			if err != nil {
				return nil, err
			}
			result[index] = converted
		}
		return result, nil
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			converted, err := tomlJSONValue(item, pointer+"/"+escapePointer(key))
			if err != nil {
				return nil, err
			}
			result[key] = converted
		}
		return result, nil
	default:
		return nil, fmt.Errorf("TOML 类型 %T 不可无损转换为 JSON: %s", value, pointer)
	}
}
