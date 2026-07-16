package rules

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type Lifecycle struct {
	cel      *celRuntime
	packages sync.Map
	bindings sync.Map
}

type ValidationResult struct {
	CanonicalJSON []byte `json:"canonicalJson"`
	PackageHash   string `json:"packageHash"`
	SemanticHash  string `json:"semanticHash"`
}

type CompileResult struct {
	ValidationResult
	RuleIRHash          string `json:"ruleIrHash"`
	CanonicalParameters []byte `json:"canonicalParameters"`
	IR                  RuleIR `json:"ruleIr"`
	CacheHit            bool   `json:"cacheHit"`
}

type DryRunInput struct {
	Path     string       `json:"path"`
	Files    []DryRunFile `json:"files"`
	Metadata any          `json:"metadata"`
}

type DryRunFile struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Metadata any    `json:"metadata"`
}

type DryRunResult struct {
	Work   DryRunWork  `json:"work"`
	Trace  []TraceStep `json:"trace"`
	Issues []RuleIssue `json:"issues"`
}

type DryRunWork struct {
	StableKey  string        `json:"stableKey"`
	Title      string        `json:"title"`
	ExternalID string        `json:"externalId,omitempty"`
	Creator    string        `json:"creator,omitempty"`
	Tags       []string      `json:"tags"`
	Ignored    bool          `json:"ignored"`
	Media      []DryRunMedia `json:"media"`
	CoverPath  string        `json:"coverPath,omitempty"`
}

type DryRunMedia struct {
	StableKey  string `json:"stableKey"`
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	MIME       string `json:"mime"`
	Ordinal    int    `json:"ordinal"`
	Hidden     bool   `json:"hidden"`
	CoverScore int    `json:"coverScore"`
}

type TraceStep struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	InputPointer   string `json:"inputPointer,omitempty"`
	CandidateCount int    `json:"candidateCount"`
	Selected       bool   `json:"selected"`
	ReasonCode     string `json:"reasonCode"`
	Cost           uint64 `json:"cost"`
	DurationMicros int64  `json:"durationMicros"`
}

type RuleIssue struct {
	Code     string `json:"code"`
	Path     string `json:"path,omitempty"`
	Required bool   `json:"required"`
}

type ImpactResult struct {
	Fields         []string `json:"fields"`
	Actions        []string `json:"actions"`
	FullRescan     bool     `json:"fullRescan"`
	Reproject      bool     `json:"reproject"`
	RebuildSearch  bool     `json:"rebuildSearch"`
	RebuildDerived bool     `json:"rebuildDerived"`
	BindingReview  bool     `json:"bindingReview"`
}

func NewLifecycle() (*Lifecycle, error) {
	runtime, err := newCELRuntime()
	if err != nil {
		return nil, err
	}
	return &Lifecycle{cel: runtime}, nil
}

func (l *Lifecycle) Validate(input []byte) (ValidationResult, error) {
	compiled, err := l.compilePackage(input)
	if err != nil {
		return ValidationResult{}, err
	}
	return ValidationResult{CanonicalJSON: append([]byte(nil), compiled.Canonical...), PackageHash: compiled.PackageHash, SemanticHash: compiled.SemanticHash}, nil
}

func (l *Lifecycle) Compile(input, parameters []byte) (CompileResult, error) {
	compiled, err := l.compilePackage(input)
	if err != nil {
		return CompileResult{}, err
	}
	ir, irHash, canonicalParameters, err := CompileBinding(compiled, parameters)
	if err != nil {
		return CompileResult{}, withField("/parameters", err)
	}
	cacheKey := compiled.SemanticHash + "\x00" + irHash
	_, loaded := l.bindings.LoadOrStore(cacheKey, ir)
	return CompileResult{
		ValidationResult: ValidationResult{CanonicalJSON: append([]byte(nil), compiled.Canonical...), PackageHash: compiled.PackageHash, SemanticHash: compiled.SemanticHash},
		RuleIRHash:       irHash, CanonicalParameters: canonicalParameters, IR: ir, CacheHit: loaded,
	}, nil
}

func (l *Lifecycle) DryRun(ctx context.Context, input, parameters []byte, sample DryRunInput) (DryRunResult, error) {
	compiled, err := l.Compile(input, parameters)
	if err != nil {
		return DryRunResult{}, err
	}
	var params map[string]any
	if err := decodeJSONValue(compiled.CanonicalParameters, &params); err != nil {
		return DryRunResult{}, err
	}
	if err := validateDryRunInput(sample); err != nil {
		return DryRunResult{}, withField("/sample", err)
	}
	return l.evaluate(ctx, compiled.IR, params, sample)
}

func (l *Lifecycle) EvaluateIR(ctx context.Context, ir RuleIR, parameters []byte, sample DryRunInput) (DryRunResult, error) {
	var params map[string]any
	if len(parameters) == 0 {
		parameters = []byte("{}")
	}
	if err := decodeJSONValue(parameters, &params); err != nil {
		return DryRunResult{}, withField("/parameters", err)
	}
	if err := validateDryRunInput(sample); err != nil {
		return DryRunResult{}, withField("/sample", err)
	}
	return l.evaluate(ctx, ir, params, sample)
}

func (l *Lifecycle) Impact(before, after []byte) (ImpactResult, error) {
	left, err := l.compilePackage(before)
	if err != nil {
		return ImpactResult{}, err
	}
	right, err := l.compilePackage(after)
	if err != nil {
		return ImpactResult{}, err
	}
	if left.SemanticHash == right.SemanticHash {
		return ImpactResult{Actions: []string{"none"}}, nil
	}
	result := ImpactResult{}
	if left.IR.WorkDirectoryGlob != right.IR.WorkDirectoryGlob || left.IR.WorkStableKey != right.IR.WorkStableKey {
		result.Fields = append(result.Fields, "source_identity")
		result.FullRescan, result.BindingReview = true, true
	}
	if left.IR.WorkTitle != right.IR.WorkTitle || primitiveKindsChanged(left.IR, right.IR, "selector", "fallback", "metadata_map") {
		result.Fields = append(result.Fields, "effective_fields")
		result.Reproject, result.RebuildSearch = true, true
	}
	if left.IR.MediaGlob != right.IR.MediaGlob || left.IR.MediaKind != right.IR.MediaKind || left.IR.MediaMIME != right.IR.MediaMIME || primitiveKindsChanged(left.IR, right.IR, "media_classify", "media_order", "condition") {
		result.Fields = append(result.Fields, "media")
		result.FullRescan, result.Reproject = true, true
	}
	if primitiveKindsChanged(left.IR, right.IR, "cover_candidate") {
		result.Fields = append(result.Fields, "cover")
		result.Reproject, result.RebuildDerived = true, true
	}
	if len(result.Fields) == 0 {
		result.Fields = append(result.Fields, "runtime_semantics")
		result.FullRescan = true
	}
	if result.FullRescan {
		result.Actions = append(result.Actions, "full_rescan")
	}
	if result.BindingReview {
		result.Actions = append(result.Actions, "binding_review")
	}
	if result.Reproject {
		result.Actions = append(result.Actions, "reproject")
	}
	if result.RebuildSearch {
		result.Actions = append(result.Actions, "rebuild_search")
	}
	if result.RebuildDerived {
		result.Actions = append(result.Actions, "rebuild_derived")
	}
	sort.Strings(result.Fields)
	return result, nil
}

func (l *Lifecycle) compilePackage(input []byte) (CompiledPackage, error) {
	compiled, err := CompilePackage(input)
	if err != nil {
		return CompiledPackage{}, err
	}
	if cached, ok := l.packages.Load(compiled.PackageHash); ok {
		return cached.(CompiledPackage), nil
	}
	l.packages.Store(compiled.PackageHash, compiled)
	return compiled, nil
}

func (l *Lifecycle) evaluate(ctx context.Context, ir RuleIR, params map[string]any, sample DryRunInput) (DryRunResult, error) {
	result := DryRunResult{
		Work:  DryRunWork{StableKey: sample.Path, Title: path.Base(sample.Path), Tags: []string{}, Media: []DryRunMedia{}},
		Trace: []TraceStep{}, Issues: []RuleIssue{},
	}
	expressions := make(map[string]IRExpression, len(ir.CELExpressions))
	for _, expression := range ir.CELExpressions {
		expressions[expression.ID] = expression
	}
	for _, primitive := range ir.Primitives {
		switch primitive.Kind {
		case "selector", "fallback":
			if err := applySelector(primitive, sample.Metadata, &result); err != nil {
				return DryRunResult{}, err
			}
		case "metadata_map":
			if err := applyMetadataMap(primitive, sample.Metadata, &result); err != nil {
				return DryRunResult{}, err
			}
		case "stable_key":
			applyStableKey(primitive, sample.Metadata, &result)
		case "condition":
			if err := l.applyCondition(ctx, primitive, expressions, params, sample, nil, &result); err != nil {
				return DryRunResult{}, err
			}
		}
	}
	for _, file := range sample.Files {
		media, matched, err := l.classifyFile(ctx, ir, expressions, params, sample, file, &result)
		if err != nil {
			return DryRunResult{}, err
		}
		if matched {
			result.Work.Media = append(result.Work.Media, media)
		}
	}
	orderMedia(ir, result.Work.Media)
	for index := range result.Work.Media {
		result.Work.Media[index].Ordinal = index
	}
	for _, media := range result.Work.Media {
		if result.Work.CoverPath == "" || media.CoverScore > coverScore(result.Work.Media, result.Work.CoverPath) {
			result.Work.CoverPath = media.Path
		}
	}
	return result, nil
}

func (l *Lifecycle) classifyFile(ctx context.Context, ir RuleIR, expressions map[string]IRExpression, params map[string]any, sample DryRunInput, file DryRunFile, result *DryRunResult) (DryRunMedia, bool, error) {
	media := DryRunMedia{StableKey: file.Path, Path: file.Path}
	matched := false
	for _, primitive := range ir.Primitives {
		if primitive.Kind != "media_classify" {
			continue
		}
		config := rawConfig(primitive.Config)
		glob := stringConfig(config, "glob")
		ok, err := path.Match(glob, path.Base(file.Path))
		if err != nil {
			return DryRunMedia{}, false, err
		}
		if !ok {
			continue
		}
		if expressionID := stringConfig(config, "condition"); expressionID != "" {
			passed, trace, err := l.evalPredicate(ctx, expressions[expressionID], params, sample, &file)
			if err != nil {
				return DryRunMedia{}, false, err
			}
			trace.ID = primitive.ID + ":" + expressionID
			result.Trace = append(result.Trace, trace)
			if !passed {
				continue
			}
		}
		media.Kind, media.MIME, matched = stringConfig(config, "kind"), stringConfig(config, "mime"), true
		break
	}
	if !matched {
		return DryRunMedia{}, false, nil
	}
	for _, primitive := range ir.Primitives {
		config := rawConfig(primitive.Config)
		switch primitive.Kind {
		case "cover_candidate":
			ok, _ := path.Match(stringConfig(config, "glob"), path.Base(file.Path))
			if ok {
				media.CoverScore = intConfig(config, "score")
			}
		case "condition":
			scope, effect := stringConfig(config, "scope"), stringConfig(config, "effect")
			if scope != "media" {
				continue
			}
			passed, trace, err := l.evalPredicate(ctx, expressions[stringConfig(config, "expression")], params, sample, &file)
			if err != nil {
				return DryRunMedia{}, false, err
			}
			trace.ID = primitive.ID
			result.Trace = append(result.Trace, trace)
			if passed && effect == "ignore" {
				return DryRunMedia{}, false, nil
			}
			if passed && effect == "hide" {
				media.Hidden = true
			}
		case "stable_key":
			if stringConfig(config, "target") == "media" {
				if pointer := stringConfig(config, "pointer"); pointer != "" {
					if value, ok := resolvePointer(file.Metadata, pointer); ok {
						media.StableKey = fmt.Sprint(value)
					}
				}
			}
		}
	}
	return media, true, nil
}

func (l *Lifecycle) applyCondition(ctx context.Context, primitive IRPrimitive, expressions map[string]IRExpression, params map[string]any, sample DryRunInput, file *DryRunFile, result *DryRunResult) error {
	config := rawConfig(primitive.Config)
	if stringConfig(config, "scope") != "work" {
		return nil
	}
	passed, trace, err := l.evalPredicate(ctx, expressions[stringConfig(config, "expression")], params, sample, file)
	if err != nil {
		return err
	}
	trace.ID = primitive.ID
	result.Trace = append(result.Trace, trace)
	if passed && (stringConfig(config, "effect") == "ignore" || stringConfig(config, "effect") == "hide") {
		result.Work.Ignored = true
	}
	return nil
}

func (l *Lifecycle) evalPredicate(ctx context.Context, expression IRExpression, params map[string]any, sample DryRunInput, file *DryRunFile) (bool, TraceStep, error) {
	if expression.ID == "" {
		return false, TraceStep{}, fmt.Errorf("条件引用不存在的 CEL expression")
	}
	fileValue := any(map[string]any{})
	if file != nil {
		fileValue = map[string]any{"path": file.Path, "size": file.Size, "metadata": file.Metadata}
	}
	evaluation, err := l.cel.evaluate(ctx, expression, map[string]any{
		"source": map[string]any{"mode": "dry_run"}, "path": sample.Path, "file": fileValue,
		"metadata": celCompatible(nilToMap(sample.Metadata)), "candidate": celCompatible(fileValue), "params": celCompatible(params),
	})
	if err != nil {
		return false, TraceStep{}, err
	}
	passed, ok := evaluation.Value.(bool)
	if !ok {
		return false, TraceStep{}, fmt.Errorf("CEL predicate 未返回 bool")
	}
	return passed, TraceStep{Kind: "condition", Selected: passed, ReasonCode: boolReason(passed), Cost: evaluation.Cost, DurationMicros: evaluation.Duration.Microseconds()}, nil
}

func applySelector(primitive IRPrimitive, metadata any, result *DryRunResult) error {
	config := rawConfig(primitive.Config)
	target := stringConfig(config, "target")
	pointers := stringListConfig(config, "pointers")
	if len(pointers) == 0 && stringConfig(config, "pointer") != "" {
		pointers = []string{stringConfig(config, "pointer")}
	}
	var selected any
	selectedPointer := ""
	for _, pointer := range pointers {
		if value, ok := resolvePointer(metadata, pointer); ok && value != nil && fmt.Sprint(value) != "" {
			selected, selectedPointer = value, pointer
			break
		}
	}
	if selected == nil {
		selected = config["default"]
	}
	required := boolConfig(config, "required")
	if selected == nil {
		result.Issues = append(result.Issues, RuleIssue{Code: "RULE_SELECTOR_MISSING", Path: firstString(pointers), Required: required})
		result.Trace = append(result.Trace, TraceStep{ID: primitive.ID, Kind: primitive.Kind, InputPointer: firstString(pointers), CandidateCount: len(pointers), ReasonCode: "missing"})
		if required {
			return fmt.Errorf("selector %s 缺少必需字段", primitive.ID)
		}
		return nil
	}
	assignTarget(&result.Work, target, selected)
	result.Trace = append(result.Trace, TraceStep{ID: primitive.ID, Kind: primitive.Kind, InputPointer: selectedPointer, CandidateCount: len(pointers), Selected: true, ReasonCode: "selected"})
	return nil
}

func applyMetadataMap(primitive IRPrimitive, metadata any, result *DryRunResult) error {
	config := rawConfig(primitive.Config)
	fields, _ := config["fields"].(map[string]any)
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, target := range keys {
		pointers := anyStringList(fields[target])
		for _, pointer := range pointers {
			if value, ok := resolvePointer(metadata, pointer); ok {
				assignTarget(&result.Work, target, value)
				result.Trace = append(result.Trace, TraceStep{ID: primitive.ID + ":" + target, Kind: primitive.Kind, InputPointer: pointer, CandidateCount: len(pointers), Selected: true, ReasonCode: "selected"})
				break
			}
		}
	}
	return nil
}

func applyStableKey(primitive IRPrimitive, metadata any, result *DryRunResult) {
	config := rawConfig(primitive.Config)
	if stringConfig(config, "target") != "work" {
		return
	}
	if pointer := stringConfig(config, "pointer"); pointer != "" {
		if value, ok := resolvePointer(metadata, pointer); ok && fmt.Sprint(value) != "" {
			result.Work.StableKey = stringConfig(config, "prefix") + fmt.Sprint(value)
		}
	}
}

func assignTarget(work *DryRunWork, target string, value any) {
	switch target {
	case "title":
		work.Title = fmt.Sprint(value)
	case "external_id":
		work.ExternalID = fmt.Sprint(value)
	case "creator":
		work.Creator = fmt.Sprint(value)
	case "tags":
		work.Tags = anyStringList(value)
	}
}

func orderMedia(ir RuleIR, media []DryRunMedia) {
	direction := "asc"
	for _, primitive := range ir.Primitives {
		if primitive.Kind == "media_order" {
			direction = stringConfig(rawConfig(primitive.Config), "direction")
		}
	}
	sort.SliceStable(media, func(i, j int) bool {
		if direction == "desc" {
			return media[i].Path > media[j].Path
		}
		return media[i].Path < media[j].Path
	})
}

func validateDryRunInput(input DryRunInput) error {
	if input.Path == "" || strings.HasPrefix(input.Path, "/") || strings.Contains(input.Path, "..") {
		return fmt.Errorf("Dry Run path 无效")
	}
	encoded, err := json.Marshal(input.Metadata)
	if err != nil {
		return err
	}
	if len(encoded) > CELProfileV1.InputJSONBytes {
		return fmt.Errorf("RULE_INPUT_LIMIT")
	}
	if exceedsArrayLimit(input.Metadata) {
		return fmt.Errorf("CEL_ARRAY_LIMIT")
	}
	return nil
}

func exceedsArrayLimit(value any) bool {
	switch typed := value.(type) {
	case []any:
		if len(typed) > CELProfileV1.ArrayElements {
			return true
		}
		for _, item := range typed {
			if exceedsArrayLimit(item) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if exceedsArrayLimit(item) {
				return true
			}
		}
	}
	return false
}

func resolvePointer(value any, pointer string) (any, bool) {
	if pointer == "" {
		return value, true
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, false
	}
	current := value
	for _, token := range strings.Split(pointer[1:], "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		switch typed := current.(type) {
		case map[string]any:
			var ok bool
			current, ok = typed[token]
			if !ok {
				return nil, false
			}
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, false
			}
			current = typed[index]
		default:
			return nil, false
		}
	}
	return current, true
}

func rawConfig(input json.RawMessage) map[string]any {
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	_ = decoder.Decode(&result)
	return result
}
func stringConfig(config map[string]any, key string) string {
	value, _ := config[key].(string)
	return value
}
func boolConfig(config map[string]any, key string) bool { value, _ := config[key].(bool); return value }
func intConfig(config map[string]any, key string) int {
	switch value := config[key].(type) {
	case json.Number:
		result, _ := strconv.Atoi(value.String())
		return result
	case float64:
		return int(value)
	case int:
		return value
	}
	return 0
}
func stringListConfig(config map[string]any, key string) []string { return anyStringList(config[key]) }
func anyStringList(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []string:
		return append([]string(nil), typed...)
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
		return result
	}
	return nil
}
func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
func boolReason(value bool) string {
	if value {
		return "matched"
	}
	return "not_matched"
}
func nilToMap(value any) any {
	if value == nil {
		return map[string]any{}
	}
	return value
}
func coverScore(items []DryRunMedia, path string) int {
	for _, item := range items {
		if item.Path == path {
			return item.CoverScore
		}
	}
	return -1
}

func primitiveKindsChanged(left, right RuleIR, kinds ...string) bool {
	wanted := make(map[string]struct{}, len(kinds))
	for _, kind := range kinds {
		wanted[kind] = struct{}{}
	}
	filter := func(ir RuleIR) []IRPrimitive {
		var result []IRPrimitive
		for _, item := range ir.Primitives {
			if _, ok := wanted[item.Kind]; ok {
				result = append(result, item)
			}
		}
		return result
	}
	return !bytes.Equal(mustJSON(filter(left)), mustJSON(filter(right))) || !bytes.Equal(mustJSON(left.CELExpressions), mustJSON(right.CELExpressions))
}

func decodeJSONValue(input []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	return decoder.Decode(target)
}

func celCompatible(value any) any {
	switch typed := value.(type) {
	case json.Number:
		if integer, err := strconv.ParseInt(typed.String(), 10, 64); err == nil {
			return integer
		}
		if number, err := strconv.ParseFloat(typed.String(), 64); err == nil {
			return number
		}
		return typed.String()
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = celCompatible(item)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = celCompatible(item)
		}
		return result
	default:
		return value
	}
}
