package scanner_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/hashjob"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/scanner"
	"github.com/RecRivenVI/gallery/internal/storage"
)

// rebuildFixture 持有可关闭、可删除 catalog.db 并重开 control 的完整 Scanner 运行环境，
// 用于验证 Catalog 删除重建边界的默认 scanProfile 选择。所有断言都经过真实 Scanner
// 端到端路径（CreateScanWithProfile + Execute），不直接调用 EnsureCanonical 伪装扫描覆盖。
type rebuildFixture struct {
	dirs       appdirs.Dirs
	fixedClock clock.Fixed
	sourceRoot string
	store      *storage.Store
	resources  *application.Resources
	jobs       *jobs.Store
	catalog    *catalog.Store
	scanner    *scanner.Service
	source     application.Source
}

func newRebuildFixture(t *testing.T) *rebuildFixture {
	t.Helper()
	root := t.TempDir()
	dirs := appdirs.UnderRoot(filepath.Join(root, "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	f := &rebuildFixture{
		dirs:       dirs,
		fixedClock: clock.Fixed{Time: time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)},
		sourceRoot: filepath.Join(root, "source"),
	}
	t.Cleanup(func() { _ = f.store.Close() })
	f.open(t)
	library, err := f.resources.CreateLibrary(context.Background(), "rebuild")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.sourceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	source, err := f.resources.CreateSource(context.Background(), library.ID, "rebuild-source", f.sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	f.source = source
	rulePackage, err := os.ReadFile(filepath.Join("..", "rules", "testdata", "minimal-rule-package.json"))
	if err != nil {
		t.Fatal(err)
	}
	version, err := f.resources.CreateRuleVersion(context.Background(), rulePackage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.resources.CreateSourceRuleBinding(context.Background(), source.ID, version.SemanticHash, []byte("{}"), 0); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *rebuildFixture) open(t *testing.T) {
	t.Helper()
	store, err := storage.Open(context.Background(), f.dirs)
	if err != nil {
		t.Fatal(err)
	}
	generator := identity.NewGenerator(f.fixedClock)
	resources, err := application.NewResources(store.Control.SQL(), f.dirs, filesystem.OS{}, f.fixedClock, generator)
	if err != nil {
		t.Fatal(err)
	}
	jobStore, err := jobs.NewStore(store.Control.SQL(), f.fixedClock, generator)
	if err != nil {
		t.Fatal(err)
	}
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), f.fixedClock, generator)
	if err != nil {
		t.Fatal(err)
	}
	scannerService, err := scanner.New(context.Background(), resources, jobStore, catalogStore, nil)
	if err != nil {
		t.Fatal(err)
	}
	hashService, err := hashjob.New(context.Background(), resources, jobStore)
	if err != nil {
		t.Fatal(err)
	}
	scannerService.SetHashService(hashService)
	f.store, f.resources, f.jobs, f.catalog, f.scanner = store, resources, jobStore, catalogStore, scannerService
}

// rebuildCatalog 关闭 store，只删除 catalog.db 及其 WAL/SHM，重开 control 并重建全部依赖
// catalog.db 的服务，模拟"Catalog 删除重建但 control 历史保留"。
func (f *rebuildFixture) rebuildCatalog(t *testing.T) {
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
	f.open(t)
	source, err := f.resources.GetSource(context.Background(), f.source.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.source = source
}

func (f *rebuildFixture) writeWork(t *testing.T, workDir string, media map[string][]byte) {
	t.Helper()
	dir := filepath.Join(f.sourceRoot, workDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range media {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o400); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(`{"creator":{"name":"Rebuild Creator"}}`), 0o400); err != nil {
		t.Fatal(err)
	}
}

func (f *rebuildFixture) removeWork(t *testing.T, workDir string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(f.sourceRoot, workDir)); err != nil {
		t.Fatal(err)
	}
}

func (f *rebuildFixture) persistedScanProfile(t *testing.T, job jobs.Job) string {
	t.Helper()
	var request struct {
		ScanProfile string `json:"scanProfile,omitempty"`
	}
	if err := json.Unmarshal(job.RequestJSON, &request); err != nil {
		t.Fatal(err)
	}
	return request.ScanProfile
}

func (f *rebuildFixture) scanJobCount(t *testing.T) int {
	t.Helper()
	all, err := f.jobs.ListByStatuses(context.Background(), jobs.StatusQueued, jobs.StatusRunning,
		jobs.StatusPublishing, jobs.StatusCompleted, jobs.StatusFailed, jobs.StatusCancelled, jobs.StatusNeedsRepair)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, job := range all {
		if job.Type == "scan" {
			count++
		}
	}
	return count
}

func (f *rebuildFixture) activeWorkID(t *testing.T, sourceKey string) string {
	t.Helper()
	var workID string
	err := f.store.Control.SQL().QueryRowContext(context.Background(), `SELECT work_id FROM work_bindings
WHERE source_id=? AND source_key=? AND status='active'`, f.source.ID, sourceKey).Scan(&workID)
	if err != nil {
		return ""
	}
	return workID
}

func (f *rebuildFixture) canonicalWorkCount(t *testing.T) int {
	t.Helper()
	var count int
	if err := f.store.Control.SQL().QueryRowContext(context.Background(), "SELECT count(*) FROM canonical_works").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// TestScanProfileDefaultsToIndexForBrandNewSource 覆盖全新 Source：无 publication、无
// Binding/决策历史时，未显式指定档案默认选择 index，显式 index 也可用。
func TestScanProfileDefaultsToIndexForBrandNewSource(t *testing.T) {
	f := newRebuildFixture(t)
	f.writeWork(t, "work-one", map[string][]byte{"media.bin": []byte("brand new source content")})

	job, err := f.scanner.CreateScanWithProfile(context.Background(), f.source.ID, "owner", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if profile := f.persistedScanProfile(t, job); profile != scanner.ScanProfileIndex {
		t.Fatalf("全新 Source 默认应选择 index，实际=%q", profile)
	}
	if err := f.scanner.Execute(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}

	// 显式 index 只在"无 publication 且无持久领域历史"时可用，因此用另一个从未扫描过的
	// 全新 Source 验证，而不是复用上面已经建立了 Binding 历史的同一个 Source。
	other := newRebuildFixture(t)
	other.writeWork(t, "work-one", map[string][]byte{"media.bin": []byte("another brand new source")})
	explicit, err := other.scanner.CreateScanWithProfile(context.Background(), other.source.ID, "owner", "", scanner.ScanProfileIndex)
	if err != nil {
		t.Fatalf("全新 Source 显式 index 应可用: %v", err)
	}
	if profile := other.persistedScanProfile(t, explicit); profile != scanner.ScanProfileIndex {
		t.Fatalf("显式 index 请求持久化档案错误: %q", profile)
	}
	if err := other.scanner.Execute(context.Background(), explicit.ID); err != nil {
		t.Fatal(err)
	}
}

// TestScanProfileDefaultsToIncrementalAfterCatalogRebuildWithDurableHistory 是 5.3 的核心
// 用例：Source 完成过一次带完整 digest 的扫描 → 记录 Binding/publication → 关闭数据库 →
// 只删除 catalog.db/WAL/SHM、保留 control.db → 重新打开 → 未指定 scanProfile 创建扫描 →
// 断言最终持久化为 incremental、会重新建立必要 Hash Job、Canonical ID 不漂移。
func TestScanProfileDefaultsToIncrementalAfterCatalogRebuildWithDurableHistory(t *testing.T) {
	f := newRebuildFixture(t)
	f.writeWork(t, "work-one", map[string][]byte{"media.bin": []byte("durable history survives catalog rebuild")})

	first, err := f.scanner.CreateScanWithProfile(context.Background(), f.source.ID, "owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.scanner.Execute(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	_, works, err := f.catalog.ListWorks(context.Background())
	if err != nil || len(works) != 1 {
		t.Fatalf("首次扫描未发布 Work: %+v %v", works, err)
	}
	_, mediaBefore, err := f.catalog.ListMediaForWork(context.Background(), works[0].ID)
	if err != nil || len(mediaBefore) != 1 || mediaBefore[0].Digest == "" {
		t.Fatalf("首次扫描未建立完整 digest: %+v %v", mediaBefore, err)
	}
	originWorkID := f.activeWorkID(t, "work-one")
	if originWorkID == "" {
		t.Fatal("首次扫描未建立 active Binding")
	}
	var hashJobsBefore int
	if err := f.store.Control.SQL().QueryRowContext(context.Background(), "SELECT count(*) FROM jobs WHERE job_type='hash'").Scan(&hashJobsBefore); err != nil {
		t.Fatal(err)
	}

	f.rebuildCatalog(t)

	second, err := f.scanner.CreateScanWithProfile(context.Background(), f.source.ID, "owner", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if profile := f.persistedScanProfile(t, second); profile != scanner.ScanProfileIncremental {
		t.Fatalf("Catalog 重建但 control 历史保留时默认应选择 incremental，实际=%q", profile)
	}
	if err := f.scanner.Execute(context.Background(), second.ID); err != nil {
		t.Fatal(err)
	}
	_, worksAfter, err := f.catalog.ListWorks(context.Background())
	if err != nil || len(worksAfter) != 1 || worksAfter[0].ID != originWorkID {
		t.Fatalf("重建后 Canonical Work ID 漂移: before=%s after=%+v err=%v", originWorkID, worksAfter, err)
	}
	_, mediaAfter, err := f.catalog.ListMediaForWork(context.Background(), worksAfter[0].ID)
	if err != nil || len(mediaAfter) != 1 || mediaAfter[0].Digest == "" ||
		mediaAfter[0].ContentVerificationState != catalog.ContentVerificationStateContentVerified {
		t.Fatalf("重建后默认 incremental 未重新建立完整确认: %+v %v", mediaAfter, err)
	}
	var hashJobsAfter int
	if err := f.store.Control.SQL().QueryRowContext(context.Background(), "SELECT count(*) FROM jobs WHERE job_type='hash'").Scan(&hashJobsAfter); err != nil {
		t.Fatal(err)
	}
	if hashJobsAfter <= hashJobsBefore {
		t.Fatalf("重建后应为丢失的 catalog 观察重新建立 Hash Job: before=%d after=%d", hashJobsBefore, hashJobsAfter)
	}
}

// TestExplicitIndexRejectedAfterCatalogRebuildWithDurableHistory 覆盖"Catalog 已丢失，但
// control.db 仍有持久领域历史"时显式 index 必须返回结构化 CONFLICT，不创建 Job、不修改
// Binding/Catalog。
func TestExplicitIndexRejectedAfterCatalogRebuildWithDurableHistory(t *testing.T) {
	f := newRebuildFixture(t)
	f.writeWork(t, "work-one", map[string][]byte{"media.bin": []byte("explicit index after rebuild must conflict")})

	first, err := f.scanner.CreateScanWithProfile(context.Background(), f.source.ID, "owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.scanner.Execute(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	originWorkID := f.activeWorkID(t, "work-one")
	if originWorkID == "" {
		t.Fatal("首次扫描未建立 active Binding")
	}

	f.rebuildCatalog(t)
	jobCountBefore := f.scanJobCount(t)

	_, err = f.scanner.CreateScanWithProfile(context.Background(), f.source.ID, "owner", "", scanner.ScanProfileIndex)
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeConflict {
		t.Fatalf("Catalog 重建但仍有持久历史时显式 index 应返回结构化 CONFLICT: %v", err)
	}
	if got := f.scanJobCount(t); got != jobCountBefore {
		t.Fatalf("被拒绝的显式 index 请求不应创建 Job: before=%d after=%d", jobCountBefore, got)
	}
	if got := f.activeWorkID(t, "work-one"); got != originWorkID {
		t.Fatalf("拒绝后 Binding 不应变化: before=%s after=%s", originWorkID, got)
	}
	if _, err := f.catalog.Current(context.Background()); err == nil {
		t.Fatal("拒绝后不应存在被错误发布的 Catalog publication")
	}
}

// TestScanProfileTriggersSplitReviewAfterCatalogRebuild 覆盖"Catalog 重建期间发生 SourceWork
// 拆分"：首次已验证扫描后保留 control，删除并重建 Catalog，修改合成 Source 形成明确拆分，
// 默认扫描必须选择 incremental、产生完整 digest 证据、触发既有 SOURCE_WORK_SPLIT_REVIEW_
// REQUIRED，不发布绕过审查的新 Catalog，不重复创建 Canonical 实体；既有人工决策仍可按既有
// 恢复机制应用。
func TestScanProfileTriggersSplitReviewAfterCatalogRebuild(t *testing.T) {
	f := newRebuildFixture(t)
	contentA := []byte("split fixture media one")
	contentB := []byte("split fixture media two")
	f.writeWork(t, "work-combined", map[string][]byte{"m1.bin": contentA, "m2.bin": contentB})

	first, err := f.scanner.CreateScanWithProfile(context.Background(), f.source.ID, "owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.scanner.Execute(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	originWorkID := f.activeWorkID(t, "work-combined")
	if originWorkID == "" {
		t.Fatal("首次扫描未建立 active Binding")
	}
	canonicalBefore := f.canonicalWorkCount(t)

	f.rebuildCatalog(t)

	// 修改合成 Source：把原来一个 work 目录拆成两个新 work 目录，媒体字节内容保持不变，
	// 使拆分检测能够通过 ContentBlob digest 证据找到与原 CanonicalWork 的关联。
	f.removeWork(t, "work-combined")
	f.writeWork(t, "work-combined-a", map[string][]byte{"m1.bin": contentA})
	f.writeWork(t, "work-combined-b", map[string][]byte{"m2.bin": contentB})

	second, err := f.scanner.CreateScanWithProfile(context.Background(), f.source.ID, "owner", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if profile := f.persistedScanProfile(t, second); profile != scanner.ScanProfileIncremental {
		t.Fatalf("拆分场景 Catalog 重建后默认应选择 incremental，实际=%q", profile)
	}
	err = f.scanner.Execute(context.Background(), second.ID)
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeBindingReviewRequired {
		t.Fatalf("拆分应触发 BINDING_REVIEW_REQUIRED: %v", err)
	}
	failedJob, err := f.jobs.Get(context.Background(), second.ID)
	if err != nil || failedJob.Status != jobs.StatusFailed || failedJob.IssueCode != string(fault.CodeBindingReviewRequired) {
		t.Fatalf("拆分审查 Job 状态错误: %+v %v", failedJob, err)
	}

	// 未发布绕过审查的新 Catalog。
	if _, err := f.catalog.Current(context.Background()); err == nil {
		t.Fatal("拆分审查未决时不应存在新发布的 Catalog publication")
	}
	// 无重复 Canonical 实体。
	if got := f.canonicalWorkCount(t); got != canonicalBefore {
		t.Fatalf("拆分审查未决时不应新增 CanonicalWork: before=%d after=%d", canonicalBefore, got)
	}

	// 既有人工决策仍可按既有恢复机制应用：解决 issue 后重新扫描应成功。
	page, err := f.resources.ListBindingIssues(context.Background(), application.BindingIssueFilter{SourceID: f.source.ID, Status: "open"}, "", 50)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("期望恰好一个 open 拆分 issue: %+v %v", page.Items, err)
	}
	if _, err := f.resources.ResolveSourceStructureIssue(context.Background(), page.Items[0].ID, "owner", "split_inherit", "work-combined-a", "", 1); err != nil {
		t.Fatalf("拆分决策失败: %v", err)
	}
	third, err := f.scanner.CreateScanWithProfile(context.Background(), f.source.ID, "owner", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.scanner.Execute(context.Background(), third.ID); err != nil {
		t.Fatalf("拆分决策应用后重扫应成功: %v", err)
	}
	if got := f.activeWorkID(t, "work-combined-a"); got != originWorkID {
		t.Fatalf("拆分继承目标未复用原 CanonicalWork: got=%s want=%s", got, originWorkID)
	}
	if got := f.canonicalWorkCount(t); got != canonicalBefore+1 {
		t.Fatalf("拆分应用后应恰好新增一个 CanonicalWork: before=%d after=%d", canonicalBefore, got)
	}
}
