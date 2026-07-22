package media

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	api "github.com/RecRivenVI/gallery/pkg/galleryapi"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/environment"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/report"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/sourceguard"
)

// SetupRealMediaSourceWithRule 建立指向真实、只读、已授权 Source 有界子目录的
// Library/Source，绑定 fixtures/rules/<来源> 下与该 Source 结构匹配的规则包，
// 并触发首次真实 index 扫描。调用方必须先确认 sourceRoot 属于本机配置
// （Documents/本地/testlab.local.json）登记的授权真实 Source 之一，并只传入该
// Source 根目录本身（由本函数在其中选择有界子目录），不得传入未授权目录；
// ruleFixturePath 必须指向与该 Source 目录结构匹配的规则夹具。
func SetupRealMediaSourceWithRule(rep *report.Report, sess *environment.Session, sourceRoot string, maxMediaItems int, ruleFixturePath string) (libraryID, sourceID string, workCount int, err error) {
	ctx := context.Background()
	boundedRoot, fileCount, err := sourceguard.SelectBoundedSubdirectory(sourceRoot, 10, maxMediaItems*3, 200)
	if err != nil {
		return "", "", 0, fmt.Errorf("选择有界子目录失败: %w", err)
	}
	rep.Add("media/bounded-subdirectory-selected", true, fmt.Sprintf("fileCount=%d", fileCount))

	preManifest, err := sourceguard.Walk(boundedRoot)
	if err != nil {
		return "", "", 0, fmt.Errorf("生成 pre-manifest 失败: %w", err)
	}

	libResp, err := sess.Client.CreateLibraryWithResponse(ctx, &api.CreateLibraryParams{XGalleryCSRF: sess.CSRF}, api.LibraryCreateRequest{Name: "TestlabRealMedia"}, sess.SameOrigin)
	if err != nil || libResp.JSON201 == nil {
		return "", "", 0, fmt.Errorf("创建 Library 失败: %v status=%d", err, environment.StatusOf(libResp))
	}
	libraryID = libResp.JSON201.Id

	sourceResp, err := sess.Client.CreateSourceWithResponse(ctx, &api.CreateSourceParams{XGalleryCSRF: sess.CSRF},
		api.SourceCreateRequest{LibraryId: libraryID, DisplayName: "TestlabRealMediaSource", RootPath: boundedRoot}, sess.SameOrigin)
	if err != nil || sourceResp.JSON201 == nil {
		return "", "", 0, fmt.Errorf("创建 Source 失败: %v status=%d body=%s", err, environment.StatusOf(sourceResp), string(sourceResp.Body))
	}
	sourceID = sourceResp.JSON201.Id

	if ruleFixturePath == "" {
		return "", "", 0, fmt.Errorf("必须指定与该 Source 结构匹配的规则夹具路径")
	}
	ruleBytes, err := os.ReadFile(ruleFixturePath)
	if err != nil {
		return "", "", 0, fmt.Errorf("读取规则夹具失败: %w", err)
	}
	var rulePackage map[string]any
	if err := json.Unmarshal(ruleBytes, &rulePackage); err != nil {
		return "", "", 0, err
	}
	ruleResp, err := sess.Client.CreateRuleVersionWithResponse(ctx, &api.CreateRuleVersionParams{XGalleryCSRF: sess.CSRF}, api.RuleVersionCreateRequest{Package: rulePackage}, sess.SameOrigin)
	if err != nil || ruleResp.JSON201 == nil {
		return "", "", 0, fmt.Errorf("创建 RuleVersion 失败: %v status=%d body=%s", err, environment.StatusOf(ruleResp), string(ruleResp.Body))
	}
	if _, err := sess.Client.CreateSourceRuleBindingWithResponse(ctx, &api.CreateSourceRuleBindingParams{XGalleryCSRF: sess.CSRF},
		api.SourceRuleBindingCreateRequest{SourceId: sourceID, SemanticHash: ruleResp.JSON201.SemanticHash, Parameters: map[string]any{}, Priority: 0}, sess.SameOrigin); err != nil {
		return "", "", 0, fmt.Errorf("绑定规则失败: %v", err)
	}

	scanResp, err := sess.Client.CreateScanJobWithResponse(ctx, sourceID, &api.CreateScanJobParams{XGalleryCSRF: sess.CSRF}, api.ScanJobCreateRequest{}, sess.SameOrigin)
	if err != nil || scanResp.JSON202 == nil {
		return "", "", 0, fmt.Errorf("创建首次扫描失败: %v status=%d body=%s", err, environment.StatusOf(scanResp), string(scanResp.Body))
	}
	job, err := waitForJob(sess, scanResp.JSON202.Id, 25*time.Minute)
	if err != nil {
		return "", "", 0, err
	}
	if job.Status != "completed" {
		issue := ""
		if job.IssueCode != nil {
			issue = *job.IssueCode
		}
		return "", "", 0, fmt.Errorf("首次真实扫描未完成: status=%s issue=%s", job.Status, issue)
	}
	rep.Add("media/real-first-index-scan-completed", true, "")

	postManifest, err := sourceguard.Walk(boundedRoot)
	if err != nil {
		return "", "", 0, fmt.Errorf("生成 post-manifest 失败: %w", err)
	}
	guardOK := preManifest.Equal(postManifest)
	rep.Add("media/real-source-guard-unchanged", guardOK, fmt.Sprintf("preFiles=%d postFiles=%d preDirs=%d postDirs=%d", preManifest.FileCount, postManifest.FileCount, preManifest.DirCount, postManifest.DirCount))
	if !guardOK {
		return "", "", 0, fmt.Errorf("真实 Source 只读 guard 不一致，本次真实媒体测试结果无效")
	}

	// 实际发布的作品数以查询 API 的精确 total 为准，不假设规则解析出的作品数等于
	// 原始文件数（一个作品目录通常同时包含一个媒体文件与一个 metadata 文件）。
	listResp, err := listWorks(sess, api.ListWorksParams{LibraryId: &libraryID, Limit: ptr(1)})
	if err != nil || listResp.JSON200 == nil || string(listResp.JSON200.Total.Mode) != "exact" || listResp.JSON200.Total.Value == nil {
		return "", "", 0, fmt.Errorf("扫描后无法取得精确作品数: err=%v", err)
	}
	workCount = int(*listResp.JSON200.Total.Value)
	rep.Add("media/real-scan-produced-works", workCount > 0, fmt.Sprintf("workCount=%d", workCount))
	return libraryID, sourceID, workCount, nil
}
