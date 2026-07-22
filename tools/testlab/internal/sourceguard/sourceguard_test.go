package sourceguard

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSelectBoundedSubdirectorySkipsLeafWorkDirectories 覆盖真实来源常见的
// "root -> 单一归类目录 -> 大量作者 -> 作品" 三层结构：根目录只有一个直接子目录，
// 其递归文件数远超 maxFiles，必须继续下探到作者层，而不是把这个单一归类目录本身
// 或某个恰好文件数落在区间内但本身是叶子（work）目录的候选当作结果——叶子目录
// 下面没有可供 author_work 规则 work_directory glob 命中的子目录。
func TestSelectBoundedSubdirectorySkipsLeafWorkDirectories(t *testing.T) {
	root := t.TempDir()
	// 归类目录 bucket 下有一个恰好文件数在区间内、但本身是叶子的 "work-lookalike"
	// 目录，以及一个真正的、更深一层才达标的作者目录 authorA。
	for i := 0; i < 15; i++ {
		writeFile(t, filepath.Join(root, "bucket", "work-lookalike", "file"+itoa(i)))
	}
	for i := 0; i < 15; i++ {
		writeFile(t, filepath.Join(root, "bucket", "authorA", "work1", "file"+itoa(i)))
	}

	selected, count, err := SelectBoundedSubdirectory(root, 10, 20, 40)
	if err != nil {
		t.Fatalf("SelectBoundedSubdirectory failed: %v", err)
	}
	if filepath.Base(selected) != "authorA" {
		t.Fatalf("selected %q, want the authorA directory (which still has work subdirectories), not a leaf work-lookalike directory", selected)
	}
	if count != 15 {
		t.Fatalf("count = %d, want 15", count)
	}
	entries, err := os.ReadDir(selected)
	if err != nil || len(entries) == 0 {
		t.Fatalf("selected directory %q must still contain subdirectories usable as work_directory glob targets", selected)
	}
}

func TestSelectBoundedSubdirectoryReturnsErrorWhenNothingFits(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "only-child", "toolarge", "file1"))
	writeFile(t, filepath.Join(root, "only-child", "toolarge", "file2"))
	_, _, err := SelectBoundedSubdirectory(root, 100, 200, 40)
	if err == nil {
		t.Fatal("expected an error when no candidate at any depth satisfies the bounds")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
