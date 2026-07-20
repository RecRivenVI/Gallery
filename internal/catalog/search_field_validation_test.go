package catalog_test

import (
	"context"
	"errors"
	"testing"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/querytext"
)

// TestStageRejectsTagContainingFieldSeparator 覆盖阶段 4 收尾：search_tags_norm/
// search_filenames_norm 用 querytext.FieldSeparator（U+001F）拼接多个取值。规则或
// metadata 产生的 Tag/文件名同样是输入来源（不只是用户手动输入），必须在 Stage 这个
// 唯一权威落库入口拒绝携带该字符的取值，否则会在存储层伪装成两个取值，破坏
// ranking/highlight 的取值边界不变量。
func TestStageRejectsTagContainingFieldSeparator(t *testing.T) {
	catalogStore, _ := newCandidateTestStore(t)
	ctx := context.Background()
	candidate, err := catalogStore.BeginCandidate(ctx, "job-tag-separator", "src-tag-separator", 1)
	if err != nil {
		t.Fatal(err)
	}
	works, mediaFacts := minimalCandidateFacts("src-tag-separator", "wrk_tag_separator", "med_tag_separator", candidateDigestA)
	works[0].Tags = []string{"foo" + querytext.FieldSeparator + "bar"}
	err = catalogStore.Stage(ctx, candidate, works, mediaFacts)
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeValidation {
		t.Fatalf("包含 FieldSeparator 的 Tag 应被拒绝为结构化 VALIDATION_ERROR: %v", err)
	}
}

// TestStageRejectsFilenameContainingFieldSeparator 与上一个测试对称，覆盖 Filenames。
func TestStageRejectsFilenameContainingFieldSeparator(t *testing.T) {
	catalogStore, _ := newCandidateTestStore(t)
	ctx := context.Background()
	candidate, err := catalogStore.BeginCandidate(ctx, "job-filename-separator", "src-filename-separator", 1)
	if err != nil {
		t.Fatal(err)
	}
	works, mediaFacts := minimalCandidateFacts("src-filename-separator", "wrk_filename_separator", "med_filename_separator", candidateDigestA)
	works[0].Filenames = []string{"a" + querytext.FieldSeparator + "b.jpg"}
	err = catalogStore.Stage(ctx, candidate, works, mediaFacts)
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeValidation {
		t.Fatalf("包含 FieldSeparator 的文件名应被拒绝为结构化 VALIDATION_ERROR: %v", err)
	}
}
