package lock_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/platform/lock"
)

const (
	helperPathEnv = "GALLERY_LOCK_HELPER_PATH"
	helperReady   = "GALLERY_LOCK_HELPER_READY"
)

func TestAcquireContendReleaseReacquire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app", "galleryd.lock")
	first, err := lock.Acquire(path)
	if err != nil {
		t.Fatalf("首个实例取锁失败: %v", err)
	}
	if _, err := lock.Acquire(path); !errors.Is(err, lock.ErrAlreadyLocked) {
		t.Fatalf("第二个实例应因已锁定失败: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("释放失败: %v", err)
	}
	second, err := lock.Acquire(path)
	if err != nil {
		t.Fatalf("释放后无法重新取锁: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentDifferentPathsSucceed(t *testing.T) {
	root := t.TempDir()
	a, err := lock.Acquire(filepath.Join(root, "a", "galleryd.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	b, err := lock.Acquire(filepath.Join(root, "b", "galleryd.lock"))
	if err != nil {
		t.Fatalf("不同 AppDirs 应能同时锁定: %v", err)
	}
	defer b.Release()
}

func TestLeftoverLockFileDoesNotBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "galleryd.lock")
	// 预先放置一个遗留的普通锁文件，不持有任何锁。
	if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	handle, err := lock.Acquire(path)
	if err != nil {
		t.Fatalf("遗留锁文件不应阻止取锁: %v", err)
	}
	if err := handle.Release(); err != nil {
		t.Fatal(err)
	}
}

// TestCrashReleasesLock 通过子进程验证操作系统在进程强杀后自动释放锁：helper 取得锁并
// 报告就绪，父进程强杀它，然后父进程应能重新取锁。
func TestCrashReleasesLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "galleryd.lock")
	readyPath := filepath.Join(dir, "ready")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestLockHelperProcess$")
	command.Env = append(os.Environ(), helperPathEnv+"="+path, helperReady+"="+readyPath)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = command.Process.Kill()
			_ = command.Wait()
			t.Fatal("helper 未在期限内取得锁")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// helper 已持锁；此刻父进程取锁必须失败。
	if _, err := lock.Acquire(path); !errors.Is(err, lock.ErrAlreadyLocked) {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("helper 持锁时父进程仍取锁成功: %v", err)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	// 强杀后操作系统应已释放锁，父进程可重新取得。
	acquired := false
	for attempt := 0; attempt < 100; attempt++ {
		handle, err := lock.Acquire(path)
		if err == nil {
			_ = handle.Release()
			acquired = true
			break
		}
		if !errors.Is(err, lock.ErrAlreadyLocked) {
			t.Fatalf("强杀后取锁返回非预期错误: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !acquired {
		t.Fatal("helper 强杀后父进程仍无法取锁")
	}
}

// TestLockHelperProcess 是 TestCrashReleasesLock 的子进程 helper，仅在设置了 helper 环境变量
// 时运行：它取得锁、报告就绪，然后长时间阻塞等待被强杀。
func TestLockHelperProcess(t *testing.T) {
	path := os.Getenv(helperPathEnv)
	readyPath := os.Getenv(helperReady)
	if path == "" || readyPath == "" {
		t.Skip("非 helper 调用")
	}
	handle, err := lock.Acquire(path)
	if err != nil {
		t.Fatalf("helper 取锁失败: %v", err)
	}
	defer handle.Release()
	if err := os.WriteFile(readyPath, []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Second)
}
