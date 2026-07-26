package rules

import (
	"fmt"
	"regexp"
	"sort"
)

// pathCaptureConfig 是 `path_capture` 原语的配置。
//
// 它补上规则系统此前**完全不存在**的一项能力：按路径段取值。
//
// 真实规则用 `$path.author` 表达「metadata 没有作者时，用作者目录名当作者」，这是多个来源的常规
// 组织方式。此前 Gallery 没有任何路径取值入口：`path_match` 的配置是封闭集合且没有命名捕获，
// `fallback`/`stable_key` 只接受 metadata JSON Pointer，CEL 虽然拿得到 `path` 但只能返回布尔值。
// 后果在真实数据上已经实测到——某个平台的作者只存在于目录名里，于是它的全部作品**没有任何创作者**。
//
// 设计取向与 `work_date.path_pattern` 一致：**不硬编码任何目录命名约定**。由规则自己声明一个受限的
// 命名捕获模式，扫描器只负责执行，使「来源差异只由规则解释」这条边界继续成立。
type pathCaptureConfig struct {
	// Pattern 是作用于作品相对路径（Source 根相对、正斜杠分隔）的正则，用命名捕获组提供取值。
	Pattern string `json:"pattern"`
	// Targets 把**可赋值字段**映射到它的来源捕获组，方向是「字段 ← 组」。
	//
	// 方向不可颠倒：一个字段只有一个来源，但**一个组可以喂多个字段**——真实规则里确实存在同时把
	// 作者段用作创作者和标题的平台（该平台的「作者」其实是书名）。若按「组 → 字段」建映射，
	// 这种平台的两个目标会共用同一个键而互相覆盖，静默丢掉其中一个。
	Targets map[string]string `json:"targets"`
}

// pathCapturePatternLimit 与 CEL Profile 的 RegexCharacters 及 work_date 的上限一致：路径模式同样是
// 规则包提供的不可信输入，不因为它走原语而不是 CEL 就放宽长度约束。
const pathCapturePatternLimit = 512

// compilePathCapture 校验并编译 `path_capture` 配置。
//
// 全部校验放在编译期：非法正则、未声明的捕获组、不可赋值的目标字段都在规则发布时被拒绝，而不是
// 在扫描时逐作品失败——后者会让一个打错字的规则把整个来源的扫描拖垮。
func compilePathCapture(config pathCaptureConfig, primitiveID string) (IRPathCapture, error) {
	if config.Pattern == "" {
		return IRPathCapture{}, fmt.Errorf("path_capture %s 缺少 pattern", primitiveID)
	}
	if len(config.Pattern) > pathCapturePatternLimit {
		return IRPathCapture{}, fmt.Errorf("path_capture %s 的 pattern 超过 %d 字符上限", primitiveID, pathCapturePatternLimit)
	}
	if len(config.Targets) == 0 {
		return IRPathCapture{}, fmt.Errorf("path_capture %s 没有声明任何 targets", primitiveID)
	}
	expression, err := regexp.Compile(config.Pattern)
	if err != nil {
		return IRPathCapture{}, fmt.Errorf("path_capture %s 的 pattern 无效: %w", primitiveID, err)
	}
	groups := map[string]struct{}{}
	for _, name := range expression.SubexpNames() {
		if name != "" {
			groups[name] = struct{}{}
		}
	}
	if len(groups) == 0 {
		return IRPathCapture{}, fmt.Errorf("path_capture %s 的 pattern 没有任何命名捕获组", primitiveID)
	}
	targets := make(map[string]string, len(config.Targets))
	for target, group := range config.Targets {
		if _, ok := groups[group]; !ok {
			return IRPathCapture{}, fmt.Errorf("path_capture %s 的目标字段 %q 引用了模式里不存在的捕获组 %q", primitiveID, target, group)
		}
		if _, ok := assignableTargets[target]; !ok {
			return IRPathCapture{}, fmt.Errorf("path_capture %s 的目标字段 %q 不受支持", primitiveID, target)
		}
		// date 由 work_date 产出（需要 instant 与解析器版本），路径取值给不出这两样。
		if target == "date" {
			return IRPathCapture{}, fmt.Errorf("path_capture %s 不能赋值 date，作品发布时间只能由 work_date 产出", primitiveID)
		}
		// tags 是列表，「只填空」的语义对它没有意义，且路径段是单值。
		if target == "tags" {
			return IRPathCapture{}, fmt.Errorf("path_capture %s 不能赋值 tags：路径段是单值，标签是列表", primitiveID)
		}
		targets[target] = group
	}
	return IRPathCapture{Pattern: config.Pattern, Targets: targets}, nil
}

// applyPathCapture 按路径模式取值，**只填充当前为空的字段**。
//
// 「只填空」使它无论排在 metadata 取值链之前还是之后，结果都一样：metadata 有值时路径取值不覆盖它，
// metadata 没值时路径取值补上。这正是真实规则里 `$path.*` 永远位于回退链末位的语义，而把它做成与
// 原语顺序无关，可以避免「规则作者调换了两条原语的位置，作者名就悄悄变了」这类难以察觉的问题。
func applyPathCapture(plan IRPathCapture, workPath string, result *DryRunResult) error {
	expression, err := regexp.Compile(plan.Pattern)
	if err != nil {
		return fmt.Errorf("编译 path_capture pattern: %w", err)
	}
	match := expression.FindStringSubmatch(workPath)
	if match == nil {
		result.Issues = append(result.Issues, RuleIssue{Code: "RULE_PATH_CAPTURE_MISSING"})
		return nil
	}
	captured := make(map[string]string, len(match))
	for index, name := range expression.SubexpNames() {
		if name != "" && index < len(match) {
			captured[name] = match[index]
		}
	}
	// 按目标字段名排序遍历，使多目标时的执行顺序确定；各目标互不影响，排序只为可复现。
	targetNames := make([]string, 0, len(plan.Targets))
	for target := range plan.Targets {
		targetNames = append(targetNames, target)
	}
	sort.Strings(targetNames)
	for _, target := range targetNames {
		value := captured[plan.Targets[target]]
		if value == "" || currentTarget(&result.Work, target) != "" {
			continue
		}
		assignTarget(&result.Work, target, value)
	}
	return nil
}

// currentTarget 读取可赋值字段的当前值，是 assignTarget 的读侧对应物。
//
// 分支集合必须与 assignTarget 保持一致；缺一个分支会让该字段被误判为「空」而被路径取值覆盖，
// 那正是本原语刻意要避免的行为。tags 不参与：它是列表，不属于「填空」语义。
func currentTarget(work *DryRunWork, target string) string {
	switch target {
	case "title":
		return work.Title
	case "external_id":
		return work.ExternalID
	case "provider_id":
		return work.ProviderID
	case "creator":
		return work.Creator
	case "description":
		return work.Description
	case "source_url":
		return work.SourceURL
	}
	return ""
}
