package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/storage"
)

const (
	governanceKeepSameOriginKey    = "keep-origin"
	governanceKeepSameOneKey       = "keep-one"
	governanceKeepSameTwoKey       = "keep-two"
	governanceCreateNewOriginKey   = "create-origin"
	governanceCreateNewOneKey      = "create-one"
	governanceCreateNewTwoKey      = "create-two"
	governanceMergeNewOriginOneKey = "merge-new-one"
	governanceMergeNewOriginTwoKey = "merge-new-two"
	governanceMergeNewResultKey    = "merge-new-result"

	governanceOrphanWorkKey        = "orphan-work"
	governanceOrphanCreatorWorkKey = "orphan-creator-work"
	governanceOrphanMediaWorkKey   = "orphan-media-work"
	governanceOrphanMediaSourceKey = "orphan-retain-media/asset.jpg"
	governanceOrphanSplitKey       = "orphan-split-on-return"
	governanceOrphanSplitMediaKey  = "orphan-split-on-return/asset.jpg"
)

func governanceKeepSameWorks() (application.DiscoveredWork, []application.DiscoveredWork) {
	initial := application.DiscoveredWork{
		SourceKey: governanceKeepSameOriginKey,
		Title:     "拆分后保持同一作品",
		Media: []application.DiscoveredMedia{
			governanceMedia("keep-origin/a.jpg", "1", 0),
			governanceMedia("keep-origin/b.jpg", "2", 1),
			governanceMedia("keep-origin/c.jpg", "3", 2),
		},
	}
	changed := []application.DiscoveredWork{
		{SourceKey: governanceKeepSameOneKey, Title: "保持同一作品一", Media: []application.DiscoveredMedia{
			governanceMedia("keep-one/a.jpg", "1", 0), governanceMedia("keep-one/b.jpg", "2", 1),
		}},
		{SourceKey: governanceKeepSameTwoKey, Title: "保持同一作品二", Media: []application.DiscoveredMedia{
			governanceMedia("keep-two/c.jpg", "3", 0),
		}},
	}
	return initial, changed
}

func governanceCreateNewWorks() (application.DiscoveredWork, []application.DiscoveredWork) {
	initial := application.DiscoveredWork{
		SourceKey: governanceCreateNewOriginKey,
		Title:     "拆分后全部新建作品",
		Media: []application.DiscoveredMedia{
			governanceMedia("create-origin/a.jpg", "4", 0),
			governanceMedia("create-origin/b.jpg", "5", 1),
		},
	}
	changed := []application.DiscoveredWork{
		{SourceKey: governanceCreateNewOneKey, Title: "全部新建作品一", Media: []application.DiscoveredMedia{
			governanceMedia("create-one/a.jpg", "4", 0),
		}},
		{SourceKey: governanceCreateNewTwoKey, Title: "全部新建作品二", Media: []application.DiscoveredMedia{
			governanceMedia("create-two/b.jpg", "5", 0),
		}},
	}
	return initial, changed
}

func governanceMergeNewWorks() ([]application.DiscoveredWork, application.DiscoveredWork) {
	initial := []application.DiscoveredWork{
		{SourceKey: governanceMergeNewOriginOneKey, Title: "合并新建原作品一", Media: []application.DiscoveredMedia{
			governanceMedia("merge-new-one/a.jpg", "6", 0),
		}},
		{SourceKey: governanceMergeNewOriginTwoKey, Title: "合并新建原作品二", Media: []application.DiscoveredMedia{
			governanceMedia("merge-new-two/b.jpg", "7", 0),
		}},
	}
	merged := application.DiscoveredWork{
		SourceKey: governanceMergeNewResultKey,
		Title:     "合并后创建新作品",
		Media: []application.DiscoveredMedia{
			governanceMedia("merge-new-result/a.jpg", "6", 0),
			governanceMedia("merge-new-result/b.jpg", "7", 1),
		},
	}
	return initial, merged
}

func governanceOrphanWorks() []application.DiscoveredWork {
	return []application.DiscoveredWork{{
		SourceKey:  governanceOrphanWorkKey,
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
		SourceKey: governanceOrphanCreatorWorkKey, ProviderID: "e2e", ExternalID: "orphan-work-3", Title: "创作者孤儿作品",
		Creator: application.DiscoveredCreator{
			SourceKey: "orphan-confirm-creator", ProviderID: "e2e", ExternalID: "orphan-creator-3", Name: "确认孤儿创作者",
		},
		Media: []application.DiscoveredMedia{governanceMedia("orphan-creator-work/asset.jpg", "a", 0)},
	}, {
		SourceKey: governanceOrphanMediaWorkKey, ProviderID: "e2e", ExternalID: "orphan-work-4", Title: "媒体孤儿作品",
		Creator: application.DiscoveredCreator{
			SourceKey: "orphan-creator-retain", ProviderID: "e2e", ExternalID: "orphan-creator-4", Name: "保留候选创作者",
		},
		Media: []application.DiscoveredMedia{governanceMedia(governanceOrphanMediaSourceKey, "b", 0)},
	}, {
		SourceKey: governanceOrphanSplitKey, ProviderID: "e2e", ExternalID: "orphan-work-5", Title: "重现时拆分作品",
		Creator: application.DiscoveredCreator{
			SourceKey: "orphan-creator-split", ProviderID: "e2e", ExternalID: "orphan-creator-5", Name: "重现时拆分创作者",
		},
		Media: []application.DiscoveredMedia{governanceMedia(governanceOrphanSplitMediaKey, "e", 0)},
	}}
}

// advanceGovernanceFixtures 在浏览器已经提交三种剩余结构动作与孤儿决策后运行。调用方必须先
// 停止 galleryd；本函数只通过 application.Resources 消费 pre-seed Binding、重放同一合成发现事实，
// 并把非敏感验证结果写回状态对象，不直接读写数据库或 Source。
func advanceGovernanceFixtures(ctx context.Context, appRoot, statePath string) (fixtures governanceFixtureState, err error) {
	fixtures, err = readGovernanceFixtureState(statePath)
	if err != nil {
		return governanceFixtureState{}, err
	}
	dirs := appdirs.UnderRoot(appRoot)
	fileSystem := filesystem.OS{}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		return governanceFixtureState{}, err
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	systemClock := clock.System{}
	resources, err := application.NewResources(
		store.Control.SQL(), dirs, fileSystem, systemClock, identity.NewGenerator(systemClock),
	)
	if err != nil {
		return governanceFixtureState{}, err
	}

	keepDecision, err := requireAppliedStructureDecision(
		ctx, resources, fixtures.KeepSameSourceID, fixtures.KeepSameIssueID, "split_keep_same",
	)
	if err != nil {
		return governanceFixtureState{}, err
	}
	_, keepWorks := governanceKeepSameWorks()
	kept, err := resources.EnsureCanonical(ctx, fixtures.KeepSameSourceID, keepWorks)
	if err != nil {
		return governanceFixtureState{}, fmt.Errorf("消费 split_keep_same: %w", err)
	}
	if kept[governanceKeepSameOneKey].ID != fixtures.KeepSameOriginalWorkID ||
		kept[governanceKeepSameTwoKey].ID != fixtures.KeepSameOriginalWorkID {
		return governanceFixtureState{}, fmt.Errorf("split_keep_same 未让两个新 sourceKey 复用原作品")
	}
	fixtures.KeepSameDecisionID, fixtures.KeepSameDecisionVersion = keepDecision.DecisionID, keepDecision.Version

	createDecision, err := requireAppliedStructureDecision(
		ctx, resources, fixtures.CreateNewSourceID, fixtures.CreateNewIssueID, "split_create_new",
	)
	if err != nil {
		return governanceFixtureState{}, err
	}
	_, createWorks := governanceCreateNewWorks()
	created, err := resources.EnsureCanonical(ctx, fixtures.CreateNewSourceID, createWorks)
	if err != nil {
		return governanceFixtureState{}, fmt.Errorf("消费 split_create_new: %w", err)
	}
	createdOne, createdTwo := created[governanceCreateNewOneKey].ID, created[governanceCreateNewTwoKey].ID
	if createdOne == "" || createdTwo == "" || createdOne == createdTwo ||
		createdOne == fixtures.CreateNewOriginalWorkID || createdTwo == fixtures.CreateNewOriginalWorkID {
		return governanceFixtureState{}, fmt.Errorf("split_create_new 未创建两个独立新作品")
	}
	fixtures.CreateNewDecisionID, fixtures.CreateNewDecisionVersion = createDecision.DecisionID, createDecision.Version

	mergeDecision, err := requireAppliedStructureDecision(
		ctx, resources, fixtures.MergeNewSourceID, fixtures.MergeNewIssueID, "merge_create_new",
	)
	if err != nil {
		return governanceFixtureState{}, err
	}
	_, mergedWork := governanceMergeNewWorks()
	merged, err := resources.EnsureCanonical(ctx, fixtures.MergeNewSourceID, []application.DiscoveredWork{mergedWork})
	if err != nil {
		return governanceFixtureState{}, fmt.Errorf("消费 merge_create_new: %w", err)
	}
	mergedID := merged[governanceMergeNewResultKey].ID
	if mergedID == "" || containsFixtureString(fixtures.MergeNewOriginalWorkIDs, mergedID) {
		return governanceFixtureState{}, fmt.Errorf("merge_create_new 未建立独立新作品")
	}
	fixtures.MergeNewDecisionID, fixtures.MergeNewDecisionVersion = mergeDecision.DecisionID, mergeDecision.Version

	reappeared, err := resources.EnsureCanonical(ctx, fixtures.OrphanSourceID, governanceOrphanWorks())
	if err != nil {
		return governanceFixtureState{}, fmt.Errorf("重放孤儿发现事实: %w", err)
	}
	orphanWork := reappeared[governanceOrphanWorkKey]
	orphanCreatorWork := reappeared[governanceOrphanCreatorWorkKey]
	orphanMediaWork := reappeared[governanceOrphanMediaWorkKey]
	orphanSplitWork := reappeared[governanceOrphanSplitKey]
	if orphanWork.ID != fixtures.OrphanOriginalWorkID || len(orphanCreatorWork.Creators) != 1 ||
		orphanCreatorWork.Creators[0].ID != fixtures.OrphanOriginalCreatorID ||
		orphanMediaWork.Media[governanceOrphanMediaSourceKey].ID != fixtures.OrphanOriginalMediaID {
		return governanceFixtureState{}, fmt.Errorf("inactive/orphaned Binding 重现时未复用原 Canonical 身份")
	}
	newMediaID := orphanSplitWork.Media[governanceOrphanSplitMediaKey].ID
	if orphanSplitWork.ID == "" || orphanSplitWork.ID == fixtures.OrphanUnboundOldWorkID ||
		newMediaID == "" || newMediaID == fixtures.OrphanUnboundOldMediaID {
		return governanceFixtureState{}, fmt.Errorf("manual_unbound Binding 重现时未建立新 Canonical 身份")
	}
	fixtures.OrphanReappearedWorkID = orphanWork.ID
	fixtures.OrphanReappearedCreatorID = orphanCreatorWork.Creators[0].ID
	fixtures.OrphanReappearedMediaID = orphanMediaWork.Media[governanceOrphanMediaSourceKey].ID
	fixtures.OrphanUnboundNewWorkID = orphanSplitWork.ID
	fixtures.OrphanUnboundNewMediaID = newMediaID
	return fixtures, nil
}

func requireAppliedStructureDecision(
	ctx context.Context,
	resources *application.Resources,
	sourceID, issueID, action string,
) (application.SourceStructureDecision, error) {
	decisions, err := resources.ListSourceStructureDecisions(ctx, sourceID, "applied", 50)
	if err != nil {
		return application.SourceStructureDecision{}, err
	}
	for _, decision := range decisions {
		if decision.IssueID == issueID && decision.Action == action {
			return decision, nil
		}
	}
	return application.SourceStructureDecision{}, fmt.Errorf(
		"Source %s 缺少 issue=%s action=%s 的 applied 结构决策", sourceID, issueID, action,
	)
}

func readGovernanceFixtureState(path string) (governanceFixtureState, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return governanceFixtureState{}, err
	}
	var fixtures governanceFixtureState
	if err := json.Unmarshal(content, &fixtures); err != nil {
		return governanceFixtureState{}, err
	}
	return fixtures, nil
}

func containsFixtureString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
