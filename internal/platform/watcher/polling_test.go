package watcher

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/ports"
)

// Polling 是原生 Watcher 不可用时的 fallback。它提供的是 **dirty hint**，不是事实源：
// 周期性全量收敛与内容哈希才决定 Catalog 里有什么（见 ports.FileWatcher 的说明）。
// 因此本文件断言两类事实：
//
//  1. **不得变成 I/O 灾难**：默认轮询间隔与事件预算必须在退化输入下仍被夹取，否则一次
//     `NewPolling(0, 0)` 就会在 HDD/NAS 上变成不间断的递归全树 walk（并且 time.NewTicker(0)
//     直接 panic）；
//  2. **提示语义本身**：创建/修改/删除被识别、链接与目录不产生噪声、路径键跨平台稳定、
//     无法给出可信增量时降级为 overflow 而不是悄悄漏报。
//
// 同时把已知的漏报边界固定下来（见 TestPollingMissesSameSizeSameTimestampRewrite）：
// 这不是缺陷，而是"提示"与"事实源"分工的直接后果，写成断言是为了防止有人把它当成
// 可以据以跳过全量校验的保证。

// 本文件的轮询间隔取值：足够短以便测试及时收敛，又足够长以避免在慢盘上把整轮测试
// 变成连续 walk。
const testInterval = 25 * time.Millisecond

// awaitEvent 在预算内等待第一个满足 accept 的事件，并在通道被提前关闭时立刻失败。
func awaitEvent(t *testing.T, events <-chan ports.WatchEvent, accept func(ports.WatchEvent) bool) ports.WatchEvent {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case event, open := <-events:
			if !open {
				t.Fatal("事件通道在等到目标事件之前被关闭")
			}
			if event.At.Location() != time.UTC {
				t.Fatalf("事件时间戳不是 UTC：%v", event.At.Location())
			}
			if accept(event) {
				return event
			}
		case <-deadline:
			t.Fatal("在预算内没有等到目标事件")
		}
	}
}

// TestNewPollingClampsDegenerateOptions 断言退化参数被夹取为安全默认值。
//
// interval <= 0 尤其关键：Watch 内部用它构造 time.NewTicker，非正值会直接 panic；即便
// 不 panic，一个 0 间隔的轮询也会对整棵 Source 树发起不间断的递归 walk，在 HDD 与
// SMB/NAS 上等同于自我拒绝服务。maxEvents 同时是事件通道的缓冲长度，非正值会让通道
// 无缓冲，任何一次消费者短暂停顿都会把轮询协程阻塞在 send 上。
func TestNewPollingClampsDegenerateOptions(t *testing.T) {
	for name, item := range map[string]struct {
		interval      time.Duration
		maxEvents     int
		wantInterval  time.Duration
		wantMaxEvents int
	}{
		"零间隔":      {0, 10, 5 * time.Minute, 10},
		"负间隔":      {-time.Second, 10, 5 * time.Minute, 10},
		"零事件预算":    {time.Second, 0, time.Second, 4096},
		"负事件预算":    {time.Second, -1, time.Second, 4096},
		"正常取值保持不变": {2 * time.Second, 32, 2 * time.Second, 32},
	} {
		t.Run(name, func(t *testing.T) {
			polling := NewPolling(item.interval, item.maxEvents)
			if polling.interval != item.wantInterval {
				t.Fatalf("interval = %s，期望 %s", polling.interval, item.wantInterval)
			}
			if polling.maxEvents != item.wantMaxEvents {
				t.Fatalf("maxEvents = %d，期望 %d", polling.maxEvents, item.wantMaxEvents)
			}
			if polling.interval <= 0 {
				t.Fatal("间隔仍为非正值：time.NewTicker 会 panic")
			}
			if polling.maxEvents < 1 {
				t.Fatal("事件预算仍为非正值：事件通道将无缓冲")
			}
		})
	}
}

// TestWatchRejectsMissingRootBeforeStartingGoroutine 断言不存在的根在 Watch 返回前就失败。
// 失败必须发生在启动轮询协程与 ticker 之前：否则一个打错的 Source 路径会留下一个永远
// 在报错、永远在 walk 的后台协程。
func TestWatchRejectsMissingRootBeforeStartingGoroutine(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events, err := NewPolling(testInterval, 8).Watch(ctx, filepath.Join(t.TempDir(), "absent"))
	if err == nil {
		t.Fatal("不存在的根应返回错误")
	}
	if events != nil {
		t.Fatal("返回错误时不得同时返回事件通道")
	}
}

// TestWatchReportsCreateModifyRemove 断言三类基本变化都能被识别，且路径是相对根的。
func TestWatchReportsCreateModifyRemove(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events, err := NewPolling(testInterval, 64).Watch(ctx, root)
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(root, "work", "media.bin")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	created := awaitEvent(t, events, func(e ports.WatchEvent) bool { return e.Kind == ports.WatchCreated })
	if created.RelativePath != "work/media.bin" {
		t.Fatalf("创建事件路径为 %q，期望 work/media.bin", created.RelativePath)
	}

	if err := os.WriteFile(target, []byte("second value"), 0o600); err != nil {
		t.Fatal(err)
	}
	modified := awaitEvent(t, events, func(e ports.WatchEvent) bool { return e.Kind == ports.WatchModified })
	if modified.RelativePath != "work/media.bin" {
		t.Fatalf("修改事件路径为 %q", modified.RelativePath)
	}

	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	removed := awaitEvent(t, events, func(e ports.WatchEvent) bool { return e.Kind == ports.WatchRemoved })
	if removed.RelativePath != "work/media.bin" {
		t.Fatalf("删除事件路径为 %q", removed.RelativePath)
	}
}

// TestWatchUsesForwardSlashRelativePaths 断言事件路径始终使用 `/` 分隔且相对于根。
// 这些路径会作为 dirty hint 的键与扫描器的 source key 空间对齐；在 Windows 上混入 `\`
// 会让同一个文件在两条链路上表现为两个不同的键。
func TestWatchUsesForwardSlashRelativePaths(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events, err := NewPolling(testInterval, 64).Watch(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b", "c", "media.bin")
	if err := os.MkdirAll(filepath.Dir(nested), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nested, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	event := awaitEvent(t, events, func(e ports.WatchEvent) bool { return e.Kind == ports.WatchCreated })
	if event.RelativePath != "a/b/c/media.bin" {
		t.Fatalf("路径为 %q，期望 a/b/c/media.bin", event.RelativePath)
	}
	if filepath.IsAbs(event.RelativePath) {
		t.Fatalf("事件泄露了绝对路径：%q", event.RelativePath)
	}
}

// TestWatchIgnoresDirectoriesThemselves 断言目录本身不产生事件。
//
// 目录只是容器：为每个新建目录都发一次 hint 会在一次批量导入中把事件预算瞬间打满，
// 从而把本可以精确定位的增量降级成 overflow 全量重扫。这里创建一棵只有目录的子树，
// 随后创建一个真正的文件，断言先到达的事件就是那个文件——中间没有任何目录事件。
func TestWatchIgnoresDirectoriesThemselves(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events, err := NewPolling(testInterval, 64).Watch(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "one", "two", "three"), 0o700); err != nil {
		t.Fatal(err)
	}
	// 给若干轮轮询的时间，确认目录本身不会产生任何事件。
	select {
	case event := <-events:
		t.Fatalf("目录产生了事件：%+v", event)
	case <-time.After(6 * testInterval):
	}
	if err := os.WriteFile(filepath.Join(root, "one", "media.bin"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	event := awaitEvent(t, events, func(e ports.WatchEvent) bool { return true })
	if event.Kind != ports.WatchCreated || event.RelativePath != "one/media.bin" {
		t.Fatalf("首个事件不是文件创建：%+v", event)
	}
}

// TestWatchIgnoresLinks 断言链接不进入快照。
//
// 与扫描器共用 filesystem.IsLink 的理由见该函数的说明：Windows junction 报告为
// fs.ModeIrregular 且 IsDir() 为 false，只判断 fs.ModeSymlink 会让它作为"一个普通文件"
// 进入快照，于是每一轮轮询都可能围绕它产生与真实媒体无关的 dirty hint。
func TestWatchIgnoresLinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "target.bin"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events, err := NewPolling(testInterval, 64).Watch(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Join(outside, "target.bin"), link); err != nil {
		// Windows 未开启开发者模式时创建符号链接需要特权；此时无法执行本断言。
		t.Skipf("当前环境无法创建符号链接: %v", err)
	}
	select {
	case event := <-events:
		t.Fatalf("链接产生了事件：%+v", event)
	case <-time.After(6 * testInterval):
	}
}

// TestWatchDegradesToOverflowWhenChangesExceedBudget 断言变化数量超过预算时降级为单个
// overflow 事件。
//
// 这是正确的失败方向：Watcher 只是提示，宁可告诉调用方"我说不准，去做全量收敛"，
// 也不能截断成前 N 个变化后让剩下的静默消失——那会让被截断的文件一直不进入 Catalog。
func TestWatchDegradesToOverflowWhenChangesExceedBudget(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// 间隔取得比创建循环长，使这批变化尽可能落在同一轮 diff 中。
	events, err := NewPolling(500*time.Millisecond, 2).Watch(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 64; i++ {
		name := filepath.Join(root, "media-"+string(rune('a'+i%26))+"-"+time.Now().Format("150405.000000000"))
		if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	event := awaitEvent(t, events, func(e ports.WatchEvent) bool { return e.Kind == ports.WatchOverflow })
	if !event.Overflow {
		t.Fatal("overflow 事件未设置 Overflow 标志")
	}
	if event.RelativePath != "" {
		t.Fatalf("overflow 事件不应携带具体路径，实际 %q", event.RelativePath)
	}
}

// TestWatchDegradesToOverflowWhenScanFails 断言扫描失败降级为 overflow 而不是静默或退出。
//
// 根目录在运行期消失（卸载的 NAS、拔掉的外置盘、被用户移走的 Source）是真实情况。此时
// 轮询无法给出可信增量；正确行为是告诉调用方"这里不可信"，并在根恢复后继续工作，而不是
// 结束协程让 Watcher 永久静默。
func TestWatchDegradesToOverflowWhenScanFails(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "source")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events, err := NewPolling(testInterval, 8).Watch(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	event := awaitEvent(t, events, func(e ports.WatchEvent) bool { return e.Kind == ports.WatchOverflow })
	if !event.Overflow {
		t.Fatal("扫描失败产生的事件未设置 Overflow 标志")
	}

	// 根恢复后仍能继续给出增量：一次失败不得让轮询协程退出。
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "media.bin"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	awaitEvent(t, events, func(e ports.WatchEvent) bool {
		return e.Kind == ports.WatchCreated && e.RelativePath == "media.bin"
	})
}

// TestWatchClosesChannelOnContextCancel 断言取消上下文会关闭事件通道并结束协程。
// 通道关闭是消费者判断"这个 Watcher 结束了"的唯一信号；不关闭会让 range 永久阻塞。
func TestWatchClosesChannelOnContextCancel(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	events, err := NewPolling(testInterval, 8).Watch(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case _, open := <-events:
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("取消上下文后事件通道未在预算内关闭")
		}
	}
}

// TestWatchStopsWhenConsumerStalledAndContextCancelled 断言消费者停止读取、缓冲写满之后，
// 取消上下文仍能让轮询协程退出。send 在 ctx.Done 与写入之间做 select 正是为此；缺了这一
// 条，一个停止消费的调用方就会永久钉住一个协程和一个 ticker。
func TestWatchStopsWhenConsumerStalledAndContextCancelled(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	events, err := NewPolling(testInterval, 1).Watch(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	// 制造远多于缓冲长度的变化，且完全不消费。
	for i := 0; i < 8; i++ {
		if err := os.WriteFile(filepath.Join(root, "media-"+time.Now().Format("150405.000000000")), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * testInterval)
	}
	cancel()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case _, open := <-events:
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("消费者停滞时取消上下文未能结束轮询协程")
		}
	}
}

// TestPollingMissesSameSizeSameTimestampRewrite 把已知漏报边界固定成断言。
//
// 快照只记录 size/mode/mtime。一次"长度相同、且 mtime 被还原"的原地改写在两轮快照之间
// 完全不可见，因此不会产生任何事件。这不是可以顺手修掉的缺陷——要发现它必须读完整内容
// 并哈希，那正是周期性全量收敛与 ContentBlob 身份负责的事情，不属于一个必须在 HDD/NAS
// 上保持低成本的提示型 Watcher。
//
// 把它写成断言的意义在于：任何人想据此把"有 Watcher 就可以跳过全量校验"写进调度策略时，
// 会先在这里看到边界。
func TestPollingMissesSameSizeSameTimestampRewrite(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "media.bin")
	if err := os.WriteFile(target, []byte("aaaa"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events, err := NewPolling(testInterval, 8).Watch(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("bbbb"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(target, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		t.Fatalf("等长且 mtime 被还原的改写产生了事件 %+v；"+
			"若 Polling 已能发现这类改写，说明快照口径已变，本注释描述的边界需要同步修订", event)
	case <-time.After(6 * testInterval):
	}
}
