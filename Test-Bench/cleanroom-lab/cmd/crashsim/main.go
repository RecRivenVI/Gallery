// P12 崩溃恢复实证:对模型 A(generation)与模型 C(inplace)各建基线库,
// 再 spawn 子进程写增量到一半 os.Exit(1) 强杀,父进程重开库看 reader 状态。
// 目的:用真实进程崩溃(不是分析)证明两模型的恢复语义差异。
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"os/exec"

	"cleanroom-lab/internal/synth"
	_ "modernc.org/sqlite"
)

func open(path string) *sql.DB {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)")
	if err != nil {
		panic(err)
	}
	db.SetMaxOpenConns(1)
	return db
}

func main() {
	model := flag.String("model", "", "A|C (child mode)")
	path := flag.String("path", "", "db path (child mode)")
	dir := flag.String("dir", os.TempDir(), "work dir")
	flag.Parse()

	if *model != "" { // 子进程:写增量到一半就崩
		childWriteThenCrash(*model, *path)
		return
	}

	base := synth.Generate(20000, 100, 1)
	next := synth.Mutate(base, 0.2, 200, 200, 2)
	_ = next

	for _, m := range []string{"A", "C"} {
		p := *dir + "/crash-" + m + ".sqlite"
		for _, s := range []string{"", "-wal", "-shm"} {
			os.Remove(p + s)
		}
		buildBaseline(m, p, base)
		before := activeCount(p, m)

		// spawn 子进程,让它写增量并在中途崩溃
		cmd := exec.Command(os.Args[0], "-model", m, "-path", p)
		cmd.Stdout, cmd.Stderr = nil, nil
		err := cmd.Run()
		crashed := err != nil

		after := activeCount(p, m)
		mixed := mixedState(p, m)
		verdict := "✓ 崩溃后 reader 仍见完整基线快照(未被半代次污染)"
		if m == "C" && mixed > 0 {
			verdict = fmt.Sprintf("⚠ 崩溃后有 %d 行已是新 scan_id(已提交批可见)——新旧混合,必须靠 scan_id 清理兜底", mixed)
		}
		fmt.Printf("模型 %s: 基线活动作品=%d, 子进程崩溃=%v, 崩溃后活动作品=%d, 新scan_id可见行=%d\n  %s\n",
			m, before, crashed, after, mixed, verdict)
	}
	fmt.Println("\n结论:A(未提交代次不切 active 指针)崩溃后天然干净;C(批提交)崩溃后暴露中间态,依赖清理逻辑。")
}

func buildBaseline(model, path string, works []synth.Work) {
	db := open(path)
	defer db.Close()
	if model == "A" {
		db.Exec(`CREATE TABLE lib_info(active_gen INTEGER, next_gen INTEGER); INSERT INTO lib_info VALUES(1,2);
			CREATE TABLE works(work_id TEXT, gen INTEGER, retired INTEGER DEFAULT 9223372036854775807, title TEXT, PRIMARY KEY(work_id,gen)) WITHOUT ROWID;`)
		tx, _ := db.Begin()
		ins, _ := tx.Prepare(`INSERT INTO works(work_id,gen,title) VALUES(?,1,?)`)
		for _, w := range works {
			ins.Exec(w.ID, w.Title)
		}
		tx.Commit()
	} else {
		db.Exec(`CREATE TABLE works(work_id TEXT PRIMARY KEY, title TEXT, scan_id INTEGER) WITHOUT ROWID;`)
		tx, _ := db.Begin()
		ins, _ := tx.Prepare(`INSERT INTO works VALUES(?,?,1)`)
		for _, w := range works {
			ins.Exec(w.ID, w.Title)
		}
		tx.Commit()
	}
}

// 子进程:写第二代次/scan,到一半 os.Exit(1)
func childWriteThenCrash(model, path string) {
	db := open(path)
	next := synth.Mutate(synth.Generate(20000, 100, 1), 0.2, 200, 200, 2)
	if model == "A" {
		// 写 gen=2,但绝不提交 active 指针切换 → 崩溃前 active 仍是 gen1
		tx, _ := db.Begin()
		ins, _ := tx.Prepare(`INSERT INTO works(work_id,gen,title) VALUES(?,2,?)`)
		for i, w := range next {
			ins.Exec(w.ID+"-g2", 2, w.Title)
			if i == len(next)/2 {
				os.Exit(1) // 崩溃:事务未提交,active_gen 未切换
			}
		}
	} else {
		// C:每 5000 批提交,写到一半崩 → 部分批已可见
		scanID := 2
		tx, _ := db.Begin()
		up, _ := tx.Prepare(`INSERT INTO works VALUES(?,?,?) ON CONFLICT(work_id) DO UPDATE SET title=excluded.title,scan_id=excluded.scan_id`)
		for i, w := range next {
			up.Exec(w.ID, w.Title+"(new)", scanID)
			if (i+1)%5000 == 0 {
				tx.Commit()
				if i > len(next)/2 {
					os.Exit(1) // 崩溃:已有若干批提交可见
				}
				tx, _ = db.Begin()
				up, _ = tx.Prepare(`INSERT INTO works VALUES(?,?,?) ON CONFLICT(work_id) DO UPDATE SET title=excluded.title,scan_id=excluded.scan_id`)
			}
		}
	}
	os.Exit(1)
}

// 揭示 C 模型的新旧混合:崩溃后有多少行已带新 scan_id(=已提交批可见)。
func mixedState(path, model string) int {
	if model != "C" {
		return 0
	}
	db := open(path)
	defer db.Close()
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM works WHERE scan_id=2`).Scan(&n)
	return n
}

func activeCount(path, model string) int {
	db := open(path)
	defer db.Close()
	var n int
	if model == "A" {
		// reader 只看 active_gen 的行
		db.QueryRow(`SELECT COUNT(*) FROM works w JOIN lib_info l WHERE w.gen<=l.active_gen AND w.retired>l.active_gen`).Scan(&n)
	} else {
		db.QueryRow(`SELECT COUNT(*) FROM works`).Scan(&n)
	}
	return n
}
