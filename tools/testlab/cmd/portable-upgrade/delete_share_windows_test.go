package main

import (
	"os"
	"path/filepath"
	"testing"
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
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, rotated); err != nil {
		t.Fatalf("释放句柄后仍不能 Rename: %v", err)
	}
}
