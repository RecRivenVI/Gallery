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
		hidden                                                                                                bool
		favorite                                                                                              bool
		progress                                                                                              float64
	}
	specs := []workSpec{
		{id: workA, sourceID: "src_test", sourceKey: "alpha", title: "Alpha Work", providerID: "providerA", creatorID: ctrRoot, creatorRole: "author", mediaKind: "image", location: "present", verification: "content_verified", favorite: true, progress: 0.25},
		{id: workB, sourceID: "src_test", sourceKey: "beta", title: "Beta Work", providerID: "providerB", creatorID: ctrAbsorbed, creatorRole: "author", mediaKind: "video", location: "offline", verification: "located_unverified", favorite: false, progress: 0.75},
		{id: workC, sourceID: "src_other", sourceKey: "gamma", title: "Gamma Work", providerID: "providerA", creatorID: ctrRoot, creatorRole: "illustrator", mediaKind: "image", location: "present", verification: "content_verified", favorite: true, progress: 1},
	}
	for index, spec := range specs {
		document := querytext.BuildDocument(spec.title, "", nil, nil)
		exec(`INSERT INTO source_works
(catalog_revision_id, source_id, source_key, title, creator, tags_json, filenames_text, provider_id, external_id)
VALUES (?, ?, ?, ?, '', '[]', '', ?, '')`, cat, spec.sourceID, spec.sourceKey, spec.title, spec.providerID)
		hidden, favorite := 0, 0
		if spec.hidden {
			hidden = 1
		}
		if spec.favorite {
			favorite = 1
		}
		exec(`INSERT INTO work_projections
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id, title, creator, tags_json, filenames_text,
 normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text, sort_title_key, hidden, favorite, progress,
 search_title_norm, search_creator_norm, search_tags_norm, search_filenames_norm)
VALUES (?, ?, ?, ?, ?, 'lib_test', ?, '', '[]', '', ?, ?, ?, ?, ?, ?, ?, ?, '', '', '')`,
			cat, ov, spec.id, spec.sourceID, spec.sourceKey, spec.title,
			document.NormalizedOriginal, document.CJKTokens, document.LatinTokens, document.SortTitleKey,
			hidden, favorite, spec.progress, document.TitleNorm)
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
		{"overlay.favorite", `{"field":"overlay.favorite","op":"eq","value":true}`, []string{"Alpha Work", "Gamma Work"}},
		{"overlay.progress_gte", `{"field":"overlay.progress","op":"gte","value":0.5}`, []string{"Beta Work", "Gamma Work"}},
		{"overlay.progress_lt", `{"field":"overlay.progress","op":"lt","value":0.5}`, []string{"Alpha Work"}},
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
		// 顶层尾随 JSON：两个独立对象拼接、或紧跟一个额外数值——decoder.Decode 单次
		// 调用不会拒绝这些，必须靠"再解码一次并要求 io.EOF"才能识别。
		`{"field":"tag","op":"eq","value":"a"} {"field":"tag","op":"eq","value":"b"}`,
		`{"field":"tag","op":"eq","value":"a"} 123`,
		// 尾随字节恰好以 '}' 或 ']' 开头：encoding/json 的 Decoder.More() 会把这类
		// 尾随内容误判为"当前容器结束"而不视为额外数据，是本轮修正的具体回归目标；
		// 必须用"再 Decode 一次判断 io.EOF"的方式才能正确拒绝。
		`{"field":"tag","op":"eq","value":"a"}}`,
		`{"field":"tag","op":"eq","value":"a"}]`,
	} {
		_, err := service.Search(context.Background(), galleryquery.Request{Filter: filter, Limit: 20, AuthorizationScope: scope})
		assertCode(t, err, fault.CodeValidation)
	}
}

// TestParseFilterAcceptsCleanSingleObject 确认修正尾随 JSON 校验后，正常的单个 filter
// 对象（含结尾空白）仍然被接受，不会被过度严格化误伤。
func TestParseFilterAcceptsCleanSingleObject(t *testing.T) {
	for _, raw := range []string{
		`{"field":"tag","op":"eq","value":"a"}`,
		"{\"field\":\"tag\",\"op\":\"eq\",\"value\":\"a\"}   \n  ",
	} {
		node, err := galleryquery.ParseFilter(raw)
		if err != nil || node == nil {
			t.Fatalf("合法单个 filter 对象被拒绝: raw=%q err=%v", raw, err)
		}
	}
}

// TestOverlayHiddenExplicitFilterRequiresLibraryWriteCapability 覆盖 06-查询-搜索与
// 排序.md「Overlay 查询依赖」对 Hidden 可见性的要求：默认隐式条件隐藏 Hidden Work；
// 显式查询 overlay.hidden 接管可见性语义，需要 library.write capability，没有该
// capability 时返回结构化 FORBIDDEN，不静默退化为默认行为或空结果。
func TestOverlayHiddenExplicitFilterRequiresLibraryWriteCapability(t *testing.T) {
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
	catalogDB := store.Catalog.SQL()
	const cat = "cat_018f47d2-5c16-7a44-a8a0-000000000950"
	const ov = "ovr_018f47d2-5c16-7a44-a8a0-000000000950"
	const job = "job_018f47d2-5c16-7a44-a8a0-000000000950"
	const pub = "qpub_018f47d2-5c16-7a44-a8a0-000000000950"
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := catalogDB.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("seed 失败: %v query=%s", err, query)
		}
	}
	exec("INSERT INTO catalog_revisions VALUES (?, ?, 'src_test', 'published', 1, 1)", cat, job)
	exec(`INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, published_at)
VALUES (?, ?, 1, 'published', 1, 1)`, ov, cat)
	exec("INSERT INTO query_publications VALUES (?, ?, ?, ?, 1, 1)", pub, cat, ov, job)
	seedHiddenWork := func(id, title string, hidden int) {
		document := querytext.BuildDocument(title, "", nil, nil)
		exec(`INSERT INTO work_projections
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id, title, creator, tags_json, filenames_text,
 normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text, sort_title_key, hidden, favorite, progress,
 search_title_norm, search_creator_norm, search_tags_norm, search_filenames_norm)
VALUES (?, ?, ?, 'src_test', ?, 'lib_test', ?, '', '[]', '', ?, ?, ?, ?, ?, 0, 0, ?, '', '', '')`,
			cat, ov, id, id, title, document.NormalizedOriginal, document.CJKTokens, document.LatinTokens, document.SortTitleKey, hidden, document.TitleNorm)
	}
	seedHiddenWork("wrk_018f47d2-5c16-7a44-a8a0-000000000951", "Visible Work", 0)
	seedHiddenWork("wrk_018f47d2-5c16-7a44-a8a0-000000000952", "Hidden Work", 1)
	exec(`INSERT INTO active_query_publication VALUES (1, ?) ON CONFLICT(singleton) DO UPDATE SET query_publication_id=excluded.query_publication_id`, pub)

	service := newFixtureService(t, store)

	defaultResult, err := service.Search(ctx, galleryquery.Request{Limit: 20,
		AuthorizationScope: galleryquery.AuthorizationScope("owner", []string{"library.read"}), Capabilities: []string{"library.read"}})
	if err != nil {
		t.Fatal(err)
	}
	if titles := titlesOf(defaultResult.Items); !sameSet(titles, []string{"Visible Work"}) {
		t.Fatalf("默认查询应隐式隐藏 Hidden Work: %v", titles)
	}

	_, err = service.Search(ctx, galleryquery.Request{Limit: 20, Filter: `{"field":"overlay.hidden","op":"eq","value":true}`,
		AuthorizationScope: galleryquery.AuthorizationScope("owner", []string{"library.read"}), Capabilities: []string{"library.read"}})
	assertCode(t, err, fault.CodeForbidden)

	hiddenResult, err := service.Search(ctx, galleryquery.Request{Limit: 20, Filter: `{"field":"overlay.hidden","op":"eq","value":true}`,
		AuthorizationScope: galleryquery.AuthorizationScope("owner", []string{"library.read", "library.write"}), Capabilities: []string{"library.read", "library.write"}})
	if err != nil {
		t.Fatal(err)
	}
	if titles := titlesOf(hiddenResult.Items); !sameSet(titles, []string{"Hidden Work"}) {
		t.Fatalf("持有 library.write 显式查询 hidden=true 应只返回 Hidden Work: %v", titles)
	}

	// 显式引用 overlay.hidden 统一要求 library.write，即使值是 false：这避免了
	// "NOT hidden=true"与默认隐式条件产生不可解释的双重语义的一般情形（对任意深度
	// 的 all/any/not 组合判断"最终是否只暴露非 Hidden 作品"等价于布尔可满足性问题），
	// 用统一门槛换取简单、无歧义、可审计的规则。
	_, err = service.Search(ctx, galleryquery.Request{Limit: 20, Filter: `{"field":"overlay.hidden","op":"eq","value":false}`,
		AuthorizationScope: galleryquery.AuthorizationScope("owner", []string{"library.read"}), Capabilities: []string{"library.read"}})
	assertCode(t, err, fault.CodeForbidden)

	visibleResult, err := service.Search(ctx, galleryquery.Request{Limit: 20, Filter: `{"field":"overlay.hidden","op":"eq","value":false}`,
		AuthorizationScope: galleryquery.AuthorizationScope("owner", []string{"library.read", "library.write"}), Capabilities: []string{"library.read", "library.write"}})
	if err != nil {
		t.Fatal(err)
	}
	if titles := titlesOf(visibleResult.Items); !sameSet(titles, []string{"Visible Work"}) {
		t.Fatalf("显式 hidden=false 应等价于默认行为: %v", titles)
	}
}

func titlesOf(items []galleryquery.Work) []string {
	titles := make([]string, len(items))
	for index, item := range items {
		titles[index] = item.Title
	}
	return titles
}

// TestDependencySetReflectsActualRequest 覆盖动态 dependency set：不同请求形状产出不同
// 的 dependencySet，不是把全部已注册字段的静态能力表当成每次查询的依赖集合。
func TestDependencySetReflectsActualRequest(t *testing.T) {
	store, _ := richFixture(t)
	service := newFixtureService(t, store)
	scope := galleryquery.AuthorizationScope("owner", []string{"library.read"})

	plain, err := service.Search(context.Background(), galleryquery.Request{Limit: 20, AuthorizationScope: scope})
	if err != nil {
		t.Fatal(err)
	}
	if hasDependencyField(plain.DependencySet, "overlay.favorite") {
		t.Fatalf("未过滤 favorite 的查询不应把 overlay.favorite 纳入 dependencySet: %+v", plain.DependencySet)
	}
	if !hasDependencyRole(plain.DependencySet, "overlay.hidden", galleryquery.DependencyRoleMembership) {
		t.Fatalf("默认隐式隐藏应作为 membership 依赖出现: %+v", plain.DependencySet)
	}

	filtered, err := service.Search(context.Background(), galleryquery.Request{Limit: 20, AuthorizationScope: scope,
		Filter: `{"field":"overlay.favorite","op":"eq","value":true}`})
	if err != nil {
		t.Fatal(err)
	}
	if !hasDependencyRole(filtered.DependencySet, "overlay.favorite", galleryquery.DependencyRolePredicate) {
		t.Fatalf("显式过滤 favorite 应把 overlay.favorite 记为 predicate 依赖: %+v", filtered.DependencySet)
	}

	searched, err := service.Search(context.Background(), galleryquery.Request{Limit: 20, AuthorizationScope: scope, Search: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"title", "creator", "tag", "filename"} {
		if !hasDependencyRole(searched.DependencySet, field, galleryquery.DependencyRoleSearch) {
			t.Fatalf("带搜索词的查询应把 %s 记为 search 依赖: %+v", field, searched.DependencySet)
		}
	}
	if len(plain.LiveUserStateFields) == 0 || !contains(plain.LiveUserStateFields, "favorite") || !contains(plain.LiveUserStateFields, "progress") {
		t.Fatalf("响应应声明 favorite/progress 具备 live 展示: %+v", plain.LiveUserStateFields)
	}
}

func hasDependencyField(fields []galleryquery.DependencyField, name string) bool {
	for _, field := range fields {
		if field.Field == name {
			return true
		}
	}
	return false
}

func hasDependencyRole(fields []galleryquery.DependencyField, name, role string) bool {
	for _, field := range fields {
		if field.Field == name && field.Role == role {
			return true
		}
	}
	return false
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
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
	// 字段级 ranking：match_class 优先于 field_priority，因此 Creator 前缀命中
	// ("unrelated"，creator="apple seller"，combined=2*10+2=22) 排在标题中缀命中
	// ("middle apple pie"，combined=1*10+3=13) 之前——完全匹配("apple"，33)优于
	// 标题前缀("apple juice"，23)优于 Creator 前缀(22)优于标题中缀(13)，即使标题
	// 字段本身优先级更高，也不能让低 match_class 反超高 match_class。
	want := []string{"apple", "apple juice", "unrelated", "middle apple pie"}
	for index := range want {
		if titles[index] != want[index] {
			t.Fatalf("ranking 顺序 = %v, want %v", titles, want)
		}
	}
	exactMatch := result.Items[0]
	if len(exactMatch.Matches) == 0 || exactMatch.Matches[0].Field != "title" || len(exactMatch.Matches[0].Spans) == 0 {
		t.Fatalf("精确命中应携带 title 字段的通用 matches: %+v", exactMatch.Matches)
	}
	if exactMatch.Matches[0].Spans[0] != (galleryquery.MatchSpan{Start: 0, End: 5}) {
		t.Fatalf("matches spans = %+v", exactMatch.Matches[0].Spans)
	}
	creatorMatch := result.Items[2]
	if creatorMatch.Title != "unrelated" {
		t.Fatalf("第三名应为 Creator 前缀命中的 unrelated: %+v", creatorMatch)
	}
	foundCreatorMatch := false
	for _, match := range creatorMatch.Matches {
		if match.Field == "creator" && match.Value == "apple seller" {
			foundCreatorMatch = true
		}
	}
	if !foundCreatorMatch {
		t.Fatalf("Creator 命中的 Work 应携带 field=creator 的 matches: %+v", creatorMatch.Matches)
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
