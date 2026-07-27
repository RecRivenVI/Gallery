// Package report 定义 tools/testlab 全部阶段共用的机器可读结果模型：Finding、
// LatencySample 统计恒等式与 Report 的脱敏、原子持久化。任何阶段（stage3/stage4/
// 未来阶段）的 orchestrator 都只依赖本包，不重复实现各自的结果结构。
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/RecRivenVI/gallery/tools/testlab/internal/hostfacts"
)

// Finding 是单条断言结果：name 描述被验证的具体行为，pass 是断言结论，detail 是
// 失败时的诊断信息。detail 必须经过 sanitizeDetail 脱敏，不得包含真实媒体路径、
// 监听地址或 secret；调用方全程只操作合成数据或授权的真实 Source 有界子集。
type Finding struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

// 缓存状态取值。它们描述的是**本次实际测到的进程状态**，不是人工标注，也不是
// 「大概是热的」这种默认假设：
//
//   - CacheStateColdProcess：本组合的第一次测量请求由一个自启动以来从未服务过任何
//     查询请求的 galleryd 进程处理（进程内 SQLite 连接池、页缓存、prepared statement
//     缓存与查询服务缓存全为空）。
//   - CacheStateWarm：本组合在测量前执行了显式 warmup，且至少有一次 warmup 请求成功。
//   - CacheStateWarmIncidental：本组合没有 warmup，但同一进程已经服务过前面组合的
//     请求，因此缓存被前序组合偶然预热。这是本工具在 2026-07-27 之前实际处于的状态，
//     当时却被硬编码标注为 warm。
//   - CacheStateUnknown：无法判定（例如要求 warmup 但全部 warmup 请求失败）。
//
// **这四个取值都不表示存储介质冷读**：清空操作系统文件系统缓存需要管理员权限与平台
// 专有接口，本工具不做，因此 cold-process 只覆盖进程内缓存这一层，必须按此解读。
const (
	CacheStateColdProcess    = "cold-process"
	CacheStateWarm           = "warm"
	CacheStateWarmIncidental = "warm-incidental"
	CacheStateUnknown        = "unknown"
)

// MinSamplesForP99 是「P99 可估」的最小成功样本数。最近秩分位数在 n 个样本上能表达的
// 最细分位间隔是 1/n，因此 n=30 时最高可分辨的分位点是 96.7%，此时 ceil(0.99·30)=30
// 直接落到最大值——报出来的"P99"其实就是 max，不是 P99 的估计。要让 P99 落在最大值
// 之前至少需要 n≥100。
const MinSamplesForP99 = 100

// LatencySample 汇总一个查询类别在给定 limit/concurrency 下的重复测量分位数与
// 运行统计恒等式。四类运行计数必须始终满足：
//
//	SuccessfulRuns + FailedRuns       == AttemptedRuns
//	AttemptedRuns  + NotAttemptedRuns == PlannedRuns
//
// TimedOutRuns 是 FailedRuns 的一个子集（因请求自身超时而失败），不是独立计入的
// 第五类；调用方不得把 NotAttemptedRuns（组合截止时间已过、从未派发）静默折叠进
// FailedRuns，否则会破坏第一条恒等式，把"部分组合未完整执行"误报为"全部已尝试
// 的请求都成功"。
//
// WarmupRuns/WarmupFailedRuns 独立于上面四类计数：warmup 请求既不进入 PlannedRuns
// 也不进入 Durations，否则第一批本来就偏慢的样本会污染分位数。
type LatencySample struct {
	Category    string `json:"category"`
	Limit       int    `json:"limit"`
	Concurrency int    `json:"concurrency"`

	PlannedRuns      int `json:"plannedRuns"`
	AttemptedRuns    int `json:"attemptedRuns"`
	SuccessfulRuns   int `json:"successfulRuns"`
	FailedRuns       int `json:"failedRuns"`
	TimedOutRuns     int `json:"timedOutRuns"`
	NotAttemptedRuns int `json:"notAttemptedRuns"`

	WarmupRuns       int `json:"warmupRuns"`
	WarmupFailedRuns int `json:"warmupFailedRuns"`

	CacheState string  `json:"cacheState"`
	P50Ms      float64 `json:"p50Ms"`
	P95Ms      float64 `json:"p95Ms"`
	P99Ms      float64 `json:"p99Ms"`
	MinMs      float64 `json:"minMs"`
	MaxMs      float64 `json:"maxMs"`

	// PercentileMethod 固定为 nearest-rank，写进每条样本使读者不必猜测这批数字用的是
	// 哪种分位数定义；历史结果文件里没有这个字段，即为旧口径。
	PercentileMethod string `json:"percentileMethod"`
	// CarriesP99 表示本组合被矩阵指定为承载 P99 的组合，因此样本量不足是一条失败。
	CarriesP99 bool `json:"carriesP99"`
	// P99Estimable 报告 SuccessfulRuns 是否达到 MinSamplesForP99；为 false 时 P99Ms
	// 只是"最近秩落在最大值上"的结果，不能当作 P99 估计使用。
	P99Estimable bool `json:"p99Estimable"`

	HitCount   int    `json:"hitCount"`
	TotalMode  string `json:"totalMode"`
	TotalValue int    `json:"totalValue"`
}

// IdentityOK 报告本样本的运行计数是否满足统计恒等式；调用方应在写入报告前检查，
// 并在违反时产生一条独立的失败 Finding，而不是静默发布不一致的统计。
func (s LatencySample) IdentityOK() bool {
	return s.SuccessfulRuns+s.FailedRuns == s.AttemptedRuns &&
		s.AttemptedRuns+s.NotAttemptedRuns == s.PlannedRuns
}

// Report 是一次 testlab 阶段运行的完整机器可读结果。字段只保留可以安全提交或
// 对外展示的脱敏摘要：不含 AppDirs 绝对路径、监听地址/端口、真实 Source 路径、
// token、Cookie、CSRF 或 correlationId。运行期间需要的原始诊断信息只写入
// 本地日志文件（由调用方放在授权测试根的 logs/ 目录内），不进入本结构。
type Report struct {
	SchemaVersion int    `json:"schemaVersion"`
	GeneratedAt   string `json:"generatedAt"`
	Scenario      string `json:"scenario"`
	ScenarioAlias string `json:"scenarioAlias,omitempty"`
	SourceAlias   string `json:"sourceAlias,omitempty"`
	StorageClass  string `json:"storageClass,omitempty"`
	Tier          string `json:"tier,omitempty"`
	// Environment 是本次运行**实测**的环境事实（CPU/内存/OS/SQLite 版本/物理盘介质
	// 与型号）。Reference Performance Gate 要求每次结果同时记录这些项，缺任一项时结果
	// 只能作为方向性证据；StorageClass 是人工标注，只用于与 Environment.Storage 交叉
	// 核对，不得覆盖实测结论。
	Environment *hostfacts.Facts `json:"environment,omitempty"`
	// Corpus 描述本次被测语料的结构性事实（例如 Source 数量），使读者能判断哪些发布
	// 路径确实被覆盖过。
	Corpus         *CorpusFacts    `json:"corpus,omitempty"`
	Transport      string          `json:"transport"`
	Scale          int             `json:"scale,omitempty"`
	Nonrecommended bool            `json:"nonrecommendedScale,omitempty"`
	Findings       []Finding       `json:"findings"`
	Latencies      []LatencySample `json:"latencies,omitempty"`
	Limitations    []string        `json:"limitations,omitempty"`
	FailureCount   int             `json:"failureCount"`

	// 以下字段只对有超时/分批语义的场景（目前是 perf）有意义；其它场景保持零值。
	StartedAt             string `json:"startedAt,omitempty"`
	FinishedAt            string `json:"finishedAt,omitempty"`
	PlannedCombinations   int    `json:"plannedCombinations,omitempty"`
	CompletedCombinations int    `json:"completedCombinations,omitempty"`
	AbortedByTimeLimit    bool   `json:"abortedByTimeLimit,omitempty"`
	AbortReason           string `json:"abortReason,omitempty"`
}

// CorpusFacts 记录本次被测语料的结构性事实。SourceCount 尤其重要：单 Source 语料
// 永远不会执行 catalog.cloneUnchangedSources 的全量搬运语句（那 12 条语句的 WHERE
// 条件是 `source_id<>?`），因此单 Source 的发布耗时不能代表"多 Source 环境下重扫其中
// 一个 Source"的真实发布代价——后者要按比例复制其余全部 Source 的投影与 FTS5 索引。
type CorpusFacts struct {
	Scale       int `json:"scale"`
	SourceCount int `json:"sourceCount"`
	// ClonedSourceCountOnLastPublish 是最后一次 BeginCandidate 时被 cloneUnchangedSources
	// 搬运的 Source 数量；为 0 表示这条路径在本次语料构建中一次都没有执行。
	ClonedSourceCountOnLastPublish int `json:"clonedSourceCountOnLastPublish"`
	// SourceBeginDurationsMs 是逐个 Source 的 BeginCandidate 耗时，即 cloneUnchangedSources
	// 的实际代价（第一个 Source 没有可搬运的对象，因此是该路径的空载基线）。
	SourceBeginDurationsMs []int64 `json:"sourceBeginDurationsMs,omitempty"`
	// SourceValidationDurationsMs 是逐个 Source 的完整候选验证耗时，不属于短 publication 事务。
	SourceValidationDurationsMs []int64 `json:"sourceValidationDurationsMs,omitempty"`
	// SourcePublishDurationsMs 是逐个 Source 的 Publish 耗时。
	SourcePublishDurationsMs []int64 `json:"sourcePublishDurationsMs,omitempty"`
}

func (r *Report) Add(name string, pass bool, detail string) {
	r.Findings = append(r.Findings, Finding{Name: name, Pass: pass, Detail: sanitizeDetail(detail)})
	if !pass {
		r.FailureCount++
	}
}

// sensitiveDetailPatterns 匹配绝对路径、监听地址和 URL 形式的诊断文本片段。
var sensitiveDetailPatterns = []*regexp.Regexp{
	regexp.MustCompile(`[A-Za-z]:\\`),
	regexp.MustCompile(`\\\\[^\s"]+`),
	regexp.MustCompile(`https?://\S+`),
	regexp.MustCompile(`127\.0\.0\.1:\d+`),
	regexp.MustCompile(`localhost:\d+`),
	regexp.MustCompile(`(?i)0\.0\.0\.0:\d+`),
}

// sanitizeDetail 把诊断文本中可能出现的绝对路径/监听地址/URL 替换为占位符，同时
// 保留错误分类、状态码和稳定错误码等对诊断仍有价值的信息。
func sanitizeDetail(detail string) string {
	for _, pattern := range sensitiveDetailPatterns {
		detail = pattern.ReplaceAllString(detail, "[redacted]")
	}
	return detail
}

// containsSensitiveMarker 报告一段文本是否仍然包含疑似绝对路径、URL 或监听地址，
// 用于 Save 前的最终防线：即便某处遗漏调用 sanitizeDetail，也不允许把这些内容
// 写入可能被提交或对外展示的结果文件。
func containsSensitiveMarker(text string) bool {
	markers := []string{`:\`, `\\`, "http://", "https://", "127.0.0.1:", "localhost:"}
	lower := strings.ToLower(text)
	for _, marker := range markers {
		if strings.Contains(lower, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

// scanForSensitiveContent 遍历 Report 的全部文本字段，拒绝把绝对路径、URL 或
// 监听地址写入结果文件；发现时返回具体命中的字段名，不吞掉错误静默继续。
func (r *Report) scanForSensitiveContent() error {
	if containsSensitiveMarker(r.ScenarioAlias) || containsSensitiveMarker(r.SourceAlias) || containsSensitiveMarker(r.StorageClass) {
		return fmt.Errorf("report 顶层别名字段疑似包含绝对路径或地址")
	}
	for _, finding := range r.Findings {
		if containsSensitiveMarker(finding.Detail) || containsSensitiveMarker(finding.Name) {
			return fmt.Errorf("finding %q 的内容疑似包含绝对路径或地址", finding.Name)
		}
	}
	for _, limitation := range r.Limitations {
		if containsSensitiveMarker(limitation) {
			return fmt.Errorf("limitation 文本疑似包含绝对路径或地址: %s", limitation)
		}
	}
	// 环境事实由物理盘/卷/设备描述符采集，天然接触大量路径形态的字符串，必须同样
	// 经过这道防线，不能因为"它是自动采集的"就默认安全。
	if r.Environment != nil {
		facts := *r.Environment
		texts := append([]string{
			facts.OSVersion, facts.CPUModel, facts.SQLiteVersion, facts.SQLiteLibrary, facts.GoVersion,
			facts.Storage.Model, facts.Storage.BusType, facts.Storage.MediumEvidence,
			facts.Storage.VolumeID, facts.Storage.LogicalDrive, facts.Storage.PhysicalDrive,
		}, facts.Errors...)
		texts = append(texts, facts.Storage.Errors...)
		for _, text := range texts {
			if containsSensitiveMarker(text) {
				return fmt.Errorf("environment 字段疑似包含绝对路径或地址: %s", text)
			}
		}
	}
	return nil
}

// Save 原子写出结果文件：先写临时文件、fsync、关闭，再 rename，避免正在轮询
// partial report 的读者（例如长时间运行的性能矩阵每完成一个组合就调用一次 Save）
// 读到半写状态，也避免进程在 rename 之前崩溃时临时文件停留在只被 OS 缓存、尚未
// 落盘的状态。
func (r *Report) Save(path string) error {
	r.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	if r.Transport == "" {
		r.Transport = "loopback-http"
	}
	if err := r.scanForSensitiveContent(); err != nil {
		return fmt.Errorf("拒绝写入结果文件，疑似泄露敏感内容: %w", err)
	}
	encoded, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tempPath := path + ".tmp"
	f, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(encoded); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

// PercentileMethodNearestRank 是本工具自 2026-07-27 起使用的分位数定义标识，写入每条
// LatencySample，使读者能一眼区分新旧口径的数字。
const PercentileMethodNearestRank = "nearest-rank"

// 分位点以整数千分比表达，见 percentileNearestRank 中关于浮点误差的说明。
const (
	perMilleP50 = 500
	perMilleP95 = 950
	perMilleP99 = 990
)

// percentileNearestRank 按标准最近秩（nearest-rank）定义取分位数：秩 = ceil(p·n)，
// 返回升序样本中第「秩」个（1 基）值。
//
// 此前的实现是 `int(p·n) - 1`（即 floor(p·n) 再减一，0 基），比标准最近秩整整低一名：
// n=30 时 P95 取到第 28 个样本（经验分位 93.3%）、P99 取到第 29 个（96.7%），系统性
// 低估尾延迟。修正后同一批原始样本的 P95/P99 只会等于或高于旧口径，因此**新旧数字
// 不可直接比较**，调用方必须在报告的 Limitations 中写明这一点。
//
// 分位点用整数千分比而不是 float64 字面量：0.95 在二进制下不可精确表示，
// `math.Ceil(0.95*20)` 会因为乘积是 19.000000000000004 而返回 20，凭空多抬一名。
// 整数运算 (perMille*n + 999) / 1000 就是精确的 ceil(p·n)。
func percentileNearestRank(sortedMs []float64, perMille int) float64 {
	n := len(sortedMs)
	if n == 0 {
		return 0
	}
	rank := (perMille*n + 999) / 1000
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return sortedMs[rank-1]
}

// Measurement 是一个 (类别,limit,并发) 组合的原始测量输入。用结构体而不是继续增加
// 位置参数：这些字段之间没有自然顺序，位置参数一旦超过十来个，调用点插错一列不会有
// 任何编译错误，而这里错一列就是一份看起来正常、实际错误的性能报告。
type Measurement struct {
	Category    string
	Limit       int
	Concurrency int

	// PlannedRuns/AttemptedRuns/FailedRuns/TimedOutRuns/NotAttemptedRuns 的语义见
	// LatencySample 的统计恒等式说明；Durations 只包含成功请求的耗时。
	PlannedRuns      int
	AttemptedRuns    int
	FailedRuns       int
	TimedOutRuns     int
	NotAttemptedRuns int
	Durations        []time.Duration

	// WarmupRuns/WarmupFailedRuns 是显式预热阶段的计数，不进入上面任何一类，也不进入
	// Durations。
	WarmupRuns       int
	WarmupFailedRuns int

	// CacheState 必须是本次实测到的状态（见 CacheState* 常量），不得填写字面量猜测。
	CacheState string
	// CarriesP99 表示本组合被矩阵指定为承载 P99 的组合。
	CarriesP99 bool

	HitCount   int
	TotalMode  string
	TotalValue int
}

// Summarize 把一个组合的原始测量结果汇总为一条满足统计恒等式的 LatencySample。
func Summarize(m Measurement) LatencySample {
	ms := make([]float64, len(m.Durations))
	for i, d := range m.Durations {
		ms[i] = float64(d.Microseconds()) / 1000.0
	}
	sort.Float64s(ms)
	min, max := 0.0, 0.0
	if len(ms) > 0 {
		min, max = ms[0], ms[len(ms)-1]
	}
	cacheState := m.CacheState
	if cacheState == "" {
		// 空缓存状态是调用方的实现缺陷，绝不能默认成 warm——那正是本轮修掉的那个
		// 硬编码假设。如实记为 unknown。
		cacheState = CacheStateUnknown
	}
	return LatencySample{
		Category: m.Category, Limit: m.Limit, Concurrency: m.Concurrency,
		PlannedRuns: m.PlannedRuns, AttemptedRuns: m.AttemptedRuns, SuccessfulRuns: len(ms),
		FailedRuns: m.FailedRuns, TimedOutRuns: m.TimedOutRuns, NotAttemptedRuns: m.NotAttemptedRuns,
		WarmupRuns: m.WarmupRuns, WarmupFailedRuns: m.WarmupFailedRuns,
		CacheState: cacheState,
		P50Ms:      percentileNearestRank(ms, perMilleP50),
		P95Ms:      percentileNearestRank(ms, perMilleP95),
		P99Ms:      percentileNearestRank(ms, perMilleP99),
		MinMs:      min, MaxMs: max,
		PercentileMethod: PercentileMethodNearestRank,
		CarriesP99:       m.CarriesP99,
		P99Estimable:     len(ms) >= MinSamplesForP99,
		HitCount:         m.HitCount, TotalMode: m.TotalMode, TotalValue: m.TotalValue,
	}
}
