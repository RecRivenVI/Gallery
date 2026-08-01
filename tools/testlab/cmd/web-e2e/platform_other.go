//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
)

func platformExecutableName(name string) string { return name }

func fixedGo() (string, error) {
	if configured := os.Getenv("GALLERY_GO"); configured != "" {
		if info, err := os.Stat(configured); err == nil && !info.IsDir() {
			return configured, nil
		}
		return "", fmt.Errorf("GALLERY_GO 指向的文件不存在")
	}
	return exec.LookPath("go")
}
