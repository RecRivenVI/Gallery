// Package legacy 把外部工具的 `gallery-rules.json`（`schema_version: 3`）转换为 Gallery 规则包。
//
// 转换是**一次性导入**，不是持续兼容层：产出的规则包此后由 Gallery 自己的生命周期管理，旧配置不再
// 是事实源。因此这里只做结构映射与显式拒绝，不保留任何「运行时回读旧配置」的路径。
//
// 一个旧配置产出多份结果：每个启用平台一份规则包（平台在 Gallery 领域模型中对应一个 Source，
// 见 `docs/architecture/domain-model-and-data-ownership.md`），外加文件根声明。库级默认值（metadata 文件名、时间显示语义、排序集合）逐平台
// 下发到各自的规则包，使每个 Source 的解释完全自包含。
package legacy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Config 是旧配置中本转换器实际使用的部分。刻意不是全字段映射：未被使用的字段会在
// Convert 中被显式登记为「未转换」，而不是静默忽略。
//
// 「显式登记」由两条互补的机制共同保证，缺一不可：
//   - 本文件与 platform.go 中逐字段的手写登记，说明「这个语义我们理解、但当前无法表达」；
//   - unknown.go 的 collectUnknownFields，把原文与本结构体的已声明字段集合做差集，说明
//     「这个字段我们根本没看见」。没有它时，任何未在此声明的字段都会被 encoding/json 静默
//     丢弃，且没有任何返回值、错误或日志能暴露它。
type Config struct {
	SchemaVersion int             `json:"schema_version"`
	Library       LibraryConfig   `json:"library"`
	Time          TimeConfig      `json:"time"`
	Media         MediaConfig     `json:"media"`
	Cover         CoverConfig     `json:"cover"`
	FileRoots     []FileRootEntry `json:"file_roots"`
	Sort          SortConfig      `json:"sort"`
	Platforms     []Platform      `json:"platforms"`
	Badges        []Badge         `json:"badges"`
}

type LibraryConfig struct {
	ID           string `json:"id"`
	MetadataFile string `json:"metadata_file"`
	PathCase     string `json:"path_case"`
}

type TimeConfig struct {
	StorageTimezone            string `json:"storage_timezone"`
	DisplayTimezone            string `json:"display_timezone"`
	DisplayFormat              string `json:"display_format"`
	NaiveTimestampTimezone     string `json:"naive_timestamp_timezone"`
	DirectoryTimestampTimezone string `json:"directory_timestamp_timezone"`
}

type MediaConfig struct {
	ImageExtensions []string `json:"image_extensions"`
	VideoExtensions []string `json:"video_extensions"`
	HiddenNameGlobs []string `json:"hidden_name_globs"`
}

type CoverConfig struct {
	DisableMarker string            `json:"disable_marker"`
	ExplicitGlobs []string          `json:"explicit_globs"`
	LeafFallback  string            `json:"leaf_fallback"`
	Aggregate     map[string]string `json:"aggregate"`
}

type FileRootEntry struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	Enabled bool   `json:"enabled"`
	Order   int    `json:"order"`
}

type SortConfig struct {
	Collation     string   `json:"collation"`
	WorkDefault   string   `json:"work_default"`
	WorkOptions   []string `json:"work_options"`
	AuthorDefault string   `json:"author_default"`
	AuthorOptions []string `json:"author_options"`
	BrowseDefault string   `json:"browse_default"`
	BrowseOptions []string `json:"browse_options"`
}

type Platform struct {
	ID        string         `json:"id"`
	Enabled   bool           `json:"enabled"`
	Path      string         `json:"path"`
	Order     int            `json:"order"`
	ScanOrder int            `json:"scan_order"`
	UI        PlatformUI     `json:"ui"`
	Structure Structure      `json:"structure"`
	Metadata  PlatformMeta   `json:"metadata"`
	Media     *PlatformMedia `json:"media,omitempty"`
	Cover     *PlatformCover `json:"cover,omitempty"`
}

type PlatformUI struct {
	Name          string       `json:"name"`
	Description   string       `json:"description"`
	ShowInSidebar bool         `json:"show_in_sidebar"`
	ShowInManager bool         `json:"show_in_manager"`
	AuthorLabel   string       `json:"author_label"`
	Icon          PlatformIcon `json:"icon"`
}

type PlatformIcon struct {
	Kind       string `json:"kind"`
	Glyph      string `json:"glyph"`
	Background string `json:"background"`
	Color      string `json:"color"`
	Border     string `json:"border"`
}

type Structure struct {
	Mode          string `json:"mode"`
	AuthorPattern string `json:"author_pattern"`
	WorkPattern   string `json:"work_pattern"`
	WorkDetection string `json:"work_detection"`
	// UnknownDirectory 声明不匹配目录模式的目录如何处理。
	UnknownDirectory string `json:"unknown_directory"`
	// AllowMediaInWorkChildren 声明作品子目录中的文件是否算作该作品的媒体。
	AllowMediaInWorkChildren bool            `json:"allow_media_in_work_children"`
	Author                   StructureAuthor `json:"author"`
	Work                     StructureWork   `json:"work"`
}

type StructureAuthor struct {
	// KeySource 声明作者身份取自 metadata 还是路径。
	KeySource string `json:"key_source"`
	// PathCapture 是作者段在目录模式中的命名捕获名。
	PathCapture string `json:"path_capture"`
}

type StructureWork struct {
	PathCapture string `json:"path_capture"`
	// MetadataRequired 声明每个作品目录是否必须存在 metadata 文件。
	//
	// 用指针区分「未声明」与「显式声明为 false」：生产扫描器对 `path_match.metadata_file` 的语义
	// 是**强制**的（缺文件即整个扫描失败），因此这两种情况不能混为一谈——未声明时保持既有行为，
	// 显式声明为 false 时必须不下发文件名。
	MetadataRequired *bool `json:"metadata_required"`
}

type PlatformMeta struct {
	Categories []string `json:"categories"`
	// CategoryPaths 是 metadata 中承载来源类别的取值路径，与 Categories 一起构成旧工具的来源判别。
	CategoryPaths []string `json:"category_paths"`
	DateTitle     bool     `json:"date_title"`
	Title         []string `json:"title"`
	Author        []string `json:"author"`
	AuthorID      []string `json:"author_id"`
	Description   []string `json:"description"`
	Tags          []string `json:"tags"`
	Date          []string `json:"date"`
	SourceURL     []string `json:"source_url"`
	// Transforms 声明每个字段取值后的归一化方式。它改变的是**取到的值本身**，因此必须显式声明：
	// 漏掉时会静默丢失一个值变换步骤，产出的字段看起来完全正常，只是内容与旧工具不同。
	Transforms PlatformTransforms `json:"transforms"`
	Time       PlatformMetaTime   `json:"time"`
}

type PlatformTransforms struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Tags        string `json:"tags"`
	Date        string `json:"date"`
}

type PlatformMetaTime struct {
	InputTimezone  string `json:"input_timezone"`
	OutputTimezone string `json:"output_timezone"`
	// DirectoryTimestampTimezone 是**平台级**的目录名日期时区，与库级 `time.directory_timestamp_timezone`
	// 同名同义，平台级优先。真实旧配置为每个平台都声明了它。
	//
	// 它必须在此声明：漏掉时 encoding/json 会静默丢弃平台级取值，转换器只能读到库级取值，于是
	// 「平台单独声明了另一个目录时区」这件事无声消失，产出的发布时间格式合法、排序正常、只是整体
	// 偏移若干小时——与本轮修掉的「两类朴素时间戳共用一个时区」是同一类静默缺陷。
	DirectoryTimestampTimezone string `json:"directory_timestamp_timezone"`
	DisplayTimezone            string `json:"display_timezone"`
}

type PlatformMedia struct {
	Hide []MediaHideRule `json:"hide"`
}

// MediaHideRule 是旧配置的平台专属条件隐藏规则。逐字段声明而不是保留原文，使规则内部的未识别
// 字段也能进入差集登记，并使「哪一条隐藏规则因为什么原因未转换」可以逐条说明。
type MediaHideRule struct {
	ID        string        `json:"id"`
	NameRegex string        `json:"name_regex"`
	When      MediaHideWhen `json:"when"`
}

type MediaHideWhen struct {
	// Files 声明「同目录兄弟文件的扩展名集合命中任意一项」。
	Files *MediaHideFiles `json:"files,omitempty"`
	// MetadataCategory 限定作品 metadata 的来源类别。
	MetadataCategory []string `json:"metadata_category,omitempty"`
	// MetadataAnyTextPaths 是若干 metadata 取值路径，其中任意一处文本命中 TextRegex 即成立。
	MetadataAnyTextPaths []string `json:"metadata_any_text_paths,omitempty"`
	TextRegex            string   `json:"text_regex,omitempty"`
}

type MediaHideFiles struct {
	Extensions []string `json:"extensions"`
}

type PlatformCover struct {
	Candidates []CoverCandidate `json:"candidates"`
}

type CoverCandidate struct {
	ID        string          `json:"id"`
	Priority  int             `json:"priority"`
	NameRegex string          `json:"name_regex"`
	MediaType string          `json:"media_type"`
	When      json.RawMessage `json:"when"`
}

type Badge struct {
	ID              string    `json:"id"`
	Enabled         bool      `json:"enabled"`
	Order           int       `json:"order"`
	Position        string    `json:"position"`
	Label           string    `json:"label"`
	Color           string    `json:"color"`
	Background      string    `json:"background"`
	Border          string    `json:"border"`
	ColorLight      string    `json:"color_light"`
	BackgroundLight string    `json:"background_light"`
	BorderLight     string    `json:"border_light"`
	When            BadgeWhen `json:"when"`
}

type BadgeWhen struct {
	Platform []string                   `json:"platform"`
	Suffix   []string                   `json:"suffix"`
	Metadata map[string]json.RawMessage `json:"metadata"`
}

// Result 是一次转换的完整产物。
type Result struct {
	// Packages 按平台 ID 索引，值是规范 JSON 形态的 Gallery 规则包。
	Packages map[string]json.RawMessage
	// SourceRoots 按平台 ID 给出该平台的只读根路径，供登记 Source 使用。
	SourceRoots map[string]string
	// FileRoots 是启用的文件根声明。
	FileRoots []FileRootEntry
	// Unconverted 逐项登记**未被转换的旧配置语义**及其原因。
	//
	// 它是转换结果的一等组成部分，不是附注：静默丢弃无法表达的旧语义会让「导入成功」变成一句
	// 无法核实的断言。调用方必须核对本列表，确认每一项要么不影响正确性，要么已另行处理。
	Unconverted []Note
}

// Note 是一条未转换登记。
type Note struct {
	Platform string
	Field    string
	Reason   string
}

// Convert 把旧配置转换为逐平台规则包。
func Convert(input []byte, ruleSetIDs map[string]string) (Result, error) {
	var config Config
	decoder := json.NewDecoder(strings.NewReader(string(input)))
	if err := decoder.Decode(&config); err != nil {
		return Result{}, fmt.Errorf("解析旧配置: %w", err)
	}
	if config.SchemaVersion != 3 {
		return Result{}, fmt.Errorf("不支持的旧配置 schema_version %d，本转换器只处理 3", config.SchemaVersion)
	}
	result := Result{
		Packages: map[string]json.RawMessage{}, SourceRoots: map[string]string{},
		Unconverted: []Note{},
	}
	// 未识别字段的差集先于一切转换收集：它衡量的是「本转换器看见了旧配置的多少」，与转换是否
	// 成功无关，也不因为后续任何一步失败而丢失。
	unknown, err := collectUnknownFields(input)
	if err != nil {
		return Result{}, fmt.Errorf("比对旧配置字段集合: %w", err)
	}
	result.Unconverted = append(result.Unconverted, unknown...)
	// Gallery 的时刻一律以 UTC 存储（`docs/architecture/query-search-and-sorting.md`），因此声明为 UTC 的存储时区是等价承接而不是
	// 未转换；声明为其它时区则是真正无法承接的语义，必须登记。
	if config.Time.StorageTimezone != "" && config.Time.StorageTimezone != "UTC" {
		result.Unconverted = append(result.Unconverted, Note{
			Field:  "time.storage_timezone",
			Reason: fmt.Sprintf("Gallery 一律以 UTC 存储时刻，不支持按 %q 存储", config.Time.StorageTimezone),
		})
	}
	if config.Library.PathCase != "" && config.Library.PathCase != "preserve" {
		result.Unconverted = append(result.Unconverted, Note{
			Field:  "library.path_case",
			Reason: fmt.Sprintf("Gallery 逐 code point 保留路径，不支持 %q 折叠", config.Library.PathCase),
		})
	}
	if config.Cover.LeafFallback != "" && config.Cover.LeafFallback != "first_natural_media" {
		result.Unconverted = append(result.Unconverted, Note{
			Field:  "cover.leaf_fallback",
			Reason: fmt.Sprintf("Gallery 的封面回退固定为第一张可见媒体，不支持 %q", config.Cover.LeafFallback),
		})
	}
	// 聚合封面策略由 Catalog 投影实现（规则不表达全局聚合），因此这里只核对策略与实现一致。
	for scope, strategy := range config.Cover.Aggregate {
		expected := map[string]string{
			"author": "latest_dated_work", "platform": "latest_dated_author", "library": "latest_dated_platform",
		}[scope]
		if expected == "" || strategy != expected {
			result.Unconverted = append(result.Unconverted, Note{
				Field:  "cover.aggregate." + scope,
				Reason: fmt.Sprintf("当前实现固定为 %q，旧配置声明 %q", expected, strategy),
			})
		}
	}
	for _, root := range config.FileRoots {
		if root.Enabled {
			result.FileRoots = append(result.FileRoots, root)
		}
	}
	for _, platform := range config.Platforms {
		if !platform.Enabled {
			continue
		}
		ruleSetID := ruleSetIDs[platform.ID]
		if ruleSetID == "" {
			return Result{}, fmt.Errorf("平台 %s 缺少 rule_set_id", platform.ID)
		}
		encoded, notes, err := convertPlatform(config, platform, ruleSetID)
		if err != nil {
			return Result{}, fmt.Errorf("平台 %s: %w", platform.ID, err)
		}
		result.Packages[platform.ID] = encoded
		result.SourceRoots[platform.ID] = platform.Path
		result.Unconverted = append(result.Unconverted, notes...)
	}
	sort.SliceStable(result.Unconverted, func(i, j int) bool {
		if result.Unconverted[i].Platform != result.Unconverted[j].Platform {
			return result.Unconverted[i].Platform < result.Unconverted[j].Platform
		}
		return result.Unconverted[i].Field < result.Unconverted[j].Field
	})
	return result, nil
}
