package querytext_test

import (
	"testing"
	"time"

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

// TestNormalizeInvalidUTF8DoesNotHang 回归 GO-2026-5970：golang.org/x/text
// v0.39.0 之前 norm.Form.String 处理非法 UTF-8 字节时可能进入无限循环。用带超时
// 的 goroutine 隔离调用，使该测试本身在回归重现时会失败退出而不是无限期挂起。
func TestNormalizeInvalidUTF8DoesNotHang(t *testing.T) {
	invalidInputs := []string{
		"\xff\xfe",
		"\x80\x80\x80",
		"\xc0\xaf",
		"\xe2\x82",
		"\xf0\x28\x8c\x28",
		"作品\xed\xa0\x80",
		"\xc2",
	}
	for _, input := range invalidInputs {
		done := make(chan struct{})
		go func() {
			querytext.Normalize(input)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("Normalize(%q) 未在超时内返回", input)
		}
	}
}
