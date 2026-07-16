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
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/storage"
)

func TestStableBindingRenameOrphanAndManualSplit(t *testing.T) {
	ctx := context.Background()
	now := clock.Fixed{Time: time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC)}
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, now, identity.NewGenerator(now))
	if err != nil {
		t.Fatal(err)
	}
	library, err := resources.CreateLibrary(ctx, "bindings")
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
	firstInput := application.DiscoveredWork{SourceKey: "old/path", ProviderID: "example", ExternalID: "post-42", Title: "标题",
		Media: []application.DiscoveredMedia{{SourceKey: "old/path/a.jpg", RuleKey: "a.jpg", Algorithm: "sha256-v1", Digest: digest, Ordinal: 0}}}
	first, err := resources.EnsureCanonical(ctx, source.ID, []application.DiscoveredWork{firstInput})
	if err != nil {
		t.Fatal(err)
	}
	firstWork, firstMedia := first[firstInput.SourceKey], first[firstInput.SourceKey].Media[firstInput.Media[0].SourceKey]

	renamedInput := application.DiscoveredWork{SourceKey: "renamed/path", ProviderID: "example", ExternalID: "post-42", Title: "扫描标题变化",
		Media: []application.DiscoveredMedia{{SourceKey: "renamed/path/renamed.jpg", RuleKey: "renamed.jpg", Algorithm: "sha256-v1", Digest: digest, Ordinal: 0}}}
	renamed, err := resources.EnsureCanonical(ctx, source.ID, []application.DiscoveredWork{renamedInput})
	if err != nil {
		t.Fatal(err)
	}
	if renamed[renamedInput.SourceKey].ID != firstWork.ID || renamed[renamedInput.SourceKey].Title != firstWork.Title ||
		renamed[renamedInput.SourceKey].Media[renamedInput.Media[0].SourceKey].ID != firstMedia.ID {
		t.Fatalf("改名后 Canonical 身份漂移: first=%+v renamed=%+v", first, renamed)
	}
	var oldStatus, newStatus string
	_ = store.Control.SQL().QueryRowContext(ctx, `SELECT status FROM work_bindings
WHERE source_id=? AND source_key='old/path' ORDER BY created_at LIMIT 1`, source.ID).Scan(&oldStatus)
	_ = store.Control.SQL().QueryRowContext(ctx, `SELECT status FROM work_bindings
WHERE source_id=? AND source_key='renamed/path' ORDER BY created_at DESC LIMIT 1`, source.ID).Scan(&newStatus)
	if oldStatus != "orphaned" || newStatus != "active" {
		t.Fatalf("alias 生命周期错误: old=%s new=%s", oldStatus, newStatus)
	}

	if _, err := resources.EnsureCanonical(ctx, source.ID, nil); err != nil {
		t.Fatal(err)
	}
	var orphaned string
	_ = store.Control.SQL().QueryRowContext(ctx, `SELECT status FROM work_bindings
WHERE source_id=? AND source_key='renamed/path' AND work_id=?`, source.ID, firstWork.ID).Scan(&orphaned)
	if orphaned != "orphaned" {
		t.Fatalf("消失记录未 orphan: %s", orphaned)
	}
	reappeared, err := resources.EnsureCanonical(ctx, source.ID, []application.DiscoveredWork{renamedInput})
	if err != nil || reappeared[renamedInput.SourceKey].ID != firstWork.ID {
		t.Fatalf("重新出现未复用 orphan binding: %+v %v", reappeared, err)
	}

	unboundWork, err := resources.ManualUnbindWork(ctx, source.ID, renamedInput.SourceKey)
	if err != nil || unboundWork != firstWork.ID {
		t.Fatalf("手动解绑失败: %s %v", unboundWork, err)
	}
	split, err := resources.EnsureCanonical(ctx, source.ID, []application.DiscoveredWork{renamedInput})
	if err != nil {
		t.Fatal(err)
	}
	if split[renamedInput.SourceKey].ID == firstWork.ID || split[renamedInput.SourceKey].Media[renamedInput.Media[0].SourceKey].ID == firstMedia.ID {
		t.Fatalf("手动解绑被扫描恢复: old=%+v split=%+v", firstWork, split)
	}
	var manualCount int
	_ = store.Control.SQL().QueryRowContext(ctx, `SELECT count(*) FROM work_bindings
WHERE source_id=? AND source_key=? AND work_id=? AND status='manual_unbound'`, source.ID, renamedInput.SourceKey, firstWork.ID).Scan(&manualCount)
	if manualCount != 1 {
		t.Fatal("manual_unbound 历史未保留")
	}

	conflictingWorkID, err := identity.NewGenerator(now).New(domain.IDCanonicalWork)
	if err != nil {
		t.Fatal(err)
	}
	conflictingBindingID, _ := identity.NewGenerator(now).New(domain.IDWorkBinding)
	if _, err := store.Control.SQL().ExecContext(ctx, `INSERT INTO canonical_works
(work_id, title, created_at) VALUES (?, '冲突候选', 1)`, conflictingWorkID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Control.SQL().ExecContext(ctx, `INSERT INTO work_bindings
(binding_id, source_id, provider_id, external_id, source_key, work_id, identity_version,
 status, last_seen_generation, created_at, updated_at)
VALUES (?, ?, 'example', 'post-42', 'other-alias', ?, 1, 'orphaned', 0, 1, 1)`,
		conflictingBindingID.String(), source.ID, conflictingWorkID.String()); err != nil {
		t.Fatal(err)
	}
	_, err = resources.EnsureCanonical(ctx, source.ID, []application.DiscoveredWork{{
		SourceKey: "third-alias", ProviderID: "example", ExternalID: "post-42", Title: "冲突",
	}})
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeBindingReviewRequired {
		t.Fatalf("多候选未阻塞 publication: %v", err)
	}
	var openIssues int
	_ = store.Control.SQL().QueryRowContext(ctx, `SELECT count(*) FROM binding_issues
WHERE source_id=? AND status='open' AND code='BINDING_REVIEW_REQUIRED'`, source.ID).Scan(&openIssues)
	if openIssues != 1 {
		t.Fatalf("冲突 issue 未持久化: %d", openIssues)
	}
}
