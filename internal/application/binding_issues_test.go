package application_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/storage"
)

type issueFixture struct {
	ctx       context.Context
	resources *application.Resources
	control   *sql.DB
	source    application.Source
	libraryID string
	ids       func(domain.IDKind) string
}

func newIssueFixture(t *testing.T) *issueFixture {
	t.Helper()
	ctx := context.Background()
	now := clock.Fixed{Time: time.Date(2026, 7, 17, 5, 0, 0, 0, time.UTC)}
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	generator := identity.NewGenerator(now)
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, now, generator)
	if err != nil {
		t.Fatal(err)
	}
	library, err := resources.CreateLibrary(ctx, "issues")
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
	return &issueFixture{ctx: ctx, resources: resources, control: store.Control.SQL(), source: source, libraryID: library.ID,
		ids: func(kind domain.IDKind) string {
			id, err := generator.New(kind)
			if err != nil {
				t.Fatal(err)
			}
			return id.String()
		}}
}

// seedOrphanWork 建立一个 CanonicalWork 及其 orphaned WorkBinding，用于制造 external_id 冲突。
func (f *issueFixture) seedOrphanWork(t *testing.T, title, sourceKey, externalID string) string {
	t.Helper()
	workID := f.ids(domain.IDCanonicalWork)
	bindingID := f.ids(domain.IDWorkBinding)
	if _, err := f.control.ExecContext(f.ctx, `INSERT INTO canonical_works
(work_id, title, created_at) VALUES (?, ?, 1)`, workID, title); err != nil {
		t.Fatal(err)
	}
	if _, err := f.control.ExecContext(f.ctx, `INSERT INTO work_bindings
(binding_id, source_id, provider_id, external_id, source_key, work_id, identity_version,
 status, last_seen_generation, created_at, updated_at)
VALUES (?, ?, 'example', ?, ?, ?, 1, 'orphaned', 0, 1, 1)`,
		bindingID, f.source.ID, externalID, sourceKey, workID); err != nil {
		t.Fatal(err)
	}
	return workID
}

func (f *issueFixture) discover(sourceKey, externalID, title string) []application.DiscoveredWork {
	return []application.DiscoveredWork{{SourceKey: sourceKey, ProviderID: "example", ExternalID: externalID, Title: title}}
}

func (f *issueFixture) openIssues(t *testing.T) []application.BindingIssue {
	t.Helper()
	page, err := f.resources.ListBindingIssues(f.ctx, application.BindingIssueFilter{SourceID: f.source.ID, Status: "open"}, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	return page.Items
}

func TestBindingIssueEvidenceDedupSupersedeAndStale(t *testing.T) {
	f := newIssueFixture(t)
	workA := f.seedOrphanWork(t, "作品甲", "alias-a", "post-42")
	workB := f.seedOrphanWork(t, "作品乙", "alias-b", "post-42")

	// 新 source_key 通过相同 external_id 命中两个候选，扫描无法唯一绑定。
	_, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, f.discover("alias-new", "post-42", "冲突作品"))
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeBindingReviewRequired {
		t.Fatalf("多候选未阻塞: %v", err)
	}
	open := f.openIssues(t)
	if len(open) != 1 {
		t.Fatalf("首次冲突未产生唯一 open issue: %d", len(open))
	}
	issue, err := f.resources.GetBindingIssue(f.ctx, open[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if issue.EntityType != "work" || issue.SourceKey != "alias-new" || issue.CandidateCount != 2 ||
		issue.Status != "open" || issue.Version != 1 || len(issue.Candidates) != 2 {
		t.Fatalf("issue 元数据错误: %+v", issue)
	}
	labels := map[string]string{}
	for _, candidate := range issue.Candidates {
		if candidate.CandidateKind != "work" || candidate.MatchSignal != "external_id" || candidate.MatchValue != "post-42" {
			t.Fatalf("候选证据错误: %+v", candidate)
		}
		labels[candidate.CandidateID] = candidate.Label
	}
	if labels[workA] != "作品甲" || labels[workB] != "作品乙" {
		t.Fatalf("候选标签未脱敏映射: %+v", labels)
	}

	// 相同证据重扫：复用现有 issue，不重复产生。
	_, _ = f.resources.EnsureCanonical(f.ctx, f.source.ID, f.discover("alias-new", "post-42", "冲突作品"))
	if got := f.openIssues(t); len(got) != 1 || got[0].ID != issue.ID {
		t.Fatalf("相同证据重扫产生重复 issue: %+v", got)
	}

	// 证据变化（新增第三候选）：旧 issue superseded，新 open issue 有三个候选。
	f.seedOrphanWork(t, "作品丙", "alias-c", "post-42")
	_, _ = f.resources.EnsureCanonical(f.ctx, f.source.ID, f.discover("alias-new", "post-42", "冲突作品"))
	open = f.openIssues(t)
	if len(open) != 1 || open[0].ID == issue.ID || open[0].CandidateCount != 3 {
		t.Fatalf("证据变化未产生新 issue: %+v", open)
	}
	superseded, err := f.resources.ListBindingIssues(f.ctx, application.BindingIssueFilter{SourceID: f.source.ID, Status: "superseded"}, "", 50)
	if err != nil || len(superseded.Items) != 1 || superseded.Items[0].ID != issue.ID {
		t.Fatalf("旧 issue 未标 superseded: %+v %v", superseded.Items, err)
	}

	// 成功扫描但不再发现该 source_key：其候选已消失，open issue 变 stale。
	other := f.seedOrphanWork(t, "独立作品", "alias-other", "post-99")
	_ = other
	if _, err := f.resources.EnsureCanonical(f.ctx, f.source.ID, f.discover("alias-other", "post-99", "独立作品")); err != nil {
		t.Fatalf("无冲突扫描应成功: %v", err)
	}
	if got := f.openIssues(t); len(got) != 0 {
		t.Fatalf("消失来源的 open issue 未收敛: %+v", got)
	}
	stale, err := f.resources.ListBindingIssues(f.ctx, application.BindingIssueFilter{SourceID: f.source.ID, Status: "stale"}, "", 50)
	if err != nil || len(stale.Items) != 1 {
		t.Fatalf("消失来源未标 stale: %+v %v", stale.Items, err)
	}
}
