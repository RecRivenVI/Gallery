package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/RecRivenVI/gallery/internal/config"
	"github.com/RecRivenVI/gallery/internal/contract/api"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/contract/query"
	"github.com/RecRivenVI/gallery/internal/contract/realtime"
	"github.com/RecRivenVI/gallery/internal/ports"
	"github.com/RecRivenVI/gallery/internal/rules"
	"github.com/RecRivenVI/gallery/internal/storage"
)

type Server struct {
	mode   config.Mode
	store  *storage.Store
	clock  ports.Clock
	logger *slog.Logger
}

func New(mode config.Mode, store *storage.Store, clock ports.Clock, logger *slog.Logger) http.Handler {
	server := &Server{mode: mode, store: store, clock: clock, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", server.health)
	mux.HandleFunc("GET /api/v1/bootstrap", server.bootstrap)
	mux.Handle("/ws/v1", realtime.NewHandler(clock, func(*http.Request) bool { return false }))
	return requestLog(logger, mux)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.store.Control.SQL().PingContext(r.Context()) != nil || s.store.Catalog.SQL().PingContext(r.Context()) != nil {
		writeFault(w, fault.New(fault.CodeInternal, true, nil), http.StatusInternalServerError)
		return
	}
	response := api.HealthResponse{
		Status: api.Ok, ApiVersion: api.HealthResponseApiVersionV1,
	}
	response.Databases.Control = api.HealthResponseDatabasesControlOk
	response.Databases.Catalog = api.HealthResponseDatabasesCatalogOk
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) bootstrap(w http.ResponseWriter, _ *http.Request) {
	mode := api.Personal
	if s.mode == config.ModeLAN {
		mode = api.Lan
	}
	response := api.BootstrapResponse{
		Mode: mode, Authenticated: false, EffectiveCapabilities: []string{},
		ApiVersion:               api.BootstrapResponseApiVersionV1,
		WebsocketProtocolVersion: api.BootstrapResponseWebsocketProtocolVersion(realtime.ProtocolVersion),
		SortProtocolVersion:      api.BootstrapResponseSortProtocolVersion(query.SortProtocolVersion),
		RuleSchemaVersion:        api.BootstrapResponseRuleSchemaVersion(rules.RuleSchemaVersion),
	}
	writeJSON(w, http.StatusOK, response)
}

func writeFault(w http.ResponseWriter, err *fault.Error, status int) {
	correlation := make([]byte, 16)
	if _, randomErr := rand.Read(correlation); randomErr != nil {
		correlation = []byte("correlation-fallback")
	}
	detail := api.ErrorDetail{
		Code: api.ErrorCode(err.Code), Retryable: err.Retryable, CorrelationId: hex.EncodeToString(correlation),
	}
	if err.Field != "" {
		detail.Field = &err.Field
	}
	writeJSON(w, status, api.ErrorEnvelope{Error: detail})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func requestLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.InfoContext(r.Context(), "http_request", "method", r.Method, "route", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
