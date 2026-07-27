//go:build windows

package process

import "golang.org/x/sys/windows"

func processStillActive(pid int) bool {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(process)
	var code uint32
	if err := windows.GetExitCodeProcess(process, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}
