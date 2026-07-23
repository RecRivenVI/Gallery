package api_test

import (
	"bytes"
	"testing"

	contractapi "github.com/RecRivenVI/gallery/internal/contract/api"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	api "github.com/RecRivenVI/gallery/pkg/galleryapi"
)

func TestGeneratedErrorEnumCoversCanonicalCodes(t *testing.T) {
	if !bytes.Contains(contractapi.OpenAPISpec(), []byte("openapi: 3.0.3")) {
		t.Fatal("OpenAPI 规范未嵌入")
	}
	if !bytes.Contains(contractapi.OpenAPISpec(), []byte("version: "+contractapi.ContractVersion)) {
		t.Fatal("ContractVersion 与 OpenAPI info.version 漂移")
	}
	for _, code := range fault.AllCodes() {
		if !api.ErrorCode(code).Valid() {
			t.Fatalf("稳定错误码 %s 未进入生成的 OpenAPI DTO", code)
		}
	}
}
