package media_test

// ParseSingleRange 的结果直接决定媒体正文读取的 seek 起点与长度。若 partial 为真
// 却给出越界或负长度的区间，就是对只读媒体文件的越界读原语，因此这里断言的是
// 硬性安全边界而不是协议细节：
//
//	partial == true  =>  err == nil && 0 <= Start <= End < size && Length() > 0
//	partial == false =>  区间必须是零值，且只有空 Range 头才允许 err == nil

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/media"
)

func FuzzParseSingleRange(f *testing.F) {
	for _, seed := range rangeHeaderSeeds() {
		for _, size := range []int64{-1, 0, 1, 2, 10, 1 << 20, 1<<63 - 1} {
			f.Add(seed, size)
		}
	}
	f.Fuzz(func(t *testing.T, header string, size int64) {
		interval, partial, err := media.ParseSingleRange(header, size)

		// 确定性：同一输入必须给出完全相同的结果。
		repeatInterval, repeatPartial, repeatErr := media.ParseSingleRange(header, size)
		if interval != repeatInterval || partial != repeatPartial || (err == nil) != (repeatErr == nil) {
			t.Fatalf("ParseSingleRange 不确定: %q size=%d", header, size)
		}

		if err != nil {
			if partial {
				t.Fatalf("失败路径不得声明 partial: %q size=%d", header, size)
			}
			if interval != (media.ByteRange{}) {
				t.Fatalf("失败路径必须返回零值区间: %q size=%d -> %+v", header, size, interval)
			}
			var structured *fault.Error
			if !errors.As(err, &structured) || structured.Code != fault.CodeRangeInvalid {
				t.Fatalf("拒绝必须是 RANGE_INVALID，实际 %v", err)
			}
			return
		}

		if !partial {
			// 唯一允许的「成功但不分段」情形是客户端根本没发 Range 头。
			if header != "" {
				t.Fatalf("非空 Range 头 %q 既未被拒绝也未产生分段", header)
			}
			if interval != (media.ByteRange{}) {
				t.Fatalf("整体响应必须返回零值区间: %+v", interval)
			}
			return
		}

		// partial == true 的硬性安全边界。
		if size <= 0 {
			t.Fatalf("size=%d 时不得产生分段: %q -> %+v", size, header, interval)
		}
		if interval.Start < 0 {
			t.Fatalf("区间起点为负: %q size=%d -> %+v", header, size, interval)
		}
		if interval.End < interval.Start {
			t.Fatalf("区间终点早于起点: %q size=%d -> %+v", header, size, interval)
		}
		if interval.End >= size {
			t.Fatalf("区间终点越界: %q size=%d -> %+v", header, size, interval)
		}
		if interval.Length() <= 0 {
			t.Fatalf("区间长度非正: %q size=%d -> %+v len=%d", header, size, interval, interval.Length())
		}
		if interval.Length() > size {
			t.Fatalf("区间长度超过文件大小: %q size=%d -> %+v len=%d", header, size, interval, interval.Length())
		}

		// 服务端回写的 Content-Range 必须能被自己重新解析成同一区间，否则
		// 客户端按 206 响应续传时会读到与服务端认定不同的字节窗口。
		echo := fmt.Sprintf("bytes=%d-%d", interval.Start, interval.End)
		echoInterval, echoPartial, echoErr := media.ParseSingleRange(echo, size)
		if echoErr != nil || !echoPartial || echoInterval != interval {
			t.Fatalf("回显区间 %q 无法往返: partial=%v interval=%+v err=%v", echo, echoPartial, echoInterval, echoErr)
		}
	})
}

func TestParseSingleRangeRejectsSignedPositions(t *testing.T) {
	const size = 100
	cases := []string{"bytes=+5-", "bytes=+5-+9", "bytes=-+5", "bytes=5-+9"}
	for _, header := range cases {
		interval, partial, err := media.ParseSingleRange(header, size)
		if err == nil || partial || interval != (media.ByteRange{}) {
			t.Fatalf("带符号 Range 位置未拒绝: %q -> %+v partial=%v err=%v", header, interval, partial, err)
		}
		var structured *fault.Error
		if !errors.As(err, &structured) || structured.Code != fault.CodeRangeInvalid || structured.Retryable {
			t.Fatalf("带符号 Range 位置必须返回不可重试 RANGE_INVALID: %q -> %v", header, err)
		}
	}
}

func rangeHeaderSeeds() []string {
	seeds := []string{
		// 空与整体响应
		"", " ", "bytes=",
		// 正常单区间
		"bytes=0-0", "bytes=0-1", "bytes=1-", "bytes=0-", "bytes=-1", "bytes=-5",
		"bytes=0-999999", "bytes=5-4", "bytes=9-9",
		// 越界与截断
		"bytes=100-", "bytes=100-200", "bytes=0-100", "bytes=-0", "bytes=-100000",
		// 语法偏差
		"bytes=+5-", "bytes=+5-+9", "bytes=-+5", "bytes=5-+9", "bytes= 5-9", "bytes=5 -9",
		"bytes=05-09", "bytes=0000000000000000000005-",
		"BYTES=0-1", "Bytes=0-1", "bytes =0-1", "bytes:0-1",
		// 多区间与非法单位
		"bytes=0-1,2-3", "bytes=0-1, 2-3", "items=0-1", "0-1", "bytes=bytes=0-1",
		// 溢出与畸形
		"bytes=9223372036854775807-", "bytes=9223372036854775808-",
		"bytes=-9223372036854775808", "bytes=-9223372036854775809",
		"bytes=99999999999999999999-", "bytes=0-99999999999999999999",
		"bytes=--1", "bytes=1--2", "bytes=1-2-3", "bytes=a-b", "bytes=-", "bytes=0x10-",
		"bytes=1_0-", "bytes=１-２",
		// 控制字符与超长
		"bytes=0-1\x00", "bytes=0-\n1", "bytes=\x000-1",
		"bytes=" + strings.Repeat("0", 4096) + "1-",
	}
	return seeds
}
