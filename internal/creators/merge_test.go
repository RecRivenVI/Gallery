package creators_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/creators"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/overlay"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	galleryquery "github.com/RecRivenVI/gallery/internal/query"
	"github.com/RecRivenVI/gallery/internal/scanner"
	"github.com/RecRivenVI/gallery/internal/storage"
)

const mergeScope = "personal-owner"

type fixture struct {
	ctx       context.Context
	root      string
	dirs      appdirs.Dirs
	clock     clock.Fixed
	store     *storage.Store
	resources *application.Resources
	jobs      *jobs.Store
	catalog   *catalog.Store
	scanner   *scanner.Service
	overlay   *overlay.Service
	creators  *creators.Service
	query     *galleryquery.Service
	source1   application.Source
	source2   application.Source
}

func setupTwoSources(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	dirs := appdirs.UnderRoot(filepath.Join(root, "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	f := &fixture{ctx: ctx, root: root, dirs: dirs, store: store,
		clock: clock.Fixed{Time: time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC)}}
	f.buildServices(t, store)
	library, err := f.resources.CreateLibrary(ctx, "创作者合并库")
	if err != nil {
		t.Fatal(err)
	}
	f.source1 = f.registerSource(t, library.ID, "source-one", "source1", "work-alpha", "alpha-bytes", "作者甲")
	f.source2 = f.registerSource(t, library.ID, "source-two", "source2", "work-beta", "beta-bytes", "作者乙")
	return f
}

func (f *fixture) buildServices(t *testing.T, store *storage.Store) {
	t.Helper()
	generator := identity.NewGenerator(f.clock)
	var err error
	f.resources, err = application.NewResources(store.Control.SQL(), f.dirs, filesystem.OS{}, f.clock, generator)
	if err != nil {
		t.Fatal(err)
	}
	f.jobs, err = jobs.NewStore(store.Control.SQL(), f.clock, generator)
	if err != nil {
		t.Fatal(err)
	}
	f.catalog, err = catalog.NewStore(store.Catalog.SQL(), f.clock, generator)
	if err != nil {
		t.Fatal(err)
	}
	f.scanner, err = scanner.New(f.ctx, f.resources, f.jobs, f.catalog, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.overlay, err = overlay.New(f.ctx, store.Control.SQL(), f.jobs, f.catalog, f.clock, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.creators, err = creators.New(f.ctx, store.Control.SQL(), f.jobs, f.catalog, f.clock, generator, f.overlay)
	if err != nil {
		t.Fatal(err)
	}
	f.query, err = galleryquery.NewService(f.ctx, store.Control.SQL(), store.Catalog.SQL(), f.clock, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) registerSource(t *testing.T, libraryID, display, dir, work, media, creator string) application.Source {
	t.Helper()
	sourceRoot := filepath.Join(f.root, dir)
	workDir := filepath.Join(sourceRoot, work)
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "media.bin"), []byte(media), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "metadata.json"),
		[]byte(`{"creator":{"name":"`+creator+`"}}`), 0o400); err != nil {
		t.Fatal(err)
	}
	source, err := f.resources.CreateSource(f.ctx, libraryID, display, sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	rulePackage, err := os.ReadFile(filepath.Join("..", "rules", "testdata", "minimal-rule-package.json"))
	if err != nil {
		t.Fatal(err)
	}
	version, err := f.resources.CreateRuleVersion(f.ctx, rulePackage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.resources.CreateSourceRuleBinding(f.ctx, source.ID, version.SemanticHash, []byte("{}"), 0); err != nil {
		t.Fatal(err)
	}
	return source
}

func (f *fixture) scan(t *testing.T, sourceID string) {
	t.Helper()
	job, err := f.scanner.CreateScan(f.ctx, sourceID, mergeScope)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.scanner.Execute(f.ctx, job.ID); err != nil {
		t.Fatalf("扫描执行失败: %v", err)
	}
}

func (f *fixture) creatorByName(t *testing.T, name string) creators.Creator {
	t.Helper()
	list, err := f.creators.List(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, creator := range list {
		if creator.Name == name {
			return creator
		}
	}
	t.Fatalf("未找到创作者 %q（当前 %+v）", name, list)
	return creators.Creator{}
}

func (f *fixture) displayedCreator(t *testing.T, title string) string {
	t.Helper()
	result, err := f.query.Search(f.ctx, galleryquery.Request{Limit: 50, AuthorizationScope: mergeScope})
	if err != nil {
		t.Fatal(err)
	}
	for _, work := range result.Items {
		if work.Title == title {
			return work.Creator
		}
	}
	t.Fatalf("查询结果缺少作品 %q（当前 %+v）", title, result.Items)
	return ""
}

func (f *fixture) searchCount(t *testing.T, term string) int {
	t.Helper()
	result, err := f.query.Search(f.ctx, galleryquery.Request{Search: term, Limit: 50, AuthorizationScope: mergeScope})
	if err != nil {
		t.Fatal(err)
	}
	return len(result.Items)
}

func (f *fixture) mergeAndWait(t *testing.T, target string, absorbed ...string) creators.MergeResult {
	t.Helper()
	result, err := f.creators.Merge(f.ctx, mergeScope, target, absorbed)
	if err != nil {
		t.Fatalf("合并失败: %v", err)
	}
	f.overlay.Wait()
	f.requireCompleted(t, result.ProjectionJobID)
	return result
}

func (f *fixture) undoAndWait(t *testing.T, mergeID string) creators.MergeResult {
	t.Helper()
	result, err := f.creators.Undo(f.ctx, mergeScope, mergeID)
	if err != nil {
		t.Fatalf("撤销失败: %v", err)
	}
	f.overlay.Wait()
	f.requireCompleted(t, result.ProjectionJobID)
	return result
}

func (f *fixture) requireCompleted(t *testing.T, jobID string) {
	t.Helper()
	if jobID == "" {
		t.Fatal("未排队投影 Job")
	}
	job, err := f.jobs.Get(f.ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != jobs.StatusCompleted {
		t.Fatalf("投影 Job 未完成: status=%s issue=%s", job.Status, job.IssueCode)
	}
}

func (f *fixture) currentPublicationID(t *testing.T) string {
	t.Helper()
	publication, err := f.catalog.Current(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return publication.ID
}
