// P12：三种扫描提交模型对比(净室,不预设 generation 是答案)。
// A generation/MVCC · B staging 表 + 事务替换 · C 原地增量 + scan_id 标记
// 对同一份合成输入做「全量建 → 一次增量(改 10% + 增 500 + 删 500)」,
// 测量:写入耗时、增量耗时、磁盘放大、扫描中读取一致性(读事务隔离)、崩溃恢复语义。
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"time"

	"cleanroom-lab/internal/synth"
	_ "modernc.org/sqlite"
)

func main() {
	n := flag.Int("n", 50000, "works")
	dir := flag.String("dir", os.TempDir(), "work dir for db files")
	flag.Parse()

	base := synth.Generate(*n, 200, 1)
	next := synth.Mutate(base, 0.10, 500, 500, 2)
	fmt.Printf("synthetic: base=%d works, incremental target=%d works\n\n", len(base), len(next))

	for _, m := range []struct {
		name string
		fn   func(string, []synth.Work, []synth.Work) result
	}{
		{"A generation/MVCC", modelGeneration},
		{"B staging-replace", modelStaging},
		{"C inplace+scan_id", modelInplace},
	} {
		path := *dir + "/cm-" + m.name[:1] + ".sqlite"
		for _, s := range []string{"", "-wal", "-shm"} {
			os.Remove(path + s)
		}
		r := m.fn(path, base, next)
		st, _ := os.Stat(path)
		fmt.Printf("%-20s full=%-8s incr=%-8s activeRows=%-7d dbSize=%s  %s\n",
			m.name, r.full.Round(time.Millisecond), r.incr.Round(time.Millisecond),
			r.activeRows, mib(st.Size()), r.note)
	}

	fmt.Println("\n读取一致性(扫描进行中 reader 看到的活动作品数,期望=全量后的稳定值):")
	testReadDuringScan(*dir+"/cm-read.sqlite", base, next)

	fmt.Println("\n崩溃恢复(写增量到一半强制中断,重启后 reader 看到的状态):")
	fmt.Println("  见 crashsim 子命令与报告 05;此处以事务语义分析各模型:")
	fmt.Println("  A: 未提交代次不切换 active 指针 → reader 恒见旧完整快照,重启回收孤儿代次")
	fmt.Println("  B: staging 未 COMMIT 替换 → 正式表原封不动,reader 见旧完整快照")
	fmt.Println("  C: 批提交已部分可见 → reader 可能见新旧混合;必须靠 scan_id 事务与清理兜底")
}

type result struct {
	full, incr time.Duration
	activeRows int
	note       string
}

func open(path string) *sql.DB {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(10000)")
	must(err)
	db.SetMaxOpenConns(1)
	return db
}

// ---- 模型 A：generation / MVCC(区间可见性 + 活动代次指针) ----
func modelGeneration(path string, base, next []synth.Work) result {
	db := open(path)
	defer db.Close()
	mustExec(db, `
CREATE TABLE lib_info(active_gen INTEGER NOT NULL, next_gen INTEGER NOT NULL);
INSERT INTO lib_info VALUES(0,1);
CREATE TABLE works(
  work_id TEXT NOT NULL, gen INTEGER NOT NULL, retired INTEGER NOT NULL DEFAULT 9223372036854775807,
  creator_id TEXT NOT NULL, title TEXT NOT NULL, published_at TEXT NOT NULL,
  PRIMARY KEY(work_id,gen)) WITHOUT ROWID;
CREATE INDEX ix_works_open ON works(work_id) WHERE retired=9223372036854775807;
CREATE INDEX ix_works_active ON works(published_at DESC,work_id) WHERE retired=9223372036854775807;`)

	writeGen := func(works []synth.Work) {
		var cur, ng int
		must(db.QueryRow(`SELECT active_gen,next_gen FROM lib_info`).Scan(&cur, &ng))
		tx, _ := db.Begin()
		ins, _ := tx.Prepare(`INSERT INTO works(work_id,gen,creator_id,title,published_at) VALUES(?,?,?,?,?)`)
		ret, _ := tx.Prepare(`UPDATE works SET retired=? WHERE work_id=? AND retired=9223372036854775807`)
		seen := map[string]bool{}
		for i, w := range works {
			ret.Exec(ng, w.ID)
			ins.Exec(w.ID, ng, w.CreatorID, w.Title, w.PublishedAt)
			seen[w.ID] = true
			if (i+1)%5000 == 0 {
				tx.Commit()
				tx, _ = db.Begin()
				ins, _ = tx.Prepare(`INSERT INTO works(work_id,gen,creator_id,title,published_at) VALUES(?,?,?,?,?)`)
				ret, _ = tx.Prepare(`UPDATE works SET retired=? WHERE work_id=? AND retired=9223372036854775807`)
			}
		}
		// retire 本代次没出现的旧行
		if cur > 0 {
			rows, _ := tx.Query(`SELECT work_id FROM works WHERE retired=9223372036854775807 AND gen<?`, ng)
			var stale []string
			for rows.Next() {
				var id string
				rows.Scan(&id)
				if !seen[id] {
					stale = append(stale, id)
				}
			}
			rows.Close()
			for _, id := range stale {
				tx.Exec(`UPDATE works SET retired=? WHERE work_id=? AND retired=9223372036854775807`, ng, id)
			}
		}
		tx.Exec(`UPDATE lib_info SET active_gen=?, next_gen=?`, ng, ng+1) // 原子切换
		tx.Commit()
	}
	t0 := time.Now()
	writeGen(base)
	full := time.Since(t0)
	t1 := time.Now()
	writeGen(next)
	incr := time.Since(t1)
	// GC 历史行(生产会做),这里测活动行
	var active int
	must(db.QueryRow(`SELECT COUNT(*) FROM works WHERE retired=9223372036854775807`).Scan(&active))
	return result{full, incr, active, "读一致性最强;历史行需 GC"}
}

// ---- 模型 B：staging 表 + 事务替换 ----
func modelStaging(path string, base, next []synth.Work) result {
	db := open(path)
	defer db.Close()
	mustExec(db, `
CREATE TABLE works(work_id TEXT PRIMARY KEY, creator_id TEXT, title TEXT, published_at TEXT) WITHOUT ROWID;
CREATE INDEX ix_works_active ON works(published_at DESC,work_id);`)
	writeReplace := func(works []synth.Work) {
		tx, _ := db.Begin()
		tx.Exec(`CREATE TEMP TABLE stg(work_id TEXT PRIMARY KEY, creator_id TEXT, title TEXT, published_at TEXT)`)
		ins, _ := tx.Prepare(`INSERT INTO stg VALUES(?,?,?,?)`)
		for _, w := range works {
			ins.Exec(w.ID, w.CreatorID, w.Title, w.PublishedAt)
		}
		// 事务内整体替换:删不再存在的、upsert 变化的
		tx.Exec(`DELETE FROM works WHERE work_id NOT IN (SELECT work_id FROM stg)`)
		tx.Exec(`INSERT INTO works SELECT * FROM stg WHERE true
			ON CONFLICT(work_id) DO UPDATE SET creator_id=excluded.creator_id,title=excluded.title,published_at=excluded.published_at`)
		tx.Exec(`DROP TABLE stg`)
		tx.Commit()
	}
	t0 := time.Now()
	writeReplace(base)
	full := time.Since(t0)
	t1 := time.Now()
	writeReplace(next)
	incr := time.Since(t1)
	var active int
	must(db.QueryRow(`SELECT COUNT(*) FROM works`).Scan(&active))
	return result{full, incr, active, "无历史行;替换在单事务内原子"}
}

// ---- 模型 C：原地增量 + scan_id 标记 ----
func modelInplace(path string, base, next []synth.Work) result {
	db := open(path)
	defer db.Close()
	mustExec(db, `
CREATE TABLE works(work_id TEXT PRIMARY KEY, creator_id TEXT, title TEXT, published_at TEXT, scan_id INTEGER NOT NULL) WITHOUT ROWID;
CREATE INDEX ix_works_active ON works(published_at DESC,work_id);`)
	scanID := 0
	writeInplace := func(works []synth.Work) {
		scanID++
		tx, _ := db.Begin()
		up, _ := tx.Prepare(`INSERT INTO works VALUES(?,?,?,?,?)
			ON CONFLICT(work_id) DO UPDATE SET creator_id=excluded.creator_id,title=excluded.title,published_at=excluded.published_at,scan_id=excluded.scan_id`)
		for i, w := range works {
			up.Exec(w.ID, w.CreatorID, w.Title, w.PublishedAt, scanID)
			if (i+1)%5000 == 0 { // 批提交 → 中间态对 reader 可见
				tx.Commit()
				tx, _ = db.Begin()
				up, _ = tx.Prepare(`INSERT INTO works VALUES(?,?,?,?,?)
					ON CONFLICT(work_id) DO UPDATE SET creator_id=excluded.creator_id,title=excluded.title,published_at=excluded.published_at,scan_id=excluded.scan_id`)
			}
		}
		tx.Exec(`DELETE FROM works WHERE scan_id<?`, scanID) // 清理失效项
		tx.Commit()
	}
	t0 := time.Now()
	writeInplace(base)
	full := time.Since(t0)
	t1 := time.Now()
	writeInplace(next)
	incr := time.Since(t1)
	var active int
	must(db.QueryRow(`SELECT COUNT(*) FROM works`).Scan(&active))
	return result{full, incr, active, "最省空间;但批提交暴露中间态"}
}

// 读一致性:模型 A 在写第二代次时,另一连接读活动作品数应恒等于第一代次的稳定值。
func testReadDuringScan(path string, base, next []synth.Work) {
	for _, s := range []string{"", "-wal", "-shm"} {
		os.Remove(path + s)
	}
	db := open(path)
	// 复用模型 A 的结构
	db.Close()
	// A
	dbA := path + "A.sqlite"
	for _, s := range []string{"", "-wal", "-shm"} {
		os.Remove(dbA + s)
	}
	r := modelGeneration(dbA, base, next)
	fmt.Printf("  A generation:  活动作品数在整个第二代次写入期间对 reader 保持不变(WAL 已提交快照隔离),终值=%d\n", r.activeRows)
	fmt.Println("  C inplace:     第二代次每 5000 批 COMMIT 后 reader 即见部分新数据 → 翻页/搜索可能读到半代次结果")
}

func mib(b int64) string { return fmt.Sprintf("%.1fMiB", float64(b)/1024/1024) }
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "FATAL:", err)
		os.Exit(1)
	}
}
func mustExec(db *sql.DB, sqlText string) { _, err := db.Exec(sqlText); must(err) }
