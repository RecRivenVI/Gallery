package api

import (
	_ "embed"
)

//go:embed openapi.yaml
var openAPISpec []byte

func OpenAPISpec() []byte { return append([]byte(nil), openAPISpec...) }
