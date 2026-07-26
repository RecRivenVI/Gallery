package fileroot_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/fileroot"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
)

func faultCode(t *testing.T, err error) fault.Code {
	t.Helper()
	var structured *fault.Error
	if !errors.As(err, &structured) {
		t.Fatalf("非结构化错误: %v", err)
	}
	return structured.Code
}

// TestRegistryAllowsFileRootAboveSourcesButRejectsBelow 覆盖本切片最关键的边界决定。
//
// 真实配置里文件根 `F:\Gallery` 正是各平台 Source 的祖先，因此**必须允许**祖先关系——这正是
// 文件根不能注册成普通 Source 的原因（AppDirs 的双向包含判定会拒绝）。反方向则必须拒绝：
// 文件根若是某个 Source 的后代，同一目录树就会既在文件根内又在文件根外，不可解释。
func TestRegistryAllowsFileRootAboveSourcesButRejectsBelow(t *testing.T) {
	base := t.TempDir()
	gallery := filepath.Join(base, "Gallery")
	platform := filepath.Join(gallery, "Galleries", "pixiv")
	nested := filepath.Join(platform, "inner")
	appData := filepath.Join(base, "AppData")
	for _, dir := range []string{platform, nested, appData} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("文件根是来源祖先应被接受", func(t *testing.T) {
		registry, err := fileroot.NewRegistry(filesystem.OS{},
			[]fileroot.Root{{ID: "files", Name: "所有文件", Path: gallery}},
			[]string{appData}, []string{platform})
		if err != nil {
			t.Fatalf("文件根作为来源祖先被拒绝: %v", err)
		}
		if len(registry.List()) != 1 {
			t.Fatalf("登记结果 = %+v", registry.List())
		}
	})

	t.Run("文件根是来源后代应被拒绝", func(t *testing.T) {
		_, err := fileroot.NewRegistry(filesystem.OS{},
			[]fileroot.Root{{ID: "files", Path: nested}},
			[]string{appData}, []string{platform})
		if faultCode(t, err) != fault.CodeSourceRootsOverlap {
			t.Fatalf("文件根位于来源之下未被拒绝: %v", err)
		}
	})

	t.Run("文件根与 AppDirs 重叠应被拒绝", func(t *testing.T) {
		_, err := fileroot.NewRegistry(filesystem.OS{},
			[]fileroot.Root{{ID: "files", Path: base}},
			[]string{appData}, nil)
		if faultCode(t, err) != fault.CodeAppDirsOverlap {
			t.Fatalf("文件根覆盖 AppDirs 未被拒绝: %v", err)
		}
	})

	t.Run("文件根之间重叠应被拒绝", func(t *testing.T) {
		_, err := fileroot.NewRegistry(filesystem.OS{},
			[]fileroot.Root{{ID: "outer", Path: gallery}, {ID: "inner", Path: platform}},
			[]string{appData}, nil)
		if faultCode(t, err) != fault.CodeSourceRootsOverlap {
			t.Fatalf("文件根互相包含未被拒绝: %v", err)
		}
	})

	t.Run("重复标识应被拒绝", func(t *testing.T) {
		_, err := fileroot.NewRegistry(filesystem.OS{},
			[]fileroot.Root{{ID: "files", Path: gallery}, {ID: "files", Path: appData}},
			nil, nil)
		if faultCode(t, err) != fault.CodeConfigInvalid {
			t.Fatalf("重复文件根标识未被拒绝: %v", err)
		}
	})
}

func newBrowseRoot(t *testing.T) (fileroot.Root, string) {
	t.Helper()
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "10-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "2-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"10.txt", "2.txt", "a.txt"} {
		if err := os.WriteFile(filepath.Join(base, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	registry, err := fileroot.NewRegistry(filesystem.OS{},
		[]fileroot.Root{{ID: "files", Name: "所有文件", Path: base}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	root, ok := registry.Lookup("files")
	if !ok {
		t.Fatal("登记后无法按标识取回文件根")
	}
	return root, base
}

// TestListEntriesOrdersDirectoriesFirstInNaturalOrder 锁定浏览顺序：目录在前，同类按自然序，
// 因此 `2` 排在 `10` 之前而不是字典序的相反结果。
func TestListEntriesOrdersDirectoriesFirstInNaturalOrder(t *testing.T) {
	root, _ := newBrowseRoot(t)
	page, err := fileroot.ListEntries(root, "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range page.Entries {
		names = append(names, entry.Name)
	}
	// 目录在前；同类内按 querytext.NaturalSortKey，该编码把文本段前缀为 0、数字段前缀为 1，
	// 因此**文本排在数字之前**，而数字之间是数值序（2 在 10 之前）。这里刻意复用全产品同一套
	// 排序键而不是另造一套贴近资源管理器的顺序——媒体顺序、作品排序与目录浏览必须同源，
	// 否则同一批名字在不同界面会呈现不同顺序。zh-CN 拼音排序仍是已登记的未实现项。
	want := []string{"2-dir", "10-dir", "a.txt", "2.txt", "10.txt"}
	if len(names) != len(want) {
		t.Fatalf("条目数 = %d want %d: %+v", len(names), len(want), names)
	}
	for index := range want {
		if names[index] != want[index] {
			t.Fatalf("顺序错误: %+v want %+v", names, want)
		}
	}
	// 普通文件带大小，目录不带——目录的 0 字节没有意义，不用 0 冒充。
	for _, entry := range page.Entries {
		if entry.Kind == fileroot.EntryKindDirectory && entry.SizeBytes != nil {
			t.Fatalf("目录不应携带大小: %+v", entry)
		}
		if entry.Kind == fileroot.EntryKindFile && entry.SizeBytes == nil {
			t.Fatalf("普通文件应携带大小: %+v", entry)
		}
	}
}

// TestListEntriesPaginatesWithoutGapsOrRepeats 覆盖续页锚点：逐页取完应恰好覆盖全部条目一次。
func TestListEntriesPaginatesWithoutGapsOrRepeats(t *testing.T) {
	root, _ := newBrowseRoot(t)
	seen := make([]string, 0, 5)
	after := ""
	for page := 0; page < 10; page++ {
		result, err := fileroot.ListEntries(root, "", after, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range result.Entries {
			seen = append(seen, entry.Name)
		}
		if result.NextAfter == "" {
			break
		}
		after = result.NextAfter
	}
	// 目录在前；同类内按 querytext.NaturalSortKey，该编码把文本段前缀为 0、数字段前缀为 1，
	// 因此**文本排在数字之前**，而数字之间是数值序（2 在 10 之前）。这里刻意复用全产品同一套
	// 排序键而不是另造一套贴近资源管理器的顺序——媒体顺序、作品排序与目录浏览必须同源，
	// 否则同一批名字在不同界面会呈现不同顺序。zh-CN 拼音排序仍是已登记的未实现项。
	want := []string{"2-dir", "10-dir", "a.txt", "2.txt", "10.txt"}
	if len(seen) != len(want) {
		t.Fatalf("分页遍历结果 = %+v want %+v", seen, want)
	}
	for index := range want {
		if seen[index] != want[index] {
			t.Fatalf("分页顺序错误: %+v want %+v", seen, want)
		}
	}
}

// TestListEntriesRejectsPathEscape 复用媒体读取同一条越界防线。
func TestListEntriesRejectsPathEscape(t *testing.T) {
	root, _ := newBrowseRoot(t)
	for _, relative := range []string{"..", "../outside", "/absolute", "a\\b", "a/../../b"} {
		if _, err := fileroot.ListEntries(root, relative, "", 0); err == nil {
			t.Fatalf("越界相对路径未被拒绝: %q", relative)
		}
	}
}

// TestListEntriesShowsLinksAsDistinctKindWithoutTarget 覆盖调查给出的链接呈现裁决：
// 链接必须作为独立第三态可见（不能并入 file，否则会被显示成 0 字节普通文件——那是错误信息），
// 但不下降、不带大小、不暴露目标（目标是绝对路径，返回它会违反路径不外泄边界）。
func TestListEntriesShowsLinksAsDistinctKindWithoutTarget(t *testing.T) {
	root, base := newBrowseRoot(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(base, "zz-link")
	if runtime.GOOS == "windows" {
		command := exec.Command("cmd", "/c", "mklink", "/J", linkPath, outside)
		if output, err := command.CombinedOutput(); err != nil {
			t.Skipf("无法建立 junction: %v %s", err, output)
		}
	} else if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("无法建立 symlink: %v", err)
	}

	page, err := fileroot.ListEntries(root, "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	var link *fileroot.Entry
	for index := range page.Entries {
		if page.Entries[index].Name == "zz-link" {
			link = &page.Entries[index]
		}
	}
	if link == nil {
		t.Fatal("链接未出现在列举结果中；静默隐藏用户可见的目录项本身就是缺陷")
	}
	if link.Kind != fileroot.EntryKindLink {
		t.Fatalf("链接类型 = %q，必须是独立第三态而不是文件或目录", link.Kind)
	}
	if link.SizeBytes != nil {
		t.Fatalf("链接不应携带大小: %+v", link)
	}
	// 不得下降进链接。
	if _, err := fileroot.ListEntries(root, "zz-link", "", 0); err == nil {
		t.Fatal("下降进链接未被拒绝")
	}
}
