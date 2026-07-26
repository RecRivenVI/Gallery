package legacy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/rules"
	"github.com/RecRivenVI/gallery/internal/rules/legacy"
)

// updateGolden 重新生成 golden，而不是比较。它是显式开关而不是环境变量，使「更新 golden」这件事
// 必须出现在命令行里，不会被某个残留的环境变量在无人注意时打开。
var updateGolden = flag.Bool("update-golden", false, "重新生成转换结果 golden")

// syntheticConfig 是一份结构与真实 `gallery-rules.json` 同形的合成旧配置。
//
// 单元测试刻意使用合成配置而不是真实文件：真实文件在仓库之外，且其内容属于用户私有配置。
// 真实文件的转换由 TestConvertRealConfigurationWhenAvailable 在可用时另行验证。
//
// 三个平台分别覆盖三种真实存在的形态：alpha 是「metadata 齐全 + 路径回退」的常见形态；
// beta 覆盖 date_title、条件隐藏与带条件的封面候选；delta 覆盖「标题与作者都只能从路径取、
// 且取的是作者段」这一形态。全部字段都在 Config 中有声明，因此本配置**不应**产生任何
// 「未识别字段」登记——该性质由 TestConvertRegistersUndeclaredFields 反向锁定。
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
  "file_roots": [{"id": "files", "name": "所有文件", "path": "合成根/全部", "enabled": true, "order": 10},
                 {"id": "disabled", "name": "停用", "path": "合成根/停用", "enabled": false, "order": 20}],
  "sort": {"collation": "zh-CN", "work_default": "date_desc",
           "work_options": ["date_desc", "date_asc", "title_asc", "title_desc", "name_asc", "name_desc"],
           "author_default": "name_asc",
           "author_options": ["name_asc", "name_desc", "latest_desc", "latest_asc", "posts_desc", "posts_asc"],
           "browse_default": "natural_asc", "browse_options": ["natural_asc", "natural_desc"]},
  "platforms": [
    {"id": "alpha", "enabled": true, "path": "合成根/alpha", "order": 1, "scan_order": 1,
     "ui": {"name": "alpha", "description": "示例平台", "show_in_sidebar": true, "show_in_manager": true,
            "author_label": "画师",
            "icon": {"kind": "glyph", "glyph": "A", "background": "#1688f0", "color": "#ffffff", "border": "transparent"}},
     "structure": {"mode": "author_work", "author_pattern": "{author}", "work_pattern": "{author}/{work}",
                   "work_detection": "leaf_with_visible_media", "unknown_directory": "ignore",
                   "allow_media_in_work_children": false,
                   "author": {"key_source": "metadata_then_path", "path_capture": "author"},
                   "work": {"path_capture": "work", "metadata_required": true}},
     "metadata": {"categories": ["alpha"], "category_paths": ["category"],
                  "transforms": {"title": "display_text", "description": "display_text",
                                 "tags": "array_or_brace_list", "date": "iso_or_path_datetime"},
                  "title": ["title.text", "title", "$path.work"],
                  "author": ["user.name", "$path.author"], "author_id": ["user.id", "$path.author"],
                  "description": ["caption", "text"], "tags": ["tags"],
                  "date": ["date", "$path.datetime"],
                  "source_url": ["postUrl", "url"],
                  "time": {"input_timezone": "UTC", "output_timezone": "UTC", "display_timezone": "inherit",
                           "directory_timestamp_timezone": "Asia/Tokyo"}}},
    {"id": "beta", "enabled": true, "path": "合成根/beta", "order": 2, "scan_order": 2,
     "ui": {"name": "beta", "description": "另一个平台", "show_in_sidebar": true, "show_in_manager": true,
            "author_label": "用户",
            "icon": {"kind": "glyph", "glyph": "B", "background": "#050608", "color": "#ffffff", "border": "#333842"}},
     "structure": {"mode": "author_work", "author_pattern": "{author}", "work_pattern": "{author}/{work}",
                   "work_detection": "leaf_with_visible_media", "unknown_directory": "ignore",
                   "author": {"key_source": "metadata_then_path", "path_capture": "author"},
                   "work": {"path_capture": "work", "metadata_required": true}},
     "metadata": {"categories": ["beta"], "date_title": true, "title": ["date", "$path.work"],
                  "author": ["user.nick", "$path.author"], "author_id": ["user.id", "profile.id", "$path.author"],
                  "description": ["content"], "tags": ["tags"], "date": ["date", "$path.datetime"],
                  "source_url": ["url"],
                  "time": {"input_timezone": "UTC", "output_timezone": "UTC", "display_timezone": "inherit"}},
     "media": {"hide": [{"id": "beta-archive-preview", "name_regex": "^[1-9]\\.[^.]+$",
                         "when": {"files": {"extensions": ["zip", "rar"]}}},
                        {"id": "beta-linked-preview", "name_regex": "^[1-9]\\.[^.]+$",
                         "when": {"metadata_category": ["beta"],
                                  "metadata_any_text_paths": ["content", "links"],
                                  "text_regex": "https://example\\.invalid/(?:file|folder)/"}}]},
     "cover": {"candidates": [{"id": "beta-first", "priority": 200, "name_regex": "^1\\.[^.]+$",
                               "media_type": "static_image", "when": {"rule": "beta-archive-preview"}}]}},
    {"id": "delta", "enabled": true, "path": "合成根/delta", "order": 3, "scan_order": 3,
     "ui": {"name": "delta", "description": "路径取值平台", "show_in_sidebar": true, "show_in_manager": true,
            "author_label": "书名",
            "icon": {"kind": "glyph", "glyph": "D", "background": "#202020", "color": "#ffffff", "border": "#404040"}},
     "structure": {"mode": "author_work", "author_pattern": "{author}", "work_pattern": "{author}/{work}",
                   "work_detection": "leaf_with_visible_media", "unknown_directory": "ignore",
                   "author": {"key_source": "path_only", "path_capture": "author"},
                   "work": {"path_capture": "work", "metadata_required": false}},
     "metadata": {"categories": ["delta"], "title": ["$path.author"], "author": ["$path.author"],
                  "date": ["$path.datetime"],
                  "time": {"input_timezone": "UTC", "output_timezone": "UTC", "display_timezone": "inherit"}}},
    {"id": "gamma", "enabled": false, "path": "合成根/gamma", "order": 4, "scan_order": 4,
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
	result, err := legacy.Convert([]byte(syntheticConfig), ruleSetIDs("alpha", "beta", "delta"))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	if len(result.Packages) != 3 {
		t.Fatalf("启用平台数 = %d want 3（停用平台不得产出规则包）", len(result.Packages))
	}
	if _, ok := result.Packages["gamma"]; ok {
		t.Fatal("停用平台产出了规则包")
	}
	if len(result.FileRoots) != 1 || result.FileRoots[0].ID != "files" {
		t.Fatalf("文件根转换错误（停用的根不得进入结果）: %+v", result.FileRoots)
	}
	for _, platformID := range []string{"alpha", "beta", "delta"} {
		if result.SourceRoots[platformID] == "" {
			t.Fatalf("平台 %s 的来源根路径缺失", platformID)
		}
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
		// delta 声明 metadata 文件可选。扫描器对 path_match.metadata_file 是强制语义（缺文件即
		// 整次扫描失败），因此这类平台必须**不**下发文件名，否则第一个没有 metadata 的作品目录
		// 就会让整个 Source 扫描失败。
		wantMetadataFile := "metadata.json"
		if platformID == "delta" {
			wantMetadataFile = ""
		}
		if ir.MetadataFile != wantMetadataFile {
			t.Fatalf("平台 %s 的 metadata 文件名 = %q want %q", platformID, ir.MetadataFile, wantMetadataFile)
		}
		if len(ir.HiddenNameGlobs) != 3 {
			t.Fatalf("平台 %s 的隐藏 glob = %+v", platformID, ir.HiddenNameGlobs)
		}
		if ir.CoverDisableMarker != ".nocover" {
			t.Fatalf("平台 %s 的封面禁用标记 = %q", platformID, ir.CoverDisableMarker)
		}
		if ir.WorkDate == nil || ir.WorkDate.PathPattern != legacy.PathDatetimePattern {
			t.Fatalf("平台 %s 未带上路径日期模式: %+v", platformID, ir.WorkDate)
		}
		// delta 的日期链只有路径回退，其余平台先走 metadata。
		wantPointers := 1
		if platformID == "delta" {
			wantPointers = 0
		}
		if len(ir.WorkDate.Pointers) != wantPointers {
			t.Fatalf("平台 %s 的发布时间 pointer 链 = %+v want %d 条", platformID, ir.WorkDate.Pointers, wantPointers)
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

// TestConvertPrefersPlatformDirectoryTimezone 锁定目录名日期时区的三级回退：平台级声明必须压过
// 库级声明。旧实现只读库级取值，平台级声明会被 encoding/json 静默丢弃，产出的发布时间格式合法、
// 排序正常，只是整体偏移若干小时——没有任何信号能暴露它。
func TestConvertPrefersPlatformDirectoryTimezone(t *testing.T) {
	result, err := legacy.Convert([]byte(syntheticConfig), ruleSetIDs("alpha", "beta", "delta"))
	if err != nil {
		t.Fatal(err)
	}
	for platformID, want := range map[string]string{"alpha": "Asia/Tokyo", "beta": "UTC", "delta": "UTC"} {
		ir := compileIR(t, result.Packages[platformID])
		if ir.WorkDate == nil || ir.WorkDate.PathTimezone != want {
			t.Fatalf("平台 %s 的路径日期时区 = %+v want %q", platformID, ir.WorkDate, want)
		}
	}
}

// TestConvertMapsAuthorIdentifierToCreatorStableKey 锁定作者标识的落点。
//
// 旧实现把 `metadata.author_id` 映射到作品的 `external_id`。那是**作品**的跨路径身份：同一作者的
// 第二个作品会与第一个撞上同一个 external_id，扫描判定为 duplicate_external_id 并返回
// BINDING_REVIEW_REQUIRED，该 Source 的扫描整体失败。正确落点是 Creator 的稳定键。
func TestConvertMapsAuthorIdentifierToCreatorStableKey(t *testing.T) {
	result, err := legacy.Convert([]byte(syntheticConfig), ruleSetIDs("alpha", "beta", "delta"))
	if err != nil {
		t.Fatal(err)
	}
	ir := compileIR(t, result.Packages["alpha"])
	found := false
	for _, item := range ir.Primitives {
		if item.Kind == "fallback" || item.Kind == "selector" || item.Kind == "metadata_map" {
			if strings.Contains(string(item.Config), `"external_id"`) {
				t.Fatalf("作者标识仍被写入作品 external_id: %s", item.Config)
			}
		}
		if item.ID != "creator-id" {
			continue
		}
		found = true
		if item.Kind != "stable_key" {
			t.Fatalf("作者标识原语 kind = %q want stable_key", item.Kind)
		}
		if !strings.Contains(string(item.Config), `"target":"creator"`) {
			t.Fatalf("作者标识未落到 creator: %s", item.Config)
		}
	}
	if !found {
		t.Fatal("alpha 缺少作者标识原语")
	}
	// beta 的 author_id 是二级回退链，stable_key 只接受单 pointer，因此必须登记而不是猜一个。
	for _, item := range compileIR(t, result.Packages["beta"]).Primitives {
		if item.ID == "creator-id" {
			t.Fatalf("beta 的多级作者标识链不应被降级为单 pointer: %s", item.Config)
		}
	}
	if !hasNote(result.Unconverted, "beta", "metadata.author_id") {
		t.Fatalf("缺少 beta 多级作者标识链的未转换登记: %+v", result.Unconverted)
	}
}

// TestConvertKeepsDirectoryNameTitleAndEmptyTags 锁定去掉 `default: ""` 之后的求值行为。
//
// 旧实现给每条 metadata 取值链补了 `"default": ""`。求值端只在候选为 nil 时跳过赋值，拿到空串会
// 照样赋值，于是：标题被空串覆盖（作品目录名默认值失效，且 EnsureCanonical 对空标题直接返回校验
// 错误，整个 Source 扫描失败）、标签被写成一个空标签。两者都不是"留空"，而是写入了错误的值。
func TestConvertKeepsDirectoryNameTitleAndEmptyTags(t *testing.T) {
	result, err := legacy.Convert([]byte(syntheticConfig), ruleSetIDs("alpha", "beta", "delta"))
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := rules.NewLifecycle()
	if err != nil {
		t.Fatal(err)
	}
	const workPath = "creator-a/2024-03-05_06-07-08_12345678"
	evaluated, err := lifecycle.EvaluateIR(context.Background(), compileIR(t, result.Packages["alpha"]), nil,
		rules.DryRunInput{
			Path:     workPath,
			Files:    []rules.DryRunFile{{Path: "1.jpg"}, {Path: "cover.jpg"}},
			Metadata: map[string]any{"user": map[string]any{"name": "作者甲"}},
		})
	if err != nil {
		t.Fatalf("求值失败: %v", err)
	}
	if evaluated.Work.Title != "2024-03-05_06-07-08_12345678" {
		t.Fatalf("metadata 缺标题时的标题 = %q want 作品目录名", evaluated.Work.Title)
	}
	if len(evaluated.Work.Tags) != 0 {
		t.Fatalf("metadata 缺标签时的标签 = %+v want 空", evaluated.Work.Tags)
	}
	if evaluated.Work.Creator != "作者甲" {
		t.Fatalf("创作者 = %q", evaluated.Work.Creator)
	}
	if evaluated.Work.Description != "" || evaluated.Work.SourceURL != "" {
		t.Fatalf("缺失字段被写成了非空值: %+v", evaluated.Work)
	}
}

// TestConvertDropsConditionalCoverCandidate 锁定「条件无法表达时封面候选整条不发出」。
//
// 保留候选而省略条件不是温和的近似：这类候选的 priority 高于库级显式封面 glob，于是任何作品里
// 只要存在符合该 glob 的文件，它就会压过 `cover.*` 这类明确的封面声明，把旧行为里本不该被选中的
// 文件变成封面。封面选错比没有封面严重——没有封面时还有确定性的回退。
func TestConvertDropsConditionalCoverCandidate(t *testing.T) {
	result, err := legacy.Convert([]byte(syntheticConfig), ruleSetIDs("alpha", "beta", "delta"))
	if err != nil {
		t.Fatal(err)
	}
	ir := compileIR(t, result.Packages["beta"])
	for _, item := range ir.Primitives {
		if item.Kind == "cover_candidate" && strings.Contains(string(item.Config), `"1.*"`) {
			t.Fatalf("带条件的封面候选仍被发出: %s", item.Config)
		}
	}
	lifecycle, err := rules.NewLifecycle()
	if err != nil {
		t.Fatal(err)
	}
	evaluated, err := lifecycle.EvaluateIR(context.Background(), ir, nil, rules.DryRunInput{
		Path:     "creator-b/2024-03-05_06-07-08_12345678",
		Files:    []rules.DryRunFile{{Path: "1.jpg"}, {Path: "2.jpg"}, {Path: "cover.jpg"}},
		Metadata: map[string]any{},
	})
	if err != nil {
		t.Fatalf("求值失败: %v", err)
	}
	if evaluated.Work.CoverPath != "cover.jpg" {
		t.Fatalf("封面 = %q want cover.jpg（显式封面 glob 不得被无条件生效的候选压过）", evaluated.Work.CoverPath)
	}
	if !hasNote(result.Unconverted, "beta", "cover.candidates.beta-first.when") {
		t.Fatalf("缺少封面候选条件的未转换登记: %+v", result.Unconverted)
	}
}

func compileIR(t *testing.T, encoded json.RawMessage) rules.RuleIR {
	t.Helper()
	compiled, err := rules.CompilePackage(encoded)
	if err != nil {
		t.Fatalf("规则包无法编译: %v", err)
	}
	ir, _, _, err := rules.CompileBinding(compiled, []byte(`{}`))
	if err != nil {
		t.Fatalf("绑定无法编译: %v", err)
	}
	return ir
}

func hasNote(notes []legacy.Note, platform, field string) bool {
	for _, note := range notes {
		if note.Platform == platform && note.Field == field {
			return true
		}
	}
	return false
}

// TestConvertAppliesBadgesOnlyToDeclaredPlatforms 覆盖角标的平台过滤：旧配置用
// `when.platform` 限定角标适用范围，转换后必须体现为「只有该平台的规则包含有该角标」。
func TestConvertAppliesBadgesOnlyToDeclaredPlatforms(t *testing.T) {
	result, err := legacy.Convert([]byte(syntheticConfig), ruleSetIDs("alpha", "beta", "delta"))
	if err != nil {
		t.Fatal(err)
	}
	badgeIDs := func(platformID string) []string {
		var ids []string
		for _, badge := range compileIR(t, result.Packages[platformID]).Badges {
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
	result, err := legacy.Convert([]byte(syntheticConfig), ruleSetIDs("alpha", "beta", "delta"))
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
		// 两条条件隐藏必须**分别**登记：一条缺的是求值输入（兄弟文件清单），另一条只是尚未实现，
		// 合成一条会掩盖这个差别。
		"beta|media.hide.beta-archive-preview",
		"beta|media.hide.beta-linked-preview",
		"beta|cover.candidates.beta-first.when",
		"beta|metadata.date_title",
		// 作者段取值没有任何原语能承接，必须逐字段登记。
		"alpha|metadata.creator.$path.author",
		"alpha|metadata.creator-id.$path.author",
		"delta|metadata.creator.$path.author",
		// 「用作者段作标题」与「用作品目录名作标题」是两回事：后者已被 path_match 等价承接。
		"delta|metadata.title.$path.author",
		"delta|structure.author.key_source",
		"delta|structure.work.metadata_required",
		// 取值归一化改变的是取到的值本身，尤其 tags 的花括号列表形态会改变标签数量。
		"alpha|metadata.transforms.tags",
		"alpha|metadata.transforms.title",
		"alpha|metadata.category_paths",
	} {
		if !fields[wanted] {
			t.Fatalf("缺少未转换登记 %q；已登记项: %+v", wanted, result.Unconverted)
		}
	}
	// `$path.work` 作标题由 path_match 的 directory_name 默认值等价承接，不应再登记为未转换。
	for _, unwanted := range []string{"alpha|metadata.title.$path.work", "beta|metadata.title.$path.work"} {
		if fields[unwanted] {
			t.Fatalf("已被等价承接的语义仍被登记为未转换: %q", unwanted)
		}
	}
}

// TestConvertRegistersUndeclaredFields 锁定「未声明字段不得静默消失」。
//
// Config 是手写的部分映射，`encoding/json` 对未声明字段一律静默丢弃，没有返回值、错误或日志能
// 暴露它。因此「转换成功」本身不能证明旧配置的每个字段都被处理过。本用例在合成配置的多个嵌套
// 深度上各插入一个杜撰字段，断言每一个都出现在未转换登记里。
func TestConvertRegistersUndeclaredFields(t *testing.T) {
	clean, err := legacy.Convert([]byte(syntheticConfig), ruleSetIDs("alpha", "beta", "delta"))
	if err != nil {
		t.Fatal(err)
	}
	for _, note := range clean.Unconverted {
		if strings.HasPrefix(note.Field, "/") {
			t.Fatalf("合成配置的字段应全部已声明，却登记了未识别字段 %q", note.Field)
		}
	}

	var document map[string]any
	if err := json.Unmarshal([]byte(syntheticConfig), &document); err != nil {
		t.Fatal(err)
	}
	document["杜撰顶层字段"] = "值"
	platforms, _ := document["platforms"].([]any)
	first, _ := platforms[0].(map[string]any)
	first["杜撰平台字段"] = map[string]any{"内层": 1}
	structure, _ := first["structure"].(map[string]any)
	structure["杜撰结构字段"] = "值"
	metadata, _ := first["metadata"].(map[string]any)
	timeConfig, _ := metadata["time"].(map[string]any)
	timeConfig["杜撰时间字段"] = "Asia/Tokyo"
	badges, _ := document["badges"].([]any)
	badge, _ := badges[0].(map[string]any)
	badge["杜撰角标字段"] = true
	mutated, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}

	result, err := legacy.Convert(mutated, ruleSetIDs("alpha", "beta", "delta"))
	if err != nil {
		t.Fatalf("未识别字段不得让转换失败: %v", err)
	}
	if len(result.Packages) != 3 {
		t.Fatalf("未识别字段不得影响产出的规则包数: %d", len(result.Packages))
	}
	fields := map[string]string{}
	for _, note := range result.Unconverted {
		fields[note.Field] = note.Platform
	}
	for pointer, wantPlatform := range map[string]string{
		"/杜撰顶层字段":                           "",
		"/platforms/0/杜撰平台字段":               "alpha",
		"/platforms/0/structure/杜撰结构字段":     "alpha",
		"/platforms/0/metadata/time/杜撰时间字段": "alpha",
		"/badges/0/杜撰角标字段":                  "",
	} {
		platform, ok := fields[pointer]
		if !ok {
			t.Fatalf("未识别字段 %q 未被登记；已登记项: %+v", pointer, result.Unconverted)
		}
		if platform != wantPlatform {
			t.Fatalf("未识别字段 %q 的平台归属 = %q want %q", pointer, platform, wantPlatform)
		}
	}
	// 未声明的**子树**只登记它自己这一条，不逐个叶子展开。
	if _, ok := fields["/platforms/0/杜撰平台字段/内层"]; ok {
		t.Fatal("未声明子树被逐叶展开，登记变成噪声")
	}
}

// TestPathDatetimePatternMatchesObservedDirectoryConvention 锁定由真实来源有界观察得出的目录
// 命名约定，并锁定它**不**匹配的两类情况。
//
// 用例中的路径是按观察到的字符形状构造的合成样本，不含任何真实目录名。
func TestPathDatetimePatternMatchesObservedDirectoryConvention(t *testing.T) {
	expression := regexp.MustCompile(legacy.PathDatetimePattern)
	for _, item := range []struct {
		name, path string
		want       bool
		year       string
	}{
		{"纯数字标识", "creator/2024-03-05_06-07-08_12345678", true, "2024"},
		{"含字母与短横线的标识", "creator/2024-03-05_06-07-08_ab12c345-d1-2024-01", true, "2024"},
		{"长数字标识", "creator/2019-11-30_01-02-03_1234567890123456", true, "2019"},
		// Venera 的作品目录是纯数字章节号，没有日期：模式自然匹配不到，该平台的作品因此没有
		// 发布时间。这是真实情况，不为它伪造时间。
		{"Venera 式纯数字章节", "书名/12345", false, ""},
		// 作者段若恰好以日期时间开头，不得被误当作作品时间——前导斜杠正是为此存在。
		{"仅作者段带日期时间", "2024-03-05_06-07-08_x", false, ""},
	} {
		t.Run(item.name, func(t *testing.T) {
			match := expression.FindStringSubmatch(item.path)
			if (match != nil) != item.want {
				t.Fatalf("匹配结果 = %v want %v", match != nil, item.want)
			}
			if !item.want {
				return
			}
			for index, name := range expression.SubexpNames() {
				if name == "year" && match[index] != item.year {
					t.Fatalf("year = %q want %q", match[index], item.year)
				}
			}
		})
	}
}

// goldenSnapshot 是转换结果的完整可比较形态。四个字段就是 Result 的全部产出，没有任何一项被
// 摘要或省略——golden 的意义正在于「任何行为变化都必须显式更新它」，摘要会让变化漏过去。
type goldenSnapshot struct {
	Packages    map[string]json.RawMessage `json:"packages"`
	SourceRoots map[string]string          `json:"sourceRoots"`
	FileRoots   []goldenFileRoot           `json:"fileRoots"`
	Unconverted []goldenNote               `json:"unconverted"`
}

type goldenFileRoot struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	Order int    `json:"order"`
}

type goldenNote struct {
	Platform string `json:"platform"`
	Field    string `json:"field"`
	Reason   string `json:"reason"`
}

const goldenPath = "testdata/golden/synthetic-conversion.json"

// TestConvertMatchesGolden 把合成配置的完整转换结果与检入的 golden 逐字节比较。
//
// 目的不是「断言当前产出是对的」——对不对由其它用例逐条论证——而是让转换器的**任何**行为变化
// 都必须显式更新 golden。规则包会被 `package_hash`/`semantic_hash`/`rule_ir_hash` 固化为
// RuleVersion 身份，产出的一个字符差别就是另一个规则版本；悄悄改变产出等于悄悄改变已发布规则的
// 身份。golden 全部使用合成数据，不含任何真实配置内容。
//
// 需要更新时用 `-update-golden` 重新生成，并在提交里说明为什么该变化是期望的。
func TestConvertMatchesGolden(t *testing.T) {
	result, err := legacy.Convert([]byte(syntheticConfig), ruleSetIDs("alpha", "beta", "delta"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := goldenSnapshot{
		Packages: result.Packages, SourceRoots: result.SourceRoots,
		FileRoots: []goldenFileRoot{}, Unconverted: []goldenNote{},
	}
	for _, root := range result.FileRoots {
		snapshot.FileRoots = append(snapshot.FileRoots,
			goldenFileRoot{ID: root.ID, Name: root.Name, Path: root.Path, Order: root.Order})
	}
	for _, note := range result.Unconverted {
		snapshot.Unconverted = append(snapshot.Unconverted,
			goldenNote{Platform: note.Platform, Field: note.Field, Reason: note.Reason})
	}
	encoded, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("已更新 golden: %s", goldenPath)
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("读取 golden 失败（首次生成用 -update-golden）: %v", err)
	}
	if !bytes.Equal(want, encoded) {
		t.Fatalf("转换产出与 golden 不一致。若该变化是期望的，用 -update-golden 重新生成并在提交里说明理由。\n"+
			"golden 长度=%d 实际长度=%d", len(want), len(encoded))
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
