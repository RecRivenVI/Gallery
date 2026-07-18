package media_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/media"
)

func TestFullHashDetectsContentChangedAndPathEscape(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "media.bin")
	if err := os.WriteFile(file, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := media.HashSourceFile(root, "media.bin", nil)
	if err != nil || result.Blob.Digest != "6db7d803e74f1ffa7d8f5adc0bf95b3e15bf4c8373fffadf546227cc6c6742cb" {
		t.Fatalf("完整 SHA-256 错误: %+v %v", result, err)
	}
	_, err = media.HashSourceFile(root, "media.bin", func() {
		_ = os.WriteFile(file, []byte("changed-size"), 0o600)
	})
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeContentChangedDuringHash {
		t.Fatalf("哈希期间变化错误 = %v", err)
	}
	original := []byte("same-size-original")
	if err := os.WriteFile(file, original, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	var replacementErr error
	_, err = media.HashSourceFile(root, "media.bin", func() {
		replacement := filepath.Join(root, "replacement.bin")
		if replacementErr = os.WriteFile(replacement, []byte("same-size-replace!"), 0o600); replacementErr != nil {
			return
		}
		if replacementErr = os.Chtimes(replacement, info.ModTime(), info.ModTime()); replacementErr != nil {
			return
		}
		if replacementErr = os.Remove(file); replacementErr != nil {
			return
		}
		replacementErr = os.Rename(replacement, file)
	})
	if replacementErr != nil && runtime.GOOS != "windows" {
		t.Fatalf("测试未能替换打开中的路径: %v", replacementErr)
	}
	if replacementErr == nil && (!errors.As(err, &structured) || structured.Code != fault.CodeContentChangedDuringHash) {
		t.Fatalf("哈希期间同大小同 mtime 路径替换错误 = %v", err)
	}
	if replacementErr != nil && err != nil {
		t.Fatalf("Windows 句柄阻止路径替换时哈希意外失败: %v", err)
	}
	for _, path := range []string{"../outside", "/absolute", `C:\outside`, "trailing. "} {
		if _, err := media.ValidateRelativePath(path); !errors.As(err, &structured) || structured.Code != fault.CodePathEscape {
			t.Fatalf("危险相对路径 %q 未拒绝: %v", path, err)
		}
	}
}
