package webapp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesIndexAndSPAFallback(t *testing.T) {
	handler := New("0.6.0-pre-alpha", "v1")
	for _, target := range []string{"/", "/works/wrk_example"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Gallery") {
			t.Fatalf("%s status=%d body=%q", target, response.Code, response.Body.String())
		}
		if response.Header().Get("Content-Security-Policy") == "" {
			t.Fatalf("%s 缺少 CSP", target)
		}
	}
}

func TestHandlerDoesNotSwallowReservedRoutes(t *testing.T) {
	handler := New("0.6.0-pre-alpha", "v1")
	for _, target := range []string{"/api/v1/unknown", "/ws/v2"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "NOT_FOUND") {
			t.Fatalf("%s status=%d body=%q", target, response.Code, response.Body.String())
		}
	}
}

func TestHandlerRejectsContractMismatch(t *testing.T) {
	handler := New("different", "v1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "WEB_VERSION_MISMATCH") {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestRootWebPatternCanCoexistWithWebSocketPattern(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/ws/v1", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	mux.Handle("/", New("0.6.0-pre-alpha", "v1"))

	request := httptest.NewRequest(http.MethodGet, "/ws/v1", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("WebSocket 精确路由被根 Web handler 截获: %d", response.Code)
	}
}
