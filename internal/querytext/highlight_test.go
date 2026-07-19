package querytext_test

import (
	"testing"

	"github.com/RecRivenVI/gallery/internal/querytext"
)

// TestHighlightSpansGoldenCorpus 锁定高亮偏移映射在阶段 4 要求的黄金语料场景下的行为：
// 大小写折叠、全角半角、组合字符、连字扩展（一个原始 code point 展开为多个规范化字符，
// 反方向验证多个原始 code point 折叠为一个规范化字符）、emoji 变体选择符、CJK、拉丁扩展
// 字符（重音不被折叠）、文件名与扩展名。所有偏移均为原文 code point（rune）区间。组合
// 字符、连字和 emoji 变体选择符样本用显式 rune 码位拼接而非源文件字面量构造，保证实际
// code point 序列不受编辑/存储链路的规范化影响。
func TestHighlightSpansGoldenCorpus(t *testing.T) {
	combiningAcute := "caf" + "e" + string(rune(0x0301))           // c a f + e + U+0301 COMBINING ACUTE ACCENT（分解形式）
	precomposedCafe := "caf" + string(rune(0x00E9))                // c a f + U+00E9 é（预组合形式）
	ligatureFile := string(rune(0xFB01)) + "le"                    // U+FB01 LATIN SMALL LIGATURE FI + l e
	heavyBlackHeart := string(rune(0x2764)) + string(rune(0xFE0F)) // U+2764 HEAVY BLACK HEART + U+FE0F VARIATION SELECTOR-16

	cases := []struct {
		name     string
		original string
		query    string
		want     []querytext.Span
	}{
		{
			name: "大小写折叠", original: "HELLO World",
			query: querytext.Normalize("hello"), want: []querytext.Span{{Start: 0, End: 5}},
		},
		{
			name: "全角半角与数字", original: "ＡＢＣ123",
			query: querytext.Normalize("abc123"), want: []querytext.Span{{Start: 0, End: 6}},
		},
		{
			// 5 个原始 code point（caf + e + 组合重音）；NFKC 把 e+U+0301 组合为单个 é，
			// 规范化文本只剩 4 个 rune，命中区间必须映射回全部 5 个原始 code point。
			name:     "组合字符_多个原始code point折叠为一个规范化字符",
			original: combiningAcute,
			query:    querytext.Normalize(precomposedCafe),
			want:     []querytext.Span{{Start: 0, End: 5}},
		},
		{
			// 连字 U+FB01 是单个原始 code point；NFKC 把它分解为 f、i 两个规范化 rune，
			// 即一个原始 code point 展开为多个规范化字符。
			name:     "连字扩展_一个原始code point展开为多个规范化字符",
			original: ligatureFile,
			query:    querytext.Normalize("file"),
			want:     []querytext.Span{{Start: 0, End: 3}},
		},
		{
			name:     "emoji变体选择符簇",
			original: "喜欢" + heavyBlackHeart + "这张图",
			query:    querytext.Normalize(heavyBlackHeart),
			want:     []querytext.Span{{Start: 2, End: 4}},
		},
		{
			name: "CJK双字连续", original: "东京都渋谷区写真集",
			query: querytext.Normalize("渋谷"), want: []querytext.Span{{Start: 3, End: 5}},
		},
		{
			// é 是预组合形式；NFKC/case fold 不剥离重音，本用例只验证精确重音查询能
			// 命中自身（不测试不带重音的查询命中带重音原文，因为规范上不应命中）。
			name:     "拉丁扩展重音不被折叠",
			original: precomposedCafe + " au lait",
			query:    querytext.Normalize(precomposedCafe),
			want:     []querytext.Span{{Start: 0, End: 4}},
		},
		{
			name: "文件名与扩展名", original: "IMG_0002.JPG",
			query: querytext.Normalize("0002.jpg"), want: []querytext.Span{{Start: 4, End: 12}},
		},
		{
			name: "无命中", original: "hello world",
			query: querytext.Normalize("xyz"), want: nil,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := querytext.HighlightSpans(testCase.original, testCase.query)
			if len(got) != len(testCase.want) {
				t.Fatalf("span 数量 = %d，want %d（got=%v）", len(got), len(testCase.want), got)
			}
			for index := range got {
				if got[index] != testCase.want[index] {
					t.Fatalf("span[%d] = %+v，want %+v", index, got[index], testCase.want[index])
				}
			}
		})
	}
}

// TestHighlightSpansMultipleOccurrences 验证多次命中都被定位且不重叠。
func TestHighlightSpansMultipleOccurrences(t *testing.T) {
	spans := querytext.HighlightSpans("ababab", querytext.Normalize("ab"))
	if len(spans) != 3 {
		t.Fatalf("span 数量 = %d，want 3（got=%v）", len(spans), spans)
	}
	want := []querytext.Span{{Start: 0, End: 2}, {Start: 2, End: 4}, {Start: 4, End: 6}}
	for index := range spans {
		if spans[index] != want[index] {
			t.Fatalf("span[%d] = %+v，want %+v", index, spans[index], want[index])
		}
	}
}

func TestHighlightSpansEmptyInputs(t *testing.T) {
	if got := querytext.HighlightSpans("", querytext.Normalize("x")); got != nil {
		t.Fatalf("空原文应无命中，got %v", got)
	}
	if got := querytext.HighlightSpans("hello", ""); got != nil {
		t.Fatalf("空查询应无命中，got %v", got)
	}
}
