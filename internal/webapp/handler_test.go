package webapp

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
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

// synthAssets 构造一份最小的合成产物集合，使外壳路由能在真实构建产物之外单独验证。
// withManage 为 false 时刻意不含 manage.html，用于验证「声明了界面但外壳缺失」的行为。
func synthAssets(withManage bool) fstest.MapFS {
	assets := fstest.MapFS{
		"gallery-web.json": &fstest.MapFile{Data: []byte(
			`{"webVersion":"0.0.0-test","contractVersion":"0.6.0-pre-alpha","apiVersion":"v1"}`)},
		"index.html":        &fstest.MapFile{Data: []byte("<main>gallery</main>")},
		"assets/app-abc.js": &fstest.MapFile{Data: []byte("// gallery bundle")},
		"assets/mng-def.js": &fstest.MapFile{Data: []byte("// manage bundle")},
	}
	if withManage {
		assets["manage.html"] = &fstest.MapFile{Data: []byte("<main>manage</main>")}
	}
	return assets
}

// TestManagementDeepLinkServesManagementShell 锁定双入口的深链行为。
//
// 此前任何不存在的路径都回落到 index.html，因此 `/manage/jobs` 这类管理端深链——刷新、书签或
// 直接打开链接——会落进**画廊**外壳。两套界面共用一个 Handler，外壳必须按路径前缀决定。
func TestManagementDeepLinkServesManagementShell(t *testing.T) {
	handler := newFromAssets(synthAssets(true), "0.6.0-pre-alpha", "v1")
	for _, item := range []struct{ path, want string }{
		{"/", "gallery"},
		{"/browse", "gallery"},
		{"/works/wrk_1", "gallery"},
		{"/manage", "manage"},
		{"/manage.html", "manage"},
		{"/manage/jobs", "manage"},
		{"/manage/rules/pkg_1/versions", "manage"},
		// 前缀必须是完整路径段：`/management` 不属于管理端。
		{"/management", "gallery"},
		{"/manageable/thing", "gallery"},
	} {
		t.Run(item.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, item.path, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d", recorder.Code)
			}
			if !strings.Contains(recorder.Body.String(), item.want) {
				t.Fatalf("%s 未由 %s 外壳承接: %q", item.path, item.want, recorder.Body.String())
			}
		})
	}
}

// TestManagementDeepLinkFailsLoudlyWhenShellMissing 保证外壳缺失时**不静默回落到画廊**。
// 回落会把「管理端还没构建出来」伪装成「管理端深链打开了画廊」，让缺陷不可见。
func TestManagementDeepLinkFailsLoudlyWhenShellMissing(t *testing.T) {
	handler := newFromAssets(synthAssets(false), "0.6.0-pre-alpha", "v1")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/manage/jobs", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if strings.Contains(recorder.Body.String(), "gallery") {
		t.Fatalf("外壳缺失时回落到了画廊: %q", recorder.Body.String())
	}
	// 画廊本身不受影响。
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/browse", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "gallery") {
		t.Fatalf("画廊深链被连带影响: status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}
