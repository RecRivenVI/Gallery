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

func TestLANInitializationAndSessionAuthentication(t *testing.T) {
	server, _ := newLANSecurityServer(t, false)
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
	_ = response.Body.Close()

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
	logout := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/logout", server.URL, established.CsrfToken, nil)
	if logout.StatusCode != http.StatusNoContent {
		t.Fatalf("登出 status=%d body=%s", logout.StatusCode, readAndClose(t, logout))
	}
	_ = logout.Body.Close()
	after := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/bootstrap", "", "", nil)
	if body := readAndClose(t, after); !bytes.Contains(body, []byte(`"authenticated":false`)) {
		t.Fatalf("登出后仍被视为已认证: %s", body)
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

func TestAccountAndGrantManagement(t *testing.T) {
	server, _ := newLANSecurityServer(t, false)
	client, csrf := establishLANOwner(t, server)
	libraryID := createLibrary(t, client, server, csrf, "Accounts")

	create := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/admin/users", server.URL, csrf,
		map[string]any{"username": "viewer", "displayName": "Viewer", "password": "viewer-password-strong", "roles": []string{"viewer"}, "grants": []any{
			map[string]any{"effect": "allow", "capability": "library.read", "scope": map[string]any{"kind": "library", "id": libraryID}},
		}})
	createBody := readAndClose(t, create)
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("创建账户 status=%d body=%s", create.StatusCode, createBody)
	}
	if bytes.Contains(createBody, []byte("viewer-password-strong")) || bytes.Contains(createBody, []byte(`"password"`)) {
		t.Fatalf("账户响应泄露口令: %s", createBody)
	}
	var created struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(createBody, &created) != nil || created.ID == "" {
		t.Fatalf("账户响应缺少 id: %s", createBody)
	}

	grant := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/admin/users/"+created.ID+"/grants", server.URL, csrf,
		map[string]any{"effect": "allow", "capability": "bindings.read", "scope": map[string]any{"kind": "library", "id": libraryID}})
	grantBody := readAndClose(t, grant)
	if grant.StatusCode != http.StatusCreated {
		t.Fatalf("授予 Grant status=%d body=%s", grant.StatusCode, grantBody)
	}
	var grantValue struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(grantBody, &grantValue) != nil || grantValue.ID == "" {
		t.Fatalf("Grant 响应缺少 id: %s", grantBody)
	}
	revokeGrant := requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/admin/grants/"+grantValue.ID, server.URL, csrf, nil)
	if revokeGrant.StatusCode != http.StatusNoContent {
		t.Fatalf("撤销 Grant status=%d body=%s", revokeGrant.StatusCode, readAndClose(t, revokeGrant))
	}
	_ = revokeGrant.Body.Close()

	disable := requestJSON(t, client, http.MethodPatch, server.URL+"/api/v1/admin/users/"+created.ID+"/status", server.URL, csrf,
		map[string]any{"status": "disabled"})
	if disable.StatusCode != http.StatusNoContent {
		t.Fatalf("禁用账户 status=%d body=%s", disable.StatusCode, readAndClose(t, disable))
	}
	_ = disable.Body.Close()
	viewerJar, _ := cookiejar.New(nil)
	viewerClient := &http.Client{Jar: viewerJar}
	viewerCSRF := bootstrapCSRF(t, viewerClient, server.URL)
	disabledLogin := requestJSON(t, viewerClient, http.MethodPost, server.URL+"/api/v1/auth/login", server.URL, viewerCSRF,
		map[string]any{"username": "viewer", "password": "viewer-password-strong"})
	if disabledLogin.StatusCode == http.StatusCreated {
		t.Fatalf("禁用账户仍可登录: %d", disabledLogin.StatusCode)
	}
	_ = disabledLogin.Body.Close()

	// 改密放在最后：修改自身口令会吊销当前会话，因此改密后必须用新凭据重新认证。
	changePassword := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/account/password", server.URL, csrf,
		map[string]any{"currentPassword": "owner-password-strong", "newPassword": "owner-password-rotated"})
	if changePassword.StatusCode != http.StatusNoContent {
		t.Fatalf("改密 status=%d body=%s", changePassword.StatusCode, readAndClose(t, changePassword))
	}
	_ = changePassword.Body.Close()
	staleJar, _ := cookiejar.New(nil)
	staleClient := &http.Client{Jar: staleJar}
	staleCSRF := bootstrapCSRF(t, staleClient, server.URL)
	oldLogin := requestJSON(t, staleClient, http.MethodPost, server.URL+"/api/v1/auth/login", server.URL, staleCSRF,
		map[string]any{"username": "owner", "password": "owner-password-strong"})
	if oldLogin.StatusCode == http.StatusCreated {
		t.Fatalf("旧口令改密后仍可登录: %d", oldLogin.StatusCode)
	}
	_ = oldLogin.Body.Close()
	newLogin := requestJSON(t, staleClient, http.MethodPost, server.URL+"/api/v1/auth/login", server.URL, staleCSRF,
		map[string]any{"username": "owner", "password": "owner-password-rotated"})
	if newLogin.StatusCode != http.StatusCreated {
		t.Fatalf("新口令登录失败: status=%d body=%s", newLogin.StatusCode, readAndClose(t, newLogin))
	}
	_ = newLogin.Body.Close()
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

func establishLANOwner(t *testing.T, server *httptest.Server) (*http.Client, string) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := bootstrapCSRF(t, client, server.URL)
	owner := map[string]any{"username": "owner", "displayName": "Owner", "password": "owner-password-strong"}
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/lan/owner", server.URL, csrf, owner)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("Owner 初始化失败: status=%d body=%s", response.StatusCode, readAndClose(t, response))
	}
	_ = response.Body.Close()
	login := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/login", server.URL, csrf,
		map[string]any{"username": "owner", "password": "owner-password-strong"})
	var established struct {
		CsrfToken string `json:"csrfToken"`
	}
	if body := readAndClose(t, login); json.Unmarshal(body, &established) != nil || login.StatusCode != http.StatusCreated || established.CsrfToken == "" {
		t.Fatalf("Owner 登录失败: status=%d", login.StatusCode)
	}
	return client, established.CsrfToken
}

func createLibrary(t *testing.T, client *http.Client, server *httptest.Server, csrf, name string) string {
	t.Helper()
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/libraries", server.URL, csrf, map[string]any{"name": name})
	var library struct {
		ID string `json:"id"`
	}
	if body := readAndClose(t, response); json.Unmarshal(body, &library) != nil || response.StatusCode != http.StatusCreated || library.ID == "" {
		t.Fatalf("创建 Library %s 失败: status=%d body=%s", name, response.StatusCode, body)
	}
	return library.ID
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
