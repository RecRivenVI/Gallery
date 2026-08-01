package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	api "github.com/RecRivenVI/gallery/api"
	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/auth"
	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/config"
	"github.com/RecRivenVI/gallery/internal/creators"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/overlay"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/querytext"
	"github.com/RecRivenVI/gallery/internal/storage"
	"github.com/RecRivenVI/gallery/internal/transport/httpapi"
)

// TestAggregateCoversRespectMediaAuthorization 锁定 Creator/Library 身份与媒体资源的
// 独立授权边界：library.read 仍可看到身份，但只有同一 Source 上同时具备 media.read
// 才能得到封面；deny 与 Token scope 排除全局胜出项后，必须回退到仍获授权的候选。
func TestAggregateCoversRespectMediaAuthorization(t *testing.T) {
	server, store, catalogStore, generator := newLANAggregateCoverServer(t)
	owner, csrf := establishLANOwner(t, server)
	libraryID := createLibrary(t, owner, server, csrf, "Aggregate cover authorization")
	sourceA := createQueryAuthorizationSource(t, owner, server.URL, csrf, libraryID, "cover-source-a")
	sourceB := createQueryAuthorizationSource(t, owner, server.URL, csrf, libraryID, "cover-source-b")
	creatorID, mediaA, mediaB, publicationID := seedAuthorizedAggregateCovers(
		t, store, catalogStore, generator, libraryID, sourceA, sourceB,
	)

	// Owner 基线能看到全局更新的 Source B 封面，证明 fixture 确实会触发授权重选。
	assertSessionAggregateCover(t, owner, server.URL, libraryID, creatorID, mediaB, publicationID)

	readOnly := createAndLoginQueryAuthorizationUser(t, owner, server.URL, csrf, "cover-read-only", []string{"viewer"}, []any{
		map[string]any{
			"effect": "allow", "capability": "library.read",
			"scope": map[string]any{"kind": "library", "id": libraryID},
		},
	})
	// 身份仍可见，但没有独立 media.read 时两个 DTO 都不得携带媒体 ID 或快照 ID。
	assertSessionAggregateCover(t, readOnly, server.URL, libraryID, creatorID, "", "")

	denyNewest := createAndLoginQueryAuthorizationUser(t, owner, server.URL, csrf, "cover-deny-newest", []string{"owner"}, []any{
		map[string]any{
			"effect": "deny", "capability": "media.read",
			"scope": map[string]any{"kind": "source", "id": sourceB},
		},
	})
	// Source B 的 deny 必须同时裁剪 Creator 与 Library，并回退到 Source A，而不是只清空封面。
	assertSessionAggregateCover(t, denyNewest, server.URL, libraryID, creatorID, mediaA, publicationID)

	denyNewestIdentity := createAndLoginQueryAuthorizationUser(t, owner, server.URL, csrf, "cover-deny-source-read", []string{"owner"}, []any{
		map[string]any{
			"effect": "deny", "capability": "library.read",
			"scope": map[string]any{"kind": "source", "id": sourceB},
		},
	})
	// library.read 的逐 Source deny 同样参与媒体候选求交；Library 自身仍可见，但不得借用
	// 其内部已被拒绝的 Source B 代表封面。
	assertSessionAggregateCover(t, denyNewestIdentity, server.URL, libraryID, creatorID, mediaA, publicationID)

	tokenResponse := requestJSON(t, owner, http.MethodPost, server.URL+"/api/v1/api-tokens", server.URL, csrf,
		map[string]any{
			"name": "aggregate-cover-source-a", "capabilities": []string{"library.read", "media.read"},
			"scopes": []map[string]string{{"kind": "source", "id": sourceA}},
		})
	tokenBody := readAndClose(t, tokenResponse)
	var token struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(tokenBody, &token); err != nil || tokenResponse.StatusCode != http.StatusCreated || token.Secret == "" {
		t.Fatalf("创建聚合封面 Token 失败: status=%d err=%v body=%s", tokenResponse.StatusCode, err, tokenBody)
	}
	assertBearerCreatorCover(t, token.Secret, server.URL, creatorID, mediaA, publicationID)

	// 分页浏览必须在 LIMIT 前完成 Source 授权；否则一页可能先被未授权身份占满，再在
	// transport 层裁成空页。这里给 Source A/B 各加一个独占作者，Source A Token 应连续
	// 取得“共享作者 + A 独占作者”，永远看不到 B 独占作者，sourceCount 也只能是 1。
	creatorAOnly := seedAggregateBoundCreator(t, store, generator, sourceA, "A 独占作者")
	creatorBOnly := seedAggregateBoundCreator(t, store, generator, sourceB, "B 独占作者")
	endpoint := server.URL + "/api/v1/creators?includeMerged=false&sourceId=" + url.QueryEscape(sourceA) + "&sort=name_asc&limit=1"
	seen := make(map[string]api.Creator)
	for pageNumber := 0; pageNumber < 4; pageNumber++ {
		var page api.CreatorListResponse
		if status := getAggregateJSON(t, nil, token.Secret, endpoint, &page); status != http.StatusOK {
			t.Fatalf("Creator 分页失败: page=%d status=%d", pageNumber, status)
		}
		for _, creator := range page.Creators {
			seen[creator.Id] = creator
		}
		if page.NextCursor == nil {
			break
		}
		endpoint = server.URL + "/api/v1/creators?includeMerged=false&sourceId=" + url.QueryEscape(sourceA) +
			"&sort=name_asc&limit=1&cursor=" + url.QueryEscape(*page.NextCursor)
	}
	if len(seen) != 2 || seen[creatorID].SourceCount != 1 || seen[creatorAOnly].SourceCount != 1 {
		t.Fatalf("Source A Creator 分页集合错误: %+v", seen)
	}
	if _, leaked := seen[creatorBOnly]; leaked {
		t.Fatalf("Source A Token 泄露 Source B 独占 Creator: %+v", seen[creatorBOnly])
	}

	// Source 范围 Token 不自动获得整个 Library 身份；列表为空且详情保持 404。
	var libraries api.LibraryListResponse
	if status := getAggregateJSON(t, nil, token.Secret, server.URL+"/api/v1/libraries", &libraries); status != http.StatusOK || len(libraries.Libraries) != 0 {
		t.Fatalf("Source Token Library 列表未收敛为空: status=%d body=%+v", status, libraries)
	}
	var library api.Library
	if status := getAggregateJSON(t, nil, token.Secret, server.URL+"/api/v1/libraries/"+libraryID, &library); status != http.StatusNotFound {
		t.Fatalf("Source Token Library 详情未隐藏: status=%d body=%+v", status, library)
	}
}

func seedAggregateBoundCreator(t *testing.T, store *storage.Store, generator identity.Generator, sourceID, name string) string {
	t.Helper()
	ctx := context.Background()
	creatorID := newAggregateCoverID(t, generator, domain.IDCanonicalCreator)
	bindingID := newAggregateCoverID(t, generator, domain.IDCreatorBinding)
	if _, err := store.Control.SQL().ExecContext(ctx,
		"INSERT INTO canonical_creators (creator_id, name, sort_name_key, created_at) VALUES (?, ?, ?, 1)",
		creatorID, name, querytext.NaturalSortKey(name)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Control.SQL().ExecContext(ctx, `INSERT INTO creator_bindings
(binding_id, source_id, provider_id, external_id, source_key, creator_id, identity_version,
 status, last_seen_generation, created_at, updated_at)
VALUES (?, ?, 'test', ?, ?, ?, 1, 'active', 1, 1, 1)`,
		bindingID, sourceID, creatorID, "creator-"+creatorID, creatorID); err != nil {
		t.Fatal(err)
	}
	return creatorID
}

func newLANAggregateCoverServer(t *testing.T) (*httptest.Server, *storage.Store, *catalog.Store, identity.Generator) {
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
	t.Cleanup(func() { _ = store.Close() })
	fixed := clock.Fixed{Time: time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)}
	generator := identity.NewGenerator(fixed)
	manager, err := auth.NewPersonal(store.Control.SQL(), fixed, generator, nil)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, fixed, generator)
	if err != nil {
		t.Fatal(err)
	}
	jobStore, err := jobs.NewStore(store.Control.SQL(), fixed, generator)
	if err != nil {
		t.Fatal(err)
	}
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), fixed, generator)
	if err != nil {
		t.Fatal(err)
	}
	overlayService, err := overlay.New(ctx, store.Control.SQL(), jobStore, catalogStore, fixed, nil)
	if err != nil {
		t.Fatal(err)
	}
	creatorsService, err := creators.New(ctx, store.Control.SQL(), jobStore, catalogStore, fixed, generator, overlayService)
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.New(config.ModeLAN, store, fixed, manager, resources, jobStore, catalogStore,
		nil, overlayService, creatorsService, nil, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server, store, catalogStore, generator
}

func seedAuthorizedAggregateCovers(
	t *testing.T,
	store *storage.Store,
	catalogStore *catalog.Store,
	generator identity.Generator,
	libraryID, sourceA, sourceB string,
) (creatorID, mediaA, mediaB, publicationID string) {
	t.Helper()
	ctx := context.Background()
	creatorID = newAggregateCoverID(t, generator, domain.IDCanonicalCreator)
	if _, err := store.Control.SQL().ExecContext(ctx,
		"INSERT INTO canonical_creators (creator_id, name, sort_name_key, created_at) VALUES (?, '授权作者', ?, 1)",
		creatorID, querytext.NaturalSortKey("授权作者")); err != nil {
		t.Fatal(err)
	}
	for index, sourceID := range []string{sourceA, sourceB} {
		bindingID := newAggregateCoverID(t, generator, domain.IDCreatorBinding)
		if _, err := store.Control.SQL().ExecContext(ctx, `INSERT INTO creator_bindings
(binding_id, source_id, provider_id, external_id, source_key, creator_id, identity_version,
 status, last_seen_generation, created_at, updated_at)
VALUES (?, ?, 'test', ?, ?, ?, 1, 'active', 1, 1, 1)`,
			bindingID, sourceID, "creator-external", "creator-source-"+string(rune('a'+index)), creatorID); err != nil {
			t.Fatal(err)
		}
	}

	workA := newAggregateCoverID(t, generator, domain.IDCanonicalWork)
	workB := newAggregateCoverID(t, generator, domain.IDCanonicalWork)
	mediaA = newAggregateCoverID(t, generator, domain.IDCanonicalMedia)
	mediaB = newAggregateCoverID(t, generator, domain.IDCanonicalMedia)
	stage := func(sourceID, workID, mediaID, suffix string, publishedAt, watermark int64) catalog.Publication {
		t.Helper()
		jobID := newAggregateCoverID(t, generator, domain.IDJob)
		candidate, err := catalogStore.BeginCandidate(ctx, jobID, sourceID, watermark)
		if err != nil {
			t.Fatal(err)
		}
		workSourceKey := "work-" + suffix
		mediaSourceKey := workSourceKey + "/01.jpg"
		work := catalog.WorkFact{
			SourceID: sourceID, LibraryID: libraryID, SourceKey: workSourceKey,
			SourceTitle: workSourceKey, Title: workSourceKey, WorkID: workID,
			Creator: "授权作者", CreatorID: creatorID, CreatorSourceKey: "creator-" + suffix,
			CreatorProviderID: "test", CreatorExternalID: "creator-external", SourceCreatorName: "授权作者",
			RuleCoverMediaSourceKey: mediaSourceKey, RuleCoverMediaID: mediaID,
			PublishedAtNanos: publishedAt, PublishedAtRaw: "raw", PublishedAtParser: "gallery-work-date-v1",
		}
		media := catalog.MediaFact{
			SourceID: sourceID, SourceKey: mediaSourceKey, WorkSourceKey: workSourceKey,
			RuleKey: mediaSourceKey, RelativePath: mediaSourceKey, Kind: "image", MIME: "image/jpeg", Size: 1,
			Algorithm: "sha256-v1", Digest: aggregateCoverDigest(suffix), LocationKey: "location-" + mediaID,
			MediaID: mediaID, WorkID: workID, Ordinal: 0,
		}
		if err := catalogStore.Stage(ctx, candidate, []catalog.WorkFact{work}, []catalog.MediaFact{media}); err != nil {
			t.Fatal(err)
		}
		if err := catalogStore.ValidateCandidate(ctx, candidate); err != nil {
			t.Fatal(err)
		}
		publication, err := catalogStore.Publish(ctx, candidate)
		if err != nil {
			t.Fatal(err)
		}
		return publication
	}
	stage(sourceA, workA, mediaA, "a", 1_000, 1)
	publication := stage(sourceB, workB, mediaB, "b", 9_000, 2)
	return creatorID, mediaA, mediaB, publication.ID
}

func newAggregateCoverID(t *testing.T, generator identity.Generator, kind domain.IDKind) string {
	t.Helper()
	id, err := generator.New(kind)
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}

func aggregateCoverDigest(suffix string) string {
	if suffix == "a" {
		return "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	}
	return "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
}

func assertSessionAggregateCover(
	t *testing.T,
	client *http.Client,
	serverURL, libraryID, creatorID, wantMediaID, wantPublicationID string,
) {
	t.Helper()
	assertLibraryCover(t, client, "", serverURL, libraryID, wantMediaID, wantPublicationID)
	assertCreatorCover(t, client, "", serverURL, creatorID, wantMediaID, wantPublicationID)
}

func assertBearerCreatorCover(t *testing.T, secret, serverURL, creatorID, wantMediaID, wantPublicationID string) {
	t.Helper()
	assertCreatorCover(t, nil, secret, serverURL, creatorID, wantMediaID, wantPublicationID)
}

func assertLibraryCover(
	t *testing.T,
	client *http.Client,
	bearer, serverURL, libraryID, wantMediaID, wantPublicationID string,
) {
	t.Helper()
	var list api.LibraryListResponse
	if status := getAggregateJSON(t, client, bearer, serverURL+"/api/v1/libraries", &list); status != http.StatusOK {
		t.Fatalf("Library 列表失败: status=%d", status)
	}
	var listed *api.Library
	for index := range list.Libraries {
		if list.Libraries[index].Id == libraryID {
			listed = &list.Libraries[index]
			break
		}
	}
	if listed == nil {
		t.Fatalf("Library 列表缺少 %s: %+v", libraryID, list)
	}
	assertAggregateCoverPointers(t, "Library 列表", listed.CoverMediaId, listed.QueryPublicationId, wantMediaID, wantPublicationID)

	var detail api.Library
	if status := getAggregateJSON(t, client, bearer, serverURL+"/api/v1/libraries/"+libraryID, &detail); status != http.StatusOK {
		t.Fatalf("Library 详情失败: status=%d", status)
	}
	assertAggregateCoverPointers(t, "Library 详情", detail.CoverMediaId, detail.QueryPublicationId, wantMediaID, wantPublicationID)
}

func assertCreatorCover(
	t *testing.T,
	client *http.Client,
	bearer, serverURL, creatorID, wantMediaID, wantPublicationID string,
) {
	t.Helper()
	var list api.CreatorListResponse
	if status := getAggregateJSON(t, client, bearer, serverURL+"/api/v1/creators", &list); status != http.StatusOK {
		t.Fatalf("Creator 列表失败: status=%d", status)
	}
	var listed *api.Creator
	for index := range list.Creators {
		if list.Creators[index].Id == creatorID {
			listed = &list.Creators[index]
			break
		}
	}
	if listed == nil {
		t.Fatalf("Creator 列表缺少 %s: %+v", creatorID, list)
	}
	assertAggregateCoverPointers(t, "Creator 列表", listed.CoverMediaId, listed.QueryPublicationId, wantMediaID, wantPublicationID)

	var detail api.CreatorDetail
	if status := getAggregateJSON(t, client, bearer, serverURL+"/api/v1/creators/"+creatorID, &detail); status != http.StatusOK {
		t.Fatalf("Creator 详情失败: status=%d", status)
	}
	assertAggregateCoverPointers(t, "Creator 详情", detail.Creator.CoverMediaId, detail.Creator.QueryPublicationId, wantMediaID, wantPublicationID)
}

func assertAggregateCoverPointers(
	t *testing.T,
	label string,
	mediaID *api.CanonicalMediaId,
	publicationID *api.QueryPublicationId,
	wantMediaID, wantPublicationID string,
) {
	t.Helper()
	if wantMediaID == "" {
		if mediaID != nil || publicationID != nil {
			t.Fatalf("%s 未获媒体授权仍暴露封面: media=%v publication=%v", label, mediaID, publicationID)
		}
		return
	}
	if mediaID == nil || string(*mediaID) != wantMediaID || publicationID == nil || string(*publicationID) != wantPublicationID {
		t.Fatalf("%s 封面错误: media=%v publication=%v wantMedia=%s wantPublication=%s",
			label, mediaID, publicationID, wantMediaID, wantPublicationID)
	}
}

func getAggregateJSON(t *testing.T, client *http.Client, bearer, endpoint string, target any) int {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body := readAndClose(t, response)
	if response.StatusCode >= 200 && response.StatusCode < 300 && target != nil {
		if err := json.Unmarshal(body, target); err != nil {
			t.Fatalf("解析聚合封面响应失败: status=%d err=%v body=%s", response.StatusCode, err, body)
		}
	}
	return response.StatusCode
}
