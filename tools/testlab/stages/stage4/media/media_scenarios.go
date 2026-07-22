// Package media 覆盖阶段 4「查询和媒体」中媒体侧（真实/合成 Source 建立、目标化
// 按需内容确认、Range/ETag/If-Range、DerivedAsset 端到端）的 orchestrator 与断言，
// 只依赖 tools/testlab 的共享模块（report/environment/sourceguard），不导入任何
// internal/* 包，也不按 Provider 写任何特例分支——真实 Source 的差异只通过规则
// 夹具（tools/testlab/fixtures/rules/<来源>）表达。
package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"time"

	api "github.com/RecRivenVI/gallery/pkg/galleryapi"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/environment"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/report"
)

const testlabMinimalRulePackage = `{
  "rule_set_id": "rset_018f47d2-5c16-7a44-a8a0-900000000001",
  "version": "0.1.0",
  "schema_version": 1,
  "normalization_algorithm_version": "gallery-canonical-json-v1",
  "compiler_requirement": "gallery-rule-compiler-v1",
  "cel_profile_version": "gallery-cel-v1",
  "parameter_schema": {"type": "object", "additionalProperties": false},
  "provider_namespaces": [],
  "primitives": [
    {"id": "work", "kind": "path_match", "config": {"scope": "work_directory", "glob": "*", "title": "directory_name", "stable_key": "relative_path", "metadata_file": "metadata.json"}},
    {"id": "creator", "kind": "metadata_map", "config": {"fields": {"creator": ["/creator/name"]}}},
    {"id": "media", "kind": "media_classify", "config": {"glob": "*.bin", "kind": "image", "mime": "application/octet-stream"}}
  ],
  "cel_expressions": [],
  "tests": [{"id": "one-work-one-media"}],
  "extensions": {}
}`

func ptr[T any](v T) *T { return &v }

func listWorks(sess *environment.Session, params api.ListWorksParams) (*api.ListWorksResponse, error) {
	return sess.ListWorks(params)
}

func waitForJob(sess *environment.Session, jobID string, timeout time.Duration) (*api.Job, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := sess.Client.GetJobWithResponse(context.Background(), jobID, sess.SameOrigin)
		if err != nil {
			return nil, err
		}
		if resp.JSON200 == nil {
			return nil, fmt.Errorf("job snapshot 状态 %d", environment.StatusOf(resp))
		}
		status := string(resp.JSON200.Status)
		if status == "completed" || status == "failed" || status == "cancelled" || status == "needs_repair" {
			job := *resp.JSON200
			return &job, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil, fmt.Errorf("job %s 未在 %s 内终止", jobID, timeout)
}

func encodeTestJPEG(seed int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 320, 240))
	for y := 0; y < 240; y++ {
		for x := 0; x < 320; x++ {
			img.Set(x, y, color.RGBA{R: uint8((x + seed) % 256), G: uint8((y + seed) % 256), B: 96, A: 255})
		}
	}
	var buffer bytes.Buffer
	_ = jpeg.Encode(&buffer, img, &jpeg.Options{Quality: 85})
	return buffer.Bytes()
}

// SetupMediaSource 建立一个真实的、有界的小型只读 Source（约 12 个作品，各一个真实
// JPEG 字节内容），通过公开 API 绑定最小规则包并触发首次真实扫描。首次扫描省略
// scanProfile：全新 Source 且 control.db 无历史，默认解析为 index（只发现、不确认
// 内容），返回的媒体应全部是 located_unverified，用于验证媒体读取与按需确认。
func SetupMediaSource(rep *report.Report, sess *environment.Session, sourceRoot string) (libraryID, sourceID string, workCount int, err error) {
	ctx := context.Background()
	libResp, err := sess.Client.CreateLibraryWithResponse(ctx, &api.CreateLibraryParams{XGalleryCSRF: sess.CSRF}, api.LibraryCreateRequest{Name: "TestlabMedia"}, sess.SameOrigin)
	if err != nil || libResp.JSON201 == nil {
		return "", "", 0, fmt.Errorf("创建 Library 失败: %v status=%d", err, environment.StatusOf(libResp))
	}
	libraryID = libResp.JSON201.Id

	workCount = 12
	for i := 0; i < workCount; i++ {
		workDir := filepath.Join(sourceRoot, fmt.Sprintf("work-%02d", i))
		if err := os.MkdirAll(workDir, 0o700); err != nil {
			return "", "", 0, err
		}
		if err := os.WriteFile(filepath.Join(workDir, "photo.bin"), encodeTestJPEG(i), 0o600); err != nil {
			return "", "", 0, err
		}
		metadata := fmt.Sprintf(`{"creator":{"name":"TestlabCreator%02d"}}`, i%3)
		if err := os.WriteFile(filepath.Join(workDir, "metadata.json"), []byte(metadata), 0o600); err != nil {
			return "", "", 0, err
		}
	}

	sourceResp, err := sess.Client.CreateSourceWithResponse(ctx, &api.CreateSourceParams{XGalleryCSRF: sess.CSRF},
		api.SourceCreateRequest{LibraryId: libraryID, DisplayName: "TestlabMediaSource", RootPath: sourceRoot}, sess.SameOrigin)
	if err != nil || sourceResp.JSON201 == nil {
		return "", "", 0, fmt.Errorf("创建 Source 失败: %v status=%d body=%s", err, environment.StatusOf(sourceResp), string(sourceResp.Body))
	}
	sourceID = sourceResp.JSON201.Id

	var rulePackage map[string]any
	if err := json.Unmarshal([]byte(testlabMinimalRulePackage), &rulePackage); err != nil {
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
	job, err := waitForJob(sess, scanResp.JSON202.Id, 60*time.Second)
	if err != nil {
		return "", "", 0, err
	}
	if job.Status != "completed" {
		issue := ""
		if job.IssueCode != nil {
			issue = *job.IssueCode
		}
		return "", "", 0, fmt.Errorf("首次扫描未完成: status=%s issue=%s", job.Status, issue)
	}
	rep.Add("media/first-index-scan-completed", true, "")
	return libraryID, sourceID, workCount, nil
}
