//go:build windows

package process_test

import "golang.org/x/sys/windows"

var limitTestAllocations []uintptr

// allocateForLimitTest 直接向 Windows 请求一块已提交内存。JobMemoryLimit 约束的是提交量，
// 因此超过整棵树预算时 VirtualAlloc 必须同步失败；地址保留到辅助进程退出，避免提前释放。
func allocateForLimitTest(bytes int) bool {
	address, err := windows.VirtualAlloc(0, uintptr(bytes), windows.MEM_RESERVE|windows.MEM_COMMIT, windows.PAGE_READWRITE)
	if err != nil {
		return false
	}
	limitTestAllocations = append(limitTestAllocations, address)
	return true
}
