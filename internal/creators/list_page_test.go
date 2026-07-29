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
