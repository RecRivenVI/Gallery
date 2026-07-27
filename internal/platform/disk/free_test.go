package disk

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/RecRivenVI/gallery/internal/ports"
)

// OS 是 GC/VACUUM、control 备份恢复、Catalog staging 与外部工具输出的空间前置闸的唯一
// 事实来源（internal/maintenance 的 CheckSpace 与 EstimateSpace 都建立在它之上）。它的返回
// 值向任何一个方向出错都可被利用：高报会让恢复过程在写到一半时耗尽磁盘，低报会让备份恢复
// 被永久拒绝。本文件断言的是这条数字契约本身，不是调用方的策略。
var _ ports.SpaceChecker = OS{}

// TestFreeBytesReportsNonNegativeValueForExistingVolume 断言已知存在的卷返回非负值。
// 负值会让 `free >= requiredBytes` 恒假，把空间闸变成永久拒绝。
func TestFreeBytesReportsNonNegativeValueForExistingVolume(t *testing.T) {
	directory := t.TempDir()
	free, err := OS{}.FreeBytes(directory)
	if err != nil {
		t.Fatalf("已知存在的卷不应报错: %v", err)
	}
	if free < 0 {
		t.Fatalf("剩余空间为负值 %d：uint64 到 int64 的转换或单位换算溢出", free)
	}
	if free == 0 {
		t.Fatal("临时目录所在卷剩余空间为 0，这与测试能够写入临时文件的事实矛盾")
	}
}

// TestFreeBytesReportsErrorInsteadOfSilentZero 断言失败时返回错误，而不是静默的 0。
//
// 这是两个方向里更危险的一个：如果错误被吞掉、只返回 0，调用方看到的是「剩余 0 字节」，
// 于是所有 GC、VACUUM、备份恢复和外部工具都会被稳定地判定为空间不足而永久拒绝，且没有
// 任何可解释的失败原因。
func TestFreeBytesReportsErrorInsteadOfSilentZero(t *testing.T) {
	cases := map[string]string{
		"不存在的目录":  filepath.Join(t.TempDir(), "absent", "deeper"),
		"路径含 NUL": string([]byte{'a', 0, 'b'}),
		"空路径":     "",
	}
	if runtime.GOOS == "windows" {
		cases["不存在的盘符"] = `Q:\absent\path`
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			free, err := OS{}.FreeBytes(path)
			if err == nil {
				t.Fatalf("无效路径应返回错误，实际返回剩余空间 %d", free)
			}
			if free != 0 {
				t.Fatalf("返回错误时剩余空间必须是 0，实际 %d", free)
			}
		})
	}
}

// TestFreeBytesIsVolumeScopedNotPathScoped 断言同一卷上的不同路径给出同一个数量级的答案。
// free_windows.go 用 GetDiskFreeSpaceEx、free_unix.go 用 statfs，二者都以卷为单位；如果哪天
// 换成按目录配额或按子树统计，这条断言会失败。
func TestFreeBytesIsVolumeScopedNotPathScoped(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	rootFree, err := OS{}.FreeBytes(root)
	if err != nil {
		t.Fatal(err)
	}
	nestedFree, err := OS{}.FreeBytes(nested)
	if err != nil {
		t.Fatal(err)
	}
	difference := rootFree - nestedFree
	if difference < 0 {
		difference = -difference
	}
	// 两次调用之间系统仍在正常写盘，因此只断言量级一致：任何按子树统计的实现都会给出
	// 相差若干数量级的结果。
	if difference > rootFree/4 {
		t.Fatalf("同一卷上的两个路径给出显著不同的剩余空间：root=%d nested=%d", rootFree, nestedFree)
	}
}

// TestFreeBytesUnitIsBytesNotBlocks 断言返回值的单位是字节。
//
// free_unix.go 返回 Bavail*Bsize，free_windows.go 返回 GetDiskFreeSpaceEx 的
// lpFreeBytesAvailableToCaller，两者都必须是字节。如果哪个平台实现漏掉块大小相乘、或改用
// KiB/簇为单位，调用方为 MaxOutputBytes*2 预留的空间就会被高报数千倍，外部工具会在写输出
// 的中途把磁盘写满。这里写入一段不可压缩的真实数据，断言剩余空间的下降量与写入量同量级：
// 若单位是 KiB，1 GiB 的下降只会表现为约 1 MiB。
func TestFreeBytesUnitIsBytesNotBlocks(t *testing.T) {
	directory := t.TempDir()
	before, err := OS{}.FreeBytes(directory)
	if err != nil {
		t.Fatal(err)
	}
	const payloadBytes = 64 << 20
	if before < 4*payloadBytes {
		t.Skipf("临时卷剩余空间 %d 字节不足以安全执行单位断言", before)
	}
	payload := make([]byte, payloadBytes)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, "unit-probe.bin")
	file, err := os.Create(target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(payload); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := OS{}.FreeBytes(directory)
	if err != nil {
		t.Fatal(err)
	}
	consumed := before - after
	// 同机其它进程也在写盘，因此只设下界：单位若是 KiB/块，consumed 会比 payloadBytes 小
	// 三个数量级，绝无可能达到 1/4。
	if consumed < payloadBytes/4 {
		t.Fatalf("写入 %d 字节后剩余空间只下降 %d 字节，返回值的单位不是字节", payloadBytes, consumed)
	}
}

// TestFreeBytesAcceptsFilePathNotOnlyDirectory 断言把文件路径交给 FreeBytes 也能得到该卷的
// 剩余空间。调用方（例如按 control.db 路径预检备份空间）传入的常常是文件而不是目录。
func TestFreeBytesAcceptsFilePathNotOnlyDirectory(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "probe.bin")
	if err := os.WriteFile(target, []byte("probe"), 0o600); err != nil {
		t.Fatal(err)
	}
	fileFree, err := OS{}.FreeBytes(target)
	if err != nil {
		t.Fatalf("文件路径应可用于卷空间查询: %v", err)
	}
	directoryFree, err := OS{}.FreeBytes(directory)
	if err != nil {
		t.Fatal(err)
	}
	difference := fileFree - directoryFree
	if difference < 0 {
		difference = -difference
	}
	if difference > directoryFree/4 {
		t.Fatalf("文件路径与其所在目录给出显著不同的剩余空间：file=%d dir=%d", fileFree, directoryFree)
	}
}

// TestFreeBytesIsStableAcrossRepeatedCalls 断言连续调用不会返回互相矛盾的数量级，
// 用于捕捉未初始化的输出参数或错误的 out 参数顺序（GetDiskFreeSpaceEx 的三个 out 参数
// 顺序是 free-to-caller / total / total-free，写错顺序会得到另一个同样"看起来合理"的数字）。
func TestFreeBytesIsStableAcrossRepeatedCalls(t *testing.T) {
	directory := t.TempDir()
	first, err := OS{}.FreeBytes(directory)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		next, err := OS{}.FreeBytes(directory)
		if err != nil {
			t.Fatal(err)
		}
		difference := first - next
		if difference < 0 {
			difference = -difference
		}
		if difference > first/4 {
			t.Fatalf("第 %d 次调用与首次结果量级不同：%d vs %d", i, first, next)
		}
	}
}
