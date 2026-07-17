package application_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/storage"
)

type orphanFixture struct {
	ctx       context.Context
	resources *application.Resources
	control   *storage.Database
	sourceID  string
	input     application.DiscoveredWork
}

func newOrphanFixture(t *testing.T) orphanFixture {
	t.Helper()
	ctx := context.Background()
	now := clock.Fixed{Time: time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC)}
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, now, identity.NewGenerator(now))
	if err != nil {
		t.Fatal(err)
	}
	library, err := resources.CreateLibrary(ctx, "orphans")
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(sourceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	source, err := resources.CreateSource(ctx, library.ID, "source", sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	input := application.DiscoveredWork{SourceKey: "w1", ProviderID: "example", ExternalID: "post-1", Title: "标题",
		Creator: application.DiscoveredCreator{SourceKey: "creator/c1", ProviderID: "example", ExternalID: "creator-1", Name: "创作者"},
		Media:   []application.DiscoveredMedia{{SourceKey: "w1/a.jpg", RuleKey: "a.jpg", Algorithm: "sha256-v1", Digest: digest, Ordinal: 0}}}
	return orphanFixture{ctx: ctx, resources: resources, control: store.Control, sourceID: source.ID, input: input}
}

// escalateToCandidate 先发现一次作品，再连续 3 次成功扫描均不再发现它，使 Binding 到达
// 默认保留窗口并升级为 orphan_candidate。
func (f orphanFixture) escalateToCandidate(t *testing.T) application.CanonicalWork {
	t.Helper()
	first, err := f.resources.EnsureCanonical(f.ctx, f.sourceID, []application.DiscoveredWork{f.input})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := f.resources.EnsureCanonical(f.ctx, f.sourceID, nil); err != nil {
			t.Fatal(err)
		}
	}
	return first[f.input.SourceKey]
}

func (f orphanFixture) workBinding(t *testing.T) (bindingID, status string, missed int) {
	t.Helper()
	if err := f.control.SQL().QueryRowContext(f.ctx, `SELECT binding_id, status, missed_scans FROM work_bindings
WHERE source_id=? AND source_key=? ORDER BY created_at DESC LIMIT 1`, f.sourceID, f.input.SourceKey).
		Scan(&bindingID, &status, &missed); err != nil {
		t.Fatal(err)
	}
	return bindingID, status, missed
}

func TestOrphanRetentionEscalatesAfterWindow(t *testing.T) {
	f := newOrphanFixture(t)
	if _, err := f.resources.EnsureCanonical(f.ctx, f.sourceID, []application.DiscoveredWork{f.input}); err != nil {
		t.Fatal(err)
	}
	// 前两次缺失仍在保留窗口内，保持 inactive。
	for want := 1; want <= 2; want++ {
		if _, err := f.resources.EnsureCanonical(f.ctx, f.sourceID, nil); err != nil {
			t.Fatal(err)
		}
		_, status, missed := f.workBinding(t)
		if status != "inactive" || missed != want {
			t.Fatalf("第 %d 次缺失: status=%s missed=%d", want, status, missed)
		}
	}
	// 第三次缺失达到默认窗口，升级为 orphan_candidate。
	if _, err := f.resources.EnsureCanonical(f.ctx, f.sourceID, nil); err != nil {
		t.Fatal(err)
	}
	if _, status, missed := f.workBinding(t); status != "orphan_candidate" || missed != 3 {
		t.Fatalf("未升级为 orphan_candidate: status=%s missed=%d", status, missed)
	}
}

func TestOrphanCandidateReappearsRestoresActive(t *testing.T) {
	f := newOrphanFixture(t)
	original := f.escalateToCandidate(t)
	reappeared, err := f.resources.EnsureCanonical(f.ctx, f.sourceID, []application.DiscoveredWork{f.input})
	if err != nil {
		t.Fatal(err)
	}
	if reappeared[f.input.SourceKey].ID != original.ID {
		t.Fatalf("重现未复用原 Canonical 作品: %s vs %s", reappeared[f.input.SourceKey].ID, original.ID)
	}
	if _, status, missed := f.workBinding(t); status != "active" || missed != 0 {
		t.Fatalf("重现后未恢复 active 并清零: status=%s missed=%d", status, missed)
	}
}

func TestOrphanListAndRetainDecision(t *testing.T) {
	f := newOrphanFixture(t)
	f.escalateToCandidate(t)
	page, err := f.resources.ListOrphanCandidates(f.ctx, application.OrphanCandidateFilter{}, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	// work、media、creator 三个 Binding 都应到达 orphan_candidate。
	if len(page.Items) != 3 {
		t.Fatalf("orphan candidate 数量: %d", len(page.Items))
	}
	var workCandidate application.OrphanCandidate
	for _, item := range page.Items {
		if item.EntityType == "work" {
			workCandidate = item
		}
		if item.MissedScans != 3 || item.RetentionThreshold != 3 {
			t.Fatalf("候选计数错误: %+v", item)
		}
	}
	if workCandidate.CanonicalLabel != f.input.Title {
		t.Fatalf("work 候选 label 错误: %q", workCandidate.CanonicalLabel)
	}
	result, err := f.resources.DecideOrphanCandidate(f.ctx, workCandidate.BindingID, "retain", 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.NewStatus != "inactive" {
		t.Fatalf("retain 后状态: %s", result.NewStatus)
	}
	if _, status, missed := f.workBinding(t); status != "inactive" || missed != 0 {
		t.Fatalf("retain 未复位: status=%s missed=%d", status, missed)
	}
}

func TestOrphanExtendDecisionWidensWindow(t *testing.T) {
	f := newOrphanFixture(t)
	f.escalateToCandidate(t)
	bindingID, _, _ := f.workBinding(t)
	if _, err := f.resources.DecideOrphanCandidate(f.ctx, bindingID, "extend", 5); err != nil {
		t.Fatal(err)
	}
	var override int
	var status string
	var missed int
	if err := f.control.SQL().QueryRowContext(f.ctx, `SELECT retention_scans_override, status, missed_scans
FROM work_bindings WHERE binding_id=?`, bindingID).Scan(&override, &status, &missed); err != nil {
		t.Fatal(err)
	}
	if override != 8 || status != "inactive" || missed != 0 {
		t.Fatalf("extend 结果错误: override=%d status=%s missed=%d", override, status, missed)
	}
	// 一次缺失不应立即再次升级。
	if _, err := f.resources.EnsureCanonical(f.ctx, f.sourceID, nil); err != nil {
		t.Fatal(err)
	}
	if _, status, missed := f.workBinding(t); status != "inactive" || missed != 1 {
		t.Fatalf("延长窗口后过早升级: status=%s missed=%d", status, missed)
	}
}

func TestOrphanConfirmKeepsCanonicalAndRestoresOnReturn(t *testing.T) {
	f := newOrphanFixture(t)
	original := f.escalateToCandidate(t)
	bindingID, _, _ := f.workBinding(t)
	if _, err := f.resources.DecideOrphanCandidate(f.ctx, bindingID, "confirm_orphaned", 0); err != nil {
		t.Fatal(err)
	}
	if _, status, _ := f.workBinding(t); status != "orphaned" {
		t.Fatalf("confirm 未转 orphaned: %s", status)
	}
	// 确认孤立不得删除 Canonical 作品。
	var count int
	if err := f.control.SQL().QueryRowContext(f.ctx, `SELECT count(*) FROM canonical_works WHERE work_id=?`,
		original.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("确认孤立后 Canonical 作品被删除: %d", count)
	}
	reappeared, err := f.resources.EnsureCanonical(f.ctx, f.sourceID, []application.DiscoveredWork{f.input})
	if err != nil {
		t.Fatal(err)
	}
	if reappeared[f.input.SourceKey].ID != original.ID {
		t.Fatalf("确认孤立后重现未复用原作品: %s vs %s", reappeared[f.input.SourceKey].ID, original.ID)
	}
}

func TestOrphanUnbindDecisionSplitsOnReturn(t *testing.T) {
	f := newOrphanFixture(t)
	original := f.escalateToCandidate(t)
	bindingID, _, _ := f.workBinding(t)
	if _, err := f.resources.DecideOrphanCandidate(f.ctx, bindingID, "unbind", 0); err != nil {
		t.Fatal(err)
	}
	if _, status, _ := f.workBinding(t); status != "manual_unbound" {
		t.Fatalf("unbind 未转 manual_unbound: %s", status)
	}
	reappeared, err := f.resources.EnsureCanonical(f.ctx, f.sourceID, []application.DiscoveredWork{f.input})
	if err != nil {
		t.Fatal(err)
	}
	if reappeared[f.input.SourceKey].ID == original.ID {
		t.Fatalf("解绑后重现仍复用原作品: %s", original.ID)
	}
}

func TestOrphanPerEntityWhileSourceOnline(t *testing.T) {
	f := newOrphanFixture(t)
	second := application.DiscoveredWork{SourceKey: "w2", ProviderID: "example", ExternalID: "post-2", Title: "第二",
		Media: []application.DiscoveredMedia{{SourceKey: "w2/b.jpg", RuleKey: "b.jpg", Algorithm: "sha256-v1",
			Digest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Ordinal: 0}}}
	if _, err := f.resources.EnsureCanonical(f.ctx, f.sourceID, []application.DiscoveredWork{f.input, second}); err != nil {
		t.Fatal(err)
	}
	// Source 仍在线，但 w2 在随后三次成功扫描中缺失：只有 w2 升级，w1 保持 active。
	for i := 0; i < 3; i++ {
		if _, err := f.resources.EnsureCanonical(f.ctx, f.sourceID, []application.DiscoveredWork{f.input}); err != nil {
			t.Fatal(err)
		}
	}
	if _, status, _ := f.workBinding(t); status != "active" {
		t.Fatalf("在线作品被误升级: %s", status)
	}
	var w2Status string
	if err := f.control.SQL().QueryRowContext(f.ctx, `SELECT status FROM work_bindings
WHERE source_id=? AND source_key='w2'`, f.sourceID).Scan(&w2Status); err != nil {
		t.Fatal(err)
	}
	if w2Status != "orphan_candidate" {
		t.Fatalf("缺失作品未升级: %s", w2Status)
	}
}

func TestOrphanDecisionRejectsInvalidTargets(t *testing.T) {
	f := newOrphanFixture(t)
	f.escalateToCandidate(t)
	bindingID, _, _ := f.workBinding(t)
	if _, err := f.resources.DecideOrphanCandidate(f.ctx, bindingID, "bogus", 0); !hasCode(err, fault.CodeValidation) {
		t.Fatalf("非法决策未被拒绝: %v", err)
	}
	if _, err := f.resources.DecideOrphanCandidate(f.ctx, "wbind_00000000-0000-7000-8000-000000000000", "retain", 0); !hasCode(err, fault.CodeNotFound) {
		t.Fatalf("不存在 Binding 未返回 NOT_FOUND: %v", err)
	}
	// 已经 retain 回 inactive 的 Binding 不再是候选，重复决策应冲突。
	if _, err := f.resources.DecideOrphanCandidate(f.ctx, bindingID, "retain", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.resources.DecideOrphanCandidate(f.ctx, bindingID, "retain", 0); !hasCode(err, fault.CodeConflict) {
		t.Fatalf("非候选 Binding 决策未冲突: %v", err)
	}
}

func hasCode(err error, code fault.Code) bool {
	var structured *fault.Error
	return errors.As(err, &structured) && structured.Code == code
}
