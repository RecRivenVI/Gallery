package api_test

import (
	"bytes"
	"testing"

	"github.com/RecRivenVI/gallery/internal/contract/api"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
)

func TestGeneratedErrorEnumCoversCanonicalCodes(t *testing.T) {
	if !bytes.Contains(api.OpenAPISpec(), []byte("openapi: 3.0.3")) {
		t.Fatal("OpenAPI 规范未嵌入")
	}
	for _, code := range fault.AllCodes() {
		if !api.ErrorCode(code).Valid() {
			t.Fatalf("稳定错误码 %s 未进入生成的 OpenAPI DTO", code)
		}
	}
}
