// searchlatency 对已建索引重复执行分页查询，记录 P50/P95；不把全量 count 混入列表延迟。
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/blevesearch/bleve/v2"
	_ "github.com/blevesearch/bleve/v2/analysis/lang/cjk"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	_ "modernc.org/sqlite"
)

type metric struct {
	Engine, Query, Kind   string
	Runs, Limit, Verified int
	P50US, P95US          int64
	Hits                  int
}
type report struct {
	SchemaVersion int
	Metrics       []metric
}

func main() {
	dir := flag.String("dir", "results/search-million", "index directory")
	out := flag.String("out", "results/search-million-latency.json", "result JSON")
	runs := flag.Int("runs", 30, "runs")
	withBleve := flag.Bool("bleve", true, "measure Bleve")
	flag.Parse()
	db, e := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(*dir, "fts.sqlite"))+"?mode=ro")
	must(e)
	defer db.Close()
	var idx bleve.Index
	if *withBleve {
		idx, e = bleve.Open(filepath.Join(*dir, "bleve"))
		must(e)
		defer idx.Close()
	}
	r := report{SchemaVersion: 1}
	for _, p := range []struct{ q, k string }{{"星空", "broad-cjk"}, {"mid", "broad-filename"}, {"星空机械 作品 00000120", "selective-cjk"}, {"abc_mid_xyz_00000123", "selective-filename"}} {
		r.Metrics = append(r.Metrics, ftsMetric(db, p.q, p.k, *runs, 200))
		if idx != nil {
			r.Metrics = append(r.Metrics, bleveMetric(idx, p.q, p.k, *runs, 200))
		}
	}
	b, e := json.MarshalIndent(r, "", "  ")
	must(e)
	must(os.WriteFile(*out, b, 0o644))
}
func ftsMetric(db *sql.DB, q, kind string, runs, limit int) metric {
	times := make([]int64, 0, runs)
	verified := 0
	for i := 0; i < runs; i++ {
		t := time.Now()
		rows, e := db.Query(`SELECT docs.normalized FROM fts JOIN docs ON docs.id=fts.id WHERE fts MATCH ? LIMIT ?`, queryExpr(q), limit)
		must(e)
		n := 0
		for rows.Next() {
			var s string
			must(rows.Scan(&s))
			if strings.Contains(s, normalize(q)) {
				n++
			}
		}
		must(rows.Close())
		times = append(times, time.Since(t).Microseconds())
		verified = n
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	return metric{"fts5", q, kind, runs, limit, verified, times[len(times)/2], times[(len(times)*95+99)/100-1], verified}
}
func bleveMetric(idx bleve.Index, q, kind string, runs, limit int) metric {
	times := make([]int64, 0, runs)
	hits := 0
	for i := 0; i < runs; i++ {
		t := time.Now()
		req := bleve.NewSearchRequest(bleve.NewMatchQuery(q))
		req.Size = limit
		res, e := idx.Search(req)
		must(e)
		hits = int(res.Total)
		times = append(times, time.Since(t).Microseconds())
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	return metric{"bleve", q, kind, runs, limit, hits, times[len(times)/2], times[(len(times)*95+99)/100-1], hits}
}
func normalize(s string) string { return cases.Fold().String(norm.NFKC.String(s)) }
func tokens(s string) string {
	var out []string
	var run []rune
	flush := func() {
		if len(run) == 0 {
			return
		}
		if isCJK(run[0]) {
			if len(run) == 1 {
				out = append(out, string(run))
			} else {
				for i := 0; i+1 < len(run); i++ {
					out = append(out, string(run[i:i+2]))
				}
			}
		} else {
			out = append(out, string(run))
			if len(run) >= 3 {
				for i := 0; i+2 < len(run); i++ {
					out = append(out, string(run[i:i+3]))
				}
			}
		}
		run = nil
	}
	var cjk bool
	for _, r := range []rune(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			c := isCJK(r)
			if len(run) > 0 && c != cjk {
				flush()
			}
			run = append(run, r)
			cjk = c
		} else {
			flush()
		}
	}
	flush()
	return strings.Join(out, " ")
}
func queryExpr(q string) string {
	t := strings.Fields(tokens(normalize(q)))
	for i := range t {
		t[i] = `"` + t[i] + `"`
	}
	return strings.Join(t, " AND ")
}
func isCJK(r rune) bool {
	return r >= 0x3400 && r <= 0x9fff || r >= 0x3040 && r <= 0x30ff || r >= 0xac00 && r <= 0xd7af
}
func must(e error) {
	if e != nil {
		panic(e)
	}
}
