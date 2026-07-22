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
)

// Finding 是单条断言结果：name 描述被验证的具体行为，pass 是断言结论，detail 是
// 失败时的诊断信息。detail 必须经过 sanitizeDetail 脱敏，不得包含真实媒体路径、
// 监听地址或 secret；调用方全程只操作合成数据或授权的真实 Source 有界子集。
type Finding struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

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

	CacheState string  `json:"cacheState"`
	P50Ms      float64 `json:"p50Ms"`
	P95Ms      float64 `json:"p95Ms"`
	P99Ms      float64 `json:"p99Ms"`
	MinMs      float64 `json:"minMs"`
	MaxMs      float64 `json:"maxMs"`
	HitCount   int     `json:"hitCount"`
	TotalMode  string  `json:"totalMode"`
	TotalValue int     `json:"totalValue"`
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
	SchemaVersion  int             `json:"schemaVersion"`
	GeneratedAt    string          `json:"generatedAt"`
	Scenario       string          `json:"scenario"`
	ScenarioAlias  string          `json:"scenarioAlias,omitempty"`
	SourceAlias    string          `json:"sourceAlias,omitempty"`
	StorageClass   string          `json:"storageClass,omitempty"`
	Tier           string          `json:"tier,omitempty"`
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

func percentile(sortedMs []float64, p float64) float64 {
	if len(sortedMs) == 0 {
		return 0
	}
	index := int(p*float64(len(sortedMs))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sortedMs) {
		index = len(sortedMs) - 1
	}
	return sortedMs[index]
}

// Summarize 把一个 (类别,limit,并发) 组合的原始测量结果汇总为一条满足统计恒等式
// 的 LatencySample。durations 只包含成功请求的耗时；failed/timedOut/notAttempted
// 由调用方按上文定义分别统计，timedOut 是 failed 的子集，不额外计入总数。
func Summarize(category string, limit, concurrency, planned, attempted int, durations []time.Duration,
	failed, timedOut, notAttempted int, cacheState string, hitCount int, totalMode string, totalValue int) LatencySample {
	ms := make([]float64, len(durations))
	for i, d := range durations {
		ms[i] = float64(d.Microseconds()) / 1000.0
	}
	sort.Float64s(ms)
	min, max := 0.0, 0.0
	if len(ms) > 0 {
		min, max = ms[0], ms[len(ms)-1]
	}
	return LatencySample{
		Category: category, Limit: limit, Concurrency: concurrency,
		PlannedRuns: planned, AttemptedRuns: attempted, SuccessfulRuns: len(ms),
		FailedRuns: failed, TimedOutRuns: timedOut, NotAttemptedRuns: notAttempted,
		CacheState: cacheState,
		P50Ms:      percentile(ms, 0.50), P95Ms: percentile(ms, 0.95), P99Ms: percentile(ms, 0.99),
		MinMs: min, MaxMs: max, HitCount: hitCount, TotalMode: totalMode, TotalValue: totalValue,
	}
}
