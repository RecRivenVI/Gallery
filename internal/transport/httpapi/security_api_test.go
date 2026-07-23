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

func TestShareManagementLifecycle(t *testing.T) {
	server, _ := newLANSecurityServer(t, false)
	client, csrf := establishLANOwner(t, server)
	libraryID := createLibrary(t, client, server, csrf, "Shareable")

	create := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/shares", server.URL, csrf,
		map[string]any{"scopeKind": "library", "scopeId": libraryID, "permissions": []string{"view"}, "expiresAt": time.Now().UTC().Add(time.Hour)})
	createBody := readAndClose(t, create)
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("创建 Share status=%d body=%s", create.StatusCode, createBody)
	}
	var share struct {
		ID, Secret, SecretPrefix string
	}
	if err := json.Unmarshal(createBody, &share); err != nil || share.ID == "" || share.Secret == "" || share.SecretPrefix == "" {
		t.Fatalf("Share 创建响应缺少一次性 secret 或前缀: %v body=%s", err, createBody)
	}

	list := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/shares", "", "", nil)
	listBody := readAndClose(t, list)
	if list.StatusCode != http.StatusOK || !bytes.Contains(listBody, []byte(share.ID)) ||
		bytes.Contains(listBody, []byte(share.Secret)) || bytes.Contains(listBody, []byte(`"secret"`)) {
		t.Fatalf("Share 列表缺失或泄露 secret: status=%d body=%s", list.StatusCode, listBody)
	}

	badKind := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/shares", server.URL, csrf,
		map[string]any{"scopeKind": "bogus", "scopeId": libraryID, "permissions": []string{"view"}, "expiresAt": time.Now().UTC().Add(time.Hour)})
	if badKind.StatusCode != http.StatusBadRequest {
		t.Fatalf("非法 scopeKind 未拒绝: %d body=%s", badKind.StatusCode, readAndClose(t, badKind))
	}
	_ = badKind.Body.Close()
	badFixed := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/shares", server.URL, csrf,
		map[string]any{"scopeKind": "library", "scopeId": libraryID, "permissions": []string{"view"},
			"fixedBlobAlgorithm": "sha256-v1", "fixedBlobDigest": strings.Repeat("0", 64), "expiresAt": time.Now().UTC().Add(time.Hour)})
	if badFixed.StatusCode != http.StatusBadRequest {
		t.Fatalf("Library 范围固定 Blob 未拒绝: %d body=%s", badFixed.StatusCode, readAndClose(t, badFixed))
	}
	_ = badFixed.Body.Close()

	revoke := requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/shares/"+share.ID, server.URL, csrf, nil)
	if revoke.StatusCode != http.StatusNoContent {
		t.Fatalf("撤销 Share status=%d body=%s", revoke.StatusCode, readAndClose(t, revoke))
	}
	_ = revoke.Body.Close()
}

func TestAnonymousShareConsumption(t *testing.T) {
	server, _ := newLANSecurityServer(t, false)
	client, csrf := establishLANOwner(t, server)
	libraryID := createLibrary(t, client, server, csrf, "Anonymous")

	create := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/shares", server.URL, csrf,
		map[string]any{"scopeKind": "library", "scopeId": libraryID, "permissions": []string{"view"}, "expiresAt": time.Now().UTC().Add(time.Hour)})
	var share struct {
		ID, Secret string
	}
	if body := readAndClose(t, create); json.Unmarshal(body, &share) != nil || create.StatusCode != http.StatusCreated {
		t.Fatalf("创建 Share 失败: status=%d body=%s", create.StatusCode, body)
	}

	resolve, err := http.Get(server.URL + "/api/v1/public/shares/" + share.Secret)
	if err != nil {
		t.Fatal(err)
	}
	resolveBody := readAndClose(t, resolve)
	if resolve.StatusCode != http.StatusOK || !bytes.Contains(resolveBody, []byte(`"library"`)) ||
		bytes.Contains(resolveBody, []byte(`"createdBy"`)) || bytes.Contains(resolveBody, []byte(`"secretPrefix"`)) {
		t.Fatalf("匿名解析资源或脱敏错误: status=%d body=%s", resolve.StatusCode, resolveBody)
	}

	missing, err := http.Get(server.URL + "/api/v1/public/shares/shr_00000000-0000-7000-8000-000000000001.absent-secret")
	if err != nil {
		t.Fatal(err)
	}
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("无效凭据未隐藏为 404: %d", missing.StatusCode)
	}
	_ = missing.Body.Close()

	downloadDenied, err := http.Get(server.URL + "/api/v1/public/shares/" + share.Secret + "/media/med_00000000-0000-7000-8000-000000000002/content?download=true")
	if err != nil {
		t.Fatal(err)
	}
	if downloadDenied.StatusCode != http.StatusForbidden {
		t.Fatalf("view 权限 Share 未拒绝 download: %d", downloadDenied.StatusCode)
	}
	_ = downloadDenied.Body.Close()

	revoke := requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/shares/"+share.ID, server.URL, csrf, nil)
	if revoke.StatusCode != http.StatusNoContent {
		t.Fatalf("撤销 Share status=%d", revoke.StatusCode)
	}
	_ = revoke.Body.Close()
	afterRevoke, err := http.Get(server.URL + "/api/v1/public/shares/" + share.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if afterRevoke.StatusCode != http.StatusNotFound {
		t.Fatalf("已吊销 Share 仍可解析: %d", afterRevoke.StatusCode)
	}
	_ = afterRevoke.Body.Close()
}

func TestSecurityAuditAccessControl(t *testing.T) {
	server, _ := newLANSecurityServer(t, false)
	client, csrf := establishLANOwner(t, server)

	audits := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/admin/security-audits", "", "", nil)
	auditsBody := readAndClose(t, audits)
	if audits.StatusCode != http.StatusOK || !bytes.Contains(auditsBody, []byte(`"audits"`)) ||
		bytes.Contains(auditsBody, []byte("owner-password-strong")) {
		t.Fatalf("审计读取或脱敏错误: status=%d body=%s", audits.StatusCode, auditsBody)
	}

	viewerCreate := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/admin/users", server.URL, csrf,
		map[string]any{"username": "auditless", "displayName": "Auditless", "password": "auditless-password-strong", "roles": []string{"viewer"}})
	if viewerCreate.StatusCode != http.StatusCreated {
		t.Fatalf("创建无审计权限账户 status=%d body=%s", viewerCreate.StatusCode, readAndClose(t, viewerCreate))
	}
	_ = viewerCreate.Body.Close()
	viewerJar, _ := cookiejar.New(nil)
	viewerClient := &http.Client{Jar: viewerJar}
	viewerCSRF := bootstrapCSRF(t, viewerClient, server.URL)
	viewerLogin := requestJSON(t, viewerClient, http.MethodPost, server.URL+"/api/v1/auth/login", server.URL, viewerCSRF,
		map[string]any{"username": "auditless", "password": "auditless-password-strong"})
	if viewerLogin.StatusCode != http.StatusCreated {
		t.Fatalf("无审计权限账户登录失败: %d body=%s", viewerLogin.StatusCode, readAndClose(t, viewerLogin))
	}
	_ = viewerLogin.Body.Close()
	denied := requestJSON(t, viewerClient, http.MethodGet, server.URL+"/api/v1/admin/security-audits", "", "", nil)
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("缺少 audit.read 未拒绝: %d body=%s", denied.StatusCode, readAndClose(t, denied))
	}
	_ = denied.Body.Close()
}

func TestAPITokenLifecycleAndBearerAuthentication(t *testing.T) {
	server, store := newLANSecurityServer(t, false)
	client, csrf := establishLANOwner(t, server)

	tokenResponse := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/api-tokens", server.URL, csrf,
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
	revoke := requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/api-tokens/"+token.ID, server.URL, csrf, nil)
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
	if !bytes.Contains(readAndClose(t, bearerResponse), []byte(`"authenticated":false`)) {
		t.Fatal("已吊销 Token 仍被 bootstrap 视为已认证")
	}
}

func TestExistingEndpointsEnforceResourceScopes(t *testing.T) {
	server, store := newLANSecurityServer(t, false)
	client, csrf := establishLANOwner(t, server)

	libraryOneID := createLibrary(t, client, server, csrf, "One")
	libraryTwoID := createLibrary(t, client, server, csrf, "Two")
	createSource := func(libraryID, name string) string {
		root := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/sources", server.URL, csrf,
			map[string]any{"libraryId": libraryID, "displayName": name, "rootPath": root})
		var source struct {
			ID string `json:"id"`
		}
		if body := readAndClose(t, response); json.Unmarshal(body, &source) != nil || response.StatusCode != http.StatusCreated {
			t.Fatalf("创建 Source %s 失败: status=%d body=%s", name, response.StatusCode, body)
		}
		return source.ID
	}
	sourceOneID := createSource(libraryOneID, "source-one")
	sourceTwoID := createSource(libraryTwoID, "source-two")
	createViewer := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/admin/users", server.URL, csrf,
		map[string]any{"username": "viewer", "displayName": "Viewer", "password": "viewer-password-strong", "roles": []string{"viewer"}, "grants": []any{
			map[string]any{"effect": "allow", "capability": "library.read", "scope": map[string]any{"kind": "library", "id": libraryOneID}},
			map[string]any{"effect": "allow", "capability": "bindings.read", "scope": map[string]any{"kind": "library", "id": libraryOneID}},
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
	visible := requestJSON(t, viewerClient, http.MethodGet, server.URL+"/api/v1/libraries/"+libraryOneID, "", "", nil)
	if visible.StatusCode != http.StatusOK {
		t.Fatalf("Viewer 无法读取授权 Library: %d", visible.StatusCode)
	}
	_ = visible.Body.Close()
	hidden := requestJSON(t, viewerClient, http.MethodGet, server.URL+"/api/v1/libraries/"+libraryTwoID, "", "", nil)
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
		map[string]any{"name": "scoped", "capabilities": []string{"library.read"}, "scopes": []map[string]string{{"kind": "library", "id": libraryOneID}}})
	var viewerTokenValue struct {
		Secret string `json:"secret"`
	}
	if body := readAndClose(t, viewerToken); json.Unmarshal(body, &viewerTokenValue) != nil || viewerToken.StatusCode != http.StatusCreated {
		t.Fatalf("Viewer Token 创建失败: status=%d body=%s", viewerToken.StatusCode, body)
	}
	for _, item := range []struct {
		id   string
		want int
	}{{libraryOneID, http.StatusOK}, {libraryTwoID, http.StatusNotFound}} {
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

// TestLANModeRejectsPersonalPairing 锁定「一次性配对只属于 Personal 模式」这条边界。
// createPairingAttempt/exchangePairingCredential 此前没有与 login、initializeLANOwner
// 对称的模式判定，因此 LAN 模式下任何能访问 loopback 的本机进程都可以换取 principal 为
// personal-owner、拥有全部 capability 的 Session，完全绕过 LAN 账户、密码与 Grant 模型。
func TestLANModeRejectsPersonalPairing(t *testing.T) {
	server, _ := newLANSecurityServer(t, false)
	client := &http.Client{}
	csrf := bootstrapCSRF(t, client, server.URL)

	attempt := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/personal/pairing-attempts", server.URL, csrf, nil)
	defer attempt.Body.Close()
	if attempt.StatusCode != http.StatusNotFound {
		t.Fatalf("LAN 模式下创建配对凭据的 status = %d，应为 404", attempt.StatusCode)
	}

	exchange := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/personal/pair", server.URL, csrf,
		map[string]string{"credential": "0123456789abcdef0123456789abcdef0123456789abcdef"})
	defer exchange.Body.Close()
	if exchange.StatusCode != http.StatusNotFound {
		t.Fatalf("LAN 模式下兑换配对凭据的 status = %d，应为 404", exchange.StatusCode)
	}
	if cookies := exchange.Cookies(); len(cookies) != 0 {
		t.Fatalf("LAN 模式下兑换配对凭据签发了 %d 个 Cookie", len(cookies))
	}
}

// TestRuleLifecycleMutationHonoursAllowedHostsAndBearerTokens 锁定规则生命周期端点必须
// 与其余变更端点使用同一套请求校验。这 18 个端点此前直接调用 loopback-only 的
// auth.ValidateMutation，导致真实 LAN 部署下全部返回 HOST_REJECTED，且 Bearer API Token
// 因为没有 Cookie CSRF 而永远得到 CSRF_INVALID。
func TestRuleLifecycleMutationHonoursAllowedHostsAndBearerTokens(t *testing.T) {
	source, err := os.ReadFile("rules_lifecycle_api.go")
	if err != nil {
		t.Fatalf("读取规则生命周期路由源失败: %v", err)
	}
	if count := bytes.Count(source, []byte("auth.ValidateMutation(")); count != 0 {
		t.Fatalf("规则生命周期仍有 %d 处直接调用 loopback-only 的 auth.ValidateMutation", count)
	}
	if !bytes.Contains(source, []byte("s.validateMutation(r, session)")) {
		t.Fatal("规则生命周期未改用读取 allowedHosts 且对 Bearer 豁免 CSRF 的 s.validateMutation")
	}
}

// TestAPIResponsesForbidCaching 锁定 /api/v1 响应不得进入任何 HTTP 缓存。
// 这些响应包含 bootstrap 的 CSRF token、只展示一次的凭据和按授权范围过滤的列表。
func TestAPIResponsesForbidCaching(t *testing.T) {
	server, _ := newLANSecurityServer(t, false)
	response, err := http.Get(server.URL + "/api/v1/bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if got := response.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("/api/v1 响应的 Cache-Control = %q，应为 no-store", got)
	}
	if got := response.Header.Get("Vary"); got != "Cookie, Authorization" {
		t.Fatalf("/api/v1 响应的 Vary = %q", got)
	}
}
