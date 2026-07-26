package query_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
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

// signedCursor 用与 CursorSigner 相同的方式手工签发游标，**绕过 Issue 的校验**。
//
// 必须绕过：Issue 对协议版本与结构都做校验（签发不合法的游标属服务端缺陷），因此无法用它构造本
// 测试需要的两类输入——签名有效但协议版本过时、签名有效但结构非法。用篡改签名代替也不行，那只会
// 走到签名校验分支，证明不了版本分支与结构分支各自的映射。
func signedCursor(t *testing.T, key []byte, claims query.CursorClaims) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, key)
	if _, err := mac.Write(payload); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// TestVerifySeparatesProtocolUpgradeFromMalformedCursor 锁定「协议升级」与「游标结构非法」在校验端
// 映射到**不同**错误码。
//
// 二者曾共用不可重试的 `CURSOR_INVALID`：协议一旦升级，每一个正在分页的客户端都会收到一个声称
// 「不可恢复」的错误，而它拿着的游标在签发时完全合法，正确做法只是从第一页重新开始。
// `规范/06` 明写「排序协议升级 → CURSOR_EXPIRED」，本测试使代码与规范不再分叉，也使将来为修排序键
// 而递增 SortProtocolVersion 时不会把不可重试错误发给所有正在分页的客户端。
func TestVerifySeparatesProtocolUpgradeFromMalformedCursor(t *testing.T) {
	now := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	key := []byte(strings.Repeat("k", 32))
	signer, err := query.NewCursorSigner(key, clock.Fixed{Time: now})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		name      string
		mutate    func(*query.CursorClaims)
		wantCode  fault.Code
		wantRetry bool
	}{
		{"排序协议升级", func(c *query.CursorClaims) { c.SortProtocolVersion = query.SortProtocolVersion + 1 },
			fault.CodeCursorExpired, true},
		{"ranking 协议升级", func(c *query.CursorClaims) { c.RankProtocolVersion = query.RankProtocolVersion + 1 },
			fault.CodeCursorExpired, true},
		{"rank tier 越界", func(c *query.CursorClaims) { c.LastRankTier = query.MaxRankTier + 1 },
			fault.CodeCursorInvalid, false},
		{"作品 ID 非法", func(c *query.CursorClaims) { c.LastCanonicalWorkID = "not-an-id" },
			fault.CodeCursorInvalid, false},
		{"排序键为空", func(c *query.CursorClaims) { c.LastSortKey = "" },
			fault.CodeCursorInvalid, false},
	} {
		t.Run(item.name, func(t *testing.T) {
			claims := validClaims(now)
			item.mutate(&claims)
			_, err := signer.Verify(signedCursor(t, key, claims))
			var structured *fault.Error
			if !errors.As(err, &structured) {
				t.Fatalf("未返回结构化错误: %v", err)
			}
			if structured.Code != item.wantCode {
				t.Fatalf("code = %q want %q", structured.Code, item.wantCode)
			}
			if structured.Retryable != item.wantRetry {
				t.Fatalf("retryable = %v want %v", structured.Retryable, item.wantRetry)
			}
		})
	}
}

// TestIssueRejectsUnimplementedProtocolVersion 保证放宽只发生在校验端：签发端仍拒绝产出协议版本与
// 本服务不符的游标，那属于服务端缺陷而不是客户端可以重试恢复的情形。
func TestIssueRejectsUnimplementedProtocolVersion(t *testing.T) {
	now := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	signer, err := query.NewCursorSigner([]byte(strings.Repeat("k", 32)), clock.Fixed{Time: now})
	if err != nil {
		t.Fatal(err)
	}
	claims := validClaims(now)
	claims.SortProtocolVersion = query.SortProtocolVersion + 1
	var structured *fault.Error
	if _, err := signer.Issue(claims); !errors.As(err, &structured) ||
		structured.Code != fault.CodeCursorInvalid || structured.Retryable {
		t.Fatalf("签发端未拒绝未实现的协议版本: %v", err)
	}
}
