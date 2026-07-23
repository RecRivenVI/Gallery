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
