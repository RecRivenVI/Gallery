package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
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
