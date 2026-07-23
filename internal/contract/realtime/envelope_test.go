package realtime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/realtime"
	"github.com/RecRivenVI/gallery/internal/jobs"
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

func TestWebSocketBroadcastRechecksResourceScope(t *testing.T) {
	hub := realtime.NewHub(clock.Fixed{Time: time.Now().UTC()})
	server := httptest.NewServer(hub.Handler(func(r *http.Request) (realtime.Principal, error) {
		allowedSource := r.URL.Query().Get("source")
		return realtime.Principal{
			SessionID: "credential-" + allowedSource, PrincipalID: "principal-" + allowedSource,
			Capabilities: []string{"library.read"},
			Authorize: func(_ context.Context, _ string, scope realtime.Scope) bool {
				return allowedSource == "global" || scope.SourceID == allowedSource
			},
		}, nil
	}, func(context.Context, string) bool { return true }))
	defer server.Close()
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	dial := func(source string) *websocket.Conn {
		conn, _, err := websocket.Dial(context.Background(), endpoint+"?source="+source, nil)
		if err != nil {
			t.Fatal(err)
		}
		var ready realtime.Envelope
		if err := wsjson.Read(context.Background(), conn, &ready); err != nil {
			t.Fatal(err)
		}
		return conn
	}
	global, scoped := dial("global"), dial("src-a")
	defer global.CloseNow()
	defer scoped.CloseNow()

	hub.JobChanged(jobs.Job{ID: "job-b", SourceID: "src-b", Status: jobs.StatusQueued})
	var event realtime.Envelope
	if err := wsjson.Read(context.Background(), global, &event); err != nil || event.Scope.SourceID != "src-b" {
		t.Fatalf("global Principal 未收到 Source B 事件: %+v err=%v", event, err)
	}
	readCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := wsjson.Read(readCtx, scoped, &event); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("受限 Principal 收到越权 Source B 事件: %+v err=%v", event, err)
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

func TestSessionAndGrantRevocationCloseExistingWebSockets(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		revoke    func(*realtime.Hub)
		eventType realtime.EventType
		closeCode websocket.StatusCode
	}{
		{name: "session-or-token", revoke: func(h *realtime.Hub) { h.RevokeSession("credential-1") }, eventType: realtime.EventSessionRevoked, closeCode: 4401},
		{name: "principal-grant", revoke: func(h *realtime.Hub) { h.RevokePrincipal("principal-1") }, eventType: realtime.EventGrantRevoked, closeCode: 4403},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			hub := realtime.NewHub(clock.Fixed{Time: time.Now().UTC()})
			server := httptest.NewServer(hub.Handler(func(*http.Request) (realtime.Principal, error) {
				return realtime.Principal{SessionID: "credential-1", PrincipalID: "principal-1", Capabilities: []string{"library.read"}}, nil
			}, func(context.Context, string) bool { return true }))
			defer server.Close()
			conn, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()
			var ready realtime.Envelope
			if err := wsjson.Read(context.Background(), conn, &ready); err != nil || ready.EventType != realtime.EventConnectionReady {
				t.Fatalf("WS ready 错误: %+v err=%v", ready, err)
			}
			scenario.revoke(hub)
			var revoked realtime.Envelope
			if err := wsjson.Read(context.Background(), conn, &revoked); err != nil || revoked.EventType != scenario.eventType {
				t.Fatalf("吊销事件错误: %+v err=%v", revoked, err)
			}
			if _, _, err := conn.Read(context.Background()); websocket.CloseStatus(err) != scenario.closeCode {
				t.Fatalf("吊销关闭码=%d err=%v", websocket.CloseStatus(err), err)
			}
		})
	}
}

func TestWebSocketConnectionAndInboundFrameLimits(t *testing.T) {
	hub := realtime.NewHub(clock.Fixed{Time: time.Now().UTC()})
	server := httptest.NewServer(hub.Handler(func(*http.Request) (realtime.Principal, error) {
		return realtime.Principal{SessionID: "credential-1", PrincipalID: "principal-1", Capabilities: []string{"library.read"}}, nil
	}, func(context.Context, string) bool { return true }))
	defer server.Close()
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")

	connections := make([]*websocket.Conn, 0, realtime.MaxConnectionsPerPrincipal)
	defer func() {
		for _, conn := range connections {
			conn.CloseNow()
		}
	}()
	for range realtime.MaxConnectionsPerPrincipal {
		conn, _, err := websocket.Dial(context.Background(), endpoint, nil)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, conn)
		var ready realtime.Envelope
		if err := wsjson.Read(context.Background(), conn, &ready); err != nil {
			t.Fatal(err)
		}
	}
	if conn, response, err := websocket.Dial(context.Background(), endpoint, nil); err == nil {
		conn.CloseNow()
		t.Fatal("同一 Principal 超额 WebSocket 连接未拒绝")
	} else if response == nil || response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("超额连接 status=%v err=%v", response, err)
	}

	connections[0].CloseNow()
	connections = connections[1:]
	deadline := time.Now().Add(2 * time.Second)
	var frameConn *websocket.Conn
	for time.Now().Before(deadline) {
		conn, response, err := websocket.Dial(context.Background(), endpoint, nil)
		if err == nil {
			frameConn = conn
			break
		}
		if response == nil || response.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("释放连接后重连 status=%v err=%v", response, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if frameConn == nil {
		t.Fatal("释放连接后未恢复容量")
	}
	connections = append(connections, frameConn)
	var ready realtime.Envelope
	if err := wsjson.Read(context.Background(), frameConn, &ready); err != nil {
		t.Fatal(err)
	}
	for range realtime.MaxInboundFramesPerMinute + 1 {
		if err := frameConn.Write(context.Background(), websocket.MessageText, []byte(`{}`)); err != nil {
			break
		}
	}
	if _, _, err := frameConn.Read(context.Background()); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("入站帧速率关闭码=%d err=%v", websocket.CloseStatus(err), err)
	}
}

func TestWebSocketRejectsOversizedInboundMessage(t *testing.T) {
	hub := realtime.NewHub(clock.Fixed{Time: time.Now().UTC()})
	server := httptest.NewServer(hub.Handler(func(*http.Request) (realtime.Principal, error) {
		return realtime.Principal{SessionID: "credential-size", PrincipalID: "principal-size", Capabilities: []string{"library.read"}}, nil
	}, func(context.Context, string) bool { return true }))
	defer server.Close()
	conn, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	var ready realtime.Envelope
	if err := wsjson.Read(context.Background(), conn, &ready); err != nil {
		t.Fatal(err)
	}
	_ = conn.Write(context.Background(), websocket.MessageBinary, bytes.Repeat([]byte{'x'}, realtime.MaxInboundMessageBytes+1))
	if _, _, err := conn.Read(context.Background()); websocket.CloseStatus(err) != websocket.StatusMessageTooBig {
		t.Fatalf("超大入站消息关闭码=%d err=%v", websocket.CloseStatus(err), err)
	}
}
