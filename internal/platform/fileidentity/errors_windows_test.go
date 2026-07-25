//go:build windows

package fileidentity

import (
	"errors"
	"testing"
	"unsafe"

	"github.com/RecRivenVI/gallery/internal/ports"
	"golang.org/x/sys/windows"
)

func TestWindowsFileIDInfoLayout(t *testing.T) {
	var info fileIDInfo
	if size := unsafe.Sizeof(info); size != 24 {
		t.Fatalf("FILE_ID_INFO 布局大小错误：%d", size)
	}
	if offset := unsafe.Offsetof(info.FileID); offset != 8 {
		t.Fatalf("FILE_ID_INFO FileID 偏移错误：%d", offset)
	}
}

func TestWindowsUnsupportedFileIDIsExplicitlyUnavailable(t *testing.T) {
	for _, source := range []error{
		windows.ERROR_INVALID_FUNCTION,
		windows.ERROR_INVALID_PARAMETER,
		windows.ERROR_NOT_SUPPORTED,
		windows.ERROR_CALL_NOT_IMPLEMENTED,
	} {
		if err := classifyWindowsError(source); !errors.Is(err, ports.ErrFileIdentityUnavailable) {
			t.Fatalf("unsupported 错误未归一为 unavailable：%v", err)
		}
	}
	if err := classifyWindowsError(windows.ERROR_INVALID_HANDLE); !errors.Is(err, ports.ErrFileIdentityInvalidHandle) {
		t.Fatalf("无效句柄错误未归一：%v", err)
	}
}
