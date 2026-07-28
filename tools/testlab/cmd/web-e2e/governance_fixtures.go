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
	governanceIssueSourceName     = "治理 E2E · 绑定问题"
	governanceStructureSourceName = "治理 E2E · 结构决策"
	governanceMergeSourceName     = "治理 E2E · 合并决策"
	governanceConsumedSourceName  = "治理 E2E · 已消费决策"
	governanceOrphanSourceName    = "治理 E2E · 孤儿候选"
	governanceMediaSourceName     = "治理 E2E · 媒体解绑"
)

type governanceFixtureState struct {
	IssueSourceID            string `json:"issueSourceId"`
	IssueSourceName          string `json:"issueSourceName"`
	IssueID                  string `json:"issueId"`
	IssueSourceKey           string `json:"issueSourceKey"`
	StructureSourceID        string `json:"structureSourceId"`
	StructureSourceName      string `json:"structureSourceName"`
	StructureIssueID         string `json:"structureIssueId"`
	StructureTargetSourceKey string `json:"structureTargetSourceKey"`
	MergeSourceID            string `json:"mergeSourceId"`
	MergeSourceName          string `json:"mergeSourceName"`
	MergeIssueID             string `json:"mergeIssueId"`
	MergeTargetWorkID        string `json:"mergeTargetWorkId"`
	ConsumedDecisionID       string `json:"consumedDecisionId"`
	ConsumedDecisionIssueID  string `json:"consumedDecisionIssueId"`
	ConsumedDecisionVersion  int    `json:"consumedDecisionVersion"`
	OrphanSourceID           string `json:"orphanSourceId"`
	OrphanSourceName         string `json:"orphanSourceName"`
	OrphanBindingID          string `json:"orphanBindingId"`
	OrphanSourceKey          string `json:"orphanSourceKey"`
	OrphanUnbindBindingID    string `json:"orphanUnbindBindingId"`
	OrphanUnbindSourceKey    string `json:"orphanUnbindSourceKey"`
	OrphanCreatorBindingID   string `json:"orphanCreatorBindingId"`
	OrphanCreatorSourceKey   string `json:"orphanCreatorSourceKey"`
	OrphanMediaBindingID     string `json:"orphanMediaBindingId"`
	OrphanMediaSourceKey     string `json:"orphanMediaSourceKey"`
	MediaSourceID            string `json:"mediaSourceId"`
	MediaSourceName          string `json:"mediaSourceName"`
	MediaSourceKey           string `json:"mediaSourceKey"`
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
		"issue":     filepath.Join(sourceRoot, "binding-issue"),
		"structure": filepath.Join(sourceRoot, "structure"),
		"merge":     filepath.Join(sourceRoot, "structure-merge"),
		"consumed":  filepath.Join(sourceRoot, "structure-consumed"),
		"orphan":    filepath.Join(sourceRoot, "orphan"),
		"media":     filepath.Join(sourceRoot, "media"),
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

	const issueSourceKey = "duplicate-work"
	_, issueErr := resources.EnsureCanonical(ctx, issueSource.ID, []application.DiscoveredWork{
		{SourceKey: issueSourceKey, Title: "重复作品甲"},
		{SourceKey: issueSourceKey, Title: "重复作品乙"},
	})
	if err := requireBindingReview(issueErr); err != nil {
		return governanceFixtureState{}, fmt.Errorf("建立绑定问题夹具: %w", err)
	}
	issue, err := uniqueBindingIssue(ctx, resources, issueSource.ID, string(fault.CodeBindingReviewRequired))
	if err != nil {
		return governanceFixtureState{}, err
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
		IssueSourceKey:    issueSourceKey,
		StructureSourceID: structureSource.ID, StructureSourceName: structureSource.DisplayName,
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

func writeGovernanceFixtureState(path string, fixtures governanceFixtureState) error {
	content, err := json.MarshalIndent(fixtures, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	return os.WriteFile(path, content, 0o600)
}
