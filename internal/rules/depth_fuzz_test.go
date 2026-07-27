package rules_test

// 本文件把 M8-3 安全审计里唯一「只能靠实验定论」的推断固定成可复现的回归用例：
//
//	推断：internal/rules/normalize.go 的 decodeStrictValue 为了检出重复键改用
//	      json.Decoder.Token() 而不是 Decode()，Token() 走自己的 tokenStack，
//	      因此**绕过**了 encoding/json 在 scanner 里的 maxNestingDepth=10000 守卫。
//	      若成立，规则管线上唯一约束嵌套深度的就只剩 goroutine 栈，而 Go 的栈溢出是
//	      fatal error，recover 拦不住，整个 galleryd 进程直接死亡。
//
// 这里不用 fuzz 回答它——fuzz 只能撞见崩溃，回答不了「为什么」。下面三组测试给出
// 因果链上的三个独立事实：
//
//	1. TestEncodingJSONEnforcesMaxNestingDepth
//	   基准：encoding/json 的深度守卫确实存在，且阈值就是 10000。
//	2. TestRulesPipelineBypassesEncodingJSONDepthGuard
//	   绕过：同一份被 encoding/json 拒绝的输入，被规则管线的三个入口全部接受。
//	3. TestRulesPipelineDepthIsBoundedOnlyByGoroutineStack
//	   后果：在子进程里改变 goroutine 栈上限就能改变「能吃下多深」，说明代码里
//	   根本没有深度上限；栈不够时进程以 fatal error 死亡而不是返回结构化错误。
//
// 三条合起来才是结论。任何一条单独成立都不足以定论：只有 (1) 说明标准库有守卫，
// 只有 (2) 说明守卫没生效，只有 (3) 说明边界由栈决定而不是由代码决定。

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/rules"
)

// encodingJSONMaxNestingDepth 是 encoding/json scanner 里 maxNestingDepth 的值。
// 它不是被测代码的常量，而是**参照实现**的常量，因此由测试 1 独立复核，不硬信。
const encodingJSONMaxNestingDepth = 10000

// nestedArrayText 构造 depth 层的 `[[[…]]]`。数组是最省字节的嵌套形态：一层只要
// 两个字节，因此它决定了「每 KiB 攻击载荷能换到多少层栈帧」这个最坏比值。
func nestedArrayText(depth int) []byte {
	buffer := make([]byte, 0, depth*2)
	for index := 0; index < depth; index++ {
		buffer = append(buffer, '[')
	}
	for index := 0; index < depth; index++ {
		buffer = append(buffer, ']')
	}
	return buffer
}

// nestedObjectText 构造 depth 层的 `{"a":{"a":…}}`，用于确认对象分支与数组分支
// 同样无守卫（decodeStrictValue 的两个 case 各自独立递归）。
func nestedObjectText(depth int) []byte {
	var builder strings.Builder
	builder.Grow(depth * 6)
	for index := 0; index < depth; index++ {
		builder.WriteString(`{"a":`)
	}
	builder.WriteString("1")
	for index := 0; index < depth; index++ {
		builder.WriteString("}")
	}
	return []byte(builder.String())
}

// TestEncodingJSONEnforcesMaxNestingDepth 复核参照实现的守卫确实存在且阈值为 10000。
// 若某次 Go 升级改动了这个常量，本测试会先失败，避免后面两条测试基于错误前提得出
// 错误结论。
func TestEncodingJSONEnforcesMaxNestingDepth(t *testing.T) {
	var value any
	if err := json.Unmarshal(nestedArrayText(encodingJSONMaxNestingDepth), &value); err != nil {
		t.Fatalf("encoding/json 应当接受 %d 层嵌套: %v", encodingJSONMaxNestingDepth, err)
	}
	err := json.Unmarshal(nestedArrayText(encodingJSONMaxNestingDepth+1), &value)
	if err == nil {
		t.Fatalf("encoding/json 应当拒绝 %d 层嵌套", encodingJSONMaxNestingDepth+1)
	}
	if !strings.Contains(err.Error(), "exceeded max depth") {
		t.Fatalf("拒绝理由不是深度守卫: %v", err)
	}

	// json.Compact 走同一个 scanner 而完全不碰反射层，因此它同样报深度错误这件事
	// 排除了「守卫其实位于 Unmarshal 的反射解码层」这种替代解释——守卫确实在
	// scanner 里，而 Token() 恰恰不走 scanner 的那条计数路径。
	var compacted bytes.Buffer
	compactErr := json.Compact(&compacted, nestedArrayText(encodingJSONMaxNestingDepth+1))
	if compactErr == nil || !strings.Contains(compactErr.Error(), "exceeded max depth") {
		t.Fatalf("json.Compact 未按 scanner 深度守卫拒绝: %v", compactErr)
	}
}

// TestDecoderTokenAPIHasNoDepthGuard 定位绕过的**机制**，而不只是现象。
//
// 审计的推断是：守卫位于 encoding/json 的 scanner（`Decode` 走它），而 `Token()` 走
// 自己的 tokenStack，二者不共享深度计数。本测试在同一个 *json.Decoder 上分别调用两条
// API 来判定：同一份 20000 层的输入，`Decode` 必须报 "exceeded max depth"，
// 逐 token 读到底必须一路成功。两者结论相反即证实机制成立——decodeStrictValue 之所以
// 越过守卫，正是因为它为了检出重复键而选择了 Token()。
func TestDecoderTokenAPIHasNoDepthGuard(t *testing.T) {
	const probeDepth = encodingJSONMaxNestingDepth * 2
	input := nestedArrayText(probeDepth)

	// 走 Decode：scanner 生效。
	var value any
	decodeErr := json.NewDecoder(bytes.NewReader(input)).Decode(&value)
	if decodeErr == nil || !strings.Contains(decodeErr.Error(), "exceeded max depth") {
		t.Fatalf("Decode 应当被 scanner 的深度守卫拦下，实际 %v", decodeErr)
	}

	// 走 Token：逐 token 读完整份输入。
	tokenDecoder := json.NewDecoder(bytes.NewReader(input))
	tokens, depth, maxDepth := 0, 0, 0
	for {
		token, err := tokenDecoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Token() 在第 %d 个 token（深度 %d）失败: %v"+
				"——若这是深度守卫，说明推断不成立，请改写本测试", tokens, depth, err)
		}
		tokens++
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '[', '{':
				depth++
				if depth > maxDepth {
					maxDepth = depth
				}
			case ']', '}':
				depth--
			}
		}
	}
	if maxDepth != probeDepth {
		t.Fatalf("Token() 未读到预期深度：实际 %d 期望 %d", maxDepth, probeDepth)
	}
	t.Logf("机制确认：同一份 %d 层输入，Decode 被 scanner 拦下（%v），"+
		"而 Token() 一路读完 %d 个 token、最大深度 %d，**完全没有深度守卫**",
		probeDepth, decodeErr, tokens, maxDepth)
}

// TestRulesPipelineBypassesEncodingJSONDepthGuard 是绕过的直接证据。
//
// 断言的是**当前行为**：三个入口都接受了参照实现明确拒绝的深度。一旦
// decodeStrictValue 补上显式深度计数，本测试会立即失败，提示把它改写成
// 「深度超过 N 必须返回结构化错误」的正向断言。
func TestRulesPipelineBypassesEncodingJSONDepthGuard(t *testing.T) {
	// 取 10 倍于参照阈值的深度，排除「阈值只是略有不同」这种解释。
	const probeDepth = encodingJSONMaxNestingDepth * 10

	arrayProbe := nestedArrayText(probeDepth)
	objectProbe := nestedObjectText(probeDepth)

	var discard any
	if err := json.Unmarshal(arrayProbe, &discard); err == nil {
		t.Fatalf("前提不成立：参照实现接受了 %d 层嵌套", probeDepth)
	}

	cases := []struct {
		name  string
		probe []byte
		run   func([]byte) error
	}{
		{"CanonicalJSON/数组", arrayProbe, func(input []byte) error {
			_, err := rules.CanonicalJSON(input)
			return err
		}},
		{"CanonicalJSON/对象", objectProbe, func(input []byte) error {
			_, err := rules.CanonicalJSON(input)
			return err
		}},
		{"NormalizeWithSchema/数组", arrayProbe, func(input []byte) error {
			_, err := rules.NormalizeWithSchema(input, []byte(`{}`))
			return err
		}},
		{"ImportRulePackage/数组", arrayProbe, func(input []byte) error {
			_, err := rules.ImportRulePackage("json", input)
			return err
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.run(testCase.probe)
			if err == nil {
				t.Logf("已确认绕过：%s 接受了 %d 层嵌套（参照实现在 %d 层就拒绝）",
					testCase.name, probeDepth, encodingJSONMaxNestingDepth)
				return
			}
			if strings.Contains(err.Error(), "exceeded max depth") {
				t.Fatalf("守卫已生效，请把本测试改写成正向的深度上限断言: %v", err)
			}
			// ImportRulePackage 可能因为 Schema 校验而拒绝，但那必须发生在解析
			// 之后；只要拒绝理由不是深度，绕过这个事实依然成立。
			t.Logf("已确认绕过：%s 越过了深度守卫，随后因其他原因拒绝: %v", testCase.name, err)
		})
	}
}

// TestRuleImportFormatsDisagreeOnDepthGuard 定位「绕过」在真实入口上的可达面。
//
// `POST /api/v1/rules/import` 的三种格式各自走不同的解析器，而**深度策略并不统一**：
//
//	json —— transport 的 decodeJSON 用 encoding/json scanner 解 content，10000 层拦截；
//	        但 ImportRulePackage 内部的 decodeAny 自己没有守卫（见上面几条测试）。
//	yaml —— go-yaml 有自己的 `exceeded max depth of 10000`，独立生效。
//	toml —— go-toml/v2 的内联数组**没有任何深度守卫**。
//
// 这条差异是关键：transport 的 decodeRuleContent 对 format != "json" 的请求把 content
// 当作 JSON **字符串**取出，字符串内部的括号对 encoding/json scanner 不可见，因此
// scanner 的 10000 层守卫对 yaml/toml 两条路径完全不生效。yaml 因为解析器自带守卫而
// 幸免，toml 则是三条路径里唯一既绕过 scanner、解析器自身又不设限的组合。
//
// 断言的是**当前行为**。任一格式补上统一的深度策略后，本测试会失败并提示改写。
func TestRuleImportFormatsDisagreeOnDepthGuard(t *testing.T) {
	// 取 2 倍于 10000 的深度：足以越过 yaml 与 encoding/json 的守卫，又不至于让
	// 无守卫的那条路径耗尽内存。
	const probeDepth = 20000

	yamlInput := []byte("a: " + strings.Repeat("[", probeDepth) + strings.Repeat("]", probeDepth) + "\n")
	_, yamlErr := rules.ImportRulePackage("yaml", yamlInput)
	if yamlErr == nil {
		t.Fatalf("go-yaml 应当有自己的深度守卫，%d 层却被接受", probeDepth)
	}
	if !strings.Contains(yamlErr.Error(), "max depth") {
		t.Fatalf("YAML 拒绝理由不是深度守卫: %v", yamlErr)
	}
	t.Logf("yaml：解析器自带守卫，%d 层被拒（%v）", probeDepth, yamlErr)

	tomlInput := []byte("a = " + strings.Repeat("[", probeDepth) + strings.Repeat("]", probeDepth) + "\n")
	_, tomlErr := rules.ImportRulePackage("toml", tomlInput)
	if tomlErr != nil && strings.Contains(tomlErr.Error(), "max depth") {
		t.Fatalf("go-toml 已有深度守卫，请把本测试改写成正向断言: %v", tomlErr)
	}
	t.Logf("toml：**没有任何深度守卫**，%d 层内联数组被完整接受（err=%v）；"+
		"这条路径同时绕过 transport scanner（content 以 JSON 字符串承载）"+
		"与解析器守卫，是三种格式里唯一两层都不设限的组合", probeDepth, tomlErr)
}

const (
	deepProbeDepthEnv    = "GALLERY_RULES_DEEP_PROBE_DEPTH"
	deepProbeMaxStackEnv = "GALLERY_RULES_DEEP_PROBE_MAXSTACK"
	deepProbeShapeEnv    = "GALLERY_RULES_DEEP_PROBE_SHAPE"
	deepProbeSurvived    = "GALLERY-DEEP-PROBE-SURVIVED"

	// deepProbeShapeBalanced 用配对的 `[…]`，每层要 2 字节。
	deepProbeShapeBalanced = "balanced"
	// deepProbeShapeOpen 只用 `[`，每层 1 字节。decodeStrictValue 在读到匹配的
	// `]` 之前就已经递归下去了（decoder.More() 只需要窥见下一个 token 不是 `]`），
	// 因此**不闭合的括号照样制造完整深度的栈帧**，这让同样大小的载荷换到双倍层数。
	deepProbeShapeOpen = "open"

	deepProbeEntryEnv      = "GALLERY_RULES_DEEP_PROBE_ENTRY"
	deepProbeEntryCanonial = "canonical"
	deepProbeEntryYAML     = "yaml-import"
)

// TestRulesPipelineDepthIsBoundedOnlyByGoroutineStack 在子进程里回答「后果」。
//
// 同一个深度、同一份输入，只改 goroutine 栈上限：栈够大就正常返回，栈不够就
// `fatal error: stack overflow`。这正是「代码里没有任何深度上限」的判定性证据——
// 若存在代码级上限，改栈大小不可能改变结果。
//
// 子进程用 debug.SetMaxStack 人为缩小上限，只是为了让实验在毫秒级、几 MiB 内存内
// 完成。生产进程的默认上限是 1 GiB，结论不变，只是需要更深的输入触发。
func TestRulesPipelineDepthIsBoundedOnlyByGoroutineStack(t *testing.T) {
	if depthText := os.Getenv(deepProbeDepthEnv); depthText != "" {
		runDeepProbeChild(t, depthText, os.Getenv(deepProbeMaxStackEnv))
		return
	}
	if testing.Short() {
		t.Skip("子进程栈探针在 -short 下跳过")
	}

	const probeDepth = 400000

	roomy := runDeepProbe(t, probeDepth, 512<<20)
	if !strings.Contains(roomy.output, deepProbeSurvived) {
		t.Fatalf("栈上限 512 MiB 时 %d 层嵌套未能正常返回:\n%s", probeDepth, roomy.tail())
	}
	if roomy.err != nil {
		t.Fatalf("栈上限 512 MiB 时子进程异常退出 %v:\n%s", roomy.err, roomy.tail())
	}
	t.Logf("栈上限 512 MiB：%d 层嵌套被完整接受并正常返回（代码没有拒绝它）", probeDepth)

	cramped := runDeepProbe(t, probeDepth, 4<<20)
	if cramped.err == nil {
		t.Fatalf("栈上限 4 MiB 时 %d 层嵌套竟然正常返回，请复核本结论:\n%s", probeDepth, cramped.tail())
	}
	if !strings.Contains(cramped.output, "stack overflow") {
		t.Fatalf("栈上限 4 MiB 时子进程不是因栈溢出死亡（%v）:\n%s", cramped.err, cramped.tail())
	}
	if strings.Contains(cramped.output, "panic:") && !strings.Contains(cramped.output, "fatal error:") {
		t.Fatalf("栈溢出表现为可 recover 的 panic，与结论不符:\n%s", cramped.tail())
	}
	t.Logf("栈上限 4 MiB：同一份输入触发 `fatal error: stack overflow`，子进程以 %v 死亡；"+
		"Go 的栈溢出不是 panic，recover/中间件都拦不住", cramped.err)
}

// TestRulesPipelineDepthReachableWithinRulePackageLimit 回答「这在生产上够得着吗」。
//
// 上一条测试人为缩小了栈上限。本条不缩：用 Go 的默认 1 GiB 上限，载荷大小恰好等于
// ImportRulePackage 自己接受的最大规则包 MaxRulePackageBytes，形状取 `[` × N
// （每层 1 字节，不闭合也照样递归）。它回答的是：**攻击者在现有大小限制之内，能不能
// 把默认配置的 galleryd 打到 fatal error。**
//
// 这条测试要 1 GiB 栈和数秒时间，因此在 -short 下跳过；它不是常规回归的必需项，
// 而是结论的量化依据，必须能被复现。
func TestRulesPipelineDepthReachableWithinRulePackageLimit(t *testing.T) {
	if os.Getenv(deepProbeDepthEnv) != "" {
		t.Skip("子进程分支由另一条测试承担")
	}
	if testing.Short() {
		t.Skip("1 GiB 栈探针在 -short 下跳过")
	}
	const defaultMaxStack = 1 << 30 // runtime 默认 goroutine 栈上限（64 位）
	result := runDeepProbeShaped(t, rules.MaxRulePackageBytes, defaultMaxStack, deepProbeShapeOpen)
	if result.err == nil {
		t.Fatalf("默认 1 GiB 栈下 %d 字节载荷未致命，请据此下调结论的严重级别:\n%s",
			rules.MaxRulePackageBytes, result.tail())
	}
	if !strings.Contains(result.output, "stack overflow") {
		t.Fatalf("子进程异常退出但不是栈溢出（%v）:\n%s", result.err, result.tail())
	}
	t.Logf("默认 1 GiB 栈：%d 字节（= MaxRulePackageBytes）的 `[` 载荷即触发 "+
		"`fatal error: stack overflow`，子进程以 %v 死亡。该深度在 ImportRulePackage "+
		"自身的大小限制之内，无需任何超限请求", rules.MaxRulePackageBytes, result.err)
}

type deepProbeResult struct {
	output string
	err    error
}

// tail 只保留输出尾部，避免把几百 KiB 的 runtime traceback 灌进测试日志。
func (r deepProbeResult) tail() string {
	const limit = 2048
	if len(r.output) <= limit {
		return r.output
	}
	return "…" + r.output[len(r.output)-limit:]
}

func runDeepProbe(t *testing.T, depth int, maxStack int) deepProbeResult {
	t.Helper()
	return runDeepProbeShaped(t, depth, maxStack, deepProbeShapeBalanced)
}

func runDeepProbeShaped(t *testing.T, depth int, maxStack int, shape string) deepProbeResult {
	t.Helper()
	return runDeepProbeAt(t, deepProbeEntryCanonial, depth, maxStack, shape)
}

func runDeepProbeAt(t *testing.T, entry string, depth int, maxStack int, shape string) deepProbeResult {
	t.Helper()
	command := exec.Command(os.Args[0],
		"-test.run=^TestRulesPipelineDepthIsBoundedOnlyByGoroutineStack$",
		"-test.count=1", "-test.timeout=120s", "-test.v")
	command.Env = append(os.Environ(),
		deepProbeDepthEnv+"="+strconv.Itoa(depth),
		deepProbeMaxStackEnv+"="+strconv.Itoa(maxStack),
		deepProbeShapeEnv+"="+shape,
		deepProbeEntryEnv+"="+entry,
		// traceback 对结论没有价值，但会产生大量输出。
		"GOTRACEBACK=none")
	output, err := command.CombinedOutput()
	return deepProbeResult{output: string(output), err: err}
}

// runDeepProbeChild 是子进程分支：设定栈上限后直接调用被测入口。
// 正常返回时打印哨兵字符串；栈不够时进程在这里 fatal，永远走不到打印。
func runDeepProbeChild(t *testing.T, depthText, maxStackText string) {
	depth, err := strconv.Atoi(depthText)
	if err != nil {
		t.Fatalf("子进程深度参数无效 %q: %v", depthText, err)
	}
	maxStack, err := strconv.Atoi(maxStackText)
	if err != nil {
		t.Fatalf("子进程栈上限参数无效 %q: %v", maxStackText, err)
	}
	debug.SetMaxStack(maxStack)
	shape := os.Getenv(deepProbeShapeEnv)
	var input []byte
	if shape == deepProbeShapeOpen {
		input = bytes.Repeat([]byte("["), depth)
	} else {
		input = nestedArrayText(depth)
	}
	entry := os.Getenv(deepProbeEntryEnv)
	var outputBytes int
	var probeErr error
	switch entry {
	case deepProbeEntryYAML:
		// YAML 的流式序列同样用 `[`，因此同一份载荷可以直接喂给导入路径。
		result, err := rules.ImportRulePackage("yaml", input)
		outputBytes, probeErr = len(result.CanonicalJSON), err
	default:
		entry = deepProbeEntryCanonial
		output, err := rules.CanonicalJSON(input)
		outputBytes, probeErr = len(output), err
	}
	fmt.Printf("%s entry=%s depth=%d maxStack=%d shape=%s outputBytes=%d err=%v\n",
		deepProbeSurvived, entry, depth, maxStack, shape, outputBytes, probeErr)
}
