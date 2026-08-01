//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

func providerName() string { return "unix-device-inode" }

func fileIDByPath(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return "?"
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "n/a"
	}
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino)
}
