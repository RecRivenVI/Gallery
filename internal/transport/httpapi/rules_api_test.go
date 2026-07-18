package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/auth"
	"github.com/RecRivenVI/gallery/internal/config"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/storage"
	"github.com/RecRivenVI/gallery/internal/transport/httpapi"
	api "github.com/RecRivenVI/gallery/pkg/galleryapi"
)

func TestRuleLifecycleIsAvailableThroughAuthenticatedAPI(t *testing.T) {
	server, client, csrf := pairedRuleServer(t)
	packageJSON, err := os.ReadFile(filepath.Join("..", "..", "rules", "testdata", "minimal-rule-package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var packageValue map[string]any
	if err := json.Unmarshal(packageJSON, &packageValue); err != nil {
		t.Fatal(err)
	}

	validate := postRuleJSON(t, client, server.URL, csrf, "/api/v1/rules/validate", map[string]any{"package": packageValue})
	if validate["packageHash"] == "" || validate["semanticHash"] == "" || validate["canonicalPackage"] == nil {
		t.Fatalf("validate 响应不完整: %+v", validate)
	}
	first := postRuleJSON(t, client, server.URL, csrf, "/api/v1/rules/compile", map[string]any{"package": packageValue, "parameters": map[string]any{}})
	second := postRuleJSON(t, client, server.URL, csrf, "/api/v1/rules/compile", map[string]any{"package": packageValue, "parameters": map[string]any{}})
	if first["cacheHit"] != false || second["cacheHit"] != true || first["ruleIrHash"] != second["ruleIrHash"] {
		t.Fatalf("compile/cache 响应错误: first=%+v second=%+v", first, second)
	}
	dryRun := postRuleJSON(t, client, server.URL, csrf, "/api/v1/rules/dry-run", map[string]any{
		"package": packageValue, "parameters": map[string]any{},
		"sample": map[string]any{"path": "layout-a/work", "metadata": map[string]any{}, "files": []any{map[string]any{"path": "media.bin", "size": 8}}},
	})
	work := dryRun["work"].(map[string]any)
	if work["title"] != "work" || len(work["media"].([]any)) != 1 || dryRun["trace"] == nil {
		t.Fatalf("Dry Run 响应错误: %+v", dryRun)
	}
	changed := cloneMap(t, packageValue)
	changed["version"] = "0.2.0"
	primitives := changed["primitives"].([]any)
	for _, raw := range primitives {
		primitive := raw.(map[string]any)
		if primitive["id"] == "media" {
			primitive["config"].(map[string]any)["glob"] = "*.jpg"
		}
	}
	impact := postRuleJSON(t, client, server.URL, csrf, "/api/v1/rules/impact", map[string]any{"before": packageValue, "after": changed})
	if impact["fullRescan"] != true || impact["reproject"] != true {
		t.Fatalf("Impact 响应错误: %+v", impact)
	}
}

func TestPersistentRulePackageAPIUsesRevisionAndPublishCapability(t *testing.T) {
	server, client, csrf := pairedRuleServer(t)
	packageJSON, err := os.ReadFile(filepath.Join("..", "..", "rules", "testdata", "minimal-rule-package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var packageValue map[string]any
	if err := json.Unmarshal(packageJSON, &packageValue); err != nil {
		t.Fatal(err)
	}
	pkg := requestRuleJSON(t, client, server.URL, csrf, http.MethodPost, "/api/v1/rule-packages", map[string]any{"name": "API 规则包"}, http.StatusCreated)
	draft := requestRuleJSON(t, client, server.URL, csrf, http.MethodPut, "/api/v1/rule-packages/"+pkg["id"].(string)+"/draft", map[string]any{"format": "json", "content": packageValue, "expectedRevision": 0}, http.StatusOK)
	if draft["validationStatus"] != "validated" {
		t.Fatalf("草稿未校验: %+v", draft)
	}
	version := requestRuleJSON(t, client, server.URL, csrf, http.MethodPost, "/api/v1/rule-packages/"+pkg["id"].(string)+"/publish", map[string]any{"expectedRevision": int(draft["revision"].(float64)), "reason": "API 发布"}, http.StatusCreated)
	if version["semanticHash"] == "" {
		t.Fatalf("发布响应缺少 semanticHash: %+v", version)
	}
	versions := requestRuleJSON(t, client, server.URL, csrf, http.MethodGet, "/api/v1/rule-packages/"+pkg["id"].(string)+"/versions", nil, http.StatusOK)
	if len(versions["items"].([]any)) != 1 {
		t.Fatalf("版本列表错误: %+v", versions)
	}
	trace := requestRuleJSON(t, client, server.URL, csrf, http.MethodPost, "/api/v1/rules/trace", map[string]any{
		"semanticHash": version["semanticHash"], "parameters": map[string]any{}, "sample": map[string]any{"path": "work", "metadata": map[string]any{}, "files": []any{map[string]any{"path": "media.bin", "size": 1}}},
	}, http.StatusOK)
	if trace["trace"] == nil {
		t.Fatalf("Trace 响应缺少 trace: %+v", trace)
	}
}

func TestBuiltInRuleExamplesAndExecutionAPI(t *testing.T) {
	server, client, csrf := pairedRuleServer(t)
	response := requestRuleJSON(t, client, server.URL, csrf, http.MethodGet, "/api/v1/rules/examples", nil, http.StatusOK)
	items, ok := response["items"].([]any)
	if !ok || len(items) != 3 {
		t.Fatalf("内置示例列表错误: %+v", response)
	}
	for _, raw := range items {
		item := raw.(map[string]any)
		id := item["id"].(string)
		result := requestRuleJSON(t, client, server.URL, csrf, http.MethodPost, "/api/v1/rules/examples/"+id+"/test", map[string]any{"parameters": map[string]any{}}, http.StatusOK)
		if result["semanticHash"] == "" || result["result"] == nil {
			t.Fatalf("内置示例执行响应错误: %+v", result)
		}
	}
}

func pairedRuleServer(t *testing.T) (*httptest.Server, *http.Client, string) {
	t.Helper()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	fixedClock := clock.Fixed{Time: time.Now().UTC()}
	personal, err := auth.NewPersonal(store.Control.SQL(), fixedClock, identity.NewGenerator(fixedClock), nil)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, fixedClock, identity.NewGenerator(fixedClock))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.New(config.ModePersonal, store, fixedClock, personal, resources, nil, nil, nil, nil, nil, nil, nil, slog.New(slog.NewJSONHandler(io.Discard, nil))))
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	httpClient := &http.Client{Jar: jar}
	generated, err := api.NewClientWithResponses(server.URL, api.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := generated.GetBootstrapWithResponse(context.Background())
	if err != nil || bootstrap.JSON200 == nil {
		t.Fatal(err)
	}
	editor := sameOrigin(server.URL)
	attempt, err := generated.CreatePairingAttemptWithResponse(context.Background(), &api.CreatePairingAttemptParams{XGalleryCSRF: bootstrap.JSON200.CsrfToken}, editor)
	if err != nil || attempt.JSON201 == nil {
		t.Fatal(err)
	}
	exchange, err := generated.ExchangePairingCredentialWithResponse(context.Background(), &api.ExchangePairingCredentialParams{XGalleryCSRF: bootstrap.JSON200.CsrfToken}, api.PairingExchangeRequest{Credential: attempt.JSON201.Credential}, editor)
	if err != nil || exchange.JSON201 == nil {
		t.Fatal(err)
	}
	return server, httpClient, exchange.JSON201.CsrfToken
}

func postRuleJSON(t *testing.T, client *http.Client, baseURL, csrf, path string, body any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", baseURL)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("X-Gallery-CSRF", csrf)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	content, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s status=%d body=%s", path, response.StatusCode, content)
	}
	var result map[string]any
	if err := json.Unmarshal(content, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func requestRuleJSON(t *testing.T, client *http.Client, baseURL, csrf, method, path string, body any, wantStatus int) map[string]any {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, baseURL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", baseURL)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	if method != http.MethodGet {
		request.Header.Set("X-Gallery-CSRF", csrf)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	content, _ := io.ReadAll(response.Body)
	if response.StatusCode != wantStatus {
		t.Fatalf("%s %s status=%d body=%s", method, path, response.StatusCode, content)
	}
	var result map[string]any
	if err := json.Unmarshal(content, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func cloneMap(t *testing.T, input map[string]any) map[string]any {
	t.Helper()
	encoded, _ := json.Marshal(input)
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
