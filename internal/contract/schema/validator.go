package schema

import (
	"bytes"
	"encoding/json"
	"fmt"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

type Validator struct {
	schema *jsonschema.Schema
}

func Compile(name string, schemaBytes []byte) (*Validator, error) {
	decoder := json.NewDecoder(bytes.NewReader(schemaBytes))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("解析 JSON Schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(name, document); err != nil {
		return nil, fmt.Errorf("注册 JSON Schema: %w", err)
	}
	compiled, err := compiler.Compile(name)
	if err != nil {
		return nil, fmt.Errorf("编译 JSON Schema: %w", err)
	}
	return &Validator{schema: compiled}, nil
}

func (v *Validator) ValidateJSON(data []byte) error {
	if v == nil || v.schema == nil {
		return fmt.Errorf("JSON Schema validator 未初始化")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("解析 JSON: %w", err)
	}
	if err := v.schema.Validate(value); err != nil {
		return fmt.Errorf("JSON Schema 校验: %w", err)
	}
	return nil
}
