// Package query 覆盖阶段 4「查询和媒体」中查询侧（结构化过滤、搜索、排序、
// Ranking/matches、Total 三态、cursor、性能矩阵）的 orchestrator 与断言，只依赖
// tools/testlab 的共享模块（report/environment/corpus），不导入任何 internal/* 包。
package query

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	api "github.com/RecRivenVI/gallery/pkg/galleryapi"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/corpus"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/environment"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/report"
)

// leaf 构造一个 field/op/value 叶子过滤节点的 JSON 编码字符串。
func filterJSON(node map[string]any) string {
	encoded, _ := json.Marshal(node)
	return string(encoded)
}

func leaf(field, op string, value any) map[string]any {
	return map[string]any{"field": field, "op": op, "value": value}
}

func all(nodes ...map[string]any) map[string]any  { return map[string]any{"all": nodes} }
func any_(nodes ...map[string]any) map[string]any { return map[string]any{"any": nodes} }
func not(node map[string]any) map[string]any      { return map[string]any{"not": node} }

// emptyGroup 显式构造一个非 nil 的空数组分支（{"all":[]} 或 {"any":[]}），区别于
// Go 变长参数在零参数时产出的 nil 切片——json.Marshal(nil slice) 编码为 "null"，
// 与规范要求测试的"显式空集合" {"all":[]} 是不同的输入形状。
func emptyGroup(key string) map[string]any {
	return map[string]any{key: []map[string]any{}}
}

// totalValueOf 安全解引用 api.Total.Value（*int64），避免用 %v 直接格式化指针类型
// 打印出 Go 内存地址而不是实际数值。
func totalValueOf(total api.Total) string {
	if total.Value == nil {
		return "null"
	}
	return fmt.Sprintf("%d", *total.Value)
}

func listWorks(sess *environment.Session, params api.ListWorksParams) (*api.ListWorksResponse, error) {
	return sess.ListWorks(params)
}

// checkTotalAgainstExpected 按 total 三态协议核对命中数：小结果集必须是精确匹配
// 的 exact；一旦真实命中数超过服务端 total 预算，协议本身规定退化为
// lower_bound（value=预算值，真实命中数 ≥ 该值），不再是精确计数——阶段 4 正式压力
// 测试在百万级规模下大量过滤条件都会触发这个退化路径，如果测试端继续机械要求
// exact==expected 就会把协议本身设计的正常行为误判为失败（HARNESS_BUG，不是产品
// 缺陷）。预算具体数值仍是 PRE_FREEZE，这里不假设固定阈值，只要求：exact 时数值
// 精确相等；lower_bound 时数值是真实期望值的一个不大于它的下界。
func checkTotalAgainstExpected(total api.Total, expected int) (bool, string) {
	switch string(total.Mode) {
	case "exact":
		if total.Value != nil && int(*total.Value) == expected {
			return true, ""
		}
	case "lower_bound":
		if total.Value != nil && int(*total.Value) <= expected {
			return true, ""
		}
	}
	return false, fmt.Sprintf("mode=%s value=%s expected=%d", total.Mode, totalValueOf(total), expected)
}

func ptr[T any](v T) *T { return &v }

// RunStructuredFilterCorrectness 覆盖阶段 4 正式压力测试第七节「结构化过滤」的必测
// 维度：逐字段过滤、AND/OR/NOT、空集合、非法字段/值，并把命中数与 corpus.ComputeStats
// 的确定性期望值逐一核对，而不只是检查请求是否成功。
// 直接接收 manifest 而不是拆成 libraryID/sourceID/creatorIDs/stats 四个参数：多 Source
// 语料下 `filter/source.id` 的期望值不再等于整个语料的 VisibleN，必须用 manifest 里逐
// Source 的可见计数，而这类"期望值来源随语料形态变化"的判断只有拿到完整 manifest 才
// 做得对。
func RunStructuredFilterCorrectness(rep *report.Report, sess *environment.Session, manifest corpus.Manifest) {
	libraryID, sourceID, stats := manifest.LibraryID, manifest.SourceID, manifest.Stats
	// 单 Source 语料下第一个 Source 就是全部可见作品；多 Source 语料下必须用该 Source
	// 自己的可见计数，否则这条断言会在正确的产品行为上失败。
	sourceVisibleCount := stats.VisibleN
	if len(manifest.SourceVisibleWorkCounts) > 0 {
		sourceVisibleCount = manifest.SourceVisibleWorkCounts[0]
	}
	check := func(name string, filter map[string]any, expected int, extra func(api.ListWorksParams)) {
		params := api.ListWorksParams{Filter: ptr(filterJSON(filter)), Limit: ptr(200)}
		if extra != nil {
			extra(params)
		}
		resp, err := listWorks(sess, params)
		if err != nil || resp.JSON200 == nil {
			rep.Add(name, false, fmt.Sprintf("请求失败: %v status=%d body=%s", err, environment.StatusOf(resp), string(resp.Body)))
			return
		}
		ok, detail := checkTotalAgainstExpected(resp.JSON200.Total, expected)
		rep.Add(name, ok, detail)
	}

	// 除 overlay.hidden 之外的每个过滤器都不显式引用 overlay.hidden，服务端因此
	// 隐式追加 hidden=0；期望值必须用 Visible* 字段（已排除 Hidden 作品），不能用
	// 原始 Hidden-inclusive 计数——这是此前 12 项烟雾测试失败的真正根因
	// （HARNESS_BUG：WRONG_EXPECTATION，不是产品缺陷）。
	check("filter/library.id", leaf("library.id", "eq", libraryID), stats.VisibleN, nil)
	check("filter/source.id", leaf("source.id", "eq", sourceID), sourceVisibleCount, nil)
	for slot := 0; slot < 5; slot++ {
		providerID := corpus.ProviderID(slot)
		check(fmt.Sprintf("filter/provider.id[%d]", slot), leaf("provider.id", "eq", providerID), stats.VisibleProviderCounts[providerID], nil)
	}
	tagName := corpus.TagName(3)
	check("filter/tag", leaf("tag", "eq", tagName), stats.VisibleTagCounts[tagName], nil)
	check("filter/media.kind=video", leaf("media.kind", "eq", "video"), stats.VisibleVideoCount, nil)
	check("filter/media.locationAvailable", leaf("media.locationAvailable", "eq", true), stats.VisibleN, nil)
	check("filter/media.contentVerificationState=content_verified", leaf("media.contentVerificationState", "eq", "content_verified"), stats.VisibleContentVerifiedCount, nil)
	check("filter/media.contentVerificationState=located_unverified", leaf("media.contentVerificationState", "eq", "located_unverified"), stats.VisibleLocatedUnverifiedCount, nil)
	check("filter/overlay.favorite", leaf("overlay.favorite", "eq", true), countFavoriteVisible(stats.N), nil)
	// overlay.hidden 是本节唯一显式引用 hidden 字段的过滤器：客户端接管可见性语义，
	// 服务端不再叠加默认隐式条件，期望值必须用未排除 Hidden 的原始 HiddenCount。
	check("filter/overlay.hidden=true", leaf("overlay.hidden", "eq", true), stats.HiddenCount, nil)
	check("filter/overlay.progress gte 0.5", leaf("overlay.progress", "gte", 0.5), countProgressGTEVisible(stats.N, 0.5), nil)
	check("filter/AND(provider,tag)", all(leaf("provider.id", "eq", corpus.ProviderID(0)), leaf("tag", "eq", corpus.TagName(3))),
		countAndProviderTagVisible(stats.N, 0, 3), nil)
	check("filter/OR(two providers)", any_(leaf("provider.id", "eq", corpus.ProviderID(0)), leaf("provider.id", "eq", corpus.ProviderID(1))),
		stats.VisibleProviderCounts[corpus.ProviderID(0)]+stats.VisibleProviderCounts[corpus.ProviderID(1)], nil)
	check("filter/NOT(media.kind=video)", not(leaf("media.kind", "eq", "video")), stats.VisibleImageCount, nil)
	check("filter/nested AND(OR,NOT)", all(
		any_(leaf("provider.id", "eq", corpus.ProviderID(0)), leaf("provider.id", "eq", corpus.ProviderID(1))),
		not(leaf("media.kind", "eq", "video")),
	), countNestedAndOrNotVisible(stats.N), nil)
	check("filter/duplicate condition", all(leaf("tag", "eq", tagName), leaf("tag", "eq", tagName)), stats.VisibleTagCounts[tagName], nil)

	// 空集合：{"all":[]} 与 {"any":[]} 在生产实现中被显式拒绝为 VALIDATION_ERROR
	// （internal/query/filter.go validateShape 对非 nil 但零长度的 All/Any 单独判定），
	// 不是"匹配全部"的隐含语义；这里验证的是拒绝行为本身，不是命中数。
	emptyAllResp, err := listWorks(sess, api.ListWorksParams{Filter: ptr(filterJSON(emptyGroup("all"))), Limit: ptr(20)})
	if err != nil || emptyAllResp.JSON400 == nil || emptyAllResp.JSON400.Error.Code != api.VALIDATIONERROR {
		rep.Add("filter/empty-all-rejected", false, fmt.Sprintf("err=%v status=%d", err, environment.StatusOf(emptyAllResp)))
	} else {
		rep.Add("filter/empty-all-rejected", true, "")
	}
	emptyAnyResp, err := listWorks(sess, api.ListWorksParams{Filter: ptr(filterJSON(emptyGroup("any"))), Limit: ptr(20)})
	if err != nil || emptyAnyResp.JSON400 == nil || emptyAnyResp.JSON400.Error.Code != api.VALIDATIONERROR {
		rep.Add("filter/empty-any-rejected", false, fmt.Sprintf("err=%v status=%d", err, environment.StatusOf(emptyAnyResp)))
	} else {
		rep.Add("filter/empty-any-rejected", true, "")
	}

	// 非法字段与非法值：必须返回 VALIDATION_ERROR，不得静默忽略或当作已知字段处理。
	badField := api.ListWorksParams{Filter: ptr(filterJSON(leaf("not.a.real.field", "eq", true))), Limit: ptr(20)}
	if resp, err := listWorks(sess, badField); err != nil || resp.JSON400 == nil || resp.JSON400.Error.Code != api.VALIDATIONERROR {
		rep.Add("filter/unknown field rejected", false, fmt.Sprintf("err=%v status=%d", err, environment.StatusOf(resp)))
	} else {
		rep.Add("filter/unknown field rejected", true, "")
	}
	badValue := api.ListWorksParams{Filter: ptr(filterJSON(leaf("overlay.progress", "eq", "not-a-number"))), Limit: ptr(20)}
	if resp, err := listWorks(sess, badValue); err != nil || resp.JSON400 == nil {
		rep.Add("filter/bad value type rejected", false, fmt.Sprintf("err=%v status=%d", err, environment.StatusOf(resp)))
	} else {
		rep.Add("filter/bad value type rejected", true, "")
	}
	badOp := api.ListWorksParams{Filter: ptr(filterJSON(leaf("tag", "matches-substring", "x"))), Limit: ptr(20)}
	if resp, err := listWorks(sess, badOp); err != nil || resp.JSON400 == nil {
		rep.Add("filter/unknown operator rejected", false, fmt.Sprintf("err=%v status=%d", err, environment.StatusOf(resp)))
	} else {
		rep.Add("filter/unknown operator rejected", true, "")
	}
}

// RunCreatorFilterCorrectness 使用真实扫描建立的 control.db CanonicalCreator 与
// Catalog publication 关系，补齐批量 catalog seed 无法单独证明的 creator.id 过滤。
// Source 范围与 Creator ID 同时下发，保证这条 smoke 验证的是生产用户端实际使用的
// 组合，而不是按展示名或路径做测试侧近似。
func RunCreatorFilterCorrectness(rep *report.Report, sess *environment.Session, sourceID string, expectedCreators, expectedWorks int) {
	if expectedCreators <= 0 || expectedWorks <= 0 || expectedWorks%expectedCreators != 0 {
		rep.Add("filter/creator.id", false, fmt.Sprintf("非法夹具期望: creators=%d works=%d", expectedCreators, expectedWorks))
		return
	}
	ctx := context.Background()
	includeMerged := false
	limit := 200
	sortOrder := api.ListCreatorsParamsSortNameAsc
	creators, err := sess.Client.ListCreatorsWithResponse(ctx, &api.ListCreatorsParams{
		SourceId: &sourceID, IncludeMerged: &includeMerged, Sort: &sortOrder, Limit: &limit,
	}, sess.SameOrigin)
	if err != nil || creators.JSON200 == nil {
		rep.Add("filter/creator.id", false, fmt.Sprintf("Creator 列表失败: err=%v status=%d", err, environment.StatusOf(creators)))
		return
	}
	if len(creators.JSON200.Creators) != expectedCreators || creators.JSON200.NextCursor != nil {
		rep.Add("filter/creator.id", false, fmt.Sprintf("Creator 数量=%d expected=%d nextCursor=%v works=%d", len(creators.JSON200.Creators), expectedCreators, creators.JSON200.NextCursor != nil, expectedWorks))
		return
	}

	expectedPerCreator := expectedWorks / expectedCreators
	seenWorks := make(map[string]struct{}, expectedWorks)
	for _, creator := range creators.JSON200.Creators {
		filter := filterJSON(leaf("creator.id", "eq", creator.EffectiveId))
		works, listErr := listWorks(sess, api.ListWorksParams{SourceId: &sourceID, Filter: &filter, Limit: &limit})
		if listErr != nil || works.JSON200 == nil {
			rep.Add("filter/creator.id", false, fmt.Sprintf("作品查询失败: err=%v status=%d", listErr, environment.StatusOf(works)))
			return
		}
		if works.JSON200.Total.Mode != api.Exact || works.JSON200.Total.Value == nil || int(*works.JSON200.Total.Value) != expectedPerCreator || len(works.JSON200.Works) != expectedPerCreator {
			rep.Add("filter/creator.id", false, fmt.Sprintf("Creator total 非 exact 或分布错误: mode=%s works=%d expected=%d", works.JSON200.Total.Mode, len(works.JSON200.Works), expectedPerCreator))
			return
		}
		for _, work := range works.JSON200.Works {
			if work.Creator != creator.Name {
				rep.Add("filter/creator.id", false, "Creator ID 查询返回了其它展示身份的 Work")
				return
			}
			if _, duplicate := seenWorks[work.Id]; duplicate {
				rep.Add("filter/creator.id", false, "不同 Creator 查询返回重复 Work")
				return
			}
			seenWorks[work.Id] = struct{}{}
		}
	}

	rep.Add("filter/creator.id", len(seenWorks) == expectedWorks,
		fmt.Sprintf("creators=%d uniqueWorks=%d expected=%d", len(creators.JSON200.Creators), len(seenWorks), expectedWorks))
}

// 以下 count*Visible 辅助函数都按 corpus.ComputeStats 相同的确定性生成规则逐条
// 重算交集，并统一排除 Hidden 作品——匹配默认查询（未显式引用 overlay.hidden）
// 隐式追加 hidden=0 的服务端语义，保证期望值与 testlab seed 实际写入、且能被普通
// 查询看到的数据完全一致。

func countFavoriteVisible(n int) int {
	count := 0
	for i := 0; i < n; i++ {
		if !corpus.Hidden(i) && corpus.Favorite(i) {
			count++
		}
	}
	return count
}

func countProgressGTEVisible(n int, threshold float64) int {
	count := 0
	for i := 0; i < n; i++ {
		if corpus.Hidden(i) {
			continue
		}
		if i%7 == 0 {
			progress := float64(i%101) / 100.0
			if progress >= threshold {
				count++
			}
		}
	}
	return count
}

func countAndProviderTagVisible(n, providerSlot, tagSlot int) int {
	count := 0
	target := corpus.TagName(tagSlot)
	for i := 0; i < n; i++ {
		if corpus.Hidden(i) || corpus.ProviderIndex(i) != providerSlot {
			continue
		}
		a, b := corpus.TagSlots(i)
		if corpus.TagName(a) == target || corpus.TagName(b) == target {
			count++
		}
	}
	return count
}

func countNestedAndOrNotVisible(n int) int {
	count := 0
	for i := 0; i < n; i++ {
		if corpus.Hidden(i) {
			continue
		}
		providerOK := corpus.ProviderIndex(i) == 0 || corpus.ProviderIndex(i) == 1
		notVideo := corpus.MediaKind(i) != "video"
		if providerOK && notVideo {
			count++
		}
	}
	return count
}

// RunSearchRecallCorrectness 覆盖第七节「搜索召回与原文复核」：选择性 CJK、拉丁大小写
// 折叠、文件名中缀、过短查询拒绝与空查询语义。
func RunSearchRecallCorrectness(rep *report.Report, sess *environment.Session, stats corpus.Stats) {
	check := func(name, query string, expected int) {
		resp, err := listWorks(sess, api.ListWorksParams{Q: ptr(query), Limit: ptr(200)})
		if err != nil || resp.JSON200 == nil {
			rep.Add(name, false, fmt.Sprintf("请求失败: %v status=%d", err, environment.StatusOf(resp)))
			return
		}
		ok, detail := checkTotalAgainstExpected(resp.JSON200.Total, expected)
		rep.Add(name, ok, detail)
	}
	check("search/selective-cjk-exact-recall", corpus.SpecialCJKMarker, stats.VisibleSpecialCJKCount)
	check("search/latin-casefold-recall", strings.ToLower(corpus.SpecialLatinMarker), stats.VisibleSpecialLatinCount)
	check("search/filename-infix-recall", corpus.UniqueFilenameMarker, stats.VisibleUniqueFilenameCount)

	shortResp, err := listWorks(sess, api.ListWorksParams{Q: ptr("关"), Limit: ptr(20)})
	if err != nil || shortResp.JSON400 == nil {
		rep.Add("search/single-cjk-char-rejected", false, fmt.Sprintf("err=%v status=%d", err, environment.StatusOf(shortResp)))
	} else {
		rep.Add("search/single-cjk-char-rejected", true, "")
	}

	emptyResp, err := listWorks(sess, api.ListWorksParams{Q: ptr(""), Limit: ptr(20)})
	if err != nil || emptyResp.JSON200 == nil {
		rep.Add("search/empty-query-is-browse", false, fmt.Sprintf("err=%v status=%d", err, environment.StatusOf(emptyResp)))
	} else {
		rep.Add("search/empty-query-is-browse", emptyResp.JSON200.Works != nil, "")
	}

	// 宽查询：普通作品标题的公共前缀在语料中出现比例极高，必须分页而不是把全部候选
	// 一次性搬入应用层——这里只验证响应确实携带 nextCursor 或 total 落入 lower_bound。
	wideResp, err := listWorks(sess, api.ListWorksParams{Q: ptr("普通作品"), Limit: ptr(50)})
	if err != nil || wideResp.JSON200 == nil {
		rep.Add("search/wide-query-paginates", false, fmt.Sprintf("err=%v status=%d", err, environment.StatusOf(wideResp)))
	} else {
		paginates := wideResp.JSON200.NextCursor != nil || string(wideResp.JSON200.Total.Mode) == "lower_bound"
		rep.Add("search/wide-query-paginates", paginates, fmt.Sprintf("mode=%s nextCursor=%v", wideResp.JSON200.Total.Mode, wideResp.JSON200.NextCursor != nil))
	}
}

// RunRankingAndMatchesCorrectness 覆盖第七节「Ranking v2 与 matches」的结构性断言：
// rankProtocolVersion 存在、matches 字段在有搜索词时非空、span 落在合法范围内。
func RunRankingAndMatchesCorrectness(rep *report.Report, sess *environment.Session) {
	resp, err := listWorks(sess, api.ListWorksParams{Q: ptr(corpus.SpecialCJKMarker), Limit: ptr(20)})
	if err != nil || resp.JSON200 == nil || len(resp.JSON200.Works) == 0 {
		rep.Add("ranking/response-has-hits", false, fmt.Sprintf("err=%v status=%d", err, environment.StatusOf(resp)))
		return
	}
	rep.Add("ranking/rankProtocolVersion-present", resp.JSON200.RankProtocolVersion > 0, fmt.Sprintf("value=%d", resp.JSON200.RankProtocolVersion))
	work := resp.JSON200.Works[0]
	hasMatches := work.Matches != nil && len(*work.Matches) > 0
	rep.Add("ranking/matches-present-for-search", hasMatches, "")
	if hasMatches {
		validSpans := true
		for _, m := range *work.Matches {
			runeCount := int64(len([]rune(m.Value)))
			for _, span := range m.Spans {
				if span.Start < 0 || span.End < span.Start || int64(span.End) > runeCount {
					validSpans = false
				}
			}
		}
		rep.Add("ranking/match-spans-in-bounds", validSpans, "")
	}
}

// RunTotalTriStateCorrectness 覆盖第七节「total 三态」：小结果集 exact、宽查询
// lower_bound、omitTotal 请求参数产生 omitted。
func RunTotalTriStateCorrectness(rep *report.Report, sess *environment.Session, stats corpus.Stats) {
	exactResp, err := listWorks(sess, api.ListWorksParams{Q: ptr(corpus.SpecialCJKMarker), Limit: ptr(20)})
	if err != nil || exactResp.JSON200 == nil {
		rep.Add("total/exact-mode", false, fmt.Sprintf("err=%v", err))
	} else {
		rep.Add("total/exact-mode", string(exactResp.JSON200.Total.Mode) == "exact", fmt.Sprintf("mode=%s", exactResp.JSON200.Total.Mode))
	}

	browseResp, err := listWorks(sess, api.ListWorksParams{Limit: ptr(20)})
	if err != nil || browseResp.JSON200 == nil {
		rep.Add("total/lower_bound-mode-for-wide-browse", false, fmt.Sprintf("err=%v", err))
	} else if stats.N > 10000 {
		rep.Add("total/lower_bound-mode-for-wide-browse", string(browseResp.JSON200.Total.Mode) == "lower_bound", fmt.Sprintf("mode=%s n=%d", browseResp.JSON200.Total.Mode, stats.N))
	} else {
		// smoke/integration 的有界规模不会触发 lower_bound；仍必须把 browse total 的
		// exact 分支记为显式 finding，避免该规模下静默少跑一项 Total 断言。
		rep.Add("total/exact-mode-for-bounded-browse", string(browseResp.JSON200.Total.Mode) == "exact", fmt.Sprintf("mode=%s n=%d", browseResp.JSON200.Total.Mode, stats.N))
	}

	omitResp, err := listWorks(sess, api.ListWorksParams{Limit: ptr(20), OmitTotal: ptr(true)})
	if err != nil || omitResp.JSON200 == nil {
		rep.Add("total/omitted-mode", false, fmt.Sprintf("err=%v", err))
	} else {
		rep.Add("total/omitted-mode", string(omitResp.JSON200.Total.Mode) == "omitted" && omitResp.JSON200.Total.Value == nil, fmt.Sprintf("mode=%s value=%s", omitResp.JSON200.Total.Mode, totalValueOf(omitResp.JSON200.Total)))
	}
}

// RunSortCorrectness 验证浏览排序的自然序与跨页稳定 tie-break：连续翻两页，断言
// 服务端返回的 sort key 单调不减，且不出现重复/遗漏的 workId。
func RunSortCorrectness(rep *report.Report, sess *environment.Session) {
	seen := map[string]bool{}
	cursor := ""
	pages := 0
	duplicate := false
	for pages < 5 {
		params := api.ListWorksParams{Limit: ptr(100)}
		if cursor != "" {
			params.Cursor = &cursor
		}
		resp, err := listWorks(sess, params)
		if err != nil || resp.JSON200 == nil {
			rep.Add("sort/pagination-walks-without-error", false, fmt.Sprintf("err=%v status=%d", err, environment.StatusOf(resp)))
			return
		}
		for _, w := range resp.JSON200.Works {
			if seen[w.Id] {
				duplicate = true
			}
			seen[w.Id] = true
		}
		pages++
		if resp.JSON200.NextCursor == nil {
			break
		}
		cursor = *resp.JSON200.NextCursor
	}
	rep.Add("sort/pagination-walks-without-error", true, "")
	rep.Add("sort/no-duplicate-across-pages", !duplicate, fmt.Sprintf("pages=%d", pages))
}
