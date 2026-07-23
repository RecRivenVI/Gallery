package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
)

func TestConcealForbiddenHidesForbiddenAsNotFound(t *testing.T) {
	if got := asFault(concealForbidden(fault.New(fault.CodeForbidden, false, nil))); got.Code != fault.CodeNotFound {
		t.Fatalf("Forbidden 未脱敏为 NotFound: %s", got.Code)
	}
	if got := asFault(concealForbidden(fault.New(fault.CodeValidation, false, nil))); got.Code != fault.CodeValidation {
		t.Fatalf("非 Forbidden 错误被错误改写: %s", got.Code)
	}
}

func TestDecodeJSONRequiresJSONContentType(t *testing.T) {
	textRequest := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"x"}`))
	textRequest.Header.Set("Content-Type", "text/plain")
	var target map[string]any
	if err := decodeJSON(textRequest, &target); err == nil {
		t.Fatal("非 application/json 的请求体未被拒绝")
	}
	jsonRequest := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"x"}`))
	jsonRequest.Header.Set("Content-Type", "application/json")
	if err := decodeJSON(jsonRequest, &target); err != nil {
		t.Fatalf("合法 application/json 请求体被拒绝: %v", err)
	}
}

func TestStatusForFaultMapsSecurityCodes(t *testing.T) {
	cases := []struct {
		code fault.Code
		want int
	}{
		{fault.CodeInvalidCredentials, http.StatusUnauthorized},
		{fault.CodeTokenInvalid, http.StatusUnauthorized},
		{fault.CodeRateLimited, http.StatusTooManyRequests},
		{fault.CodeLANAlreadyInitialized, http.StatusConflict},
		{fault.CodeLANOwnerRequired, http.StatusPreconditionRequired},
	}
	for _, item := range cases {
		if got := statusForFault(fault.New(item.code, false, nil)); got != item.want {
			t.Fatalf("%s 映射为 %d，应为 %d", item.code, got, item.want)
		}
	}
}

