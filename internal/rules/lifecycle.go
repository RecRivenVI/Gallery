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
	"time"

	"github.com/RecRivenVI/gallery/internal/querytext"
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
	RuleVersion string      `json:"ruleVersion,omitempty"`
	RuleIRHash  string      `json:"ruleIrHash,omitempty"`
	Work        DryRunWork  `json:"work"`
	Trace       []TraceStep `json:"trace"`
	Issues      []RuleIssue `json:"issues"`
}

type DryRunWork struct {
	StableKey  string `json:"stableKey"`
	Title      string `json:"title"`
	ExternalID string `json:"externalId,omitempty"`
	ProviderID string `json:"providerId,omitempty"`
	Creator    string `json:"creator,omitempty"`
	// CreatorStableKey 是规则为 SourceCreator occurrence 生成的稳定身份候选。
	// 它只供扫描/Binding 链使用，不扩张当前 Dry Run 公共 DTO；Trace/Explain 仍会
	// 记录该字段由哪个原语产生，但不会泄露 metadata 原值。
	CreatorStableKey string `json:"-"`
	// Description 与 SourceURL 是来源自述的作品描述与原始链接。真实规则为每个平台声明了它们的
	// 回退链（例如 pixiv 的 `caption`→`text`、通用的 `postUrl`→…→`source.url`），此前这两个
	// target 在编译期被放行、在求值期被静默丢弃。
	Description string        `json:"description,omitempty"`
	SourceURL   string        `json:"sourceUrl,omitempty"`
	Tags        []string      `json:"tags"`
	Ignored     bool          `json:"ignored"`
	Media       []DryRunMedia `json:"media"`
	CoverPath   string        `json:"coverPath,omitempty"`
	Badges      []DryRunBadge `json:"badges,omitempty"`
	// Date 是规则解析出的作品发布时间。它只能由 `work_date` 原语产出，因此要么带有完整的
	// instant + 原始输入 + 解析器版本，要么整体缺失——不存在「有原始串但没有 instant」的中间态。
	Date *DryRunWorkDate `json:"date,omitempty"`
}

// DryRunWorkDate 是作品发布时间的对外形态。RawValue 与 ParserVersion 是 `规范/06` 对时间排序的
// 明确要求：解析规则一旦变化，必须能识别出历史结果由旧规则产生，而不是让两代解析静默混用。
type DryRunWorkDate struct {
	// Instant 是 RFC3339 形式的 UTC 时刻。
	Instant string `json:"instant"`
	// RawValue 是产生该时刻的原始输入，逐字保留。
	RawValue string `json:"rawValue"`
	// Source 说明来自哪个 JSON Pointer，或 `$path`。
	Source string `json:"source"`
	// ParserVersion 是产生该 Instant 的解析规则版本。
	ParserVersion string `json:"parserVersion"`
}

// DryRunBadge 是规则为该作品派生的角标。它是 Source-derived 展示事实：随重扫重新计算，
// 不进 control.db，也不由客户端自行推导条件。
type DryRunBadge struct {
	ID              string `json:"id"`
	Order           int    `json:"order"`
	Position        string `json:"position"`
	Label           string `json:"label"`
	Color           string `json:"color,omitempty"`
	Background      string `json:"background,omitempty"`
	Border          string `json:"border,omitempty"`
	ColorLight      string `json:"colorLight,omitempty"`
	BackgroundLight string `json:"backgroundLight,omitempty"`
	BorderLight     string `json:"borderLight,omitempty"`
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
	ID             string   `json:"id"`
	Kind           string   `json:"kind"`
	InputPointer   string   `json:"inputPointer,omitempty"`
	InputSummary   string   `json:"inputSummary,omitempty"`
	OutputSummary  string   `json:"outputSummary,omitempty"`
	CandidateCount int      `json:"candidateCount"`
	Selected       bool     `json:"selected"`
	ReasonCode     string   `json:"reasonCode"`
	Cost           uint64   `json:"cost"`
	DurationMicros int64    `json:"durationMicros"`
	Warnings       []string `json:"warnings,omitempty"`
	ErrorPath      string   `json:"errorPath,omitempty"`
}

type RuleIssue struct {
	Code     string `json:"code"`
	Path     string `json:"path,omitempty"`
	Required bool   `json:"required"`
}

type ImpactResult struct {
	Category        string   `json:"category"`
	ReasonCodes     []string `json:"reasonCodes"`
	Fields          []string `json:"fields"`
	Actions         []string `json:"actions"`
	AffectedSources []string `json:"affectedSources"`
	EntityTypes     []string `json:"entityTypes"`
	BlockPublish    bool     `json:"blockPublish"`
	ManualConfirm   bool     `json:"manualConfirmation"`
	EstimatedJob    string   `json:"estimatedJob,omitempty"`
	PartialRescan   bool     `json:"partialRescan"`
	OldHash         string   `json:"oldHash,omitempty"`
	NewHash         string   `json:"newHash,omitempty"`
	TraceSummary    []string `json:"traceSummary"`
	FullRescan      bool     `json:"fullRescan"`
	Reproject       bool     `json:"reproject"`
	RebuildSearch   bool     `json:"rebuildSearch"`
	RebuildDerived  bool     `json:"rebuildDerived"`
	BindingReview   bool     `json:"bindingReview"`
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
	result, err := l.evaluate(ctx, compiled.IR, params, sample)
	if err != nil {
		return DryRunResult{}, err
	}
	result.RuleVersion, result.RuleIRHash = compiled.SemanticHash, compiled.RuleIRHash
	return result, nil
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
	if trimmed := bytes.TrimSpace(before); len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		right, err := l.compilePackage(after)
		if err != nil {
			return ImpactResult{}, err
		}
		return ImpactResult{
			Category:        "RESCAN_FULL",
			ReasonCodes:     []string{"initial_rule_version"},
			Fields:          []string{"runtime_semantics"},
			Actions:         []string{"full_rescan"},
			AffectedSources: []string{},
			EntityTypes:     []string{"creator", "media", "work"},
			NewHash:         right.SemanticHash,
			TraceSummary:    []string{"initial_rule_version"},
			FullRescan:      true,
			EstimatedJob:    "scan_all_sources",
		}, nil
	}
	left, err := l.compilePackage(before)
	if err != nil {
		return ImpactResult{}, err
	}
	right, err := l.compilePackage(after)
	if err != nil {
		return ImpactResult{}, err
	}
	result := ImpactResult{
		ReasonCodes: []string{}, Fields: []string{}, Actions: []string{}, AffectedSources: []string{},
		EntityTypes: []string{}, TraceSummary: []string{}, OldHash: left.SemanticHash, NewHash: right.SemanticHash,
	}
	if left.SemanticHash == right.SemanticHash {
		result.Category = "NO_ACTION"
		result.ReasonCodes = []string{"runtime_identity_unchanged"}
		result.Actions = []string{"none"}
		return result, nil
	}
	if !bytes.Equal(left.ParameterSchema, right.ParameterSchema) {
		result.Fields = append(result.Fields, "parameter_schema")
		result.BindingReview = true
		result.EntityTypes = append(result.EntityTypes, "binding", "work", "media")
		result.ReasonCodes = append(result.ReasonCodes, "parameter_schema_changed")
	}
	leftPackage, _ := decodeObject(left.Canonical)
	rightPackage, _ := decodeObject(right.Canonical)
	if !bytes.Equal(mustJSON(leftPackage["provider_namespaces"]), mustJSON(rightPackage["provider_namespaces"])) {
		result.Fields = append(result.Fields, "provider_namespaces")
		result.BindingReview = true
		result.EntityTypes = append(result.EntityTypes, "work", "creator", "media")
		result.ReasonCodes = append(result.ReasonCodes, "provider_identity_changed")
	}
	if left.IR.WorkDirectoryGlob != right.IR.WorkDirectoryGlob || left.IR.WorkStableKey != right.IR.WorkStableKey || primitiveKindsChanged(left.IR, right.IR, "path_match", "stable_key") {
		result.Fields = append(result.Fields, "source_identity")
		result.FullRescan, result.BindingReview = true, true
		result.EntityTypes = append(result.EntityTypes, "work", "creator", "media")
		result.ReasonCodes = append(result.ReasonCodes, "stable_source_identity_changed")
	}
	if left.IR.WorkTitle != right.IR.WorkTitle || primitiveKindsChanged(left.IR, right.IR, "selector", "fallback", "metadata_map") {
		result.Fields = append(result.Fields, "effective_fields")
		result.Reproject, result.RebuildSearch = true, true
		result.EntityTypes = append(result.EntityTypes, "work", "creator")
		result.ReasonCodes = append(result.ReasonCodes, "query_projection_changed")
	}
	if left.IR.MediaGlob != right.IR.MediaGlob || left.IR.MediaKind != right.IR.MediaKind || left.IR.MediaMIME != right.IR.MediaMIME || primitiveKindsChanged(left.IR, right.IR, "media_classify", "condition") {
		result.Fields = append(result.Fields, "media")
		result.FullRescan, result.Reproject = true, true
		result.EntityTypes = append(result.EntityTypes, "media")
		result.ReasonCodes = append(result.ReasonCodes, "media_execution_changed")
	}
	if primitiveKindsChanged(left.IR, right.IR, "media_order") {
		result.Fields = append(result.Fields, "media_order")
		result.PartialRescan, result.Reproject = true, true
		result.EntityTypes = append(result.EntityTypes, "media")
		result.ReasonCodes = append(result.ReasonCodes, "media_order_changed")
	}
	if primitiveKindsChanged(left.IR, right.IR, "cover_candidate") {
		result.Fields = append(result.Fields, "cover")
		result.Reproject, result.RebuildDerived = true, true
		result.EntityTypes = append(result.EntityTypes, "work", "media")
		result.ReasonCodes = append(result.ReasonCodes, "cover_selection_changed")
	}
	if !bytes.Equal(mustJSON(left.IR.Extensions), mustJSON(right.IR.Extensions)) {
		result.Fields = append(result.Fields, "extensions")
		result.EntityTypes = append(result.EntityTypes, "work")
		result.ReasonCodes = append(result.ReasonCodes, "semantic_extension_changed")
		result.FullRescan = true
	}
	// 平台呈现只改变已有事实如何展示（平台名、图标、作者称谓、默认排序、显示时区），不改变任何
	// Source-derived 事实，因此重投影足够。若落到下方「未识别的语义变化一律全量重扫」的兜底分支，
	// 改一个平台名字就会触发整库重扫。
	if !bytes.Equal(mustJSON(left.IR.Presentation), mustJSON(right.IR.Presentation)) {
		result.Fields = append(result.Fields, "platform_presentation")
		result.EntityTypes = append(result.EntityTypes, "source")
		result.ReasonCodes = append(result.ReasonCodes, "platform_presentation_changed")
		result.Reproject = true
	}
	// 下列原语改变的是随快照冻结的 Source-derived 事实，必须重扫才能重新产出；这里显式登记
	// 是为了让影响报告给出具体字段与原因，而不是笼统的 runtime_semantics。
	if primitiveKindsChanged(left.IR, right.IR, "badge") {
		result.Fields = append(result.Fields, "badges")
		result.EntityTypes = append(result.EntityTypes, "work")
		result.ReasonCodes = append(result.ReasonCodes, "badge_rules_changed")
		result.FullRescan = true
	}
	if primitiveKindsChanged(left.IR, right.IR, "media_hidden", "cover_disable_marker") {
		result.Fields = append(result.Fields, "media_visibility")
		result.EntityTypes = append(result.EntityTypes, "media", "work")
		result.ReasonCodes = append(result.ReasonCodes, "media_visibility_rules_changed")
		result.FullRescan = true
	}
	if !bytes.Equal(mustJSON(left.IR.WorkDate), mustJSON(right.IR.WorkDate)) {
		result.Fields = append(result.Fields, "published_at")
		result.EntityTypes = append(result.EntityTypes, "work")
		result.ReasonCodes = append(result.ReasonCodes, "work_date_rules_changed")
		result.FullRescan = true
	}
	// 路径取值改变会改变创作者、标题等身份相关字段，且这些字段参与 Binding。因此不仅要重扫，
	// 还要走 Binding 复核——把作者从「目录名」换成别的取法可能让既有 occurrence 归到别的
	// CanonicalCreator 上，那不是可以静默完成的重投影。
	if !bytes.Equal(mustJSON(left.IR.PathCaptures), mustJSON(right.IR.PathCaptures)) {
		result.Fields = append(result.Fields, "path_capture")
		result.EntityTypes = append(result.EntityTypes, "work", "creator")
		result.ReasonCodes = append(result.ReasonCodes, "path_capture_rules_changed")
		result.FullRescan = true
		result.BindingReview = true
	}
	if len(result.Fields) == 0 {
		result.Fields = append(result.Fields, "runtime_semantics")
		result.FullRescan = true
		result.ReasonCodes = append(result.ReasonCodes, "runtime_semantics_changed")
	}
	if result.FullRescan {
		result.Actions = append(result.Actions, "full_rescan")
	}
	if result.PartialRescan && !result.FullRescan {
		result.Actions = append(result.Actions, "partial_rescan")
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
	if result.BindingReview {
		result.Category, result.BlockPublish, result.ManualConfirm = "BINDING_REVIEW", true, true
		result.EstimatedJob = "manual_binding_review"
	} else if result.FullRescan {
		result.Category, result.ManualConfirm = "RESCAN_FULL", false
		result.EstimatedJob = "scan_all_sources"
	} else if result.PartialRescan {
		result.Category = "RESCAN_PARTIAL"
		result.EstimatedJob = "scan_media_occurrences"
	} else if result.Reproject {
		result.Category = "REPROJECT"
		result.EstimatedJob = "reproject_query"
	} else {
		result.Category = "NO_ACTION"
	}
	result.TraceSummary = append(result.TraceSummary, result.ReasonCodes...)
	sort.Strings(result.Fields)
	sort.Strings(result.EntityTypes)
	sort.Strings(result.ReasonCodes)
	sort.Strings(result.TraceSummary)
	return result, nil
}

// ImpactParameters 判断同一 RuleVersion 的参数快照变化。参数值本身不改变 RuleVersion
// semantic identity，但会改变 Binding 执行身份，因此必须显式进入 Binding review 和可追踪的
// 重扫计划，不能被当成普通配置覆盖。
func (l *Lifecycle) ImpactParameters(packageJSON, before, after []byte) (ImpactResult, error) {
	compiled, err := l.compilePackage(packageJSON)
	if err != nil {
		return ImpactResult{}, err
	}
	_, _, left, err := CompileBinding(compiled, before)
	if err != nil {
		return ImpactResult{}, withField("/beforeParameters", err)
	}
	_, _, right, err := CompileBinding(compiled, after)
	if err != nil {
		return ImpactResult{}, withField("/afterParameters", err)
	}
	result := ImpactResult{OldHash: prefixedHash("gallery-rule-parameters\x00v1\x00", left), NewHash: prefixedHash("gallery-rule-parameters\x00v1\x00", right), TraceSummary: []string{}}
	if bytes.Equal(left, right) {
		result.Category, result.ReasonCodes, result.Actions = "NO_ACTION", []string{"parameter_identity_unchanged"}, []string{"none"}
		return result, nil
	}
	result.Category = "BINDING_REVIEW"
	result.Fields = []string{"parameters"}
	result.EntityTypes = []string{"binding", "work", "media"}
	result.ReasonCodes = []string{"parameter_value_changed"}
	result.Actions = []string{"binding_review", "full_rescan", "reproject"}
	result.TraceSummary = append(result.TraceSummary, result.ReasonCodes...)
	result.FullRescan, result.Reproject, result.BindingReview = true, true, true
	result.ManualConfirm = true
	result.EstimatedJob = "scan_and_reproject"
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
	// 路径取值在全部 metadata 取值原语之后执行，且只填充仍为空的字段：metadata 是来源自己声明的
	// 事实，路径只是回退推断，二者的优先级不能因为原语书写顺序而改变。
	for _, capture := range ir.PathCaptures {
		if err := applyPathCapture(capture, sample.Path, &result); err != nil {
			return DryRunResult{}, err
		}
	}
	applyIdentityExtensions(ir, &result)
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
	result.Work.CoverPath = selectCoverPath(ir, sample, result.Work.Media)
	result.Work.Badges = evaluateBadges(ir, sample, result.Work)
	// 作品发布时间在媒体与角标之后解析：它只依赖 metadata 与作品相对路径，放在末尾使前面的
	// 求值顺序不受影响，也让「没有 work_date 原语」这一常见情况完全零成本。
	if ir.WorkDate != nil {
		resolved, err := resolveWorkDate(*ir.WorkDate, sample.Metadata, sample.Path)
		if err != nil {
			return DryRunResult{}, err
		}
		if resolved.HasInstant() {
			result.Work.Date = &DryRunWorkDate{
				Instant:       resolved.Instant.Format(time.RFC3339Nano),
				RawValue:      resolved.Raw,
				Source:        resolved.Source,
				ParserVersion: resolved.ParserVersion,
			}
			result.Trace = append(result.Trace, TraceStep{
				ID: "work_date", Kind: "work_date", InputPointer: resolved.Source,
				OutputSummary: "field=date", Selected: true, ReasonCode: "selected",
			})
		} else {
			// 解析不出时间不是错误：多数来源的部分作品确实没有可用日期。但必须留下可解释的
			// issue，而不是像旧的静默丢弃那样让缺失无迹可寻。
			result.Issues = append(result.Issues, RuleIssue{Code: "RULE_WORK_DATE_MISSING"})
			result.Trace = append(result.Trace, TraceStep{
				ID: "work_date", Kind: "work_date", OutputSummary: "field=date", ReasonCode: "missing",
			})
		}
	}
	return result, nil
}

// evaluateBadges 计算该作品命中的角标。结果按 order、再按 badgeId 稳定排序，使同一作品
// 在任意一次扫描后得到逐字节相同的角标序列——角标进入 publication 快照，顺序不稳定会让
// 相同事实产生不同的 Catalog 内容。
func evaluateBadges(ir RuleIR, sample DryRunInput, work DryRunWork) []DryRunBadge {
	var result []DryRunBadge
	for _, badge := range ir.Badges {
		if !badgeMatches(badge, sample, work) {
			continue
		}
		result = append(result, DryRunBadge{
			ID: badge.BadgeID, Order: badge.Order, Position: badge.Position, Label: badge.Label,
			Color: badge.Color, Background: badge.Background, Border: badge.Border,
			ColorLight: badge.ColorLight, BackgroundLight: badge.BackgroundLight, BorderLight: badge.BorderLight,
		})
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Order != result[j].Order {
			return result[i].Order < result[j].Order
		}
		return result[i].ID < result[j].ID
	})
	return result
}

// badgeMatches 对同时声明的多类条件取交集：真实规则中每个角标只用一类条件，交集语义
// 因此既兼容又更严格，不会把「标签命中」误当成「后缀命中」。
func badgeMatches(badge IRBadge, sample DryRunInput, work DryRunWork) bool {
	if len(badge.Tags) > 0 && !anyTagMatches(badge, work.Tags) {
		return false
	}
	if badge.MetadataPointer != "" && !metadataValueMatches(badge, sample.Metadata) {
		return false
	}
	if len(badge.MediaSuffix) > 0 && !anyMediaSuffixMatches(badge, work.Media) {
		return false
	}
	return true
}

func anyTagMatches(badge IRBadge, tags []string) bool {
	for _, wanted := range badge.Tags {
		for _, tag := range tags {
			if badge.CaseInsensitive {
				if strings.EqualFold(tag, wanted) {
					return true
				}
				continue
			}
			if tag == wanted {
				return true
			}
		}
	}
	return false
}

// metadataValueMatches 按 JSON Pointer 取值后与候选值逐一比较。比较在规范 JSON 层面进行，
// 因此 `2` 与 `2.0` 视为同一个值，不受 metadata 书写形式影响。
func metadataValueMatches(badge IRBadge, metadata any) bool {
	value, ok := resolvePointer(metadata, badge.MetadataPointer)
	if !ok {
		return false
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return false
	}
	actual, err := CanonicalJSON(encoded)
	if err != nil {
		return false
	}
	for _, candidate := range badge.MetadataValues {
		wanted, err := CanonicalJSON(candidate)
		if err != nil {
			continue
		}
		if bytes.Equal(actual, wanted) {
			return true
		}
	}
	return false
}

// anyMediaSuffixMatches 只看已被规则分类为媒体的文件，因此「图片/视频」角标描述的是
// 该作品实际发布的媒体构成，而不是目录里碰巧存在的任意文件。
func anyMediaSuffixMatches(badge IRBadge, media []DryRunMedia) bool {
	for _, item := range media {
		extension := strings.TrimPrefix(path.Ext(item.Path), ".")
		if extension == "" {
			continue
		}
		for _, wanted := range badge.MediaSuffix {
			if badge.CaseInsensitive {
				if strings.EqualFold(extension, wanted) {
					return true
				}
				continue
			}
			if extension == wanted {
				return true
			}
		}
	}
	return false
}

// selectCoverPath 按下列顺序决定作品封面，与真实规则的 cover 语义一一对应：
//
//  1. 作品目录存在 cover_disable_marker 声明的标记文件（`.nocover`）时**没有封面**。
//     这是终态：既不选显式候选，也不回退到第一张自然序媒体；
//  2. 否则取 CoverScore 最高的候选（cover_candidate 的 priority/score）。同分时取自然序
//     靠前者，因为 media 已按 media_order 排好序，遍历顺序即自然序；
//  3. 没有任何候选时回退到第一张**可见**媒体（leaf_fallback: first_natural_media）。
//     隐藏媒体不参与回退——`.*` 这类隐藏项不应成为作品封面——但仍可通过显式候选被选中。
func selectCoverPath(ir RuleIR, sample DryRunInput, media []DryRunMedia) string {
	if ir.CoverDisableMarker != "" {
		for _, file := range sample.Files {
			if path.Base(file.Path) == ir.CoverDisableMarker {
				return ""
			}
		}
	}
	best, bestScore := "", 0
	for _, item := range media {
		if item.CoverScore > bestScore {
			best, bestScore = item.Path, item.CoverScore
		}
	}
	if best != "" {
		return best
	}
	for _, item := range media {
		if !item.Hidden {
			return item.Path
		}
	}
	return ""
}

// mediaTypeMatches 把 cover_candidate 的 media_type 约束解释为对已分类媒体的判定。
// static_image 明确排除 GIF/APNG 这类动图与视频，因为需要静态封面的场景不接受它们。
func mediaTypeMatches(wanted, kind, mimeType string) bool {
	switch wanted {
	case "static_image":
		return kind == "image" && mimeType != "image/gif" && mimeType != "image/apng"
	case "image":
		return kind == "image"
	case "video":
		return kind == "video"
	default:
		return wanted == kind
	}
}

func applyIdentityExtensions(ir RuleIR, result *DryRunResult) {
	for _, extension := range ir.Extensions {
		if extension.Namespace != "gallery.identity" || extension.Payload == nil {
			continue
		}
		if prefix, ok := extension.Payload["stable_key_prefix"].(string); ok && prefix != "" {
			result.Work.StableKey = prefix + result.Work.StableKey
		}
		result.Trace = append(result.Trace, TraceStep{
			ID: extension.Namespace, Kind: "extension", Selected: true,
			ReasonCode: "extension_applied", OutputSummary: "stable_key_transformed",
		})
	}
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
	// 名称 glob 隐藏在 CEL 条件之前应用：它只看文件名，成本恒定且可静态分析。隐藏只影响
	// 展示，媒体仍然进入身份与内容确认，也仍可被 cover_candidate 选为封面——真实规则里
	// `cover.*` 同时出现在隐藏与显式封面两张表中，正是这个意图。
	for _, glob := range ir.HiddenNameGlobs {
		if ok, _ := path.Match(glob, path.Base(file.Path)); ok {
			media.Hidden = true
			break
		}
	}
	for _, primitive := range ir.Primitives {
		config := rawConfig(primitive.Config)
		switch primitive.Kind {
		case "cover_candidate":
			ok, _ := path.Match(stringConfig(config, "glob"), path.Base(file.Path))
			if !ok {
				continue
			}
			// media_type 限定候选必须是该类媒体：真实规则用它把「解压预览的第一张图」限定
			// 为静态图片，避免把视频选成需要静态缩略图的封面。
			if wanted := stringConfig(config, "media_type"); wanted != "" && !mediaTypeMatches(wanted, media.Kind, media.MIME) {
				continue
			}
			// priority 与 score 是同一维度的两种写法，取较大者，使同一作品的多条候选按
			// 优先级择一，且缺省值不会压过显式声明。
			if score := intConfig(config, "priority"); score > media.CoverScore {
				media.CoverScore = score
			}
			if score := intConfig(config, "score"); score > media.CoverScore {
				media.CoverScore = score
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
		result.Trace = append(result.Trace, TraceStep{ID: primitive.ID, Kind: primitive.Kind, InputPointer: firstString(pointers), InputSummary: fmt.Sprintf("candidates=%d", len(pointers)), CandidateCount: len(pointers), ReasonCode: "missing"})
		if required {
			return fmt.Errorf("selector %s 缺少必需字段", primitive.ID)
		}
		return nil
	}
	assignTarget(&result.Work, target, selected)
	result.Trace = append(result.Trace, TraceStep{ID: primitive.ID, Kind: primitive.Kind, InputPointer: selectedPointer, InputSummary: fmt.Sprintf("candidates=%d", len(pointers)), OutputSummary: "field=" + target, CandidateCount: len(pointers), Selected: true, ReasonCode: "selected"})
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
				result.Trace = append(result.Trace, TraceStep{ID: primitive.ID + ":" + target, Kind: primitive.Kind, InputPointer: pointer, InputSummary: fmt.Sprintf("candidates=%d", len(pointers)), OutputSummary: "field=" + target, CandidateCount: len(pointers), Selected: true, ReasonCode: "selected"})
				break
			}
		}
	}
	return nil
}

func applyStableKey(primitive IRPrimitive, metadata any, result *DryRunResult) {
	config := rawConfig(primitive.Config)
	pointer := stringConfig(config, "pointer")
	if pointer == "" {
		return
	}
	value, ok := resolvePointer(metadata, pointer)
	if !ok || fmt.Sprint(value) == "" {
		return
	}
	stableKey := stringConfig(config, "prefix") + fmt.Sprint(value)
	field := ""
	switch stringConfig(config, "target") {
	case "work":
		result.Work.StableKey = stableKey
		field = "work.stable_key"
	case "creator":
		result.Work.CreatorStableKey = stableKey
		field = "creator.stable_key"
	}
	if field != "" {
		result.Trace = append(result.Trace, TraceStep{ID: primitive.ID, Kind: primitive.Kind, InputPointer: pointer, InputSummary: "pointer_resolved", OutputSummary: "field=" + field, CandidateCount: 1, Selected: true, ReasonCode: "selected"})
	}
}

// assignTarget 把 selector/fallback/metadata_map 选出的值写入作品字段。
//
// 分支集合必须与 package.go 的 assignableTargets 逐项一致：那里在**编译期**拒绝未知 target，
// 这里才可以安全地没有 default 分支。两处一旦不同步，就会退回到旧实现「规则声明了一个字段、
// 扫描成功、值凭空消失」的静默丢弃行为。新增可赋值字段必须同时改两处。
func assignTarget(work *DryRunWork, target string, value any) {
	switch target {
	case "title":
		work.Title = fmt.Sprint(value)
	case "external_id":
		work.ExternalID = fmt.Sprint(value)
	case "provider_id":
		work.ProviderID = fmt.Sprint(value)
	case "creator":
		work.Creator = fmt.Sprint(value)
	case "tags":
		work.Tags = anyStringList(value)
	case "description":
		work.Description = fmt.Sprint(value)
	case "source_url":
		work.SourceURL = fmt.Sprint(value)
	}
}

func orderMedia(ir RuleIR, media []DryRunMedia) {
	direction := "asc"
	for _, primitive := range ir.Primitives {
		if primitive.Kind == "media_order" {
			direction = stringConfig(rawConfig(primitive.Config), "direction")
		}
	}
	// 按路径排序统一使用与查询/排序协议相同的自然排序键（querytext.NaturalSortKey），
	// 使 "2.jpg" 排在 "10.jpg" 之前；纯字节比较会把多分页作品的媒体顺序按字典序打乱。
	sort.SliceStable(media, func(i, j int) bool {
		left, right := querytext.NaturalSortKey(media[i].Path), querytext.NaturalSortKey(media[j].Path)
		if direction == "desc" {
			return left > right
		}
		return left < right
	})
}

func validateDryRunInput(input DryRunInput) error {
	if !safeSamplePath(input.Path) {
		return fmt.Errorf("Dry Run path 无效")
	}
	if len(input.Files) > 10000 {
		return fmt.Errorf("RULE_SAMPLE_LIMIT")
	}
	for index, file := range input.Files {
		if !safeSamplePath(file.Path) {
			return fmt.Errorf("Dry Run file path 无效: %d", index)
		}
		if file.Size < 0 {
			return fmt.Errorf("Dry Run file size 无效: %d", index)
		}
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

func safeSamplePath(value string) bool {
	if value == "" || path.IsAbs(value) || strings.ContainsAny(value, `\\:`) {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." || part == "." || part == "" {
			return false
		}
	}
	return true
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
