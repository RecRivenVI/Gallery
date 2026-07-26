package rules_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/rules"
)

func presentationPackage(extraPrimitives, uiMetadata string) string {
	return fmt.Sprintf(`{
  "rule_set_id":"rset_018f47d2-5c16-7a44-a8a0-0000000000f0","version":"1.0.0",
  "schema_version":1,"normalization_algorithm_version":"gallery-canonical-json-v1","compiler_requirement":"gallery-rule-compiler-v1","cel_profile_version":"gallery-cel-v1",
  "parameter_schema":{"type":"object","additionalProperties":false},"provider_namespaces":[],
  "primitives":[
    {"id":"work","kind":"path_match","config":{"scope":"work_directory","glob":"*","title":"directory_name","stable_key":"relative_path"}},
    {"id":"media","kind":"media_classify","config":{"glob":"*.jpg","kind":"image","mime":"image/jpeg"}}%s
  ],
  "cel_expressions":[],"tests":[{"id":"presentation"}],"extensions":{}%s
}`, extraPrimitives, uiMetadata)
}

func compilePresentationIR(t *testing.T, extraPrimitives string) rules.RuleIR {
	t.Helper()
	compiled, err := rules.CompilePackage([]byte(presentationPackage(extraPrimitives, "")))
	if err != nil {
		t.Fatalf("编译规则包失败: %v", err)
	}
	ir, _, _, err := rules.CompileBinding(compiled, []byte(`{}`))
	if err != nil {
		t.Fatalf("绑定编译失败: %v", err)
	}
	return ir
}

// TestPresentationCompilesPlatformConfiguration 覆盖真实规则里每个平台的 `ui`、`sort` 与 `time`
// 三段配置。
func TestPresentationCompilesPlatformConfiguration(t *testing.T) {
	const primitives = `,
    {"id":"ui","kind":"presentation","config":{
      "name":"pixiv","description":"pixiv 插画与漫画","author_label":"画师",
      "show_in_sidebar":true,"show_in_manager":true,
      "icon":{"kind":"glyph","glyph":"P","background":"#1688f0","color":"#ffffff","border":"transparent"},
      "sort":{"collation":"zh-CN",
        "work_default":"date_desc","work_options":["date_desc","date_asc","title_asc","title_desc","name_asc","name_desc"],
        "author_default":"name_asc","author_options":["name_asc","name_desc","latest_desc","latest_asc","posts_desc","posts_asc"],
        "browse_default":"natural_asc","browse_options":["natural_asc","natural_desc"]},
      "time":{"display_timezone":"Asia/Shanghai","display_format":"YYYY-MM-DD HH:mm:ss"}}}`
	ir := compilePresentationIR(t, primitives)
	if ir.Presentation == nil {
		t.Fatal("平台呈现未进入执行计划")
	}
	got := ir.Presentation
	if got.Name != "pixiv" || got.AuthorLabel != "画师" || !got.ShowInSidebar || !got.ShowInManager {
		t.Fatalf("基础呈现字段错误: %+v", got)
	}
	if got.Icon == nil || got.Icon.Glyph != "P" || got.Icon.Background != "#1688f0" {
		t.Fatalf("图标错误: %+v", got.Icon)
	}
	if got.Sort == nil || got.Sort.Collation != "zh-CN" || got.Sort.WorkDefault != "date_desc" ||
		len(got.Sort.WorkOptions) != 6 || got.Sort.BrowseDefault != "natural_asc" {
		t.Fatalf("排序配置错误: %+v", got.Sort)
	}
	if got.Time == nil || got.Time.DisplayTimezone != "Asia/Shanghai" || got.Time.DisplayFormat != "YYYY-MM-DD HH:mm:ss" {
		t.Fatalf("时间显示配置错误: %+v", got.Time)
	}
}

// TestPresentationVisibilityDefaultsToVisible 锁定缺省语义：未声明可见性时视为可见，
// 使只想改名字的规则不必重复声明。
func TestPresentationVisibilityDefaultsToVisible(t *testing.T) {
	ir := compilePresentationIR(t, `,{"id":"ui","kind":"presentation","config":{"name":"X"}}`)
	if ir.Presentation == nil || !ir.Presentation.ShowInSidebar || !ir.Presentation.ShowInManager {
		t.Fatalf("缺省可见性错误: %+v", ir.Presentation)
	}
	hidden := compilePresentationIR(t, `,{"id":"ui","kind":"presentation","config":{"name":"X","show_in_sidebar":false}}`)
	if hidden.Presentation.ShowInSidebar || !hidden.Presentation.ShowInManager {
		t.Fatalf("显式隐藏未生效: %+v", hidden.Presentation)
	}
}

// TestPresentationChangesProduceNewRuleVersionUnlikeUIMetadata 是本切片最重要的回归：
// 平台呈现会改变客户端的实际行为，因此必须参与 semantic_hash 并产生新 RuleVersion；
// 而 `ui_metadata` 只描述编辑器表单，改动不应产生新 RuleVersion。两者不得混为一谈。
func TestPresentationChangesProduceNewRuleVersionUnlikeUIMetadata(t *testing.T) {
	const baseUI = `,{"id":"ui","kind":"presentation","config":{"name":"甲","author_label":"画师"}}`
	const changedUI = `,{"id":"ui","kind":"presentation","config":{"name":"乙","author_label":"画师"}}`

	base, err := rules.CompilePackage([]byte(presentationPackage(baseUI, "")))
	if err != nil {
		t.Fatal(err)
	}
	changed, err := rules.CompilePackage([]byte(presentationPackage(changedUI, "")))
	if err != nil {
		t.Fatal(err)
	}
	if base.SemanticHash == changed.SemanticHash {
		t.Fatal("平台呈现改动没有产生新的 semantic_hash，会让客户端行为静默变化且无法追溯")
	}

	// 对照：只改 ui_metadata 不得改变 semantic_hash。
	withMetadata, err := rules.CompilePackage([]byte(presentationPackage(baseUI,
		`,"ui_metadata":{"title":"表单标题","description":"仅供编辑器"}`)))
	if err != nil {
		t.Fatal(err)
	}
	if withMetadata.SemanticHash != base.SemanticHash {
		t.Fatal("ui_metadata 改动不应产生新的 semantic_hash")
	}
	if withMetadata.PackageHash == base.PackageHash {
		t.Fatal("ui_metadata 仍应进入 package_hash 以保证编辑器数据无损往返")
	}
}

// TestPresentationImpactRequiresReprojectionNotRescan 锁定影响分类：平台呈现只改变已有事实如何
// 展示，不改变任何 Source-derived 事实，因此重投影足够；判成重扫会让改个平台名字触发全量重扫。
func TestPresentationImpactRequiresReprojectionNotRescan(t *testing.T) {
	lifecycle, err := rules.NewLifecycle()
	if err != nil {
		t.Fatal(err)
	}
	before := []byte(presentationPackage(`,{"id":"ui","kind":"presentation","config":{"name":"甲"}}`, ""))
	after := []byte(presentationPackage(`,{"id":"ui","kind":"presentation","config":{"name":"乙"}}`, ""))
	result, err := lifecycle.Impact(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if result.Category != "REPROJECT" {
		t.Fatalf("平台呈现改动的影响类别 = %q want REPROJECT", result.Category)
	}
	if result.FullRescan {
		t.Fatal("平台呈现改动不应触发全量重扫")
	}
}

// TestPresentationRejectsInvalidConfiguration 锁定编译期校验：未知排序、默认值不在可选集合内、
// 非法时区与不受支持的图标类型都必须在规则发布时被拒绝，而不是让客户端显示一个无法生效的选项。
func TestPresentationRejectsInvalidConfiguration(t *testing.T) {
	for _, item := range []struct{ name, primitives, reason string }{
		{"未知作品排序", `,{"id":"ui","kind":"presentation","config":{"sort":{"work_options":["by_vibes"]}}}`, "未知排序"},
		{"默认值不在可选集合内", `,{"id":"ui","kind":"presentation","config":{"sort":{"work_default":"title_asc","work_options":["date_desc"]}}}`, "不在"},
		{"默认值不是已知排序", `,{"id":"ui","kind":"presentation","config":{"sort":{"work_default":"by_vibes"}}}`, "不是已知排序"},
		{"浏览排序用了作品排序名", `,{"id":"ui","kind":"presentation","config":{"sort":{"browse_options":["date_desc"]}}}`, "未知排序"},
		{"非法显示时区", `,{"id":"ui","kind":"presentation","config":{"time":{"display_timezone":"Mars/Olympus"}}}`, "IANA"},
		{"不受支持的图标类型", `,{"id":"ui","kind":"presentation","config":{"icon":{"kind":"bitmap","glyph":"P"}}}`, "只支持 glyph"},
		{"图标缺 glyph", `,{"id":"ui","kind":"presentation","config":{"icon":{"kind":"glyph","glyph":"  "}}}`, "缺少 glyph"},
		{"重复声明", `,{"id":"a","kind":"presentation","config":{"name":"甲"}},{"id":"b","kind":"presentation","config":{"name":"乙"}}`, "重复声明"},
	} {
		t.Run(item.name, func(t *testing.T) {
			_, err := rules.CompilePackage([]byte(presentationPackage(item.primitives, "")))
			if err == nil {
				t.Fatal("非法平台呈现配置未在编译期被拒绝")
			}
			if !strings.Contains(err.Error(), item.reason) {
				t.Fatalf("拒绝理由不明确（期望包含 %q）: %v", item.reason, err)
			}
		})
	}
}
