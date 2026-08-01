//go:build darwin

package appdirs

import (
	"os"
	"path/filepath"
)

// Defaults 保留既有 macOS 目录映射；Windows x64 RC 之前不把它列为验证目标。
func Defaults() (Dirs, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Dirs{}, err
	}
	root := filepath.Join(home, "Library", "Application Support", "Gallery")
	dirs := UnderRoot(root)
	dirs.Cache = filepath.Join(home, "Library", "Caches", "Gallery")
	dirs.Logs = filepath.Join(home, "Library", "Logs", "Gallery")
	return dirs, nil
}
