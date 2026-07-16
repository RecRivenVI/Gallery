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
	if bytes.Contains(content, []byte("github.com/RecRivenVI/gallery/internal/")) {
		t.Fatal("galleryctl 依赖了后端 internal Go 包")
	}
}
