// P13：搜索方案对比(净室,不以"替换旧 LIKE"为目标)。
// 同一份多语言合成库,分别建 SQLite FTS5(trigram) 与 Bleve(纯 Go 全文引擎),
// 对中文/日文/英文、中缀、前缀、标签过滤跑一致性与耗时。
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"time"

	"cleanroom-lab/internal/synth"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/lang/cjk"
	"github.com/blevesearch/bleve/v2/mapping"
	_ "modernc.org/sqlite"
)

type doc struct {
	ID     string
	Title  string
	Author string
	Tags   string
	Lang   string
}

func main() {
	n := flag.Int("n", 50000, "works")
	dir := flag.String("dir", os.TempDir(), "work dir")
	flag.Parse()

	works := synth.Generate(*n, 200, 7)
	docs := make([]doc, len(works))
	for i, w := range works {
		tags := ""
		for _, t := range w.Tags {
			tags += t + " "
		}
		docs[i] = doc{w.ID, w.Title, w.CreatorName, tags, w.Media[0].Language}
	}
	fmt.Printf("corpus: %d docs (zh/ja/en mixed)\n\n", len(docs))

	ftsPath := *dir + "/sb-fts.sqlite"
	blevePath := *dir + "/sb-bleve"
	os.Remove(ftsPath)
	os.RemoveAll(blevePath)

	// ---- SQLite FTS5 trigram ----
	db, err := sql.Open("sqlite", "file:"+ftsPath+"?_pragma=journal_mode(WAL)")
	must(err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	mustExec(db, `CREATE VIRTUAL TABLE fts USING fts5(id UNINDEXED, title, author, tags, tokenize='trigram')`)
	t0 := time.Now()
	tx, _ := db.Begin()
	ins, _ := tx.Prepare(`INSERT INTO fts VALUES(?,?,?,?)`)
	for _, d := range docs {
		ins.Exec(d.ID, d.Title, d.Author, d.Tags)
	}
	tx.Commit()
	ftsBuild := time.Since(t0)
	stFts, _ := os.Stat(ftsPath)

	// ---- Bleve ----
	t0 = time.Now()
	im := bleve.NewIndexMapping()
	// cjk analyzer:CJK 走 bigram(二元组),恰好覆盖 FTS5 trigram 覆盖不了的 2 字 CJK;拉丁走标准分词
	im.DefaultAnalyzer = cjk.AnalyzerName
	tm := bleve.NewDocumentMapping()
	im.DefaultMapping = tm
	_ = mapping.NewTextFieldMapping()
	idx, err := bleve.New(blevePath, im)
	must(err)
	batch := idx.NewBatch()
	for i, d := range docs {
		batch.Index(d.ID, map[string]any{"title": d.Title, "author": d.Author, "tags": d.Tags})
		if (i+1)%5000 == 0 {
			idx.Batch(batch)
			batch = idx.NewBatch()
		}
	}
	idx.Batch(batch)
	bleveBuild := time.Since(t0)
	bleveSize := dirSize(blevePath)

	fmt.Printf("build:  FTS5=%-8s (%s)    Bleve=%-8s (%s)\n\n",
		ftsBuild.Round(time.Millisecond), mib(stFts.Size()),
		bleveBuild.Round(time.Millisecond), mib(bleveSize))

	// 用真实语料取查询词
	probes := []struct {
		label, q string
	}{
		{"中文中缀", "机械"},
		{"日文中缀", "機械"},
		{"英文词", "Mechanical"},
		{"中文2字", "星空"},
		{"标签", "original"},
	}
	fmt.Printf("%-12s %-10s | FTS5 hits/time         | Bleve hits/time\n", "probe", "query")
	for _, p := range probes {
		fc, fd := ftsQuery(db, p.q)
		bc, bd := bleveQuery(idx, p.q)
		fmt.Printf("%-12s %-10q | %-6d %-14s | %-6d %s\n",
			p.label, p.q, fc, fd.Round(time.Microsecond), bc, bd.Round(time.Microsecond))
	}
	idx.Close()
	fmt.Println("\n注:Bleve cjk analyzer 对 CJK 走 bigram → 2 字中文/日文词可精确命中(FTS5 trigram 在此返回 0);")
	fmt.Println("   代价:Bleve 是独立索引目录(非单文件),需与主库分别备份/一致性维护。")
	fmt.Println("   FTS5 单文件、随事务一致,但 <3 字符 CJK 需 LIKE/前缀兜底或自建 bigram 分词表。")
}

func ftsQuery(db *sql.DB, q string) (int, time.Duration) {
	t := time.Now()
	var n int
	// trigram MATCH 需要短语引号
	err := db.QueryRow(`SELECT COUNT(*) FROM fts WHERE fts MATCH ?`, `"`+q+`"`).Scan(&n)
	if err != nil {
		return -1, time.Since(t)
	}
	return n, time.Since(t)
}

func bleveQuery(idx bleve.Index, q string) (int, time.Duration) {
	t := time.Now()
	query := bleve.NewMatchQuery(q)
	req := bleve.NewSearchRequest(query)
	req.Size = 0
	res, err := idx.Search(req)
	if err != nil {
		return -1, time.Since(t)
	}
	return int(res.Total), time.Since(t)
}

func dirSize(p string) int64 {
	var total int64
	entries, _ := os.ReadDir(p)
	for _, e := range entries {
		if info, err := e.Info(); err == nil {
			if e.IsDir() {
				total += dirSize(p + "/" + e.Name())
			} else {
				total += info.Size()
			}
		}
	}
	return total
}

func mib(b int64) string { return fmt.Sprintf("%.1fMiB", float64(b)/1024/1024) }
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "FATAL:", err)
		os.Exit(1)
	}
}
func mustExec(db *sql.DB, s string) { _, err := db.Exec(s); must(err) }
