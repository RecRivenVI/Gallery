//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package fileidentity

import (
	"fmt"
	"os"

	"github.com/RecRivenVI/gallery/internal/ports"
	"golang.org/x/sys/unix"
)

func identify(file *os.File) (ports.FileIdentityCandidate, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return ports.FileIdentityCandidate{}, classifyUnixError(err)
	}
	return ports.FileIdentityCandidate{Encoded: fmt.Sprintf(
		"%s:unix:%016x:%016x",
		encodingPrefix,
		uint64(stat.Dev),
		uint64(stat.Ino),
	)}, nil
}

func classifyUnixError(err error) error {
	if err == unix.EBADF {
		return ports.ErrFileIdentityInvalidHandle
	}
	if err == unix.ENOSYS || err == unix.ENOTSUP {
		return ports.ErrFileIdentityUnavailable
	}
	return fmt.Errorf("%w: %v", ports.ErrFileIdentityUnavailable, err)
}
