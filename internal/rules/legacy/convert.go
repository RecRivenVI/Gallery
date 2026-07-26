// Package legacy 把外部工具的 `gallery-rules.json`（`schema_version: 3`）转换为 Gallery 规则包。
//
// 转换是**一次性导入**，不是持续兼容层：产出的规则包此后由 Gallery 自己的生命周期管理，旧配置不再
// 是事实源。因此这里只做结构映射与显式拒绝，不保留任何「运行时回读旧配置」的路径。
//
// 一个旧配置产出多份结果：每个启用平台一份规则包（平台在 Gallery 领域模型中对应一个 Source，
// 见 `规范/03`），外加文件根声明。库级默认值（metadata 文件名、时间显示语义、排序集合）逐平台
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
}

type PlatformMeta struct {
	Categories  []string         `json:"categories"`
	DateTitle   bool             `json:"date_title"`
	Title       []string         `json:"title"`
	Author      []string         `json:"author"`
	AuthorID    []string         `json:"author_id"`
	Description []string         `json:"description"`
	Tags        []string         `json:"tags"`
	Date        []string         `json:"date"`
	SourceURL   []string         `json:"source_url"`
	Time        PlatformMetaTime `json:"time"`
}

type PlatformMetaTime struct {
	InputTimezone   string `json:"input_timezone"`
	OutputTimezone  string `json:"output_timezone"`
	DisplayTimezone string `json:"display_timezone"`
}

type PlatformMedia struct {
	Hide []json.RawMessage `json:"hide"`
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
