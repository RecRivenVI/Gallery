package legacy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// PathDatetimePattern 是旧工具 `$path.datetime` 的目录命名约定，由对真实来源的**有界只读观察**
// 得出。观察范围、逐平台结果与零写入 guard 结论登记在
// [验证记录](../../../Documents/证据/验证记录.md) 的 EV-47，可核实。
//
// 观察结论：9 个平台共 177 个作品目录**全部**形如 `YYYY-MM-DD_HH-MM-SS_<标识>`，标识部分在不同
// 平台分别是纯数字或含字母与短横线。因此模式只锚定日期时间部分，不约束其后的标识。
//
// 前导 `/` 不可省略：全部 10 个平台都是 `author_work` 两级结构，相对路径为 `{作者}/{作品}`，作品段
// 前必有分隔符而作者段位于首段、其前没有分隔符。没有它时，若某个作者目录名恰好以日期时间开头，
// 就会先命中作者段而取到错误的时间。Go 的 RE2 没有后顾断言，靠这个前导字符定位是可用且精确的做法。
//
// **Venera 不遵循该约定**：它的作品目录是纯数字章节号（观察到 0/5 匹配），且它把路径日期声明为
// **唯一**日期来源（其余 9 个平台都另有 metadata 回退链）。因此该平台的作品在 v1 没有发布时间。
// 这不是缺陷而是数据本身没有该事实：规则会为每个作品留下 `RULE_WORK_DATE_MISSING`，不做掩盖，
// 也不用目录 mtime、数据库时间或章节号顺序伪造一个时间。
const PathDatetimePattern = `/(?P<year>\d{4})-(?P<month>\d{2})-(?P<day>\d{2})_(?P<hour>\d{2})-(?P<minute>\d{2})-(?P<second>\d{2})_`

// primitive 是待编码的规则原语。
type primitive struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Config any    `json:"config"`
}

// pointerChain 把旧配置的 metadata 取值链（如 `["user.name", "$path.author"]`）转换为
// JSON Pointer 链，并对其中的 `$path.*` 虚拟取值逐项给出承接结论。
//
// `$path.*` 是旧工具的虚拟取值（从目录路径推断），不是 metadata 中的字段，因此不能生成一个永远
// 解析不到的 pointer。当前 Gallery 只有一处路径取值能力：
//
//   - `path_match` 的 `title: "directory_name"`——求值把作品标题**初始化**为作品目录名，
//     metadata 链解析不出标题时该默认值保留下来。这正好等价于 `$path.work` 作为标题的回退。
//   - `work_date` 的 `path_pattern`——由 convertPlatform 单独承接 `$path.datetime`。
//
// 除此之外没有别的路径取值：`path_match` 的配置是**封闭**集合（scope/glob/title/stable_key/
// metadata_file），既没有 `author_pattern`/`work_pattern`，也没有任何命名捕获；`fallback` 与
// `stable_key` 只接受 metadata JSON Pointer；CEL 虽然拿得到 `path`，但只能作布尔谓词，不能赋值。
// 因此作者段取值与「用父目录名作标题」都无法表达，逐项登记而不是假装等价。
func pointerChain(values []string, platformID, field string, notes *[]Note) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if strings.HasPrefix(value, "$path.") {
			if reason := pathFallbackNote(field, value); reason != "" {
				*notes = append(*notes, Note{
					Platform: platformID, Field: "metadata." + field + "." + value, Reason: reason,
				})
			}
			continue
		}
		result = append(result, "/"+strings.ReplaceAll(value, ".", "/"))
	}
	return result
}

// pathFallbackNote 返回某个 `$path.*` 回退的未转换原因；返回空串表示该回退已被等价承接。
func pathFallbackNote(field, token string) string {
	switch {
	case field == "date":
		// `$path.datetime` 由 convertPlatform 的 work_date.path_pattern 承接，或在缺少目录命名
		// 模式时由它给出更具体的原因，这里不重复登记同一件事。
		return ""
	case field == "title" && token == "$path.work":
		// path_match 的 title=directory_name 已经把作品目录名作为标题默认值，等价承接。
		// 这条等价成立的前提是 title 的 fallback 原语**不带** default——带了就会用 default
		// 覆盖掉这个默认值，见 convertPlatform 中对 default 的说明。
		return ""
	case field == "title":
		return "标题取自作品目录之外的路径段（作者段）；path_match 的 title 只支持作品目录名本身，" +
			"当前原语没有路径命名捕获，无法表达"
	case token == "$path.author":
		return "作者段取值当前无法表达：path_match 配置是封闭集合且无命名捕获，fallback/stable_key " +
			"只接受 metadata JSON Pointer，CEL 只能作布尔谓词。metadata 缺该字段时作品将没有创作者"
	default:
		return "路径虚拟取值 " + token + " 当前没有对应的规则原语"
	}
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
	// 目录模式只有恰好是两级 `{作者}/{作品}` 时才与固定的 `*/*` glob 等价。声明成别的形状而
	// 转换器照样发出 `*/*`，会让整个平台按错误的层级被识别，因此逐个核对而不是默认忽略。
	if platform.Structure.AuthorPattern != "" && platform.Structure.AuthorPattern != "{author}" {
		notes = append(notes, Note{
			Platform: platform.ID, Field: "structure.author_pattern",
			Reason: fmt.Sprintf("path_match 固定按两级目录识别，不支持作者模式 %q", platform.Structure.AuthorPattern),
		})
	}
	if platform.Structure.WorkPattern != "" && platform.Structure.WorkPattern != "{author}/{work}" {
		notes = append(notes, Note{
			Platform: platform.ID, Field: "structure.work_pattern",
			Reason: fmt.Sprintf("path_match 固定按两级目录识别，不支持作品模式 %q", platform.Structure.WorkPattern),
		})
	}
	// 展示顺序与扫描顺序是旧工具自己的调度事实：Gallery 的列表顺序由 API 协议决定，扫描顺序由
	// Job 调度决定，规则包里没有承接位置。它们在旧配置里有明确取值，因此必须登记而不是消失。
	if platform.Order != 0 {
		notes = append(notes, Note{
			Platform: platform.ID, Field: "order",
			Reason: "平台展示顺序由 Gallery 的 API 排序协议决定，规则包不表达列表顺序",
		})
	}
	if platform.ScanOrder != 0 {
		notes = append(notes, Note{
			Platform: platform.ID, Field: "scan_order",
			Reason: "扫描顺序由 Gallery 的 Job 调度决定，规则包不表达扫描次序",
		})
	}
	notes = append(notes, structureNotes(platform)...)
	notes = append(notes, transformNotes(platform)...)
	if platform.Metadata.CategoryPaths != nil {
		notes = append(notes, Note{
			Platform: platform.ID, Field: "metadata.category_paths",
			Reason: "与 metadata.categories 同属旧工具的来源判别，Gallery 由 Source 与规则绑定表达",
		})
	}
	primitives := []primitive{}

	// 目录结构：author_work 对应两级 glob。
	pathMatch := map[string]any{
		"scope": "work_directory", "glob": "*/*",
		"title": "directory_name", "stable_key": "relative_path",
	}
	// metadata 文件名只在「每个作品都必须有 metadata」时下发。
	//
	// 生产扫描器对 `path_match.metadata_file` 的语义是**强制**的：作品目录里缺少该文件，整次扫描
	// 直接失败，而不是把该作品当作没有 metadata。因此把一个「可选的」metadata 文件名写进规则，
	// 等于让任何一个没有 metadata 的作品目录炸掉整个 Source。旧配置显式声明 metadata 可选的平台
	// 必须不下发文件名；它的 metadata 取值链（如果有）随之失效，这一点单独登记。
	if optional := platform.Structure.Work.MetadataRequired; optional != nil && !*optional {
		if hasMetadataChain(platform.Metadata) {
			notes = append(notes, Note{
				Platform: platform.ID, Field: "structure.work.metadata_required",
				Reason: "旧配置声明 metadata 文件可选，但扫描器对 path_match.metadata_file 是强制语义（缺文件即整次扫描失败），" +
					"因此不下发文件名；该平台所有 metadata 取值链随之失效",
			})
		} else {
			notes = append(notes, Note{
				Platform: platform.ID, Field: "structure.work.metadata_required",
				Reason: "旧配置声明 metadata 文件可选，但扫描器对 path_match.metadata_file 是强制语义（缺文件即整次扫描失败），" +
					"因此不下发文件名；该平台没有 metadata 取值链，无字段因此失效",
			})
		}
	} else {
		pathMatch["metadata_file"] = config.Library.MetadataFile
	}
	primitives = append(primitives, primitive{ID: "work", Kind: "path_match", Config: pathMatch})

	// metadata 取值链一律**不带 default**。
	//
	// 旧实现给每条链补了 `"default": ""`，那不是「没取到就留空」而是「没取到就写入空串」：求值端
	// 只在候选为 nil 时才跳过赋值，拿到空串会照样赋值。后果逐字段都是实际错误——
	//   - 标题被空串覆盖，于是 path_match 的 directory_name 默认值失效，作品标题为空；
	//     EnsureCanonical 对空标题返回校验错误，整个 Source 的扫描直接失败；
	//   - 标签被写成 `[""]`，凭空多出一个空标签进入索引与角标判定；
	//   - 创作者、描述、来源链接被空串而不是「缺失」表示，缺失事实不可辨。
	// 去掉 default 后，取不到值会留下 `RULE_SELECTOR_MISSING`（非必需），既保留默认值也留下痕迹。
	titlePointers := pointerChain(platform.Metadata.Title, platform.ID, "title", &notes)
	if len(titlePointers) > 0 {
		primitives = append(primitives, primitive{
			ID: "title", Kind: "fallback",
			Config: map[string]any{"target": "title", "pointers": titlePointers},
		})
	}
	if platform.Metadata.DateTitle {
		notes = append(notes, Note{
			Platform: platform.ID, Field: "metadata.date_title",
			Reason: "该标志只声明「本平台的标题就是日期」，标题链本身已取到 metadata 日期原值，" +
				"因此它改变的是标题**如何渲染**（按显示时区与格式），不是服务端产出的标题；" +
				"presentation 没有该标志位，故标题按原值展示。客户端不得改用 publishedAt 顶替标题：" +
				"标题链与日期链互相独立，metadata 缺日期时二者会分叉",
		})
	}

	for _, item := range []struct {
		id, target string
		values     []string
	}{
		{"creator", "creator", platform.Metadata.Author},
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
			Config: map[string]any{"target": item.target, "pointers": pointers},
		})
	}

	// 作者标识落到 **Creator 的稳定键**，不是作品的 external_id。
	//
	// 旧实现映射到 `external_id`，那是**作品**的外部身份：扫描把它作为 Work 的跨路径身份使用，
	// 同一次扫描中两个作品拿到同一个 external_id 会被判定为 duplicate_external_id 并返回
	// BINDING_REVIEW_REQUIRED——也就是说，只要某个作者有第二个作品，该 Source 的扫描就整体失败。
	// 正确的承接点是 `stable_key{target: "creator"}`：它产出 CreatorStableKey，扫描据此让同一
	// 作者的多个 occurrence 复用同一个 CanonicalCreator，这正是 author_id 的原意。
	authorIDPointers := pointerChain(platform.Metadata.AuthorID, platform.ID, "creator-id", &notes)
	switch {
	case len(authorIDPointers) == 1:
		primitives = append(primitives, primitive{
			ID: "creator-id", Kind: "stable_key",
			Config: map[string]any{"target": "creator", "pointer": authorIDPointers[0]},
		})
	case len(authorIDPointers) > 1:
		notes = append(notes, Note{
			Platform: platform.ID, Field: "metadata.author_id",
			Reason: "stable_key 只接受单个 pointer，无法表达多级回退链；该平台的作者标识未转换，" +
				"作者身份退化为逐作品 occurrence",
		})
	}

	// 发布时间：metadata 链 + 路径回退，两类朴素时间戳各自的时区分别映射。
	//
	// 旧配置把它们分成两个独立字段：`time.naive_timestamp_timezone` 管 metadata 里不带偏移量的时间戳，
	// `time.directory_timestamp_timezone` 管从目录名解析出的时间。二者当前恰好都是 UTC，但这是配置
	// 值巧合，不是语义等同——把目录时间也按 metadata 时区解释，会在用户改动其中一个时静默产生偏移了
	// 若干小时的发布时间，且格式合法、排序正常，没有任何信号能暴露它。
	datePointers := pointerChain(platform.Metadata.Date, platform.ID, "date", &notes)
	wantsPathDate := hasPathDatetime(platform.Metadata.Date)
	if len(datePointers) > 0 || wantsPathDate {
		item := map[string]any{
			"input_timezone": firstNonEmpty(
				platform.Metadata.Time.InputTimezone, config.Time.NaiveTimestampTimezone, "UTC"),
		}
		if len(datePointers) > 0 {
			item["pointers"] = datePointers
		}
		if wantsPathDate {
			item["path_pattern"] = PathDatetimePattern
			// 目录时区与 metadata 时区一样是「平台级 → 库级 → 内置默认」三级回退。旧配置为每个
			// 平台都声明了 `metadata.time.directory_timestamp_timezone`；只读库级取值会让平台级
			// 声明静默失效，产出偏移若干小时却完全合法的发布时间。
			item["path_timezone"] = firstNonEmpty(
				platform.Metadata.Time.DirectoryTimestampTimezone, config.Time.DirectoryTimestampTimezone, "UTC")
		}
		primitives = append(primitives, primitive{ID: "work-date", Kind: "work_date", Config: item})
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

	// 平台专属的条件隐藏：逐条登记，因为两类条件的可实现性完全不同，笼统一条会掩盖差别。
	if platform.Media != nil {
		for _, rule := range platform.Media.Hide {
			notes = append(notes, Note{
				Platform: platform.ID, Field: "media.hide." + rule.ID, Reason: mediaHideReason(rule),
			})
		}
	}
	if platform.Cover != nil {
		for _, candidate := range platform.Cover.Candidates {
			if candidate.NameRegex == "" {
				continue
			}
			// 条件无法表达时**整条候选不发出**，而不是保留候选、只登记条件。
			//
			// 保留候选等于把「满足某条件时才用它作封面」放宽成「永远用它作封面」。这不是一个
			// 温和的近似：这类候选的 priority 显著高于库级显式封面 glob 的 priority，于是任何
			// 作品里只要存在符合该 glob 的文件，它就会压过 `cover.*` 这类明确的封面声明——
			// 旧行为里本不该被选中的文件成了封面。封面选错比没有封面严重得多（没有封面时还有
			// 「第一张可见媒体」的确定性回退），因此宁可不选中。
			if hasCandidateCondition(candidate.When) {
				notes = append(notes, Note{
					Platform: platform.ID, Field: "cover.candidates." + candidate.ID + ".when",
					Reason: "候选条件引用另一条隐藏规则，当前原语无法表达；省略条件会让该候选无条件生效并" +
						"以更高优先级压过显式封面 glob，因此整条候选不发出，封面回落到显式 glob 或第一张可见媒体",
				})
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

	if platform.Metadata.Categories != nil {
		notes = append(notes, Note{
			Platform: platform.ID, Field: "metadata.categories",
			Reason: "category 校验属于旧工具的来源判别，Gallery 由 Source 与规则绑定表达",
		})
	}
	// 输出时区：Gallery 的时刻一律以 UTC 存储，声明为 UTC 是等价承接；声明为其它时区则会改变
	// 存储值本身，规则包没有对应表达位置，必须登记。
	if timezone := platform.Metadata.Time.OutputTimezone; timezone != "" && timezone != "UTC" {
		notes = append(notes, Note{
			Platform: platform.ID, Field: "metadata.time.output_timezone",
			Reason: fmt.Sprintf("Gallery 一律以 UTC 存储时刻，不支持按 %q 输出", timezone),
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

// hasMetadataChain 报告该平台是否声明了任何从 metadata 取值的字段（`$path.*` 是路径取值，不算）。
func hasMetadataChain(meta PlatformMeta) bool {
	for _, chain := range [][]string{
		meta.Title, meta.Author, meta.AuthorID, meta.Description, meta.Tags, meta.Date, meta.SourceURL,
	} {
		for _, value := range chain {
			if !strings.HasPrefix(value, "$path.") {
				return true
			}
		}
	}
	return false
}

// structureNotes 逐项核对目录结构声明中**改变识别行为**的开关。
//
// 每一项都只在取值与 Gallery 的等价行为不一致时登记：取值一致时它是等价承接而不是损失，
// 无差别登记会让未转换清单变成噪声，真正的损失反而淹没其中。
func structureNotes(platform Platform) []Note {
	notes := []Note{}
	if value := platform.Structure.UnknownDirectory; value != "" && value != "ignore" {
		notes = append(notes, Note{
			Platform: platform.ID, Field: "structure.unknown_directory",
			// 扫描器对不匹配 work_directory glob 的目录不产生任何作品，等价于 ignore；
			// 其它处理方式（例如登记为待归类）没有对应表达。
			Reason: fmt.Sprintf("扫描器对不匹配作品 glob 的目录一律不产生作品，不支持 %q", value),
		})
	}
	if platform.Structure.AllowMediaInWorkChildren {
		notes = append(notes, Note{
			Platform: platform.ID, Field: "structure.allow_media_in_work_children",
			// 扫描器只读取作品目录的直接子项，子目录一律跳过，因此 false 是等价承接、true 不是。
			Reason: "扫描器只把作品目录的直接子文件作为媒体，不下降到子目录，无法表达「子目录中的媒体也算」",
		})
	}
	if platform.Structure.Author.KeySource == "path_only" {
		notes = append(notes, Note{
			Platform: platform.ID, Field: "structure.author.key_source",
			Reason: "作者身份只能从路径段取；当前没有路径取值原语，该平台的作品将没有创作者",
		})
	}
	return notes
}

// legacyTransforms 是旧配置的取值归一化方式与 Gallery 承接结论的对照表。
// 值为空串表示 Gallery 有等价行为、无需登记；非空串是必须登记的原因。
var legacyTransforms = map[string]map[string]string{
	"title": {
		"display_text": "Gallery 逐 code point 保留来源文本、不做展示型归一化，标题文本可能与旧工具不同",
	},
	"description": {
		"display_text": "Gallery 逐 code point 保留来源文本、不做展示型归一化，描述文本可能与旧工具不同",
	},
	"tags": {
		// 这一条与其它几条不同：它改变的是标签**数量**，不只是文本外观。
		"array_or_brace_list": "Gallery 只把 JSON 数组形态识别为多个标签；花括号列表形态的字符串会成为**一个**标签，" +
			"标签数量、按标签的搜索与按标签命中的角标都会因此不同",
	},
	"date": {
		// work_date 原语本身就同时支持 metadata 时间戳与路径日期，等价承接。
		"iso_or_path_datetime": "",
	},
}

// transformNotes 逐字段登记无法承接的取值归一化。未知的归一化方式一律登记：把没见过的变换当作
// 恒等映射，等于默认「它什么也没做」，而这恰恰是最可能产生静默差异的假设。
func transformNotes(platform Platform) []Note {
	notes := []Note{}
	for _, item := range []struct{ field, value string }{
		{"title", platform.Metadata.Transforms.Title},
		{"description", platform.Metadata.Transforms.Description},
		{"tags", platform.Metadata.Transforms.Tags},
		{"date", platform.Metadata.Transforms.Date},
	} {
		if item.value == "" {
			continue
		}
		reason, known := legacyTransforms[item.field][item.value]
		if known && reason == "" {
			continue
		}
		if !known {
			reason = fmt.Sprintf("未知的取值归一化方式 %q，未转换", item.value)
		}
		notes = append(notes, Note{
			Platform: platform.ID, Field: "metadata.transforms." + item.field, Reason: reason,
		})
	}
	return notes
}

// mediaHideReason 给出一条条件隐藏规则未转换的**具体**原因。两类条件的差别是实质性的：
//
//   - 兄弟文件条件需要「同目录其它文件的扩展名集合」。求值时 CEL 的环境变量只有
//     source/path/file/metadata/candidate/params，其中 file 只有当前文件的 path/size/metadata，
//     没有任何形式的目录清单。这不是写法问题，是输入本身不存在，必须先扩展求值上下文。
//   - metadata 文本条件只需要作品 metadata 与当前文件名，二者 CEL 都拿得到，
//     `condition{scope: "media", effect: "hide"}` 加一条 CEL 谓词即可承接——它是可实现的。
//     本轮没有实现，因为忠实移植需要知道被匹配取值的实际形态（字符串还是字符串数组）：CEL 谓词
//     求值出错会中断整个 Source 的求值，而取值形态只能从真实来源的 metadata 观察得到，本轮不在
//     允许触碰的范围内。凭猜测写一个可能在真实数据上抛错的谓词，比暂不实现更糟。
func mediaHideReason(rule MediaHideRule) string {
	switch {
	case rule.When.Files != nil && len(rule.When.Files.Extensions) > 0:
		return "条件依赖同目录兄弟文件的扩展名集合；CEL 上下文只提供当前文件的 path/size/metadata，" +
			"没有目录清单这一输入，需要先扩展求值上下文或新增原语"
	case rule.When.TextRegex != "":
		return "条件只依赖作品 metadata 与文件名，CEL 上下文均已提供，可由 condition(scope=media, effect=hide) " +
			"加一条 CEL 谓词承接；本轮未实现——忠实移植需要先观察被匹配取值的实际形态，否则谓词可能在" +
			"真实数据上求值出错并中断整个 Source"
	default:
		return "未识别的条件隐藏形态，未转换"
	}
}

// hasCandidateCondition 判断封面候选是否带有条件。保留原文判断而不是解析成结构体，是为了让
// **任何**形态的条件（包括本转换器不认识的新形态）都落到「条件无法表达 → 候选不发出」这一侧。
func hasCandidateCondition(when json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(when))
	return trimmed != "" && trimmed != "null" && trimmed != "{}"
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

// firstNonEmpty 返回第一个非空串，用于「平台级 → 库级 → 内置默认」的逐级回退。
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
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
