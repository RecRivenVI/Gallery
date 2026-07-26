package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/RecRivenVI/gallery/internal/querytext"
	"github.com/RecRivenVI/gallery/internal/storage"
	api "github.com/RecRivenVI/gallery/pkg/galleryapi"
)

func TestWorkListAppliesMemberAuthorizationBeforeTotalAndPaging(t *testing.T) {
	server, store := newLANSecurityServer(t, false)
	owner, csrf := establishLANOwner(t, server)
	libraryAllowed := createLibrary(t, owner, server, csrf, "Query authorization")
	libraryDenied := createLibrary(t, owner, server, csrf, "Denied library")
	sourceAllowed := createQueryAuthorizationSource(t, owner, server.URL, csrf, libraryAllowed, "allowed")
	sourceDenied := createQueryAuthorizationSource(t, owner, server.URL, csrf, libraryAllowed, "source-denied")
	sourceLibraryDenied := createQueryAuthorizationSource(t, owner, server.URL, csrf, libraryDenied, "library-denied")
	seedQueryAuthorizationPublication(t, store, libraryAllowed, libraryDenied, sourceAllowed, sourceDenied, sourceLibraryDenied)

	readScoped := createAndLoginQueryAuthorizationUser(t, owner, server.URL, csrf, "read-scoped", []string{"owner"}, []any{
		map[string]any{
			"effect": "deny", "capability": "library.read",
			"scope": map[string]any{"kind": "source", "id": sourceDenied},
		},
		map[string]any{
			"effect": "deny", "capability": "library.read",
			"scope": map[string]any{"kind": "library", "id": libraryDenied},
		},
	})

	first := getQueryAuthorizationWorks(t, readScoped, server.URL, url.Values{"limit": {"1"}})
	if first.Total.Value == nil || *first.Total.Value != 2 || len(first.Works) != 1 || first.NextCursor == nil {
		t.Fatalf("第一页未在 total/limit 前应用 Source deny: %+v", first)
	}
	second := getQueryAuthorizationWorks(t, readScoped, server.URL, url.Values{
		"limit": {"1"}, "cursor": {*first.NextCursor},
	})
	if second.Total.Value == nil || *second.Total.Value != 2 || len(second.Works) != 1 || second.NextCursor != nil {
		t.Fatalf("第二页授权集合或 keyset 不稳定: %+v", second)
	}
	titles := []string{first.Works[0].Title, second.Works[0].Title}
	slices.Sort(titles)
	if !slices.Equal(titles, []string{"Allowed A", "Allowed B"}) {
		t.Fatalf("聚合列表泄露被 deny Source: %v", titles)
	}
	assertQueryAuthorizationStatus(t, readScoped, server.URL, url.Values{"sourceId": {sourceDenied}}, http.StatusForbidden)
	assertQueryAuthorizationStatus(t, readScoped, server.URL, url.Values{"libraryId": {libraryDenied}}, http.StatusForbidden)

	writeScoped := createAndLoginQueryAuthorizationUser(t, owner, server.URL, csrf, "write-scoped", []string{"owner"}, []any{
		map[string]any{
			"effect": "deny", "capability": "library.write",
			"scope": map[string]any{"kind": "source", "id": sourceDenied},
		},
		map[string]any{
			"effect": "deny", "capability": "library.write",
			"scope": map[string]any{"kind": "library", "id": libraryDenied},
		},
	})
	hiddenFilter := `{"field":"overlay.hidden","op":"eq","value":true}`
	hidden := getQueryAuthorizationWorks(t, writeScoped, server.URL, url.Values{"filter": {hiddenFilter}})
	if hidden.Total.Value == nil || *hidden.Total.Value != 1 || len(hidden.Works) != 1 || hidden.Works[0].Title != "Allowed Hidden" {
		t.Fatalf("overlay.hidden 未按资源 effective library.write 裁剪: %+v", hidden)
	}
	assertQueryAuthorizationStatus(t, writeScoped, server.URL, url.Values{
		"sourceId": {sourceDenied}, "filter": {hiddenFilter},
	}, http.StatusForbidden)

	readOnly := createAndLoginQueryAuthorizationUser(t, owner, server.URL, csrf, "read-only", []string{"viewer"}, []any{
		map[string]any{
			"effect": "allow", "capability": "library.read",
			"scope": map[string]any{"kind": "global"},
		},
	})
	assertQueryAuthorizationStatus(t, readOnly, server.URL, url.Values{"filter": {hiddenFilter}}, http.StatusForbidden)

	tokenResponse := requestJSON(t, owner, http.MethodPost, server.URL+"/api/v1/api-tokens", server.URL, csrf,
		map[string]any{
			"name": "query-library-scope", "capabilities": []string{"library.read"},
			"scopes": []map[string]string{{"kind": "library", "id": libraryAllowed}},
		})
	tokenBody := readAndClose(t, tokenResponse)
	var token struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(tokenBody, &token); err != nil || tokenResponse.StatusCode != http.StatusCreated || token.Secret == "" {
		t.Fatalf("创建查询 Token 失败: status=%d err=%v body=%s", tokenResponse.StatusCode, err, tokenBody)
	}
	assertBearerQueryAuthorizationStatus(t, token.Secret, server.URL, nil, http.StatusForbidden)
	tokenScoped := getBearerQueryAuthorizationWorks(t, token.Secret, server.URL, url.Values{"libraryId": {libraryAllowed}})
	if tokenScoped.Total.Value == nil || *tokenScoped.Total.Value != 3 || len(tokenScoped.Works) != 3 {
		t.Fatalf("Library Token scope 未与成员 Source 正确求交: %+v", tokenScoped)
	}
}

func createQueryAuthorizationSource(t *testing.T, client *http.Client, serverURL, csrf, libraryID, name string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	response := requestJSON(t, client, http.MethodPost, serverURL+"/api/v1/sources", serverURL, csrf,
		map[string]any{"libraryId": libraryID, "displayName": name, "rootPath": root})
	body := readAndClose(t, response)
	var source struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &source); err != nil || response.StatusCode != http.StatusCreated || source.ID == "" {
		t.Fatalf("创建 Source %s 失败: status=%d err=%v body=%s", name, response.StatusCode, err, body)
	}
	return source.ID
}

func createAndLoginQueryAuthorizationUser(t *testing.T, owner *http.Client, serverURL, ownerCSRF, username string, roles []string, grants []any) *http.Client {
	t.Helper()
	password := username + "-password-strong"
	response := requestJSON(t, owner, http.MethodPost, serverURL+"/api/v1/admin/users", serverURL, ownerCSRF,
		map[string]any{
			"username": username, "displayName": username, "password": password,
			"roles": roles, "grants": grants,
		})
	body := readAndClose(t, response)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("创建查询授权用户 %s 失败: status=%d body=%s", username, response.StatusCode, body)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	csrf := bootstrapCSRF(t, client, serverURL)
	login := requestJSON(t, client, http.MethodPost, serverURL+"/api/v1/auth/login", serverURL, csrf,
		map[string]any{"username": username, "password": password})
	loginBody := readAndClose(t, login)
	if login.StatusCode != http.StatusCreated {
		t.Fatalf("查询授权用户 %s 登录失败: status=%d body=%s", username, login.StatusCode, loginBody)
	}
	return client
}

func assertQueryAuthorizationStatus(t *testing.T, client *http.Client, serverURL string, query url.Values, want int) {
	t.Helper()
	response := requestJSON(t, client, http.MethodGet, serverURL+"/api/v1/works?"+query.Encode(), "", "", nil)
	body := readAndClose(t, response)
	if response.StatusCode != want {
		t.Fatalf("查询授权状态错误: got=%d want=%d body=%s", response.StatusCode, want, body)
	}
}

func assertBearerQueryAuthorizationStatus(t *testing.T, secret, serverURL string, query url.Values, want int) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, serverURL+"/api/v1/works?"+query.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+secret)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body := readAndClose(t, response)
	if response.StatusCode != want {
		t.Fatalf("Bearer 查询授权状态错误: got=%d want=%d body=%s", response.StatusCode, want, body)
	}
}

func getBearerQueryAuthorizationWorks(t *testing.T, secret, serverURL string, query url.Values) api.WorkListResponse {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, serverURL+"/api/v1/works?"+query.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+secret)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body := readAndClose(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("Bearer 查询 Works 失败: status=%d body=%s", response.StatusCode, body)
	}
	var result api.WorkListResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("解析 Bearer WorkListResponse: %v body=%s", err, body)
	}
	return result
}

func getQueryAuthorizationWorks(t *testing.T, client *http.Client, serverURL string, query url.Values) api.WorkListResponse {
	t.Helper()
	response := requestJSON(t, client, http.MethodGet, serverURL+"/api/v1/works?"+query.Encode(), "", "", nil)
	body := readAndClose(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("查询 Works 失败: status=%d body=%s", response.StatusCode, body)
	}
	var result api.WorkListResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("解析 WorkListResponse: %v body=%s", err, body)
	}
	return result
}

func seedQueryAuthorizationPublication(t *testing.T, store *storage.Store, libraryAllowed, libraryDenied, sourceAllowed, sourceDenied, sourceLibraryDenied string) {
	t.Helper()
	ctx := context.Background()
	const catalogRevision = "cat_018f47d2-5c16-7a44-a8a0-000000000950"
	const overlayRevision = "ovr_018f47d2-5c16-7a44-a8a0-000000000950"
	const jobID = "job_018f47d2-5c16-7a44-a8a0-000000000950"
	const publicationID = "qpub_018f47d2-5c16-7a44-a8a0-000000000950"
	db := store.Catalog.SQL()
	exec := func(statement string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, statement, args...); err != nil {
			t.Fatalf("建立查询授权 publication: %v\n%s", err, statement)
		}
	}
	exec("INSERT INTO catalog_revisions VALUES (?, ?, ?, 'published', 1, 1)", catalogRevision, jobID, sourceAllowed)
	exec(`INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, published_at)
VALUES (?, ?, 1, 'published', 1, 1)`, overlayRevision, catalogRevision)
	exec("INSERT INTO query_publications VALUES (?, ?, ?, ?, 1, 1)", publicationID, catalogRevision, overlayRevision, jobID)
	for _, member := range []struct {
		sourceID, libraryID string
	}{
		{sourceAllowed, libraryAllowed},
		{sourceDenied, libraryAllowed},
		{sourceLibraryDenied, libraryDenied},
	} {
		exec(`INSERT INTO catalog_revision_sources
(catalog_revision_id, source_id, library_id) VALUES (?, ?, ?)`, catalogRevision, member.sourceID, member.libraryID)
	}

	works := []struct {
		id, libraryID, sourceID, sourceKey, title string
		hidden                                    bool
	}{
		{"wrk_018f47d2-5c16-7a44-a8a0-000000000951", libraryAllowed, sourceAllowed, "allowed-a", "Allowed A", false},
		{"wrk_018f47d2-5c16-7a44-a8a0-000000000952", libraryAllowed, sourceAllowed, "allowed-b", "Allowed B", false},
		{"wrk_018f47d2-5c16-7a44-a8a0-000000000953", libraryAllowed, sourceAllowed, "allowed-hidden", "Allowed Hidden", true},
		{"wrk_018f47d2-5c16-7a44-a8a0-000000000954", libraryAllowed, sourceDenied, "source-denied-visible", "Source Denied Visible", false},
		{"wrk_018f47d2-5c16-7a44-a8a0-000000000955", libraryAllowed, sourceDenied, "source-denied-hidden", "Source Denied Hidden", true},
		{"wrk_018f47d2-5c16-7a44-a8a0-000000000956", libraryDenied, sourceLibraryDenied, "library-denied-visible", "Library Denied Visible", false},
		{"wrk_018f47d2-5c16-7a44-a8a0-000000000957", libraryDenied, sourceLibraryDenied, "library-denied-hidden", "Library Denied Hidden", true},
	}
	for _, work := range works {
		document := querytext.BuildDocument(work.title, "", nil, nil)
		hidden := 0
		if work.hidden {
			hidden = 1
		}
		exec(`INSERT INTO work_projections
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id, title, creator, tags_json, filenames_text,
 normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text, sort_title_key, hidden, favorite, progress,
 search_title_norm, search_creator_norm, search_tags_norm, search_filenames_norm)
VALUES (?, ?, ?, ?, ?, ?, ?, '', '[]', '[]', ?, ?, ?, ?, ?, 0, 0, ?, '', '', '')`,
			catalogRevision, overlayRevision, work.id, work.sourceID, work.sourceKey, work.libraryID, work.title,
			document.NormalizedOriginal, document.CJKTokens, document.LatinTokens, document.SortTitleKey, hidden, document.TitleNorm)
	}
	exec(`INSERT INTO active_query_publication VALUES (1, ?)
ON CONFLICT(singleton) DO UPDATE SET query_publication_id=excluded.query_publication_id`, publicationID)
}
