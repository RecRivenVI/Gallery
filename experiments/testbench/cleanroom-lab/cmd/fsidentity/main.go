// P15：稳定媒体身份(净室)。验证一个媒体文件在下列操作后,
// 三种身份方案(路径 / Windows FileID / 内容签名)各自是否保持稳定。
// 结论直接决定领域模型里 Media 的 identity 字段(见报告 02/09)。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func main() {
	base, err := os.MkdirTemp("", "fsid")
	must(err)
	defer os.RemoveAll(base)

	orig := filepath.Join(base, "a", "work1", "1.jpg")
	os.MkdirAll(filepath.Dir(orig), 0o755)
	writeFile(orig, "hello media content")

	report := func(op, path string) {
		p := pathID(path)
		f := fileID(path)
		c := contentID(path)
		fmt.Printf("%-18s path=%-10s fileID=%-14s content=%s\n", op, short(p), f, short(c))
	}
	fmt.Printf("%-18s %-15s %-16s %s\n", "operation", "path-id", "os-fileID", "content-sha256")
	report("original", orig)

	// 1) 同卷重命名
	renamed := filepath.Join(base, "a", "work1", "renamed.jpg")
	os.Rename(orig, renamed)
	report("rename(same dir)", renamed)

	// 2) 同卷移动到别的目录
	moved := filepath.Join(base, "b", "work2", "renamed.jpg")
	os.MkdirAll(filepath.Dir(moved), 0o755)
	os.Rename(renamed, moved)
	report("move(same volume)", moved)

	// 3) mtime 改变(touch)
	future := time.Now().Add(48 * time.Hour)
	os.Chtimes(moved, future, future)
	report("touch(mtime++)", moved)

	// 4) 内容替换(路径不变)
	writeFile(moved, "DIFFERENT content now")
	report("content replaced", moved)

	// 5) 复制(新文件,内容相同的另一份)
	copyPath := filepath.Join(base, "b", "work2", "copy.jpg")
	writeFile(copyPath, "DIFFERENT content now")
	report("copy(dup content)", copyPath)

	// 6) 硬链接(同内容、同 fileID、不同路径)
	hardlink := filepath.Join(base, "b", "work2", "hard.jpg")
	if err := os.Link(moved, hardlink); err == nil {
		report("hardlink", hardlink)
	} else {
		fmt.Printf("%-18s (hardlink unsupported: %v)\n", "hardlink", err)
	}

	// 7) 跨卷移动(用临时同卷模拟说明):Windows 跨卷 rename = copy+delete → fileID 变
	fmt.Println("\n跨卷移动:Windows/NTFS 跨卷 rename 实为 copy+delete,目标 FileID 必变(与复制等价);内容签名不变。")

	fmt.Println("\n判定矩阵(✓稳定 ✗变化):")
	fmt.Println("  操作                路径   FileID  内容签名")
	fmt.Println("  同卷重命名/移动       ✗      ✓       ✓")
	fmt.Println("  touch mtime         ✓      ✓       ✓")
	fmt.Println("  内容替换(路径不变)    ✓      ✓*      ✗   (*NTFS 原地写 FileID 不变)")
	fmt.Println("  复制                 ✗      ✗       =(与源同)")
	fmt.Println("  硬链接               ✗      ✓(=源)  =(与源同)")
	fmt.Println("  跨卷移动             ✗      ✗       ✓")
	fmt.Println("\n结论:三者都不能单独作为身份。推荐组合签名 = 内容签名(去重/移动跟踪主键)")
	fmt.Println("      + FileID(同卷移动快速关联)+ 路径(当前位置,可变属性,非身份)。")
	fmt.Println("      内容签名用大小+首尾块采样哈希(避免全量读大视频),仅在冲突时全量校验。")
}

func pathID(p string) string {
	s := sha256.Sum256([]byte(filepath.ToSlash(p)))
	return hex.EncodeToString(s[:])
}
func fileID(p string) string {
	return fileIDByPath(p)
}
func contentID(p string) string {
	data, err := os.ReadFile(p)
	if err != nil {
		return "?"
	}
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}
func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
func writeFile(p, content string) {
	must(os.WriteFile(p, []byte(content), 0o644))
}
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "FATAL:", err)
		os.Exit(1)
	}
}
