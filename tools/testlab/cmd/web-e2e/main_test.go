package main

import (
	"os"
	"path/filepath"
	"testing"
)

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
