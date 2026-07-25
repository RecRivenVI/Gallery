//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package fileidentity_test

import (
	"os"
	"testing"
)

func TestUnixStableFileIdentity(t *testing.T) {
	exerciseStableFileIdentity(t)
}

func openTestFile(path string) (*os.File, error) {
	return os.Open(path)
}
