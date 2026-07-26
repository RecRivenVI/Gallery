package httpapi_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/auth"
	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/config"
	"github.com/RecRivenVI/gallery/internal/contract/realtime"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/scanner"
	"github.com/RecRivenVI/gallery/internal/storage"
	"github.com/RecRivenVI/gallery/internal/transport/httpapi"
)

// newScanAuthorizationServer 是一个 LAN 模式测试服务端，与 newLANSecurityServer 的区别只是
// 真正接上了 Catalog 与 Scanner：createScanJob 通过授权之后必须能走到业务层，否则「不存在的
// sourceId 落到 404」这一半根本无法被观测，对比也就失去意义。
func newScanAuthorizationServer(t *testing.T) *httptest.Server {
	t.Helper()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	fixed := clock.Fixed{Time: time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)}
	manager, err := auth.NewPersonal(store.Control.SQL(), fixed, identity.NewGenerator(fixed), nil)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, fixed, identity.NewGenerator(fixed))
	if err != nil {
		t.Fatal(err)
	}
	jobStore, err := jobs.NewStore(store.Control.SQL(), fixed, identity.NewGenerator(fixed))
	if err != nil {
		t.Fatal(err)
	}
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), fixed, identity.NewGenerator(fixed))
	if err != nil {
		t.Fatal(err)
	}
	hub := realtime.NewHub(fixed)
	scannerService, err := scanner.New(context.Background(), resources, jobStore, catalogStore, hub)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.New(config.ModeLAN, store, fixed, manager, resources, jobStore, catalogStore,
		scannerService, nil, nil, nil, hub, slog.New(slog.NewJSONHandler(io.Discard, nil))))
	t.Cleanup(server.Close)
	return server
}

// TestScanJobCreationDoesNotRevealSourceExistence 是缺陷二的行为回归：对同一个持 global
// scan.run、但在某个 library 上被 deny 的主体，「存在但被拒的 sourceId」与「根本不存在的
// sourceId」必须返回完全相同的状态码。修复前前者是 403、后者是 404，两者之差就把整个
// Source ID 空间变成了可枚举的存在性预言机。
func TestScanJobCreationDoesNotRevealSourceExistence(t *testing.T) {
	server := newScanAuthorizationServer(t)
	owner, csrf := establishLANOwner(t, server)
	libraryDenied := createLibrary(t, owner, server, csrf, "Scan denied library")
	sourceDenied := createQueryAuthorizationSource(t, owner, server.URL, csrf, libraryDenied, "scan-denied")

	// owner 角色带来 global scan.run，deny 只落在这一个 library 上：这正是缺陷描述的主体。
	scoped := createAndLoginQueryAuthorizationUser(t, owner, server.URL, csrf, "scan-scoped", []string{"owner"}, []any{
		map[string]any{
			"effect": "deny", "capability": "scan.run",
			"scope": map[string]any{"kind": "library", "id": libraryDenied},
		},
	})
	scopedCSRF := bootstrapCSRF(t, scoped, server.URL)

	denied := requestJSON(t, scoped, http.MethodPost,
		server.URL+"/api/v1/sources/"+sourceDenied+"/scan-jobs", server.URL, scopedCSRF, map[string]any{})
	deniedBody := readAndClose(t, denied)
	missing := requestJSON(t, scoped, http.MethodPost,
		server.URL+"/api/v1/sources/"+mutateIDTail(t, sourceDenied)+"/scan-jobs", server.URL, scopedCSRF, map[string]any{})
	missingBody := readAndClose(t, missing)

	if denied.StatusCode == http.StatusForbidden {
		t.Fatalf("存在且被 deny 的 sourceId 仍返回裸 403，可与不存在的 ID 区分: body=%s", deniedBody)
	}
	if denied.StatusCode != missing.StatusCode {
		t.Fatalf("存在但被拒=%d，不存在=%d：状态码之差泄露 Source 是否存在 deniedBody=%s missingBody=%s",
			denied.StatusCode, missing.StatusCode, deniedBody, missingBody)
	}
	if denied.StatusCode != http.StatusNotFound {
		t.Fatalf("两条路径收敛到 %d，应为 404: body=%s", denied.StatusCode, deniedBody)
	}
}

// mutateIDTail 把一个真实 ID 的最后一个十六进制位改掉，得到一个格式合法但一定不存在的
// 同类 ID；直接用畸形字符串会在 ID 解析处就被拒，测不到授权与业务层的差异。
func mutateIDTail(t *testing.T, id string) string {
	t.Helper()
	if len(id) == 0 {
		t.Fatal("空 ID")
	}
	last := id[len(id)-1]
	replacement := byte('a')
	if last == 'a' {
		replacement = 'b'
	}
	return id[:len(id)-1] + string(replacement)
}

// TestResourceScopedAuthorizationFailuresAreConcealed 是一条路由契约：凡是第一道授权就
// 带资源作用域的处理器，其授权失败必须经 concealForbidden 收敛为 404。
//
// 缺陷形态：一个持 global `scan.run`、但在某个 library 上被 deny 的主体，向
// POST /api/v1/sources/{sourceId}/scan-jobs 提交攻击者自选的 sourceId 时——
//   - sourceId 存在且属于被 deny 的 library：Library→Source 查表命中 deny，返回 403；
//   - sourceId 不存在：查表 ErrNoRows，deny 不匹配，global allow 生效，通过授权后在业务
//     层落到 404。
//
// 于是 403 恰好等价于「这个 ID 真实存在且属于你被拒的 library」，整个 ID 空间可枚举。
// 本测试在修复前对 createScanJob 失败，并额外发现 createSource 有完全相同的形态——只是资源
// ID 来自请求体而不是路径。这正是它比修复本身更重要的原因：新增处理器只要接受调用方提供的
// 资源 ID 又忘了脱敏，这里就会红，而人工枚举漏掉的分支不会自己冒出来。
//
// 两类形态视为已满足契约：调用 concealForbidden 收敛为 404；或像聚合列表那样直接跳过被拒
// 条目、根本不写出 403（后者更严格）。
//
// 全局作用域（requireCapability）的处理器返回裸 403 是正确的——被判定的资源是服务器自身，
// 其存在不是秘密，因此本测试**不**要求它们脱敏。
func TestResourceScopedAuthorizationFailuresAreConcealed(t *testing.T) {
	files := parsePackageSources(t)
	var scoped, unchecked, staleExemptions []string
	usedExemptions := map[string]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !isServerHandler(fn) {
				continue
			}
			call, kind, ok := firstAuthorizationCall(fn)
			if !ok || kind == scopeGlobal {
				continue
			}
			scoped = append(scoped, fn.Name.Name)
			guard := guardFor(fn, call)
			// 把 403 过滤掉而不写出去，比脱敏更严格：调用方连「有东西被过滤了」都观测不到。
			if guardSkipsForbidden(guard) || guardConcealsForbidden(guard) {
				if _, exempt := concealExemptions[fn.Name.Name]; exempt {
					staleExemptions = append(staleExemptions, fn.Name.Name)
				}
				continue
			}
			if _, exempt := concealExemptions[fn.Name.Name]; exempt {
				usedExemptions[fn.Name.Name] = true
				continue
			}
			unchecked = append(unchecked, fn.Name.Name)
		}
	}
	sort.Strings(scoped)
	sort.Strings(unchecked)
	sort.Strings(staleExemptions)
	// 门槛既防止解析规则失效后测试静默通过，也在处理器被大量删除时提醒重新评估。当前实际
	// 识别出 33 个，取 20 作为下限：留出正常重构的余量，但解析规则一旦失效就会立刻暴露。
	if len(scoped) < 20 {
		t.Fatalf("只识别出 %d 个资源作用域处理器（%v），解析规则已经失效", len(scoped), scoped)
	}
	if len(unchecked) != 0 {
		t.Fatalf("以下处理器的资源作用域授权失败未经 concealForbidden，403/404 的差异会泄露资源是否存在: %v", unchecked)
	}
	// 豁免必须始终对应一个真实存在、且确实没有脱敏的处理器：处理器改好或被删掉之后豁免要
	// 一起消失，否则豁免表会变成永久放行的暗门。
	if len(staleExemptions) != 0 {
		t.Fatalf("以下处理器已经脱敏或已经过滤 403，请从 concealExemptions 中删除对应条目: %v", staleExemptions)
	}
	for name := range concealExemptions {
		if !usedExemptions[name] {
			t.Fatalf("concealExemptions 中的 %q 不是资源作用域处理器（可能已重命名或删除），豁免表已失效", name)
		}
	}
	t.Logf("已覆盖 %d 个资源作用域处理器: %v", len(scoped), scoped)
}

// concealExemptions 是经过审查、明确不收敛为 404 的资源作用域处理器。每一条都必须写明为什么
// 403 在这里不构成资源存在性预言机，或者为什么另有更强的约束覆盖它。新增条目等同于新增一处
// 安全例外，不得只为让测试变绿而添加。
var concealExemptions = map[string]string{
	// listWorks 对被 deny 的 sourceId/libraryId 过滤参数返回 403，是 EV-44 建立、并由
	// query_authorization_api_test.go 直接断言的既有契约。
	//
	// 这条豁免记录的是既有契约，不是「此处不构成预言机」的结论：传入一个不存在的 sourceId
	// 时 deny 不匹配、global allow 生效，该处理器返回 200 空集而不是 403，因此 403/200 仍
	// 然区分了「存在且被 deny」与「不存在」，与 createScanJob 修复前的形态同类。同为集合
	// 端点、同为「作用域由可选查询参数动态收窄」的 listBindingIssues 已经脱敏，说明这里不
	// 存在「集合端点不能脱敏」的技术障碍。收口它需要同时改动 EV-44 锁定的断言，属于单独
	// 决策，不在本轮范围。
	"listWorks": "403 由 EV-44 与 query_authorization_api_test.go 锁定；遗留预言机待单独决策",
}

type scopeKind int

const (
	scopeGlobal scopeKind = iota
	scopeResource
)

// authorizationHelpers 是 Server 上会做出「允许/拒绝」判定的方法。requireCapability 固定
// 使用 global 作用域；authorizeJob 对绑定 Source 的 Job 使用 source 作用域，因此按资源作
// 用域处理。
var authorizationHelpers = map[string]int{
	"requireCapability":         -1, // 无作用域实参，恒为 global
	"requireCapabilityForScope": 2,
	"authorizeSession":          3,
	"authorizeJob":              -2, // 作用域由 Job 自身决定，按资源作用域处理
}

func parsePackageSources(t *testing.T) []*ast.File {
	t.Helper()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("枚举包内源文件失败: %v", err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", name, err)
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		t.Fatal("未找到任何包内源文件，测试本身已失效")
	}
	return files
}

// isServerHandler 判定一个方法是否是 net/http 处理器：*Server 接收者、(ResponseWriter,
// *Request) 参数、无返回值。
func isServerHandler(fn *ast.FuncDecl) bool {
	if fn.Recv == nil || len(fn.Recv.List) != 1 || fn.Body == nil || fn.Type.Results != nil {
		return false
	}
	star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	if name, ok := star.X.(*ast.Ident); !ok || name.Name != "Server" {
		return false
	}
	params := fn.Type.Params.List
	total := 0
	for _, param := range params {
		total += max(len(param.Names), 1)
	}
	if total != 2 {
		return false
	}
	return strings.HasSuffix(exprText(params[0].Type), "http.ResponseWriter") &&
		strings.HasSuffix(exprText(params[len(params)-1].Type), "*http.Request")
}

// firstAuthorizationCall 返回处理器中第一次授权判定及其作用域类别。只看第一道是因为一旦
// 第一道就是 global capability，攻击者必须先持有该 capability 才可能观测到后续差异，403
// 本身不再构成资源存在性预言机。
func firstAuthorizationCall(fn *ast.FuncDecl) (*ast.CallExpr, scopeKind, bool) {
	var found *ast.CallExpr
	var kind scopeKind
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if found != nil {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		index, ok := authorizationHelpers[selector.Sel.Name]
		if !ok {
			return true
		}
		found = call
		switch {
		case index == -1:
			kind = scopeGlobal
		case index == -2 || index >= len(call.Args):
			kind = scopeResource
		default:
			kind = scopeKindOf(call.Args[index])
		}
		return false
	})
	return found, kind, found != nil
}

// scopeKindOf 只把字面量 Kind 为 "global" 的作用域认定为全局；无法静态判定时按资源作用域
// 处理，保证新写法默认落在更严格的一侧。
func scopeKindOf(arg ast.Expr) scopeKind {
	composite, ok := arg.(*ast.CompositeLit)
	if !ok {
		return scopeResource
	}
	for _, element := range composite.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := pair.Key.(*ast.Ident)
		if !ok || key.Name != "Kind" {
			continue
		}
		literal, ok := pair.Value.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return scopeResource
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil && value == "global" {
			return scopeGlobal
		}
		return scopeResource
	}
	return scopeResource
}

// guardSkipsForbidden 识别聚合列表处理器的过滤形态：授权失败是 FORBIDDEN 时跳过该条目而不
// 写出任何响应。这比脱敏更强——调用方拿到的列表里既没有 403 也没有被拒条目的任何痕迹。
func guardSkipsForbidden(guard *ast.IfStmt) bool {
	if guard == nil {
		return false
	}
	mentionsForbidden := false
	skips := false
	ast.Inspect(guard.Body, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.SelectorExpr:
			if typed.Sel.Name == "CodeForbidden" {
				mentionsForbidden = true
			}
		case *ast.BranchStmt:
			if typed.Tok == token.CONTINUE {
				skips = true
			}
		}
		return true
	})
	return mentionsForbidden && skips
}

// guardConcealsForbidden 检查包住该次授权调用的 err != nil 分支，要求分支里写出的每一个错误
// 都经过 concealForbidden。
func guardConcealsForbidden(guard *ast.IfStmt) bool {
	if guard == nil {
		return false
	}
	responses := 0
	concealed := 0
	ast.Inspect(guard.Body, func(node ast.Node) bool {
		inner, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := inner.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "writeRequestError" || len(inner.Args) == 0 {
			return true
		}
		responses++
		wrapper, ok := inner.Args[len(inner.Args)-1].(*ast.CallExpr)
		if !ok {
			return true
		}
		if name, ok := wrapper.Fun.(*ast.Ident); ok && name.Name == "concealForbidden" {
			concealed++
		}
		return true
	})
	return responses > 0 && responses == concealed
}

// guardFor 返回紧跟在授权调用之后、或以该调用为 Init 的 if 语句。取包含该调用的最内层语句，
// 避免把外层 for/if 误判为守卫。
func guardFor(fn *ast.FuncDecl, call *ast.CallExpr) *ast.IfStmt {
	var guard *ast.IfStmt
	var bestSpan token.Pos = -1
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		block, ok := node.(*ast.BlockStmt)
		if !ok {
			return true
		}
		for index, stmt := range block.List {
			if !containsNode(stmt, call) {
				continue
			}
			span := stmt.End() - stmt.Pos()
			if bestSpan >= 0 && span >= bestSpan {
				continue
			}
			var candidate *ast.IfStmt
			if ifStmt, ok := stmt.(*ast.IfStmt); ok && ifStmt.Init != nil && containsNode(ifStmt.Init, call) {
				candidate = ifStmt
			} else if index+1 < len(block.List) {
				if next, ok := block.List[index+1].(*ast.IfStmt); ok {
					candidate = next
				}
			}
			bestSpan = span
			guard = candidate
		}
		return true
	})
	return guard
}

func containsNode(root ast.Node, target ast.Node) bool {
	found := false
	ast.Inspect(root, func(node ast.Node) bool {
		if found {
			return false
		}
		if node == target {
			found = true
			return false
		}
		return true
	})
	return found
}

func exprText(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.StarExpr:
		return "*" + exprText(typed.X)
	case *ast.SelectorExpr:
		return exprText(typed.X) + "." + typed.Sel.Name
	default:
		return ""
	}
}
