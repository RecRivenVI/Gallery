package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/config"
	"github.com/RecRivenVI/gallery/internal/contract/api"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
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
	server := httptest.NewServer(httpapi.New(config.ModePersonal, store, clock.Fixed{Time: time.Now()}, logger))
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
