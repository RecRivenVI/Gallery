package auth_test

import (
	"context"
	"fmt"
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
)

// newGatedSecurityManager 与 newSecurityManager 相同，只是显式收窄 Argon2 闸门参数，用于
// 确定性地触发名额耗尽。
func newGatedSecurityManager(t *testing.T, width int, wait time.Duration) *auth.Personal {
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
	manual := clock.NewManual(time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC))
	manager, err := auth.NewPersonal(store.Control.SQL(), manual, identity.NewGenerator(manual), nil,
		auth.SecurityOptions{PasswordConcurrency: width, PasswordGateWait: wait})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

// TestPasswordGateBoundsConcurrentHashing 是闸门本身的确定性覆盖：名额有界、超额请求经过
// 有界等待后得到可重试的 RATE_LIMITED、名额释放后立即可用、全部释放后不泄漏名额。
func TestPasswordGateBoundsConcurrentHashing(t *testing.T) {
	gate := auth.NewPasswordGate(2, 20*time.Millisecond)
	if gate.Width() != 2 {
		t.Fatalf("闸门宽度 = %d", gate.Width())
	}
	first, err := gate.Acquire(context.Background())
	if err != nil {
		t.Fatalf("首个名额获取失败: %v", err)
	}
	second, err := gate.Acquire(context.Background())
	if err != nil {
		t.Fatalf("第二个名额获取失败: %v", err)
	}
	start := time.Now()
	if _, err := gate.Acquire(context.Background()); faultCode(err) != fault.CodeRateLimited {
		t.Fatalf("名额耗尽错误 = %v", err)
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Fatalf("超额请求没有经过有界等待就被拒绝: %v", elapsed)
	}
	if got := gate.PeakInFlight(); got != 2 {
		t.Fatalf("峰值在飞数 = %d，被拒绝的请求不得计入", got)
	}
	first()
	third, err := gate.Acquire(context.Background())
	if err != nil {
		t.Fatalf("名额释放后仍无法获取: %v", err)
	}
	third()
	second()
	// 全部释放后闸门必须回到空闲状态：连续 Width() 次立即获取都不能阻塞。
	for i := 0; i < gate.Width(); i++ {
		release, err := gate.Acquire(context.Background())
		if err != nil {
			t.Fatalf("名额泄漏，第 %d 次重新获取失败: %v", i, err)
		}
		defer release()
	}
	if got, want := gate.Admitted(), int64(2+1+2); got != want {
		t.Fatalf("累计取得名额次数 = %d，应为 %d", got, want)
	}
}

// TestPasswordGateHonorsRequestCancellation 证明客户端断开后等待名额的请求立即退出，不继续
// 占用等待队列。
func TestPasswordGateHonorsRequestCancellation(t *testing.T) {
	gate := auth.NewPasswordGate(1, time.Minute)
	release, err := gate.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if _, err := gate.Acquire(ctx); faultCode(err) != fault.CodeProcessInterrupted {
		t.Fatalf("请求取消错误 = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("请求取消后仍等待了 %v", elapsed)
	}
}

// TestDefaultPasswordConcurrencyStaysWithinMemoryBudget 锁定宽度选取同时受 CPU 与内存预算
// 约束：默认宽度乘以单次 Argon2id 的 19 MiB 不得超过为低端设备设定的预算。
func TestDefaultPasswordConcurrencyStaysWithinMemoryBudget(t *testing.T) {
	width := auth.DefaultPasswordConcurrency()
	if width < 1 {
		t.Fatalf("默认宽度 = %d", width)
	}
	if budgetMiB := width * 19; budgetMiB > 128 {
		t.Fatalf("默认宽度 %d 允许 Argon2id 同时占用 %d MiB，超过低端设备预算", width, budgetMiB)
	}
	if auth.NewPasswordGate(0, 0).Width() != width {
		t.Fatal("零值宽度未回落到默认值")
	}
}

// TestUnauthenticatedLoginCannotAmplifyArgon2Memory 是缺陷回归：N 个并发登录打向 N 个各不
// 相同的**不存在**用户名。防枚举的 dummy hash 保证每一次都会跑完整的 Argon2id，而限流键
// 包含用户名，因此这些请求一个共享的失败桶都命中不了。修复前它们会同时分配 N × 19 MiB；
// 修复后同时在飞的 Argon2 调用数必须被闸门宽度硬性限死。
func TestUnauthenticatedLoginCannotAmplifyArgon2Memory(t *testing.T) {
	const (
		attempts = 24
		width    = 3
	)
	ctx := context.Background()
	manager := newGatedSecurityManager(t, width, 5*time.Second)
	gate := manager.PasswordGate()
	if gate.Width() != width {
		t.Fatalf("注入的闸门宽度未生效: %d", gate.Width())
	}

	start := make(chan struct{})
	codes := make([]fault.Code, attempts)
	var group sync.WaitGroup
	for i := 0; i < attempts; i++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			// 同一个对端 IP、各不相同且都不存在的用户名：这正是按 (用户名, 对端) 计数的桶
			// 完全挡不住的形态。
			_, _, err := manager.Login(ctx, fmt.Sprintf("sprayed-user-%04d", index), "any-password-value", "", "203.0.113.7")
			codes[index] = faultCode(err)
		}(i)
	}
	close(start)
	group.Wait()

	if peak := gate.PeakInFlight(); peak > width {
		t.Fatalf("同时在飞的 Argon2 调用数达到 %d，超过闸门宽度 %d：未认证请求仍可放大内存到 %d MiB",
			peak, width, peak*19)
	}
	if peak := gate.PeakInFlight(); peak < 2 {
		t.Fatalf("峰值在飞数只有 %d，%d 个并发请求没有真正并发，测试无法证明上限生效", peak, attempts)
	}
	if admitted := gate.Admitted(); admitted == 0 {
		t.Fatal("没有任何 Argon2 调用经过闸门，登录路径绕开了信号量")
	}
	for index, code := range codes {
		if code != fault.CodeInvalidCredentials && code != fault.CodeRateLimited {
			t.Fatalf("第 %d 个请求的结果 = %q，未认证登录只能得到凭据错误或限流", index, code)
		}
	}
}

// TestLoginOverCapacityIsRateLimitedNotQueued 证明超出宽度的请求经**有界等待**后返回
// RATE_LIMITED，而不是无限排队占住连接。等待上限远小于一次 Argon2id 的耗时，因此只有当
// 散列本身位于名额之内时，落败的请求才会成规模地超时——这同时锁定了「闸门确实包住了
// Argon2 计算，而不只是包住一次函数调用的入口」。
func TestLoginOverCapacityIsRateLimitedNotQueued(t *testing.T) {
	const attempts = 12
	ctx := context.Background()
	manager := newGatedSecurityManager(t, 1, time.Millisecond)

	start := make(chan struct{})
	codes := make([]fault.Code, attempts)
	var group sync.WaitGroup
	for i := 0; i < attempts; i++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			_, _, err := manager.Login(ctx, fmt.Sprintf("queued-user-%04d", index), "any-password-value", "", "198.51.100.11")
			codes[index] = faultCode(err)
		}(i)
	}
	close(start)
	group.Wait()

	rateLimited := 0
	for index, code := range codes {
		switch code {
		case fault.CodeRateLimited:
			rateLimited++
		case fault.CodeInvalidCredentials:
		default:
			t.Fatalf("第 %d 个请求的结果 = %q", index, code)
		}
	}
	if rateLimited == 0 {
		t.Fatalf("%d 个并发请求在宽度 1、等待上限 1ms 下无一被限流：超额请求仍在无界排队，或 Argon2 计算不在名额之内", attempts)
	}
	if manager.PasswordGate().PeakInFlight() > 1 {
		t.Fatalf("宽度 1 下仍观测到 %d 个并发 Argon2 调用", manager.PasswordGate().PeakInFlight())
	}
}

// TestLoginSprayAcrossUsernamesHitsPeerRateLimit 覆盖新增的按对端计数桶：同一个对端换用户名
// 喷洒时，按 (用户名, 对端) 的桶每个都只有一次失败、永远不会触发；只有按对端的桶能收敛。
// 修复前这条路径没有任何上限。
func TestLoginSprayAcrossUsernamesHitsPeerRateLimit(t *testing.T) {
	ctx := context.Background()
	manager := newGatedSecurityManager(t, 0, 0)
	const subject = "192.0.2.44"
	blockedAt := -1
	for i := 0; i < 200; i++ {
		_, _, err := manager.Login(ctx, fmt.Sprintf("spray-%04d", i), "any-password-value", "", subject)
		code := faultCode(err)
		if code == fault.CodeRateLimited {
			blockedAt = i
			break
		}
		if code != fault.CodeInvalidCredentials {
			t.Fatalf("第 %d 次喷洒的结果 = %q", i, code)
		}
	}
	if blockedAt < 0 {
		t.Fatal("同一对端换用户名喷洒 200 次仍未被限流：只按 (用户名, 对端) 计数的桶挡不住喷洒")
	}
	// 单账户上限必须仍然更严格，且按对端的桶不能严到把正常多账户设备锁死。
	if blockedAt < 8 {
		t.Fatalf("按对端的上限在第 %d 次就触发，比单账户上限还严", blockedAt)
	}

	// 另一个对端不受影响：限流主体仍然是直连对端，不是全局开关。
	if _, _, err := manager.Login(ctx, "spray-9999", "any-password-value", "", "192.0.2.45"); faultCode(err) != fault.CodeInvalidCredentials {
		t.Fatalf("其它对端被连带限流: %v", err)
	}
}

// TestSuccessfulLoginDoesNotResetPeerSprayCounter 锁定一条容易被写错的语义：登录成功只清除
// 该账户自己的失败桶。若同时清掉按对端的桶，攻击者只要持有任意一个有效账户，就能在每次
// 喷洒之间登录一次把计数清零，整条按对端的限流形同虚设。
func TestSuccessfulLoginDoesNotResetPeerSprayCounter(t *testing.T) {
	ctx := context.Background()
	manager := newGatedSecurityManager(t, 0, 0)
	const subject = "192.0.2.77"
	if _, err := manager.InitializeLANOwner(ctx, auth.CreateUserInput{
		Username: "owner", DisplayName: "Owner", Password: "owner-password-strong",
	}); err != nil {
		t.Fatal(err)
	}
	blockedAt := -1
	for i := 0; i < 200 && blockedAt < 0; i++ {
		// 每次喷洒之间插入一次真正成功的登录。成功路径若连按对端的桶一起清掉，下面的
		// 喷洒就永远不会收敛。
		_, _, validErr := manager.Login(ctx, "owner", "owner-password-strong", "", subject)
		switch code := faultCode(validErr); code {
		case "":
		case fault.CodeRateLimited:
			// 该对端已进入封禁期，凭据正确也一样被挡下：这正是按对端计数仍在生效的证据。
			blockedAt = i
			continue
		default:
			t.Fatalf("第 %d 轮的有效登录结果 = %q", i, code)
		}
		_, _, err := manager.Login(ctx, fmt.Sprintf("spray-%04d", i), "any-password-value", "", subject)
		switch code := faultCode(err); code {
		case fault.CodeRateLimited:
			blockedAt = i
		case fault.CodeInvalidCredentials:
		default:
			t.Fatalf("第 %d 次喷洒的结果 = %q", i, code)
		}
	}
	if blockedAt < 0 {
		t.Fatal("每次喷洒之间插入一次成功登录即可无限喷洒：成功路径清除了按对端的失败桶")
	}
}
