package httpapi

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
)

const validJobCursorID = "job_018f47d2-5c16-7a44-a8a0-000000000001"

func TestJobListCursorRequiresCanonicalPayloadAndExpiresByVersion(t *testing.T) {
	value := jobListCursor{
		Version:          jobListCursorVersion,
		QueryFingerprint: strings.Repeat("a", 64),
		CreatedAt:        1_700_000_000,
		JobID:            validJobCursorID,
	}
	token, err := encodeJobListCursor(value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeJobListCursor(token)
	if err != nil || decoded != value {
		t.Fatalf("规范 Job cursor 未往返: %+v err=%v", decoded, err)
	}

	for name, raw := range map[string]string{
		"空白非规范": ` {"version":1,"queryFingerprint":"` + strings.Repeat("a", 64) + `","createdAt":1700000000,"jobId":"` + validJobCursorID + `"}`,
		"未知字段":  `{"version":1,"queryFingerprint":"` + strings.Repeat("a", 64) + `","createdAt":1700000000,"jobId":"` + validJobCursorID + `","extra":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeJobListCursor(base64.RawURLEncoding.EncodeToString([]byte(raw)))
			assertCursorFault(t, err, fault.CodeCursorInvalid)
		})
	}
	expired, err := encodeJobListCursor(jobListCursor{
		Version:          jobListCursorVersion + 1,
		QueryFingerprint: strings.Repeat("a", 64),
		CreatedAt:        value.CreatedAt,
		JobID:            value.JobID,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = decodeJobListCursor(expired)
	assertCursorFault(t, err, fault.CodeCursorExpired)
}

func assertCursorFault(t *testing.T, err error, code fault.Code) {
	t.Helper()
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != code {
		t.Fatalf("cursor fault=%v，期望 %s", err, code)
	}
}
