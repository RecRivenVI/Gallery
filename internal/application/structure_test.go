package application_test

import (
	"errors"
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
