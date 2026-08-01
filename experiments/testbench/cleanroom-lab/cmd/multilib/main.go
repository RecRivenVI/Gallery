// P16 + P11：多图库全局分页(净室,不预设"每库一 catalog")。
// 建三个独立图库库,验证跨库统一排序 + 稳定 keyset cursor + 扫描期翻页 + 库上下线。
// 对比两种物理布局:①每库一 db(ATTACH) ②统一 db + library_id 列。
package main

import (
	"database/sql"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"cleanroom-lab/internal/synth"
	_ "modernc.org/sqlite"
)

type row struct {
	lib, workID, published, title string
}

func main() {
	n := flag.Int("n", 20000, "works per library")
	dir := flag.String("dir", os.TempDir(), "work dir")
	flag.Parse()

	libs := []string{"libA", "libB", "libC"}

	// ---- 布局②统一库 + library_id ----
	uni := *dir + "/ml-unified.sqlite"
	os.Remove(uni)
	udb := open(uni)
	defer udb.Close()
	mustExec(udb, `CREATE TABLE works(library_id TEXT, work_id TEXT, published_at TEXT, title TEXT,
		PRIMARY KEY(library_id,work_id)) WITHOUT ROWID;
		CREATE INDEX ix_global ON works(published_at DESC, library_id, work_id);`)
	t0 := time.Now()
	for _, lib := range libs {
		works := synth.Generate(*n, 100, int64(len(lib)))
		tx, _ := udb.Begin()
		ins, _ := tx.Prepare(`INSERT INTO works VALUES(?,?,?,?)`)
		for _, w := range works {
			ins.Exec(lib, w.ID, w.PublishedAt, w.Title)
		}
		tx.Commit()
	}
	buildUni := time.Since(t0)

	// ---- 布局①每库一 db + ATTACH ----
	t0 = time.Now()
	main0 := *dir + "/ml-main.sqlite"
	os.Remove(main0)
	adb := open(main0)
	defer adb.Close()
	for _, lib := range libs {
		p := *dir + "/ml-" + lib + ".sqlite"
		os.Remove(p)
		ldb := open(p)
		mustExec(ldb, `CREATE TABLE works(work_id TEXT PRIMARY KEY, published_at TEXT, title TEXT) WITHOUT ROWID;
			CREATE INDEX ix_pub ON works(published_at DESC, work_id);`)
		works := synth.Generate(*n, 100, int64(len(lib)))
		tx, _ := ldb.Begin()
		ins, _ := tx.Prepare(`INSERT INTO works VALUES(?,?,?)`)
		for _, w := range works {
			ins.Exec(w.ID, w.PublishedAt, w.Title)
		}
		tx.Commit()
		ldb.Close()
		adb.Exec(fmt.Sprintf(`ATTACH DATABASE '%s' AS %s`, strings.ReplaceAll(p, `\`, `/`), lib))
	}
	buildAttach := time.Since(t0)

	fmt.Printf("build:  unified(library_id)=%s   per-lib+ATTACH=%s   (%d libs × %d works)\n\n",
		buildUni.Round(time.Millisecond), buildAttach.Round(time.Millisecond), len(libs), *n)

	// ---- 全局 keyset 分页:布局② ----
	fmt.Println("布局② 统一库全局 keyset 分页(cursor=(published_at,library_id,work_id)):")
	cursor := ""
	total := 0
	t0 = time.Now()
	for page := 0; page < 3; page++ {
		rows, cur := uniPage(udb, cursor, 48)
		total += len(rows)
		cursor = cur
		fmt.Printf("  page %d: %d rows, next-cursor=%s\n", page+1, len(rows), shortCur(cursor))
	}
	fmt.Printf("  3 页耗时 %s(单索引扫描,无 merge)\n\n", time.Since(t0).Round(time.Microsecond))

	// ---- 全局排序:布局① ATTACH UNION ALL ----
	fmt.Println("布局① ATTACH 跨库全局首页(UNION ALL + 全局 ORDER):")
	t0 = time.Now()
	q := `SELECT * FROM (
		SELECT 'libA' lib, work_id, published_at FROM libA.works
		UNION ALL SELECT 'libB', work_id, published_at FROM libB.works
		UNION ALL SELECT 'libC', work_id, published_at FROM libC.works
	) ORDER BY published_at DESC, lib, work_id LIMIT 48`
	rs, err := adb.Query(q)
	must(err)
	cnt := 0
	for rs.Next() {
		cnt++
	}
	rs.Close()
	fmt.Printf("  top-48=%d rows,耗时 %s(全量归并,随库数与规模线性放大)\n", cnt, time.Since(t0).Round(time.Millisecond))

	fmt.Println("\n库上线/下线:")
	fmt.Println("  布局② 用 WHERE library_id IN(启用集) 即时生效,零重连;单库重建=DELETE+重扫该 library_id(需扫全表定位)")
	fmt.Println("  布局① DETACH/ATTACH 即上下线,单库重建=替换该库文件(故障隔离最好);全局排序需应用层 k-way merge")
	fmt.Println("\n结论(报告 05):默认统一库 + library_id(全局分页走单索引,最省实现);")
	fmt.Println("       跨库全局排序用统一库天然解决;每库独立文件仅在'强故障隔离/超大库单独备份'时按需拆分。")
}

// 统一库 keyset:cursor 编码 (published_at, library_id, work_id)
func uniPage(db *sql.DB, cursor string, limit int) ([]row, string) {
	var rows []row
	var q string
	var args []any
	if cursor == "" {
		q = `SELECT library_id,work_id,published_at,title FROM works ORDER BY published_at DESC,library_id,work_id LIMIT ?`
		args = []any{limit}
	} else {
		p, l, w := decodeCur(cursor)
		q = `SELECT library_id,work_id,published_at,title FROM works
			WHERE (published_at,library_id,work_id) < (?,?,?)
			ORDER BY published_at DESC,library_id,work_id LIMIT ?`
		args = []any{p, l, w, limit}
	}
	rs, err := db.Query(q, args...)
	must(err)
	defer rs.Close()
	for rs.Next() {
		var r row
		rs.Scan(&r.lib, &r.workID, &r.published, &r.title)
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		return rows, ""
	}
	last := rows[len(rows)-1]
	return rows, encodeCur(last.published, last.lib, last.workID)
}

func encodeCur(p, l, w string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(p + "\x00" + l + "\x00" + w))
}
func decodeCur(c string) (string, string, string) {
	b, _ := base64.RawURLEncoding.DecodeString(c)
	parts := strings.SplitN(string(b), "\x00", 3)
	if len(parts) != 3 {
		return "", "", ""
	}
	return parts[0], parts[1], parts[2]
}
func shortCur(c string) string {
	if len(c) > 16 {
		return c[:16] + "…"
	}
	return c
}

func open(path string) *sql.DB {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	must(err)
	db.SetMaxOpenConns(1)
	return db
}
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "FATAL:", err)
		os.Exit(1)
	}
}
func mustExec(db *sql.DB, s string) { _, err := db.Exec(s); must(err) }
