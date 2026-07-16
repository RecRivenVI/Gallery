package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/ports"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const ProtocolVersion = 1

type EventType string

const (
	EventConnectionReady           EventType = "connection.ready"
	EventJobStatus                 EventType = "job.status"
	EventJobIssue                  EventType = "job.issue"
	EventCatalogPublication        EventType = "catalog.publication"
	EventOverlayPublication        EventType = "overlay.publication"
	EventOverlayPublicationFailed  EventType = "overlay.publication_failed"
	EventSessionRevoked            EventType = "session.revoked"
	EventGrantRevoked              EventType = "grant.revoked"
	EventServiceLifecycle          EventType = "service.lifecycle"
	EventJobQueued                 EventType = "job.queued"
	EventJobProgress               EventType = "job.progress"
	EventJobCompleted              EventType = "job.completed"
	EventJobFailed                 EventType = "job.failed"
	EventQueryPublicationPublished EventType = "query.publication.published"
)

var allowedEvents = map[EventType]struct{}{
	EventConnectionReady: {}, EventJobStatus: {}, EventJobIssue: {}, EventCatalogPublication: {},
	EventOverlayPublication: {}, EventOverlayPublicationFailed: {}, EventSessionRevoked: {},
	EventGrantRevoked: {}, EventServiceLifecycle: {},
	EventJobQueued: {}, EventJobProgress: {}, EventJobCompleted: {}, EventJobFailed: {},
	EventQueryPublicationPublished: {},
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

type Principal struct {
	SessionID    string
	Capabilities []string
}

type Authenticate func(*http.Request) (Principal, error)
type Active func(context.Context, string) bool

type outbound struct {
	eventType  EventType
	scope      Scope
	payload    json.RawMessage
	closeAfter websocket.StatusCode
}

type subscriber struct {
	principal Principal
	messages  chan outbound
	conn      *websocket.Conn
}

type Hub struct {
	clock       ports.Clock
	mu          sync.Mutex
	subscribers map[string]map[*subscriber]struct{}
}

func NewHub(clock ports.Clock) *Hub {
	return &Hub{clock: clock, subscribers: make(map[string]map[*subscriber]struct{})}
}

// NewHandler 保留阶段 0 探针兼容入口；正式服务使用 Hub.Handler。
func NewHandler(clock ports.Clock, authorize Authorize) http.Handler {
	hub := NewHub(clock)
	return hub.Handler(func(r *http.Request) (Principal, error) {
		if authorize == nil || !authorize(r) {
			return Principal{}, fault.New(fault.CodeUnauthenticated, false, nil)
		}
		return Principal{SessionID: "probe", Capabilities: []string{"library.read"}}, nil
	}, nil)
}

// Handler 只有认证通过后才升级连接；HTTP snapshot 仍是恢复事实源。
func (h *Hub) Handler(authenticate Authenticate, active Active) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authenticate == nil {
			writeHandshakeFault(w, fault.CodeUnauthenticated, http.StatusUnauthorized)
			return
		}
		principal, err := authenticate(r)
		if err != nil || principal.SessionID == "" {
			code, status := fault.CodeUnauthenticated, http.StatusUnauthorized
			var structured *fault.Error
			if errors.As(err, &structured) && (structured.Code == fault.CodeHostRejected || structured.Code == fault.CodeOriginRejected) {
				code, status = structured.Code, http.StatusForbidden
			}
			writeHandshakeFault(w, code, status)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		subscription := &subscriber{principal: principal, messages: make(chan outbound, 64), conn: conn}
		h.add(subscription)
		defer h.remove(subscription)

		payload, _ := json.Marshal(map[string]any{"snapshotRequired": true})
		sequence := uint64(0)
		if err := h.write(r.Context(), conn, &sequence, outbound{eventType: EventConnectionReady, scope: Scope{}, payload: payload}); err != nil {
			return
		}

		readContext, cancelRead := context.WithCancel(r.Context())
		defer cancelRead()
		readDone := make(chan struct{})
		go func() {
			defer close(readDone)
			for {
				if _, _, err := conn.Read(readContext); err != nil {
					return
				}
			}
		}()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-readDone:
				return
			case message := <-subscription.messages:
				if message.closeAfter == 0 && active != nil && !active(r.Context(), principal.SessionID) {
					_ = conn.Close(websocket.StatusCode(4401), "session revoked")
					return
				}
				if err := h.write(r.Context(), conn, &sequence, message); err != nil {
					return
				}
				if message.closeAfter != 0 {
					_ = conn.Close(message.closeAfter, "session revoked")
					return
				}
			}
		}
	})
}

func (h *Hub) JobChanged(job jobs.Job) {
	eventType := EventJobProgress
	switch job.Status {
	case jobs.StatusQueued:
		eventType = EventJobQueued
	case jobs.StatusCompleted:
		eventType = EventJobCompleted
	case jobs.StatusFailed, jobs.StatusCancelled, jobs.StatusNeedsRepair:
		eventType = EventJobFailed
	}
	payload, _ := json.Marshal(map[string]any{
		"jobId": job.ID, "status": job.Status, "stage": job.Stage,
		"progressSequence": job.ProgressSequence, "current": job.ProgressCurrent, "total": job.ProgressTotal,
		"issueCode": nullable(job.IssueCode), "queryPublicationId": nullable(job.PublicationID),
	})
	h.broadcast(outbound{eventType: eventType, scope: Scope{SourceID: job.SourceID, JobID: job.ID}, payload: payload}, "library.read")
}

func (h *Hub) PublicationPublished(publication catalog.Publication) {
	payload, _ := json.Marshal(map[string]any{
		"queryPublicationId": publication.ID, "catalogRevision": publication.CatalogRevisionID,
		"overlayProjectionRevision": publication.OverlayRevisionID, "jobId": publication.JobID,
	})
	h.broadcast(outbound{eventType: EventQueryPublicationPublished, scope: Scope{JobID: publication.JobID}, payload: payload}, "library.read")
}

func (h *Hub) RevokeSession(sessionID string) {
	payload, _ := json.Marshal(map[string]any{"sessionId": sessionID})
	h.mu.Lock()
	items := make([]*subscriber, 0, len(h.subscribers[sessionID]))
	for item := range h.subscribers[sessionID] {
		items = append(items, item)
	}
	h.mu.Unlock()
	for _, item := range items {
		select {
		case item.messages <- outbound{eventType: EventSessionRevoked, payload: payload, closeAfter: websocket.StatusCode(4401)}:
		default:
			item.conn.CloseNow()
		}
	}
}

func (h *Hub) broadcast(message outbound, capability string) {
	h.mu.Lock()
	var items []*subscriber
	for _, subscriptions := range h.subscribers {
		for item := range subscriptions {
			if hasCapability(item.principal.Capabilities, capability) {
				items = append(items, item)
			}
		}
	}
	h.mu.Unlock()
	for _, item := range items {
		select {
		case item.messages <- message:
		default:
			item.conn.CloseNow()
		}
	}
}

func (h *Hub) add(item *subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subscribers[item.principal.SessionID] == nil {
		h.subscribers[item.principal.SessionID] = make(map[*subscriber]struct{})
	}
	h.subscribers[item.principal.SessionID][item] = struct{}{}
}

func (h *Hub) remove(item *subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subscribers[item.principal.SessionID], item)
	if len(h.subscribers[item.principal.SessionID]) == 0 {
		delete(h.subscribers, item.principal.SessionID)
	}
}

func (h *Hub) write(ctx context.Context, conn *websocket.Conn, sequence *uint64, message outbound) error {
	*sequence++
	envelope := Envelope{ProtocolVersion: ProtocolVersion, EventType: message.eventType, Sequence: *sequence, Scope: message.scope, Payload: message.payload, ServerTime: h.clock.Now().UTC()}
	writeContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return wsjson.Write(writeContext, conn, envelope)
}

func hasCapability(capabilities []string, required string) bool {
	for _, capability := range capabilities {
		if capability == required {
			return true
		}
	}
	return false
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func writeHandshakeFault(w http.ResponseWriter, code fault.Code, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "retryable": false, "correlationId": "ws-handshake"}})
}
