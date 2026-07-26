package rules

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"mime"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	contractschema "github.com/RecRivenVI/gallery/internal/contract/schema"
)

// PrimitiveRegistryVersion 参与 rule_ir_hash，因此新增或改变原语语义必须递增它。
//
//   - v2：media_hidden（按名称 glob 隐藏媒体）、cover_disable_marker（`.nocover` 禁用封面）、
//     badge（规则派生的作品角标），并让 cover_candidate 支持 priority 与 media_type。
//   - v3：work_date（作品发布时间解析），把 selector/fallback/metadata_map 的 target 收敛为
//     封闭枚举以消除静默丢弃，并新增 description 与 source_url 两个可赋值字段。
//   - v4：presentation（平台呈现、排序集合与时间显示语义）。
//   - v5：work_date 的 path_timezone —— 目录名日期与 metadata 时间戳分别声明各自的朴素时间时区。
//   - v6：selector/fallback 的空串 default 在编译期被拒绝（它会静默覆盖已有默认值且不留缺失记录）。
//   - v7：path_capture（按路径段取值）—— 补上规则系统此前完全缺失的路径取值能力。
const PrimitiveRegistryVersion = "gallery-primitives-v7"

var jsonNumberPattern = regexp.MustCompile(`^(-?)(0|[1-9][0-9]*)(?:\.([0-9]+))?(?:[eE]([+-]?[0-9]+))?$`)

type CompiledPackage struct {
	RuleSetID                string
	Version                  string
	PackageHash              string
	SemanticHash             string
	RuleIRHash               string
	Canonical                []byte
	IR                       RuleIR
	ParameterSQL             []byte
	ParameterSchema          []byte
	ExtensionRegistryVersion string
}

type RuleIR struct {
	CompilerVersion          string                `json:"compilerVersion"`
	PrimitiveRegistryVersion string                `json:"primitiveRegistryVersion"`
	ExtensionRegistryVersion string                `json:"extensionRegistryVersion"`
	WorkDirectoryGlob        string                `json:"workDirectoryGlob"`
	WorkTitle                string                `json:"workTitle"`
	WorkStableKey            string                `json:"workStableKey"`
	MetadataFile             string                `json:"metadataFile,omitempty"`
	MediaGlob                string                `json:"mediaGlob"`
	MediaKind                string                `json:"mediaKind"`
	MediaMIME                string                `json:"mediaMime"`
	HiddenNameGlobs          []string              `json:"hiddenNameGlobs,omitempty"`
	CoverDisableMarker       string                `json:"coverDisableMarker,omitempty"`
	Badges                   []IRBadge             `json:"badges,omitempty"`
	WorkDate                 *IRWorkDate           `json:"workDate,omitempty"`
	PathCaptures             []IRPathCapture       `json:"pathCaptures,omitempty"`
	Presentation             *Presentation         `json:"presentation,omitempty"`
	Primitives               []IRPrimitive         `json:"primitives"`
	CELExpressions           []IRExpression        `json:"celExpressions"`
	Extensions               []IRCompiledExtension `json:"extensions,omitempty"`
}

type IRCompiledExtension struct {
	Namespace string         `json:"namespace"`
	Version   string         `json:"version"`
	Payload   map[string]any `json:"payload,omitempty"`
}

type IRPrimitive struct {
	ID     string          `json:"id"`
	Kind   string          `json:"kind"`
	Config json.RawMessage `json:"config"`
}

// IRBadge 是编译后的角标执行计划。它把「什么条件下出现」与「怎么显示」放在一起，
// 因为角标是完整的 Source-derived 展示事实，客户端不得再自行推导条件。
type IRBadge struct {
	BadgeID         string            `json:"badgeId"`
	Order           int               `json:"order"`
	Position        string            `json:"position"`
	Label           string            `json:"label"`
	Color           string            `json:"color,omitempty"`
	Background      string            `json:"background,omitempty"`
	Border          string            `json:"border,omitempty"`
	ColorLight      string            `json:"colorLight,omitempty"`
	BackgroundLight string            `json:"backgroundLight,omitempty"`
	BorderLight     string            `json:"borderLight,omitempty"`
	Tags            []string          `json:"tags,omitempty"`
	MetadataPointer string            `json:"metadataPointer,omitempty"`
	MetadataValues  []json.RawMessage `json:"metadataValues,omitempty"`
	MediaSuffix     []string          `json:"mediaSuffix,omitempty"`
	CaseInsensitive bool              `json:"caseInsensitive,omitempty"`
}

// IRPathCapture 是一条按路径段取值的执行计划。允许多条：不同字段可以来自不同的路径模式。
// 实际取值在 applyPathCapture 中执行，见 pathcapture.go。
type IRPathCapture struct {
	Pattern string            `json:"pattern"`
	Targets map[string]string `json:"targets"`
}

// IRWorkDate 是编译后的作品发布时间解析计划。它只描述「去哪里取、怎么解释」，实际解析在
// resolveWorkDate 中执行，见 worktime.go。
type IRWorkDate struct {
	Pointers      []string `json:"pointers,omitempty"`
	PathPattern   string   `json:"pathPattern,omitempty"`
	InputTimezone string   `json:"inputTimezone,omitempty"`
	PathTimezone  string   `json:"pathTimezone,omitempty"`
}

type IRExpression struct {
	ID         string `json:"id"`
	Purpose    string `json:"purpose"`
	Expression string `json:"expression"`
}

type rawPackage struct {
	RuleSetID       string          `json:"rule_set_id"`
	Version         string          `json:"version"`
	ParameterSchema json.RawMessage `json:"parameter_schema"`
	Primitives      []rawPrimitive  `json:"primitives"`
	CELExpressions  []IRExpression  `json:"cel_expressions"`
	Extensions      json.RawMessage `json:"extensions"`
}

type rawPrimitive struct {
	ID     string          `json:"id"`
	Kind   string          `json:"kind"`
	Config json.RawMessage `json:"config"`
}

type pathMatchConfig struct {
	Scope        string `json:"scope"`
	Glob         string `json:"glob"`
	Title        string `json:"title"`
	StableKey    string `json:"stable_key"`
	MetadataFile string `json:"metadata_file,omitempty"`
}

type mediaClassifyConfig struct {
	Glob      string `json:"glob"`
	Kind      string `json:"kind"`
	MIME      string `json:"mime"`
	Condition string `json:"condition,omitempty"`
}

// mediaHiddenConfig 用文件名 glob 声明「已识别但默认不展示」的媒体，对应真实规则中的
// hidden_name_globs（例如 `.*`、`cover.*`、`.cover.*`）。它与 condition{effect:hide}
// 互补：后者需要 CEL 且按 metadata 判定，前者只按名称，既便宜又可静态分析。
//
// 隐藏只影响展示，不影响身份：隐藏媒体仍然进入 SourceMedia 与内容确认，也仍然可以被
// cover_candidate 选为封面——真实规则里 `cover.*` 同时出现在隐藏与显式封面两张表中，
// 正是「不出现在图片列表里、但作为封面」这一意图。
type mediaHiddenConfig struct {
	Globs []string `json:"globs"`
}

// coverDisableMarkerConfig 声明一个「存在即禁用该作品封面」的标记文件名（`.nocover`）。
// 禁用是终态：既不选显式候选，也不回退到第一张自然序媒体。
type coverDisableMarkerConfig struct {
	Filename string `json:"filename"`
}

// badgeConfig 声明一个由规则派生的作品角标（R-18、AI 生成、图片、视频等）。
//
// 角标是 **Source-derived 事实**，不是用户 Overlay：它完全由规则和该作品的 metadata /
// 媒体构成决定，因此随重扫重新计算，不进 control.db。平台差异由「哪个规则包声明了哪些
// 角标」表达，业务代码里不得再出现按平台名分支。
//
// 三类条件覆盖真实规则中出现的全部形态，同时出现时取交集：
//   - Tags：作品标签命中任意一项；
//   - MetadataPointer + MetadataValues：metadata 指定位置等于任意一个给定值；
//   - MediaSuffix：作品中存在该后缀的媒体。
type badgeConfig struct {
	BadgeID  string          `json:"badge_id"`
	Order    int             `json:"order"`
	Position string          `json:"position"`
	Label    string          `json:"label"`
	Style    badgeStyleValue `json:"style"`
	When     badgeWhenValue  `json:"when"`
}

type badgeStyleValue struct {
	Color           string `json:"color,omitempty"`
	Background      string `json:"background,omitempty"`
	Border          string `json:"border,omitempty"`
	ColorLight      string `json:"color_light,omitempty"`
	BackgroundLight string `json:"background_light,omitempty"`
	BorderLight     string `json:"border_light,omitempty"`
}

type badgeWhenValue struct {
	Tags            []string          `json:"tags,omitempty"`
	MetadataPointer string            `json:"metadata_pointer,omitempty"`
	MetadataValues  []json.RawMessage `json:"metadata_values,omitempty"`
	MediaSuffix     []string          `json:"media_suffix,omitempty"`
	CaseInsensitive bool              `json:"case_insensitive,omitempty"`
}

// badgePositions 是角标可出现的位置。位置是契约的一部分：前端按位置决定渲染槽位，
// 不得接受未知位置后各自猜测。
var badgePositions = map[string]struct{}{
	"cover_top_left":  {},
	"cover_top_right": {},
	"tag_leading":     {},
}

func CompilePackage(input []byte) (CompiledPackage, error) {
	validator, err := NewRulePackageValidator()
	if err != nil {
		return CompiledPackage{}, err
	}
	input, err = NormalizeWithSchema(input, RulePackageSchema())
	if err != nil {
		return CompiledPackage{}, fmt.Errorf("规则包规范化: %w", err)
	}
	if err := validator.ValidateJSON(input); err != nil {
		return CompiledPackage{}, fmt.Errorf("规则包 Schema: %w", err)
	}
	root, err := decodeObject(input)
	if err != nil {
		return CompiledPackage{}, err
	}
	delete(root, "package_hash")
	delete(root, "semantic_hash")
	packageCanonical, err := canonicalObject(root)
	if err != nil {
		return CompiledPackage{}, err
	}
	packageHash := prefixedHash("gallery-rule-package\x00canonical-json-v1\x00", packageCanonical)

	semantic := cloneRawObject(root)
	delete(semantic, "tests")
	// UI 元数据只描述表单、分组和帮助文本，不属于执行语义；仍保留在
	// package_hash/canonical 中，以便编辑器数据可无损往返。
	delete(semantic, "ui_metadata")
	semanticExtensions, err := classifyExtensions(root["extensions"])
	if err != nil {
		return CompiledPackage{}, err
	}
	if len(semanticExtensions) == 0 {
		// 没有 semantic extension 时删除整个 extensions 键，使既有（仅含 nonsemantic 或遗留
		// extension 的）RuleVersion 的 semantic_hash 与历史保持完全一致。
		delete(semantic, "extensions")
	} else {
		encoded, err := json.Marshal(semanticExtensions)
		if err != nil {
			return CompiledPackage{}, err
		}
		semantic["extensions"] = encoded
	}
	semanticCanonical, err := canonicalObject(semantic)
	if err != nil {
		return CompiledPackage{}, err
	}
	semanticHash := prefixedHash("gallery-rule-semantic\x00v1\x00", semanticCanonical)
	root["package_hash"], _ = json.Marshal(packageHash)
	root["semantic_hash"], _ = json.Marshal(semanticHash)
	canonical, err := canonicalObject(root)
	if err != nil {
		return CompiledPackage{}, err
	}

	var parsed rawPackage
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	if err := decoder.Decode(&parsed); err != nil {
		return CompiledPackage{}, fmt.Errorf("解析规范规则包: %w", err)
	}
	ir, err := compilePrimitives(parsed.Primitives, parsed.CELExpressions)
	if err != nil {
		return CompiledPackage{}, err
	}
	ir.Extensions, err = compileSemanticExtensions(root["extensions"])
	if err != nil {
		return CompiledPackage{}, err
	}
	ir.ExtensionRegistryVersion = ExtensionRegistryVersion
	irJSON, err := CanonicalJSON(mustJSON(ir))
	if err != nil {
		return CompiledPackage{}, err
	}
	irHashInput := append([]byte(semanticHash+"\x00"+ExtensionRegistryVersion+"\x00"), irJSON...)
	irHash := prefixedHash("gallery-rule-ir\x00v1\x00", irHashInput)
	return CompiledPackage{
		RuleSetID: parsed.RuleSetID, Version: parsed.Version, PackageHash: packageHash,
		SemanticHash: semanticHash, RuleIRHash: irHash, Canonical: canonical, IR: ir,
		ParameterSQL: append([]byte(nil), parsed.ParameterSchema...), ParameterSchema: append([]byte(nil), parsed.ParameterSchema...),
		ExtensionRegistryVersion: ExtensionRegistryVersion,
	}, nil
}

func CompileBinding(rule CompiledPackage, parameters []byte) (RuleIR, string, []byte, error) {
	if len(parameters) == 0 {
		parameters = []byte("{}")
	}
	parameters, err := NormalizeWithSchema(parameters, rule.ParameterSQL)
	if err != nil {
		return RuleIR{}, "", nil, fmt.Errorf("规则参数规范化: %w", err)
	}
	validator, err := contractschema.Compile("rule-parameters.json", rule.ParameterSQL)
	if err != nil {
		return RuleIR{}, "", nil, fmt.Errorf("参数 Schema 无效: %w", err)
	}
	if err := validator.ValidateJSON(parameters); err != nil {
		return RuleIR{}, "", nil, fmt.Errorf("规则参数无效: %w", err)
	}
	canonical, err := CanonicalJSON(parameters)
	if err != nil {
		return RuleIR{}, "", nil, err
	}
	irJSON, err := CanonicalJSON(mustJSON(rule.IR))
	if err != nil {
		return RuleIR{}, "", nil, err
	}
	hashInput := []byte(rule.SemanticHash + "\x00" + CompilerVersion + "\x00" + CELProfileVersion + "\x00" + PrimitiveRegistryVersion + "\x00" + ExtensionRegistryVersion + "\x00")
	hashInput = append(hashInput, canonical...)
	hashInput = append(hashInput, '\x00')
	hashInput = append(hashInput, irJSON...)
	return rule.IR, prefixedHash("gallery-rule-ir\x00v1\x00", hashInput), canonical, nil
}

func DecodeIR(input []byte) (RuleIR, error) {
	var ir RuleIR
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ir); err != nil {
		return RuleIR{}, err
	}
	// 00017 之前保存的编译 IR 没有 extension registry 字段；按当时唯一的
	// registry 版本补齐读取视图，不改写历史 RuleVersion 的 hash 或 canonical。
	if ir.ExtensionRegistryVersion == "" {
		ir.ExtensionRegistryVersion = ExtensionRegistryVersion
	}
	if err := validateIR(ir); err != nil {
		return RuleIR{}, err
	}
	return ir, nil
}

func CanonicalJSON(input []byte) ([]byte, error) {
	value, err := decodeAny(input)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := writeCanonical(&output, value); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func compilePrimitives(primitives []rawPrimitive, expressions []IRExpression) (RuleIR, error) {
	ir := RuleIR{CompilerVersion: CompilerVersion, PrimitiveRegistryVersion: PrimitiveRegistryVersion, ExtensionRegistryVersion: ExtensionRegistryVersion, CELExpressions: expressions}
	for index, primitive := range primitives {
		canonicalConfig, err := CanonicalJSON(primitive.Config)
		if err != nil {
			return RuleIR{}, withField(fmt.Sprintf("/primitives/%d/config", index), fmt.Errorf("原语 %s 配置无效: %w", primitive.ID, err))
		}
		ir.Primitives = append(ir.Primitives, IRPrimitive{ID: primitive.ID, Kind: primitive.Kind, Config: canonicalConfig})
		switch primitive.Kind {
		case "path_match":
			var config pathMatchConfig
			if err := strictDecode(primitive.Config, &config); err != nil {
				return RuleIR{}, withField(fmt.Sprintf("/primitives/%d/config", index), fmt.Errorf("path_match %s: %w", primitive.ID, err))
			}
			if config.Scope != "work_directory" || config.Glob == "" || config.Title != "directory_name" || config.StableKey != "relative_path" {
				return RuleIR{}, withField(fmt.Sprintf("/primitives/%d/config", index), fmt.Errorf("path_match %s 配置不受支持", primitive.ID))
			}
			ir.WorkDirectoryGlob, ir.WorkTitle, ir.WorkStableKey, ir.MetadataFile = config.Glob, config.Title, config.StableKey, config.MetadataFile
		case "media_classify":
			var config mediaClassifyConfig
			if err := strictDecode(primitive.Config, &config); err != nil {
				return RuleIR{}, withField(fmt.Sprintf("/primitives/%d/config", index), fmt.Errorf("media_classify %s: %w", primitive.ID, err))
			}
			if config.Glob == "" || config.Kind == "" || config.MIME == "" {
				return RuleIR{}, withField(fmt.Sprintf("/primitives/%d/config", index), fmt.Errorf("media_classify %s 缺少 glob/kind/mime", primitive.ID))
			}
			if _, _, err := mime.ParseMediaType(config.MIME); err != nil {
				return RuleIR{}, withField(fmt.Sprintf("/primitives/%d/config/mime", index), fmt.Errorf("media_classify %s MIME 无效: %w", primitive.ID, err))
			}
			ir.MediaGlob, ir.MediaKind, ir.MediaMIME = config.Glob, config.Kind, config.MIME
		case "media_hidden":
			var config mediaHiddenConfig
			if err := strictDecode(primitive.Config, &config); err != nil {
				return RuleIR{}, withField(fmt.Sprintf("/primitives/%d/config", index), fmt.Errorf("media_hidden %s: %w", primitive.ID, err))
			}
			if len(config.Globs) == 0 {
				return RuleIR{}, withField(fmt.Sprintf("/primitives/%d/config/globs", index), fmt.Errorf("media_hidden %s 缺少 globs", primitive.ID))
			}
			for globIndex, glob := range config.Globs {
				if glob == "" {
					return RuleIR{}, withField(fmt.Sprintf("/primitives/%d/config/globs/%d", index, globIndex), fmt.Errorf("media_hidden %s glob 为空", primitive.ID))
				}
				if _, err := path.Match(glob, "probe"); err != nil {
					return RuleIR{}, withField(fmt.Sprintf("/primitives/%d/config/globs/%d", index, globIndex), fmt.Errorf("media_hidden %s glob 无效: %w", primitive.ID, err))
				}
			}
			ir.HiddenNameGlobs = append(ir.HiddenNameGlobs, config.Globs...)
		case "cover_disable_marker":
			var config coverDisableMarkerConfig
			if err := strictDecode(primitive.Config, &config); err != nil {
				return RuleIR{}, withField(fmt.Sprintf("/primitives/%d/config", index), fmt.Errorf("cover_disable_marker %s: %w", primitive.ID, err))
			}
			if config.Filename == "" || strings.ContainsAny(config.Filename, `/\`) || config.Filename == "." || config.Filename == ".." {
				return RuleIR{}, withField(fmt.Sprintf("/primitives/%d/config/filename", index), fmt.Errorf("cover_disable_marker %s filename 无效", primitive.ID))
			}
			ir.CoverDisableMarker = config.Filename
		case "badge":
			var config badgeConfig
			if err := strictDecode(primitive.Config, &config); err != nil {
				return RuleIR{}, withField(fmt.Sprintf("/primitives/%d/config", index), fmt.Errorf("badge %s: %w", primitive.ID, err))
			}
			if config.BadgeID == "" || config.Label == "" {
				return RuleIR{}, withField(fmt.Sprintf("/primitives/%d/config", index), fmt.Errorf("badge %s 缺少 badge_id/label", primitive.ID))
			}
			if _, ok := badgePositions[config.Position]; !ok {
				return RuleIR{}, withField(fmt.Sprintf("/primitives/%d/config/position", index), fmt.Errorf("badge %s position %q 不受支持", primitive.ID, config.Position))
			}
			if len(config.When.Tags) == 0 && config.When.MetadataPointer == "" && len(config.When.MediaSuffix) == 0 {
				return RuleIR{}, withField(fmt.Sprintf("/primitives/%d/config/when", index), fmt.Errorf("badge %s 缺少任何触发条件", primitive.ID))
			}
			if config.When.MetadataPointer != "" && len(config.When.MetadataValues) == 0 {
				return RuleIR{}, withField(fmt.Sprintf("/primitives/%d/config/when", index), fmt.Errorf("badge %s 声明了 metadata_pointer 但没有 metadata_values", primitive.ID))
			}
			for badgeIndex, existing := range ir.Badges {
				if existing.BadgeID == config.BadgeID {
					return RuleIR{}, withField(fmt.Sprintf("/primitives/%d/config/badge_id", index),
						fmt.Errorf("badge %s 的 badge_id %q 与第 %d 个角标重复", primitive.ID, config.BadgeID, badgeIndex))
				}
			}
			ir.Badges = append(ir.Badges, IRBadge{
				BadgeID: config.BadgeID, Order: config.Order, Position: config.Position, Label: config.Label,
				Color: config.Style.Color, Background: config.Style.Background, Border: config.Style.Border,
				ColorLight: config.Style.ColorLight, BackgroundLight: config.Style.BackgroundLight, BorderLight: config.Style.BorderLight,
				Tags: config.When.Tags, MetadataPointer: config.When.MetadataPointer,
				MetadataValues: config.When.MetadataValues, MediaSuffix: config.When.MediaSuffix,
				CaseInsensitive: config.When.CaseInsensitive,
			})
		case "work_date":
			var config workDateConfig
			if err := strictDecode(primitive.Config, &config); err != nil {
				return RuleIR{}, withField(fmt.Sprintf("/primitives/%d/config", index), fmt.Errorf("work_date %s: %w", primitive.ID, err))
			}
			if ir.WorkDate != nil {
				return RuleIR{}, withField(fmt.Sprintf("/primitives/%d", index),
					fmt.Errorf("work_date %s 重复声明；一个规则包只能有一条作品发布时间解析计划", primitive.ID))
			}
			plan, err := compileWorkDate(config, primitive.ID)
			if err != nil {
				return RuleIR{}, withField(fmt.Sprintf("/primitives/%d/config", index), err)
			}
			ir.WorkDate = &plan
		case "presentation":
			var config presentationConfig
			if err := strictDecode(primitive.Config, &config); err != nil {
				return RuleIR{}, withField(fmt.Sprintf("/primitives/%d/config", index), fmt.Errorf("presentation %s: %w", primitive.ID, err))
			}
			if ir.Presentation != nil {
				return RuleIR{}, withField(fmt.Sprintf("/primitives/%d", index),
					fmt.Errorf("presentation %s 重复声明；一个规则包只能有一份平台呈现配置", primitive.ID))
			}
			resolved, err := compilePresentation(config, primitive.ID)
			if err != nil {
				return RuleIR{}, withField(fmt.Sprintf("/primitives/%d/config", index), err)
			}
			ir.Presentation = &resolved
		case "path_capture":
			var config pathCaptureConfig
			if err := strictDecode(primitive.Config, &config); err != nil {
				return RuleIR{}, withField(fmt.Sprintf("/primitives/%d/config", index), fmt.Errorf("path_capture %s: %w", primitive.ID, err))
			}
			plan, err := compilePathCapture(config, primitive.ID)
			if err != nil {
				return RuleIR{}, withField(fmt.Sprintf("/primitives/%d/config", index), err)
			}
			ir.PathCaptures = append(ir.PathCaptures, plan)
		case "selector", "fallback", "stable_key", "media_order", "cover_candidate", "metadata_map", "condition":
			if err := validateExtendedPrimitive(primitive); err != nil {
				return RuleIR{}, withField(fmt.Sprintf("/primitives/%d/config", index), err)
			}
		}
	}
	if err := validateCELExpressions(expressions); err != nil {
		return RuleIR{}, err
	}
	if err := validateIR(ir); err != nil {
		return RuleIR{}, err
	}
	return ir, nil
}

func validateIR(ir RuleIR) error {
	if ir.CompilerVersion != CompilerVersion || ir.PrimitiveRegistryVersion != PrimitiveRegistryVersion || ir.ExtensionRegistryVersion != ExtensionRegistryVersion || ir.WorkDirectoryGlob == "" || ir.MediaGlob == "" {
		return fmt.Errorf("规则缺少最小 work_directory/media_classify 执行计划")
	}
	return nil
}

func compileSemanticExtensions(raw json.RawMessage) ([]IRCompiledExtension, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var extensions map[string]json.RawMessage
	if err := json.Unmarshal(raw, &extensions); err != nil {
		return nil, err
	}
	result := make([]IRCompiledExtension, 0, len(extensions))
	for namespace, value := range extensions {
		fields, object := objectFields(value)
		if !object || fields["semantic"] == nil {
			continue
		}
		var entry extensionEntry
		if err := strictDecode(value, &entry); err != nil {
			return nil, withField("/extensions/"+namespace, err)
		}
		if !entry.Semantic {
			continue
		}
		payload, err := compileExtensionPayload(namespace, entry)
		if err != nil {
			return nil, withField("/extensions/"+namespace+"/payload", err)
		}
		result = append(result, IRCompiledExtension{Namespace: namespace, Version: entry.Version, Payload: payload})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Namespace < result[j].Namespace })
	return result, nil
}

// assignableTargets 是 selector/fallback/metadata_map 可以写入的**封闭**字段集合，与
// lifecycle.assignTarget 的分支一一对应。
//
// 这里必须是封闭枚举而不是「非空字符串」：旧实现只校验 target 非空，而 assignTarget 对未知
// target 没有 default 分支，于是规则可以声明一个永远不生效的 target——规则导入成功、扫描成功、
// 值凭空消失，既无 issue 也无 trace 告警。仓库自己的 Pawchive 真实来源夹具就声明了
// `target: "date"` 并因此被静默丢弃。任何新增可赋值字段都必须同时改这里与 assignTarget。
//
// **`date` 刻意不在此列。** 作品发布时间必须携带解析后的 instant、原始输入与解析器版本
// （见 `规范/06` 对时间排序的要求），而普通 selector 只能搬运原始值、无法产出 instant。
// 因此时间只能由 `work_date` 原语产出，规则里写 `target: "date"` 会在编译期被明确拒绝，
// 而不是留下一个看似成功却没有时间的规则。
var assignableTargets = map[string]struct{}{
	"title":       {},
	"external_id": {},
	"provider_id": {},
	"creator":     {},
	"tags":        {},
	"description": {},
	"source_url":  {},
}

func requireAssignableTarget(raw json.RawMessage, primitive rawPrimitive) error {
	var target string
	if json.Unmarshal(raw, &target) != nil {
		return fmt.Errorf("%s %s target 无效", primitive.Kind, primitive.ID)
	}
	if _, ok := assignableTargets[target]; !ok {
		return fmt.Errorf("%s %s target %q 不受支持", primitive.Kind, primitive.ID, target)
	}
	return nil
}

// rejectEmptyDefault 在编译期拒绝 `"default": ""`。
//
// 求值端只把 **nil** 当作「没取到值」：`default` 为空串时它是一个非 nil 的接口值，于是既不会留下
// `RULE_SELECTOR_MISSING`，也不会走缺失分支，而是直接把空串赋给目标字段。后果是**静默破坏**——
// 例如 `path_match` 已经把作品标题初始化为目录名，一条带空串 default 的 title 取值链会在 metadata
// 缺少标题时把它覆盖成空，得到一个没有标题、也没有任何问题记录的作品。
//
// 空串 default 从来不表达有用的事实：它想说的「这个字段没有值」正是省略 default 的语义，而省略会
// 如实留下 `RULE_SELECTOR_MISSING`。因此这里不做运行期兼容，直接在编译期拒绝，让这一类彻底不可写出。
func rejectEmptyDefault(config map[string]json.RawMessage, primitive rawPrimitive) error {
	raw, ok := config["default"]
	if !ok {
		return nil
	}
	var value string
	if json.Unmarshal(raw, &value) == nil && value == "" {
		return fmt.Errorf("%s %s 的 default 是空串：它会覆盖已有默认值并且不留下缺失记录；"+
			"要表达「没有值」请省略 default", primitive.Kind, primitive.ID)
	}
	return nil
}

func validateExtendedPrimitive(primitive rawPrimitive) error {
	var config map[string]json.RawMessage
	if err := strictDecode(primitive.Config, &config); err != nil {
		return fmt.Errorf("%s %s: %w", primitive.Kind, primitive.ID, err)
	}
	if len(config) == 0 {
		return fmt.Errorf("%s %s 配置为空", primitive.Kind, primitive.ID)
	}
	requireString := func(name string) error {
		var value string
		if raw, ok := config[name]; !ok || json.Unmarshal(raw, &value) != nil || value == "" {
			return fmt.Errorf("%s %s 缺少 %s", primitive.Kind, primitive.ID, name)
		}
		return nil
	}
	switch primitive.Kind {
	case "selector", "fallback":
		if err := requireString("target"); err != nil {
			return err
		}
		if err := requireAssignableTarget(config["target"], primitive); err != nil {
			return err
		}
		if _, pointer := config["pointer"]; !pointer {
			if _, pointers := config["pointers"]; !pointers {
				return fmt.Errorf("%s %s 缺少 pointer/pointers", primitive.Kind, primitive.ID)
			}
		}
		if err := rejectEmptyDefault(config, primitive); err != nil {
			return err
		}
	case "stable_key":
		if err := requireString("target"); err != nil {
			return err
		}
		if err := requireString("pointer"); err != nil {
			return err
		}
		var target string
		if err := json.Unmarshal(config["target"], &target); err != nil {
			return fmt.Errorf("stable_key %s target 无效", primitive.ID)
		}
		if target != "work" && target != "creator" {
			return fmt.Errorf("stable_key %s target %q 不受支持", primitive.ID, target)
		}
	case "media_order":
		if err := requireString("by"); err != nil {
			return err
		}
		if err := requireString("direction"); err != nil {
			return err
		}
	case "cover_candidate":
		if err := requireString("glob"); err != nil {
			return err
		}
		// priority 与 score 是同一维度的两种写法：priority 让真实规则里「同一作品多条候选
		// 按优先级择一」可以直接表达，score 保留既有包的兼容性。两者都缺省时候选按出现
		// 顺序竞争，仍然确定。media_type 限定候选必须是某类媒体（例如静态图片），避免把
		// 视频或动图选成需要静态缩略图的封面。
		if raw, ok := config["priority"]; ok {
			var priority int
			if json.Unmarshal(raw, &priority) != nil || priority < 0 {
				return fmt.Errorf("cover_candidate %s priority 无效", primitive.ID)
			}
		}
		if raw, ok := config["media_type"]; ok {
			var mediaType string
			if json.Unmarshal(raw, &mediaType) != nil || mediaType == "" {
				return fmt.Errorf("cover_candidate %s media_type 无效", primitive.ID)
			}
		}
	case "metadata_map":
		raw, ok := config["fields"]
		if !ok {
			return fmt.Errorf("metadata_map %s 缺少 fields", primitive.ID)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return fmt.Errorf("metadata_map %s fields 无效: %w", primitive.ID, err)
		}
		// fields 的每个 key 都是赋值 target，与 selector/fallback 走同一条落地路径，
		// 因此必须受同一张封闭白名单约束。
		for name := range fields {
			encoded, err := json.Marshal(name)
			if err != nil {
				return fmt.Errorf("metadata_map %s field 名无效", primitive.ID)
			}
			if err := requireAssignableTarget(encoded, primitive); err != nil {
				return err
			}
		}
	case "condition":
		for _, name := range []string{"scope", "expression", "effect"} {
			if err := requireString(name); err != nil {
				return err
			}
		}
	}
	return nil
}

func decodeObject(input []byte) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(input))
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, fmt.Errorf("规则包必须是对象")
	}
	return object, nil
}

func cloneRawObject(input map[string]json.RawMessage) map[string]json.RawMessage {
	result := make(map[string]json.RawMessage, len(input))
	for key, value := range input {
		result[key] = append(json.RawMessage(nil), value...)
	}
	return result
}

func canonicalObject(object map[string]json.RawMessage) ([]byte, error) {
	raw, err := json.Marshal(object)
	if err != nil {
		return nil, err
	}
	return CanonicalJSON(raw)
}

func writeCanonical(output *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case nil:
		output.WriteString("null")
	case bool:
		if typed {
			output.WriteString("true")
		} else {
			output.WriteString("false")
		}
	case string:
		encoded, _ := json.Marshal(typed)
		output.Write(encoded)
	case json.Number:
		normalized, err := normalizeNumber(typed.String())
		if err != nil {
			return err
		}
		output.WriteString(normalized)
	case []any:
		output.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				output.WriteByte(',')
			}
			if err := writeCanonical(output, item); err != nil {
				return err
			}
		}
		output.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		output.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				output.WriteByte(',')
			}
			encoded, _ := json.Marshal(key)
			output.Write(encoded)
			output.WriteByte(':')
			if err := writeCanonical(output, typed[key]); err != nil {
				return err
			}
		}
		output.WriteByte('}')
	default:
		return fmt.Errorf("不支持的 JSON 类型 %T", typed)
	}
	return nil
}

func normalizeNumber(input string) (string, error) {
	matches := jsonNumberPattern.FindStringSubmatch(input)
	if matches == nil {
		return "", fmt.Errorf("无效 JSON 数字")
	}
	exponent := 0
	if matches[4] != "" {
		parsed, err := strconv.Atoi(matches[4])
		if err != nil || parsed < -10000 || parsed > 10000 {
			return "", fmt.Errorf("JSON exponent 超限")
		}
		exponent = parsed
	}
	digits := strings.TrimLeft(matches[2]+matches[3], "0")
	if digits == "" {
		return "0", nil
	}
	exponent -= len(matches[3])
	coefficient := new(big.Int)
	if _, ok := coefficient.SetString(digits, 10); !ok {
		return "", fmt.Errorf("无效 JSON coefficient")
	}
	for exponent < 0 && new(big.Int).Mod(coefficient, big.NewInt(10)).Sign() == 0 {
		coefficient.Div(coefficient, big.NewInt(10))
		exponent++
	}
	digits = coefficient.String()
	var result string
	switch {
	case exponent >= 0:
		result = digits + strings.Repeat("0", exponent)
	case len(digits)+exponent > 0:
		point := len(digits) + exponent
		result = digits[:point] + "." + digits[point:]
	default:
		result = "0." + strings.Repeat("0", -(len(digits)+exponent)) + digits
	}
	if matches[1] == "-" {
		result = "-" + result
	}
	return result, nil
}

func strictDecode(input []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func prefixedHash(prefix string, content []byte) string {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(prefix))
	_, _ = hasher.Write(content)
	return hex.EncodeToString(hasher.Sum(nil))
}

func mustJSON(value any) []byte { output, _ := json.Marshal(value); return output }
