package query

import (
	"strings"
	"testing"
)

func TestTamperCursorProducesDifferentString(t *testing.T) {
	original := "abcXYZ123"
	tampered := tamperCursor(original)
	if tampered == original {
		t.Fatal("tamperCursor did not change the input")
	}
	if len(tampered) != len(original) && !strings.HasSuffix(tampered, "x") {
		t.Fatalf("tamperCursor produced unexpected shape: %q -> %q", original, tampered)
	}
}

func TestTamperCursorHandlesNoLowercaseLetters(t *testing.T) {
	original := "123456"
	tampered := tamperCursor(original)
	if tampered == original {
		t.Fatal("tamperCursor did not change an all-digit input")
	}
}
