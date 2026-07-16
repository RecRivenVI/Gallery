package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/ports"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const ProtocolVersion = 1

type EventType string

const (
	EventConnectionReady          EventType = "connection.ready"
	EventJobStatus                EventType = "job.status"
	EventJobIssue                 EventType = "job.issue"
	EventCatalogPublication       EventType = "catalog.publication"
	EventOverlayPublication       EventType = "overlay.publication"
	EventOverlayPublicationFailed EventType = "overlay.publication_failed"
	EventSessionRevoked           EventType = "session.revoked"
	EventGrantRevoked             EventType = "grant.revoked"
	EventServiceLifecycle         EventType = "service.lifecycle"
)

var allowedEvents = map[EventType]struct{}{
	EventConnectionReady: {}, EventJobStatus: {}, EventJobIssue: {}, EventCatalogPublication: {},
	EventOverlayPublication: {}, EventOverlayPublicationFailed: {}, EventSessionRevoked: {},
	EventGrantRevoked: {}, EventServiceLifecycle: {},
}

type Scope struct {
	LibraryID string `json:"libraryId,omitempty"`
	SourceID  string `json:"sourceId,omitempty"`
	JobID     string `json:"jobId,omitempty"`
}

type Envelope struct {
	ProtocolVersion int             `json:"protocolVersion"`
	EventType       EventType       `json:"eventType"`
	Sequence        uint64          `json:"sequence"`
	Scope           Scope           `json:"scope"`
	Payload         json.RawMessage `json:"payload"`
	ServerTime      time.Time       `json:"serverTime"`
}

func (e Envelope) Validate() error {
	if e.ProtocolVersion != ProtocolVersion || e.Sequence == 0 || e.ServerTime.IsZero() {
		return fmt.Errorf("WebSocket envelope 基础字段无效")
	}
	if _, ok := allowedEvents[e.EventType]; !ok {
		return fmt.Errorf("未知 WebSocket event type")
	}
	if !json.Valid(e.Payload) {
		return fmt.Errorf("WebSocket payload 不是有效 JSON")
	}
	return nil
}

type Authorize func(*http.Request) bool

// NewHandler 只有认证通过后才升级连接；HTTP snapshot 仍是恢复事实源。
func NewHandler(clock ports.Clock, authorize Authorize) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authorize == nil || !authorize(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
				"code": fault.CodeUnauthenticated, "retryable": false, "correlationId": "ws-handshake",
			}})
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		payload, _ := json.Marshal(map[string]any{"snapshotRequired": true})
		envelope := Envelope{
			ProtocolVersion: ProtocolVersion, EventType: EventConnectionReady, Sequence: 1,
			Scope: Scope{}, Payload: payload, ServerTime: clock.Now().UTC(),
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := wsjson.Write(ctx, conn, envelope); err != nil {
			return
		}
		_ = conn.Close(websocket.StatusNormalClosure, "ready")
	})
}
