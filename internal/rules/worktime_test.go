package rules_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/rules"
)

func datePackage(extraPrimitives string) string {
	return fmt.Sprintf(`{
  "rule_set_id":"rset_018f47d2-5c16-7a44-a8a0-0000000000e0","version":"1.0.0",
  "schema_version":1,"normalization_algorithm_version":"gallery-canonical-json-v1","compiler_requirement":"gallery-rule-compiler-v1","cel_profile_version":"gallery-cel-v1",
  "parameter_schema":{"type":"object","additionalProperties":false},"provider_namespaces":[],
  "primitives":[
    {"id":"work","kind":"path_match","config":{"scope":"work_directory","glob":"*","title":"directory_name","stable_key":"relative_path"}},
    {"id":"media","kind":"media_classify","config":{"glob":"*.jpg","kind":"image","mime":"image/jpeg"}}%s
  ],
  "cel_expressions":[],"tests":[{"id":"date"}],"extensions":{}
}`, extraPrimitives)
}

func evaluateWork(t *testing.T, extraPrimitives, workPath string, metadata map[string]any) rules.DryRunResult {
	t.Helper()
	compiled, err := rules.CompilePackage([]byte(datePackage(extraPrimitives)))
	if err != nil {
		t.Fatalf("编译规则包失败: %v", err)
	}
	ir, _, parameters, err := rules.CompileBinding(compiled, []byte(`{}`))
	if err != nil {
		t.Fatalf("绑定编译失败: %v", err)
	}
	lifecycle, err := rules.NewLifecycle()
	if err != nil {
		t.Fatal(err)
	}
	result, err := lifecycle.EvaluateIR(context.Background(), ir, parameters, rules.DryRunInput{
		Path: workPath, Metadata: metadata,
		Files: []rules.DryRunFile{{Path: "01.jpg", Size: 1}},
	})
	if err != nil {
		t.Fatalf("规则执行失败: %v", err)
	}
	return result
}

// TestUnknownAssignTargetIsRejectedAtCompileTime 是本轮最重要的回归：旧实现只校验 target 非空，
// 而 assignTarget 对未知 target 没有 default 分支，于是规则可以声明一个永远不生效的字段——
// 规则导入成功、扫描成功、值凭空消失，既无 issue 也无 trace。仓库自己的 8 个真实来源夹具当时都
// 声明了会被静默丢弃的 target。
func TestUnknownAssignTargetIsRejectedAtCompileTime(t *testing.T) {
	for _, item := range []struct{ name, primitives string }{
		{"selector 未知 target", `,{"id":"x","kind":"selector","config":{"target":"nonexistent","pointers":["/a"]}}`},
		{"fallback 未知 target", `,{"id":"x","kind":"fallback","config":{"target":"nonexistent","pointers":["/a"]}}`},
		{"metadata_map 未知字段", `,{"id":"x","kind":"metadata_map","config":{"fields":{"nonexistent":["/a"]}}}`},
		// date 不是可赋值 target：作品发布时间必须携带 instant 与解析器版本，普通 selector
		// 产不出这些，因此只能由 work_date 原语产出。
		{"date 不能由 selector 赋值", `,{"id":"x","kind":"fallback","config":{"target":"date","pointers":["/date"]}}`},
	} {
		t.Run(item.name, func(t *testing.T) {
			_, err := rules.CompilePackage([]byte(datePackage(item.primitives)))
			if err == nil {
				t.Fatal("未知 target 未在编译期被拒绝，规则会静默丢弃该字段")
			}
			if !strings.Contains(err.Error(), "不受支持") {
				t.Fatalf("拒绝理由不明确: %v", err)
			}
		})
	}
}

// TestDescriptionAndSourceURLAreAssignable 覆盖 8 个真实来源夹具都声明、旧实现却全部丢弃的两个字段。
func TestDescriptionAndSourceURLAreAssignable(t *testing.T) {
	const primitives = `,
    {"id":"desc","kind":"fallback","config":{"target":"description","pointers":["/caption","/text"],"default":""}},
    {"id":"url","kind":"fallback","config":{"target":"source_url","pointers":["/postUrl","/url"],"default":""}}`
	result := evaluateWork(t, primitives, "work", map[string]any{
		"text": "来自 text 的描述", "url": "https://example.invalid/a",
	})
	if result.Work.Description != "来自 text 的描述" {
		t.Fatalf("description 未落地: %q", result.Work.Description)
	}
	if result.Work.SourceURL != "https://example.invalid/a" {
		t.Fatalf("source_url 未落地: %q", result.Work.SourceURL)
	}
}

// TestWorkDateResolvesMetadataChainThenPath 覆盖真实规则的日期回退链语义：metadata 优先，
// 路径只是回退推断，顺序不可颠倒。
func TestWorkDateResolvesMetadataChainThenPath(t *testing.T) {
	const primitives = `,
    {"id":"date","kind":"work_date","config":{
      "pointers":["/published","/date","/added"],
      "path_pattern":"(?P<year>\\d{4})-(?P<month>\\d{2})-(?P<day>\\d{2})",
      "input_timezone":"UTC"}}`
	for _, item := range []struct {
		name        string
		workPath    string
		metadata    map[string]any
		wantInstant string
		wantSource  string
		wantRaw     string
	}{
		{"首个 pointer 命中", "2019-01-01-work", map[string]any{"published": "2024-03-05T06:07:08Z"},
			"2024-03-05T06:07:08Z", "/published", "2024-03-05T06:07:08Z"},
		{"回退到链中后续 pointer", "2019-01-01-work", map[string]any{"added": "2022-11-30T01:02:03Z"},
			"2022-11-30T01:02:03Z", "/added", "2022-11-30T01:02:03Z"},
		{"metadata 缺失时回退路径", "2019-01-02-work", map[string]any{},
			"2019-01-02T00:00:00Z", "$path", "2019-01-02"},
		{"朴素时间戳按 input_timezone 解释", "2019-01-01-work", map[string]any{"date": "2023-06-07 08:09:10"},
			"2023-06-07T08:09:10Z", "/date", "2023-06-07 08:09:10"},
		{"Unix 秒是绝对时刻", "2019-01-01-work", map[string]any{"date": "1700000000"},
			"2023-11-14T22:13:20Z", "/date", "1700000000"},
	} {
		t.Run(item.name, func(t *testing.T) {
			result := evaluateWork(t, primitives, item.workPath, item.metadata)
			if result.Work.Date == nil {
				t.Fatalf("未解析出作品发布时间")
			}
			if result.Work.Date.Instant != item.wantInstant {
				t.Fatalf("instant = %q want %q", result.Work.Date.Instant, item.wantInstant)
			}
			if result.Work.Date.Source != item.wantSource {
				t.Fatalf("source = %q want %q", result.Work.Date.Source, item.wantSource)
			}
			if result.Work.Date.RawValue != item.wantRaw {
				t.Fatalf("rawValue = %q want %q", result.Work.Date.RawValue, item.wantRaw)
			}
			if result.Work.Date.ParserVersion != rules.WorkDateParserVersion {
				t.Fatalf("parserVersion = %q", result.Work.Date.ParserVersion)
			}
		})
	}
}

// TestWorkDateHonoursExplicitOffsetOverInputTimezone 证明带偏移量的输入不会被 input_timezone
// 二次平移——那会把一个已经明确的时刻改成另一个时刻。
func TestWorkDateHonoursExplicitOffsetOverInputTimezone(t *testing.T) {
	const primitives = `,
    {"id":"date","kind":"work_date","config":{"pointers":["/date"],"input_timezone":"Asia/Shanghai"}}`
	explicit := evaluateWork(t, primitives, "work", map[string]any{"date": "2024-03-05T06:07:08Z"})
	if explicit.Work.Date == nil || explicit.Work.Date.Instant != "2024-03-05T06:07:08Z" {
		t.Fatalf("带偏移量的输入被二次平移: %+v", explicit.Work.Date)
	}
	// 朴素输入才按 input_timezone 解释：上海 +08:00，因此 06:07:08 对应 UTC 前一日 22:07:08。
	naive := evaluateWork(t, primitives, "work", map[string]any{"date": "2024-03-05 06:07:08"})
	if naive.Work.Date == nil || naive.Work.Date.Instant != "2024-03-04T22:07:08Z" {
		t.Fatalf("朴素输入未按 input_timezone 解释: %+v", naive.Work.Date)
	}
}

// TestWorkDatePathTimezoneIsIndependentOfMetadataTimezone 锁定「目录名日期」与「metadata 朴素
// 时间戳」使用**各自**的时区。
//
// 二者曾共用一个 input_timezone。那是一处静默缺陷：来源不同的两类朴素时间戳被同一个时区假设解释，
// 一旦真实配置里两个时区不同，产出的仍是格式合法、排序正常、只是偏移了若干小时的时刻，没有任何
// issue、告警或校验会暴露它。本测试刻意让两个时区相差 13 小时，使任何共用都无法通过。
func TestWorkDatePathTimezoneIsIndependentOfMetadataTimezone(t *testing.T) {
	const primitives = `,
    {"id":"date","kind":"work_date","config":{
      "pointers":["/date"],
      "path_pattern":"(?P<year>\\d{4})-(?P<month>\\d{2})-(?P<day>\\d{2})_(?P<hour>\\d{2})",
      "input_timezone":"Asia/Shanghai",
      "path_timezone":"America/Los_Angeles"}}`

	// 路径日期按 path_timezone 解释：洛杉矶 2024-03-05 是 -08:00，因此 04:00 对应 UTC 12:00。
	// 若错误沿用 input_timezone（上海 +08:00），会得到前一日 20:00 —— 相差正好 16 小时。
	fromPath := evaluateWork(t, primitives, "2024-03-05_04-work", map[string]any{})
	if fromPath.Work.Date == nil {
		t.Fatal("路径日期未解析")
	}
	if fromPath.Work.Date.Instant != "2024-03-05T12:00:00Z" {
		t.Fatalf("路径日期 instant = %q want %q（沿用 metadata 时区会得到 2024-03-04T20:00:00Z）",
			fromPath.Work.Date.Instant, "2024-03-05T12:00:00Z")
	}

	// 同一个规则包里，metadata 朴素时间戳仍按 input_timezone 解释，不受 path_timezone 影响。
	fromMetadata := evaluateWork(t, primitives, "2024-03-05_04-work", map[string]any{"date": "2024-03-05 04:00:00"})
	if fromMetadata.Work.Date == nil || fromMetadata.Work.Date.Instant != "2024-03-04T20:00:00Z" {
		t.Fatalf("metadata 时间戳被 path_timezone 污染: %+v", fromMetadata.Work.Date)
	}
}

// TestWorkDatePathTimezoneDefaultsToInputTimezone 保证未声明 path_timezone 时行为不变——已发布的
// 规则包不因为新增这个可选字段而改变解析结果。
func TestWorkDatePathTimezoneDefaultsToInputTimezone(t *testing.T) {
	const primitives = `,
    {"id":"date","kind":"work_date","config":{
      "path_pattern":"(?P<year>\\d{4})-(?P<month>\\d{2})-(?P<day>\\d{2})_(?P<hour>\\d{2})",
      "input_timezone":"Asia/Shanghai"}}`
	result := evaluateWork(t, primitives, "2024-03-05_04-work", map[string]any{})
	if result.Work.Date == nil || result.Work.Date.Instant != "2024-03-04T20:00:00Z" {
		t.Fatalf("缺省 path_timezone 未沿用 input_timezone: %+v", result.Work.Date)
	}
}

// TestWorkDateRejectsOutOfRangePathComponents 证明越界的路径日期分量按未解析处理，而不是被
// time.Date 静默规范化成一个看似合理的错误时刻。
func TestWorkDateRejectsOutOfRangePathComponents(t *testing.T) {
	const primitives = `,
    {"id":"date","kind":"work_date","config":{
      "path_pattern":"(?P<year>\\d{4})-(?P<month>\\d{2})-(?P<day>\\d{2})"}}`
	result := evaluateWork(t, primitives, "2024-13-45-work", map[string]any{})
	if result.Work.Date != nil {
		t.Fatalf("越界日期被规范化为有效时刻: %+v", result.Work.Date)
	}
	// 缺失必须留下可解释的 issue，而不是像旧的静默丢弃那样无迹可寻。
	found := false
	for _, issue := range result.Issues {
		if issue.Code == "RULE_WORK_DATE_MISSING" {
			found = true
		}
	}
	if !found {
		t.Fatalf("未解析出时间时缺少 issue: %+v", result.Issues)
	}
}

// TestWorkDateRejectsInvalidConfiguration 锁定编译期校验：非法时区、非法正则、不受支持的捕获组、
// 缺少必需分量与重复声明都必须在规则发布时被拒绝，而不是在扫描时逐作品失败。
func TestWorkDateRejectsInvalidConfiguration(t *testing.T) {
	for _, item := range []struct{ name, primitives string }{
		{"既无 pointers 也无 pattern", `,{"id":"d","kind":"work_date","config":{"input_timezone":"UTC"}}`},
		{"pointer 不是 JSON Pointer", `,{"id":"d","kind":"work_date","config":{"pointers":["date"]}}`},
		{"非法时区", `,{"id":"d","kind":"work_date","config":{"pointers":["/date"],"input_timezone":"Mars/Olympus"}}`},
		{"非法正则", `,{"id":"d","kind":"work_date","config":{"path_pattern":"(?P<year>[0-9"}}`},
		{"不受支持的捕获组", `,{"id":"d","kind":"work_date","config":{"path_pattern":"(?P<century>\\d{2})"}}`},
		{"缺少 day 分量", `,{"id":"d","kind":"work_date","config":{"path_pattern":"(?P<year>\\d{4})-(?P<month>\\d{2})"}}`},
		{"重复声明", `,{"id":"a","kind":"work_date","config":{"pointers":["/a"]}},{"id":"b","kind":"work_date","config":{"pointers":["/b"]}}`},
		{"非法路径时区", `,{"id":"d","kind":"work_date","config":{"path_pattern":"(?P<year>\\d{4})-(?P<month>\\d{2})-(?P<day>\\d{2})","path_timezone":"Mars/Olympus"}}`},
		// path_timezone 没有 path_pattern 时无处生效，静默忽略会让配置者以为它起了作用。
		{"path_timezone 无对应 pattern", `,{"id":"d","kind":"work_date","config":{"pointers":["/date"],"path_timezone":"UTC"}}`},
	} {
		t.Run(item.name, func(t *testing.T) {
			if _, err := rules.CompilePackage([]byte(datePackage(item.primitives))); err == nil {
				t.Fatal("非法 work_date 配置未在编译期被拒绝")
			}
		})
	}
}
