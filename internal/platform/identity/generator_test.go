package identity_test

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/ports"
)

// Generator 是 Session、API Token、Share、Grant 以及全部 Canonical 实体主键的唯一来源。
// 它同时承担两件事：
//
//   - **不可预测性**：Session/Token/Share 的 ID 会出现在 URL 与凭据关联结构中，一旦随机
//     部分可被预测或复现，攻击者就能枚举出别人的会话与分享；
//   - **唯一性**：ID 是数据库主键，重复会表现为插入冲突或——更糟——覆盖另一条用户事实。
//
// 因此本文件断言的是「时间前缀 + 74 位 CSPRNG 随机」这条结构契约本身，而不是任何调用方
// 的使用方式。
var _ ports.IDGenerator = identity.Generator{}

// uuidHexOf 从公共 ID 表示中取出 32 位十六进制 UUID。断言全部建立在公共表示之上，
// 这样「内部字节正确但公共表示错误」不会被漏掉。
func uuidHexOf(t *testing.T, id domain.ID) string {
	t.Helper()
	text := id.String()
	underscore := strings.IndexByte(text, '_')
	if underscore < 0 {
		t.Fatalf("ID %q 缺少类型前缀", text)
	}
	hexText := strings.ReplaceAll(text[underscore+1:], "-", "")
	if len(hexText) != 32 {
		t.Fatalf("ID %q 的 UUID 部分不是 32 位十六进制", text)
	}
	return hexText
}

func uuidBytesOf(t *testing.T, id domain.ID) [16]byte {
	t.Helper()
	var raw [16]byte
	if _, err := hex.Decode(raw[:], []byte(uuidHexOf(t, id))); err != nil {
		t.Fatalf("ID %q 的 UUID 部分无法解码: %v", id, err)
	}
	return raw
}

// TestNewGeneratorBindsCSPRNG 断言默认构造使用 crypto/rand。
//
// 这是最直接的 CSPRNG 断言：若哪天有人为了"可测试"把默认源换成 math/rand 或一个
// 固定种子的 Reader，Session/Token/Share 的 ID 立刻变成可枚举的。
func TestNewGeneratorBindsCSPRNG(t *testing.T) {
	generator := identity.NewGenerator(clock.System{})
	if generator.Random != rand.Reader {
		t.Fatal("NewGenerator 未绑定 crypto/rand.Reader")
	}
	if generator.Clock == nil {
		t.Fatal("NewGenerator 未绑定 Clock")
	}
}

// TestNewRejectsMissingClockInsteadOfFabricatingTime 断言缺少 Clock 时返回错误。
// 静默退回 time.Now() 会让一个装配错误变成"看起来正常工作"，从而绕过所有可控时钟的
// 确定性测试与时间线断言。
func TestNewRejectsMissingClockInsteadOfFabricatingTime(t *testing.T) {
	id, err := identity.Generator{Random: rand.Reader}.New(domain.IDSession)
	if err == nil {
		t.Fatalf("缺少 Clock 时应返回错误，实际得到 %s", id)
	}
	if !id.IsZero() {
		t.Fatal("失败时不得返回非零 ID")
	}
}

// TestNewFallsBackToCSPRNGWhenRandomUnset 断言零值 Random 退回 crypto/rand，而不是
// 退回一个确定性源。两个各自独立构造、共享同一个固定时钟的 Generator 必须产生不同的
// ID：任何以固定种子初始化的 PRNG 都会在这里给出相同结果。
func TestNewFallsBackToCSPRNGWhenRandomUnset(t *testing.T) {
	fixed := clock.Fixed{Time: time.Date(2026, 7, 27, 1, 2, 3, 0, time.UTC)}
	first, err := identity.Generator{Clock: fixed}.New(domain.IDSession)
	if err != nil {
		t.Fatal(err)
	}
	second, err := identity.Generator{Clock: fixed}.New(domain.IDSession)
	if err != nil {
		t.Fatal(err)
	}
	if first.String() == second.String() {
		t.Fatal("同一固定时钟下两次生成得到相同 ID：随机源是确定性的")
	}
}

// TestNewRejectsUnknownKind 断言未知 kind 在生成期失败。ID 前缀是防止不同实体 ID 被
// 静默混用的唯一屏障，不能产生一个前缀为空的"通用 ID"。
func TestNewRejectsUnknownKind(t *testing.T) {
	generator := identity.NewGenerator(clock.System{})
	id, err := generator.New(domain.IDKind("bogus"))
	if err == nil {
		t.Fatalf("未知 kind 应返回错误，实际得到 %s", id)
	}
	if !id.IsZero() {
		t.Fatal("失败时不得返回非零 ID")
	}
}

// failingReader 在读取到第 limit 个字节后失败，用于区分「完全没有熵」与「熵不足」。
type failingReader struct {
	limit int
	read  int
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.read >= r.limit {
		return 0, errors.New("熵源不可用")
	}
	remaining := r.limit - r.read
	if len(p) > remaining {
		p = p[:remaining]
	}
	for i := range p {
		p[i] = 0
	}
	r.read += len(p)
	return len(p), nil
}

// TestNewFailsClosedOnEntropyFailure 断言熵源失败时 New 报错，而不是返回一个只填了
// 一部分随机字节的 ID。
//
// 这是本包最容易被"顺手优化"掉的失败模式：io.ReadFull 的部分读结果 raw[6:] 其余字节
// 仍是零，随后 raw[6]/raw[8] 的版本与变体位仍会被正确写上，于是 IDFromUUIDv7 会认为
// 它是一个结构合法的 UUIDv7。若错误被忽略，产出的就是一个几乎全零、完全可预测的
// Session/Token ID，而且外部无法从形状上分辨。
func TestNewFailsClosedOnEntropyFailure(t *testing.T) {
	for name, limit := range map[string]int{
		"完全没有熵": 0,
		"熵不足":   5,
	} {
		t.Run(name, func(t *testing.T) {
			generator := identity.Generator{Clock: clock.System{}, Random: &failingReader{limit: limit}}
			id, err := generator.New(domain.IDSession)
			if err == nil {
				t.Fatalf("熵源失败时应返回错误，实际得到 %s", id)
			}
			if !id.IsZero() {
				t.Fatal("失败时不得返回非零 ID")
			}
		})
	}
}

// TestNewEncodesClockMillisecondsBigEndian 断言前 48 位是 Clock 的 Unix 毫秒大端表示。
// 这既是 RFC 9562 UUIDv7 的定义，也是"ID 近似按创建时间排序"这一使用前提的全部依据；
// 字节序写反会让 ID 在数据库索引中呈现随机分布。
func TestNewEncodesClockMillisecondsBigEndian(t *testing.T) {
	moment := time.Date(2026, 7, 27, 8, 30, 15, 123_000_000, time.UTC)
	generator := identity.Generator{Clock: clock.Fixed{Time: moment}, Random: rand.Reader}
	id, err := generator.New(domain.IDJob)
	if err != nil {
		t.Fatal(err)
	}
	expected := fmt.Sprintf("%012x", moment.UnixMilli())
	if prefix := uuidHexOf(t, id)[:12]; prefix != expected {
		t.Fatalf("时间前缀不是 Clock 的大端 Unix 毫秒：期望 %s，实际 %s", expected, prefix)
	}
	if !strings.HasPrefix(id.String(), string(domain.IDJob)+"_") {
		t.Fatalf("ID %s 未携带 kind 前缀", id)
	}
}

// TestNewSetsVersionAndVariantBits 断言版本位固定为 7、变体位固定为 RFC 4122/9562 的
// `10`，且这两处的固定不依赖随机字节恰好落在正确的取值上。
func TestNewSetsVersionAndVariantBits(t *testing.T) {
	generator := identity.NewGenerator(clock.System{})
	for i := 0; i < 256; i++ {
		id, err := generator.New(domain.IDCanonicalWork)
		if err != nil {
			t.Fatal(err)
		}
		raw := uuidBytesOf(t, id)
		if version := raw[6] >> 4; version != 7 {
			t.Fatalf("第 %d 个 ID 的版本位是 %d，不是 7", i, version)
		}
		if variant := raw[8] >> 6; variant != 0b10 {
			t.Fatalf("第 %d 个 ID 的变体位是 %02b，不是 10", i, variant)
		}
	}
}

// TestIDsIncreaseAsClockAdvances 断言时钟每前进 1 毫秒，ID 的字典序严格增大。
// 这是"UUIDv7 可按创建顺序排序"的可检验形式；用可控时钟而不是真实时钟断言，避免把
// 一次偶然的时序当成契约。
func TestIDsIncreaseAsClockAdvances(t *testing.T) {
	manual := clock.NewManual(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	generator := identity.Generator{Clock: manual, Random: rand.Reader}
	previous := ""
	for i := 0; i < 512; i++ {
		id, err := generator.New(domain.IDCatalogRevision)
		if err != nil {
			t.Fatal(err)
		}
		current := uuidHexOf(t, id)
		if previous != "" && current <= previous {
			t.Fatalf("第 %d 个 ID 未随时钟前进而增大：%s 之后是 %s", i, previous, current)
		}
		previous = current
		manual.Advance(time.Millisecond)
	}
}

// TestSameMillisecondIDsAreUnique 断言同一毫秒内的唯一性完全由随机部分承担。
//
// 时间前缀的分辨率只有毫秒，没有序列号字段：一台正常机器在 1 毫秒内可以轻松创建成千
// 上万个 ID。如果哪天有人把随机位数缩短去换取"更好的排序性"，这条断言会先失败，而不是
// 等到生产环境出现主键冲突或 Session 碰撞。
func TestSameMillisecondIDsAreUnique(t *testing.T) {
	generator := identity.Generator{
		Clock:  clock.Fixed{Time: time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)},
		Random: rand.Reader,
	}
	const count = 20000
	seen := make(map[string]struct{}, count)
	for i := 0; i < count; i++ {
		id, err := generator.New(domain.IDSession)
		if err != nil {
			t.Fatal(err)
		}
		text := id.String()
		if _, duplicate := seen[text]; duplicate {
			t.Fatalf("同一毫秒内出现重复 ID（第 %d 个）", i)
		}
		seen[text] = struct{}{}
	}
}

// TestConcurrentGenerationIsUniqueAndValid 断言并发生成既不重复也不产生结构损坏的 ID。
// Generator 是值类型且没有内部可变状态，这条断言同时是「不得引入共享计数器/缓冲区」的
// 回归护栏——在 -race 下运行时，任何为了"优化"引入的共享状态都会被检测出来。
func TestConcurrentGenerationIsUniqueAndValid(t *testing.T) {
	generator := identity.NewGenerator(clock.System{})
	// goroutine 数固定为 8，不随核数放大：交叉执行的证明力来自"有多个 goroutine 同时调用"
	// 这个事实本身，而不是并发度大小；总量由每个 goroutine 的循环次数承担。
	const workers = 8
	const perWorker = 1024

	results := make([][]domain.ID, workers)
	var group sync.WaitGroup
	group.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func(worker int) {
			defer group.Done()
			batch := make([]domain.ID, 0, perWorker)
			for i := 0; i < perWorker; i++ {
				id, err := generator.New(domain.IDAPIToken)
				if err != nil {
					t.Error(err)
					return
				}
				batch = append(batch, id)
			}
			results[worker] = batch
		}(worker)
	}
	group.Wait()
	if t.Failed() {
		return
	}

	seen := make(map[string]struct{}, workers*perWorker)
	for _, batch := range results {
		for _, id := range batch {
			raw := uuidBytesOf(t, id)
			if raw[6]>>4 != 7 || raw[8]>>6 != 0b10 {
				t.Fatalf("并发生成的 ID %s 结构损坏", id)
			}
			if _, duplicate := seen[id.String()]; duplicate {
				t.Fatalf("并发生成出现重复 ID %s", id)
			}
			seen[id.String()] = struct{}{}
		}
	}
	if len(seen) != workers*perWorker {
		t.Fatalf("并发生成得到 %d 个唯一 ID，期望 %d", len(seen), workers*perWorker)
	}
}

// TestRandomBitsCoverEveryFreePosition 断言 74 个自由位每一位都真实取到过 0 和 1。
//
// 这是对"熵确实进入了每一个应当随机的位"的直接检验：掩码写错（例如把 raw[6] 整字节
// 覆盖成 0x70、或把变体位写成 raw[8] = 0x80）会让若干位恒定，而版本/变体断言完全看不出
// 差别——ID 仍然是结构合法的 UUIDv7，只是熵少了几位。反过来，版本与变体所在的固定位
// 必须恒定，否则说明位运算写反。
func TestRandomBitsCoverEveryFreePosition(t *testing.T) {
	generator := identity.NewGenerator(clock.Fixed{Time: time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)})
	var everZero, everOne [16]byte
	const samples = 4096
	for i := 0; i < samples; i++ {
		id, err := generator.New(domain.IDShare)
		if err != nil {
			t.Fatal(err)
		}
		raw := uuidBytesOf(t, id)
		for index, value := range raw {
			everOne[index] |= value
			everZero[index] |= ^value
		}
	}
	// raw[0:6] 是时间前缀，本次使用固定时钟，不参与自由位统计。
	freeMask := [16]byte{6: 0x0f, 7: 0xff, 8: 0x3f, 9: 0xff, 10: 0xff, 11: 0xff, 12: 0xff, 13: 0xff, 14: 0xff, 15: 0xff}
	for index, mask := range freeMask {
		if mask == 0 {
			continue
		}
		if stuck := mask &^ (everZero[index] & everOne[index]); stuck != 0 {
			t.Fatalf("字节 %d 的位 %08b 在 %d 个样本中恒定，随机部分的熵少于 74 位", index, stuck, samples)
		}
	}
	// 版本半字节恒为 0111：高位恒 0（只出现在 everZero），低三位恒 1（只出现在 everOne）。
	if everOne[6]&0xf0 != 0x70 || everZero[6]&0xf0 != 0x80 {
		t.Fatalf("版本位不恒为 0111：everOne=%08b everZero=%08b", everOne[6]&0xf0, everZero[6]&0xf0)
	}
	// 变体位恒为 10。
	if everOne[8]&0xc0 != 0x80 || everZero[8]&0xc0 != 0x40 {
		t.Fatalf("变体位不恒为 10：everOne=%08b everZero=%08b", everOne[8]&0xc0, everZero[8]&0xc0)
	}
}

// TestClockRegressionDoesNotBreakUniqueness 断言时钟回拨只影响排序性，不影响唯一性。
//
// 判断与边界：Generator 没有、也不应该有跨进程的单调化状态（那需要持久化的 last-timestamp
// 和序列号）。因此本包对外承诺的是「唯一」，而「近似有序」只在时钟单调前进时成立。真实
// 时钟回拨（NTP 步进、休眠唤醒、用户改时间）会让新 ID 排在旧 ID 之前；依赖 ID 顺序做
// 分页或游标的调用方必须另行使用显式时间戳列，不能把这条排序性当成保证。
func TestClockRegressionDoesNotBreakUniqueness(t *testing.T) {
	manual := clock.NewManual(time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC))
	generator := identity.Generator{Clock: manual, Random: rand.Reader}
	seen := make(map[string]struct{}, 1024)
	for i := 0; i < 1024; i++ {
		id, err := generator.New(domain.IDOverlayRevision)
		if err != nil {
			t.Fatal(err)
		}
		if _, duplicate := seen[id.String()]; duplicate {
			t.Fatalf("时钟回拨期间出现重复 ID（第 %d 个）", i)
		}
		seen[id.String()] = struct{}{}
		manual.Advance(-time.Millisecond)
	}
}

// TestNewConsumesExactlyTenRandomBytes 断言每次生成只从熵源取走 10 字节。
// 取多了会无谓消耗共享的 crypto/rand 读取；取少了意味着有位没有被随机填充。
func TestNewConsumesExactlyTenRandomBytes(t *testing.T) {
	counter := &countingReader{inner: rand.Reader}
	generator := identity.Generator{Clock: clock.System{}, Random: counter}
	if _, err := generator.New(domain.IDGrant); err != nil {
		t.Fatal(err)
	}
	if counter.count != 10 {
		t.Fatalf("单次生成读取了 %d 字节随机数据，期望 10", counter.count)
	}
}

type countingReader struct {
	inner io.Reader
	count int
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	r.count += n
	return n, err
}
