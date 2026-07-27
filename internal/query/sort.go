package query

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	contractquery "github.com/RecRivenVI/gallery/internal/contract/query"
)

type workSortValueKind uint8

const (
	workSortText workSortValueKind = iota
	workSortInstant
	workSortProgress
)

// workSortSpec 是公开排序名到 publication 快照列、方向、游标值类型与依赖字段的唯一映射。
// name_* 是旧规则词表对作品标题的别名；查询指纹会收敛到 title_*，避免两个等价名字产生
// 不同游标语义。
type workSortSpec struct {
	name       string
	column     string
	direction  string
	dependency string
	kind       workSortValueKind
	nullLast   bool
}

func resolveWorkSort(value string) (workSortSpec, error) {
	if value == "" {
		value = "title_asc"
	}
	if value == "name_asc" {
		value = "title_asc"
	} else if value == "name_desc" {
		value = "title_desc"
	}
	specs := map[string]workSortSpec{
		"title_asc":     {name: "title_asc", column: "sort_title_key", direction: "ASC", dependency: "title", kind: workSortText},
		"title_desc":    {name: "title_desc", column: "sort_title_key", direction: "DESC", dependency: "title", kind: workSortText},
		"date_asc":      {name: "date_asc", column: "published_at_ns", direction: "ASC", dependency: "publishedAt", kind: workSortInstant, nullLast: true},
		"date_desc":     {name: "date_desc", column: "published_at_ns", direction: "DESC", dependency: "publishedAt", kind: workSortInstant, nullLast: true},
		"progress_asc":  {name: "progress_asc", column: "progress", direction: "ASC", dependency: "overlay.progress", kind: workSortProgress},
		"progress_desc": {name: "progress_desc", column: "progress", direction: "DESC", dependency: "overlay.progress", kind: workSortProgress},
	}
	spec, ok := specs[value]
	if !ok {
		return workSortSpec{}, fmt.Errorf("未知作品排序 %q", value)
	}
	return spec, nil
}

func (s workSortSpec) operator() string {
	if s.direction == "DESC" {
		return "<"
	}
	return ">"
}

func (s workSortSpec) orderBy(sortColumn, workIDColumn string) string {
	parts := make([]string, 0, 3)
	if s.nullLast {
		parts = append(parts, fmt.Sprintf("(%s=0) ASC", sortColumn))
	}
	parts = append(parts, sortColumn+" "+s.direction, workIDColumn+" "+s.direction)
	return strings.Join(parts, ", ")
}

// continuation 构建严格位于游标之后的二级排序谓词。发布日期用 0 表示缺失；无论升降序，
// 缺失值都排在非空值之后，因此不能套用普通的单列比较式。
func (s workSortSpec) continuation(sortColumn, workIDColumn string, claims contractquery.CursorClaims) (string, []any, error) {
	value, err := s.parseCursorKey(claims.LastSortKey)
	if err != nil {
		return "", nil, err
	}
	op := s.operator()
	if s.nullLast {
		instant := value.(int64)
		if instant == 0 {
			return fmt.Sprintf("(%s=0 AND %s %s ?)", sortColumn, workIDColumn, op), []any{claims.LastCanonicalWorkID}, nil
		}
		return fmt.Sprintf("(%s=0 OR %s %s ? OR (%s=? AND %s %s ?))",
				sortColumn, sortColumn, op, sortColumn, workIDColumn, op),
			[]any{instant, instant, claims.LastCanonicalWorkID}, nil
	}
	return fmt.Sprintf("(%s %s ? OR (%s=? AND %s %s ?))", sortColumn, op, sortColumn, workIDColumn, op),
		[]any{value, value, claims.LastCanonicalWorkID}, nil
}

func (s workSortSpec) parseCursorKey(value string) (any, error) {
	switch s.kind {
	case workSortText:
		if len(value) > 8192 {
			return nil, fmt.Errorf("标题排序键过长")
		}
		return value, nil
	case workSortInstant:
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("发布日期排序键无效")
		}
		return parsed, nil
	case workSortProgress:
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < 0 || parsed > 1 {
			return nil, fmt.Errorf("Progress 排序键无效")
		}
		return parsed, nil
	default:
		return nil, fmt.Errorf("未知排序键类型")
	}
}

func (s workSortSpec) formatCursorKey(value any) (string, error) {
	switch s.kind {
	case workSortText:
		switch typed := value.(type) {
		case string:
			return typed, nil
		case []byte:
			return string(typed), nil
		}
	case workSortInstant:
		switch typed := value.(type) {
		case int64:
			return strconv.FormatInt(typed, 10), nil
		case float64:
			if typed >= math.MinInt64 && typed <= math.MaxInt64 && typed == math.Trunc(typed) {
				return strconv.FormatInt(int64(typed), 10), nil
			}
		}
	case workSortProgress:
		switch typed := value.(type) {
		case float64:
			if !math.IsNaN(typed) && !math.IsInf(typed, 0) && typed >= 0 && typed <= 1 {
				return strconv.FormatFloat(typed, 'g', -1, 64), nil
			}
		case int64:
			if typed == 0 || typed == 1 {
				return strconv.FormatInt(typed, 10), nil
			}
		}
	}
	return "", fmt.Errorf("数据库返回了不兼容的排序键类型 %T", value)
}
