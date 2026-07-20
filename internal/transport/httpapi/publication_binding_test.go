package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/auth"
	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/config"
	"github.com/RecRivenVI/gallery/internal/contract/realtime"
	"github.com/RecRivenVI/gallery/internal/creators"
	"github.com/RecRivenVI/gallery/internal/derived"
	"github.com/RecRivenVI/gallery/internal/derived/thumbnail"
	"github.com/RecRivenVI/gallery/internal/derivedjob"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/overlay"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/scanner"
	"github.com/RecRivenVI/gallery/internal/storage"
	"github.com/RecRivenVI/gallery/internal/transport/httpapi"
	api "github.com/RecRivenVI/gallery/pkg/galleryapi"
)

// TestMediaVerificationJobScopedToTargetMediaViaAPI 是阶段 4 Correctness 收尾的核心
// 回归：通过公共 HTTP API 对多媒体 Source 中的一个媒体请求按需确认，必须只强制该媒体
// 重新完整哈希，同 Source 的其余媒体保持 located_unverified，不再触发整个 Source 的
// verify 档案。
func TestMediaVerificationJobScopedToTargetMediaViaAPI(t *testing.T) {
	ctx := context.Background()
	_, client, mutation, csrf, cleanup := newPublicationBindingServer(t)
	defer cleanup()

	sourceRoot := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(filepath.Join(sourceRoot, "work-one"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"media-a.bin", "media-b.bin", "media-c.bin"} {
		if err := os.WriteFile(filepath.Join(sourceRoot, "work-one", name), []byte("content-"+name), 0o400); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "work-one", "metadata.json"), []byte(`{"creator":{"name":"Publication Creator"}}`), 0o400); err != nil {
		t.Fatal(err)
	}
	libraryResponse, err := client.CreateLibraryWithResponse(ctx, &api.CreateLibraryParams{XGalleryCSRF: csrf}, api.LibraryCreateRequest{Name: "PubBinding"}, mutation)
	if err != nil || libraryResponse.JSON201 == nil {
		t.Fatalf("创建 Library 失败: %v", err)
	}
	sourceResponse, err := client.CreateSourceWithResponse(ctx, &api.CreateSourceParams{XGalleryCSRF: csrf},
		api.SourceCreateRequest{LibraryId: libraryResponse.JSON201.Id, DisplayName: "PubBinding Source", RootPath: sourceRoot}, mutation)
	if err != nil || sourceResponse.JSON201 == nil {
		t.Fatalf("创建 Source 失败: %v", err)
	}
	ruleJSON, err := os.ReadFile(filepath.Join("..", "..", "rules", "testdata", "minimal-rule-package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rulePackage map[string]any
	if err := json.Unmarshal(ruleJSON, &rulePackage); err != nil {
		t.Fatal(err)
	}
	ruleResponse, err := client.CreateRuleVersionWithResponse(ctx, &api.CreateRuleVersionParams{XGalleryCSRF: csrf}, api.RuleVersionCreateRequest{Package: rulePackage}, mutation)
	if err != nil || ruleResponse.JSON201 == nil {
		t.Fatalf("创建 RuleVersion 失败: %v", err)
	}
	if _, err := client.CreateSourceRuleBindingWithResponse(ctx, &api.CreateSourceRuleBindingParams{XGalleryCSRF: csrf},
		api.SourceRuleBindingCreateRequest{SourceId: sourceResponse.JSON201.Id, SemanticHash: ruleResponse.JSON201.SemanticHash, Parameters: map[string]any{}, Priority: 0}, mutation); err != nil {
		t.Fatal(err)
	}
	scanJob, err := client.CreateScanJobWithResponse(ctx, sourceResponse.JSON201.Id, &api.CreateScanJobParams{XGalleryCSRF: csrf}, api.ScanJobCreateRequest{}, mutation)
	if err != nil || scanJob.JSON202 == nil {
		t.Fatalf("创建首次扫描失败: %v", err)
	}
	if completed := waitForJob(t, client, scanJob.JSON202.Id); string(completed.Status) != "completed" {
		t.Fatalf("首次 index 扫描未完成: %+v", completed)
	}
	worksResponse, err := client.ListWorksWithResponse(ctx, nil)
	if err != nil || worksResponse.JSON200 == nil || len(worksResponse.JSON200.Works) != 1 {
		t.Fatalf("Work 查询失败: %v", err)
	}
	mediaResponse, err := client.ListWorkMediaWithResponse(ctx, worksResponse.JSON200.Works[0].Id, &api.ListWorkMediaParams{})
	if err != nil || mediaResponse.JSON200 == nil || len(mediaResponse.JSON200.Media) != 3 {
		t.Fatalf("Media 查询失败或数量不为 3: %v %+v", err, mediaResponse.JSON200)
	}
	for _, item := range mediaResponse.JSON200.Media {
		if item.ContentVerificationState != api.LocatedUnverified {
			t.Fatalf("index 扫描后所有媒体都应是 located_unverified: %+v", item)
		}
	}
	publicationBefore := mediaResponse.JSON200.QueryPublicationId
	target := mediaResponse.JSON200.Media[1]

	verifyJob, err := client.CreateMediaVerificationJobWithResponse(ctx, target.Id, &api.CreateMediaVerificationJobParams{XGalleryCSRF: csrf}, mutation)
	if err != nil || verifyJob.JSON202 == nil {
		t.Fatalf("创建按需确认 Job 失败: %v status=%d body=%s", err, verifyJob.StatusCode(), verifyJob.Body)
	}
	if completed := waitForJob(t, client, verifyJob.JSON202.Id); string(completed.Status) != "completed" {
		t.Fatalf("按需确认 Job 未完成: %+v", completed)
	}

	afterResponse, err := client.ListWorkMediaWithResponse(ctx, worksResponse.JSON200.Works[0].Id, &api.ListWorkMediaParams{})
	if err != nil || afterResponse.JSON200 == nil || len(afterResponse.JSON200.Media) != 3 {
		t.Fatalf("确认后 Media 查询失败: %v", err)
	}
	publicationAfter := afterResponse.JSON200.QueryPublicationId
	if publicationAfter == publicationBefore {
		t.Fatal("按需确认后应发布新 queryPublicationId")
	}
	verifiedCount := 0
	for _, item := range afterResponse.JSON200.Media {
		if item.Id == target.Id {
			if item.ContentVerificationState != api.ContentVerified || item.Blob == nil {
				t.Fatalf("目标媒体应完成确认: %+v", item)
			}
			verifiedCount++
			continue
		}
		if item.ContentVerificationState != api.LocatedUnverified {
			t.Fatalf("非目标媒体不应被确认: %+v", item)
		}
	}
	if verifiedCount != 1 {
		t.Fatalf("应恰好一个媒体被确认，实际=%d", verifiedCount)
	}

	// queryPublicationId=publicationBefore 仍应读取确认前的快照事实：目标媒体仍是
	// located_unverified，内容端点仍返回 CONTENT_NOT_VERIFIED——证明媒体读取绑定
	// 了具体 publication，不会静默切换到之后发布的 active。
	oldSnapshot, err := client.GetMediaWithResponse(ctx, target.Id, &api.GetMediaParams{QueryPublicationId: &publicationBefore})
	if err != nil || oldSnapshot.JSON200 == nil || oldSnapshot.JSON200.ContentVerificationState != api.LocatedUnverified {
		t.Fatalf("旧 publication 快照读取应仍为 located_unverified: %v %+v", err, oldSnapshot.JSON200)
	}
	oldContent, err := client.GetMediaContentWithResponse(ctx, target.Id, &api.GetMediaContentParams{QueryPublicationId: &publicationBefore})
	if err != nil || oldContent.JSON409 == nil || oldContent.JSON409.Error.Code != api.CONTENTNOTVERIFIED {
		t.Fatalf("旧 publication 快照内容读取应返回 CONTENT_NOT_VERIFIED: %v status=%d", err, oldContent.StatusCode())
	}

	// current 模式（省略 queryPublicationId）读取最新 active publication，能看到确认后事实。
	currentSnapshot, err := client.GetMediaWithResponse(ctx, target.Id, &api.GetMediaParams{})
	if err != nil || currentSnapshot.JSON200 == nil || currentSnapshot.JSON200.ContentVerificationState != api.ContentVerified {
		t.Fatalf("current 模式应读取到确认后状态: %v %+v", err, currentSnapshot.JSON200)
	}
	if currentSnapshot.JSON200.QueryPublicationId != publicationAfter {
		t.Fatalf("current 模式响应应携带实际使用的 queryPublicationId: got=%s want=%s", currentSnapshot.JSON200.QueryPublicationId, publicationAfter)
	}

	// 显式 queryPublicationId=publicationAfter 读取目标媒体正文，应得到真实内容。
	newContent, err := client.GetMediaContentWithResponse(ctx, target.Id, &api.GetMediaContentParams{QueryPublicationId: &publicationAfter})
	if err != nil || newContent.StatusCode() != http.StatusOK || !bytes.Equal(newContent.Body, []byte("content-media-b.bin")) {
		t.Fatalf("新 publication 快照内容读取失败: %v status=%d body=%s", err, newContent.StatusCode(), newContent.Body)
	}

	// 不存在/已过期的 queryPublicationId 必须返回稳定 CURSOR_EXPIRED，不静默回退到 active。
	bogus := "qpub_018f47d2-5c16-7a44-a8a0-0000000000ff"
	expired, err := client.GetMediaWithResponse(ctx, target.Id, &api.GetMediaParams{QueryPublicationId: &bogus})
	if err != nil {
		t.Fatal(err)
	}
	var expiredBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if jsonErr := json.Unmarshal(expired.Body, &expiredBody); jsonErr != nil || expiredBody.Error.Code != string(api.CURSOREXPIRED) {
		t.Fatalf("不存在的 queryPublicationId 应返回结构化 CURSOR_EXPIRED: status=%d body=%s", expired.StatusCode(), expired.Body)
	}
}

// TestMediaVerificationJobRejectsExplicitHistoricalPublication 复现并回归本轮修复的核心
// 缺口：媒体身份（GetMediaAt）与冻结 observation 曾经可能分别来自请求 publication 与
// 当前 active publication。构造两次真实扫描——index 发布 P1（目标媒体 located_unverified）、
// 随后一次普通 incremental 重扫把该媒体完整确认为 content_verified 并发布 P2（active）——
// 使同一媒体在 P1、P2 下具有可区分的 observation，再显式针对已经不是 active 的历史
// publication P1 发起按需确认请求。修复前该请求会把从 P1 解析出的媒体身份（仍显示
// located_unverified）与从当前 active（P2，已经 content_verified）读取的 observation
// 混在一起，静默建立一个针对"已经确认完成"媒体的确认 Job；修复后必须直接拒绝为结构化
// CONFLICT，不创建 Job、不产生新的 Catalog revision/publication，也不改变 Source。
func TestMediaVerificationJobRejectsExplicitHistoricalPublication(t *testing.T) {
	ctx := context.Background()
	_, client, mutation, csrf, cleanup := newPublicationBindingServer(t)
	defer cleanup()

	sourceRoot := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(filepath.Join(sourceRoot, "work-one"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "work-one", "media-a.bin"), []byte("historical-publication-fixture"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "work-one", "metadata.json"), []byte(`{"creator":{"name":"Historical Creator"}}`), 0o400); err != nil {
		t.Fatal(err)
	}
	libraryResponse, err := client.CreateLibraryWithResponse(ctx, &api.CreateLibraryParams{XGalleryCSRF: csrf}, api.LibraryCreateRequest{Name: "Historical"}, mutation)
	if err != nil || libraryResponse.JSON201 == nil {
		t.Fatalf("创建 Library 失败: %v", err)
	}
	sourceResponse, err := client.CreateSourceWithResponse(ctx, &api.CreateSourceParams{XGalleryCSRF: csrf},
		api.SourceCreateRequest{LibraryId: libraryResponse.JSON201.Id, DisplayName: "Historical Source", RootPath: sourceRoot}, mutation)
	if err != nil || sourceResponse.JSON201 == nil {
		t.Fatalf("创建 Source 失败: %v", err)
	}
	ruleJSON, err := os.ReadFile(filepath.Join("..", "..", "rules", "testdata", "minimal-rule-package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rulePackage map[string]any
	if err := json.Unmarshal(ruleJSON, &rulePackage); err != nil {
		t.Fatal(err)
	}
	ruleResponse, err := client.CreateRuleVersionWithResponse(ctx, &api.CreateRuleVersionParams{XGalleryCSRF: csrf}, api.RuleVersionCreateRequest{Package: rulePackage}, mutation)
	if err != nil || ruleResponse.JSON201 == nil {
		t.Fatalf("创建 RuleVersion 失败: %v", err)
	}
	if _, err := client.CreateSourceRuleBindingWithResponse(ctx, &api.CreateSourceRuleBindingParams{XGalleryCSRF: csrf},
		api.SourceRuleBindingCreateRequest{SourceId: sourceResponse.JSON201.Id, SemanticHash: ruleResponse.JSON201.SemanticHash, Parameters: map[string]any{}, Priority: 0}, mutation); err != nil {
		t.Fatal(err)
	}

	// 第一次扫描：Source 尚无 publication，默认选择 index，媒体以 located_unverified 发布，
	// 建立 P1。
	firstScan, err := client.CreateScanJobWithResponse(ctx, sourceResponse.JSON201.Id, &api.CreateScanJobParams{XGalleryCSRF: csrf}, api.ScanJobCreateRequest{}, mutation)
	if err != nil || firstScan.JSON202 == nil {
		t.Fatalf("创建首次扫描失败: %v", err)
	}
	if completed := waitForJob(t, client, firstScan.JSON202.Id); string(completed.Status) != "completed" {
		t.Fatalf("首次 index 扫描未完成: %+v", completed)
	}
	worksResponse, err := client.ListWorksWithResponse(ctx, nil)
	if err != nil || worksResponse.JSON200 == nil || len(worksResponse.JSON200.Works) != 1 {
		t.Fatalf("Work 查询失败: %v", err)
	}
	mediaResponse, err := client.ListWorkMediaWithResponse(ctx, worksResponse.JSON200.Works[0].Id, &api.ListWorkMediaParams{})
	if err != nil || mediaResponse.JSON200 == nil || len(mediaResponse.JSON200.Media) != 1 {
		t.Fatalf("Media 查询失败: %v", err)
	}
	if mediaResponse.JSON200.Media[0].ContentVerificationState != api.LocatedUnverified {
		t.Fatalf("index 扫描后媒体应是 located_unverified: %+v", mediaResponse.JSON200.Media[0])
	}
	target := mediaResponse.JSON200.Media[0]
	publicationP1 := mediaResponse.JSON200.QueryPublicationId

	// 第二次扫描：Source 已发布，默认选择 incremental；目标媒体此前是 located_unverified
	// 且未被列为确认目标，普通 incremental 档案仍会为它建立 Hash Job 并完整确认，产生
	// P2（active）。P1、P2 现在对同一媒体具有可区分的 observation（located_unverified vs
	// content_verified）。
	secondScan, err := client.CreateScanJobWithResponse(ctx, sourceResponse.JSON201.Id, &api.CreateScanJobParams{XGalleryCSRF: csrf}, api.ScanJobCreateRequest{}, mutation)
	if err != nil || secondScan.JSON202 == nil {
		t.Fatalf("创建第二次扫描失败: %v", err)
	}
	if completed := waitForJob(t, client, secondScan.JSON202.Id); string(completed.Status) != "completed" {
		t.Fatalf("第二次 incremental 扫描未完成: %+v", completed)
	}
	afterSecondScan, err := client.ListWorkMediaWithResponse(ctx, worksResponse.JSON200.Works[0].Id, &api.ListWorkMediaParams{})
	if err != nil || afterSecondScan.JSON200 == nil || len(afterSecondScan.JSON200.Media) != 1 {
		t.Fatalf("第二次扫描后 Media 查询失败: %v", err)
	}
	if afterSecondScan.JSON200.Media[0].ContentVerificationState != api.ContentVerified {
		t.Fatalf("第二次扫描后媒体应已 content_verified: %+v", afterSecondScan.JSON200.Media[0])
	}
	publicationP2 := afterSecondScan.JSON200.QueryPublicationId
	if publicationP2 == publicationP1 {
		t.Fatal("第二次扫描应发布与 P1 不同的新 queryPublicationId")
	}

	jobsBefore, err := client.ListJobsWithResponse(ctx, nil)
	if err != nil || jobsBefore.JSON200 == nil {
		t.Fatalf("查询 Job 列表失败: %v", err)
	}
	jobCountBefore := len(jobsBefore.JSON200.Jobs)

	// 显式针对已经不是 active 的历史 publication P1 请求确认：必须拒绝为结构化
	// CONFLICT，不得把 P1 的媒体身份（仍显示 located_unverified）与当前 active P2 的
	// observation（已经 content_verified）混在一起静默建立 Job。
	rejected, err := client.CreateMediaVerificationJobWithResponse(ctx, target.Id, &api.CreateMediaVerificationJobParams{QueryPublicationId: &publicationP1, XGalleryCSRF: csrf}, mutation)
	if err != nil {
		t.Fatal(err)
	}
	if rejected.JSON409 == nil || rejected.JSON409.Error.Code != api.CONFLICT {
		t.Fatalf("显式历史 publication 的按需确认应返回结构化 CONFLICT: status=%d body=%s", rejected.StatusCode(), rejected.Body)
	}
	if rejected.JSON202 != nil {
		t.Fatalf("被拒绝的请求不应返回 Job: %+v", rejected.JSON202)
	}

	// 不创建新 Job、不产生新 publication、Source 状态不变。
	jobsAfter, err := client.ListJobsWithResponse(ctx, nil)
	if err != nil || jobsAfter.JSON200 == nil {
		t.Fatalf("查询 Job 列表失败: %v", err)
	}
	if len(jobsAfter.JSON200.Jobs) != jobCountBefore {
		t.Fatalf("被拒绝的历史 publication 请求不应创建新 Job: before=%d after=%d", jobCountBefore, len(jobsAfter.JSON200.Jobs))
	}
	unchanged, err := client.ListWorkMediaWithResponse(ctx, worksResponse.JSON200.Works[0].Id, &api.ListWorkMediaParams{})
	if err != nil || unchanged.JSON200 == nil || len(unchanged.JSON200.Media) != 1 {
		t.Fatalf("查询 Media 失败: %v", err)
	}
	if unchanged.JSON200.QueryPublicationId != publicationP2 {
		t.Fatalf("被拒绝的历史 publication 请求不应产生新 publication: got=%s want=%s", unchanged.JSON200.QueryPublicationId, publicationP2)
	}
	if unchanged.JSON200.Media[0].ContentVerificationState != api.ContentVerified {
		t.Fatalf("被拒绝的请求不应改变媒体确认状态: %+v", unchanged.JSON200.Media[0])
	}
}

// TestDerivedAssetInputBindsToRequestedPublication 覆盖 DerivedAsset 输入绑定：对同一
// 媒体，指定确认前的 queryPublicationId 必须因为该快照下媒体仍未 content_verified 而
// 拒绝创建；指定确认后的 queryPublicationId（或省略走 current）必须成功，且创建时
// 需要 media.derive capability。
func TestDerivedAssetInputBindsToRequestedPublication(t *testing.T) {
	ctx := context.Background()
	_, client, mutation, csrf, cleanup := newPublicationBindingServer(t)
	defer cleanup()

	sourceRoot := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(filepath.Join(sourceRoot, "work-one"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "work-one", "media.bin"), []byte("derived asset publication binding fixture"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "work-one", "metadata.json"), []byte(`{"creator":{"name":"Derived Creator"}}`), 0o400); err != nil {
		t.Fatal(err)
	}
	libraryResponse, err := client.CreateLibraryWithResponse(ctx, &api.CreateLibraryParams{XGalleryCSRF: csrf}, api.LibraryCreateRequest{Name: "DerivedPub"}, mutation)
	if err != nil || libraryResponse.JSON201 == nil {
		t.Fatalf("创建 Library 失败: %v", err)
	}
	sourceResponse, err := client.CreateSourceWithResponse(ctx, &api.CreateSourceParams{XGalleryCSRF: csrf},
		api.SourceCreateRequest{LibraryId: libraryResponse.JSON201.Id, DisplayName: "DerivedPub Source", RootPath: sourceRoot}, mutation)
	if err != nil || sourceResponse.JSON201 == nil {
		t.Fatalf("创建 Source 失败: %v", err)
	}
	ruleJSON, err := os.ReadFile(filepath.Join("..", "..", "rules", "testdata", "minimal-rule-package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rulePackage map[string]any
	if err := json.Unmarshal(ruleJSON, &rulePackage); err != nil {
		t.Fatal(err)
	}
	ruleResponse, err := client.CreateRuleVersionWithResponse(ctx, &api.CreateRuleVersionParams{XGalleryCSRF: csrf}, api.RuleVersionCreateRequest{Package: rulePackage}, mutation)
	if err != nil || ruleResponse.JSON201 == nil {
		t.Fatalf("创建 RuleVersion 失败: %v", err)
	}
	if _, err := client.CreateSourceRuleBindingWithResponse(ctx, &api.CreateSourceRuleBindingParams{XGalleryCSRF: csrf},
		api.SourceRuleBindingCreateRequest{SourceId: sourceResponse.JSON201.Id, SemanticHash: ruleResponse.JSON201.SemanticHash, Parameters: map[string]any{}, Priority: 0}, mutation); err != nil {
		t.Fatal(err)
	}
	scanJob, err := client.CreateScanJobWithResponse(ctx, sourceResponse.JSON201.Id, &api.CreateScanJobParams{XGalleryCSRF: csrf}, api.ScanJobCreateRequest{}, mutation)
	if err != nil || scanJob.JSON202 == nil {
		t.Fatalf("创建首次扫描失败: %v", err)
	}
	if completed := waitForJob(t, client, scanJob.JSON202.Id); string(completed.Status) != "completed" {
		t.Fatalf("首次 index 扫描未完成: %+v", completed)
	}
	worksResponse, err := client.ListWorksWithResponse(ctx, nil)
	if err != nil || worksResponse.JSON200 == nil || len(worksResponse.JSON200.Works) != 1 {
		t.Fatalf("Work 查询失败: %v", err)
	}
	mediaResponse, err := client.ListWorkMediaWithResponse(ctx, worksResponse.JSON200.Works[0].Id, &api.ListWorkMediaParams{})
	if err != nil || mediaResponse.JSON200 == nil || len(mediaResponse.JSON200.Media) != 1 {
		t.Fatalf("Media 查询失败: %v", err)
	}
	mediaID := mediaResponse.JSON200.Media[0].Id
	publicationBefore := mediaResponse.JSON200.QueryPublicationId

	rejectedBefore, err := client.CreateDerivedAssetWithResponse(ctx, mediaID, &api.CreateDerivedAssetParams{QueryPublicationId: &publicationBefore, XGalleryCSRF: csrf},
		api.DerivedAssetCreateRequest{TransformId: thumbnail.TransformID, TransformVersion: thumbnail.TransformVersion}, mutation)
	if err != nil || rejectedBefore.JSON409 == nil || rejectedBefore.JSON409.Error.Code != api.CONTENTNOTVERIFIED {
		t.Fatalf("确认前快照上创建 DerivedAsset 应返回 CONTENT_NOT_VERIFIED: %v status=%d body=%s", err, rejectedBefore.StatusCode(), rejectedBefore.Body)
	}

	verifyJob, err := client.CreateMediaVerificationJobWithResponse(ctx, mediaID, &api.CreateMediaVerificationJobParams{XGalleryCSRF: csrf}, mutation)
	if err != nil || verifyJob.JSON202 == nil {
		t.Fatalf("创建按需确认 Job 失败: %v", err)
	}
	if completed := waitForJob(t, client, verifyJob.JSON202.Id); string(completed.Status) != "completed" {
		t.Fatalf("按需确认 Job 未完成: %+v", completed)
	}

	created, err := client.CreateDerivedAssetWithResponse(ctx, mediaID, &api.CreateDerivedAssetParams{XGalleryCSRF: csrf},
		api.DerivedAssetCreateRequest{TransformId: thumbnail.TransformID, TransformVersion: thumbnail.TransformVersion}, mutation)
	if err != nil || created.JSON202 == nil {
		t.Fatalf("确认后 current 模式创建 DerivedAsset 失败: %v status=%d body=%s", err, created.StatusCode(), created.Body)
	}
}

// newPublicationBindingServer 建立一个与 TestScanProfileDefaultSelection... /
// TestDerivedAssetThumbnailEndToEnd 相同风格的全栈测试服务器，供本文件的多个测试复用。
func newPublicationBindingServer(t *testing.T) (*httptest.Server, *api.ClientWithResponses, api.RequestEditorFn, api.CSRFHeader, func()) {
	t.Helper()
	ctx := context.Background()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	fixedClock := clock.Fixed{Time: time.Now().UTC()}
	personal, err := auth.NewPersonal(store.Control.SQL(), fixedClock, identity.NewGenerator(fixedClock), nil)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, fixedClock, identity.NewGenerator(fixedClock))
	if err != nil {
		t.Fatal(err)
	}
	jobStore, err := jobs.NewStore(store.Control.SQL(), fixedClock, identity.NewGenerator(fixedClock))
	if err != nil {
		t.Fatal(err)
	}
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), fixedClock, identity.NewGenerator(fixedClock))
	if err != nil {
		t.Fatal(err)
	}
	hub := realtime.NewHub(fixedClock)
	scannerService, err := scanner.New(ctx, resources, jobStore, catalogStore, hub)
	if err != nil {
		t.Fatal(err)
	}
	overlayService, err := overlay.New(ctx, store.Control.SQL(), jobStore, catalogStore, fixedClock, hub)
	if err != nil {
		t.Fatal(err)
	}
	creatorsService, err := creators.New(ctx, store.Control.SQL(), jobStore, catalogStore, fixedClock, identity.NewGenerator(fixedClock), overlayService)
	if err != nil {
		t.Fatal(err)
	}
	derivedService, err := derived.New(store.Catalog.SQL(), dirs.Cache, fixedClock, nil)
	if err != nil {
		t.Fatal(err)
	}
	derivedJobService, err := derivedjob.New(jobStore, derivedService, thumbnail.New(catalogStore, resources))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.New(
		config.ModePersonal, store, fixedClock, personal, resources, jobStore, catalogStore, scannerService, overlayService, creatorsService, nil, hub,
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		httpapi.Options{Derived: derivedService, DerivedJob: derivedJobService},
	))
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := api.NewClientWithResponses(server.URL, api.WithHTTPClient(&http.Client{Jar: jar}))
	if err != nil {
		t.Fatal(err)
	}
	ctxBackground := context.Background()
	bootstrap, err := client.GetBootstrapWithResponse(ctxBackground)
	if err != nil || bootstrap.JSON200 == nil {
		t.Fatalf("bootstrap 失败: %v", err)
	}
	requestEditor := sameOrigin(server.URL)
	attempt, err := client.CreatePairingAttemptWithResponse(ctxBackground, &api.CreatePairingAttemptParams{XGalleryCSRF: bootstrap.JSON200.CsrfToken}, requestEditor)
	if err != nil || attempt.JSON201 == nil {
		t.Fatalf("创建配对 attempt 失败: %v", err)
	}
	exchange, err := client.ExchangePairingCredentialWithResponse(ctxBackground, &api.ExchangePairingCredentialParams{XGalleryCSRF: bootstrap.JSON200.CsrfToken},
		api.PairingExchangeRequest{Credential: attempt.JSON201.Credential}, requestEditor)
	if err != nil || exchange.JSON201 == nil {
		t.Fatalf("配对交换失败: %v", err)
	}
	mutation := sameOrigin(server.URL)
	cleanup := func() {
		server.Close()
		store.Close()
	}
	return server, client, mutation, exchange.JSON201.CsrfToken, cleanup
}
