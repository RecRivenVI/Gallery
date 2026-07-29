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
		api.DeleteRulePackageParams{IfMatch: `"0"`}.IfMatch,
		api.DeprecateRulePackageParams{IfMatch: `"0"`}.IfMatch,
		api.SaveRuleDraftParams{IfMatch: `"0"`}.IfMatch,
		api.ValidateRuleDraftParams{IfMatch: `"1"`}.IfMatch,
		api.PublishRuleDraftParams{IfMatch: `"1"`}.IfMatch,
		api.RollbackRulePackageParams{IfMatch: `"1"`}.IfMatch,
		api.UpdateRuleParameterSetParams{IfMatch: `"1"`}.IfMatch,
		api.DeprecateRuleParameterSetParams{IfMatch: `"1"`}.IfMatch,
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

func TestGeneratedRuleParameterAndBindingContractsPreserveExactJSON(t *testing.T) {
	const exactText = `{"exact":9007199254740993123}`
	exact := api.ExactJSONObject{}
	if err := exact.FromExactJSONObject1(exactText); err != nil {
		t.Fatal(err)
	}
	requests := []any{
		api.RuleParameterCreateRequest{Name: "exact", SemanticHash: string(bytes.Repeat([]byte{'0'}, 64)), Parameters: exact},
		api.RuleParameterUpdateRequest{Parameters: exact, ExpectedRevision: 1, ConfirmImpact: true},
		api.RuleParameterImpactRequest{Parameters: exact},
	}
	for _, request := range requests {
		encoded, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(encoded, []byte(`"parameters":"{\"exact\":9007199254740993123}"`)) {
			t.Fatalf("参数精确文本未以 string union 保留: %s", encoded)
		}
	}

	direct := api.SourceRuleBindingCreateRequest{
		SourceId: "src_00000000-0000-7000-8000-000000000001", SemanticHash: ptr(api.SHA256Digest(string(bytes.Repeat([]byte{'0'}, 64)))), Parameters: &exact, Priority: 0,
	}
	parameterID := api.RuleParameterId("rparam_00000000-0000-7000-8000-000000000001")
	parameter := api.SourceRuleBindingCreateRequest{
		SourceId: "src_00000000-0000-7000-8000-000000000001", ParameterId: &parameterID, Override: &exact, Priority: 0,
	}
	for name, request := range map[string]api.SourceRuleBindingCreateRequest{"direct": direct, "parameter": parameter} {
		encoded, err := json.Marshal(request)
		if err != nil {
			t.Fatalf("%s binding: %v", name, err)
		}
		if !bytes.Contains(encoded, []byte(`9007199254740993123`)) {
			t.Fatalf("%s binding 未保留精确文本: %s", name, encoded)
		}
	}

	parameterSet := api.RuleParameterSet{ParametersText: exactText}
	binding := api.SourceRuleBinding{ParametersText: exactText, OverrideText: `{}`}
	if parameterSet.ParametersText != exactText || binding.ParametersText != exactText || binding.OverrideText != `{}` {
		t.Fatal("参数集或 Binding 响应缺少 required 精确文本字段")
	}
	update := api.RuleParameterUpdateRequest{Parameters: exact, ExpectedRevision: 7, ConfirmImpact: true}
	deprecate := api.RuleParameterDeprecateRequest{ExpectedRevision: 7, Reason: "replaced"}
	if update.ExpectedRevision != 7 || !update.ConfirmImpact || deprecate.Reason == "" {
		t.Fatal("参数更新/弃用请求缺少 required revision、impact confirmation 或 reason")
	}
}

func TestGeneratedRuleAuditIdentifiesSubject(t *testing.T) {
	audit := api.RuleAudit{SubjectType: api.ParameterSet, SubjectId: "rparam_00000000-0000-7000-8000-000000000001"}
	if audit.SubjectType != api.ParameterSet || audit.SubjectId == "" {
		t.Fatal("RuleAudit 必须生成 required subjectType/subjectId")
	}
}

func ptr[T any](value T) *T { return &value }

func TestPublicationBoundResponsesExposeValidationErrors(t *testing.T) {
	validation := &api.ValidationError{}
	responses := []any{
		api.GetWorkResponse{JSON400: validation},
		api.ListWorkMediaResponse{JSON400: validation},
		api.GetMediaResponse{JSON400: validation},
		api.HeadMediaContentResponse{JSON400: validation},
		api.GetMediaContentResponse{JSON400: validation},
		api.CreateMediaVerificationJobResponse{JSON400: validation},
		api.CreateMediaVerificationBatchJobResponse{JSON400: validation},
	}
	if len(responses) != 7 {
		t.Fatalf("publication-bound validation response count = %d, want 7", len(responses))
	}
}
