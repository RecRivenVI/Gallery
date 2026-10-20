package main

import (
	"bytes"
	"os"
	"testing"
)

func TestGalleryctlDoesNotImportBackendInternals(t *testing.T) {
	content, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	// 构建元数据不是后端业务依赖；只允许这个 internal 包。
	content = bytes.ReplaceAll(content, []byte("github.com/RecRivenVI/gallery/internal/version"), nil)
	if bytes.Contains(content, []byte("github.com/RecRivenVI/gallery/internal/")) {
		t.Fatal("galleryctl 依赖了后端 internal Go 包")
	}
}
