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
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("解析 JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("JSON 包含多个值")
		}
		return nil, fmt.Errorf("JSON 尾部无效: %w", err)
	}
	return value, nil
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
