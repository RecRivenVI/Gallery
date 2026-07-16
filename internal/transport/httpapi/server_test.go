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
	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/config"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/contract/realtime"
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
	server := httptest.NewServer(httpapi.New(config.ModePersonal, store, fixedClock, personal, resources, nil, nil, nil, nil, nil, logger))
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
	server := httptest.NewServer(httpapi.New(
		config.ModePersonal, store, fixedClock, personal, resources, jobStore, catalogStore, scannerService, overlayService, hub,
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
	scanResponse, err := client.CreateScanJobWithResponse(context.Background(), sourceResponse.JSON201.Id, &api.CreateScanJobParams{
		XGalleryCSRF: exchange.JSON201.CsrfToken,
	}, mutation)
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
	}, mutation)
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
