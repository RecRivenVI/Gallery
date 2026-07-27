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

// SourceSetAuthorizer 在同一个授权快照中判定 publication 的完整候选 Source 集合。
// requiredCapabilities 使用 all-of 语义；返回值只包含同时满足全部 capability 的 Source。
// 调用方必须提供与当前 Session/API Token 绑定的实现，Query Service 不信任扁平
// capability 列表，也不把某个 Source 的 deny 扩大成整个列表的 deny。
type SourceSetAuthorizer func(ctx context.Context, requiredCapabilities, candidateSourceIDs []string) ([]string, error)

type Request struct {
	Search             string
	Tag                string
	LibraryID          string
	SourceID           string
	Filter             string
	Sort               string
	Limit              int
	Cursor             string
	QueryPublicationID string
	AuthorizationScope string
	// AuthorizeSources 必须 fail-closed：nil 会拒绝查询，返回 error 会使整次查询失败；
	// 返回的允许集合只影响对应 Source，不得把局部 deny 解释为全局 deny。
	AuthorizeSources SourceSetAuthorizer
	OmitTotal        bool
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
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Creator      string   `json:"creator,omitempty"`
	Tags         []string `json:"tags"`
	MediaCount   int      `json:"mediaCount"`
	CoverMediaID string   `json:"coverMediaId,omitempty"`
	// Badges 是该 publication 冻结的规则派生角标，顺序即展示顺序。客户端按 position
	// 渲染，不得自行推导出现条件或配色。
	Badges []domain.Badge `json:"badges,omitempty"`
	// Description、SourceURL 与 PublishedAt 是规则派生的作品标量事实，随快照冻结。
	// PublishedAt 为 nil 表示该作品没有可用发布时间——这是常态而非错误。
	Description string     `json:"description,omitempty"`
	SourceURL   string     `json:"sourceUrl,omitempty"`
	PublishedAt *time.Time `json:"publishedAt,omitempty"`
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

type sourceAuthorization struct {
	CandidateSourceIDs   []string
	AllowedSourceIDs     []string
	DeniedSourceIDs      []string
	RequiredCapabilities []string
}

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
	sortSpec, err := resolveWorkSort(request.Sort)
	if err != nil {
		return Result{}, fault.WithField(fault.CodeValidation, "sort", err)
	}
	request.Sort = sortSpec.name
	plan := querytext.PlanSearch(request.Search)
	if plan.TooShort {
		return Result{}, fault.WithField(fault.CodeQueryTooShort, "q", nil)
	}
	filterNode, err := ParseFilter(request.Filter)
	if err != nil {
		return Result{}, err
	}
	// 显式查询 overlay.hidden 接管该字段的可见性语义，取代默认隐式 hidden=0 条件；
	// 因为这会让原本默认隐藏的 Work 可能出现在结果中，后续对每个 publication
	// candidate Source 除 library.read 外再要求 library.write。这里不能信任 transport
	// 已计算的扁平能力列表，否则会绕过资源 grant、deny 与 API Token scope。
	requiresHiddenWrite := filterReferencesField(filterNode, "overlay.hidden")
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
		"filter": filterCanonical, "sort": request.Sort, "limit": request.Limit,
		"rankProtocolVersion": contractquery.RankProtocolVersion, "dependencySet": dependencyFingerprint,
	})
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
		if claims.QueryFingerprint != queryFingerprint {
			return Result{}, fault.New(fault.CodeCursorExpired, true, nil)
		}
		pub, err = s.publication(ctx, claims.QueryPublicationID)
		if err != nil {
			return Result{}, asExpired(err)
		}
	} else {
		if request.QueryPublicationID != "" {
			pub, err = s.publication(ctx, request.QueryPublicationID)
		} else {
			pub, err = s.currentPublication(ctx)
		}
		if err != nil {
			return Result{}, err
		}
	}
	authorization, err := s.authorizePublicationSources(ctx, pub, request, requiresHiddenWrite)
	if err != nil {
		return Result{}, err
	}
	authHash := authorizationHash(request.AuthorizationScope, authorization)
	if request.Cursor != "" {
		if claims.AuthorizationScopeHash != authHash {
			return Result{}, fault.New(fault.CodeCursorExpired, true, nil)
		}
		if err := s.verifyLease(ctx, claims.LeaseID, pub.ID, authHash); err != nil {
			return Result{}, err
		}
		leaseID = claims.LeaseID
	} else {
		leaseID, err = s.createLease(ctx, pub.ID, authHash)
		if err != nil {
			return Result{}, err
		}
	}
	items, more, err := s.query(ctx, pub, authorization, request, plan, filterNode, claims)
	if err != nil {
		return Result{}, err
	}
	total, err := s.computeTotal(ctx, pub, authorization, request, plan, filterNode)
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

// authorizePublicationSources 在一次授权快照中计算本次请求涉及的有效 Source 集合。
// 回调缺失或执行失败时无法证明任何结果可见，必须 fail-closed。候选集合来自已经确定
// 的 publication；显式 Source/Library 请求只枚举自身范围，避免无关资源权限变化使窄
// 查询的 cursor 失效。
func (s *Service) authorizePublicationSources(ctx context.Context, pub publication, request Request, requireWrite bool) (sourceAuthorization, error) {
	required := []string{"library.read"}
	if requireWrite {
		required = append(required, "library.write")
	}
	if request.AuthorizeSources == nil {
		return sourceAuthorization{}, fault.New(fault.CodeForbidden, false, nil)
	}

	candidates, err := s.publicationSourceIDs(ctx, pub, request)
	if err != nil {
		return sourceAuthorization{}, err
	}
	allowedRaw, err := request.AuthorizeSources(ctx,
		append([]string(nil), required...), append([]string(nil), candidates...))
	if err != nil {
		return sourceAuthorization{}, fault.New(fault.CodeInternal, true, err)
	}

	// 回调输出不是信任边界：只接受 publication 候选集合的交集，重复项与未知 Source
	// 均不能扩大结果。排序使 SQL 参数与 authorization hash 都具有 canonical 表达。
	candidateSet := make(map[string]struct{}, len(candidates))
	for _, sourceID := range candidates {
		candidateSet[sourceID] = struct{}{}
	}
	allowedSet := make(map[string]struct{}, len(allowedRaw))
	for _, sourceID := range allowedRaw {
		if _, candidate := candidateSet[sourceID]; candidate {
			allowedSet[sourceID] = struct{}{}
		}
	}
	allowed := make([]string, 0, len(allowedSet))
	denied := make([]string, 0, len(candidates)-len(allowedSet))
	for _, sourceID := range candidates {
		if _, ok := allowedSet[sourceID]; ok {
			allowed = append(allowed, sourceID)
		} else {
			denied = append(denied, sourceID)
		}
	}
	return sourceAuthorization{
		CandidateSourceIDs: candidates, AllowedSourceIDs: allowed,
		DeniedSourceIDs: denied, RequiredCapabilities: required,
	}, nil
}

// publicationSourceIDs 读取 Catalog revision 发布时冻结的 Source/Library 成员小表，
// 成本只随 Source 数增长。不能改回从 work_projections 做 DISTINCT；后者会扫描
// publication 的全部 Work，并在普通分页请求上反复建立去重结果。
func (s *Service) publicationSourceIDs(ctx context.Context, pub publication, request Request) ([]string, error) {
	if request.SourceID != "" {
		return []string{request.SourceID}, nil
	}

	query := `SELECT source_id FROM catalog_revision_sources
WHERE catalog_revision_id=?`
	args := []any{pub.CatalogRevision}
	if request.LibraryID != "" {
		query += " AND library_id=?"
		args = append(args, request.LibraryID)
	}
	query += " ORDER BY source_id"
	rows, err := s.catalog.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var sourceID string
		if err := rows.Scan(&sourceID); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		result = append(result, sourceID)
	}
	if err := rows.Err(); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	return result, nil
}

// authorizationHash 把调用方的不透明 Principal/Session/Token 身份熵、这次查询实际要求
// 的 capability，以及在目标 publication 上逐资源计算出的允许 Source 集合共同绑定进
// cursor/lease。Grant 或 Token scope 变化导致集合变化时，旧游标立即过期。
func authorizationHash(scope string, authorization sourceAuthorization) string {
	return fingerprint(map[string]any{
		"scope":                strings.Split(scope, "\x00"),
		"requiredCapabilities": authorization.RequiredCapabilities,
		"allowedSourceIds":     authorization.AllowedSourceIDs,
	})
}

// baseFilter 构建结构化过滤、图书馆/来源/标签快捷参数与搜索召回共用的 WHERE 片段，
// 供分页查询与 total 统计复用同一语义，避免两处判据分叉。
func (s *Service) baseFilter(ctx context.Context, pub publication, authorization sourceAuthorization, request Request, plan querytext.SearchPlan, filterNode *FilterNode, forTotal bool) ([]string, string, []any, error) {
	args := []any{pub.CatalogRevision, pub.OverlayRevision}
	where := []string{"w.catalog_revision_id = ?", "w.overlay_revision_id = ?"}
	positiveAllowedFilter := false
	negativeDeniedFilter := false
	// 全允许必须完全省略授权谓词，保留原查询索引快路径；全拒绝直接短路。部分允许
	// 选择较小的 allowed/denied 集合：单元素直接使用等值/不等值，多元素才以单个 JSON
	// 参数物化为 LIST SUBQUERY。这样既避免 SQLite host parameter 上限，也避免
	// correlated json_each 对外层每行重复扫描。
	switch {
	case len(authorization.CandidateSourceIDs) == 0:
		// 缺失或空 membership 不能被误判为“全部允许”；即使 Catalog 数据损坏，
		// 也必须在 SQL 层 fail-closed，不能把未授权 Work 暴露给调用方。
		where = append(where, "1=0")
	case len(authorization.AllowedSourceIDs) == len(authorization.CandidateSourceIDs):
		// 无授权 SQL 谓词。
	case len(authorization.AllowedSourceIDs) == 0:
		where = append(where, "1=0")
	case len(authorization.DeniedSourceIDs) < len(authorization.AllowedSourceIDs):
		negativeDeniedFilter = true
		if len(authorization.DeniedSourceIDs) == 1 {
			where = append(where, "w.source_id <> ?")
			args = append(args, authorization.DeniedSourceIDs[0])
		} else {
			deniedSourcesJSON, _ := json.Marshal(authorization.DeniedSourceIDs)
			where = append(where, "w.source_id NOT IN (SELECT value FROM json_each(?))")
			args = append(args, string(deniedSourcesJSON))
		}
	default:
		if len(authorization.AllowedSourceIDs) == 1 {
			where = append(where, "w.source_id = ?")
			args = append(args, authorization.AllowedSourceIDs[0])
		} else {
			allowedSourcesJSON, _ := json.Marshal(authorization.AllowedSourceIDs)
			where = append(where, "w.source_id IN (SELECT value FROM json_each(?))")
			args = append(args, string(allowedSourcesJSON))
		}
		positiveAllowedFilter = true
	}
	// 客户端显式过滤 overlay.hidden 时由该谓词完全接管可见性语义（buildOverlayHidden
	// 编译进 filterNode 的 SQL 片段），不再叠加默认隐式条件；未显式过滤时保持默认
	// 隐藏 Hidden Work 的既有行为。二者不会同时生效，不产生双重语义。
	if !filterReferencesField(filterNode, "overlay.hidden") {
		where = append(where, "w.hidden = 0")
	}
	fromSuffix := ""
	// 无搜索 browse 按实际收窄维度和排序字段选择能同时服务过滤与稳定排序的索引。FTS 路径由
	// SQLite 根据 work_search 驱动关系自行规划，不在这里强制 WorkProjection 索引。
	if plan.NormalizedQuery == "" {
		sortSpec, err := resolveWorkSort(request.Sort)
		if err != nil {
			return nil, "", nil, err
		}
		indexFor := func(scope string) string {
			suffix := "query"
			switch sortSpec.kind {
			case workSortInstant:
				suffix = "published"
			case workSortProgress:
				suffix = "progress"
			}
			if scope == "global" {
				if suffix == "query" {
					return "work_projections_query_idx"
				}
				return "work_projections_" + suffix + "_idx"
			}
			return "work_projections_" + scope + "_" + suffix + "_idx"
		}
		switch {
		case request.SourceID != "":
			fromSuffix = " INDEXED BY " + indexFor("source")
		case positiveAllowedFilter && (forTotal || len(authorization.AllowedSourceIDs) == 1):
			if forTotal {
				fromSuffix = " INDEXED BY work_projections_source_query_idx"
			} else {
				fromSuffix = " INDEXED BY " + indexFor("source")
			}
		case request.LibraryID != "":
			if forTotal {
				fromSuffix = " INDEXED BY work_projections_library_query_idx"
			} else {
				fromSuffix = " INDEXED BY " + indexFor("library")
			}
		case forTotal && negativeDeniedFilter:
			fromSuffix = " INDEXED BY work_projections_source_query_idx"
		default:
			if forTotal {
				fromSuffix = " INDEXED BY work_projections_query_idx"
			} else {
				fromSuffix = " INDEXED BY " + indexFor("global")
			}
		}
	}
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
			fromSuffix += " JOIN work_search ON work_search.catalog_revision_id=w.catalog_revision_id AND work_search.overlay_revision_id=w.overlay_revision_id AND work_search.work_id=w.work_id"
			where = append(where, "work_search MATCH ?")
			args = append(args, plan.FTSQuery)
		}
	}
	return where, fromSuffix, args, nil
}

// mediaCountExpr 是可见媒体计数的相关子查询：它对携带它的那一行求值一次，成本随
// 求值行数线性增长。因此它只能出现在**分页之后**的投影阶段，绝不能进入排序器的输入。
const mediaCountExpr = "(SELECT count(*) FROM media_projections m WHERE m.catalog_revision_id=w.catalog_revision_id AND m.overlay_revision_id=w.overlay_revision_id AND m.work_id=w.work_id AND m.hidden=0)"

// buildPageStatement 构建一页结果的完整语句与实参。列顺序必须与 query 中 rows.Scan 的
// 顺序保持一致。
//
// 搜索形态采用两阶段：排序阶段只携带排序真正需要的窄列（work_id、sort_title_key 与四个
// 档位列），分页之后再按主键 JOIN 回 work_projections 取宽负载。原因是
// `ORDER BY rank_tier DESC` 是依赖运行期查询串的计算表达式，不可能有索引，所以通过
// WHERE 的全部候选行都必须进排序器，LIMIT 只限制排序器的输出而不限制它的输入。既然排序器
// 规模无法降低，唯一可做的就是缩小它每条记录的宽度，并把 mediaCountExpr 这类逐行代价从
// "逐候选行"降到"逐输出行"（limit+1 次）。
//
// 这是纯粹的等价改写：tiers/scored 的档位计算、keyset 谓词、ORDER BY 元组与 LIMIT 全部
// 不变；JOIN 回表用的是 (catalog_revision_id, overlay_revision_id, work_id) 主键，而
// baseFilter 恒定把同一对 revision 作为前两个条件，因此回表对每个分页行恰好命中一行，
// 既不丢行也不增行，游标三元组 (rank_tier, sort_title_key, work_id) 的取值与来源都不变。
//
// 无搜索的浏览形态不走这条改写：它的 ORDER BY 由 work_projections_query_idx 直接满足，
// 既没有排序器也本来就能提前终止，mediaCountExpr 只对输出行求值；套一层两阶段只会多一次
// 物化而没有任何收益。
func (s *Service) buildPageStatement(ctx context.Context, pub publication, authorization sourceAuthorization, request Request, plan querytext.SearchPlan, filterNode *FilterNode, claims contractquery.CursorClaims) (string, []any, error) {
	where, join, fromArgs, err := s.baseFilter(ctx, pub, authorization, request, plan, filterNode, false)
	if err != nil {
		return "", nil, err
	}

	sortSpec, err := resolveWorkSort(request.Sort)
	if err != nil {
		return "", nil, err
	}
	sortColumn := "w." + sortSpec.column

	if plan.NormalizedQuery == "" {
		statement := fmt.Sprintf(`WITH scored AS (
	SELECT w.work_id, w.title, w.creator, w.tags_json, w.filenames_text, %s AS sort_key, w.favorite, w.progress, w.cover_media_id, w.badges_json, w.description, w.source_url, w.published_at_ns,
	%s AS media_count, 0 AS rank_tier
	FROM work_projections w%s WHERE %s
	)
	SELECT work_id, title, creator, tags_json, filenames_text, sort_key, favorite, progress, cover_media_id, badges_json, description, source_url, published_at_ns, media_count, rank_tier FROM scored`,
			sortColumn, mediaCountExpr, join, strings.Join(where, " AND "))
		args := append([]any{}, fromArgs...)
		if claims.LastCanonicalWorkID != "" {
			// 无搜索时 rank_tier 恒为 0；把它保留在 keyset 谓词会阻止 SQLite
			// 直接利用 sort_title_key/work_id 的索引顺序，并诱发整批排序。
			continuation, continuationArgs, err := sortSpec.continuation("sort_key", "work_id", claims)
			if err != nil {
				return "", nil, err
			}
			statement += " WHERE " + continuation
			args = append(args, continuationArgs...)
		}
		statement += " ORDER BY " + sortSpec.orderBy("sort_key", "work_id") + " LIMIT ?"
		return statement, append(args, request.Limit+1), nil
	}

	// 字段级 ranking：标题/Creator/Tag/文件名各自在内层 tiers CTE 算出 0..3 的
	// match_class，scored CTE 用 combinedFieldScoreSQL 合成 match_class*10+
	// field_priority 后取 max()，保证"完全匹配优于前缀，前缀优于中缀"对全部四个
	// 字段一致成立，且同一 match_class 下按字段优先级排列（标题>Creator>Tag>
	// 文件名）。无搜索词时完全跳过这层，rank_tier 恒为 0，与不带 ranking 时行为一致。
	titleTierSQL := singleFieldTierSQL("w.search_title_norm")
	creatorTierSQL := singleFieldTierSQL("w.search_creator_norm")
	tagTierSQL := multiFieldTierSQL("w.search_tags_norm")
	filenameTierSQL := multiFieldTierSQL("w.search_filenames_norm")

	keysetSQL := ""
	var keysetArgs []any
	if claims.LastCanonicalWorkID != "" {
		continuation, continuationArgs, err := sortSpec.continuation("sort_key", "work_id", claims)
		if err != nil {
			return "", nil, err
		}
		keysetSQL = " WHERE (rank_tier < ? OR (rank_tier = ? AND " + continuation + "))"
		keysetArgs = append([]any{claims.LastRankTier, claims.LastRankTier}, continuationArgs...)
	}

	statement := fmt.Sprintf(`WITH tiers AS (
	SELECT w.work_id AS work_id, %s AS sort_key,
(%s) AS title_tier, (%s) AS creator_tier, (%s) AS tag_tier, (%s) AS filename_tier
FROM work_projections w%s WHERE %s
),
scored AS (
	SELECT work_id, sort_key, max(%s, %s, %s, %s) AS rank_tier FROM tiers
),
page AS (
	SELECT work_id, sort_key, rank_tier FROM scored%s ORDER BY rank_tier DESC, %s LIMIT ?
)
	SELECT p.work_id, w.title, w.creator, w.tags_json, w.filenames_text, p.sort_key, w.favorite, w.progress, w.cover_media_id, w.badges_json, w.description, w.source_url, w.published_at_ns,
%s AS media_count, p.rank_tier
FROM page p JOIN work_projections w ON w.catalog_revision_id = ? AND w.overlay_revision_id = ? AND w.work_id = p.work_id
	ORDER BY p.rank_tier DESC, %s`,
		sortColumn, titleTierSQL, creatorTierSQL, tagTierSQL, filenameTierSQL, join, strings.Join(where, " AND "),
		combinedFieldScoreSQL("title_tier", fieldPriorityTitle), combinedFieldScoreSQL("creator_tier", fieldPriorityCreator),
		combinedFieldScoreSQL("tag_tier", fieldPriorityTag), combinedFieldScoreSQL("filename_tier", fieldPriorityFilename),
		keysetSQL, sortSpec.orderBy("sort_key", "work_id"), mediaCountExpr, sortSpec.orderBy("p.sort_key", "p.work_id"))

	args := []any{
		plan.NormalizedQuery, plan.NormalizedQuery, plan.NormalizedQuery, // title
		plan.NormalizedQuery, plan.NormalizedQuery, plan.NormalizedQuery, // creator
		plan.NormalizedQuery, plan.NormalizedQuery, plan.NormalizedQuery, // tag
		plan.NormalizedQuery, plan.NormalizedQuery, plan.NormalizedQuery, // filename
	}
	args = append(args, fromArgs...)
	args = append(args, keysetArgs...)
	args = append(args, request.Limit+1, pub.CatalogRevision, pub.OverlayRevision)
	return statement, args, nil
}

func (s *Service) query(ctx context.Context, pub publication, authorization sourceAuthorization, request Request, plan querytext.SearchPlan, filterNode *FilterNode, claims contractquery.CursorClaims) ([]Work, bool, error) {
	statement, args, err := s.buildPageStatement(ctx, pub, authorization, request, plan, filterNode, claims)
	if err != nil {
		return nil, false, err
	}

	rows, err := s.catalog.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, false, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	items := make([]Work, 0, request.Limit+1)
	sortSpec, err := resolveWorkSort(request.Sort)
	if err != nil {
		return nil, false, fault.New(fault.CodeInternal, false, err)
	}
	for rows.Next() {
		var work Work
		var tags, filenames, badges string
		var favorite int
		var publishedAtNanos int64
		var rawSortKey any
		if err := rows.Scan(&work.ID, &work.Title, &work.Creator, &tags, &filenames, &rawSortKey,
			&favorite, &work.Progress, &work.CoverMediaID, &badges,
			&work.Description, &work.SourceURL, &publishedAtNanos,
			&work.MediaCount, &work.RankTier); err != nil {
			return nil, false, fault.New(fault.CodeInternal, true, err)
		}
		work.SortKey, err = sortSpec.formatCursorKey(rawSortKey)
		if err != nil {
			return nil, false, fault.New(fault.CodeInternal, false, err)
		}
		work.Favorite = favorite != 0
		// 0 表示该作品没有可用发布时间；不把它表达成 Unix 纪元，否则客户端无法区分
		// 「没有日期」与「日期恰好是 1970」。
		if publishedAtNanos != 0 {
			instant := time.Unix(0, publishedAtNanos).UTC()
			work.PublishedAt = &instant
		}
		// 角标随 publication 冻结，直接按快照内容下发；损坏内容按无角标处理，不让可重建的
		// 展示事实使整个查询失败。
		_ = json.Unmarshal([]byte(badges), &work.Badges)
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
func (s *Service) computeTotal(ctx context.Context, pub publication, authorization sourceAuthorization, request Request, plan querytext.SearchPlan, filterNode *FilterNode) (TotalInfo, error) {
	if request.OmitTotal {
		return TotalInfo{Mode: TotalModeOmitted, ProtocolVersion: TotalProtocolVersion}, nil
	}
	where, join, args, err := s.baseFilter(ctx, pub, authorization, request, plan, filterNode, true)
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

// buildDependencySet 是查询 planner 的核心：根据本次实际请求（而不是字段的静态能力表）
// 生成这次查询真正依赖的字段集合。默认隐式 hidden 可见性、显式过滤字段、搜索命中字段
// 各自贡献一条或多条记录；同一字段在同一次查询里可能因为不同用途出现多次（如
// overlay.progress 既作为过滤条件、"progress" 又作为搜索字段——当前搜索字段固定为
// title/creator/tag/filename，不含 progress，因此暂不会重复，但结构上允许）。
func buildDependencySet(request Request, plan querytext.SearchPlan, filterNode *FilterNode) []DependencyField {
	fields := []DependencyField{{Field: "overlay.customCoverMediaId", Role: DependencyRoleResource}}
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
	sortSpec, err := resolveWorkSort(request.Sort)
	if err == nil {
		fields = append(fields, DependencyField{Field: sortSpec.dependency, Role: DependencyRoleOrdering})
	}
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
