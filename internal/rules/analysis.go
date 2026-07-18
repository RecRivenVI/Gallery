package rules

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

type DiffChangeType string

const (
	DiffAdded    DiffChangeType = "added"
	DiffRemoved  DiffChangeType = "removed"
	DiffModified DiffChangeType = "modified"
)

type RuleDiffEntry struct {
	Path                 string         `json:"path"`
	Change               DiffChangeType `json:"change"`
	OldSummary           string         `json:"oldSummary,omitempty"`
	NewSummary           string         `json:"newSummary,omitempty"`
	ImpactCategory       string         `json:"impactCategory"`
	ParameterCompatible  bool           `json:"parameterCompatible"`
	BindingReview        bool           `json:"bindingReview"`
	RequiresRescan       bool           `json:"requiresRescan"`
	RequiresReprojection bool           `json:"requiresReprojection"`
}

type RuleVersionDiff struct {
	OldSemanticHash     string          `json:"oldSemanticHash"`
	NewSemanticHash     string          `json:"newSemanticHash"`
	OldPackageHash      string          `json:"oldPackageHash"`
	NewPackageHash      string          `json:"newPackageHash"`
	Category            string          `json:"category"`
	ParameterCompatible bool            `json:"parameterCompatible"`
	BindingReview       bool            `json:"bindingReview"`
	Entries             []RuleDiffEntry `json:"entries"`
}

type ExplainField struct {
	Field              string   `json:"field"`
	FinalValue         any      `json:"finalValue,omitempty"`
	SourceStep         string   `json:"sourceStep,omitempty"`
	InputPointers      []string `json:"inputPointers"`
	Fallbacks          []string `json:"fallbacks"`
	RejectedCandidates []string `json:"rejectedCandidates"`
	ReasonCode         string   `json:"reasonCode"`
}

type ExplainResult struct {
	RuleVersion string         `json:"ruleVersion"`
	RuleIRHash  string         `json:"ruleIrHash"`
	Fields      []ExplainField `json:"fields"`
	Trace       []TraceStep    `json:"trace"`
}

// DiffRulePackages 对规范包做结构化 diff。它只返回受限摘要，避免把完整 metadata
// 或大 extension payload 作为公开 diff 响应。
func (l *Lifecycle) DiffRulePackages(before, after []byte) (RuleVersionDiff, error) {
	left, err := l.compilePackage(before)
	if err != nil {
		return RuleVersionDiff{}, err
	}
	right, err := l.compilePackage(after)
	if err != nil {
		return RuleVersionDiff{}, err
	}
	var leftValue, rightValue map[string]any
	if err := decodeJSONValue(left.Canonical, &leftValue); err != nil {
		return RuleVersionDiff{}, err
	}
	if err := decodeJSONValue(right.Canonical, &rightValue); err != nil {
		return RuleVersionDiff{}, err
	}
	entries := make([]RuleDiffEntry, 0)
	diffValues(leftValue, rightValue, "", &entries, leftValue, rightValue)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	result := RuleVersionDiff{
		OldSemanticHash: left.SemanticHash, NewSemanticHash: right.SemanticHash,
		OldPackageHash: left.PackageHash, NewPackageHash: right.PackageHash,
		Category: "NO_ACTION", ParameterCompatible: true, Entries: entries,
	}
	var fullRescan, partialRescan, reproject bool
	for _, entry := range entries {
		if !entry.ParameterCompatible {
			result.ParameterCompatible = false
		}
		if entry.BindingReview {
			result.BindingReview = true
		}
		switch entry.ImpactCategory {
		case "RESCAN_FULL":
			fullRescan = true
		case "RESCAN_PARTIAL":
			partialRescan = true
		case "REPROJECT":
			reproject = true
		}
	}
	if result.BindingReview {
		result.Category = "BINDING_REVIEW"
	} else if fullRescan {
		result.Category = "RESCAN_FULL"
	} else if partialRescan {
		result.Category = "RESCAN_PARTIAL"
	} else if reproject {
		result.Category = "REPROJECT"
	}
	return result, nil
}

func diffValues(left, right any, pointer string, entries *[]RuleDiffEntry, leftRoot, rightRoot map[string]any) {
	diffValuesPresent(left, right, true, true, pointer, entries, leftRoot, rightRoot)
}

func diffValuesPresent(left, right any, leftPresent, rightPresent bool, pointer string, entries *[]RuleDiffEntry, leftRoot, rightRoot map[string]any) {
	if !leftPresent {
		addDiff(entries, pointer, DiffAdded, nil, right, false, rightPresent, leftRoot, rightRoot)
		return
	}
	if !rightPresent {
		addDiff(entries, pointer, DiffRemoved, left, nil, true, false, leftRoot, rightRoot)
		return
	}
	leftObject, leftOK := left.(map[string]any)
	rightObject, rightOK := right.(map[string]any)
	if leftOK && rightOK {
		keys := make(map[string]struct{}, len(leftObject)+len(rightObject))
		for key := range leftObject {
			keys[key] = struct{}{}
		}
		for key := range rightObject {
			keys[key] = struct{}{}
		}
		ordered := make([]string, 0, len(keys))
		for key := range keys {
			ordered = append(ordered, key)
		}
		sort.Strings(ordered)
		for _, key := range ordered {
			// package_hash 和 semantic_hash 是编译器派生字段，不是用户规则
			// 输入。把它们纳入 diff 会把每个真正的局部变化错误升级成全量重扫。
			if key == "package_hash" || key == "semantic_hash" {
				continue
			}
			child := pointer + "/" + escapePointer(key)
			leftValue, leftExists := leftObject[key]
			rightValue, rightExists := rightObject[key]
			diffValuesPresent(leftValue, rightValue, leftExists, rightExists, child, entries, leftRoot, rightRoot)
		}
		return
	}
	leftArray, leftOK := left.([]any)
	rightArray, rightOK := right.([]any)
	if leftOK && rightOK {
		max := len(leftArray)
		if len(rightArray) > max {
			max = len(rightArray)
		}
		for index := 0; index < max; index++ {
			child := pointer + "/" + strconv.Itoa(index)
			var oldValue, newValue any
			if index < len(leftArray) {
				oldValue = leftArray[index]
			}
			if index < len(rightArray) {
				newValue = rightArray[index]
			}
			diffValuesPresent(oldValue, newValue, index < len(leftArray), index < len(rightArray), child, entries, leftRoot, rightRoot)
		}
		return
	}
	if !bytes.Equal(mustJSON(left), mustJSON(right)) {
		addDiff(entries, pointer, DiffModified, left, right, true, true, leftRoot, rightRoot)
	}
}

func addDiff(entries *[]RuleDiffEntry, pointer string, change DiffChangeType, oldValue, newValue any, oldPresent, newPresent bool, leftRoot, rightRoot map[string]any) {
	category := classifyDiffPath(pointer, leftRoot, rightRoot)
	entry := RuleDiffEntry{
		Path: pointer, Change: change, OldSummary: summarizeDiffValue(oldValue, oldPresent), NewSummary: summarizeDiffValue(newValue, newPresent),
		ImpactCategory: category, ParameterCompatible: category != "INVALID", BindingReview: category == "BINDING_REVIEW",
		RequiresRescan:       category == "RESCAN_FULL" || category == "RESCAN_PARTIAL" || category == "BINDING_REVIEW",
		RequiresReprojection: category == "REPROJECT",
	}
	if strings.HasPrefix(pointer, "/parameter_schema") {
		entry.ParameterCompatible = false
	}
	*entries = append(*entries, entry)
}

func classifyDiffPath(pointer string, leftRoot, rightRoot map[string]any) string {
	switch {
	case pointer == "/tests" || strings.HasPrefix(pointer, "/tests/"):
		return "NO_ACTION"
	case pointer == "/ui_metadata" || strings.HasPrefix(pointer, "/ui_metadata/"):
		return "NO_ACTION"
	case strings.HasPrefix(pointer, "/parameter_schema"):
		return "BINDING_REVIEW"
	case pointer == "/extensions" || strings.HasPrefix(pointer, "/extensions/"):
		if extensionSemanticAt(pointer, leftRoot) || extensionSemanticAt(pointer, rightRoot) {
			return "RESCAN_FULL"
		}
		return "NO_ACTION"
	case strings.HasPrefix(pointer, "/provider_namespaces"):
		return "BINDING_REVIEW"
	case strings.HasPrefix(pointer, "/compiler_requirement"), strings.HasPrefix(pointer, "/cel_profile_version"), strings.HasPrefix(pointer, "/normalization_algorithm_version"):
		return "RESCAN_FULL"
	case strings.HasPrefix(pointer, "/description"), strings.HasPrefix(pointer, "/name"), strings.HasPrefix(pointer, "/display"), strings.HasPrefix(pointer, "/icon"):
		return "NO_ACTION"
	case strings.HasPrefix(pointer, "/cel_expressions"):
		return "RESCAN_FULL"
	case strings.HasPrefix(pointer, "/primitives/"):
		kind := primitiveKindAt(pointer, leftRoot)
		if kind == "" {
			kind = primitiveKindAt(pointer, rightRoot)
		}
		switch kind {
		case "path_match", "stable_key":
			return "BINDING_REVIEW"
		case "media_order":
			return "RESCAN_PARTIAL"
		case "cover_candidate":
			return "REPROJECT"
		case "selector", "fallback", "metadata_map":
			return "REPROJECT"
		default:
			return "RESCAN_FULL"
		}
	default:
		return "RESCAN_FULL"
	}
}

func extensionSemanticAt(pointer string, root map[string]any) bool {
	value, ok := root["extensions"].(map[string]any)
	if !ok {
		return false
	}
	trimmed := strings.TrimPrefix(pointer, "/extensions/")
	if trimmed == "" {
		for _, item := range value {
			if extensionValueSemantic(item) {
				return true
			}
		}
		return false
	}
	parts := strings.Split(trimmed, "/")
	name := strings.ReplaceAll(strings.ReplaceAll(parts[0], "~1", "/"), "~0", "~")
	return extensionValueSemantic(value[name])
}

func extensionValueSemantic(value any) bool {
	fields, ok := value.(map[string]any)
	if !ok {
		return false
	}
	semantic, ok := fields["semantic"].(bool)
	return ok && semantic
}

func primitiveKindAt(pointer string, root map[string]any) string {
	parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	if len(parts) < 2 || parts[0] != "primitives" {
		return ""
	}
	index, err := strconv.Atoi(parts[1])
	if err != nil {
		return ""
	}
	items, _ := root["primitives"].([]any)
	if index < 0 || index >= len(items) {
		return ""
	}
	item, _ := items[index].(map[string]any)
	kind, _ := item["kind"].(string)
	return kind
}

func summarizeValue(value any) string {
	if value == nil {
		return "<absent>"
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "<invalid>"
	}
	canonical, err := CanonicalJSON(encoded)
	if err != nil {
		return "<invalid>"
	}
	if len(canonical) > 256 {
		return string(canonical[:256]) + "…"
	}
	return string(canonical)
}

func summarizeDiffValue(value any, present bool) string {
	if !present {
		return "<absent>"
	}
	if value == nil {
		return "null"
	}
	return summarizeValue(value)
}

func (l *Lifecycle) Explain(ctx context.Context, input, parameters []byte, sample DryRunInput) (ExplainResult, error) {
	compiled, err := l.Compile(input, parameters)
	if err != nil {
		return ExplainResult{}, err
	}
	var params map[string]any
	if err := decodeJSONValue(compiled.CanonicalParameters, &params); err != nil {
		return ExplainResult{}, err
	}
	if err := validateDryRunInput(sample); err != nil {
		return ExplainResult{}, withField("/sample", err)
	}
	result, err := l.evaluate(ctx, compiled.IR, params, sample)
	if err != nil {
		return ExplainResult{}, err
	}
	fields := []ExplainField{
		{Field: "work.stable_key", FinalValue: result.Work.StableKey},
		{Field: "work.title", FinalValue: result.Work.Title},
		{Field: "work.external_id", FinalValue: result.Work.ExternalID},
		{Field: "work.provider_id", FinalValue: result.Work.ProviderID},
		{Field: "work.creator", FinalValue: result.Work.Creator},
		{Field: "work.tags", FinalValue: result.Work.Tags},
		{Field: "work.hidden", FinalValue: result.Work.Ignored},
		{Field: "work.cover", FinalValue: result.Work.CoverPath},
		{Field: "work.media", FinalValue: result.Work.Media},
	}
	for index := range fields {
		fields[index].InputPointers, fields[index].Fallbacks, fields[index].RejectedCandidates, fields[index].ReasonCode, fields[index].SourceStep = explainTrace(fields[index].Field, result.Trace)
	}
	return ExplainResult{RuleVersion: compiled.SemanticHash, RuleIRHash: compiled.RuleIRHash, Fields: fields, Trace: result.Trace}, nil
}

func explainTrace(field string, trace []TraceStep) ([]string, []string, []string, string, string) {
	var pointers, fallbacks, rejected []string
	reason, step := "default", ""
	target := strings.TrimPrefix(field, "work.")
	for _, item := range trace {
		if item.InputPointer != "" {
			pointers = append(pointers, item.InputPointer)
		}
		if item.Selected {
			if step == "" {
				step = item.ID
			}
			reason = item.ReasonCode
		} else if item.ID != "" {
			rejected = append(rejected, item.ID)
		}
		if strings.Contains(item.ID, target) || (target == "title" && item.Kind == "selector") || (target == "tags" && item.Kind == "metadata_map") {
			if !item.Selected {
				fallbacks = append(fallbacks, item.ID)
			}
		}
	}
	return uniqueStrings(pointers), uniqueStrings(fallbacks), uniqueStrings(rejected), reason, step
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
