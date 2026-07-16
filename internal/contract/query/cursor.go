package query

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/ports"
)

const SortProtocolVersion = 1

type CursorClaims struct {
	QueryFingerprint       string    `json:"queryFingerprint"`
	SortProtocolVersion    int       `json:"sortProtocolVersion"`
	QueryPublicationID     string    `json:"queryPublicationId"`
	AuthorizationScopeHash string    `json:"authorizationScopeHash"`
	LastSortKey            string    `json:"lastSortKey"`
	LastCanonicalWorkID    string    `json:"lastCanonicalWorkId"`
	IssuedAt               time.Time `json:"issuedAt"`
	LeaseID                string    `json:"leaseId"`
	ExpiresAt              time.Time `json:"expiresAt"`
}

type CursorSigner struct {
	key   []byte
	clock ports.Clock
}

func NewCursorSigner(key []byte, clock ports.Clock) (*CursorSigner, error) {
	if len(key) < 32 {
		return nil, fmt.Errorf("cursor HMAC key 至少需要 32 bytes")
	}
	if clock == nil {
		return nil, fmt.Errorf("cursor signer 缺少 Clock")
	}
	return &CursorSigner{key: append([]byte(nil), key...), clock: clock}, nil
}

func (s *CursorSigner) Issue(claims CursorClaims) (string, error) {
	if err := validateClaims(claims); err != nil {
		return "", fault.New(fault.CodeCursorInvalid, false, err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fault.New(fault.CodeCursorInvalid, false, err)
	}
	signature := s.sign(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (s *CursorSigner) Verify(token string) (CursorClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return CursorClaims{}, fault.New(fault.CodeCursorInvalid, false, nil)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return CursorClaims{}, fault.New(fault.CodeCursorInvalid, false, err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(signature, s.sign(payload)) {
		return CursorClaims{}, fault.New(fault.CodeCursorInvalid, false, err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	var claims CursorClaims
	if err := decoder.Decode(&claims); err != nil {
		return CursorClaims{}, fault.New(fault.CodeCursorInvalid, false, err)
	}
	if err := validateClaims(claims); err != nil {
		return CursorClaims{}, fault.New(fault.CodeCursorInvalid, false, err)
	}
	if !s.clock.Now().Before(claims.ExpiresAt) {
		return CursorClaims{}, fault.New(fault.CodeCursorExpired, true, nil)
	}
	return claims, nil
}

func (s *CursorSigner) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}

func validateClaims(claims CursorClaims) error {
	if claims.SortProtocolVersion != SortProtocolVersion {
		return fmt.Errorf("sort protocol version 不匹配")
	}
	if !isLowerHexSHA256(claims.QueryFingerprint) || !isLowerHexSHA256(claims.AuthorizationScopeHash) {
		return fmt.Errorf("cursor fingerprint 必须是小写完整 SHA-256")
	}
	if _, err := domain.ParseID(domain.IDQueryPublication, claims.QueryPublicationID); err != nil {
		return fmt.Errorf("query publication ID 无效: %w", err)
	}
	if _, err := domain.ParseID(domain.IDCanonicalWork, claims.LastCanonicalWorkID); err != nil {
		return fmt.Errorf("last work ID 无效: %w", err)
	}
	if claims.LastSortKey == "" || len(claims.LastSortKey) > 8192 || claims.LeaseID == "" || len(claims.LeaseID) > 128 {
		return fmt.Errorf("cursor 必需字段为空")
	}
	if claims.IssuedAt.IsZero() || !claims.ExpiresAt.After(claims.IssuedAt) {
		return fmt.Errorf("cursor 租约时间无效")
	}
	return nil
}

func isLowerHexSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
