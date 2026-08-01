//go:build windows

package main

import (
	"fmt"
	"os"
)

func platformExecutableName(name string) string { return name + ".exe" }

func fixedGo() (string, error) {
	if configured := os.Getenv("GALLERY_GO"); configured != "" {
		if info, err := os.Stat(configured); err == nil && !info.IsDir() {
			return configured, nil
		}
		return "", fmt.Errorf("GALLERY_GO 指向的文件不存在")
	}
	return "", fmt.Errorf("Windows 必须显式设置 GALLERY_GO")
}
