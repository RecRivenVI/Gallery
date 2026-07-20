package query_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/contract/query"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
)

func validClaims(now time.Time) query.CursorClaims {
	return query.CursorClaims{
		QueryFingerprint: strings.Repeat("a", 64), SortProtocolVersion: query.SortProtocolVersion,
		RankProtocolVersion:    query.RankProtocolVersion,
		QueryPublicationID:     "qpub_018f47d2-5c16-7a44-a8a0-000000000001",
		AuthorizationScopeHash: strings.Repeat("b", 64), LastSortKey: "opaque-sort-key", LastRankTier: 0,
		LastCanonicalWorkID: "wrk_018f47d2-5c16-7a44-a8a0-000000000002",
		IssuedAt:            now, LeaseID: "lease-1", ExpiresAt: now.Add(time.Minute),
	}
}

func TestCursorSigningTamperAndExpiry(t *testing.T) {
	now := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	signer, err := query.NewCursorSigner([]byte(strings.Repeat("k", 32)), clock.Fixed{Time: now})
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.Issue(validClaims(now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signer.Verify(token); err != nil {
		t.Fatalf("有效游标被拒绝: %v", err)
	}
	parts := strings.Split(token, ".")
	tampered := "A" + parts[0][1:] + "." + parts[1]
	var structured *fault.Error
	if err := func() error { _, err := signer.Verify(tampered); return err }(); !errors.As(err, &structured) || structured.Code != fault.CodeCursorInvalid {
		t.Fatalf("篡改游标错误 = %v", err)
	}

	expiredSigner, _ := query.NewCursorSigner([]byte(strings.Repeat("k", 32)), clock.Fixed{Time: now.Add(2 * time.Minute)})
	if err := func() error { _, err := expiredSigner.Verify(token); return err }(); !errors.As(err, &structured) || structured.Code != fault.CodeCursorExpired {
		t.Fatalf("过期游标错误 = %v", err)
	}
}

func TestCursorJSONSchemaMatchesClaims(t *testing.T) {
	validator, err := query.NewCursorValidator()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	data := []byte(`{"queryFingerprint":"` + strings.Repeat("a", 64) + `","sortProtocolVersion":1,"rankProtocolVersion":2,"queryPublicationId":"qpub_018f47d2-5c16-7a44-a8a0-000000000001","authorizationScopeHash":"` + strings.Repeat("b", 64) + `","lastSortKey":"key","lastRankTier":0,"lastCanonicalWorkId":"wrk_018f47d2-5c16-7a44-a8a0-000000000002","issuedAt":"` + now.Format(time.RFC3339) + `","leaseId":"lease","expiresAt":"` + now.Add(time.Minute).Format(time.RFC3339) + `"}`)
	if err := validator.ValidateJSON(data); err != nil {
		t.Fatal(err)
	}
}
