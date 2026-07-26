package legacy_test

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/rules"
	"github.com/RecRivenVI/gallery/internal/rules/legacy"
)

// syntheticConfig 是一份结构与真实 `gallery-rules.json` 同形的合成旧配置。
//
// 单元测试刻意使用合成配置而不是真实文件：真实文件在仓库之外，且其内容属于用户私有配置。
// 真实文件的转换由 TestConvertRealConfigurationWhenAvailable 在可用时另行验证。
const syntheticConfig = `{
  "schema_version": 3,
  "library": {"id": "main", "metadata_file": "metadata.json", "path_case": "preserve"},
  "time": {"storage_timezone": "UTC", "display_timezone": "Asia/Shanghai",
           "display_format": "YYYY-MM-DD HH:mm:ss", "naive_timestamp_timezone": "UTC",
           "directory_timestamp_timezone": "UTC"},
  "media": {"image_extensions": ["jpg", "png", "gif"], "video_extensions": ["mp4"],
            "hidden_name_globs": [".*", "cover.*", ".cover.*"]},
  "cover": {"disable_marker": ".nocover", "explicit_globs": ["cover.*", ".cover.*"],
            "leaf_fallback": "first_natural_media",
            "aggregate": {"author": "latest_dated_work", "platform": "latest_dated_author",
                          "library": "latest_dated_platform"}},
  "file_roots": [{"id": "files", "name": "所有文件", "path": "F:\\Gallery", "enabled": true, "order": 10},
                 {"id": "disabled", "name": "停用", "path": "F:\\Other", "enabled": false, "order": 20}],
  "sort": {"collation": "zh-CN", "work_default": "date_desc",
           "work_options": ["date_desc", "date_asc", "title_asc", "title_desc", "name_asc", "name_desc"],
           "author_default": "name_asc",
           "author_options": ["name_asc", "name_desc", "latest_desc", "latest_asc", "posts_desc", "posts_asc"],
           "browse_default": "natural_asc", "browse_options": ["natural_asc", "natural_desc"]},
  "platforms": [
    {"id": "alpha", "enabled": true, "path": "F:\\Gallery\\alpha", "order": 1, "scan_order": 1,
     "ui": {"name": "alpha", "description": "示例平台", "show_in_sidebar": true, "show_in_manager": true,
            "author_label": "画师",
            "icon": {"kind": "glyph", "glyph": "A", "background": "#1688f0", "color": "#ffffff", "border": "transparent"}},
     "structure": {"mode": "author_work", "author_pattern": "{author}", "work_pattern": "{author}/{work}",
                   "work_detection": "leaf_with_visible_media"},
     "metadata": {"categories": ["alpha"], "title": ["title.text", "title", "$path.work"],
                  "author": ["user.name", "$path.author"], "author_id": ["user.id", "$path.author"],
                  "description": ["caption", "text"], "tags": ["tags"],
                  "date": ["date", "$path.datetime"],
                  "source_url": ["postUrl", "url"],
                  "time": {"input_timezone": "UTC", "output_timezone": "UTC", "display_timezone": "inherit"}}},
    {"id": "beta", "enabled": true, "path": "F:\\Gallery\\beta", "order": 2, "scan_order": 2,
     "ui": {"name": "beta", "description": "另一个平台", "show_in_sidebar": true, "show_in_manager": true,
            "author_label": "用户",
            "icon": {"kind": "glyph", "glyph": "B", "background": "#050608", "color": "#ffffff", "border": "#333842"}},
     "structure": {"mode": "author_work", "author_pattern": "{author}", "work_pattern": "{author}/{work}",
                   "work_detection": "leaf_with_visible_media"},
     "metadata": {"categories": ["beta"], "date_title": true, "title": ["date", "$path.work"],
                  "author": ["user.nick", "$path.author"], "author_id": ["user.id", "$path.author"],
                  "description": ["content"], "tags": ["tags"], "date": ["date", "$path.datetime"],
                  "source_url": ["url"],
                  "time": {"input_timezone": "UTC", "output_timezone": "UTC", "display_timezone": "inherit"}},
     "media": {"hide": [{"id": "beta-preview", "name_regex": "^[1-9]\\.[^.]+$",
                         "when": {"files": {"extensions": ["zip", "rar"]}}}]},
     "cover": {"candidates": [{"id": "beta-first", "priority": 200, "name_regex": "^1\\.[^.]+$",
                               "media_type": "static_image", "when": {"rule": "beta-preview"}}]}},
    {"id": "gamma", "enabled": false, "path": "F:\\Gallery\\gamma", "order": 3, "scan_order": 3,
     "ui": {"name": "gamma"}, "structure": {"mode": "author_work", "work_detection": "leaf_with_visible_media"},
     "metadata": {}}
  ],
  "badges": [
    {"id": "r18", "enabled": true, "order": 1, "position": "cover_top_left", "label": "R-18",
     "color": "#ffffff", "background": "#773333", "border": "#9b4a4a",
     "when": {"platform": ["alpha"], "metadata": {"tags": ["R-18"]}}},
    {"id": "ai", "enabled": true, "order": 1, "position": "tag_leading", "label": "AI生成",
     "color": "#f2e7e7", "background": "#3a2020", "color_light": "#7a1a20", "background_light": "#fbeaea",
     "when": {"platform": ["alpha"], "metadata": {"illust_ai_type": [2]}}},
    {"id": "image", "enabled": true, "order": 1, "position": "cover_top_right", "label": "图片",
     "color": "#f1f1f1", "background": "#121316",
     "when": {"suffix": ["jpg", "png", "JPG", "PNG"]}},
    {"id": "video", "enabled": true, "order": 2, "position": "cover_top_right", "label": "视频",
     "color": "#d7fffb", "background": "#1d4d49",
     "when": {"suffix": ["mp4", "MP4"]}}
  ]
}`

func ruleSetIDs(platformIDs ...string) map[string]string {
	result := map[string]string{}
	for index, id := range platformIDs {
		result[id] = fmt.Sprintf("rset_018f47d2-5c16-7a44-a8a0-%012d", index+1)
	}
	return result
}

// TestConvertProducesCompilablePackagesPerEnabledPlatform 是转换器的核心断言：每个启用平台
// 产出一份**能被生产编译器接受**的规则包。转换器不自己实现一套校验——那会与编译器漂移；
// 「能编译」就是转换正确性的判据。
func TestConvertProducesCompilablePackagesPerEnabledPlatform(t *testing.T) {
	result, err := legacy.Convert([]byte(syntheticConfig), ruleSetIDs("alpha", "beta"))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	if len(result.Packages) != 2 {
		t.Fatalf("启用平台数 = %d want 2（停用平台不得产出规则包）", len(result.Packages))
	}
	if _, ok := result.Packages["gamma"]; ok {
		t.Fatal("停用平台产出了规则包")
	}
	if len(result.FileRoots) != 1 || result.FileRoots[0].ID != "files" {
		t.Fatalf("文件根转换错误（停用的根不得进入结果）: %+v", result.FileRoots)
	}
	if result.SourceRoots["alpha"] == "" || result.SourceRoots["beta"] == "" {
		t.Fatalf("来源根路径缺失: %+v", result.SourceRoots)
	}

	for platformID, encoded := range result.Packages {
		compiled, err := rules.CompilePackage(encoded)
		if err != nil {
			t.Fatalf("平台 %s 的规则包无法编译: %v", platformID, err)
		}
		ir, _, _, err := rules.CompileBinding(compiled, []byte(`{}`))
		if err != nil {
			t.Fatalf("平台 %s 的绑定无法编译: %v", platformID, err)
		}
		if ir.WorkDirectoryGlob != "*/*" {
			t.Fatalf("平台 %s 的作品目录 glob = %q", platformID, ir.WorkDirectoryGlob)
		}
		if ir.MetadataFile != "metadata.json" {
			t.Fatalf("平台 %s 的 metadata 文件名 = %q", platformID, ir.MetadataFile)
		}
		if len(ir.HiddenNameGlobs) != 3 {
			t.Fatalf("平台 %s 的隐藏 glob = %+v", platformID, ir.HiddenNameGlobs)
		}
		if ir.CoverDisableMarker != ".nocover" {
			t.Fatalf("平台 %s 的封面禁用标记 = %q", platformID, ir.CoverDisableMarker)
		}
		if ir.WorkDate == nil || len(ir.WorkDate.Pointers) != 1 || ir.WorkDate.Pointers[0] != "/date" {
			t.Fatalf("平台 %s 的发布时间计划 = %+v", platformID, ir.WorkDate)
		}
		if ir.Presentation == nil || ir.Presentation.Sort == nil ||
			ir.Presentation.Sort.WorkDefault != "date_desc" || ir.Presentation.Sort.Collation != "zh-CN" {
			t.Fatalf("平台 %s 的呈现配置 = %+v", platformID, ir.Presentation)
		}
		if ir.Presentation.Time == nil || ir.Presentation.Time.DisplayTimezone != "Asia/Shanghai" {
			t.Fatalf("平台 %s 的显示时区未从 inherit 解析为库级取值: %+v", platformID, ir.Presentation.Time)
		}
	}
}

// TestConvertAppliesBadgesOnlyToDeclaredPlatforms 覆盖角标的平台过滤：旧配置用
// `when.platform` 限定角标适用范围，转换后必须体现为「只有该平台的规则包含有该角标」。
func TestConvertAppliesBadgesOnlyToDeclaredPlatforms(t *testing.T) {
	result, err := legacy.Convert([]byte(syntheticConfig), ruleSetIDs("alpha", "beta"))
	if err != nil {
		t.Fatal(err)
	}
	badgeIDs := func(platformID string) []string {
		compiled, err := rules.CompilePackage(result.Packages[platformID])
		if err != nil {
			t.Fatal(err)
		}
		ir, _, _, err := rules.CompileBinding(compiled, []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, badge := range ir.Badges {
			ids = append(ids, badge.BadgeID)
		}
		sort.Strings(ids)
		return ids
	}
	alpha := badgeIDs("alpha")
	if strings.Join(alpha, ",") != "ai,image,r18,video" {
		t.Fatalf("alpha 角标 = %+v，应含限定平台的 r18/ai 与全平台的 image/video", alpha)
	}
	beta := badgeIDs("beta")
	if strings.Join(beta, ",") != "image,video" {
		t.Fatalf("beta 角标 = %+v，不应含限定给 alpha 的角标", beta)
	}
}

// TestConvertRegistersUnconvertedSemantics 锁定「未转换项必须被显式登记」这条性质。
//
// 静默丢弃无法表达的旧语义会让「导入成功」变成一句无法核实的断言。真实配置里确实存在当前原语
// 无法表达的东西（Gank 的条件隐藏、封面候选的跨规则引用、`$path.datetime` 的目录命名约定），
// 它们必须出现在登记里，而不是消失。
func TestConvertRegistersUnconvertedSemantics(t *testing.T) {
	result, err := legacy.Convert([]byte(syntheticConfig), ruleSetIDs("alpha", "beta"))
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]bool{}
	for _, note := range result.Unconverted {
		fields[note.Platform+"|"+note.Field] = true
		if note.Reason == "" {
			t.Fatalf("未转换登记缺少原因: %+v", note)
		}
	}
	for _, wanted := range []string{
		"alpha|metadata.date.$path.datetime",
		"beta|media.hide",
		"beta|cover.candidates.beta-first.when",
		"beta|metadata.date_title",
		"alpha|metadata.title.$path.work",
	} {
		if !fields[wanted] {
			t.Fatalf("缺少未转换登记 %q；已登记项: %+v", wanted, result.Unconverted)
		}
	}
}

// TestConvertRejectsUnsupportedSchemaVersion 证明转换器只接受它真正理解的版本。
func TestConvertRejectsUnsupportedSchemaVersion(t *testing.T) {
	for _, body := range []string{`{"schema_version": 2}`, `{"schema_version": 4}`, `{}`} {
		if _, err := legacy.Convert([]byte(body), nil); err == nil {
			t.Fatalf("未拒绝不支持的 schema_version: %s", body)
		}
	}
}

// TestConvertRealConfigurationWhenAvailable 在真实配置可读时转换它，并断言每个平台的规则包都能
// 被生产编译器接受。它**不输出任何路径、metadata 原文或平台内容**，只输出计数与未转换字段名。
//
// 真实文件不在仓库内，因此不可用时跳过而不是失败——CI 上必然跳过，本地开发机上提供真实覆盖。
func TestConvertRealConfigurationWhenAvailable(t *testing.T) {
	const path = `D:\GitHubRecRivenVI\PowerShell-Tools\Gallery\data\config\gallery-rules.json`
	body, err := os.ReadFile(path)
	if err != nil {
		t.Skip("真实旧配置不可读，跳过真实转换覆盖")
	}
	var probe struct {
		Platforms []struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		} `json:"platforms"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		t.Fatalf("真实配置不是合法 JSON: %v", err)
	}
	identifiers := map[string]string{}
	enabled := 0
	for index, platform := range probe.Platforms {
		if !platform.Enabled {
			continue
		}
		enabled++
		identifiers[platform.ID] = fmt.Sprintf("rset_018f47d2-5c16-7a44-a8a0-%012d", index+1)
	}
	result, err := legacy.Convert(body, identifiers)
	if err != nil {
		t.Fatalf("真实配置转换失败: %v", err)
	}
	if len(result.Packages) != enabled {
		t.Fatalf("产出规则包数 = %d，启用平台数 = %d", len(result.Packages), enabled)
	}
	for platformID, encoded := range result.Packages {
		compiled, err := rules.CompilePackage(encoded)
		if err != nil {
			t.Fatalf("真实平台 %s 的规则包无法编译: %v", platformID, err)
		}
		if _, _, _, err := rules.CompileBinding(compiled, []byte(`{}`)); err != nil {
			t.Fatalf("真实平台 %s 的绑定无法编译: %v", platformID, err)
		}
	}
	// 只输出字段名与计数，绝不输出路径或 metadata 内容。
	unconverted := map[string]int{}
	for _, note := range result.Unconverted {
		unconverted[note.Field]++
	}
	names := make([]string, 0, len(unconverted))
	for field := range unconverted {
		names = append(names, field)
	}
	sort.Strings(names)
	t.Logf("真实配置转换: %d 个启用平台、%d 个文件根、%d 条未转换登记",
		len(result.Packages), len(result.FileRoots), len(result.Unconverted))
	for _, field := range names {
		t.Logf("  未转换 %s ×%d", field, unconverted[field])
	}
}
