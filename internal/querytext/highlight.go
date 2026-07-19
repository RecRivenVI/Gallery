package querytext

import (
	"strings"
	"unicode"
)

// Span 是原始显示文本中的一个高亮区间，单位为 code point（rune）偏移，左闭右开。
// 不是 UTF-16 code unit，也不是字节偏移；调用方必须按此单位切片原文。
type Span struct {
	Start int
	End   int
}

// HighlightSpans 把规范化查询词在原始显示文本中的命中位置映射回原文 code point 偏移。
//
// 算法：先把原文按"簇"切分——每个簇是一个基础 rune 加上紧随其后的组合标记
// （Unicode 类别 Mn/Mc/Me）与常见 emoji 修饰符（变体选择符 U+FE0F/U+FE0E、
// ZWJ U+200D、肤色修饰符 U+1F3FB-U+1F3FF）；每个簇整体做 NFKC + Unicode 大小写
// 折叠后拼接为规范化文本，同时记录每个规范化 rune 来自哪个簇。命中区间按规范化
// rune 下标定位后，通过来源簇映射回原文 rune 区间。
//
// 已记录的简化：真正的 Unicode 默认大小写折叠极少数情况依赖跨字符上下文（例如
// 希腊语词尾 sigma），簇级独立折叠不保证与整串一次性折叠逐字节一致；本函数不
// 用于生成规范化候选（那仍由 Normalize 对整串处理），只用于高亮映射，因此该简化
// 只影响极端边界样本的高亮范围，不影响搜索召回正确性。
func HighlightSpans(original, normalizedQuery string) []Span {
	if original == "" || normalizedQuery == "" {
		return nil
	}
	runes := []rune(original)
	clusters := clusterRunes(runes)
	var builder strings.Builder
	provenance := make([]int, 0, len(runes))
	for clusterIndex, span := range clusters {
		normalizedCluster := Normalize(string(runes[span.start:span.end]))
		builder.WriteString(normalizedCluster)
		for range []rune(normalizedCluster) {
			provenance = append(provenance, clusterIndex)
		}
	}
	normalizedRunes := []rune(builder.String())
	queryRunes := []rune(normalizedQuery)
	if len(queryRunes) == 0 || len(queryRunes) > len(normalizedRunes) {
		return nil
	}
	var spans []Span
	for start := 0; start+len(queryRunes) <= len(normalizedRunes); start++ {
		if !runesEqual(normalizedRunes[start:start+len(queryRunes)], queryRunes) {
			continue
		}
		end := start + len(queryRunes) - 1
		firstCluster, lastCluster := provenance[start], provenance[end]
		spans = append(spans, Span{Start: clusters[firstCluster].start, End: clusters[lastCluster].end})
	}
	return mergeOverlappingSpans(spans)
}

type clusterSpan struct{ start, end int }

func clusterRunes(runes []rune) []clusterSpan {
	var clusters []clusterSpan
	index := 0
	for index < len(runes) {
		start := index
		index++
		for index < len(runes) && isClusterContinuation(runes[index]) {
			index++
		}
		clusters = append(clusters, clusterSpan{start: start, end: index})
	}
	return clusters
}

func isClusterContinuation(value rune) bool {
	if unicode.Is(unicode.Mn, value) || unicode.Is(unicode.Mc, value) || unicode.Is(unicode.Me, value) {
		return true
	}
	switch value {
	case 0x200D, 0xFE0F, 0xFE0E:
		return true
	}
	return value >= 0x1F3FB && value <= 0x1F3FF
}

func runesEqual(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

func mergeOverlappingSpans(spans []Span) []Span {
	if len(spans) < 2 {
		return spans
	}
	merged := []Span{spans[0]}
	for _, span := range spans[1:] {
		last := &merged[len(merged)-1]
		if span.Start < last.End {
			if span.End > last.End {
				last.End = span.End
			}
			continue
		}
		merged = append(merged, span)
	}
	return merged
}
