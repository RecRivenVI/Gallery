package query_test

// ParseFilter 解析的是**完全由客户端控制**的 filter JSON，而它的输出同时驱动两件
// 与授权直接相关的事：
//
//  1. transport 的 FilterReferencesField 预检——filter 里出现 overlay.hidden 就要求
//     额外的 library.write capability；
//  2. 查询指纹——FilterNode 的 canonical 编码（即 json.Marshal(node)）参与
//     queryFingerprint，而 queryFingerprint 被签进游标，续页时决定服务端按哪一组
//     授权范围继续返回行。
//
// 因此本文件断言的不是"解析不崩"，而是三类硬性属性：
//
//   - **形状守卫不可绕过**：任何被接受的 AST，深度 ≤ 6 且节点数 ≤ 64（独立重算，
//     不复用实现的计数器）。这是防"深递归 filter 打爆 compileFilter/SQL 生成"的唯一防线。
//   - **往返稳定**：ParseFilter 必须接受自己的 canonical 编码，且再编码逐字节不变。
//     若不成立，客户端重放同一 filter 会算出不同指纹，续页链路直接断掉。
//   - **叶子字段封闭**：任何被接受的叶子字段都必须在 FieldNames() 注册表内，且
//     FilterReferencesField 对每个注册字段的判定与独立遍历一致——授权预检漏看一个
//     位置（例如藏在 not 里的 overlay.hidden）就是一次越权。

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	galleryquery "github.com/RecRivenVI/gallery/internal/query"
)

// 与 internal/query/filter.go 的 maxFilterDepth / maxFilterNodes 对应。它们是**被测
// 常量的独立副本**：实现改了值而没改这里，本测试就会失败，从而强制这次放宽被显式审视。
const (
	expectedMaxFilterDepth = 6
	expectedMaxFilterNodes = 64
)

func FuzzParseFilter(f *testing.F) {
	for _, seed := range filterSeeds() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		node, err := galleryquery.ParseFilter(raw)

		// 确定性：同一输入必须给出同一结论，否则指纹不可复现。
		repeatNode, repeatErr := galleryquery.ParseFilter(raw)
		if (err == nil) != (repeatErr == nil) {
			t.Fatalf("ParseFilter 不确定: %q", truncateFilter(raw))
		}
		if err == nil && !reflect.DeepEqual(node, repeatNode) {
			t.Fatalf("ParseFilter 对同一输入产生了不同 AST: %q", truncateFilter(raw))
		}

		if err != nil {
			if node != nil {
				t.Fatalf("拒绝路径必须返回 nil AST: %q -> %+v", truncateFilter(raw), node)
			}
			var structured *fault.Error
			if !errors.As(err, &structured) || structured.Code != fault.CodeValidation {
				t.Fatalf("拒绝必须是 VALIDATION_ERROR，实际 %v", err)
			}
			if !strings.HasPrefix(structured.Field, "filter") {
				t.Fatalf("拒绝的 field 必须落在 filter 命名空间，实际 %q", structured.Field)
			}
			return
		}

		if node == nil {
			// 唯一合法的 (nil, nil)：空白输入表示"未提供结构化过滤"。
			if strings.TrimSpace(raw) != "" {
				t.Fatalf("非空输入 %q 既未被拒绝也未产生 AST", truncateFilter(raw))
			}
			return
		}

		assertFilterShapeGuard(t, node, raw)
		assertFilterLeavesRegistered(t, node, raw)
		assertFilterFingerprintRoundTrip(t, node, raw)
		assertFilterReferenceDetection(t, node, raw)
	})
}

// assertFilterShapeGuard 独立重算深度与节点数。实现用 validateShape 里的计数器，
// 这里用一次纯遍历，两者必须给出同一结论。
func assertFilterShapeGuard(t *testing.T, node *galleryquery.FilterNode, raw string) {
	t.Helper()
	depth, count := filterDepthAndCount(node)
	if depth > expectedMaxFilterDepth {
		t.Fatalf("形状守卫被绕过：接受了深度 %d（上限 %d）: %q",
			depth, expectedMaxFilterDepth, truncateFilter(raw))
	}
	if count > expectedMaxFilterNodes {
		t.Fatalf("形状守卫被绕过：接受了 %d 个节点（上限 %d）: %q",
			count, expectedMaxFilterNodes, truncateFilter(raw))
	}
}

// assertFilterLeavesRegistered 复核"恰好一个分支有效"以及叶子字段在注册表内。
// 若某个节点同时带 all 与 field，compileFilter 与 filterReferencesField 的 switch
// 顺序会各自选一条分支，授权预检看到的树就和实际编译的 SQL 不是同一棵。
func assertFilterLeavesRegistered(t *testing.T, node *galleryquery.FilterNode, raw string) {
	t.Helper()
	registered := map[string]bool{}
	for _, name := range galleryquery.FieldNames() {
		registered[name] = true
	}
	var walk func(*galleryquery.FilterNode)
	walk = func(current *galleryquery.FilterNode) {
		if current == nil {
			t.Fatalf("AST 含 nil 节点: %q", truncateFilter(raw))
		}
		branches := 0
		if current.All != nil {
			branches++
		}
		if current.Any != nil {
			branches++
		}
		if current.Not != nil {
			branches++
		}
		if current.Field != "" {
			branches++
		}
		if branches != 1 {
			t.Fatalf("被接受的节点有 %d 个有效分支（必须恰好 1 个）: %q",
				branches, truncateFilter(raw))
		}
		switch {
		case len(current.All) > 0:
			for index := range current.All {
				walk(&current.All[index])
			}
		case len(current.Any) > 0:
			for index := range current.Any {
				walk(&current.Any[index])
			}
		case current.Not != nil:
			walk(current.Not)
		default:
			if !registered[current.Field] {
				t.Fatalf("接受了未注册字段 %q: %q", current.Field, truncateFilter(raw))
			}
		}
	}
	walk(node)
}

// assertFilterFingerprintRoundTrip 是授权正确性属性。
//
// canonical 编码就是 json.Marshal(node)（filter.go 的 canonicalJSON 只做这一步），
// 它参与 queryFingerprint，而 queryFingerprint 被签进游标。必须成立：
//   - ParseFilter 接受自己的 canonical 编码（否则续页时重算指纹会直接失败）；
//   - 再编码逐字节不变（**不动点**，否则同一 filter 的指纹随重放次数漂移）；
//   - 不动点之后 AST 也稳定。
//
// 注意这里断言的是"一轮之后成为不动点"，而不是"canonical 编码等于原文解析出的 AST"。
// 后者不成立且不必成立：Value 是 json.RawMessage，json.Marshal 会对它做 compact 与
// HTML 转义（`<` → `<`），因此首轮编码必然与客户端原文不同。真正致命的是"永远
// 不收敛"，那会让续页指纹每轮都变。首轮差异的**副作用**另见
// TestFilterFingerprintIsNotSemanticallyCanonical。
func assertFilterFingerprintRoundTrip(t *testing.T, node *galleryquery.FilterNode, raw string) {
	t.Helper()
	canonical, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("canonical 编码失败: %v: %q", err, truncateFilter(raw))
	}
	reparsed, err := galleryquery.ParseFilter(string(canonical))
	if err != nil {
		t.Fatalf("ParseFilter 拒绝了自己的 canonical 编码: %v\ncanonical=%s",
			err, truncateFilter(string(canonical)))
	}
	if reparsed == nil {
		t.Fatalf("canonical 编码被解析成空 AST: %s", truncateFilter(string(canonical)))
	}
	again, err := json.Marshal(reparsed)
	if err != nil {
		t.Fatalf("二次 canonical 编码失败: %v", err)
	}
	if string(again) != string(canonical) {
		t.Fatalf("查询指纹不是不动点，同一 filter 重放会算出不同指纹\n第一次: %s\n第二次: %s",
			truncateFilter(string(canonical)), truncateFilter(string(again)))
	}
	// 不动点之后 AST 必须稳定，否则 compileFilter 在续页时生成的 SQL 会与首页不同。
	settled, err := galleryquery.ParseFilter(string(again))
	if err != nil || settled == nil {
		t.Fatalf("不动点编码无法重新解析: %v", err)
	}
	if !reflect.DeepEqual(reparsed, settled) {
		t.Fatalf("canonical 不动点上 AST 仍在变化: %q", truncateFilter(raw))
	}
}

// TestFilterFingerprintIsNotSemanticallyCanonical 记录一条**当前行为**：查询指纹绑定
// 的是客户端提交的字节，而不是 filter 的语义。
//
// FilterNode.Value 是 json.RawMessage，ParseFilter 原样保留其字节。因此下面每一对
// filter 在语义上完全相同，却会算出不同的 canonical 编码，进而算出不同的
// queryFingerprint。后果是：客户端如果在翻页之间重新序列化了一次 filter（换个 JSON
// 库、改个缩进、把 `<` 写成 `<`），续页就会因指纹不匹配而拿到 CURSOR_INVALID。
//
// 这**不是**越权风险——危险的方向是"语义不同却指纹相同"，那会让客户端拿着 A 的游标
// 去续 B 的页；这里是反方向，只会多拒绝、不会多放行。因此登记为健壮性/互操作缺陷，
// 不作为安全门禁。若将来引入语义级 canonical 化（例如对 Value 也做规范 JSON），
// 本测试会失败并提示改写成正向断言。
func TestFilterFingerprintIsNotSemanticallyCanonical(t *testing.T) {
	pairs := [][2]string{
		{`{"field":"tag","op":"eq","value":[1,2]}`, `{"field":"tag","op":"eq","value":[1, 2]}`},
		{`{"field":"tag","op":"eq","value":{"a":1,"b":2}}`, `{"field":"tag","op":"eq","value":{"b":2,"a":1}}`},
		{`{"field":"tag","op":"eq","value":"<"}`, `{"field":"tag","op":"eq","value":"<"}`},
		{`{"field":"overlay.progress","op":"eq","value":0.5}`, `{"field":"overlay.progress","op":"eq","value":5e-1}`},
	}
	var diverged [][2]string
	for _, pair := range pairs {
		left, leftErr := galleryquery.ParseFilter(pair[0])
		right, rightErr := galleryquery.ParseFilter(pair[1])
		if leftErr != nil || rightErr != nil {
			t.Fatalf("种子对必须都能解析: %v / %v", leftErr, rightErr)
		}
		leftCanonical, _ := json.Marshal(left)
		rightCanonical, _ := json.Marshal(right)
		if string(leftCanonical) != string(rightCanonical) {
			diverged = append(diverged, pair)
		}
	}
	if len(diverged) == 0 {
		t.Fatalf("查询指纹已经语义规范化，请把本测试改写成正向断言")
	}
	for _, pair := range diverged {
		t.Logf("已知行为：语义等价但指纹不同\n  A=%s\n  B=%s", pair[0], pair[1])
	}
}

// assertFilterReferenceDetection 复核授权预检的字段探测。
//
// transport 用 FilterReferencesField 决定是否要求额外 capability；它必须能看见
// AST 里**任意深度、任意 all/any/not 组合**下的字段引用。漏看一处就是一次越权。
func assertFilterReferenceDetection(t *testing.T, node *galleryquery.FilterNode, raw string) {
	t.Helper()
	actual := map[string]bool{}
	collectFilterLeafFields(node, actual)
	for _, name := range galleryquery.FieldNames() {
		want := actual[name]
		got := galleryquery.FilterReferencesField(node, name)
		if want != got {
			t.Fatalf("FilterReferencesField(%q) = %v，独立遍历得到 %v: %q",
				name, got, want, truncateFilter(raw))
		}
	}
	// 往返后判定必须不变：授权预检发生在解析之后，指纹重算发生在续页时，两者
	// 看到的必须是同一棵树。
	canonical, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("canonical 编码失败: %v", err)
	}
	reparsed, err := galleryquery.ParseFilter(string(canonical))
	if err != nil || reparsed == nil {
		t.Fatalf("canonical 编码无法重新解析: %v", err)
	}
	for _, name := range galleryquery.FieldNames() {
		if galleryquery.FilterReferencesField(node, name) != galleryquery.FilterReferencesField(reparsed, name) {
			t.Fatalf("往返改变了 FilterReferencesField(%q) 的判定: %q", name, truncateFilter(raw))
		}
	}
}

func collectFilterLeafFields(node *galleryquery.FilterNode, into map[string]bool) {
	if node == nil {
		return
	}
	switch {
	case len(node.All) > 0:
		for index := range node.All {
			collectFilterLeafFields(&node.All[index], into)
		}
	case len(node.Any) > 0:
		for index := range node.Any {
			collectFilterLeafFields(&node.Any[index], into)
		}
	case node.Not != nil:
		collectFilterLeafFields(node.Not, into)
	default:
		if node.Field != "" {
			into[node.Field] = true
		}
	}
}

func filterDepthAndCount(node *galleryquery.FilterNode) (depth, count int) {
	if node == nil {
		return 0, 0
	}
	count = 1
	deepest := 0
	visit := func(child *galleryquery.FilterNode) {
		childDepth, childCount := filterDepthAndCount(child)
		if childDepth > deepest {
			deepest = childDepth
		}
		count += childCount
	}
	for index := range node.All {
		visit(&node.All[index])
	}
	for index := range node.Any {
		visit(&node.Any[index])
	}
	if node.Not != nil {
		visit(node.Not)
	}
	return deepest + 1, count
}

func truncateFilter(value string) string {
	const limit = 300
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

// ---------- 种子语料 ----------

func filterSeeds() []string {
	seeds := []string{
		// 空与空白：唯一允许的 (nil, nil)
		"", " ", "\t", "\n", "   \r\n  ",
		// 合法叶子
		`{"field":"library.id","op":"eq","value":"lib_1"}`,
		`{"field":"tag","op":"eq","value":"x"}`,
		`{"field":"overlay.hidden","op":"eq","value":true}`,
		`{"field":"overlay.favorite","op":"eq","value":false}`,
		`{"field":"overlay.progress","op":"gte","value":0.5}`,
		`{"field":"overlay.progress","op":"lt","value":1}`,
		// 缺 value（validateShape 不检查 value，属当前行为）
		`{"field":"tag","op":"eq"}`,
		// 未知字段 / 未知 op / 未知 JSON key
		`{"field":"nope","op":"eq","value":1}`,
		`{"field":"tag","op":"like","value":"x"}`,
		`{"field":"overlay.progress","op":"between","value":1}`,
		`{"field":"tag","op":"eq","value":"x","extra":1}`,
		`{"unknown":1}`,
		// 分支数不为 1
		`{}`,
		`{"all":[{"field":"tag","op":"eq","value":"x"}],"field":"tag","op":"eq"}`,
		`{"all":[],"any":[]}`,
		`{"all":[]}`,
		`{"any":[]}`,
		`{"not":null}`,
		`{"all":null}`,
		// 组合
		`{"all":[{"field":"tag","op":"eq","value":"a"},{"field":"tag","op":"eq","value":"b"}]}`,
		`{"any":[{"field":"tag","op":"eq","value":"a"},{"not":{"field":"overlay.hidden","op":"eq","value":true}}]}`,
		`{"not":{"not":{"not":{"field":"overlay.hidden","op":"eq","value":false}}}}`,
		// 非对象顶层
		`[]`, `null`, `true`, `1`, `"x"`, `[{"field":"tag","op":"eq","value":"x"}]`,
		// 尾随垃圾（filter.go 有专门注释处理 '}' / ']' 尾随）
		`{"field":"tag","op":"eq","value":"x"}}`,
		`{"field":"tag","op":"eq","value":"x"}]`,
		`{"field":"tag","op":"eq","value":"x"} {"field":"tag","op":"eq","value":"y"}`,
		`{"field":"tag","op":"eq","value":"x"},`,
		`{"field":"tag","op":"eq","value":"x"}` + "\n\n",
		// value 里的转义与 HTML 字符（canonical 编码会转义，考验不动点）
		`{"field":"tag","op":"eq","value":"<script>"}`,
		`{"field":"tag","op":"eq","value":"a&b"}`,
		`{"field":"tag","op":"eq","value":"<"}`,
		`{"field":"tag","op":"eq","value":" "}`,
		`{"field":"tag","op":"eq","value":"Å"}`,
		`{"field":"tag","op":"eq","value":"Å"}`,
		`{"field":"tag","op":"eq","value":"\ud800"}`,
		// value 内部空白（考验指纹是否随排版漂移）
		`{"field":"tag","op":"eq","value":[1, 2]}`,
		`{"field":"tag","op":"eq","value":[1,2]}`,
		`{"field":"tag","op":"eq","value":{ "a" : 1 }}`,
		// 数字边界
		`{"field":"overlay.progress","op":"eq","value":1e1000}`,
		`{"field":"overlay.progress","op":"eq","value":-0}`,
		`{"field":"overlay.progress","op":"eq","value":0.30000000000000004}`,
		// 非法 JSON
		`{`, `{"field":}`, `{"field":"tag",}`, `'x'`, `NaN`,
	}

	// 深度探针：正好 6 层必须接受，7 层必须拒绝——形状守卫的边界。
	for _, depth := range []int{1, 5, 6, 7, 8, 64, 512} {
		seeds = append(seeds, nestedNotFilter(depth))
	}
	// 节点数探针：正好 64 个必须接受，65 个必须拒绝。
	for _, width := range []int{1, 62, 63, 64, 65, 128, 4096} {
		seeds = append(seeds, wideAllFilter(width))
	}
	return seeds
}

// nestedNotFilter 生成 depth 层 not 嵌套，最内层是一个合法叶子。
func nestedNotFilter(depth int) string {
	var builder strings.Builder
	for index := 0; index < depth-1; index++ {
		builder.WriteString(`{"not":`)
	}
	builder.WriteString(`{"field":"tag","op":"eq","value":"x"}`)
	for index := 0; index < depth-1; index++ {
		builder.WriteString(`}`)
	}
	return builder.String()
}

// wideAllFilter 生成含 width 个叶子的 all 组合，总节点数为 width+1。
func wideAllFilter(width int) string {
	leaves := make([]string, width)
	for index := range leaves {
		leaves[index] = `{"field":"tag","op":"eq","value":"x"}`
	}
	return `{"all":[` + strings.Join(leaves, ",") + `]}`
}
