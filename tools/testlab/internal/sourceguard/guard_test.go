package sourceguard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGuardAroundPassesWhenOperationDoesNotWrite(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, "authorA/work1/a.jpg", "authorA/work1/metadata.json")
	guard, err := NewGuard(root, "p-test", Options{})
	if err != nil {
		t.Fatal(err)
	}
	readOnly := func() error {
		_, err := os.ReadFile(filepath.Join(root, "authorA", "work1", "a.jpg"))
		return err
	}
	if err := guard.Around("read", readOnly); err != nil {
		t.Fatalf("只读操作不应触发 guard: %v", err)
	}
	if len(guard.Checks()) != 2 {
		t.Fatalf("Around 必须留下前后两条阶段校验，实际 %d 条", len(guard.Checks()))
	}
	for _, check := range guard.Checks() {
		if !check.OK {
			t.Fatalf("阶段 %s 未通过: %s", check.Stage, check.Summary())
		}
	}
}

// TestGuardAroundDetectsWrite 是 guard 存在的理由：被包住的操作一旦写了 Source，必须失败。
func TestGuardAroundDetectsWrite(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, "authorA/work1/a.jpg")
	guard, err := NewGuard(root, "p-test", Options{})
	if err != nil {
		t.Fatal(err)
	}
	err = guard.Around("writes", func() error {
		return os.WriteFile(filepath.Join(root, "authorA", "work1", "injected.jpg"), []byte("x"), 0o644)
	})
	if err == nil {
		t.Fatal("向 Source 写入必须被 guard 检出")
	}
	last := guard.Checks()[len(guard.Checks())-1]
	if last.OK || last.Added == 0 {
		t.Fatalf("最后一条阶段校验应记录新增条目: %s", last.Summary())
	}
}

// TestGuardComparesAgainstOriginalBaseline 锁定「所有阶段都对照最初那份清单」：若每次校验
// 都刷新基线，中途某一步的写入会被后续基线掩盖，只留下一条早已通过的历史结论。
func TestGuardComparesAgainstOriginalBaseline(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, "authorA/work1/a.jpg")
	guard, err := NewGuard(root, "p-test", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "authorA", "work1", "injected.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Verify("first"); err == nil {
		t.Fatal("第一次校验应检出写入")
	}
	if _, err := guard.Verify("second"); err == nil {
		t.Fatal("第二次校验仍必须检出同一处写入，基线不得被刷新")
	}
}

func TestNewGuardRejectsEmptyRoot(t *testing.T) {
	if _, err := NewGuard(t.TempDir(), "p-test", Options{}); err == nil {
		t.Fatal("空根必须拒绝建立 guard 基线")
	}
}

// TestGuardSurfacesHashBound 保证有界内容哈希会在阶段结论里留下痕迹，读者不会把
// 「只哈希了前 N 个文件」误读为「已全量校验内容」。
func TestGuardSurfacesHashBound(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, "a/1.bin", "a/2.bin", "a/3.bin")
	guard, err := NewGuard(root, "p-test", Options{HashContent: true, MaxHashFiles: 1})
	if err != nil {
		t.Fatal(err)
	}
	if guard.Baseline().HashStopReason == "" {
		t.Fatal("基线未记录内容哈希因边界停止")
	}
	check, err := guard.Verify("stage")
	if err != nil {
		t.Fatal(err)
	}
	if check.HashStopReason == "" || check.HashedFileCount != 1 {
		t.Fatalf("阶段结论未体现有界哈希: %s", check.Summary())
	}
}
