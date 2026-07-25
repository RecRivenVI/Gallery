//go:build windows

package fileidentity_test

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsStableFileIdentity(t *testing.T) {
	exerciseStableFileIdentity(t)
}

func openTestFile(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		name,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), "file-identity-test")
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, os.ErrInvalid
	}
	return file, nil
}
