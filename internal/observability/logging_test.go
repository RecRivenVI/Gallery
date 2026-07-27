package observability_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/observability"
)

// NewLogger 是 galleryd 唯一的日志入口（cmd/galleryd/main.go 与 bootstrap 全链路共用同一个
// *slog.Logger）。本文件断言两类事实：
//
//  1. **输出契约**：JSON、级别过滤、以及记录时间戳被规范化为固定宽度毫秒精度 UTC 文本——
//     日志时间戳是事故复盘时对齐多台设备的唯一依据，格式随本地时区漂移会让它失去意义。
//  2. **职责边界**：ReplaceAttr 只重写时间戳，不做脱敏。这条边界是有意的，见 logging.go
//     的说明；用测试固定它，是为了防止后来的读者误以为日志层会兜底而放松调用点纪律。
//
// 日志本身处在错误处理路径上，因此"记录一条日志"必须永不 panic——这也是下面
// TestLoggerDoesNotPanicOnAttributeNamedTime 存在的原因。

func decodeRecords(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	records := make([]map[string]any, 0, 4)
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("日志行不是合法 JSON：%q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

// TestLoggerEmitsOneJSONObjectPerRecord 断言输出是逐行 JSON 对象，且携带标准字段。
// 日志被机器消费（testlab 的 galleryd 日志、未来的诊断包），非 JSON 行会直接破坏解析。
func TestLoggerEmitsOneJSONObjectPerRecord(t *testing.T) {
	var buffer bytes.Buffer
	logger := observability.NewLogger(&buffer, slog.LevelInfo)
	logger.Info("galleryd_started", "address", "127.0.0.1:0", "mode", "personal")
	logger.Error("galleryd_failed", "error", errors.New("boom").Error())

	records := decodeRecords(t, buffer.Bytes())
	if len(records) != 2 {
		t.Fatalf("期望 2 条记录，实际 %d", len(records))
	}
	if records[0]["msg"] != "galleryd_started" || records[0]["level"] != "INFO" {
		t.Fatalf("首条记录字段不符：%v", records[0])
	}
	if records[0]["address"] != "127.0.0.1:0" || records[0]["mode"] != "personal" {
		t.Fatalf("首条记录未携带调用方 attr：%v", records[0])
	}
	if records[1]["msg"] != "galleryd_failed" || records[1]["level"] != "ERROR" {
		t.Fatalf("第二条记录字段不符：%v", records[1])
	}
}

// TestLoggerNormalisesRecordTimestampToUTCMilliseconds 断言时间戳被重写为固定宽度、
// 毫秒精度的 UTC 文本。
//
// 断言方式刻意不比较字面量：本地时区为 UTC 的机器上，"没有做时区转换"与"做了转换"
// 会给出相同结果，因此这里在测试进程内把本地时区改成一个非零偏移，再要求输出仍以
// `Z` 结尾且与 UTC 挂钟一致。
func TestLoggerNormalisesRecordTimestampToUTCMilliseconds(t *testing.T) {
	original := time.Local
	time.Local = time.FixedZone("probe", 9*60*60)
	t.Cleanup(func() { time.Local = original })

	before := time.Now().UTC().Truncate(time.Millisecond)
	var buffer bytes.Buffer
	observability.NewLogger(&buffer, slog.LevelInfo).Info("probe")
	after := time.Now().UTC()

	records := decodeRecords(t, buffer.Bytes())
	if len(records) != 1 {
		t.Fatalf("期望 1 条记录，实际 %d", len(records))
	}
	text, ok := records[0][slog.TimeKey].(string)
	if !ok {
		t.Fatalf("time 字段不是字符串：%v", records[0][slog.TimeKey])
	}
	// 固定宽度：`2006-01-02T15:04:05.000Z`，毫秒恒为三位，便于按文本排序与对齐。
	if len(text) != len("2006-01-02T15:04:05.000Z") || !strings.HasSuffix(text, "Z") {
		t.Fatalf("时间戳 %q 不是固定宽度的 UTC 文本，本地时区仍在影响输出", text)
	}
	if text[len(text)-5] != '.' {
		t.Fatalf("时间戳 %q 不是毫秒精度", text)
	}
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		t.Fatalf("时间戳 %q 无法按 RFC3339 解析: %v", text, err)
	}
	if parsed.Before(before) || parsed.After(after) {
		t.Fatalf("时间戳 %s 不在 [%s, %s] 区间内", parsed, before, after)
	}
}

// TestLoggerFiltersByLevel 断言级别过滤生效，且 Leveler 是动态求值的。
// galleryd 以 LevelInfo 构造；调试级别的噪声不得进入生产日志，而排障时通过共享
// LevelVar 提高详细度也不能要求重建 logger。
func TestLoggerFiltersByLevel(t *testing.T) {
	var buffer bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)
	logger := observability.NewLogger(&buffer, level)

	logger.Debug("suppressed")
	if buffer.Len() != 0 {
		t.Fatalf("低于阈值的记录被输出：%s", buffer.String())
	}
	level.Set(slog.LevelDebug)
	logger.Debug("emitted")
	records := decodeRecords(t, buffer.Bytes())
	if len(records) != 1 || records[0]["msg"] != "emitted" {
		t.Fatalf("提高详细度后未输出 Debug 记录：%v", records)
	}
}

// TestLoggerDoesNotRedactAttributeValues 断言职责边界：ReplaceAttr 不脱敏。
//
// 判断见 logging.go 的说明——脱敏无法在 handler 层正确完成（键是自由文本，黑名单必然
// 不完整；值到达 handler 时已经materialized，无从判断语义），因此产品边界"不得输出
// secret、token、Cookie、私密 metadata、完整媒体路径"的执行点在调用方。这条断言把边界
// 钉死：如果哪天有人在这里加了半套基于键名的擦除，本用例会失败，并强制他把这个决定
// 摆到明面上讨论，而不是让调用方误以为已经有兜底。
func TestLoggerDoesNotRedactAttributeValues(t *testing.T) {
	var buffer bytes.Buffer
	logger := observability.NewLogger(&buffer, slog.LevelInfo)
	// 这些取值只是本用例构造的字面量，不是任何真实凭据。
	logger.Info("probe",
		"token", "test-token-value",
		"password", "test-password-value",
		"cookie", "session=test-cookie-value",
		"path", "/probe/absolute/path",
	)
	records := decodeRecords(t, buffer.Bytes())
	if len(records) != 1 {
		t.Fatalf("期望 1 条记录，实际 %d", len(records))
	}
	for key, want := range map[string]string{
		"token":    "test-token-value",
		"password": "test-password-value",
		"cookie":   "session=test-cookie-value",
		"path":     "/probe/absolute/path",
	} {
		if got := records[0][key]; got != want {
			t.Fatalf("attr %q 被改写：期望 %q，实际 %v。日志层不承担脱敏职责；"+
				"要改变这条边界必须同时更新 logging.go 的说明与调用点纪律", key, want, got)
		}
	}
}

// TestLoggerPreservesGroupedAndNestedAttributes 断言分组 attr 原样嵌套输出。
// ReplaceAttr 会对每一个非分组 attr 调用一次，包括分组内部的；只要它按 groups 正确
// 限定范围，嵌套结构就应当完全保留。
func TestLoggerPreservesGroupedAndNestedAttributes(t *testing.T) {
	var buffer bytes.Buffer
	logger := observability.NewLogger(&buffer, slog.LevelInfo)
	logger.With("job", "job_x").WithGroup("request").Info("probe", "method", "GET", "status", 200)

	records := decodeRecords(t, buffer.Bytes())
	if len(records) != 1 {
		t.Fatalf("期望 1 条记录，实际 %d", len(records))
	}
	if records[0]["job"] != "job_x" {
		t.Fatalf("With 附加的 attr 丢失：%v", records[0])
	}
	group, ok := records[0]["request"].(map[string]any)
	if !ok {
		t.Fatalf("分组未以嵌套对象输出：%v", records[0]["request"])
	}
	if group["method"] != "GET" || group["status"] != float64(200) {
		t.Fatalf("分组内 attr 不符：%v", group)
	}
}

// TestLoggerDoesNotPanicOnAttributeNamedTime 是本包最重要的健壮性断言。
//
// ReplaceAttr 在收到键为 `time` 的 attr 时会调用 slog.Value.Time()，而该方法对非
// time.Time 的值直接 panic（"Value kind is String, not Time"）。`time` 是极其普通的
// 业务词，调用方写出 logger.Info("...", "time", elapsed.String()) 是完全自然的；
// 日志调用又大量分布在错误处理路径上，让"记录一条日志"把进程打崩是不可接受的。
//
// 因此这里覆盖顶层 attr、With 预计算 attr、WithGroup 与内联 Group 五种形态，断言不 panic、
// 输出仍是合法 JSON，且记录自身的规范化时间戳仍然被写出。
//
// 已知且不在本层修复的残留：顶层用户 attr 命名为 `time` 时，JSON 里会出现两个 `time` 键
// （记录时间戳在前、用户值在后）。这是合法 JSON，但多数消费者按"后者胜"解析，等于用户值
// 遮蔽了记录时间戳。要消除它只能改写用户键名，那是比 panic 更大的语义变更，属于调用点
// 命名纪律问题，这里只把事实记录下来。
func TestLoggerDoesNotPanicOnAttributeNamedTime(t *testing.T) {
	normalised := regexp.MustCompile(`"time":"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z"`)
	cases := map[string]func(*slog.Logger){
		"顶层字符串":     func(l *slog.Logger) { l.Info("probe", "time", "12:00:00") },
		"顶层整数":      func(l *slog.Logger) { l.Info("probe", "time", 42) },
		"With 预计算":  func(l *slog.Logger) { l.With("time", "12:00:00").Info("probe") },
		"WithGroup": func(l *slog.Logger) { l.WithGroup("request").Info("probe", "time", "12:00:00") },
		"内联 Group": func(l *slog.Logger) {
			l.Info("probe", slog.Group("request", slog.String("time", "12:00:00")))
		},
	}
	for name, emit := range cases {
		t.Run(name, func(t *testing.T) {
			var buffer bytes.Buffer
			logger := observability.NewLogger(&buffer, slog.LevelInfo)
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("记录一条日志导致 panic：%v", recovered)
				}
			}()
			emit(logger)
			if records := decodeRecords(t, buffer.Bytes()); len(records) != 1 {
				t.Fatalf("期望 1 条记录，实际 %d", len(records))
			}
			// 直接在原始行上断言：顶层同名 attr 会让 JSON 出现重复的 `time` 键，
			// 解码成 map 后只剩最后一个，因此不能用解码结果检验记录时间戳是否仍被写出。
			if !normalised.Match(buffer.Bytes()) {
				t.Fatalf("记录自身的规范化时间戳未写出：%s", buffer.String())
			}
		})
	}
}

// TestLoggerIsSafeForConcurrentUse 断言同一个 *slog.Logger 可以被多个 goroutine 并发使用
// 而不撕裂输出：galleryd 把同一个 logger 传给 HTTP、调度器、Watcher 与各服务，任何一行
// 被交错写入都会破坏 JSON 解析。本用例在 -race 下同时充当竞态回归护栏。
func TestLoggerIsSafeForConcurrentUse(t *testing.T) {
	var buffer bytes.Buffer
	// slog 的内建 handler 用互斥量保护 io.Writer；这里直接使用未加锁的 buffer，
	// 正是为了检验这一保证确实生效。
	logger := observability.NewLogger(&buffer, slog.LevelInfo)

	// goroutine 数固定且小：交错写入的证明力来自"多个 goroutine 同时写同一个未加锁的
	// buffer"，不需要按核数放大。
	const workers = 6
	const perWorker = 96
	var group sync.WaitGroup
	group.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func(worker int) {
			defer group.Done()
			for i := 0; i < perWorker; i++ {
				logger.Info("probe", "worker", worker, "index", i)
			}
		}(worker)
	}
	group.Wait()

	records := decodeRecords(t, buffer.Bytes())
	if len(records) != workers*perWorker {
		t.Fatalf("并发写入得到 %d 条可解析记录，期望 %d", len(records), workers*perWorker)
	}
}
