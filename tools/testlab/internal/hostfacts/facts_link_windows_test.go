//go:build windows

package hostfacts

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestStorageResolvesThroughDirectoryJunction 锁定"介质判定必须走到物理盘、不看盘符"
// 这条要求在目录联接（junction）下成立。
//
// 背景：本仓库已经实测到逻辑路径看似落在某个盘、实际经 junction 落在另一块物理盘的
// 情形——一次人工核对里，`C:\...\junction-to-d` 解析后落在 PhysicalDrive2（D: 所在盘，
// 型号 CT2000T500SSD8），而真正的 C: 是 PhysicalDrive0（CT1000T500SSD8）。按盘符判定
// 会给出另一块盘的型号与介质，进而让整份性能报告的存储事实是错的。
//
// 本测试只能做机器无关的那一半：junction 与其目标必须解析到**同一个卷、同一块物理盘、
// 同一种介质**。跨物理盘的那一半需要机器上真的有两块盘，不能作为通用测试的前提；它由
// 上述人工核对提供证据。即便如此，本测试仍然锁住了最容易坏的部分——路径解析一旦回退成
// "取输入字符串的盘符"，junction 与目标的结论就会开始漂移。
func TestStorageResolvesThroughDirectoryJunction(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	// 目录联接不需要管理员权限（符号链接才需要）；仍然可能被组策略或文件系统限制，
	// 那种情况下跳过而不是伪造一个通过。
	if output, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("无法创建目录联接，跳过: %v: %s", err, output)
	}

	viaLink := collectStorage(link)
	direct := collectStorage(target)

	if viaLink.Medium != direct.Medium {
		t.Errorf("经 junction 判定的介质 %q 与目标 %q 不一致（errors: link=%v target=%v）",
			viaLink.Medium, direct.Medium, viaLink.Errors, direct.Errors)
	}
	if viaLink.VolumeID != direct.VolumeID {
		t.Errorf("经 junction 解析到的卷 %q 与目标 %q 不一致", viaLink.VolumeID, direct.VolumeID)
	}
	if viaLink.Model != direct.Model {
		t.Errorf("经 junction 判定的物理盘型号 %q 与目标 %q 不一致", viaLink.Model, direct.Model)
	}
	if len(viaLink.PhysicalDiskNumbers) != len(direct.PhysicalDiskNumbers) {
		t.Fatalf("经 junction 得到的物理盘数 %v 与目标 %v 不一致", viaLink.PhysicalDiskNumbers, direct.PhysicalDiskNumbers)
	}
	for i := range viaLink.PhysicalDiskNumbers {
		if viaLink.PhysicalDiskNumbers[i] != direct.PhysicalDiskNumbers[i] {
			t.Fatalf("经 junction 得到的物理盘编号 %v 与目标 %v 不一致", viaLink.PhysicalDiskNumbers, direct.PhysicalDiskNumbers)
		}
	}
	// 解析确实走到了最终路径：PhysicalDrive 由 GetFinalPathNameByHandle 的结果决定，
	// 不是把输入路径的盘符抄一遍。
	if viaLink.PhysicalDrive == "" {
		t.Errorf("经 junction 未能解析出最终盘符: errors=%v", viaLink.Errors)
	}
}

// TestCombineMediumsRefusesToFoldMixedMedia 复核跨盘卷的合并规则：只要有一块盘判不出
// 来，或者两块盘介质不同，整卷就必须是 unknown。把 SSD+HDD 折叠成其中一种会让报告声称
// 一个并不存在的均质存储。
func TestCombineMediumsRefusesToFoldMixedMedia(t *testing.T) {
	cases := []struct {
		name  string
		input []string
		want  string
	}{
		{"empty", nil, MediumUnknown},
		{"uniform-ssd", []string{MediumSSD, MediumSSD}, MediumSSD},
		{"uniform-hdd", []string{MediumHDD}, MediumHDD},
		{"mixed", []string{MediumSSD, MediumHDD}, MediumUnknown},
		{"partial-unknown", []string{MediumSSD, MediumUnknown}, MediumUnknown},
	}
	for _, test := range cases {
		if got := combineMediums(test.input); got != test.want {
			t.Errorf("combineMediums(%v) = %q, want %q", test.input, got, test.want)
		}
	}
}
