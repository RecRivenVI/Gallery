package toolrunner

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"runtime"
	"sync"
	"testing"
)

// TestDigestWriterEnforcesLimitAndFiresOverflowOnce 直接把每流输出上限打满：正好等于上限的
// 写入必须被接受，再多一个字节必须被拒绝且不改变累计量，溢出回调只触发一次。
func TestDigestWriterEnforcesLimitAndFiresOverflowOnce(t *testing.T) {
	const limit = 1024
	var fired int
	writer := &digestWriter{limit: limit, sum: sha256.New(), onOverflow: func() { fired++ }}

	payload := bytes.Repeat([]byte("g"), limit)
	if n, err := writer.Write(payload); n != limit || err != nil {
		t.Fatalf("恰好写满上限被拒绝: n=%d err=%v", n, err)
	}
	if writer.overflowed() || fired != 0 {
		t.Fatalf("未溢出却报告溢出: overflow=%v fired=%d", writer.overflowed(), fired)
	}
	for i := 0; i < 3; i++ {
		n, err := writer.Write([]byte("!"))
		if n != 0 || !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("越界写入未被拒绝: n=%d err=%v", n, err)
		}
	}
	if !writer.overflowed() {
		t.Fatal("越界写入之后 overflow 仍为假")
	}
	if fired != 1 {
		t.Fatalf("溢出回调触发 %d 次，期望恰好 1 次", fired)
	}
	if writer.total() != limit {
		t.Fatalf("越界写入改变了累计量: %d", writer.total())
	}
	expected := sha256.Sum256(payload)
	if writer.digest() != hex.EncodeToString(expected[:]) {
		t.Fatal("摘要与已接受字节不一致")
	}
}

// TestDigestWriterKeepsMemoryConstant 证明内存是 O(1)：写入 16 MiB 期间的累计分配远小于
// 数据体量，说明 digestWriter 只把数据喂给 sha256 而没有缓冲。
func TestDigestWriterKeepsMemoryConstant(t *testing.T) {
	const total = 16 << 20
	const chunk = 32 << 10
	writer := &digestWriter{limit: total, sum: sha256.New()}
	payload := bytes.Repeat([]byte("g"), chunk)

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for written := 0; written < total; written += chunk {
		if _, err := writer.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > total/4 {
		t.Fatalf("写入 %d 字节期间分配了 %d 字节，digestWriter 疑似在缓冲输出", total, allocated)
	}
	if writer.total() != total || writer.overflowed() {
		t.Fatalf("累计量或溢出标记不正确: n=%d overflow=%v", writer.total(), writer.overflowed())
	}
}

// TestKillSwitchKillsExactlyOnceWhenOverflowPrecedesAttach 覆盖溢出早于 attach 的补发路径，
// 以及并发触发下 Kill 只发生一次的约束。
func TestKillSwitchKillsExactlyOnceWhenOverflowPrecedesAttach(t *testing.T) {
	var cancelled int
	killer := &killSwitch{}
	killer.arm(func() { cancelled++ })

	killer.trigger()
	killer.trigger()
	if cancelled == 0 {
		t.Fatal("尚未 attach 进程时也必须取消运行 context")
	}

	recorder := &countingProcess{}
	killer.attach(recorder)
	if recorder.count() != 1 {
		t.Fatalf("attach 之后补发的 Kill 次数 = %d，期望 1 次", recorder.count())
	}
	killer.trigger()
	if recorder.count() != 1 {
		t.Fatalf("重复触发导致 Kill 次数 = %d", recorder.count())
	}
}

// TestKillSwitchIsSafeUnderConcurrentTriggers 覆盖 stdout/stderr 同时溢出的并发形态。
func TestKillSwitchIsSafeUnderConcurrentTriggers(t *testing.T) {
	killer := &killSwitch{}
	killer.arm(func() {})
	recorder := &countingProcess{}
	killer.attach(recorder)

	var group sync.WaitGroup
	for i := 0; i < 16; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			killer.trigger()
		}()
	}
	group.Wait()
	if recorder.count() != 1 {
		t.Fatalf("并发触发下 Kill 次数 = %d，期望 1 次", recorder.count())
	}
}

type countingProcess struct {
	mu     sync.Mutex
	killed int
}

func (p *countingProcess) Wait() error { return nil }

func (p *countingProcess) Kill() error {
	p.mu.Lock()
	p.killed++
	p.mu.Unlock()
	return nil
}

func (p *countingProcess) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killed
}
