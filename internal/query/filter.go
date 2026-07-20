package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/creators"
	"github.com/RecRivenVI/gallery/internal/domain"
)

// FilterNode 是服务端权威的结构化过滤 AST：恰好一个分支有效——all（AND）、any（OR）、
// not（取反）或 field/op/value（叶子谓词）。未知字段、未知操作符、类型错误和形状错误
// 一律拒绝为 VALIDATION_ERROR，不把任意 SQL、内部列名或 row ID 暴露给客户端。
type FilterNode struct {
	All   []FilterNode    `json:"all,omitempty"`
	Any   []FilterNode    `json:"any,omitempty"`
	Not   *FilterNode     `json:"not,omitempty"`
	Field string          `json:"field,omitempty"`
	Op    string          `json:"op,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

const (
	maxFilterDepth = 6
	maxFilterNodes = 64
)

// ParseFilter 解析并校验客户端提交的原始 filter JSON。空字符串返回 (nil, nil)，
// 表示未提供结构化过滤。校验只检查形状、字段名和操作符是否已注册；具体值类型在
// Compile 阶段随 SQL 构建一起返回结构化错误。
func ParseFilter(raw string) (*FilterNode, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var node FilterNode
	if err := decoder.Decode(&node); err != nil {
		return nil, fault.WithField(fault.CodeValidation, "filter", err)
	}
	// decoder.More() 只能可靠判断"是否仍在同一个数组/对象内部还有更多元素"；顶层单个
	// JSON 值解码完毕后，如果紧随其后的尾随字节恰好是 '}' 或 ']'（例如
	// `{...}}`、`{...}]`），More() 会把它误判为"当前容器的结束符"而返回 false，从而让
	// 真实存在的尾随垃圾逃过校验。唯一可靠的做法是对同一个 Decoder 再解码一次并要求
	// 恰好得到 io.EOF：任何其他结果（成功解出另一个值，或非 EOF 错误）都说明流中还有
	// 不属于这个单一 filter 对象的字节。
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, fault.WithField(fault.CodeValidation, "filter", nil)
	}
	count := 0
	if err := validateShape(&node, 1, &count); err != nil {
		return nil, err
	}
	return &node, nil
}

func validateShape(node *FilterNode, depth int, count *int) error {
	*count++
	if depth > maxFilterDepth || *count > maxFilterNodes {
		return fault.WithField(fault.CodeValidation, "filter", nil)
	}
	branches := 0
	if node.All != nil {
		branches++
	}
	if node.Any != nil {
		branches++
	}
	if node.Not != nil {
		branches++
	}
	if node.Field != "" {
		branches++
	}
	if branches != 1 {
		return fault.WithField(fault.CodeValidation, "filter", nil)
	}
	if (node.All != nil && len(node.All) == 0) || (node.Any != nil && len(node.Any) == 0) {
		return fault.WithField(fault.CodeValidation, "filter", nil)
	}
	switch {
	case len(node.All) > 0:
		for index := range node.All {
			if err := validateShape(&node.All[index], depth+1, count); err != nil {
				return err
			}
		}
	case len(node.Any) > 0:
		for index := range node.Any {
			if err := validateShape(&node.Any[index], depth+1, count); err != nil {
				return err
			}
		}
	case node.Not != nil:
		return validateShape(node.Not, depth+1, count)
	default:
		spec, ok := fieldRegistry[node.Field]
		if !ok {
			return fault.WithField(fault.CodeValidation, "filter.field", nil)
		}
		if !spec.ops[node.Op] {
			return fault.WithField(fault.CodeValidation, "filter.op", nil)
		}
	}
	return nil
}

// canonicalJSON 提供确定性编码用于查询指纹：Go 结构体固定字段顺序天然规范化了
// key 顺序。数组内客户端提交顺序被视为有意义并保留（AND/OR 语义上可交换，但本轮
// 不做重排规范化，属已记录的简化，见 Documents/规范/06-查询-搜索与排序.md）。
func (n *FilterNode) canonicalJSON() string {
	if n == nil {
		return ""
	}
	encoded, _ := json.Marshal(n)
	return string(encoded)
}

// filterBuilder 编译一个叶子谓词为 SQL 片段。op 总是已知在 fieldSpec.ops 中注册过的
// 合法值——大多数字段只登记一个 op（历史上一直是 "eq"，因此多数实现直接忽略该参数），
// 但如 overlay.progress 这类支持多比较操作符的字段需要据此选择不同的 SQL 比较符。
type filterBuilder func(ctx context.Context, control *sql.DB, op string, raw json.RawMessage, args *[]any) (string, error)

type fieldSpec struct {
	ops   map[string]bool
	build filterBuilder
}

var fieldRegistry = map[string]fieldSpec{
	"library.id":                     {ops: map[string]bool{"eq": true}, build: buildLibraryID},
	"source.id":                      {ops: map[string]bool{"eq": true}, build: buildSourceID},
	"provider.id":                    {ops: map[string]bool{"eq": true}, build: buildProviderID},
	"tag":                            {ops: map[string]bool{"eq": true}, build: buildTag},
	"creator.id":                     {ops: map[string]bool{"eq": true}, build: buildCreatorID},
	"creator.role":                   {ops: map[string]bool{"eq": true}, build: buildCreatorRole},
	"media.kind":                     {ops: map[string]bool{"eq": true}, build: buildMediaKind},
	"media.locationAvailable":        {ops: map[string]bool{"eq": true}, build: buildMediaLocationAvailable},
	"media.contentVerificationState": {ops: map[string]bool{"eq": true}, build: buildMediaContentVerificationState},
	"overlay.favorite":               {ops: map[string]bool{"eq": true}, build: buildOverlayFavorite},
	"overlay.hidden":                 {ops: map[string]bool{"eq": true}, build: buildOverlayHidden},
	"overlay.progress": {
		ops:   map[string]bool{"eq": true, "lt": true, "lte": true, "gt": true, "gte": true},
		build: buildOverlayProgress,
	},
}

// filterReferencesField 递归判断 filter AST 是否在任意位置（all/any/not 的任意深度）
// 引用了指定字段，供 overlay.hidden 这类需要抑制默认隐式条件、且可能需要额外
// capability 的字段判定"客户端是否显式接管了该字段的可见性语义"。
func filterReferencesField(node *FilterNode, field string) bool {
	if node == nil {
		return false
	}
	switch {
	case len(node.All) > 0:
		for index := range node.All {
			if filterReferencesField(&node.All[index], field) {
				return true
			}
		}
		return false
	case len(node.Any) > 0:
		for index := range node.Any {
			if filterReferencesField(&node.Any[index], field) {
				return true
			}
		}
		return false
	case node.Not != nil:
		return filterReferencesField(node.Not, field)
	default:
		return node.Field == field
	}
}

// collectFilterFields 收集 filter AST 中出现的全部叶子字段名（去重），用于构建本次
// 查询实际使用的 dependency set，不需要遍历内部 SQL 片段。
func collectFilterFields(node *FilterNode) []string {
	seen := map[string]struct{}{}
	var walk func(n *FilterNode)
	walk = func(n *FilterNode) {
		if n == nil {
			return
		}
		switch {
		case len(n.All) > 0:
			for index := range n.All {
				walk(&n.All[index])
			}
		case len(n.Any) > 0:
			for index := range n.Any {
				walk(&n.Any[index])
			}
		case n.Not != nil:
			walk(n.Not)
		default:
			if n.Field != "" {
				seen[n.Field] = struct{}{}
			}
		}
	}
	walk(node)
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// FieldNames 返回已注册字段名的稳定列表，供 API 文档、契约测试和客户端能力探测使用。
func FieldNames() []string {
	names := make([]string, 0, len(fieldRegistry))
	for name := range fieldRegistry {
		names = append(names, name)
	}
	return names
}

func compileFilter(ctx context.Context, control *sql.DB, node *FilterNode) (string, []any, error) {
	if node == nil {
		return "", nil, nil
	}
	switch {
	case len(node.All) > 0:
		return compileList(ctx, control, node.All, " AND ")
	case len(node.Any) > 0:
		return compileList(ctx, control, node.Any, " OR ")
	case node.Not != nil:
		fragment, args, err := compileFilter(ctx, control, node.Not)
		if err != nil {
			return "", nil, err
		}
		return "NOT (" + fragment + ")", args, nil
	default:
		spec, ok := fieldRegistry[node.Field]
		if !ok {
			return "", nil, fault.WithField(fault.CodeValidation, "filter.field", nil)
		}
		var args []any
		fragment, err := spec.build(ctx, control, node.Op, node.Value, &args)
		if err != nil {
			return "", nil, err
		}
		return fragment, args, nil
	}
}

func compileList(ctx context.Context, control *sql.DB, nodes []FilterNode, separator string) (string, []any, error) {
	parts := make([]string, 0, len(nodes))
	var allArgs []any
	for index := range nodes {
		fragment, args, err := compileFilter(ctx, control, &nodes[index])
		if err != nil {
			return "", nil, err
		}
		parts = append(parts, fragment)
		allArgs = append(allArgs, args...)
	}
	return "(" + strings.Join(parts, separator) + ")", allArgs, nil
}

func decodeFilterString(raw json.RawMessage, maxLen int) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fault.WithField(fault.CodeValidation, "filter.value", err)
	}
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > maxLen {
		return "", fault.WithField(fault.CodeValidation, "filter.value", nil)
	}
	return value, nil
}

func decodeFilterBool(raw json.RawMessage) (bool, error) {
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, fault.WithField(fault.CodeValidation, "filter.value", err)
	}
	return value, nil
}

// buildLibraryID/buildSourceID 与既有 legacy Request.LibraryID/SourceID 参数一致，
// 把值当作不透明字符串直接等值比较，不强制 domain UUID 格式——这两个字段的最终
// 物理 ID 形态仍属阶段 4 之外的 pre-freeze 范围，见 01-v1实施计划.md。
func buildLibraryID(_ context.Context, _ *sql.DB, _ string, raw json.RawMessage, args *[]any) (string, error) {
	value, err := decodeFilterString(raw, 256)
	if err != nil {
		return "", err
	}
	*args = append(*args, value)
	return "w.library_id = ?", nil
}

func buildSourceID(_ context.Context, _ *sql.DB, _ string, raw json.RawMessage, args *[]any) (string, error) {
	value, err := decodeFilterString(raw, 256)
	if err != nil {
		return "", err
	}
	*args = append(*args, value)
	return "w.source_id = ?", nil
}

// buildProviderID 通过关联 source_works（阶段 1 已有的 provider_id 事实列）过滤，
// 不在 work_projections 新增列，避免为尚未冻结的 WorkOrigin/Provider 物理模型
// 抢先落地新 Schema。
func buildProviderID(_ context.Context, _ *sql.DB, _ string, raw json.RawMessage, args *[]any) (string, error) {
	value, err := decodeFilterString(raw, 256)
	if err != nil {
		return "", err
	}
	*args = append(*args, value)
	return "EXISTS (SELECT 1 FROM source_works sw WHERE sw.catalog_revision_id=w.catalog_revision_id AND sw.source_id=w.source_id AND sw.source_key=w.source_key AND sw.provider_id=?)", nil
}

func buildTag(_ context.Context, _ *sql.DB, _ string, raw json.RawMessage, args *[]any) (string, error) {
	value, err := decodeFilterString(raw, 512)
	if err != nil {
		return "", err
	}
	*args = append(*args, value)
	return "EXISTS (SELECT 1 FROM json_each(w.tags_json) WHERE value = ?)", nil
}

// buildCreatorID 在查询时把输入 Creator ID（可以是合并根，也可以是已被合并的旧 ID）
// 解析为完整等价组再过滤，使合并后的 Creator 页面/过滤命中全部成员作品，且不改写
// work_creator_relations（撤销合并后关系照常恢复）。
func buildCreatorID(ctx context.Context, control *sql.DB, _ string, raw json.RawMessage, args *[]any) (string, error) {
	value, err := decodeFilterString(raw, 256)
	if err != nil {
		return "", err
	}
	if _, err := domain.ParseID(domain.IDCanonicalCreator, value); err != nil {
		return "", fault.WithField(fault.CodeValidation, "filter.value", err)
	}
	group, err := creators.ResolveEquivalenceGroup(ctx, control, value)
	if err != nil {
		return "", err
	}
	placeholders := make([]string, len(group))
	for index, id := range group {
		placeholders[index] = "?"
		*args = append(*args, id)
	}
	return fmt.Sprintf("EXISTS (SELECT 1 FROM work_creator_relations r WHERE r.catalog_revision_id=w.catalog_revision_id AND r.overlay_revision_id=w.overlay_revision_id AND r.work_id=w.work_id AND r.creator_id IN (%s))", strings.Join(placeholders, ",")), nil
}

func buildCreatorRole(_ context.Context, _ *sql.DB, _ string, raw json.RawMessage, args *[]any) (string, error) {
	value, err := decodeFilterString(raw, 128)
	if err != nil {
		return "", err
	}
	*args = append(*args, value)
	return "EXISTS (SELECT 1 FROM work_creator_relations r WHERE r.catalog_revision_id=w.catalog_revision_id AND r.overlay_revision_id=w.overlay_revision_id AND r.work_id=w.work_id AND r.role=?)", nil
}

func buildMediaKind(_ context.Context, _ *sql.DB, _ string, raw json.RawMessage, args *[]any) (string, error) {
	value, err := decodeFilterString(raw, 64)
	if err != nil {
		return "", err
	}
	*args = append(*args, value)
	return "EXISTS (SELECT 1 FROM media_projections m WHERE m.catalog_revision_id=w.catalog_revision_id AND m.overlay_revision_id=w.overlay_revision_id AND m.work_id=w.work_id AND m.hidden=0 AND m.media_kind=?)", nil
}

func buildMediaLocationAvailable(_ context.Context, _ *sql.DB, _ string, raw json.RawMessage, args *[]any) (string, error) {
	value, err := decodeFilterBool(raw)
	if err != nil {
		return "", err
	}
	comparator := "<>"
	if value {
		comparator = "="
	}
	*args = append(*args, "present")
	return fmt.Sprintf("EXISTS (SELECT 1 FROM media_projections m WHERE m.catalog_revision_id=w.catalog_revision_id AND m.overlay_revision_id=w.overlay_revision_id AND m.work_id=w.work_id AND m.hidden=0 AND m.location_status %s ?)", comparator), nil
}

func buildMediaContentVerificationState(_ context.Context, _ *sql.DB, _ string, raw json.RawMessage, args *[]any) (string, error) {
	value, err := decodeFilterString(raw, 32)
	if err != nil {
		return "", err
	}
	if value != "located_unverified" && value != "content_verified" {
		return "", fault.WithField(fault.CodeValidation, "filter.value", nil)
	}
	*args = append(*args, value)
	return "EXISTS (SELECT 1 FROM media_projections m WHERE m.catalog_revision_id=w.catalog_revision_id AND m.overlay_revision_id=w.overlay_revision_id AND m.work_id=w.work_id AND m.hidden=0 AND m.content_verification_state=?)", nil
}

// buildOverlayFavorite 按 work_projections.favorite 快照列过滤，读取的是本次查询所在
// publication 冻结的值，不是 control.db 当前 live 值——客户端刚保存的 Favorite 要等
// 下一次 publication 才会改变这里的过滤结果，这是 Snapshot 语义的直接体现。
func buildOverlayFavorite(_ context.Context, _ *sql.DB, _ string, raw json.RawMessage, args *[]any) (string, error) {
	value, err := decodeFilterBool(raw)
	if err != nil {
		return "", err
	}
	*args = append(*args, boolInt(value))
	return "w.favorite = ?", nil
}

// buildOverlayHidden 允许显式查询 Hidden 可见性，取代默认隐式 "w.hidden = 0" 条件
// （见 baseFilter：filter 中出现 overlay.hidden 时不再叠加默认条件）。任何显式引用
// overlay.hidden 都需要 library.write capability（见 Service.Search 的授权检查），
// 不区分 true/false 或是否被 not 包裹——对任意深度的 all/any/not 组合判断"最终是否
// 只暴露非 Hidden 作品"等价于布尔可满足性问题，统一门槛换取简单、无歧义、可审计的
// 规则，避免"NOT hidden=true"与默认隐式条件产生不可解释的双重语义。
func buildOverlayHidden(_ context.Context, _ *sql.DB, _ string, raw json.RawMessage, args *[]any) (string, error) {
	value, err := decodeFilterBool(raw)
	if err != nil {
		return "", err
	}
	*args = append(*args, boolInt(value))
	return "w.hidden = ?", nil
}

var progressComparators = map[string]string{"eq": "=", "lt": "<", "lte": "<=", "gt": ">", "gte": ">="}

// buildOverlayProgress 按 work_projections.progress 快照列比较，值域必须落在 [0,1]，
// 与 overlay.normalizeInput 对 Progress 的校验语义一致；比较符按 op 从 progressComparators
// 选择（fieldRegistry 已限定 op 只能是这里注册的五个值之一）。
func buildOverlayProgress(_ context.Context, _ *sql.DB, op string, raw json.RawMessage, args *[]any) (string, error) {
	comparator, ok := progressComparators[op]
	if !ok {
		return "", fault.WithField(fault.CodeValidation, "filter.op", nil)
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fault.WithField(fault.CodeValidation, "filter.value", err)
	}
	if value < 0 || value > 1 {
		return "", fault.WithField(fault.CodeValidation, "filter.value", nil)
	}
	*args = append(*args, value)
	return "w.progress " + comparator + " ?", nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
