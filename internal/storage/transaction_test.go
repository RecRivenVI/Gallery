package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestDeferredReadThenWriteTransactionsSurviveConcurrentWriters 复现并锁定一个 WAL 下的
// 真实健壮性缺陷类别。
//
// 缺省的 DEFERRED 事务在第一条 SELECT 时取得读快照，直到第一条写语句才尝试升级为写事务。
// 若在这两步之间另一个连接完成了提交，SQLite 返回 SQLITE_BUSY（快照过期），而
// `busy_timeout` **对这种情况不生效**——再等也没用，该事务的读快照已经不可能再写。应用层
// 若把它当成内部错误，就会在高并发下间歇性地返回 500，而不是稳定、可重试的结构化错误。
//
// 观察到的现象：`TestStage4CorrectnessSmoke` 在 CI 的 race 作业上偶发
// 「配对交换失败 status=500」，而配对交换正是一条 SELECT 后 UPDATE 的 DEFERRED 事务。
//
// 本测试用一个持续写入的并发写者制造该竞态，断言读后写事务不会因此失败。
func TestDeferredReadThenWriteTransactionsSurviveConcurrentWriters(t *testing.T) {
	store, _ := openTestStore(t)
	db := store.Control.SQL()
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `CREATE TABLE _tx_probe (id INTEGER PRIMARY KEY, counter INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO _tx_probe (id, counter) VALUES (1, 0), (2, 0)`); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var writers sync.WaitGroup
	// 两个持续写者不断提交，使读后写事务的读快照有很高概率在升级为写之前过期。
	for worker := 0; worker < 2; worker++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = db.ExecContext(ctx, `UPDATE _tx_probe SET counter = counter + 1 WHERE id = 2`)
			}
		}()
	}
	defer func() {
		close(stop)
		writers.Wait()
	}()

	deadline := time.Now().Add(5 * time.Second)
	var failures []string
	for attempt := 0; attempt < 300 && time.Now().Before(deadline); attempt++ {
		if err := readThenWrite(ctx, db); err != nil {
			failures = append(failures, err.Error())
			if len(failures) > 3 {
				break
			}
		}
	}
	if len(failures) > 0 {
		t.Fatalf("读后写事务在并发写者下失败 %d 次，DEFERRED 事务的过期读快照无法被 busy_timeout 吸收: %s",
			len(failures), strings.Join(failures, " | "))
	}
}

// readThenWrite 执行一次「先读后写」事务，与配对交换、Job 领取、Overlay 写入等生产路径
// 的事务形态一致：先按主键读一行，再基于读到的值写另一行。
func readThenWrite(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务: %w", err)
	}
	defer tx.Rollback()
	var counter int64
	if err := tx.QueryRowContext(ctx, `SELECT counter FROM _tx_probe WHERE id = 1`).Scan(&counter); err != nil {
		return fmt.Errorf("读取: %w", err)
	}
	// 读与写之间的这段时间正是竞态窗口；生产路径里它是校验、哈希或 ID 生成。
	time.Sleep(time.Millisecond)
	if _, err := tx.ExecContext(ctx, `UPDATE _tx_probe SET counter = ? WHERE id = 1`, counter+1); err != nil {
		return fmt.Errorf("写入: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交: %w", err)
	}
	return nil
}
