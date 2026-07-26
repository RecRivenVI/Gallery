package sourceguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// makeDirectoryLink 建立一个指向目录的平台链接，并返回是否成功建立。
//
// Windows 上刻意使用 `mklink /J`（junction）而不是 os.Symlink：目录 symlink 需要
// SeCreateSymbolicLinkPrivilege 或开发者模式，普通用户会话通常没有；junction 不需要
// 特权，而且它正是真实 Source 中实际出现的形态（EV-46 `LINK-1` 与本轮 GUARD-1 都由
// junction 触发）。
func makeDirectoryLink(t *testing.T, link, target string) bool {
	t.Helper()
	if runtime.GOOS == "windows" {
		output, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
		if err != nil {
			t.Logf("mklink /J 不可用，跳过链接用例: %v: %s", err, strings.TrimSpace(string(output)))
			return false
		}
		return true
	}
	if err := os.Symlink(target, link); err != nil {
		t.Logf("os.Symlink 不可用，跳过链接用例: %v", err)
		return false
	}
	return true
}

func writeTree(t *testing.T, root string, relativePaths ...string) {
	t.Helper()
	for _, relative := range relativePaths {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(relative), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestWalkTraversesLinkRoot 是 GUARD-1 的核心回归：把 junction/symlink 目录本身当作
// guard 根传入时，必须跟随并完整遍历。
//
// 缺陷形态：根用 Lstat 判定时，Windows junction 报告为 fs.ModeIrregular 且
// IsDir()=false，filepath.Walk 只产出根自身一条，清单恒为 fileCount=0/dirCount=0。
// 空清单与空清单自比必然相等，于是 verify 打印 PASS 却什么都没有守护——数据量最大的
// 两个 HDD 平台恰好都是 junction 根。
func TestWalkTraversesLinkRoot(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	writeTree(t, target, "authorA/work1/a.jpg", "authorA/work1/metadata.json", "authorB/work2/b.jpg")

	link := filepath.Join(base, "link-root")
	if !makeDirectoryLink(t, link, target) {
		t.Skip("本环境无法建立目录链接（Windows 需要 mklink /J，Unix 需要 symlink 权限），跳过 junction 根用例")
	}

	direct, err := Walk(target)
	if err != nil {
		t.Fatalf("普通目录根遍历失败: %v", err)
	}
	viaLink, err := Walk(link)
	if err != nil {
		t.Fatalf("链接根遍历失败（GUARD-1 复发：根必须按 os.Stat 跟随判定）: %v", err)
	}
	if viaLink.IsEmpty() {
		t.Fatal("链接根产出空清单：guard 完全空转")
	}
	if viaLink.FileCount != direct.FileCount || viaLink.DirCount != direct.DirCount ||
		viaLink.TotalBytes != direct.TotalBytes || viaLink.GuardSHA256 != direct.GuardSHA256 {
		t.Fatalf("经链接根遍历的结果与直接遍历目标不一致: link=%+v direct=%+v",
			struct{ F, D int }{viaLink.FileCount, viaLink.DirCount},
			struct{ F, D int }{direct.FileCount, direct.DirCount})
	}
}

// TestWalkRecordsInnerLinkWithoutFollowing 锁定子树内部链接的两条性质：不递归（保持
// `LINK-1` 的既有裁决），但必须作为一条独立条目进入清单——否则「链接被替换成真实
// 目录」这类改动会完全逃过 guard。
func TestWalkRecordsInnerLinkWithoutFollowing(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	writeTree(t, outside, "hidden1.jpg", "nested/hidden2.jpg", "nested/hidden3.jpg")

	root := filepath.Join(base, "root")
	writeTree(t, root, "authorA/work1/a.jpg")
	link := filepath.Join(root, "linked")
	if !makeDirectoryLink(t, link, outside) {
		t.Skip("本环境无法建立目录链接，跳过内部链接用例")
	}

	manifest, err := Walk(root)
	if err != nil {
		t.Fatalf("遍历失败: %v", err)
	}
	if manifest.LinkCount != 1 {
		t.Fatalf("linkCount = %d want 1（内部链接必须计入清单）", manifest.LinkCount)
	}
	if manifest.FileCount != 1 {
		t.Fatalf("fileCount = %d want 1（不得跟随链接把目标子树计进来）", manifest.FileCount)
	}
	var linkEntries int
	for _, entry := range manifest.Entries {
		if entry.Kind == KindLink {
			linkEntries++
			if entry.RelativePath != "linked" {
				t.Fatalf("链接条目相对路径 = %q want \"linked\"", entry.RelativePath)
			}
		}
		if strings.Contains(entry.RelativePath, "hidden") {
			t.Fatalf("链接目标子树被跟随进入清单: %q", entry.RelativePath)
		}
	}
	if linkEntries != 1 {
		t.Fatalf("清单中的链接条目数 = %d want 1", linkEntries)
	}
}

// TestWalkDetectsLinkReplacedByDirectory 证明「计入链接条目」确实换来了检测能力：把
// 链接替换成同名真实目录后，guard 摘要必须改变。
func TestWalkDetectsLinkReplacedByDirectory(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	writeTree(t, outside, "x.jpg")

	root := filepath.Join(base, "root")
	writeTree(t, root, "authorA/work1/a.jpg")
	link := filepath.Join(root, "swapped")
	if !makeDirectoryLink(t, link, outside) {
		t.Skip("本环境无法建立目录链接，跳过链接替换用例")
	}

	before, err := Walk(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	writeTree(t, root, "swapped/x.jpg")

	after, err := Walk(root)
	if err != nil {
		t.Fatal(err)
	}
	if before.Equal(after) {
		t.Fatal("链接被替换为真实目录后 guard 仍判定一致：该改动逃过了 guard")
	}
}

// TestWalkIsRepeatableOnUnchangedTree 锁定「未被修改的树连续遍历两次必须完全相等」。
//
// 这不是一句显然的性质：Windows 上 os.ReadDir 返回的 DirEntry.Info() 取自**父目录的
// 目录项缓存**，子目录 mtime 在那里惰性刷新，同一棵树连续两次遍历会得到不同的目录
// mtime，让 guard 在没有任何写入时报告不一致（假阳性，同样会毁掉 guard 的可信度）。
// 遍历改用 os.Lstat 直接查询对象本身即可稳定。
func TestWalkIsRepeatableOnUnchangedTree(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, "authorA/work1/a.jpg", "authorA/work2/b.jpg", "authorB/work3/c.jpg")
	first, err := Walk(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Walk(root)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Equal(second) {
		t.Fatalf("未修改的树连续两次遍历不相等: first=%s second=%s", first.GuardSHA256, second.GuardSHA256)
	}
}

// TestWalkRejectsEmptyManifest 是防止本类缺陷静默复发的兜底：任何产出 0 文件、0 目录、
// 0 链接的遍历都必须失败，而不是返回一个「与自己相等」的空基线。
func TestWalkRejectsEmptyManifest(t *testing.T) {
	if _, err := Walk(t.TempDir()); err == nil {
		t.Fatal("空目录必须判失败：空清单会把「未验证」伪装成「已验证」")
	}
}

// TestWalkRejectsNonDirectoryRoot 保证根是文件时立即失败，而不是产出空清单。
func TestWalkRejectsNonDirectoryRoot(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Walk(file); err == nil {
		t.Fatal("文件根必须判失败")
	}
}

// TestSaveManifestRejectsEmptyManifest 保证空清单不会被写成可复用的基线文件。
func TestSaveManifestRejectsEmptyManifest(t *testing.T) {
	if err := SaveManifest(Manifest{}, filepath.Join(t.TempDir(), "m.json")); err == nil {
		t.Fatal("空清单必须拒绝落盘")
	}
}
