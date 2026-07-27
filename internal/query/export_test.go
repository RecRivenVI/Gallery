package query

import (
	"context"
	"fmt"
	"strings"

	contractquery "github.com/RecRivenVI/gallery/internal/contract/query"
	"github.com/RecRivenVI/gallery/internal/querytext"
)

// 本文件只在测试构建中存在，把分页语句构建暴露给同目录的 query_test 包，使
// EXPLAIN QUERY PLAN 与差分回归测试可以断言**真实执行的 SQL**，而不是维护一份随时会与
// 生产语句漂移的副本。

// testAuthorization 按 authorizePublicationSources 的既有语义从候选集合与允许集合推导
// denied 集合，保证测试构造的授权形态与生产路径一致。
func testAuthorization(candidateSourceIDs, allowedSourceIDs []string) sourceAuthorization {
	allowedSet := make(map[string]struct{}, len(allowedSourceIDs))
	for _, sourceID := range allowedSourceIDs {
		allowedSet[sourceID] = struct{}{}
	}
	authorization := sourceAuthorization{
		CandidateSourceIDs:   append([]string(nil), candidateSourceIDs...),
		RequiredCapabilities: []string{"library.read"},
	}
	for _, sourceID := range candidateSourceIDs {
		if _, ok := allowedSet[sourceID]; ok {
			authorization.AllowedSourceIDs = append(authorization.AllowedSourceIDs, sourceID)
		} else {
			authorization.DeniedSourceIDs = append(authorization.DeniedSourceIDs, sourceID)
		}
	}
	return authorization
}

// BuildPageStatementForTest 返回生产分页语句与实参。request 必须已经带上调用方期望的
// Limit/SortDirection，本函数不重复 Search 的默认值填充。
func BuildPageStatementForTest(ctx context.Context, catalogRevision, overlayRevision string,
	candidateSourceIDs, allowedSourceIDs []string, request Request, claims contractquery.CursorClaims) (string, []any, error) {
	filterNode, err := ParseFilter(request.Filter)
	if err != nil {
		return "", nil, err
	}
	service := &Service{}
	return service.buildPageStatement(ctx,
		publication{CatalogRevision: catalogRevision, OverlayRevision: overlayRevision},
		testAuthorization(candidateSourceIDs, allowedSourceIDs), request,
		querytext.PlanSearch(request.Search), filterNode, claims)
}

// BuildSinglePhaseSearchStatementForTest 是两阶段改写之前那种单阶段形态的参照实现，只
// 用作差分校验的 oracle，不参与任何生产路径。
//
// 它与生产语句共享同一个 baseFilter、同一组档位表达式、同一 keyset 谓词和同一 ORDER BY
// 元组，唯一区别是把"选出一页"这件事放在同一层 SELECT 里完成（即排序器直接吞下 scored 的
// 全部候选行并输出前 limit+1 行），而不是先分页再回表。因此如果两阶段改写改变了行集合、
// 顺序或 rank_tier 取值，两者的 (work_id, sort_title_key, rank_tier) 序列必然不同。
//
// media_count 相关子查询保留在 tiers 一层，因为它正是改写前"逐候选行求值"的那个代价；
// 计划回归测试用它作为反向对照，证明两阶段断言不是恒真。其余宽负载列省略：它们在生产
// 语句里来自同一张表的同一主键行，宽窄与否不影响行集合与顺序，负载本身的正确性由
// Service.Search 层的端到端测试覆盖。
func BuildSinglePhaseSearchStatementForTest(ctx context.Context, catalogRevision, overlayRevision string,
	candidateSourceIDs, allowedSourceIDs []string, request Request, claims contractquery.CursorClaims) (string, []any, error) {
	filterNode, err := ParseFilter(request.Filter)
	if err != nil {
		return "", nil, err
	}
	plan := querytext.PlanSearch(request.Search)
	if plan.NormalizedQuery == "" {
		return "", nil, fmt.Errorf("单阶段参照实现只覆盖搜索形态")
	}
	service := &Service{}
	where, join, fromArgs, err := service.baseFilter(ctx,
		publication{CatalogRevision: catalogRevision, OverlayRevision: overlayRevision},
		testAuthorization(candidateSourceIDs, allowedSourceIDs), request, plan, filterNode, false)
	if err != nil {
		return "", nil, err
	}

	operator, direction := ">", "ASC"
	if request.SortDirection == "desc" {
		operator, direction = "<", "DESC"
	}

	statement := fmt.Sprintf(`WITH tiers AS (
SELECT w.work_id AS work_id, w.sort_title_key AS sort_title_key,
%s AS media_count,
(%s) AS title_tier, (%s) AS creator_tier, (%s) AS tag_tier, (%s) AS filename_tier
FROM work_projections w%s WHERE %s
),
scored AS (
SELECT *, max(%s, %s, %s, %s) AS rank_tier FROM tiers
)
SELECT work_id, sort_title_key, media_count, rank_tier FROM scored`,
		mediaCountExpr,
		singleFieldTierSQL("w.search_title_norm"), singleFieldTierSQL("w.search_creator_norm"),
		multiFieldTierSQL("w.search_tags_norm"), multiFieldTierSQL("w.search_filenames_norm"),
		join, strings.Join(where, " AND "),
		combinedFieldScoreSQL("title_tier", fieldPriorityTitle), combinedFieldScoreSQL("creator_tier", fieldPriorityCreator),
		combinedFieldScoreSQL("tag_tier", fieldPriorityTag), combinedFieldScoreSQL("filename_tier", fieldPriorityFilename))

	args := []any{
		plan.NormalizedQuery, plan.NormalizedQuery, plan.NormalizedQuery,
		plan.NormalizedQuery, plan.NormalizedQuery, plan.NormalizedQuery,
		plan.NormalizedQuery, plan.NormalizedQuery, plan.NormalizedQuery,
		plan.NormalizedQuery, plan.NormalizedQuery, plan.NormalizedQuery,
	}
	args = append(args, fromArgs...)
	if claims.LastSortKey != "" {
		statement += fmt.Sprintf(
			" WHERE (rank_tier < ? OR (rank_tier = ? AND (sort_title_key %s ? OR (sort_title_key = ? AND work_id %s ?))))",
			operator, operator)
		args = append(args, claims.LastRankTier, claims.LastRankTier, claims.LastSortKey, claims.LastSortKey, claims.LastCanonicalWorkID)
	}
	statement += fmt.Sprintf(" ORDER BY rank_tier DESC, sort_title_key %s, work_id %s LIMIT ?", direction, direction)
	return statement, append(args, request.Limit+1), nil
}
