package rules_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/RecRivenVI/gallery/internal/rules"
)

func badgeRulePackage(badgePrimitives string) string {
	return fmt.Sprintf(`{
  "rule_set_id":"rset_018f47d2-5c16-7a44-a8a0-0000000000d0","version":"1.0.0",
  "schema_version":1,"normalization_algorithm_version":"gallery-canonical-json-v1","compiler_requirement":"gallery-rule-compiler-v1","cel_profile_version":"gallery-cel-v1",
  "parameter_schema":{"type":"object","additionalProperties":false},"provider_namespaces":[],
  "primitives":[
    {"id":"work","kind":"path_match","config":{"scope":"work_directory","glob":"*","title":"directory_name","stable_key":"relative_path","metadata_file":"metadata.json"}},
    {"id":"tags","kind":"selector","config":{"target":"tags","pointers":["/tags"]}},
    {"id":"image","kind":"media_classify","config":{"glob":"*.jpg","kind":"image","mime":"image/jpeg"}},
    {"id":"video","kind":"media_classify","config":{"glob":"*.mp4","kind":"video","mime":"video/mp4"}}%s
  ],
  "cel_expressions":[],"tests":[{"id":"badge"}],"extensions":{}
}`, badgePrimitives)
}

func evaluateBadges(t *testing.T, badgePrimitives string, metadata map[string]any, filenames ...string) []rules.DryRunBadge {
	t.Helper()
	compiled, err := rules.CompilePackage([]byte(badgeRulePackage(badgePrimitives)))
	if err != nil {
		t.Fatalf("编译规则包失败: %v", err)
	}
	ir, _, parameters, err := rules.CompileBinding(compiled, []byte(`{}`))
	if err != nil {
		t.Fatalf("绑定编译失败: %v", err)
	}
	lifecycle, err := rules.NewLifecycle()
	if err != nil {
		t.Fatal(err)
	}
	sample := rules.DryRunInput{Path: "work", Metadata: metadata}
	for _, name := range filenames {
		sample.Files = append(sample.Files, rules.DryRunFile{Path: name, Size: 1})
	}
	result, err := lifecycle.EvaluateIR(context.Background(), ir, parameters, sample)
	if err != nil {
		t.Fatalf("规则执行失败: %v", err)
	}
	return result.Work.Badges
}

func badgeIDs(badges []rules.DryRunBadge) []string {
	result := make([]string, 0, len(badges))
	for _, item := range badges {
		result = append(result, item.ID)
	}
	return result
}

// TestBadgeTagConditionMatchesWorkTags 覆盖真实规则中的 R-18 角标：按作品标签命中。
func TestBadgeTagConditionMatchesWorkTags(t *testing.T) {
	const primitives = `,
    {"id":"r18","kind":"badge","config":{"badge_id":"r18","order":1,"position":"cover_top_left","label":"R-18",
     "style":{"color":"#ffffff","background":"#773333","border":"#9b4a4a"},
     "when":{"tags":["R-18"]}}}`
	matched := evaluateBadges(t, primitives, map[string]any{"tags": []any{"風景", "R-18"}}, "01.jpg")
	if len(matched) != 1 || matched[0].ID != "r18" || matched[0].Label != "R-18" ||
		matched[0].Position != "cover_top_left" || matched[0].Background != "#773333" {
		t.Fatalf("标签角标未按规则派生: %+v", matched)
	}
	missing := evaluateBadges(t, primitives, map[string]any{"tags": []any{"風景"}}, "01.jpg")
	if len(missing) != 0 {
		t.Fatalf("未命中标签仍产生角标: %+v", missing)
	}
}

// TestBadgeMetadataValueConditionComparesCanonically 覆盖 pixiv 的 AI 生成角标：按
// metadata 指定位置取值比较。比较在规范 JSON 层面进行，因此 2 与 2.0 视为同一个值。
func TestBadgeMetadataValueConditionComparesCanonically(t *testing.T) {
	const primitives = `,
    {"id":"ai","kind":"badge","config":{"badge_id":"ai","order":1,"position":"tag_leading","label":"AI生成",
     "style":{"color":"#f2e7e7","background":"#3a2020","color_light":"#7a1a20","background_light":"#fbeaea"},
     "when":{"metadata_pointer":"/illust_ai_type","metadata_values":[2]}}}`
	for _, item := range []struct {
		name  string
		value any
		want  int
	}{
		{"整数命中", 2, 1},
		{"浮点等值命中", 2.0, 1},
		{"其它取值不命中", 1, 0},
		{"缺失字段不命中", nil, 0},
	} {
		t.Run(item.name, func(t *testing.T) {
			metadata := map[string]any{"tags": []any{}}
			if item.value != nil {
				metadata["illust_ai_type"] = item.value
			}
			matched := evaluateBadges(t, primitives, metadata, "01.jpg")
			if len(matched) != item.want {
				t.Fatalf("角标数量 = %d want %d: %+v", len(matched), item.want, matched)
			}
			if item.want == 1 && matched[0].BackgroundLight != "#fbeaea" {
				t.Fatalf("浅色样式未随角标下发: %+v", matched[0])
			}
		})
	}
}

// TestBadgeMediaSuffixConditionUsesClassifiedMedia 覆盖图片/视频角标：只看已被规则分类
// 为媒体的文件，而不是目录里碰巧存在的任意文件。
func TestBadgeMediaSuffixConditionUsesClassifiedMedia(t *testing.T) {
	const primitives = `,
    {"id":"image","kind":"badge","config":{"badge_id":"image","order":1,"position":"cover_top_right","label":"图片",
     "style":{"color":"#f1f1f1","background":"#121316"},"when":{"media_suffix":["jpg","jpeg","png"]}}},
    {"id":"video","kind":"badge","config":{"badge_id":"video","order":2,"position":"cover_top_right","label":"视频",
     "style":{"color":"#d7fffb","background":"#1d4d49"},"when":{"media_suffix":["mp4","webm"]}}}`
	both := evaluateBadges(t, primitives, map[string]any{"tags": []any{}}, "01.jpg", "02.mp4")
	if got := badgeIDs(both); len(got) != 2 || got[0] != "image" || got[1] != "video" {
		t.Fatalf("图片与视频角标未按 order 排序输出: %+v", got)
	}
	// metadata.json 不是被分类的媒体，因此不应触发任何后缀角标。
	onlyMetadata := evaluateBadges(t, primitives, map[string]any{"tags": []any{}}, "metadata.json")
	if len(onlyMetadata) != 0 {
		t.Fatalf("非媒体文件触发了后缀角标: %+v", onlyMetadata)
	}
}

// TestBadgeOrderIsStable 锁定角标序列的确定性：角标进入 publication 快照，顺序不稳定会让
// 相同事实产生不同的 Catalog 内容。
func TestBadgeOrderIsStable(t *testing.T) {
	const primitives = `,
    {"id":"b","kind":"badge","config":{"badge_id":"bbb","order":1,"position":"cover_top_right","label":"B","style":{},"when":{"media_suffix":["jpg"]}}},
    {"id":"a","kind":"badge","config":{"badge_id":"aaa","order":1,"position":"cover_top_right","label":"A","style":{},"when":{"media_suffix":["jpg"]}}},
    {"id":"c","kind":"badge","config":{"badge_id":"ccc","order":0,"position":"cover_top_left","label":"C","style":{},"when":{"media_suffix":["jpg"]}}}`
	for attempt := 0; attempt < 3; attempt++ {
		got := badgeIDs(evaluateBadges(t, primitives, map[string]any{"tags": []any{}}, "01.jpg"))
		if len(got) != 3 || got[0] != "ccc" || got[1] != "aaa" || got[2] != "bbb" {
			t.Fatalf("角标顺序不稳定或未按 order 再按 id 排序: %+v", got)
		}
	}
}

// TestBadgeConditionsIntersect 证明同时声明多类条件时取交集，而不是任一命中即出现。
func TestBadgeConditionsIntersect(t *testing.T) {
	const primitives = `,
    {"id":"strict","kind":"badge","config":{"badge_id":"strict","order":1,"position":"cover_top_left","label":"严格",
     "style":{},"when":{"tags":["R-18"],"media_suffix":["mp4"]}}}`
	if matched := evaluateBadges(t, primitives, map[string]any{"tags": []any{"R-18"}}, "01.jpg"); len(matched) != 0 {
		t.Fatalf("只满足标签条件不应命中交集角标: %+v", matched)
	}
	if matched := evaluateBadges(t, primitives, map[string]any{"tags": []any{}}, "01.mp4"); len(matched) != 0 {
		t.Fatalf("只满足后缀条件不应命中交集角标: %+v", matched)
	}
	if matched := evaluateBadges(t, primitives, map[string]any{"tags": []any{"R-18"}}, "01.mp4"); len(matched) != 1 {
		t.Fatalf("同时满足两个条件应命中: %+v", matched)
	}
}

// TestBadgeRejectsInvalidConfiguration 锁定编译期校验：未知位置、缺条件、重复 badge_id
// 都必须在编译时被拒绝，而不是在扫描时静默产生无法渲染的角标。
func TestBadgeRejectsInvalidConfiguration(t *testing.T) {
	for _, item := range []struct{ name, primitives string }{
		{"未知位置", `,{"id":"x","kind":"badge","config":{"badge_id":"x","order":1,"position":"nowhere","label":"X","style":{},"when":{"tags":["a"]}}}`},
		{"缺条件", `,{"id":"x","kind":"badge","config":{"badge_id":"x","order":1,"position":"tag_leading","label":"X","style":{},"when":{}}}`},
		{"缺标签", `,{"id":"x","kind":"badge","config":{"badge_id":"x","order":1,"position":"tag_leading","label":"","style":{},"when":{"tags":["a"]}}}`},
		{"指针无候选值", `,{"id":"x","kind":"badge","config":{"badge_id":"x","order":1,"position":"tag_leading","label":"X","style":{},"when":{"metadata_pointer":"/a"}}}`},
		{"重复 badge_id", `,{"id":"x","kind":"badge","config":{"badge_id":"dup","order":1,"position":"tag_leading","label":"X","style":{},"when":{"tags":["a"]}}},
    {"id":"y","kind":"badge","config":{"badge_id":"dup","order":2,"position":"tag_leading","label":"Y","style":{},"when":{"tags":["b"]}}}`},
	} {
		t.Run(item.name, func(t *testing.T) {
			if _, err := rules.CompilePackage([]byte(badgeRulePackage(item.primitives))); err == nil {
				t.Fatal("非法角标配置未在编译期被拒绝")
			}
		})
	}
}
