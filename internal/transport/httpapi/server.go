package httpapi

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/auth"
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
	auth   *auth.Personal
	data   *application.Resources
}

func New(mode config.Mode, store *storage.Store, clock ports.Clock, personal *auth.Personal, resources *application.Resources, logger *slog.Logger) http.Handler {
	server := &Server{mode: mode, store: store, clock: clock, auth: personal, data: resources, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", server.health)
	mux.HandleFunc("GET /api/v1/bootstrap", server.bootstrap)
	mux.HandleFunc("POST /api/v1/personal/pairing-attempts", server.createPairingAttempt)
	mux.HandleFunc("POST /api/v1/personal/pair", server.exchangePairingCredential)
	mux.HandleFunc("GET /api/v1/sessions", server.listSessions)
	mux.HandleFunc("DELETE /api/v1/sessions/{sessionId}", server.revokeSession)
	mux.HandleFunc("POST /api/v1/libraries", server.createLibrary)
	mux.HandleFunc("GET /api/v1/libraries/{libraryId}", server.getLibrary)
	mux.HandleFunc("POST /api/v1/sources", server.createSource)
	mux.HandleFunc("GET /api/v1/sources/{sourceId}", server.getSource)
	mux.HandleFunc("POST /api/v1/rule-versions", server.createRuleVersion)
	mux.HandleFunc("GET /api/v1/rule-versions/{semanticHash}", server.getRuleVersion)
	mux.HandleFunc("POST /api/v1/source-rule-bindings", server.createSourceRuleBinding)
	mux.HandleFunc("GET /api/v1/source-rule-bindings/{bindingId}", server.getSourceRuleBinding)
	mux.Handle("/ws/v1", realtime.NewHandler(clock, func(r *http.Request) bool {
		_, err := server.authenticate(r)
		return err == nil
	}))
	return requestLog(logger, hostGuard(mux))
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

func (s *Server) bootstrap(w http.ResponseWriter, r *http.Request) {
	mode := api.Personal
	if s.mode == config.ModeLAN {
		mode = api.Lan
	}
	response := api.BootstrapResponse{
		Mode: mode, Authenticated: false, AvailableCapabilities: s.auth.AvailableCapabilities(),
		EffectiveCapabilities: []string{}, CsrfToken: s.auth.BootstrapCSRF(),
		ApiVersion:               api.BootstrapResponseApiVersionV1,
		WebsocketProtocolVersion: api.BootstrapResponseWebsocketProtocolVersion(realtime.ProtocolVersion),
		SortProtocolVersion:      api.BootstrapResponseSortProtocolVersion(query.SortProtocolVersion),
		RuleSchemaVersion:        api.BootstrapResponseRuleSchemaVersion(rules.RuleSchemaVersion),
	}
	if session, err := s.authenticate(r); err == nil {
		response.Authenticated = true
		response.PrincipalId = &session.PrincipalID
		response.EffectiveCapabilities = append([]string(nil), session.Capabilities...)
		response.CsrfToken = session.CSRFToken
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) createPairingAttempt(w http.ResponseWriter, r *http.Request) {
	if err := auth.ValidateLoopbackRequest(r); err != nil {
		writeFault(w, asFault(err), statusForFault(err))
		return
	}
	if err := auth.ValidateMutation(r, s.auth.BootstrapCSRF()); err != nil {
		writeFault(w, asFault(err), statusForFault(err))
		return
	}
	attempt, err := s.auth.CreatePairingAttempt(r.Context())
	if err != nil {
		writeFault(w, asFault(err), statusForFault(err))
		return
	}
	writeJSON(w, http.StatusCreated, api.PairingAttemptResponse{Credential: attempt.Credential, ExpiresAt: attempt.ExpiresAt})
}

func (s *Server) exchangePairingCredential(w http.ResponseWriter, r *http.Request) {
	if err := auth.ValidateLoopbackRequest(r); err != nil {
		writeFault(w, asFault(err), statusForFault(err))
		return
	}
	if err := auth.ValidateMutation(r, s.auth.BootstrapCSRF()); err != nil {
		writeFault(w, asFault(err), statusForFault(err))
		return
	}
	var request api.PairingExchangeRequest
	if err := decodeJSON(r, &request); err != nil {
		writeFault(w, fault.WithField(fault.CodeValidation, "credential", err), http.StatusBadRequest)
		return
	}
	session, cookie, err := s.auth.Exchange(r.Context(), request.Credential)
	if err != nil {
		writeFault(w, asFault(err), statusForFault(err))
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: auth.CookieName, Value: cookie, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: int(time.Until(session.ExpiresAt).Seconds()),
	})
	writeJSON(w, http.StatusCreated, api.SessionEstablishedResponse{
		Session: sessionSummary(session), CsrfToken: session.CSRFToken,
		EffectiveCapabilities: append([]string(nil), session.Capabilities...),
	})
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "clients.manage"); err != nil {
		writeFault(w, asFault(err), statusForFault(err))
		return
	}
	sessions, err := s.auth.ListSessions(r.Context())
	if err != nil {
		writeFault(w, asFault(err), statusForFault(err))
		return
	}
	items := make([]api.SessionSummary, 0, len(sessions))
	for _, session := range sessions {
		items = append(items, sessionSummary(session))
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": items})
}

func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) {
	session, err := s.requireCapability(r, "clients.manage")
	if err != nil {
		writeFault(w, asFault(err), statusForFault(err))
		return
	}
	if err := auth.ValidateMutation(r, session.CSRFToken); err != nil {
		writeFault(w, asFault(err), statusForFault(err))
		return
	}
	if err := s.auth.Revoke(r.Context(), r.PathValue("sessionId")); err != nil {
		writeFault(w, asFault(err), statusForFault(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createLibrary(w http.ResponseWriter, r *http.Request) {
	session, err := s.requireCapability(r, "library.write")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := auth.ValidateMutation(r, session.CSRFToken); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request api.LibraryCreateRequest
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	result, err := s.data.CreateLibrary(r.Context(), request.Name)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, libraryDTO(result))
}

func (s *Server) getLibrary(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "library.read"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	result, err := s.data.GetLibrary(r.Context(), r.PathValue("libraryId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, libraryDTO(result))
}

func (s *Server) createSource(w http.ResponseWriter, r *http.Request) {
	session, err := s.requireCapability(r, "library.write")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := auth.ValidateMutation(r, session.CSRFToken); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request api.SourceCreateRequest
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	result, err := s.data.CreateSource(r.Context(), request.LibraryId, request.DisplayName, request.RootPath)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sourceDTO(result, s.data.SourceAvailable(result)))
}

func (s *Server) getSource(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "library.read"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	result, err := s.data.GetSource(r.Context(), r.PathValue("sourceId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sourceDTO(result, s.data.SourceAvailable(result)))
}

func (s *Server) createRuleVersion(w http.ResponseWriter, r *http.Request) {
	session, err := s.requireCapability(r, "rules.write")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := auth.ValidateMutation(r, session.CSRFToken); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request struct {
		Package json.RawMessage `json:"package"`
	}
	if err := decodeJSON(r, &request); err != nil || len(request.Package) == 0 {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "package", err))
		return
	}
	result, err := s.data.CreateRuleVersion(r.Context(), request.Package)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ruleVersionDTO(result))
}

func (s *Server) getRuleVersion(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "rules.read"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	result, err := s.data.GetRuleVersion(r.Context(), r.PathValue("semanticHash"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ruleVersionDTO(result))
}

func (s *Server) createSourceRuleBinding(w http.ResponseWriter, r *http.Request) {
	session, err := s.requireCapability(r, "rules.write")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := auth.ValidateMutation(r, session.CSRFToken); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request struct {
		SourceID     string          `json:"sourceId"`
		SemanticHash string          `json:"semanticHash"`
		Parameters   json.RawMessage `json:"parameters"`
		Priority     int             `json:"priority"`
	}
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	result, err := s.data.CreateSourceRuleBinding(r.Context(), request.SourceID, request.SemanticHash, request.Parameters, request.Priority)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sourceRuleBindingDTO(result))
}

func (s *Server) getSourceRuleBinding(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "rules.read"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	result, err := s.data.GetSourceRuleBinding(r.Context(), r.PathValue("bindingId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sourceRuleBindingDTO(result))
}

func (s *Server) writeRequestError(w http.ResponseWriter, err error) {
	writeFault(w, asFault(err), statusForFault(err))
}

func libraryDTO(value application.Library) api.Library {
	return api.Library{Id: value.ID, Name: value.Name, CreatedAt: value.CreatedAt}
}

func sourceDTO(value application.Source, available bool) api.Source {
	return api.Source{Id: value.ID, LibraryId: value.LibraryID, DisplayName: value.DisplayName, ReadOnly: true, Available: available, CreatedAt: value.CreatedAt}
}

func ruleVersionDTO(value application.RuleVersion) api.RuleVersion {
	return api.RuleVersion{RuleSetId: value.RuleSetID, Version: value.Version, PackageHash: value.PackageHash, SemanticHash: value.SemanticHash, RuleIrHash: value.RuleIRHash, CreatedAt: value.CreatedAt}
}

func sourceRuleBindingDTO(value application.SourceRuleBinding) api.SourceRuleBinding {
	parameters := map[string]any{}
	decoder := json.NewDecoder(io.LimitReader(bytes.NewReader(value.Parameters), 1<<20))
	decoder.UseNumber()
	_ = decoder.Decode(&parameters)
	return api.SourceRuleBinding{Id: value.ID, SourceId: value.SourceID, SemanticHash: value.SemanticHash, Parameters: parameters, Priority: value.Priority, RuleIrHash: value.RuleIRHash, CreatedAt: value.CreatedAt}
}

func (s *Server) authenticate(r *http.Request) (auth.Session, error) {
	cookie, err := r.Cookie(auth.CookieName)
	if err != nil {
		return auth.Session{}, fault.New(fault.CodeUnauthenticated, false, nil)
	}
	return s.auth.Authenticate(r.Context(), cookie.Value)
}

func (s *Server) requireCapability(r *http.Request, capability string) (auth.Session, error) {
	session, err := s.authenticate(r)
	if err != nil {
		return auth.Session{}, err
	}
	if !auth.HasCapability(session, capability) {
		return auth.Session{}, fault.New(fault.CodeForbidden, false, nil)
	}
	return session, nil
}

func sessionSummary(session auth.Session) api.SessionSummary {
	return api.SessionSummary{
		Id: session.ID, PrincipalId: session.PrincipalID, CreatedAt: session.CreatedAt,
		ExpiresAt: session.ExpiresAt, LastSeenAt: session.LastSeenAt, Revoked: session.RevokedAt != nil,
	}
}

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("请求必须只包含一个 JSON 值")
	}
	return nil
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

func hostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := auth.ValidateHost(r); err != nil {
			writeFault(w, asFault(err), statusForFault(err))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func asFault(err error) *fault.Error {
	var structured *fault.Error
	if errors.As(err, &structured) {
		return structured
	}
	return fault.New(fault.CodeInternal, false, err)
}

func statusForFault(err error) int {
	structured := asFault(err)
	switch structured.Code {
	case fault.CodeValidation:
		return http.StatusBadRequest
	case fault.CodeSourcePathInvalid, fault.CodeRuleSchemaInvalid, fault.CodeRuleParameterInvalid:
		return http.StatusBadRequest
	case fault.CodeUnauthenticated, fault.CodePairingInvalid, fault.CodePairingExpired:
		return http.StatusUnauthorized
	case fault.CodeForbidden, fault.CodeHostRejected, fault.CodeOriginRejected, fault.CodeCSRFInvalid:
		return http.StatusForbidden
	case fault.CodeNotFound:
		return http.StatusNotFound
	case fault.CodeConflict:
		return http.StatusConflict
	case fault.CodeAppDirsOverlap, fault.CodeSourceRootsOverlap:
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func requestLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.InfoContext(r.Context(), "http_request", "method", r.Method, "route", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
