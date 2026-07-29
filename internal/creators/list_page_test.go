package creators_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/creators"
)

func TestCreatorListPageKeysetScopeAndMergedRoots(t *testing.T) {
	f := setupTwoSources(t)
	f.scan(t, f.source1.ID)
	f.scan(t, f.source2.ID)
	alpha := f.creatorByName(t, "作者甲")
	beta := f.creatorByName(t, "作者乙")
	allSources := []string{f.source1.ID, f.source2.ID}

	allowedJSON, err := json.Marshal(allSources)
	if err != nil {
		t.Fatal(err)
	}
	planRows, err := f.store.Control.SQL().QueryContext(f.ctx,
		"EXPLAIN QUERY PLAN "+creators.CreatorPageStatementForTest(false, false, "name_asc", false),
		string(allowedJSON), 2)
	if err != nil {
		t.Fatal(err)
	}
	var plan []string
	for planRows.Next() {
		var id, parent, unused int
		var detail string
		if err := planRows.Scan(&id, &parent, &unused, &detail); err != nil {
			planRows.Close()
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := planRows.Close(); err != nil {
		t.Fatal(err)
	}
	planText := strings.Join(plan, "\n")
	if !strings.Contains(planText, "canonical_creators_live_sort_idx") ||
		!strings.Contains(planText, "creator_bindings_creator_idx") {
		t.Fatalf("无合并分页未沿排序/Binding 窄索引执行:\n%s", planText)
	}
	mergedPlanRows, err := f.store.Control.SQL().QueryContext(f.ctx,
		"EXPLAIN QUERY PLAN "+creators.CreatorPageStatementForTest(false, true, "name_asc", false),
		string(allowedJSON), 2)
	if err != nil {
		t.Fatal(err)
	}
	plan = plan[:0]
	for mergedPlanRows.Next() {
		var id, parent, unused int
		var detail string
		if err := mergedPlanRows.Scan(&id, &parent, &unused, &detail); err != nil {
			mergedPlanRows.Close()
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := mergedPlanRows.Close(); err != nil {
		t.Fatal(err)
	}
	planText = strings.Join(plan, "\n")
	if !strings.Contains(planText, "canonical_creators_live_sort_idx") ||
		!strings.Contains(planText, "canonical_creators_merged_idx") ||
		!strings.Contains(planText, "creator_bindings_creator_idx") {
		t.Fatalf("合并图分页未沿排序/合并/Binding 窄索引执行:\n%s", planText)
	}

	first, err := f.creators.ListPage(f.ctx, creators.ListPageRequest{
		AllowedSourceIDs: allSources, Sort: "name_asc", Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Items[0].ID != beta.ID || first.NextCursor == "" {
		t.Fatalf("升序第一页错误: %+v", first)
	}
	second, err := f.creators.ListPage(f.ctx, creators.ListPageRequest{
		AllowedSourceIDs: allSources, Sort: "name_asc", Limit: 1, Cursor: first.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].ID != alpha.ID || second.NextCursor != "" {
		t.Fatalf("升序第二页错误: %+v", second)
	}
	descending, err := f.creators.ListPage(f.ctx, creators.ListPageRequest{
		AllowedSourceIDs: allSources, Sort: "name_desc", Limit: 1,
	})
	if err != nil || len(descending.Items) != 1 || descending.Items[0].ID != alpha.ID {
		t.Fatalf("降序第一页错误: page=%+v err=%v", descending, err)
	}

	// 游标绑定授权 Source 集合和排序/页大小。权限或范围变化后必须从第一页重取，不能把
	// 原锚点静默套到另一组结果；破坏编码的输入则是不可重试的 CURSOR_INVALID。
	_, err = f.creators.ListPage(f.ctx, creators.ListPageRequest{
		AllowedSourceIDs: []string{f.source1.ID}, Sort: "name_asc", Limit: 1, Cursor: first.NextCursor,
	})
	if !hasCode(err, fault.CodeCursorExpired) {
		t.Fatalf("授权范围变化未让游标过期: %v", err)
	}
	tampered := first.NextCursor[:len(first.NextCursor)-1] + "!"
	_, err = f.creators.ListPage(f.ctx, creators.ListPageRequest{
		AllowedSourceIDs: allSources, Sort: "name_asc", Limit: 1, Cursor: tampered,
	})
	if !hasCode(err, fault.CodeCursorInvalid) {
		t.Fatalf("破坏游标未按 CURSOR_INVALID 拒绝: %v", err)
	}

	f.mergeAndWait(t, alpha.ID, beta.ID)
	merged, err := f.creators.ListPage(f.ctx, creators.ListPageRequest{
		AllowedSourceIDs: []string{f.source2.ID}, SourceID: f.source2.ID,
		IncludeMerged: false, Sort: "name_asc", Limit: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Items) != 1 || merged.Items[0].ID != alpha.ID || merged.Items[0].SourceCount != 1 {
		t.Fatalf("被并成员所在 Source 未汇总到有效根: %+v", merged.Items)
	}
	if got := strings.Join(merged.Items[0].MemberIDs, ","); got != strings.Join([]string{alpha.ID, beta.ID}, ",") && got != strings.Join([]string{beta.ID, alpha.ID}, ",") {
		t.Fatalf("有效根成员集合错误: %v", merged.Items[0].MemberIDs)
	}
	if denied, err := f.creators.ListPage(f.ctx, creators.ListPageRequest{
		AllowedSourceIDs: []string{f.source1.ID}, SourceID: f.source2.ID,
		IncludeMerged: false, Sort: "name_asc", Limit: 50,
	}); err != nil || len(denied.Items) != 0 {
		t.Fatalf("未授权 Source 未收敛为空页: page=%+v err=%v", denied, err)
	}
}

func TestCreatorGovernancePageIsBoundedAndPreservesMergedIdentityEvidence(t *testing.T) {
	f := setupTwoSources(t)
	f.scan(t, f.source1.ID)
	f.scan(t, f.source2.ID)
	alpha := f.creatorByName(t, "作者甲")
	beta := f.creatorByName(t, "作者乙")

	allowedJSON, err := json.Marshal([]string{f.source1.ID, f.source2.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, planCase := range []struct {
		name      string
		statement string
		args      []any
	}{
		{"global", creators.GovernancePageStatementForTest(true, false), []any{2}},
		{"restricted", creators.GovernancePageStatementForTest(false, false), []any{string(allowedJSON), 2}},
	} {
		rows, planErr := f.store.Control.SQL().QueryContext(f.ctx, "EXPLAIN QUERY PLAN "+planCase.statement, planCase.args...)
		if planErr != nil {
			t.Fatalf("%s 治理计划: %v", planCase.name, planErr)
		}
		var details []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if scanErr := rows.Scan(&id, &parent, &unused, &detail); scanErr != nil {
				rows.Close()
				t.Fatal(scanErr)
			}
			details = append(details, detail)
		}
		if closeErr := rows.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		planText := strings.Join(details, "\n")
		if !strings.Contains(planText, "canonical_creators_sort_idx") ||
			!strings.Contains(planText, "creator_bindings_creator_idx") {
			t.Fatalf("%s 治理页未沿排序/Binding 窄索引执行:\n%s", planCase.name, planText)
		}
	}

	first, err := f.creators.ListGovernancePage(f.ctx, creators.GovernancePageRequest{
		Global: true, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Items[0].ID != beta.ID || first.NextCursor == "" {
		t.Fatalf("治理第一页未按有界 keyset 返回: %+v", first)
	}
	second, err := f.creators.ListGovernancePage(f.ctx, creators.GovernancePageRequest{
		Global: true, Limit: 1, Cursor: first.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].ID != alpha.ID || second.NextCursor != "" {
		t.Fatalf("治理第二页错误: %+v", second)
	}

	// cursor 绑定权限形态和获授权 Source 集合；不能把 global 页锚点套到受限主体。
	_, err = f.creators.ListGovernancePage(f.ctx, creators.GovernancePageRequest{
		AllowedSourceIDs: []string{f.source1.ID}, Limit: 1, Cursor: first.NextCursor,
	})
	if !hasCode(err, fault.CodeCursorExpired) {
		t.Fatalf("治理游标未绑定授权范围: %v", err)
	}

	f.mergeAndWait(t, alpha.ID, beta.ID)
	global, err := f.creators.ListGovernancePage(f.ctx, creators.GovernancePageRequest{
		Global: true, Limit: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(global.Items) != 2 {
		t.Fatalf("治理页必须保留根和已合并身份: %+v", global.Items)
	}
	byID := make(map[string]creators.Creator, len(global.Items))
	for _, item := range global.Items {
		byID[item.ID] = item
	}
	if byID[alpha.ID].EffectiveID != alpha.ID || byID[beta.ID].EffectiveID != alpha.ID {
		t.Fatalf("治理页 effectiveId 未解析到当前合并根: %+v", byID)
	}

	// 资源受限主体仍按每个 base 身份自己的全部状态 Binding 判断可见性，不把用户浏览
	// 页的等价组折叠语义误用于治理证据；sourceCount 只统计获授权 active Source。
	restricted, err := f.creators.ListGovernancePage(f.ctx, creators.GovernancePageRequest{
		AllowedSourceIDs: []string{f.source2.ID}, Limit: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(restricted.Items) != 1 || restricted.Items[0].ID != beta.ID ||
		restricted.Items[0].EffectiveID != alpha.ID || restricted.Items[0].SourceCount != 1 {
		t.Fatalf("受限治理页泄露或丢失身份证据: %+v", restricted.Items)
	}
}
