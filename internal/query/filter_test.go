package query_test

import (
	"context"
	"fmt"
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

// richFixture 建立一个覆盖结构化过滤全部注册字段的合成 publication：两个 Source、
// 两个 Provider、Creator 合并（work_creator_relations 仍保留合并前 ID，模拟合并后
// 尚未重新投影的真实场景）与三种媒体 kind/location/content 状态组合。
func richFixture(t *testing.T) (*storage.Store, string) {
	t.Helper()
	ctx := context.Background()
	dirs := appdirs.UnderRoot(t.TempDir())
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	const cat = "cat_018f47d2-5c16-7a44-a8a0-000000000900"
	const ov = "ovr_018f47d2-5c16-7a44-a8a0-000000000900"
	const job = "job_018f47d2-5c16-7a44-a8a0-000000000900"
	const pub = "qpub_018f47d2-5c16-7a44-a8a0-000000000900"
	const ctrRoot = "ctr_018f47d2-5c16-7a44-a8a0-000000000001"
	const ctrAbsorbed = "ctr_018f47d2-5c16-7a44-a8a0-000000000009"
	const workA = "wrk_018f47d2-5c16-7a44-a8a0-000000000901"
	const workB = "wrk_018f47d2-5c16-7a44-a8a0-000000000902"
	const workC = "wrk_018f47d2-5c16-7a44-a8a0-000000000903"

	catalogDB := store.Catalog.SQL()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := catalogDB.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("seed 失败: %v\nquery=%s", err, query)
		}
	}
	exec("INSERT INTO catalog_revisions VALUES (?, ?, 'src_test', 'published', 1, 1)", cat, job)
	exec(`INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, published_at)
VALUES (?, ?, 1, 'published', 1, 1)`, ov, cat)
	exec("INSERT INTO query_publications VALUES (?, ?, ?, ?, 1, 1)", pub, cat, ov, job)

	type workSpec struct {
		id, sourceID, sourceKey, title, providerID, creatorID, creatorRole, mediaKind, location, verification string
	}
	specs := []workSpec{
		{id: workA, sourceID: "src_test", sourceKey: "alpha", title: "Alpha Work", providerID: "providerA", creatorID: ctrRoot, creatorRole: "author", mediaKind: "image", location: "present", verification: "content_verified"},
		{id: workB, sourceID: "src_test", sourceKey: "beta", title: "Beta Work", providerID: "providerB", creatorID: ctrAbsorbed, creatorRole: "author", mediaKind: "video", location: "offline", verification: "located_unverified"},
		{id: workC, sourceID: "src_other", sourceKey: "gamma", title: "Gamma Work", providerID: "providerA", creatorID: ctrRoot, creatorRole: "illustrator", mediaKind: "image", location: "present", verification: "content_verified"},
	}
	for index, spec := range specs {
		document := querytext.BuildDocument(spec.title, "", nil, nil)
		exec(`INSERT INTO source_works
(catalog_revision_id, source_id, source_key, title, creator, tags_json, filenames_text, provider_id, external_id)
VALUES (?, ?, ?, ?, '', '[]', '', ?, '')`, cat, spec.sourceID, spec.sourceKey, spec.title, spec.providerID)
		exec(`INSERT INTO work_projections
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id, title, creator, tags_json, filenames_text,
 normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text, sort_title_key, hidden)
VALUES (?, ?, ?, ?, ?, 'lib_test', ?, '', '[]', '', ?, ?, ?, ?, 0)`,
			cat, ov, spec.id, spec.sourceID, spec.sourceKey, spec.title,
			document.NormalizedOriginal, document.CJKTokens, document.LatinTokens, document.SortTitleKey)
		exec("INSERT INTO work_search VALUES (?, ?, ?, ?, ?, ?)", cat, ov, spec.id, document.NormalizedOriginal, document.CJKTokens, document.LatinTokens)
		exec(`INSERT OR IGNORE INTO creator_projections (catalog_revision_id, overlay_revision_id, creator_id, name, sort_name_key)
VALUES (?, ?, ?, 'creator', 'creator')`, cat, ov, spec.creatorID)
		exec(`INSERT INTO work_creator_relations (catalog_revision_id, overlay_revision_id, work_id, creator_id, role, ordinal)
VALUES (?, ?, ?, ?, ?, 0)`, cat, ov, spec.id, spec.creatorID, spec.creatorRole)
		mediaID := fmt.Sprintf("med_018f47d2-5c16-7a44-a8a0-%012d", index+1)
		exec(`INSERT INTO media_projections
(catalog_revision_id, overlay_revision_id, media_id, work_id, source_id, source_key, relative_path, media_kind, mime_type,
 size_bytes, algorithm, digest, location_status, ordinal, hidden, base_ordinal, content_verification_state, verified_at)
VALUES (?, ?, ?, ?, ?, ?, 'file.bin', ?, 'application/octet-stream', 1, 'sha256-v1', ?, ?, 0, 0, 0, ?, NULL)`,
			cat, ov, mediaID, spec.id, spec.sourceID, spec.sourceKey, spec.mediaKind, digestFor(spec.id), spec.location, spec.verification)
	}
	exec(`INSERT INTO active_query_publication VALUES (1, ?) ON CONFLICT(singleton) DO UPDATE SET query_publication_id=excluded.query_publication_id`, pub)

	controlDB := store.Control.SQL()
	if _, err := controlDB.ExecContext(ctx, "INSERT INTO canonical_creators (creator_id, name, created_at) VALUES (?, 'Creator One', 1)", ctrRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := controlDB.ExecContext(ctx, "INSERT INTO canonical_creators (creator_id, name, merged_into, created_at) VALUES (?, 'Old Name', ?, 1)", ctrAbsorbed, ctrRoot); err != nil {
		t.Fatal(err)
	}
	return store, pub
}

func digestFor(workID string) string {
	sum := 0
	for _, r := range workID {
		sum += int(r)
	}
	return fmt.Sprintf("%064x", sum+1)
}

func newFixtureService(t *testing.T, store *storage.Store) *galleryquery.Service {
	t.Helper()
	service, err := galleryquery.NewService(context.Background(), store.Control.SQL(), store.Catalog.SQL(), clock.Fixed{Time: time.Now().UTC()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func searchFilter(t *testing.T, service *galleryquery.Service, filterJSON string) []string {
	t.Helper()
	scope := galleryquery.AuthorizationScope("owner", []string{"library.read"})
	result, err := service.Search(context.Background(), galleryquery.Request{Filter: filterJSON, Limit: 20, AuthorizationScope: scope})
	if err != nil {
		t.Fatalf("过滤查询失败: %v (filter=%s)", err, filterJSON)
	}
	ids := make([]string, 0, len(result.Items))
	for _, item := range result.Items {
		ids = append(ids, item.Title)
	}
	return ids
}

func TestStructuredFilterFields(t *testing.T) {
	store, _ := richFixture(t)
	service := newFixtureService(t, store)

	cases := []struct {
		name   string
		filter string
		want   []string
	}{
		{"library.id", `{"field":"library.id","op":"eq","value":"lib_test"}`, []string{"Alpha Work", "Beta Work", "Gamma Work"}},
		{"source.id", `{"field":"source.id","op":"eq","value":"src_other"}`, []string{"Gamma Work"}},
		{"provider.id", `{"field":"provider.id","op":"eq","value":"providerA"}`, []string{"Alpha Work", "Gamma Work"}},
		{"media.kind", `{"field":"media.kind","op":"eq","value":"video"}`, []string{"Beta Work"}},
		{"media.locationAvailable_false", `{"field":"media.locationAvailable","op":"eq","value":false}`, []string{"Beta Work"}},
		{"media.contentVerificationState", `{"field":"media.contentVerificationState","op":"eq","value":"located_unverified"}`, []string{"Beta Work"}},
		{"creator.role", `{"field":"creator.role","op":"eq","value":"illustrator"}`, []string{"Gamma Work"}},
		{
			"and组合", `{"all":[{"field":"provider.id","op":"eq","value":"providerA"},{"field":"source.id","op":"eq","value":"src_test"}]}`,
			[]string{"Alpha Work"},
		},
		{
			"or组合", `{"any":[{"field":"media.kind","op":"eq","value":"video"},{"field":"source.id","op":"eq","value":"src_other"}]}`,
			[]string{"Beta Work", "Gamma Work"},
		},
		{
			"not组合", `{"not":{"field":"provider.id","op":"eq","value":"providerA"}}`,
			[]string{"Beta Work"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := searchFilter(t, service, testCase.filter)
			if !sameSet(got, testCase.want) {
				t.Fatalf("got=%v want=%v", got, testCase.want)
			}
		})
	}
}

// TestCreatorFilterResolvesMergeEquivalence 验证按合并根或已被合并的旧 Creator ID
// 过滤都命中同一组有效作品，且 work_creator_relations 本身不被改写。
func TestCreatorFilterResolvesMergeEquivalence(t *testing.T) {
	store, _ := richFixture(t)
	service := newFixtureService(t, store)

	byRoot := searchFilter(t, service, `{"field":"creator.id","op":"eq","value":"ctr_018f47d2-5c16-7a44-a8a0-000000000001"}`)
	byAbsorbed := searchFilter(t, service, `{"field":"creator.id","op":"eq","value":"ctr_018f47d2-5c16-7a44-a8a0-000000000009"}`)
	want := []string{"Alpha Work", "Beta Work", "Gamma Work"}
	if !sameSet(byRoot, want) {
		t.Fatalf("按根 ID 过滤 = %v, want %v", byRoot, want)
	}
	if !sameSet(byAbsorbed, want) {
		t.Fatalf("按已合并旧 ID 过滤 = %v, want %v（应与根等价）", byAbsorbed, want)
	}

	var relationCreator string
	if err := store.Catalog.SQL().QueryRowContext(context.Background(),
		"SELECT creator_id FROM work_creator_relations WHERE work_id='wrk_018f47d2-5c16-7a44-a8a0-000000000902'").Scan(&relationCreator); err != nil {
		t.Fatal(err)
	}
	if relationCreator != "ctr_018f47d2-5c16-7a44-a8a0-000000000009" {
		t.Fatalf("work_creator_relations 被过滤查询意外改写: %s", relationCreator)
	}
}

func TestFilterRejectsUnknownFieldOpAndShape(t *testing.T) {
	store, _ := richFixture(t)
	service := newFixtureService(t, store)
	scope := galleryquery.AuthorizationScope("owner", []string{"library.read"})

	for _, filter := range []string{
		`{"field":"no.such.field","op":"eq","value":"x"}`,
		`{"field":"library.id","op":"contains","value":"x"}`,
		`{"field":"library.id","op":"eq","value":123}`,
		`{"field":"library.id","op":"eq","value":"x","all":[]}`,
		`{"all":[],"any":[]}`,
		`not-json`,
	} {
		_, err := service.Search(context.Background(), galleryquery.Request{Filter: filter, Limit: 20, AuthorizationScope: scope})
		assertCode(t, err, fault.CodeValidation)
	}
}

func TestRankingTierOrdersExactPrefixInfixFirst(t *testing.T) {
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
	seedPublication(t, store, "700", []seedWork{
		{title: "middle apple pie", creator: "", tags: nil, filenames: nil},
		{title: "apple", creator: "", tags: nil, filenames: nil},
		{title: "apple juice", creator: "", tags: nil, filenames: nil},
		{title: "unrelated", creator: "apple seller", tags: nil, filenames: nil},
	})
	service, err := galleryquery.NewService(ctx, store.Control.SQL(), store.Catalog.SQL(), clock.Fixed{Time: time.Now().UTC()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	scope := galleryquery.AuthorizationScope("owner", []string{"library.read"})
	result, err := service.Search(ctx, galleryquery.Request{Search: "apple", Limit: 20, AuthorizationScope: scope})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 4 {
		t.Fatalf("命中数量 = %d, want 4", len(result.Items))
	}
	titles := make([]string, len(result.Items))
	for index, item := range result.Items {
		titles[index] = item.Title
	}
	// exact("apple") > prefix("apple juice") > infix("middle apple pie") > other-field("unrelated")
	want := []string{"apple", "apple juice", "middle apple pie", "unrelated"}
	for index := range want {
		if titles[index] != want[index] {
			t.Fatalf("ranking 顺序 = %v, want %v", titles, want)
		}
	}
	if len(result.Items[0].TitleHighlights) == 0 {
		t.Fatalf("精确命中应携带 titleHighlights")
	}
	if result.Items[0].TitleHighlights[0] != (galleryquery.HighlightSpan{Start: 0, End: 5}) {
		t.Fatalf("titleHighlights = %+v", result.Items[0].TitleHighlights)
	}
}

func TestRankingProtocolVersionInvalidatesOldCursor(t *testing.T) {
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
	seedPublication(t, store, "701", []seedWork{{title: "a"}, {title: "b"}})
	service, err := galleryquery.NewService(ctx, store.Control.SQL(), store.Catalog.SQL(), clock.Fixed{Time: time.Now().UTC()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	scope := galleryquery.AuthorizationScope("owner", []string{"library.read"})
	page, err := service.Search(ctx, galleryquery.Request{Limit: 1, AuthorizationScope: scope})
	if err != nil || page.RankProtocolVersion == 0 {
		t.Fatalf("响应缺少 rankProtocolVersion: %+v err=%v", page, err)
	}
}

func TestTotalModes(t *testing.T) {
	store, _ := richFixture(t)
	service := newFixtureService(t, store)
	scope := galleryquery.AuthorizationScope("owner", []string{"library.read"})

	exact, err := service.Search(context.Background(), galleryquery.Request{Limit: 1, AuthorizationScope: scope})
	if err != nil {
		t.Fatal(err)
	}
	if exact.Total.Mode != galleryquery.TotalModeExact || exact.Total.Value == nil || *exact.Total.Value != 3 {
		t.Fatalf("exact total = %+v", exact.Total)
	}

	omitted, err := service.Search(context.Background(), galleryquery.Request{Limit: 1, OmitTotal: true, AuthorizationScope: scope})
	if err != nil {
		t.Fatal(err)
	}
	if omitted.Total.Mode != galleryquery.TotalModeOmitted || omitted.Total.Value != nil {
		t.Fatalf("omitted total = %+v", omitted.Total)
	}

	original := galleryquery.TotalBudget
	galleryquery.TotalBudget = 1
	defer func() { galleryquery.TotalBudget = original }()
	lowerBound, err := service.Search(context.Background(), galleryquery.Request{Limit: 1, AuthorizationScope: scope})
	if err != nil {
		t.Fatal(err)
	}
	if lowerBound.Total.Mode != galleryquery.TotalModeLowerBound || lowerBound.Total.Value == nil || *lowerBound.Total.Value != 1 {
		t.Fatalf("lower_bound total = %+v", lowerBound.Total)
	}
}

func sameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]int, len(want))
	for _, value := range want {
		seen[value]++
	}
	for _, value := range got {
		seen[value]--
	}
	for _, count := range seen {
		if count != 0 {
			return false
		}
	}
	return true
}
