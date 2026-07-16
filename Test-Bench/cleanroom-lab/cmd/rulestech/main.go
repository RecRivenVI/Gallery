// P14：规则技术对比(净室)。同一组"平台规则"目标——从一份 metadata + 路径上下文
// 提取 title/author/tags 并判定媒体可见性——分别用三种表达技术实现,比较表达力、
// 错误信息、可静态分析性、Explain 友好度、安全性与求值耗时。
//
//	方案 1：声明式 JSON + 自建有限原语(readJson/firstNonEmpty/glob…)
//	方案 2：CEL(google/cel-go,受限表达式语言,可编译期类型检查)
//	方案 3：Starlark(go.starlark.net,受限 Python 方言,图灵完备但可沙箱)
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"go.starlark.net/starlark"
)

// 统一输入:一条作品的 metadata + 文件名。
var meta = map[string]any{
	"post": map[string]any{
		"title":  "星空下的约定",
		"userId": "12345",
		"tags":   []any{"原创", "R-18", "风景"},
	},
	"filename": "cover.jpg",
}

func main() {
	fmt.Println("目标:提取 title、判定 R-18 badge、判定 cover 文件可见性。三种技术各实现一次。")
	fmt.Println()

	// ---- 方案 1：声明式 JSON + 自建原语 ----
	t := time.Now()
	title := readPath(meta, "post.title")
	isR18 := containsTag(meta, "post.tags", "R-18")
	visible := !glob(meta["filename"].(string), ".*") // 隐藏点文件
	d1 := time.Since(t)
	fmt.Printf("方案1 自建原语:   title=%q r18=%v visible=%v  (%s)\n", title, isR18, visible, d1.Round(time.Microsecond))
	fmt.Println("   表达力:受限(只能组合注册原语);错误:字段级、可精确定位;静态分析:完全;Explain:天然(每原语一步);安全:最高(无任意执行);UI 生成:直接从 Schema")

	// ---- 方案 2：CEL ----
	env, err := cel.NewEnv(
		cel.Variable("post", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("filename", cel.StringType),
	)
	must(err)
	celTitle := celEval(env, `post.title`)
	celR18 := celEval(env, `"R-18" in post.tags`)
	celVis := celEval(env, `!filename.startsWith(".")`)
	// 编译期错误示例
	_, iss := env.Compile(`post.nonexistent_method(`)
	t = time.Now()
	_ = celEval(env, `post.title`)
	d2 := time.Since(t)
	fmt.Printf("\n方案2 CEL:        title=%v r18=%v visible=%v  (%s/eval)\n", celTitle, celR18, celVis, d2.Round(time.Microsecond))
	fmt.Printf("   编译期错误示例可捕获: %v\n", iss.Err() != nil)
	fmt.Println("   表达力:中(布尔/算术/字符串/列表,无循环无副作用);错误:编译期类型检查;静态分析:强;Explain:表达式级;安全:高(非图灵完备,可限时);UI 生成:难(表达式不结构化)")

	// ---- 方案 3：Starlark ----
	star := `
def extract(meta):
    title = meta["post"]["title"]
    r18 = "R-18" in meta["post"]["tags"]
    visible = not meta["filename"].startswith(".")
    return title, r18, visible
`
	t = time.Now()
	sTitle, sR18, sVis := starlarkEval(star)
	d3 := time.Since(t)
	fmt.Printf("\n方案3 Starlark:   title=%q r18=%v visible=%v  (%s incl. parse)\n", sTitle, sR18, sVis, d3.Round(time.Microsecond))
	fmt.Println("   表达力:最高(函数/循环/推导,平台作者可写复杂解析);错误:运行期栈;静态分析:弱;Explain:需插桩;安全:中(可沙箱+限步数,但需谨慎);UI 生成:不可")

	fmt.Println("\n混合结论(见报告 04):结构=JSON Schema;常见字段/条件=自建原语(默认、可生成 UInline);")
	fmt.Println("复杂平台=CEL 条件表达式(受限、可分析);真正超出=版本化外部进程/WASM 插件,不在配置里放 Starlark 任意执行。")
}

// —— 方案 1 原语实现 ——
func readPath(m map[string]any, path string) string {
	cur := any(m)
	for _, seg := range strings.Split(path, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = mm[seg]
	}
	if s, ok := cur.(string); ok {
		return s
	}
	return ""
}
func containsTag(m map[string]any, path, want string) bool {
	cur := any(m)
	for _, seg := range strings.Split(path, ".") {
		mm, _ := cur.(map[string]any)
		cur = mm[seg]
	}
	arr, ok := cur.([]any)
	if !ok {
		return false
	}
	for _, v := range arr {
		if s, _ := v.(string); s == want {
			return true
		}
	}
	return false
}
func glob(name, pattern string) bool {
	if pattern == ".*" {
		return strings.HasPrefix(name, ".")
	}
	return false
}

func celEval(env *cel.Env, expr string) any {
	ast, iss := env.Compile(expr)
	if iss.Err() != nil {
		return "ERR:" + iss.Err().Error()
	}
	prg, err := env.Program(ast)
	if err != nil {
		return "ERR"
	}
	out, _, err := prg.Eval(map[string]any{
		"post":     meta["post"],
		"filename": meta["filename"],
	})
	if err != nil {
		return "ERR"
	}
	if out == types.True {
		return true
	}
	if out == types.False {
		return false
	}
	return out.Value()
}

func starlarkEval(src string) (string, bool, bool) {
	thread := &starlark.Thread{Name: "rules"}
	globals, err := starlark.ExecFile(thread, "rules.star", src, nil)
	if err != nil {
		return "ERR", false, false
	}
	extract := globals["extract"]
	// 构造 starlark dict
	post := starlark.NewDict(0)
	post.SetKey(starlark.String("title"), starlark.String("星空下的约定"))
	tags := starlark.NewList([]starlark.Value{starlark.String("原创"), starlark.String("R-18"), starlark.String("风景")})
	post.SetKey(starlark.String("tags"), tags)
	m := starlark.NewDict(0)
	m.SetKey(starlark.String("post"), post)
	m.SetKey(starlark.String("filename"), starlark.String("cover.jpg"))
	res, err := starlark.Call(thread, extract, starlark.Tuple{m}, nil)
	if err != nil {
		return "ERR", false, false
	}
	tup := res.(starlark.Tuple)
	title := string(tup[0].(starlark.String))
	r18 := bool(tup[1].(starlark.Bool))
	vis := bool(tup[2].(starlark.Bool))
	return title, r18, vis
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "FATAL:", err)
		os.Exit(1)
	}
}
