//go:build !windows

package disk

import (
	"golang.org/x/sys/unix"
)

type OS struct{}

// FreeBytes 返回 path 所在文件系统对当前调用者可用的剩余字节数。
//
// 单位契约与 free_windows.go 一致，必须是字节，因此这里必须乘上块大小；返回块数会把可用
// 空间高报数千倍，让按字节预算做前置闸的 GC/VACUUM/备份恢复在写到一半时耗尽磁盘。
// 符号契约同样一致：结果永不为负，因为负值会让 `free >= requiredBytes` 恒假，把空间闸变成
// 永久拒绝。statfs 失败时返回错误而不是静默的 0。
func (OS) FreeBytes(path string) (int64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	// Bavail/Bsize 的具体整数类型逐平台不同（Linux 为 uint64/int64，部分 BSD 的 f_bavail 是
	// 有符号并可以为负，表示已经吃掉了文件系统预留）。统一先转 int64 再做保守夹取：
	// 任何非正的可用块都按 0 处理，绝不向上报告。
	blocks := int64(stat.Bavail)
	size := int64(stat.Bsize)
	if blocks <= 0 || size <= 0 {
		return 0, nil
	}
	const maxInt64 = 1<<63 - 1
	// 乘法溢出会把巨大的可用空间翻成负数。真实值确实超过 int64 上限时饱和到上限是准确的
	// 保守表述，而回绕成负数会被调用方读成"空间不足"。
	if blocks > maxInt64/size {
		return maxInt64, nil
	}
	return blocks * size, nil
}
