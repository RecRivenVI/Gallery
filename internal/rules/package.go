package rules

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"mime"
	"regexp"
	"sort"
	"strconv"
	"strings"

	contractschema "github.com/RecRivenVI/gallery/internal/contract/schema"
)

const PrimitiveRegistryVersion = "gallery-primitives-v1"

var jsonNumberPattern = regexp.MustCompile(`^(-?)(0|[1-9][0-9]*)(?:\.([0-9]+))?(?:[eE]([+-]?[0-9]+))?$`)

type CompiledPackage struct {
	RuleSetID    string
	Version      string
	PackageHash  string
	SemanticHash string
	RuleIRHash   string
	Canonical    []byte
	IR           RuleIR
	ParameterSQL []byte
}

type RuleIR struct {
	CompilerVersion          string         `json:"compilerVersion"`
	PrimitiveRegistryVersion string         `json:"primitiveRegistryVersion"`
	WorkDirectoryGlob        string         `json:"workDirectoryGlob"`
	WorkTitle                string         `json:"workTitle"`
	WorkStableKey            string         `json:"workStableKey"`
	MetadataFile             string         `json:"metadataFile,omitempty"`
	MediaGlob                string         `json:"mediaGlob"`
	MediaKind                string         `json:"mediaKind"`
	MediaMIME                string         `json:"mediaMime"`
	Primitives               []IRPrimitive  `json:"primitives"`
	CELExpressions           []IRExpression `json:"celExpressions"`
}

type IRPrimitive struct {
	ID     string          `json:"id"`
	Kind   string          `json:"kind"`
	Config json.RawMessage `json:"config"`
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
	irJSON, err := CanonicalJSON(mustJSON(ir))
	if err != nil {
		return CompiledPackage{}, err
	}
	irHash := prefixedHash("gallery-rule-ir\x00v1\x00", append([]byte(semanticHash+"\x00"), irJSON...))
	return CompiledPackage{
		RuleSetID: parsed.RuleSetID, Version: parsed.Version, PackageHash: packageHash,
		SemanticHash: semanticHash, RuleIRHash: irHash, Canonical: canonical, IR: ir,
		ParameterSQL: append([]byte(nil), parsed.ParameterSchema...),
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
	hashInput := []byte(rule.SemanticHash + "\x00" + CompilerVersion + "\x00" + CELProfileVersion + "\x00" + PrimitiveRegistryVersion + "\x00")
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
	if err := validateIR(ir); err != nil {
		return RuleIR{}, err
	}
	return ir, nil
}

func CanonicalJSON(input []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("解析 JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return nil, fmt.Errorf("JSON 包含多个值")
	} else if err != io.EOF {
		return nil, fmt.Errorf("JSON 尾部无效: %w", err)
	}
	var output bytes.Buffer
	if err := writeCanonical(&output, value); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func compilePrimitives(primitives []rawPrimitive, expressions []IRExpression) (RuleIR, error) {
	ir := RuleIR{CompilerVersion: CompilerVersion, PrimitiveRegistryVersion: PrimitiveRegistryVersion, CELExpressions: expressions}
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
	if ir.CompilerVersion != CompilerVersion || ir.PrimitiveRegistryVersion != PrimitiveRegistryVersion || ir.WorkDirectoryGlob == "" || ir.MediaGlob == "" {
		return fmt.Errorf("规则缺少最小 work_directory/media_classify 执行计划")
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
		if _, pointer := config["pointer"]; !pointer {
			if _, pointers := config["pointers"]; !pointers {
				return fmt.Errorf("%s %s 缺少 pointer/pointers", primitive.Kind, primitive.ID)
			}
		}
	case "stable_key":
		if err := requireString("target"); err != nil {
			return err
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
	case "metadata_map":
		if _, ok := config["fields"]; !ok {
			return fmt.Errorf("metadata_map %s 缺少 fields", primitive.ID)
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
