// ruleprobe 在真实媒体目录上只读执行一组通用规则，并且只输出聚合计数。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type rootFlags []string

func (r *rootFlags) String() string     { return strings.Join(*r, ",") }
func (r *rootFlags) Set(v string) error { *r = append(*r, v); return nil }

type ruleResult struct {
	ID, Primitive, Need string
	Matched, Evaluated  int64
}
type rootResult struct {
	Alias             string       `json:"alias"`
	MetadataEvaluated int64        `json:"metadata_evaluated"`
	Rules             []ruleResult `json:"rules"`
}
type report struct {
	SchemaVersion int          `json:"schema_version"`
	GeneratedAt   string       `json:"generated_at"`
	Roots         []rootResult `json:"roots"`
}

var mediaRE = regexp.MustCompile(`(?i)\.(jpe?g|png|gif|webp|avif|mp4|webm|mov|m4v)$`)
var previewRE = regexp.MustCompile(`(?i)(preview|thumb|thumbnail|blur)`)

func main() {
	var roots rootFlags
	flag.Var(&roots, "root", "repeatable alias=absolute-path")
	out := flag.String("out", "results/rules-real.json", "result JSON")
	limit := flag.Int("limit", 3000, "metadata files per root")
	flag.Parse()
	if len(roots) < 2 {
		panic("at least two roots required")
	}
	rep := report{1, time.Now().UTC().Format(time.RFC3339), nil}
	for _, raw := range roots {
		parts := strings.SplitN(raw, "=", 2)
		if len(parts) != 2 {
			panic("invalid root")
		}
		root := filepath.Clean(parts[1])
		if inside(root, *out) {
			panic("output inside media root")
		}
		r := rootResult{Alias: parts[0], Rules: newRules()}
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.EqualFold(d.Name(), "metadata.json") || int(r.MetadataEvaluated) >= *limit {
				return nil
			}
			b, e := os.ReadFile(path)
			if e != nil {
				return nil
			}
			var v any
			if json.Unmarshal(b, &v) != nil {
				return nil
			}
			r.MetadataEvaluated++
			files, _ := os.ReadDir(filepath.Dir(path))
			eval(&r, v, files)
			return nil
		})
		rep.Roots = append(rep.Roots, r)
		fmt.Printf("%s metadata=%d rules=%d\n", r.Alias, r.MetadataEvaluated, len(r.Rules))
	}
	b, e := json.MarshalIndent(rep, "", "  ")
	must(e)
	must(os.MkdirAll(filepath.Dir(*out), 0o755))
	must(os.WriteFile(*out, b, 0o644))
}

func newRules() []ruleResult {
	return []ruleResult{
		{ID: "R01-media-extension", Primitive: "file.match", Need: "primitive"},
		{ID: "R02-cover-basename", Primitive: "file.basename", Need: "primitive"},
		{ID: "R03-preview-hide", Primitive: "file.regex", Need: "primitive"},
		{ID: "R04-title-fallback", Primitive: "metadata.first", Need: "primitive"},
		{ID: "R05-creator-fallback", Primitive: "metadata.first", Need: "primitive"},
		{ID: "R06-attachment-array", Primitive: "metadata.array", Need: "primitive"},
		{ID: "R07-conditional-cover", Primitive: "when+array.filter", Need: "cel-profile"},
		{ID: "R08-date-normalize", Primitive: "time.parse", Need: "primitive"},
		{ID: "R09-derived-preview", Primitive: "derive.policy", Need: "primitive"},
		{ID: "R10-cross-record-dedupe", Primitive: "stateful.aggregate", Need: "plugin"},
	}
}

func eval(r *rootResult, v any, files []fs.DirEntry) {
	for i := range r.Rules {
		r.Rules[i].Evaluated++
	}
	media, cover, preview := false, false, false
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		n := f.Name()
		if mediaRE.MatchString(n) {
			media = true
		}
		base := strings.TrimSuffix(n, filepath.Ext(n))
		if strings.EqualFold(base, "cover") {
			cover = true
		}
		if previewRE.MatchString(n) {
			preview = true
		}
	}
	if media {
		r.Rules[0].Matched++
	}
	if cover {
		r.Rules[1].Matched++
	}
	if preview {
		r.Rules[2].Matched++
	}
	if first(v, "title", "name", "caption", "post.title") != "" {
		r.Rules[3].Matched++
	}
	if first(v, "user.name", "creator.name", "author.name", "user.displayName", "service") != "" {
		r.Rules[4].Matched++
	}
	arr := arrayLen(v, "attachments") + arrayLen(v, "postMedia") + arrayLen(v, "files")
	if arr > 0 {
		r.Rules[5].Matched++
		r.Rules[6].Matched++
	}
	if first(v, "published", "publishedAt", "create_date", "date", "createdAt") != "" {
		r.Rules[7].Matched++
	}
	if media {
		r.Rules[8].Matched++
	}
	// 跨作品去重要求全局状态，本地单作品规则故意不声称命中。
}

func get(v any, path string) any {
	cur := v
	for _, p := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[p]
	}
	return cur
}
func first(v any, paths ...string) string {
	for _, p := range paths {
		switch x := get(v, p).(type) {
		case string:
			if strings.TrimSpace(x) != "" {
				return "present"
			}
		case float64:
			return "present"
		}
	}
	return ""
}
func arrayLen(v any, path string) int {
	if x, ok := get(v, path).([]any); ok {
		return len(x)
	}
	return 0
}
func inside(root, candidate string) bool {
	a, _ := filepath.Abs(root)
	b, _ := filepath.Abs(candidate)
	rel, e := filepath.Rel(a, b)
	return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func must(e error) {
	if e != nil {
		panic(e)
	}
}
