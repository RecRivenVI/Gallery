package descriptor_test

import (
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
