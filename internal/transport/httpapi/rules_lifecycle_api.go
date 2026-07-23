package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/auth"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/rules"
)

func (s *Server) listRulePackages(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "rules.read"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	items, err := s.data.ListRulePackages(r.Context())
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	result := make([]any, 0, len(items))
	for _, item := range items {
		result = append(result, rulePackageMap(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": result})
}

func (s *Server) listRuleExamples(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "rules.read"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	items := make([]map[string]any, 0)
	for _, example := range rules.BuiltInRuleExamples() {
		compiled, err := rules.CompilePackage(example.PackageJSON)
		if err != nil {
			s.writeRequestError(w, fault.New(fault.CodeInternal, false, err))
			return
		}
		items = append(items, map[string]any{
			"id": example.ID, "name": example.Name, "category": example.Category,
			"packageHash": compiled.PackageHash, "semanticHash": compiled.SemanticHash,
			"package": json.RawMessage(compiled.Canonical),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) testRuleExample(w http.ResponseWriter, r *http.Request) {
	session, err := s.requireCapability(r, "rules.debug")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := auth.ValidateMutation(r, session.CSRFToken); err != nil {
		s.writeRequestError(w, err)
		return
	}
	example, ok := rules.BuiltInRuleExampleByID(r.PathValue("exampleId"))
	if !ok {
		s.writeRequestError(w, fault.New(fault.CodeNotFound, false, nil))
		return
	}
	var request struct {
		Parameters json.RawMessage    `json:"parameters"`
		Sample     *rules.DryRunInput `json:"sample"`
	}
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeRuleDryRun, "body", err))
		return
	}
	result, err := rules.RunBuiltInRuleExample(r.Context(), example.ID, request.Parameters, request.Sample)
	if err != nil {
		s.writeRequestError(w, ruleRequestFault(fault.CodeRuleDryRun, "sample", err))
		return
	}
	compiled, err := rules.CompilePackage(example.PackageJSON)
	if err != nil {
		s.writeRequestError(w, fault.New(fault.CodeInternal, false, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"example": map[string]any{
			"id": example.ID, "name": example.Name, "category": example.Category,
			"packageHash": compiled.PackageHash, "semanticHash": compiled.SemanticHash,
			"package": json.RawMessage(compiled.Canonical),
		},
		"packageHash": compiled.PackageHash, "semanticHash": compiled.SemanticHash, "result": result,
	})
}

func (s *Server) createRulePackage(w http.ResponseWriter, r *http.Request) {
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
		RuleSetID   string `json:"ruleSetId"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	item, err := s.data.CreateRulePackage(r.Context(), request.RuleSetID, request.Name, request.Description, session.PrincipalID)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rulePackageMap(item))
}

func (s *Server) getRulePackage(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "rules.read"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	item, err := s.data.GetRulePackage(r.Context(), r.PathValue("packageId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rulePackageMap(item))
}

func (s *Server) getRuleDraft(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "rules.read"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	draft, err := s.data.GetRuleDraft(r.Context(), r.PathValue("packageId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ruleDraftMap(draft))
}

func (s *Server) saveRuleDraft(w http.ResponseWriter, r *http.Request) {
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
		Content          json.RawMessage `json:"content"`
		Format           string          `json:"format"`
		BaseSemanticHash string          `json:"baseSemanticHash"`
		ExpectedRevision *int            `json:"expectedRevision"`
	}
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	content, err := decodeRuleContent(request.Content, request.Format)
	if err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "content", err))
		return
	}
	expected := ifMatchRevision(r)
	if request.ExpectedRevision != nil {
		expected = *request.ExpectedRevision
	}
	draft, err := s.data.SaveRuleDraft(r.Context(), r.PathValue("packageId"), content, request.Format, request.BaseSemanticHash, expected, session.PrincipalID)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", draft.Revision))
	writeJSON(w, http.StatusOK, ruleDraftMap(draft))
}

func (s *Server) validateRuleDraft(w http.ResponseWriter, r *http.Request) {
	session, err := s.requireCapability(r, "rules.write")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := auth.ValidateMutation(r, session.CSRFToken); err != nil {
		s.writeRequestError(w, err)
		return
	}
	result, err := s.data.ValidateRuleDraft(r.Context(), r.PathValue("packageId"), ifMatchRevision(r), session.PrincipalID)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", result.Draft.Revision))
	writeJSON(w, http.StatusOK, map[string]any{"draft": ruleDraftMap(result.Draft), "valid": result.Valid, "diagnostics": result.Diagnostics, "validation": result.Validation})
}

func (s *Server) publishRuleDraft(w http.ResponseWriter, r *http.Request) {
	session, err := s.requireCapability(r, "rules.publish")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := auth.ValidateMutation(r, session.CSRFToken); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request struct {
		ExpectedRevision *int   `json:"expectedRevision"`
		Reason           string `json:"reason"`
	}
	if err := decodeOptionalJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	expected := ifMatchRevision(r)
	if request.ExpectedRevision != nil {
		expected = *request.ExpectedRevision
	}
	version, err := s.data.PublishRuleDraft(r.Context(), r.PathValue("packageId"), expected, session.PrincipalID, request.Reason)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ruleVersionMap(version))
}

func (s *Server) deprecateRulePackage(w http.ResponseWriter, r *http.Request) {
	session, err := s.requireCapability(r, "rules.publish")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := auth.ValidateMutation(r, session.CSRFToken); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if err := decodeOptionalJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	item, err := s.data.SetRulePackageStatus(r.Context(), r.PathValue("packageId"), application.RulePackageDeprecated, session.PrincipalID, request.Reason, ifMatchRevision(r))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rulePackageMap(item))
}

func (s *Server) deleteRulePackage(w http.ResponseWriter, r *http.Request) {
	session, err := s.requireCapability(r, "rules.publish")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := auth.ValidateMutation(r, session.CSRFToken); err != nil {
		s.writeRequestError(w, err)
		return
	}
	item, err := s.data.SetRulePackageStatus(r.Context(), r.PathValue("packageId"), application.RulePackageDeleted, session.PrincipalID, "soft delete", ifMatchRevision(r))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rulePackageMap(item))
}

func (s *Server) listRuleAudits(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "rules.read"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	items, err := s.data.ListRuleAudits(r.Context(), r.PathValue("packageId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{"id": item.ID, "packageId": item.PackageID, "action": item.Action, "fromSemanticHash": item.FromSemanticHash, "toSemanticHash": item.ToSemanticHash, "reason": item.Reason, "actorId": item.ActorID, "createdAt": item.CreatedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": result})
}

func (s *Server) listRuleVersions(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "rules.read"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	items, err := s.data.ListRuleVersions(r.Context(), application.RuleVersionListOptions{PackageID: r.PathValue("packageId"), Status: r.URL.Query().Get("status")})
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	result := make([]any, 0, len(items))
	for _, item := range items {
		result = append(result, ruleVersionMap(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": result})
}

func (s *Server) rollbackRulePackage(w http.ResponseWriter, r *http.Request) {
	session, err := s.requireCapability(r, "rules.publish")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := auth.ValidateMutation(r, session.CSRFToken); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request struct {
		TargetSemanticHash string `json:"targetSemanticHash"`
		ExpectedRevision   *int   `json:"expectedRevision"`
		Reason             string `json:"reason"`
		ConfirmImpact      bool   `json:"confirmImpact"`
	}
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	expected := ifMatchRevision(r)
	if request.ExpectedRevision != nil {
		expected = *request.ExpectedRevision
	}
	version, err := s.data.RollbackRulePackage(r.Context(), r.PathValue("packageId"), request.TargetSemanticHash, expected, session.PrincipalID, request.Reason, request.ConfirmImpact)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ruleVersionMap(version))
}

func (s *Server) deprecateRuleVersion(w http.ResponseWriter, r *http.Request) {
	session, err := s.requireCapability(r, "rules.publish")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := auth.ValidateMutation(r, session.CSRFToken); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if err := decodeOptionalJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	version, err := s.data.DeprecateRuleVersion(r.Context(), r.PathValue("semanticHash"), session.PrincipalID, request.Reason)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ruleVersionMap(version))
}

func (s *Server) diffRuleVersions(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "rules.read"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request struct {
		OldSemanticHash string `json:"oldSemanticHash"`
		NewSemanticHash string `json:"newSemanticHash"`
	}
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	result, err := s.data.DiffRuleVersions(r.Context(), request.OldSemanticHash, request.NewSemanticHash)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) exportRuleVersion(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "rules.read"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	version, err := s.data.GetRuleVersion(r.Context(), r.PathValue("semanticHash"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "" || format == "json" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(version.Canonical)
		return
	}
	// 导入格式的身份事实仍是 canonical JSON；服务端不生成可能改变语义的 YAML/TOML。
	s.writeRequestError(w, fault.WithField(fault.CodeValidation, "format", fmt.Errorf("仅支持 json 导出")))
}

func (s *Server) createRuleParameterSet(w http.ResponseWriter, r *http.Request) {
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
		Name         string          `json:"name"`
		SemanticHash string          `json:"semanticHash"`
		Parameters   json.RawMessage `json:"parameters"`
	}
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	item, err := s.data.CreateRuleParameterSet(r.Context(), request.Name, request.SemanticHash, request.Parameters, session.PrincipalID)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ruleParameterMap(item))
}

func (s *Server) listRuleParameterSets(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "rules.read"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	items, err := s.data.ListRuleParameterSets(r.Context(), r.URL.Query().Get("semanticHash"), r.URL.Query().Get("status"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, ruleParameterMap(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"parameterSets": result})
}

func (s *Server) getRuleParameterSet(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "rules.read"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	item, err := s.data.GetRuleParameterSet(r.Context(), r.PathValue("parameterId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ruleParameterMap(item))
}

func (s *Server) updateRuleParameterSet(w http.ResponseWriter, r *http.Request) {
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
		Parameters       json.RawMessage `json:"parameters"`
		ExpectedRevision *int            `json:"expectedRevision"`
	}
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	expected := ifMatchRevision(r)
	if request.ExpectedRevision != nil {
		expected = *request.ExpectedRevision
	}
	item, err := s.data.UpdateRuleParameterSet(r.Context(), r.PathValue("parameterId"), request.Parameters, expected, session.PrincipalID)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", item.CurrentRevision))
	writeJSON(w, http.StatusOK, ruleParameterMap(item))
}

func (s *Server) impactRuleParameterSet(w http.ResponseWriter, r *http.Request) {
	session, err := s.requireCapability(r, "rules.read")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := auth.ValidateMutation(r, session.CSRFToken); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request struct {
		Parameters json.RawMessage `json:"parameters"`
	}
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeRuleParameterInvalid, "body", err))
		return
	}
	result, err := s.data.ImpactRuleParameterSet(r.Context(), r.PathValue("parameterId"), request.Parameters)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) copyRuleParameterSet(w http.ResponseWriter, r *http.Request) {
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
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	item, err := s.data.CopyRuleParameterSet(r.Context(), r.PathValue("parameterId"), request.Name, session.PrincipalID)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ruleParameterMap(item))
}

func (s *Server) deprecateRuleParameterSet(w http.ResponseWriter, r *http.Request) {
	session, err := s.requireCapability(r, "rules.write")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := auth.ValidateMutation(r, session.CSRFToken); err != nil {
		s.writeRequestError(w, err)
		return
	}
	item, err := s.data.DeprecateRuleParameterSet(r.Context(), r.PathValue("parameterId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ruleParameterMap(item))
}

func (s *Server) updateSourceRuleBinding(w http.ResponseWriter, r *http.Request) {
	session, err := s.authenticate(r)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := auth.ValidateMutation(r, session.CSRFToken); err != nil {
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
	existing, err := s.data.GetSourceRuleBinding(r.Context(), r.PathValue("bindingId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	if err := s.authorizeSession(r, session, "rules.write", auth.ResourceScope{Kind: "source", ID: existing.SourceID}); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	item, err := s.data.SetSourceRuleBindingStatus(r.Context(), r.PathValue("bindingId"), request.Status)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sourceRuleBindingMap(item))
}

func (s *Server) getEffectiveRuleBinding(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapabilityForScope(r, "rules.read", auth.ResourceScope{Kind: "source", ID: r.PathValue("sourceId")}); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	item, err := s.data.BindingForSource(r.Context(), r.PathValue("sourceId"))
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sourceRuleBindingMap(item))
}

func (s *Server) importRulePackage(w http.ResponseWriter, r *http.Request) {
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
		Format  string          `json:"format"`
		Content json.RawMessage `json:"content"`
	}
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	content, err := decodeRuleContent(request.Content, request.Format)
	if err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "content", err))
		return
	}
	result, err := rules.ImportRulePackage(request.Format, content)
	if err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeRuleImportInvalid, "content", err))
		return
	}
	_ = session
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) diffRulePackages(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "rules.read"); err != nil {
		s.writeRequestError(w, err)
		return
	}
	var request struct {
		Before json.RawMessage `json:"before"`
		After  json.RawMessage `json:"after"`
	}
	if err := decodeJSON(r, &request); err != nil {
		s.writeRequestError(w, fault.WithField(fault.CodeValidation, "body", err))
		return
	}
	life := s.rules
	result, err := life.DiffRulePackages(request.Before, request.After)
	if err != nil {
		s.writeRequestError(w, ruleRequestFault(fault.CodeRuleImpact, "after", err))
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) explainRulePackage(w http.ResponseWriter, r *http.Request) {
	result, err := s.runRuleExplain(r, "rules.debug")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) traceRulePackage(w http.ResponseWriter, r *http.Request) {
	result, err := s.runRuleTrace(r, "rules.debug")
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) runRuleExplain(r *http.Request, capability string) (rules.ExplainResult, error) {
	session, err := s.requireCapability(r, capability)
	if err != nil {
		return rules.ExplainResult{}, err
	}
	if err := auth.ValidateMutation(r, session.CSRFToken); err != nil {
		return rules.ExplainResult{}, err
	}
	var request struct {
		Package      json.RawMessage   `json:"package"`
		SemanticHash string            `json:"semanticHash"`
		Parameters   json.RawMessage   `json:"parameters"`
		Sample       rules.DryRunInput `json:"sample"`
	}
	if err := decodeJSON(r, &request); err != nil {
		return rules.ExplainResult{}, fault.WithField(fault.CodeRuleDryRun, "body", err)
	}
	packageJSON, err := s.resolveRulePackage(r, request.Package, request.SemanticHash)
	if err != nil {
		return rules.ExplainResult{}, err
	}
	return s.rules.Explain(r.Context(), packageJSON, request.Parameters, request.Sample)
}

func (s *Server) runRuleTrace(r *http.Request, capability string) (map[string]any, error) {
	session, err := s.requireCapability(r, capability)
	if err != nil {
		return nil, err
	}
	if err := auth.ValidateMutation(r, session.CSRFToken); err != nil {
		return nil, err
	}
	var request struct {
		Package      json.RawMessage   `json:"package"`
		SemanticHash string            `json:"semanticHash"`
		Parameters   json.RawMessage   `json:"parameters"`
		Sample       rules.DryRunInput `json:"sample"`
	}
	if err := decodeJSON(r, &request); err != nil {
		return nil, fault.WithField(fault.CodeRuleDryRun, "body", err)
	}
	packageJSON, err := s.resolveRulePackage(r, request.Package, request.SemanticHash)
	if err != nil {
		return nil, err
	}
	result, err := s.rules.DryRun(r.Context(), packageJSON, request.Parameters, request.Sample)
	if err != nil {
		return nil, ruleRequestFault(fault.CodeRuleDryRun, "sample", err)
	}
	return map[string]any{"ruleVersion": result.RuleVersion, "ruleIrHash": result.RuleIRHash, "trace": result.Trace, "issues": result.Issues}, nil
}

func (s *Server) resolveRulePackage(r *http.Request, packageJSON json.RawMessage, semanticHash string) ([]byte, error) {
	if len(packageJSON) > 0 {
		return packageJSON, nil
	}
	if semanticHash == "" {
		return nil, fault.WithField(fault.CodeValidation, "package", nil)
	}
	version, err := s.data.GetRuleVersion(r.Context(), semanticHash)
	if err != nil {
		return nil, err
	}
	return version.Canonical, nil
}

func decodeOptionalJSON(r *http.Request, target any) error {
	if r.Body == nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(data)) == "" {
		return nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("请求必须只包含一个 JSON 值")
	}
	return nil
}

func decodeRuleContent(raw json.RawMessage, format string) ([]byte, error) {
	if len(raw) == 0 {
		return nil, errors.New("content 不能为空")
	}
	if strings.ToLower(strings.TrimSpace(format)) != "json" {
		var text string
		if json.Unmarshal(raw, &text) == nil {
			return []byte(text), nil
		}
	}
	return append([]byte(nil), raw...), nil
}

func ifMatchRevision(r *http.Request) int {
	header := strings.TrimSpace(r.Header.Get("If-Match"))
	if header == "" {
		return -1
	}
	header = strings.Trim(header, "\"")
	value, err := strconv.Atoi(header)
	if err != nil || value < 0 {
		return -1
	}
	return value
}

func rulePackageMap(value application.RulePackage) map[string]any {
	return map[string]any{
		"id": value.ID, "ruleSetId": value.RuleSetID, "name": value.Name, "description": value.Description,
		"status": value.Status, "currentSemanticHash": value.CurrentSemanticHash, "latestValidSemanticHash": value.LatestValidSemanticHash,
		"draftId": value.DraftID, "extensionRequirements": json.RawMessage(defaultJSON(value.ExtensionRequirements, []byte("{}"))),
		"createdBy": value.CreatedBy, "createdAt": value.CreatedAt, "updatedAt": value.UpdatedAt, "revision": value.Revision,
	}
}

func ruleDraftMap(value application.RuleDraft) map[string]any {
	content := any(string(value.Content))
	if value.SourceFormat == "json" && json.Valid(value.Content) {
		content = json.RawMessage(value.Content)
	}
	return map[string]any{
		"id": value.ID, "packageId": value.PackageID, "baseSemanticHash": value.BaseSemanticHash, "content": content,
		"format": value.SourceFormat, "validationStatus": value.ValidationStatus,
		"diagnostics": json.RawMessage(defaultJSON(value.Diagnostics, []byte("[]"))), "revision": value.Revision,
		"savedBy": value.SavedBy, "createdAt": value.CreatedAt, "updatedAt": value.UpdatedAt,
	}
}

func ruleVersionMap(value application.RuleVersion) map[string]any {
	return map[string]any{
		"id": value.ID, "packageId": value.PackageID, "ruleSetId": value.RuleSetID, "version": value.Version,
		"packageHash": value.PackageHash, "semanticHash": value.SemanticHash, "ruleIrHash": value.RuleIRHash,
		"status": value.Status, "normalizationAlgorithmVersion": value.NormalizationAlgorithmVersion,
		"celProfileVersion": value.CELProfileVersion, "parameterSchema": json.RawMessage(defaultJSON(value.ParameterSchema, []byte("{}"))),
		"tests": json.RawMessage(defaultJSON(value.Tests, []byte("[]"))), "extensions": json.RawMessage(defaultJSON(value.Extensions, []byte("{}"))),
		"parentSemanticHash": value.ParentSemanticHash, "createdBy": value.CreatedBy, "publishedAt": value.PublishedAt,
		"deprecatedAt": value.DeprecatedAt, "executable": value.Executable, "compileError": value.CompileError, "createdAt": value.CreatedAt,
	}
}

func ruleParameterMap(value application.RuleParameterSet) map[string]any {
	return map[string]any{
		"id": value.ID, "name": value.Name, "semanticHash": value.SemanticHash, "currentRevision": value.CurrentRevision,
		"currentHash": value.CurrentHash, "status": value.Status, "parameters": json.RawMessage(defaultJSON(value.Parameters, []byte("{}"))),
		"createdBy": value.CreatedBy, "createdAt": value.CreatedAt, "updatedAt": value.UpdatedAt,
	}
}

func sourceRuleBindingMap(value application.SourceRuleBinding) map[string]any {
	return map[string]any{
		"id": value.ID, "sourceId": value.SourceID, "semanticHash": value.SemanticHash,
		"parameters": json.RawMessage(defaultJSON(value.Parameters, []byte("{}"))), "priority": value.Priority,
		"ruleIrHash": value.RuleIRHash, "parameterId": value.ParameterID, "parameterRevision": value.ParameterRevision,
		"parameterHash": value.ParameterHash, "override": json.RawMessage(defaultJSON(value.Override, []byte("{}"))),
		"condition": json.RawMessage(defaultJSON(value.Condition, []byte("{}"))), "status": value.Status,
		"createdAt": value.CreatedAt, "updatedAt": value.UpdatedAt,
	}
}

func defaultJSON(value, fallback []byte) []byte {
	if len(value) == 0 || !json.Valid(value) {
		return fallback
	}
	return value
}
