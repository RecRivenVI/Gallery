// commitbench 复验 Gallery 的 Catalog 提交边界：代次增量与快照暂存均把长构建放在发布前。
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const infinity = int64(9223372036854775807)

type run struct {
	Model             string  `json:"model"`
	Works             int     `json:"works"`
	ChangeRatio       float64 `json:"change_ratio"`
	InitialBuildMS    int64   `json:"initial_build_ms"`
	LongBuildMS       int64   `json:"long_build_ms"`
	PublishMS         int64   `json:"publish_ms"`
	ReaderBefore      int     `json:"reader_before"`
	ReaderDuring      int     `json:"reader_during"`
	ReaderAfter       int     `json:"reader_after"`
	SearchBefore      int     `json:"search_before"`
	SearchAfter       int     `json:"search_after"`
	CrashKeptRevision bool    `json:"crash_kept_revision"`
	GCMS              int64   `json:"gc_ms"`
	CheckpointMS      int64   `json:"checkpoint_ms"`
	VacuumMS          int64   `json:"vacuum_ms"`
	BytesBeforeGC     int64   `json:"bytes_before_gc"`
	BytesAfterGC      int64   `json:"bytes_after_gc"`
	WALBytesBeforeGC  int64   `json:"wal_bytes_before_gc"`
	RelationsPerWork  int     `json:"relations_per_work"`
	PublishRows       int     `json:"publish_rows"`
}

type report struct {
	SchemaVersion int    `json:"schema_version"`
	GeneratedAt   string `json:"generated_at"`
	SQLiteVersion string `json:"sqlite_version"`
	Runs          []run  `json:"runs"`
}

func main() {
	n := flag.Int("n", 1_000_000, "works")
	dir := flag.String("dir", "results/commitbench", "database directory")
	out := flag.String("out", "results/commitbench.json", "result JSON")
	ratiosText := flag.String("ratios", "0.01,0.10,0.50", "comma-separated change ratios")
	flag.Parse()
	must(os.MkdirAll(*dir, 0o755))
	ratios := parseRatios(*ratiosText)
	r := report{SchemaVersion: 1, GeneratedAt: time.Now().UTC().Format(time.RFC3339)}
	probe := open(filepath.Join(*dir, "version.sqlite"))
	must(probe.QueryRow(`SELECT sqlite_version()`).Scan(&r.SQLiteVersion))
	probe.Close()
	os.Remove(filepath.Join(*dir, "version.sqlite"))
	for _, model := range []string{"generation-delta", "staging-snapshot"} {
		for _, ratio := range ratios {
			path := filepath.Join(*dir, fmt.Sprintf("%s-%03d.sqlite", model, int(ratio*100)))
			removeDB(path)
			var one run
			if model == "generation-delta" {
				one = generation(path, *n, ratio)
			} else {
				one = staging(path, *n, ratio)
			}
			r.Runs = append(r.Runs, one)
			fmt.Printf("%s change=%.0f%% build=%s publish=%s before/during/after=%d/%d/%d size=%.1f->%.1fMiB\n",
				one.Model, ratio*100, ms(one.LongBuildMS), ms(one.PublishMS), one.ReaderBefore, one.ReaderDuring,
				one.ReaderAfter, mib(one.BytesBeforeGC), mib(one.BytesAfterGC))
		}
	}
	writeJSON(*out, r)
}

func generation(path string, n int, ratio float64) run {
	db := open(path)
	defer db.Close()
	mustExec(db, `
CREATE TABLE state(active_rev INTEGER NOT NULL); INSERT INTO state VALUES(0);
CREATE TABLE works(id INTEGER, valid_from INTEGER, valid_to INTEGER, creator_id INTEGER, title TEXT, sort_key TEXT, PRIMARY KEY(id,valid_from)) WITHOUT ROWID;
CREATE TABLE work_creators(work_id INTEGER, valid_from INTEGER, valid_to INTEGER, creator_id INTEGER, PRIMARY KEY(work_id,valid_from)) WITHOUT ROWID;
CREATE TABLE media(media_id INTEGER, valid_from INTEGER, valid_to INTEGER, work_id INTEGER, blob_id TEXT, location_id TEXT, PRIMARY KEY(media_id,valid_from)) WITHOUT ROWID;
CREATE VIRTUAL TABLE search_fts USING fts5(work_id UNINDEXED, valid_from UNINDEXED, valid_to UNINDEXED, text, tokenize='unicode61');
CREATE INDEX ix_works_visible ON works(valid_from,valid_to,id);
CREATE TEMP TABLE delta(id INTEGER PRIMARY KEY, creator_id INTEGER, title TEXT, sort_key TEXT, blob_id TEXT, location_id TEXT) WITHOUT ROWID;`)
	t0 := time.Now()
	fillDelta(db, n, 1, 1.0)
	tx, _ := db.Begin()
	_, err := tx.Exec(`INSERT INTO works SELECT id,1,?,creator_id,title,sort_key FROM delta;
INSERT INTO work_creators SELECT id,1,?,creator_id FROM delta;
INSERT INTO media SELECT id,1,?,id,blob_id,location_id FROM delta;
INSERT INTO search_fts SELECT id,1,?,title FROM delta;
UPDATE state SET active_rev=1;`, infinity, infinity, infinity, infinity)
	must(err)
	must(tx.Commit())
	initial := time.Since(t0)
	before := visibleCount(db, 1)
	searchBefore := searchCount(db, 1, "作品")

	t0 = time.Now()
	mustExec(db, `DELETE FROM delta`)
	fillDelta(db, n, 2, ratio)
	build := time.Since(t0)
	during := visibleCount(db, 1)

	changed := max(1, int(float64(n)*ratio))
	t0 = time.Now()
	tx, _ = db.Begin()
	_, err = tx.Exec(`UPDATE works SET valid_to=2 WHERE id IN (SELECT id FROM delta) AND valid_to=?;
UPDATE work_creators SET valid_to=2 WHERE work_id IN (SELECT id FROM delta) AND valid_to=?;
UPDATE media SET valid_to=2 WHERE media_id IN (SELECT id FROM delta) AND valid_to=?;
UPDATE search_fts SET valid_to=2 WHERE work_id IN (SELECT id FROM delta) AND valid_to=?;
INSERT INTO works SELECT id,2,?,creator_id,title,sort_key FROM delta;
INSERT INTO work_creators SELECT id,2,?,creator_id FROM delta;
INSERT INTO media SELECT id,2,?,id,blob_id,location_id FROM delta;
INSERT INTO search_fts SELECT id,2,?,title FROM delta;
UPDATE state SET active_rev=2;`, infinity, infinity, infinity, infinity, infinity, infinity, infinity, infinity)
	must(err)
	must(tx.Commit())
	publish := time.Since(t0)
	after := visibleCount(db, 2)
	searchAfter := searchCount(db, 2, "已改")
	crashSafe := rollbackProbe(db, 2)
	beforeBytes, wal := dbBytes(path)
	t0 = time.Now()
	mustExec(db, `DELETE FROM works WHERE valid_to<=2; DELETE FROM work_creators WHERE valid_to<=2; DELETE FROM media WHERE valid_to<=2; DELETE FROM search_fts WHERE valid_to<=2;`)
	gc := time.Since(t0)
	checkpoint := timedExec(db, `PRAGMA wal_checkpoint(TRUNCATE)`)
	vacuum := timedExec(db, `VACUUM`)
	mustExec(db, `PRAGMA wal_checkpoint(TRUNCATE)`)
	afterBytes, _ := dbBytes(path)
	return run{"generation-delta", n, ratio, initial.Milliseconds(), build.Milliseconds(), publish.Milliseconds(), before, during, after,
		searchBefore, searchAfter, crashSafe, gc.Milliseconds(), checkpoint.Milliseconds(), vacuum.Milliseconds(), beforeBytes, afterBytes, wal, 2, changed * 4}
}

func staging(path string, n int, ratio float64) run {
	db := open(path)
	defer db.Close()
	mustExec(db, `
CREATE TABLE state(active_rev INTEGER NOT NULL); INSERT INTO state VALUES(0);
CREATE TABLE works(rev INTEGER,id INTEGER,creator_id INTEGER,title TEXT,sort_key TEXT,PRIMARY KEY(rev,id)) WITHOUT ROWID;
CREATE TABLE work_creators(rev INTEGER,work_id INTEGER,creator_id INTEGER,PRIMARY KEY(rev,work_id)) WITHOUT ROWID;
CREATE TABLE media(rev INTEGER,media_id INTEGER,work_id INTEGER,blob_id TEXT,location_id TEXT,PRIMARY KEY(rev,media_id)) WITHOUT ROWID;
CREATE VIRTUAL TABLE search_fts USING fts5(rev UNINDEXED,work_id UNINDEXED,text,tokenize='unicode61');
CREATE INDEX ix_works_page ON works(rev,sort_key,id);`)
	t0 := time.Now()
	fillSnapshot(db, n, 1, 0)
	mustExec(db, `UPDATE state SET active_rev=1`)
	initial := time.Since(t0)
	before := snapshotCount(db, 1)
	searchBefore := snapshotSearch(db, 1, "作品")
	t0 = time.Now()
	fillSnapshot(db, n, 2, ratio)
	build := time.Since(t0)
	during := snapshotCount(db, 1)
	t0 = time.Now()
	mustExec(db, `BEGIN IMMEDIATE; UPDATE state SET active_rev=2; COMMIT;`)
	publish := time.Since(t0)
	after := snapshotCount(db, 2)
	searchAfter := snapshotSearch(db, 2, "已改")
	crashSafe := rollbackProbe(db, 2)
	beforeBytes, wal := dbBytes(path)
	t0 = time.Now()
	mustExec(db, `DELETE FROM works WHERE rev<>2; DELETE FROM work_creators WHERE rev<>2; DELETE FROM media WHERE rev<>2; DELETE FROM search_fts WHERE rev<>2;`)
	gc := time.Since(t0)
	checkpoint := timedExec(db, `PRAGMA wal_checkpoint(TRUNCATE)`)
	vacuum := timedExec(db, `VACUUM`)
	mustExec(db, `PRAGMA wal_checkpoint(TRUNCATE)`)
	afterBytes, _ := dbBytes(path)
	return run{"staging-snapshot", n, ratio, initial.Milliseconds(), build.Milliseconds(), publish.Milliseconds(), before, during, after,
		searchBefore, searchAfter, crashSafe, gc.Milliseconds(), checkpoint.Milliseconds(), vacuum.Milliseconds(), beforeBytes, afterBytes, wal, 2, n * 4}
}

func fillDelta(db *sql.DB, n, rev int, ratio float64) {
	limit := n
	if ratio < 1 {
		limit = max(1, int(float64(n)*ratio))
	}
	tx, _ := db.Begin()
	stmt, _ := tx.Prepare(`INSERT INTO delta VALUES(?,?,?,?,?,?)`)
	for id := 1; id <= limit; id++ {
		title := fmt.Sprintf("作品 %07d 图集", id)
		if rev > 1 {
			title = fmt.Sprintf("已改作品 %07d", id)
		}
		_, err := stmt.Exec(id, id%10000, title, fmt.Sprintf("%08d", id), fmt.Sprintf("blob-%08d-r%d", id, rev), fmt.Sprintf("loc-%08d", id))
		must(err)
	}
	must(stmt.Close())
	must(tx.Commit())
}

func fillSnapshot(db *sql.DB, n, rev int, changedRatio float64) {
	tx, _ := db.Begin()
	w, _ := tx.Prepare(`INSERT INTO works VALUES(?,?,?,?,?)`)
	wc, _ := tx.Prepare(`INSERT INTO work_creators VALUES(?,?,?)`)
	m, _ := tx.Prepare(`INSERT INTO media VALUES(?,?,?,?,?)`)
	f, _ := tx.Prepare(`INSERT INTO search_fts VALUES(?,?,?)`)
	changed := int(float64(n) * changedRatio)
	for id := 1; id <= n; id++ {
		title := fmt.Sprintf("作品 %07d 图集", id)
		blobRev := 1
		if id <= changed {
			title = fmt.Sprintf("已改作品 %07d", id)
			blobRev = 2
		}
		_, err := w.Exec(rev, id, id%10000, title, fmt.Sprintf("%08d", id))
		must(err)
		_, err = wc.Exec(rev, id, id%10000)
		must(err)
		_, err = m.Exec(rev, id, id, fmt.Sprintf("blob-%08d-r%d", id, blobRev), fmt.Sprintf("loc-%08d", id))
		must(err)
		_, err = f.Exec(rev, id, title)
		must(err)
		if id%100000 == 0 {
			fmt.Printf("  staging rev=%d %d/%d\n", rev, id, n)
		}
	}
	must(w.Close())
	must(wc.Close())
	must(m.Close())
	must(f.Close())
	must(tx.Commit())
}

func visibleCount(db *sql.DB, rev int) int {
	var n int
	must(db.QueryRow(`SELECT count(*) FROM works WHERE valid_from<=? AND valid_to>?`, rev, rev).Scan(&n))
	return n
}
func searchCount(db *sql.DB, rev int, q string) int {
	var n int
	must(db.QueryRow(`SELECT count(*) FROM search_fts WHERE search_fts MATCH ? AND valid_from<=? AND valid_to>?`, q, rev, rev).Scan(&n))
	return n
}
func snapshotCount(db *sql.DB, rev int) int {
	var n int
	must(db.QueryRow(`SELECT count(*) FROM works WHERE rev=?`, rev).Scan(&n))
	return n
}
func snapshotSearch(db *sql.DB, rev int, q string) int {
	var n int
	must(db.QueryRow(`SELECT count(*) FROM search_fts WHERE search_fts MATCH ? AND rev=?`, q, rev).Scan(&n))
	return n
}
func rollbackProbe(db *sql.DB, want int) bool {
	tx, _ := db.Begin()
	_, err := tx.Exec(`UPDATE state SET active_rev=?`, want+1)
	must(err)
	must(tx.Rollback())
	var got int
	must(db.QueryRow(`SELECT active_rev FROM state`).Scan(&got))
	return got == want
}
func timedExec(db *sql.DB, q string) time.Duration {
	t := time.Now()
	mustExec(db, q)
	return time.Since(t)
}
func open(path string) *sql.DB {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(60000)&_pragma=temp_store(MEMORY)")
	must(err)
	db.SetMaxOpenConns(1)
	return db
}
func removeDB(path string) {
	for _, s := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(path + s)
	}
}
func dbBytes(path string) (int64, int64) {
	var main, wal int64
	if s, e := os.Stat(path); e == nil {
		main = s.Size()
	}
	if s, e := os.Stat(path + "-wal"); e == nil {
		wal = s.Size()
	}
	return main + wal, wal
}
func parseRatios(s string) []float64 {
	var out []float64
	for _, p := range strings.Split(s, ",") {
		v, e := strconv.ParseFloat(strings.TrimSpace(p), 64)
		must(e)
		if v <= 0 || v > 1 {
			must(fmt.Errorf("invalid ratio %v", v))
		}
		out = append(out, v)
	}
	return out
}
func writeJSON(path string, v any) {
	must(os.MkdirAll(filepath.Dir(path), 0o755))
	b, e := json.MarshalIndent(v, "", "  ")
	must(e)
	must(os.WriteFile(path, b, 0o644))
}
func mustExec(db *sql.DB, q string) { _, e := db.Exec(q); must(e) }
func must(e error) {
	if e != nil {
		panic(e)
	}
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func mib(v int64) float64      { return float64(v) / 1024 / 1024 }
func ms(v int64) time.Duration { return time.Duration(v) * time.Millisecond }
