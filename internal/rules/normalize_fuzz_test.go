package rules_test

// 本文件建立规则规范 JSON 管线的 fuzz 目标。不可信输入有三条真实来源：
//
//  1. 规则相关的 8 个 `/api/v1/rules/*` 端点请求体；
//  2. `POST /api/v1/rules/import` 的 JSON/YAML/TOML 内容；
//  3. `CompileBinding` 用「攻击者可影响的 parameter schema」归一化绑定参数。
//
// 因此断言必须是明确的不变量，而不是「跑不崩就算过」：
//
//   - 幂等：CanonicalJSON(CanonicalJSON(x)) == CanonicalJSON(x)；
//   - 语义不变：规范化前后解码出的 JSON 值在数值意义上完全相等；
//   - 规范形态：对象键升序且唯一，且不含任何无意义空白；
//   - 输入只读：不得就地改写调用方传入的 []byte；
//   - 永不 panic、永不挂起。

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/rules"
)

// referenceDepthLimit 是 encoding/json 自身的 maxNestingDepth。超过该深度时
// json.Unmarshal / json.Compact 会直接报 "exceeded max depth"，无法充当参照实现，
// 因此语义比对与紧凑形态比对只在该深度以内进行。
const referenceDepthLimit = 10000

// harnessDepthLimit 是**夹具自身的**栈安全上限，不是被测代码的安全上限。
//
// 已确认缺陷：internal/rules/normalize.go 的 decodeStrictValue 用 json.Decoder.Token()
// 而不是 Decode()，Token() 走自己的 tokenStack，**完全绕过** encoding/json 在 scanner 里
// 的 maxNestingDepth=10000 守卫；normalizeValue、cloneJSONValue 与 package.go 的
// writeCanonical 同样没有任何深度计数。实测在本工具链上约 1.3×10^6 层嵌套即触发
// `fatal error: stack overflow`（Go 的栈溢出是 fatal error，recover 无法拦截，整个
// galleryd 进程直接死亡）。
//
// 夹具在此处提前放弃，只是为了不让 fuzz worker 被不可恢复的 fatal error 杀死而丢失
// 语料；它**不代表被测代码在该深度以内是安全的**。一旦 decodeStrictValue 与
// writeCanonical 补上显式深度计数，应当删除这道 skip，并把它换成
// 「深度超过 N 必须返回结构化错误」的正向断言。
const harnessDepthLimit = 200000

func FuzzCanonicalJSON(f *testing.F) {
	addCanonicalSeeds(f)
	f.Fuzz(func(t *testing.T, input []byte) {
		if jsonNestingDepth(input) > harnessDepthLimit {
			t.Skip("超出夹具栈安全上限，见 harnessDepthLimit 注释")
		}
		original := append([]byte(nil), input...)
		output, err := rules.CanonicalJSON(input)
		if !bytes.Equal(original, input) {
			t.Fatalf("CanonicalJSON 就地改写了调用方输入")
		}
		if err != nil {
			if output != nil {
				t.Fatalf("失败路径必须返回 nil 输出，实际 %d bytes", len(output))
			}
			return
		}
		if len(output) == 0 {
			t.Fatalf("成功路径返回了空输出")
		}
		assertCanonicalForm(t, output)

		// 幂等：规范形态再规范化必须逐字节不变。
		again, againErr := rules.CanonicalJSON(output)
		if againErr != nil {
			t.Fatalf("规范输出无法再次规范化: %v", againErr)
		}
		if !bytes.Equal(output, again) {
			t.Fatalf("CanonicalJSON 不幂等\n第一次: %s\n第二次: %s", truncateForLog(output), truncateForLog(again))
		}

		// 语义不变：只有在参照实现能处理的深度内才可比对。
		if jsonNestingDepth(input) <= referenceDepthLimit {
			left, leftErr := decodeReference(input)
			right, rightErr := decodeReference(output)
			if leftErr != nil {
				t.Fatalf("CanonicalJSON 接受了参照解码器拒绝的输入: %v", leftErr)
			}
			if rightErr != nil {
				t.Fatalf("规范输出无法被参照解码器解析: %v", rightErr)
			}
			if !sameJSONValue(left, right) {
				t.Fatalf("规范化改变了 JSON 语义\n输入: %s\n输出: %s", truncateForLog(input), truncateForLog(output))
			}
			var compact bytes.Buffer
			if compactErr := json.Compact(&compact, output); compactErr != nil {
				t.Fatalf("规范输出无法紧凑化: %v", compactErr)
			}
			if !bytes.Equal(compact.Bytes(), output) {
				t.Fatalf("规范输出含无意义空白: %s", truncateForLog(output))
			}
		}
	})
}

func FuzzNormalizeWithSchema(f *testing.F) {
	addNormalizeSeeds(f)
	f.Fuzz(func(t *testing.T, input, schemaJSON []byte) {
		if jsonNestingDepth(input) > harnessDepthLimit || jsonNestingDepth(schemaJSON) > harnessDepthLimit {
			t.Skip("超出夹具栈安全上限，见 harnessDepthLimit 注释")
		}
		originalInput := append([]byte(nil), input...)
		originalSchema := append([]byte(nil), schemaJSON...)
		output, err := rules.NormalizeWithSchema(input, schemaJSON)
		if !bytes.Equal(originalInput, input) || !bytes.Equal(originalSchema, schemaJSON) {
			t.Fatalf("NormalizeWithSchema 就地改写了调用方输入或 Schema")
		}
		if err != nil {
			if output != nil {
				t.Fatalf("失败路径必须返回 nil 输出，实际 %d bytes", len(output))
			}
			return
		}
		if len(output) == 0 {
			t.Fatalf("成功路径返回了空输出")
		}
		assertCanonicalForm(t, output)

		// 输出必须已经是规范 JSON：CompileBinding 直接把它交给 CanonicalJSON 求
		// rule_ir_hash，若这里不是不动点，规则身份会随一次多余的往返而漂移。
		canonical, canonicalErr := rules.CanonicalJSON(output)
		if canonicalErr != nil {
			t.Fatalf("归一化输出不是合法规范 JSON: %v", canonicalErr)
		}
		if !bytes.Equal(canonical, output) {
			t.Fatalf("归一化输出不是 CanonicalJSON 的不动点\n归一化: %s\n规范化: %s",
				truncateForLog(output), truncateForLog(canonical))
		}

		// 幂等：同一 Schema 再归一化一次必须逐字节不变（默认值已物化、NFC 已收敛）。
		again, againErr := rules.NormalizeWithSchema(output, schemaJSON)
		if againErr != nil {
			t.Fatalf("归一化输出无法再次归一化: %v", againErr)
		}
		if !bytes.Equal(output, again) {
			t.Fatalf("NormalizeWithSchema 不幂等\n第一次: %s\n第二次: %s",
				truncateForLog(output), truncateForLog(again))
		}
	})
}

// FuzzImportRulePackage 覆盖 `POST /api/v1/rules/import` 的三种格式。
//
// 这条路径值得单独 fuzz 的原因是：YAML/TOML 内容以 **JSON 字符串**形式承载，
// 因此 transport 层 decodeJSON 的 encoding/json scanner 深度守卫对它不生效；
// 解析结果又经 json.Marshal（无深度守卫）回到 decodeAny。它是 decodeStrictValue
// 缺失深度计数在真实入口上唯一未被外层守卫遮蔽的到达路径。
func FuzzImportRulePackage(f *testing.F) {
	addImportSeeds(f)
	f.Fuzz(func(t *testing.T, format string, content []byte) {
		if len(content) > rules.MaxRulePackageBytes {
			t.Skip("超出规则包大小上限")
		}
		if jsonNestingDepth(content) > harnessDepthLimit || bracketNestingDepth(content) > harnessDepthLimit {
			t.Skip("超出夹具栈安全上限，见 harnessDepthLimit 注释")
		}
		result, err := rules.ImportRulePackage(format, content)
		if err != nil {
			if result.CanonicalJSON != nil {
				t.Fatalf("失败路径必须返回空 CanonicalJSON")
			}
			return
		}
		if result.Format != "json" && result.Format != "yaml" && result.Format != "toml" {
			t.Fatalf("导入格式必须收敛到三种规范值，实际 %q", result.Format)
		}
		if len(result.CanonicalJSON) == 0 {
			t.Fatalf("成功导入返回了空 CanonicalJSON")
		}
		assertCanonicalForm(t, result.CanonicalJSON)
		canonical, canonicalErr := rules.CanonicalJSON(result.CanonicalJSON)
		if canonicalErr != nil {
			t.Fatalf("导入结果不是合法规范 JSON: %v", canonicalErr)
		}
		if !bytes.Equal(canonical, result.CanonicalJSON) {
			t.Fatalf("导入结果不是 CanonicalJSON 的不动点")
		}
		// 同一份规范 JSON 以 json 格式再导入一次必须得到同一结果，否则规则身份
		// （package_hash / semantic_hash）会依赖导入次数。
		second, secondErr := rules.ImportRulePackage("json", result.CanonicalJSON)
		if secondErr != nil {
			t.Fatalf("规范 JSON 无法再次导入: %v", secondErr)
		}
		if !bytes.Equal(second.CanonicalJSON, result.CanonicalJSON) {
			t.Fatalf("导入不幂等")
		}
	})
}

// ---------- 种子语料 ----------

func addCanonicalSeeds(f *testing.F) {
	f.Helper()
	for _, seed := range canonicalSeedInputs() {
		f.Add([]byte(seed))
	}
	for _, fixture := range readRuleFixtures(f) {
		f.Add(fixture)
	}
	f.Add(rules.RulePackageSchema())
}

func addNormalizeSeeds(f *testing.F) {
	f.Helper()
	schema := rules.RulePackageSchema()
	for _, seed := range canonicalSeedInputs() {
		f.Add([]byte(seed), schema)
	}
	for _, fixture := range readRuleFixtures(f) {
		f.Add(fixture, schema)
	}
	// 攻击者可影响的 Schema：声明未知/合法规范化策略、递归 default、非对象 properties。
	f.Add([]byte(`{"a":"ﬁ"}`), []byte(`{"properties":{"a":{"x-gallery-normalization":"nfc"}}}`))
	f.Add([]byte(`{"a":"Å"}`), []byte(`{"properties":{"a":{"x-gallery-normalization":"identifier"}}}`))
	f.Add([]byte(`{"a":"x"}`), []byte(`{"properties":{"a":{"x-gallery-normalization":"unknown-policy"}}}`))
	f.Add([]byte(`{}`), []byte(`{"properties":{"a":{"default":{"b":[1,2,{"c":"Å"}]}}}}`))
	f.Add([]byte(`{}`), []byte(`{"properties":{"a":{"default":1e10000}}}`))
	f.Add([]byte(`{}`), []byte(`{"properties":{"a":{"default":1e100000}}}`))
	f.Add([]byte(`[1,2,3]`), []byte(`{"items":{"x-gallery-normalization":"nfc"}}`))
	f.Add([]byte(`{"a":1}`), []byte(`{"properties":"not-an-object"}`))
	f.Add([]byte(`{"a":1}`), []byte(`{"properties":{"a":"not-an-object"}}`))
	f.Add([]byte(`{"a":1}`), []byte(`[]`))
	f.Add([]byte(`{"a":1}`), []byte(``))
	f.Add([]byte(``), []byte(`{}`))
}

func addImportSeeds(f *testing.F) {
	f.Helper()
	for _, fixture := range readRuleFixtures(f) {
		f.Add("json", fixture)
	}
	f.Add("json", []byte(`{"a":1}`))
	f.Add("JSON", []byte(`{"a":1}`))
	f.Add("", []byte(`{"a":1}`))
	f.Add("yaml", []byte("a: 1\nb:\n  - x\n  - y\n"))
	f.Add("yml", []byte("a: 1\n"))
	f.Add("yaml", []byte("a: 1\n---\nb: 2\n"))
	f.Add("yaml", []byte("&a [*a]"))
	f.Add("yaml", []byte("a: !!binary QUJD\n"))
	f.Add("toml", []byte("a = 1\n[b]\nc = \"x\"\n"))
	f.Add("toml", []byte("a = 1.5\n"))
	f.Add("toml", []byte("a = 1979-05-27T07:32:00Z\n"))
	f.Add("toml", []byte("a = "+strings.Repeat("[", 64)+strings.Repeat("]", 64)+"\n"))
	f.Add("cue", []byte(`{"a":1}`))
	f.Add("json", []byte(``))
}

// canonicalSeedInputs 是审计要求的定向探针语料：嵌套、重复键、转义后碰撞、
// 数字边界、Unicode 规范化形式、孤立代理与尾随垃圾。
//
// 嵌套深度刻意只到 10000：更深的输入会让 encoding/json 参照实现失效，且
// decodeStrictValue 在缺少深度计数的当前实现下最终会 fatal 栈溢出（见
// harnessDepthLimit 注释），把它写进种子语料等于让 `go test ./...` 直接崩掉。
func canonicalSeedInputs() []string {
	seeds := []string{
		// 结构基线
		`{}`, `[]`, `null`, `true`, `false`, `0`, `""`, `" "`,
		`{"a":1}`, `[1,2,3]`, `{"b":1,"a":2}`, `{"":1}`,
		// 重复键与转义后碰撞
		`{"a":1,"a":2}`,
		`{"a":1,"a":2}`,
		`{"a":{"b":1,"b":2}}`,
		`[{"a":1,"a":2}]`,
		// 数字边界
		`1e1000`, `-1e1000`, `1e10000`, `1e10001`, `1e-10000`, `1e-10001`,
		`-0`, `-0.0`, `0.0`, `0e0`, `1.0`, `1.000`, `10e-1`, `0.1`, `1E+2`,
		`123456789012345678901234567890`,
		`-123456789012345678901234567890.000000000000000000001`,
		`[1e1000,-0,0.0,1.0,100e-2]`,
		// Unicode：NFC / NFD 同字符串对、孤立代理、非法 UTF-8 转义
		"\"Å\"", "\"Å\"",
		`{"Å":1,"Å":2}`,
		`"\ud800"`, `"\udfff"`, `"𐀀"`, `"�"`,
		"\"\x00\"", "\"\x1f\"", "\"‪‬\"", `"\\"`, `"\/"`, `"</script>"`,
		// 尾随垃圾与多值
		`{}{}`, `{}]`, `[]}`, `{} `, `{}` + "\n", `1 2`, `[1,2]x`, `{"a":1},`,
		// 非法与截断
		`{`, `[`, `{"a"}`, `{"a":}`, `[,]`, `[1,]`, `{"a":1,}`, `'x'`, `NaN`, `Infinity`, `+1`, `01`, `.1`, `1.`,
	}
	for _, depth := range []int{1, 2, 100, 1000, referenceDepthLimit} {
		seeds = append(seeds,
			strings.Repeat("[", depth)+strings.Repeat("]", depth),
			strings.Repeat(`{"a":`, depth)+`1`+strings.Repeat("}", depth),
		)
	}
	return seeds
}

func readRuleFixtures(f *testing.F) [][]byte {
	f.Helper()
	patterns := []string{
		filepath.Join("testdata", "*.json"),
		filepath.Join("testdata", "examples", "*.json"),
	}
	var fixtures [][]byte
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			f.Fatalf("枚举夹具 %s: %v", pattern, err)
		}
		for _, match := range matches {
			data, readErr := os.ReadFile(match)
			if readErr != nil {
				f.Fatalf("读取夹具 %s: %v", match, readErr)
			}
			fixtures = append(fixtures, data)
		}
	}
	if len(fixtures) == 0 {
		f.Fatalf("未找到任何规则夹具，种子语料不完整")
	}
	return fixtures
}

// ---------- 不变量断言 ----------

// assertCanonicalForm 迭代（非递归，因此不受输入深度影响）校验规范形态：
// 每个对象的键必须是字符串、严格升序且互不相同。
func assertCanonicalForm(t *testing.T, canonical []byte) {
	t.Helper()
	type frame struct {
		object    bool
		expectKey bool
		lastKey   string
		seenKey   bool
	}
	var stack []*frame
	consumeValue := func() {
		if len(stack) > 0 && stack[len(stack)-1].object {
			stack[len(stack)-1].expectKey = true
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.UseNumber()
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("规范输出无法逐 token 解析: %v", err)
		}
		delimiter, isDelimiter := token.(json.Delim)
		if top := len(stack) - 1; top >= 0 && stack[top].object && stack[top].expectKey &&
			!(isDelimiter && delimiter == '}') {
			key, ok := token.(string)
			if !ok {
				t.Fatalf("对象键不是字符串: %T", token)
			}
			if stack[top].seenKey && key <= stack[top].lastKey {
				t.Fatalf("对象键未严格升序或存在重复: %q 出现在 %q 之后", key, stack[top].lastKey)
			}
			stack[top].lastKey, stack[top].seenKey, stack[top].expectKey = key, true, false
			continue
		}
		if !isDelimiter {
			consumeValue()
			continue
		}
		switch delimiter {
		case '{':
			consumeValue()
			stack = append(stack, &frame{object: true, expectKey: true})
		case '[':
			consumeValue()
			stack = append(stack, &frame{})
		case '}', ']':
			if len(stack) == 0 {
				t.Fatalf("规范输出出现多余结束符 %v", delimiter)
			}
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) != 0 {
		t.Fatalf("规范输出容器未闭合，剩余 %d 层", len(stack))
	}
}

// decodeReference 用 encoding/json 作为独立参照实现解码，数字保留为 json.Number
// 以免 float64 中转丢失规则身份所依赖的十进制精度。
func decodeReference(input []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("参照解码器发现尾随内容")
	}
	return value, nil
}

// sameJSONValue 逐值比较两棵 JSON 树。数字按精确十进制值比较而不是按字面量比较，
// 因为规范化的既定语义就是把 1E+2 与 100 收敛为同一身份。
func sameJSONValue(left, right any) bool {
	switch typed := left.(type) {
	case nil:
		return right == nil
	case bool:
		other, ok := right.(bool)
		return ok && typed == other
	case string:
		other, ok := right.(string)
		return ok && typed == other
	case json.Number:
		other, ok := right.(json.Number)
		if !ok {
			return false
		}
		leftRat, leftOK := new(big.Rat).SetString(typed.String())
		rightRat, rightOK := new(big.Rat).SetString(other.String())
		if !leftOK || !rightOK {
			return typed.String() == other.String()
		}
		return leftRat.Cmp(rightRat) == 0
	case []any:
		other, ok := right.([]any)
		if !ok || len(typed) != len(other) {
			return false
		}
		for index := range typed {
			if !sameJSONValue(typed[index], other[index]) {
				return false
			}
		}
		return true
	case map[string]any:
		other, ok := right.(map[string]any)
		if !ok || len(typed) != len(other) {
			return false
		}
		for key, value := range typed {
			counterpart, exists := other[key]
			if !exists || !sameJSONValue(value, counterpart) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// jsonNestingDepth 以迭代方式估算 JSON 文本的最大括号嵌套层数，字符串内的括号
// 不计入。它只用于夹具自我保护，不参与任何被测语义。
func jsonNestingDepth(input []byte) int {
	depth, maximum := 0, 0
	inString, escaped := false, false
	for _, char := range input {
		if inString {
			switch {
			case escaped:
				escaped = false
			case char == '\\':
				escaped = true
			case char == '"':
				inString = false
			}
			continue
		}
		switch char {
		case '"':
			inString = true
		case '{', '[':
			depth++
			if depth > maximum {
				maximum = depth
			}
		case '}', ']':
			depth--
		}
	}
	return maximum
}

// bracketNestingDepth 忽略字符串边界，直接统计括号嵌套。YAML/TOML 的流式序列
// 与内联数组同样用方括号表达，但它们不受 JSON 字符串规则约束。
func bracketNestingDepth(input []byte) int {
	depth, maximum := 0, 0
	for _, char := range input {
		switch char {
		case '{', '[':
			depth++
			if depth > maximum {
				maximum = depth
			}
		case '}', ']':
			depth--
		}
	}
	return maximum
}

func truncateForLog(value []byte) string {
	const limit = 256
	if len(value) <= limit {
		return string(value)
	}
	return string(value[:limit]) + "…(共 " + itoa(len(value)) + " bytes)"
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
