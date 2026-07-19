package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/auth"
	"github.com/RecRivenVI/gallery/internal/backup"
	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/config"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/contract/realtime"
	"github.com/RecRivenVI/gallery/internal/creators"
	"github.com/RecRivenVI/gallery/internal/domain"
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
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestGeneratedClientHealthBootstrapAndAnonymousWS(t *testing.T) {
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	fixedClock := clock.Fixed{Time: time.Now().UTC()}
	personal, err := auth.NewPersonal(store.Control.SQL(), fixedClock, identity.NewGenerator(fixedClock), nil)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, fixedClock, identity.NewGenerator(fixedClock))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.New(config.ModePersonal, store, fixedClock, personal, resources, nil, nil, nil, nil, nil, nil, nil, logger))
	defer server.Close()

	client, err := api.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	health, err := client.GetHealthWithResponse(context.Background())
	if err != nil || health.JSON200 == nil || health.JSON200.Status != api.Ok {
		t.Fatalf("health 响应无效: %v status=%d", err, health.StatusCode())
	}
	bootstrap, err := client.GetBootstrapWithResponse(context.Background())
	if err != nil || bootstrap.JSON200 == nil {
		t.Fatalf("bootstrap 响应无效: %v", err)
	}
	if bootstrap.JSON200.Authenticated || len(bootstrap.JSON200.EffectiveCapabilities) != 0 {
		t.Fatal("loopback 匿名主体获得了 capability")
	}

	request, err := http.NewRequest(http.MethodGet, server.URL+"/ws/v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", server.URL)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("匿名 WS status = %d", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := fault.NewErrorValidator()
	if err != nil {
		t.Fatal(err)
	}
	if err := validator.ValidateJSON(body); err != nil {
		t.Fatalf("WS 错误不符合正式信封: %v", err)
	}
	var envelope api.ErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Error.Code != api.UNAUTHENTICATED {
		t.Fatalf("WS 错误 DTO 无效: %v", err)
	}
}

func TestPersonalPairingIsSingleUseAndRevocationInvalidatesREST(t *testing.T) {
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
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
	scannerService, err := scanner.New(context.Background(), resources, jobStore, catalogStore, hub)
	if err != nil {
		t.Fatal(err)
	}
	overlayService, err := overlay.New(context.Background(), store.Control.SQL(), jobStore, catalogStore, fixedClock, hub)
	if err != nil {
		t.Fatal(err)
	}
	creatorsService, err := creators.New(context.Background(), store.Control.SQL(), jobStore, catalogStore, fixedClock, identity.NewGenerator(fixedClock), overlayService)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.New(
		config.ModePersonal, store, fixedClock, personal, resources, jobStore, catalogStore, scannerService, overlayService, creatorsService, nil, hub,
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
	))
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{Jar: jar}
	client, err := api.NewClientWithResponses(server.URL, api.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := client.GetBootstrapWithResponse(context.Background())
	if err != nil || bootstrap.JSON200 == nil {
		t.Fatalf("bootstrap 失败: %v", err)
	}
	if len(bootstrap.JSON200.AvailableCapabilities) == 0 || len(bootstrap.JSON200.EffectiveCapabilities) != 0 {
		t.Fatal("匿名 bootstrap 未区分 available/effective capability")
	}
	requestEditor := sameOrigin(server.URL)
	attempt, err := client.CreatePairingAttemptWithResponse(context.Background(), &api.CreatePairingAttemptParams{
		XGalleryCSRF: bootstrap.JSON200.CsrfToken,
	}, requestEditor)
	if err != nil || attempt.JSON201 == nil {
		t.Fatalf("创建配对 attempt 失败: %v status=%d", err, attempt.StatusCode())
	}
	exchangeBody := api.PairingExchangeRequest{Credential: attempt.JSON201.Credential}
	exchange, err := client.ExchangePairingCredentialWithResponse(context.Background(), &api.ExchangePairingCredentialParams{
		XGalleryCSRF: bootstrap.JSON200.CsrfToken,
	}, exchangeBody, requestEditor)
	if err != nil || exchange.JSON201 == nil {
		t.Fatalf("配对交换失败: %v status=%d", err, exchange.StatusCode())
	}
	second, err := client.ExchangePairingCredentialWithResponse(context.Background(), &api.ExchangePairingCredentialParams{
		XGalleryCSRF: bootstrap.JSON200.CsrfToken,
	}, exchangeBody, requestEditor)
	if err != nil || second.JSON401 == nil || second.JSON401.Error.Code != api.PAIRINGINVALID {
		t.Fatalf("配对凭据被重复消费: %v status=%d", err, second.StatusCode())
	}
	mutation := sameOrigin(server.URL)
	libraryResponse, err := client.CreateLibraryWithResponse(context.Background(), &api.CreateLibraryParams{
		XGalleryCSRF: exchange.JSON201.CsrfToken,
	}, api.LibraryCreateRequest{Name: "Walking Skeleton"}, mutation)
	if err != nil || libraryResponse.JSON201 == nil {
		t.Fatalf("通过公开 API 创建 Library 失败: %v status=%d", err, libraryResponse.StatusCode())
	}
	sourceRoot := filepath.Join(filepath.Dir(dirs.Data), "synthetic-source")
	if err := os.MkdirAll(filepath.Join(sourceRoot, "work-one"), 0o700); err != nil {
		t.Fatal(err)
	}
	mediaContent := []byte("gallery walking skeleton media\n")
	mediaPath := filepath.Join(sourceRoot, "work-one", "media.bin")
	if err := os.WriteFile(mediaPath, mediaContent, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "work-one", "metadata.json"),
		[]byte(`{"creator":{"name":"HTTP Creator"}}`), 0o400); err != nil {
		t.Fatal(err)
	}
	sourceResponse, err := client.CreateSourceWithResponse(context.Background(), &api.CreateSourceParams{
		XGalleryCSRF: exchange.JSON201.CsrfToken,
	}, api.SourceCreateRequest{
		LibraryId: libraryResponse.JSON201.Id, DisplayName: "Synthetic", RootPath: sourceRoot,
	}, mutation)
	if err != nil || sourceResponse.JSON201 == nil {
		t.Fatalf("通过公开 API 创建 Source 失败: %v status=%d", err, sourceResponse.StatusCode())
	}
	if bytes.Contains(sourceResponse.Body, []byte(sourceRoot)) || bytes.Contains(sourceResponse.Body, []byte(`rootPath`)) {
		t.Fatal("Source 响应泄露绝对路径")
	}
	ruleJSON, err := os.ReadFile(filepath.Join("..", "..", "rules", "testdata", "minimal-rule-package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rulePackage map[string]any
	if err := json.Unmarshal(ruleJSON, &rulePackage); err != nil {
		t.Fatal(err)
	}
	ruleResponse, err := client.CreateRuleVersionWithResponse(context.Background(), &api.CreateRuleVersionParams{
		XGalleryCSRF: exchange.JSON201.CsrfToken,
	}, api.RuleVersionCreateRequest{Package: rulePackage}, mutation)
	if err != nil || ruleResponse.JSON201 == nil {
		t.Fatalf("通过公开 API 创建 RuleVersion 失败: %v status=%d body=%s", err, ruleResponse.StatusCode(), ruleResponse.Body)
	}
	bindingResponse, err := client.CreateSourceRuleBindingWithResponse(context.Background(), &api.CreateSourceRuleBindingParams{
		XGalleryCSRF: exchange.JSON201.CsrfToken,
	}, api.SourceRuleBindingCreateRequest{
		SourceId: sourceResponse.JSON201.Id, SemanticHash: ruleResponse.JSON201.SemanticHash,
		Parameters: map[string]any{}, Priority: 0,
	}, mutation)
	if err != nil || bindingResponse.JSON201 == nil {
		t.Fatalf("通过公开 API 创建 SourceRuleBinding 失败: %v status=%d body=%s", err, bindingResponse.StatusCode(), bindingResponse.Body)
	}
	websocketConnection := dialWebSocket(t, server.URL, jar)
	defer websocketConnection.CloseNow()
	var ready realtime.Envelope
	if err := wsjson.Read(context.Background(), websocketConnection, &ready); err != nil || ready.EventType != realtime.EventConnectionReady {
		t.Fatalf("WebSocket ready 错误: %+v %v", ready, err)
	}
	// 尚无 publication 的 Source 未显式指定档案时默认自动选择 index（不建立 ContentBlob）；
	// 本测试要验证媒体正文读取全链路，因此显式请求 incremental 以立即完成内容确认。
	incrementalProfile := api.ScanJobCreateRequestScanProfileIncremental
	scanResponse, err := client.CreateScanJobWithResponse(context.Background(), sourceResponse.JSON201.Id, &api.CreateScanJobParams{
		XGalleryCSRF: exchange.JSON201.CsrfToken,
	}, api.ScanJobCreateRequest{ScanProfile: &incrementalProfile}, mutation)
	if err != nil || scanResponse.JSON202 == nil {
		t.Fatalf("创建 Scan Job 失败: %v status=%d body=%s", err, scanResponse.StatusCode(), scanResponse.Body)
	}
	assertScanEvents(t, websocketConnection, ready.Sequence, scanResponse.JSON202.Id)
	completedJob := waitForJob(t, client, scanResponse.JSON202.Id)
	if string(completedJob.Status) != "completed" || completedJob.QueryPublicationId == nil {
		t.Fatalf("Scan Job 未完成: %+v", completedJob)
	}
	worksResponse, err := client.ListWorksWithResponse(context.Background(), nil)
	if err != nil || worksResponse.JSON200 == nil || len(worksResponse.JSON200.Works) != 1 {
		t.Fatalf("公开 Work 查询失败: %v status=%d", err, worksResponse.StatusCode())
	}
	mediaResponse, err := client.ListWorkMediaWithResponse(context.Background(), worksResponse.JSON200.Works[0].Id)
	if err != nil || mediaResponse.JSON200 == nil || len(mediaResponse.JSON200.Media) != 1 {
		t.Fatalf("公开 Media 查询失败: %v status=%d", err, mediaResponse.StatusCode())
	}
	mediaID := mediaResponse.JSON200.Media[0].Id
	overlayResponse, err := client.PutWorkOverlayWithResponse(context.Background(), worksResponse.JSON200.Works[0].Id,
		&api.PutWorkOverlayParams{XGalleryCSRF: exchange.JSON201.CsrfToken}, api.WorkOverlayPutRequest{
			TitleOverride: "HTTP 覆盖标题", ManualTags: []string{"HTTP 标签"}, Hidden: false,
			Favorite: true, Progress: 0.4,
		}, mutation)
	if err != nil || overlayResponse.JSON200 == nil || overlayResponse.JSON200.ProjectionJobId == nil ||
		string(overlayResponse.JSON200.ProjectionStatus) != "pending" {
		t.Fatalf("Overlay 同步写入失败: %v status=%d body=%s", err, overlayResponse.StatusCode(), overlayResponse.Body)
	}
	overlayJob := waitForJob(t, client, *overlayResponse.JSON200.ProjectionJobId)
	if string(overlayJob.Status) != "completed" || overlayJob.SourceId != nil || overlayJob.QueryPublicationId == nil {
		t.Fatalf("Overlay projection Job 未完成或伪造 Source: %+v", overlayJob)
	}
	overlaySequence := waitForWebSocketJobCompleted(t, websocketConnection, overlayJob.Id)
	searchText, tag := "覆盖标题", "HTTP 标签"
	searched, err := client.ListWorksWithResponse(context.Background(), &api.ListWorksParams{Q: &searchText, Tag: &tag})
	if err != nil || searched.JSON200 == nil || len(searched.JSON200.Works) != 1 || searched.JSON200.Works[0].Title != "HTTP 覆盖标题" {
		t.Fatalf("Overlay publication 未切换查询结果: %v status=%d body=%s", err, searched.StatusCode(), searched.Body)
	}
	state, err := client.GetWorkOverlayWithResponse(context.Background(), worksResponse.JSON200.Works[0].Id)
	if err != nil || state.JSON200 == nil || string(state.JSON200.ProjectionStatus) != "published" || !state.JSON200.Favorite {
		t.Fatalf("Overlay live/projection 状态错误: %v status=%d body=%s", err, state.StatusCode(), state.Body)
	}
	rescan, err := client.CreateScanJobWithResponse(context.Background(), sourceResponse.JSON201.Id, &api.CreateScanJobParams{
		XGalleryCSRF: exchange.JSON201.CsrfToken,
	}, api.ScanJobCreateRequest{}, mutation)
	if err != nil || rescan.JSON202 == nil {
		t.Fatalf("Overlay 后重扫创建失败: %v status=%d", err, rescan.StatusCode())
	}
	assertScanEvents(t, websocketConnection, overlaySequence, rescan.JSON202.Id)
	rescannedJob := waitForJob(t, client, rescan.JSON202.Id)
	if string(rescannedJob.Status) != "completed" {
		t.Fatalf("Overlay 后重扫失败: %+v", rescannedJob)
	}
	rescanned, err := client.ListWorksWithResponse(context.Background(), &api.ListWorksParams{Q: &searchText, Tag: &tag})
	if err != nil || rescanned.JSON200 == nil || len(rescanned.JSON200.Works) != 1 || rescanned.JSON200.CatalogRevision == searched.JSON200.CatalogRevision {
		t.Fatalf("新 Catalog 未携带 control Overlay 快照: %v status=%d body=%s", err, rescanned.StatusCode(), rescanned.Body)
	}
	headResponse, err := client.HeadMediaContentWithResponse(context.Background(), mediaID, &api.HeadMediaContentParams{})
	if err != nil || headResponse.StatusCode() != http.StatusOK || headResponse.HTTPResponse.Header.Get("Accept-Ranges") != "bytes" || headResponse.HTTPResponse.Header.Get("Content-Length") != fmt.Sprint(len(mediaContent)) {
		t.Fatalf("媒体 HEAD 错误: %v status=%d headers=%v", err, headResponse.StatusCode(), headResponse.HTTPResponse.Header)
	}
	fullResponse, err := client.GetMediaContentWithResponse(context.Background(), mediaID, &api.GetMediaContentParams{})
	if err != nil || fullResponse.StatusCode() != http.StatusOK || !bytes.Equal(fullResponse.Body, mediaContent) {
		t.Fatalf("媒体完整 GET 错误: %v status=%d body=%q", err, fullResponse.StatusCode(), fullResponse.Body)
	}
	etag := fullResponse.HTTPResponse.Header.Get("ETag")
	rangeHeader := "bytes=0-6"
	rangeResponse, err := client.GetMediaContentWithResponse(context.Background(), mediaID, &api.GetMediaContentParams{Range: &rangeHeader})
	if err != nil || rangeResponse.StatusCode() != http.StatusPartialContent || string(rangeResponse.Body) != "gallery" || rangeResponse.HTTPResponse.Header.Get("Content-Range") != fmt.Sprintf("bytes 0-6/%d", len(mediaContent)) {
		t.Fatalf("媒体 Range GET 错误: %v status=%d body=%q headers=%v", err, rangeResponse.StatusCode(), rangeResponse.Body, rangeResponse.HTTPResponse.Header)
	}
	notModified, err := client.GetMediaContentWithResponse(context.Background(), mediaID, &api.GetMediaContentParams{IfNoneMatch: &etag})
	if err != nil || notModified.StatusCode() != http.StatusNotModified || len(notModified.Body) != 0 {
		t.Fatalf("If-None-Match 错误: %v status=%d", err, notModified.StatusCode())
	}
	invalidRange := "bytes=0-1,3-4"
	invalid, err := client.GetMediaContentWithResponse(context.Background(), mediaID, &api.GetMediaContentParams{Range: &invalidRange})
	if err != nil || invalid.JSON416 == nil || invalid.JSON416.Error.Code != api.RANGEINVALID {
		t.Fatalf("无效 Range 未结构化拒绝: %v status=%d body=%s", err, invalid.StatusCode(), invalid.Body)
	}
	changed := bytes.Repeat([]byte("x"), len(mediaContent))
	if err := os.Chmod(mediaPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mediaPath, changed, 0o400); err != nil {
		t.Fatal(err)
	}
	contentChanged, err := client.GetMediaContentWithResponse(context.Background(), mediaID, &api.GetMediaContentParams{})
	if err != nil || contentChanged.JSON409 == nil || contentChanged.JSON409.Error.Code != api.CONTENTCHANGEDDURINGHASH {
		t.Fatalf("内容变化未在发送前拒绝: %v status=%d body=%s", err, contentChanged.StatusCode(), contentChanged.Body)
	}
	if err := os.Remove(mediaPath); err != nil {
		t.Fatal(err)
	}
	offline, err := client.GetMediaContentWithResponse(context.Background(), mediaID, &api.GetMediaContentParams{})
	if err != nil || offline.JSON503 == nil || offline.JSON503.Error.Code != api.MEDIAOFFLINE {
		t.Fatalf("离线媒体未返回 MEDIA_OFFLINE: %v status=%d body=%s", err, offline.StatusCode(), offline.Body)
	}
	temporaryEntries, err := os.ReadDir(dirs.Temp)
	if err != nil || len(temporaryEntries) != 0 {
		t.Fatalf("媒体临时快照未清理: %v entries=%d", err, len(temporaryEntries))
	}
	sessions, err := client.ListSessionsWithResponse(context.Background())
	if err != nil || sessions.JSON200 == nil || len(sessions.JSON200.Sessions) != 1 {
		t.Fatalf("Session 列表失败: %v status=%d", err, sessions.StatusCode())
	}
	revoke, err := client.RevokeSessionWithResponse(context.Background(), exchange.JSON201.Session.Id, &api.RevokeSessionParams{
		XGalleryCSRF: exchange.JSON201.CsrfToken,
	}, requestEditor)
	if err != nil || revoke.StatusCode() != http.StatusNoContent {
		t.Fatalf("吊销 Session 失败: %v status=%d", err, revoke.StatusCode())
	}
	var revoked realtime.Envelope
	if err := wsjson.Read(context.Background(), websocketConnection, &revoked); err != nil || revoked.EventType != realtime.EventSessionRevoked {
		t.Fatalf("Session 吊销事件错误: %+v %v", revoked, err)
	}
	if _, _, err := websocketConnection.Read(context.Background()); websocket.CloseStatus(err) != websocket.StatusCode(4401) {
		t.Fatalf("已吊销 WebSocket close status = %d err=%v", websocket.CloseStatus(err), err)
	}
	afterRevoke, err := client.ListSessionsWithResponse(context.Background())
	if err != nil || afterRevoke.JSON401 == nil {
		t.Fatalf("已吊销 Session 仍可访问 REST: %v status=%d", err, afterRevoke.StatusCode())
	}
	afterRevokeMedia, err := client.GetMediaContentWithResponse(context.Background(), mediaID, &api.GetMediaContentParams{})
	if err != nil || afterRevokeMedia.JSON401 == nil {
		t.Fatalf("已吊销 Session 仍可读取媒体: %v status=%d", err, afterRevokeMedia.StatusCode())
	}
}

// TestScanProfileDefaultSelectionValidationConflictAndContentVerificationAPI 是 scanProfile
// 相关行为的端到端 API 契约测试：非法档案值的 VALIDATION_ERROR、尚无 publication 的
// Source 默认自动选择 index、已发布 Source 显式请求 index 的结构化冲突、index → 默认
// incremental 完成内容确认、Job DTO 的 scanProfile 字段，以及未确认媒体内容端点的
// CONTENT_NOT_VERIFIED（区别于 Source 离线的 MEDIA_OFFLINE）。
func TestScanProfileDefaultSelectionValidationConflictAndContentVerificationAPI(t *testing.T) {
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
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
	scannerService, err := scanner.New(context.Background(), resources, jobStore, catalogStore, hub)
	if err != nil {
		t.Fatal(err)
	}
	overlayService, err := overlay.New(context.Background(), store.Control.SQL(), jobStore, catalogStore, fixedClock, hub)
	if err != nil {
		t.Fatal(err)
	}
	creatorsService, err := creators.New(context.Background(), store.Control.SQL(), jobStore, catalogStore, fixedClock, identity.NewGenerator(fixedClock), overlayService)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.New(
		config.ModePersonal, store, fixedClock, personal, resources, jobStore, catalogStore, scannerService, overlayService, creatorsService, nil, hub,
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
	))
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := api.NewClientWithResponses(server.URL, api.WithHTTPClient(&http.Client{Jar: jar}))
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := client.GetBootstrapWithResponse(context.Background())
	if err != nil || bootstrap.JSON200 == nil {
		t.Fatalf("bootstrap 失败: %v", err)
	}
	requestEditor := sameOrigin(server.URL)
	attempt, err := client.CreatePairingAttemptWithResponse(context.Background(), &api.CreatePairingAttemptParams{
		XGalleryCSRF: bootstrap.JSON200.CsrfToken,
	}, requestEditor)
	if err != nil || attempt.JSON201 == nil {
		t.Fatalf("创建配对 attempt 失败: %v", err)
	}
	exchange, err := client.ExchangePairingCredentialWithResponse(context.Background(), &api.ExchangePairingCredentialParams{
		XGalleryCSRF: bootstrap.JSON200.CsrfToken,
	}, api.PairingExchangeRequest{Credential: attempt.JSON201.Credential}, requestEditor)
	if err != nil || exchange.JSON201 == nil {
		t.Fatalf("配对交换失败: %v", err)
	}
	mutation := sameOrigin(server.URL)
	libraryResponse, err := client.CreateLibraryWithResponse(context.Background(), &api.CreateLibraryParams{
		XGalleryCSRF: exchange.JSON201.CsrfToken,
	}, api.LibraryCreateRequest{Name: "ScanProfile API"}, mutation)
	if err != nil || libraryResponse.JSON201 == nil {
		t.Fatalf("创建 Library 失败: %v", err)
	}
	sourceRoot := filepath.Join(filepath.Dir(dirs.Data), "scanprofile-source")
	if err := os.MkdirAll(filepath.Join(sourceRoot, "work-one"), 0o700); err != nil {
		t.Fatal(err)
	}
	mediaContent := []byte("scan profile api media content\n")
	if err := os.WriteFile(filepath.Join(sourceRoot, "work-one", "media.bin"), mediaContent, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "work-one", "metadata.json"),
		[]byte(`{"creator":{"name":"ScanProfile Creator"}}`), 0o400); err != nil {
		t.Fatal(err)
	}
	sourceResponse, err := client.CreateSourceWithResponse(context.Background(), &api.CreateSourceParams{
		XGalleryCSRF: exchange.JSON201.CsrfToken,
	}, api.SourceCreateRequest{
		LibraryId: libraryResponse.JSON201.Id, DisplayName: "ScanProfile Synthetic", RootPath: sourceRoot,
	}, mutation)
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
	ruleResponse, err := client.CreateRuleVersionWithResponse(context.Background(), &api.CreateRuleVersionParams{
		XGalleryCSRF: exchange.JSON201.CsrfToken,
	}, api.RuleVersionCreateRequest{Package: rulePackage}, mutation)
	if err != nil || ruleResponse.JSON201 == nil {
		t.Fatalf("创建 RuleVersion 失败: %v body=%s", err, ruleResponse.Body)
	}
	if _, err := client.CreateSourceRuleBindingWithResponse(context.Background(), &api.CreateSourceRuleBindingParams{
		XGalleryCSRF: exchange.JSON201.CsrfToken,
	}, api.SourceRuleBindingCreateRequest{
		SourceId: sourceResponse.JSON201.Id, SemanticHash: ruleResponse.JSON201.SemanticHash,
		Parameters: map[string]any{}, Priority: 0,
	}, mutation); err != nil {
		t.Fatal(err)
	}

	// 非法 scanProfile 必须返回结构化 VALIDATION_ERROR，不创建任何 Job。
	invalidProfile := api.ScanJobCreateRequestScanProfile("incrementall")
	invalid, err := client.CreateScanJobWithResponse(context.Background(), sourceResponse.JSON201.Id, &api.CreateScanJobParams{
		XGalleryCSRF: exchange.JSON201.CsrfToken,
	}, api.ScanJobCreateRequest{ScanProfile: &invalidProfile}, mutation)
	if err != nil || invalid.JSON400 == nil || invalid.JSON400.Error.Code != api.VALIDATIONERROR {
		t.Fatalf("非法 scanProfile 未返回 VALIDATION_ERROR: %v status=%d body=%s", err, invalid.StatusCode(), invalid.Body)
	}
	listAfterInvalid, err := client.ListJobsWithResponse(context.Background(), nil)
	if err != nil || listAfterInvalid.JSON200 == nil || len(listAfterInvalid.JSON200.Jobs) != 0 {
		t.Fatalf("非法 scanProfile 请求不应创建 Job: %v jobs=%+v", err, listAfterInvalid.JSON200)
	}

	// 未显式指定档案：尚无 publication 的 Source 应自动选择 index。
	first, err := client.CreateScanJobWithResponse(context.Background(), sourceResponse.JSON201.Id, &api.CreateScanJobParams{
		XGalleryCSRF: exchange.JSON201.CsrfToken,
	}, api.ScanJobCreateRequest{}, mutation)
	if err != nil || first.JSON202 == nil {
		t.Fatalf("首次扫描创建失败: %v status=%d", err, first.StatusCode())
	}
	firstCompleted := waitForJob(t, client, first.JSON202.Id)
	if firstCompleted.ScanProfile == nil || string(*firstCompleted.ScanProfile) != "index" {
		t.Fatalf("首次扫描 Job 应持久化实际执行的 index 档案: %+v", firstCompleted)
	}
	worksResponse, err := client.ListWorksWithResponse(context.Background(), nil)
	if err != nil || worksResponse.JSON200 == nil || len(worksResponse.JSON200.Works) != 1 {
		t.Fatalf("Work 查询失败: %v", err)
	}
	mediaResponse, err := client.ListWorkMediaWithResponse(context.Background(), worksResponse.JSON200.Works[0].Id)
	if err != nil || mediaResponse.JSON200 == nil || len(mediaResponse.JSON200.Media) != 1 {
		t.Fatalf("Media 查询失败: %v", err)
	}
	firstMedia := mediaResponse.JSON200.Media[0]
	if !firstMedia.Available || firstMedia.Blob != nil || firstMedia.ContentVerificationState != api.LocatedUnverified || firstMedia.VerifiedAt != nil {
		t.Fatalf("index 档案媒体的 API 表达错误: %+v", firstMedia)
	}
	contentResponse, err := client.GetMediaContentWithResponse(context.Background(), firstMedia.Id, &api.GetMediaContentParams{})
	if err != nil || contentResponse.JSON409 == nil || contentResponse.JSON409.Error.Code != api.CONTENTNOTVERIFIED {
		t.Fatalf("未确认媒体内容端点未返回 CONTENT_NOT_VERIFIED: %v status=%d body=%s", err, contentResponse.StatusCode(), contentResponse.Body)
	}

	// 已发布 Source 显式请求 index 必须被拒绝为结构化冲突，不创建 Job。
	indexProfile := api.ScanJobCreateRequestScanProfileIndex
	rejected, err := client.CreateScanJobWithResponse(context.Background(), sourceResponse.JSON201.Id, &api.CreateScanJobParams{
		XGalleryCSRF: exchange.JSON201.CsrfToken,
	}, api.ScanJobCreateRequest{ScanProfile: &indexProfile}, mutation)
	if err != nil || rejected.JSON409 == nil || rejected.JSON409.Error.Code != api.CONFLICT {
		t.Fatalf("已发布 Source 显式 index 未被拒绝: %v status=%d body=%s", err, rejected.StatusCode(), rejected.Body)
	}

	// 未显式指定档案：已有 publication 的 Source 应自动选择 incremental 并完成内容确认。
	second, err := client.CreateScanJobWithResponse(context.Background(), sourceResponse.JSON201.Id, &api.CreateScanJobParams{
		XGalleryCSRF: exchange.JSON201.CsrfToken,
	}, api.ScanJobCreateRequest{}, mutation)
	if err != nil || second.JSON202 == nil {
		t.Fatalf("第二次扫描创建失败: %v", err)
	}
	secondCompleted := waitForJob(t, client, second.JSON202.Id)
	if secondCompleted.ScanProfile == nil || string(*secondCompleted.ScanProfile) != "incremental" {
		t.Fatalf("已发布 Source 的默认扫描 Job 应持久化 incremental: %+v", secondCompleted)
	}
	mediaAfter, err := client.ListWorkMediaWithResponse(context.Background(), worksResponse.JSON200.Works[0].Id)
	if err != nil || mediaAfter.JSON200 == nil || len(mediaAfter.JSON200.Media) != 1 {
		t.Fatalf("确认后 Media 查询失败: %v", err)
	}
	confirmedMedia := mediaAfter.JSON200.Media[0]
	if confirmedMedia.Blob == nil || confirmedMedia.ContentVerificationState != api.ContentVerified || confirmedMedia.VerifiedAt == nil {
		t.Fatalf("index → 默认 incremental 后媒体应完成确认: %+v", confirmedMedia)
	}
	confirmedContent, err := client.GetMediaContentWithResponse(context.Background(), firstMedia.Id, &api.GetMediaContentParams{})
	if err != nil || confirmedContent.StatusCode() != http.StatusOK || !bytes.Equal(confirmedContent.Body, mediaContent) {
		t.Fatalf("确认后的内容端点应返回真实媒体正文: %v status=%d", err, confirmedContent.StatusCode())
	}
	jobsList, err := client.ListJobsWithResponse(context.Background(), nil)
	if err != nil || jobsList.JSON200 == nil {
		t.Fatalf("Job 列表查询失败: %v", err)
	}
	for _, job := range jobsList.JSON200.Jobs {
		if job.Type == "scan" && job.ScanProfile == nil {
			t.Fatalf("GET /jobs 未返回 scan Job 的 scanProfile: %+v", job)
		}
	}
}

func sameOrigin(origin string) api.RequestEditorFn {
	return func(_ context.Context, request *http.Request) error {
		request.Header.Set("Origin", origin)
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		return nil
	}
}

func waitForJob(t *testing.T, client *api.ClientWithResponses, jobID string) api.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.GetJobWithResponse(context.Background(), jobID)
		if err != nil || response.JSON200 == nil {
			t.Fatalf("Job snapshot 失败: %v status=%d", err, response.StatusCode())
		}
		if status := string(response.JSON200.Status); status == "completed" || status == "failed" || status == "cancelled" || status == "needs_repair" {
			return *response.JSON200
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Job 未在期限内终止")
	return api.Job{}
}

func dialWebSocket(t *testing.T, serverURL string, jar http.CookieJar) *websocket.Conn {
	t.Helper()
	parsed, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	header := http.Header{}
	header.Set("Origin", serverURL)
	header.Set("Sec-Fetch-Site", "same-origin")
	request := &http.Request{Header: header}
	for _, cookie := range jar.Cookies(parsed) {
		request.AddCookie(cookie)
	}
	websocketURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/ws/v1"
	connection, _, err := websocket.Dial(context.Background(), websocketURL, &websocket.DialOptions{HTTPHeader: request.Header})
	if err != nil {
		t.Fatal(err)
	}
	return connection
}

func assertScanEvents(t *testing.T, connection *websocket.Conn, previous uint64, jobID string) {
	t.Helper()
	validator, err := realtime.NewEnvelopeValidator()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[realtime.EventType]bool{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for !(seen[realtime.EventJobQueued] && seen[realtime.EventJobProgress] && seen[realtime.EventQueryPublicationPublished] && seen[realtime.EventJobCompleted]) {
		var envelope realtime.Envelope
		if err := wsjson.Read(ctx, connection, &envelope); err != nil {
			t.Fatalf("等待扫描事件: %v seen=%v", err, seen)
		}
		if envelope.Sequence <= previous {
			t.Fatalf("WebSocket sequence 未递增: %d <= %d", envelope.Sequence, previous)
		}
		previous = envelope.Sequence
		serialized, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		if err := validator.ValidateJSON(serialized); err != nil {
			t.Fatalf("事件不符合 WS Schema: %v body=%s", err, serialized)
		}
		if envelope.Scope.JobID == jobID {
			seen[envelope.EventType] = true
		}
	}
}

func waitForWebSocketJobCompleted(t *testing.T, connection *websocket.Conn, jobID string) uint64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		var envelope realtime.Envelope
		if err := wsjson.Read(ctx, connection, &envelope); err != nil {
			t.Fatalf("等待 Overlay 事件: %v", err)
		}
		if envelope.Scope.JobID == jobID && envelope.EventType == realtime.EventJobCompleted {
			return envelope.Sequence
		}
	}
}

func TestCreatorMergeAPIContract(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	fixedClock := clock.Fixed{Time: time.Now().UTC()}
	generator := identity.NewGenerator(fixedClock)
	personal, err := auth.NewPersonal(store.Control.SQL(), fixedClock, generator, nil)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, fixedClock, generator)
	if err != nil {
		t.Fatal(err)
	}
	jobStore, err := jobs.NewStore(store.Control.SQL(), fixedClock, generator)
	if err != nil {
		t.Fatal(err)
	}
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), fixedClock, generator)
	if err != nil {
		t.Fatal(err)
	}
	overlayService, err := overlay.New(ctx, store.Control.SQL(), jobStore, catalogStore, fixedClock, nil)
	if err != nil {
		t.Fatal(err)
	}
	creatorsService, err := creators.New(ctx, store.Control.SQL(), jobStore, catalogStore, fixedClock, generator, overlayService)
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.New(config.ModePersonal, store, fixedClock, personal, resources, jobStore, catalogStore, nil, overlayService, creatorsService, nil, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	server := httptest.NewServer(handler)
	defer server.Close()

	alphaID := newSeededCreator(t, ctx, store, generator, "作者甲")
	betaID := newSeededCreator(t, ctx, store, generator, "作者乙")

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := api.NewClientWithResponses(server.URL, api.WithHTTPClient(&http.Client{Jar: jar}))
	if err != nil {
		t.Fatal(err)
	}

	// 未认证时创作者列表应 401。
	anonymous, err := api.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if unauth, err := anonymous.ListCreatorsWithResponse(ctx); err != nil || unauth.JSON401 == nil {
		t.Fatalf("未认证列表未 401: %v status=%d", err, unauth.StatusCode())
	}

	csrf := pairSession(t, ctx, client, server.URL)
	editor := sameOrigin(server.URL)

	list, err := client.ListCreatorsWithResponse(ctx)
	if err != nil || list.JSON200 == nil || len(list.JSON200.Creators) != 2 {
		t.Fatalf("创作者列表错误: %v status=%d", err, list.StatusCode())
	}
	detail, err := client.GetCreatorWithResponse(ctx, alphaID)
	if err != nil || detail.JSON200 == nil || detail.JSON200.Creator.Id != alphaID || detail.JSON200.Creator.EffectiveId != alphaID {
		t.Fatalf("创作者详情错误: %v status=%d", err, detail.StatusCode())
	}
	if missing, err := client.GetCreatorWithResponse(ctx, newCreatorID(t, generator)); err != nil || missing.JSON404 == nil {
		t.Fatalf("不存在创作者未 404: %v status=%d", err, missing.StatusCode())
	}

	// 缺少 CSRF 头的合并应被拒绝。
	if noCSRF, err := client.MergeCreatorsWithResponse(ctx, &api.MergeCreatorsParams{XGalleryCSRF: ""},
		api.CreatorMergeRequest{TargetCreatorId: alphaID, AbsorbedCreatorIds: []string{betaID}}, editor); err != nil || noCSRF.JSON403 == nil {
		t.Fatalf("缺 CSRF 合并未 403: %v status=%d", err, noCSRF.StatusCode())
	}

	merge, err := client.MergeCreatorsWithResponse(ctx, &api.MergeCreatorsParams{XGalleryCSRF: csrf},
		api.CreatorMergeRequest{TargetCreatorId: alphaID, AbsorbedCreatorIds: []string{betaID}}, editor)
	if err != nil || merge.JSON201 == nil || string(merge.JSON201.Status) != "applied" ||
		merge.JSON201.TargetCreatorId != alphaID || len(merge.JSON201.AbsorbedCreatorIds) != 1 {
		t.Fatalf("合并接口错误: %v status=%d body=%s", err, merge.StatusCode(), merge.Body)
	}
	mergeID := merge.JSON201.Id

	mergedBeta, err := client.GetCreatorWithResponse(ctx, betaID)
	if err != nil || mergedBeta.JSON200 == nil || mergedBeta.JSON200.Creator.MergedInto == nil ||
		*mergedBeta.JSON200.Creator.MergedInto != alphaID || mergedBeta.JSON200.Creator.EffectiveId != alphaID {
		t.Fatalf("合并后被合并者状态错误: %v status=%d", err, mergedBeta.StatusCode())
	}
	merges, err := client.ListCreatorMergesWithResponse(ctx)
	if err != nil || merges.JSON200 == nil || len(merges.JSON200.Merges) != 1 || merges.JSON200.Merges[0].Id != mergeID {
		t.Fatalf("合并记录列表错误: %v status=%d", err, merges.StatusCode())
	}

	// 被合并者已非 live，重复合并应 409。
	if repeat, err := client.MergeCreatorsWithResponse(ctx, &api.MergeCreatorsParams{XGalleryCSRF: csrf},
		api.CreatorMergeRequest{TargetCreatorId: alphaID, AbsorbedCreatorIds: []string{betaID}}, editor); err != nil || repeat.JSON409 == nil {
		t.Fatalf("重复合并未 409: %v status=%d", err, repeat.StatusCode())
	}

	undo, err := client.UndoCreatorMergeWithResponse(ctx, mergeID, &api.UndoCreatorMergeParams{XGalleryCSRF: csrf}, editor)
	if err != nil || undo.JSON200 == nil || string(undo.JSON200.Status) != "undone" {
		t.Fatalf("撤销接口错误: %v status=%d body=%s", err, undo.StatusCode(), undo.Body)
	}
	restored, err := client.GetCreatorWithResponse(ctx, betaID)
	if err != nil || restored.JSON200 == nil || restored.JSON200.Creator.MergedInto != nil {
		t.Fatalf("撤销后被合并者未恢复 live: %v status=%d", err, restored.StatusCode())
	}
	if repeat, err := client.UndoCreatorMergeWithResponse(ctx, mergeID, &api.UndoCreatorMergeParams{XGalleryCSRF: csrf}, editor); err != nil || repeat.JSON409 == nil {
		t.Fatalf("重复撤销未 409: %v status=%d", err, repeat.StatusCode())
	}
}

func pairSession(t *testing.T, ctx context.Context, client *api.ClientWithResponses, origin string) string {
	t.Helper()
	editor := sameOrigin(origin)
	bootstrap, err := client.GetBootstrapWithResponse(ctx)
	if err != nil || bootstrap.JSON200 == nil {
		t.Fatalf("bootstrap 失败: %v", err)
	}
	attempt, err := client.CreatePairingAttemptWithResponse(ctx, &api.CreatePairingAttemptParams{XGalleryCSRF: bootstrap.JSON200.CsrfToken}, editor)
	if err != nil || attempt.JSON201 == nil {
		t.Fatalf("配对 attempt 失败: %v", err)
	}
	exchange, err := client.ExchangePairingCredentialWithResponse(ctx, &api.ExchangePairingCredentialParams{XGalleryCSRF: bootstrap.JSON200.CsrfToken},
		api.PairingExchangeRequest{Credential: attempt.JSON201.Credential}, editor)
	if err != nil || exchange.JSON201 == nil {
		t.Fatalf("配对交换失败: %v", err)
	}
	return exchange.JSON201.CsrfToken
}

func newSeededCreator(t *testing.T, ctx context.Context, store *storage.Store, generator interface {
	New(domain.IDKind) (domain.ID, error)
}, name string) string {
	t.Helper()
	id := newCreatorID(t, generator)
	if _, err := store.Control.SQL().ExecContext(ctx, `INSERT INTO canonical_creators
(creator_id, name, created_at) VALUES (?, ?, 1)`, id, name); err != nil {
		t.Fatal(err)
	}
	return id
}

func newCreatorID(t *testing.T, generator interface {
	New(domain.IDKind) (domain.ID, error)
}) string {
	t.Helper()
	id, err := generator.New(domain.IDCanonicalCreator)
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}

func TestBindingIssueQueryAPI(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	fixedClock := clock.Fixed{Time: time.Now().UTC()}
	generator := identity.NewGenerator(fixedClock)
	personal, err := auth.NewPersonal(store.Control.SQL(), fixedClock, generator, nil)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, fixedClock, generator)
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.New(config.ModePersonal, store, fixedClock, personal, resources, nil, nil, nil, nil, nil, nil, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	server := httptest.NewServer(handler)
	defer server.Close()

	newID := func(kind domain.IDKind) string {
		id, err := generator.New(kind)
		if err != nil {
			t.Fatal(err)
		}
		return id.String()
	}
	libraryID, sourceID := newID(domain.IDLibrary), newID(domain.IDSource)
	workID, issueID := newID(domain.IDCanonicalWork), newID(domain.IDBindingIssue)
	exec := func(query string, args ...any) {
		if _, err := store.Control.SQL().ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO libraries (library_id, name, created_at) VALUES (?, 'lib', 1)`, libraryID)
	exec(`INSERT INTO sources (source_id, library_id, display_name, root_path, root_key, created_at)
VALUES (?, ?, 'src', '/seed/root', '/seed/root', 1)`, sourceID, libraryID)
	exec(`INSERT INTO canonical_works (work_id, title, created_at) VALUES (?, '候选作品', 1)`, workID)
	exec(`INSERT INTO binding_issues
(issue_id, source_id, entity_type, source_key, provider_id, external_id, code,
 candidate_fingerprint, candidate_count, status, version, created_at, updated_at)
VALUES (?, ?, 'work', 'alias-new', 'example', 'post-42', 'BINDING_REVIEW_REQUIRED', ?, 1, 'open', 1, 10, 10)`,
		issueID, sourceID, workID)
	exec(`INSERT INTO binding_issue_candidates
(issue_id, ordinal, candidate_id, candidate_kind, match_signal, match_value, label)
VALUES (?, 0, ?, 'work', 'external_id', 'post-42', '候选作品')`, issueID, workID)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := api.NewClientWithResponses(server.URL, api.WithHTTPClient(&http.Client{Jar: jar}))
	if err != nil {
		t.Fatal(err)
	}
	anonymous, err := api.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if unauth, err := anonymous.ListBindingIssuesWithResponse(ctx, nil); err != nil || unauth.JSON401 == nil {
		t.Fatalf("未认证列表未 401: %v status=%d", err, unauth.StatusCode())
	}
	_ = pairSession(t, ctx, client, server.URL)

	status := api.ListBindingIssuesParamsStatus("open")
	entity := api.ListBindingIssuesParamsEntityType("work")
	list, err := client.ListBindingIssuesWithResponse(ctx, &api.ListBindingIssuesParams{Status: &status, EntityType: &entity})
	if err != nil || list.JSON200 == nil || len(list.JSON200.Issues) != 1 || list.JSON200.Issues[0].Id != issueID {
		t.Fatalf("issue 列表错误: %v status=%d body=%s", err, list.StatusCode(), list.Body)
	}
	if bytes.Contains(list.Body, []byte("/seed/root")) {
		t.Fatal("issue 列表泄露绝对路径")
	}
	detail, err := client.GetBindingIssueWithResponse(ctx, issueID)
	if err != nil || detail.JSON200 == nil || len(detail.JSON200.Candidates) != 1 ||
		detail.JSON200.Candidates[0].CandidateId != workID || detail.JSON200.Candidates[0].Label != "候选作品" {
		t.Fatalf("issue 详情候选错误: %v status=%d body=%s", err, detail.StatusCode(), detail.Body)
	}
	if missing, err := client.GetBindingIssueWithResponse(ctx, newID(domain.IDBindingIssue)); err != nil || missing.JSON404 == nil {
		t.Fatalf("不存在 issue 未 404: %v status=%d", err, missing.StatusCode())
	}
}

func TestBindingRepairAPI(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	fixedClock := clock.Fixed{Time: time.Now().UTC()}
	generator := identity.NewGenerator(fixedClock)
	personal, err := auth.NewPersonal(store.Control.SQL(), fixedClock, generator, nil)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, fixedClock, generator)
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.New(config.ModePersonal, store, fixedClock, personal, resources, nil, nil, nil, nil, nil, nil, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	server := httptest.NewServer(handler)
	defer server.Close()

	newID := func(kind domain.IDKind) string {
		id, err := generator.New(kind)
		if err != nil {
			t.Fatal(err)
		}
		return id.String()
	}
	exec := func(query string, args ...any) {
		if _, err := store.Control.SQL().ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	libraryID, sourceID := newID(domain.IDLibrary), newID(domain.IDSource)
	workA, workB := newID(domain.IDCanonicalWork), newID(domain.IDCanonicalWork)
	issueID := newID(domain.IDBindingIssue)
	exec(`INSERT INTO libraries (library_id, name, created_at) VALUES (?, 'lib', 1)`, libraryID)
	exec(`INSERT INTO sources (source_id, library_id, display_name, root_path, root_key, created_at)
VALUES (?, ?, 'src', '/seed/root', '/seed/root', 1)`, sourceID, libraryID)
	exec(`INSERT INTO canonical_works (work_id, title, created_at) VALUES (?, '作品甲', 1), (?, '作品乙', 1)`, workA, workB)
	exec(`INSERT INTO binding_issues
(issue_id, source_id, entity_type, source_key, provider_id, external_id, code,
 candidate_fingerprint, candidate_count, status, version, created_at, updated_at)
VALUES (?, ?, 'work', 'alias-new', 'example', 'post-42', 'BINDING_REVIEW_REQUIRED', ?, 2, 'open', 1, 10, 10)`,
		issueID, sourceID, workA+"\x00"+workB)
	exec(`INSERT INTO binding_issue_candidates (issue_id, ordinal, candidate_id, candidate_kind, match_signal, match_value, label)
VALUES (?, 0, ?, 'work', 'external_id', 'post-42', '作品甲'), (?, 1, ?, 'work', 'external_id', 'post-42', '作品乙')`,
		issueID, workA, issueID, workB)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := api.NewClientWithResponses(server.URL, api.WithHTTPClient(&http.Client{Jar: jar}))
	if err != nil {
		t.Fatal(err)
	}
	csrf := pairSession(t, ctx, client, server.URL)
	editor := sameOrigin(server.URL)

	// 缺少 CSRF 的修复被拒绝。
	if noCSRF, err := client.ResolveBindingIssueWithResponse(ctx, issueID, &api.ResolveBindingIssueParams{XGalleryCSRF: ""},
		api.BindingIssueResolveRequest{Decision: "bind_existing", TargetId: &workA, Version: 1}, editor); err != nil || noCSRF.JSON403 == nil {
		t.Fatalf("缺 CSRF 修复未 403: %v status=%d", err, noCSRF.StatusCode())
	}
	// 过时 version 冲突。
	if stale, err := client.ResolveBindingIssueWithResponse(ctx, issueID, &api.ResolveBindingIssueParams{XGalleryCSRF: csrf},
		api.BindingIssueResolveRequest{Decision: "bind_existing", TargetId: &workA, Version: 99}, editor); err != nil || stale.JSON409 == nil {
		t.Fatalf("过时 version 未 409: %v status=%d", err, stale.StatusCode())
	}
	// bind_existing 修复。
	resolved, err := client.ResolveBindingIssueWithResponse(ctx, issueID, &api.ResolveBindingIssueParams{XGalleryCSRF: csrf},
		api.BindingIssueResolveRequest{Decision: "bind_existing", TargetId: &workA, Version: 1}, editor)
	if err != nil || resolved.JSON200 == nil || string(resolved.JSON200.Status) != "resolved" ||
		resolved.JSON200.ResolvedTargetId == nil || *resolved.JSON200.ResolvedTargetId != workA {
		t.Fatalf("bind_existing 接口失败: %v status=%d body=%s", err, resolved.StatusCode(), resolved.Body)
	}
	var activeCount int
	_ = store.Control.SQL().QueryRowContext(ctx, `SELECT count(*) FROM work_bindings
WHERE source_id=? AND source_key='alias-new' AND work_id=? AND status='active'`, sourceID, workA).Scan(&activeCount)
	if activeCount != 1 {
		t.Fatalf("修复未建立 active binding: %d", activeCount)
	}

	// unbind-work + undo。
	unbindKey := "clean-key"
	exec(`INSERT INTO work_bindings
(binding_id, source_id, source_key, work_id, identity_version, status, last_seen_generation, created_at, updated_at)
VALUES (?, ?, ?, ?, 1, 'active', 0, 1, 1)`, newID(domain.IDWorkBinding), sourceID, unbindKey, workB)
	unbind, err := client.UnbindWorkWithResponse(ctx, &api.UnbindWorkParams{XGalleryCSRF: csrf},
		api.BindingUnbindRequest{SourceId: sourceID, SourceKey: unbindKey}, editor)
	if err != nil || unbind.JSON200 == nil || unbind.JSON200.CanonicalId != workB || string(unbind.JSON200.EntityKind) != "work" {
		t.Fatalf("unbind-work 接口失败: %v status=%d body=%s", err, unbind.StatusCode(), unbind.Body)
	}
	undo, err := client.UndoManualUnbindWithResponse(ctx, &api.UndoManualUnbindParams{XGalleryCSRF: csrf},
		api.BindingUnbindRequest{SourceId: sourceID, SourceKey: unbindKey}, editor)
	if err != nil || undo.JSON200 == nil || undo.JSON200.CanonicalId != workB {
		t.Fatalf("undo-unbind 接口失败: %v status=%d body=%s", err, undo.StatusCode(), undo.Body)
	}

	// dismiss + reopen 另一个 issue。
	issue2 := newID(domain.IDBindingIssue)
	exec(`INSERT INTO binding_issues
(issue_id, source_id, entity_type, source_key, code, candidate_fingerprint, candidate_count, status, version, created_at, updated_at)
VALUES (?, ?, 'work', 'alias-2', 'BINDING_REVIEW_REQUIRED', '', 2, 'open', 1, 20, 20)`, issue2, sourceID)
	dismissed, err := client.DismissBindingIssueWithResponse(ctx, issue2, &api.DismissBindingIssueParams{XGalleryCSRF: csrf},
		api.BindingIssueVersionRequest{Version: 1}, editor)
	if err != nil || dismissed.JSON200 == nil || string(dismissed.JSON200.Status) != "dismissed" {
		t.Fatalf("dismiss 接口失败: %v status=%d body=%s", err, dismissed.StatusCode(), dismissed.Body)
	}
	reopened, err := client.ReopenBindingIssueWithResponse(ctx, issue2, &api.ReopenBindingIssueParams{XGalleryCSRF: csrf},
		api.BindingIssueVersionRequest{Version: dismissed.JSON200.Version}, editor)
	if err != nil || reopened.JSON200 == nil || string(reopened.JSON200.Status) != "open" {
		t.Fatalf("reopen 接口失败: %v status=%d body=%s", err, reopened.StatusCode(), reopened.Body)
	}
}

func TestOrphanCandidateAPIContract(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	fixedClock := clock.Fixed{Time: time.Now().UTC()}
	generator := identity.NewGenerator(fixedClock)
	personal, err := auth.NewPersonal(store.Control.SQL(), fixedClock, generator, nil)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, fixedClock, generator)
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.New(config.ModePersonal, store, fixedClock, personal, resources, nil, nil, nil, nil, nil, nil, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	server := httptest.NewServer(handler)
	defer server.Close()

	newID := func(kind domain.IDKind) string {
		id, err := generator.New(kind)
		if err != nil {
			t.Fatal(err)
		}
		return id.String()
	}
	exec := func(query string, args ...any) {
		if _, err := store.Control.SQL().ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	libraryID, sourceID := newID(domain.IDLibrary), newID(domain.IDSource)
	workID, bindingID := newID(domain.IDCanonicalWork), newID(domain.IDWorkBinding)
	exec(`INSERT INTO libraries (library_id, name, created_at) VALUES (?, 'lib', 1)`, libraryID)
	exec(`INSERT INTO sources (source_id, library_id, display_name, root_path, root_key, created_at)
VALUES (?, ?, 'src', '/seed/root', '/seed/root', 1)`, sourceID, libraryID)
	exec(`INSERT INTO canonical_works (work_id, title, created_at) VALUES (?, '孤立作品', 1)`, workID)
	exec(`INSERT INTO work_bindings
(binding_id, source_id, source_key, work_id, identity_version, status, missed_scans, last_seen_generation, created_at, updated_at)
VALUES (?, ?, 'gone-key', ?, 1, 'orphan_candidate', 3, 0, 1, 1)`, bindingID, sourceID, workID)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := api.NewClientWithResponses(server.URL, api.WithHTTPClient(&http.Client{Jar: jar}))
	if err != nil {
		t.Fatal(err)
	}
	csrf := pairSession(t, ctx, client, server.URL)
	editor := sameOrigin(server.URL)

	listed, err := client.ListOrphanCandidatesWithResponse(ctx, &api.ListOrphanCandidatesParams{}, editor)
	if err != nil || listed.JSON200 == nil || len(listed.JSON200.Candidates) != 1 {
		t.Fatalf("orphan 列表失败: %v status=%d body=%s", err, listed.StatusCode(), listed.Body)
	}
	candidate := listed.JSON200.Candidates[0]
	if candidate.BindingId != bindingID || string(candidate.EntityType) != "work" ||
		candidate.CanonicalId != workID || candidate.CanonicalLabel != "孤立作品" ||
		candidate.MissedScans != 3 || candidate.RetentionThreshold != 3 {
		t.Fatalf("orphan 候选字段错误: %+v", candidate)
	}

	// 缺少 CSRF 的决策被拒绝。
	if noCSRF, err := client.DecideOrphanCandidateWithResponse(ctx, bindingID, &api.DecideOrphanCandidateParams{XGalleryCSRF: ""},
		api.OrphanDecisionRequest{Decision: "retain"}, editor); err != nil || noCSRF.JSON403 == nil {
		t.Fatalf("缺 CSRF 决策未 403: %v status=%d", err, noCSRF.StatusCode())
	}
	// confirm_orphaned 决策。
	decided, err := client.DecideOrphanCandidateWithResponse(ctx, bindingID, &api.DecideOrphanCandidateParams{XGalleryCSRF: csrf},
		api.OrphanDecisionRequest{Decision: "confirm_orphaned"}, editor)
	if err != nil || decided.JSON200 == nil || string(decided.JSON200.NewStatus) != "orphaned" ||
		decided.JSON200.CanonicalId != workID {
		t.Fatalf("confirm_orphaned 接口失败: %v status=%d body=%s", err, decided.StatusCode(), decided.Body)
	}
	// 确认孤立不得删除 Canonical 作品。
	var count int
	_ = store.Control.SQL().QueryRowContext(ctx, `SELECT count(*) FROM canonical_works WHERE work_id=?`, workID).Scan(&count)
	if count != 1 {
		t.Fatalf("确认孤立后 Canonical 作品被删: %d", count)
	}
	// 已非候选，再次决策冲突。
	if again, err := client.DecideOrphanCandidateWithResponse(ctx, bindingID, &api.DecideOrphanCandidateParams{XGalleryCSRF: csrf},
		api.OrphanDecisionRequest{Decision: "retain"}, editor); err != nil || again.JSON409 == nil {
		t.Fatalf("非候选决策未 409: %v status=%d", err, again.StatusCode())
	}
}

func TestControlBackupEndpoints(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	fixedClock := clock.Fixed{Time: time.Now().UTC()}
	generator := identity.NewGenerator(fixedClock)
	personal, err := auth.NewPersonal(store.Control.SQL(), fixedClock, generator, nil)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, fixedClock, generator)
	if err != nil {
		t.Fatal(err)
	}
	jobStore, err := jobs.NewStore(store.Control.SQL(), fixedClock, generator)
	if err != nil {
		t.Fatal(err)
	}
	backupService, err := backup.New(ctx, store.Control, jobStore, dirs, fixedClock, generator, "test-1.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.New(config.ModePersonal, store, fixedClock, personal, resources, jobStore, nil, nil, nil, nil, backupService, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	server := httptest.NewServer(handler)
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := api.NewClientWithResponses(server.URL, api.WithHTTPClient(&http.Client{Jar: jar}))
	if err != nil {
		t.Fatal(err)
	}

	// 未认证时不得创建备份。
	anonymous, err := api.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if unauth, err := anonymous.CreateControlBackupWithResponse(ctx, &api.CreateControlBackupParams{XGalleryCSRF: "x"}, sameOrigin(server.URL)); err != nil || unauth.JSON401 == nil {
		t.Fatalf("未认证创建备份未 401: %v status=%d", err, unauth.StatusCode())
	}

	csrf := pairSession(t, ctx, client, server.URL)
	editor := sameOrigin(server.URL)

	// 缺少 CSRF 头应 403。
	if noCSRF, err := client.CreateControlBackupWithResponse(ctx, &api.CreateControlBackupParams{XGalleryCSRF: ""}, editor); err != nil || noCSRF.JSON403 == nil {
		t.Fatalf("缺 CSRF 创建备份未 403: %v status=%d", err, noCSRF.StatusCode())
	}

	created, err := client.CreateControlBackupWithResponse(ctx, &api.CreateControlBackupParams{XGalleryCSRF: csrf}, editor)
	if err != nil || created.JSON202 == nil || created.JSON202.Type != "control_backup" {
		t.Fatalf("创建备份失败: %v status=%d", err, created.StatusCode())
	}
	jobID := created.JSON202.Id

	// 轮询直到备份 Job 完成。
	deadline := time.Now().Add(10 * time.Second)
	for {
		snapshot, err := client.GetJobWithResponse(ctx, jobID)
		if err != nil || snapshot.JSON200 == nil {
			t.Fatalf("读取备份 Job 失败: %v status=%d", err, snapshot.StatusCode())
		}
		if snapshot.JSON200.Status == "completed" {
			break
		}
		if snapshot.JSON200.Status == "failed" || snapshot.JSON200.Status == "cancelled" {
			t.Fatalf("备份 Job 异常终止: %s", snapshot.JSON200.Status)
		}
		if time.Now().After(deadline) {
			t.Fatalf("备份 Job 超时未完成，最后状态 %s", snapshot.JSON200.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	list, err := client.ListControlBackupsWithResponse(ctx)
	if err != nil || list.JSON200 == nil || len(list.JSON200.Backups) != 1 {
		t.Fatalf("备份列表错误: %v status=%d", err, list.StatusCode())
	}
	manifest := list.JSON200.Backups[0]
	if manifest.Role != "control" || manifest.Database.ChecksumAlgorithm != "sha256" {
		t.Fatalf("备份 manifest 字段错误: %+v", manifest)
	}

	got, err := client.GetControlBackupWithResponse(ctx, manifest.BackupId)
	if err != nil || got.JSON200 == nil || got.JSON200.BackupId != manifest.BackupId {
		t.Fatalf("读取单个备份错误: %v status=%d", err, got.StatusCode())
	}
	if missing, err := client.GetControlBackupWithResponse(ctx, "bkp_00000000-0000-7000-8000-000000000000"); err != nil || missing.JSON404 == nil {
		t.Fatalf("未知备份未 404: %v status=%d", err, missing.StatusCode())
	}

	// 恢复 Dry Run 验证。
	verify, err := client.VerifyControlRestoreWithResponse(ctx, &api.VerifyControlRestoreParams{XGalleryCSRF: csrf},
		api.ControlRestoreRequest{BackupId: manifest.BackupId}, editor)
	if err != nil || verify.JSON200 == nil || !verify.JSON200.Compatible {
		t.Fatalf("恢复验证失败: %v status=%d", err, verify.StatusCode())
	}
	if unknownVerify, err := client.VerifyControlRestoreWithResponse(ctx, &api.VerifyControlRestoreParams{XGalleryCSRF: csrf},
		api.ControlRestoreRequest{BackupId: "bkp_00000000-0000-7000-8000-000000000000"}, editor); err != nil || unknownVerify.JSON404 == nil {
		t.Fatalf("未知备份验证未 404: %v status=%d", err, unknownVerify.StatusCode())
	}

	// 登记待应用恢复请求。
	request, err := client.RequestControlRestoreWithResponse(ctx, &api.RequestControlRestoreParams{XGalleryCSRF: csrf},
		api.ControlRestoreRequest{BackupId: manifest.BackupId}, editor)
	if err != nil || request.JSON202 == nil || !request.JSON202.RestartRequired {
		t.Fatalf("恢复请求失败: %v status=%d", err, request.StatusCode())
	}
	if _, err := os.Stat(filepath.Join(dirs.State, "restore-pending.json")); err != nil {
		t.Fatalf("恢复请求未登记待应用标记: %v", err)
	}

	// 缺少 admin.restore 之外的 capability 或 CSRF 被拒。
	if noCSRF, err := client.RequestControlRestoreWithResponse(ctx, &api.RequestControlRestoreParams{XGalleryCSRF: ""},
		api.ControlRestoreRequest{BackupId: manifest.BackupId}, editor); err != nil || noCSRF.JSON403 == nil {
		t.Fatalf("缺 CSRF 恢复请求未 403: %v status=%d", err, noCSRF.StatusCode())
	}
}

func TestSourceStructureDecisionAPI(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	fixedClock := clock.Fixed{Time: time.Now().UTC()}
	generator := identity.NewGenerator(fixedClock)
	personal, err := auth.NewPersonal(store.Control.SQL(), fixedClock, generator, nil)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, fixedClock, generator)
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.New(config.ModePersonal, store, fixedClock, personal, resources, nil, nil, nil, nil, nil, nil, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	server := httptest.NewServer(handler)
	defer server.Close()

	newID := func(kind domain.IDKind) string {
		id, err := generator.New(kind)
		if err != nil {
			t.Fatal(err)
		}
		return id.String()
	}
	exec := func(query string, args ...any) {
		if _, err := store.Control.SQL().ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	libraryID, sourceID := newID(domain.IDLibrary), newID(domain.IDSource)
	originWork, issueID := newID(domain.IDCanonicalWork), newID(domain.IDBindingIssue)
	exec(`INSERT INTO libraries (library_id, name, created_at) VALUES (?, 'lib', 1)`, libraryID)
	exec(`INSERT INTO sources (source_id, library_id, display_name, root_path, root_key, created_at)
VALUES (?, ?, 'src', '/seed/root', '/seed/root', 1)`, sourceID, libraryID)
	exec(`INSERT INTO canonical_works (work_id, title, created_at) VALUES (?, '原作品', 1)`, originWork)
	// 模拟检测产生的拆分审查 issue：原 SourceWork wkA 拆为 wkA1、wkA2。
	exec(`INSERT INTO binding_issues
(issue_id, source_id, entity_type, structure_kind, source_key, code, candidate_fingerprint, candidate_count, status, version, created_at, updated_at)
VALUES (?, ?, 'work', 'split', 'wkA', 'SOURCE_WORK_SPLIT_REVIEW_REQUIRED', 'split|wkA|wkA1\x00wkA2', 3, 'open', 1, 10, 10)`,
		issueID, sourceID, originWork)
	exec(`INSERT INTO binding_issue_candidates (issue_id, ordinal, candidate_id, candidate_kind, match_signal, match_value, label)
VALUES (?, 0, ?, 'work', 'origin_canonical', 'wkA', '原作品'),
       (?, 1, 'wkA1', 'work', 'new_source_work', '', ''),
       (?, 2, 'wkA2', 'work', 'new_source_work', '', '')`, issueID, originWork, issueID, issueID)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := api.NewClientWithResponses(server.URL, api.WithHTTPClient(&http.Client{Jar: jar}))
	if err != nil {
		t.Fatal(err)
	}
	csrf := pairSession(t, ctx, client, server.URL)
	editor := sameOrigin(server.URL)

	// issue 详情应携带 structureKind。
	detail, err := client.GetBindingIssueWithResponse(ctx, issueID)
	if err != nil || detail.JSON200 == nil || detail.JSON200.StructureKind == nil || string(*detail.JSON200.StructureKind) != "split" {
		t.Fatalf("issue structureKind 缺失: %v status=%d body=%s", err, detail.StatusCode(), detail.Body)
	}

	// 缺 CSRF 决策被拒绝。
	target := "wkA1"
	if noCSRF, err := client.ResolveSourceStructureIssueWithResponse(ctx, issueID, &api.ResolveSourceStructureIssueParams{XGalleryCSRF: ""},
		api.SourceStructureDecisionRequest{Action: "split_inherit", TargetSourceKey: &target, Version: 1}, editor); err != nil || noCSRF.JSON403 == nil {
		t.Fatalf("缺 CSRF 决策未 403: %v status=%d", err, noCSRF.StatusCode())
	}
	// kind 不匹配的合并动作应校验失败。
	if bad, err := client.ResolveSourceStructureIssueWithResponse(ctx, issueID, &api.ResolveSourceStructureIssueParams{XGalleryCSRF: csrf},
		api.SourceStructureDecisionRequest{Action: "merge_create_new", Version: 1}, editor); err != nil || bad.JSON400 == nil {
		t.Fatalf("kind 不匹配未 400: %v status=%d body=%s", err, bad.StatusCode(), bad.Body)
	}
	// split_inherit 决策成功。
	resolved, err := client.ResolveSourceStructureIssueWithResponse(ctx, issueID, &api.ResolveSourceStructureIssueParams{XGalleryCSRF: csrf},
		api.SourceStructureDecisionRequest{Action: "split_inherit", TargetSourceKey: &target, Version: 1}, editor)
	if err != nil || resolved.JSON200 == nil || string(resolved.JSON200.Status) != "applied" ||
		string(resolved.JSON200.Kind) != "split" || resolved.JSON200.TargetWorkId == nil || *resolved.JSON200.TargetWorkId != originWork {
		t.Fatalf("split_inherit 接口失败: %v status=%d body=%s", err, resolved.StatusCode(), resolved.Body)
	}
	decisionID := resolved.JSON200.DecisionId

	// 列表与详情。
	appliedStatus := api.ListSourceStructureDecisionsParamsStatus("applied")
	list, err := client.ListSourceStructureDecisionsWithResponse(ctx, &api.ListSourceStructureDecisionsParams{SourceId: &sourceID, Status: &appliedStatus})
	if err != nil || list.JSON200 == nil || len(list.JSON200.Decisions) != 1 || list.JSON200.Decisions[0].DecisionId != decisionID {
		t.Fatalf("决策列表错误: %v status=%d body=%s", err, list.StatusCode(), list.Body)
	}
	if bytes.Contains(list.Body, []byte("/seed/root")) {
		t.Fatal("决策列表泄露绝对路径")
	}

	// 重扫前撤销成功（clean）。
	undone, err := client.UndoSourceStructureDecisionWithResponse(ctx, decisionID, &api.UndoSourceStructureDecisionParams{XGalleryCSRF: csrf},
		api.BindingIssueVersionRequest{Version: 1}, editor)
	if err != nil || undone.JSON200 == nil || string(undone.JSON200.Status) != "undone" {
		t.Fatalf("撤销接口失败: %v status=%d body=%s", err, undone.StatusCode(), undone.Body)
	}
	// issue 应重新打开。
	reopened, err := client.GetBindingIssueWithResponse(ctx, issueID)
	if err != nil || reopened.JSON200 == nil || string(reopened.JSON200.Status) != "open" {
		t.Fatalf("撤销后 issue 未重开: %v body=%s", err, reopened.Body)
	}
}
