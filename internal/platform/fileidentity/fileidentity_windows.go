//go:build windows

package fileidentity

import (
	"encoding/hex"
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"github.com/RecRivenVI/gallery/internal/ports"
	"golang.org/x/sys/windows"
)

// fileIDInfo 与 Windows FILE_ID_INFO 的内存布局一致。x/sys/windows 暴露了
// FileIdInfo 与 GetFileInformationByHandleEx，但当前版本没有导出该结构体。
type fileIDInfo struct {
	VolumeSerialNumber uint64
	FileID             [16]byte
}

func identify(file *os.File) (ports.FileIdentityCandidate, error) {
	var info fileIDInfo
	err := windows.GetFileInformationByHandleEx(
		windows.Handle(file.Fd()),
		windows.FileIdInfo,
		(*byte)(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	runtime.KeepAlive(file)
	if err != nil {
		return ports.FileIdentityCandidate{}, classifyWindowsError(err)
	}
	return ports.FileIdentityCandidate{Encoded: fmt.Sprintf(
		"%s:windows:%016x:%s",
		encodingPrefix,
		info.VolumeSerialNumber,
		hex.EncodeToString(info.FileID[:]),
	)}, nil
}

func classifyWindowsError(err error) error {
	switch err {
	case windows.ERROR_INVALID_HANDLE:
		return ports.ErrFileIdentityInvalidHandle
	case windows.ERROR_INVALID_FUNCTION,
		windows.ERROR_INVALID_PARAMETER,
		windows.ERROR_NOT_SUPPORTED,
		windows.ERROR_CALL_NOT_IMPLEMENTED:
		// 不静默降级到 64-bit FileIndex；缺少 FILE_ID_128 时明确报告 unavailable。
		return ports.ErrFileIdentityUnavailable
	default:
		return fmt.Errorf("%w: %v", ports.ErrFileIdentityUnavailable, err)
	}
}
