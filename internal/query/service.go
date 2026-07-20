package query

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	contractquery "github.com/RecRivenVI/gallery/internal/contract/query"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/ports"
	"github.com/RecRivenVI/gallery/internal/querytext"
)

const CursorLeaseDuration = 5 * time.Minute

// TotalProtocolVersion 标识 total 字段的表达版本，供未来预算/策略演进时区分。
const TotalProtocolVersion = 1

// TotalBudget 是精确计数与下限估算的分界：WHERE 命中行数不超过该值时返回精确值，
// 否则返回 lower_bound=TotalBudget，避免普通列表路径执行无上限全库 COUNT。变量
// （非常量）是有意为之：PRE_FREEZE，正式预算与默认策略留待下一轮真实规模压力测试后
// 冻结，测试可临时调整以验证 lower_bound 分支而不必构造万级合成语料。
var TotalBudget int64 = 10000

// TotalMode 区分 total 语义：exact 精确、lower_bound 命中数超过预算的下限估算、
// omitted 客户端显式跳过统计。
type TotalMode string

const (
	TotalModeExact      TotalMode = "exact"
	TotalModeLowerBound TotalMode = "lower_bound"
	TotalModeOmitted    TotalMode = "omitted"
)

type TotalInfo struct {
	Mode            TotalMode `json:"mode"`
	Value           *int64    `json:"value,omitempty"`
	ProtocolVersion int       `json:"protocolVersion"`
}

// MatchSpan 是原文 code point（rune）偏移，左闭右开；不是 UTF-16 code unit，也不是
// 字节偏移。
type MatchSpan struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// FieldMatch 是通用、版本化的命中表达：field 取值为 "title"、"creator"、"tag"、
// "filename" 之一；value 是命中的原始显示值（tag/filename 为具体命中的那一个取值，
// 不是整个列表；filename 只是 path.Base 之后的安全显示名，不泄露相对/绝对路径）；
// spans 是该 value 内的命中区间列表（同一字段同一 value 可能有多段）。同一 Work 可以
// 同时出现多个同字段条目（例如两个不同的 tag 分别命中）。
type FieldMatch struct {
	Field string      `json:"field"`
	Value string      `json:"value"`
	Spans []MatchSpan `json:"spans"`
}

// maxMatchesPerWork 是单个 Work 返回的命中条目数上限，避免病态数据（超大量 tag/文件名）
// 让单条结果的高亮表达无界增长。
const maxMatchesPerWork = 8

// maxMatchValueRunes 是单个命中 value 展示文本的最大 rune 数，超出截断（标题已在写入
// 时限制 4096 rune，这里是防御性上限，不代表当前存在更大的合法输入）。
const maxMatchValueRunes = 512

// DependencyField 是本次查询实际用到的一个依赖字段：Field 是字段名（复用 filter 字段
// 命名空间，如 overlay.favorite，或 "title"/"creator"/"tag"/"filename" 表示参与搜索/
// 排序的内容字段），Role 说明这次查询里它被用作什么用途。dependencySet 由 planner
// 按实际请求生成，不是把全部已注册字段的静态能力表当成这次查询的依赖集合。
type DependencyField struct {
	Field string `json:"field"`
	Role  string `json:"role"` // predicate | ordering | search | membership | resource
}

const (
	DependencyRolePredicate  = "predicate"
	DependencyRoleOrdering   = "ordering"
	DependencyRoleSearch     = "search"
	DependencyRoleMembership = "membership"
	DependencyRoleResource   = "resource"
)

// LiveUserStateFields 列出当前哪些 Overlay 字段除了本响应中的 snapshot 值以外，还可以
// 通过 GET /works/{workId}/overlay 读取 control.db 当前 live 值；不属于每次查询动态
// 变化的 dependency set，是静态能力声明（见 overlay.OverlayFieldCapabilities 的
// LiveUserState 能力位，两处必须保持一致，由 dependency_test.go 锁定）。
var LiveUserStateFields = []string{"favorite", "progress"}

type Request struct {
	Search             string
	Tag                string
	LibraryID          string
	SourceID           string
	Filter             string
	SortDirection      string
	Limit              int
	Cursor             string
	QueryPublicationID string
	AuthorizationScope string
	// Capabilities 是调用方当前 effective capability 列表，用于判定是否允许显式查询
	// overlay.hidden=true（见 requireHiddenCapability）。与 AuthorizationScope 分开
	// 传递：后者只是不透明的游标/缓存身份熵输入，不应被反解析出 capability 列表。
	Capabilities []string
	OmitTotal    bool
}

type Result struct {
	QueryPublicationID        string            `json:"queryPublicationId"`
	CatalogRevision           string            `json:"catalogRevision"`
	OverlayProjectionRevision string            `json:"overlayProjectionRevision"`
	SortProtocolVersion       int               `json:"sortProtocolVersion"`
	RankProtocolVersion       int               `json:"rankProtocolVersion"`
	Items                     []Work            `json:"items"`
	Total                     TotalInfo         `json:"total"`
	DependencySet             []DependencyField `json:"dependencySet"`
	LiveUserStateFields       []string          `json:"liveUserStateFields"`
	NextCursor                string            `json:"nextCursor,omitempty"`
}

type Work struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Creator    string   `json:"creator,omitempty"`
	Tags       []string `json:"tags"`
	MediaCount int      `json:"mediaCount"`
	// Favorite/Progress 是本次查询所在 publication 冻结的 snapshot 值（用于解释本次
	// 结果的过滤/排序判据），不是 control.db 当前 live 值；真正的 live 值通过
	// GET /works/{workId}/overlay 读取，见 LiveUserStateFields。
	Favorite bool         `json:"favorite"`
	Progress float64      `json:"progress"`
	Matches  []FieldMatch `json:"matches,omitempty"`
	SortKey  string       `json:"-"`
	RankTier int          `json:"-"`
}

type publication struct{ ID, CatalogRevision, OverlayRevision string }

type Service struct {
	control *sql.DB
	catalog *sql.DB
	clock   ports.Clock
	random  io.Reader
	signer  *contractquery.CursorSigner
}

func NewService(ctx context.Context, control, catalog *sql.DB, clock ports.Clock, random io.Reader) (*Service, error) {
	if control == nil || catalog == nil || clock == nil {
		return nil, fmt.Errorf("Query Service 缺少依赖")
	}
	if random == nil {
		random = rand.Reader
	}
	key, err := loadOrCreateSigningKey(ctx, control, clock, random)
	if err != nil {
		return nil, err
	}
	signer, err := contractquery.NewCursorSigner(key, clock)
	if err != nil {
		return nil, err
	}
	return &Service{control: control, catalog: catalog, clock: clock, random: random, signer: signer}, nil
}

func (s *Service) Search(ctx context.Context, request Request) (Result, error) {
	if request.Limit == 0 {
		request.Limit = 50
	}
	if request.Limit < 1 || request.Limit > 200 {
		return Result{}, fault.WithField(fault.CodeValidation, "limit", nil)
	}
	if request.SortDirection == "" {
		request.SortDirection = "asc"
	}
	if request.SortDirection != "asc" && request.SortDirection != "desc" {
		return Result{}, fault.WithField(fault.CodeValidation, "sortDirection", nil)
	}
	plan := querytext.PlanSearch(request.Search)
	if plan.TooShort {
		return Result{}, fault.WithField(fault.CodeQueryTooShort, "q", nil)
	}
	filterNode, err := ParseFilter(request.Filter)
	if err != nil {
		return Result{}, err
	}
	// 显式查询 overlay.hidden 接管该字段的可见性语义，取代默认隐式 hidden=0 条件；
	// 因为这会让原本默认隐藏的 Work 可能出现在结果中，要求 library.write capability，
	// 避免只读账户绕过默认隐藏可见性。hidden=false 与默认行为等价，但同样按"显式接管"
	// 统一要求，不制造"值不同、门槛不同"的隐性特例。
	if filterReferencesField(filterNode, "overlay.hidden") && !hasCapability(request.Capabilities, "library.write") {
		return Result{}, fault.New(fault.CodeForbidden, false, nil)
	}
	dependencySet := buildDependencySet(request, plan, filterNode)
	var filterCanonical string
	if filterNode != nil {
		filterCanonical = filterNode.canonicalJSON()
	}
	dependencyFingerprint := make([]string, 0, len(dependencySet))
	for _, field := range dependencySet {
		dependencyFingerprint = append(dependencyFingerprint, field.Field+":"+field.Role)
	}
	queryFingerprint := fingerprint(map[string]any{
		"q": plan.NormalizedQuery, "tag": request.Tag, "libraryId": request.LibraryID, "sourceId": request.SourceID,
		"filter": filterCanonical, "sort": "title", "direction": request.SortDirection, "limit": request.Limit,
		"rankProtocolVersion": contractquery.RankProtocolVersion, "dependencySet": dependencyFingerprint,
	})
	authHash := fingerprint(strings.Split(request.AuthorizationScope, "\x00"))
	var claims contractquery.CursorClaims
	var pub publication
	var leaseID string
	if request.Cursor != "" {
		claims, err = s.signer.Verify(request.Cursor)
		if err != nil {
			return Result{}, err
		}
		if request.QueryPublicationID != "" && request.QueryPublicationID != claims.QueryPublicationID {
			return Result{}, fault.New(fault.CodeCursorExpired, true, nil)
		}
		if claims.QueryFingerprint != queryFingerprint || claims.AuthorizationScopeHash != authHash {
			return Result{}, fault.New(fault.CodeCursorExpired, true, nil)
		}
		pub, err = s.publication(ctx, claims.QueryPublicationID)
		if err != nil {
			return Result{}, asExpired(err)
		}
		if err := s.verifyLease(ctx, claims.LeaseID, pub.ID, authHash); err != nil {
			return Result{}, err
		}
		leaseID = claims.LeaseID
	} else {
		if request.QueryPublicationID != "" {
			pub, err = s.publication(ctx, request.QueryPublicationID)
		} else {
			pub, err = s.currentPublication(ctx)
		}
		if err != nil {
			return Result{}, err
		}
		leaseID, err = s.createLease(ctx, pub.ID, authHash)
		if err != nil {
			return Result{}, err
		}
	}
	items, more, err := s.query(ctx, pub, request, plan, filterNode, claims)
	if err != nil {
		return Result{}, err
	}
	total, err := s.computeTotal(ctx, pub, request, plan, filterNode)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		QueryPublicationID: pub.ID, CatalogRevision: pub.CatalogRevision, OverlayProjectionRevision: pub.OverlayRevision,
		SortProtocolVersion: contractquery.SortProtocolVersion, RankProtocolVersion: contractquery.RankProtocolVersion,
		Items: items, Total: total, DependencySet: dependencySet, LiveUserStateFields: append([]string(nil), LiveUserStateFields...),
	}
	if more && len(items) > 0 {
		last := items[len(items)-1]
		now := s.clock.Now().UTC()
		result.NextCursor, err = s.signer.Issue(contractquery.CursorClaims{
			QueryFingerprint: queryFingerprint, SortProtocolVersion: contractquery.SortProtocolVersion,
			RankProtocolVersion: contractquery.RankProtocolVersion,
			QueryPublicationID:  pub.ID, AuthorizationScopeHash: authHash, LastSortKey: last.SortKey, LastRankTier: last.RankTier,
			LastCanonicalWorkID: last.ID, IssuedAt: now, LeaseID: leaseID, ExpiresAt: now.Add(CursorLeaseDuration),
		})
		if err != nil {
			return Result{}, err
		}
	}
	return result, nil
}

// fieldPriority 是 ranking 元组的字段优先级分量：同一 match_class 下，标题优先于
// Creator，优先于 Tag，优先于文件名。具体数值仍是 PRE_FREEZE（见 01-v1实施计划.md），
// 但字段级结构本身（哪个字段更优先）已经冻结，变化需要升级 RankProtocolVersion。
const (
	fieldPriorityTitle    = 3
	fieldPriorityCreator  = 2
	fieldPriorityTag      = 1
	fieldPriorityFilename = 0
)

// singleFieldTierSQL 为单值规范化字段（标题/Creator）构建 match_class 表达式：
// 3=与查询完全相等，2=以查询为前缀，1=包含查询子串，0=都不匹配。三个占位符都绑定
// 同一个 plan.NormalizedQuery 值。
func singleFieldTierSQL(column string) string {
	return fmt.Sprintf("CASE WHEN %s = ? THEN 3 WHEN instr(%s, ?) = 1 THEN 2 WHEN instr(%s, ?) > 0 THEN 1 ELSE 0 END", column, column, column)
}

// multiFieldTierSQL 为按 querytext.FieldSeparator（U+001F）连接的多值规范化字段
// （Tag/文件名）构建同样的 match_class 表达式：3=某个取值与查询完全相等，2=某个取值
// 以查询为前缀，1=连接文本中出现查询子串（可能跨越取值边界，作为召回层级的已记录
// 简化，不影响"某个具体取值完全/前缀匹配"这两个更高层级的精确判定），0=都不匹配。
// 分隔符是控制字符，不需要对查询值做 LIKE 通配符转义。
func multiFieldTierSQL(column string) string {
	wrapped := "(char(31) || " + column + " || char(31))"
	return fmt.Sprintf("CASE WHEN instr(%s, char(31) || ? || char(31)) > 0 THEN 3 WHEN instr(%s, char(31) || ?) > 0 THEN 2 WHEN instr(%s, ?) > 0 THEN 1 ELSE 0 END", wrapped, wrapped, column)
}

// combinedFieldScoreSQL 把一个字段的 0..3 match_class 列（tierColumn，已在内层 CTE 计算
// 好）与其固定 field_priority 合成一个可直接比较大小的整数：未命中(0)时贡献 0，
// 命中时贡献 match_class*10+field_priority，从而保证"完全匹配优于前缀，前缀优于
// 中缀"在任何字段组合下都成立（match_class 是十位，field_priority 只在同一
// match_class 内部充当次级排序，不会让低 match_class 的高优先级字段反超）。
func combinedFieldScoreSQL(tierColumn string, priority int) string {
	return fmt.Sprintf("CASE WHEN %s = 0 THEN 0 ELSE %s * 10 + %d END", tierColumn, tierColumn, priority)
}

// baseFilter 构建结构化过滤、图书馆/来源/标签快捷参数与搜索召回共用的 WHERE 片段，
// 供分页查询与 total 统计复用同一语义，避免两处判据分叉。
func (s *Service) baseFilter(ctx context.Context, pub publication, request Request, plan querytext.SearchPlan, filterNode *FilterNode) ([]string, string, []any, error) {
	args := []any{pub.CatalogRevision, pub.OverlayRevision}
	where := []string{"w.catalog_revision_id = ?", "w.overlay_revision_id = ?"}
	// 客户端显式过滤 overlay.hidden 时由该谓词完全接管可见性语义（buildOverlayHidden
	// 编译进 filterNode 的 SQL 片段），不再叠加默认隐式条件；未显式过滤时保持默认
	// 隐藏 Hidden Work 的既有行为。二者不会同时生效，不产生双重语义。
	if !filterReferencesField(filterNode, "overlay.hidden") {
		where = append(where, "w.hidden = 0")
	}
	join := ""
	if request.LibraryID != "" {
		where = append(where, "w.library_id = ?")
		args = append(args, request.LibraryID)
	}
	if request.SourceID != "" {
		where = append(where, "w.source_id = ?")
		args = append(args, request.SourceID)
	}
	if request.Tag != "" {
		where = append(where, "EXISTS (SELECT 1 FROM json_each(w.tags_json) WHERE value = ?)")
		args = append(args, request.Tag)
	}
	if filterNode != nil {
		filterSQL, filterArgs, err := compileFilter(ctx, s.control, filterNode)
		if err != nil {
			return nil, "", nil, err
		}
		where = append(where, filterSQL)
		args = append(args, filterArgs...)
	}
	if plan.NormalizedQuery != "" {
		where = append(where, "instr(w.normalized_original_text, ?) > 0")
		args = append(args, plan.NormalizedQuery)
		if plan.FTSQuery != "" {
			join = " JOIN work_search ON work_search.catalog_revision_id=w.catalog_revision_id AND work_search.overlay_revision_id=w.overlay_revision_id AND work_search.work_id=w.work_id"
			where = append(where, "work_search MATCH ?")
			args = append(args, plan.FTSQuery)
		}
	}
	return where, join, args, nil
}

func (s *Service) query(ctx context.Context, pub publication, request Request, plan querytext.SearchPlan, filterNode *FilterNode, claims contractquery.CursorClaims) ([]Work, bool, error) {
	where, join, fromArgs, err := s.baseFilter(ctx, pub, request, plan, filterNode)
	if err != nil {
		return nil, false, err
	}

	mediaCountExpr := "(SELECT count(*) FROM media_projections m WHERE m.catalog_revision_id=w.catalog_revision_id AND m.overlay_revision_id=w.overlay_revision_id AND m.work_id=w.work_id AND m.hidden=0)"
	var cte string
	var selectArgs []any
	if plan.NormalizedQuery != "" {
		// 字段级 ranking：标题/Creator/Tag/文件名各自在内层 tiers CTE 算出 0..3 的
		// match_class，外层 scored CTE 用 combinedFieldScoreSQL 合成 match_class*10+
		// field_priority 后取 max()，保证"完全匹配优于前缀，前缀优于中缀"对全部四个
		// 字段一致成立，且同一 match_class 下按字段优先级排列（标题>Creator>Tag>
		// 文件名）。无搜索词时完全跳过这层，rank_tier 恒为 0，与不带 ranking 时行为
		// 一致。
		titleTierSQL := singleFieldTierSQL("w.search_title_norm")
		creatorTierSQL := singleFieldTierSQL("w.search_creator_norm")
		tagTierSQL := multiFieldTierSQL("w.search_tags_norm")
		filenameTierSQL := multiFieldTierSQL("w.search_filenames_norm")
		cte = fmt.Sprintf(`WITH tiers AS (
SELECT w.work_id, w.title, w.creator, w.tags_json, w.filenames_text, w.sort_title_key, w.favorite, w.progress,
%s AS media_count,
(%s) AS title_tier, (%s) AS creator_tier, (%s) AS tag_tier, (%s) AS filename_tier
FROM work_projections w%s WHERE %s
),
scored AS (
SELECT *, max(%s, %s, %s, %s) AS rank_tier FROM tiers
)`, mediaCountExpr, titleTierSQL, creatorTierSQL, tagTierSQL, filenameTierSQL, join, strings.Join(where, " AND "),
			combinedFieldScoreSQL("title_tier", fieldPriorityTitle), combinedFieldScoreSQL("creator_tier", fieldPriorityCreator),
			combinedFieldScoreSQL("tag_tier", fieldPriorityTag), combinedFieldScoreSQL("filename_tier", fieldPriorityFilename))
		selectArgs = []any{
			plan.NormalizedQuery, plan.NormalizedQuery, plan.NormalizedQuery, // title
			plan.NormalizedQuery, plan.NormalizedQuery, plan.NormalizedQuery, // creator
			plan.NormalizedQuery, plan.NormalizedQuery, plan.NormalizedQuery, // tag
			plan.NormalizedQuery, plan.NormalizedQuery, plan.NormalizedQuery, // filename
		}
	} else {
		cte = fmt.Sprintf(`WITH scored AS (
SELECT w.work_id, w.title, w.creator, w.tags_json, w.filenames_text, w.sort_title_key, w.favorite, w.progress,
%s AS media_count, 0 AS rank_tier
FROM work_projections w%s WHERE %s
)`, mediaCountExpr, join, strings.Join(where, " AND "))
	}

	operator, direction := ">", "ASC"
	if request.SortDirection == "desc" {
		operator, direction = "<", "DESC"
	}

	var outerWhere []string
	var outerArgs []any
	if claims.LastSortKey != "" {
		outerWhere = append(outerWhere, fmt.Sprintf(
			"(rank_tier < ? OR (rank_tier = ? AND (sort_title_key %s ? OR (sort_title_key = ? AND work_id %s ?))))",
			operator, operator))
		outerArgs = append(outerArgs, claims.LastRankTier, claims.LastRankTier, claims.LastSortKey, claims.LastSortKey, claims.LastCanonicalWorkID)
	}

	statement := cte + `
SELECT work_id, title, creator, tags_json, filenames_text, sort_title_key, favorite, progress, media_count, rank_tier FROM scored`

	args := append(append([]any{}, selectArgs...), fromArgs...)
	if len(outerWhere) > 0 {
		statement += " WHERE " + strings.Join(outerWhere, " AND ")
		args = append(args, outerArgs...)
	}
	statement += fmt.Sprintf(" ORDER BY rank_tier DESC, sort_title_key %s, work_id %s LIMIT ?", direction, direction)
	args = append(args, request.Limit+1)

	rows, err := s.catalog.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, false, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	items := make([]Work, 0, request.Limit+1)
	for rows.Next() {
		var work Work
		var tags, filenames string
		var favorite int
		if err := rows.Scan(&work.ID, &work.Title, &work.Creator, &tags, &filenames, &work.SortKey,
			&favorite, &work.Progress, &work.MediaCount, &work.RankTier); err != nil {
			return nil, false, fault.New(fault.CodeInternal, true, err)
		}
		work.Favorite = favorite != 0
		_ = json.Unmarshal([]byte(tags), &work.Tags)
		if work.Tags == nil {
			work.Tags = []string{}
		}
		if plan.NormalizedQuery != "" {
			var filenameList []string
			_ = json.Unmarshal([]byte(filenames), &filenameList)
			work.Matches = computeMatches(plan.NormalizedQuery, work.Title, work.Creator, work.Tags, filenameList)
		}
		items = append(items, work)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fault.New(fault.CodeInternal, true, err)
	}
	more := len(items) > request.Limit
	if more {
		items = items[:request.Limit]
	}
	return items, more, nil
}

// computeMatches 为标题/Creator/Tag/文件名逐一计算命中区间，产出通用高亮 DTO。同一
// 字段可能出现多个条目（例如两个不同的 tag 分别命中，各自携带自己的 spans）；结果按
// maxMatchesPerWork 截断，value 按 maxMatchValueRunes 截断（防御性上限）。
//
// span 必须先针对完整原文计算（HighlightSpans 依赖簇边界与规范化折叠映射，不能对已经
// 截断的半截文本重新计算，否则会改变簇划分与命中结果），再按截断后实际返回的 value
// 裁剪：起点落在截断边界之后的 span 整体丢弃（用户看不到这次命中，展示它没有意义），
// 跨越边界的 span 把 End 收紧到截断长度，确保每个返回的 span 都满足
// 0 <= start <= end <= runeCount(value)，不指向 value 之外的字符。
func computeMatches(normalizedQuery, title, creator string, tags, filenames []string) []FieldMatch {
	var matches []FieldMatch
	add := func(field, value string) {
		if len(matches) >= maxMatchesPerWork {
			return
		}
		spans := querytext.HighlightSpans(value, normalizedQuery)
		if len(spans) == 0 {
			return
		}
		truncated := truncateRunes(value, maxMatchValueRunes)
		truncatedRuneCount := len([]rune(truncated))
		converted := make([]MatchSpan, 0, len(spans))
		for _, span := range spans {
			if span.Start >= truncatedRuneCount {
				continue
			}
			end := span.End
			if end > truncatedRuneCount {
				end = truncatedRuneCount
			}
			if end <= span.Start {
				continue
			}
			converted = append(converted, MatchSpan{Start: span.Start, End: end})
		}
		if len(converted) == 0 {
			return
		}
		matches = append(matches, FieldMatch{Field: field, Value: truncated, Spans: converted})
	}
	add("title", title)
	add("creator", creator)
	for _, tag := range tags {
		add("tag", tag)
	}
	for _, filename := range filenames {
		add("filename", filename)
	}
	return matches
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

// computeTotal 复用 baseFilter 的相同判据，只在命中行数超过 TotalBudget 时退化为
// lower_bound，避免普通列表路径执行无上限全库 COUNT。
func (s *Service) computeTotal(ctx context.Context, pub publication, request Request, plan querytext.SearchPlan, filterNode *FilterNode) (TotalInfo, error) {
	if request.OmitTotal {
		return TotalInfo{Mode: TotalModeOmitted, ProtocolVersion: TotalProtocolVersion}, nil
	}
	where, join, args, err := s.baseFilter(ctx, pub, request, plan, filterNode)
	if err != nil {
		return TotalInfo{}, err
	}
	statement := "SELECT count(*) FROM (SELECT 1 FROM work_projections w" + join + " WHERE " + strings.Join(where, " AND ") + " LIMIT ?)"
	args = append(args, TotalBudget+1)
	var count int64
	if err := s.catalog.QueryRowContext(ctx, statement, args...).Scan(&count); err != nil {
		return TotalInfo{}, fault.New(fault.CodeInternal, true, err)
	}
	if count > TotalBudget {
		value := TotalBudget
		return TotalInfo{Mode: TotalModeLowerBound, Value: &value, ProtocolVersion: TotalProtocolVersion}, nil
	}
	return TotalInfo{Mode: TotalModeExact, Value: &count, ProtocolVersion: TotalProtocolVersion}, nil
}

func (s *Service) currentPublication(ctx context.Context) (publication, error) {
	var id string
	err := s.catalog.QueryRowContext(ctx, "SELECT query_publication_id FROM active_query_publication WHERE singleton=1").Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return publication{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return publication{}, fault.New(fault.CodeInternal, true, err)
	}
	return s.publication(ctx, id)
}

func (s *Service) publication(ctx context.Context, id string) (publication, error) {
	if _, err := domain.ParseID(domain.IDQueryPublication, id); err != nil {
		return publication{}, fault.New(fault.CodeNotFound, false, nil)
	}
	var result publication
	err := s.catalog.QueryRowContext(ctx, "SELECT query_publication_id, catalog_revision_id, overlay_revision_id FROM query_publications WHERE query_publication_id=?", id).Scan(&result.ID, &result.CatalogRevision, &result.OverlayRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return publication{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return publication{}, fault.New(fault.CodeInternal, true, err)
	}
	return result, nil
}

func (s *Service) createLease(ctx context.Context, publicationID, authHash string) (string, error) {
	buffer := make([]byte, 16)
	if _, err := io.ReadFull(s.random, buffer); err != nil {
		return "", fault.New(fault.CodeInternal, true, err)
	}
	id := "lease_" + hex.EncodeToString(buffer)
	now := s.clock.Now().UTC()
	_, err := s.catalog.ExecContext(ctx, "INSERT INTO query_publication_leases (lease_id, query_publication_id, authorization_scope_hash, expires_at, created_at) VALUES (?, ?, ?, ?, ?)", id, publicationID, authHash, now.Add(CursorLeaseDuration).Unix(), now.Unix())
	if err != nil {
		return "", fault.New(fault.CodeInternal, true, err)
	}
	return id, nil
}

func (s *Service) verifyLease(ctx context.Context, leaseID, publicationID, authHash string) error {
	var expires int64
	err := s.catalog.QueryRowContext(ctx, "SELECT expires_at FROM query_publication_leases WHERE lease_id=? AND query_publication_id=? AND authorization_scope_hash=?", leaseID, publicationID, authHash).Scan(&expires)
	if err != nil || s.clock.Now().Unix() >= expires {
		return fault.New(fault.CodeCursorExpired, true, nil)
	}
	return nil
}

func loadOrCreateSigningKey(ctx context.Context, db *sql.DB, clock ports.Clock, random io.Reader) ([]byte, error) {
	var key []byte
	err := db.QueryRowContext(ctx, "SELECT key_bytes FROM query_signing_keys WHERE key_version=1").Scan(&key)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	key = make([]byte, 32)
	if _, err := io.ReadFull(random, key); err != nil {
		return nil, err
	}
	_, err = db.ExecContext(ctx, "INSERT OR IGNORE INTO query_signing_keys (key_version, key_bytes, created_at) VALUES (1, ?, ?)", key, clock.Now().Unix())
	if err != nil {
		return nil, err
	}
	err = db.QueryRowContext(ctx, "SELECT key_bytes FROM query_signing_keys WHERE key_version=1").Scan(&key)
	return key, err
}

func fingerprint(value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func asExpired(err error) error {
	var structured *fault.Error
	if errors.As(err, &structured) && structured.Code == fault.CodeNotFound {
		return fault.New(fault.CodeCursorExpired, true, nil)
	}
	return err
}

func hasCapability(capabilities []string, capability string) bool {
	for _, value := range capabilities {
		if value == capability {
			return true
		}
	}
	return false
}

// buildDependencySet 是查询 planner 的核心：根据本次实际请求（而不是字段的静态能力表）
// 生成这次查询真正依赖的字段集合。默认隐式 hidden 可见性、显式过滤字段、搜索命中字段
// 各自贡献一条或多条记录；同一字段在同一次查询里可能因为不同用途出现多次（如
// overlay.progress 既作为过滤条件、"progress" 又作为搜索字段——当前搜索字段固定为
// title/creator/tag/filename，不含 progress，因此暂不会重复，但结构上允许）。
func buildDependencySet(request Request, plan querytext.SearchPlan, filterNode *FilterNode) []DependencyField {
	var fields []DependencyField
	if filterReferencesField(filterNode, "overlay.hidden") {
		fields = append(fields, DependencyField{Field: "overlay.hidden", Role: DependencyRolePredicate})
	} else {
		fields = append(fields, DependencyField{Field: "overlay.hidden", Role: DependencyRoleMembership})
	}
	for _, name := range collectFilterFields(filterNode) {
		if name == "overlay.hidden" {
			continue
		}
		fields = append(fields, DependencyField{Field: name, Role: DependencyRolePredicate})
	}
	if request.Tag != "" {
		fields = append(fields, DependencyField{Field: "tag", Role: DependencyRolePredicate})
	}
	if request.LibraryID != "" {
		fields = append(fields, DependencyField{Field: "library.id", Role: DependencyRolePredicate})
	}
	if request.SourceID != "" {
		fields = append(fields, DependencyField{Field: "source.id", Role: DependencyRolePredicate})
	}
	fields = append(fields, DependencyField{Field: "title", Role: DependencyRoleOrdering})
	if plan.NormalizedQuery != "" {
		fields = append(fields,
			DependencyField{Field: "title", Role: DependencyRoleSearch},
			DependencyField{Field: "creator", Role: DependencyRoleSearch},
			DependencyField{Field: "tag", Role: DependencyRoleSearch},
			DependencyField{Field: "filename", Role: DependencyRoleSearch},
		)
	}
	return fields
}

func AuthorizationScope(principal string, capabilities []string) string {
	copyCapabilities := append([]string(nil), capabilities...)
	sort.Strings(copyCapabilities)
	return principal + "\x00" + strings.Join(copyCapabilities, "\x00")
}
