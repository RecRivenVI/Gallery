package rules

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"golang.org/x/text/unicode/norm"
)

// NormalizeWithSchema materializes JSON Schema defaults and applies only the
// string normalization explicitly declared by x-gallery-normalization.
func NormalizeWithSchema(input, schemaJSON []byte) ([]byte, error) {
	value, err := decodeAny(input)
	if err != nil {
		return nil, err
	}
	schemaValue, err := decodeAny(schemaJSON)
	if err != nil {
		return nil, fmt.Errorf("解析 Schema: %w", err)
	}
	schema, ok := schemaValue.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Schema 必须是对象")
	}
	value, err = normalizeValue(value, schema)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := writeCanonical(&output, value); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func decodeAny(input []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	value, err := decodeStrictValue(decoder)
	if err != nil {
		return nil, fmt.Errorf("解析 JSON: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("JSON 包含多个值")
		}
		return nil, fmt.Errorf("JSON 尾部无效: %w", err)
	}
	return value, nil
}

// decodeStrictValue 使用 Token API 检查对象重复键。encoding/json 默认会让后一个
// 重复键覆盖前一个键，这对规则身份和导入安全性不可接受。
func decodeStrictValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			result := map[string]any{}
			seen := map[string]struct{}{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, fmt.Errorf("对象键必须是字符串")
				}
				if _, exists := seen[key]; exists {
					return nil, fmt.Errorf("对象包含重复键 %q", key)
				}
				seen[key] = struct{}{}
				item, err := decodeStrictValue(decoder)
				if err != nil {
					return nil, err
				}
				result[key] = item
			}
			_, err = decoder.Token()
			if err != nil {
				return nil, err
			}
			return result, nil
		case '[':
			result := []any{}
			for decoder.More() {
				item, err := decodeStrictValue(decoder)
				if err != nil {
					return nil, err
				}
				result = append(result, item)
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return result, nil
		default:
			return nil, fmt.Errorf("JSON 分隔符无效")
		}
	case nil, bool, string, json.Number:
		return value, nil
	default:
		return nil, fmt.Errorf("JSON 类型无效 %T", value)
	}
}

func normalizeValue(value any, schema map[string]any) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch typed := value.(type) {
	case map[string]any:
		properties, _ := schema["properties"].(map[string]any)
		for name, propertyValue := range properties {
			property, _ := propertyValue.(map[string]any)
			current, exists := typed[name]
			if !exists {
				if defaultValue, ok := property["default"]; ok {
					typed[name] = cloneJSONValue(defaultValue)
					current, exists = typed[name], true
				}
			}
			if exists {
				normalized, err := normalizeValue(current, property)
				if err != nil {
					return nil, fmt.Errorf("/%s: %w", escapePointer(name), err)
				}
				typed[name] = normalized
			}
		}
		return typed, nil
	case []any:
		items, _ := schema["items"].(map[string]any)
		for index := range typed {
			normalized, err := normalizeValue(typed[index], items)
			if err != nil {
				return nil, fmt.Errorf("/%d: %w", index, err)
			}
			typed[index] = normalized
		}
		return typed, nil
	case string:
		policy, _ := schema["x-gallery-normalization"].(string)
		switch policy {
		case "", "preserve":
			return typed, nil
		case "nfc", "identifier":
			return norm.NFC.String(typed), nil
		default:
			return nil, fmt.Errorf("未知字符串规范化策略 %q", policy)
		}
	default:
		return value, nil
	}
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = cloneJSONValue(item)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = cloneJSONValue(item)
		}
		return result
	default:
		return typed
	}
}

func escapePointer(input string) string {
	result := bytes.ReplaceAll([]byte(input), []byte("~"), []byte("~0"))
	result = bytes.ReplaceAll(result, []byte("/"), []byte("~1"))
	return string(result)
}
