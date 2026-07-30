// Package fileidentity 从已打开文件句柄读取平台稳定的候选身份。
package fileidentity

import (
	"errors"
	"os"

	"github.com/RecRivenVI/gallery/internal/ports"
)

const encodingPrefix = ports.FileIdentityKindV1

// OS 是生产环境的平台文件身份 adapter。
type OS struct{}

var _ ports.FileIdentityProvider = OS{}

// Identify 只查询现有句柄，不读取文件内容，也不根据路径重新打开目标。
func (OS) Identify(file *os.File) (ports.FileIdentityCandidate, error) {
	if file == nil {
		return ports.FileIdentityCandidate{}, ports.ErrFileIdentityInvalidHandle
	}
	if file.Fd() == ^uintptr(0) {
		return ports.FileIdentityCandidate{}, ports.ErrFileIdentityInvalidHandle
	}
	info, err := file.Stat()
	if err != nil {
		if errors.Is(err, os.ErrClosed) || errors.Is(err, os.ErrInvalid) {
			return ports.FileIdentityCandidate{}, ports.ErrFileIdentityInvalidHandle
		}
		return ports.FileIdentityCandidate{}, ports.ErrFileIdentityUnavailable
	}
	if !info.Mode().IsRegular() {
		return ports.FileIdentityCandidate{}, ports.ErrFileIdentityNotRegular
	}
	return identify(file)
}
