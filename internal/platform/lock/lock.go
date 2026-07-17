// Package lock 提供 AppDirs 级的进程独占所有权原语。锁由操作系统在进程退出（含崩溃强杀）
// 时自动释放，因此遗留的空锁文件不会永久阻止后续启动。Windows 与 Unix 使用不同的平台
// adapter 实现底层锁，公共 API 保持一致。
package lock

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrAlreadyLocked 表示另一个进程已持有该锁文件的独占所有权。
var ErrAlreadyLocked = errors.New("appdirs 已被其他实例锁定")

// Handle 表示对某个锁文件的进程级独占所有权。
type Handle struct {
	file *os.File
	path string
}

// Path 返回锁文件路径。
func (h *Handle) Path() string {
	if h == nil {
		return ""
	}
	return h.path
}

// Acquire 打开（或创建）锁文件并尝试取得非阻塞独占锁。已被其他进程占用时返回
// ErrAlreadyLocked；目录不可用或打开失败返回底层错误。
func Acquire(path string) (*Handle, error) {
	if path == "" {
		return nil, errors.New("锁路径为空")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &Handle{file: file, path: path}, nil
}

// Release 释放独占锁并关闭句柄，可重复调用。锁文件本身保留作为下一次锁定的对象，不删除，
// 因此不会因为竞态删除而让另一个实例误取所有权。
func (h *Handle) Release() error {
	if h == nil || h.file == nil {
		return nil
	}
	unlockErr := unlockFile(h.file)
	closeErr := h.file.Close()
	h.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
