//go:build windows

package appdirs

import (
	"fmt"
	"os"
	"path/filepath"
)

// Defaults 返回当前 Windows 用户的默认 Gallery 数据目录。
func Defaults() (Dirs, error) {
	roaming := os.Getenv("APPDATA")
	local := os.Getenv("LOCALAPPDATA")
	if roaming == "" || local == "" {
		return Dirs{}, fmt.Errorf("APPDATA/LOCALAPPDATA 不可用")
	}
	dirs := UnderRoot(filepath.Join(local, "Gallery"))
	dirs.Config = filepath.Join(roaming, "Gallery")
	return dirs, nil
}
