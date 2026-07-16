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
