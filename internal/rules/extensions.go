package rules

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// supportedExtensions 是本编译器识别的 extension namespace 及其支持的 version 集合。required 或
// semantic 的 extension 只有落在该表内且 version 受支持时才允许编译。这是 pre-freeze 的最小注册表，
// 目前仅用于身份识别与校验，behavioral 消费属于未来阶段；namespace 集合尚未冻结。
var supportedExtensions = map[string]map[string]struct{}{
	"gallery.identity": {"1": {}},
}

// extensionEntry 是分类后的 extension 声明。
//
// 分类维度与身份语义：
//   - required + semantic：参与 package_hash 与 semantic_hash；namespace/version 未受支持时阻止编译；
//   - optional + semantic：参与 package_hash 与 semantic_hash；未受支持时显式拒绝，不静默忽略；
//   - required + nonsemantic：参与 package_hash，不参与 semantic_hash；未受支持时阻止编译；
//   - optional + nonsemantic：参与 package_hash，不参与 semantic_hash；容忍未知 namespace。
//
// 未按本结构声明（缺少 semantic 字段）的遗留 extension 一律按 optional + nonsemantic 处理，
// 因此既不改变既有 RuleVersion 的 semantic_hash，也不会因未知 namespace 阻止编译。extension 之间
// 的顺序不影响任何 hash（规范化时对象键排序）；相同 semantic payload 经规范化后得到相同 hash。
type extensionEntry struct {
	Required bool            `json:"required"`
	Semantic bool            `json:"semantic"`
	Version  string          `json:"version"`
	Payload  json.RawMessage `json:"payload"`
}

// classifyExtensions 解析规则包的 extensions 对象，校验 required/semantic 声明，并返回参与
// semantic_hash 的 semantic extension 子集（其余仅参与 package_hash）。返回 nil 表示没有任何
// semantic extension，调用方据此删除 semantic 视图中的 extensions 键以保持历史身份不变。
func classifyExtensions(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var extensions map[string]json.RawMessage
	if err := json.Unmarshal(raw, &extensions); err != nil {
		return nil, withField("/extensions", fmt.Errorf("extensions 必须是对象: %w", err))
	}
	semantic := make(map[string]json.RawMessage)
	for namespace, value := range extensions {
		fields, isObject := objectFields(value)
		if _, classified := fields["semantic"]; !isObject || !classified {
			// 遗留/未分类 extension：optional + nonsemantic，容忍未知 namespace，只进入 package_hash。
			continue
		}
		var entry extensionEntry
		if err := strictDecode(value, &entry); err != nil {
			return nil, withField("/extensions/"+namespace, fmt.Errorf("extension %q 结构无效: %w", namespace, err))
		}
		if entry.Required || entry.Semantic {
			if err := checkExtensionSupported(namespace, entry); err != nil {
				return nil, err
			}
		}
		if entry.Semantic {
			canonical, err := CanonicalJSON(value)
			if err != nil {
				return nil, withField("/extensions/"+namespace, err)
			}
			semantic[namespace] = canonical
		}
	}
	if len(semantic) == 0 {
		return nil, nil
	}
	return semantic, nil
}

func checkExtensionSupported(namespace string, entry extensionEntry) error {
	versions, ok := supportedExtensions[namespace]
	if !ok {
		return withField("/extensions/"+namespace, fmt.Errorf("不支持的 extension namespace %q", namespace))
	}
	if entry.Version == "" {
		return withField("/extensions/"+namespace, fmt.Errorf("required/semantic extension %q 缺少 version", namespace))
	}
	if _, ok := versions[entry.Version]; !ok {
		return withField("/extensions/"+namespace, fmt.Errorf("extension %q 不支持 version %q", namespace, entry.Version))
	}
	return nil
}

func objectFields(value json.RawMessage) (map[string]json.RawMessage, bool) {
	if len(bytes.TrimSpace(value)) == 0 || bytes.TrimSpace(value)[0] != '{' {
		return nil, false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(value, &fields); err != nil {
		return nil, false
	}
	return fields, true
}
