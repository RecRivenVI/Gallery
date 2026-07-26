package sourceguard

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/tools/testlab/internal/bounds"
)

func TestCensusCompletesWithinGenerousBounds(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root,
		"authorA/work1/a.jpg", "authorA/work1/metadata.json",
		"authorA/work2/b.jpg", "authorB/work3/c.jpg")

	census, err := TakeCensus(root, bounds.Limits{MaxDirs: 100, MaxFiles: 100, MaxWallClock: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if !census.Outcome.Completed {
		t.Fatalf("宽松边界下应完整跑完: %+v", census.Outcome)
	}
	if census.Outcome.Files != 4 || census.Outcome.Dirs != 5 {
		t.Fatalf("census = %+v want files=4 dirs=5", census.Outcome)
	}
	if census.TopLevelDirs != 2 {
		t.Fatalf("topLevelDirs = %d want 2", census.TopLevelDirs)
	}
	if census.MaxDepth < 3 {
		t.Fatalf("maxDepth = %d want >=3", census.MaxDepth)
	}
}

// TestCensusReportsStoppedByBound 是「有界模式」的核心性质：触顶必须报告为「因边界停止」，
// 而不是当成跑完了。把后者说成前者会让一份只覆盖几百个目录的运行看起来覆盖了整个来源。
func TestCensusReportsStoppedByBound(t *testing.T) {
	root := t.TempDir()
	for _, creator := range []string{"a", "b", "c", "d"} {
		writeTree(t, root, creator+"/w1/1.jpg", creator+"/w1/2.jpg", creator+"/w2/1.jpg")
	}

	byFiles, err := TakeCensus(root, bounds.Limits{MaxFiles: 3})
	if err != nil {
		t.Fatal(err)
	}
	if byFiles.Outcome.Completed || byFiles.Outcome.Reason != bounds.ReasonMaxFiles {
		t.Fatalf("文件数触顶未被报告: %+v", byFiles.Outcome)
	}
	if byFiles.Outcome.Files != 3 {
		t.Fatalf("files = %d want 3", byFiles.Outcome.Files)
	}

	byDirs, err := TakeCensus(root, bounds.Limits{MaxDirs: 2})
	if err != nil {
		t.Fatal(err)
	}
	if byDirs.Outcome.Completed || byDirs.Outcome.Reason != bounds.ReasonMaxDirs {
		t.Fatalf("目录数触顶未被报告: %+v", byDirs.Outcome)
	}
}

// TestCensusCountsLinksWithoutFollowing 保证 census 与 Walk 对链接的处理一致：计数但不下降。
func TestCensusCountsLinksWithoutFollowing(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	writeTree(t, outside, "deep/1.jpg", "deep/2.jpg", "deep/3.jpg")

	root := filepath.Join(base, "root")
	writeTree(t, root, "authorA/work1/a.jpg")
	if !makeDirectoryLink(t, filepath.Join(root, "linked"), outside) {
		t.Skip("本环境无法建立目录链接，跳过链接用例")
	}

	census, err := TakeCensus(root, bounds.Limits{MaxDirs: 100, MaxFiles: 100})
	if err != nil {
		t.Fatal(err)
	}
	if census.Links != 1 {
		t.Fatalf("links = %d want 1", census.Links)
	}
	if census.Outcome.Files != 1 {
		t.Fatalf("files = %d want 1（不得跟随链接）", census.Outcome.Files)
	}
	if census.TopLevelDirs != 1 {
		t.Fatalf("topLevelDirs = %d want 1（链接不算普通目录）", census.TopLevelDirs)
	}
}

func TestCensusRejectsEmptyRoot(t *testing.T) {
	if _, err := TakeCensus(t.TempDir(), bounds.Limits{MaxFiles: 10}); err == nil {
		t.Fatal("空根必须判失败")
	}
}
