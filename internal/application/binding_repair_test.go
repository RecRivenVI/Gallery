package application_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
)

func (f *issueFixture) firstOpenIssue(t *testing.T) application.BindingIssue {
	t.Helper()
	open := f.openIssues(t)
	if len(open) != 1 {
		t.Fatalf("期望唯一 open issue，实际 %d", len(open))
	}
	issue, err := f.resources.GetBindingIssue(f.ctx, open[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return issue
}

func (f *issueFixture) activeWorkBinding(t *testing.T, sourceKey string) (string, string) {
	t.Helper()
	var workID, status string
	err := f.control.QueryRowContext(f.ctx, `SELECT work_id, status FROM work_bindings
WHERE source_id=? AND source_key=? ORDER BY CASE status WHEN 'active' THEN 0 ELSE 1 END, updated_at DESC LIMIT 1`,
		f.source.ID, sourceKey).Scan(&workID, &status)
	if err != nil {
		t.Fatalf("读取 work binding 失败: %v", err)
	}
	return workID, status
}

func (f *issueFixture) hasCode(t *testing.T, err error, code fault.Code) bool {
	t.Helper()
	var structured *fault.Error
	return errors.As(err, &structured) && structured.Code == code
}

func TestBindingIssueResolveBindExistingHonoredByRescan(t *testing.T) {
	f := newIssueFixture(t)
	workA := f.seedOrphanWork(t, "作品甲", "alias-a", "post-42")
	f.seedOrphanWork(t, "作品乙", "alias-b", "post-42")
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, f.discover("alias-new", "post-42", "冲突")); !f.hasCode(t, err, fault.CodeBindingReviewRequired) {
		t.Fatalf("未产生冲突: %v", err)
	}
	issue := f.firstOpenIssue(t)

	resolved, err := f.resources.ResolveBindingIssue(f.ctx, issue.ID, "owner", "bind_existing", workA, issue.Version)
	if err != nil || resolved.Status != "resolved" || resolved.Resolution != "bind_existing" || resolved.ResolvedTargetID != workA {
		t.Fatalf("bind_existing 修复失败: %+v %v", resolved, err)
	}
	result, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, f.discover("alias-new", "post-42", "冲突"))
	if err != nil {
		t.Fatalf("修复后重扫仍失败: %v", err)
	}
	if result["alias-new"].ID != workA {
		t.Fatalf("重扫未绑定到指定候选: got=%s want=%s", result["alias-new"].ID, workA)
	}
	if _, status := f.activeWorkBinding(t, "alias-new"); status != "active" {
		t.Fatalf("修复后未建立 active binding: %s", status)
	}
}

func TestBindingIssueResolveCreateNewHonoredByRescan(t *testing.T) {
	f := newIssueFixture(t)
	workA := f.seedOrphanWork(t, "作品甲", "alias-a", "post-42")
	workB := f.seedOrphanWork(t, "作品乙", "alias-b", "post-42")
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, f.discover("alias-new", "post-42", "冲突")); !f.hasCode(t, err, fault.CodeBindingReviewRequired) {
		t.Fatalf("未产生冲突: %v", err)
	}
	issue := f.firstOpenIssue(t)
	if _, err := f.resources.ResolveBindingIssue(f.ctx, issue.ID, "owner", "create_new", "", issue.Version); err != nil {
		t.Fatalf("create_new 修复失败: %v", err)
	}
	result, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, f.discover("alias-new", "post-42", "独立"))
	if err != nil {
		t.Fatalf("修复后重扫仍失败: %v", err)
	}
	newID := result["alias-new"].ID
	if newID == workA || newID == workB || newID == "" {
		t.Fatalf("create_new 未创建独立实体: %s", newID)
	}
}

func TestBindingIssueDismissSuppressesDuplicateButStillBlocks(t *testing.T) {
	f := newIssueFixture(t)
	f.seedOrphanWork(t, "作品甲", "alias-a", "post-42")
	f.seedOrphanWork(t, "作品乙", "alias-b", "post-42")
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, f.discover("alias-new", "post-42", "冲突")); !f.hasCode(t, err, fault.CodeBindingReviewRequired) {
		t.Fatalf("未产生冲突: %v", err)
	}
	issue := f.firstOpenIssue(t)
	dismissed, err := f.resources.DismissBindingIssue(f.ctx, issue.ID, "owner", issue.Version)
	if err != nil || dismissed.Status != "dismissed" {
		t.Fatalf("dismiss 失败: %+v %v", dismissed, err)
	}
	// 相同证据重扫仍失败（身份未解决），但不产生新 issue，忽略记录被复用。
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, f.discover("alias-new", "post-42", "冲突")); !f.hasCode(t, err, fault.CodeBindingReviewRequired) {
		t.Fatalf("dismiss 后应仍阻塞: %v", err)
	}
	if got := f.openIssues(t); len(got) != 0 {
		t.Fatalf("dismiss 后产生了新 open issue: %+v", got)
	}
	page, err := f.resources.ListBindingIssues(f.ctx, application.BindingIssueFilter{SourceID: f.source.ID, Status: "dismissed"}, "", 50)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != issue.ID {
		t.Fatalf("dismissed issue 未被复用: %+v %v", page.Items, err)
	}
	// 重新打开后可再次处理。
	reopened, err := f.resources.ReopenBindingIssue(f.ctx, page.Items[0].ID, "owner", page.Items[0].Version)
	if err != nil || reopened.Status != "open" || reopened.Resolution != "" {
		t.Fatalf("reopen 失败: %+v %v", reopened, err)
	}
}

func TestBindingIssueOptimisticVersionConflict(t *testing.T) {
	f := newIssueFixture(t)
	workA := f.seedOrphanWork(t, "作品甲", "alias-a", "post-42")
	f.seedOrphanWork(t, "作品乙", "alias-b", "post-42")
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, f.discover("alias-new", "post-42", "冲突")); !f.hasCode(t, err, fault.CodeBindingReviewRequired) {
		t.Fatalf("未产生冲突: %v", err)
	}
	issue := f.firstOpenIssue(t)
	if _, err := f.resources.ResolveBindingIssue(f.ctx, issue.ID, "owner", "bind_existing", workA, issue.Version+1); !f.hasCode(t, err, fault.CodeConflict) {
		t.Fatalf("过时 version 未冲突: %v", err)
	}
	if _, err := f.resources.ResolveBindingIssue(f.ctx, issue.ID, "owner", "bind_existing", "wrk_not-a-candidate", issue.Version); !f.hasCode(t, err, fault.CodeValidation) {
		t.Fatalf("非候选 target 未拒绝: %v", err)
	}
}

func TestManualUnbindWorkUndoAndConflict(t *testing.T) {
	f := newIssueFixture(t)
	first, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, f.discover("clean-key", "", "干净作品"))
	if err != nil {
		t.Fatal(err)
	}
	workID := first["clean-key"].ID

	unbound, err := f.resources.ManualUnbindWork(f.ctx, f.source.ID, "clean-key")
	if err != nil || unbound != workID {
		t.Fatalf("解绑失败: %s %v", unbound, err)
	}
	// 未重扫前撤销：恢复原 active binding。
	restored, err := f.resources.UndoManualUnbind(f.ctx, f.source.ID, "clean-key")
	if err != nil || restored != workID {
		t.Fatalf("撤销解绑失败: %s %v", restored, err)
	}
	again, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, f.discover("clean-key", "", "干净作品"))
	if err != nil || again["clean-key"].ID != workID {
		t.Fatalf("撤销后未复用原作品: %+v %v", again, err)
	}

	// 解绑后重扫已拆分出新作品，再撤销应冲突。
	if _, err := f.resources.ManualUnbindWork(f.ctx, f.source.ID, "clean-key"); err != nil {
		t.Fatal(err)
	}
	split, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, f.discover("clean-key", "", "干净作品"))
	if err != nil || split["clean-key"].ID == workID {
		t.Fatalf("解绑后重扫未拆分: %+v %v", split, err)
	}
	if _, err := f.resources.UndoManualUnbind(f.ctx, f.source.ID, "clean-key"); !f.hasCode(t, err, fault.CodeConflict) {
		t.Fatalf("已被后续绑定依赖的撤销未冲突: %v", err)
	}
}

func TestManualUnbindIsolatedAcrossSources(t *testing.T) {
	f := newIssueFixture(t)
	// 第二个 Source 指向同一个 CanonicalWork。
	root2 := filepath.Join(t.TempDir(), "source2")
	if err := os.MkdirAll(root2, 0o700); err != nil {
		t.Fatal(err)
	}
	source2, err := f.resources.CreateSource(f.ctx, f.libraryID, "source2", root2)
	if err != nil {
		t.Fatal(err)
	}
	first, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, f.discover("shared-key", "", "共享作品"))
	if err != nil {
		t.Fatal(err)
	}
	workID := first["shared-key"].ID
	if _, err := f.control.ExecContext(f.ctx, `INSERT INTO work_bindings
(binding_id, source_id, source_key, work_id, identity_version, status, last_seen_generation, created_at, updated_at)
VALUES (?, ?, 'shared-key', ?, 1, 'active', 0, 1, 1)`, f.ids(domain.IDWorkBinding), source2.ID, workID); err != nil {
		t.Fatal(err)
	}

	if _, err := f.resources.ManualUnbindWork(f.ctx, f.source.ID, "shared-key"); err != nil {
		t.Fatal(err)
	}
	var source1Status, source2Status string
	_ = f.control.QueryRowContext(f.ctx, `SELECT status FROM work_bindings WHERE source_id=? AND source_key='shared-key'`, f.source.ID).Scan(&source1Status)
	_ = f.control.QueryRowContext(f.ctx, `SELECT status FROM work_bindings WHERE source_id=? AND source_key='shared-key'`, source2.ID).Scan(&source2Status)
	if source1Status != "manual_unbound" || source2Status != "active" {
		t.Fatalf("解绑越过 Source 边界: source1=%s source2=%s", source1Status, source2Status)
	}
}

func TestManualUnbindMediaHonoredByRescan(t *testing.T) {
	f := newIssueFixture(t)
	discover := func() []application.DiscoveredWork {
		return []application.DiscoveredWork{{SourceKey: "work-key", Title: "带媒体作品",
			Media: []application.DiscoveredMedia{{SourceKey: "work-key/a.jpg", RuleKey: "a.jpg",
				Algorithm: "sha256-v1", Digest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Ordinal: 0}}}}
	}
	first, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, discover())
	if err != nil {
		t.Fatal(err)
	}
	mediaID := first["work-key"].Media["work-key/a.jpg"].ID

	unbound, err := f.resources.UnbindMedia(f.ctx, f.source.ID, "work-key/a.jpg")
	if err != nil || unbound != mediaID {
		t.Fatalf("媒体解绑失败: %s %v", unbound, err)
	}
	split, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, discover())
	if err != nil {
		t.Fatal(err)
	}
	if split["work-key"].Media["work-key/a.jpg"].ID == mediaID {
		t.Fatalf("媒体解绑被扫描恢复: %s", split["work-key"].Media["work-key/a.jpg"].ID)
	}
}
