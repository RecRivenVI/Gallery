package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestHoldControlWithoutDeleteSharingBlocksOnlyRename(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "control.db")
	if err := os.WriteFile(path, []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}
	release, err := holdControlWithoutDeleteSharing(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(path); err != nil {
		t.Fatalf("句柄不应阻止读取: %v", err)
	}
	rotated := filepath.Join(root, "control.db.bak")
	if err := os.Rename(path, rotated); err == nil {
		_ = release()
		t.Fatal("未阻止 control.db Rename")
	} else if !isDeleteSharingViolation(err) {
		_ = release()
		t.Fatalf("control.db Rename 未返回 sharing violation: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, rotated); err != nil {
		t.Fatalf("释放句柄后仍不能 Rename: %v", err)
	}
}

func TestDenyCurrentUserDeleteWithACLBlocksOnlyRenameAndRestores(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "control.db")
	if err := os.WriteFile(path, []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}
	restore, err := denyCurrentUserDeleteWithACL(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := restore(); err != nil {
			t.Errorf("恢复 control.db DACL: %v", err)
		}
	})
	if _, err := os.ReadFile(path); err != nil {
		t.Fatalf("ACL 不应阻止读取: %v", err)
	}
	if err := os.WriteFile(path, []byte("updated"), 0o600); err != nil {
		t.Fatalf("ACL 不应阻止写入: %v", err)
	}
	rotated := filepath.Join(root, "control.db.bak")
	if err := os.Rename(path, rotated); err == nil {
		t.Fatal("DELETE deny ACL 未阻止 control.db Rename")
	} else if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("control.db Rename 未返回 access denied: %v", err)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, rotated); err != nil {
		t.Fatalf("恢复 DACL 后仍不能 Rename: %v", err)
	}
}

func TestWatchNextFileWithoutDeleteSharingBlocksRename(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "control.db.incoming")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results, stop, err := watchNextFileWithoutDeleteSharing(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if err := os.WriteFile(path, []byte("restored"), 0o600); err != nil {
		t.Fatal(err)
	}
	var hold pendingFileHold
	select {
	case hold = <-results:
	case <-ctx.Done():
		t.Fatalf("等待恢复候选阻断句柄: %v", ctx.Err())
	}
	if hold.err != nil || hold.release == nil {
		t.Fatalf("恢复候选阻断句柄无效: %v", hold.err)
	}
	landed := filepath.Join(root, "control.db")
	if err := os.Rename(path, landed); err == nil {
		_ = hold.release()
		t.Fatal("未阻止恢复候选 Rename")
	} else if !isDeleteSharingViolation(err) {
		_ = hold.release()
		t.Fatalf("恢复候选 Rename 未返回 sharing violation: %v", err)
	}
	if err := hold.release(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, landed); err != nil {
		t.Fatalf("释放句柄后仍不能落位恢复候选: %v", err)
	}
}

func TestWatchPathMissingThenReopenBlocksRollbackRename(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "control.db")
	rotated := filepath.Join(root, "control.db.pre-restore-test.bak")
	if err := os.WriteFile(current, []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results, stop, err := watchPathMissingThenReopenWithoutDeleteSharing(ctx, current)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if err := os.Rename(current, rotated); err != nil {
		t.Fatal(err)
	}
	var hold pendingFileHold
	select {
	case hold = <-results:
	case <-ctx.Done():
		t.Fatalf("等待旧库回滚阻断句柄: %v", ctx.Err())
	}
	if hold.err != nil || hold.release == nil {
		t.Fatalf("旧库回滚阻断句柄无效: %v", hold.err)
	}
	if err := os.Rename(rotated, current); err == nil {
		_ = hold.release()
		t.Fatal("未阻止旧库回滚 Rename")
	} else if !isDeleteSharingViolation(err) {
		_ = hold.release()
		t.Fatalf("旧库回滚 Rename 未返回 sharing violation: %v", err)
	}
	if err := hold.release(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(rotated, current); err != nil {
		t.Fatalf("释放句柄后仍不能回滚旧库: %v", err)
	}
}

func TestWatchObservedFileReplacementDoesNotBlockAtomicRename(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "restore-pending.json")
	if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done, err := watchObservedFileReplacement(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	temporary := filepath.Join(root, ".restore-pending.json-test")
	if err := os.WriteFile(temporary, []byte("after"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, path); err != nil {
		t.Fatalf("状态观察器阻断了原子替换: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "after" {
		t.Fatalf("状态观察完成后未落位新内容: data=%q err=%v", data, err)
	}
}
