package legacyrules_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/rules"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/legacyrules"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/ruleindex"
)

// syntheticConfig 与真实 `gallery-rules.json` 同形，但全部取值由本测试构造。真实配置属于
// 用户私有内容，不进入仓库，也不由单元测试读取。
const syntheticConfig = `{
  "schema_version": 3,
  "library": {"id": "main", "metadata_file": "metadata.json", "path_case": "preserve"},
  "time": {"storage_timezone": "UTC", "display_timezone": "Asia/Shanghai",
           "display_format": "YYYY-MM-DD HH:mm:ss", "naive_timestamp_timezone": "UTC",
           "directory_timestamp_timezone": "UTC"},
  "media": {"image_extensions": ["jpg", "png"], "video_extensions": ["mp4"],
            "hidden_name_globs": [".*"]},
  "cover": {"disable_marker": ".nocover", "explicit_globs": ["cover.*"],
            "leaf_fallback": "first_natural_media",
            "aggregate": {"author": "latest_dated_work", "platform": "latest_dated_author",
                          "library": "latest_dated_platform"}},
  "file_roots": [{"id": "files", "name": "全部", "path": "Z:\\synthetic", "enabled": true, "order": 10},
                 {"id": "off", "name": "停用", "path": "Z:\\other", "enabled": false, "order": 20}],
  "sort": {"collation": "zh-CN", "work_default": "date_desc",
           "work_options": ["date_desc", "date_asc"], "author_default": "name_asc",
           "author_options": ["name_asc"], "browse_default": "natural_asc",
           "browse_options": ["natural_asc"]},
  "platforms": [
    {"id": "alpha", "enabled": true, "path": "Z:\\synthetic\\alpha", "order": 1, "scan_order": 1,
     "ui": {"name": "A", "description": "d", "show_in_sidebar": true, "show_in_manager": true,
            "author_label": "作者",
            "icon": {"kind": "glyph", "glyph": "A", "background": "#1688f0", "color": "#ffffff", "border": "transparent"}},
     "structure": {"mode": "author_work", "author_pattern": "{author}", "work_pattern": "{author}/{work}",
                   "work_detection": "leaf_with_visible_media"},
     "metadata": {"title": ["title"], "author": ["user.name", "$path.author"],
                  "author_id": ["user.id"], "description": ["caption"], "tags": ["tags"],
                  "date": ["date", "$path.datetime"], "source_url": ["postUrl"],
                  "time": {"input_timezone": "UTC", "output_timezone": "UTC", "display_timezone": "inherit"}},
     "media": {"hide": [{"id": "preview", "name_regex": "^[1-9]\\.[^.]+$",
                         "when": {"files": {"extensions": ["zip"]}}}]}},
    {"id": "beta", "enabled": false, "path": "Z:\\synthetic\\beta", "order": 2, "scan_order": 2,
     "ui": {"name": "B"}, "structure": {"mode": "author_work", "work_detection": "leaf_with_visible_media"},
     "metadata": {}}
  ],
  "badges": []
}`

// TestConvertProducesCompilablePackagesForEnabledPlatformsOnly 是本包的核心断言：转换产物
// 必须能被**生产编译器**接受。本包不自己实现一套校验——那会与编译器漂移；「能编译」就是
// 判据。停用平台不得产出规则包。
func TestConvertProducesCompilablePackagesForEnabledPlatformsOnly(t *testing.T) {
	bundle, err := legacyrules.Convert([]byte(syntheticConfig))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	if len(bundle.Index.Entries) != 1 {
		t.Fatalf("产出平台数 = %d want 1（停用平台不得产出）", len(bundle.Index.Entries))
	}
	if bundle.Index.LegacySchemaVersion != 3 {
		t.Fatalf("legacySchemaVersion = %d", bundle.Index.LegacySchemaVersion)
	}
	if bundle.Index.FileRootCount != 1 {
		t.Fatalf("fileRootCount = %d want 1（停用的文件根不得计入）", bundle.Index.FileRootCount)
	}
	entry := bundle.Index.Entries[0]
	if entry.PlatformCode != ruleindex.Code("alpha") {
		t.Fatalf("平台代号 = %q", entry.PlatformCode)
	}
	if entry.RuleSetID != ruleindex.RuleSetID("alpha") {
		t.Fatalf("rule_set_id 未按平台确定性派生: %q", entry.RuleSetID)
	}
	if entry.PrimitiveCount == 0 {
		t.Fatal("primitiveCount 为 0")
	}
	compiled, err := rules.CompilePackage(bundle.Packages[entry.PlatformCode])
	if err != nil {
		t.Fatalf("转换产物无法编译: %v", err)
	}
	ir, _, _, err := rules.CompileBinding(compiled, []byte(`{}`))
	if err != nil {
		t.Fatalf("转换产物的绑定无法编译: %v", err)
	}
	if ir.WorkDirectoryGlob != "*/*" {
		t.Fatalf("作品目录 glob = %q；author_work 结构必须是两级", ir.WorkDirectoryGlob)
	}
	if ir.MetadataFile != "metadata.json" {
		t.Fatalf("metadata 文件名 = %q", ir.MetadataFile)
	}
}

// TestConvertRejectsUnsupportedInput 保证转换器只接受它真正理解的输入。
func TestConvertRejectsUnsupportedInput(t *testing.T) {
	for name, body := range map[string]string{
		"schema_version 2": `{"schema_version":2,"platforms":[{"id":"a","enabled":true}]}`,
		"没有启用平台":           `{"schema_version":3,"platforms":[{"id":"a","enabled":false}]}`,
		"非法 JSON":          `{`,
	} {
		if _, err := legacyrules.Convert([]byte(body)); err == nil {
			t.Fatalf("%s：必须被拒绝", name)
		}
	}
	if _, err := legacyrules.ConvertFile(""); err == nil {
		t.Fatal("空路径必须被拒绝，不得猜测或扫描磁盘")
	}
}

// TestUnconvertedRegistryBucketsByStructuralField 锁定未转换登记的两条性质：
// 存在（静默丢弃无法表达的旧语义会让「导入成功」变成无法核实的断言），以及
// 只按旧配置 schema 的结构字段名聚合，不把用户自定义 ID 带进可能被提交的产物。
func TestUnconvertedRegistryBucketsByStructuralField(t *testing.T) {
	bundle, err := legacyrules.Convert([]byte(syntheticConfig))
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Index.UnconvertedByField["media.hide"] == 0 {
		t.Fatalf("缺少 media.hide 的未转换登记: %+v", bundle.Index.UnconvertedByField)
	}
	for field := range bundle.Index.UnconvertedByField {
		if strings.Count(field, ".") > 1 {
			t.Fatalf("未转换字段名 %q 超过两段，可能带出用户自定义 ID", field)
		}
	}
}

// TestConvertNeverMapsAuthorIDToWorkExternalID 是一条针对真实 Source 的回归保护：转换产物
// 不得把旧配置的 `metadata.author_id` 喂给**作品**的 `external_id`。
//
// 后果是硬性的：同一作者的多个作品共享同一个 author_id，一旦它成为作品 external_id，扫描解析
// 阶段就会命中 `duplicate_external_id`，以 `BINDING_REVIEW_REQUIRED` 阻塞该 Source 的
// publication——任何「一个作者有多个作品」的真实平台都会在第一次扫描就卡住。这条断言与
// stages/sourcelab 的端到端用例（同一作者多个作品）互为里外两层。
func TestConvertNeverMapsAuthorIDToWorkExternalID(t *testing.T) {
	bundle, err := legacyrules.Convert([]byte(syntheticConfig))
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Primitives []struct {
			ID     string          `json:"id"`
			Kind   string          `json:"kind"`
			Config json.RawMessage `json:"config"`
		} `json:"primitives"`
	}
	if err := json.Unmarshal(bundle.Packages[ruleindex.Code("alpha")], &parsed); err != nil {
		t.Fatal(err)
	}
	for _, primitive := range parsed.Primitives {
		var config struct {
			Target   string   `json:"target"`
			Pointers []string `json:"pointers"`
		}
		if json.Unmarshal(primitive.Config, &config) != nil || config.Target != "external_id" {
			continue
		}
		if fmt.Sprint(config.Pointers) == "[/user/id]" {
			t.Fatalf("原语 %q 把旧配置的 author_id（%v）映射成了作品 external_id；"+
				"同一作者的多个作品会因此共享 external_id，扫描将以 BINDING_REVIEW_REQUIRED 阻塞",
				primitive.ID, config.Pointers)
		}
	}
}
