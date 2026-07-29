package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"

	"github.com/RecRivenVI/gallery/internal/auth"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/jobs"
)

const jobListCursorVersion = 1

var jobListCursorEncoding = base64.RawURLEncoding.Strict()

type jobListCursor struct {
	Version          int    `json:"version"`
	QueryFingerprint string `json:"queryFingerprint"`
	CreatedAt        int64  `json:"createdAt"`
	JobID            string `json:"jobId"`
}

func jobListFingerprint(status jobs.Status, limit int, session auth.Session) string {
	capabilities := append([]string(nil), session.Capabilities...)
	sort.Strings(capabilities)
	payload, _ := json.Marshal(struct {
		Status          jobs.Status          `json:"status"`
		Limit           int                  `json:"limit"`
		SessionID       string               `json:"sessionId"`
		PrincipalID     string               `json:"principalId"`
		SecurityVersion int64                `json:"securityVersion"`
		TokenID         string               `json:"tokenId"`
		TokenScopes     []auth.ResourceScope `json:"tokenScopes"`
		Capabilities    []string             `json:"capabilities"`
	}{status, limit, session.ID, session.PrincipalID, session.SecurityVersion, session.TokenID, session.TokenScopes, capabilities})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func encodeJobListCursor(value jobListCursor) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fault.New(fault.CodeInternal, true, err)
	}
	return jobListCursorEncoding.EncodeToString(payload), nil
}

func decodeJobListCursor(cursor string) (jobListCursor, error) {
	if len(cursor) == 0 || len(cursor) > 4096 {
		return jobListCursor{}, fault.New(fault.CodeCursorInvalid, false, nil)
	}
	raw, err := jobListCursorEncoding.DecodeString(cursor)
	if err != nil || jobListCursorEncoding.EncodeToString(raw) != cursor {
		return jobListCursor{}, fault.New(fault.CodeCursorInvalid, false, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var result jobListCursor
	if err := decoder.Decode(&result); err != nil {
		return jobListCursor{}, fault.New(fault.CodeCursorInvalid, false, err)
	}
	if err := ensureJobCursorEOF(decoder); err != nil {
		return jobListCursor{}, fault.New(fault.CodeCursorInvalid, false, err)
	}
	canonical, err := json.Marshal(result)
	if err != nil || !bytes.Equal(canonical, raw) {
		return jobListCursor{}, fault.New(fault.CodeCursorInvalid, false, err)
	}
	if result.Version != jobListCursorVersion {
		return jobListCursor{}, fault.New(fault.CodeCursorExpired, true, nil)
	}
	if result.CreatedAt < 0 || len(result.QueryFingerprint) != sha256.Size*2 {
		return jobListCursor{}, fault.New(fault.CodeCursorInvalid, false, nil)
	}
	if _, err := hex.DecodeString(result.QueryFingerprint); err != nil || result.QueryFingerprint != string(bytes.ToLower([]byte(result.QueryFingerprint))) {
		return jobListCursor{}, fault.New(fault.CodeCursorInvalid, false, err)
	}
	if _, err := domain.ParseID(domain.IDJob, result.JobID); err != nil {
		return jobListCursor{}, fault.New(fault.CodeCursorInvalid, false, err)
	}
	return result, nil
}

func ensureJobCursorEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("cursor 含多个 JSON 值")
	}
	return err
}
