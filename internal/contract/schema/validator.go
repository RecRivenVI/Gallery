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

// deniedLoader 拒绝一切外部 schema 资源加载。
//
// jsonschema/v6 的默认 URLLoader 是 FileLoader{}，并且 AddResource 的 name 会被解析成
// 相对进程工作目录的 file:// 基 URI。因此在默认配置下，schema 里的 `$ref` 可以是
// `"../../x.json"`、`"file:///abs/path"` 乃至 `"file://///host/share/x.json"`，编译器会真的
// 去读本地文件、甚至在 Windows 上发起 UNC/SMB 访问。Compile 的输入并不总是仓库内置的
// 可信 schema：CompileBinding 会把规则包携带的 parameter_schema 原样喂进来（见
// internal/rules/package.go），而 rule-package.schema.json 只把该字段约束为 `type: object`。
// 这与「规则不得触达文件与网络」的产品边界直接冲突，因此这里显式关闭外部加载：schema
// 必须自包含，只允许同文档内的 `#/...` 引用与库内嵌的 JSON Schema 元 schema。
type deniedLoader struct{}

func (deniedLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("拒绝加载外部 JSON Schema 资源 %q：schema 必须自包含", url)
}

func Compile(name string, schemaBytes []byte) (*Validator, error) {
	decoder := json.NewDecoder(bytes.NewReader(schemaBytes))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("解析 JSON Schema: %w", err)
	}
	if decoder.More() {
		return nil, fmt.Errorf("解析 JSON Schema: 首个 JSON 值之后存在多余内容")
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(deniedLoader{})
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
	// 只解码首个 JSON 值会让 `{"a":1} {"b":2}` 这类载荷被静默接受，而校验只作用于前半段。
	// 校验器的结论必须覆盖调用方交进来的全部字节，否则「已通过校验」不再等价于「这段字节
	// 是合法的」。
	if decoder.More() {
		return fmt.Errorf("解析 JSON: 首个 JSON 值之后存在多余内容")
	}
	if err := v.schema.Validate(value); err != nil {
		return fmt.Errorf("JSON Schema 校验: %w", err)
	}
	return nil
}
