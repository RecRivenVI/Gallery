package descriptor_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/RecRivenVI/gallery/internal/platform/descriptor"
)

func TestDescriptorIsRemovedOnlyByOwner(t *testing.T) {
	dir := t.TempDir()
	value, err := descriptor.New("127.0.0.1:12345")
	if err != nil {
		t.Fatal(err)
	}
	path, err := descriptor.Publish(dir, value)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != dir {
		t.Fatal("descriptor 写到 Runtime 之外")
	}
	if err := descriptor.RemoveIfOwned(path, "wrong"); err == nil {
		t.Fatal("非 owner 删除了 descriptor")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("descriptor 被错误删除")
	}
	if err := descriptor.RemoveIfOwned(path, value.StartupNonce); err != nil {
		t.Fatal(err)
	}
}

func TestDescriptorPublishReplacesStaleOwner(t *testing.T) {
	dir := t.TempDir()
	stale, err := descriptor.New("127.0.0.1:41000")
	if err != nil {
		t.Fatal(err)
	}
	path, err := descriptor.Publish(dir, stale)
	if err != nil {
		t.Fatal(err)
	}
	current, err := descriptor.New("127.0.0.1:41001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := descriptor.Publish(dir, current); err != nil {
		t.Fatalf("陈旧 descriptor 阻止同一 AppDirs 重启发布: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored descriptor.Descriptor
	if err := json.Unmarshal(content, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Address != current.Address || stored.StartupNonce != current.StartupNonce {
		t.Fatalf("重启 descriptor 未替换为当前所有者: %+v", stored)
	}
	if err := descriptor.RemoveIfOwned(path, stale.StartupNonce); err == nil {
		t.Fatal("旧进程 nonce 不应能删除新 descriptor")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("旧进程清理误删新 descriptor: %v", err)
	}
}
