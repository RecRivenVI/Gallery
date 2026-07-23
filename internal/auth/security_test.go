package auth_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/auth"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
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
