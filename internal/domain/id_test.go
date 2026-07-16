package domain_test

import (
	"bytes"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
)

func TestUUIDv7DomainIDIsTypedAndStable(t *testing.T) {
	fixed := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	generator := identity.Generator{Clock: clock.Fixed{Time: fixed}, Random: bytes.NewReader(make([]byte, 10))}
	id, err := generator.New(domain.IDLibrary)
	if err != nil {
		t.Fatal(err)
	}
	const expected = "lib_018cc251-f400-7000-8000-000000000000"
	if id.String() != expected {
		t.Fatalf("ID = %q, want %q", id.String(), expected)
	}
	if _, err := domain.ParseID(domain.IDLibrary, id.String()); err != nil {
		t.Fatalf("合法 ID 无法重读: %v", err)
	}
	if _, err := domain.ParseID(domain.IDSource, id.String()); err == nil {
		t.Fatal("不同实体前缀被静默接受")
	}
}

func TestBlobAndLocationReferencesDoNotUsePathsOrRowIDs(t *testing.T) {
	sum := sha256.Sum256([]byte("synthetic-media"))
	blob := domain.NewSHA256BlobRef(sum)
	if _, err := domain.ParseContentBlobRef(blob.Algorithm, blob.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err := domain.ParseContentBlobRef(blob.Algorithm, blob.Digest[:62]); err == nil {
		t.Fatal("不完整 digest 被接受")
	}

	generator := identity.Generator{Clock: clock.Fixed{Time: time.Now()}, Random: bytes.NewReader(make([]byte, 10))}
	sourceID, err := generator.New(domain.IDSource)
	if err != nil {
		t.Fatal(err)
	}
	location, err := domain.NewFileLocationRef(sourceID, 1, "opaque-source-key")
	if err != nil {
		t.Fatal(err)
	}
	if location.LocationKey != "opaque-source-key" {
		t.Fatal("FileLocation 稳定键发生漂移")
	}
}
