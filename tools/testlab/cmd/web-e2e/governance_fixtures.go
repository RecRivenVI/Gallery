package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/storage"
)

const (
	governanceIssueSourceName      = "治理 E2E · 绑定问题"
	governanceLifecycleSourceName  = "治理 E2E · 问题生命周期"
	governancePaginationSourceName = "治理 E2E · 问题分页"
	governanceStructureSourceName  = "治理 E2E · 结构决策"
	governanceMergeSourceName      = "治理 E2E · 合并决策"
	governanceConsumedSourceName   = "治理 E2E · 已消费决策"
	governanceOrphanSourceName     = "治理 E2E · 孤儿候选"
	governanceMediaSourceName      = "治理 E2E · 媒体解绑"
)

type governanceFixtureState struct {
	IssueSourceID              string `json:"issueSourceId"`
	IssueSourceName            string `json:"issueSourceName"`
	IssueID                    string `json:"issueId"`
	IssueSourceKey             string `json:"issueSourceKey"`
	IssueBindID                string `json:"issueBindId"`
	IssueBindSourceKey         string `json:"issueBindSourceKey"`
	IssueBindTargetID          string `json:"issueBindTargetId"`
	IssueSeparateID            string `json:"issueSeparateId"`
	IssueSeparateSourceKey     string `json:"issueSeparateSourceKey"`
	LifecycleSourceID          string `json:"lifecycleSourceId"`
	LifecycleSourceName        string `json:"lifecycleSourceName"`
	LifecycleSourceKey         string `json:"lifecycleSourceKey"`
	LifecycleSupersededID      string `json:"lifecycleSupersededId"`
	LifecycleSupersededVersion int    `json:"lifecycleSupersededVersion"`
	LifecycleStaleID           string `json:"lifecycleStaleId"`
	LifecycleStaleVersion      int    `json:"lifecycleStaleVersion"`
	PaginationSourceID         string `json:"paginationSourceId"`
	PaginationSourceName       string `json:"paginationSourceName"`
	PaginationIssueCount       int    `json:"paginationIssueCount"`
	StructureSourceID          string `json:"structureSourceId"`
	StructureSourceName        string `json:"structureSourceName"`
	StructureIssueID           string `json:"structureIssueId"`
	StructureTargetSourceKey   string `json:"structureTargetSourceKey"`
	MergeSourceID              string `json:"mergeSourceId"`
	MergeSourceName            string `json:"mergeSourceName"`
	MergeIssueID               string `json:"mergeIssueId"`
	MergeTargetWorkID          string `json:"mergeTargetWorkId"`
	ConsumedDecisionID         string `json:"consumedDecisionId"`
	ConsumedDecisionIssueID    string `json:"consumedDecisionIssueId"`
	ConsumedDecisionVersion    int    `json:"consumedDecisionVersion"`
	OrphanSourceID             string `json:"orphanSourceId"`
	OrphanSourceName           string `json:"orphanSourceName"`
	OrphanBindingID            string `json:"orphanBindingId"`
	OrphanSourceKey            string `json:"orphanSourceKey"`
	OrphanUnbindBindingID      string `json:"orphanUnbindBindingId"`
	OrphanUnbindSourceKey      string `json:"orphanUnbindSourceKey"`
	OrphanCreatorBindingID     string `json:"orphanCreatorBindingId"`
	OrphanCreatorSourceKey     string `json:"orphanCreatorSourceKey"`
	OrphanMediaBindingID       string `json:"orphanMediaBindingId"`
	OrphanMediaSourceKey       string `json:"orphanMediaSourceKey"`
	MediaSourceID              string `json:"mediaSourceId"`
	MediaSourceName            string `json:"mediaSourceName"`
	MediaSourceKey             string `json:"mediaSourceKey"`
}

// seedGovernanceFixtures 只通过正式 application.Resources 建立治理事实。调用方必须先停止
// galleryd，避免绕过 AppDirs 单写者边界；函数不直接写数据库，也不读写任何真实 Source。
func seedGovernanceFixtures(ctx context.Context, appRoot, sourceRoot string) (fixtures governanceFixtureState, err error) {
	dirs := appdirs.UnderRoot(appRoot)
	fileSystem := filesystem.OS{}
	if err := dirs.Ensure(fileSystem); err != nil {
		return governanceFixtureState{}, err
	}
	sourceRoots := map[string]string{
		"issue":      filepath.Join(sourceRoot, "binding-issue"),
		"lifecycle":  filepath.Join(sourceRoot, "binding-lifecycle"),
		"pagination": filepath.Join(sourceRoot, "binding-pagination"),
		"structure":  filepath.Join(sourceRoot, "structure"),
		"merge":      filepath.Join(sourceRoot, "structure-merge"),
		"consumed":   filepath.Join(sourceRoot, "structure-consumed"),
		"orphan":     filepath.Join(sourceRoot, "orphan"),
		"media":      filepath.Join(sourceRoot, "media"),
	}
	for _, root := range sourceRoots {
		if err := os.MkdirAll(root, 0o700); err != nil {
			return governanceFixtureState{}, err
		}
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		return governanceFixtureState{}, err
	}
	defer func() {
		err = errors.Join(err, store.Close())
	}()
	systemClock := clock.System{}
	resources, err := application.NewResources(
		store.Control.SQL(), dirs, fileSystem, systemClock, identity.NewGenerator(systemClock),
	)
	if err != nil {
		return governanceFixtureState{}, err
	}
	library, err := resources.CreateLibrary(ctx, "治理 E2E 合成夹具")
	if err != nil {
		return governanceFixtureState{}, err
	}
	issueSource, err := resources.CreateSource(ctx, library.ID, governanceIssueSourceName, sourceRoots["issue"])
	if err != nil {
		return governanceFixtureState{}, err
	}
	lifecycleSource, err := resources.CreateSource(ctx, library.ID, governanceLifecycleSourceName, sourceRoots["lifecycle"])
	if err != nil {
		return governanceFixtureState{}, err
	}
	paginationSource, err := resources.CreateSource(ctx, library.ID, governancePaginationSourceName, sourceRoots["pagination"])
	if err != nil {
		return governanceFixtureState{}, err
	}
	structureSource, err := resources.CreateSource(ctx, library.ID, governanceStructureSourceName, sourceRoots["structure"])
	if err != nil {
		return governanceFixtureState{}, err
	}
	mergeSource, err := resources.CreateSource(ctx, library.ID, governanceMergeSourceName, sourceRoots["merge"])
	if err != nil {
		return governanceFixtureState{}, err
	}
	consumedSource, err := resources.CreateSource(ctx, library.ID, governanceConsumedSourceName, sourceRoots["consumed"])
	if err != nil {
		return governanceFixtureState{}, err
	}
	orphanSource, err := resources.CreateSource(ctx, library.ID, governanceOrphanSourceName, sourceRoots["orphan"])
	if err != nil {
		return governanceFixtureState{}, err
	}
	mediaSource, err := resources.CreateSource(ctx, library.ID, governanceMediaSourceName, sourceRoots["media"])
	if err != nil {
		return governanceFixtureState{}, err
	}

	bindCandidates, err := prepareWorkConflictCandidates(ctx, resources, issueSource.ID, "bind", "bind-external")
	if err != nil {
		return governanceFixtureState{}, fmt.Errorf("建立 bind_existing 候选: %w", err)
	}
	if _, err := prepareWorkConflictCandidates(ctx, resources, issueSource.ID, "separate", "separate-external"); err != nil {
		return governanceFixtureState{}, fmt.Errorf("建立 keep_separate 候选: %w", err)
	}

	const issueSourceKey = "duplicate-work"
	_, issueErr := resources.EnsureCanonical(ctx, issueSource.ID, []application.DiscoveredWork{
		{SourceKey: issueSourceKey, Title: "重复作品甲"},
		{SourceKey: issueSourceKey, Title: "重复作品乙"},
	})
	if err := requireBindingReview(issueErr); err != nil {
		return governanceFixtureState{}, fmt.Errorf("建立绑定问题夹具: %w", err)
	}
	issue, err := bindingIssueByKey(ctx, resources, issueSource.ID, issueSourceKey, "open")
	if err != nil {
		return governanceFixtureState{}, err
	}
	const issueBindSourceKey = "bind-new"
	_, issueBindErr := resources.EnsureCanonical(ctx, issueSource.ID, []application.DiscoveredWork{{
		SourceKey: issueBindSourceKey, ProviderID: "e2e", ExternalID: "bind-external", Title: "绑定到已有作品",
	}})
	if err := requireBindingReview(issueBindErr); err != nil {
		return governanceFixtureState{}, fmt.Errorf("建立 bind_existing 问题: %w", err)
	}
	issueBind, err := bindingIssueByKey(ctx, resources, issueSource.ID, issueBindSourceKey, "open")
	if err != nil {
		return governanceFixtureState{}, err
	}
	if len(issueBind.Candidates) != 2 || !containsFixtureCandidate(issueBind.Candidates, bindCandidates[0]) {
		return governanceFixtureState{}, fmt.Errorf("bind_existing 问题候选不完整: %+v", issueBind.Candidates)
	}
	const issueSeparateSourceKey = "separate-new"
	_, issueSeparateErr := resources.EnsureCanonical(ctx, issueSource.ID, []application.DiscoveredWork{{
		SourceKey: issueSeparateSourceKey, ProviderID: "e2e", ExternalID: "separate-external", Title: "保持独立作品",
	}})
	if err := requireBindingReview(issueSeparateErr); err != nil {
		return governanceFixtureState{}, fmt.Errorf("建立 keep_separate 问题: %w", err)
	}
	issueSeparate, err := bindingIssueByKey(ctx, resources, issueSource.ID, issueSeparateSourceKey, "open")
	if err != nil {
		return governanceFixtureState{}, err
	}

	if _, err := prepareWorkConflictCandidates(ctx, resources, lifecycleSource.ID, "lifecycle", "lifecycle-external"); err != nil {
		return governanceFixtureState{}, fmt.Errorf("建立生命周期候选: %w", err)
	}
	const lifecycleCandidateKey = "lifecycle-c"
	if _, err := resources.EnsureCanonical(ctx, lifecycleSource.ID, []application.DiscoveredWork{{
		SourceKey: lifecycleCandidateKey, Title: "生命周期候选丙",
	}}); err != nil {
		return governanceFixtureState{}, fmt.Errorf("建立生命周期第三候选: %w", err)
	}
	const lifecycleSourceKey = "lifecycle-new"
	_, lifecycleFirstErr := resources.EnsureCanonical(ctx, lifecycleSource.ID, []application.DiscoveredWork{{
		SourceKey: lifecycleSourceKey, ProviderID: "e2e", ExternalID: "lifecycle-external", Title: "生命周期冲突",
	}})
	if err := requireBindingReview(lifecycleFirstErr); err != nil {
		return governanceFixtureState{}, fmt.Errorf("建立生命周期首次问题: %w", err)
	}
	lifecycleSuperseded, err := bindingIssueByKey(ctx, resources, lifecycleSource.ID, lifecycleSourceKey, "open")
	if err != nil {
		return governanceFixtureState{}, err
	}
	_, lifecycleSecondErr := resources.EnsureCanonical(ctx, lifecycleSource.ID, []application.DiscoveredWork{{
		SourceKey: lifecycleCandidateKey, ProviderID: "e2e", ExternalID: "lifecycle-external", Title: "生命周期候选丙",
	}, {
		SourceKey: lifecycleSourceKey, ProviderID: "e2e", ExternalID: "lifecycle-external", Title: "生命周期冲突",
	}})
	if err := requireBindingReview(lifecycleSecondErr); err != nil {
		return governanceFixtureState{}, fmt.Errorf("建立生命周期证据变化: %w", err)
	}
	lifecycleStale, err := bindingIssueByKey(ctx, resources, lifecycleSource.ID, lifecycleSourceKey, "open")
	if err != nil {
		return governanceFixtureState{}, err
	}
	if lifecycleStale.ID == lifecycleSuperseded.ID {
		return governanceFixtureState{}, fmt.Errorf("生命周期证据变化未产生新 issue")
	}
	if _, err := resources.EnsureCanonical(ctx, lifecycleSource.ID, []application.DiscoveredWork{{
		SourceKey: "lifecycle-stable", Title: "生命周期稳定作品",
	}}); err != nil {
		return governanceFixtureState{}, fmt.Errorf("收敛生命周期问题: %w", err)
	}
	lifecycleSuperseded, err = resources.GetBindingIssue(ctx, lifecycleSuperseded.ID)
	if err != nil {
		return governanceFixtureState{}, err
	}
	if lifecycleSuperseded.Status != "superseded" {
		return governanceFixtureState{}, fmt.Errorf("生命周期旧 issue 未 superseded: %+v", lifecycleSuperseded)
	}
	lifecycleStale, err = resources.GetBindingIssue(ctx, lifecycleStale.ID)
	if err != nil {
		return governanceFixtureState{}, err
	}
	if lifecycleStale.Status != "stale" {
		return governanceFixtureState{}, fmt.Errorf("生命周期新 issue 未 stale: %+v", lifecycleStale)
	}

	const paginationIssueCount = 51
	for index := range paginationIssueCount {
		sourceKey := fmt.Sprintf("page-%02d", index)
		_, issueErr := resources.EnsureCanonical(ctx, paginationSource.ID, []application.DiscoveredWork{
			{SourceKey: sourceKey, Title: fmt.Sprintf("分页作品 %02d 甲", index)},
			{SourceKey: sourceKey, Title: fmt.Sprintf("分页作品 %02d 乙", index)},
		})
		if err := requireBindingReview(issueErr); err != nil {
			return governanceFixtureState{}, fmt.Errorf("建立分页问题 %d: %w", index, err)
		}
	}
	paginationFirst, err := resources.ListBindingIssues(ctx, application.BindingIssueFilter{
		SourceID: paginationSource.ID, Status: "open",
	}, "", 50)
	if err != nil {
		return governanceFixtureState{}, err
	}
	if len(paginationFirst.Items) != 50 || paginationFirst.NextCursor == "" {
		return governanceFixtureState{}, fmt.Errorf("分页问题首页不完整: count=%d cursor=%q", len(paginationFirst.Items), paginationFirst.NextCursor)
	}
	paginationSecond, err := resources.ListBindingIssues(ctx, application.BindingIssueFilter{
		SourceID: paginationSource.ID, Status: "open",
	}, paginationFirst.NextCursor, 50)
	if err != nil {
		return governanceFixtureState{}, err
	}
	if len(paginationSecond.Items) != 1 || paginationSecond.NextCursor != "" {
		return governanceFixtureState{}, fmt.Errorf("分页问题次页不完整: count=%d cursor=%q", len(paginationSecond.Items), paginationSecond.NextCursor)
	}

	initialStructure := application.DiscoveredWork{
		SourceKey: "wkA",
		Title:     "待拆分作品",
		Media: []application.DiscoveredMedia{
			governanceMedia("wkA/m1", "1", 0),
			governanceMedia("wkA/m2", "2", 1),
			governanceMedia("wkA/m3", "3", 2),
		},
	}
	if _, err := resources.EnsureCanonical(ctx, structureSource.ID, []application.DiscoveredWork{initialStructure}); err != nil {
		return governanceFixtureState{}, fmt.Errorf("建立结构初始事实: %w", err)
	}
	_, structureErr := resources.EnsureCanonical(ctx, structureSource.ID, []application.DiscoveredWork{
		{SourceKey: "wkA1", Title: "拆分作品一", Media: []application.DiscoveredMedia{
			governanceMedia("wkA1/m1", "1", 0), governanceMedia("wkA1/m2", "2", 1),
		}},
		{SourceKey: "wkA2", Title: "拆分作品二", Media: []application.DiscoveredMedia{
			governanceMedia("wkA2/m3", "3", 0),
		}},
	})
	if err := requireBindingReview(structureErr); err != nil {
		return governanceFixtureState{}, fmt.Errorf("建立结构决策夹具: %w", err)
	}
	structureIssue, err := uniqueBindingIssue(ctx, resources, structureSource.ID, "SOURCE_WORK_SPLIT_REVIEW_REQUIRED")
	if err != nil {
		return governanceFixtureState{}, err
	}

	if _, err := resources.EnsureCanonical(ctx, mergeSource.ID, []application.DiscoveredWork{
		{SourceKey: "wkM1", Title: "待合并作品一", Media: []application.DiscoveredMedia{governanceMedia("wkM1/m1", "6", 0)}},
		{SourceKey: "wkM2", Title: "待合并作品二", Media: []application.DiscoveredMedia{governanceMedia("wkM2/m2", "7", 0)}},
	}); err != nil {
		return governanceFixtureState{}, fmt.Errorf("建立合并初始事实: %w", err)
	}
	_, mergeErr := resources.EnsureCanonical(ctx, mergeSource.ID, []application.DiscoveredWork{{
		SourceKey: "wkM", Title: "合并作品", Media: []application.DiscoveredMedia{
			governanceMedia("wkM/m1", "6", 0), governanceMedia("wkM/m2", "7", 1),
		},
	}})
	if err := requireBindingReview(mergeErr); err != nil {
		return governanceFixtureState{}, fmt.Errorf("建立合并决策夹具: %w", err)
	}
	mergeIssue, err := uniqueBindingIssue(ctx, resources, mergeSource.ID, "SOURCE_WORK_MERGE_REVIEW_REQUIRED")
	if err != nil {
		return governanceFixtureState{}, err
	}
	mergeIssue, err = resources.GetBindingIssue(ctx, mergeIssue.ID)
	if err != nil {
		return governanceFixtureState{}, err
	}
	mergeTargetWorkID := ""
	for _, candidate := range mergeIssue.Candidates {
		if candidate.MatchSignal == "origin_canonical" {
			mergeTargetWorkID = candidate.CandidateID
			break
		}
	}
	if mergeTargetWorkID == "" {
		return governanceFixtureState{}, fmt.Errorf("合并决策夹具缺少 origin_canonical 候选")
	}

	consumedInitial := application.DiscoveredWork{
		SourceKey: "wkC", Title: "已消费决策作品", Media: []application.DiscoveredMedia{
			governanceMedia("wkC/m1", "c", 0), governanceMedia("wkC/m2", "d", 1),
		},
	}
	if _, err := resources.EnsureCanonical(ctx, consumedSource.ID, []application.DiscoveredWork{consumedInitial}); err != nil {
		return governanceFixtureState{}, fmt.Errorf("建立已消费决策初始事实: %w", err)
	}
	consumedSplit := []application.DiscoveredWork{
		{SourceKey: "wkC1", Title: "已消费拆分一", Media: []application.DiscoveredMedia{governanceMedia("wkC1/m1", "c", 0)}},
		{SourceKey: "wkC2", Title: "已消费拆分二", Media: []application.DiscoveredMedia{governanceMedia("wkC2/m2", "d", 0)}},
	}
	_, consumedErr := resources.EnsureCanonical(ctx, consumedSource.ID, consumedSplit)
	if err := requireBindingReview(consumedErr); err != nil {
		return governanceFixtureState{}, fmt.Errorf("建立已消费决策 issue: %w", err)
	}
	consumedIssue, err := uniqueBindingIssue(ctx, resources, consumedSource.ID, "SOURCE_WORK_SPLIT_REVIEW_REQUIRED")
	if err != nil {
		return governanceFixtureState{}, err
	}
	consumedDecision, err := resources.ResolveSourceStructureIssue(
		ctx, consumedIssue.ID, "governance-e2e", "split_inherit", "wkC1", "", consumedIssue.Version,
	)
	if err != nil {
		return governanceFixtureState{}, fmt.Errorf("建立待消费结构决策: %w", err)
	}
	if _, err := resources.EnsureCanonical(ctx, consumedSource.ID, consumedSplit); err != nil {
		return governanceFixtureState{}, fmt.Errorf("消费结构决策: %w", err)
	}

	const orphanSourceKey = "orphan-work"
	orphanWorks := []application.DiscoveredWork{{
		SourceKey:  orphanSourceKey,
		ProviderID: "e2e",
		ExternalID: "orphan-work-1",
		Title:      "待处理孤儿作品",
		Creator: application.DiscoveredCreator{
			SourceKey: "orphan-creator-extend", ProviderID: "e2e", ExternalID: "orphan-creator-1", Name: "延长候选创作者",
		},
		Media: []application.DiscoveredMedia{governanceMedia("orphan-work/asset.jpg", "8", 0)},
	}, {
		SourceKey: "orphan-unbind-work", ProviderID: "e2e", ExternalID: "orphan-work-2", Title: "解绑孤儿作品",
		Creator: application.DiscoveredCreator{
			SourceKey: "orphan-creator-unbind", ProviderID: "e2e", ExternalID: "orphan-creator-2", Name: "解绑候选创作者",
		},
		Media: []application.DiscoveredMedia{governanceMedia("orphan-unbind-work/asset.jpg", "9", 0)},
	}, {
		SourceKey: "orphan-creator-work", ProviderID: "e2e", ExternalID: "orphan-work-3", Title: "创作者孤儿作品",
		Creator: application.DiscoveredCreator{
			SourceKey: "orphan-confirm-creator", ProviderID: "e2e", ExternalID: "orphan-creator-3", Name: "确认孤儿创作者",
		},
		Media: []application.DiscoveredMedia{governanceMedia("orphan-creator-work/asset.jpg", "a", 0)},
	}, {
		SourceKey: "orphan-media-work", ProviderID: "e2e", ExternalID: "orphan-work-4", Title: "媒体孤儿作品",
		Creator: application.DiscoveredCreator{
			SourceKey: "orphan-creator-retain", ProviderID: "e2e", ExternalID: "orphan-creator-4", Name: "保留候选创作者",
		},
		Media: []application.DiscoveredMedia{governanceMedia("orphan-retain-media/asset.jpg", "b", 0)},
	}}
	if _, err := resources.EnsureCanonical(ctx, orphanSource.ID, orphanWorks); err != nil {
		return governanceFixtureState{}, fmt.Errorf("建立孤儿初始事实: %w", err)
	}
	for range 3 {
		if _, err := resources.EnsureCanonical(ctx, orphanSource.ID, nil); err != nil {
			return governanceFixtureState{}, fmt.Errorf("推进孤儿保留窗口: %w", err)
		}
	}
	workOrphans, err := resources.ListOrphanCandidates(ctx, application.OrphanCandidateFilter{
		SourceID: orphanSource.ID, EntityType: "work",
	}, "", 50)
	if err != nil {
		return governanceFixtureState{}, err
	}
	creatorOrphans, err := resources.ListOrphanCandidates(ctx, application.OrphanCandidateFilter{
		SourceID: orphanSource.ID, EntityType: "creator",
	}, "", 50)
	if err != nil {
		return governanceFixtureState{}, err
	}
	mediaOrphans, err := resources.ListOrphanCandidates(ctx, application.OrphanCandidateFilter{
		SourceID: orphanSource.ID, EntityType: "media",
	}, "", 50)
	if err != nil {
		return governanceFixtureState{}, err
	}
	orphanExtend, err := requireOrphanCandidate(workOrphans.Items, orphanSourceKey)
	if err != nil {
		return governanceFixtureState{}, err
	}
	orphanUnbind, err := requireOrphanCandidate(workOrphans.Items, "orphan-unbind-work")
	if err != nil {
		return governanceFixtureState{}, err
	}
	orphanCreator, err := requireOrphanCandidate(creatorOrphans.Items, "orphan-confirm-creator")
	if err != nil {
		return governanceFixtureState{}, err
	}
	orphanMedia, err := requireOrphanCandidate(mediaOrphans.Items, "orphan-retain-media/asset.jpg")
	if err != nil {
		return governanceFixtureState{}, err
	}

	const mediaSourceKey = "media-work/asset.jpg"
	mediaWork := application.DiscoveredWork{
		SourceKey: "media-work",
		Title:     "媒体解绑作品",
		Media:     []application.DiscoveredMedia{governanceMedia(mediaSourceKey, "5", 0)},
	}
	if _, err := resources.EnsureCanonical(ctx, mediaSource.ID, []application.DiscoveredWork{mediaWork}); err != nil {
		return governanceFixtureState{}, fmt.Errorf("建立媒体解绑事实: %w", err)
	}

	return governanceFixtureState{
		IssueSourceID: issueSource.ID, IssueSourceName: issueSource.DisplayName, IssueID: issue.ID,
		IssueSourceKey: issueSourceKey,
		IssueBindID:    issueBind.ID, IssueBindSourceKey: issueBindSourceKey, IssueBindTargetID: bindCandidates[0],
		IssueSeparateID: issueSeparate.ID, IssueSeparateSourceKey: issueSeparateSourceKey,
		LifecycleSourceID: lifecycleSource.ID, LifecycleSourceName: lifecycleSource.DisplayName,
		LifecycleSourceKey:    lifecycleSourceKey,
		LifecycleSupersededID: lifecycleSuperseded.ID, LifecycleSupersededVersion: lifecycleSuperseded.Version,
		LifecycleStaleID: lifecycleStale.ID, LifecycleStaleVersion: lifecycleStale.Version,
		PaginationSourceID: paginationSource.ID, PaginationSourceName: paginationSource.DisplayName,
		PaginationIssueCount: paginationIssueCount,
		StructureSourceID:    structureSource.ID, StructureSourceName: structureSource.DisplayName,
		StructureIssueID: structureIssue.ID, StructureTargetSourceKey: "wkA1",
		MergeSourceID: mergeSource.ID, MergeSourceName: mergeSource.DisplayName,
		MergeIssueID: mergeIssue.ID, MergeTargetWorkID: mergeTargetWorkID,
		ConsumedDecisionID: consumedDecision.DecisionID, ConsumedDecisionIssueID: consumedDecision.IssueID,
		ConsumedDecisionVersion: consumedDecision.Version,
		OrphanSourceID:          orphanSource.ID, OrphanSourceName: orphanSource.DisplayName,
		OrphanBindingID: orphanExtend.BindingID, OrphanSourceKey: orphanSourceKey,
		OrphanUnbindBindingID: orphanUnbind.BindingID, OrphanUnbindSourceKey: orphanUnbind.SourceKey,
		OrphanCreatorBindingID: orphanCreator.BindingID, OrphanCreatorSourceKey: orphanCreator.SourceKey,
		OrphanMediaBindingID: orphanMedia.BindingID, OrphanMediaSourceKey: orphanMedia.SourceKey,
		MediaSourceID: mediaSource.ID, MediaSourceName: mediaSource.DisplayName, MediaSourceKey: mediaSourceKey,
	}, nil
}

func prepareWorkConflictCandidates(
	ctx context.Context,
	resources *application.Resources,
	sourceID, prefix, externalID string,
) ([]string, error) {
	firstKey, secondKey := prefix+"-a", prefix+"-b"
	first, err := resources.EnsureCanonical(ctx, sourceID, []application.DiscoveredWork{{
		SourceKey: firstKey, ProviderID: "e2e", ExternalID: externalID, Title: prefix + " 候选甲",
	}})
	if err != nil {
		return nil, err
	}
	firstID := first[firstKey].ID
	if _, err := resources.ManualUnbindWork(ctx, sourceID, firstKey); err != nil {
		return nil, err
	}
	second, err := resources.EnsureCanonical(ctx, sourceID, []application.DiscoveredWork{{
		SourceKey: secondKey, ProviderID: "e2e", ExternalID: externalID, Title: prefix + " 候选乙",
	}})
	if err != nil {
		return nil, err
	}
	secondID := second[secondKey].ID
	if _, err := resources.UndoManualUnbind(ctx, sourceID, firstKey); err != nil {
		return nil, err
	}
	if firstID == "" || secondID == "" || firstID == secondID {
		return nil, fmt.Errorf("候选身份未分离: first=%q second=%q", firstID, secondID)
	}
	return []string{firstID, secondID}, nil
}

func containsFixtureCandidate(candidates []application.BindingIssueCandidate, candidateID string) bool {
	for _, candidate := range candidates {
		if candidate.CandidateID == candidateID {
			return true
		}
	}
	return false
}

func requireOrphanCandidate(items []application.OrphanCandidate, sourceKey string) (application.OrphanCandidate, error) {
	for _, item := range items {
		if item.SourceKey == sourceKey {
			return item, nil
		}
	}
	return application.OrphanCandidate{}, fmt.Errorf("缺少孤儿候选 sourceKey=%s: %+v", sourceKey, items)
}

func governanceMedia(sourceKey, digit string, ordinal int) application.DiscoveredMedia {
	return application.DiscoveredMedia{
		SourceKey: sourceKey,
		RuleKey:   filepath.Base(sourceKey),
		Algorithm: "sha256-v1",
		Digest:    digit + "000000000000000000000000000000000000000000000000000000000000000",
		Ordinal:   ordinal,
	}
}

func requireBindingReview(err error) error {
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeBindingReviewRequired {
		return fmt.Errorf("期望 %s，实际 %v", fault.CodeBindingReviewRequired, err)
	}
	return nil
}

func uniqueBindingIssue(ctx context.Context, resources *application.Resources, sourceID, code string) (application.BindingIssue, error) {
	page, err := resources.ListBindingIssues(ctx, application.BindingIssueFilter{SourceID: sourceID, Status: "open"}, "", 50)
	if err != nil {
		return application.BindingIssue{}, err
	}
	if len(page.Items) != 1 || page.Items[0].Code != code {
		return application.BindingIssue{}, fmt.Errorf("Source %s 的 open issue 不唯一或 code 不符: %+v", sourceID, page.Items)
	}
	return page.Items[0], nil
}

func bindingIssueByKey(
	ctx context.Context,
	resources *application.Resources,
	sourceID, sourceKey, status string,
) (application.BindingIssue, error) {
	page, err := resources.ListBindingIssues(ctx, application.BindingIssueFilter{SourceID: sourceID, Status: status}, "", 200)
	if err != nil {
		return application.BindingIssue{}, err
	}
	for _, item := range page.Items {
		if item.SourceKey == sourceKey {
			return resources.GetBindingIssue(ctx, item.ID)
		}
	}
	return application.BindingIssue{}, fmt.Errorf("Source %s 缺少 status=%s sourceKey=%s 的 issue", sourceID, status, sourceKey)
}

func writeGovernanceFixtureState(path string, fixtures governanceFixtureState) error {
	content, err := json.MarshalIndent(fixtures, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	return os.WriteFile(path, content, 0o600)
}
