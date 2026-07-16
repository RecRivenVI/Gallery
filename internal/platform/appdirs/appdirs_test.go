package appdirs_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
)

func TestValidateBeforeEnsureKeepsSourceReadOnly(t *testing.T) {
	root := t.TempDir()
	dirs := appdirs.UnderRoot(filepath.Join(root, "app"))
	source := filepath.Join(dirs.Data, "media")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	err := dirs.ValidateDisjoint(filesystem.OS{}, []string{source})
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeAppDirsOverlap {
		t.Fatalf("重叠错误 = %v", err)
	}
	if _, err := os.Stat(dirs.Config); !os.IsNotExist(err) {
		t.Fatal("重叠校验失败前已经创建了写入目录")
	}
}

func TestDisjointDirsCanBeCreated(t *testing.T) {
	root := t.TempDir()
	dirs := appdirs.UnderRoot(filepath.Join(root, "app"))
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	fs := filesystem.OS{}
	if err := dirs.ValidateDisjoint(fs, []string{source}); err != nil {
		t.Fatal(err)
	}
	if err := dirs.Ensure(fs); err != nil {
		t.Fatal(err)
	}
	for _, path := range dirs.WriteRoots() {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			t.Fatalf("AppDir 未创建: %v", err)
		}
	}
}

func TestOverlappingSourcesAreRejected(t *testing.T) {
	root := t.TempDir()
	dirs := appdirs.UnderRoot(filepath.Join(root, "app"))
	source := filepath.Join(root, "source")
	nested := filepath.Join(source, "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	err := dirs.ValidateDisjoint(filesystem.OS{}, []string{source, nested})
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeSourceRootsOverlap {
		t.Fatalf("Source 重叠错误 = %v", err)
	}
}
