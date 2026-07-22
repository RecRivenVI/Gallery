package query

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	api "github.com/RecRivenVI/gallery/pkg/galleryapi"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/environment"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/report"
)

// perfShape 描述第九节「查询类别」中的一个可重复查询类别。
type perfShape struct {
	name   string
	params func(limit int) api.ListWorksParams
}

func perfShapes() map[string]perfShape {
	shapes := []perfShape{
		{"browse", func(limit int) api.ListWorksParams { return api.ListWorksParams{Limit: ptr(limit)} }},
		{"selective-cjk", func(limit int) api.ListWorksParams {
			return api.ListWorksParams{Q: ptr("关键词命中"), Limit: ptr(limit)}
		}},
		{"wide-cjk", func(limit int) api.ListWorksParams {
			return api.ListWorksParams{Q: ptr("普通作品"), Limit: ptr(limit)}
		}},
		{"filename-infix", func(limit int) api.ListWorksParams {
			return api.ListWorksParams{Q: ptr("middle-000"), Limit: ptr(limit)}
		}},
		{"structured-and", func(limit int) api.ListWorksParams {
			return api.ListWorksParams{Filter: ptr(filterJSON(all(leaf("provider.id", "eq", "provider-0"), leaf("media.kind", "eq", "image")))), Limit: ptr(limit)}
		}},
		{"structured-or", func(limit int) api.ListWorksParams {
			return api.ListWorksParams{Filter: ptr(filterJSON(any_(leaf("provider.id", "eq", "provider-0"), leaf("provider.id", "eq", "provider-1")))), Limit: ptr(limit)}
		}},
		{"overlay-favorite", func(limit int) api.ListWorksParams {
			return api.ListWorksParams{Filter: ptr(filterJSON(leaf("overlay.favorite", "eq", true))), Limit: ptr(limit)}
		}},
	}
	byName := make(map[string]perfShape, len(shapes))
	for _, s := range shapes {
		byName[s.name] = s
	}
	return byName
}

// combination 是性能矩阵里的一个可独立执行、可独立超时、可独立记录进度的单元。
type combination struct {
	shape       perfShape
	limit       int
	concurrency int
	runs        int
}

// buildFullMatrix 是均匀笛卡尔积矩阵：每个 shape × 每个 limit × 每个并发档位，各
// 跑 runs 次。用于数据规模较小、已经证明可以在预算内完成的场景（例如 1k/10k/100k/500k）。
func buildFullMatrix(limits, concurrencies []int, runs int) []combination {
	shapes := perfShapes()
	order := []string{"browse", "selective-cjk", "wide-cjk", "filename-infix", "structured-and", "structured-or", "overlay-favorite"}
	var combos []combination
	for _, name := range order {
		shape := shapes[name]
		for _, limit := range limits {
			for _, concurrency := range concurrencies {
				combos = append(combos, combination{shape: shape, limit: limit, concurrency: concurrency, runs: runs})
			}
		}
	}
	return combos
}

// buildDirectionalMatrix 是非推荐（≥1,000,000）规模下使用的精简采样矩阵：已经由
// 500k/1M 完整矩阵证明 wide-cjk/structured-and/structured-or/overlay.favorite 在
// 单并发下就可能已经是秒级到分钟级的严重退化，非推荐规模下继续测试并发 16 只会
// 重复证明同一个已知问题、并消耗远超预算的时间，因此这些较慢的类别在这里最多只
// 跑 1 次、只用并发 1，仅用于确认"退化仍然存在、量级大致符合预期"这一方向性事实，
// 不用于任何正式性能门禁判定。
func buildDirectionalMatrix() []combination {
	shapes := perfShapes()
	return []combination{
		{shapes["browse"], 20, 1, 5},
		{shapes["browse"], 100, 1, 5},
		{shapes["selective-cjk"], 20, 1, 5},
		{shapes["selective-cjk"], 20, 4, 5},
		{shapes["selective-cjk"], 100, 1, 5},
		{shapes["selective-cjk"], 100, 4, 5},
		{shapes["filename-infix"], 20, 1, 3},
		{shapes["wide-cjk"], 20, 1, 1},
		{shapes["structured-and"], 20, 1, 1},
		{shapes["structured-or"], 20, 1, 1},
		{shapes["overlay-favorite"], 20, 1, 1},
	}
}

// PerfTimeouts 控制性能矩阵的三层超时：单请求、单组合、整个场景。默认值见
// DefaultPerfTimeouts；非推荐（≥1,000,000）等已知会命中已证实退化路径的规模应
// 使用更紧的场景预算（例如 20 分钟的精简矩阵），不得为了跑完整矩阵而不断放宽
// 这些数值。
type PerfTimeouts struct {
	PerRequest     time.Duration
	PerCombination time.Duration
	Scenario       time.Duration
}

func DefaultPerfTimeouts() PerfTimeouts {
	return PerfTimeouts{PerRequest: 30 * time.Second, PerCombination: 5 * time.Minute, Scenario: 30 * time.Minute}
}

// PerfCombosFor 按 -perf-matrix 选择组合矩阵。full 对小规模/推荐规模数据集使用统一
// limit×concurrency 笛卡尔积（runs 由调用方指定）；directional 是非推荐（≥1,000,000）
// 等规模使用的精简采样矩阵，runs 参数被忽略（每个组合的重复次数已经按查询类别单独
// 固定在 buildDirectionalMatrix 里）。
func PerfCombosFor(kind string, runs int) []combination {
	if kind == "directional" {
		return buildDirectionalMatrix()
	}
	return buildFullMatrix([]int{20, 100, 200}, []int{1, 4, 16}, runs)
}

// RunPerfMatrix 依次执行 combos 中的每个组合，每完成一个立即输出进度并调用
// savePartial（若非 nil）把当前已完成的部分原子保存为 partial report。任何请求
// 超时、取消或 HTTP 失败都计入 FailedRuns，绝不只用成功样本静默计算分位数；组合
// 截止时间耗尽而从未派发的请求计入 NotAttemptedRuns，二者结构上独立，保证
// report.LatencySample 的统计恒等式成立。一旦整个场景的时间预算耗尽，停止派发新
// 组合并将 report.AbortedByTimeLimit 置为 true——调用方在报告和最终判定里必须把
// 这种情况视为"未完整执行"，不得因为已完成部分的请求全部成功就宣称整体矩阵通过。
func RunPerfMatrix(rep *report.Report, sess *environment.Session, combos []combination, timeouts PerfTimeouts, savePartial func()) {
	rep.StartedAt = time.Now().UTC().Format(time.RFC3339)
	rep.PlannedCombinations = len(combos)
	scenarioDeadline := time.Now().Add(timeouts.Scenario)

	aborted := false
	abortReason := ""
	for _, combo := range combos {
		if time.Now().After(scenarioDeadline) {
			aborted = true
			abortReason = fmt.Sprintf("整体场景超时预算 %s 耗尽，已完成 %d/%d 个组合后停止派发新组合", timeouts.Scenario, rep.CompletedCombinations, len(combos))
			break
		}
		comboDeadline := time.Now().Add(timeouts.PerCombination)
		if scenarioDeadline.Before(comboDeadline) {
			comboDeadline = scenarioDeadline
		}
		runOneCombination(rep, sess, combo, timeouts.PerRequest, comboDeadline)
		rep.CompletedCombinations++
		fmt.Fprintf(os.Stderr, "perf progress: %d/%d combinations done (%s limit=%d concurrency=%d)\n",
			rep.CompletedCombinations, len(combos), combo.shape.name, combo.limit, combo.concurrency)
		if savePartial != nil {
			savePartial()
		}
	}
	rep.AbortedByTimeLimit = aborted
	rep.AbortReason = abortReason
	rep.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	// 部分矩阵不得被下游读者误当作"全部通过"：即使已完成的组合全部成功，只要被
	// 时间预算中止就必须有一条失败 finding 明确反映这一点。
	rep.Add("perf/matrix-completed-without-time-abort", !aborted, abortReason)
}

// runOneCombination 在 comboDeadline 之前最多尝试 combo.runs 次请求（并发度为
// combo.concurrency），每个请求各自受 perRequestTimeout 约束。到达 comboDeadline
// 后不再派发新请求，但已经派发的请求仍会在各自的请求超时内正常结束或被取消——
// 不需要额外的宽限期计时器，因为每个请求本身已经有界。failed/timedOut/
// notAttempted 分别独立计数（timedOut 是 failed 的子集），交给 report.Summarize
// 产出满足统计恒等式的 LatencySample，不在这里手工合并 failed+notAttempted。
func runOneCombination(rep *report.Report, sess *environment.Session, combo combination, perRequestTimeout time.Duration, comboDeadline time.Time) {
	params := combo.shape.params(combo.limit)
	durations := make([]time.Duration, 0, combo.runs)
	failed, timedOut, notAttempted := 0, 0, 0
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, combo.concurrency)
	var hitCount int
	var totalMode string
	var totalValue int
	sampled := false

	dispatched := 0
	for run := 0; run < combo.runs; run++ {
		if time.Now().After(comboDeadline) {
			notAttempted = combo.runs - run
			break
		}
		dispatched++
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), perRequestTimeout)
			defer cancel()
			started := time.Now()
			r, callErr := sess.Client.ListWorksWithResponse(ctx, copyParams(params), sess.SameOrigin)
			elapsed := time.Since(started)
			mu.Lock()
			defer mu.Unlock()
			if callErr != nil || r == nil || r.JSON200 == nil {
				failed++
				if ctx.Err() == context.DeadlineExceeded {
					timedOut++
				}
				return
			}
			durations = append(durations, elapsed)
			if !sampled {
				hitCount = len(r.JSON200.Works)
				totalMode = string(r.JSON200.Total.Mode)
				if r.JSON200.Total.Value != nil {
					totalValue = int(*r.JSON200.Total.Value)
				}
				sampled = true
			}
		}()
	}
	wg.Wait()

	attempted := dispatched
	sample := report.Summarize(combo.shape.name, combo.limit, combo.concurrency, combo.runs, attempted, durations, failed, timedOut, notAttempted, "warm", hitCount, totalMode, totalValue)
	rep.Latencies = append(rep.Latencies, sample)

	findingName := fmt.Sprintf("perf/%s-limit%d-concurrency%d-no-failed-runs", combo.shape.name, combo.limit, combo.concurrency)
	if failed > 0 || notAttempted > 0 {
		rep.Add(findingName, false, fmt.Sprintf("planned=%d attempted=%d failed=%d timedOut=%d notAttempted=%d(combination-deadline)", combo.runs, attempted, failed, timedOut, notAttempted))
	} else {
		rep.Add(findingName, true, "")
	}
	identityName := fmt.Sprintf("perf/%s-limit%d-concurrency%d-run-count-identity", combo.shape.name, combo.limit, combo.concurrency)
	if !sample.IdentityOK() {
		rep.Add(identityName, false, fmt.Sprintf("planned=%d attempted=%d successful=%d failed=%d notAttempted=%d", sample.PlannedRuns, sample.AttemptedRuns, sample.SuccessfulRuns, sample.FailedRuns, sample.NotAttemptedRuns))
	}
}

func copyParams(p api.ListWorksParams) *api.ListWorksParams {
	copyOf := p
	return &copyOf
}
