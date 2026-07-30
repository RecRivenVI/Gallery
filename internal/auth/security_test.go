package auth_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/auth"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/storage"
	"golang.org/x/crypto/argon2"
)

func TestArgon2idPasswordFormatAndVerification(t *testing.T) {
	salt := bytes.Repeat([]byte{0x42}, 64)
	encoded, err := auth.HashPassword("correct horse battery staple", bytes.NewReader(salt))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=") || strings.Contains(encoded, "correct horse") {
		t.Fatalf("Argon2id 表达无效或泄露密码: %q", encoded)
	}
	valid, rehash, err := auth.VerifyPassword(encoded, "correct horse battery staple")
	if err != nil || !valid || rehash {
		t.Fatalf("正确密码验证失败: valid=%v rehash=%v err=%v", valid, rehash, err)
	}
	valid, _, err = auth.VerifyPassword(encoded, "wrong password")
	if err != nil || valid {
		t.Fatalf("错误密码被接受: valid=%v err=%v", valid, err)
	}
	if _, err := auth.HashPassword("short", bytes.NewReader(salt)); !errors.Is(err, auth.ErrPasswordInvalid) {
		t.Fatalf("过短密码错误 = %v", err)
	}
	malformed := []string{
		"", "$argon2id$v=19$m=19456,t=2,p=257$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=19$m=19456,m=19456,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=19$m=262145,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAA",
	}
	for _, encoded := range malformed {
		if _, _, err := auth.VerifyPassword(encoded, "correct horse battery staple"); !errors.Is(err, auth.ErrPasswordInvalid) {
			t.Fatalf("恶意 PHC 未被有界拒绝: %q err=%v", encoded, err)
		}
	}
	legacySalt := bytes.Repeat([]byte{0x21}, 16)
	legacyKey := argon2.IDKey([]byte("correct horse battery staple"), legacySalt, 1, 8*1024, 1, 32)
	legacy := fmt.Sprintf("$argon2id$v=19$m=8192,t=1,p=1$%s$%s",
		base64.RawStdEncoding.EncodeToString(legacySalt), base64.RawStdEncoding.EncodeToString(legacyKey))
	valid, rehash, err = auth.VerifyPassword(legacy, "correct horse battery staple")
	if err != nil || !valid || !rehash {
		t.Fatalf("旧 Argon2id 参数未触发重哈希: valid=%v rehash=%v err=%v", valid, rehash, err)
	}
	if _, err := auth.HashPassword(strings.Repeat("x", auth.PasswordMaxBytes+1), bytes.NewReader(salt)); !errors.Is(err, auth.ErrPasswordInvalid) {
		t.Fatalf("过长密码错误 = %v", err)
	}
}

func TestLANOwnerInitializationIsAtomicUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	manager, store, _ := newSecurityManager(t)
	const competitors = 6
	start := make(chan struct{})
	results := make(chan error, competitors)
	var wait sync.WaitGroup
	for i := 0; i < competitors; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, err := manager.InitializeLANOwner(ctx, auth.CreateUserInput{
				Username: "owner-" + string(rune('a'+index)), DisplayName: "Owner", Password: "owner-password-strong",
			})
			results <- err
		}(i)
	}
	close(start)
	wait.Wait()
	close(results)
	succeeded, alreadyInitialized := 0, 0
	for err := range results {
		switch faultCode(err) {
		case "":
			succeeded++
		case fault.CodeLANAlreadyInitialized:
			alreadyInitialized++
		default:
			t.Fatalf("并发初始化返回意外错误: %v", err)
		}
	}
	if succeeded != 1 || alreadyInitialized != competitors-1 {
		t.Fatalf("并发初始化结果 success=%d already=%d", succeeded, alreadyInitialized)
	}
	var owners int
	if err := store.Control.SQL().QueryRowContext(ctx, "SELECT count(*) FROM local_users").Scan(&owners); err != nil || owners != 1 {
		t.Fatalf("初始化留下非单一 Owner: count=%d err=%v", owners, err)
	}
}

func TestLANOwnerUserGrantTokenAndRevocationLifecycle(t *testing.T) {
	ctx := context.Background()
	manager, store, manual := newSecurityManager(t)
	owner, err := manager.InitializeLANOwner(ctx, auth.CreateUserInput{
		Username: "Owner", DisplayName: "LAN Owner", Password: "owner-password-strong",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.InitializeLANOwner(ctx, auth.CreateUserInput{
		Username: "Second", DisplayName: "Second Owner", Password: "another-password-strong",
	}); faultCode(err) != fault.CodeLANAlreadyInitialized {
		t.Fatalf("重复 Owner 初始化错误 = %v", err)
	}

	ownerSession, _, err := manager.Login(ctx, "owner", "owner-password-strong", "test", "loopback")
	if err != nil {
		t.Fatal(err)
	}
	if ownerSession.PrincipalID != owner.ID || !auth.HasCapability(ownerSession, "users.manage") {
		t.Fatalf("Owner Session 能力错误: %+v", ownerSession)
	}

	libraryID := "lib_00000000-0000-7000-8000-000000000001"
	if _, err := store.Control.SQL().ExecContext(ctx, `INSERT INTO libraries (library_id, name, created_at) VALUES (?, 'Security', ?)`, libraryID, manual.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	viewer, err := manager.CreateUser(ctx, owner.ID, auth.CreateUserInput{
		Username: "Viewer", DisplayName: "Scoped Viewer", Password: "viewer-password-strong", Roles: []string{"viewer"},
		Grants: []auth.GrantInput{
			{Effect: "allow", Capability: "library.read", Scope: auth.ResourceScope{Kind: "library", ID: libraryID}},
			// Token 自助管理是显式全局账户能力；Token 的业务能力仍受下面的资源 scope 限制。
			{Effect: "allow", Capability: "tokens.manage", Scope: auth.ResourceScope{Kind: "global"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	grantPage, err := manager.ListGrants(ctx, owner.ID, viewer.ID, "", 50)
	if err != nil || len(grantPage.Items) != 2 {
		t.Fatalf("Viewer Grant 集合无效: count=%d err=%v", len(grantPage.Items), err)
	}
	viewerSession, viewerCookie, err := manager.Login(ctx, "VIEWER", "viewer-password-strong", "browser", "loopback")
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := manager.AuthorizeSession(ctx, viewerSession, "library.read", auth.ResourceScope{Kind: "library", ID: libraryID})
	if err != nil || !allowed {
		t.Fatalf("Library grant 未生效: allowed=%v err=%v", allowed, err)
	}
	allowed, err = manager.AuthorizeSession(ctx, viewerSession, "library.read", auth.ResourceScope{Kind: "library", ID: "lib_00000000-0000-7000-8000-000000000002"})
	if err != nil || allowed {
		t.Fatalf("Viewer 越过 Library scope: allowed=%v err=%v", allowed, err)
	}

	expires := manual.Now().Add(time.Hour)
	created, err := manager.CreateAPIToken(ctx, viewerSession, "automation", []string{"library.read"},
		[]auth.ResourceScope{{Kind: "library", ID: libraryID}}, &expires)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(created.Token.SecretPrefix, created.Secret) {
		t.Fatal("Token 摘要错误包含完整 secret")
	}
	var storedHash string
	if err := store.Control.SQL().QueryRowContext(ctx, "SELECT secret_hash FROM api_tokens WHERE token_id=?", created.Token.ID).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(storedHash, created.Secret) || len(storedHash) != 64 {
		t.Fatalf("数据库 Token 验证材料不安全: %q", storedHash)
	}
	tokenIdentity, err := manager.AuthenticateAPIToken(ctx, created.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if !manager.IsActive(ctx, created.Token.ID) {
		t.Fatal("有效 API Token 未被 WS active 检查识别")
	}
	allowed, err = manager.AuthorizeSession(ctx, tokenIdentity, "library.read", auth.ResourceScope{Kind: "library", ID: libraryID})
	if err != nil || !allowed {
		t.Fatalf("限定 Token 未获授权: allowed=%v err=%v", allowed, err)
	}
	allowed, err = manager.AuthorizeSession(ctx, tokenIdentity, "library.read", auth.ResourceScope{Kind: "global"})
	if err != nil || allowed {
		t.Fatalf("限定 Token 越权到 global: allowed=%v err=%v", allowed, err)
	}
	if err := manager.SetUserStatus(ctx, owner.ID, viewer.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Authenticate(ctx, viewerCookie); faultCode(err) != fault.CodeUnauthenticated {
		t.Fatalf("禁用用户的 Session 仍有效: %v", err)
	}
	if _, err := manager.AuthenticateAPIToken(ctx, created.Secret); faultCode(err) != fault.CodeTokenInvalid {
		t.Fatalf("禁用用户的 Token 仍有效: %v", err)
	}
	if manager.IsActive(ctx, created.Token.ID) {
		t.Fatal("禁用用户的 API Token 仍被 WS active 检查视为有效")
	}

	var passwordHash string
	if err := store.Control.SQL().QueryRowContext(ctx, "SELECT password_hash FROM local_users WHERE user_id=?", viewer.ID).Scan(&passwordHash); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(passwordHash, "viewer-password-strong") || !strings.HasPrefix(passwordHash, "$argon2id$") {
		t.Fatalf("数据库密码材料不安全: %q", passwordHash)
	}
}

func TestGlobalEffectiveCapabilitiesApplyAllowAndDenyGrants(t *testing.T) {
	ctx := context.Background()
	manager, _, _ := newSecurityManager(t)
	owner, err := manager.InitializeLANOwner(ctx, auth.CreateUserInput{
		Username: "owner", DisplayName: "Owner", Password: "owner-password-strong",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.CreateUser(ctx, owner.ID, auth.CreateUserInput{
		Username: "viewer", DisplayName: "Viewer", Password: "viewer-password-strong", Roles: []string{"viewer"},
		Grants: []auth.GrantInput{
			{Effect: "allow", Capability: "library.read", Scope: auth.ResourceScope{Kind: "global"}},
			{Effect: "allow", Capability: "media.read", Scope: auth.ResourceScope{Kind: "global"}},
			{Effect: "deny", Capability: "media.read", Scope: auth.ResourceScope{Kind: "global"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, _, err := manager.Login(ctx, "viewer", "viewer-password-strong", "browser", "loopback")
	if err != nil {
		t.Fatal(err)
	}
	if !auth.HasCapability(session, "media.read") {
		t.Fatalf("Session capability 上限意外丢失 media.read: %+v", session.Capabilities)
	}
	effective, err := manager.EffectiveCapabilities(ctx, session, auth.ResourceScope{Kind: "global"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(effective, ","); got != "library.read" {
		t.Fatalf("global effective capability 未应用 allow/deny: %q", got)
	}
}

func TestSessionIdleExpiryAndPasswordChangeInvalidateCredentials(t *testing.T) {
	ctx := context.Background()
	manager, _, manual := newSecurityManager(t)
	_, err := manager.InitializeLANOwner(ctx, auth.CreateUserInput{Username: "owner", DisplayName: "Owner", Password: "owner-password-strong"})
	if err != nil {
		t.Fatal(err)
	}
	session, cookie, err := manager.Login(ctx, "owner", "owner-password-strong", "browser", "loopback")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ChangePassword(ctx, session, "owner-password-strong", "new-owner-password-strong"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Authenticate(ctx, cookie); faultCode(err) != fault.CodeUnauthenticated {
		t.Fatalf("改密后的旧 Session 仍有效: %v", err)
	}
	_, cookie, err = manager.Login(ctx, "owner", "new-owner-password-strong", "browser", "loopback")
	if err != nil {
		t.Fatal(err)
	}
	manual.Advance(auth.SessionIdleLifetime)
	if _, err := manager.Authenticate(ctx, cookie); faultCode(err) != fault.CodeUnauthenticated {
		t.Fatalf("达到 idle expiry 的 Session 仍有效: %v", err)
	}
}

func TestLoginRateLimitTokenExpiryShareAndAudit(t *testing.T) {
	ctx := context.Background()
	manager, _, manual := newSecurityManager(t)
	owner, err := manager.InitializeLANOwner(ctx, auth.CreateUserInput{Username: "owner", DisplayName: "Owner", Password: "owner-password-strong"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.Login(ctx, "missing", "wrong-password", "", "same-peer"); faultCode(err) != fault.CodeInvalidCredentials {
		t.Fatalf("未知账户泄露或错误语义不一致: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, _, err := manager.Login(ctx, "owner", "wrong-password", "", "same-peer"); faultCode(err) != fault.CodeInvalidCredentials {
			t.Fatalf("错误密码语义不一致: %v", err)
		}
	}
	if _, _, err := manager.Login(ctx, "owner", "wrong-password", "", "same-peer"); faultCode(err) != fault.CodeRateLimited {
		t.Fatalf("登录限流未生效: %v", err)
	}
	manual.Advance(16 * time.Minute)
	session, _, err := manager.Login(ctx, "owner", "owner-password-strong", "device-a", "same-peer")
	if err != nil {
		t.Fatal(err)
	}
	expires := manual.Now().Add(time.Minute)
	token, err := manager.CreateAPIToken(ctx, session, "short-lived", []string{"library.read"}, []auth.ResourceScope{{Kind: "global"}}, &expires)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AuthenticateAPIToken(ctx, token.Secret); err != nil {
		t.Fatal(err)
	}
	share, err := manager.CreateShare(ctx, session, "library", "lib_00000000-0000-7000-8000-000000000001", []string{"view"}, "", "", expires)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CreateShare(ctx, session, "library", "not-a-library-id", []string{"view"}, "", "", expires); faultCode(err) != fault.CodeValidation {
		t.Fatalf("分享 scope 未拒绝错误 ID 类型: %v", err)
	}
	if _, err := manager.CreateShare(ctx, session, "library", "lib_00000000-0000-7000-8000-000000000001", []string{"view"}, "sha256-v1", strings.Repeat("z", 64), expires); faultCode(err) != fault.CodeValidation {
		t.Fatalf("固定 Blob 未拒绝非十六进制摘要: %v", err)
	}
	if _, err := manager.ResolveShare(ctx, share.Secret); err != nil {
		t.Fatal(err)
	}
	manual.Advance(time.Minute)
	if _, err := manager.AuthenticateAPIToken(ctx, token.Secret); faultCode(err) != fault.CodeTokenExpired {
		t.Fatalf("过期 Token 仍有效: %v", err)
	}
	if _, err := manager.ResolveShare(ctx, share.Secret); faultCode(err) != fault.CodeNotFound {
		t.Fatalf("过期分享未统一隐藏: %v", err)
	}
	audits, err := manager.ListSecurityAudits(ctx, 100)
	if err != nil || len(audits) < 3 {
		t.Fatalf("安全审计缺失: count=%d err=%v", len(audits), err)
	}
	encoded := fmt.Sprintf("%v", audits)
	if strings.Contains(encoded, "owner-password-strong") || strings.Contains(encoded, token.Secret) || strings.Contains(encoded, share.Secret) {
		t.Fatal("安全审计泄露凭据")
	}
	if owner.ID == "" {
		t.Fatal("Owner ID 为空")
	}
}

func TestAPITokenConcurrentAuthenticationAndRevocationConverges(t *testing.T) {
	ctx := context.Background()
	manager, _, _ := newSecurityManager(t)
	owner, err := manager.InitializeLANOwner(ctx, auth.CreateUserInput{Username: "owner", DisplayName: "Owner", Password: "owner-password-strong"})
	if err != nil {
		t.Fatal(err)
	}
	session, _, err := manager.Login(ctx, "owner", "owner-password-strong", "device", "peer")
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.CreateAPIToken(ctx, session, "race", []string{"library.read"}, []auth.ResourceScope{{Kind: "global"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		<-start
		_, authenticateErr := manager.AuthenticateAPIToken(ctx, created.Secret)
		result <- authenticateErr
	}()
	close(start)
	if err := manager.RevokeAPIToken(ctx, owner.ID, created.Token.ID); err != nil {
		t.Fatal(err)
	}
	// 与吊销事务并发、先完成的认证允许成功；吊销返回后必须稳定收敛为无效。
	_ = <-result
	for range 100 {
		if _, err := manager.AuthenticateAPIToken(ctx, created.Secret); faultCode(err) != fault.CodeTokenInvalid {
			t.Fatalf("吊销返回后 Token 仍可认证: %v", err)
		}
	}
}

func TestSecurityResourceListsUseBoundedStableKeysets(t *testing.T) {
	ctx := context.Background()
	manager, store, manual := newSecurityManager(t)
	db := store.Control.SQL()
	generator := identity.NewGenerator(manual)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	var targetUserID string
	for index := range 55 {
		sessionID, err := generator.New(domain.IDSession)
		if err != nil {
			t.Fatal(err)
		}
		tokenID, err := generator.New(domain.IDAPIToken)
		if err != nil {
			t.Fatal(err)
		}
		shareID, err := generator.New(domain.IDShare)
		if err != nil {
			t.Fatal(err)
		}
		userID, err := generator.New(domain.IDUser)
		if err != nil {
			t.Fatal(err)
		}
		createdAt := manual.Now().Unix()
		secretHash := fmt.Sprintf("%064x", index+1)
		if _, err := tx.ExecContext(ctx, `INSERT INTO sessions
(session_id, secret_hash, principal_id, csrf_hash, auth_method, client_label,
 principal_security_version, created_at, absolute_expires_at, idle_expires_at, last_seen_at)
VALUES (?, ?, ?, ?, 'personal_pairing', ?, 1, ?, ?, ?, ?)`, sessionID.String(), secretHash,
			auth.PersonalOwnerID, fmt.Sprintf("%064x", index+101), fmt.Sprintf("session-%02d", index),
			createdAt, createdAt+7200, createdAt+3600, createdAt); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO api_tokens
(token_id, principal_id, secret_hash, secret_prefix, name, capabilities_json, scopes_json,
 principal_security_version, created_by, created_at)
VALUES (?, ?, ?, ?, ?, '["library.read"]', '[{"kind":"global"}]', 1, ?, ?)`, tokenID.String(),
			auth.PersonalOwnerID, fmt.Sprintf("%064x", index+201), fmt.Sprintf("tok%02d", index),
			fmt.Sprintf("token-%02d", index), auth.PersonalOwnerID, createdAt); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO shares
(share_id, secret_hash, secret_prefix, created_by, scope_kind, scope_id, permissions_json,
 created_at, expires_at)
VALUES (?, ?, ?, ?, 'library', 'lib_00000000-0000-7000-8000-000000000001', '["view"]', ?, ?)`,
			shareID.String(), fmt.Sprintf("%064x", index+301), fmt.Sprintf("shr%02d", index),
			auth.PersonalOwnerID, createdAt, createdAt+3600); err != nil {
			t.Fatal(err)
		}
		username := fmt.Sprintf("user-%02d", index)
		if _, err := tx.ExecContext(ctx, `INSERT INTO security_principals
(principal_id, principal_kind, display_name, status, security_version, created_at, updated_at)
VALUES (?, 'local_user', ?, 'active', 1, ?, ?)`, userID.String(), username, createdAt, createdAt); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO local_users
(user_id, username, username_normalized, password_hash, password_algorithm,
 password_parameters_version, password_changed_at, created_by, created_at, updated_at)
VALUES (?, ?, ?, 'fixture', 'argon2id', 1, ?, ?, ?, ?)`, userID.String(), username, username,
			createdAt, auth.PersonalOwnerID, createdAt, createdAt); err != nil {
			t.Fatal(err)
		}
		if targetUserID == "" {
			targetUserID = userID.String()
		}
	}
	for index := range 55 {
		grantID, err := generator.New(domain.IDGrant)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO authorization_grants
(grant_id, principal_id, effect, capability, scope_kind, scope_id, created_by, created_at)
VALUES (?, ?, 'allow', ?, 'global', '', ?, ?)`, grantID.String(), targetUserID,
			fmt.Sprintf("fixture.capability.%02d", index), auth.PersonalOwnerID, manual.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	sessionIDs, sessionCursor := collectSecurityPages(t, func(cursor string) (auth.ListPage[auth.Session], error) {
		return manager.ListSessions(ctx, cursor, 20)
	}, func(item auth.Session) string { return item.ID })
	assertUniqueSecurityIDs(t, "Session", sessionIDs, 55)
	tokenIDs, tokenCursor := collectSecurityPages(t, func(cursor string) (auth.ListPage[auth.APIToken], error) {
		return manager.ListAPITokens(ctx, auth.PersonalOwnerID, cursor, 20)
	}, func(item auth.APIToken) string { return item.ID })
	assertUniqueSecurityIDs(t, "API Token", tokenIDs, 55)
	shareIDs, _ := collectSecurityPages(t, func(cursor string) (auth.ListPage[auth.Share], error) {
		return manager.ListShares(ctx, auth.PersonalOwnerID, cursor, 20)
	}, func(item auth.Share) string { return item.ID })
	assertUniqueSecurityIDs(t, "Share", shareIDs, 55)
	userIDs, _ := collectSecurityPages(t, func(cursor string) (auth.ListPage[auth.User], error) {
		return manager.ListUsers(ctx, cursor, 20)
	}, func(item auth.User) string { return item.ID })
	assertUniqueSecurityIDs(t, "User", userIDs, 55)
	grantIDs, _ := collectSecurityPages(t, func(cursor string) (auth.ListPage[auth.Grant], error) {
		return manager.ListGrants(ctx, auth.PersonalOwnerID, targetUserID, cursor, 20)
	}, func(item auth.Grant) string { return item.ID })
	assertUniqueSecurityIDs(t, "Grant", grantIDs, 55)

	if _, err := manager.ListSessions(ctx, "%%%", 20); faultCode(err) != fault.CodeCursorInvalid {
		t.Fatalf("破坏的安全列表 cursor 未返回 CURSOR_INVALID: %v", err)
	}
	if _, err := manager.ListAPITokens(ctx, auth.PersonalOwnerID, sessionCursor, 20); faultCode(err) != fault.CodeCursorExpired {
		t.Fatalf("跨资源 cursor 未返回 CURSOR_EXPIRED: %v", err)
	}
	if _, err := manager.ListAPITokens(ctx, "other-principal", tokenCursor, 20); faultCode(err) != fault.CodeCursorExpired {
		t.Fatalf("跨 Principal cursor 未返回 CURSOR_EXPIRED: %v", err)
	}

	for _, indexName := range []string{
		"sessions_list_idx", "api_tokens_principal_list_idx", "shares_creator_list_idx",
		"local_users_list_idx", "authorization_grants_principal_list_idx",
	} {
		var found int
		if err := db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", indexName).Scan(&found); err != nil || found != 1 {
			t.Fatalf("安全列表索引 %s 不存在: found=%d err=%v", indexName, found, err)
		}
	}
}

func collectSecurityPages[T any](t *testing.T, list func(string) (auth.ListPage[T], error), id func(T) string) ([]string, string) {
	t.Helper()
	cursor := ""
	firstCursor := ""
	result := make([]string, 0, 55)
	wantPageSizes := []int{20, 20, 15}
	for pageIndex, wantSize := range wantPageSizes {
		page, err := list(cursor)
		if err != nil {
			t.Fatalf("第 %d 页读取失败: %v", pageIndex+1, err)
		}
		if len(page.Items) != wantSize {
			t.Fatalf("第 %d 页数量=%d，期望 %d", pageIndex+1, len(page.Items), wantSize)
		}
		for _, item := range page.Items {
			result = append(result, id(item))
		}
		if pageIndex < len(wantPageSizes)-1 && page.NextCursor == "" {
			t.Fatalf("第 %d 页缺少 nextCursor", pageIndex+1)
		}
		if pageIndex == len(wantPageSizes)-1 && page.NextCursor != "" {
			t.Fatalf("末页不应返回 nextCursor: %q", page.NextCursor)
		}
		if pageIndex == 0 {
			firstCursor = page.NextCursor
		}
		cursor = page.NextCursor
	}
	return result, firstCursor
}

func assertUniqueSecurityIDs(t *testing.T, label string, ids []string, want int) {
	t.Helper()
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, exists := seen[id]; exists {
			t.Fatalf("%s 分页出现重复 ID: %s", label, id)
		}
		seen[id] = struct{}{}
	}
	if len(ids) != want {
		t.Fatalf("%s 分页总数=%d，期望 %d", label, len(ids), want)
	}
}

func newSecurityManager(t *testing.T) (*auth.Personal, *storage.Store, *clock.Manual) {
	t.Helper()
	dirs := appdirs.UnderRoot(t.TempDir())
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manual := clock.NewManual(time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC))
	manager, err := auth.NewPersonal(store.Control.SQL(), manual, identity.NewGenerator(manual), nil)
	if err != nil {
		t.Fatal(err)
	}
	return manager, store, manual
}

func faultCode(err error) fault.Code {
	var structured *fault.Error
	if errors.As(err, &structured) {
		return structured.Code
	}
	return ""
}

// TestWebSocketOriginAcceptsBrowserHandshakeWithoutFetchMetadata 锁定 WebSocket 握手
// 的同源判定形态。Chrome 与 Edge 的同源 ws:// 握手只发送 Origin，不发送任何
// Sec-Fetch-* 头；若沿用要求 Sec-Fetch-Site 的普通 HTTP 判定，/ws/v1 会对所有主流
// 浏览器恒定返回 ORIGIN_REJECTED。本测试在修复前会失败。
func TestWebSocketOriginAcceptsBrowserHandshakeWithoutFetchMetadata(t *testing.T) {
	const host = "127.0.0.1:18080"
	newRequest := func(origin, fetchSite string) *http.Request {
		request := httptest.NewRequest(http.MethodGet, "/ws/v1", nil)
		request.Host = host
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if fetchSite != "" {
			request.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		return request
	}
	allowed := []string{host}

	// 真实浏览器形态：同源 Origin、无任何 Fetch Metadata。
	if err := auth.ValidateWebSocketOriginAllowed(newRequest("http://"+host, ""), allowed); err != nil {
		t.Fatalf("浏览器形态的 WebSocket 握手被拒绝: %v", err)
	}
	// 该形态在普通 HTTP 判定下必须仍被拒绝，证明两条路径确实不同。
	if err := auth.ValidateOriginAllowed(newRequest("http://"+host, ""), allowed); err == nil {
		t.Fatal("普通 HTTP 变更请求在缺少 Fetch Metadata 时被接受")
	}

	rejected := []struct {
		name      string
		origin    string
		fetchSite string
	}{
		{"跨站 Origin", "http://attacker.invalid", ""},
		{"缺少 Origin", "", ""},
		{"非 HTTP scheme", "file://" + host, ""},
		{"存在但非同源的 Fetch Metadata", "http://" + host, "cross-site"},
	}
	for _, testCase := range rejected {
		err := auth.ValidateWebSocketOriginAllowed(newRequest(testCase.origin, testCase.fetchSite), allowed)
		var structured *fault.Error
		if !errors.As(err, &structured) || structured.Code != fault.CodeOriginRejected {
			t.Fatalf("%s 未被拒绝为 ORIGIN_REJECTED: %v", testCase.name, err)
		}
	}

	// Host allowlist 仍然优先生效。
	other := newRequest("http://127.0.0.1:19999", "")
	other.Host = "127.0.0.1:19999"
	err := auth.ValidateWebSocketOriginAllowed(other, allowed)
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeHostRejected {
		t.Fatalf("allowlist 之外的 Host 未被拒绝: %v", err)
	}
}
