// celprofile 验证 Gallery CEL Profile 的编译、求值与成本上限。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/cel-go/cel"
)

type check struct {
	ID, Expression              string
	Compiled, Evaluated, Result bool
	ErrorCode                   string
}
type report struct {
	SchemaVersion, ProfileVersion int
	MaxExpressionBytes            int
	MaxRuntimeCost                uint64
	Checks                        []check
	ForbiddenCapabilities         []string
}

func main() {
	out := flag.String("out", "results/cel-profile.json", "result JSON")
	flag.Parse()
	env, e := cel.NewEnv(cel.Variable("meta", cel.DynType), cel.Variable("filename", cel.StringType))
	must(e)
	activation := map[string]any{"filename": "cover.jpg", "meta": map[string]any{"service": "fanbox", "tags": []any{"original", "R-18"}, "attachments": []any{map[string]any{"name": "cover", "type": "image"}, map[string]any{"name": "2", "type": "image"}}}}
	expressions := []check{{ID: "cover-name", Expression: `filename.startsWith("cover.")`}, {ID: "tag-exists", Expression: `meta.tags.exists(t, t == "R-18")`}, {ID: "attachment-predicate", Expression: `meta.attachments.exists(a, a.name == "cover" && a.type == "image")`}, {ID: "service-conditional", Expression: `meta.service == "fanbox" ? filename.startsWith("cover.") : true`}, {ID: "invalid-function", Expression: `meta.shell("cmd")`}}
	for i := range expressions {
		ast, iss := env.Compile(expressions[i].Expression)
		if iss.Err() != nil {
			expressions[i].ErrorCode = "COMPILE_ERROR"
			continue
		}
		expressions[i].Compiled = true
		prg, e := env.Program(ast, cel.CostLimit(10_000))
		if e != nil {
			expressions[i].ErrorCode = "PROGRAM_ERROR"
			continue
		}
		v, _, e := prg.Eval(activation)
		if e != nil {
			expressions[i].ErrorCode = "EVAL_ERROR"
			continue
		}
		expressions[i].Evaluated = true
		if b, ok := v.Value().(bool); ok {
			expressions[i].Result = b
		}
	}
	r := report{1, 1, 4096, 10_000, expressions, []string{"file-io", "network", "process", "clock", "random", "reflection", "custom-host-functions"}}
	b, e := json.MarshalIndent(r, "", "  ")
	must(e)
	must(os.MkdirAll(filepath.Dir(*out), 0o755))
	must(os.WriteFile(*out, b, 0o644))
	fmt.Printf("cel-profile compiled=%d evaluated=%d invalid-rejected=%v\n", count(r.Checks, "compiled"), count(r.Checks, "evaluated"), r.Checks[len(r.Checks)-1].ErrorCode == "COMPILE_ERROR")
}
func count(c []check, kind string) int {
	n := 0
	for _, x := range c {
		if kind == "compiled" && x.Compiled || kind == "evaluated" && x.Evaluated {
			n++
		}
	}
	return n
}
func must(e error) {
	if e != nil {
		panic(e)
	}
}
