// searchbench 实测 FTS5 自建 CJK bigram/拉丁 trigram 候选召回，并以归一化原文二次确认。
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/lang/cjk"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	_ "modernc.org/sqlite"
)

type queryResult struct {
	Query, Kind                          string
	GroundTruth, Candidates, Verified    int
	Recall                               float64
	FTSMS, VerifyMS, FullScanMS, BleveMS int64
	BleveHits                            int
}
type report struct {
	SchemaVersion int           `json:"schema_version"`
	Documents     int           `json:"documents"`
	FTSBuildMS    int64         `json:"fts_build_ms"`
	BleveBuildMS  int64         `json:"bleve_build_ms"`
	FTSBytes      int64         `json:"fts_bytes"`
	BleveBytes    int64         `json:"bleve_bytes"`
	BleveEnabled  bool          `json:"bleve_enabled"`
	Queries       []queryResult `json:"queries"`
}

func main() {
	n := flag.Int("n", 1_000_000, "documents")
	dir := flag.String("dir", "results/search-million", "index directory")
	out := flag.String("out", "results/search-million.json", "result JSON")
	withBleve := flag.Bool("bleve", true, "build Bleve comparison")
	reuse := flag.Bool("reuse", false, "reuse existing indexes and only run queries")
	flag.Parse()
	dbPath := filepath.Join(*dir, "fts.sqlite")
	if !*reuse {
		must(os.RemoveAll(*dir))
		must(os.MkdirAll(*dir, 0o755))
	}
	db := open(dbPath)
	defer db.Close()
	var ftsBuild time.Duration
	if !*reuse {
		mustExec(db, `CREATE TABLE docs(id INTEGER PRIMARY KEY,text TEXT,normalized TEXT); CREATE VIRTUAL TABLE fts USING fts5(id UNINDEXED,tokens,tokenize='unicode61');`)
		t0 := time.Now()
		tx, _ := db.Begin()
		d, _ := tx.Prepare(`INSERT INTO docs VALUES(?,?,?)`)
		f, _ := tx.Prepare(`INSERT INTO fts VALUES(?,?)`)
		for id := 1; id <= *n; id++ {
			text := document(id)
			normalized := normalize(text)
			_, e := d.Exec(id, text, normalized)
			must(e)
			_, e = f.Exec(id, tokens(normalized))
			must(e)
			if id%250000 == 0 {
				fmt.Printf("  fts %d/%d\n", id, *n)
			}
		}
		must(d.Close())
		must(f.Close())
		must(tx.Commit())
		mustExec(db, `PRAGMA wal_checkpoint(TRUNCATE)`)
		ftsBuild = time.Since(t0)
	}
	var idx bleve.Index
	var bleveBuild time.Duration
	if *withBleve {
		var e error
		if *reuse {
			idx, e = bleve.Open(filepath.Join(*dir, "bleve"))
			must(e)
		} else {
			mapping := bleve.NewIndexMapping()
			mapping.DefaultAnalyzer = cjk.AnalyzerName
			t0 := time.Now()
			idx, e = bleve.New(filepath.Join(*dir, "bleve"), mapping)
			must(e)
			batch := idx.NewBatch()
			for id := 1; id <= *n; id++ {
				batch.Index(fmt.Sprintf("%d", id), map[string]any{"text": document(id)})
				if id%10000 == 0 {
					must(idx.Batch(batch))
					batch = idx.NewBatch()
				}
				if id%250000 == 0 {
					fmt.Printf("  bleve %d/%d\n", id, *n)
				}
			}
			must(idx.Batch(batch))
			bleveBuild = time.Since(t0)
		}
		defer idx.Close()
	}
	r := report{1, *n, ftsBuild.Milliseconds(), bleveBuild.Milliseconds(), fileSize(dbPath), dirSize(filepath.Join(*dir, "bleve")), *withBleve, nil}
	for _, p := range []struct{ q, kind string }{
		{"星", "CJK-1-gap"}, {"星空", "CJK-2"}, {"星空机械", "CJK-4"}, {"機械", "Japanese-2"}, {"かな", "kana"},
		{"Mechanical", "English-word"}, {"ＭＥＣＨＡＮＩＣＡＬ", "fullwidth-casefold"}, {"mid", "filename-infix"},
		{"original", "exact-tag-synthetic"}, {"00000120", "number"}, {"😀10", "emoji-natural-text"},
		{"xingkong", "pinyin-gap"}, {"Mechancal", "fuzzy-gap"},
		{"星空机械 作品 00000120", "CJK-selective"}, {"abc_mid_xyz_00000123", "filename-selective"},
	} {
		one := runQuery(db, idx, p.q, p.kind)
		r.Queries = append(r.Queries, one)
		fmt.Printf("%-16s truth=%d candidate=%d verified=%d recall=%.3f fts=%s verify=%s\n", p.kind, one.GroundTruth, one.Candidates, one.Verified, one.Recall, time.Duration(one.FTSMS)*time.Millisecond, time.Duration(one.VerifyMS)*time.Millisecond)
	}
	b, e := json.MarshalIndent(r, "", "  ")
	must(e)
	must(os.MkdirAll(filepath.Dir(*out), 0o755))
	must(os.WriteFile(*out, b, 0o644))
}

func document(id int) string {
	switch id % 6 {
	case 0:
		return fmt.Sprintf("星空机械 作品 %08d original", id)
	case 1:
		return fmt.Sprintf("機械少女 かなカナ %08d", id)
	case 2:
		return fmt.Sprintf("Mechanical Archive file%08d", id)
	case 3:
		return fmt.Sprintf("gallery abc_mid_xyz_%08d.png", id)
	case 4:
		return fmt.Sprintf("星空摄影 photo %08d", id)
	default:
		return fmt.Sprintf("emoji 😀10 set %08d", id)
	}
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
			word := string(run)
			out = append(out, word)
			if len(run) >= 3 {
				for i := 0; i+2 < len(run); i++ {
					out = append(out, string(run[i:i+3]))
				}
			}
		}
		run = nil
	}
	var lastCJK bool
	for _, r := range []rune(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			c := isCJK(r)
			if len(run) > 0 && c != lastCJK {
				flush()
			}
			run = append(run, r)
			lastCJK = c
		} else {
			flush()
		}
	}
	flush()
	return strings.Join(out, " ")
}
func queryExpr(q string) string {
	ts := strings.Fields(tokens(normalize(q)))
	if len(ts) == 0 {
		return `""`
	}
	for i := range ts {
		ts[i] = `"` + strings.ReplaceAll(ts[i], `"`, `""`) + `"`
	}
	return strings.Join(ts, " AND ")
}
func runQuery(db *sql.DB, idx bleve.Index, q, kind string) queryResult {
	normQ := normalize(q)
	t := time.Now()
	var truth int
	must(db.QueryRow(`SELECT count(*) FROM docs WHERE instr(normalized,?)>0`, normQ).Scan(&truth))
	full := time.Since(t)
	t = time.Now()
	rows, e := db.Query(`SELECT docs.normalized FROM fts JOIN docs ON docs.id=fts.id WHERE fts MATCH ?`, queryExpr(q))
	must(e)
	ftsDur := time.Since(t)
	var candidates, verified int
	t = time.Now()
	for rows.Next() {
		var original string
		must(rows.Scan(&original))
		candidates++
		if strings.Contains(original, normQ) {
			verified++
		}
	}
	must(rows.Close())
	verifyDur := time.Since(t)
	bleveHits := -1
	var bleveDur time.Duration
	if idx != nil {
		t = time.Now()
		req := bleve.NewSearchRequest(bleve.NewMatchQuery(q))
		req.Size = 0
		res, e := idx.Search(req)
		must(e)
		bleveDur = time.Since(t)
		bleveHits = int(res.Total)
	}
	recall := 1.0
	if truth > 0 {
		recall = float64(verified) / float64(truth)
	}
	return queryResult{q, kind, truth, candidates, verified, recall, ftsDur.Milliseconds(), verifyDur.Milliseconds(), full.Milliseconds(), bleveDur.Milliseconds(), bleveHits}
}
func isCJK(r rune) bool {
	return r >= 0x3400 && r <= 0x9fff || r >= 0x3040 && r <= 0x30ff || r >= 0xac00 && r <= 0xd7af
}
func open(path string) *sql.DB {
	db, e := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=temp_store(MEMORY)&_pragma=cache_size(-131072)")
	must(e)
	db.SetMaxOpenConns(1)
	return db
}
func mustExec(db *sql.DB, q string) { _, e := db.Exec(q); must(e) }
func fileSize(p string) int64 {
	s, e := os.Stat(p)
	if e != nil {
		return 0
	}
	return s.Size()
}
func dirSize(p string) int64 {
	var n int64
	_ = filepath.Walk(p, func(_ string, i os.FileInfo, e error) error {
		if e == nil && !i.IsDir() {
			n += i.Size()
		}
		return nil
	})
	return n
}
func must(e error) {
	if e != nil {
		panic(e)
	}
}
