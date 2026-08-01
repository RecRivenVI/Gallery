package rules

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// WorkDateParserVersion 是作品发布时间解析规则的身份。
//
// [查询、搜索与排序](../../docs/architecture/query-search-and-sorting.md) 要求时间排序「必须先解析为明确
// instant，并保留原始时间和解析规则版本」。保留版本的意义是：解析规则一旦变化，历史投影里由旧规则
// 得到的 instant 就不再等价于新规则的结果，必须能被识别出来并按需重扫，而不是让两代解析结果在同一
// 个排序里静默混用。修改任何解析行为都必须递增本常量。
const WorkDateParserVersion = "gallery-work-date-v1"

// WorkDate 是规则解析出的作品发布时间事实。
//
// 三个字段缺一不可：Instant 供排序与聚合使用；Raw 保留来源原文，使「解析是否正确」永远可以被人工
// 复核，也让未来换解析规则时不必回到 Source 重读；ParserVersion 标识产生该 Instant 的规则版本。
type WorkDate struct {
	// Instant 是解析后的 UTC 时刻。零值表示未解析出时间。
	Instant time.Time
	// Raw 是产生该时刻的原始输入（metadata 原值或路径片段），逐字保留。
	Raw string
	// Source 说明该时刻来自哪个 JSON Pointer，或来自路径（`$path`）。
	Source string
	// ParserVersion 是产生该 Instant 的解析规则版本。
	ParserVersion string
}

// HasInstant 报告是否真正解析出了时刻。
func (d WorkDate) HasInstant() bool { return !d.Instant.IsZero() }

// workDateConfig 是 `work_date` 原语的配置。
//
// 设计取向：**不硬编码任何目录命名约定**。真实规则用 `$path.datetime` 表达「metadata 没有日期时从
// 目录名里取」，但各来源的目录命名互不相同；若把某一种约定写进扫描器，就等于在业务代码里增加了按
// 来源的特例分支，违反「规则是 Source 差异的唯一解释入口」。因此路径日期由规则自己声明一个受限的
// 命名捕获模式，扫描器只负责执行。
type workDateConfig struct {
	// Pointers 是 metadata JSON Pointer 回退链，按顺序取第一个能解析出时刻的。
	Pointers []string `json:"pointers,omitempty"`
	// PathPattern 是作用于作品相对路径的受限正则，用命名捕获组提供日期分量。
	// 支持的组名：year、month、day、hour、minute、second。year 与 month、day 必需。
	PathPattern string `json:"path_pattern,omitempty"`
	// InputTimezone 是 **metadata 中朴素时间戳**（不带偏移量的输入）的解释时区，IANA 名称。
	// 缺省为 UTC。带明确偏移量的输入不受它影响。
	InputTimezone string `json:"input_timezone,omitempty"`
	// PathTimezone 是 **PathPattern 从目录名捕获的日期分量**的解释时区，IANA 名称。缺省沿用
	// InputTimezone。
	//
	// 它必须与 InputTimezone 分开，因为二者是**来源不同的两类朴素时间戳**：metadata 里的时间戳由
	// 抓取工具按它自己的约定写入，目录名里的时间戳由归档工具按**另一套**约定命名，二者用同一个时区
	// 假设纯属巧合。一旦二者不同而实现只有一个时区，错误是静默的——仍然产出一个格式合法、排序正常、
	// 只是偏移了若干小时的时刻，没有任何 issue 或告警能暴露它。
	PathTimezone string `json:"path_timezone,omitempty"`
}

// workDatePatternLimit 与 CEL Profile 的 RegexCharacters 采用同一上限：路径模式同样是规则包提供的
// 不可信输入，不能因为它走的是原语而不是 CEL 就放宽长度约束。
const workDatePatternLimit = 512

var workDateGroupNames = map[string]struct{}{
	"year": {}, "month": {}, "day": {}, "hour": {}, "minute": {}, "second": {},
}

// compileWorkDate 校验并编译 `work_date` 配置。校验在编译期完成，使非法时区、非法正则和无效捕获组
// 在规则发布时就被拒绝，而不是在扫描时逐作品失败。
func compileWorkDate(config workDateConfig, primitiveID string) (IRWorkDate, error) {
	result := IRWorkDate{
		Pointers:      append([]string(nil), config.Pointers...),
		PathPattern:   config.PathPattern,
		InputTimezone: config.InputTimezone,
		PathTimezone:  config.PathTimezone,
	}
	if len(config.Pointers) == 0 && config.PathPattern == "" {
		return IRWorkDate{}, fmt.Errorf("work_date %s 既没有 pointers 也没有 path_pattern", primitiveID)
	}
	for _, pointer := range config.Pointers {
		if pointer == "" || !strings.HasPrefix(pointer, "/") {
			return IRWorkDate{}, fmt.Errorf("work_date %s 的 pointer %q 不是 JSON Pointer", primitiveID, pointer)
		}
	}
	if config.PathPattern != "" {
		if len(config.PathPattern) > workDatePatternLimit {
			return IRWorkDate{}, fmt.Errorf("work_date %s 的 path_pattern 超过 %d 字符上限", primitiveID, workDatePatternLimit)
		}
		expression, err := regexp.Compile(config.PathPattern)
		if err != nil {
			return IRWorkDate{}, fmt.Errorf("work_date %s 的 path_pattern 无效: %w", primitiveID, err)
		}
		named := 0
		for _, name := range expression.SubexpNames() {
			if name == "" {
				continue
			}
			if _, ok := workDateGroupNames[name]; !ok {
				return IRWorkDate{}, fmt.Errorf("work_date %s 的 path_pattern 含不受支持的捕获组 %q", primitiveID, name)
			}
			named++
		}
		if named == 0 {
			return IRWorkDate{}, fmt.Errorf("work_date %s 的 path_pattern 没有任何日期命名捕获组", primitiveID)
		}
		if !hasGroup(expression, "year") || !hasGroup(expression, "month") || !hasGroup(expression, "day") {
			return IRWorkDate{}, fmt.Errorf("work_date %s 的 path_pattern 必须至少捕获 year、month、day", primitiveID)
		}
	}
	if config.InputTimezone != "" {
		if _, err := time.LoadLocation(config.InputTimezone); err != nil {
			return IRWorkDate{}, fmt.Errorf("work_date %s 的 input_timezone %q 不是有效 IANA 时区: %w", primitiveID, config.InputTimezone, err)
		}
	}
	if config.PathTimezone != "" {
		if config.PathPattern == "" {
			return IRWorkDate{}, fmt.Errorf("work_date %s 声明了 path_timezone 却没有 path_pattern", primitiveID)
		}
		if _, err := time.LoadLocation(config.PathTimezone); err != nil {
			return IRWorkDate{}, fmt.Errorf("work_date %s 的 path_timezone %q 不是有效 IANA 时区: %w", primitiveID, config.PathTimezone, err)
		}
	}
	return result, nil
}

// loadTimezone 加载 IANA 时区；空串表示 UTC。字段名进错误信息，使「两个时区里哪一个配错了」可辨。
func loadTimezone(name, field string) (*time.Location, error) {
	if name == "" {
		return time.UTC, nil
	}
	loaded, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("加载 %s: %w", field, err)
	}
	return loaded, nil
}

func hasGroup(expression *regexp.Regexp, name string) bool {
	for _, candidate := range expression.SubexpNames() {
		if candidate == name {
			return true
		}
	}
	return false
}

// resolveWorkDate 按「metadata pointer 链 → 路径模式」的顺序解析作品发布时间。
//
// 顺序不可颠倒：metadata 是来源自己声明的事实，路径只是回退推断。任一 pointer 解析成功即停止，
// 与真实规则中 `date` 的回退链语义一致。
func resolveWorkDate(plan IRWorkDate, metadata any, workPath string) (WorkDate, error) {
	location, err := loadTimezone(plan.InputTimezone, "input_timezone")
	if err != nil {
		return WorkDate{}, err
	}
	// 路径日期用**独立**时区解释：目录命名约定与 metadata 的时间戳约定是两套互不相关的约定。
	// 未声明时沿用 input_timezone，因为「只有一种朴素时间约定」是常见情形，强制两处都写反而更易写错。
	pathLocation := location
	if plan.PathTimezone != "" {
		pathLocation, err = loadTimezone(plan.PathTimezone, "path_timezone")
		if err != nil {
			return WorkDate{}, err
		}
	}
	for _, pointer := range plan.Pointers {
		value, ok := resolvePointer(metadata, pointer)
		if !ok || value == nil {
			continue
		}
		raw := strings.TrimSpace(fmt.Sprint(value))
		if raw == "" {
			continue
		}
		instant, ok := parseTimestamp(raw, location)
		if !ok {
			continue
		}
		return WorkDate{Instant: instant.UTC(), Raw: raw, Source: pointer, ParserVersion: WorkDateParserVersion}, nil
	}
	if plan.PathPattern == "" {
		return WorkDate{}, nil
	}
	expression, compileErr := regexp.Compile(plan.PathPattern)
	if compileErr != nil {
		return WorkDate{}, fmt.Errorf("编译 path_pattern: %w", compileErr)
	}
	match := expression.FindStringSubmatch(workPath)
	if match == nil {
		return WorkDate{}, nil
	}
	parts := map[string]int{"hour": 0, "minute": 0, "second": 0}
	for index, name := range expression.SubexpNames() {
		if name == "" || index >= len(match) {
			continue
		}
		number, convErr := strconv.Atoi(match[index])
		if convErr != nil {
			return WorkDate{}, nil
		}
		parts[name] = number
	}
	instant := time.Date(parts["year"], time.Month(parts["month"]), parts["day"],
		parts["hour"], parts["minute"], parts["second"], 0, pathLocation)
	// time.Date 会把越界分量规范化（例如 13 月变成次年 1 月）。日期来自不可信输入，因此这里回读
	// 校验：规范化后与输入不一致说明输入本身非法，按未解析处理，不产生一个看似合理的错误时刻。
	if instant.Year() != parts["year"] || int(instant.Month()) != parts["month"] || instant.Day() != parts["day"] ||
		instant.Hour() != parts["hour"] || instant.Minute() != parts["minute"] || instant.Second() != parts["second"] {
		return WorkDate{}, nil
	}
	return WorkDate{Instant: instant.UTC(), Raw: match[0], Source: "$path", ParserVersion: WorkDateParserVersion}, nil
}

// timestampLayouts 是 metadata 时间戳接受的格式，按从最明确到最宽松排列。
//
// 带偏移量的格式先于朴素格式：只有真正没有偏移量的输入才落到 input_timezone 解释，避免把一个已经
// 明确的时刻按配置时区二次平移。
var timestampLayouts = []struct {
	layout string
	naive  bool
}{
	{time.RFC3339Nano, false},
	{time.RFC3339, false},
	{"2006-01-02T15:04:05Z0700", false},
	{"2006-01-02 15:04:05Z0700", false},
	{"2006-01-02T15:04:05.999999999", true},
	{"2006-01-02T15:04:05", true},
	{"2006-01-02 15:04:05", true},
	{"2006-01-02 15:04", true},
	{"2006-01-02", true},
}

// parseTimestamp 解析 metadata 中的时间戳。朴素输入按 location 解释，带偏移量的输入保留其偏移量。
func parseTimestamp(raw string, location *time.Location) (time.Time, bool) {
	for _, candidate := range timestampLayouts {
		if candidate.naive {
			if parsed, err := time.ParseInLocation(candidate.layout, raw, location); err == nil {
				return parsed, true
			}
			continue
		}
		if parsed, err := time.Parse(candidate.layout, raw); err == nil {
			return parsed, true
		}
	}
	// Unix 秒：部分来源直接给出整数时间戳。它是绝对时刻，不受 input_timezone 影响。
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds > 0 {
		return time.Unix(seconds, 0).UTC(), true
	}
	return time.Time{}, false
}
