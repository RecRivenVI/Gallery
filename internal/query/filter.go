package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	if decoder.More() {
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

type filterBuilder func(ctx context.Context, control *sql.DB, raw json.RawMessage, args *[]any) (string, error)

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
		fragment, err := spec.build(ctx, control, node.Value, &args)
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
func buildLibraryID(_ context.Context, _ *sql.DB, raw json.RawMessage, args *[]any) (string, error) {
	value, err := decodeFilterString(raw, 256)
	if err != nil {
		return "", err
	}
	*args = append(*args, value)
	return "w.library_id = ?", nil
}

func buildSourceID(_ context.Context, _ *sql.DB, raw json.RawMessage, args *[]any) (string, error) {
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
func buildProviderID(_ context.Context, _ *sql.DB, raw json.RawMessage, args *[]any) (string, error) {
	value, err := decodeFilterString(raw, 256)
	if err != nil {
		return "", err
	}
	*args = append(*args, value)
	return "EXISTS (SELECT 1 FROM source_works sw WHERE sw.catalog_revision_id=w.catalog_revision_id AND sw.source_id=w.source_id AND sw.source_key=w.source_key AND sw.provider_id=?)", nil
}

func buildTag(_ context.Context, _ *sql.DB, raw json.RawMessage, args *[]any) (string, error) {
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
func buildCreatorID(ctx context.Context, control *sql.DB, raw json.RawMessage, args *[]any) (string, error) {
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

func buildCreatorRole(_ context.Context, _ *sql.DB, raw json.RawMessage, args *[]any) (string, error) {
	value, err := decodeFilterString(raw, 128)
	if err != nil {
		return "", err
	}
	*args = append(*args, value)
	return "EXISTS (SELECT 1 FROM work_creator_relations r WHERE r.catalog_revision_id=w.catalog_revision_id AND r.overlay_revision_id=w.overlay_revision_id AND r.work_id=w.work_id AND r.role=?)", nil
}

func buildMediaKind(_ context.Context, _ *sql.DB, raw json.RawMessage, args *[]any) (string, error) {
	value, err := decodeFilterString(raw, 64)
	if err != nil {
		return "", err
	}
	*args = append(*args, value)
	return "EXISTS (SELECT 1 FROM media_projections m WHERE m.catalog_revision_id=w.catalog_revision_id AND m.overlay_revision_id=w.overlay_revision_id AND m.work_id=w.work_id AND m.hidden=0 AND m.media_kind=?)", nil
}

func buildMediaLocationAvailable(_ context.Context, _ *sql.DB, raw json.RawMessage, args *[]any) (string, error) {
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

func buildMediaContentVerificationState(_ context.Context, _ *sql.DB, raw json.RawMessage, args *[]any) (string, error) {
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
