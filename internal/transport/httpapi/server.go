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
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/auth"
	"github.com/RecRivenVI/gallery/internal/backup"
	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/config"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	contractquery "github.com/RecRivenVI/gallery/internal/contract/query"
	"github.com/RecRivenVI/gallery/internal/contract/realtime"
	"github.com/RecRivenVI/gallery/internal/creators"
	"github.com/RecRivenVI/gallery/internal/derived"
	"github.com/RecRivenVI/gallery/internal/derived/thumbnail"
	"github.com/RecRivenVI/gallery/internal/derivedjob"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/maintenance"
	"github.com/RecRivenVI/gallery/internal/media"
	"github.com/RecRivenVI/gallery/internal/overlay"
	"github.com/RecRivenVI/gallery/internal/ports"
	queryservice "github.com/RecRivenVI/gallery/internal/query"
	"github.com/RecRivenVI/gallery/internal/rules"
	"github.com/RecRivenVI/gallery/internal/scanner"
	"github.com/RecRivenVI/gallery/internal/storage"
	watcherservice "github.com/RecRivenVI/gallery/internal/watcher"
	api "github.com/RecRivenVI/gallery/pkg/galleryapi"
)

type Server struct {
	mode         config.Mode
	store        *storage.Store
	clock        ports.Clock
	logger       *slog.Logger
	auth         *auth.Personal
	data         *application.Resources
	jobs         *jobs.Store
	catalog      *catalog.Store
	scanner      *scanner.Service
	hub          *realtime.Hub
	rules        *rules.Lifecycle
	query        *queryservice.Service
	overlay      *overlay.Service
	creators     *creators.Service
	backup       *backup.Service
	maintenance  *maintenance.Service
	watcher      *watcherservice.Service
	scheduler    JobController
	derived      *derived.Service
	derivedJob   *derivedjob.Service
	allowedHosts []string
}

type JobController interface {
	Submit(class, jobID string) bool
	Cancel(jobID string) bool
}

type Options struct {
	Maintenance  *maintenance.Service
	Watcher      *watcherservice.Service
	Scheduler    JobController
	Derived      *derived.Service
	DerivedJob   *derivedjob.Service
	AllowedHosts []string
}

func New(mode config.Mode, store *storage.Store, clock ports.Clock, personal *auth.Personal, resources *application.Resources, jobStore *jobs.Store, catalogStore *catalog.Store, scannerService *scanner.Service, overlayService *overlay.Service, creatorsService *creators.Service, backupService *backup.Service, hub *realtime.Hub, logger *slog.Logger, options ...Options) http.Handler {
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
	var option Options
	if len(options) > 0 {
		option = options[0]
	}
	server := &Server{mode: mode, store: store, clock: clock, auth: personal, data: resources, jobs: jobStore, catalog: catalogStore, scanner: scannerService, hub: hub, logger: logger, rules: ruleLifecycle, query: queryService, overlay: overlayService, creators: creatorsService, backup: backupService, maintenance: option.Maintenance, watcher: option.Watcher, scheduler: option.Scheduler, derived: option.Derived, derivedJob: option.DerivedJob, allowedHosts: append([]string(nil), option.AllowedHosts...)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", server.health)
	mux.HandleFunc("GET /api/v1/bootstrap", server.bootstrap)
	mux.HandleFunc("POST /api/v1/personal/pairing-attempts", server.createPairingAttempt)
	mux.HandleFunc("POST /api/v1/personal/pair", server.exchangePairingCredential)
	mux.HandleFunc("POST /api/v1/lan/owner", server.initializeLANOwner)
	mux.HandleFunc("POST /api/v1/auth/login", server.login)
	mux.HandleFunc("POST /api/v1/auth/logout", server.logout)
	mux.HandleFunc("GET /api/v1/sessions", server.listSessions)
	mux.HandleFunc("DELETE /api/v1/sessions/{sessionId}", server.revokeSession)
	mux.HandleFunc("GET /api/v1/admin/users", server.listUsers)
	mux.HandleFunc("POST /api/v1/admin/users", server.createUser)
	mux.HandleFunc("PATCH /api/v1/admin/users/{userId}/status", server.setUserStatus)
	mux.HandleFunc("POST /api/v1/account/password", server.changePassword)
	mux.HandleFunc("POST /api/v1/admin/users/{userId}/grants", server.createGrant)
	mux.HandleFunc("DELETE /api/v1/admin/grants/{grantId}", server.revokeGrant)
	mux.HandleFunc("GET /api/v1/api-tokens", server.listAPITokens)
	mux.HandleFunc("POST /api/v1/api-tokens", server.createAPIToken)
	mux.HandleFunc("DELETE /api/v1/api-tokens/{tokenId}", server.revokeAPIToken)
	mux.HandleFunc("GET /api/v1/shares", server.listShares)
	mux.HandleFunc("POST /api/v1/shares", server.createShare)
	mux.HandleFunc("DELETE /api/v1/shares/{shareId}", server.revokeShare)
	mux.HandleFunc("GET /api/v1/public/shares/{credential}", server.resolveShare)
	mux.HandleFunc("GET /api/v1/public/shares/{credential}/media/{mediaId}/content", server.publicShareMediaContent)
	mux.HandleFunc("HEAD /api/v1/public/shares/{credential}/media/{mediaId}/content", server.publicShareMediaContent)
	mux.HandleFunc("GET /api/v1/admin/security-audits", server.listSecurityAudits)
	mux.HandleFunc("POST /api/v1/libraries", server.createLibrary)
	mux.HandleFunc("GET /api/v1/libraries/{libraryId}", server.getLibrary)
	mux.HandleFunc("POST /api/v1/sources", server.createSource)
	mux.HandleFunc("GET /api/v1/sources/{sourceId}", server.getSource)
	mux.HandleFunc("POST /api/v1/rules/validate", server.validateRulePackage)
	mux.HandleFunc("GET /api/v1/rules/schema", server.getRulePackageSchema)
	mux.HandleFunc("POST /api/v1/rules/compile", server.compileRulePackage)
	mux.HandleFunc("POST /api/v1/rules/dry-run", server.dryRunRulePackage)
	mux.HandleFunc("POST /api/v1/rules/impact", server.analyzeRuleImpact)
	mux.HandleFunc("POST /api/v1/rules/import", server.importRulePackage)
	mux.HandleFunc("POST /api/v1/rules/diff", server.diffRulePackages)
	mux.HandleFunc("POST /api/v1/rules/explain", server.explainRulePackage)
	mux.HandleFunc("POST /api/v1/rules/trace", server.traceRulePackage)
	mux.HandleFunc("GET /api/v1/rules/examples", server.listRuleExamples)
	mux.HandleFunc("POST /api/v1/rules/examples/{exampleId}/test", server.testRuleExample)
	mux.HandleFunc("GET /api/v1/rule-packages", server.listRulePackages)
	mux.HandleFunc("POST /api/v1/rule-packages", server.createRulePackage)
	mux.HandleFunc("GET /api/v1/rule-packages/{packageId}", server.getRulePackage)
	mux.HandleFunc("GET /api/v1/rule-packages/{packageId}/draft", server.getRuleDraft)
	mux.HandleFunc("PUT /api/v1/rule-packages/{packageId}/draft", server.saveRuleDraft)
	mux.HandleFunc("POST /api/v1/rule-packages/{packageId}/draft/validate", server.validateRuleDraft)
	mux.HandleFunc("POST /api/v1/rule-packages/{packageId}/publish", server.publishRuleDraft)
	mux.HandleFunc("POST /api/v1/rule-packages/{packageId}/deprecate", server.deprecateRulePackage)
	mux.HandleFunc("DELETE /api/v1/rule-packages/{packageId}", server.deleteRulePackage)
	mux.HandleFunc("GET /api/v1/rule-packages/{packageId}/audits", server.listRuleAudits)
	mux.HandleFunc("GET /api/v1/rule-packages/{packageId}/versions", server.listRuleVersions)
	mux.HandleFunc("POST /api/v1/rule-packages/{packageId}/rollback", server.rollbackRulePackage)
	mux.HandleFunc("POST /api/v1/rule-versions", server.createRuleVersion)
	mux.HandleFunc("GET /api/v1/rule-versions/{semanticHash}", server.getRuleVersion)
	mux.HandleFunc("POST /api/v1/rule-versions/diff", server.diffRuleVersions)
	mux.HandleFunc("GET /api/v1/rule-versions/{semanticHash}/export", server.exportRuleVersion)
	mux.HandleFunc("POST /api/v1/rule-versions/{semanticHash}/deprecate", server.deprecateRuleVersion)
	mux.HandleFunc("POST /api/v1/rule-parameters", server.createRuleParameterSet)
	mux.HandleFunc("GET /api/v1/rule-parameters/{parameterId}", server.getRuleParameterSet)
	mux.HandleFunc("PUT /api/v1/rule-parameters/{parameterId}", server.updateRuleParameterSet)
	mux.HandleFunc("POST /api/v1/rule-parameters/{parameterId}/copy", server.copyRuleParameterSet)
	mux.HandleFunc("POST /api/v1/rule-parameters/{parameterId}/deprecate", server.deprecateRuleParameterSet)
	mux.HandleFunc("POST /api/v1/rule-parameters/{parameterId}/impact", server.impactRuleParameterSet)
	mux.HandleFunc("POST /api/v1/source-rule-bindings", server.createSourceRuleBinding)
	mux.HandleFunc("GET /api/v1/source-rule-bindings/{bindingId}", server.getSourceRuleBinding)
	mux.HandleFunc("PATCH /api/v1/source-rule-bindings/{bindingId}", server.updateSourceRuleBinding)
	mux.HandleFunc("GET /api/v1/sources/{sourceId}/effective-rule-binding", server.getEffectiveRuleBinding)
	mux.HandleFunc("POST /api/v1/sources/{sourceId}/scan-jobs", server.createScanJob)
	mux.HandleFunc("GET /api/v1/jobs/{jobId}", server.getJob)
	mux.HandleFunc("GET /api/v1/jobs", server.listJobs)
	mux.HandleFunc("POST /api/v1/jobs/{jobId}/cancel", server.cancelJob)
	mux.HandleFunc("POST /api/v1/jobs/{jobId}/retry", server.retryJob)
	mux.HandleFunc("GET /api/v1/jobs/{jobId}/attempts", server.listJobAttempts)
	mux.HandleFunc("GET /api/v1/sources/{sourceId}/scan-status", server.getSourceScanStatus)
	mux.HandleFunc("POST /api/v1/admin/maintenance/gc", server.createCatalogGCJob)
	mux.HandleFunc("POST /api/v1/admin/maintenance/checkpoint", server.createCatalogCheckpointJob)
	mux.HandleFunc("POST /api/v1/admin/maintenance/vacuum", server.createCatalogVacuumJob)
	mux.HandleFunc("GET /api/v1/creators", server.listCreators)
	mux.HandleFunc("GET /api/v1/creators/{creatorId}", server.getCreator)
	mux.HandleFunc("GET /api/v1/creators/merges", server.listCreatorMerges)
	mux.HandleFunc("POST /api/v1/creators/merges", server.mergeCreators)
	mux.HandleFunc("DELETE /api/v1/creators/merges/{mergeId}", server.undoCreatorMerge)
	mux.HandleFunc("GET /api/v1/binding-issues", server.listBindingIssues)
	mux.HandleFunc("GET /api/v1/binding-issues/{issueId}", server.getBindingIssue)
	mux.HandleFunc("POST /api/v1/binding-issues/{issueId}/resolve", server.resolveBindingIssue)
	mux.HandleFunc("POST /api/v1/binding-issues/{issueId}/dismiss", server.dismissBindingIssue)
	mux.HandleFunc("POST /api/v1/binding-issues/{issueId}/reopen", server.reopenBindingIssue)
	mux.HandleFunc("POST /api/v1/binding-issues/{issueId}/resolve-structure", server.resolveSourceStructureIssue)
	mux.HandleFunc("GET /api/v1/source-structure-decisions", server.listSourceStructureDecisions)
	mux.HandleFunc("GET /api/v1/source-structure-decisions/{decisionId}", server.getSourceStructureDecision)
	mux.HandleFunc("POST /api/v1/source-structure-decisions/{decisionId}/undo", server.undoSourceStructureDecision)
	mux.HandleFunc("POST /api/v1/binding-actions/unbind-work", server.unbindWork)
	mux.HandleFunc("POST /api/v1/binding-actions/unbind-media", server.unbindMedia)
	mux.HandleFunc("POST /api/v1/binding-actions/undo-unbind", server.undoManualUnbind)
	mux.HandleFunc("GET /api/v1/orphan-candidates", server.listOrphanCandidates)
	mux.HandleFunc("POST /api/v1/orphan-candidates/{bindingId}/decide", server.decideOrphanCandidate)
	mux.HandleFunc("POST /api/v1/admin/control-backups", server.createControlBackup)
	mux.HandleFunc("GET /api/v1/admin/control-backups", server.listControlBackups)
	mux.HandleFunc("GET /api/v1/admin/control-backups/{backupId}", server.getControlBackup)
	mux.HandleFunc("POST /api/v1/admin/control-restores/verify", server.verifyControlRestore)
	mux.HandleFunc("POST /api/v1/admin/control-restores", server.requestControlRestore)
	mux.HandleFunc("GET /api/v1/query-publications/current", server.getCurrentQueryPublication)
	mux.HandleFunc("GET /api/v1/works", server.listWorks)
	mux.HandleFunc("GET /api/v1/works/{workId}", server.getWork)
	mux.HandleFunc("GET /api/v1/works/{workId}/overlay", server.getWorkOverlay)
	mux.HandleFunc("PUT /api/v1/works/{workId}/overlay", server.putWorkOverlay)
	mux.HandleFunc("GET /api/v1/works/{workId}/media", server.listWorkMedia)
	mux.HandleFunc("GET /api/v1/media/{mediaId}", server.getMedia)
	mux.HandleFunc("GET /api/v1/media/{mediaId}/content", server.mediaContent)
	mux.HandleFunc("HEAD /api/v1/media/{mediaId}/content", server.mediaContent)
	mux.HandleFunc("POST /api/v1/media/{mediaId}/verification-jobs", server.createMediaVerificationJob)
	mux.HandleFunc("POST /api/v1/media/{mediaId}/derived-assets", server.createDerivedAsset)
	mux.HandleFunc("GET /api/v1/derived-assets/{assetKey}/content", server.derivedAssetContent)
	mux.Handle("/ws/v1", hub.Handler(func(r *http.Request) (realtime.Principal, error) {
		if err := server.validateOrigin(r); err != nil {
			return realtime.Principal{}, err
		}
		session, err := server.authenticate(r)
		if err != nil {
			return realtime.Principal{}, err
		}
		return realtime.Principal{
			SessionID: session.ID, PrincipalID: session.PrincipalID,
			Capabilities: append([]string(nil), session.Capabilities...),
			Authorize: func(ctx context.Context, capability string, scope realtime.Scope) bool {
				resourceScope := auth.ResourceScope{Kind: "global"}
				if scope.SourceID != "" {
					resourceScope = auth.ResourceScope{Kind: "source", ID: scope.SourceID}
				} else if scope.LibraryID != "" {
					resourceScope = auth.ResourceScope{Kind: "library", ID: scope.LibraryID}
				}
				allowed, authorizeErr := server.auth.AuthorizeSession(ctx, session, capability, resourceScope)
				return authorizeErr == nil && allowed
			},
		}, nil
	}, personal.IsActive))
	return requestLog(logger, hostGuard(server, mux))
}

func (s *Server) getRulePackageSchema(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "rules.read"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/schema+json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(rules.RulePackageSchema())
}

func (s *Server) validateRulePackage(w http.ResponseWriter, r *http.Request) {
	session, err := s.requireCapability(r, "rules.write")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, session); err != nil {
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
	if err := s.validateMutation(r, session); err != nil {
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
	result, err := s.data.CompileRulePackage(r.Context(), request.Package, request.Parameters)
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
	if err := s.validateMutation(r, session); err != nil {
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
	if err := s.validateMutation(r, session); err != nil {
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
		EffectiveCapabilities: []string{}, CsrfToken: s.auth.BootstrapCSRF(), LanInitialized: s.mode == config.ModePersonal,
		ApiVersion:               api.BootstrapResponseApiVersionV1,
		WebsocketProtocolVersion: api.BootstrapResponseWebsocketProtocolVersion(realtime.ProtocolVersion),
		SortProtocolVersion:      api.BootstrapResponseSortProtocolVersion(contractquery.SortProtocolVersion),
		RuleSchemaVersion:        api.BootstrapResponseRuleSchemaVersion(rules.RuleSchemaVersion),
	}
	if s.mode == config.ModeLAN {
		initialized, err := s.auth.LANInitialized(r.Context())
		if err != nil {
			s.writeRequestError(w, err)
			return
		}
		response.LanInitialized = initialized
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
	s.setSessionCookie(w, r, session, cookie)
	writeJSON(w, http.StatusCreated, api.SessionEstablishedResponse{
		Session: sessionSummary(session), CsrfToken: session.CSRFToken,
		EffectiveCapabilities: append([]string(nil), session.Capabilities...),
	})
}

func (s *Server) initializeLANOwner(w http.ResponseWriter, r *http.Request) {
	if s.mode != config.ModeLAN {
		s.writeRequestError(w, fault.New(fault.CodeNotFound, false, nil))
		return
	}
	if err := auth.ValidateLoopbackRequest(r); err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := auth.ValidateMutationAllowed(r, s.auth.BootstrapCSRF(), s.allowedHosts); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request struct {
		Username    string `json:"username"`
		DisplayName string `json:"displayName"`
		Password    string `json:"password"`
	}
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	user, err := s.auth.InitializeLANOwner(r.Context(), auth.CreateUserInput{
		Username: request.Username, DisplayName: request.DisplayName, Password: request.Password,
	})
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, userDTO(user))
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s.mode != config.ModeLAN {
		s.writeRequestError(w, fault.New(fault.CodeNotFound, false, nil))
		return
	}
	if err := auth.ValidateMutationAllowed(r, s.auth.BootstrapCSRF(), s.allowedHosts); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		ClientLabel string `json:"clientLabel"`
	}
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	session, cookie, err := s.auth.Login(r.Context(), request.Username, request.Password, request.ClientLabel, loginRateSubject(r))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	s.setSessionCookie(w, r, session, cookie)
	writeJSON(w, http.StatusCreated, api.SessionEstablishedResponse{
		Session: sessionSummary(session), CsrfToken: session.CSRFToken,
		EffectiveCapabilities: append([]string(nil), session.Capabilities...),
	})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, session); err != nil {
		s.writeRequestError(w, err)
		return
	}
	if session.TokenID != "" {
		err = s.auth.RevokeAPIToken(r.Context(), session.PrincipalID, session.TokenID)
	} else {
		err = s.auth.Revoke(r.Context(), session.PrincipalID, session.ID)
	}
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	s.hub.RevokeSession(session.ID)
	http.SetCookie(w, &http.Cookie{Name: auth.CookieName, Value: "", Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "users.manage"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	users, err := s.auth.ListUsers(r.Context())
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(users))
	for _, user := range users {
		items = append(items, userDTO(user))
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": items})
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	actor, err := s.requireCapability(r, "users.manage")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, actor); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request struct {
		Username    string            `json:"username"`
		DisplayName string            `json:"displayName"`
		Password    string            `json:"password"`
		Roles       []string          `json:"roles"`
		Grants      []auth.GrantInput `json:"grants"`
	}
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	user, err := s.auth.CreateUser(r.Context(), actor.PrincipalID, auth.CreateUserInput{
		Username: request.Username, DisplayName: request.DisplayName, Password: request.Password,
		Roles: request.Roles, Grants: request.Grants,
	})
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, userDTO(user))
}

func (s *Server) setUserStatus(w http.ResponseWriter, r *http.Request) {
	actor, err := s.requireCapability(r, "users.manage")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, actor); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request struct {
		Status string `json:"status"`
	}
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	userID := r.PathValue("userId")
	if err := s.auth.SetUserStatus(r.Context(), actor.PrincipalID, userID, request.Status); err != nil {
		s.writeRequestError(w, err)
		return
	}
	s.hub.RevokePrincipal(userID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	actor, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, actor); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	if err := s.auth.ChangePassword(r.Context(), actor, request.CurrentPassword, request.NewPassword); err != nil {
		s.writeRequestError(w, err)
		return
	}
	s.hub.RevokePrincipal(actor.PrincipalID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createGrant(w http.ResponseWriter, r *http.Request) {
	actor, err := s.requireCapability(r, "users.manage")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, actor); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request auth.GrantInput
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	userID := r.PathValue("userId")
	grant, err := s.auth.CreateGrant(r.Context(), actor.PrincipalID, userID, request)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	s.hub.RevokePrincipal(userID)
	writeJSON(w, http.StatusCreated, grantDTO(grant))
}

func (s *Server) revokeGrant(w http.ResponseWriter, r *http.Request) {
	actor, err := s.requireCapability(r, "users.manage")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, actor); err != nil {
		s.writeRequestError(w, err)
		return
	}
	grantID := r.PathValue("grantId")
	var principalID string
	_ = s.store.Control.SQL().QueryRowContext(r.Context(), "SELECT principal_id FROM authorization_grants WHERE grant_id=?", grantID).Scan(&principalID)
	if err := s.auth.RevokeGrant(r.Context(), actor.PrincipalID, grantID); err != nil {
		s.writeRequestError(w, err)
		return
	}
	if principalID != "" {
		s.hub.RevokePrincipal(principalID)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listAPITokens(w http.ResponseWriter, r *http.Request) {
	actor, err := s.requireCapability(r, "tokens.manage")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	tokens, err := s.auth.ListAPITokens(r.Context(), actor.PrincipalID)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(tokens))
	for _, token := range tokens {
		items = append(items, tokenDTO(token))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": items})
}

func (s *Server) createAPIToken(w http.ResponseWriter, r *http.Request) {
	actor, err := s.requireCapability(r, "tokens.manage")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, actor); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request struct {
		Name         string               `json:"name"`
		Capabilities []string             `json:"capabilities"`
		Scopes       []auth.ResourceScope `json:"scopes"`
		ExpiresAt    *time.Time           `json:"expiresAt"`
	}
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	created, err := s.auth.CreateAPIToken(r.Context(), actor, request.Name, request.Capabilities, request.Scopes, request.ExpiresAt)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	response := tokenDTO(created.Token)
	response["secret"] = created.Secret
	writeJSON(w, http.StatusCreated, response)
}

func (s *Server) revokeAPIToken(w http.ResponseWriter, r *http.Request) {
	actor, err := s.requireCapability(r, "tokens.manage")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, actor); err != nil {
		s.writeRequestError(w, err)
		return
	}
	tokenID := r.PathValue("tokenId")
	if err := s.auth.RevokeAPIToken(r.Context(), actor.PrincipalID, tokenID); err != nil {
		s.writeRequestError(w, err)
		return
	}
	s.hub.RevokeSession(tokenID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listShares(w http.ResponseWriter, r *http.Request) {
	actor, err := s.requireCapability(r, "shares.create")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	shares, err := s.auth.ListShares(r.Context(), actor.PrincipalID)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(shares))
	for _, share := range shares {
		items = append(items, shareDTO(share))
	}
	writeJSON(w, http.StatusOK, map[string]any{"shares": items})
}

func (s *Server) createShare(w http.ResponseWriter, r *http.Request) {
	actor, err := s.requireCapability(r, "shares.create")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, actor); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request struct {
		ScopeKind          string    `json:"scopeKind"`
		ScopeID            string    `json:"scopeId"`
		Permissions        []string  `json:"permissions"`
		FixedBlobAlgorithm string    `json:"fixedBlobAlgorithm"`
		FixedBlobDigest    string    `json:"fixedBlobDigest"`
		ExpiresAt          time.Time `json:"expiresAt"`
	}
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	if err := s.validateShareTarget(r, actor, request.ScopeKind, request.ScopeID,
		request.FixedBlobAlgorithm, request.FixedBlobDigest); err != nil {
		s.writeRequestError(w, err)
		return
	}
	created, err := s.auth.CreateShare(r.Context(), actor, request.ScopeKind, request.ScopeID, request.Permissions,
		request.FixedBlobAlgorithm, request.FixedBlobDigest, request.ExpiresAt)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	response := shareDTO(created.Share)
	response["secret"] = created.Secret
	writeJSON(w, http.StatusCreated, response)
}

func (s *Server) revokeShare(w http.ResponseWriter, r *http.Request) {
	actor, err := s.requireCapability(r, "shares.create")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, actor); err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.auth.RevokeShare(r.Context(), actor.PrincipalID, r.PathValue("shareId")); err != nil {
		s.writeRequestError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) resolveShare(w http.ResponseWriter, r *http.Request) {
	share, err := s.auth.ResolveShare(r.Context(), r.PathValue("credential"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	response, err := s.publicShareResource(r, share)
	if err != nil {
		s.writeRequestError(w, concealPublicShareError(err))
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) validateShareTarget(r *http.Request, actor auth.Session, scopeKind, scopeID, fixedAlgorithm, fixedDigest string) error {
	switch scopeKind {
	case "library":
		if _, err := domain.ParseID(domain.IDLibrary, scopeID); err != nil {
			return fault.WithField(fault.CodeValidation, "scopeId", err)
		}
		if fixedAlgorithm != "" || fixedDigest != "" {
			return fault.WithField(fault.CodeValidation, "fixedBlobAlgorithm", nil)
		}
		if _, err := s.data.GetLibrary(r.Context(), scopeID); err != nil {
			return err
		}
		return concealForbidden(s.authorizeSession(r, actor, "library.read", auth.ResourceScope{Kind: "library", ID: scopeID}))
	case "work":
		if _, err := domain.ParseID(domain.IDCanonicalWork, scopeID); err != nil {
			return fault.WithField(fault.CodeValidation, "scopeId", err)
		}
		if fixedAlgorithm != "" || fixedDigest != "" {
			return fault.WithField(fault.CodeValidation, "fixedBlobAlgorithm", nil)
		}
		_, work, err := s.catalog.GetWork(r.Context(), scopeID)
		if err != nil {
			return err
		}
		return concealForbidden(s.authorizeSession(r, actor, "library.read", auth.ResourceScope{Kind: "source", ID: work.SourceID}))
	case "media":
		if _, err := domain.ParseID(domain.IDCanonicalMedia, scopeID); err != nil {
			return fault.WithField(fault.CodeValidation, "scopeId", err)
		}
		_, item, err := s.catalog.GetMedia(r.Context(), scopeID)
		if err != nil {
			return err
		}
		if err := s.authorizeSession(r, actor, "media.read", auth.ResourceScope{Kind: "source", ID: item.SourceID}); err != nil {
			return concealForbidden(err)
		}
		if fixedAlgorithm != "" || fixedDigest != "" {
			if item.ContentVerificationState != catalog.ContentVerificationStateContentVerified ||
				fixedAlgorithm != item.Algorithm || fixedDigest != item.Digest {
				return fault.WithField(fault.CodeValidation, "fixedBlobDigest", nil)
			}
		}
		return nil
	default:
		return fault.WithField(fault.CodeValidation, "scopeKind", nil)
	}
}

func (s *Server) publicShareResource(r *http.Request, share auth.Share) (map[string]any, error) {
	response := map[string]any{
		"scopeKind": share.ScopeKind, "scopeId": share.ScopeID, "permissions": share.Permissions,
		"expiresAt": share.ExpiresAt, "fixed": share.FixedBlobAlgorithm != "",
	}
	switch share.ScopeKind {
	case "library":
		library, err := s.data.GetLibrary(r.Context(), share.ScopeID)
		if err != nil {
			return nil, err
		}
		response["library"] = libraryDTO(library)
	case "work":
		publication, work, err := s.catalog.GetWork(r.Context(), share.ScopeID)
		if err != nil {
			return nil, err
		}
		_, items, err := s.catalog.ListMediaForWorkAt(r.Context(), publication.ID, work.ID)
		if err != nil {
			return nil, err
		}
		mediaItems := make([]api.PublishedMedia, 0, len(items))
		for _, item := range items {
			mediaItems = append(mediaItems, mediaDTO(publication, item))
		}
		response["queryPublicationId"], response["work"], response["mediaItems"] = publication.ID, workDTO(publication, work), mediaItems
	case "media":
		if share.FixedBlobAlgorithm != "" {
			locations, err := s.catalog.BlobLocations(r.Context(), domain.ContentBlobRef{Algorithm: share.FixedBlobAlgorithm, Digest: share.FixedBlobDigest})
			if err != nil {
				return nil, err
			}
			response["fixedBlob"] = map[string]any{"algorithm": share.FixedBlobAlgorithm, "digest": share.FixedBlobDigest,
				"sizeBytes": locations[0].Size, "mimeType": locations[0].MIME}
			if publication, item, currentErr := s.catalog.GetMedia(r.Context(), share.ScopeID); currentErr == nil &&
				item.Algorithm == share.FixedBlobAlgorithm && item.Digest == share.FixedBlobDigest {
				response["queryPublicationId"], response["media"] = publication.ID, mediaDTO(publication, item)
			}
			break
		}
		publication, item, err := s.catalog.GetMedia(r.Context(), share.ScopeID)
		if err != nil {
			return nil, err
		}
		response["queryPublicationId"], response["media"] = publication.ID, mediaDTO(publication, item)
	default:
		return nil, fault.New(fault.CodeNotFound, false, nil)
	}
	return response, nil
}

func (s *Server) publicShareMediaContent(w http.ResponseWriter, r *http.Request) {
	share, err := s.auth.ResolveShare(r.Context(), r.PathValue("credential"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	download := r.URL.Query().Get("download") == "true"
	if (!shareHasPermission(share, "view") && !shareHasPermission(share, "download")) ||
		(download && !shareHasPermission(share, "download")) {
		s.writeRequestError(w, fault.New(fault.CodeForbidden, false, nil))
		return
	}
	mediaID := r.PathValue("mediaId")
	if _, err := domain.ParseID(domain.IDCanonicalMedia, mediaID); err != nil {
		s.writeRequestError(w, fault.New(fault.CodeNotFound, false, nil))
		return
	}
	if share.FixedBlobAlgorithm != "" {
		if share.ScopeKind != "media" || mediaID != share.ScopeID {
			s.writeRequestError(w, fault.New(fault.CodeNotFound, false, nil))
			return
		}
		s.serveFixedShareBlob(w, r, share, download)
		return
	}
	_, item, err := s.catalog.GetMedia(r.Context(), mediaID)
	if err != nil {
		s.writeRequestError(w, concealPublicShareError(err))
		return
	}
	switch share.ScopeKind {
	case "media":
		if item.ID != share.ScopeID {
			err = fault.New(fault.CodeNotFound, false, nil)
		}
	case "work":
		if item.WorkID != share.ScopeID {
			err = fault.New(fault.CodeNotFound, false, nil)
		}
	case "library":
		source, sourceErr := s.data.GetSource(r.Context(), item.SourceID)
		if sourceErr != nil || source.LibraryID != share.ScopeID {
			err = fault.New(fault.CodeNotFound, false, nil)
		}
	default:
		err = fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	s.serveMediaItem(w, r, item, download)
}

func shareHasPermission(share auth.Share, permission string) bool {
	for _, value := range share.Permissions {
		if value == permission {
			return true
		}
	}
	return false
}

func concealPublicShareError(err error) error {
	var structured *fault.Error
	if errors.As(err, &structured) && structured.Code == fault.CodeInternal {
		return err
	}
	return fault.New(fault.CodeNotFound, false, nil)
}

func (s *Server) listSecurityAudits(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "audit.read"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	items, err := s.auth.ListSecurityAudits(r.Context(), 100)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"audits": items})
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, session auth.Session, value string) {
	maxAge := int(session.ExpiresAt.Sub(s.clock.Now().UTC()).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	http.SetCookie(w, &http.Cookie{Name: auth.CookieName, Value: value, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: maxAge})
}

func userDTO(user auth.User) map[string]any {
	return map[string]any{"id": user.ID, "username": user.Username, "displayName": user.DisplayName,
		"status": user.Status, "roles": user.Roles, "securityVersion": user.SecurityVersion,
		"createdAt": user.CreatedAt, "updatedAt": user.UpdatedAt}
}

func grantDTO(grant auth.Grant) map[string]any {
	return map[string]any{"id": grant.ID, "principalId": grant.PrincipalID, "effect": grant.Effect,
		"capability": grant.Capability, "scope": grant.Scope, "revoked": grant.Revoked}
}

func tokenDTO(token auth.APIToken) map[string]any {
	return map[string]any{"id": token.ID, "principalId": token.PrincipalID, "name": token.Name,
		"secretPrefix": token.SecretPrefix, "capabilities": token.Capabilities, "scopes": token.Scopes,
		"createdAt": token.CreatedAt, "expiresAt": token.ExpiresAt, "lastUsedAt": token.LastUsedAt,
		"revoked": token.RevokedAt != nil}
}

func shareDTO(share auth.Share) map[string]any {
	return map[string]any{"id": share.ID, "createdBy": share.CreatedBy, "scopeKind": share.ScopeKind,
		"scopeId": share.ScopeID, "permissions": share.Permissions, "secretPrefix": share.SecretPrefix,
		"fixedBlobAlgorithm": nullableString(share.FixedBlobAlgorithm), "fixedBlobDigest": nullableString(share.FixedBlobDigest),
		"createdAt": share.CreatedAt, "expiresAt": share.ExpiresAt, "revoked": share.RevokedAt != nil}
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
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
	if err := s.validateMutation(r, session); err != nil {
		writeFault(w, asFault(err), statusForFault(err))
		return
	}
	if err := s.auth.Revoke(r.Context(), session.PrincipalID, r.PathValue("sessionId")); err != nil {
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
	if err := s.validateMutation(r, session); err != nil {
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
	if _, err := s.requireCapabilityForScope(r, "library.read", auth.ResourceScope{Kind: "library", ID: r.PathValue("libraryId")}); err != nil {
		s.writeRequestError(w, concealForbidden(err))
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
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, session); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request api.SourceCreateRequest
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	if err := s.authorizeSession(r, session, "library.write", auth.ResourceScope{Kind: "library", ID: request.LibraryId}); err != nil {
		s.writeRequestError(w, err)
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
	if _, err := s.requireCapabilityForScope(r, "library.read", auth.ResourceScope{Kind: "source", ID: r.PathValue("sourceId")}); err != nil {
		s.writeRequestError(w, concealForbidden(err))
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
	if err := s.validateMutation(r, session); err != nil {
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
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, session); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request struct {
		SourceID     string          `json:"sourceId"`
		SemanticHash string          `json:"semanticHash"`
		Parameters   json.RawMessage `json:"parameters"`
		Priority     int             `json:"priority"`
		ParameterID  string          `json:"parameterId"`
		Override     json.RawMessage `json:"override"`
		Condition    json.RawMessage `json:"condition"`
	}
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	if err := s.authorizeSession(r, session, "rules.write", auth.ResourceScope{Kind: "source", ID: request.SourceID}); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	var result application.SourceRuleBinding
	if request.ParameterID != "" {
		result, err = s.data.CreateSourceRuleBindingFromParameterSet(r.Context(), request.SourceID, request.ParameterID, request.Priority, request.Override, request.Condition)
	} else {
		result, err = s.data.CreateSourceRuleBinding(r.Context(), request.SourceID, request.SemanticHash, request.Parameters, request.Priority)
	}
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sourceRuleBindingDTO(result))
}

func (s *Server) getSourceRuleBinding(w http.ResponseWriter, r *http.Request) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	result, err := s.data.GetSourceRuleBinding(r.Context(), r.PathValue("bindingId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.authorizeSession(r, session, "rules.read", auth.ResourceScope{Kind: "source", ID: result.SourceID}); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	writeJSON(w, http.StatusOK, sourceRuleBindingDTO(result))
}

func (s *Server) createScanJob(w http.ResponseWriter, r *http.Request) {
	session, err := s.requireCapabilityForScope(r, "scan.run", auth.ResourceScope{Kind: "source", ID: r.PathValue("sourceId")})
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, session); err != nil {
		s.writeRequestError(w, err)
		return
	}
	request := api.ScanJobCreateRequest{}
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &request); err != nil {
			s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
			return
		}
	}
	scanProfile := ""
	if request.ScanProfile != nil {
		scanProfile = string(*request.ScanProfile)
	}
	job, err := s.scanner.CreateScanWithProfile(r.Context(), r.PathValue("sourceId"), session.PrincipalID, r.Header.Get("Idempotency-Key"), scanProfile)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, jobDTO(job))
	s.scanner.Start(job.ID)
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	job, err := s.jobs.Get(r.Context(), r.PathValue("jobId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.authorizeJob(r, session, job, false); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	writeJSON(w, http.StatusOK, jobDTO(job))
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	statuses := []jobs.Status{jobs.StatusQueued, jobs.StatusRunning, jobs.StatusPublishing, jobs.StatusCancelling,
		jobs.StatusCompleted, jobs.StatusFailed, jobs.StatusCancelled, jobs.StatusSuperseded, jobs.StatusNeedsRepair}
	if raw := strings.TrimSpace(r.URL.Query().Get("status")); raw != "" {
		statuses = []jobs.Status{jobs.Status(raw)}
	}
	items, err := s.jobs.ListByStatuses(r.Context(), statuses...)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed < 1 || parsed > 200 {
			s.writeRequestError(w, fault.New(fault.CodeValidation, false, nil))
			return
		}
		limit = parsed
	}
	result := api.JobListResponse{Jobs: make([]api.Job, 0, min(limit, len(items)))}
	for _, item := range items {
		if err := s.authorizeJob(r, session, item, false); err != nil {
			var structured *fault.Error
			if errors.As(err, &structured) && structured.Code == fault.CodeForbidden {
				continue
			}
			s.writeRequestError(w, err)
			return
		}
		result.Jobs = append(result.Jobs, jobDTO(item))
		if len(result.Jobs) == limit {
			break
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, session); err != nil {
		s.writeRequestError(w, err)
		return
	}
	current, err := s.jobs.Get(r.Context(), r.PathValue("jobId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.authorizeJob(r, session, current, true); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	job, err := s.jobs.RequestCancel(r.Context(), r.PathValue("jobId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if s.scheduler != nil {
		s.scheduler.Cancel(job.ID)
	}
	writeJSON(w, http.StatusAccepted, jobDTO(job))
}

func (s *Server) retryJob(w http.ResponseWriter, r *http.Request) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, session); err != nil {
		s.writeRequestError(w, err)
		return
	}
	current, err := s.jobs.Get(r.Context(), r.PathValue("jobId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.authorizeJob(r, session, current, true); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	job, err := s.jobs.Retry(r.Context(), r.PathValue("jobId"), session.PrincipalID)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	s.startJob(job)
	writeJSON(w, http.StatusAccepted, jobDTO(job))
}

func (s *Server) listJobAttempts(w http.ResponseWriter, r *http.Request) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	job, err := s.jobs.Get(r.Context(), r.PathValue("jobId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.authorizeJob(r, session, job, false); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	items, err := s.jobs.ListAttempts(r.Context(), r.PathValue("jobId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	result := api.JobAttemptsResponse{Attempts: make([]api.JobAttempt, 0, len(items))}
	for _, item := range items {
		result.Attempts = append(result.Attempts, jobAttemptDTO(item))
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) getSourceScanStatus(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapabilityForScope(r, "library.read", auth.ResourceScope{Kind: "source", ID: r.PathValue("sourceId")}); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	if s.watcher == nil {
		if _, err := s.data.GetSource(r.Context(), r.PathValue("sourceId")); err != nil {
			s.writeRequestError(w, err)
			return
		}
		now := s.clock.Now().UTC()
		writeJSON(w, http.StatusOK, api.SourceScanState{SourceId: r.PathValue("sourceId"), Status: api.SourceScanStateStatus("unknown"), Dirty: true, UpdatedAt: now})
		return
	}
	state, err := s.watcher.GetState(r.Context(), r.PathValue("sourceId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sourceScanStateDTO(state))
}

func (s *Server) createCatalogGCJob(w http.ResponseWriter, r *http.Request) {
	session, err := s.requireCapability(r, "admin.maintenance")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, session); err != nil {
		s.writeRequestError(w, err)
		return
	}
	if s.maintenance == nil {
		s.writeRequestError(w, fault.New(fault.CodeInternal, false, nil))
		return
	}
	request := api.MaintenanceGCRequest{}
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &request); err != nil {
			s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
			return
		}
	}
	retention, dryRun := int64(24*60*60), false
	if request.RetentionSeconds != nil {
		retention = *request.RetentionSeconds
	}
	if request.DryRun != nil {
		dryRun = *request.DryRun
	}
	job, err := s.maintenance.CreateGC(r.Context(), session.PrincipalID, maintenance.Request{RetentionSeconds: retention, DryRun: dryRun})
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	s.startJob(job)
	writeJSON(w, http.StatusAccepted, maintenanceJobDTO(job))
}

func (s *Server) createCatalogCheckpointJob(w http.ResponseWriter, r *http.Request) {
	s.createMaintenanceJob(w, r, "catalog_checkpoint")
}

func (s *Server) createCatalogVacuumJob(w http.ResponseWriter, r *http.Request) {
	s.createMaintenanceJob(w, r, "catalog_vacuum")
}

func (s *Server) createMaintenanceJob(w http.ResponseWriter, r *http.Request, jobType string) {
	session, err := s.requireCapability(r, "admin.maintenance")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, session); err != nil {
		s.writeRequestError(w, err)
		return
	}
	if s.maintenance == nil {
		s.writeRequestError(w, fault.New(fault.CodeInternal, false, nil))
		return
	}
	job, err := s.maintenance.Create(r.Context(), jobType, session.PrincipalID)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	s.startJob(job)
	writeJSON(w, http.StatusAccepted, maintenanceJobDTO(job))
}

func (s *Server) startJob(job jobs.Job) {
	if job.Type == "scan" && s.scanner != nil {
		s.scanner.Start(job.ID)
		return
	}
	if s.scheduler != nil {
		class := job.ResourceClass
		if class == "" {
			class = jobs.ResourceMaintenance
		}
		s.scheduler.Submit(class, job.ID)
	}
}

func (s *Server) listCreators(w http.ResponseWriter, r *http.Request) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if s.creators == nil {
		s.writeRequestError(w, fault.New(fault.CodeInternal, false, nil))
		return
	}
	list, err := s.creators.List(r.Context())
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	items := make([]api.Creator, 0, len(list))
	for _, creator := range list {
		_, bindings, getErr := s.creators.Get(r.Context(), creator.ID)
		if getErr != nil {
			s.writeRequestError(w, getErr)
			return
		}
		_, visible, authErr := s.creatorBindingsAllowed(r, session, bindings)
		if authErr != nil {
			s.writeRequestError(w, authErr)
			return
		}
		if !visible {
			continue
		}
		items = append(items, creatorDTO(creator))
	}
	writeJSON(w, http.StatusOK, api.CreatorListResponse{Creators: items})
}

func (s *Server) getCreator(w http.ResponseWriter, r *http.Request) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if s.creators == nil {
		s.writeRequestError(w, fault.New(fault.CodeInternal, false, nil))
		return
	}
	creator, bindings, err := s.creators.Get(r.Context(), r.PathValue("creatorId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	allowed, visible, err := s.creatorBindingsAllowed(r, session, bindings)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if !visible {
		s.writeRequestError(w, fault.New(fault.CodeNotFound, false, nil))
		return
	}
	items := make([]api.CreatorSourceBinding, 0, len(allowed))
	for _, binding := range allowed {
		items = append(items, api.CreatorSourceBinding{
			BindingId: binding.BindingID, SourceId: binding.SourceID, ProviderId: binding.ProviderID,
			ExternalId: binding.ExternalID, SourceKey: binding.SourceKey,
			Status: api.CreatorSourceBindingStatus(binding.Status),
		})
	}
	writeJSON(w, http.StatusOK, api.CreatorDetail{Creator: creatorDTO(creator), SourceBindings: items})
}

func (s *Server) creatorBindingsAllowed(r *http.Request, session auth.Session, bindings []creators.SourceBinding) ([]creators.SourceBinding, bool, error) {
	global, err := s.auth.AuthorizeSession(r.Context(), session, "library.read", auth.ResourceScope{Kind: "global"})
	if err != nil {
		return nil, false, fault.New(fault.CodeInternal, true, err)
	}
	if global {
		return bindings, true, nil
	}
	allowed := make([]creators.SourceBinding, 0, len(bindings))
	for _, binding := range bindings {
		ok, err := s.auth.AuthorizeSession(r.Context(), session, "library.read", auth.ResourceScope{Kind: "source", ID: binding.SourceID})
		if err != nil {
			return nil, false, fault.New(fault.CodeInternal, true, err)
		}
		if ok {
			allowed = append(allowed, binding)
		}
	}
	return allowed, len(allowed) > 0, nil
}

func (s *Server) listCreatorMerges(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "library.read"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	if s.creators == nil {
		s.writeRequestError(w, fault.New(fault.CodeInternal, false, nil))
		return
	}
	list, err := s.creators.ListMerges(r.Context())
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	items := make([]api.CreatorMerge, 0, len(list))
	for _, merge := range list {
		items = append(items, creatorMergeDTO(merge, ""))
	}
	writeJSON(w, http.StatusOK, api.CreatorMergeListResponse{Merges: items})
}

func (s *Server) mergeCreators(w http.ResponseWriter, r *http.Request) {
	session, err := s.requireCapability(r, "creators.write")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, session); err != nil {
		s.writeRequestError(w, err)
		return
	}
	if s.creators == nil {
		s.writeRequestError(w, fault.New(fault.CodeInternal, false, nil))
		return
	}
	var request api.CreatorMergeRequest
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	result, err := s.creators.Merge(r.Context(), session.PrincipalID, request.TargetCreatorId, request.AbsorbedCreatorIds)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, creatorMergeDTO(result.Merge, result.ProjectionJobID))
}

func (s *Server) undoCreatorMerge(w http.ResponseWriter, r *http.Request) {
	session, err := s.requireCapability(r, "creators.write")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, session); err != nil {
		s.writeRequestError(w, err)
		return
	}
	if s.creators == nil {
		s.writeRequestError(w, fault.New(fault.CodeInternal, false, nil))
		return
	}
	result, err := s.creators.Undo(r.Context(), session.PrincipalID, r.PathValue("mergeId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, creatorMergeDTO(result.Merge, result.ProjectionJobID))
}

func (s *Server) listBindingIssues(w http.ResponseWriter, r *http.Request) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	scope := auth.ResourceScope{Kind: "global"}
	if sourceID := r.URL.Query().Get("sourceId"); sourceID != "" {
		scope = auth.ResourceScope{Kind: "source", ID: sourceID}
	}
	if err := s.authorizeSession(r, session, "bindings.read", scope); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			s.writeRequestError(w, fault.WithField(fault.CodeValidation, "limit", err))
			return
		}
		limit = parsed
	}
	page, err := s.data.ListBindingIssues(r.Context(), application.BindingIssueFilter{
		SourceID: r.URL.Query().Get("sourceId"), EntityType: r.URL.Query().Get("entityType"),
		Status: r.URL.Query().Get("status"),
	}, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	items := make([]api.BindingIssue, 0, len(page.Items))
	for _, issue := range page.Items {
		items = append(items, bindingIssueDTO(issue))
	}
	response := api.BindingIssueListResponse{Issues: items}
	if page.NextCursor != "" {
		response.NextCursor = &page.NextCursor
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) getBindingIssue(w http.ResponseWriter, r *http.Request) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	issue, err := s.data.GetBindingIssue(r.Context(), r.PathValue("issueId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.authorizeSession(r, session, "bindings.read", auth.ResourceScope{Kind: "source", ID: issue.SourceID}); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	writeJSON(w, http.StatusOK, bindingIssueDTO(issue))
}

func (s *Server) resolveBindingIssue(w http.ResponseWriter, r *http.Request) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, session); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request api.BindingIssueResolveRequest
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	existing, err := s.data.GetBindingIssue(r.Context(), r.PathValue("issueId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.authorizeSession(r, session, "bindings.write", auth.ResourceScope{Kind: "source", ID: existing.SourceID}); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	target := ""
	if request.TargetId != nil {
		target = *request.TargetId
	}
	issue, err := s.data.ResolveBindingIssue(r.Context(), r.PathValue("issueId"), session.PrincipalID,
		string(request.Decision), target, request.Version)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, bindingIssueDTO(issue))
}

func (s *Server) dismissBindingIssue(w http.ResponseWriter, r *http.Request) {
	s.transitionBindingIssue(w, r, s.data.DismissBindingIssue)
}

func (s *Server) reopenBindingIssue(w http.ResponseWriter, r *http.Request) {
	s.transitionBindingIssue(w, r, s.data.ReopenBindingIssue)
}

func (s *Server) transitionBindingIssue(w http.ResponseWriter, r *http.Request, action func(context.Context, string, string, int) (application.BindingIssue, error)) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, session); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request api.BindingIssueVersionRequest
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	existing, err := s.data.GetBindingIssue(r.Context(), r.PathValue("issueId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.authorizeSession(r, session, "bindings.write", auth.ResourceScope{Kind: "source", ID: existing.SourceID}); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	issue, err := action(r.Context(), r.PathValue("issueId"), session.PrincipalID, request.Version)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, bindingIssueDTO(issue))
}

func (s *Server) unbindWork(w http.ResponseWriter, r *http.Request) {
	s.bindingAction(w, r, "work", s.data.ManualUnbindWork)
}

func (s *Server) unbindMedia(w http.ResponseWriter, r *http.Request) {
	s.bindingAction(w, r, "media", s.data.UnbindMedia)
}

func (s *Server) undoManualUnbind(w http.ResponseWriter, r *http.Request) {
	s.bindingAction(w, r, "work", s.data.UndoManualUnbind)
}

func (s *Server) bindingAction(w http.ResponseWriter, r *http.Request, kind string, action func(context.Context, string, string) (string, error)) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, session); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request api.BindingUnbindRequest
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	if err := s.authorizeSession(r, session, "bindings.write", auth.ResourceScope{Kind: "source", ID: request.SourceId}); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	canonicalID, err := action(r.Context(), request.SourceId, request.SourceKey)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api.BindingActionResult{CanonicalId: canonicalID, EntityKind: api.BindingActionResultEntityKind(kind)})
}

func (s *Server) listOrphanCandidates(w http.ResponseWriter, r *http.Request) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	scope := auth.ResourceScope{Kind: "global"}
	if sourceID := r.URL.Query().Get("sourceId"); sourceID != "" {
		scope = auth.ResourceScope{Kind: "source", ID: sourceID}
	}
	if err := s.authorizeSession(r, session, "bindings.read", scope); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			s.writeRequestError(w, fault.WithField(fault.CodeValidation, "limit", err))
			return
		}
		limit = parsed
	}
	page, err := s.data.ListOrphanCandidates(r.Context(), application.OrphanCandidateFilter{
		SourceID: r.URL.Query().Get("sourceId"), EntityType: r.URL.Query().Get("entityType"),
	}, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	items := make([]api.OrphanCandidate, 0, len(page.Items))
	for _, candidate := range page.Items {
		items = append(items, orphanCandidateDTO(candidate))
	}
	response := api.OrphanCandidateListResponse{Candidates: items}
	if page.NextCursor != "" {
		response.NextCursor = &page.NextCursor
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) decideOrphanCandidate(w http.ResponseWriter, r *http.Request) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, session); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request api.OrphanDecisionRequest
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	sourceID, err := s.data.OrphanCandidateSource(r.Context(), r.PathValue("bindingId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.authorizeSession(r, session, "bindings.write", auth.ResourceScope{Kind: "source", ID: sourceID}); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	extend := 0
	if request.ExtendScans != nil {
		extend = *request.ExtendScans
	}
	result, err := s.data.DecideOrphanCandidate(r.Context(), r.PathValue("bindingId"), string(request.Decision), extend)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api.OrphanDecisionResult{
		BindingId: result.BindingID, EntityType: api.OrphanDecisionResultEntityType(result.EntityType),
		Decision: api.OrphanDecisionResultDecision(result.Decision), NewStatus: api.OrphanDecisionResultNewStatus(result.NewStatus),
		CanonicalId: result.CanonicalID,
	})
}

func (s *Server) resolveSourceStructureIssue(w http.ResponseWriter, r *http.Request) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, session); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request api.SourceStructureDecisionRequest
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	existing, err := s.data.GetBindingIssue(r.Context(), r.PathValue("issueId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.authorizeSession(r, session, "bindings.write", auth.ResourceScope{Kind: "source", ID: existing.SourceID}); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	targetSourceKey, targetWorkID := "", ""
	if request.TargetSourceKey != nil {
		targetSourceKey = *request.TargetSourceKey
	}
	if request.TargetWorkId != nil {
		targetWorkID = *request.TargetWorkId
	}
	decision, err := s.data.ResolveSourceStructureIssue(r.Context(), r.PathValue("issueId"), session.PrincipalID,
		string(request.Action), targetSourceKey, targetWorkID, request.Version)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, structureDecisionDTO(decision))
}

func (s *Server) listSourceStructureDecisions(w http.ResponseWriter, r *http.Request) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	scope := auth.ResourceScope{Kind: "global"}
	if sourceID := r.URL.Query().Get("sourceId"); sourceID != "" {
		scope = auth.ResourceScope{Kind: "source", ID: sourceID}
	}
	if err := s.authorizeSession(r, session, "bindings.read", scope); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			s.writeRequestError(w, fault.WithField(fault.CodeValidation, "limit", err))
			return
		}
		limit = parsed
	}
	decisions, err := s.data.ListSourceStructureDecisions(r.Context(), r.URL.Query().Get("sourceId"),
		r.URL.Query().Get("status"), limit)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	items := make([]api.SourceStructureDecision, 0, len(decisions))
	for _, decision := range decisions {
		items = append(items, structureDecisionDTO(decision))
	}
	writeJSON(w, http.StatusOK, api.SourceStructureDecisionListResponse{Decisions: items})
}

func (s *Server) getSourceStructureDecision(w http.ResponseWriter, r *http.Request) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	decision, err := s.data.GetSourceStructureDecision(r.Context(), r.PathValue("decisionId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.authorizeSession(r, session, "bindings.read", auth.ResourceScope{Kind: "source", ID: decision.SourceID}); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	writeJSON(w, http.StatusOK, structureDecisionDTO(decision))
}

func (s *Server) undoSourceStructureDecision(w http.ResponseWriter, r *http.Request) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, session); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request api.BindingIssueVersionRequest
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	existing, err := s.data.GetSourceStructureDecision(r.Context(), r.PathValue("decisionId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.authorizeSession(r, session, "bindings.write", auth.ResourceScope{Kind: "source", ID: existing.SourceID}); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	decision, err := s.data.UndoSourceStructureDecision(r.Context(), r.PathValue("decisionId"), session.PrincipalID, request.Version)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, structureDecisionDTO(decision))
}

func structureDecisionDTO(value application.SourceStructureDecision) api.SourceStructureDecision {
	result := api.SourceStructureDecision{
		DecisionId: value.DecisionID, IssueId: value.IssueID, SourceId: value.SourceID,
		Kind: api.SourceStructureDecisionKind(value.Kind), Action: api.SourceStructureDecisionAction(value.Action),
		Status: api.SourceStructureDecisionStatus(value.Status), Version: value.Version,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
	if value.TargetSourceKey != "" {
		result.TargetSourceKey = &value.TargetSourceKey
	}
	if value.TargetWorkID != "" {
		result.TargetWorkId = &value.TargetWorkID
	}
	return result
}

func orphanCandidateDTO(value application.OrphanCandidate) api.OrphanCandidate {
	return api.OrphanCandidate{
		BindingId: value.BindingID, EntityType: api.OrphanCandidateEntityType(value.EntityType),
		SourceId: value.SourceID, SourceKey: value.SourceKey, CanonicalId: value.CanonicalID,
		CanonicalLabel: value.CanonicalLabel, MissedScans: value.MissedScans,
		RetentionThreshold: value.RetentionThreshold, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func (s *Server) createControlBackup(w http.ResponseWriter, r *http.Request) {
	session, err := s.requireCapability(r, "admin.backup")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, session); err != nil {
		s.writeRequestError(w, err)
		return
	}
	if s.backup == nil {
		s.writeRequestError(w, fault.New(fault.CodeInternal, false, nil))
		return
	}
	job, err := s.backup.CreateBackup(r.Context(), session.PrincipalID)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, jobDTO(job))
	s.backup.Start(job.ID)
}

func (s *Server) listControlBackups(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "admin.backup"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	if s.backup == nil {
		s.writeRequestError(w, fault.New(fault.CodeInternal, false, nil))
		return
	}
	list, err := s.backup.List(r.Context())
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	items := make([]api.ControlBackupManifest, 0, len(list))
	for _, manifest := range list {
		items = append(items, backupManifestDTO(manifest))
	}
	writeJSON(w, http.StatusOK, api.ControlBackupListResponse{Backups: items})
}

func (s *Server) getControlBackup(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "admin.backup"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	if s.backup == nil {
		s.writeRequestError(w, fault.New(fault.CodeInternal, false, nil))
		return
	}
	manifest, err := s.backup.Get(r.Context(), r.PathValue("backupId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, backupManifestDTO(manifest))
}

func (s *Server) verifyControlRestore(w http.ResponseWriter, r *http.Request) {
	backupID, _, err := s.decodeRestoreRequest(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	report, err := s.backup.Verify(r.Context(), backupID)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, restoreReportDTO(report))
}

func (s *Server) requestControlRestore(w http.ResponseWriter, r *http.Request) {
	backupID, session, err := s.decodeRestoreRequest(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	report, err := s.backup.RequestRestore(r.Context(), session.PrincipalID, backupID)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, api.ControlRestoreRequestResponse{RestartRequired: true, Report: restoreReportDTO(report)})
}

// decodeRestoreRequest 统一处理恢复端点的 capability、CSRF 与请求体解析。
func (s *Server) decodeRestoreRequest(r *http.Request) (string, auth.Session, error) {
	session, err := s.requireCapability(r, "admin.restore")
	if err != nil {
		return "", auth.Session{}, err
	}
	if err := s.validateMutation(r, session); err != nil {
		return "", auth.Session{}, err
	}
	if s.backup == nil {
		return "", auth.Session{}, fault.New(fault.CodeInternal, false, nil)
	}
	var request api.ControlRestoreRequest
	if err := decodeJSON(r, &request); err != nil || request.BackupId == "" {
		return "", auth.Session{}, fault.WithField(fault.CodeValidation, "backupId", err)
	}
	return request.BackupId, session, nil
}

func restoreReportDTO(value backup.RestoreReport) api.ControlRestoreReport {
	result := api.ControlRestoreReport{
		BackupId: value.BackupID, Compatible: value.Compatible,
		BackupSchemaVersion: value.BackupSchemaVersion, CurrentSchemaVersion: value.CurrentSchemaVersion,
		WillMigrate: value.WillMigrate, ChecksumVerified: value.ChecksumVerified,
		IntegrityOk: value.IntegrityOK, InvariantsOk: value.InvariantsOK,
	}
	if value.Detail != "" {
		result.Detail = &value.Detail
	}
	return result
}

func backupManifestDTO(value backup.Manifest) api.ControlBackupManifest {
	result := api.ControlBackupManifest{
		BackupId: value.BackupID, ManifestVersion: value.ManifestVersion,
		Role: api.ControlBackupManifestRole(value.Role), AppVersion: value.AppVersion,
		SchemaVersion: value.SchemaVersion, MigrationChecksum: value.MigrationChecksum, CreatedAt: value.CreatedAt,
	}
	result.Database.FileName = value.Database.FileName
	result.Database.SizeBytes = value.Database.SizeBytes
	result.Database.Checksum = value.Database.Checksum
	result.Database.ChecksumAlgorithm = api.ControlBackupManifestDatabaseChecksumAlgorithm(value.Database.ChecksumAlgorithm)
	result.Security.Sessions = value.Security.Sessions
	result.Security.PairingCredentials = value.Security.PairingCredentials
	result.Security.ApiTokens = value.Security.APITokens
	result.Security.Shares = value.Security.Shares
	result.Security.CredentialStoreRefs = value.Security.CredentialStoreRefs
	result.Security.Note = value.Security.Note
	if value.Notes != "" {
		result.Notes = &value.Notes
	}
	return result
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
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	scope := auth.ResourceScope{Kind: "global"}
	if sourceID := r.URL.Query().Get("sourceId"); sourceID != "" {
		scope = auth.ResourceScope{Kind: "source", ID: sourceID}
	} else if libraryID := r.URL.Query().Get("libraryId"); libraryID != "" {
		scope = auth.ResourceScope{Kind: "library", ID: libraryID}
	}
	if err := s.authorizeSession(r, session, "library.read", scope); err != nil {
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
	omitTotal := false
	if raw := r.URL.Query().Get("omitTotal"); raw != "" {
		omitTotal, err = strconv.ParseBool(raw)
		if err != nil {
			s.writeRequestError(w, fault.WithField(fault.CodeValidation, "omitTotal", err))
			return
		}
	}
	result, err := s.query.Search(r.Context(), queryservice.Request{
		Search: r.URL.Query().Get("q"), Tag: r.URL.Query().Get("tag"),
		LibraryID: r.URL.Query().Get("libraryId"), SourceID: r.URL.Query().Get("sourceId"),
		Filter:        r.URL.Query().Get("filter"),
		SortDirection: r.URL.Query().Get("sortDirection"), Limit: limit, Cursor: r.URL.Query().Get("cursor"),
		QueryPublicationID: r.URL.Query().Get("queryPublicationId"), OmitTotal: omitTotal,
		AuthorizationScope: queryservice.AuthorizationScope(fmt.Sprintf("%s:%d:%v", session.PrincipalID, session.SecurityVersion, session.TokenScopes), session.Capabilities),
		Capabilities:       session.Capabilities,
	})
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	items := make([]api.PublishedWork, 0, len(result.Items))
	for _, work := range result.Items {
		dto := api.PublishedWork{
			Id: work.ID, Title: work.Title, Creator: work.Creator, Tags: work.Tags,
			MediaCount: work.MediaCount, Favorite: work.Favorite, Progress: float32(work.Progress),
			QueryPublicationId: result.QueryPublicationID,
		}
		if len(work.Matches) > 0 {
			matches := make([]api.FieldMatch, 0, len(work.Matches))
			for _, match := range work.Matches {
				spans := make([]api.MatchSpan, 0, len(match.Spans))
				for _, span := range match.Spans {
					spans = append(spans, api.MatchSpan{Start: span.Start, End: span.End})
				}
				matches = append(matches, api.FieldMatch{Field: api.FieldMatchField(match.Field), Value: match.Value, Spans: spans})
			}
			dto.Matches = &matches
		}
		items = append(items, dto)
	}
	dependencySet := make([]api.DependencyField, 0, len(result.DependencySet))
	for _, field := range result.DependencySet {
		dependencySet = append(dependencySet, api.DependencyField{Field: field.Field, Role: api.DependencyFieldRole(field.Role)})
	}
	response := api.WorkListResponse{
		QueryPublicationId: result.QueryPublicationID, CatalogRevision: result.CatalogRevision,
		OverlayProjectionRevision: result.OverlayProjectionRevision,
		SortProtocolVersion:       api.WorkListResponseSortProtocolVersion(result.SortProtocolVersion),
		RankProtocolVersion:       api.WorkListResponseRankProtocolVersion(result.RankProtocolVersion),
		Total:                     api.Total{Mode: api.TotalMode(result.Total.Mode), Value: result.Total.Value, ProtocolVersion: api.TotalProtocolVersion(result.Total.ProtocolVersion)},
		Works:                     items, DependencySet: dependencySet, LiveUserStateFields: result.LiveUserStateFields,
	}
	if result.NextCursor != "" {
		response.NextCursor = &result.NextCursor
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) getWork(w http.ResponseWriter, r *http.Request) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	publication, work, err := s.catalog.GetWork(r.Context(), r.PathValue("workId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.authorizeSession(r, session, "library.read", auth.ResourceScope{Kind: "source", ID: work.SourceID}); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	writeJSON(w, http.StatusOK, workDTO(publication, work))
}

func (s *Server) getWorkOverlay(w http.ResponseWriter, r *http.Request) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	_, work, err := s.catalog.GetWork(r.Context(), r.PathValue("workId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.authorizeSession(r, session, "library.read", auth.ResourceScope{Kind: "source", ID: work.SourceID}); err != nil {
		s.writeRequestError(w, concealForbidden(err))
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
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	_, work, err := s.catalog.GetWork(r.Context(), r.PathValue("workId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.authorizeSession(r, session, "overlays.write", auth.ResourceScope{Kind: "source", ID: work.SourceID}); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	if err := s.validateMutation(r, session); err != nil {
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
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	requestedPub := r.URL.Query().Get("queryPublicationId")
	release, err := s.acquirePublicationLeaseIfExplicit(r, session, requestedPub)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	defer release()
	_, work, err := s.catalog.GetWorkAt(r.Context(), requestedPub, r.PathValue("workId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.authorizeSession(r, session, "media.read", auth.ResourceScope{Kind: "source", ID: work.SourceID}); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	publication, items, err := s.catalog.ListMediaForWorkAt(r.Context(), requestedPub, r.PathValue("workId"))
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
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	requestedPub := r.URL.Query().Get("queryPublicationId")
	release, err := s.acquirePublicationLeaseIfExplicit(r, session, requestedPub)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	defer release()
	publication, item, err := s.catalog.GetMediaAt(r.Context(), requestedPub, r.PathValue("mediaId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.authorizeSession(r, session, "media.read", auth.ResourceScope{Kind: "source", ID: item.SourceID}); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	writeJSON(w, http.StatusOK, mediaDTO(publication, item))
}

func (s *Server) mediaContent(w http.ResponseWriter, r *http.Request) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	requestedPub := r.URL.Query().Get("queryPublicationId")
	release, err := s.acquirePublicationLeaseIfExplicit(r, session, requestedPub)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	defer release()
	_, item, err := s.catalog.GetMediaAt(r.Context(), requestedPub, r.PathValue("mediaId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.authorizeSession(r, session, "media.read", auth.ResourceScope{Kind: "source", ID: item.SourceID}); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	s.serveMediaItem(w, r, item, false)
}

func (s *Server) serveMediaItem(w http.ResponseWriter, r *http.Request, item catalog.Media, download bool) {
	if item.LocationStatus != "present" {
		s.writeRequestError(w, fault.New(fault.CodeMediaOffline, true, nil))
		return
	}
	if item.ContentVerificationState == catalog.ContentVerificationStateLocatedUnverified {
		// 位置在线但内容尚未完整确认：这不是 Source 离线，不能伪装成 MEDIA_OFFLINE；
		// 未确认媒体也不得进入依赖 ContentBlob/ETag 的已验证读取路径。
		s.writeRequestError(w, fault.New(fault.CodeContentNotVerified, true, nil))
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
	s.writeMediaSnapshot(w, r, snapshot, item.Algorithm, item.Digest, item.MIME, download)
}

func (s *Server) serveFixedShareBlob(w http.ResponseWriter, r *http.Request, share auth.Share, download bool) {
	blob := domain.ContentBlobRef{Algorithm: share.FixedBlobAlgorithm, Digest: share.FixedBlobDigest}
	locations, err := s.catalog.BlobLocations(r.Context(), blob)
	if err != nil {
		s.writeRequestError(w, concealPublicShareError(err))
		return
	}
	lease, err := media.AcquireBlobReadLease(r.Context(), s.store.Catalog.SQL(), s.clock, blob, nil)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	defer lease.Close()
	var lastErr error
	for _, location := range locations {
		source, sourceErr := s.data.GetSource(r.Context(), location.SourceID)
		if sourceErr != nil {
			lastErr = sourceErr
			continue
		}
		snapshot, snapshotErr := media.PrepareSnapshot(source.RootPath, location.RelativePath,
			location.Algorithm, location.Digest, location.Size, s.data.TempRoot())
		if snapshotErr != nil {
			lastErr = snapshotErr
			continue
		}
		s.writeMediaSnapshot(w, r, snapshot, location.Algorithm, location.Digest, location.MIME, download)
		_ = snapshot.Close()
		return
	}
	if lastErr == nil {
		lastErr = fault.New(fault.CodeMediaOffline, true, nil)
	}
	var structured *fault.Error
	if errors.As(lastErr, &structured) && structured.Code == fault.CodeInternal {
		s.writeRequestError(w, lastErr)
		return
	}
	s.writeRequestError(w, fault.New(fault.CodeMediaOffline, true, nil))
}

func (s *Server) writeMediaSnapshot(w http.ResponseWriter, r *http.Request, snapshot *media.Snapshot, algorithm, digest, mimeType string, download bool) {
	etag := `"gallery-` + algorithm + `-` + digest + `"`
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if download {
		w.Header().Set("Content-Disposition", `attachment; filename="gallery-media"`)
	}
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	rangeHeader := r.Header.Get("Range")
	// If-Range：校验值与当前 ETag 不一致时说明内容在两次请求间已变化，必须退回完整
	// 200 响应而不是对旧偏移执行 Range，避免返回来自不同内容版本的字节拼接结果。
	if ifRange := r.Header.Get("If-Range"); rangeHeader != "" && ifRange != "" && !etagMatches(ifRange, etag) {
		rangeHeader = ""
	}
	selected, partial, err := media.ParseSingleRange(rangeHeader, snapshot.Size)
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

// createMediaVerificationJob 为 located_unverified 媒体建立按需内容确认闭环：不在 HTTP
// 请求内同步阻塞计算完整 SHA-256，而是建立一个只强制该媒体重新完整哈希的 incremental
// 扫描 Job（scanner.CreateVerificationScan），不再触发整个 Source 的 verify 档案——同一
// Source 内其余媒体继续按既有 incremental 规则处理，不被这次按需确认强制重新哈希。
//
// 按需确认是"当前 publication 操作"：省略 queryPublicationId 解析当前 active
// publication；显式提供时必须精确等于当前 active publication，仍存在但已经不是
// active 的历史 publication 一律拒绝为结构化 CONFLICT，不创建 Job、不修改
// Binding/Catalog/Overlay/Source。媒体身份（MediaID/SourceID/RelativePath）与冻结
// observation 一律从同一个已确认为 active 的 publication 解析，不混用请求 publication
// 与 active publication。幂等键按实际使用的 queryPublicationId、媒体 ID、Source、
// 相对路径与 observation 指纹（size、mtime、内容确认状态）派生：文件未变化且请求针对
// 同一快照时重复请求复用同一 Job；publication 切换后即使媒体 ID 和相对路径相同，也
// 不会误复用旧 publication 的 Job。
func (s *Server) createMediaVerificationJob(w http.ResponseWriter, r *http.Request) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, session); err != nil {
		s.writeRequestError(w, err)
		return
	}
	requestedPub := r.URL.Query().Get("queryPublicationId")
	release, err := s.acquirePublicationLeaseIfExplicit(r, session, requestedPub)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	defer release()
	resolvedPub, item, err := s.catalog.GetMediaAt(r.Context(), requestedPub, r.PathValue("mediaId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.authorizeSession(r, session, "scan.run", auth.ResourceScope{Kind: "source", ID: item.SourceID}); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	if requestedPub != "" {
		current, currentErr := s.catalog.Current(r.Context())
		if currentErr != nil {
			s.writeRequestError(w, currentErr)
			return
		}
		if resolvedPub.ID != current.ID {
			// 显式指定的 publication 仍然存在，但已经不是当前 active publication：
			// 按需确认只承诺确认当前可见 Catalog 中的媒体，不重新确认历史快照描述的
			// 旧 observation。
			s.writeRequestError(w, fault.New(fault.CodeConflict, false, nil))
			return
		}
	}
	if item.ContentVerificationState == catalog.ContentVerificationStateContentVerified {
		s.writeRequestError(w, fault.New(fault.CodeConflict, false, nil))
		return
	}
	observation, err := s.catalog.LookupObservationAt(r.Context(), resolvedPub.ID, item.SourceID, item.RelativePath)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	observationFingerprint := scanner.ObservationFingerprint(observation.Size, observation.MTimeNanos, observation.ContentVerificationState)
	idempotencyKey := fmt.Sprintf("verify-media:v3:%s:%s:%s:%s:%s", resolvedPub.ID, item.ID, item.SourceID, item.RelativePath, observationFingerprint)
	target := scanner.VerificationTarget{
		MediaID: item.ID, SourceID: item.SourceID, RelativePath: item.RelativePath,
		QueryPublicationID: resolvedPub.ID, ObservationFingerprint: observationFingerprint,
	}
	job, err := s.scanner.CreateVerificationScan(r.Context(), item.SourceID, session.PrincipalID, idempotencyKey, []scanner.VerificationTarget{target})
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, jobDTO(job))
	s.scanner.Start(job.ID)
}

// createDerivedAsset 请求生成或复用一个 DerivedAsset。总是返回持久 Job（缓存命中时
// Job 立即以 completed 态返回，不需要客户端区分"新生成"与"命中缓存"两种响应形状），
// 媒体尚未 content_verified 时拒绝，外部 Resolver 未配置时返回稳定 unavailable。生成
// 需要独立的 media.derive capability，不再复用只读的 media.read——只读媒体账户可以读
// 已生成资源（derivedAssetContent 仍检查 media.read），但不能触发新的生成工作。输入
// ContentBlob 从请求指定（或省略时当前 active）的 queryPublicationId 解析，不重新从
// active publication 寻找"当前 Blob"代替请求时刻的 Blob。
func (s *Server) createDerivedAsset(w http.ResponseWriter, r *http.Request) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.validateMutation(r, session); err != nil {
		s.writeRequestError(w, err)
		return
	}
	if s.derivedJob == nil || !s.derivedJob.Available() {
		s.writeRequestError(w, fault.New(fault.CodeDerivedAssetUnavailable, false, nil))
		return
	}
	var request api.DerivedAssetCreateRequest
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeDerivedAssetInvalid, "body", err))
		return
	}
	// 已知 transform 的白名单在创建时同步拒绝，避免为一个注定失败的请求消耗 Job 槽位；
	// 具体生成仍完全异步，白名单只是快速失败，不代替 Resolver 在真正生成时的最终校验。
	if request.TransformId != thumbnail.TransformID || request.TransformVersion != thumbnail.TransformVersion {
		s.writeRequestError(w, fault.WithField(fault.CodeDerivedAssetInvalid, "transformId", nil))
		return
	}
	requestedPub := r.URL.Query().Get("queryPublicationId")
	release, err := s.acquirePublicationLeaseIfExplicit(r, session, requestedPub)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	defer release()
	_, item, err := s.catalog.GetMediaAt(r.Context(), requestedPub, r.PathValue("mediaId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.authorizeSession(r, session, "media.derive", auth.ResourceScope{Kind: "source", ID: item.SourceID}); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	if item.ContentVerificationState != catalog.ContentVerificationStateContentVerified {
		s.writeRequestError(w, fault.New(fault.CodeContentNotVerified, true, nil))
		return
	}
	parameters := []byte("{}")
	if request.Parameters != nil {
		encoded, marshalErr := json.Marshal(*request.Parameters)
		if marshalErr != nil {
			s.writeRequestError(w, fault.WithField(fault.CodeDerivedAssetInvalid, "parameters", marshalErr))
			return
		}
		parameters = encoded
	}
	job, err := s.derivedJob.Create(r.Context(), derivedjob.Request{
		BlobAlgorithm: item.Algorithm, BlobDigest: item.Digest,
		TransformID: request.TransformId, TransformVersion: request.TransformVersion, Parameters: parameters,
	}, session.PrincipalID)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, jobDTO(job))
	s.startJob(job)
}

// derivedAssetContent 通过内容寻址 assetKey（不是 Catalog 内部 row ID）流式读取一个
// 已就绪 DerivedAsset 的正文，读取期间持有 derived.Service 的租约以防止与 GC 竞争。
func (s *Server) derivedAssetContent(w http.ResponseWriter, r *http.Request) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if s.derived == nil {
		s.writeRequestError(w, fault.New(fault.CodeNotFound, false, nil))
		return
	}
	blob, err := s.derived.InputBlob(r.Context(), r.PathValue("assetKey"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	allowed, authErr := s.auth.AuthorizeSession(r.Context(), session, "media.read", auth.ResourceScope{Kind: "global"})
	if authErr != nil {
		s.writeRequestError(w, fault.New(fault.CodeInternal, true, authErr))
		return
	}
	if !allowed {
		locations, locationErr := s.catalog.BlobLocations(r.Context(), blob)
		if locationErr != nil {
			s.writeRequestError(w, concealForbidden(locationErr))
			return
		}
		for _, location := range locations {
			allowed, authErr = s.auth.AuthorizeSession(r.Context(), session, "media.read", auth.ResourceScope{Kind: "source", ID: location.SourceID})
			if authErr != nil {
				s.writeRequestError(w, fault.New(fault.CodeInternal, true, authErr))
				return
			}
			if allowed {
				break
			}
		}
	}
	if !allowed {
		s.writeRequestError(w, fault.New(fault.CodeNotFound, false, nil))
		return
	}
	lease, err := s.derived.Open(r.Context(), r.PathValue("assetKey"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	defer lease.Close()
	etag := `"gallery-derived-` + lease.Asset.OutputDigest + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Type", lease.Asset.OutputMIME)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", strconv.FormatInt(lease.Asset.OutputSize, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, lease.File)
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

// acquirePublicationLeaseIfExplicit 是媒体/DerivedAsset 快照绑定读取的公共入口：
// requested 为空表示客户端未显式指定 queryPublicationId（current 模式，读当前 active
// publication，永不被 GC，不需要额外 lease）；非空表示显式快照模式，必须先验证该
// publication 真实存在，再建立短期 lease 覆盖本次请求剩余处理时间，防止在解析媒体、
// 读取正文期间被 GarbageCollect 回收——不得静默回退到 active publication。返回的
// release 函数在两种模式下都可安全无条件 defer 调用。
func (s *Server) acquirePublicationLeaseIfExplicit(r *http.Request, session auth.Session, requested string) (func(), error) {
	if requested == "" {
		return func() {}, nil
	}
	publication, err := s.catalog.PublicationByID(r.Context(), requested)
	if err != nil {
		return func() {}, err
	}
	authHash := queryservice.AuthorizationScope(session.PrincipalID, session.Capabilities)
	lease, err := s.catalog.AcquirePublicationLease(r.Context(), publication.ID, authHash)
	if err != nil {
		return func() {}, err
	}
	return func() { _ = lease.Close() }, nil
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
	result := api.RuleVersion{RuleSetId: value.RuleSetID, Version: value.Version, PackageHash: value.PackageHash, SemanticHash: value.SemanticHash, RuleIrHash: value.RuleIRHash, CreatedAt: value.CreatedAt}
	if value.ID != "" {
		result.Id = &value.ID
	}
	if value.PackageID != "" {
		result.PackageId = &value.PackageID
	}
	if value.Status != "" {
		status := api.RuleVersionStatus(value.Status)
		result.Status = &status
	}
	if value.NormalizationAlgorithmVersion != "" {
		result.NormalizationAlgorithmVersion = &value.NormalizationAlgorithmVersion
	}
	if value.CELProfileVersion != "" {
		result.CelProfileVersion = &value.CELProfileVersion
	}
	if value.CreatedBy != "" {
		result.CreatedBy = &value.CreatedBy
	}
	if value.ParentSemanticHash != "" {
		result.ParentSemanticHash = &value.ParentSemanticHash
	}
	if value.CompileError != "" {
		result.CompileError = &value.CompileError
	}
	if metadata := jsonObjectPointer(value.ParameterSchema); metadata != nil {
		result.ParameterSchema = metadata
	}
	if tests := jsonObjectSlicePointer(value.Tests); tests != nil {
		result.Tests = tests
	}
	if extensions := jsonObjectPointer(value.Extensions); extensions != nil {
		result.Extensions = extensions
	}
	if value.PublishedAt != nil {
		result.PublishedAt = value.PublishedAt
	}
	if value.DeprecatedAt != nil {
		result.DeprecatedAt = value.DeprecatedAt
	}
	executable := value.Executable
	result.Executable = &executable
	return result
}

func sourceRuleBindingDTO(value application.SourceRuleBinding) api.SourceRuleBinding {
	parameters := map[string]any{}
	decoder := json.NewDecoder(io.LimitReader(bytes.NewReader(value.Parameters), 1<<20))
	decoder.UseNumber()
	_ = decoder.Decode(&parameters)
	result := api.SourceRuleBinding{Id: value.ID, SourceId: value.SourceID, SemanticHash: value.SemanticHash, Parameters: parameters, Priority: value.Priority, RuleIrHash: value.RuleIRHash, CreatedAt: value.CreatedAt}
	if value.ParameterID != "" {
		result.ParameterId = &value.ParameterID
	}
	if value.ParameterRevision != 0 {
		result.ParameterRevision = &value.ParameterRevision
	}
	if value.ParameterHash != "" {
		result.ParameterHash = &value.ParameterHash
	}
	if value.Status != "" {
		status := api.SourceRuleBindingStatus(value.Status)
		result.Status = &status
	}
	if override := jsonObjectPointer(value.Override); override != nil {
		result.Override = override
	}
	if condition := jsonObjectPointer(value.Condition); condition != nil {
		result.Condition = condition
	}
	if !value.UpdatedAt.IsZero() {
		result.UpdatedAt = &value.UpdatedAt
	}
	return result
}

func jsonObjectPointer(raw []byte) *map[string]interface{} {
	if len(raw) == 0 {
		return nil
	}
	value := map[string]interface{}{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil
	}
	return &value
}

func jsonObjectSlicePointer(raw []byte) *[]map[string]interface{} {
	if len(raw) == 0 {
		return nil
	}
	value := []map[string]interface{}{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil
	}
	return &value
}

func jobDTO(value jobs.Job) api.Job {
	result := api.Job{
		Id: value.ID, Type: api.JobType(value.Type), Status: api.JobStatus(value.Status),
		Stage: value.Stage, Attempt: value.Attempt, CreatedAt: value.CreatedAt, StartedAt: value.StartedAt,
		FinishedAt: value.FinishedAt, UpdatedAt: value.UpdatedAt,
	}
	result.NextAttemptAt = value.NextAttemptAt
	if value.SourceID != "" {
		sourceID := api.SourceId(value.SourceID)
		result.SourceId = &sourceID
	}
	result.Progress.Current, result.Progress.Total, result.Progress.Sequence = value.ProgressCurrent, value.ProgressTotal, int64(value.ProgressSequence)
	result.Progress.Bytes = int64Ptr(value.ProgressBytes)
	result.Progress.Entities = int64Ptr(value.ProgressEntities)
	result.Progress.Estimated = boolPtr(value.ProgressEstimated)
	if value.ProgressPhase != "" {
		result.Progress.Phase = stringPtr(value.ProgressPhase)
	}
	if value.ProgressUnit != "" {
		result.Progress.Unit = stringPtr(value.ProgressUnit)
	}
	if value.ProgressMessage != "" {
		result.Progress.Message = stringPtr(value.ProgressMessage)
	}
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
	if value.RuleSemanticHash != "" {
		result.RuleSemanticHash = &value.RuleSemanticHash
	}
	if value.RuleParametersHash != "" {
		result.RuleParametersHash = &value.RuleParametersHash
	}
	if value.RuleIRHash != "" {
		result.RuleIrHash = &value.RuleIRHash
	}
	if value.CompilerVersion != "" {
		result.CompilerVersion = &value.CompilerVersion
	}
	if value.CELProfileVersion != "" {
		result.CelProfileVersion = &value.CELProfileVersion
	}
	if value.ExtensionRegistryVersion != "" {
		result.ExtensionRegistryVersion = &value.ExtensionRegistryVersion
	}
	if value.ResourceClass != "" {
		result.ResourceClass = &value.ResourceClass
	}
	if value.TargetResource != "" {
		result.TargetResource = &value.TargetResource
	}
	result.CancelRequested = boolPtr(value.CancelRequested)
	result.FailureRetryable = boolPtr(value.FailureRetryable)
	if value.Type == "scan" {
		// scanProfile 只从持久请求（request_json）读取最终实际执行的档案，不依赖进程内
		// 临时状态；Get/List 在服务重启后仍返回一致值。
		var request struct {
			ScanProfile string `json:"scanProfile,omitempty"`
		}
		if err := json.Unmarshal(value.RequestJSON, &request); err == nil && request.ScanProfile != "" {
			profile := api.JobScanProfile(request.ScanProfile)
			result.ScanProfile = &profile
		}
	}
	if value.Type == "derived" && value.Status == jobs.StatusCompleted {
		// derivedAssetKey 只从持久结果（result_json）读取，不缓存于进程内状态；Get/List
		// 在服务重启后仍返回一致值，且不会暴露 Catalog 内部 row ID 或绝对路径。
		var derivedResult struct {
			Key string `json:"Key"`
		}
		if err := json.Unmarshal(value.ResultJSON, &derivedResult); err == nil && derivedResult.Key != "" {
			result.DerivedAssetKey = &derivedResult.Key
		}
	}
	return result
}

func maintenanceJobDTO(value jobs.Job) api.MaintenanceJobResponse {
	var request maintenance.Request
	_ = json.Unmarshal(value.RequestJSON, &request)
	return api.MaintenanceJobResponse{
		Job: jobDTO(value),
		SpaceEstimate: api.SpaceEstimate{
			Operation:      api.SpaceEstimateOperation(request.Space.Operation),
			RequiredBytes:  request.Space.RequiredBytes,
			AvailableBytes: request.Space.FreeBytes,
			Sufficient:     request.Space.Sufficient,
			Conservative:   request.Space.Conservative,
		},
	}
}

func jobAttemptDTO(value jobs.Attempt) api.JobAttempt {
	result := api.JobAttempt{AttemptId: value.ID, JobId: value.JobID, Attempt: value.Attempt,
		ResourceClass: value.ResourceClass, Status: api.JobAttemptStatus(value.Status), ProgressSequence: int64(value.ProgressSequence),
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, StartedAt: value.StartedAt, HeartbeatAt: value.HeartbeatAt, FinishedAt: value.FinishedAt}
	if value.ErrorCode != "" {
		result.ErrorCode = stringPtr(value.ErrorCode)
	}
	result.ErrorRetryable = boolPtr(value.ErrorRetryable)
	return result
}

func sourceScanStateDTO(value watcherservice.State) api.SourceScanState {
	result := api.SourceScanState{SourceId: value.SourceID, Status: api.SourceScanStateStatus(value.Status), Dirty: value.Dirty,
		WatcherAvailable: value.WatcherAvailable, WatcherOverflow: value.WatcherOverflow, PendingHashCount: value.PendingHashCount,
		LastEventAt: value.LastEventAt, LastCheckedAt: value.LastCheckedAt, UpdatedAt: value.UpdatedAt}
	if value.CurrentJobID != "" {
		result.CurrentJobId = stringPtr(value.CurrentJobID)
	}
	if value.CurrentPublicationID != "" {
		result.CurrentPublicationId = stringPtr(value.CurrentPublicationID)
	}
	if value.BlockingIssueCode != "" {
		result.BlockingIssueCode = stringPtr(value.BlockingIssueCode)
	}
	return result
}

func stringPtr(value string) *string { return &value }
func int64Ptr(value int64) *int64    { return &value }
func boolPtr(value bool) *bool       { return &value }

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

func bindingIssueDTO(value application.BindingIssue) api.BindingIssue {
	result := api.BindingIssue{
		Id: value.ID, SourceId: value.SourceID, EntityType: api.BindingIssueEntityType(value.EntityType),
		SourceKey: value.SourceKey, Code: api.ErrorCode(value.Code), CandidateCount: value.CandidateCount,
		Status: api.BindingIssueStatus(value.Status), Version: value.Version,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
		Candidates: make([]api.BindingIssueCandidate, 0, len(value.Candidates)),
	}
	if value.StructureKind != "" {
		structureKind := api.BindingIssueStructureKind(value.StructureKind)
		result.StructureKind = &structureKind
	}
	if value.WorkSourceKey != "" {
		result.WorkSourceKey = &value.WorkSourceKey
	}
	if value.ProviderID != "" {
		result.ProviderId = &value.ProviderID
	}
	if value.ExternalID != "" {
		result.ExternalId = &value.ExternalID
	}
	if value.Resolution != "" {
		resolution := api.BindingIssueResolution(value.Resolution)
		result.Resolution = &resolution
	}
	if value.ResolvedTargetID != "" {
		result.ResolvedTargetId = &value.ResolvedTargetID
	}
	if value.ResolvedBy != "" {
		result.ResolvedBy = &value.ResolvedBy
	}
	if value.ResolvedAt != nil {
		resolvedAt := *value.ResolvedAt
		result.ResolvedAt = &resolvedAt
	}
	for _, candidate := range value.Candidates {
		result.Candidates = append(result.Candidates, api.BindingIssueCandidate{
			CandidateId: candidate.CandidateID, CandidateKind: api.BindingIssueCandidateCandidateKind(candidate.CandidateKind),
			MatchSignal: candidate.MatchSignal, MatchValue: candidate.MatchValue, Label: candidate.Label,
		})
	}
	return result
}

func creatorDTO(value creators.Creator) api.Creator {
	result := api.Creator{
		Id: value.ID, Name: value.Name, EffectiveId: value.EffectiveID,
		SourceCount: value.SourceCount, CreatedAt: value.CreatedAt,
	}
	if value.MergedInto != "" {
		merged := api.CanonicalCreatorId(value.MergedInto)
		result.MergedInto = &merged
	}
	return result
}

func creatorMergeDTO(value creators.MergeRecord, projectionJobID string) api.CreatorMerge {
	result := api.CreatorMerge{
		Id: value.ID, TargetCreatorId: value.TargetID,
		AbsorbedCreatorIds: append([]string(nil), value.AbsorbedIDs...),
		Status:             api.CreatorMergeStatus(value.Status), CreatedBy: value.CreatedBy, CreatedAt: value.CreatedAt,
	}
	if value.UndoneAt != nil {
		undone := *value.UndoneAt
		result.UndoneAt = &undone
	}
	if projectionJobID != "" {
		jobID := api.JobId(projectionJobID)
		result.ProjectionJobId = &jobID
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

// mediaDTO 把位置可用性（Available/LocationStatus）与内容确认状态
// （ContentVerificationState/Blob/VerifiedAt）作为两个正交维度分别表达：位置在线但内容
// 未确认时 Available 仍为 true、Blob 与 VerifiedAt 为 nil，不得把两者混为一谈。
func mediaDTO(publication catalog.Publication, value catalog.Media) api.PublishedMedia {
	var blob *api.ContentBlobRef
	if value.ContentVerificationState == catalog.ContentVerificationStateContentVerified {
		blob = &api.ContentBlobRef{Algorithm: api.ContentBlobRefAlgorithm(value.Algorithm), Digest: value.Digest}
	}
	result := api.PublishedMedia{
		Id: value.ID, WorkId: value.WorkID, Kind: value.Kind, MimeType: value.MIME, SizeBytes: value.Size, Blob: blob,
		Available: value.LocationStatus == "present", Ordinal: value.Ordinal, QueryPublicationId: publication.ID,
		ContentVerificationState: api.PublishedMediaContentVerificationState(value.ContentVerificationState),
	}
	if !value.VerifiedAt.IsZero() {
		verifiedAt := value.VerifiedAt
		result.VerifiedAt = &verifiedAt
	}
	return result
}

func (s *Server) authenticate(r *http.Request) (auth.Session, error) {
	if authorization := r.Header.Get("Authorization"); authorization != "" {
		scheme, value, ok := strings.Cut(authorization, " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(value) == "" {
			return auth.Session{}, fault.New(fault.CodeTokenInvalid, false, nil)
		}
		return s.auth.AuthenticateAPIToken(r.Context(), strings.TrimSpace(value))
	}
	cookie, err := r.Cookie(auth.CookieName)
	if err != nil {
		return auth.Session{}, fault.New(fault.CodeUnauthenticated, false, nil)
	}
	return s.auth.Authenticate(r.Context(), cookie.Value)
}

func (s *Server) requireCapability(r *http.Request, capability string) (auth.Session, error) {
	return s.requireCapabilityForScope(r, capability, auth.ResourceScope{Kind: "global"})
}

func (s *Server) requireCapabilityForScope(r *http.Request, capability string, scope auth.ResourceScope) (auth.Session, error) {
	session, err := s.authenticate(r)
	if err != nil {
		return auth.Session{}, err
	}
	if err := s.authorizeSession(r, session, capability, scope); err != nil {
		return auth.Session{}, err
	}
	return session, nil
}

func (s *Server) authorizeSession(r *http.Request, session auth.Session, capability string, scope auth.ResourceScope) error {
	allowed, authErr := s.auth.AuthorizeSession(r.Context(), session, capability, scope)
	if authErr != nil {
		return fault.New(fault.CodeInternal, true, authErr)
	}
	if !allowed {
		return fault.New(fault.CodeForbidden, false, nil)
	}
	return nil
}

// authorizeJob 把 Job 的读取/控制权限绑定到它实际拥有的资源。Source Job 继承
// Library→Source 授权；不绑定 Source 的维护、备份、恢复和 Derived Job 只接受相应的
// global capability，避免 scan.run 被用来控制管理员维护任务。
func (s *Server) authorizeJob(r *http.Request, session auth.Session, job jobs.Job, mutate bool) error {
	capability := "library.read"
	scope := auth.ResourceScope{Kind: "global"}
	if job.SourceID != "" {
		scope = auth.ResourceScope{Kind: "source", ID: job.SourceID}
		if mutate {
			capability = "scan.run"
		}
		return s.authorizeSession(r, session, capability, scope)
	}
	switch job.Type {
	case "control_backup":
		capability = "admin.backup"
	case "control_restore":
		capability = "admin.restore"
	case "catalog_gc", "catalog_checkpoint", "catalog_vacuum", "derived_gc":
		capability = "admin.maintenance"
	case "derived":
		if mutate {
			capability = "media.derive"
		} else {
			capability = "media.read"
		}
	case "overlay_projection":
		if mutate {
			capability = "overlays.write"
		} else {
			capability = "library.read"
		}
	default:
		return fault.New(fault.CodeForbidden, false, nil)
	}
	return s.authorizeSession(r, session, capability, scope)
}

func concealForbidden(err error) error {
	var structured *fault.Error
	if errors.As(err, &structured) && structured.Code == fault.CodeForbidden {
		return fault.New(fault.CodeNotFound, false, nil)
	}
	return err
}

func sessionSummary(session auth.Session) api.SessionSummary {
	return api.SessionSummary{
		Id: session.ID, PrincipalId: session.PrincipalID, CreatedAt: session.CreatedAt,
		AuthMethod: api.SessionSummaryAuthMethod(session.AuthMethod), ClientLabel: session.ClientLabel,
		ExpiresAt: session.ExpiresAt, LastSeenAt: session.LastSeenAt, Revoked: session.RevokedAt != nil,
	}
}

func decodeJSON(r *http.Request, target any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errors.New("Content-Type 必须是 application/json")
	}
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

func hostGuard(server *Server, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := server.validateHost(r); err != nil {
			writeFault(w, asFault(err), statusForFault(err))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) validateHost(r *http.Request) error {
	return auth.ValidateHostAllowed(r, s.allowedHosts)
}

func (s *Server) validateOrigin(r *http.Request) error {
	return auth.ValidateOriginAllowed(r, s.allowedHosts)
}

func (s *Server) validateMutation(r *http.Request, session auth.Session) error {
	if session.TokenID != "" {
		return nil
	}
	return auth.ValidateMutationAllowed(r, session.CSRFToken, s.allowedHosts)
}

// loginRateSubject 使用直连对端 IP 作为登录限流主体。RemoteAddr 的端口通常每个连接都会变化，
// 不能进入限流键；服务当前不支持反向代理部署，因此也不能信任 X-Forwarded-For 等可伪造请求头。
func loginRateSubject(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return ip.String()
	}
	return strings.ToLower(host)
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
		fault.CodeOverlayFactInvalid, fault.CodeDerivedAssetInvalid,
		fault.CodeRuleSchemaInvalid, fault.CodeRuleParameterInvalid, fault.CodeRuleCompile,
		fault.CodeRuleCELLimit, fault.CodeRuleDryRun, fault.CodeRuleImpact, fault.CodeRuleEval:
		return http.StatusBadRequest
	case fault.CodeRuleImportInvalid:
		return http.StatusBadRequest
	case fault.CodeUnauthenticated, fault.CodePairingInvalid, fault.CodePairingExpired,
		fault.CodeInvalidCredentials, fault.CodeTokenInvalid, fault.CodeTokenExpired:
		return http.StatusUnauthorized
	case fault.CodeRateLimited:
		return http.StatusTooManyRequests
	case fault.CodeForbidden, fault.CodeHostRejected, fault.CodeOriginRejected, fault.CodeCSRFInvalid:
		return http.StatusForbidden
	case fault.CodeNotFound, fault.CodeBackupNotFound:
		return http.StatusNotFound
	case fault.CodeBackupCorrupt, fault.CodeBackupIncompatible:
		return http.StatusConflict
	case fault.CodeConflict, fault.CodeRuleDraftConflict, fault.CodeRulePackageConflict,
		fault.CodeRuleParameterConflict, fault.CodeRulePublishBlocked, fault.CodeRuleRollbackBlocked,
		fault.CodeRuleVersionInUse, fault.CodeRuleBindingConflict:
		return http.StatusConflict
	case fault.CodeLANAlreadyInitialized:
		return http.StatusConflict
	case fault.CodeLANOwnerRequired:
		return http.StatusPreconditionRequired
	case fault.CodeJobStateConflict, fault.CodeJobProgressRegression, fault.CodeJobRetryExhausted, fault.CodeJobCancellationRequested,
		fault.CodeScanAlreadyRunning, fault.CodeCatalogCandidateInvalid, fault.CodeMaintenanceBlocked:
		return http.StatusConflict
	case fault.CodeBindingReviewRequired:
		return http.StatusConflict
	case fault.CodeContentChangedDuringHash:
		return http.StatusConflict
	case fault.CodeContentNotVerified:
		return http.StatusConflict
	case fault.CodeRangeInvalid:
		return http.StatusRequestedRangeNotSatisfiable
	case fault.CodeMediaOffline, fault.CodeSourceUnavailable, fault.CodeSourceReadFailed, fault.CodeContentDisappeared:
		return http.StatusServiceUnavailable
	case fault.CodeWatcherOverflow, fault.CodeSourceIdentityChanged, fault.CodeSourcePermissionDenied:
		return http.StatusServiceUnavailable
	case fault.CodeDerivedAssetUnavailable, fault.CodeExternalToolUnavailable:
		return http.StatusServiceUnavailable
	case fault.CodeDiskSpaceInsufficient:
		return http.StatusInsufficientStorage
	case fault.CodeAppDirsOverlap, fault.CodeSourceRootsOverlap:
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func requestLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		route := r.Pattern
		if _, path, ok := strings.Cut(route, " "); ok {
			route = path
		}
		if route == "" {
			route = redactedRequestPath(r.URL.Path)
		}
		logger.InfoContext(r.Context(), "http_request", "method", r.Method, "route", route)
	})
}

func redactedRequestPath(path string) string {
	const prefix = "/api/v1/public/shares/"
	if !strings.HasPrefix(path, prefix) {
		return path
	}
	remainder := strings.TrimPrefix(path, prefix)
	if slash := strings.IndexByte(remainder, '/'); slash >= 0 {
		return prefix + "{credential}" + remainder[slash:]
	}
	return prefix + "{credential}"
}
