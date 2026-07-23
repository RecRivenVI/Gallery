package httpapi

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
