package api

import (
	_ "embed"
)

// ContractVersion 与 OpenAPI info.version 保持一致，并用于验证随 galleryd 一起
// 发行的 Web 静态资源确实由同一份公共契约生成。
const ContractVersion = "0.6.0-pre-alpha"

//go:embed openapi.yaml
var openAPISpec []byte

func OpenAPISpec() []byte { return append([]byte(nil), openAPISpec...) }
