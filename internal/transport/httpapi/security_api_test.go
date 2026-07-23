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
	"strings"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/auth"
	"github.com/RecRivenVI/gallery/internal/config"
	"github.com/RecRivenVI/gallery/internal/contract/realtime"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/storage"
	"github.com/RecRivenVI/gallery/internal/transport/httpapi"
)

func TestLANSecurityAPIInitializationLoginCookieTokenAndRevocation(t *testing.T) {
	server, store := newLANSecurityServer(t, false)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	bootstrap := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/bootstrap", "", "", nil)
	if bootstrap.StatusCode != http.StatusOK || !bytes.Contains(readAndClose(t, bootstrap), []byte(`"lanInitialized":false`)) {
		t.Fatal("LAN bootstrap 未表达未初始化状态")
	}
	csrf := bootstrapCSRF(t, client, server.URL)
	owner := map[string]any{"username": "owner", "displayName": "Owner", "password": "owner-password-strong"}
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/lan/owner", server.URL, csrf, owner)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("Owner 初始化 status=%d body=%s", response.StatusCode, readAndClose(t, response))
	}
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/lan/owner", server.URL, csrf, owner)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("Owner 重复初始化 status=%d", response.StatusCode)
	}

	badLogin := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/login", server.URL, csrf,
		map[string]any{"username": "missing", "password": "wrong-password"})
	badBody := readAndClose(t, badLogin)
	if badLogin.StatusCode != http.StatusUnauthorized || !bytes.Contains(badBody, []byte("INVALID_CREDENTIALS")) || bytes.Contains(badBody, []byte("missing")) {
		t.Fatalf("登录失败泄露或错误码错误: status=%d body=%s", badLogin.StatusCode, badBody)
	}
	login := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/login", server.URL, csrf,
		map[string]any{"username": "owner", "password": "owner-password-strong", "clientLabel": "browser-a"})
	loginBody := readAndClose(t, login)
	if login.StatusCode != http.StatusCreated {
		t.Fatalf("登录 status=%d body=%s", login.StatusCode, loginBody)
	}
	cookieHeader := login.Header.Get("Set-Cookie")
	if !strings.Contains(cookieHeader, "HttpOnly") || !strings.Contains(cookieHeader, "SameSite=Strict") || strings.Contains(cookieHeader, "Secure") {
		t.Fatalf("HTTP Cookie 属性错误: %s", cookieHeader)
	}
	var established struct {
		CsrfToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(loginBody, &established); err != nil || established.CsrfToken == "" {
		t.Fatalf("登录响应无 CSRF: %v", err)
	}
	libraryOneResponse := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/libraries", server.URL, established.CsrfToken, map[string]any{"name": "One"})
	var libraryOne struct {
		ID string `json:"id"`
	}
	if body := readAndClose(t, libraryOneResponse); json.Unmarshal(body, &libraryOne) != nil || libraryOneResponse.StatusCode != http.StatusCreated {
		t.Fatalf("创建 Library One 失败: status=%d body=%s", libraryOneResponse.StatusCode, body)
	}
	libraryTwoResponse := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/libraries", server.URL, established.CsrfToken, map[string]any{"name": "Two"})
	var libraryTwo struct {
		ID string `json:"id"`
	}
	if body := readAndClose(t, libraryTwoResponse); json.Unmarshal(body, &libraryTwo) != nil || libraryTwoResponse.StatusCode != http.StatusCreated {
		t.Fatalf("创建 Library Two 失败: status=%d body=%s", libraryTwoResponse.StatusCode, body)
	}
	createSource := func(libraryID, name string) string {
		root := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/sources", server.URL, established.CsrfToken,
			map[string]any{"libraryId": libraryID, "displayName": name, "rootPath": root})
		var source struct {
			ID string `json:"id"`
		}
		if body := readAndClose(t, response); json.Unmarshal(body, &source) != nil || response.StatusCode != http.StatusCreated {
			t.Fatalf("创建 Source %s 失败: status=%d body=%s", name, response.StatusCode, body)
		}
		return source.ID
	}
	sourceOneID := createSource(libraryOne.ID, "source-one")
	sourceTwoID := createSource(libraryTwo.ID, "source-two")
	createViewer := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/admin/users", server.URL, established.CsrfToken,
		map[string]any{"username": "viewer", "displayName": "Viewer", "password": "viewer-password-strong", "roles": []string{"viewer"}, "grants": []any{
			map[string]any{"effect": "allow", "capability": "library.read", "scope": map[string]any{"kind": "library", "id": libraryOne.ID}},
			map[string]any{"effect": "allow", "capability": "bindings.read", "scope": map[string]any{"kind": "library", "id": libraryOne.ID}},
			map[string]any{"effect": "allow", "capability": "tokens.manage", "scope": map[string]any{"kind": "global"}},
		}})
	if createViewer.StatusCode != http.StatusCreated {
		t.Fatalf("创建 Viewer status=%d body=%s", createViewer.StatusCode, readAndClose(t, createViewer))
	}
	_ = createViewer.Body.Close()
	viewerJar, _ := cookiejar.New(nil)
	viewerClient := &http.Client{Jar: viewerJar}
	viewerCSRF := bootstrapCSRF(t, viewerClient, server.URL)
	viewerLogin := requestJSON(t, viewerClient, http.MethodPost, server.URL+"/api/v1/auth/login", server.URL, viewerCSRF,
		map[string]any{"username": "viewer", "password": "viewer-password-strong"})
	var viewerEstablished struct {
		CsrfToken string `json:"csrfToken"`
	}
	if body := readAndClose(t, viewerLogin); json.Unmarshal(body, &viewerEstablished) != nil || viewerLogin.StatusCode != http.StatusCreated {
		t.Fatalf("Viewer 登录失败: status=%d body=%s", viewerLogin.StatusCode, body)
	}
	visible := requestJSON(t, viewerClient, http.MethodGet, server.URL+"/api/v1/libraries/"+libraryOne.ID, "", "", nil)
	if visible.StatusCode != http.StatusOK {
		t.Fatalf("Viewer 无法读取授权 Library: %d", visible.StatusCode)
	}
	_ = visible.Body.Close()
	hidden := requestJSON(t, viewerClient, http.MethodGet, server.URL+"/api/v1/libraries/"+libraryTwo.ID, "", "", nil)
	if hidden.StatusCode != http.StatusNotFound {
		t.Fatalf("越权 Library 未按 NOT_FOUND 隐藏: %d", hidden.StatusCode)
	}
	_ = hidden.Body.Close()
	for _, check := range []struct {
		path string
		want int
	}{
		{"/api/v1/sources/" + sourceOneID, http.StatusOK},
		{"/api/v1/sources/" + sourceTwoID, http.StatusNotFound},
		{"/api/v1/sources/" + sourceOneID + "/scan-status", http.StatusOK},
		{"/api/v1/sources/" + sourceTwoID + "/scan-status", http.StatusNotFound},
		{"/api/v1/binding-issues?sourceId=" + sourceOneID, http.StatusOK},
		{"/api/v1/binding-issues?sourceId=" + sourceTwoID, http.StatusNotFound},
		{"/api/v1/binding-issues", http.StatusNotFound},
	} {
		got := requestJSON(t, viewerClient, http.MethodGet, server.URL+check.path, "", "", nil)
		if got.StatusCode != check.want {
			t.Fatalf("资源矩阵 %s status=%d want=%d body=%s", check.path, got.StatusCode, check.want, readAndClose(t, got))
		}
		_ = got.Body.Close()
	}
	fixed := clock.Fixed{Time: time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)}
	jobStore, err := jobs.NewStore(store.Control.SQL(), fixed, identity.NewGenerator(fixed))
	if err != nil {
		t.Fatal(err)
	}
	visibleJob, err := jobStore.CreateScan(context.Background(), sourceOneID, "personal-owner", "")
	if err != nil {
		t.Fatal(err)
	}
	hiddenJob, err := jobStore.CreateScan(context.Background(), sourceTwoID, "personal-owner", "")
	if err != nil {
		t.Fatal(err)
	}
	jobsResponse := requestJSON(t, viewerClient, http.MethodGet, server.URL+"/api/v1/jobs", "", "", nil)
	jobsBody := readAndClose(t, jobsResponse)
	if jobsResponse.StatusCode != http.StatusOK || !bytes.Contains(jobsBody, []byte(visibleJob.ID)) || bytes.Contains(jobsBody, []byte(hiddenJob.ID)) {
		t.Fatalf("Job 列表未按 Source 授权过滤: status=%d body=%s", jobsResponse.StatusCode, jobsBody)
	}
	hiddenJobResponse := requestJSON(t, viewerClient, http.MethodGet, server.URL+"/api/v1/jobs/"+hiddenJob.ID, "", "", nil)
	if hiddenJobResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("越权 Job 未隐藏为 404: %d body=%s", hiddenJobResponse.StatusCode, readAndClose(t, hiddenJobResponse))
	}
	_ = hiddenJobResponse.Body.Close()
	viewerToken := requestJSON(t, viewerClient, http.MethodPost, server.URL+"/api/v1/api-tokens", server.URL, viewerEstablished.CsrfToken,
		map[string]any{"name": "scoped", "capabilities": []string{"library.read"}, "scopes": []map[string]string{{"kind": "library", "id": libraryOne.ID}}})
	var viewerTokenValue struct {
		Secret string `json:"secret"`
	}
	if body := readAndClose(t, viewerToken); json.Unmarshal(body, &viewerTokenValue) != nil || viewerToken.StatusCode != http.StatusCreated {
		t.Fatalf("Viewer Token 创建失败: status=%d body=%s", viewerToken.StatusCode, body)
	}
	for _, item := range []struct {
		id   string
		want int
	}{{libraryOne.ID, http.StatusOK}, {libraryTwo.ID, http.StatusNotFound}} {
		request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/libraries/"+item.id, nil)
		request.Header.Set("Authorization", "Bearer "+viewerTokenValue.Secret)
		got, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if got.StatusCode != item.want {
			t.Fatalf("scoped Token Library %s status=%d want=%d", item.id, got.StatusCode, item.want)
		}
		_ = got.Body.Close()
	}

	noJSON, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/api-tokens", strings.NewReader(`{}`))
	noJSON.Header.Set("Origin", server.URL)
	noJSON.Header.Set("Sec-Fetch-Site", "same-origin")
	noJSON.Header.Set("X-Gallery-CSRF", established.CsrfToken)
	noJSONResponse, err := client.Do(noJSON)
	if err != nil {
		t.Fatal(err)
	}
	if noJSONResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("非 JSON Content-Type 未拒绝: %d", noJSONResponse.StatusCode)
	}
	_ = noJSONResponse.Body.Close()

	tokenResponse := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/api-tokens", server.URL, established.CsrfToken,
		map[string]any{"name": "automation", "capabilities": []string{"library.read"}, "scopes": []map[string]string{{"kind": "global"}}})
	tokenBody := readAndClose(t, tokenResponse)
	if tokenResponse.StatusCode != http.StatusCreated {
		t.Fatalf("Token 创建 status=%d body=%s", tokenResponse.StatusCode, tokenBody)
	}
	var token struct{ ID, Secret string }
	if err := json.Unmarshal(tokenBody, &token); err != nil || token.ID == "" || token.Secret == "" {
		t.Fatalf("Token 创建响应无一次性 secret: %v body=%s", err, tokenBody)
	}
	list := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/api-tokens", "", "", nil)
	listBody := readAndClose(t, list)
	if list.StatusCode != http.StatusOK || bytes.Contains(listBody, []byte(token.Secret)) || bytes.Contains(listBody, []byte(`"secret"`)) {
		t.Fatalf("Token 列表泄露 secret: status=%d body=%s", list.StatusCode, listBody)
	}
	var stored string
	if err := store.Control.SQL().QueryRow("SELECT secret_hash FROM api_tokens WHERE token_id=?", token.ID).Scan(&stored); err != nil || stored == token.Secret || len(stored) != 64 {
		t.Fatalf("Token 数据库存储不安全: len=%d err=%v", len(stored), err)
	}

	bearer, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/bootstrap", nil)
	bearer.Header.Set("Authorization", "Bearer "+token.Secret)
	bearerResponse, err := http.DefaultClient.Do(bearer)
	if err != nil || bearerResponse.StatusCode != http.StatusOK || !bytes.Contains(readAndClose(t, bearerResponse), []byte(`"authenticated":true`)) {
		t.Fatalf("Bearer Token 未认证: status=%d err=%v", bearerResponse.StatusCode, err)
	}
	revoke := requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/api-tokens/"+token.ID, server.URL, established.CsrfToken, nil)
	if revoke.StatusCode != http.StatusNoContent {
		t.Fatalf("Token 吊销 status=%d", revoke.StatusCode)
	}
	_ = revoke.Body.Close()
	bearer, _ = http.NewRequest(http.MethodGet, server.URL+"/api/v1/bootstrap", nil)
	bearer.Header.Set("Authorization", "Bearer "+token.Secret)
	bearerResponse, err = http.DefaultClient.Do(bearer)
	if err != nil {
		t.Fatal(err)
	}
	// bootstrap 是匿名可读端点；吊销后的凭据不得被视为已认证。
	if !bytes.Contains(readAndClose(t, bearerResponse), []byte(`"authenticated":false`)) {
		t.Fatal("已吊销 Token 仍被 bootstrap 视为已认证")
	}
}

func TestLANSecurityRejectsCrossOriginHostAndSetsSecureCookieOnTLS(t *testing.T) {
	server, _ := newLANSecurityServer(t, true)
	client := server.Client()
	jar, _ := cookiejar.New(nil)
	client.Jar = jar
	csrf := bootstrapCSRF(t, client, server.URL)
	owner := map[string]any{"username": "owner", "displayName": "Owner", "password": "owner-password-strong"}
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/lan/owner", server.URL, csrf, owner)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("TLS Owner 初始化 status=%d", response.StatusCode)
	}
	_ = response.Body.Close()
	login := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/login", server.URL, csrf,
		map[string]any{"username": "owner", "password": "owner-password-strong"})
	if !strings.Contains(login.Header.Get("Set-Cookie"), "Secure") {
		t.Fatalf("TLS Cookie 缺少 Secure: %s", login.Header.Get("Set-Cookie"))
	}
	_ = login.Body.Close()

	cross := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/login", "https://evil.invalid", csrf,
		map[string]any{"username": "owner", "password": "owner-password-strong"})
	if cross.StatusCode != http.StatusForbidden {
		t.Fatalf("跨站登录未拒绝: %d", cross.StatusCode)
	}
	_ = cross.Body.Close()
	hostRequest, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/bootstrap", nil)
	hostRequest.Host = "evil.invalid"
	hostResponse, err := client.Do(hostRequest)
	if err != nil {
		t.Fatal(err)
	}
	if hostResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("恶意 Host 未拒绝: %d", hostResponse.StatusCode)
	}
	_ = hostResponse.Body.Close()
}

func newLANSecurityServer(t *testing.T, tls bool) (*httptest.Server, *storage.Store) {
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
	fixed := clock.Fixed{Time: time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)}
	manager, err := auth.NewPersonal(store.Control.SQL(), fixed, identity.NewGenerator(fixed), nil)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, fixed, identity.NewGenerator(fixed))
	if err != nil {
		t.Fatal(err)
	}
	jobStore, err := jobs.NewStore(store.Control.SQL(), fixed, identity.NewGenerator(fixed))
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.New(config.ModeLAN, store, fixed, manager, resources, jobStore, nil, nil, nil, nil, nil,
		realtime.NewHub(fixed), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	var server *httptest.Server
	if tls {
		server = httptest.NewTLSServer(handler)
	} else {
		server = httptest.NewServer(handler)
	}
	t.Cleanup(server.Close)
	return server, store
}

func requestJSON(t *testing.T, client *http.Client, method, endpoint, origin, csrf string, body any) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, endpoint, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
		request.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if csrf != "" {
		request.Header.Set("X-Gallery-CSRF", csrf)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func bootstrapCSRF(t *testing.T, client *http.Client, endpoint string) string {
	t.Helper()
	response := requestJSON(t, client, http.MethodGet, endpoint+"/api/v1/bootstrap", "", "", nil)
	body := readAndClose(t, response)
	var value struct {
		CsrfToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(body, &value); err != nil || value.CsrfToken == "" {
		t.Fatalf("bootstrap 无 CSRF: %v body=%s", err, body)
	}
	return value.CsrfToken
}

func readAndClose(t *testing.T, response *http.Response) []byte {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return body
}
