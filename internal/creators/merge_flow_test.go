package creators_test

import (
	"context"
	"errors"
	"testing"

	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/creators"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/overlay"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	galleryquery "github.com/RecRivenVI/gallery/internal/query"
	"github.com/RecRivenVI/gallery/internal/scanner"
	"github.com/RecRivenVI/gallery/internal/storage"
)

// TestMultiSourceCreatorMergeUndoAndReprojection 覆盖用户可观察的完整流程：两个 Source
// 发现被系统保持分离的疑似同一创作者，用户查看证据并合并，查询投影与搜索反映合并，
// 撤销后原创作者与投影可靠恢复。
func TestMultiSourceCreatorMergeUndoAndReprojection(t *testing.T) {
	f := setupTwoSources(t)
	f.scan(t, f.source1.ID)
	f.scan(t, f.source2.ID)

	// 两个 Source 各自发现一个创作者，跨 Source 不自动合并。
	alpha := f.creatorByName(t, "作者甲")
	beta := f.creatorByName(t, "作者乙")
	if alpha.MergedInto != "" || beta.MergedInto != "" || alpha.ID == beta.ID {
		t.Fatalf("跨 Source 创作者未保持分离: alpha=%+v beta=%+v", alpha, beta)
	}
	if _, evidence, err := f.creators.Get(f.ctx, alpha.ID); err != nil || len(evidence) != 1 || evidence[0].SourceID != f.source1.ID {
		t.Fatalf("创作者证据不足: %+v %v", evidence, err)
	}
	if f.displayedCreator(t, "work-alpha") != "作者甲" || f.displayedCreator(t, "work-beta") != "作者乙" {
		t.Fatal("合并前展示创作者错误")
	}
	beforeMerge := f.currentPublicationID(t)

	// 合并 作者乙 -> 作者甲，等待投影发布。
	merge := f.mergeAndWait(t, alpha.ID, beta.ID)
	if f.currentPublicationID(t) == beforeMerge {
		t.Fatal("合并未发布新 query publication")
	}
	mergedBeta := f.creatorByName(t, "作者乙")
	if mergedBeta.MergedInto != alpha.ID || mergedBeta.EffectiveID != alpha.ID {
		t.Fatalf("被合并者未指向 target: %+v", mergedBeta)
	}
	if f.displayedCreator(t, "work-alpha") != "作者甲" || f.displayedCreator(t, "work-beta") != "作者甲" {
		t.Fatalf("合并后两作品未统一到 target 创作者名")
	}
	if got := f.searchCount(t, "作者甲"); got != 2 {
		t.Fatalf("合并后按 target 搜索应命中两作品，实际 %d", got)
	}
	if got := f.searchCount(t, "作者乙"); got != 0 {
		t.Fatalf("合并后按被合并名仍可搜索: %d", got)
	}

	// work_creator_relations 与 creator_projections 保持 base 身份不变（当前不参与查询）。
	assertRelationCreator(t, f.store, "work-beta", beta.ID)
	if got := creatorProjectionCount(t, f.store); got != 2 {
		t.Fatalf("creator_projections 应保持 base 两条，实际 %d", got)
	}

	// 撤销合并，原创作者与投影恢复。
	f.undoAndWait(t, merge.Merge.ID)
	if restored := f.creatorByName(t, "作者乙"); restored.MergedInto != "" || restored.EffectiveID != restored.ID {
		t.Fatalf("撤销后被合并者未恢复 live: %+v", restored)
	}
	if f.displayedCreator(t, "work-alpha") != "作者甲" || f.displayedCreator(t, "work-beta") != "作者乙" {
		t.Fatal("撤销后未恢复各自创作者名")
	}
	if got := f.searchCount(t, "作者乙"); got != 1 {
		t.Fatalf("撤销后按原名应命中一作品，实际 %d", got)
	}
	if got := f.searchCount(t, "作者甲"); got != 1 {
		t.Fatalf("撤销后 target 只应命中自身作品，实际 %d", got)
	}
}

// TestCreatorMergeSurvivesRescanAndRestart 证明合并在被合并者所属 Source 重扫后仍生效
// （扫描路径应用合并映射），并在进程重启且对账后保持一致。
func TestCreatorMergeSurvivesRescanAndRestart(t *testing.T) {
	f := setupTwoSources(t)
	f.scan(t, f.source1.ID)
	f.scan(t, f.source2.ID)
	alpha := f.creatorByName(t, "作者甲")
	beta := f.creatorByName(t, "作者乙")
	f.mergeAndWait(t, alpha.ID, beta.ID)

	// 重扫 作者乙 所属的 Source：其自身发现的创作者是被合并者，扫描路径须应用合并。
	f.scan(t, f.source2.ID)
	if f.displayedCreator(t, "work-beta") != "作者甲" {
		t.Fatal("重扫后被合并 Source 的作品未保持合并展示")
	}
	if got := f.searchCount(t, "作者乙"); got != 0 {
		t.Fatalf("重扫后被合并名仍可搜索: %d", got)
	}
	mergedPublication := f.currentPublicationID(t)

	// 重启：关闭并重开数据库，重建服务并对账，结果保持一致。
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(f.ctx, f.dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	generator := identity.NewGenerator(f.clock)
	resources, _ := application.NewResources(reopened.Control.SQL(), f.dirs, filesystem.OS{}, f.clock, generator)
	jobStore, _ := jobs.NewStore(reopened.Control.SQL(), f.clock, generator)
	catalogStore, _ := catalog.NewStore(reopened.Catalog.SQL(), f.clock, generator)
	scan, _ := scanner.New(f.ctx, resources, jobStore, catalogStore, nil)
	overlayService, _ := overlay.New(f.ctx, reopened.Control.SQL(), jobStore, catalogStore, f.clock, nil)
	if err := scan.Reconcile(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := overlayService.Reconcile(f.ctx); err != nil {
		t.Fatal(err)
	}
	scan.Wait()
	overlayService.Wait()

	current, err := catalogStore.Current(f.ctx)
	if err != nil || current.ID != mergedPublication {
		t.Fatalf("重启对账改变了已发布 publication: %+v %v", current, err)
	}
	restartedCreators, err := creators.New(f.ctx, reopened.Control.SQL(), jobStore, catalogStore, f.clock, generator, overlayService)
	if err != nil {
		t.Fatal(err)
	}
	if merged := creatorByNameIn(t, restartedCreators, f.ctx, "作者乙"); merged.MergedInto != alpha.ID {
		t.Fatalf("重启后合并事实丢失: %+v", merged)
	}
	queryService, _ := galleryquery.NewService(f.ctx, reopened.Control.SQL(), reopened.Catalog.SQL(), f.clock, nil)
	result, err := queryService.Search(f.ctx, galleryquery.Request{Search: "作者甲", Limit: 50, AuthorizationScope: mergeScope})
	if err != nil || len(result.Items) != 2 {
		t.Fatalf("重启后合并查询结果漂移: %+v %v", result.Items, err)
	}
}

// TestCreatorMergeValidationAndConflicts 覆盖合并与撤销的结构化错误语义。
func TestCreatorMergeValidationAndConflicts(t *testing.T) {
	f := setupTwoSources(t)
	f.scan(t, f.source1.ID)
	f.scan(t, f.source2.ID)
	alpha := f.creatorByName(t, "作者甲")
	beta := f.creatorByName(t, "作者乙")

	if _, err := f.creators.Merge(f.ctx, mergeScope, alpha.ID, []string{alpha.ID}); !hasCode(err, fault.CodeValidation) {
		t.Fatalf("自合并未拒绝: %v", err)
	}
	if _, err := f.creators.Merge(f.ctx, mergeScope, alpha.ID, nil); !hasCode(err, fault.CodeValidation) {
		t.Fatalf("空成员未拒绝: %v", err)
	}
	missing := newCreatorID(t)
	if _, err := f.creators.Merge(f.ctx, mergeScope, alpha.ID, []string{missing}); !hasCode(err, fault.CodeNotFound) {
		t.Fatalf("不存在成员未按 NotFound 拒绝: %v", err)
	}

	merge := f.mergeAndWait(t, alpha.ID, beta.ID)
	// 被合并者已非 live，再次并入任意 target 应冲突。
	if _, err := f.creators.Merge(f.ctx, mergeScope, alpha.ID, []string{beta.ID}); !hasCode(err, fault.CodeConflict) {
		t.Fatalf("重复合并未冲突: %v", err)
	}
	// 已合并者作为 target 也应冲突。
	if _, err := f.creators.Merge(f.ctx, mergeScope, beta.ID, []string{alpha.ID}); !hasCode(err, fault.CodeConflict) {
		t.Fatalf("已合并者作为 target 未冲突: %v", err)
	}
	f.undoAndWait(t, merge.Merge.ID)
	if _, err := f.creators.Undo(f.ctx, mergeScope, merge.Merge.ID); !hasCode(err, fault.CodeConflict) {
		t.Fatalf("重复撤销未冲突: %v", err)
	}
}

func creatorByNameIn(t *testing.T, service *creators.Service, ctx context.Context, name string) creators.Creator {
	t.Helper()
	list, err := service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, creator := range list {
		if creator.Name == name {
			return creator
		}
	}
	t.Fatalf("未找到创作者 %q", name)
	return creators.Creator{}
}

func assertRelationCreator(t *testing.T, store *storage.Store, sourceKey, creatorID string) {
	t.Helper()
	var got string
	err := store.Catalog.SQL().QueryRowContext(context.Background(), `SELECT r.creator_id
FROM work_creator_relations r
JOIN active_query_publication a ON a.singleton=1
JOIN query_publications q ON q.query_publication_id=a.query_publication_id
JOIN work_projections w ON w.catalog_revision_id=r.catalog_revision_id
 AND w.overlay_revision_id=r.overlay_revision_id AND w.work_id=r.work_id
WHERE r.catalog_revision_id=q.catalog_revision_id AND r.overlay_revision_id=q.overlay_revision_id
 AND w.source_key=?`, sourceKey).Scan(&got)
	if err != nil {
		t.Fatalf("读取 work_creator_relations 失败: %v", err)
	}
	if got != creatorID {
		t.Fatalf("work_creator_relations 应保持 base 创作者 %s，实际 %s", creatorID, got)
	}
}

func creatorProjectionCount(t *testing.T, store *storage.Store) int {
	t.Helper()
	var count int
	if err := store.Catalog.SQL().QueryRowContext(context.Background(), `SELECT count(*)
FROM creator_projections c
JOIN active_query_publication a ON a.singleton=1
JOIN query_publications q ON q.query_publication_id=a.query_publication_id
WHERE c.catalog_revision_id=q.catalog_revision_id AND c.overlay_revision_id=q.overlay_revision_id`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func newCreatorID(t *testing.T) string {
	t.Helper()
	id, err := identity.NewGenerator(clock.Fixed{Time: time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC)}).New(domain.IDCanonicalCreator)
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}

func hasCode(err error, code fault.Code) bool {
	var structured *fault.Error
	return errors.As(err, &structured) && structured.Code == code
}
