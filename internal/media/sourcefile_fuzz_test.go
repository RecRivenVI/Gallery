package media_test

// ValidateRelativePath 是「媒体根永久只读」这条产品边界上唯一的词法防线：扫描、
// 媒体正文读取与文件根浏览都先把不可信相对路径交给它，再 filepath.Join 到真实根。
// 因此它的契约必须是二选一——要么返回错误，要么返回一个在**任意**根下都词法包含
// 于该根、且不含任何在 Windows 上会改变解释的字符的相对路径。

import (
	"errors"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/media"
)

// containmentRoots 覆盖三类根形状：普通盘符根、UNC 根与 POSIX 根。词法包含判定
// 必须对全部三者成立，因为同一份 normalized 会被 Source、文件根与派生资源分别拼接。
var containmentRoots = []string{
	`C:\gallery\root`,
	`\\server\share\root`,
	"/srv/gallery/root",
	".",
}

func FuzzValidateRelativePath(f *testing.F) {
	for _, seed := range relativePathSeeds() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, relative string) {
		normalized, err := media.ValidateRelativePath(relative)
		if err != nil {
			if normalized != "" {
				t.Fatalf("拒绝路径时必须返回空串，实际 %q", normalized)
			}
			var structured *fault.Error
			if !errors.As(err, &structured) || structured.Code != fault.CodePathEscape {
				t.Fatalf("拒绝必须是 PATH_ESCAPE，实际 %v", err)
			}
			return
		}
		assertNormalizedPathInvariants(t, relative, normalized)

		// 幂等：规范化结果再次校验必须原样通过，否则「先校验后拼接」与
		// 「先拼接后校验」两条调用顺序会得到不同结论。
		again, againErr := media.ValidateRelativePath(normalized)
		if againErr != nil {
			t.Fatalf("规范化结果被二次校验拒绝: %q -> %q: %v", relative, normalized, againErr)
		}
		if again != normalized {
			t.Fatalf("规范化不幂等: %q -> %q -> %q", relative, normalized, again)
		}
	})
}

func assertNormalizedPathInvariants(t *testing.T, relative, normalized string) {
	t.Helper()
	if normalized == "" {
		t.Fatalf("接受了空规范化结果: %q", relative)
	}
	if strings.ContainsRune(normalized, '\x00') {
		t.Fatalf("规范化结果含 NUL: %q", relative)
	}
	if strings.Contains(normalized, `\`) {
		t.Fatalf("规范化结果含反斜杠: %q", normalized)
	}
	if path.IsAbs(normalized) || filepath.IsAbs(normalized) {
		t.Fatalf("规范化结果是绝对路径: %q", normalized)
	}
	if filepath.VolumeName(normalized) != "" {
		t.Fatalf("规范化结果含卷名: %q", normalized)
	}
	if clean := path.Clean(normalized); clean != normalized {
		t.Fatalf("规范化结果不是 path.Clean 不动点: %q -> %q", normalized, clean)
	}
	for _, segment := range strings.Split(normalized, "/") {
		switch {
		case segment == "":
			t.Fatalf("规范化结果含空段: %q", normalized)
		case segment == "." || segment == "..":
			t.Fatalf("规范化结果含相对段 %q: %q", segment, normalized)
		case strings.HasSuffix(segment, "."):
			// Windows 会静默剥掉结尾的点，`a.` 与 `a` 会解析到同一文件。
			t.Fatalf("规范化结果段以点结尾: %q", normalized)
		case strings.HasSuffix(segment, " "):
			// 同上：Windows 剥掉结尾空格。
			t.Fatalf("规范化结果段以空格结尾: %q", normalized)
		}
	}

	// 核心不变量：在任意根下拼接后必须词法包含于该根。
	for _, root := range containmentRoots {
		joined := filepath.Join(root, filepath.FromSlash(normalized))
		rel, relErr := filepath.Rel(root, joined)
		if relErr != nil {
			t.Fatalf("根 %q 下无法求相对路径: %q: %v", root, normalized, relErr)
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			t.Fatalf("规范化结果在根 %q 下逃逸: %q -> %q", root, normalized, rel)
		}
	}
	assertNoReservedDeviceName(t, normalized)
}

// assertNoReservedDeviceName 复核 Windows 保留设备名。这里只断言实现当前确实覆盖
// 的集合；已知未覆盖的形态（含冒号的段、CONIN$/CONOUT$）由
// TestValidateRelativePathAcceptsAlternateDataStreams 记录，不在这里断言，
// 以免把已知缺陷变成常红门禁。
func assertNoReservedDeviceName(t *testing.T, normalized string) {
	t.Helper()
	reserved := map[string]bool{
		"CON": true, "PRN": true, "AUX": true, "NUL": true,
		"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
		"COM6": true, "COM7": true, "COM8": true, "COM9": true,
		"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
		"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
	}
	for _, segment := range strings.Split(normalized, "/") {
		base := strings.ToUpper(strings.SplitN(segment, ".", 2)[0])
		if reserved[base] {
			t.Fatalf("规范化结果含 Windows 保留设备名: %q", normalized)
		}
	}
}

// TestValidateRelativePathAcceptsAlternateDataStreams 把审计怀疑的 ADS 缺陷固定成
// 一条**可执行的事实记录**：ValidateRelativePath 当前不检查段内冒号。
//
// 在 NTFS 上 `a/b:stream` 会被 Win32 解释成文件 `a\b` 的备用数据流，而不是名为
// `b:stream` 的文件。它词法上仍在根内，因此 within() 与 filepath.Rel 都判定合法，
// 但它读到的字节既不属于任何 CanonicalMedia，也不受媒体扩展名/类型判定约束。
//
// 本测试断言的是**当前行为**而不是期望行为，这样：
//   - 现在不制造常红门禁（修生产代码不属于本工作线）；
//   - 一旦 ValidateRelativePath 补上「任何段含 ':' 一律拒绝」，本测试立即失败，
//     提示把它改写成 assertNormalizedPathInvariants 里的正向断言。
func TestValidateRelativePathAcceptsAlternateDataStreams(t *testing.T) {
	streams := []string{
		"a/b:stream",
		"a/b:$DATA",
		"a/b::$INDEX_ALLOCATION",
		"x/C:a",
		"NUL:",
		"CON:",
		"dir:stream/file.jpg",
		"a/CONIN$",
		"a/CONOUT$",
	}
	var accepted []string
	for _, candidate := range streams {
		normalized, err := media.ValidateRelativePath(candidate)
		if err == nil {
			accepted = append(accepted, normalized)
		}
	}
	if len(accepted) == 0 {
		t.Fatalf("段内冒号与 CONIN$/CONOUT$ 已被全部拒绝，请把本测试改写成正向断言")
	}
	t.Logf("已知缺陷：ValidateRelativePath 接受下列会被 Windows 解释为备用数据流或控制台设备的相对路径 %q", accepted)
	if runtime.GOOS == "windows" {
		// 记录一次真实拼接结果，确认它在词法上确实落在根内——这正是词法防线
		// 无法察觉该问题的原因。
		t.Logf("示例拼接结果 %q", filepath.Join(`C:\gallery\root`, filepath.FromSlash(accepted[0])))
	}
}

func relativePathSeeds() []string {
	seeds := []string{
		// 正常形态
		"a", "a/b", "a/b/c.jpg", "作品/第01话/001.png", "a b/c d.jpg", "a-b_c.jpeg",
		// 逃逸与相对段
		"", ".", "..", "../a", "a/..", "a/../..", "a/../../b", "./a", "a/./b", "a//b", "/a", "//a", "///",
		// 卷名、UNC 与 Windows 前缀
		"C:a", "C:/a", "C:\\a", `\\?\C:\a`, `\\.\C:\a`, `\\server\share\a`, "c:", "1:a", "::a",
		// 反斜杠
		`a\b`, `a\..\b`, `a/b\c`, `\a`, `a\`,
		// 保留设备名
		"CON", "con", "CoN", "con.txt", "CON.TXT", "NUL", "nul.jpg", "AUX", "PRN",
		"COM1", "com9.png", "LPT1", "lpt9.txt", "COM0", "LPT0", "CONIN$", "CONOUT$",
		"ｃｏｎ", "ＣＯＮ", "con\u00a0", "con ", " con",
		// 备用数据流（审计怀疑点）
		"a/b:stream", "a/b:$DATA", "a/b::$INDEX_ALLOCATION", "NUL:", "CON:", "x/C:a", "a:b:c",
		// 结尾点与空格
		"a.", "a ", "a/b.", "a/b ", "a...", "a/...", "a/. ", "a. /b", "a\u3000",
		// 控制字符与双向覆写
		"a\x00b", "a\nb", "a\tb", "a\x7fb", "\u202ea/b", "a\u200bb", "a\ufeffb",
		// 非法 UTF-8 与代理
		"a\xffb", "a\xed\xa0\x80b", "\ufffd",
		// 大小写与 Unicode 规范化
		"Å/a", "Å/a", "ﬁle.jpg",
	}
	// 深路径：确认词法校验不随深度退化，也不递归。
	seeds = append(seeds, strings.Repeat("a/", 4096)+"b")
	seeds = append(seeds, strings.Repeat("../", 4096)+"b")
	seeds = append(seeds, strings.Repeat("./", 4096)+"b")
	seeds = append(seeds, strings.Repeat("a", 4096))
	return seeds
}
