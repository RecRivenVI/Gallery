//go:build !windows && !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package fileidentity

import (
	"os"

	"github.com/RecRivenVI/gallery/internal/ports"
)

func identify(*os.File) (ports.FileIdentityCandidate, error) {
	return ports.FileIdentityCandidate{}, ports.ErrFileIdentityUnavailable
}
