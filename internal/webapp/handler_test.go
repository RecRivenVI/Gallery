package webapp

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
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

func TestPrecacheManifestIsCanonical(t *testing.T) {
	contents, err := embedded.ReadFile("dist/sw.js")
	if err != nil {
		t.Fatal(err)
	}

	const marker = "precacheAndRoute("
	start := strings.Index(string(contents), marker)
	if start < 0 {
		t.Fatal("sw.js 缺少 precacheAndRoute 清单")
	}
	manifest := string(contents[start+len(marker):])
	end := strings.Index(manifest, "],{})")
	if end < 0 {
		t.Fatal("sw.js 的 precacheAndRoute 清单格式不可识别")
	}

	matches := regexp.MustCompile(`url:"([^"]+)"`).FindAllStringSubmatch(manifest[:end+1], -1)
	if len(matches) == 0 {
		t.Fatal("precache 清单为空")
	}

	urls := make([]string, len(matches))
	for i, match := range matches {
		urls[i] = match[1]
	}
	want := slices.Clone(urls)
	slices.Sort(want)
	if !slices.Equal(urls, want) {
		t.Fatalf("precache URL 未按稳定二元序排列: got=%v want=%v", urls, want)
	}
	for i := 1; i < len(urls); i++ {
		if urls[i-1] == urls[i] {
			t.Fatalf("precache URL 重复: %q", urls[i])
		}
	}

	var expected []string
	err = fs.WalkDir(embedded, "dist", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		name = strings.TrimPrefix(name, "dist/")
		if name == "sw.js" || strings.HasPrefix(name, "workbox-") && strings.HasSuffix(name, ".js") {
			return nil
		}
		switch {
		case strings.HasSuffix(name, ".js"),
			strings.HasSuffix(name, ".css"),
			strings.HasSuffix(name, ".html"),
			strings.HasSuffix(name, ".svg"),
			strings.HasSuffix(name, ".png"),
			strings.HasSuffix(name, ".json"),
			strings.HasSuffix(name, ".webmanifest"):
			expected = append(expected, name)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("枚举嵌入 Web 资产: %v", err)
	}
	slices.Sort(expected)
	if !slices.Equal(urls, expected) {
		t.Fatalf("precache 资产集合不完整: got=%v want=%v", urls, expected)
	}
}
