// Command testlabprobe 是正式压力测试的"被测查询路径"驱动器：只导入
// pkg/galleryapi 生成的公开契约客户端与标准库（以及 tools/testlab 的共享模块和
// stages/* 阶段包），从不导入 internal/* 包、不直接读写 SQLite 数据库。
// 它编译并启动真实的 cmd/galleryd 二进制，指向一个既有 AppDirs（通常由
// tools/testlab/cmd/seed 预先构建），通过一次性配对建立 Personal 管理 Session，再用
// 真实 HTTP 驱动结构化过滤、搜索、排序、游标、Overlay 依赖集、媒体与 DerivedAsset
// 场景，并把结果写成脱敏后的机器可读 JSON。
//
// `source-*` 系列场景针对真实只读 Source：它们消费 testlabrulesimport 由
// internal/rules/legacy.Convert 产出的**转换产物索引**（`-rules-index`），而不是手写
// 规则夹具——手写夹具与用户真实配置之间没有任何同步机制，用它验证只能证明夹具自洽。
// 转换本身在 testlabrulesimport 内完成，因此 probe 仍然只经公开契约驱动被测系统。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RecRivenVI/gallery/tools/testlab/internal/bounds"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/config"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/corpus"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/environment"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/hostfacts"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/process"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/report"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/ruleindex"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/sourceguard"
	"github.com/RecRivenVI/gallery/tools/testlab/stages/sourcelab"
	"github.com/RecRivenVI/gallery/tools/testlab/stages/stage4/media"
	"github.com/RecRivenVI/gallery/tools/testlab/stages/stage4/query"
)

const nonrecommendedScaleThreshold = 1_000_000

// main 只负责把 run() 的退出码传给 os.Exit：run() 内部用普通 return（不是
// log.Fatal/os.Exit）汇报错误，这样 defer 的 galleryd 停止/清理逻辑总能执行——
// 早期草稿在 log.Fatalf 或末尾 os.Exit(1) 处直接终止进程，跳过了停止 galleryd 子
// 进程的 defer，导致每次断言失败（即测试本身的正常结果）都会真实泄漏一个仍在监听
// 的 galleryd 进程，必须手动 taskkill 才能清理，且会一直占着 AppDirs 单写者锁。
func main() {
	os.Exit(run())
}

func run() int {
	goBin := flag.String("go", "", "固定 Go 工具链可执行文件路径")
	repoRoot := flag.String("repo", "", "仓库根目录（用于 go build ./cmd/galleryd）")
	appRoot := flag.String("approot", "", "既有 AppDirs 根（由 testlabseed 预先构建，或为空目录用于 media 场景）")
	logPath := flag.String("log", "", "galleryd 标准输出/错误日志的写入路径（必须位于授权测试根 logs/ 目录内）")
	scenario := flag.String("scenario", "correctness", "correctness | perf | media | cursor | all | source-bounded | source-index | source-incremental | source-verify")
	manifestPath := flag.String("manifest", "", "testlabseed 产出的 manifest JSON 路径（correctness/perf/cursor/all 场景必需）")
	resultsOut := flag.String("results-out", "", "脱敏结果 JSON 输出路径")
	runs := flag.Int("runs", 30, "每个延迟场景的重复次数")
	sourceRoot := flag.String("source-root", "", "media 场景（合成、非真实 Source）使用的可写临时根")
	sourceIDFlag := flag.String("source-id", "", "real-media 场景使用的本机配置逻辑来源 ID（如 pixiv/微博），从 -config 指向的 testlab.local.json 解析物理路径")
	configPath := flag.String("config", "", "本地测试配置路径（通常是 Documents/本地/testlab.local.json），real-media 场景必需")
	sourceAlias := flag.String("source-alias", "", "media 场景中真实 Source 的脱敏代号（写入结果，不写真实路径）")
	storageClass := flag.String("storage-class", "", "人工标注的存储介质分类（ssd/hdd）。**只作交叉核对**：介质由 hostfacts 走到物理盘实测，两者不一致时报告冲突并以实测为准")
	tier := flag.String("tier", "", "本次运行的规模等级标签（smoke/integration/preflight/reference/nonrecommended）")
	scenarioAlias := flag.String("scenario-alias", "", "本次运行的脱敏场景别名，写入结果代替具体路径信息")
	realMediaMode := flag.Bool("real-media", false, "media 场景是否针对已存在的真实只读 Source（跳过合成夹具写入）")
	maxMediaItems := flag.Int("max-media-items", 12, "media 场景处理的媒体数量上限")
	ruleFixture := flag.String("rule-fixture", "", "real-media 场景使用的规则夹具 JSON 路径（必须匹配该 Source 的真实目录结构），未显式指定且 -source-id 已知时按 fixtures/rules/<source-id>/ 约定查找")
	perfMatrixKind := flag.String("perf-matrix", "full", "perf 场景使用的组合矩阵：full（均匀笛卡尔积，适合已证明能在预算内完成的规模，例如 1k/10k/100k/500k）| directional（非推荐 >=1,000,000 规模等已知会命中已证实退化路径的规模使用的精简采样，仅方向性证据）")
	perfRequestTimeout := flag.Duration("perf-request-timeout", 30*time.Second, "perf 场景单个请求超时")
	perfCombinationTimeout := flag.Duration("perf-combination-timeout", 5*time.Minute, "perf 场景单个 (类别,limit,并发) 组合超时")
	perfScenarioTimeout := flag.Duration("perf-scenario-timeout", 30*time.Minute, "perf 场景整体超时；directional 矩阵建议显式传入更短的值（例如 20m）。承载 P99 的类别 runs 提高到 -perf-p99-runs 后请相应放宽本预算")
	perfP99Runs := flag.Int("perf-p99-runs", 100, "perf full 矩阵中承载 P99 的查询类别（browse/selective-cjk/filename-infix）的重复次数；低于 100 时 P99 无法估计，报告会以失败 finding 说明")
	perfWarmupRuns := flag.Int("perf-warmup-runs", 3, "perf 每个组合在测量前串行执行的预热请求次数；预热请求不进入分位数样本。0 表示不预热（此时缓存状态如实记为 warm-incidental 或 unknown）")
	perfCacheMode := flag.String("perf-cache", "warm", "perf 缓存条件：warm（每个组合先预热再测量）| cold-process（每个组合前重启 galleryd，使首次请求由从未服务过查询的新进程处理；不清空操作系统文件缓存，因此不代表冷存储读）")
	rulesIndex := flag.String("rules-index", "", "source-* 场景：testlabrulesimport 产出的转换产物索引路径（rule-index.json 或其所在目录）")
	platformCode := flag.String("platform-code", "", "source-* 场景：要验证的平台脱敏代号（testlabrulesimport 输出中的 p-xxxxxxxx）")
	maxDirs := flag.Int("max-dirs", 0, "source-* 场景的目录数上限；0 表示不限制")
	maxFiles := flag.Int("max-files", 0, "source-* 场景的文件数上限；0 表示不限制")
	maxWallClock := flag.Duration("max-wall-clock", 0, "source-* 场景的墙钟上限；超限主动取消扫描并如实报告「因边界停止」；0 表示不限制")
	boundedMediaItems := flag.Int("max-media-items-bounded", 12, "source-* 场景有界内容哈希（按需确认）的媒体数上限")
	hashScope := flag.String("hash-scope", "", "source-index 的内容哈希范围：full（全量，SSD）| bounded（有界子集，HDD）；留空时按 -storage-class 推导")
	guardHashContent := flag.Bool("guard-hash-content", false, "guard 清单是否补充完整内容 SHA-256（默认关闭；开启时务必设置 -guard-max-hash-files/-guard-max-hash-bytes）")
	guardMaxHashFiles := flag.Int("guard-max-hash-files", 0, "guard 内容哈希的文件数硬边界；0 表示不限制")
	guardMaxHashBytes := flag.Int64("guard-max-hash-bytes", 0, "guard 内容哈希的累计字节硬边界；0 表示不限制")
	statePath := flag.String("state", "", "source-* 场景的续跑状态文件路径（本地制品，只含 ID/计数/折叠哈希）")
	flag.Parse()

	if *goBin == "" || *repoRoot == "" || *appRoot == "" || *resultsOut == "" || *logPath == "" {
		fmt.Fprintln(os.Stderr, "必须指定 -go -repo -approot -log -results-out")
		return 2
	}

	rep := &report.Report{SchemaVersion: 2, Scenario: *scenario, ScenarioAlias: *scenarioAlias, SourceAlias: *sourceAlias, StorageClass: *storageClass, Tier: *tier}

	// 环境事实必须在任何测量之前采集：门禁要求每次结果同时记录 CPU、内存、存储型号/
	// 介质、OS 与 SQLite 版本。介质判定针对 AppDirs 的 data 目录（control.db/catalog.db
	// 真正所在的目录），而不是仓库所在盘或进程工作目录——这两者经常不是同一块物理盘。
	recordEnvironmentFacts(rep, filepath.Join(*appRoot, "data"), *storageClass)

	binPath := filepath.Join(filepath.Dir(*logPath), fmt.Sprintf("testlab-galleryd-%d.exe", time.Now().UnixNano()))
	if err := process.BuildGalleryd(*goBin, *repoRoot, binPath); err != nil {
		fmt.Fprintf(os.Stderr, "build galleryd: %v\n", err)
		return 1
	}
	defer os.Remove(binPath)

	runner := &gallerydRunner{binPath: binPath, appRoot: *appRoot, logPath: *logPath}
	if err := runner.start(); err != nil {
		fmt.Fprintf(os.Stderr, "start galleryd: %v\n", err)
		return 1
	}
	// 无论后续断言、场景执行或结果保存是否失败，都必须先请求正常停止再回退强杀，
	// 不得因为函数提前返回错误码就跳过这一步；这里不用 os.Exit，函数正常 return
	// 才能保证这个 defer 执行。冷缓存路径会在矩阵中途替换子进程，因此停止的是
	// runner 当前持有的那一个，不是启动时捕获的那个快照。
	defer runner.stop()

	sess, err := runner.pair()
	if err != nil {
		fmt.Fprintf(os.Stderr, "establish session: %v\n", err)
		return 1
	}

	var manifest corpus.Manifest
	if *scenario == "correctness" || *scenario == "perf" || *scenario == "cursor" || *scenario == "all" {
		if *manifestPath == "" {
			fmt.Fprintf(os.Stderr, "-scenario=%s 必须指定 -manifest（由 testlabseed -manifest-out 产出）\n", *scenario)
			return 2
		}
		manifest, err = corpus.LoadManifest(*manifestPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "load manifest: %v\n", err)
			return 1
		}
		rep.Scale = manifest.Scale
		if rep.Tier == "" {
			rep.Tier = manifest.Tier
		}
		rep.Nonrecommended = manifest.Nonrecommended
		recordCorpusFacts(rep, manifest)
	}

	switch *scenario {
	case "correctness":
		query.RunStructuredFilterCorrectness(rep, sess, manifest)
		query.RunSearchRecallCorrectness(rep, sess, manifest.Stats)
		query.RunRankingAndMatchesCorrectness(rep, sess)
		query.RunTotalTriStateCorrectness(rep, sess, manifest.Stats)
		query.RunSortCorrectness(rep, sess)
	case "cursor":
		query.RunCursorCorrectness(rep, sess)
	case "perf":
		if *perfMatrixKind == "directional" {
			rep.Nonrecommended = true
		}
		combos := query.PerfCombosFor(*perfMatrixKind, *runs, *perfP99Runs)
		timeouts := query.PerfTimeouts{PerRequest: *perfRequestTimeout, PerCombination: *perfCombinationTimeout, Scenario: *perfScenarioTimeout}
		// perf 场景在矩阵之前只建立过 Session（bootstrap/pairing 走 control.db，不触碰
		// Catalog 查询路径），因此可以如实断言进程尚未服务过任何查询请求。
		opts, optsErr := perfOptions(*perfCacheMode, *perfWarmupRuns, true, runner)
		if optsErr != nil {
			fmt.Fprintf(os.Stderr, "%v\n", optsErr)
			return 2
		}
		query.RunPerfMatrix(rep, sess, combos, timeouts, opts, func() { _ = rep.Save(*resultsOut) })
	case "media":
		var libraryID, sourceID string
		var workCount int
		if *realMediaMode {
			if *configPath == "" || *sourceIDFlag == "" {
				fmt.Fprintln(os.Stderr, "-real-media 必须指定 -config 与 -source-id")
				return 2
			}
			cfg, cfgErr := config.Load(*configPath)
			if cfgErr != nil {
				fmt.Fprintf(os.Stderr, "load config: %v\n", cfgErr)
				return 1
			}
			realRoot, rootErr := cfg.SourceRoot(*sourceIDFlag)
			if rootErr != nil {
				fmt.Fprintf(os.Stderr, "resolve source root: %v\n", rootErr)
				return 1
			}
			fixturePath := *ruleFixture
			if fixturePath == "" {
				fixturePath = filepath.Join(*repoRoot, "tools", "testlab", "fixtures", "rules", *sourceIDFlag, "bounded-subdir-v1.json")
			}
			libraryID, sourceID, workCount, err = media.SetupRealMediaSourceWithRule(rep, sess, realRoot, *maxMediaItems, fixturePath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "setup real media source: %v\n", err)
				return 1
			}
			media.RunMediaCorrectness(rep, sess, libraryID, sourceID, workCount)
		} else {
			if *sourceRoot == "" {
				fmt.Fprintln(os.Stderr, "-scenario=media 必须指定 -source-root")
				return 2
			}
			if mkdirErr := os.MkdirAll(*sourceRoot, 0o700); mkdirErr != nil {
				fmt.Fprintf(os.Stderr, "create source root: %v\n", mkdirErr)
				return 1
			}
			libraryID, sourceID, workCount, err = media.SetupMediaSource(rep, sess, *sourceRoot)
			if err != nil {
				fmt.Fprintf(os.Stderr, "setup media source: %v\n", err)
				return 1
			}
			media.RunMediaCorrectness(rep, sess, libraryID, sourceID, workCount)
		}
	case "source-bounded", "source-index", "source-incremental", "source-verify":
		cfg, cfgErr := buildSourcelabConfig(*scenario, *rulesIndex, *platformCode, *storageClass, *hashScope,
			bounds.Limits{MaxDirs: *maxDirs, MaxFiles: *maxFiles, MaxWallClock: *maxWallClock}, *boundedMediaItems,
			sourceguard.Options{HashContent: *guardHashContent, MaxHashFiles: *guardMaxHashFiles, MaxHashBytes: *guardMaxHashBytes})
		if cfgErr != nil {
			fmt.Fprintf(os.Stderr, "%v\n", cfgErr)
			return 2
		}
		previous, stateErr := sourcelab.LoadState(*statePath)
		if stateErr != nil {
			fmt.Fprintf(os.Stderr, "load state: %v\n", stateErr)
			return 1
		}
		state, runErr := sourcelab.Run(rep, sess, cfg, previous)
		if state != nil {
			if err := sourcelab.SaveState(state, *statePath); err != nil {
				fmt.Fprintf(os.Stderr, "save state: %v\n", err)
				return 1
			}
		}
		if runErr != nil {
			// 结论不成立必须以非零退出码结束，且报告仍然落盘供事后核对。
			rep.Add("sourcelab/run", false, "本次运行因上述失败终止")
			if err := rep.Save(*resultsOut); err != nil {
				fmt.Fprintf(os.Stderr, "save report: %v\n", err)
			}
			fmt.Fprintf(os.Stderr, "sourcelab: %v\n", runErr)
			return 1
		}
	case "all":
		query.RunStructuredFilterCorrectness(rep, sess, manifest)
		query.RunSearchRecallCorrectness(rep, sess, manifest.Stats)
		query.RunRankingAndMatchesCorrectness(rep, sess)
		query.RunTotalTriStateCorrectness(rep, sess, manifest.Stats)
		query.RunSortCorrectness(rep, sess)
		query.RunCursorCorrectness(rep, sess)
		combos := query.PerfCombosFor(*perfMatrixKind, *runs, *perfP99Runs)
		timeouts := query.PerfTimeouts{PerRequest: *perfRequestTimeout, PerCombination: *perfCombinationTimeout, Scenario: *perfScenarioTimeout}
		// all 场景在矩阵之前已经跑完全部 Correctness/Cursor 断言，进程早已服务过大量
		// 查询，因此绝不能断言进程是冷的——那正是"看起来合理其实是假的"标注。
		opts, optsErr := perfOptions(*perfCacheMode, *perfWarmupRuns, false, runner)
		if optsErr != nil {
			fmt.Fprintf(os.Stderr, "%v\n", optsErr)
			return 2
		}
		query.RunPerfMatrix(rep, sess, combos, timeouts, opts, func() { _ = rep.Save(*resultsOut) })
	default:
		fmt.Fprintf(os.Stderr, "未知 scenario: %s\n", *scenario)
		return 2
	}

	finalizeEnvironmentGate(rep)
	if err := rep.Save(*resultsOut); err != nil {
		fmt.Fprintf(os.Stderr, "save report: %v\n", err)
		return 1
	}
	fmt.Printf("testlabprobe: scenario=%s findings=%d failures=%d\n", *scenario, len(rep.Findings), rep.FailureCount)
	if rep.FailureCount > 0 {
		return 1
	}
	return 0
}

// recordEnvironmentFacts 采集并登记本次运行的环境事实，同时用人工 `-storage-class`
// 标注做交叉核对。
//
// 三条规则在这里成文，因为它们决定整份报告能不能作为门禁证据：
//
//  1. 环境事实一律实测，人工标注**不得覆盖**实测结论。人工 flag 没有任何校验，写错、
//     写旧、忘写都不会被发现；整份报告的可信度正建立在"环境是测出来的"这一点上。
//  2. 实测与人工标注不一致时产生一条失败 finding，而不是二选一后继续。
//  3. 缺任一门禁必需项时同样以失败 finding 暴露，但只在本次运行确实产出了性能测量
//     （latencies 非空）时才判失败——门禁那条要求约束的是性能结论，把它套到
//     correctness/media/source-* 场景上会制造与结论无关的噪声失败。
func recordEnvironmentFacts(rep *report.Report, dbDir, manualStorageClass string) {
	facts := hostfacts.Collect(dbDir)
	rep.Environment = &facts

	if conflict, detail := hostfacts.CrossCheckStorageClass(manualStorageClass, facts.Storage); conflict {
		rep.Add("environment/storage-class-cross-check", false, detail)
	} else if detail != "" {
		// 实测为 unknown 时无法核对：不是冲突，但必须留痕，否则人工标注会被默认当成
		// 已被证实。
		rep.Limitations = append(rep.Limitations, detail)
	}

	if facts.Storage.ResolvedThroughLink {
		rep.Limitations = append(rep.Limitations,
			fmt.Sprintf("数据库目录经目录联接/符号链接落在另一个盘符（逻辑 %s → 实际 %s）；介质与型号取自实际物理盘，按盘符做的任何存储假设都不成立。",
				facts.Storage.LogicalDrive, facts.Storage.PhysicalDrive))
	}
}

// recordCorpusFacts 把语料的结构性事实写进报告，并在单 Source 语料上登记一条限制。
//
// 这条限制不是形式主义：catalog.cloneUnchangedSources 的 12 条全量搬运语句
// （WHERE source_id<>?）在单 Source 语料下一条都不会执行，而它正是"重扫其中一个
// Source 时按比例复制其余全部 Source 的投影与 FTS5 索引"这条路径，是发布代价的大头
// 之一。用单 Source 语料测出来的发布/查询数字不覆盖它，读者必须知道。
func recordCorpusFacts(rep *report.Report, manifest corpus.Manifest) {
	sources := manifest.SourceCount()
	rep.Corpus = &report.CorpusFacts{
		Scale: manifest.Scale, SourceCount: sources,
		ClonedSourceCountOnLastPublish: sources - 1,
		SourceBeginDurationsMs:         manifest.SourceBeginDurationsMs,
		SourceValidationDurationsMs:    manifest.SourceValidationDurationsMs,
		SourcePublishDurationsMs:       manifest.SourcePublishDurationsMs,
	}
	if sources <= 1 {
		rep.Limitations = append(rep.Limitations,
			"本次语料只有 1 个 Source，因此 catalog.cloneUnchangedSources 的 12 条全量搬运语句一条都没有执行过；"+
				"本结果不覆盖「重扫单个 Source 时按比例复制其余全部 Source 投影与 FTS5 索引」这条发布路径。")
	}
}

// finalizeEnvironmentGate 在结果落盘之前判定环境完整性。放在最后是因为它要看本次运行
// 是否真的产出了性能样本。
func finalizeEnvironmentGate(rep *report.Report) {
	if rep.Environment == nil || len(rep.Latencies) == 0 {
		return
	}
	missing := rep.Environment.MissingFields()
	if len(missing) == 0 {
		rep.Add("environment/gate-required-facts-complete", true, "")
		return
	}
	rep.Add("environment/gate-required-facts-complete", false,
		fmt.Sprintf("缺少门禁要求的环境事实 %v；按 Reference Performance Gate 自身规则，本次性能数字只能作为方向性证据，不能宣称通过", missing))
}

// perfOptions 把命令行的缓存条件翻译成 query.PerfOptions。
func perfOptions(cacheMode string, warmupRuns int, processColdAtStart bool, runner *gallerydRunner) (query.PerfOptions, error) {
	switch cacheMode {
	case "warm":
		return query.PerfOptions{WarmupRuns: warmupRuns, ProcessColdAtStart: processColdAtStart}, nil
	case "cold-process":
		// 冷路径与预热互斥：预热的作用就是消除冷启动效应。这里直接不传预热次数，
		// 而不是让 RunPerfMatrix 去处理一个自相矛盾的配置。
		return query.PerfOptions{ProcessColdAtStart: processColdAtStart, ColdRestart: runner.restartAndPair}, nil
	default:
		return query.PerfOptions{}, fmt.Errorf("未知 -perf-cache: %s（可选 warm | cold-process）", cacheMode)
	}
}

// gallerydRunner 持有当前 galleryd 子进程，并支持"停止后指向同一 AppDirs 重新启动"。
// 这是冷缓存路径的实现基础：重启后的进程其 SQLite 连接池、页缓存、prepared statement
// 缓存与查询服务内部缓存全部为空，因此下一次查询确实由一个从未服务过查询的进程处理。
//
// 能力边界必须说清楚：这只清空**进程内**状态。操作系统文件系统缓存不受影响——在
// Windows 上清空它需要管理员权限与平台专有接口，本工具不做，因此 cold-process 不等于
// 冷存储读，报告里据此登记为限制。
type gallerydRunner struct {
	binPath  string
	appRoot  string
	logPath  string
	restarts int
	current  *process.Process
}

func (r *gallerydRunner) start() error {
	logPath := r.logPath
	if r.restarts > 0 {
		// 每次重启写独立日志文件：复用同一路径会被 os.Create 截断，把上一轮 galleryd
		// 的启动与错误日志抹掉，而那正是诊断冷路径失败最需要的材料。
		extension := filepath.Ext(r.logPath)
		logPath = strings.TrimSuffix(r.logPath, extension) + fmt.Sprintf("-restart%02d", r.restarts) + extension
	}
	proc, err := process.StartGalleryd(r.binPath, r.appRoot, logPath, 60*time.Second)
	if err != nil {
		return err
	}
	r.current = proc
	return nil
}

func (r *gallerydRunner) pair() (*environment.Session, error) {
	if r.current == nil {
		return nil, fmt.Errorf("galleryd 尚未启动")
	}
	return environment.NewSession(r.current.BaseURL)
}

// restartAndPair 正常停止当前进程、重新启动并重新完成一次性配对，返回可用于下一个
// 组合的 Session。停止必须先于启动完成：同一 AppDirs 有进程独占写锁，两个实例不能
// 并存。
func (r *gallerydRunner) restartAndPair() (*environment.Session, error) {
	r.stop()
	r.restarts++
	if err := r.start(); err != nil {
		return nil, fmt.Errorf("冷缓存路径重启 galleryd 失败: %w", err)
	}
	sess, err := r.pair()
	if err != nil {
		return nil, fmt.Errorf("冷缓存路径重新配对失败: %w", err)
	}
	return sess, nil
}

func (r *gallerydRunner) stop() {
	if r.current == nil {
		return
	}
	outcome := r.current.Stop()
	if outcome.ForcedKill {
		fmt.Fprintf(os.Stderr, "警告：galleryd 未能在 %s 内正常停止，已回退为强制终止（requestedGraceful=%v err=%v）\n",
			process.GracefulStopTimeout, outcome.RequestedGraceful, outcome.Err)
	}
	r.current = nil
}

// buildSourcelabConfig 把命令行参数解析成一次真实 Source 验证的配置。
//
// 内容哈希范围默认由存储介质推导：SSD 全量、HDD 有界。这不是性能偏好而是可完成性——
// 对一块机械盘上的完整来源做全量 SHA-256 会把一次验证拖成不可预期的长任务。推导结果
// 可以被 -hash-scope 显式覆盖。
func buildSourcelabConfig(scenario, indexPath, platformCode, storageClass, hashScope string,
	limits bounds.Limits, boundedMediaItems int, guardOptions sourceguard.Options) (sourcelab.Config, error) {
	if indexPath == "" || platformCode == "" {
		return sourcelab.Config{}, fmt.Errorf("-scenario=%s 必须指定 -rules-index 与 -platform-code（由 testlabrulesimport 产出）", scenario)
	}
	index, dir, err := ruleindex.Load(indexPath)
	if err != nil {
		return sourcelab.Config{}, err
	}
	entry, ok := index.Find(platformCode)
	if !ok {
		return sourcelab.Config{}, fmt.Errorf("转换产物索引中没有平台代号 %q；可用代号: %v", platformCode, index.Codes())
	}
	rulePackage, err := entry.LoadPackage(dir)
	if err != nil {
		return sourcelab.Config{}, fmt.Errorf("读取平台 %s 的规则包失败: %w", platformCode, err)
	}
	if hashScope == "" {
		switch storageClass {
		case "ssd":
			hashScope = sourcelab.HashFull
		default:
			hashScope = sourcelab.HashBounded
		}
	}
	if guardOptions.HashContent && guardOptions.MaxHashFiles <= 0 && guardOptions.MaxHashBytes <= 0 {
		return sourcelab.Config{}, fmt.Errorf("-guard-hash-content 必须同时给出 -guard-max-hash-files 或 -guard-max-hash-bytes 硬边界")
	}
	return sourcelab.Config{
		Entry: entry, Package: rulePackage,
		Mode:   strings.TrimPrefix(scenario, "source-"),
		Limits: limits, HashScope: hashScope, MaxMediaItems: boundedMediaItems,
		GuardOptions: guardOptions, StorageClass: storageClass,
	}, nil
}
