package api_test

import (
	"bytes"
	"encoding/json"
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

func TestGeneratedRuleLifecycleConcurrencyAndBindingModes(t *testing.T) {
	revisions := []string{
		api.SaveRuleDraftParams{IfMatch: `"0"`}.IfMatch,
		api.ValidateRuleDraftParams{IfMatch: `"1"`}.IfMatch,
		api.PublishRuleDraftParams{IfMatch: `"1"`}.IfMatch,
		api.RollbackRulePackageParams{IfMatch: `"1"`}.IfMatch,
	}
	for _, revision := range revisions {
		if revision == "" {
			t.Fatal("规则生命周期修改端点必须生成 required If-Match")
		}
	}
	var after api.RuleImpactRequest_After
	if err := after.FromRuleImpactRequestAfter0(map[string]any{"rule_set_id": "initial"}); err != nil {
		t.Fatal(err)
	}
	impact := api.RuleImpactRequest{Before: nil, After: after}
	if impact.Before != nil {
		t.Fatal("首次 RuleImpact 必须允许显式 null before")
	}
	encodedImpact, err := json.Marshal(impact)
	if err != nil || !bytes.Contains(encodedImpact, []byte(`"before":null`)) {
		t.Fatalf("首次 RuleImpact 未保留显式 null before: %s %v", encodedImpact, err)
	}
	var exact api.RuleImpactRequest_After
	if err := exact.FromRuleImpactRequestAfter1(`{"exact":9007199254740993123}`); err != nil {
		t.Fatal(err)
	}
	if value, err := exact.AsRuleImpactRequestAfter1(); err != nil || value == "" {
		t.Fatalf("RuleImpact 精确 JSON 文本 union 不可用: value=%q err=%v", value, err)
	}
	direct := api.NewDirectSourceRuleBindingCreateRequest("src_00000000-0000-7000-8000-000000000001", string(bytes.Repeat([]byte{'0'}, 64)), map[string]any{}, 0)
	parameter := api.NewParameterSourceRuleBindingCreateRequest("src_00000000-0000-7000-8000-000000000001", "rparam_00000000-0000-7000-8000-000000000001", 0, map[string]any{}, nil)
	for name, request := range map[string]api.SourceRuleBindingCreateRequest{"direct": direct, "parameter": parameter} {
		encoded, err := json.Marshal(request)
		if err != nil {
			t.Fatalf("%s binding request: %v", name, err)
		}
		if name == "direct" && (!bytes.Contains(encoded, []byte(`"semanticHash"`)) || bytes.Contains(encoded, []byte(`"parameterId"`))) {
			t.Fatalf("direct 模式未保持互斥: %s", encoded)
		}
		if name == "parameter" && (!bytes.Contains(encoded, []byte(`"parameterId"`)) || bytes.Contains(encoded, []byte(`"semanticHash"`))) {
			t.Fatalf("parameter 模式未保持互斥: %s", encoded)
		}
	}
}

func TestPublicationBoundResponsesExposeValidationErrors(t *testing.T) {
	validation := &api.ValidationError{}
	responses := []any{
		api.GetWorkResponse{JSON400: validation},
		api.ListWorkMediaResponse{JSON400: validation},
		api.GetMediaResponse{JSON400: validation},
		api.HeadMediaContentResponse{JSON400: validation},
		api.GetMediaContentResponse{JSON400: validation},
		api.CreateMediaVerificationJobResponse{JSON400: validation},
	}
	if len(responses) != 6 {
		t.Fatalf("publication-bound validation response count = %d, want 6", len(responses))
	}
}
