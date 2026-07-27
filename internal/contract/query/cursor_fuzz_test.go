package query_test

// 游标是**授权范围的载体**：CursorClaims 里的 authorizationScopeHash 与
// queryFingerprint 决定续页时服务端按哪一组 Source、哪一份 publication 继续返回行。
// 因此 Verify 的正确性不是分页体验问题，而是授权问题：任何被接受的游标都必须是本
// 服务用自己的 HMAC key 签发过的原件。
//
// 本文件锁定两条不变量：
//
//  1. **签名不匹配必须在解码 payload 之前被拒**（cursor.go 当前 :88 早于 :94）。
//     这条顺序不是风格问题：payload 完全由攻击者控制，一旦先解码再验签，攻击者就能
//     用未验签的内容驱动服务端行为——最直接的可观测后果是错误码本身会泄漏
//     payload 内容（伪造一个协议版本过时的 claims 就能把不可重试的 CURSOR_INVALID
//     变成可重试的 CURSOR_EXPIRED），更远的后果是任何后续重构都可能顺着这个口子把
//     未验签字段喂进授权判定。fuzz 在这里的作用是**防止将来的重构改坏顺序**。
//
//  2. Verify 的接受集恰好等于独立重算的接受集（完整 oracle），且 Issue/Verify 往返
//     稳定。

import (
	"bytes"
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
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
)

// fuzzCursorKey 是本文件固定的 HMAC key。测试**知道** key，因此可以独立重算签名，
// 把 Verify 当作黑盒对拍，而不是只断言"篡改会被拒"。
var fuzzCursorKey = []byte(strings.Repeat("K", 32))

// fuzzCursorNow 是固定时钟。租约过期属于 Verify 的判定之一，必须可复现。
var fuzzCursorNow = time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)

func newFuzzCursorSigner(t testing.TB) *query.CursorSigner {
	t.Helper()
	signer, err := query.NewCursorSigner(fuzzCursorKey, clock.Fixed{Time: fuzzCursorNow})
	if err != nil {
		t.Fatalf("构造 CursorSigner: %v", err)
	}
	return signer
}

func fuzzCursorSign(payload []byte) []byte {
	mac := hmac.New(sha256.New, fuzzCursorKey)
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}

func fuzzCursorToken(payload, signature []byte) string {
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(signature)
}

func fuzzValidCursorClaims() query.CursorClaims {
	return query.CursorClaims{
		QueryFingerprint:       strings.Repeat("a", 64),
		SortProtocolVersion:    query.SortProtocolVersion,
		RankProtocolVersion:    query.RankProtocolVersion,
		QueryPublicationID:     "qpub_018f47d2-5c16-7a44-a8a0-000000000001",
		AuthorizationScopeHash: strings.Repeat("b", 64),
		LastSortKey:            "opaque-sort-key",
		LastRankTier:           0,
		LastCanonicalWorkID:    "wrk_018f47d2-5c16-7a44-a8a0-000000000002",
		IssuedAt:               fuzzCursorNow,
		LeaseID:                "lease-1",
		ExpiresAt:              fuzzCursorNow.Add(time.Minute),
	}
}

// FuzzCursorVerifyRejectsBadMACBeforeDecode 是顺序不变量的守门测试。
//
// 它对**任意** payload 字节与**任意**签名字节构造 token。只要签名不等于本服务重算的
// HMAC，Verify 就必须给出完全一致的回答：不可重试的 CURSOR_INVALID。回答里不允许
// 出现任何随 payload 内容变化的成分——特别是不允许出现 CURSOR_EXPIRED，因为那个码只
// 可能来自解码后的协议版本判定或租约判定，它出现即证明未验签的 payload 已经被解读过。
func FuzzCursorVerifyRejectsBadMACBeforeDecode(f *testing.F) {
	addCursorMACSeeds(f)
	signer := newFuzzCursorSigner(f)

	f.Fuzz(func(t *testing.T, payload, signature []byte) {
		if bytes.Equal(signature, fuzzCursorSign(payload)) {
			// 恰好命中正确签名（种子里有意包含这种情形）：交给 oracle 分支处理，
			// 这里只要求它不 panic。
			_, _ = signer.Verify(fuzzCursorToken(payload, signature))
			return
		}
		token := fuzzCursorToken(payload, signature)
		claims, err := signer.Verify(token)
		if err == nil {
			t.Fatalf("签名不匹配却通过校验: payload=%d bytes signature=%d bytes", len(payload), len(signature))
		}
		if claims != (query.CursorClaims{}) {
			t.Fatalf("失败路径必须返回零值 claims，实际 %+v", claims)
		}
		var structured *fault.Error
		if !errors.As(err, &structured) {
			t.Fatalf("拒绝必须是结构化错误，实际 %v", err)
		}
		if structured.Code != fault.CodeCursorInvalid {
			t.Fatalf("签名不匹配必须一律映射为 CURSOR_INVALID，实际 %s"+
				"（出现其他码说明未验签的 payload 已经被解读，验签顺序被改坏）", structured.Code)
		}
		if structured.Retryable {
			t.Fatalf("签名不匹配不得标记为可重试：客户端重试同一个伪造游标只会重复失败")
		}
	})
}

// FuzzCursorVerifyMatchesIndependentOracle 用独立重算的接受集对拍 Verify。
//
// oracle 不是 Verify 的复制品，而是按 cursor.go 的**契约**重写的判定：token 形状、
// 签名、严格解码、协议版本、结构合法性、租约未过期。任何一处放宽（例如接受大写
// hex 指纹、接受 ExpiresAt == IssuedAt、丢掉 DisallowUnknownFields）都会让两边分叉。
func FuzzCursorVerifyMatchesIndependentOracle(f *testing.F) {
	addCursorTokenSeeds(f)
	signer := newFuzzCursorSigner(f)

	f.Fuzz(func(t *testing.T, token string) {
		claims, err := signer.Verify(token)
		wantAccept, wantCode := cursorOracle(token)

		if err == nil {
			if !wantAccept {
				t.Fatalf("Verify 接受了 oracle 拒绝的游标（期望 %s）: %q", wantCode, truncateToken(token))
			}
			// 被接受的 claims 必须能重新签发，且重签结果必须**收敛**：再 Verify 一次
			// 得到同一组 claims，再 Issue 一次逐字节相同。
			//
			// 这里不能要求 `reissued == token`。fuzz 已经证伪了那条更强的性质：
			// base64 层是可锻的（见 TestCursorTokenEncodingIsMalleable），同一组
			// claims 对应无穷多个 Verify 都接受的 token 字符串。真正的契约是
			// **规范形态存在且稳定**，而不是"输入即规范形态"。
			reissued, issueErr := signer.Issue(claims)
			if issueErr != nil {
				t.Fatalf("Verify 接受的 claims 无法重新签发: %v", issueErr)
			}
			reverified, verifyErr := signer.Verify(reissued)
			if verifyErr != nil {
				t.Fatalf("重新签发的游标无法通过校验: %v", verifyErr)
			}
			if reverified != claims {
				t.Fatalf("Verify/Issue 往返改变了 claims\n原: %+v\n往返后: %+v", claims, reverified)
			}
			settled, issueErr := signer.Issue(reverified)
			if issueErr != nil {
				t.Fatalf("二次签发失败: %v", issueErr)
			}
			if settled != reissued {
				t.Fatalf("签发不是不动点\n第一次: %q\n第二次: %q",
					truncateToken(reissued), truncateToken(settled))
			}
			return
		}

		if wantAccept {
			t.Fatalf("Verify 拒绝了 oracle 接受的游标: %v: %q", err, truncateToken(token))
		}
		var structured *fault.Error
		if !errors.As(err, &structured) {
			t.Fatalf("拒绝必须是结构化错误，实际 %v", err)
		}
		if structured.Code != wantCode {
			t.Fatalf("错误码分类与 oracle 不符：实际 %s 期望 %s: %q",
				structured.Code, wantCode, truncateToken(token))
		}
		// CURSOR_EXPIRED 必须可重试（客户端从第一页刷新即可恢复），
		// CURSOR_INVALID 必须不可重试。这条映射是规范/06 明写的客户端契约。
		if structured.Code == fault.CodeCursorExpired && !structured.Retryable {
			t.Fatalf("CURSOR_EXPIRED 必须可重试: %q", truncateToken(token))
		}
		if structured.Code == fault.CodeCursorInvalid && structured.Retryable {
			t.Fatalf("CURSOR_INVALID 不得可重试: %q", truncateToken(token))
		}
	})
}

// cursorOracle 独立判定一个 token 是否应当被接受，以及被拒时应当归到哪个码。
// 判定顺序刻意与契约一致：签名先于任何对 payload 内容的解读。
func cursorOracle(token string) (bool, fault.Code) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return false, fault.CodeCursorInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false, fault.CodeCursorInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(signature, fuzzCursorSign(payload)) {
		return false, fault.CodeCursorInvalid
	}

	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var claims query.CursorClaims
	if err := decoder.Decode(&claims); err != nil {
		return false, fault.CodeCursorInvalid
	}
	// 协议版本不匹配 → 可重试的 CURSOR_EXPIRED（客户端拿的是签发时合法的游标）。
	if claims.SortProtocolVersion != query.SortProtocolVersion ||
		claims.RankProtocolVersion != query.RankProtocolVersion {
		return false, fault.CodeCursorExpired
	}
	if !cursorClaimsStructurallyValid(claims) {
		return false, fault.CodeCursorInvalid
	}
	if !fuzzCursorNow.Before(claims.ExpiresAt) {
		return false, fault.CodeCursorExpired
	}
	return true, ""
}

func cursorClaimsStructurallyValid(claims query.CursorClaims) bool {
	if claims.LastRankTier < 0 || claims.LastRankTier > query.MaxRankTier {
		return false
	}
	if !isLowerHexSHA256Oracle(claims.QueryFingerprint) || !isLowerHexSHA256Oracle(claims.AuthorizationScopeHash) {
		return false
	}
	if _, err := domain.ParseID(domain.IDQueryPublication, claims.QueryPublicationID); err != nil {
		return false
	}
	if _, err := domain.ParseID(domain.IDCanonicalWork, claims.LastCanonicalWorkID); err != nil {
		return false
	}
	if claims.LastSortKey == "" || len(claims.LastSortKey) > 8192 {
		return false
	}
	if claims.LeaseID == "" || len(claims.LeaseID) > 128 {
		return false
	}
	if claims.IssuedAt.IsZero() || !claims.ExpiresAt.After(claims.IssuedAt) {
		return false
	}
	return true
}

func isLowerHexSHA256Oracle(value string) bool {
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

// TestCursorTokenEncodingIsMalleable 记录 fuzz 实测发现的**当前行为**：游标 token 不是
// 规范形态，同一组 claims 对应无穷多个 Verify 都接受的 token 字符串。
//
// 成因在 base64 层，不在 HMAC 层：
//
//   - Go 的 base64 解码器**静默忽略** `\r` 与 `\n`，因此可以在两段 base64 的任意位置
//     插入换行而不改变解码结果；
//   - Go 的 base64 解码器默认**不校验尾部填充位为零**（非 Strict 模式），因此 32 字节
//     签名的最后一个 base64 字符存在多个等价写法。
//
// 两者都不改变解码后的 payload，所以 HMAC 照常匹配、claims 照常合法——**授权语义不受
// 影响，这不是越权**。风险是"token 字符串不能当作身份"：cursor 经
// `r.URL.Query().Get("cursor")` 取得，攻击者用 `%0D` 就能注入换行，于是任何以 token
// 原文为键的缓存、限流、审计去重或重放检测都会被绕开。当前实现没有这类逻辑（租约以
// HMAC 保护的 claims.LeaseID 为准），因此登记为待修的健壮性缺陷而不是安全门禁。
//
// 修法是把 Verify 收紧为"解码后重新编码必须逐字节等于输入"，或改用
// base64.RawURLEncoding.Strict() 并显式拒绝 `\r`/`\n`。修好后本测试会失败并提示改写。
func TestCursorTokenEncodingIsMalleable(t *testing.T) {
	signer := newFuzzCursorSigner(t)
	token, err := signer.Issue(fuzzValidCursorClaims())
	if err != nil {
		t.Fatalf("签发基准游标: %v", err)
	}
	baseline, err := signer.Verify(token)
	if err != nil {
		t.Fatalf("基准游标必须通过校验: %v", err)
	}

	variants := map[string]string{
		"payload 中插入 CR":   token[:10] + "\r" + token[10:],
		"payload 中插入 LF":   token[:10] + "\n" + token[10:],
		"payload 中插入 CRLF": token[:10] + "\r\n" + token[10:],
		"签名段中插入 CR":        insertIntoSignatureSegment(t, token, "\r"),
		"签名段中插入 LF":        insertIntoSignatureSegment(t, token, "\n"),
		"首尾包裹换行":           "\n" + token + "\n",
	}

	var accepted []string
	for name, variant := range variants {
		if variant == token {
			continue
		}
		claims, verifyErr := signer.Verify(variant)
		if verifyErr != nil {
			continue
		}
		if claims != baseline {
			t.Fatalf("%s：被接受的变体解出了不同的 claims，这已经越出可锻性范畴\n原: %+v\n变体: %+v",
				name, baseline, claims)
		}
		accepted = append(accepted, name)
	}

	if len(accepted) == 0 {
		t.Fatalf("游标 token 已经是规范形态，请把本测试改写成正向断言")
	}
	sortStrings(accepted)
	t.Logf("已知缺陷：下列与签发结果**不同字节**的 token 变体全部通过校验，且解出同一组 claims：%v", accepted)
	t.Logf("成因：base64 解码忽略 CR/LF；cursor 取自 URL query，%%0D/%%0A 可直接注入")
}

// insertIntoSignatureSegment 在 `.` 之后（签名段内部）插入给定字符。
func insertIntoSignatureSegment(t *testing.T, token, insert string) string {
	t.Helper()
	dot := strings.IndexByte(token, '.')
	if dot < 0 || dot+3 >= len(token) {
		t.Fatalf("token 形状异常: %q", truncateToken(token))
	}
	return token[:dot+3] + insert + token[dot+3:]
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// TestCursorVerifyRejectsEverySingleBitFlip 是顺序不变量的定向补充：fuzz 很难自己
// 撞出"只差一个比特"的 token，但那正是重构改坏验签顺序后最先失守的形态。
func TestCursorVerifyRejectsEverySingleBitFlip(t *testing.T) {
	signer := newFuzzCursorSigner(t)
	token, err := signer.Issue(fuzzValidCursorClaims())
	if err != nil {
		t.Fatalf("签发基准游标: %v", err)
	}
	if _, err := signer.Verify(token); err != nil {
		t.Fatalf("基准游标必须通过校验: %v", err)
	}

	payload, signature := splitCursorToken(t, token)
	for index := range payload {
		for bit := 0; bit < 8; bit++ {
			flipped := append([]byte(nil), payload...)
			flipped[index] ^= 1 << bit
			assertCursorRejectedAsInvalid(t, signer, fuzzCursorToken(flipped, signature))
		}
	}
	for index := range signature {
		for bit := 0; bit < 8; bit++ {
			flipped := append([]byte(nil), signature...)
			flipped[index] ^= 1 << bit
			assertCursorRejectedAsInvalid(t, signer, fuzzCursorToken(payload, flipped))
		}
	}
	t.Logf("payload %d 字节 + 签名 %d 字节，共 %d 处单比特翻转全部被拒为不可重试的 CURSOR_INVALID",
		len(payload), len(signature), (len(payload)+len(signature))*8)
}

func assertCursorRejectedAsInvalid(t *testing.T, signer *query.CursorSigner, token string) {
	t.Helper()
	_, err := signer.Verify(token)
	if err == nil {
		t.Fatalf("单比特翻转后的游标通过了校验: %q", truncateToken(token))
	}
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeCursorInvalid || structured.Retryable {
		t.Fatalf("单比特翻转必须映射为不可重试的 CURSOR_INVALID，实际 %v: %q", err, truncateToken(token))
	}
}

func splitCursorToken(t *testing.T, token string) (payload, signature []byte) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		t.Fatalf("token 形状异常: %q", truncateToken(token))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("解码 payload: %v", err)
	}
	signature, err = base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("解码签名: %v", err)
	}
	return payload, signature
}

func truncateToken(token string) string {
	const limit = 200
	if len(token) <= limit {
		return token
	}
	return token[:limit] + "…"
}

// ---------- 种子语料 ----------

func addCursorMACSeeds(f *testing.F) {
	f.Helper()
	validPayload, err := json.Marshal(fuzzValidCursorClaims())
	if err != nil {
		f.Fatalf("构造基准 payload: %v", err)
	}
	// 正确签名（走 oracle 分支），以及各种错误签名。
	f.Add(validPayload, fuzzCursorSign(validPayload))
	f.Add(validPayload, []byte(nil))
	f.Add(validPayload, []byte{0})
	f.Add(validPayload, bytes.Repeat([]byte{0}, 32))
	f.Add(validPayload, bytes.Repeat([]byte{0xff}, 32))
	f.Add(validPayload, append(append([]byte(nil), fuzzCursorSign(validPayload)...), 0))
	f.Add(validPayload, fuzzCursorSign(validPayload)[:31])

	// payload 内容刻意构造成"若先解码就会走出不同错误码"的形态：过期协议版本、
	// 已过期租约、超长字段。任何一个在坏签名下泄漏出 CURSOR_EXPIRED 都说明顺序错了。
	staleVersion := fuzzValidCursorClaims()
	staleVersion.SortProtocolVersion = query.SortProtocolVersion + 1
	stalePayload, _ := json.Marshal(staleVersion)
	f.Add(stalePayload, bytes.Repeat([]byte{0}, 32))

	staleRank := fuzzValidCursorClaims()
	staleRank.RankProtocolVersion = query.RankProtocolVersion - 1
	staleRankPayload, _ := json.Marshal(staleRank)
	f.Add(staleRankPayload, bytes.Repeat([]byte{0}, 32))

	expired := fuzzValidCursorClaims()
	expired.IssuedAt = fuzzCursorNow.Add(-2 * time.Hour)
	expired.ExpiresAt = fuzzCursorNow.Add(-time.Hour)
	expiredPayload, _ := json.Marshal(expired)
	f.Add(expiredPayload, bytes.Repeat([]byte{0}, 32))

	f.Add([]byte(`{}`), bytes.Repeat([]byte{0}, 32))
	f.Add([]byte(``), []byte(nil))
	f.Add([]byte(`not json`), bytes.Repeat([]byte{0}, 32))
	f.Add([]byte(`{"unknownField":1}`), bytes.Repeat([]byte{0}, 32))
	f.Add(bytes.Repeat([]byte("["), 4096), bytes.Repeat([]byte{0}, 32))
}

func addCursorTokenSeeds(f *testing.F) {
	f.Helper()
	claims := fuzzValidCursorClaims()
	validPayload, err := json.Marshal(claims)
	if err != nil {
		f.Fatalf("构造基准 payload: %v", err)
	}
	validSignature := fuzzCursorSign(validPayload)

	f.Add(fuzzCursorToken(validPayload, validSignature))
	f.Add("")
	f.Add(".")
	f.Add("..")
	f.Add("a.b.c")
	f.Add("a")
	f.Add("!.!")
	f.Add("=.=")
	f.Add(base64.RawURLEncoding.EncodeToString(validPayload))
	f.Add(base64.RawURLEncoding.EncodeToString(validPayload) + ".")
	f.Add("." + base64.RawURLEncoding.EncodeToString(validSignature))
	// 标准 base64（带 padding / 带 + 与 /）必须被拒：RawURLEncoding 不接受它们。
	f.Add(base64.StdEncoding.EncodeToString(validPayload) + "." +
		base64.StdEncoding.EncodeToString(validSignature))

	// 结构非法但签名正确的各种 claims：逐字段越界，覆盖 validateClaims 的每个分支。
	for _, mutate := range []func(*query.CursorClaims){
		func(c *query.CursorClaims) { c.LastRankTier = -1 },
		func(c *query.CursorClaims) { c.LastRankTier = query.MaxRankTier },
		func(c *query.CursorClaims) { c.LastRankTier = query.MaxRankTier + 1 },
		func(c *query.CursorClaims) { c.QueryFingerprint = strings.Repeat("A", 64) },
		func(c *query.CursorClaims) { c.QueryFingerprint = strings.Repeat("a", 63) },
		func(c *query.CursorClaims) { c.QueryFingerprint = strings.Repeat("g", 64) },
		func(c *query.CursorClaims) { c.AuthorizationScopeHash = "" },
		func(c *query.CursorClaims) { c.QueryPublicationID = "wrk_018f47d2-5c16-7a44-a8a0-000000000001" },
		func(c *query.CursorClaims) { c.QueryPublicationID = "" },
		func(c *query.CursorClaims) { c.LastCanonicalWorkID = "qpub_018f47d2-5c16-7a44-a8a0-000000000002" },
		func(c *query.CursorClaims) { c.LastSortKey = "" },
		func(c *query.CursorClaims) { c.LastSortKey = strings.Repeat("s", 8192) },
		func(c *query.CursorClaims) { c.LastSortKey = strings.Repeat("s", 8193) },
		func(c *query.CursorClaims) { c.LeaseID = "" },
		func(c *query.CursorClaims) { c.LeaseID = strings.Repeat("l", 128) },
		func(c *query.CursorClaims) { c.LeaseID = strings.Repeat("l", 129) },
		func(c *query.CursorClaims) { c.IssuedAt = time.Time{} },
		func(c *query.CursorClaims) { c.ExpiresAt = c.IssuedAt },
		func(c *query.CursorClaims) { c.ExpiresAt = c.IssuedAt.Add(-time.Second) },
		func(c *query.CursorClaims) { c.ExpiresAt = fuzzCursorNow },
		func(c *query.CursorClaims) { c.ExpiresAt = fuzzCursorNow.Add(time.Nanosecond) },
		func(c *query.CursorClaims) { c.SortProtocolVersion = 0 },
		func(c *query.CursorClaims) { c.SortProtocolVersion = query.SortProtocolVersion + 1 },
		func(c *query.CursorClaims) { c.RankProtocolVersion = query.RankProtocolVersion - 1 },
		func(c *query.CursorClaims) { c.RankProtocolVersion = query.RankProtocolVersion + 1 },
	} {
		mutated := fuzzValidCursorClaims()
		mutate(&mutated)
		payload, marshalErr := json.Marshal(mutated)
		if marshalErr != nil {
			f.Fatalf("构造变异 payload: %v", marshalErr)
		}
		f.Add(fuzzCursorToken(payload, fuzzCursorSign(payload)))
	}

	// 未知字段与重复字段：DisallowUnknownFields 必须生效，且必须发生在验签之后。
	for _, raw := range []string{
		`{"queryFingerprint":"` + strings.Repeat("a", 64) + `","extra":1}`,
		`{}`,
		`null`,
		`[]`,
		`"string"`,
		`123`,
		`{"lastRankTier":1e400}`,
		`{"issuedAt":"not-a-time"}`,
		`{"issuedAt":"2026-07-27T12:00:00Z","expiresAt":"2026-07-27T12:00:00Z"}`,
	} {
		payload := []byte(raw)
		f.Add(fuzzCursorToken(payload, fuzzCursorSign(payload)))
	}
}
