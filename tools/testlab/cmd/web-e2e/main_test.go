package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateBrowserProject(t *testing.T) {
	for _, project := range []string{"chromium", "firefox"} {
		if err := validateBrowserProject(project); err != nil {
			t.Fatalf("项目 %s 应受支持: %v", project, err)
		}
	}
	for _, project := range []string{"", "chrome", "edge", "../firefox"} {
		if err := validateBrowserProject(project); err == nil {
			t.Fatalf("项目 %q 应被拒绝", project)
		}
	}
}

func TestRetainDiagnosticsPreservesDistinctLogNames(t *testing.T) {
	logsRoot := t.TempDir()
	diagnosticsRoot := t.TempDir()
	first := filepath.Join(logsRoot, "galleryd.log")
	restored := filepath.Join(logsRoot, "galleryd-restored.log")
	if err := os.WriteFile(first, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(restored, []byte("restored"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := retainDiagnostics(first, diagnosticsRoot); err != nil {
		t.Fatal(err)
	}
	if err := retainDiagnostics(restored, diagnosticsRoot); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"galleryd.log":          "first",
		"galleryd-restored.log": "restored",
	} {
		got, err := os.ReadFile(filepath.Join(diagnosticsRoot, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("%s 内容=%q，期望 %q", name, got, want)
		}
	}
}
