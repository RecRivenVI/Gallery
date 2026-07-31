package query

import (
	"testing"

	"github.com/RecRivenVI/gallery/internal/querytext"
)

func TestTitlePrefixSortRangeContainsEverySupportedContinuation(t *testing.T) {
	for _, query := range []string{"普通作品", "file1x", "作品 12a", "画🎨"} {
		plan := querytext.PlanSearch(query)
		lower, upper, ok := titlePrefixSortRange(plan)
		if !ok {
			t.Fatalf("%q 应可建立排序范围", query)
		}
		for _, title := range []string{query, query + "扩展", query + "9"} {
			key := querytext.NaturalSortKey(title)
			if key < lower || key >= upper {
				t.Fatalf("query=%q title=%q key=%q 不在 [%q,%q)", query, title, key, lower, upper)
			}
		}
	}

	for _, query := range []string{"", "file1", "123"} {
		if _, _, ok := titlePrefixSortRange(querytext.PlanSearch(query)); ok {
			t.Fatalf("数字结尾或空查询 %q 不应进入单区间快路径", query)
		}
	}
}
