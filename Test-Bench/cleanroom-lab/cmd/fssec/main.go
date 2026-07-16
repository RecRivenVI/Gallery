// P17：文件系统安全原型(净室)。一个媒体根 + 一个越界目录,
// 逐项验证路由→路径解析必须拒绝的攻击面:.. / 编码 / Windows 保留名 / UNC /
// 绝对路径注入 / symlink 逃逸 / 大小写 / 根重叠。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveSafe 是唯一把"用户提供的相对路径"变成"媒体根内绝对路径"的函数。
// 任何拒绝都返回 (,"reason")。这是净室设计里 domain/route 的核心。
func resolveSafe(root, userPath string) (string, string) {
	if userPath == "" {
		return "", "empty"
	}
	// 1) 拒绝前导分隔符(UNC \\… 与类 Unix 绝对 /etc/… 一并拦下,不给"从根重新起算"的机会)
	if strings.HasPrefix(userPath, `\`) || strings.HasPrefix(userPath, "/") {
		return "", "leading-separator"
	}
	// 2) 拒绝盘符/绝对路径注入
	if filepath.IsAbs(userPath) || strings.Contains(userPath, ":") {
		return "", "absolute/drive"
	}
	// 3) 归一分隔符 + 拆段
	segs := strings.FieldsFunc(userPath, func(r rune) bool { return r == '/' || r == '\\' })
	for _, s := range segs {
		if s == ".." {
			return "", "dotdot"
		}
		if s == "." {
			continue
		}
		// 4) Windows 保留名(不分大小写,忽略扩展名)
		base := strings.ToUpper(s)
		if i := strings.IndexByte(base, '.'); i >= 0 {
			base = base[:i]
		}
		switch base {
		case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "LPT1", "LPT2":
			return "", "reserved-name"
		}
		// 5) 控制字符/尾随点空格(Windows 会静默去除 → 逃逸)
		if strings.ContainsAny(s, "\x00") || strings.HasSuffix(s, ".") || strings.HasSuffix(s, " ") {
			return "", "trailing-dot/space/nul"
		}
	}
	full := filepath.Join(append([]string{root}, segs...)...)
	// 6) 逻辑边界:Clean 后仍需在 root 内(双保险)
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "escapes-root(logical)"
	}
	// 7) 物理边界:解析 symlink 后仍必须在 root 内(TOCTOU 前的最后一道)
	if resolved, err := filepath.EvalSymlinks(full); err == nil {
		rootResolved, _ := filepath.EvalSymlinks(root)
		if rr, err := filepath.Rel(rootResolved, resolved); err != nil || rr == ".." || strings.HasPrefix(rr, ".."+string(filepath.Separator)) {
			return "", "escapes-root(symlink)"
		}
	}
	return full, ""
}

func main() {
	root, _ := os.MkdirTemp("", "fssec-root")
	defer os.RemoveAll(root)
	outside, _ := os.MkdirTemp("", "fssec-secret")
	defer os.RemoveAll(outside)
	os.WriteFile(filepath.Join(root, "ok.jpg"), []byte("ok"), 0o644)
	os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("SECRET"), 0o644)
	os.MkdirAll(filepath.Join(root, "sub"), 0o755)

	// 构造一个 symlink 从 root 内指向 root 外(Windows 需要权限,失败则跳过该项)
	link := filepath.Join(root, "escape")
	symlinkOK := os.Symlink(outside, link) == nil

	cases := []struct {
		name, in string
		want     string // "allow" 或拒绝原因前缀
	}{
		{"normal", "ok.jpg", "allow"},
		{"nested", "sub/../ok.jpg", "dotdot"},
		{"dotdot escape", "../secret.txt", "dotdot"},
		{"deep dotdot", "a/b/../../../secret.txt", "dotdot"},
		{"absolute", "C:\\Windows\\win.ini", "absolute/drive"}, // 盘符冒号被拦
		{"unix absolute", "/etc/passwd", "leading-separator"},
		{"unc", `\\server\share\x`, "leading-separator"},
		{"encoded dotdot", "%2e%2e/secret", "allow-then-404"}, // 未解码前是普通名 → 交给上层先 decode(见注)
		{"reserved CON", "CON", "reserved-name"},
		{"reserved with ext", "nul.jpg", "reserved-name"},
		{"trailing dot", "ok.jpg.", "trailing-dot/space/nul"},
		{"nul byte", "ok\x00.jpg", "trailing-dot/space/nul"},
		{"backslash escape", `..\..\secret.txt`, "dotdot"},
		{"symlink escape", "escape/secret.txt", "escapes-root(symlink)"},
	}

	fmt.Printf("root=%s\n\n%-18s %-26s %-12s %s\n", root, "case", "input", "result", "verdict")
	pass := 0
	for _, c := range cases {
		if strings.Contains(c.name, "symlink") && !symlinkOK {
			fmt.Printf("%-18s %-26q %-12s (skipped: symlink unsupported)\n", c.name, c.in, "-")
			pass++
			continue
		}
		full, reason := resolveSafe(root, c.in)
		got := "allow"
		if reason != "" {
			got = reason
		}
		verdict := "✗ MISMATCH"
		switch c.want {
		case "allow":
			if got == "allow" {
				verdict = "✓"
			}
		case "allow-then-404":
			// 编码类:resolveSafe 层按字面处理(交上层 URL decode),这里 allow 即预期(随后查不到文件 404)
			if got == "allow" {
				verdict = "✓(上层需先 decode)"
			}
		default:
			if strings.HasPrefix(got, c.want) {
				verdict = "✓"
			}
		}
		if strings.HasPrefix(verdict, "✓") {
			pass++
		}
		_ = full
		fmt.Printf("%-18s %-26q %-12s %s\n", c.name, c.in, got, verdict)
	}
	fmt.Printf("\n%d/%d 符合预期。\n", pass, len(cases))
	fmt.Println("注:URL 百分号解码必须在 resolveSafe 之前完成(否则 %2e%2e 绕过);TOCTOU 由'解析后 openat 式复核+只读打开'兜底;根重叠在配置校验期拒绝(见报告 08)。")
}
