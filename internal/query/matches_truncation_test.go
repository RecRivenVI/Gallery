package query

import (
	"strings"
	"testing"
)

// TestComputeMatchesClipsSpansToTruncatedValue 覆盖阶段 4 收尾：computeMatches 必须先对
// 完整原文计算 span，再按 maxMatchValueRunes 截断返回的 value；命中如果落在截断边界
// 之后必须整体丢弃，跨越边界的命中必须把 End 收紧到截断长度，任何返回的 span 都不得
// 越界指向 value 之外的字符（0 <= start <= end <= runeCount(value)）。
func TestComputeMatchesClipsSpansToTruncatedValue(t *testing.T) {
	needle := "查找词"
	// 构造一个长度远超 maxMatchValueRunes 的 tag：needle 出现在边界之前（完全可见）、
	// 恰好跨越边界（前半截可见、后半截被截断）、以及完全在边界之后（截断后不可见）。
	before := strings.Repeat("甲", 100) + needle // 命中完全在截断边界之前
	straddle := strings.Repeat("乙", maxMatchValueRunes-len([]rune(needle))/2-1) + needle
	after := strings.Repeat("丙", maxMatchValueRunes+50) + needle // 命中完全在截断边界之后

	matches := computeMatches(needle, "", "", []string{before, straddle, after}, nil)
	if len(matches) != 2 {
		t.Fatalf("完全越界的命中应被丢弃，期望 2 个 tag 命中，实际 %d: %+v", len(matches), matches)
	}
	for _, match := range matches {
		valueRuneCount := len([]rune(match.Value))
		if valueRuneCount > maxMatchValueRunes {
			t.Fatalf("value 未按 maxMatchValueRunes 截断: len=%d", valueRuneCount)
		}
		for _, span := range match.Spans {
			if span.Start < 0 || span.End < span.Start || span.End > valueRuneCount {
				t.Fatalf("span 越界: span=%+v valueRuneCount=%d value=%q", span, valueRuneCount, match.Value)
			}
		}
	}
	// straddle 命中必须被裁剪（End 收紧到 maxMatchValueRunes），而不是整体丢弃，因为它
	// 的起点仍在截断边界之前。
	found := false
	for _, match := range matches {
		if len([]rune(match.Value)) == maxMatchValueRunes {
			found = true
			for _, span := range match.Spans {
				if span.End != maxMatchValueRunes {
					t.Fatalf("跨越截断边界的命中应把 End 收紧到 maxMatchValueRunes: span=%+v", span)
				}
			}
		}
	}
	if !found {
		t.Fatal("跨越截断边界的命中应被裁剪保留，而不是整体丢弃")
	}
}
