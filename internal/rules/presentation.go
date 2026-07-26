package rules

import (
	"fmt"
	"strings"
	"time"
)

// PresentationRegistryVersion 标识平台呈现语义的版本，随 IR 一起进入 rule_ir_hash。
const PresentationRegistryVersion = "gallery-presentation-v1"

// Presentation 是规则为其绑定的 Source（真实规则里的「平台」）声明的呈现配置。
//
// **为什么不放进 `ui_metadata`。** 那一段被 semantic_hash 显式排除（见 CompilePackage），影响分析也
// 把它固定判为 `NO_ACTION`。它的定位是「仅供 Schema 表单和编辑器使用的非执行元数据」——改了不会产生
// 新 RuleVersion，也不会触发任何重投影。而这里的配置**会改变客户端的实际行为**：侧栏是否出现该平台、
// 作者用什么称谓、默认按什么排序、时间按哪个时区显示。把它塞进 `ui_metadata` 会让这些变化静默生效
// 于部分客户端而不生效于另一些，且无法追溯是哪个 RuleVersion 改的。
//
// 因此它是独立的 semantic 段：参与 semantic_hash，有自己的影响分类（改动需要重投影而非重扫——它不
// 改变任何 Source-derived 事实，只改变这些事实如何被呈现）。
type Presentation struct {
	Name          string            `json:"name,omitempty"`
	Description   string            `json:"description,omitempty"`
	AuthorLabel   string            `json:"authorLabel,omitempty"`
	ShowInSidebar bool              `json:"showInSidebar"`
	ShowInManager bool              `json:"showInManager"`
	Icon          *PresentationIcon `json:"icon,omitempty"`
	Sort          *PresentationSort `json:"sort,omitempty"`
	Time          *PresentationTime `json:"time,omitempty"`
}

// PresentationIcon 是平台图标。当前只支持字形图标：一个短字形加三种颜色，客户端按自己的设计系统
// 渲染。不支持位图或外链，避免规则包引入外部资源加载与其带来的隐私与安全面。
type PresentationIcon struct {
	Kind       string `json:"kind"`
	Glyph      string `json:"glyph"`
	Background string `json:"background,omitempty"`
	Color      string `json:"color,omitempty"`
	Border     string `json:"border,omitempty"`
}

// PresentationSort 是该平台的默认排序与可选排序集合。
//
// 服务端下发**默认值与可选集合**，实际排序仍由查询协议在服务端执行——客户端不得据此在本地重排
// 服务端返回的列表，那会破坏游标分页的一致性。
type PresentationSort struct {
	Collation     string   `json:"collation,omitempty"`
	WorkDefault   string   `json:"workDefault,omitempty"`
	WorkOptions   []string `json:"workOptions,omitempty"`
	AuthorDefault string   `json:"authorDefault,omitempty"`
	AuthorOptions []string `json:"authorOptions,omitempty"`
	BrowseDefault string   `json:"browseDefault,omitempty"`
	BrowseOptions []string `json:"browseOptions,omitempty"`
}

// PresentationTime 是该平台的时间显示语义。
//
// 存储时区固定为 UTC（见数据原则），因此这里只声明**显示**语义：用哪个 IANA 时区、什么格式。
// 解析语义属于 work_date 原语，不在这里重复声明，避免同一件事有两个事实源。
type PresentationTime struct {
	DisplayTimezone string `json:"displayTimezone,omitempty"`
	DisplayFormat   string `json:"displayFormat,omitempty"`
}

// presentationConfig 是 `presentation` 原语的原始配置形态（snake_case，与规则包其余部分一致）。
type presentationConfig struct {
	Name          string                 `json:"name,omitempty"`
	Description   string                 `json:"description,omitempty"`
	AuthorLabel   string                 `json:"author_label,omitempty"`
	ShowInSidebar *bool                  `json:"show_in_sidebar,omitempty"`
	ShowInManager *bool                  `json:"show_in_manager,omitempty"`
	Icon          *presentationIconValue `json:"icon,omitempty"`
	Sort          *presentationSortValue `json:"sort,omitempty"`
	Time          *presentationTimeValue `json:"time,omitempty"`
}

type presentationIconValue struct {
	Kind       string `json:"kind"`
	Glyph      string `json:"glyph"`
	Background string `json:"background,omitempty"`
	Color      string `json:"color,omitempty"`
	Border     string `json:"border,omitempty"`
}

type presentationSortValue struct {
	Collation     string   `json:"collation,omitempty"`
	WorkDefault   string   `json:"work_default,omitempty"`
	WorkOptions   []string `json:"work_options,omitempty"`
	AuthorDefault string   `json:"author_default,omitempty"`
	AuthorOptions []string `json:"author_options,omitempty"`
	BrowseDefault string   `json:"browse_default,omitempty"`
	BrowseOptions []string `json:"browse_options,omitempty"`
}

type presentationTimeValue struct {
	DisplayTimezone string `json:"display_timezone,omitempty"`
	DisplayFormat   string `json:"display_format,omitempty"`
}

// 排序选项词表是契约的一部分：客户端按这些名字渲染排序菜单，服务端按同一批名字执行排序。
// 接受未知名字会让客户端显示一个永远无法生效的选项。
var (
	workSortOptions = map[string]struct{}{
		"date_desc": {}, "date_asc": {}, "title_asc": {}, "title_desc": {},
		"name_asc": {}, "name_desc": {},
	}
	authorSortOptions = map[string]struct{}{
		"name_asc": {}, "name_desc": {}, "latest_desc": {}, "latest_asc": {},
		"posts_desc": {}, "posts_asc": {},
	}
	browseSortOptions = map[string]struct{}{
		"natural_asc": {}, "natural_desc": {},
	}
)

const presentationGlyphRunes = 4

// compilePresentation 校验并编译 `presentation` 配置。全部校验在编译期完成，使非法时区、未知排序
// 选项与不一致的默认值在规则发布时就被拒绝，而不是让客户端在运行期显示一个无法生效的选项。
func compilePresentation(config presentationConfig, primitiveID string) (Presentation, error) {
	result := Presentation{
		Name: config.Name, Description: config.Description, AuthorLabel: config.AuthorLabel,
		// 缺省可见：真实规则里绝大多数平台都可见，把「未声明」解释为可见更符合直觉，
		// 也让只想改名字的规则不必重复声明可见性。
		ShowInSidebar: config.ShowInSidebar == nil || *config.ShowInSidebar,
		ShowInManager: config.ShowInManager == nil || *config.ShowInManager,
	}
	if config.Icon != nil {
		if config.Icon.Kind != "glyph" {
			return Presentation{}, fmt.Errorf("presentation %s 的 icon.kind %q 不受支持，当前只支持 glyph", primitiveID, config.Icon.Kind)
		}
		glyph := strings.TrimSpace(config.Icon.Glyph)
		if glyph == "" {
			return Presentation{}, fmt.Errorf("presentation %s 的 icon 缺少 glyph", primitiveID)
		}
		if len([]rune(glyph)) > presentationGlyphRunes {
			return Presentation{}, fmt.Errorf("presentation %s 的 glyph 超过 %d 个字符", primitiveID, presentationGlyphRunes)
		}
		result.Icon = &PresentationIcon{
			Kind: config.Icon.Kind, Glyph: glyph, Background: config.Icon.Background,
			Color: config.Icon.Color, Border: config.Icon.Border,
		}
	}
	if config.Sort != nil {
		sort, err := compilePresentationSort(*config.Sort, primitiveID)
		if err != nil {
			return Presentation{}, err
		}
		result.Sort = &sort
	}
	if config.Time != nil {
		if config.Time.DisplayTimezone != "" {
			if _, err := time.LoadLocation(config.Time.DisplayTimezone); err != nil {
				return Presentation{}, fmt.Errorf("presentation %s 的 display_timezone %q 不是有效 IANA 时区: %w",
					primitiveID, config.Time.DisplayTimezone, err)
			}
		}
		result.Time = &PresentationTime{
			DisplayTimezone: config.Time.DisplayTimezone, DisplayFormat: config.Time.DisplayFormat,
		}
	}
	return result, nil
}

func compilePresentationSort(config presentationSortValue, primitiveID string) (PresentationSort, error) {
	result := PresentationSort{
		Collation:     config.Collation,
		WorkDefault:   config.WorkDefault,
		WorkOptions:   append([]string(nil), config.WorkOptions...),
		AuthorDefault: config.AuthorDefault,
		AuthorOptions: append([]string(nil), config.AuthorOptions...),
		BrowseDefault: config.BrowseDefault,
		BrowseOptions: append([]string(nil), config.BrowseOptions...),
	}
	for _, group := range []struct {
		label      string
		vocabulary map[string]struct{}
		def        string
		options    []string
	}{
		{"work", workSortOptions, config.WorkDefault, config.WorkOptions},
		{"author", authorSortOptions, config.AuthorDefault, config.AuthorOptions},
		{"browse", browseSortOptions, config.BrowseDefault, config.BrowseOptions},
	} {
		for _, option := range group.options {
			if _, ok := group.vocabulary[option]; !ok {
				return PresentationSort{}, fmt.Errorf("presentation %s 的 %s_options 含未知排序 %q", primitiveID, group.label, option)
			}
		}
		if group.def == "" {
			continue
		}
		if _, ok := group.vocabulary[group.def]; !ok {
			return PresentationSort{}, fmt.Errorf("presentation %s 的 %s_default %q 不是已知排序", primitiveID, group.label, group.def)
		}
		// 默认值必须出现在可选集合里，否则客户端会显示一个「当前排序」不在菜单中的状态。
		if len(group.options) > 0 && !containsString(group.options, group.def) {
			return PresentationSort{}, fmt.Errorf("presentation %s 的 %s_default %q 不在 %s_options 中",
				primitiveID, group.label, group.def, group.label)
		}
	}
	return result, nil
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
