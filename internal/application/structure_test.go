package application_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
)

// blob 构造一条带 ContentBlob digest 证据的 DiscoveredMedia。digest 用短占位串即可，检测只比较相等性。
func blob(sourceKey, digest string, ordinal int) application.DiscoveredMedia {
	return application.DiscoveredMedia{SourceKey: sourceKey, RuleKey: mediaRuleKey(sourceKey),
		Algorithm: "sha-256", Digest: digest, Ordinal: ordinal}
}

func mediaRuleKey(sourceKey string) string {
	for i := len(sourceKey) - 1; i >= 0; i-- {
		if sourceKey[i] == '/' {
			return sourceKey[i+1:]
		}
	}
	return sourceKey
}

func work(sourceKey, title string, media ...application.DiscoveredMedia) application.DiscoveredWork {
	return application.DiscoveredWork{SourceKey: sourceKey, Title: title, Media: media}
}

func asStructured(t *testing.T, err error) *fault.Error {
	t.Helper()
	var structured *fault.Error
	if !errors.As(err, &structured) {
		t.Fatalf("期望结构化错误，得到: %v", err)
	}
	return structured
}

func TestSourceWorkSplitDetectionBlocksAndDedups(t *testing.T) {
	f := newIssueFixture(t)
	// 扫描 1：建立原 SourceWork wkA（三个媒体）。
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, []application.DiscoveredWork{
		work("wkA", "作品甲", blob("wkA/m1", "d1", 0), blob("wkA/m2", "d2", 1), blob("wkA/m3", "d3", 2)),
	}); err != nil {
		t.Fatalf("扫描 1 应成功: %v", err)
	}
	var originID string
	if err := f.control.QueryRowContext(f.ctx, `SELECT work_id FROM work_bindings
WHERE source_id=? AND source_key='wkA'`, f.source.ID).Scan(&originID); err != nil {
		t.Fatal(err)
	}

	// 扫描 2：wkA 消失，其媒体分散到 wkA1(m1,m2) 与 wkA2(m3)。
	split := []application.DiscoveredWork{
		work("wkA1", "作品甲一", blob("wkA1/m1", "d1", 0), blob("wkA1/m2", "d2", 1)),
		work("wkA2", "作品甲二", blob("wkA2/m3", "d3", 0)),
	}
	_, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, split)
	if code := asStructured(t, err).Code; code != fault.CodeBindingReviewRequired {
		t.Fatalf("拆分未阻塞: %v", code)
	}
	open := f.openIssues(t)
	if len(open) != 1 || open[0].Code != "SOURCE_WORK_SPLIT_REVIEW_REQUIRED" {
		t.Fatalf("未产生唯一拆分审查 issue: %+v", open)
	}
	issue, err := f.resources.GetBindingIssue(f.ctx, open[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if issue.SourceKey != "wkA" {
		t.Fatalf("拆分 issue 代表键应为原 source_key: %q", issue.SourceKey)
	}
	var origins, news int
	for _, candidate := range issue.Candidates {
		switch candidate.MatchSignal {
		case "origin_canonical":
			origins++
			if candidate.CandidateID != originID || candidate.Label != "作品甲" {
				t.Fatalf("origin 候选错误: %+v", candidate)
			}
		case "new_source_work":
			news++
		}
	}
	if origins != 1 || news != 2 {
		t.Fatalf("候选构成错误 origins=%d news=%d", origins, news)
	}

	// 相同证据重扫：复用同一 issue，不重复产生。
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, split); err == nil {
		t.Fatal("重扫仍应阻塞")
	}
	if got := f.openIssues(t); len(got) != 1 || got[0].ID != issue.ID {
		t.Fatalf("相同证据重扫产生重复 issue: %+v", got)
	}
}

func TestSourceWorkMergeDetectionBlocks(t *testing.T) {
	f := newIssueFixture(t)
	// 扫描 1：两个独立 SourceWork。
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, []application.DiscoveredWork{
		work("wkA", "作品甲", blob("wkA/m1", "d1", 0)),
		work("wkB", "作品乙", blob("wkB/m1", "d2", 0)),
	}); err != nil {
		t.Fatalf("扫描 1 应成功: %v", err)
	}

	// 扫描 2：wkA、wkB 消失，其媒体汇聚到新 wkC。
	merge := []application.DiscoveredWork{
		work("wkC", "合并作品", blob("wkC/m1", "d1", 0), blob("wkC/m2", "d2", 1)),
	}
	_, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, merge)
	if code := asStructured(t, err).Code; code != fault.CodeBindingReviewRequired {
		t.Fatalf("合并未阻塞: %v", code)
	}
	open := f.openIssues(t)
	if len(open) != 1 || open[0].Code != "SOURCE_WORK_MERGE_REVIEW_REQUIRED" {
		t.Fatalf("未产生唯一合并审查 issue: %+v", open)
	}
	issue, err := f.resources.GetBindingIssue(f.ctx, open[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if issue.SourceKey != "wkC" {
		t.Fatalf("合并 issue 代表键应为新 source_key: %q", issue.SourceKey)
	}
	var origins, news int
	for _, candidate := range issue.Candidates {
		switch candidate.MatchSignal {
		case "origin_canonical":
			origins++
		case "new_source_work":
			news++
			if candidate.CandidateID != "wkC" {
				t.Fatalf("new 候选错误: %+v", candidate)
			}
		}
	}
	if origins != 2 || news != 1 {
		t.Fatalf("候选构成错误 origins=%d news=%d", origins, news)
	}
}

// TestUnrelatedDisappearanceNotFlagged 确认单纯删除（媒体未在新 SourceWork 出现）不误报结构变化。
func TestUnrelatedDisappearanceNotFlagged(t *testing.T) {
	f := newIssueFixture(t)
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, []application.DiscoveredWork{
		work("wkA", "作品甲", blob("wkA/m1", "d1", 0)),
		work("wkB", "作品乙", blob("wkB/m1", "d2", 0)),
	}); err != nil {
		t.Fatalf("扫描 1 应成功: %v", err)
	}
	// wkB 整体删除，wkA 保留；新 wkC 的媒体与 wkA/wkB 均无 digest 交集。
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, []application.DiscoveredWork{
		work("wkA", "作品甲", blob("wkA/m1", "d1", 0)),
		work("wkC", "全新作品", blob("wkC/m1", "d9", 0)),
	}); err != nil {
		t.Fatalf("普通删除+新增不应阻塞: %v", err)
	}
	if got := f.openIssues(t); len(got) != 0 {
		t.Fatalf("普通删除误报结构 issue: %+v", got)
	}
}

// activeWorkID 返回某 source_key 当前 active WorkBinding 指向的 CanonicalWork ID（无则空）。
func (f *issueFixture) activeWorkID(t *testing.T, sourceKey string) string {
	t.Helper()
	var workID string
	err := f.control.QueryRowContext(f.ctx, `SELECT work_id FROM work_bindings
WHERE source_id=? AND source_key=? AND status='active'`, f.source.ID, sourceKey).Scan(&workID)
	if err != nil {
		return ""
	}
	return workID
}

func (f *issueFixture) bindingStatus(t *testing.T, sourceKey string) string {
	t.Helper()
	var status string
	err := f.control.QueryRowContext(f.ctx, `SELECT status FROM work_bindings
WHERE source_id=? AND source_key=? ORDER BY CASE status WHEN 'active' THEN 0 ELSE 1 END LIMIT 1`,
		f.source.ID, sourceKey).Scan(&status)
	if err != nil {
		return ""
	}
	return status
}

// seedSplit 跑扫描 1 建立 wkA(m1,m2,m3)，扫描 2 触发拆分并返回 open issue 与原 CanonicalWork ID。
func (f *issueFixture) seedSplit(t *testing.T) (application.BindingIssue, string, []application.DiscoveredWork) {
	t.Helper()
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, []application.DiscoveredWork{
		work("wkA", "作品甲", blob("wkA/m1", "d1", 0), blob("wkA/m2", "d2", 1), blob("wkA/m3", "d3", 2)),
	}); err != nil {
		t.Fatalf("扫描 1: %v", err)
	}
	origin := f.activeWorkID(t, "wkA")
	split := []application.DiscoveredWork{
		work("wkA1", "作品甲一", blob("wkA1/m1", "d1", 0), blob("wkA1/m2", "d2", 1)),
		work("wkA2", "作品甲二", blob("wkA2/m3", "d3", 0)),
	}
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, split); err == nil {
		t.Fatal("拆分应阻塞")
	}
	open := f.openIssues(t)
	if len(open) != 1 {
		t.Fatalf("期望一个拆分 issue: %+v", open)
	}
	return open[0], origin, split
}

func TestSplitInheritAppliedByRescan(t *testing.T) {
	f := newIssueFixture(t)
	issue, origin, split := f.seedSplit(t)
	if _, err := f.resources.ResolveSourceStructureIssue(f.ctx, issue.ID, "owner", "split_inherit", "wkA1", "", issue.Version); err != nil {
		t.Fatalf("决策应成功: %v", err)
	}
	resolved, err := f.resources.GetBindingIssue(f.ctx, issue.ID)
	if err != nil || resolved.Status != "resolved" {
		t.Fatalf("issue 未标 resolved: %+v %v", resolved, err)
	}
	// 决策应用后重扫应成功。
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, split); err != nil {
		t.Fatalf("决策后重扫应成功: %v", err)
	}
	if got := f.activeWorkID(t, "wkA1"); got != origin {
		t.Fatalf("wkA1 未继承原 CanonicalWork: got=%s origin=%s", got, origin)
	}
	other := f.activeWorkID(t, "wkA2")
	if other == "" || other == origin {
		t.Fatalf("wkA2 应绑定新 CanonicalWork: got=%s", other)
	}
	// 继承作品应经 rule_key 复用原 CanonicalMedia（m1,m2 仍在原 work）。
	var kept int
	if err := f.control.QueryRowContext(f.ctx, `SELECT count(DISTINCT media_id) FROM media_bindings
WHERE source_id=? AND work_id=? AND status='active'`, f.source.ID, origin).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if kept != 2 {
		t.Fatalf("原 work 应保留 2 个媒体 occurrence: got=%d", kept)
	}
}

func TestSplitCreateNewAppliedByRescan(t *testing.T) {
	f := newIssueFixture(t)
	issue, origin, split := f.seedSplit(t)
	if _, err := f.resources.ResolveSourceStructureIssue(f.ctx, issue.ID, "owner", "split_create_new", "", "", issue.Version); err != nil {
		t.Fatalf("决策应成功: %v", err)
	}
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, split); err != nil {
		t.Fatalf("决策后重扫应成功: %v", err)
	}
	w1, w2 := f.activeWorkID(t, "wkA1"), f.activeWorkID(t, "wkA2")
	if w1 == "" || w2 == "" || w1 == origin || w2 == origin || w1 == w2 {
		t.Fatalf("拆分应各自创建新 CanonicalWork: w1=%s w2=%s origin=%s", w1, w2, origin)
	}
	// 原 CanonicalWork 仍保留（用户事实不删除），其 wkA 绑定退为非 active。
	var exists int
	if err := f.control.QueryRowContext(f.ctx, `SELECT count(*) FROM canonical_works WHERE work_id=?`, origin).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists != 1 {
		t.Fatal("原 CanonicalWork 不应被删除")
	}
	if status := f.bindingStatus(t, "wkA"); status == "active" {
		t.Fatalf("原 source_key 绑定不应仍为 active: %s", status)
	}
}

func (f *issueFixture) seedMerge(t *testing.T) (application.BindingIssue, string, string, []application.DiscoveredWork) {
	t.Helper()
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, []application.DiscoveredWork{
		work("wkA", "作品甲", blob("wkA/m1", "d1", 0)),
		work("wkB", "作品乙", blob("wkB/m1", "d2", 0)),
	}); err != nil {
		t.Fatalf("扫描 1: %v", err)
	}
	x, y := f.activeWorkID(t, "wkA"), f.activeWorkID(t, "wkB")
	merge := []application.DiscoveredWork{
		work("wkC", "合并作品", blob("wkC/m1", "d1", 0), blob("wkC/m2", "d2", 1)),
	}
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, merge); err == nil {
		t.Fatal("合并应阻塞")
	}
	open := f.openIssues(t)
	if len(open) != 1 {
		t.Fatalf("期望一个合并 issue: %+v", open)
	}
	return open[0], x, y, merge
}

func TestMergeBindExistingAppliedByRescan(t *testing.T) {
	f := newIssueFixture(t)
	issue, x, y, merge := f.seedMerge(t)
	if _, err := f.resources.ResolveSourceStructureIssue(f.ctx, issue.ID, "owner", "merge_bind_existing", "", x, issue.Version); err != nil {
		t.Fatalf("决策应成功: %v", err)
	}
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, merge); err != nil {
		t.Fatalf("决策后重扫应成功: %v", err)
	}
	if got := f.activeWorkID(t, "wkC"); got != x {
		t.Fatalf("wkC 未绑定选定 CanonicalWork: got=%s x=%s", got, x)
	}
	// 另一原 CanonicalWork Y 保留。
	var exists int
	if err := f.control.QueryRowContext(f.ctx, `SELECT count(*) FROM canonical_works WHERE work_id=?`, y).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists != 1 {
		t.Fatal("另一原 CanonicalWork 应保留")
	}
}

func TestMergeCreateNewAppliedByRescan(t *testing.T) {
	f := newIssueFixture(t)
	issue, x, y, merge := f.seedMerge(t)
	if _, err := f.resources.ResolveSourceStructureIssue(f.ctx, issue.ID, "owner", "merge_create_new", "", "", issue.Version); err != nil {
		t.Fatalf("决策应成功: %v", err)
	}
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, merge); err != nil {
		t.Fatalf("决策后重扫应成功: %v", err)
	}
	got := f.activeWorkID(t, "wkC")
	if got == "" || got == x || got == y {
		t.Fatalf("wkC 应绑定新 CanonicalWork: got=%s x=%s y=%s", got, x, y)
	}
}

func TestStructureResolveVersionConflictAndKindMismatch(t *testing.T) {
	f := newIssueFixture(t)
	issue, _, _ := f.seedSplit(t)
	if _, err := f.resources.ResolveSourceStructureIssue(f.ctx, issue.ID, "owner", "split_inherit", "wkA1", "", issue.Version+1); err == nil ||
		asStructured(t, err).Code != fault.CodeConflict {
		t.Fatalf("版本不匹配应冲突: %v", err)
	}
	// 对拆分 issue 使用合并动作应被拒绝。
	if _, err := f.resources.ResolveSourceStructureIssue(f.ctx, issue.ID, "owner", "merge_create_new", "", "", issue.Version); err == nil ||
		asStructured(t, err).Code != fault.CodeValidation {
		t.Fatalf("kind 不匹配应校验失败: %v", err)
	}
}

func TestStructureDecisionUndoBeforeRescan(t *testing.T) {
	f := newIssueFixture(t)
	issue, origin, split := f.seedSplit(t)
	decision, err := f.resources.ResolveSourceStructureIssue(f.ctx, issue.ID, "owner", "split_create_new", "", "", issue.Version)
	if err != nil {
		t.Fatalf("决策应成功: %v", err)
	}
	// 重扫前撤销：应清除 pre-seed Binding 并重新打开 issue。
	undone, err := f.resources.UndoSourceStructureDecision(f.ctx, decision.DecisionID, "owner", decision.Version)
	if err != nil || undone.Status != "undone" {
		t.Fatalf("撤销应成功: %+v %v", undone, err)
	}
	reopened, err := f.resources.GetBindingIssue(f.ctx, issue.ID)
	if err != nil || reopened.Status != "open" {
		t.Fatalf("撤销后 issue 应重新打开: %+v %v", reopened, err)
	}
	// pre-seed Binding 已清除：wkA1、wkA2 不再有任何 work binding。
	var seeded int
	if err := f.control.QueryRowContext(f.ctx, `SELECT count(*) FROM work_bindings
WHERE source_id=? AND source_key IN ('wkA1','wkA2')`, f.source.ID).Scan(&seeded); err != nil {
		t.Fatal(err)
	}
	if seeded != 0 {
		t.Fatalf("pre-seed Binding 未清除: %d", seeded)
	}
	// 撤销后重扫应再次阻塞（结构变化恢复为待审查）。
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, split); err == nil {
		t.Fatal("撤销后重扫应再次阻塞")
	}
	_ = origin
	// fingerprint 已释放，可重新决策为继承。
	current := f.openIssues(t)
	if len(current) != 1 {
		t.Fatalf("应有一个待审查 issue: %+v", current)
	}
	if _, err := f.resources.ResolveSourceStructureIssue(f.ctx, current[0].ID, "owner", "split_inherit", "wkA1", "", current[0].Version); err != nil {
		t.Fatalf("撤销后应可重新决策: %v", err)
	}
}

func TestStructureDecisionUndoConflictAfterRescan(t *testing.T) {
	f := newIssueFixture(t)
	issue, _, split := f.seedSplit(t)
	decision, err := f.resources.ResolveSourceStructureIssue(f.ctx, issue.ID, "owner", "split_inherit", "wkA1", "", issue.Version)
	if err != nil {
		t.Fatalf("决策应成功: %v", err)
	}
	// 重扫消费决策，产生新 active Binding。
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, split); err != nil {
		t.Fatalf("决策后重扫应成功: %v", err)
	}
	// 撤销应返回 CONFLICT。
	_, err = f.resources.UndoSourceStructureDecision(f.ctx, decision.DecisionID, "owner", decision.Version)
	if err == nil || asStructured(t, err).Code != fault.CodeConflict {
		t.Fatalf("已消费决策撤销应冲突: %v", err)
	}
}

// secondSource 在同一 Library 下创建第二个只读 Source，用于多 Source 隔离测试。
func (f *issueFixture) secondSource(t *testing.T) application.Source {
	t.Helper()
	root := filepath.Join(t.TempDir(), "source2")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	src, err := f.resources.CreateSource(f.ctx, f.libraryID, "source2", root)
	if err != nil {
		t.Fatal(err)
	}
	return src
}

func (f *issueFixture) activeWorkIDIn(t *testing.T, sourceID, sourceKey string) string {
	t.Helper()
	var workID string
	if err := f.control.QueryRowContext(f.ctx, `SELECT work_id FROM work_bindings
WHERE source_id=? AND source_key=? AND status='active'`, sourceID, sourceKey).Scan(&workID); err != nil {
		return ""
	}
	return workID
}

// TestSplitIsolatedAcrossSources 确认一个 Source 的拆分不影响另一个 Source 相同 source_key/blob 的 Binding。
func TestSplitIsolatedAcrossSources(t *testing.T) {
	f := newIssueFixture(t)
	src2 := f.secondSource(t)
	// 两个 Source 各有一个使用相同 source_key 与 digest 的作品。
	seed := []application.DiscoveredWork{work("wkA", "作品甲", blob("wkA/m1", "d1", 0), blob("wkA/m2", "d2", 1), blob("wkA/m3", "d3", 2))}
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, seed); err != nil {
		t.Fatalf("source1 scan1: %v", err)
	}
	if _, err := f.resources.EnsureCanonical(f.ctx, src2.ID, seed); err != nil {
		t.Fatalf("source2 scan1: %v", err)
	}
	src2Work := f.activeWorkIDIn(t, src2.ID, "wkA")

	// 仅在 source1 触发拆分并阻塞；source2 不应产生任何 issue。
	split := []application.DiscoveredWork{
		work("wkA1", "作品甲一", blob("wkA1/m1", "d1", 0), blob("wkA1/m2", "d2", 1)),
		work("wkA2", "作品甲二", blob("wkA2/m3", "d3", 0)),
	}
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, split); err == nil {
		t.Fatal("source1 拆分应阻塞")
	}
	src2Issues, err := f.resources.ListBindingIssues(f.ctx, application.BindingIssueFilter{SourceID: src2.ID, Status: "open"}, "", 50)
	if err != nil || len(src2Issues.Items) != 0 {
		t.Fatalf("source2 不应受影响: %+v %v", src2Issues.Items, err)
	}
	// source2 的 wkA 绑定保持 active 且指向原 CanonicalWork。
	if got := f.activeWorkIDIn(t, src2.ID, "wkA"); got != src2Work {
		t.Fatalf("source2 Binding 被 source1 拆分影响: got=%s want=%s", got, src2Work)
	}
}

// TestSplitThenMergeSequence 验证拆分后再对拆分出的两个 SourceWork 做合并的连续结构变化。
func TestSplitThenMergeSequence(t *testing.T) {
	f := newIssueFixture(t)
	issue, origin, split := f.seedSplit(t)
	if _, err := f.resources.ResolveSourceStructureIssue(f.ctx, issue.ID, "owner", "split_inherit", "wkA1", "", issue.Version); err != nil {
		t.Fatalf("拆分决策: %v", err)
	}
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, split); err != nil {
		t.Fatalf("拆分重扫: %v", err)
	}
	y := f.activeWorkID(t, "wkA2")
	if y == "" || y == origin {
		t.Fatalf("拆分未生成新作品: y=%s origin=%s", y, origin)
	}
	// 再合并：wkA1、wkA2 消失，媒体汇聚到 wkM。
	merge := []application.DiscoveredWork{
		work("wkM", "再合并", blob("wkM/m1", "d1", 0), blob("wkM/m2", "d2", 1), blob("wkM/m3", "d3", 2)),
	}
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, merge); err == nil {
		t.Fatal("后续合并应阻塞")
	}
	open := f.openIssues(t)
	if len(open) != 1 || open[0].Code != "SOURCE_WORK_MERGE_REVIEW_REQUIRED" {
		t.Fatalf("未产生合并审查 issue: %+v", open)
	}
	// 绑定到拆分继承作品 X。
	if _, err := f.resources.ResolveSourceStructureIssue(f.ctx, open[0].ID, "owner", "merge_bind_existing", "", origin, open[0].Version); err != nil {
		t.Fatalf("合并决策: %v", err)
	}
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, merge); err != nil {
		t.Fatalf("合并重扫: %v", err)
	}
	if got := f.activeWorkID(t, "wkM"); got != origin {
		t.Fatalf("合并后 wkM 未绑定 X: got=%s origin=%s", got, origin)
	}
}
