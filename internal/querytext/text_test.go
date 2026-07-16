package querytext_test

import (
	"testing"

	"github.com/RecRivenVI/gallery/internal/querytext"
)

func TestSearchNormalizationTokensAndNaturalSort(t *testing.T) {
	document := querytext.BuildDocument("作品１２", "Creator", []string{"青空"}, []string{"File10.JPG"})
	for _, query := range []string{"作品12", "青空", "creator", "ile10"} {
		plan := querytext.PlanSearch(query)
		if plan.NormalizedQuery == "" || plan.TooShort || (len([]rune(plan.NormalizedQuery)) >= 3 && plan.FTSQuery == "") {
			t.Fatalf("查询计划无效 %q: %+v", query, plan)
		}
		if document.NormalizedOriginal == "" {
			t.Fatal("文档原文为空")
		}
	}
	if !querytext.PlanSearch("画").TooShort {
		t.Fatal("单字符 CJK 未拒绝")
	}
	if !(querytext.NaturalSortKey("file2") < querytext.NaturalSortKey("file10")) {
		t.Fatal("自然数字排序错误")
	}
	if querytext.NaturalSortKey("ＦＩＬＥ2") != querytext.NaturalSortKey("file2") {
		t.Fatal("NFKC/case fold 排序未收敛")
	}
}
