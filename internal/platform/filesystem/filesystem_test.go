package filesystem_test

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
)

func TestIsLinkCoversModeBits(t *testing.T) {
	for _, item := range []struct {
		name string
		mode fs.FileMode
		want bool
	}{
		{"普通文件", 0o644, false},
		{"普通目录", fs.ModeDir | 0o755, false},
		{"Unix symlink", fs.ModeSymlink | 0o777, true},
		{"Windows junction 报告的 irregular", fs.ModeIrregular, true},
		{"目录位与 irregular 同时出现", fs.ModeDir | fs.ModeIrregular, true},
		{"设备文件不算链接", fs.ModeDevice | 0o600, false},
	} {
		if got := filesystem.IsLink(item.mode); got != item.want {
			t.Fatalf("%s: IsLink(%v) = %v want %v", item.name, item.mode, got, item.want)
		}
	}
}

// TestIsLinkDetectsRealPlatformLinks 用真实的平台链接验证判定，而不是只验证 mode 位的
// 组合逻辑。Windows 上建立 junction，Unix 上建立 symlink；两者都必须被判定为链接，而
// 同目录下的普通文件与普通目录都不得被误判。
func TestIsLinkDetectsRealPlatformLinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "plain-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plain.txt"), []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(root, "link-to-outside")
	if runtime.GOOS == "windows" {
		// mklink /J 不需要管理员权限，而 os.Symlink 在未开启开发者模式的 Windows 上会失败。
		// junction 正是本测试要覆盖的对象：它是 Windows 上最常见的目录链接形式。
		command := exec.Command("cmd", "/c", "mklink", "/J", linkPath, outside)
		if output, err := command.CombinedOutput(); err != nil {
			t.Skipf("无法建立 junction，跳过真实链接断言: %v %s", err, output)
		}
	} else if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("无法建立 symlink，跳过真实链接断言: %v", err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var sawLink bool
	for _, entry := range entries {
		isLink := filesystem.IsLink(entry.Type())
		switch entry.Name() {
		case "link-to-outside":
			if !isLink {
				t.Fatalf("真实平台链接未被判定为链接: type=%v isDir=%v", entry.Type(), entry.IsDir())
			}
			sawLink = true
		case "plain-dir", "plain.txt":
			if isLink {
				t.Fatalf("%s 被误判为链接: type=%v", entry.Name(), entry.Type())
			}
		}
	}
	if !sawLink {
		t.Fatal("未在目录项中找到链接")
	}
}
