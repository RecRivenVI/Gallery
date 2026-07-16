package realtime_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/realtime"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestEnvelopeSchemaAndAuthenticatedReady(t *testing.T) {
	now := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	server := httptest.NewServer(realtime.NewHandler(clock.Fixed{Time: now}, func(*http.Request) bool { return true }))
	defer server.Close()
	conn, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	var envelope realtime.Envelope
	if err := wsjson.Read(context.Background(), conn, &envelope); err != nil {
		t.Fatal(err)
	}
	if err := envelope.Validate(); err != nil {
		t.Fatal(err)
	}
	serialized, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := realtime.NewEnvelopeValidator()
	if err != nil {
		t.Fatal(err)
	}
	if err := validator.ValidateJSON(serialized); err != nil {
		t.Fatalf("Go envelope 与 JSON Schema 不一致: %v", err)
	}
}

func TestAnonymousWebSocketIsNotAdministrator(t *testing.T) {
	server := httptest.NewServer(realtime.NewHandler(clock.Fixed{Time: time.Now()}, nil))
	defer server.Close()
	response, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("匿名 WS handshake status = %d", response.StatusCode)
	}
}
