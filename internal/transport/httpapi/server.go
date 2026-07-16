package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/auth"
	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/config"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	contractquery "github.com/RecRivenVI/gallery/internal/contract/query"
	"github.com/RecRivenVI/gallery/internal/contract/realtime"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/media"
	"github.com/RecRivenVI/gallery/internal/overlay"
	"github.com/RecRivenVI/gallery/internal/ports"
	queryservice "github.com/RecRivenVI/gallery/internal/query"
	"github.com/RecRivenVI/gallery/internal/rules"
	"github.com/RecRivenVI/gallery/internal/scanner"
	"github.com/RecRivenVI/gallery/internal/storage"
	api "github.com/RecRivenVI/gallery/pkg/galleryapi"
)

type Server struct {
	mode    config.Mode
	store   *storage.Store
	clock   ports.Clock
	logger  *slog.Logger
	auth    *auth.Personal
	data    *application.Resources
	jobs    *jobs.Store
	catalog *catalog.Store
	scanner *scanner.Service
	hub     *realtime.Hub
	rules   *rules.Lifecycle
	query   *queryservice.Service
	overlay *overlay.Service
}

func New(mode config.Mode, store *storage.Store, clock ports.Clock, personal *auth.Personal, resources *application.Resources, jobStore *jobs.Store, catalogStore *catalog.Store, scannerService *scanner.Service, overlayService *overlay.Service, hub *realtime.Hub, logger *slog.Logger) http.Handler {
	if hub == nil {
		hub = realtime.NewHub(clock)
	}
	ruleLifecycle, err := rules.NewLifecycle()
	if err != nil {
		panic(fmt.Sprintf("初始化规则生命周期: %v", err))
	}
	queryService, err := queryservice.NewService(context.Background(), store.Control.SQL(), store.Catalog.SQL(), clock, nil)
	if err != nil {
		panic(fmt.Sprintf("初始化查询服务: %v", err))
	}
	server := &Server{mode: mode, store: store, clock: clock, auth: personal, data: resources, jobs: jobStore, catalog: catalogStore, scanner: scannerService, hub: hub, logger: logger, rules: ruleLifecycle, query: queryService, overlay: overlayService}
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
	mux.HandleFunc("POST /api/v1/rules/validate", server.validateRulePackage)
	mux.HandleFunc("POST /api/v1/rules/compile", server.compileRulePackage)
	mux.HandleFunc("POST /api/v1/rules/dry-run", server.dryRunRulePackage)
	mux.HandleFunc("POST /api/v1/rules/impact", server.analyzeRuleImpact)
	mux.HandleFunc("POST /api/v1/rule-versions", server.createRuleVersion)
	mux.HandleFunc("GET /api/v1/rule-versions/{semanticHash}", server.getRuleVersion)
	mux.HandleFunc("POST /api/v1/source-rule-bindings", server.createSourceRuleBinding)
	mux.HandleFunc("GET /api/v1/source-rule-bindings/{bindingId}", server.getSourceRuleBinding)
	mux.HandleFunc("POST /api/v1/sources/{sourceId}/scan-jobs", server.createScanJob)
	mux.HandleFunc("GET /api/v1/jobs/{jobId}", server.getJob)
	mux.HandleFunc("GET /api/v1/query-publications/current", server.getCurrentQueryPublication)
	mux.HandleFunc("GET /api/v1/works", server.listWorks)
	mux.HandleFunc("GET /api/v1/works/{workId}", server.getWork)
	mux.HandleFunc("GET /api/v1/works/{workId}/overlay", server.getWorkOverlay)
	mux.HandleFunc("PUT /api/v1/works/{workId}/overlay", server.putWorkOverlay)
	mux.HandleFunc("GET /api/v1/works/{workId}/media", server.listWorkMedia)
	mux.HandleFunc("GET /api/v1/media/{mediaId}", server.getMedia)
	mux.HandleFunc("GET /api/v1/media/{mediaId}/content", server.mediaContent)
	mux.HandleFunc("HEAD /api/v1/media/{mediaId}/content", server.mediaContent)
	mux.Handle("/ws/v1", hub.Handler(func(r *http.Request) (realtime.Principal, error) {
		if err := auth.ValidateOrigin(r); err != nil {
			return realtime.Principal{}, err
		}
		session, err := server.authenticate(r)
		if err != nil {
			return realtime.Principal{}, err
		}
		return realtime.Principal{SessionID: session.ID, Capabilities: append([]string(nil), session.Capabilities...)}, nil
	}, personal.IsActive))
	return requestLog(logger, hostGuard(mux))
}

func (s *Server) validateRulePackage(w http.ResponseWriter, r *http.Request) {
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
		s.writeRequestError(w, fault.WithField(fault.CodeRuleSchemaInvalid, "package", err))
		return
	}
	result, err := s.rules.Validate(request.Package)
	if err != nil {
		s.writeRequestError(w, ruleRequestFault(fault.CodeRuleSchemaInvalid, "package", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"canonicalPackage": json.RawMessage(result.CanonicalJSON), "packageHash": result.PackageHash, "semanticHash": result.SemanticHash})
}

func (s *Server) compileRulePackage(w http.ResponseWriter, r *http.Request) {
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
		Package    json.RawMessage `json:"package"`
		Parameters json.RawMessage `json:"parameters"`
	}
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeRuleCompile, "body", err))
		return
	}
	result, err := s.rules.Compile(request.Package, request.Parameters)
	if err != nil {
		s.writeRequestError(w, ruleRequestFault(fault.CodeRuleCompile, "package", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"canonicalPackage": json.RawMessage(result.CanonicalJSON), "packageHash": result.PackageHash, "semanticHash": result.SemanticHash,
		"ruleIrHash": result.RuleIRHash, "canonicalParameters": json.RawMessage(result.CanonicalParameters), "ruleIr": result.IR, "cacheHit": result.CacheHit,
	})
}

func (s *Server) dryRunRulePackage(w http.ResponseWriter, r *http.Request) {
	session, err := s.requireCapability(r, "rules.debug")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := auth.ValidateMutation(r, session.CSRFToken); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request struct {
		Package    json.RawMessage   `json:"package"`
		Parameters json.RawMessage   `json:"parameters"`
		Sample     rules.DryRunInput `json:"sample"`
	}
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeRuleDryRun, "body", err))
		return
	}
	result, err := s.rules.DryRun(r.Context(), request.Package, request.Parameters, request.Sample)
	if err != nil {
		s.writeRequestError(w, ruleRequestFault(fault.CodeRuleDryRun, "sample", err))
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) analyzeRuleImpact(w http.ResponseWriter, r *http.Request) {
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
		Before json.RawMessage `json:"before"`
		After  json.RawMessage `json:"after"`
	}
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeRuleImpact, "body", err))
		return
	}
	result, err := s.rules.Impact(request.Before, request.After)
	if err != nil {
		s.writeRequestError(w, ruleRequestFault(fault.CodeRuleImpact, "after", err))
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func ruleRequestFault(code fault.Code, field string, err error) *fault.Error {
	if strings.Contains(err.Error(), "CEL_") {
		code = fault.CodeRuleCELLimit
	}
	if exact := rules.ErrorField(err); exact != "" {
		field += exact
	}
	return fault.WithField(code, field, err)
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
		SortProtocolVersion:      api.BootstrapResponseSortProtocolVersion(contractquery.SortProtocolVersion),
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
	s.hub.RevokeSession(r.PathValue("sessionId"))
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

func (s *Server) createScanJob(w http.ResponseWriter, r *http.Request) {
	session, err := s.requireCapability(r, "scan.run")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := auth.ValidateMutation(r, session.CSRFToken); err != nil {
		s.writeRequestError(w, err)
		return
	}
	job, err := s.scanner.CreateScan(r.Context(), r.PathValue("sourceId"), session.PrincipalID)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, jobDTO(job))
	s.scanner.Start(job.ID)
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "library.read"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	job, err := s.jobs.Get(r.Context(), r.PathValue("jobId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, jobDTO(job))
}

func (s *Server) getCurrentQueryPublication(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "library.read"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	publication, err := s.catalog.Current(r.Context())
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, publicationDTO(publication))
}

func (s *Server) listWorks(w http.ResponseWriter, r *http.Request) {
	session, err := s.requireCapability(r, "library.read")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil {
			s.writeRequestError(w, fault.WithField(fault.CodeValidation, "limit", err))
			return
		}
	}
	result, err := s.query.Search(r.Context(), queryservice.Request{
		Search: r.URL.Query().Get("q"), Tag: r.URL.Query().Get("tag"),
		LibraryID: r.URL.Query().Get("libraryId"), SourceID: r.URL.Query().Get("sourceId"),
		SortDirection: r.URL.Query().Get("sortDirection"), Limit: limit, Cursor: r.URL.Query().Get("cursor"),
		QueryPublicationID: r.URL.Query().Get("queryPublicationId"),
		AuthorizationScope: queryservice.AuthorizationScope(session.PrincipalID, session.Capabilities),
	})
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	items := make([]api.PublishedWork, 0, len(result.Items))
	for _, work := range result.Items {
		items = append(items, api.PublishedWork{
			Id: work.ID, Title: work.Title, Creator: work.Creator, Tags: work.Tags,
			MediaCount: work.MediaCount, QueryPublicationId: result.QueryPublicationID,
		})
	}
	response := api.WorkListResponse{
		QueryPublicationId: result.QueryPublicationID, CatalogRevision: result.CatalogRevision,
		OverlayProjectionRevision: result.OverlayProjectionRevision,
		SortProtocolVersion:       api.WorkListResponseSortProtocolVersion(result.SortProtocolVersion), Works: items,
	}
	if result.NextCursor != "" {
		response.NextCursor = &result.NextCursor
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) getWork(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "library.read"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	publication, work, err := s.catalog.GetWork(r.Context(), r.PathValue("workId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, workDTO(publication, work))
}

func (s *Server) getWorkOverlay(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "library.read"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	if s.overlay == nil {
		s.writeRequestError(w, fault.New(fault.CodeInternal, false, nil))
		return
	}
	state, err := s.overlay.Get(r.Context(), r.PathValue("workId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, overlayDTO(state))
}

func (s *Server) putWorkOverlay(w http.ResponseWriter, r *http.Request) {
	session, err := s.requireCapability(r, "overlays.write")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := auth.ValidateMutation(r, session.CSRFToken); err != nil {
		s.writeRequestError(w, err)
		return
	}
	if s.overlay == nil {
		s.writeRequestError(w, fault.New(fault.CodeInternal, false, nil))
		return
	}
	var request api.WorkOverlayPutRequest
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeOverlayFactInvalid, "body", err))
		return
	}
	cover := ""
	if request.CustomCoverMediaId != nil {
		cover = *request.CustomCoverMediaId
	}
	result, err := s.overlay.Put(r.Context(), r.PathValue("workId"), session.PrincipalID, overlay.Input{
		TitleOverride: request.TitleOverride, ManualTags: request.ManualTags, Hidden: request.Hidden,
		CustomCoverMediaID: cover, Favorite: request.Favorite, Progress: float64(request.Progress),
	})
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if result.StartJob {
		s.overlay.Start(result.ProjectionJobID)
	}
	writeJSON(w, http.StatusOK, overlayDTO(result.State))
}

func (s *Server) listWorkMedia(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "media.read"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	publication, items, err := s.catalog.ListMediaForWork(r.Context(), r.PathValue("workId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	mediaItems := make([]api.PublishedMedia, 0, len(items))
	for _, item := range items {
		mediaItems = append(mediaItems, mediaDTO(publication, item))
	}
	writeJSON(w, http.StatusOK, api.MediaListResponse{QueryPublicationId: publication.ID, Media: mediaItems})
}

func (s *Server) getMedia(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "media.read"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	publication, item, err := s.catalog.GetMedia(r.Context(), r.PathValue("mediaId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, mediaDTO(publication, item))
}

func (s *Server) mediaContent(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "media.read"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	_, item, err := s.catalog.GetMedia(r.Context(), r.PathValue("mediaId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if item.LocationStatus != "present" {
		s.writeRequestError(w, fault.New(fault.CodeMediaOffline, true, nil))
		return
	}
	source, err := s.data.GetSource(r.Context(), item.SourceID)
	if err != nil {
		s.writeRequestError(w, fault.New(fault.CodeMediaOffline, true, nil))
		return
	}
	blobLease, err := media.AcquireBlobReadLease(r.Context(), s.store.Catalog.SQL(), s.clock,
		domain.ContentBlobRef{Algorithm: item.Algorithm, Digest: item.Digest}, nil)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	defer blobLease.Close()
	snapshot, err := media.PrepareSnapshot(source.RootPath, item.RelativePath, item.Algorithm, item.Digest, item.Size, s.data.TempRoot())
	if err != nil {
		var structured *fault.Error
		if errors.As(err, &structured) && (structured.Code == fault.CodeSourceUnavailable || structured.Code == fault.CodeSourceReadFailed || structured.Code == fault.CodeContentDisappeared) {
			err = fault.New(fault.CodeMediaOffline, true, nil)
		}
		s.writeRequestError(w, err)
		return
	}
	defer snapshot.Close()
	etag := `"gallery-` + item.Algorithm + `-` + item.Digest + `"`
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Type", item.MIME)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	selected, partial, err := media.ParseSingleRange(r.Header.Get("Range"), snapshot.Size)
	if err != nil {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", snapshot.Size))
		s.writeRequestError(w, err)
		return
	}
	status := http.StatusOK
	start, length := int64(0), snapshot.Size
	if partial {
		status, start, length = http.StatusPartialContent, selected.Start, selected.Length()
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", selected.Start, selected.End, snapshot.Size))
	}
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := snapshot.File.Seek(start, io.SeekStart); err != nil {
		return
	}
	_, _ = io.CopyN(w, snapshot.File, length)
}

func etagMatches(header, current string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || candidate == current || strings.TrimPrefix(candidate, "W/") == current {
			return true
		}
	}
	return false
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

func jobDTO(value jobs.Job) api.Job {
	result := api.Job{
		Id: value.ID, Type: api.JobType(value.Type), Status: api.JobStatus(value.Status),
		Stage: value.Stage, Attempt: value.Attempt, CreatedAt: value.CreatedAt, StartedAt: value.StartedAt,
		FinishedAt: value.FinishedAt, UpdatedAt: value.UpdatedAt,
	}
	if value.SourceID != "" {
		sourceID := api.SourceId(value.SourceID)
		result.SourceId = &sourceID
	}
	result.Progress.Current, result.Progress.Total, result.Progress.Sequence = value.ProgressCurrent, value.ProgressTotal, int64(value.ProgressSequence)
	if value.IssueCode != "" {
		result.IssueCode = &value.IssueCode
	}
	if value.PublicationID != "" {
		publication := api.QueryPublicationId(value.PublicationID)
		result.QueryPublicationId = &publication
	}
	if value.RetryOf != "" {
		retry := api.JobId(value.RetryOf)
		result.RetryOf = &retry
	}
	return result
}

func overlayDTO(value overlay.State) api.WorkOverlayState {
	result := api.WorkOverlayState{
		WorkId: value.WorkID, TitleOverride: value.TitleOverride, ManualTags: value.ManualTags,
		Hidden: value.Hidden, Favorite: value.Favorite, Progress: float32(value.Progress),
		FactWatermark: value.FactWatermark, QueryWatermark: value.QueryWatermark,
		ProjectedWatermark: value.ProjectedWatermark,
		ProjectionStatus:   api.WorkOverlayStateProjectionStatus(value.ProjectionStatus),
	}
	if value.CustomCoverMediaID != "" {
		cover := api.CanonicalMediaId(value.CustomCoverMediaID)
		result.CustomCoverMediaId = &cover
	}
	if value.ProjectionJobID != "" {
		jobID := api.JobId(value.ProjectionJobID)
		result.ProjectionJobId = &jobID
	}
	if value.PublishedQueryPublicationID != "" {
		publicationID := api.QueryPublicationId(value.PublishedQueryPublicationID)
		result.PublishedQueryPublicationId = &publicationID
	}
	if value.IssueCode != "" {
		result.IssueCode = &value.IssueCode
	}
	return result
}

func publicationDTO(value catalog.Publication) api.QueryPublication {
	return api.QueryPublication{Id: value.ID, CatalogRevision: value.CatalogRevisionID, OverlayProjectionRevision: value.OverlayRevisionID, JobId: value.JobID, ControlWatermark: value.ControlWatermark, CreatedAt: value.CreatedAt}
}

func workDTO(publication catalog.Publication, value catalog.Work) api.PublishedWork {
	return api.PublishedWork{
		Id: value.ID, Title: value.Title, Creator: value.Creator, Tags: value.Tags,
		MediaCount: value.MediaCount, QueryPublicationId: publication.ID,
	}
}

func mediaDTO(publication catalog.Publication, value catalog.Media) api.PublishedMedia {
	return api.PublishedMedia{Id: value.ID, WorkId: value.WorkID, Kind: value.Kind, MimeType: value.MIME, SizeBytes: value.Size, Blob: api.ContentBlobRef{Algorithm: api.ContentBlobRefAlgorithm(value.Algorithm), Digest: value.Digest}, Available: value.LocationStatus == "present", Ordinal: value.Ordinal, QueryPublicationId: publication.ID}
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
	case fault.CodeSourcePathInvalid, fault.CodeCursorInvalid, fault.CodeCursorExpired, fault.CodeQueryTooShort,
		fault.CodeOverlayFactInvalid,
		fault.CodeRuleSchemaInvalid, fault.CodeRuleParameterInvalid, fault.CodeRuleCompile,
		fault.CodeRuleCELLimit, fault.CodeRuleDryRun, fault.CodeRuleImpact, fault.CodeRuleEval:
		return http.StatusBadRequest
	case fault.CodeUnauthenticated, fault.CodePairingInvalid, fault.CodePairingExpired:
		return http.StatusUnauthorized
	case fault.CodeForbidden, fault.CodeHostRejected, fault.CodeOriginRejected, fault.CodeCSRFInvalid:
		return http.StatusForbidden
	case fault.CodeNotFound:
		return http.StatusNotFound
	case fault.CodeConflict:
		return http.StatusConflict
	case fault.CodeJobStateConflict, fault.CodeScanAlreadyRunning, fault.CodeCatalogCandidateInvalid:
		return http.StatusConflict
	case fault.CodeBindingReviewRequired:
		return http.StatusConflict
	case fault.CodeContentChangedDuringHash:
		return http.StatusConflict
	case fault.CodeRangeInvalid:
		return http.StatusRequestedRangeNotSatisfiable
	case fault.CodeMediaOffline, fault.CodeSourceUnavailable, fault.CodeSourceReadFailed, fault.CodeContentDisappeared:
		return http.StatusServiceUnavailable
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
