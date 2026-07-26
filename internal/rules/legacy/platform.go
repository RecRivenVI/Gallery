package legacy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// primitive 是待编码的规则原语。
type primitive struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Config any    `json:"config"`
}

// pointerChain 把旧配置的 metadata 取值链（如 `["user.name", "$path.author"]`）转换为
// JSON Pointer 链。
//
// `$path.*` 是旧工具的虚拟取值（从目录路径推断），不是 metadata 中的字段：作者与作品名的路径
// 回退由 Gallery 的 `path_match` 原语本身承担（`author_pattern`/`work_pattern` 的命名捕获），
// 因此这里把它们从 pointer 链中剔除并**逐项登记**，而不是生成一个永远解析不到的 pointer。
func pointerChain(values []string, platformID, field string, notes *[]Note) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if strings.HasPrefix(value, "$path.") {
			// 日期的路径回退由 convertPlatform 单独登记（它需要说明「缺少目录命名模式」这一
			// 更具体的原因），这里不重复登记同一件事。
			if field != "date" {
				*notes = append(*notes, Note{
					Platform: platformID, Field: "metadata." + field + "." + value,
					Reason: "路径回退由 path_match 的命名捕获承担，不进入 metadata pointer 链",
				})
			}
			continue
		}
		result = append(result, "/"+strings.ReplaceAll(value, ".", "/"))
	}
	return result
}

// convertPlatform 产出一个平台的完整规则包。
func convertPlatform(config Config, platform Platform, ruleSetID string) (json.RawMessage, []Note, error) {
	notes := []Note{}
	if platform.Structure.Mode != "author_work" {
		return nil, nil, fmt.Errorf("不支持的 structure.mode %q", platform.Structure.Mode)
	}
	if platform.Structure.WorkDetection != "leaf_with_visible_media" {
		notes = append(notes, Note{
			Platform: platform.ID, Field: "structure.work_detection",
			Reason: fmt.Sprintf("生产扫描器固定按含可见媒体的叶目录判定，旧配置声明 %q", platform.Structure.WorkDetection),
		})
	}
	primitives := []primitive{}

	// 目录结构：author_work 对应两级 glob。
	primitives = append(primitives, primitive{
		ID: "work", Kind: "path_match",
		Config: map[string]any{
			"scope": "work_directory", "glob": "*/*",
			"title": "directory_name", "stable_key": "relative_path",
			"metadata_file": config.Library.MetadataFile,
		},
	})

	// 标题：date_title 的平台用日期作标题，其余走 metadata 链。
	titlePointers := pointerChain(platform.Metadata.Title, platform.ID, "title", &notes)
	if len(titlePointers) > 0 {
		primitives = append(primitives, primitive{
			ID: "title", Kind: "fallback",
			Config: map[string]any{"target": "title", "pointers": titlePointers, "default": ""},
		})
	}
	if platform.Metadata.DateTitle {
		notes = append(notes, Note{
			Platform: platform.ID, Field: "metadata.date_title",
			Reason: "标题取日期的展示语义由客户端按 publishedAt 呈现，规则仍产出原始标题",
		})
	}

	for _, item := range []struct {
		id, target string
		values     []string
	}{
		{"creator", "creator", platform.Metadata.Author},
		{"creator-id", "external_id", platform.Metadata.AuthorID},
		{"description", "description", platform.Metadata.Description},
		{"source-url", "source_url", platform.Metadata.SourceURL},
		{"tags", "tags", platform.Metadata.Tags},
	} {
		pointers := pointerChain(item.values, platform.ID, item.id, &notes)
		if len(pointers) == 0 {
			continue
		}
		primitives = append(primitives, primitive{
			ID: item.id, Kind: "fallback",
			Config: map[string]any{"target": item.target, "pointers": pointers, "default": ""},
		})
	}

	// 发布时间：metadata 链 + 输入时区。路径日期回退见下方登记。
	datePointers := pointerChain(platform.Metadata.Date, platform.ID, "date", &notes)
	if len(datePointers) > 0 {
		inputTimezone := platform.Metadata.Time.InputTimezone
		if inputTimezone == "" {
			inputTimezone = config.Time.NaiveTimestampTimezone
		}
		if inputTimezone == "" {
			inputTimezone = "UTC"
		}
		primitives = append(primitives, primitive{
			ID: "work-date", Kind: "work_date",
			Config: map[string]any{"pointers": datePointers, "input_timezone": inputTimezone},
		})
	}

	// 媒体分类：逐扩展名。旧配置按扩展名列表声明，Gallery 用 glob，因此逐项展开。
	for _, extension := range platform.mediaExtensions(config) {
		primitives = append(primitives, primitive{
			ID: "media-" + strings.ToLower(extension.name), Kind: "media_classify",
			Config: map[string]any{
				"glob": "*." + extension.name, "kind": extension.kind, "mime": extension.mime,
			},
		})
	}

	if len(config.Media.HiddenNameGlobs) > 0 {
		primitives = append(primitives, primitive{
			ID: "hidden", Kind: "media_hidden",
			Config: map[string]any{"globs": config.Media.HiddenNameGlobs},
		})
	}
	if config.Cover.DisableMarker != "" {
		primitives = append(primitives, primitive{
			ID: "nocover", Kind: "cover_disable_marker",
			Config: map[string]any{"filename": config.Cover.DisableMarker},
		})
	}
	for index, glob := range config.Cover.ExplicitGlobs {
		primitives = append(primitives, primitive{
			ID: fmt.Sprintf("cover-%d", index), Kind: "cover_candidate",
			Config: map[string]any{"glob": glob, "priority": 100},
		})
	}

	// 平台专属的隐藏规则与封面候选：这两类依赖同目录兄弟文件与跨规则引用，当前原语无法表达。
	if platform.Media != nil && len(platform.Media.Hide) > 0 {
		notes = append(notes, Note{
			Platform: platform.ID, Field: "media.hide",
			Reason: "条件隐藏依赖同目录兄弟文件扩展名与 metadata 文本匹配，当前原语与 CEL 上下文不提供该输入",
		})
	}
	if platform.Cover != nil {
		for _, candidate := range platform.Cover.Candidates {
			if candidate.NameRegex == "" {
				continue
			}
			glob, ok := regexToGlob(candidate.NameRegex)
			if !ok {
				notes = append(notes, Note{
					Platform: platform.ID, Field: "cover.candidates." + candidate.ID,
					Reason: "name_regex 无法安全降级为 glob，封面候选未转换",
				})
				continue
			}
			item := map[string]any{"glob": glob, "priority": candidate.Priority}
			if candidate.MediaType != "" {
				item["media_type"] = candidate.MediaType
			}
			primitives = append(primitives, primitive{
				ID: "cover-" + candidate.ID, Kind: "cover_candidate", Config: item,
			})
			if len(candidate.When) > 0 && string(candidate.When) != "null" {
				notes = append(notes, Note{
					Platform: platform.ID, Field: "cover.candidates." + candidate.ID + ".when",
					Reason: "候选的规则引用条件未转换；候选按 glob 与媒体类型生效，可能比旧行为宽",
				})
			}
		}
	}

	// 角标：只取 when.platform 命中本平台的（未声明 platform 视为全平台）。
	for _, badge := range config.Badges {
		if !badge.Enabled || !badgeAppliesTo(badge, platform.ID) {
			continue
		}
		item, note := convertBadge(badge, platform.ID)
		if note != nil {
			notes = append(notes, *note)
			continue
		}
		primitives = append(primitives, *item)
	}

	// 平台呈现：UI + 库级排序 + 时间显示。
	presentation := map[string]any{
		"name": platform.UI.Name, "description": platform.UI.Description,
		"author_label":    platform.UI.AuthorLabel,
		"show_in_sidebar": platform.UI.ShowInSidebar, "show_in_manager": platform.UI.ShowInManager,
		"sort": map[string]any{
			"collation":      config.Sort.Collation,
			"work_default":   config.Sort.WorkDefault,
			"work_options":   config.Sort.WorkOptions,
			"author_default": config.Sort.AuthorDefault,
			"author_options": config.Sort.AuthorOptions,
			"browse_default": config.Sort.BrowseDefault,
			"browse_options": config.Sort.BrowseOptions,
		},
	}
	if platform.UI.Icon.Kind != "" {
		presentation["icon"] = map[string]any{
			"kind": platform.UI.Icon.Kind, "glyph": platform.UI.Icon.Glyph,
			"background": platform.UI.Icon.Background, "color": platform.UI.Icon.Color,
			"border": platform.UI.Icon.Border,
		}
	}
	displayTimezone := platform.Metadata.Time.DisplayTimezone
	if displayTimezone == "" || displayTimezone == "inherit" {
		displayTimezone = config.Time.DisplayTimezone
	}
	if displayTimezone != "" || config.Time.DisplayFormat != "" {
		presentation["time"] = map[string]any{
			"display_timezone": displayTimezone, "display_format": config.Time.DisplayFormat,
		}
	}
	primitives = append(primitives, primitive{ID: "presentation", Kind: "presentation", Config: presentation})

	// `$path.datetime` 的路径日期回退需要目录命名模式，而旧配置并不声明它——那是旧工具代码里的
	// 隐式约定。这里刻意不猜测一个模式：错误的模式会静默匹配不到，比没有模式更难发现。
	if hasPathDatetime(platform.Metadata.Date) {
		notes = append(notes, Note{
			Platform: platform.ID, Field: "metadata.date.$path.datetime",
			Reason: "路径日期模式未在旧配置中声明，需按真实目录命名补充 work_date.path_pattern 后再启用",
		})
	}
	if platform.Metadata.Categories != nil {
		notes = append(notes, Note{
			Platform: platform.ID, Field: "metadata.categories",
			Reason: "category 校验属于旧工具的来源判别，Gallery 由 Source 与规则绑定表达",
		})
	}

	encoded, err := encodePackage(ruleSetID, primitives)
	if err != nil {
		return nil, nil, err
	}
	return encoded, notes, nil
}

type mediaExtension struct{ name, kind, mime string }

// mediaExtensions 展开库级图片与视频扩展名。旧配置在库级声明一次，全部平台共用。
func (Platform) mediaExtensions(config Config) []mediaExtension {
	mimes := map[string]string{
		"jpg": "image/jpeg", "jpeg": "image/jpeg", "png": "image/png", "gif": "image/gif",
		"bmp": "image/bmp", "webp": "image/webp", "svg": "image/svg+xml", "ico": "image/x-icon",
		"tiff": "image/tiff", "tif": "image/tiff", "avif": "image/avif",
		"mp4": "video/mp4", "webm": "video/webm", "mov": "video/quicktime",
		"mkv": "video/x-matroska", "avi": "video/x-msvideo", "m4v": "video/mp4", "ogv": "video/ogg",
	}
	result := make([]mediaExtension, 0, len(config.Media.ImageExtensions)+len(config.Media.VideoExtensions))
	for _, name := range config.Media.ImageExtensions {
		result = append(result, mediaExtension{name: name, kind: "image", mime: mimeOr(mimes, name, "application/octet-stream")})
	}
	for _, name := range config.Media.VideoExtensions {
		result = append(result, mediaExtension{name: name, kind: "video", mime: mimeOr(mimes, name, "application/octet-stream")})
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result
}

func mimeOr(table map[string]string, name, fallback string) string {
	if value, ok := table[strings.ToLower(name)]; ok {
		return value
	}
	return fallback
}

func badgeAppliesTo(badge Badge, platformID string) bool {
	if len(badge.When.Platform) == 0 {
		return true
	}
	for _, candidate := range badge.When.Platform {
		if candidate == platformID {
			return true
		}
	}
	return false
}

// convertBadge 把旧角标声明转成 badge 原语。无法表达的条件形态返回登记而不是猜测。
func convertBadge(badge Badge, platformID string) (*primitive, *Note) {
	when := map[string]any{}
	if len(badge.When.Suffix) > 0 {
		when["media_suffix"] = badge.When.Suffix
		when["case_insensitive"] = true
	}
	for key, raw := range badge.When.Metadata {
		if key == "tags" {
			var tags []string
			if json.Unmarshal(raw, &tags) != nil {
				return nil, &Note{Platform: platformID, Field: "badges." + badge.ID,
					Reason: "tags 条件不是字符串数组"}
			}
			when["tags"] = tags
			continue
		}
		var values []json.RawMessage
		if json.Unmarshal(raw, &values) != nil {
			return nil, &Note{Platform: platformID, Field: "badges." + badge.ID,
				Reason: fmt.Sprintf("metadata 条件 %q 不是取值数组", key)}
		}
		when["metadata_pointer"] = "/" + strings.ReplaceAll(key, ".", "/")
		when["metadata_values"] = values
	}
	if len(when) == 0 {
		return nil, &Note{Platform: platformID, Field: "badges." + badge.ID,
			Reason: "角标没有可转换的触发条件"}
	}
	style := map[string]any{}
	for key, value := range map[string]string{
		"color": badge.Color, "background": badge.Background, "border": badge.Border,
		"color_light": badge.ColorLight, "background_light": badge.BackgroundLight,
		"border_light": badge.BorderLight,
	} {
		if value != "" {
			style[key] = value
		}
	}
	return &primitive{
		ID: "badge-" + badge.ID, Kind: "badge",
		Config: map[string]any{
			"badge_id": badge.ID, "order": badge.Order, "position": badge.Position,
			"label": badge.Label, "style": style, "when": when,
		},
	}, nil
}

// regexToGlob 只处理旧配置中实际出现的一类简单锚定正则（如 `^1\.[^.]+$`）。
// 任何超出该形态的表达式一律拒绝转换——把正则近似成 glob 会静默改变匹配范围。
func regexToGlob(expression string) (string, bool) {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(expression, "^"), "$")
	if !strings.HasSuffix(trimmed, `\.[^.]+`) {
		return "", false
	}
	stem := strings.TrimSuffix(trimmed, `\.[^.]+`)
	if stem == "" || strings.ContainsAny(stem, `\[](){}|+*?^$.`) {
		return "", false
	}
	return stem + ".*", true
}

func hasPathDatetime(values []string) bool {
	for _, value := range values {
		if value == "$path.datetime" {
			return true
		}
	}
	return false
}

// encodePackage 组装完整规则包。它只产出结构，编译与校验由 rules.CompilePackage 承担——
// 转换器不重复实现一套校验，否则两处会漂移。
func encodePackage(ruleSetID string, primitives []primitive) (json.RawMessage, error) {
	document := map[string]any{
		"rule_set_id": ruleSetID, "version": "1.0.0", "schema_version": 1,
		"normalization_algorithm_version": "gallery-canonical-json-v1",
		"compiler_requirement":            "gallery-rule-compiler-v1",
		"cel_profile_version":             "gallery-cel-v1",
		"parameter_schema":                map[string]any{"type": "object", "additionalProperties": false},
		"provider_namespaces":             []string{},
		"primitives":                      primitives,
		"cel_expressions":                 []any{},
		"tests":                           []any{map[string]any{"id": "imported"}},
		"extensions":                      map[string]any{},
	}
	return json.Marshal(document)
}
