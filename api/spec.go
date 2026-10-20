package api

import (
	_ "embed"
	"go.yaml.in/yaml/v3"
)

//go:embed openapi.yaml
var openAPISpec []byte

// ContractVersion 来自嵌入契约的 info.version。
var ContractVersion = func() string {
	var document struct {
		Info struct {
			Version string `yaml:"version"`
		} `yaml:"info"`
	}
	if err := yaml.Unmarshal(openAPISpec, &document); err != nil {
		panic(err) // 嵌入文件必须是合法的构建输入。
	}
	return document.Info.Version
}()

func OpenAPISpec() []byte { return append([]byte(nil), openAPISpec...) }
