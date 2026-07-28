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
	OrphanSourceID           string `json:"orphanSourceId"`
	OrphanSourceName         string `json:"orphanSourceName"`
	OrphanBindingID          string `json:"orphanBindingId"`
	OrphanSourceKey          string `json:"orphanSourceKey"`
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

	const orphanSourceKey = "orphan-work"
	orphanWork := application.DiscoveredWork{
		SourceKey:  orphanSourceKey,
		ProviderID: "e2e",
		ExternalID: "orphan-work-1",
		Title:      "待处理孤儿作品",
		Creator: application.DiscoveredCreator{
			SourceKey: "orphan-creator", ProviderID: "e2e", ExternalID: "orphan-creator-1", Name: "孤儿创作者",
		},
		Media: []application.DiscoveredMedia{governanceMedia("orphan-work/asset.jpg", "4", 0)},
	}
	if _, err := resources.EnsureCanonical(ctx, orphanSource.ID, []application.DiscoveredWork{orphanWork}); err != nil {
		return governanceFixtureState{}, fmt.Errorf("建立孤儿初始事实: %w", err)
	}
	for range 3 {
		if _, err := resources.EnsureCanonical(ctx, orphanSource.ID, nil); err != nil {
			return governanceFixtureState{}, fmt.Errorf("推进孤儿保留窗口: %w", err)
		}
	}
	orphans, err := resources.ListOrphanCandidates(ctx, application.OrphanCandidateFilter{
		SourceID: orphanSource.ID, EntityType: "work",
	}, "", 50)
	if err != nil {
		return governanceFixtureState{}, err
	}
	if len(orphans.Items) != 1 || orphans.Items[0].SourceKey != orphanSourceKey {
		return governanceFixtureState{}, fmt.Errorf("孤儿作品夹具不是唯一候选: %+v", orphans.Items)
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
		OrphanSourceID: orphanSource.ID, OrphanSourceName: orphanSource.DisplayName,
		OrphanBindingID: orphans.Items[0].BindingID, OrphanSourceKey: orphanSourceKey,
		MediaSourceID: mediaSource.ID, MediaSourceName: mediaSource.DisplayName, MediaSourceKey: mediaSourceKey,
	}, nil
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
