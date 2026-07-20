package query_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	galleryquery "github.com/RecRivenVI/gallery/internal/query"
	"github.com/RecRivenVI/gallery/internal/querytext"
	"github.com/RecRivenVI/gallery/internal/storage"
)

func TestFTSSnapshotKeysetCursorAndAuthorization(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 16, 2, 0, 0, 0, time.UTC)
	dirs := appdirs.UnderRoot(t.TempDir())
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	publicationID := seedPublication(t, store, "001", []seedWork{
		{title: "file10", creator: "Alice", tags: []string{"blue"}, filenames: []string{"holiday-final.JPG"}},
		{title: "file2", creator: "Bob", tags: []string{"red"}, filenames: []string{"scan-002.png"}},
		{title: "作品十二", creator: "作者甲", tags: []string{"青空"}, filenames: []string{"作品12.jpg"}},
		{title: "FILE1", creator: "Carol", tags: []string{"blue"}, filenames: []string{"prefix-middle-suffix.webp"}},
		{title: "かな作品", creator: "作者乙", tags: []string{"日本"}, filenames: []string{"kana.png"}},
	})
	service, err := galleryquery.NewService(ctx, store.Control.SQL(), store.Catalog.SQL(), clock.Fixed{Time: now}, nil)
	if err != nil {
		t.Fatal(err)
	}
	scope := galleryquery.AuthorizationScope("owner", []string{"library.read"})
	request := galleryquery.Request{Limit: 2, SortDirection: "asc", AuthorizationScope: scope}
	var ids []string
	var cursor string
	firstCursor := ""
	for {
		request.Cursor = cursor
		page, err := service.Search(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if page.QueryPublicationID != publicationID || page.CatalogRevision == "" || page.OverlayProjectionRevision == "" {
			t.Fatalf("publication 元组缺失: %+v", page)
		}
		for _, item := range page.Items {
			ids = append(ids, item.ID)
		}
		if firstCursor == "" {
			firstCursor = page.NextCursor
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(ids) != 5 || hasDuplicate(ids) {
		t.Fatalf("多页重复或遗漏: %v", ids)
	}

	for _, test := range []struct {
		query string
		want  int
	}{{"作品", 2}, {"IDDLE-SUFF", 1}, {"作品12", 1}} {
		result, err := service.Search(ctx, galleryquery.Request{Search: test.query, Limit: 20, AuthorizationScope: scope})
		if err != nil || len(result.Items) != test.want {
			t.Fatalf("搜索 %q = %d err=%v", test.query, len(result.Items), err)
		}
	}
	tagged, err := service.Search(ctx, galleryquery.Request{Tag: "blue", Limit: 20, AuthorizationScope: scope})
	if err != nil || len(tagged.Items) != 2 {
		t.Fatalf("标签过滤错误: %d %v", len(tagged.Items), err)
	}
	_, err = service.Search(ctx, galleryquery.Request{Search: "画", Limit: 20, AuthorizationScope: scope})
	assertCode(t, err, fault.CodeQueryTooShort)

	seedPublication(t, store, "002", []seedWork{{title: "new-active", creator: "", tags: nil, filenames: nil}})
	continued, err := service.Search(ctx, galleryquery.Request{Limit: 2, Cursor: firstCursor, SortDirection: "asc", AuthorizationScope: scope})
	if err != nil || continued.QueryPublicationID != publicationID {
		t.Fatalf("active 切换后旧游标未继续旧 publication: %+v %v", continued, err)
	}
	_, err = service.Search(ctx, galleryquery.Request{Limit: 2, Cursor: firstCursor, Search: "changed", AuthorizationScope: scope})
	assertCode(t, err, fault.CodeCursorExpired)
	_, err = service.Search(ctx, galleryquery.Request{Limit: 2, Cursor: firstCursor, AuthorizationScope: galleryquery.AuthorizationScope("other", []string{"library.read"})})
	assertCode(t, err, fault.CodeCursorExpired)
	_, err = service.Search(ctx, galleryquery.Request{Limit: 2, Cursor: firstCursor, QueryPublicationID: "qpub_018f47d2-5c16-7a44-a8a0-000000000002", AuthorizationScope: scope})
	assertCode(t, err, fault.CodeCursorExpired)
	tampered := "A" + firstCursor[1:]
	_, err = service.Search(ctx, galleryquery.Request{Limit: 2, Cursor: tampered, AuthorizationScope: scope})
	assertCode(t, err, fault.CodeCursorInvalid)

	expiredService, err := galleryquery.NewService(ctx, store.Control.SQL(), store.Catalog.SQL(), clock.Fixed{Time: now.Add(10 * time.Minute)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = expiredService.Search(ctx, galleryquery.Request{Limit: 2, Cursor: firstCursor, AuthorizationScope: scope})
	assertCode(t, err, fault.CodeCursorExpired)

	encoded, _ := json.Marshal(continued)
	if strings.Contains(string(encoded), "holiday-final") || strings.Contains(string(encoded), "relativePath") {
		t.Fatalf("查询响应泄露文件位置: %s", encoded)
	}
}

type seedWork struct {
	title, creator  string
	tags, filenames []string
	favorite        bool
	progress        float64
}

func seedPublication(t *testing.T, store *storage.Store, suffix string, works []seedWork) string {
	t.Helper()
	ctx := context.Background()
	cat := "cat_018f47d2-5c16-7a44-a8a0-000000000" + suffix
	ov := "ovr_018f47d2-5c16-7a44-a8a0-000000000" + suffix
	job := "job_018f47d2-5c16-7a44-a8a0-000000000" + suffix
	pub := "qpub_018f47d2-5c16-7a44-a8a0-000000000" + suffix
	if _, err := store.Catalog.SQL().ExecContext(ctx, "INSERT INTO catalog_revisions VALUES (?, ?, 'src_test', 'published', 1, 1)", cat, job); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Catalog.SQL().ExecContext(ctx, `INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, published_at)
VALUES (?, ?, 1, 'published', 1, 1)`, ov, cat); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Catalog.SQL().ExecContext(ctx, "INSERT INTO query_publications VALUES (?, ?, ?, ?, 1, 1)", pub, cat, ov, job); err != nil {
		t.Fatal(err)
	}
	for index, value := range works {
		id := fmt.Sprintf("wrk_018f47d2-5c16-7a44-a8a0-%012d", index+1+atoiSuffix(suffix)*100)
		tags, _ := json.Marshal(value.tags)
		filenames, _ := json.Marshal(value.filenames)
		document := querytext.BuildDocument(value.title, value.creator, value.tags, value.filenames)
		favorite := 0
		if value.favorite {
			favorite = 1
		}
		_, err := store.Catalog.SQL().ExecContext(ctx, `INSERT INTO work_projections
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id, title, creator, tags_json, filenames_text,
 normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text, sort_title_key, hidden, favorite, progress,
 search_title_norm, search_creator_norm, search_tags_norm, search_filenames_norm)
VALUES (?, ?, ?, 'src_test', ?, 'lib_test', ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?)`, cat, ov, id, fmt.Sprintf("source-%d", index), value.title, value.creator, string(tags), string(filenames), document.NormalizedOriginal, document.CJKTokens, document.LatinTokens, document.SortTitleKey,
			favorite, value.progress, document.TitleNorm, document.CreatorNorm, document.TagsNorm, document.FilenamesNorm)
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.Catalog.SQL().ExecContext(ctx, "INSERT INTO work_search VALUES (?, ?, ?, ?, ?, ?)", cat, ov, id, document.NormalizedOriginal, document.CJKTokens, document.LatinTokens)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Catalog.SQL().ExecContext(ctx, `INSERT INTO active_query_publication VALUES (1, ?) ON CONFLICT(singleton) DO UPDATE SET query_publication_id=excluded.query_publication_id`, pub); err != nil {
		t.Fatal(err)
	}
	return pub
}

func atoiSuffix(value string) int {
	var result int
	_, _ = fmt.Sscanf(value, "%d", &result)
	return result
}
func hasDuplicate(values []string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			return true
		}
		seen[value] = true
	}
	return false
}
func assertCode(t *testing.T, err error, code fault.Code) {
	t.Helper()
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != code {
		t.Fatalf("错误 code=%s 实际=%v", code, err)
	}
}
