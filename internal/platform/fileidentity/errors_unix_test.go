//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package fileidentity

import (
	"errors"
	"testing"

	"github.com/RecRivenVI/gallery/internal/ports"
	"golang.org/x/sys/unix"
)

func TestUnixUnsupportedFileIDIsExplicitlyUnavailable(t *testing.T) {
	for _, source := range []error{unix.ENOSYS, unix.ENOTSUP} {
		if err := classifyUnixError(source); !errors.Is(err, ports.ErrFileIdentityUnavailable) {
			t.Fatalf("unsupported 错误未归一为 unavailable：%v", err)
		}
	}
	if err := classifyUnixError(unix.EBADF); !errors.Is(err, ports.ErrFileIdentityInvalidHandle) {
		t.Fatalf("无效句柄错误未归一：%v", err)
	}
}
