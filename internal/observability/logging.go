package observability

import (
	"io"
	"log/slog"
)

// NewLogger 构造服务端唯一的结构化日志入口。
//
// 职责边界（有意为之，不是遗漏）：本函数只做两件事——选择 JSON 编码与最低级别，并把
// **记录自身的时间戳**规范化为固定宽度、毫秒精度的 UTC 文本。它**不做任何脱敏**：
// attr 的键与值原样进入输出。
//
// 判断依据：脱敏无法在这一层正确完成。slog 的 attr 键是自由文本，基于键名的黑名单既
// 必然不完整（任何新键都会漏），又会静默吞掉运维真正需要的字段；而到达 handler 的值
// 已经是materialized 的字符串/数字，handler 无从判断它是凭据还是普通标识。因此
// "不得输出 secret、token、Cookie、私密 metadata、完整媒体路径"这条产品边界的执行点
// 在**调用方**——不把这些东西放进 attr——而不是在这里事后擦除。把这条边界用测试固定
// 下来，是为了防止后来的读者误以为"日志层会兜底"，从而放松调用点的纪律。
func NewLogger(output io.Writer, level slog.Leveler) *slog.Logger {
	return slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			// 只重写记录自身的时间戳。slog 保证内建 attr（time/level/source/msg）以
			// groups == nil 传入，因此 len(groups) == 0 把范围限定在顶层。
			//
			// Kind 检查同样是必需的而不是防御性冗余：调用方写出
			// logger.Info("m", "time", "12:00") 时，这个用户 attr 的键同样是 slog.TimeKey
			// 且 groups 为 nil；无条件调用 Value.Time() 会 panic（"Value kind is String,
			// not Time"）。日志调用大量出现在错误处理路径上，让一次日志把进程打崩是
			// 不可接受的失败模式，所以这里对非 time.Time 的同名 attr 原样放行。
			if len(groups) == 0 && attr.Key == slog.TimeKey && attr.Value.Kind() == slog.KindTime {
				attr.Value = slog.StringValue(attr.Value.Time().UTC().Format("2006-01-02T15:04:05.000Z07:00"))
			}
			return attr
		},
	}))
}
