package application_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/storage"
)

// recoverableFixture 持有可关闭、可删除 catalog、可重开 control 的完整 store，用于 split/merge
// Catalog 全量重建恢复门禁。
type recoverableFixture struct {
	ctx       context.Context
	dirs      appdirs.Dirs
	clock     clock.Fixed
	generator interface {
		New(domain.IDKind) (domain.ID, error)
	}
	resources *application.Resources
	control   *sql.DB
	store     *storage.Store
	sourceID  string
}

func newRecoverableFixture(t *testing.T) *recoverableFixture {
	t.Helper()
	ctx := context.Background()
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 5, 0, 0, 0, time.UTC)}
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	generator := identity.NewGenerator(now)
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, now, generator)
	if err != nil {
		t.Fatal(err)
	}
	library, err := resources.CreateLibrary(ctx, "recover")
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
	return &recoverableFixture{ctx: ctx, dirs: dirs, clock: now, generator: generator,
		resources: resources, control: store.Control.SQL(), store: store, sourceID: source.ID}
}

func (f *recoverableFixture) activeWorkID(t *testing.T, sourceKey string) string {
	t.Helper()
	var workID string
	if err := f.control.QueryRowContext(f.ctx, `SELECT work_id FROM work_bindings
WHERE source_id=? AND source_key=? AND status='active'`, f.sourceID, sourceKey).Scan(&workID); err != nil {
		return ""
	}
	return workID
}

// reopenWithoutCatalog 关闭 store，删除 catalog.db 及其 WAL/SHM，重开 control 并重建 Resources，
// 模拟 Catalog 全量重建：control.db 中的拆分/合并决策必须原样保留。
func (f *recoverableFixture) reopenWithoutCatalog(t *testing.T) {
	t.Helper()
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(f.dirs.Data, "catalog.db")
	for _, path := range []string{catalogPath, catalogPath + "-wal", catalogPath + "-shm"} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	reopened, err := storage.Open(f.ctx, f.dirs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	generator := identity.NewGenerator(f.clock)
	resources, err := application.NewResources(reopened.Control.SQL(), f.dirs, filesystem.OS{}, f.clock, generator)
	if err != nil {
		t.Fatal(err)
	}
	f.store, f.control, f.resources, f.generator = reopened, reopened.Control.SQL(), resources, generator
}

// TestSplitMergeDecisionsSurviveCatalogRebuild 是 split/merge 的 Catalog 全量重建恢复门禁：应用
// 拆分与合并决策 → 删除 catalog.db → 重开 control → 全量重扫，验证决策恢复、Canonical ID 不漂移、
// 被否定的候选不复现、无重复 Canonical 实体、Source 零变化。
func TestSplitMergeDecisionsSurviveCatalogRebuild(t *testing.T) {
	f := newRecoverableFixture(t)

	// 建立并解决一处拆分：wkA(m1,m2,m3) → wkA1 继承 X，wkA2 新建 Y。
	if _, err := f.resources.EnsureCanonical(f.ctx, f.sourceID, []application.DiscoveredWork{
		work("wkA", "作品甲", blob("wkA/m1", "d1", 0), blob("wkA/m2", "d2", 1), blob("wkA/m3", "d3", 2)),
	}); err != nil {
		t.Fatalf("scan1: %v", err)
	}
	originX := f.activeWorkID(t, "wkA")
	split := []application.DiscoveredWork{
		work("wkA1", "作品甲一", blob("wkA1/m1", "d1", 0), blob("wkA1/m2", "d2", 1)),
		work("wkA2", "作品甲二", blob("wkA2/m3", "d3", 0)),
	}
	if _, err := f.resources.EnsureCanonical(f.ctx, f.sourceID, split); err == nil {
		t.Fatal("拆分应阻塞")
	}
	splitIssue := f.firstOpenIssue(t)
	if _, err := f.resources.ResolveSourceStructureIssue(f.ctx, splitIssue, "owner", "split_inherit", "wkA1", "", 1); err != nil {
		t.Fatalf("拆分决策: %v", err)
	}
	if _, err := f.resources.EnsureCanonical(f.ctx, f.sourceID, split); err != nil {
		t.Fatalf("拆分决策后重扫: %v", err)
	}
	inheritX := f.activeWorkID(t, "wkA1")
	newY := f.activeWorkID(t, "wkA2")
	if inheritX != originX || newY == "" || newY == originX {
		t.Fatalf("拆分应用异常: X=%s inherit=%s Y=%s", originX, inheritX, newY)
	}

	// 删除 catalog.db 并重开 control，随后全量重扫同一拆分结构。
	f.reopenWithoutCatalog(t)
	if _, err := f.resources.EnsureCanonical(f.ctx, f.sourceID, split); err != nil {
		t.Fatalf("重建后全量重扫应成功（决策已恢复）: %v", err)
	}

	// Canonical ID 不漂移。
	if got := f.activeWorkID(t, "wkA1"); got != originX {
		t.Fatalf("重建后 wkA1 未复用原 CanonicalWork: got=%s want=%s", got, originX)
	}
	if got := f.activeWorkID(t, "wkA2"); got != newY {
		t.Fatalf("重建后 wkA2 CanonicalWork 漂移: got=%s want=%s", got, newY)
	}
	// 被否定的候选不复现：wkA2 从未绑定 X。
	var toX int
	if err := f.control.QueryRowContext(f.ctx, `SELECT count(*) FROM work_bindings
WHERE source_id=? AND source_key='wkA2' AND work_id=? AND status='active'`, f.sourceID, originX).Scan(&toX); err != nil {
		t.Fatal(err)
	}
	if toX != 0 {
		t.Fatal("重建后被否定候选 X 复现于 wkA2")
	}
	// 决策记录仍为 applied，无重复。
	decisions, err := f.resources.ListSourceStructureDecisions(f.ctx, f.sourceID, "applied", 50)
	if err != nil || len(decisions) != 1 || decisions[0].Status != "applied" {
		t.Fatalf("重建后决策未保留: %+v %v", decisions, err)
	}
	// 无重复 Canonical 实体：恰好 X 与 Y 两个作品。
	var workCount int
	if err := f.control.QueryRowContext(f.ctx, "SELECT count(*) FROM canonical_works").Scan(&workCount); err != nil {
		t.Fatal(err)
	}
	if workCount != 2 {
		t.Fatalf("重建重复创建 CanonicalWork: %d", workCount)
	}
}

func (f *recoverableFixture) firstOpenIssue(t *testing.T) string {
	t.Helper()
	page, err := f.resources.ListBindingIssues(f.ctx, application.BindingIssueFilter{SourceID: f.sourceID, Status: "open"}, "", 50)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("期望一个 open issue: %+v %v", page.Items, err)
	}
	return page.Items[0].ID
}
