package filesystem

import (
	"io/fs"
	"os"
	"path/filepath"
)

type OS struct{}

func (OS) Abs(path string) (string, error)              { return filepath.Abs(path) }
func (OS) EvalSymlinks(path string) (string, error)     { return filepath.EvalSymlinks(path) }
func (OS) MkdirAll(path string, perm fs.FileMode) error { return os.MkdirAll(path, perm) }
func (OS) Stat(path string) (fs.FileInfo, error)        { return os.Stat(path) }

// IsLink 报告某个目录项是否是「指向别处的链接」，而不是普通文件或普通目录。
//
// 这个判定必须集中在平台层，因为各操作系统把链接暴露成不同的 FileMode：
//
//   - Unix symlink 与 Windows 的 symbolic link 报告为 fs.ModeSymlink；
//   - **Windows 的 junction（目录联接）与 volume mount point 报告为 fs.ModeIrregular，
//     并且 DirEntry.IsDir() 为 false**——只判断 fs.ModeSymlink 会完全漏掉它们。
//
// 漏判的后果不是「少扫一点」而是两类错误：junction 指向的整棵子树被静默跳过；同时
// 该 junction 因为 IsDir() 为 false 而被当成一个 0 字节的普通文件进入媒体列表。
// 因此凡是需要区分「可以安全下降的普通目录」与「链接」的地方，都必须使用本函数，
// 不得各自重写 fs.ModeSymlink 判断。
func IsLink(mode fs.FileMode) bool {
	return mode&(fs.ModeSymlink|fs.ModeIrregular) != 0
}
