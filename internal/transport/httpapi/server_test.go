package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/auth"
	"github.com/RecRivenVI/gallery/internal/config"
	"github.com/RecRivenVI/gallery/internal/contract/api"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/storage"
	"github.com/RecRivenVI/gallery/internal/transport/httpapi"
)

func TestGeneratedClientHealthBootstrapAndAnonymousWS(t *testing.T) {
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	fixedClock := clock.Fixed{Time: time.Now().UTC()}
	personal, err := auth.NewPersonal(store.Control.SQL(), fixedClock, identity.NewGenerator(fixedClock), nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.New(config.ModePersonal, store, fixedClock, personal, logger))
	defer server.Close()

	client, err := api.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	health, err := client.GetHealthWithResponse(context.Background())
	if err != nil || health.JSON200 == nil || health.JSON200.Status != api.Ok {
		t.Fatalf("health 响应无效: %v status=%d", err, health.StatusCode())
	}
	bootstrap, err := client.GetBootstrapWithResponse(context.Background())
	if err != nil || bootstrap.JSON200 == nil {
		t.Fatalf("bootstrap 响应无效: %v", err)
	}
	if bootstrap.JSON200.Authenticated || len(bootstrap.JSON200.EffectiveCapabilities) != 0 {
		t.Fatal("loopback 匿名主体获得了 capability")
	}

	response, err := http.Get(server.URL + "/ws/v1")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("匿名 WS status = %d", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := fault.NewErrorValidator()
	if err != nil {
		t.Fatal(err)
	}
	if err := validator.ValidateJSON(body); err != nil {
		t.Fatalf("WS 错误不符合正式信封: %v", err)
	}
	var envelope api.ErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Error.Code != api.UNAUTHENTICATED {
		t.Fatalf("WS 错误 DTO 无效: %v", err)
	}
}

func TestPersonalPairingIsSingleUseAndRevocationInvalidatesREST(t *testing.T) {
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	fixedClock := clock.Fixed{Time: time.Now().UTC()}
	personal, err := auth.NewPersonal(store.Control.SQL(), fixedClock, identity.NewGenerator(fixedClock), nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.New(
		config.ModePersonal, store, fixedClock, personal,
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
	))
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{Jar: jar}
	client, err := api.NewClientWithResponses(server.URL, api.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := client.GetBootstrapWithResponse(context.Background())
	if err != nil || bootstrap.JSON200 == nil {
		t.Fatalf("bootstrap 失败: %v", err)
	}
	if len(bootstrap.JSON200.AvailableCapabilities) == 0 || len(bootstrap.JSON200.EffectiveCapabilities) != 0 {
		t.Fatal("匿名 bootstrap 未区分 available/effective capability")
	}
	requestEditor := sameOrigin(server.URL)
	attempt, err := client.CreatePairingAttemptWithResponse(context.Background(), &api.CreatePairingAttemptParams{
		XGalleryCSRF: bootstrap.JSON200.CsrfToken,
	}, requestEditor)
	if err != nil || attempt.JSON201 == nil {
		t.Fatalf("创建配对 attempt 失败: %v status=%d", err, attempt.StatusCode())
	}
	exchangeBody := api.PairingExchangeRequest{Credential: attempt.JSON201.Credential}
	exchange, err := client.ExchangePairingCredentialWithResponse(context.Background(), &api.ExchangePairingCredentialParams{
		XGalleryCSRF: bootstrap.JSON200.CsrfToken,
	}, exchangeBody, requestEditor)
	if err != nil || exchange.JSON201 == nil {
		t.Fatalf("配对交换失败: %v status=%d", err, exchange.StatusCode())
	}
	second, err := client.ExchangePairingCredentialWithResponse(context.Background(), &api.ExchangePairingCredentialParams{
		XGalleryCSRF: bootstrap.JSON200.CsrfToken,
	}, exchangeBody, requestEditor)
	if err != nil || second.JSON401 == nil || second.JSON401.Error.Code != api.PAIRINGINVALID {
		t.Fatalf("配对凭据被重复消费: %v status=%d", err, second.StatusCode())
	}
	sessions, err := client.ListSessionsWithResponse(context.Background())
	if err != nil || sessions.JSON200 == nil || len(sessions.JSON200.Sessions) != 1 {
		t.Fatalf("Session 列表失败: %v status=%d", err, sessions.StatusCode())
	}
	revoke, err := client.RevokeSessionWithResponse(context.Background(), exchange.JSON201.Session.Id, &api.RevokeSessionParams{
		XGalleryCSRF: exchange.JSON201.CsrfToken,
	}, requestEditor)
	if err != nil || revoke.StatusCode() != http.StatusNoContent {
		t.Fatalf("吊销 Session 失败: %v status=%d", err, revoke.StatusCode())
	}
	afterRevoke, err := client.ListSessionsWithResponse(context.Background())
	if err != nil || afterRevoke.JSON401 == nil {
		t.Fatalf("已吊销 Session 仍可访问 REST: %v status=%d", err, afterRevoke.StatusCode())
	}
}

func sameOrigin(origin string) api.RequestEditorFn {
	return func(_ context.Context, request *http.Request) error {
		request.Header.Set("Origin", origin)
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		return nil
	}
}
