package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"golang.org/x/crypto/argon2"
)

const (
	PasswordParametersVersion = 1
	PasswordMaxBytes          = 1024
	PasswordMinBytes          = 10
	argonMemoryKiB            = 19 * 1024
	argonIterations           = 2
	argonParallelism          = 1
	argonSaltBytes            = 16
	argonKeyBytes             = 32
)

// Argon2id 的内存成本既是设计上的防御，也是可以被远程放大的资源：每次调用固定分配
// argonMemoryKiB（19 MiB）。POST /api/v1/auth/login 未认证可达；为了不泄露用户名是否
// 存在，不存在的账户同样要对 dummy hash 跑完整验证；而登录失败限流键包含用户名，换一个
// 用户名就是一个全新的桶。三者叠加的结果是：任何人都可以用几十个并发连接把服务端内存
// 占用放大到 GiB 级，对项目自述的低端设备目标构成未认证远程 OOM。
//
// PasswordGate 是这条路径上唯一的硬上限：任何时刻真正在飞的 Argon2 调用不超过 Width()。
// 名额耗尽时请求经有界等待后返回可重试的 RATE_LIMITED，而不是无限排队占用连接和内存。
const (
	// passwordGateMemoryBudgetKiB 是允许被 Argon2id 同时占用的内存预算。按低端设备取
	// 128 MiB，不按当前机器的物理内存推算，否则大内存开发机上的门禁挡不住目标设备上的
	// 真实放大。PRE_FREEZE。
	passwordGateMemoryBudgetKiB = 128 * 1024
	// defaultPasswordGateWait 是名额耗尽后的有界等待上限。单次 Argon2id 在目标硬件上约
	// 几十毫秒，这个等待足以吸收正常的并发登录尖峰。PRE_FREEZE。
	defaultPasswordGateWait = 2 * time.Second
)

// PasswordGate 限制同时进行的 Argon2 计算数量。名额在真正调用 Argon2 之前取得、在调用
// 返回之后释放，因此 PeakInFlight 就是实际观测到的并发散列数，可以直接作为回归断言。
type PasswordGate struct {
	slots    chan struct{}
	wait     time.Duration
	inFlight atomic.Int64
	peak     atomic.Int64
	admitted atomic.Int64
}

// DefaultPasswordConcurrency 同时受 CPU 与内存预算约束：Argon2 参数固定 p=1，一次计算
// 占满一个核心并分配 19 MiB，因此宽度取 min(NumCPU, 内存预算/单次内存)，且至少为 1。
func DefaultPasswordConcurrency() int {
	width := runtime.NumCPU()
	if byMemory := passwordGateMemoryBudgetKiB / argonMemoryKiB; byMemory < width {
		width = byMemory
	}
	if width < 1 {
		width = 1
	}
	return width
}

// NewPasswordGate 构造一个口令散列闸门。width 或 wait 为非正值时使用默认值；显式收窄
// 只用于测试中确定性地触发名额耗尽。
func NewPasswordGate(width int, wait time.Duration) *PasswordGate {
	if width <= 0 {
		width = DefaultPasswordConcurrency()
	}
	if wait <= 0 {
		wait = defaultPasswordGateWait
	}
	return &PasswordGate{slots: make(chan struct{}, width), wait: wait}
}

// Width 返回同时允许的 Argon2 调用数。
func (g *PasswordGate) Width() int { return cap(g.slots) }

// PeakInFlight 返回进程启动以来观测到的同时在飞 Argon2 调用数上限。
func (g *PasswordGate) PeakInFlight() int { return int(g.peak.Load()) }

// Admitted 返回累计取得名额并真正执行了 Argon2 的次数。
func (g *PasswordGate) Admitted() int64 { return g.admitted.Load() }

// Acquire 取得一次 Argon2 名额，返回释放函数。名额已满时先做有界等待；等待超时返回可
// 重试的 RATE_LIMITED，请求上下文取消时立即返回 PROCESS_INTERRUPTED。
func (g *PasswordGate) Acquire(ctx context.Context) (func(), error) {
	select {
	case g.slots <- struct{}{}:
		return g.enter(), nil
	default:
	}
	timer := time.NewTimer(g.wait)
	defer timer.Stop()
	select {
	case g.slots <- struct{}{}:
		return g.enter(), nil
	case <-ctx.Done():
		return nil, fault.New(fault.CodeProcessInterrupted, true, ctx.Err())
	case <-timer.C:
		return nil, fault.New(fault.CodeRateLimited, true, nil)
	}
}

func (g *PasswordGate) enter() func() {
	g.admitted.Add(1)
	current := g.inFlight.Add(1)
	for {
		peak := g.peak.Load()
		if current <= peak || g.peak.CompareAndSwap(peak, current) {
			break
		}
	}
	return func() {
		g.inFlight.Add(-1)
		<-g.slots
	}
}

var ErrPasswordInvalid = errors.New("密码无效")

type PasswordParameters struct {
	Version     int
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	SaltBytes   uint32
	KeyBytes    uint32
}

func CurrentPasswordParameters() PasswordParameters {
	return PasswordParameters{
		Version: PasswordParametersVersion, MemoryKiB: argonMemoryKiB,
		Iterations: argonIterations, Parallelism: argonParallelism,
		SaltBytes: argonSaltBytes, KeyBytes: argonKeyBytes,
	}
}

func HashPassword(password string, random io.Reader) (string, error) {
	if len(password) < PasswordMinBytes || len(password) > PasswordMaxBytes {
		return "", ErrPasswordInvalid
	}
	if random == nil {
		random = rand.Reader
	}
	p := CurrentPasswordParameters()
	salt := make([]byte, p.SaltBytes)
	if _, err := io.ReadFull(random, salt); err != nil {
		return "", fmt.Errorf("生成密码盐: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.Iterations, p.MemoryKiB, p.Parallelism, p.KeyBytes)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		p.MemoryKiB, p.Iterations, p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func VerifyPassword(encoded, password string) (valid, needsRehash bool, err error) {
	if len(password) > PasswordMaxBytes {
		return false, false, ErrPasswordInvalid
	}
	p, salt, expected, err := parsePasswordHash(encoded)
	if err != nil {
		return false, false, err
	}
	actual := argon2.IDKey([]byte(password), salt, p.Iterations, p.MemoryKiB, p.Parallelism, uint32(len(expected)))
	valid = subtle.ConstantTimeCompare(actual, expected) == 1
	current := CurrentPasswordParameters()
	needsRehash = valid && (p.MemoryKiB != current.MemoryKiB || p.Iterations != current.Iterations ||
		p.Parallelism != current.Parallelism || uint32(len(salt)) != current.SaltBytes || uint32(len(expected)) != current.KeyBytes)
	return valid, needsRehash, nil
}

func parsePasswordHash(encoded string) (PasswordParameters, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return PasswordParameters{}, nil, nil, ErrPasswordInvalid
	}
	var p PasswordParameters
	p.Version = PasswordParametersVersion
	seen := make(map[string]struct{}, 3)
	for _, item := range strings.Split(parts[3], ",") {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			return PasswordParameters{}, nil, nil, ErrPasswordInvalid
		}
		n, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return PasswordParameters{}, nil, nil, ErrPasswordInvalid
		}
		if _, duplicate := seen[key]; duplicate {
			return PasswordParameters{}, nil, nil, ErrPasswordInvalid
		}
		seen[key] = struct{}{}
		switch key {
		case "m":
			if n > 256*1024 {
				return PasswordParameters{}, nil, nil, ErrPasswordInvalid
			}
			p.MemoryKiB = uint32(n)
		case "t":
			if n > 20 {
				return PasswordParameters{}, nil, nil, ErrPasswordInvalid
			}
			p.Iterations = uint32(n)
		case "p":
			if n > 16 {
				return PasswordParameters{}, nil, nil, ErrPasswordInvalid
			}
			p.Parallelism = uint8(n)
		default:
			return PasswordParameters{}, nil, nil, ErrPasswordInvalid
		}
	}
	if len(seen) != 3 || p.MemoryKiB < 8*1024 || p.Iterations == 0 || p.Parallelism == 0 {
		return PasswordParameters{}, nil, nil, ErrPasswordInvalid
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 16 || len(salt) > 64 {
		return PasswordParameters{}, nil, nil, ErrPasswordInvalid
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > 64 {
		return PasswordParameters{}, nil, nil, ErrPasswordInvalid
	}
	return p, salt, key, nil
}
