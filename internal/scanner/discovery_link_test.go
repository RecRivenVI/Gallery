package scanner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/RecRivenVI/gallery/internal/rules"
)

// makeDirectoryLink 在 target 处建立一个指向 destination 的目录链接。Windows 使用
// junction（`mklink /J` 不需要管理员权限，也是真实 Source 中最常见的形式），Unix 使用
// symlink。无法建立时返回 false，由调用方跳过。
func makeDirectoryLink(t *testing.T, target, destination string) bool {
	t.Helper()
	if runtime.GOOS == "windows" {
		command := exec.Command("cmd", "/c", "mklink", "/J", target, destination)
		if output, err := command.CombinedOutput(); err != nil {
			t.Logf("无法建立 junction: %v %s", err, output)
			return false
		}
		return true
	}
	if err := os.Symlink(destination, target); err != nil {
		t.Logf("无法建立 symlink: %v", err)
		return false
	}
	return true
}

// TestDiscoveryTreatsDirectoryLinksAsLinksNotMedia 是链接语义的回归。
//
// Windows junction 报告为 fs.ModeIrregular 且 IsDir() 为 false，因此此前只判断
// fs.ModeSymlink 的实现有两个后果：链接指向的整棵子树被静默跳过，同时该链接本身作为一个
// 0 字节的普通文件混进作品的媒体列表，产生规则和 Catalog 都无法解释的幽灵媒体。
//
// 本测试同时锁定第三条性质：跳过链接不得连带跳过同目录下的兄弟项。
func TestDiscoveryTreatsDirectoryLinksAsLinksNotMedia(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	// 链接目标里放一个符合媒体 glob 的文件：若实现跟随链接，它会作为媒体出现。
	if err := os.WriteFile(filepath.Join(outside, "leaked.bin"), []byte("leaked"), 0o600); err != nil {
		t.Fatal(err)
	}
	workRoot := filepath.Join(root, "work")
	if err := os.MkdirAll(workRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	// 兄弟项刻意排在链接名之后（ReadDir 按名称排序：aaa.bin < link-to-outside < zzz.bin），
	// 使「跳过链接时误用 SkipDir」会真的丢掉 zzz.bin。
	for _, name := range []string{"aaa.bin", "zzz.bin"} {
		if err := os.WriteFile(filepath.Join(workRoot, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if !makeDirectoryLink(t, filepath.Join(workRoot, "link-to-outside"), outside) {
		t.Skip("当前环境无法建立目录链接，跳过链接语义断言")
	}

	compiled, err := rules.CompilePackage([]byte(ruleForDiscovery("*", "", "*", "", "", "")))
	if err != nil {
		t.Fatal(err)
	}
	ir, _, parameters, err := rules.CompileBinding(compiled, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := discover(context.Background(), root, ir, parameters)
	if err != nil {
		t.Fatalf("发现失败: %v", err)
	}
	if len(result.Works) != 1 {
		t.Fatalf("作品数=%d，期望 1: %+v", len(result.Works), result.Works)
	}
	var keys []string
	for _, item := range result.Works[0].Media {
		keys = append(keys, item.SourceKey)
	}
	if len(keys) != 2 || keys[0] != "work/aaa.bin" || keys[1] != "work/zzz.bin" {
		t.Fatalf("链接被当作媒体、或跳过链接时连带丢失兄弟项: %+v", keys)
	}
}

// TestDiscoveryDoesNotDescendIntoLinkedSubtrees 证明链接指向的目录不会被当成作品目录，
// 因此链接目标中的内容不会越过 Source 根进入 Catalog。
func TestDiscoveryDoesNotDescendIntoLinkedSubtrees(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	// 在链接目标下构造一个完全符合作品形状的目录：若实现跟随链接，它会被发现为作品。
	outsideWork := filepath.Join(outside, "outside-work")
	if err := os.MkdirAll(outsideWork, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideWork, "leaked.bin"), []byte("leaked"), 0o600); err != nil {
		t.Fatal(err)
	}
	insideWork := filepath.Join(root, "inside-work")
	if err := os.MkdirAll(insideWork, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(insideWork, "kept.bin"), []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !makeDirectoryLink(t, filepath.Join(root, "linked"), outside) {
		t.Skip("当前环境无法建立目录链接，跳过链接语义断言")
	}

	compiled, err := rules.CompilePackage([]byte(ruleForDiscovery("*", "", "*", "", "", "")))
	if err != nil {
		t.Fatal(err)
	}
	ir, _, parameters, err := rules.CompileBinding(compiled, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := discover(context.Background(), root, ir, parameters)
	if err != nil {
		t.Fatalf("发现失败: %v", err)
	}
	if len(result.Works) != 1 || len(result.Works[0].Media) != 1 || result.Works[0].Media[0].SourceKey != "inside-work/kept.bin" {
		t.Fatalf("链接子树越过 Source 根进入发现结果: %+v", result.Works)
	}
}
