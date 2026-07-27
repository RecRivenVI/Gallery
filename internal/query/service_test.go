package query_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
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
	request := authorizedRequest(galleryquery.Request{Limit: 2, Sort: "title_asc", AuthorizationScope: scope})
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
		result, err := service.Search(ctx, authorizedRequest(galleryquery.Request{Search: test.query, Limit: 20, AuthorizationScope: scope}))
		if err != nil || len(result.Items) != test.want {
			t.Fatalf("搜索 %q = %d err=%v", test.query, len(result.Items), err)
		}
	}
	tagged, err := service.Search(ctx, authorizedRequest(galleryquery.Request{Tag: "blue", Limit: 20, AuthorizationScope: scope}))
	if err != nil || len(tagged.Items) != 2 {
		t.Fatalf("标签过滤错误: %d %v", len(tagged.Items), err)
	}
	_, err = service.Search(ctx, authorizedRequest(galleryquery.Request{Search: "画", Limit: 20, AuthorizationScope: scope}))
	assertCode(t, err, fault.CodeQueryTooShort)

	seedPublication(t, store, "002", []seedWork{{title: "new-active", creator: "", tags: nil, filenames: nil}})
	continued, err := service.Search(ctx, authorizedRequest(galleryquery.Request{Limit: 2, Cursor: firstCursor, Sort: "title_asc", AuthorizationScope: scope}))
	if err != nil || continued.QueryPublicationID != publicationID {
		t.Fatalf("active 切换后旧游标未继续旧 publication: %+v %v", continued, err)
	}
	_, err = service.Search(ctx, authorizedRequest(galleryquery.Request{Limit: 2, Cursor: firstCursor, Search: "changed", AuthorizationScope: scope}))
	assertCode(t, err, fault.CodeCursorExpired)
	_, err = service.Search(ctx, authorizedRequest(galleryquery.Request{Limit: 2, Cursor: firstCursor, AuthorizationScope: galleryquery.AuthorizationScope("other", []string{"library.read"})}))
	assertCode(t, err, fault.CodeCursorExpired)
	_, err = service.Search(ctx, authorizedRequest(galleryquery.Request{Limit: 2, Cursor: firstCursor, QueryPublicationID: "qpub_018f47d2-5c16-7a44-a8a0-000000000002", AuthorizationScope: scope}))
	assertCode(t, err, fault.CodeCursorExpired)
	tampered := "A" + firstCursor[1:]
	_, err = service.Search(ctx, authorizedRequest(galleryquery.Request{Limit: 2, Cursor: tampered, AuthorizationScope: scope}))
	assertCode(t, err, fault.CodeCursorInvalid)

	expiredService, err := galleryquery.NewService(ctx, store.Control.SQL(), store.Catalog.SQL(), clock.Fixed{Time: now.Add(10 * time.Minute)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = expiredService.Search(ctx, authorizedRequest(galleryquery.Request{Limit: 2, Cursor: firstCursor, AuthorizationScope: scope}))
	assertCode(t, err, fault.CodeCursorExpired)

	encoded, _ := json.Marshal(continued)
	if strings.Contains(string(encoded), "holiday-final") || strings.Contains(string(encoded), "relativePath") {
		t.Fatalf("查询响应泄露文件位置: %s", encoded)
	}
}

func TestResourceAuthorizationFiltersBeforeTotalSortingAndPagination(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 3, 0, 0, 0, time.UTC)
	dirs := appdirs.UnderRoot(t.TempDir())
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	seedPublication(t, store, "801", []seedWork{
		{title: "a-denied", sourceID: "src_denied"},
		{title: "b-visible", sourceID: "src_allowed"},
		{title: "c-visible", sourceID: "src_allowed"},
		{title: "d-other", sourceID: "src_other"},
	})
	service, err := galleryquery.NewService(ctx, store.Control.SQL(), store.Catalog.SQL(), clock.Fixed{Time: now}, nil)
	if err != nil {
		t.Fatal(err)
	}

	authorizeAllowed := func(_ context.Context, capabilities, sourceIDs []string) ([]string, error) {
		if !reflect.DeepEqual(capabilities, []string{"library.read"}) {
			t.Fatalf("普通查询所需 capability = %v", capabilities)
		}
		var allowed []string
		for _, sourceID := range sourceIDs {
			if sourceID == "src_allowed" {
				allowed = append(allowed, sourceID)
			}
		}
		return allowed, nil
	}
	scope := galleryquery.AuthorizationScope("scoped-reader", []string{"library.read"})
	first, err := service.Search(ctx, galleryquery.Request{
		Limit: 1, AuthorizationScope: scope, AuthorizeSources: authorizeAllowed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Items[0].Title != "b-visible" || first.NextCursor == "" {
		t.Fatalf("授权 SQL 过滤未在排序/limit+1 前生效: %+v", first)
	}
	if first.Total.Mode != galleryquery.TotalModeExact || first.Total.Value == nil || *first.Total.Value != 2 {
		t.Fatalf("total 泄露无权 Source: %+v", first.Total)
	}

	second, err := service.Search(ctx, galleryquery.Request{
		Limit: 1, Cursor: first.NextCursor, AuthorizationScope: scope, AuthorizeSources: authorizeAllowed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].Title != "c-visible" || second.NextCursor != "" {
		t.Fatalf("授权集合内分页错误: %+v", second)
	}

	// AuthorizationScope 字符串未变，但 Grant/Token scope 重算出的 Source 集合改变时，
	// 旧 cursor 必须过期，不能在新的授权集合中从旧 keyset 位置继续。
	authorizeOther := func(_ context.Context, _ []string, sourceIDs []string) ([]string, error) {
		var allowed []string
		for _, sourceID := range sourceIDs {
			if sourceID == "src_other" {
				allowed = append(allowed, sourceID)
			}
		}
		return allowed, nil
	}
	_, err = service.Search(ctx, galleryquery.Request{
		Limit: 1, Cursor: first.NextCursor, AuthorizationScope: scope, AuthorizeSources: authorizeOther,
	})
	assertCode(t, err, fault.CodeCursorExpired)
}

func TestHiddenFilterRequiresPerSourceWriteAndAuthorizationFailsClosed(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 3, 30, 0, 0, time.UTC)
	dirs := appdirs.UnderRoot(t.TempDir())
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	seedPublication(t, store, "802", []seedWork{
		{title: "hidden-denied", sourceID: "src_denied", hidden: true},
		{title: "hidden-read-only", sourceID: "src_read_only", hidden: true},
		{title: "hidden-writable", sourceID: "src_writable", hidden: true},
	})
	service, err := galleryquery.NewService(ctx, store.Control.SQL(), store.Catalog.SQL(), clock.Fixed{Time: now}, nil)
	if err != nil {
		t.Fatal(err)
	}

	var calls int
	authorize := func(_ context.Context, capabilities, sourceIDs []string) ([]string, error) {
		calls++
		if !reflect.DeepEqual(capabilities, []string{"library.read", "library.write"}) {
			t.Fatalf("hidden 查询所需 capability = %v", capabilities)
		}
		if !reflect.DeepEqual(sourceIDs, []string{"src_denied", "src_read_only", "src_writable"}) {
			t.Fatalf("批量授权候选 Source = %v", sourceIDs)
		}
		return []string{"src_writable", "src_writable", "src_unknown"}, nil
	}
	filter := `{"field":"overlay.hidden","op":"eq","value":true}`
	result, err := service.Search(ctx, galleryquery.Request{
		Limit: 20, Filter: filter,
		AuthorizationScope: galleryquery.AuthorizationScope("writer", []string{"library.read", "library.write"}),
		AuthorizeSources:   authorize,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Title != "hidden-writable" || result.Total.Value == nil || *result.Total.Value != 1 {
		t.Fatalf("hidden 资源授权未进入 items/total SQL: %+v", result)
	}
	if calls != 1 {
		t.Fatalf("每次查询应只建立一次批量授权快照，调用次数=%d", calls)
	}

	_, err = service.Search(ctx, galleryquery.Request{
		Limit: 20, Filter: filter,
		AuthorizationScope: galleryquery.AuthorizationScope("flat-only", []string{"library.read", "library.write"}),
	})
	assertCode(t, err, fault.CodeForbidden)

	_, err = service.Search(ctx, galleryquery.Request{
		Limit: 20, AuthorizationScope: galleryquery.AuthorizationScope("broken", []string{"library.read"}),
		AuthorizeSources: func(context.Context, []string, []string) ([]string, error) {
			return nil, errors.New("authorization backend unavailable")
		},
	})
	assertCode(t, err, fault.CodeInternal)
}

func TestAuthorizationCandidatesRespectExplicitLibraryAndSource(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 4, 0, 0, 0, time.UTC)
	dirs := appdirs.UnderRoot(t.TempDir())
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	seedPublication(t, store, "803", []seedWork{
		{title: "a-one", sourceID: "src_a1", libraryID: "lib_a"},
		{title: "a-two", sourceID: "src_a2", libraryID: "lib_a"},
		{title: "b-one", sourceID: "src_b1", libraryID: "lib_b"},
	})
	service, err := galleryquery.NewService(ctx, store.Control.SQL(), store.Catalog.SQL(), clock.Fixed{Time: now}, nil)
	if err != nil {
		t.Fatal(err)
	}

	var calls int
	var candidates []string
	authorize := func(_ context.Context, capabilities, sourceIDs []string) ([]string, error) {
		calls++
		if !reflect.DeepEqual(capabilities, []string{"library.read"}) {
			t.Fatalf("所需 capability = %v", capabilities)
		}
		candidates = append([]string(nil), sourceIDs...)
		return append([]string(nil), sourceIDs...), nil
	}
	scope := galleryquery.AuthorizationScope("scoped", []string{"library.read"})
	libraryResult, err := service.Search(ctx, galleryquery.Request{
		LibraryID: "lib_a", Limit: 20, AuthorizationScope: scope, AuthorizeSources: authorize,
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !reflect.DeepEqual(candidates, []string{"src_a1", "src_a2"}) {
		t.Fatalf("Library 查询批量候选错误: calls=%d candidates=%v", calls, candidates)
	}
	if titles := resultTitles(libraryResult); !reflect.DeepEqual(titles, []string{"a-one", "a-two"}) {
		t.Fatalf("Library 查询结果 = %v", titles)
	}

	calls, candidates = 0, nil
	sourceResult, err := service.Search(ctx, galleryquery.Request{
		SourceID: "src_b1", Limit: 20, AuthorizationScope: scope, AuthorizeSources: authorize,
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !reflect.DeepEqual(candidates, []string{"src_b1"}) {
		t.Fatalf("Source 查询批量候选错误: calls=%d candidates=%v", calls, candidates)
	}
	if titles := resultTitles(sourceResult); !reflect.DeepEqual(titles, []string{"b-one"}) {
		t.Fatalf("Source 查询结果 = %v", titles)
	}
}

func TestMissingPublicationMembershipFailsClosedBeforeItemsAndTotal(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(t.TempDir())
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	seedPublication(t, store, "804", []seedWork{{title: "must-not-leak", sourceID: "src_orphan"}})
	if _, err := store.Catalog.SQL().ExecContext(ctx, `DELETE FROM catalog_revision_sources`); err != nil {
		t.Fatal(err)
	}
	service, err := galleryquery.NewService(ctx, store.Control.SQL(), store.Catalog.SQL(),
		clock.Fixed{Time: time.Date(2026, 7, 26, 4, 30, 0, 0, time.UTC)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Search(ctx, authorizedRequest(galleryquery.Request{
		Limit: 20, AuthorizationScope: galleryquery.AuthorizationScope("owner", []string{"library.read"}),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 0 || result.Total.Mode != galleryquery.TotalModeExact ||
		result.Total.Value == nil || *result.Total.Value != 0 {
		t.Fatalf("缺失 membership 时泄露 Work/total: %+v", result)
	}
}

func TestBrowseRankZeroCursorPaginatesEquivalentlyInBothDirections(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(t.TempDir())
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	seedPublication(t, store, "805", []seedWork{
		{title: "a"}, {title: "b"}, {title: "c"}, {title: "d"}, {title: "e"},
	})
	service, err := galleryquery.NewService(ctx, store.Control.SQL(), store.Catalog.SQL(),
		clock.Fixed{Time: time.Date(2026, 7, 26, 5, 0, 0, 0, time.UTC)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	scope := galleryquery.AuthorizationScope("owner", []string{"library.read"})
	for _, test := range []struct {
		direction string
		want      []string
	}{
		{direction: "asc", want: []string{"a", "b", "c", "d", "e"}},
		{direction: "desc", want: []string{"e", "d", "c", "b", "a"}},
	} {
		t.Run(test.direction, func(t *testing.T) {
			request := authorizedRequest(galleryquery.Request{
				Limit: 2, Sort: "title_" + test.direction, AuthorizationScope: scope,
			})
			var titles []string
			for {
				page, err := service.Search(ctx, request)
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range page.Items {
					if item.RankTier != 0 {
						t.Fatalf("无搜索 browse rankTier=%d", item.RankTier)
					}
					titles = append(titles, item.Title)
				}
				if page.NextCursor == "" {
					break
				}
				// Cursor claims 仍携带旧有 rank=0 字段；新 keyset 忽略这个恒定分量，
				// 但签名、fingerprint 与 publication/authorization 绑定保持不变。
				request.Cursor = page.NextCursor
			}
			if !reflect.DeepEqual(titles, test.want) {
				t.Fatalf("%s 分页=%v want=%v", test.direction, titles, test.want)
			}
		})
	}
}

func TestWorkSortProtocolV2KeysetNullLastAndDependencies(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(t.TempDir())
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	seedPublication(t, store, "806", []seedWork{
		{title: "date-newer-a", publishedAtNS: 200, progress: 0.25},
		{title: "date-missing", publishedAtNS: 0, progress: 0.75},
		{title: "date-old", publishedAtNS: 100, progress: 0.25},
		{title: "date-newer-b", publishedAtNS: 200, progress: 0},
	})
	service, err := galleryquery.NewService(ctx, store.Control.SQL(), store.Catalog.SQL(),
		clock.Fixed{Time: time.Date(2026, 7, 27, 3, 0, 0, 0, time.UTC)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	scope := galleryquery.AuthorizationScope("owner", []string{"library.read"})
	tests := []struct {
		sort       string
		want       []string
		dependency string
	}{
		{sort: "date_asc", want: []string{"date-old", "date-newer-a", "date-newer-b", "date-missing"}, dependency: "publishedAt"},
		{sort: "date_desc", want: []string{"date-newer-b", "date-newer-a", "date-old", "date-missing"}, dependency: "publishedAt"},
		{sort: "progress_asc", want: []string{"date-newer-b", "date-newer-a", "date-old", "date-missing"}, dependency: "overlay.progress"},
		{sort: "progress_desc", want: []string{"date-missing", "date-old", "date-newer-a", "date-newer-b"}, dependency: "overlay.progress"},
	}
	for _, test := range tests {
		t.Run(test.sort, func(t *testing.T) {
			request := authorizedRequest(galleryquery.Request{Sort: test.sort, Limit: 2, AuthorizationScope: scope})
			var titles []string
			var first galleryquery.Result
			for {
				page, err := service.Search(ctx, request)
				if err != nil {
					t.Fatal(err)
				}
				if len(titles) == 0 {
					first = page
				}
				for _, item := range page.Items {
					titles = append(titles, item.Title)
				}
				if page.NextCursor == "" {
					break
				}
				request.Cursor = page.NextCursor
			}
			if !reflect.DeepEqual(titles, test.want) {
				t.Fatalf("%s 分页顺序=%v want=%v", test.sort, titles, test.want)
			}
			if !hasDependency(first.DependencySet, test.dependency, galleryquery.DependencyRoleOrdering) {
				t.Fatalf("%s 缺少排序依赖 %s: %+v", test.sort, test.dependency, first.DependencySet)
			}
		})
	}

	first, err := service.Search(ctx, authorizedRequest(galleryquery.Request{Sort: "date_desc", Limit: 2, AuthorizationScope: scope}))
	if err != nil || first.NextCursor == "" {
		t.Fatalf("日期首页或游标无效: %+v %v", first, err)
	}
	_, err = service.Search(ctx, authorizedRequest(galleryquery.Request{
		Sort: "progress_desc", Limit: 2, Cursor: first.NextCursor, AuthorizationScope: scope,
	}))
	assertCode(t, err, fault.CodeCursorExpired)
	_, err = service.Search(ctx, authorizedRequest(galleryquery.Request{Sort: "unknown", Limit: 2, AuthorizationScope: scope}))
	assertCode(t, err, fault.CodeValidation)
}

func hasDependency(fields []galleryquery.DependencyField, name, role string) bool {
	for _, field := range fields {
		if field.Field == name && field.Role == role {
			return true
		}
	}
	return false
}

type seedWork struct {
	title, creator  string
	tags, filenames []string
	sourceID        string
	libraryID       string
	hidden          bool
	favorite        bool
	progress        float64
	publishedAtNS   int64
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
	members := map[string]string{}
	for _, value := range works {
		sourceID := value.sourceID
		if sourceID == "" {
			sourceID = "src_test"
		}
		libraryID := value.libraryID
		if libraryID == "" {
			libraryID = "lib_test"
		}
		if existing, ok := members[sourceID]; ok && existing != libraryID {
			t.Fatalf("测试 Source %s 同时属于 %s 和 %s", sourceID, existing, libraryID)
		}
		members[sourceID] = libraryID
	}
	memberSourceIDs := make([]string, 0, len(members))
	for sourceID := range members {
		memberSourceIDs = append(memberSourceIDs, sourceID)
	}
	sort.Strings(memberSourceIDs)
	for _, sourceID := range memberSourceIDs {
		if _, err := store.Catalog.SQL().ExecContext(ctx, `INSERT INTO catalog_revision_sources
(catalog_revision_id, source_id, library_id) VALUES (?, ?, ?)`, cat, sourceID, members[sourceID]); err != nil {
			t.Fatal(err)
		}
	}
	for index, value := range works {
		id := fmt.Sprintf("wrk_018f47d2-5c16-7a44-a8a0-%012d", index+1+atoiSuffix(suffix)*100)
		sourceID := value.sourceID
		if sourceID == "" {
			sourceID = "src_test"
		}
		libraryID := value.libraryID
		if libraryID == "" {
			libraryID = "lib_test"
		}
		tags, _ := json.Marshal(value.tags)
		filenames, _ := json.Marshal(value.filenames)
		document := querytext.BuildDocument(value.title, value.creator, value.tags, value.filenames)
		hidden := 0
		if value.hidden {
			hidden = 1
		}
		favorite := 0
		if value.favorite {
			favorite = 1
		}
		_, err := store.Catalog.SQL().ExecContext(ctx, `INSERT INTO work_projections
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id, title, creator, tags_json, filenames_text,
 normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text, sort_title_key, hidden, favorite, progress,
	 search_title_norm, search_creator_norm, search_tags_norm, search_filenames_norm, published_at_ns)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, cat, ov, id, sourceID, fmt.Sprintf("source-%d", index), libraryID, value.title, value.creator, string(tags), string(filenames), document.NormalizedOriginal, document.CJKTokens, document.LatinTokens, document.SortTitleKey,
			hidden, favorite, value.progress, document.TitleNorm, document.CreatorNorm, document.TagsNorm, document.FilenamesNorm, value.publishedAtNS)
		if err != nil {
			t.Fatal(err)
		}
		insertSearchFixtureDocument(t, store.Catalog.SQL(), cat, ov, id)
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

func resultTitles(result galleryquery.Result) []string {
	titles := make([]string, 0, len(result.Items))
	for _, item := range result.Items {
		titles = append(titles, item.Title)
	}
	return titles
}

func allowAllSources(_ context.Context, _ []string, sourceIDs []string) ([]string, error) {
	return append([]string(nil), sourceIDs...), nil
}

func authorizedRequest(request galleryquery.Request) galleryquery.Request {
	request.AuthorizeSources = allowAllSources
	return request
}

func assertCode(t *testing.T, err error, code fault.Code) {
	t.Helper()
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != code {
		t.Fatalf("错误 code=%s 实际=%v", code, err)
	}
}
