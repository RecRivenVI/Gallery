package query

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	api "github.com/RecRivenVI/gallery/pkg/galleryapi"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/corpus"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/environment"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/report"
)

// ValidatePerfCorpusBinding 在任何计时请求之前，证明当前 AppRoot 的 active publication
// 与 manifest 是同一个快照。否则 manifest 自报的 500k/十来源/双关系形状可能被贴到
// 另一套数据库的性能结果上。该请求不计入分位数，并会预热当前进程；cold-process
// 模式随后仍会在每个组合前重启，warm 模式则本来就会显式 warmup。
func ValidatePerfCorpusBinding(rep *report.Report, sess *environment.Session, manifest corpus.Manifest) bool {
	limit, omitTotal := 1, true
	resp, err := listWorks(sess, api.ListWorksParams{Limit: &limit, OmitTotal: &omitTotal})
	if err != nil || resp == nil || resp.JSON200 == nil {
		rep.Add("perf/corpus-publication-matches-manifest", false,
			fmt.Sprintf("预检请求失败: err=%v status=%d", err, environment.StatusOf(resp)))
		return false
	}
	actualPublication := string(resp.JSON200.QueryPublicationId)
	ok := actualPublication == manifest.QueryPublicationID && resp.JSON200.CatalogRevision == manifest.CatalogRevisionID
	detail := ""
	if !ok {
		// 只记录是否匹配，不把任何可能被手工替换的 ID 回显进报告。
		detail = "当前 publication 或 Catalog revision 与 manifest 不一致"
	}
	rep.Add("perf/corpus-publication-matches-manifest", ok, detail)
	return ok
}

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
	// carriesP99 表示本组合被指定为承载 P99 的组合，因此它的成功样本数必须达到
	// report.MinSamplesForP99，否则报告里会出现一条明确的失败 finding。
	carriesP99 bool
}

// p99CarryingShapes 是被指定承载 P99 的查询类别。只选已经被 EV-35/EV-36 证明在参考
// 规模下是毫秒级的类别：P99 需要 ≥100 个成功样本，把它加到 wide-cjk/structured-*/
// overlay-favorite 这些已知秒级退化的类别上，只会用几十分钟重复证明同一个已知问题，
// 并把整个矩阵挤出时间预算。这些慢类别继续按常规 runs 采样，其样本的 p99Estimable
// 会如实为 false。
var p99CarryingShapes = map[string]bool{
	"browse":         true,
	"selective-cjk":  true,
	"filename-infix": true,
}

// buildFullMatrix 是均匀笛卡尔积矩阵：每个 shape × 每个 limit × 每个并发档位，各
// 跑 runs 次。用于数据规模较小、已经证明可以在预算内完成的场景（例如 1k/10k/100k/500k）。
//
// p99Runs 是承载 P99 的类别使用的重复次数；小于 report.MinSamplesForP99 时该类别仍会
// 被标记为 carriesP99，从而在报告里以失败 finding 暴露"要求 P99 却没给够样本"，而不是
// 悄悄产出一个其实等于最大值的数字。
func buildFullMatrix(limits, concurrencies []int, runs, p99Runs int) []combination {
	shapes := perfShapes()
	order := []string{"browse", "selective-cjk", "wide-cjk", "filename-infix", "structured-and", "structured-or", "overlay-favorite"}
	var combos []combination
	for _, name := range order {
		shape := shapes[name]
		carriesP99 := p99CarryingShapes[name]
		comboRuns := runs
		if carriesP99 && p99Runs > comboRuns {
			comboRuns = p99Runs
		}
		for _, limit := range limits {
			for _, concurrency := range concurrencies {
				combos = append(combos, combination{shape: shape, limit: limit, concurrency: concurrency, runs: comboRuns, carriesP99: carriesP99})
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
// 这里全部使用具名字段：carriesP99 在本矩阵中恒为 false（精简采样本来就只提供方向性
// 证据，不承载任何分位数门禁），位置初始化会让未来新增字段悄悄错位。
func buildDirectionalMatrix() []combination {
	shapes := perfShapes()
	return []combination{
		{shape: shapes["browse"], limit: 20, concurrency: 1, runs: 5},
		{shape: shapes["browse"], limit: 100, concurrency: 1, runs: 5},
		{shape: shapes["selective-cjk"], limit: 20, concurrency: 1, runs: 5},
		{shape: shapes["selective-cjk"], limit: 20, concurrency: 4, runs: 5},
		{shape: shapes["selective-cjk"], limit: 100, concurrency: 1, runs: 5},
		{shape: shapes["selective-cjk"], limit: 100, concurrency: 4, runs: 5},
		{shape: shapes["filename-infix"], limit: 20, concurrency: 1, runs: 3},
		{shape: shapes["wide-cjk"], limit: 20, concurrency: 1, runs: 1},
		{shape: shapes["structured-and"], limit: 20, concurrency: 1, runs: 1},
		{shape: shapes["structured-or"], limit: 20, concurrency: 1, runs: 1},
		{shape: shapes["overlay-favorite"], limit: 20, concurrency: 1, runs: 1},
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
// limit×concurrency 笛卡尔积（runs 由调用方指定，承载 P99 的类别改用 p99Runs）；
// directional 是非推荐（≥1,000,000）等规模使用的精简采样矩阵，runs/p99Runs 参数被忽略
// （每个组合的重复次数已经按查询类别单独固定在 buildDirectionalMatrix 里），且没有任何
// 组合承载 P99——那本来就只是方向性证据。
func PerfCombosFor(kind string, runs, p99Runs int) []combination {
	if kind == "directional" {
		return buildDirectionalMatrix()
	}
	return buildFullMatrix([]int{20, 100, 200}, []int{1, 4, 16}, runs, p99Runs)
}

// PerfOptions 描述本次矩阵的缓存条件。它取代了此前直接写进 report.Summarize 的
// `"warm"` 字面量——那个字面量既没有 warmup 支撑，也没有任何冷路径能力，实际含义是
// "不知道，先当成热的"。
type PerfOptions struct {
	// WarmupRuns 是每个组合在测量前执行的预热请求次数（串行执行，不进入 durations）。
	// 大于 0 且至少一次成功时，该组合的缓存状态才是实测的 warm。
	WarmupRuns int

	// ColdRestart 非 nil 时，RunPerfMatrix 在**每个组合之前**调用它重启 galleryd 并
	// 返回新建立的 Session，使该组合的第一次测量请求确实由一个从未服务过任何查询的
	// 新进程处理，缓存状态记为 cold-process。
	//
	// 边界必须说清楚：这条路径只清空**进程内**状态（SQLite 连接池与页缓存、prepared
	// statement 缓存、查询服务缓存）。它清不掉操作系统文件系统缓存——在 Windows 上那
	// 需要管理员权限与平台专有接口，本工具不做。因此 cold-process 不等于"冷存储读"，
	// 报告里也据此登记为限制。
	//
	// 另一种曾被考虑的做法（把 AppDirs 整体复制一份再测）不成立且没有实现：复制动作
	// 本身会把目标文件读进操作系统文件缓存，结果只会更热，不会更冷。
	ColdRestart func() (*environment.Session, error)

	// ProcessColdAtStart 由调用方断言：矩阵开始时持有的那个 galleryd 进程自启动以来
	// 没有服务过任何查询请求。probe 的常规流程满足这个条件（建立 Session 只走
	// bootstrap/pairing，不触碰 Catalog 查询路径），因此第一个组合可以如实标为
	// cold-process；调用方无法保证时应留 false，那时首个组合记为 unknown。
	ProcessColdAtStart bool
}

// baseCacheState 是**没有任何预热时**本组合的进程侧缓存状态：
//
//   - processNeverServed 为 true（刚重启，或调用方断言矩阵开始时进程未服务过查询）→
//     cold-process；
//   - 同一进程已经服务过前序组合 → warm-incidental；
//   - 两者都不成立（调用方无法断言进程初始状态，且还没有前序组合）→ unknown。
//     这一支很重要：无法断言就是不知道，不能顺手当成热的。
func baseCacheState(processNeverServed, servedEarlier bool) string {
	switch {
	case processNeverServed && !servedEarlier:
		return report.CacheStateColdProcess
	case servedEarlier:
		return report.CacheStateWarmIncidental
	default:
		return report.CacheStateUnknown
	}
}

// resolveCacheState 由**已经发生的事实**推导本组合最终记录的缓存状态，而不是照抄配置
// 意图：预热确实跑过且至少成功一次才算 warm；要求预热却一次都没成功时既不能说热、也
// 不能说这次测的是冷路径，只能是 unknown。
func resolveCacheState(base string, warmupExecuted, warmupSucceeded int) string {
	switch {
	case warmupExecuted > 0 && warmupSucceeded > 0:
		return report.CacheStateWarm
	case warmupExecuted > 0:
		return report.CacheStateUnknown
	default:
		return base
	}
}

// perfLimitations 是每次性能矩阵都必须随结果一起发布的口径与覆盖限制。它们不是可选
// 说明：门禁要求结果自带足以判断其效力的上下文，缺了这些上下文的数字会被误读为可与
// 历史结果直接比较的通过证据。
func perfLimitations(opts PerfOptions) []string {
	limitations := []string{
		"分位数口径自 2026-07-27 起改为标准最近秩 ceil(p·n)；此前实现是 floor(p·n)−1，整整低一名。" +
			"因此本文件的 P95/P99 与更早的结果文件不可直接比较（同一批原始样本下新口径只会等于或高于旧口径）。",
		"cacheState 只描述 galleryd 进程内缓存：cold-process 表示该组合的首次请求由从未服务过查询的新进程处理，" +
			"warm 表示该组合测量前完成过成功的显式预热，warm-incidental 表示只被前序组合偶然预热。" +
			"本工具不清空操作系统文件系统缓存（需要管理员权限与平台专有接口），因此任何样本都不代表冷存储读。",
		"p99Estimable=false 的样本其 p99Ms 只是最近秩落在最大值上的结果，不是 P99 估计；" +
			"P99 至少需要 " + fmt.Sprintf("%d", report.MinSamplesForP99) + " 个成功样本。",
		"结构化筛选形态（structured-and/structured-or/overlay-favorite）在 internal/query 侧没有 EXPLAIN QUERY PLAN 断言，" +
			"因此这些类别的耗时没有任何结构性保证支撑，只能作为观测值，不能作为「执行计划未退化」的证据。",
	}
	if opts.ColdRestart == nil && opts.WarmupRuns <= 0 {
		limitations = append(limitations,
			"本次运行既未预热也未使用冷路径：除第一个组合外，全部样本的缓存状态是 warm-incidental（被前序组合偶然预热），不是受控的热态。")
	}
	return limitations
}

// RunPerfMatrix 依次执行 combos 中的每个组合，每完成一个立即输出进度并调用
// savePartial（若非 nil）把当前已完成的部分原子保存为 partial report。任何请求
// 超时、取消或 HTTP 失败都计入 FailedRuns，绝不只用成功样本静默计算分位数；组合
// 截止时间耗尽而从未派发的请求计入 NotAttemptedRuns，二者结构上独立，保证
// report.LatencySample 的统计恒等式成立。一旦整个场景的时间预算耗尽，停止派发新
// 组合并将 report.AbortedByTimeLimit 置为 true——调用方在报告和最终判定里必须把
// 这种情况视为"未完整执行"，不得因为已完成部分的请求全部成功就宣称整体矩阵通过。
func RunPerfMatrix(rep *report.Report, sess *environment.Session, combos []combination, timeouts PerfTimeouts, opts PerfOptions, savePartial func()) {
	rep.StartedAt = time.Now().UTC().Format(time.RFC3339)
	rep.PlannedCombinations = len(combos)
	rep.Limitations = append(rep.Limitations, perfLimitations(opts)...)
	scenarioDeadline := time.Now().Add(timeouts.Scenario)

	// 冷路径与预热在语义上互斥：预热的目的正是消除冷启动效应。同时要求两者说明调用
	// 方的配置本身是错的，必须以失败 finding 暴露，而不是任选一个悄悄执行。
	if opts.ColdRestart != nil && opts.WarmupRuns > 0 {
		rep.Add("perf/cache-state-configuration-consistent", false,
			fmt.Sprintf("同时要求冷路径与 %d 次预热；预热会抵消冷路径，本次已强制关闭预热", opts.WarmupRuns))
		opts.WarmupRuns = 0
	}

	// processNeverServed 记录"当前这个 galleryd 进程自启动以来没有服务过任何查询请求"
	// 这一**可断言的事实**；servedEarlier 记录本矩阵是否已经在该进程上派发过测量请求。
	// 两者共同决定 cold-process / warm-incidental / unknown，都必须随每次重启复位。
	processNeverServed := opts.ProcessColdAtStart
	servedEarlier := false
	aborted := false
	abortReason := ""
	for _, combo := range combos {
		if time.Now().After(scenarioDeadline) {
			aborted = true
			abortReason = fmt.Sprintf("整体场景超时预算 %s 耗尽，已完成 %d/%d 个组合后停止派发新组合", timeouts.Scenario, rep.CompletedCombinations, len(combos))
			break
		}
		if opts.ColdRestart != nil {
			restarted, err := opts.ColdRestart()
			if err != nil || restarted == nil {
				// 冷路径失败后继续测量只会产出标注为冷、实际是热的样本，比没有结果更
				// 有害。停止整个矩阵并如实说明。
				aborted = true
				abortReason = fmt.Sprintf("冷缓存路径要求在每个组合前重启 galleryd，但第 %d 个组合前重启失败: %v", rep.CompletedCombinations+1, err)
				rep.Add("perf/cold-restart-succeeded", false, abortReason)
				break
			}
			sess = restarted
			processNeverServed = true
			servedEarlier = false
		}
		comboDeadline := time.Now().Add(timeouts.PerCombination)
		if scenarioDeadline.Before(comboDeadline) {
			comboDeadline = scenarioDeadline
		}
		dispatched := runOneCombination(rep, sess, combo, timeouts.PerRequest, comboDeadline,
			opts.WarmupRuns, baseCacheState(processNeverServed, servedEarlier))
		if dispatched {
			servedEarlier = true
		}
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

// runWarmup 在测量之前串行发出 warmupRuns 次同形请求，返回成功次数。预热请求不进入
// durations，也不进入任何一类运行计数：它们的作用恰恰是把"第一次请求要付的一次性代价"
// （连接建立、prepared statement 编译、SQLite 页缓存填充、查询服务内部缓存）挪出被测
// 样本。此前没有这个阶段，因此每个组合最前面的几个样本其实是冷的，却被整批标成 warm。
//
// 预热串行执行而不是按 combo.concurrency 并发：这里要的是"缓存已填充"这个状态，不是
// 再测一遍并发行为，串行能用最少的请求达成同样的状态。预热同样受组合截止时间约束，
// 不会把整个时间预算耗在预热上。
func runWarmup(sess *environment.Session, combo combination, warmupRuns int, perRequestTimeout time.Duration, comboDeadline time.Time) (executed, succeeded int) {
	params := combo.shape.params(combo.limit)
	for i := 0; i < warmupRuns; i++ {
		if time.Now().After(comboDeadline) {
			break
		}
		executed++
		ctx, cancel := context.WithTimeout(context.Background(), perRequestTimeout)
		r, callErr := sess.Client.ListWorksWithResponse(ctx, copyParams(params), sess.SameOrigin)
		cancel()
		if callErr == nil && r != nil && r.JSON200 != nil {
			succeeded++
		}
	}
	return executed, succeeded
}

// runOneCombination 先执行 warmupRuns 次预热（见 runWarmup），再在 comboDeadline 之前
// 最多尝试 combo.runs 次测量请求（并发度为 combo.concurrency），每个请求各自受
// perRequestTimeout 约束。到达 comboDeadline 后不再派发新请求，但已经派发的请求仍会在
// 各自的请求超时内正常结束或被取消——不需要额外的宽限期计时器，因为每个请求本身已经
// 有界。failed/timedOut/notAttempted 分别独立计数（timedOut 是 failed 的子集），交给
// report.Summarize 产出满足统计恒等式的 LatencySample，不在这里手工合并
// failed+notAttempted。
//
// baseState 由调用方按 baseCacheState 给出（进入本组合时的进程侧缓存状态）；最终记录的
// 缓存状态由它与预热的实际结果共同推导（resolveCacheState），不是配置意图的复述。
// 返回值报告本组合是否真的派发过请求，供调用方维护"进程是否已被预热"的状态。
func runOneCombination(rep *report.Report, sess *environment.Session, combo combination, perRequestTimeout time.Duration, comboDeadline time.Time, warmupRuns int, baseState string) bool {
	warmupExecuted, warmupSucceeded := runWarmup(sess, combo, warmupRuns, perRequestTimeout, comboDeadline)
	cacheState := resolveCacheState(baseState, warmupExecuted, warmupSucceeded)

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
	sample := report.Summarize(report.Measurement{
		Category: combo.shape.name, Limit: combo.limit, Concurrency: combo.concurrency,
		PlannedRuns: combo.runs, AttemptedRuns: attempted, Durations: durations,
		FailedRuns: failed, TimedOutRuns: timedOut, NotAttemptedRuns: notAttempted,
		WarmupRuns: warmupExecuted, WarmupFailedRuns: warmupExecuted - warmupSucceeded,
		CacheState: cacheState,
		CarriesP99: combo.carriesP99,
		HitCount:   hitCount, TotalMode: totalMode, TotalValue: totalValue,
	})
	rep.Latencies = append(rep.Latencies, sample)

	prefix := fmt.Sprintf("perf/%s-limit%d-concurrency%d", combo.shape.name, combo.limit, combo.concurrency)
	if failed > 0 || notAttempted > 0 {
		rep.Add(prefix+"-no-failed-runs", false, fmt.Sprintf("planned=%d attempted=%d failed=%d timedOut=%d notAttempted=%d(combination-deadline)", combo.runs, attempted, failed, timedOut, notAttempted))
	} else {
		rep.Add(prefix+"-no-failed-runs", true, "")
	}
	if !sample.IdentityOK() {
		rep.Add(prefix+"-run-count-identity", false, fmt.Sprintf("planned=%d attempted=%d successful=%d failed=%d notAttempted=%d", sample.PlannedRuns, sample.AttemptedRuns, sample.SuccessfulRuns, sample.FailedRuns, sample.NotAttemptedRuns))
	}
	// 缓存状态未知意味着这条样本不满足门禁「必须记录冷/热缓存」的要求，必须显式失败，
	// 不能因为请求本身都成功了就当作可用证据。
	if sample.CacheState == report.CacheStateUnknown {
		rep.Add(prefix+"-cache-state-measured", false,
			fmt.Sprintf("缓存状态无法判定: warmupExecuted=%d warmupFailed=%d baseState=%s", sample.WarmupRuns, sample.WarmupFailedRuns, baseState))
	}
	// 承载 P99 的组合必须有足够样本，否则报出来的 p99 只是最大值。
	if combo.carriesP99 && !sample.P99Estimable {
		rep.Add(prefix+"-p99-sample-size", false,
			fmt.Sprintf("successfulRuns=%d 少于 P99 所需的 %d，p99Ms 只是最近秩落在最大值上的结果", sample.SuccessfulRuns, report.MinSamplesForP99))
	}
	return dispatched > 0
}

func copyParams(p api.ListWorksParams) *api.ListWorksParams {
	copyOf := p
	return &copyOf
}
