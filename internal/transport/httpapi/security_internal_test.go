package httpapi

import (
	"bytes"
	"log/slog"
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

func TestDecodeJSONKeepsGenericLimitAndAllowsRuleBudget(t *testing.T) {
	body := `{"value":"` + strings.Repeat("x", maxJSONRequestBodyBytes) + `"}`
	request := func() *http.Request {
		value := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		value.Header.Set("Content-Type", "application/json")
		return value
	}
	var target struct {
		Value string `json:"value"`
	}
	if err := decodeJSON(request(), &target); err == nil || err.Error() != "请求体大小超限" {
		t.Fatalf("普通请求体没有保持 1 MiB 上限: %v", err)
	}
	if err := decodeRuleJSON(request(), &target); err != nil {
		t.Fatalf("超过普通上限但低于规则预算的请求被拒绝: %v", err)
	}
	if target.Value != strings.Repeat("x", maxJSONRequestBodyBytes) {
		t.Fatalf("规则请求解码长度=%d", len(target.Value))
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
		{fault.CodeCursorExpired, http.StatusConflict},
		{fault.CodeLANAlreadyInitialized, http.StatusConflict},
		{fault.CodeLANOwnerRequired, http.StatusPreconditionRequired},
	}
	for _, item := range cases {
		if got := statusForFault(fault.New(item.code, false, nil)); got != item.want {
			t.Fatalf("%s 映射为 %d，应为 %d", item.code, got, item.want)
		}
	}
}

func TestOptionalQueryPublicationIDDistinguishesAbsentFromMalformed(t *testing.T) {
	const valid = "qpub_018f47d2-5c16-7a44-a8a0-000000000001"
	cases := []struct {
		name     string
		target   string
		rawQuery string
		want     string
		wantErr  bool
	}{
		{name: "absent uses current", target: "/api/v1/works", want: ""},
		{name: "valid snapshot", target: "/api/v1/works?queryPublicationId=" + valid, want: valid},
		{name: "present empty", target: "/api/v1/works?queryPublicationId=", wantErr: true},
		{name: "invalid id", target: "/api/v1/works?queryPublicationId=qpub_invalid", wantErr: true},
		{name: "duplicate", target: "/api/v1/works?queryPublicationId=" + valid + "&queryPublicationId=" + valid, wantErr: true},
		{name: "semicolon parse error", target: "/api/v1/works", rawQuery: "queryPublicationId=" + valid + ";x=1", wantErr: true},
		{name: "bad escape parse error", target: "/api/v1/works", rawQuery: "queryPublicationId=%zz", wantErr: true},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, item.target, nil)
			if item.rawQuery != "" {
				request.URL.RawQuery = item.rawQuery
			}
			got, err := optionalQueryPublicationID(request)
			if item.wantErr {
				structured := asFault(err)
				if structured.Code != fault.CodeValidation || structured.Field != "queryPublicationId" {
					t.Fatalf("malformed publication = (%q, %v), want VALIDATION_ERROR/queryPublicationId", got, err)
				}
				return
			}
			if err != nil || got != item.want {
				t.Fatalf("optionalQueryPublicationID() = (%q, %v), want (%q, nil)", got, err, item.want)
			}
		})
	}
}

func TestLoginRateSubjectIgnoresEphemeralPortAndProxyHeaders(t *testing.T) {
	first := &http.Request{RemoteAddr: "192.168.1.25:41000", Header: http.Header{
		"X-Forwarded-For": []string{"203.0.113.9"},
	}}
	second := &http.Request{RemoteAddr: "192.168.1.25:51000", Header: http.Header{
		"X-Forwarded-For": []string{"198.51.100.4"},
	}}
	if got, want := loginRateSubject(first), "192.168.1.25"; got != want {
		t.Fatalf("first subject=%q want=%q", got, want)
	}
	if got, want := loginRateSubject(second), "192.168.1.25"; got != want {
		t.Fatalf("second subject=%q want=%q", got, want)
	}
	if loginRateSubject(first) != loginRateSubject(second) {
		t.Fatal("同一对端 IP 的不同临时端口必须共享登录限流主体")
	}
}

func TestRequestLogNeverWritesShareCredential(t *testing.T) {
	const credential = "shr_00000000-0000-7000-8000-000000000001.super-secret"
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/public/shares/{credential}/media/{mediaId}/content", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := requestLog(logger, mux)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/public/shares/"+credential+"/media/med_00000000-0000-7000-8000-000000000002/content", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)
	logged := output.String()
	if strings.Contains(logged, credential) || strings.Contains(logged, "super-secret") {
		t.Fatalf("请求日志泄露 Share credential: %s", logged)
	}
	if !strings.Contains(logged, "/api/v1/public/shares/{credential}/media/{mediaId}/content") {
		t.Fatalf("请求日志未使用路由模板: %s", logged)
	}
}
