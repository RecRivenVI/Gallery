//go:build !windows

package process_test

// Unix 当前不会运行内存硬限制辅助角色；保留桩函数让共享测试辅助入口可交叉编译。
func allocateForLimitTest(int) bool { return false }
