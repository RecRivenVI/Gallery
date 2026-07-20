package query_test

import (
	"context"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	galleryquery "github.com/RecRivenVI/gallery/internal/query"
	"github.com/RecRivenVI/gallery/internal/storage"
)

// TestGoldenFieldLevelRankingAndHighlight 是阶段 4 Correctness 收尾第七节要求的黄金
// 语料集合：Creator 命中、Tag 多值命中、文件名中缀命中、同一 Work 多字段命中、CJK、
// 全角/半角、大小写折叠，以及"文件名高亮不泄露绝对路径"。标题命中、完全/前缀/中缀
// 优先级、CJK 单字段命中已由 TestRankingTierOrdersExactPrefixInfixFirst 与
// querytext.TestHighlightSpansGoldenCorpus 覆盖，这里聚焦 Creator/Tag/文件名字段级
// ranking 与通用 matches DTO 的端到端集成。
func TestGoldenFieldLevelRankingAndHighlight(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(t.TempDir())
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	seedPublication(t, store, "800", []seedWork{
		{title: "无标题作品", creator: "东京写真家", tags: []string{"风景", "东京"}, filenames: []string{"tokyo-street.bin"}},
		{title: "ＴＯＫＹＯ Diary", creator: "Unrelated Creator", tags: nil, filenames: nil},
		{title: "Sunset", creator: "unrelated", tags: []string{"Sunset Collection"}, filenames: []string{"IMG_sunset_0099.bin"}},
		{title: "Multi Field", creator: "Multi Field", tags: []string{"multi field tag"}, filenames: []string{"multi-field-name.bin"}},
	})
	service, err := galleryquery.NewService(ctx, store.Control.SQL(), store.Catalog.SQL(), clock.Fixed{Time: time.Now().UTC()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	scope := galleryquery.AuthorizationScope("owner", []string{"library.read"})

	// Creator 命中：CJK Creator 名，无标题命中。
	creatorHit, err := service.Search(ctx, galleryquery.Request{Search: "写真家", Limit: 20, AuthorizationScope: scope})
	if err != nil {
		t.Fatal(err)
	}
	if len(creatorHit.Items) != 1 || creatorHit.Items[0].Title != "无标题作品" {
		t.Fatalf("Creator CJK 命中失败: %+v", creatorHit.Items)
	}
	if !hasFieldValue(creatorHit.Items[0].Matches, "creator", "东京写真家") {
		t.Fatalf("Creator 命中未产生 field=creator 的 matches: %+v", creatorHit.Items[0].Matches)
	}

	// 全角/半角折叠：查询半角 "tokyo" 命中标题里的全角 "ＴＯＫＹＯ"。
	fullwidthHit, err := service.Search(ctx, galleryquery.Request{Search: "tokyo", Limit: 20, AuthorizationScope: scope})
	if err != nil {
		t.Fatal(err)
	}
	foundFullwidth := false
	for _, item := range fullwidthHit.Items {
		if item.Title == "ＴＯＫＹＯ Diary" {
			foundFullwidth = true
			if !hasFieldValue(item.Matches, "title", "ＴＯＫＹＯ Diary") {
				t.Fatalf("全角标题命中未产生 title matches: %+v", item.Matches)
			}
		}
	}
	if !foundFullwidth {
		t.Fatalf("半角查询未命中全角标题: %+v", fullwidthHit.Items)
	}

	// Tag 多值命中：查询命中具体某个 tag 取值，matches 携带该取值本身。
	tagHit, err := service.Search(ctx, galleryquery.Request{Search: "sunset collection", Limit: 20, AuthorizationScope: scope})
	if err != nil {
		t.Fatal(err)
	}
	if len(tagHit.Items) != 1 || tagHit.Items[0].Title != "Sunset" {
		t.Fatalf("Tag 多值命中失败: %+v", tagHit.Items)
	}
	if !hasFieldValue(tagHit.Items[0].Matches, "tag", "Sunset Collection") {
		t.Fatalf("Tag 命中未携带具体命中取值: %+v", tagHit.Items[0].Matches)
	}

	// 文件名中缀命中：查询命中文件名中段，且不泄露任何路径分隔符或绝对路径。
	filenameHit, err := service.Search(ctx, galleryquery.Request{Search: "sunset_0099", Limit: 20, AuthorizationScope: scope})
	if err != nil {
		t.Fatal(err)
	}
	if len(filenameHit.Items) != 1 || filenameHit.Items[0].Title != "Sunset" {
		t.Fatalf("文件名中缀命中失败: %+v", filenameHit.Items)
	}
	foundFilenameMatch := false
	for _, match := range filenameHit.Items[0].Matches {
		if match.Field != "filename" {
			continue
		}
		foundFilenameMatch = true
		if match.Value != "IMG_sunset_0099.bin" {
			t.Fatalf("文件名 matches value 应为 basename 本身: %q", match.Value)
		}
		if containsPathSeparator(match.Value) {
			t.Fatalf("文件名 matches 泄露路径分隔符: %q", match.Value)
		}
	}
	if !foundFilenameMatch {
		t.Fatalf("文件名命中未产生 field=filename 的 matches: %+v", filenameHit.Items[0].Matches)
	}

	// 同一 Work 多字段命中：标题、Creator、Tag、文件名都含 "multi field"/"multi-field"，
	// 应同时产生多条不同 field 的 matches。
	multiHit, err := service.Search(ctx, galleryquery.Request{Search: "multi field", Limit: 20, AuthorizationScope: scope})
	if err != nil {
		t.Fatal(err)
	}
	if len(multiHit.Items) != 1 || multiHit.Items[0].Title != "Multi Field" {
		t.Fatalf("多字段命中的 Work 未正确召回: %+v", multiHit.Items)
	}
	seenFields := map[string]bool{}
	for _, match := range multiHit.Items[0].Matches {
		seenFields[match.Field] = true
	}
	if !seenFields["title"] || !seenFields["creator"] || !seenFields["tag"] {
		t.Fatalf("同一 Work 多字段命中应同时出现 title/creator/tag 条目: %+v", multiHit.Items[0].Matches)
	}
}

func hasFieldValue(matches []galleryquery.FieldMatch, field, value string) bool {
	for _, match := range matches {
		if match.Field == field && match.Value == value {
			return true
		}
	}
	return false
}

func containsPathSeparator(value string) bool {
	for _, char := range value {
		if char == '/' || char == '\\' {
			return true
		}
	}
	return false
}
